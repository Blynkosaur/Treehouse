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

// CheckConfig reports a treehouse.toml that could not be parsed.
//
// FAIL rather than skip, because the file is nothing BUT human judgment —
// which env keys are required, which seed datasets exist, how to reach psql,
// which failure signatures this repo knows. A repo that has one is relying on
// it, and falling back to the built-in defaults in silence is how a
// `required = [...]` list stops being enforced without anybody finding out.
//
// Reported, never returned: config.Load's error must not abort a command that
// works fine on defaults. `th triage --hook` in particular runs after EVERY
// Bash tool call, and a hook that fails the whole session over a typo in a TOML
// file is the worst possible behaviour for something wired into an agent loop.
func (d Doctor) CheckConfig(path, parseErr string) Check {
	return Check{
		Name:   "config",
		Status: "fail",
		Detail: "could not parse " + path + ", so its judgment is not being applied: " + parseErr,
		Fix:    "fix the TOML in " + path,
	}
}

// DBState is what cmd learned about this worktree's database. Plan is PlanDB's
// own answer, re-used rather than re-derived: doctor and hydrate must agree on
// which clone belongs here, and they do that by asking the same planner.
type DBState struct {
	Plan  DBPlan // what hydrate would do (or skip) for this worktree
	EnvDB string // the database this worktree's root .env actually targets
	Main  bool   // this IS the main checkout
}

// The database states a worktree can be in, in one word each. They are the
// fleet table's DB column and the subject of CheckDB's sentences — one
// vocabulary, so the glance view and the report cannot disagree about which
// state a worktree is in. `shared` in particular is a FAILURE, not a value that
// happens to mention the template.
const (
	dbMain     = "main"     // this IS the main checkout: the template is its database
	dbUnusable = "unusable" // the template's name is one Postgres cannot hold — FAIL
	dbMissing  = "missing"  // no clone for this worktree yet
	dbOK       = "ok"       // .env targets this worktree's own clone
	dbShared   = "shared"   // the clone exists and .env still names the TEMPLATE — FAIL
	dbAdrift   = "adrift"   // the clone exists and .env names something else, or nothing
)

// DBWord is this worktree's database state in one word. It exists so the fleet
// table and the doctor report are two renderings of one switch rather than two
// switches that agree today: `th ls` used to answer clone-exists only, which
// made a worktree pointed at the SHARED database read `db: ok` in the glance
// view while `th doctor` called the same state a failure and exited 2.
func DBWord(s DBState) string {
	switch {
	case s.Plan.Bad:
		return dbUnusable
	case s.Main:
		return dbMain
	case s.Plan.Skip != "":
		return skip
	case !s.Plan.Exists:
		return dbMissing
	case s.EnvDB == s.Plan.Name:
		return dbOK
	case s.EnvDB == s.Plan.Template:
		return dbShared
	default:
		return dbAdrift
	}
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
	switch DBWord(s) {
	case dbUnusable:
		// Ahead of the main case on purpose: an unusable template name is a fact
		// about the REPO, and it is just as true standing in main. Reporting main
		// as fine would hide the reason every other worktree gets no clone.
		c.Status, c.Detail = "fail", s.Plan.Skip
		c.Fix = "rename the database, or point DATABASE_URL at a name Postgres can hold"
	case dbMain:
		// Main legitimately talks to the template — it is not a worktree that
		// should have a clone, and reporting it as one would train people to
		// ignore this row.
		c.Status, c.Detail = "ok", "the main checkout, using the shared database "+s.Plan.Template
	case skip:
		c.Status, c.Detail = skip, s.Plan.Skip
	case dbMissing:
		c.Status = "warn"
		c.Detail = fmt.Sprintf("no database clone for this worktree (want %s)", s.Plan.Name)
		c.Fix = "th hydrate"
	case dbOK:
		c.Status, c.Detail = "ok", ".env targets this worktree's own clone "+s.Plan.Name
	case dbShared:
		c.Status = "fail"
		c.Detail = fmt.Sprintf(".env still targets the SHARED database %s while this worktree's clone %s exists — a migration here hits every other worktree",
			s.Plan.Template, s.Plan.Name)
		c.Fix = "th hydrate"
	default: // dbAdrift — one word, but two sentences worth telling apart
		c.Status, c.Fix = "warn", "th hydrate"
		if s.EnvDB == "" {
			c.Detail = fmt.Sprintf("clone %s exists but this worktree's .env names no database", s.Plan.Name)
			break
		}
		c.Detail = fmt.Sprintf(".env targets %s, not this worktree's clone %s", s.EnvDB, s.Plan.Name)
	}
	return c
}

