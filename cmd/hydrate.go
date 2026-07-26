package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
	"github.com/Blynkosaur/treehouse/internal/deps"
	"github.com/Blynkosaur/treehouse/internal/envfile"
	"github.com/Blynkosaur/treehouse/internal/pg"
	"github.com/spf13/cobra"
)

var (
	hydrateDry      bool
	hydrateSkipDeps bool
	hydrateForceDB  bool
	hydrateCmd      = &cobra.Command{
		Use:   "hydrate",
		Short: "Fill this worktree's .env files from the main checkout",
		RunE:  runHydrate,
	}
)

func init() {
	rootCmd.AddCommand(hydrateCmd)
	hydrateCmd.Flags().BoolVar(&hydrateDry, "dry", false, "show what would change without writing")
	hydrateCmd.Flags().BoolVar(&hydrateSkipDeps, "skip-deps", false, "only fill .env; skip dependency provisioning")
	hydrateCmd.Flags().BoolVar(&hydrateForceDB, "force-db", false, "disconnect everything using the template database so it can be cloned")
	hydrateCmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing")
}

// runHydrate is a pure adapter (same shape as runDoctor): gather cwd, hand the
// work to hydrateAll. All judgment lives in check.
func runHydrate(cmd *cobra.Command, args []string) error {
	root, err := worktreeRoot()
	if err != nil {
		return err
	}
	return hydrateAll(root)
}

// hydrateAll runs the whole pipeline against root, in strict order: fill
// canonical → provision deps → derive per-worktree values. Derive goes last so
// we never point a derived value at a broken env. `th new` calls this too —
// that's why it isn't inlined into runHydrate.
func hydrateAll(root string) error {
	wt, err := check.Discover(root)
	if err != nil {
		return err
	}

	// Canonical values come from the main worktree's .env files.
	sourceRoot, err := check.MainWorktree(root)
	if err != nil {
		return fmt.Errorf("locating main worktree: %w", err)
	}
	source, err := check.Discover(sourceRoot)
	if err != nil {
		return err
	}
	// Read once, before any phase acts: a repo whose Postgres lives inside
	// compose needs this in place before the clone is attempted, not after.
	if cfg, err := config.Load(sourceRoot); err == nil {
		pg.Use(cfg.Database.Psql)
	}

	repairs := check.Doctor{}.PlanHydrate(wt, source)
	// A clean env phase is a note, not an exit: the phases below still run.
	if len(repairs) == 0 {
		sayln("nothing to hydrate — every .env already has its keys")
	}
	if err := applyRepairs(root, repairs); err != nil {
		return err
	}

	hydrateDeps(root, wt, source, sourceRoot)
	return hydrateDerive(root, sourceRoot, source)
}

// applyRepairs turns plans into writes and narrates them. The three writers
// share a signature precisely so this stays one loop and one switch.
func applyRepairs(root string, repairs []check.Repair) error {
	for _, r := range repairs {
		rel := relDir(root, filepath.Dir(r.EnvPath))

		// A skip is a note about ONE concern (ports), not a veto on the repair:
		// whatever else it planned still gets written.
		if r.Skip != "" {
			say("• %s: skipped (%s)\n", rel, r.Skip)
		}
		// A warn means we DID act, on a call the human should know about — so it
		// prints even when the repair itself is otherwise silent.
		if r.Warn != "" {
			say("! %s: %s\n", rel, r.Warn)
		}
		if len(r.Add) == 0 {
			continue
		}

		switch {
		case hydrateDry && r.Create:
			say("~ %s: would create .env (%s)\n", rel, nKeys(len(r.Add)))
		case hydrateDry && r.Overwrite:
			say("~ %s: would set %s\n", rel, keyList(r.Add))
		case hydrateDry:
			say("~ %s: would add %s\n", rel, nKeys(len(r.Add)))
		default:
			apply := envfile.Append
			switch {
			case r.Create:
				apply = envfile.Create
			case r.Overwrite:
				apply = envfile.Set // derived values replace what's there
			}
			// main's .env may sit in a gitignored dir that no checkout creates
			// (secrets/, infra/). Without this the whole hydrate aborts on ENOENT
			// and leaves the worktree half-built.
			if err := os.MkdirAll(filepath.Dir(r.EnvPath), 0o755); err != nil {
				return err
			}
			if err := apply(r.EnvPath, r.Add); err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			switch {
			case r.Create:
				say("✓ %s: created .env (%s)\n", rel, nKeys(len(r.Add)))
			case r.Overwrite:
				say("✓ %s: set %s\n", rel, keyList(r.Add))
			default:
				say("✓ %s: added %s\n", rel, nKeys(len(r.Add)))
			}
		}

		// Keys the main checkout couldn't supply were written empty — the human
		// still has to fill them, so name them explicitly.
		if len(r.Unsourced) > 0 {
			say("    fill manually (no value in main): %s\n", strings.Join(r.Unsourced, ", "))
		}
	}
	return nil
}

