package check

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Slug turns a branch name into one identifier usable everywhere a worktree
// needs a name: COMPOSE_PROJECT_NAME ([a-z0-9][a-z0-9_-]*), a Postgres database
// name, a directory. The shared alphabet is [a-z0-9_].
//
// A hash of the original is appended whenever the mapping lost information —
// any character rewritten, or the name truncated. That suffix is load-bearing,
// not decoration: without it feat/a-b and feat-a-b slug identically, and two
// worktrees end up sharing one database. Budget: "app_wt_" + 40 + 7 = 54 bytes,
// inside Postgres's 63-byte identifier cap.
func Slug(branch string) string {
	var b strings.Builder
	lossy := false
	for _, r := range branch {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			// Case-folding is a collision too: git lets Feat and feat coexist.
			lossy = true
			b.WriteRune(r + ('a' - 'A'))
		default:
			lossy = true
			if !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_') // runs of junk collapse to a single _
			}
		}
	}

	s := strings.Trim(b.String(), "_")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "_")
		lossy = true
	}
	if s == "" {
		// An empty branch maps to an empty slug, which is not an identifier at
		// all: COMPOSE_PROJECT_NAME=app_ and a worktree dir named "app-".
		lossy = true
	}
	if !lossy {
		return s
	}

	sum := sha256.Sum256([]byte(branch))
	suffix := hex.EncodeToString(sum[:])[:6]
	if s == "" {
		return suffix // a name of pure punctuation: the hash IS the name
	}
	return s + "_" + suffix
}

// DeriveInput is everything PlanDerive can't work out for itself. cmd gathers
// it — the planner stays pure, so it stays testable from struct literals.
type DeriveInput struct {
	App   string     // compose project prefix, resolved by cmd
	Slug  string     // Slug(branch) for this worktree
	Fleet []Worktree // the OTHER worktrees: the port registry
}

const (
	minPort    = 1024
	maxPort    = 65535
	portSpread = 200 // how many offsets exist, and so how many we may try
)

// PlanDerive plans the per-worktree derived values: a private compose project
// (E2) and a private set of ports (E3). Both are pure functions of the slug and
// what the neighbours already declare, so the same branch always derives the
// same values — the registry IS the sibling .env files, there is no state file
// to keep in sync or garbage-collect.
//
// Repairs come back Overwrite: derived values replace whatever is there, unlike
// PlanHydrate's append-only fills.
func (d Doctor) PlanDerive(w, source Worktree, in DeriveInput) []Repair {
	byPath := map[string]*Repair{}
	at := func(dir string) *Repair {
		path := filepath.Join(dir, ".env")
		if byPath[path] == nil {
			byPath[path] = &Repair{EnvPath: path, Overwrite: true, Add: map[string]string{}}
		}
		return byPath[path]
	}

	// E2: one compose project per worktree, so containers, networks and volumes
	// can never clobber another checkout's.
	for _, dir := range w.ComposeDirs {
		at(dir).Add["COMPOSE_PROJECT_NAME"] = in.App + "_" + in.Slug
	}

	// E3: main's ports are the base values; every service shifts by one shared
	// offset, which preserves the spacing services assume between each other.
	base := portsByDir(source)
	if len(base) > 0 {
		registry := make([]Worktree, 0, len(in.Fleet)+1)
		registry = append(registry, source)
		registry = append(registry, in.Fleet...)

		if offset, ok := freeOffset(in.Slug, base, registry); ok {
			for rel, ports := range base {
				r := at(filepath.Join(w.Root, rel))
				for key, port := range ports {
					r.Add[key] = strconv.Itoa(port + offset)
				}
			}
		} else {
			// Never fail hydrate over a port: report and move on, same as DepPlan.
			// Annotate the root repair rather than replacing it — ports and the
			// compose project are separate concerns, and a port skip must not
			// take E2's namespacing down with it.
			at(w.Root).Skip = "no free port offset — every one of the 200 collides with a sibling worktree"
		}
	}

	repairs := make([]Repair, 0, len(byPath))
	for _, r := range byPath {
		repairs = append(repairs, *r)
	}
	sort.Slice(repairs, func(i, j int) bool { return repairs[i].EnvPath < repairs[j].EnvPath })
	return repairs
}

// portValue reads a port declaration, or reports that the key isn't one.
//
// ponytail: detection is by key name only — PORT and *_PORT. SERVER_ADDR=:3000
// and friends are invisible; add a config list of port keys when someone hits it.
func portValue(key, val string) (int, bool) {
	if key != "PORT" && !strings.HasSuffix(key, "_PORT") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || n < minPort || n > maxPort {
		return 0, false
	}
	return n, true
}

// portsByDir extracts every port declaration in a worktree, keyed by its dir
// relative to that worktree's root — so main's dirs map onto ours.
func portsByDir(w Worktree) map[string]map[string]int {
	byDir := map[string]map[string]int{}
	for rel, vars := range w.EnvVarsByDir() {
		for key, val := range vars {
			if port, ok := portValue(key, val); ok {
				if byDir[rel] == nil {
					byDir[rel] = map[string]int{}
				}
				byDir[rel][key] = port
			}
		}
	}
	return byDir
}

// freeOffset picks the shift that makes this worktree's WHOLE port set free.
// Starting point is derived from the slug (stable), then we walk forward until
// nothing in the set collides — one predicate covers both cross-worktree
// collisions and main's own packing (PORT=3000 next to ADMIN_PORT=3001 makes
// offset 1 collide with itself).
//
// ponytail: "free" means not declared in a sibling .env, not unbound on the
// host. A live net.Listen probe would be racy, and determinism is the one
// property E3 cannot trade away.
func freeOffset(slug string, base map[string]map[string]int, registry []Worktree) (int, bool) {
	used := map[int]bool{}
	for _, o := range registry {
		for _, ports := range portsByDir(o) {
			for _, port := range ports {
				used[port] = true
			}
		}
	}

	h := fnv.New32a()
	h.Write([]byte(slug))
	first := int(h.Sum32() % portSpread)

	for i := 0; i < portSpread; i++ {
		offset := 1 + (first+i)%portSpread
		if fits(base, offset, used) {
			return offset, true
		}
	}
	return 0, false
}

func fits(base map[string]map[string]int, offset int, used map[int]bool) bool {
	for _, ports := range base {
		for _, port := range ports {
			if port+offset > maxPort || used[port+offset] {
				return false
			}
		}
	}
	return true
}
