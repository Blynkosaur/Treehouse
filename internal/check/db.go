package check

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// identRE is Postgres's unquoted identifier shape, narrowed to lower case
// because everything treehouse generates is lower case anyway.
var identRE = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// Ident reports whether name is a plain identifier — the only shape treehouse
// will put in a createdb argv or interpolate into SQL. Slug's output always
// passes by construction, but a template database name is read out of the
// user's .env and is arbitrary text; the answer to an odd one is to refuse it,
// not to escape it. internal/pg asks again at the boundary, against this same
// rule: one definition, because two would eventually disagree.
func Ident(name string) bool { return identRE.MatchString(name) }

// DBName is the clone's name: a hint of which app it belongs to, then the slug
// that makes it unique.
//
// The base is what gets truncated, never the slug. derive.go budgets
// "app_wt_" + 40 + 7 = 54 bytes, which only holds while the base stays three
// characters — a real one doesn't. myproject_development (21) + "_wt_" + 47
// comes to 72, past Postgres's 63-byte identifier cap, and a name Postgres
// truncates silently is two branches quietly sharing one database. The slug
// carries the collision-safety, so the base — a human hint and nothing more —
// is the half that pays: 12 + 4 + 47 = 63 exactly.
func DBName(base, slug string) string {
	if len(base) > 12 {
		base = base[:12]
	}
	// Trimming keeps "app_" from deriving app__wt_x. Legality survives it: a
	// prefix of an identifier is an identifier, and "" leaves a leading _.
	return strings.TrimRight(base, "_") + "_wt_" + slug
}

// DBFromURL reads the database name out of a Postgres connection URL, and
// reports whether the URL was one at all.
//
// net/url, never a regex: a connstring carries ?sslmode=require after the
// database, an @ inside the password before the host, and a :5433 that a
// hand-rolled pattern reads as part of the name. Anything that isn't a postgres
// URL naming a database comes back false — cmd resolves the template with this
// and PlanDerive rewrites with it, so one definition keeps the two from
// disagreeing about what "the database in that URL" means.
func DBFromURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || len(u.Path) < 2 {
		return "", false
	}
	return u.Path[1:], true
}

// DBInput is everything PlanDB can't work out for itself — the same bargain
// DeriveInput makes. Existing is deliberately a plain []string rather than
// something that can go and ask: cmd asks psql once, the planner stays pure,
// and there is no one-implementation "runner" interface to mock.
type DBInput struct {
	Template string   // the shared database to clone, resolved from main's .env
	Existing []string // every database name that exists right now
	Slug     string   // Slug(branch); empty when no branch can name a clone
}

// DBPlan is the decision: which database to make, from what, or why not.
type DBPlan struct {
	Name     string
	Template string
	Exists   bool   // already there — a no-op, so re-running hydrate reuses the clone
	Skip     string // why nothing will be created
}

// PlanDB decides this worktree's database clone.
//
// Every reason to do nothing is a Skip, never an error: a missing database is a
// line in the report, exactly like a port that wouldn't fit. Creation is
// conditional on there being somewhere to point the result — cmd resolves
// Template from main's .env and passes "" when the repo declares no database at
// all, which is what stops every `th new` in a non-Postgres repo from leaving an
// orphan behind.
func (d Doctor) PlanDB(in DBInput) DBPlan {
	switch {
	case in.Slug == "":
		// Naming a clone after a directory instead (hydrate's fallback for ports)
		// would key it to a path, so renaming the directory orphans the database
		// permanently. A port can afford a shaky name; a database cannot.
		return DBPlan{Skip: "detached HEAD — no stable name for a database clone"}
	case in.Template == "":
		return DBPlan{Skip: "no DATABASE_URL or POSTGRES_DB in the main checkout — a clone would have nothing pointing at it"}
	case !Ident(in.Template):
		return DBPlan{Skip: fmt.Sprintf("template database %q is not a plain identifier — refusing to quote it into SQL", in.Template)}
	}

	plan := DBPlan{Name: DBName(in.Template, in.Slug), Template: in.Template}
	plan.Exists = slices.Contains(in.Existing, plan.Name)
	return plan
}
