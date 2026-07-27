package check

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// JournalSchema is the version cmd refuses to read anything else of. It bumps
// when Entry changes shape, and a bump means "no baseline yet" for one run —
// which is exactly what the journal is allowed to cost.
const JournalSchema = 1

// Journal is this worktree's record of when each row of the doctor report was
// last green. It is the one piece of persistence treehouse keeps, and it only
// earns that because of WHERE cmd puts it: inside the worktree's own git
// directory, which `git worktree remove` deletes with everything else. Not
// committed, invisible to `git status`, nothing for `th gc` to chase — the same
// bargain the port registry (the sibling .env files) and the seed marker (a
// table inside the database) already make.
//
// It is an optimization, never a dependency. Missing, unreadable, corrupt or
// from an older schema all mean the same thing — no baseline yet — and no
// command may fail because of it.
type Journal struct {
	Schema  int              `json:"schema"`
	Entries map[string]Entry `json:"entries"`
}

// Entry is one row's history, and it is deliberately three small facts: what
// the row said at the last run, when it was last OK, and the branch it was OK
// on. Everything about what is wrong NOW comes from the live report, so the
// journal never has to store a detail line that can go stale or lie after a
// manual fix.
type Entry struct {
	Status string    `json:"status"`           // what this row said at the last run
	Green  time.Time `json:"green,omitempty"`  // last run it was ok; zero = never seen ok
	Branch string    `json:"branch,omitempty"` // the branch it was last ok on
}

// Record folds one report into the journal. Rows this run did not produce keep
// their history rather than being dropped: a check that stopped appearing is
// itself a thing `why` should be able to say, and forgetting it the moment it
// vanishes is how a state file quietly agrees with whatever ran last.
//
// ponytail: nothing is ever pruned. The key set is bounded by the check names
// plus one per service directory, and the whole file dies with the worktree.
func (j Journal) Record(current []Check, branch string, now time.Time) Journal {
	out := Journal{Schema: JournalSchema, Entries: map[string]Entry{}}
	for name, e := range j.Entries {
		out.Entries[name] = e
	}
	for _, c := range current {
		e := out.Entries[c.Name]
		e.Status = c.Status
		if c.Status == "ok" {
			e.Green, e.Branch = now, branch
		}
		out.Entries[c.Name] = e
	}
	return out
}

// Why is `th why`'s whole answer: one line where one line will do, and the full
// list behind it when several things moved at once.
type Why struct {
	Answer   string   `json:"answer"`
	Changes  []string `json:"changes,omitempty"`
	Baseline bool     `json:"baseline"` // false = no journal yet, so nothing to diff against
}

// Explain diffs the live report against the journal and says what changed. Pure
// — the clock and the branch are handed in, so every sentence below is testable
// from struct literals.
//
// A row that is ok now is never mentioned: `why` answers what changed, and
// doctor already answers what is true. skip gets its own sentence because it is
// not a shade of ok — a check that stopped being ASKED is usually the actual
// story (Postgres went down, so the db check stopped running), and "went from
// ok to skip" would bury that in the same phrasing as a failure.
func Explain(j Journal, current []Check, branch string, now time.Time) Why {
	if len(j.Entries) == 0 {
		return Why{Answer: "no baseline yet — run `th doctor` first"}
	}

	seen := map[string]bool{}
	var lines []string
	for _, c := range current {
		seen[c.Name] = true
		e, known := j.Entries[c.Name]
		switch {
		case c.Status == "ok":
		case !known:
			lines = append(lines, fmt.Sprintf("%s is new since the last run: %s", c.Name, c.Detail))
		case e.Green.IsZero():
			lines = append(lines, fmt.Sprintf("%s has never been green: %s", c.Name, c.Detail))
		case c.Status == skip:
			lines = append(lines, fmt.Sprintf("%s stopped being checked %s: %s", c.Name, since(e, branch, now), c.Detail))
		case c.Status == e.Status:
			lines = append(lines, fmt.Sprintf("%s has been %s %s: %s", c.Name, c.Status, since(e, branch, now), c.Detail))
		default:
			lines = append(lines, fmt.Sprintf("%s went from %s to %s %s: %s", c.Name, e.Status, c.Status, since(e, branch, now), c.Detail))
		}
	}

	// A row the journal knows and this run never produced. Reported for the same
	// reason skip is: it was being checked, and now nobody is asking.
	names := make([]string, 0, len(j.Entries))
	for name := range j.Entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if e := j.Entries[name]; !seen[name] && !e.Green.IsZero() {
			lines = append(lines, fmt.Sprintf("%s is not being reported at all any more (last ok %s)", name, clock(e.Green, now)))
		}
	}

	w := Why{Changes: lines, Baseline: true}
	switch len(lines) {
	case 0:
		w.Answer = "nothing to explain — every check is green"
	case 1:
		w.Answer = lines[0]
	default:
		w.Answer = fmt.Sprintf("%d things changed since everything was green:", len(lines))
	}
	return w
}

// since is the "when" half of a one-liner. The branch wins when it moved,
// because "after you switched to feat/login" is the answer people are actually
// looking for; the clock is the fallback for a row that broke where it stood.
func since(e Entry, branch string, now time.Time) string {
	if e.Branch != "" && branch != "" && e.Branch != branch {
		return "after you switched to " + branch
	}
	return "since " + clock(e.Green, now)
}

// clock reads as a wall clock while it happened today and grows a date once it
// did not — "since 14:02" is only useful for as long as it is unambiguous.
func clock(t, now time.Time) string {
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// Snapshot flattens a doctor report into the one flat vocabulary the journal
// records and `why` diffs: the sibling checks under their own names, plus one
// row per service for the env half.
//
// Findings become Checks rather than getting their own journal shape, because
// Check is already {name, status, detail} and a second near-identical struct
// would mean two diffing paths that agree until one of them changes.
func Snapshot(root string, findings []Finding, checks []Check) []Check {
	out := make([]Check, 0, len(findings)+len(checks))
	for _, f := range findings {
		out = append(out, envCheck(root, f))
	}
	return append(out, checks...)
}

// envCheck is one service's env drift as a Check. The tiers are EnvStatus's,
// per service: only a curated required key fails, everything inferred warns.
func envCheck(root string, f Finding) Check {
	c := Check{Name: envName(root, f.Dir), Status: "ok"}
	switch {
	case f.NoEnv:
		c.Status, c.Detail = "warn", fmt.Sprintf(".env missing entirely (%d keys expected)", f.Keys)
	case f.Drifted():
		var parts []string
		if len(f.Missing) > 0 {
			parts = append(parts, few(f.Missing)+" missing")
		}
		if len(f.Empty) > 0 {
			parts = append(parts, few(f.Empty)+" empty")
		}
		c.Status, c.Detail = "warn", strings.Join(parts, ", ")
	default:
		c.Detail = fmt.Sprintf("all %d expected keys present", f.Keys)
		return c
	}
	if f.Fails() {
		c.Status = "fail"
		c.Detail += " (required by treehouse.toml)"
	}
	c.Fix = "th hydrate"
	return c
}

// envName keys the env rows. A single-service repo just says "env"; a monorepo
// says which service, because "env went from ok to warn" is useless in a repo
// with six of them.
func envName(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return "env"
	}
	return "env (" + rel + ")"
}

// few names a drifted key set in one line. Three at most: `why`'s whole promise
// is one line, and a service can drift twenty keys at once.
func few(keys []string) string {
	if len(keys) <= 3 {
		return strings.Join(keys, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(keys[:3], ", "), len(keys)-3)
}
