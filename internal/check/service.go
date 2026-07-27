package check

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// Service is one address something in this repo is expected to be listening on.
//
// Inferred entries come from the PORT keys derive.go already finds; declared
// ones come from treehouse.toml [[service]] and merge over them by name, which
// is what Declared separates. There is deliberately no compose-file parsing
// behind this: a second discovery path would be a second source of truth,
// disagreeing with the one hydrate actually acts on.
type Service struct {
	Name string `toml:"name"` // the row's identity, and the merge key
	Addr string `toml:"addr"` // host:port to dial
	Fix  string `toml:"fix"`  // the command that would start it

	// Declared is true for a [[service]] entry a human wrote. It is the whole
	// difference between the WARN and FAIL tiers, and it is set by cmd after
	// parsing rather than read from the file — see CheckServices.
	Declared bool `toml:"-"`
}

// ServiceName is the merge key. Exported because the merge happens in cmd:
// config imports check, so check cannot import config's generic Merge.
func ServiceName(s Service) string { return s.Name }

// InferServices reads the service list out of the ports this worktree already
// declares. `PORT` in svc_a/.env IS the statement that svc_a should have a
// listener — the same keys E3 shifts, read for a second purpose rather than
// discovered a second way.
func InferServices(w Worktree) []Service {
	var out []Service
	for rel, ports := range portsByDir(w) {
		for key, port := range ports {
			out = append(out, Service{
				Name: serviceName(rel, key),
				// 127.0.0.1, not localhost: a loopback dial should not depend on
				// what the resolver thinks, or on whether the listener bound v6.
				Addr: fmt.Sprintf("127.0.0.1:%d", port),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// serviceName names an inferred service: the directory holding the .env, which
// is what the service is called in every layout Discover walks, plus the key,
// because one directory can declare two listeners.
//
// The key stays even when it is the only one. A name that shortened to `api`
// until somebody added ADMIN_PORT would silently rename the row a
// treehouse.toml [[service]] overrides, and an override that stops applying is
// worse than one that never did.
func serviceName(rel, key string) string {
	if rel == "." {
		return key
	}
	return rel + "/" + key
}

// CheckServices reports whether each expected listener answers. `up` is what a
// dialler found, handed in rather than gathered — the same bargain
// DBInput.Existing makes, so the judgment stays pure and testable from struct
// literals.
//
// No services means NO ROWS, not a green one. A repo that declares no port has
// nothing to check, which is a different sentence from "we checked and found
// nothing wrong" — the same rule that gives a repo with no database no db row.
//
// Inferred requirements warn; declared ones fail. That is the progressive
// configuration principle the env checks already follow: a PORT key is a guess
// about intent, a [[service]] entry is a human saying this must be up.
func (d Doctor) CheckServices(services []Service, up map[string]bool) []Check {
	var checks []Check
	for _, s := range services {
		c := Check{Name: "service"}
		switch {
		case s.Addr == "":
			// A [[service]] with no addr: nothing to dial, so nothing is known.
			// Reporting it green would be the exact lie skip exists to prevent,
			// and reporting it down would blame a typo on the service.
			c.Status = skip
			c.Detail = s.Name + " declares no addr — nothing to dial"
			c.Fix = "give " + s.Name + " an addr = \"host:port\" in treehouse.toml"
		case up[s.Addr]:
			c.Status, c.Detail = "ok", s.Name+" is listening on "+s.Addr
		default:
			c.Status = "warn"
			if s.Declared {
				c.Status = "fail"
			}
			c.Detail = s.Name + " — nothing is listening on " + s.Addr
			c.Fix = s.Fix
			if c.Fix == "" {
				c.Fix = "docker compose up -d"
			}
		}
		checks = append(checks, c)
	}
	return checks
}

// dialTimeout is short on purpose. A doctor that takes five seconds because
// three services are down is a doctor nobody runs — and a loopback connect
// either completes in microseconds or is refused instantly, so the only case
// that ever waits the full quarter second is a host that black-holes SYNs.
const dialTimeout = 250 * time.Millisecond

// DialServices is the impure half: one TCP connect per distinct address,
// reporting which answered. It lives beside the judgment for the same reason
// Row lives beside Status — a gatherer inlined in a print loop has no second
// caller, and cmd stays an adapter.
//
// Concurrent, because the timeout is per address and they share nothing.
func DialServices(services []Service) map[string]bool {
	addrs := map[string]bool{}
	for _, s := range services {
		if s.Addr != "" {
			addrs[s.Addr] = false
		}
	}

	up := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for addr := range addrs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", addr, dialTimeout)
			if err != nil {
				return // refused, unreachable, or an addr that will not parse
			}
			conn.Close()
			mu.Lock()
			defer mu.Unlock()
			up[addr] = true
		}()
	}
	wg.Wait()
	return up
}
