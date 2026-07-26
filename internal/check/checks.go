package check

import "fmt"

// Check is one non-env verdict: the database clone, its migration state, its
// seed data. A sibling of Finding, not a widening of it — a Finding is shaped
// around env keys (Missing/Empty/NoEnv/Keys), and bolting db fields onto it
// would give every env row a pile of nils to carry and every consumer a pile of
// nils to skip. Two flat lists in one envelope, each with its own shape.
type Check struct {
	Name   string `json:"name"`          // db | migrations | seed
	Status string `json:"status"`        // ok | warn | fail | skip
	Detail string `json:"detail"`        // what is true, in one line
	Fix    string `json:"fix,omitempty"` // the command that would resolve it
}

// skip is the fourth status, and it is not a synonym for ok: it means the
// question doesn't apply here (no database in this repo, no migration command
// configured). It never moves the verdict, and it says so out loud rather than
// leaving a silent gap in the report.
const skip = "skip"

// DBState is what cmd learned about this worktree's database. Plan is PlanDB's
// own answer, re-used rather than re-derived: doctor and hydrate must agree on
// which clone belongs here, and they do that by asking the same planner.
type DBState struct {
	Plan  DBPlan // what hydrate would do (or skip) for this worktree
	EnvDB string // the database this worktree's root .env actually targets
	Main  bool   // this IS the main checkout
}

// CheckDB reports whether this worktree has its own database and is pointed at
// it. The fail tier exists for exactly one case, and it is A2's whole point: a
// clone was created but .env still names the SHARED database. Nothing about
// that state looks wrong from inside the app — it connects, it queries, it
// migrates — and the migration lands on every other worktree at once. A
// half-applied hydrate has to be loud, or it is indistinguishable from a
// working one until somebody else's branch breaks.
func (d Doctor) CheckDB(s DBState) Check {
	c := Check{Name: "db"}
	switch {
	case s.Main:
		// Main legitimately talks to the template — it is not a worktree that
		// should have a clone, and reporting it as one would train people to
		// ignore this row.
		c.Status, c.Detail = "ok", "the main checkout, using the shared database "+s.Plan.Template
	case s.Plan.Skip != "":
		c.Status, c.Detail = skip, s.Plan.Skip
	case !s.Plan.Exists:
		c.Status = "warn"
		c.Detail = fmt.Sprintf("no database clone for this worktree (want %s)", s.Plan.Name)
		c.Fix = "th hydrate"
	case s.EnvDB == s.Plan.Name:
		c.Status, c.Detail = "ok", ".env targets this worktree's own clone "+s.Plan.Name
	case s.EnvDB == s.Plan.Template:
		c.Status = "fail"
		c.Detail = fmt.Sprintf(".env still targets the SHARED database %s while this worktree's clone %s exists — a migration here hits every other worktree",
			s.Plan.Template, s.Plan.Name)
		c.Fix = "th hydrate"
	case s.EnvDB == "":
		c.Status = "warn"
		c.Detail = fmt.Sprintf("clone %s exists but this worktree's .env names no database", s.Plan.Name)
		c.Fix = "th hydrate"
	default:
		c.Status = "warn"
		c.Detail = fmt.Sprintf(".env targets %s, not this worktree's clone %s", s.EnvDB, s.Plan.Name)
		c.Fix = "th hydrate"
	}
	return c
}

// Verdict folds env findings and checks into the one word the exit code, the
// --json envelope and the ls column all agree on. Inferred drift warns; a
// curated required key and a worktree pointed at the shared database fail.
func Verdict(findings []Finding, checks []Check) string {
	status := EnvStatus(findings)
	for _, c := range checks {
		switch {
		case c.Status == "fail":
			return "fail"
		case c.Status == "warn" && status == "ok":
			status = "warn"
		}
	}
	return status
}
