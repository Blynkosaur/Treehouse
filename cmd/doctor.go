package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
	"github.com/Blynkosaur/treehouse/internal/pg"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	lsMode    bool
	jsonOut   bool
	quiet     bool
	doctorDB  bool
	doctorCmd = &cobra.Command{
		Use:   "doctor",
		Short: "Report env drift for every service in this worktree",
		RunE:  runDoctor,
	}
)

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().BoolVar(&lsMode, "ls", false, "compact table output")
	doctorCmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output; suppresses all human text")
	doctorCmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing; the exit code is the answer")
	doctorCmd.Flags().BoolVar(&doctorDB, "db", false, "also run the project's migration-status and seed checks")
}

// runDoctor is pure adapter: gather input, call internal/check, pick a face.
// All judgment lives in check.Doctor — this file just talks to the terminal.
func runDoctor(cmd *cobra.Command, args []string) error {
	root, err := worktreeRoot()
	if err != nil {
		return err
	}
	findings, checks, err := diagnose(root)
	if err != nil {
		return err
	}

	switch {
	case jsonOut:
		if err := printFindingsJSON(findings, checks, root); err != nil {
			return err
		}
	case quiet:
	case lsMode:
		printTable(findings, root)
		printChecks(checks)
	default:
		printReport(findings, checks, root)
	}
	return verdict(findings, checks)
}

// diagnose is doctor's gathering half: this worktree, the main checkout as
// fallback reference, and main's curated config. `new` calls it too, so the
// verdict is computed in exactly one place.
func diagnose(root string) ([]check.Finding, []check.Check, error) {
	wt, err := check.Discover(root)
	if err != nil {
		return nil, nil, err
	}

	// Reference for expected keys: a service's own .env.example, else the main
	// checkout's .env. Outside a git repo there's no fallback — doctor still
	// works from .env.example alone, and with no curated required list.
	var source check.Worktree
	var d check.Doctor
	mainRoot, gitErr := check.MainWorktree(root)
	var cfg config.File
	if gitErr == nil {
		source, _ = check.Discover(mainRoot)
		cfg, _ = config.Load(mainRoot) // absent/broken config: inferred-only
		d.Required = cfg.Env.Required
		pg.Use(cfg.Database.Psql)
	}

	findings := d.CheckEnv(wt, source)
	if gitErr != nil {
		return findings, nil, nil // outside a repo there is no fleet and no clone
	}
	return findings, dbChecks(d, root, mainRoot, wt, source, cfg), nil
}

// dbChecks asks Postgres the one question doctor always wants answered: does
// this worktree have its own database, and is its .env pointed at it.
//
// Postgres is asked exactly once, and only when main's .env names a database at
// all — so `th doctor` in a repo with no Postgres never shells out, the same
// bargain hydrate makes.
func dbChecks(d check.Doctor, root, mainRoot string, wt, source check.Worktree, cfg config.File) []check.Check {
	template := check.EnvDB(source)
	if template == "" {
		return nil // no database in this repo: no row, not an empty one
	}
	names, err := pg.Databases()
	if err != nil {
		return []check.Check{{Name: "db", Status: "skip",
			Detail: "postgres is not reachable: " + oneLine(err)}}
	}

	refs, _ := check.Worktrees(root)
	state := check.DBState{
		Plan:  d.PlanDB(check.DBInput{Template: template, Existing: names, Slug: branchSlug(refs, root)}),
		EnvDB: check.EnvDB(wt),
		Main:  within(root, mainRoot),
	}
	checks := []check.Check{d.CheckDB(state)}
	return append(checks, dataChecks(d, root, mainBranch(refs), wt, state.Plan, cfg)...)
}