// hydrateDeps provisions heavy dependency dirs (node_modules, .venv, …) into
// this worktree. Rules come from the built-in defaults overlaid with any
// treehouse.toml in main — the source of truth. All judgment lives in
// check.PlanDeps; this loop only applies plans and talks to the terminal.
//
// Failures are printed red, never returned: a missing node_modules is a red
// line in the report, not a reason to abandon the worktree half-built.
// ponytail: the clone is a CoW copy of main's tree, so an npm install running
// in main at the same moment can yield a torn copy. Accepted, not handled.
func hydrateDeps(root string, wt, source check.Worktree, sourceRoot string) {
	if hydrateSkipDeps {
		return
	}

	cfg, _ := config.Load(sourceRoot) // absent/broken config: fall back to defaults
	rules := config.Merge(check.DefaultDepRules(), cfg.Deps, func(r check.DepRule) string { return r.Name })

	plans := check.Doctor{}.PlanDeps(wt, source, rules)
	if len(plans) == 0 {
		sayln("deps: nothing to provision")
		return
	}

	for _, p := range plans {
		rel := relDir(root, p.Dst)

		switch {
		case p.Skip != "":
			say("• %s: skipped (%s)\n", rel, p.Skip)
		case hydrateDry && p.Action == check.Clone:
			say("~ %s: would clone\n", rel)
		case hydrateDry:
			say("~ %s: would recreate (%s)\n", rel, p.Command)
		case p.Action == check.Clone:
			if err := deps.CloneTree(p.Src, p.Dst); err != nil {
				say("✗ %s: clone failed: %v\n", rel, err)
				continue
			}
			say("✓ %s: cloned\n", rel)
		default:
			say("~ %s: recreating (%s)\n", rel, p.Command)
			if err := deps.RunRecreate(filepath.Dir(p.Dst), p.Command); err != nil {
				say("✗ %s: recreate failed: %v\n", rel, err)
				continue
			}
			say("✓ %s: recreated\n", rel)
		}
	}
}

// hydrateDerive writes this worktree's private compose project name and port
// offsets (E2/E3). It gathers what PlanDerive can't see — the branch, the app
// name, the sibling worktrees that form the port registry — and applies the plan.
func hydrateDerive(root, sourceRoot string, source check.Worktree) error {
	// main IS the base. Its ports are what every other worktree offsets FROM and
	// its COMPOSE_PROJECT_NAME is the app name resolveApp reads back, so deriving
	// here rewrites the source of truth: ports walk one offset further up on
	// every run, and the project name grows another "_main" each time.
	if within(root, sourceRoot) {
		sayln("• main worktree: nothing to derive (its values are the base)")
		return nil
	}

	// Re-discover: the fill phase just created .env files that did not exist
	// when the caller walked this worktree, and derive can only rewrite files it
	// can see. Skipping this makes derive a no-op on every `th new`.
	wt, err := check.Discover(root)
	if err != nil {
		return err
	}

	refs, err := check.Worktrees(root)
	if err != nil {
		return err
	}
	branch := filepath.Base(root) // detached, bare, or run from a subdir
	// The database gets no such fallback. A directory name keys the clone to a
	// path, so renaming the directory orphans it permanently — a port can afford
	// a shaky name, a database cannot. Empty is PlanDB's detached-HEAD skip.
	dbSlug := ""
	var fleet []check.Worktree
	for _, ref := range refs {
		switch {
		case samePath(ref.Path, root):
			if ref.Branch != "" {
				branch = ref.Branch
				dbSlug = check.Slug(ref.Branch)
			}
		case samePath(ref.Path, sourceRoot):
			// main is passed as `source`; counting it twice would be harmless
			// but the fleet is documented as "the others".
		default:
			if other, err := check.Discover(ref.Path); err == nil {
				fleet = append(fleet, other)
			}
		}
	}

	// The clone is made HERE, one step before the plan that points at it: DBName
	// is only ever non-empty once the database is confirmed to exist, so a .env
	// can never name one that isn't there. It also lands after the canonical
	// repair and the deps phase, which is A2's other ordering requirement.
	in := check.DeriveInput{
		App:    resolveApp(source, sourceRoot),
		Slug:   check.Slug(branch),
		Fleet:  fleet,
		DBName: hydrateDB(sourceRoot, branch, dbSlug, source),
	}
	return applyRepairs(root, check.Doctor{}.PlanDerive(wt, source, in))
}

