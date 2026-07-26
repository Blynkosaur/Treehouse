package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
	"github.com/Blynkosaur/treehouse/internal/deps"
	"github.com/Blynkosaur/treehouse/internal/envfile"
	"github.com/spf13/cobra"
)

var (
	hydrateDry      bool
	hydrateSkipDeps bool
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
	hydrateCmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing")
}

// runHydrate is a pure adapter (same shape as runDoctor): gather cwd, hand the
// work to hydrateAll. All judgment lives in check.
func runHydrate(cmd *cobra.Command, args []string) error {
	root, err := os.Getwd()
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
	rules := config.MergeRules(check.DefaultDepRules(), cfg.Deps)

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
	var fleet []check.Worktree
	for _, ref := range refs {
		switch {
		case samePath(ref.Path, root):
			if ref.Branch != "" {
				branch = ref.Branch
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

	in := check.DeriveInput{App: resolveApp(source, sourceRoot), Slug: check.Slug(branch), Fleet: fleet}
	return applyRepairs(root, check.Doctor{}.PlanDerive(wt, source, in))
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
