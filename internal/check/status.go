package check

import (
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Status is one worktree's row in the fleet view — pure data, no presentation.
// Deliberately per-worktree rather than a batch API: the TUI streams cells in
// concurrently, and a batch call would make it wait for the slowest worktree.
type Status struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Env    string `json:"env"` // ok | warn | fail

	// DB is clone-exists or clone-missing and NOTHING else:
	// ok | missing | shared | "". Empty means the question wasn't asked — no
	// database in this repo, or Postgres wasn't reachable — and "shared" is the
	// main checkout, which is the template rather than a worktree with a clone.
	// Whether .env is actually pointed at the clone is CheckDB's job, because
	// answering it needs this worktree's env read, and the fleet view must stay
	// one git round trip per row.
	DB string `json:"db,omitempty"`

	// Migrations is filled only by `th doctor --db`. Running a project's
	// migration-status command is seconds of somebody else's tooling per
	// worktree, and `th ls` is a glance — it must never pay that.
	Migrations string `json:"migrations,omitempty"` // pending | applied | unknown | ""

	Behind  int  `json:"behind"` // commits in main this branch doesn't have
	Dirty   bool `json:"dirty"`
	Current bool `json:"current"` // the worktree the human is standing in
}

// Status folds one worktree into one row. It asks git nothing and touches no
// files — ref carries what git already told cmd. Living here rather than in
// cmd/ls.go is the point: the TUI is a renderer over these, not a second
// implementation of them.
func (d Doctor) Status(w Worktree, ref Ref, source Worktree) Status {
	s := Status{
		Path:   ref.Path,
		Branch: ref.Branch,
		Env:    EnvStatus(d.CheckEnv(w, source)),
		Behind: ref.Behind,
		Dirty:  ref.Dirty,
	}
	// One pg.Databases call serves the whole fleet — cmd makes it once and hands
	// the names down. A per-worktree subprocess here would put a psql round trip
	// behind every row of a table that exists to be glanced at.
	if template := EnvDB(source); d.Databases != nil && template != "" && ref.Branch != "" {
		switch {
		case ref.Branch == d.MainBranch:
			// Main is the template, not a worktree missing a clone. A branch can
			// only be checked out in one worktree, so this identifies it exactly.
			// Reporting it "missing" would train people to ignore the column.
			s.DB = "shared"
		case slices.Contains(d.Databases, DBName(template, Slug(ref.Branch))):
			s.DB = "ok"
		default:
			s.DB = "missing"
		}
	}

	if s.Branch == "" {
		s.Branch = "(detached)"
		if ref.Bare {
			s.Branch = "(bare)"
		}
	}
	return s
}

// Row gathers one worktree's live state and folds it into its row: the two
// questions Status refuses to ask (dirty, behind) plus the env walk behind it.
//
// It shells git and touches the filesystem where Status does neither — that is
// the split, not an accident: Status stays a pure fold so it can be tested from
// struct literals, and Row is the one place that fills a Ref in. It lives here
// rather than in cmd/ls.go because a gatherer inlined in a print loop has no
// callers: the fleet view is meant to be a renderer over []Status, and the
// second renderer would otherwise have to reimplement this.
func (d Doctor) Row(ref Ref, source Worktree) Status {
	ref.Dirty = gitDirty(ref.Path)
	ref.Behind = gitBehind(ref.Path, d.MainBranch)
	wt, _ := Discover(ref.Path) // unreadable worktree: still list it
	return d.Status(wt, ref, source)
}

// gitDirty reports uncommitted changes. A bare or unreadable worktree answers
// "clean" — the fleet view lists what it can and never fails over one bad row.
func gitDirty(path string) bool {
	out, err := gitOut(path, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

// gitBehind counts commits the main branch has that this worktree doesn't.
func gitBehind(path, mainBranch string) int {
	if mainBranch == "" {
		return 0 // detached or bare main: nothing to be behind
	}
	out, err := gitOut(path, "rev-list", "--count", "HEAD.."+mainBranch)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

func gitOut(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// EnvStatus folds findings into the one word the exit code, the --json envelope
// and the ls column all agree on. Inferred drift warns; only a curated required
// key fails.
func EnvStatus(findings []Finding) string {
	s := "ok"
	for _, f := range findings {
		if f.Fails() {
			return "fail"
		}
		if f.Drifted() {
			s = "warn"
		}
	}
	return s
}
