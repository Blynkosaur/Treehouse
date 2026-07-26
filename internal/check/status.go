package check

// Status is one worktree's row in the fleet view — pure data, no presentation.
// Deliberately per-worktree rather than a batch API: the TUI streams cells in
// concurrently, and a batch call would make it wait for the slowest worktree.
type Status struct {
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Env     string `json:"env"`    // ok | warn | fail
	Behind  int    `json:"behind"` // commits in main this branch doesn't have
	Dirty   bool   `json:"dirty"`
	Current bool   `json:"current"` // the worktree the human is standing in
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
	if s.Branch == "" {
		s.Branch = "(detached)"
		if ref.Bare {
			s.Branch = "(bare)"
		}
	}
	return s
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