// CheckBase reports how far this worktree has drifted behind the main branch.
//
// WARN, never FAIL: a stale branch is a smell, not a broken environment. The
// code still runs, the tests still pass, and the only thing waiting is a rebase
// the human may be deliberately putting off. Failing here would put an exit 2
// on worktrees that are working fine, which is how a checker teaches people to
// stop reading it.
//
// The fix names origin because that is the useful rebase — after the fetch it
// is at least as far ahead as the local branch this count came from.
func (d Doctor) CheckBase(behind int, mainBranch string) Check {
	c := Check{Name: "base"}
	if behind == 0 {
		c.Status, c.Detail = "ok", "up to date with "+mainBranch
		return c
	}
	c.Status = "warn"
	c.Detail = fmt.Sprintf("%d commit(s) behind %s", behind, mainBranch)
	c.Fix = "git fetch && git rebase origin/" + mainBranch
	return c
}

// rank orders the four words so a fold can take the worst of them. It is the
// one place the tiers are written down.
//
// skip sits ABOVE ok, and that is the load-bearing part: a check that could not
// run has not verified anything, so a report made entirely of skips must not
// come back green. It sits below warn because a known problem is more
// actionable than an unknown one. Triage already refuses to read a skipped
// check as corroboration; this is the same rule on the doctor side.
var rank = map[string]int{"ok": 0, skip: 1, "warn": 2, "fail": 3}

// worse returns whichever of the two words the reader needs to hear.
func worse(a, b string) string {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// OpenPlan is what `th new` does with a worktree once it is built: L1's
// optional hand-off to an editor or an agent.
type OpenPlan struct {
	Command string // run this with the worktree as cwd; "" = do nothing
	Skip    string // why not, when the reader deserves to hear it; "" = say nothing
}

// PlanOpen decides whether the hand-off fires.
//
// The FAIL guard is the whole point of "born ready": handing somebody a
// worktree whose database is pointed at the shared one, or whose curated
// required keys are missing, is worse than handing them nothing — they will
// start working in it before they read the report scrolling past above the
// editor. Warnings do NOT block, deliberately: inferred env drift and a dead
// port are the normal state of a repo you have not run yet, and a hand-off that
// almost never fires is a hand-off nobody configures.
//
// Silence is the default at every step. An unconfigured repo says nothing,
// because most repos have no opinion here and a nag per `th new` is how a good
// flag gets turned off.
func PlanOpen(command, verdict string, force, refuse bool) OpenPlan {
	switch {
	case refuse:
		return OpenPlan{} // --no-open: the human already said no, so no commentary
	case command == "" && force:
		// --open asked for something that does not exist. This one is worth
		// saying: the flag did nothing, and silence looks like it worked.
		return OpenPlan{Skip: "no [open] command configured in treehouse.toml"}
	case command == "":
		return OpenPlan{}
	case verdict == "fail" && !force:
		return OpenPlan{Skip: "doctor reported a failure — not handing over a broken worktree (--open overrides)"}
	}
	return OpenPlan{Command: command}
}

// Verdict folds env findings and checks into the one word the exit code, the
// --json envelope and the ls column all agree on. Inferred drift warns; a
// curated required key and a worktree pointed at the shared database fail; a
// check nobody could run comes back skip rather than quietly ok.
func Verdict(findings []Finding, checks []Check) string {
	status := EnvStatus(findings)
	for _, c := range checks {
		status = worse(status, c.Status)
	}
	return status
}