// dataChecks runs the PROJECT's own tooling, and only because a human asked:
// `th doctor --db`. A migration-status command is seconds of somebody else's
// process, so it is opt-in here and flatly unreachable from `th ls`, which
// exists to be glanced at.
func dataChecks(d check.Doctor, root, mainBranch string, wt check.Worktree, plan check.DBPlan, cfg config.File) []check.Check {
	if !doctorDB {
		return nil
	}
	var present []string
	if plan.Exists {
		present, _ = pg.Seeds(plan.Name)
	}
	return []check.Check{
		d.CheckMigrations(migrationState(root, mainBranch, wt, cfg)),
		d.CheckSeed(config.Merge(nil, cfg.Seed, seedName), present),
	}
}

// migrationState runs the configured status command and asks git what this
// branch added. Both halves are gathered here so CheckMigrations stays pure.
func migrationState(root, mainBranch string, wt check.Worktree, cfg config.File) check.MigrationInput {
	in := check.MigrationInput{Command: cfg.Migrations.Status}
	if in.Command == "" {
		return in
	}

	out, err := runIn(root, wt, in.Command)
	var exit *exec.ExitError
	switch {
	case err == nil:
	case !errors.As(err, &exit):
		in.Err = oneLine(err)
	case exit.ExitCode() == 126 || exit.ExitCode() == 127:
		// The shell could not run it at all. Reading that as "migrations pending"
		// would report a typo in treehouse.toml as a permanent database problem.
		in.Err = strings.TrimSpace(string(out))
	default:
		in.Pending = true
	}

	in.Dir = check.MigrationsDir(root, cfg.Migrations.Dir)
	if in.Dir != "" && mainBranch != "" {
		in.Added = addedMigrations(root, mainBranch, in.Dir)
	}
	return in
}

