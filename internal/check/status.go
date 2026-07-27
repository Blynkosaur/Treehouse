package check

import (
	"os/exec"
	"strconv"
	"strings"
)

// Status is one worktree's row in the fleet view — pure data, no presentation.
// Deliberately per-worktree rather than a batch API: the TUI streams cells in
// concurrently, and a batch call would make it wait for the slowest worktree.
type Status struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`

	// Status is this row's whole verdict — Verdict over the same findings and
	// checks `th doctor` folds, so `th ls --json` and `th doctor --json` can
	// never call one worktree ok and fail. Env is the env half on its own,
	// because the table has a column for it.
	Status string `json:"status"` // ok | warn | fail | skip
	Env    string `json:"env"`    // ok | warn | fail

	// DB is DBWord: main | ok | missing | shared | adrift | unusable | skip, or
	// "" when the question was never asked because this repo declares no
	// database at all. It is NOT clone-exists-only any more — `shared` is a
	// worktree whose clone exists while its .env still names the template, and
	// it is a failure here exactly as it is in doctor's report.
	DB string `json:"db,omitempty"`

	Behind  int  `json:"behind"` // commits in main this branch doesn't have
	Dirty   bool `json:"dirty"`
	Current bool `json:"current"` // the worktree the human is standing in
}

// Fleet folds every row, plus the repo-wide checks that belong to no single
// worktree (a treehouse.toml nobody could parse), into the one word `th ls`
// prints and exits on. Same tiers as Verdict, which is where each row's own
// word came from: a known problem outranks an unknown one, so warn beats skip,
// and skip still beats ok because "nobody asked" is not "verified fine".
func Fleet(rows []Status, repo []Check) string {
	s := "ok"
	for _, c := range repo {
		s = worse(s, c.Status)
	}
	for _, r := range rows {
		s = worse(s, r.Status)
	}
	return s
}

// Status folds one worktree into one row. It asks git nothing and touches no
// files — ref carries what git already told cmd. Living here rather than in
// cmd/ls.go is the point: the TUI is a renderer over these, not a second
// implementation of them.
func (d Doctor) Status(w Worktree, ref Ref, source Worktree) Status {
	findings := d.CheckEnv(w, source)
	s := Status{
		Path:   ref.Path,
		Branch: ref.Branch,
		Env:    EnvStatus(findings),
		Behind: ref.Behind,
		Dirty:  ref.Dirty,
	}

	// One pg.Databases call serves the whole fleet — cmd makes it once and hands
	// the names down. A per-worktree subprocess here would put a psql round trip
	// behind every row of a table that exists to be glanced at.
	//
	// The env read this row already paid for is what makes the FULL db question
	// affordable here: Row walked the worktree to compare its .env keys, so
	// asking that same map which database it names costs one lookup, not a round
	// trip. That is why the column can be CheckDB's answer rather than
	// clone-exists.
	var checks []Check
	if template := EnvDB(source); template != "" && ref.Branch != "" && d.Databases != nil {
		state := DBState{
			Plan:  d.PlanDB(DBInput{Template: template, Existing: d.Databases, Slug: Slug(ref.Branch)}),
			EnvDB: EnvDB(w),
			// A branch can only be checked out in one worktree, so this identifies
			// the main checkout exactly — and main is the template rather than a
			// worktree missing a clone.
			Main: ref.Branch == d.MainBranch,
		}
		s.DB = DBWord(state)
		checks = append(checks, d.CheckDB(state))
	}
	s.Status = Verdict(findings, checks)

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
	ref.Behind = Behind(ref.Path, d.MainBranch)
	wt, _ := Discover(ref.Path) // unreadable worktree: still list it
	return d.Status(wt, ref, source)
}

// gitDirty reports uncommitted changes. A bare or unreadable worktree answers
// "clean" — the fleet view lists what it can and never fails over one bad row.
func gitDirty(path string) bool {
	out, err := gitOut(path, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

// Behind counts commits the main branch has that this worktree doesn't.
// Exported so doctor's stale-base check is the same number `th ls` shows in its
// BEHIND column, asked the same way — two git calls would be two answers.
func Behind(path, mainBranch string) int {
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