// hydrateDB creates this worktree's database clone (A1) and returns the name to
// point .env at — "" when nothing was created.
//
// Every reason not to create one is a report line, never an error: hydrate has
// already filled env and provisioned deps by the time we get here, and an
// unreachable Postgres must not take that work down with it. The clone itself is
// near-instant — Postgres copies the template at the file level.
func hydrateDB(mainRoot, branch, slug string, source check.Worktree) string {
	in := check.DBInput{Template: check.EnvDB(source), Slug: slug}

	// Postgres is asked exactly once, and only when the plan would otherwise act.
	// Planning twice is free (PlanDerive's sibling is a pure function), and it
	// keeps a repo that declares no database at all from shelling out to psql
	// just to be told there was nothing to do.
	if (check.Doctor{}.PlanDB(in).Skip) == "" {
		names, err := pg.Databases()
		if err != nil {
			// Collapsed to one line: psql's connection error is three of them, and
			// this prints on every `th new` for a dev whose server simply isn't up.
			say("• db: skipped (postgres is not reachable: %s)\n", oneLine(err))
			return ""
		}
		in.Existing = names
	}

	plan := check.Doctor{}.PlanDB(in)
	switch {
	case plan.Skip != "":
		say("• db: skipped (%s)\n", plan.Skip)
		return ""
	case plan.Exists:
		// A1: re-running hydrate reuses the clone rather than making a second one.
		say("• db: %s already exists — reusing it\n", plan.Name)
		return plan.Name
	case hydrateDry:
		// Dry writes nothing, so previewing the .env repoint costs nothing either.
		say("~ db: would clone %s from %s\n", plan.Name, plan.Template)
		return plan.Name
	}

	if hydrateForceDB {
		// The one path that ever disconnects anybody, and only because a human
		// spelled out the flag. Aimed at the template, never at a clone.
		if err := pg.Terminate(plan.Template); err != nil {
			say("✗ db: could not disconnect %s: %v\n", plan.Template, err)
			return ""
		}
	}
	if err := pg.Create(plan.Name, plan.Template); err != nil {
		if errors.Is(err, pg.ErrBusy) {
			reportBusy(plan.Template)
		} else {
			say("✗ db: %v\n", err)
		}
		return ""
	}
	say("✓ db: cloned %s from %s\n", plan.Name, plan.Template)

	// Provenance carries the ORIGINAL branch name because Slug is one-way:
	// without it `th gc` can see a stray clone but not say whose it is. Failing
	// to record it costs a future gc, not this hydrate.
	if err := pg.Comment(plan.Name, check.Provenance(mainRoot, branch)); err != nil {
		say("! db: %s has no provenance comment (%v) — `th gc` won't recognise it\n", plan.Name, err)
	}
	return plan.Name
}

// reportBusy explains a locked template instead of forcing it open. A dev server
// holding a connection is the COMMON case, not an edge one, and retrying is
// useless — the app reconnects instantly. So we name who is in the way and offer
// the only two fixes that work.
func reportBusy(template string) {
	say("✗ db: %s is in use — Postgres cannot clone a database somebody is connected to\n", template)
	sessions, err := pg.Sessions(template)
	if err == nil && len(sessions) == 0 {
		// The connections went away between createdb's failure and this query.
		sayln("    (nothing is connected now — re-running may just work)")
	}
	for _, s := range sessions {
		say("    %s\n", s)
	}
	sayln("  fix: stop the app and re-run, or `th hydrate --force-db` to disconnect it")
}

// resolveApp names the compose project family. If main's root .env already
// declares COMPOSE_PROJECT_NAME, the app has named itself — reuse it, so main's
// own containers keep the name they have. Otherwise use Docker Compose's own
// default rule: the project directory's name.
func resolveApp(source check.Worktree, sourceRoot string) string {
	if name := source.EnvVarsByDir()["."]["COMPOSE_PROJECT_NAME"]; name != "" {
		return name
	}
	return check.Slug(filepath.Base(sourceRoot))
}

// samePath compares paths through symlinks: git reports /private/var where
// os.Getwd may report /var. Getting this wrong would put a worktree in its own
// port registry, and its ports would move on every hydrate.
func samePath(a, b string) bool { return resolve(a) == resolve(b) }

func resolve(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

// nKeys pluralizes a key count for human output ("1 key", "3 keys").
func nKeys(n int) string {
	if n == 1 {
		return "1 key"
	}
	return fmt.Sprintf("%d keys", n)
}

// keyList names the keys a repair writes — for derived values the names matter
// more than the count ("set COMPOSE_PROJECT_NAME, PORT").
func keyList(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