// addedMigrations counts the migration files this branch has that the main
// branch does not. Three dots, not two: we want what THIS branch added since it
// diverged, not everything the two have done since.
func addedMigrations(root, mainBranch, dir string) int {
	out, err := gitOut(root, "diff", "--name-only", mainBranch+"...HEAD", "--", dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// runIn runs one of the project's own commands in dir, with this worktree's
// root .env in the environment — that is what makes `alembic current` answer
// about THIS worktree's clone rather than whatever the shell happened to
// export. ponytail: the root .env only; a monorepo whose migration tool lives
// in a subdirectory with its own .env gets os.Environ and its own dotenv
// loading, same as it has today.
func runIn(dir string, wt check.Worktree, command string) ([]byte, error) {
	c := exec.Command("sh", "-c", command)
	c.Dir = dir
	c.Env = os.Environ()
	for key, val := range wt.EnvVarsByDir()["."] {
		c.Env = append(c.Env, key+"="+val)
	}
	return c.CombinedOutput()
}

func seedName(s check.Seed) string { return s.Name }

// branchSlug is the slug naming this worktree's clone, or "" when no branch
// does — the same detached-HEAD skip PlanDB reports.
func branchSlug(refs []check.Ref, root string) string {
	for _, ref := range refs {
		if samePath(ref.Path, root) && ref.Branch != "" {
			return check.Slug(ref.Branch)
		}
	}
	return ""
}

// mainBranch is what `added migrations` is measured against. Empty when main is
// detached or bare, and then there is nothing to diff.
func mainBranch(refs []check.Ref) string {
	if len(refs) == 0 {
		return ""
	}
	return refs[0].Branch
}

// verdict turns the whole report into this process's exit code.
func verdict(findings []check.Finding, checks []check.Check) error {
	if check.Verdict(findings, checks) == "fail" {
		return exitCode(2)
	}
	return nil
}

// printFindingsJSON emits an object, never a bare array: hooks that key off
// "status" keep working when findings grow fields or the envelope grows keys.
// `th ls` emits the same envelope with a "worktrees" list in place of findings.
//
// Schema 2 adds "checks" beside "findings". The version bumps because that is
// what the field is for — a consumer that indexes findings for the whole story
// is wrong now, and should be told so at the envelope rather than by silently
// missing the database row.
func printFindingsJSON(findings []check.Finding, checks []check.Check, root string) error {
	if findings == nil {
		findings = []check.Finding{} // [] not null — consumers shouldn't special-case
	}
	if checks == nil {
		checks = []check.Check{}
	}
	return printJSON(struct {
		Schema   int             `json:"schema"`
		Root     string          `json:"root"`
		Status   string          `json:"status"`
		Findings []check.Finding `json:"findings"`
		Checks   []check.Check   `json:"checks"`
	}{2, root, check.Verdict(findings, checks), findings, checks})
}

// printChecks renders the sibling list under the env report, one line each,
// with the fix indented beneath the ones that have one.
func printChecks(checks []check.Check) {
	for _, c := range checks {
		fmt.Printf("%s %s: %s\n", checkMark(c.Status), c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Printf("    fix: %s\n", c.Fix)
		}
	}
}

func checkMark(status string) string {
	switch status {
	case "fail":
		return missingStyle.Render("✗")
	case "warn":
		return emptyStyle.Render("!")
	case "skip":
		return "•"
	default:
		return okStyle.Render("✓")
	}
}

func printJSON(envelope any) error {
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

// relDir prettifies a finding's directory relative to the scanned root.
func relDir(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return dir
	}
	return rel
}

// printReport is the narrative face: multi-line, explanatory, human-paced. The
// checks print between the per-service lines and the summary, because the
// summary is about the whole worktree — "all clear" printed above a failing
// database row would be a lie with a line number.
func printReport(findings []check.Finding, checks []check.Check, root string) {
	problems, failed := 0, 0
	for _, f := range findings {
		rel := relDir(root, f.Dir)
		switch {
		case f.NoEnv:
			problems++
			fmt.Printf("✗ %s: .env missing entirely (%d keys expected)\n", rel, f.Keys)
		case f.Drifted():
			problems++
			if len(f.Missing) > 0 {
				fmt.Printf("! %s: %d expected keys missing from .env:\n", rel, len(f.Missing))
				for _, k := range f.Missing {
					fmt.Printf("    %s\n", k)
				}
			}
			if len(f.Empty) > 0 {
				fmt.Printf("! %s: %d keys present but empty:\n", rel, len(f.Empty))
				for _, k := range f.Empty {
					fmt.Printf("    %s\n", k)
				}
			}
		default:
			fmt.Printf("✓ %s: .env has all %d expected keys\n", rel, f.Keys)
		}
		if f.Fails() {
			failed++
			fmt.Printf("    required by treehouse.toml: %s\n", strings.Join(f.Failed, ", "))
		}
	}

	printChecks(checks)
	for _, c := range checks {
		switch c.Status {
		case "fail":
			failed++
			problems++
		case "warn":
			problems++
		}
	}

	switch {
	case problems == 0:
		fmt.Println("\nall clear")
	case failed > 0:
		fmt.Printf("\n%d problem(s), %d of them failures\n", problems, failed)
	default:
		fmt.Printf("\n%d problem(s) (inferred requirements → warnings only)\n", problems)
	}
}

var (
	missingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	emptyStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	headerStyle  = lipgloss.NewStyle().Bold(true)
)

// printTable is the glance face: one bordered row per service, keys listed
// explicitly as multi-line cells.
func printTable(findings []check.Finding, root string) {
	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderRow(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle.Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		}).
		Headers("SERVICE", "KEYS", "MISSING", "EMPTY")

	for _, f := range findings {
		service := relDir(root, f.Dir)
		if f.NoEnv {
			t.Row(service, fmt.Sprintf("%d", f.Keys),
				missingStyle.Render("(.env missing entirely)"), "")
			continue
		}
		missing := okStyle.Render("—")
		if len(f.Missing) > 0 {
			missing = missingStyle.Render(strings.Join(f.Missing, "\n"))
		}
		empty := okStyle.Render("—")
		if len(f.Empty) > 0 {
			empty = emptyStyle.Render(strings.Join(f.Empty, "\n"))
		}
		t.Row(service, fmt.Sprintf("%d", f.Keys), missing, empty)
	}

	fmt.Println(t)
}
