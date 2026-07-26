package check

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Quote renders name as a Postgres quoted identifier: wrapped in double quotes,
// with any embedded double quote doubled. That is the whole rule, it is
// well-defined, and it is what makes a legal name like app-db or APPDB work.
//
// Refusing anything that needed quoting was the earlier instinct, and it was
// wrong in the direction that matters. The injection it dodged is dodged just as
// completely here — a " cannot escape a doubled " — while the cost was a whole
// feature silently not working, permanently, for a repo whose database happens
// to have a dash in its name. IdentReason still refuses the inputs quoting
// cannot rescue.
func Quote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// IdentReason names why a database name cannot be used at all, or "" when it
// can. It is no longer a shape test — Quote handles shape — only a refusal of
// what nothing can rescue:
//
//   - empty: there is no name to quote.
//   - over 63 bytes: Postgres truncates identifiers silently, and two branches
//     truncated to the same 63 bytes share one database, which is the exact
//     failure Slug's hash suffix exists to prevent.
//   - a NUL: C strings end there, so the server would see a shorter name than
//     the one we quoted.
//   - a newline or carriage return: these ride into psql as `--set` values and
//     through its stdin, where a line break is a statement boundary.
//
// It returns the reason rather than a bool because both callers have to SAY it:
// a name treehouse won't touch is a loud check with a fix line, never a shrug.
//
// ponytail: a name containing "=" still reaches psql's -d, which libpq reads as
// a conninfo string rather than a database name. Passing it through PGDATABASE
// instead would fix that and break `[database] psql = "docker compose exec …"`,
// which does not forward host environment into the container. Rename the
// database if you are the one person this happens to.
func IdentReason(name string) string {
	switch {
	case name == "":
		return "it is empty"
	case len(name) > 63:
		return fmt.Sprintf("it is %d bytes and Postgres truncates identifiers at 63", len(name))
	case strings.ContainsAny(name, "\x00\n\r"):
		return "it contains a NUL, a newline or a carriage return"
	}
	return ""
}

// Ident is IdentReason as the predicate most callers want.
func Ident(name string) bool { return IdentReason(name) == "" }

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
		// A template name is any legal Postgres identifier now that we quote
		// rather than refuse, so it may be multi-byte — and byte 12 can land
		// inside a rune. Half a rune is not a name.
		for len(base) > 0 && !utf8.ValidString(base) {
			base = base[:len(base)-1]
		}
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

// EnvDB is the database a worktree's root .env actually points at.
// DATABASE_URL is the source of truth because it is what an ORM dials;
// POSTGRES_DB is the compose-style fallback.
//
// Asked of MAIN it answers the template every clone is cut from — and empty
// means the repo declares no database at all, so nothing is created and psql is
// never asked, which is what keeps `th new` in a non-Postgres repo from leaving
// orphans. Asked of a worktree it answers what that checkout is talking to,
// which is how doctor catches a half-applied hydrate. One function, because two
// would eventually disagree about which key wins.
func EnvDB(w Worktree) string {
	vars := w.EnvVarsByDir()["."]
	if db, ok := DBFromURL(vars["DATABASE_URL"]); ok {
		return db
	}
	return vars["POSTGRES_DB"]
}

// provenancePrefix opens every comment treehouse writes on a database it made.
const provenancePrefix = "treehouse:"

// Provenance is the comment left on a clone at creation time. It is the ONLY
// thing that makes a database ours, and it is why gc never matches on a name
// prefix: a prefix can match somebody's real database, and Slug is one-way, so
// a name can't be reversed to the branch a human would need to see before
// agreeing to a drop.
func Provenance(mainRoot, branch string) string {
	return provenancePrefix + mainRoot + ":" + branch
}

// ParseProvenance reads one back. It splits at the LAST colon because a Unix
// path may legally contain one and a git branch may not — so the tail is always
// the branch, whatever the path did.
func ParseProvenance(comment string) (mainRoot, branch string, ok bool) {
	rest, cut := strings.CutPrefix(comment, provenancePrefix)
	if !cut {
		return "", "", false
	}
	i := strings.LastIndex(rest, ":")
	if i < 1 || i == len(rest)-1 {
		return "", "", false // no branch, or no path: not a comment we wrote
	}
	return rest[:i], rest[i+1:], true
}

// Database is one database as the cluster reports it. Plain data gathered by
// internal/pg and handed to PlanGC, so the collector's judgment can be tested
// from struct literals instead of from a running Postgres.
type Database struct {
	Name    string
	Comment string
	Size    string // pg_size_pretty, for the confirmation prompt
}

// GCInput is everything PlanGC can't work out for itself.
type GCInput struct {
	Owned    []Database // every commented database in the cluster (pg.Commented)
	Fleet    []Ref      // the worktrees that exist right now
	Template string     // the shared database — never a candidate
	MainRoot string     // only clones cut from THIS repo are in scope

	// InUse is the database each live worktree's .env actually names. It is the
	// only liveness test that cannot go stale, and it is what stops gc dropping
	// a database out from under a checkout somebody is working in — see PlanGC.
	InUse []string
}

// DBDrop is one database gc would remove, with everything a human needs to
// agree to it: what it is called, whose branch it was, and how much it costs.
type DBDrop struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Size   string `json:"size"`
}

// PlanGC picks the clones whose worktrees are gone.
//
// Ownership is by comment, never by name prefix: anything without a treehouse
// provenance comment naming THIS repo is not a candidate, full stop. That is
// the whole safety model, and it is why Owned is the input rather than every
// database in the cluster.
//
// Liveness is checked three ways, and any one of them spares a database.
//
// The branch is the cheap test — a clone is usually a corpse exactly when its
// worktree is gone — and it is what the comment exists to record. The derived
// name is the belt: it costs one map lookup and it means a template renamed
// since the clones were cut (app_dev -> app_development) can't turn the whole
// live fleet into candidates.
//
// InUse is the one that actually holds. Both name-based tests fail the moment a
// worktree's BRANCH stops matching the comment, which two ordinary git commands
// do: `git checkout --detach` (a bisect, a sha checkout) and `git branch -m`.
// The worktree is still there, its .env still names the clone, somebody is still
// working in it — and gc offered to drop it. Nothing recovers from that, so the
// last word belongs to the file the app actually reads.
func (d Doctor) PlanGC(in GCInput) []DBDrop {
	liveBranch := map[string]bool{}
	liveName := map[string]bool{}
	for _, ref := range in.Fleet {
		if ref.Branch == "" {
			continue // detached: it never had a clone to begin with
		}
		liveBranch[ref.Branch] = true
		liveName[DBName(in.Template, Slug(ref.Branch))] = true
	}

	var drops []DBDrop
	for _, db := range in.Owned {
		root, branch, ok := ParseProvenance(db.Comment)
		switch {
		case !ok, root != in.MainRoot:
			continue // somebody else's comment, or another repo's clone
		case liveBranch[branch], liveName[db.Name], slices.Contains(in.InUse, db.Name):
			continue // still in use
		case db.Name == in.Template:
			// Unreachable through the comment — treehouse never writes one on a
			// template — and checked anyway, because the cost of being wrong here
			// is the shared development database.
			continue
		}
		drops = append(drops, DBDrop{Name: db.Name, Branch: branch, Size: db.Size})
	}
	sort.Slice(drops, func(i, j int) bool { return drops[i].Name < drops[j].Name })
	return drops
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

	// Bad marks a Skip that is a FAILURE rather than an absence: the template
	// database has a name treehouse cannot use at all. Every other reason to do
	// nothing is a legitimate "this repo has no clone to make"; this one means a
	// repo that WANTS clones will never get one, and a quiet skip line is how
	// that goes unnoticed for a month.
	Bad bool
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
	}
	if why := IdentReason(in.Template); why != "" {
		return DBPlan{Bad: true, Skip: fmt.Sprintf("template database %q cannot be used: %s", in.Template, why)}
	}

	plan := DBPlan{Name: DBName(in.Template, in.Slug), Template: in.Template}
	plan.Exists = slices.Contains(in.Existing, plan.Name)
	return plan
}
