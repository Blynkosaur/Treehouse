package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
	"github.com/Blynkosaur/treehouse/internal/pg"
	"github.com/Blynkosaur/treehouse/internal/vault"
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
	recordJournal(root, findings, checks) // what `th why` diffs against; never fails a run

	switch {
	case jsonOut:
		if err := printFindingsJSON(findings, checks, root); err != nil {
			return err
		}
	case quiet:
	case lsMode:
		printTable(findings, root)
		printChecks(os.Stdout, checks)
	default:
		printReport(os.Stdout, findings, checks, root)
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
	var checks []check.Check
	if gitErr == nil {
		source, _ = check.Discover(mainRoot)
		cfg, checks = loadConfig(mainRoot)
		d.Required = cfg.Env.Required
		d.Secrets = cfg.Secrets.Keys
		pg.Use(cfg.Database.Psql)
	}

	findings := d.CheckEnv(wt, source)
	if gitErr != nil {
		return findings, nil, nil // outside a repo there is no fleet and no clone
	}
	// One git call for the fleet, shared by the db row (which worktree is this)
	// and the base row (what is it behind). Two would be two chances to disagree.
	refs, _ := check.Worktrees(root)
	checks = append(checks, dbChecks(d, refs, root, mainRoot, wt, source, cfg)...)
	checks = append(checks, serviceChecks(d, wt, cfg)...)
	checks = append(checks, secretChecks(d, wt, mainRoot)...)
	return findings, append(checks, baseChecks(d, refs, root, mainRoot)...), nil
}

// serviceChecks dials what this worktree says should be listening. The list is
// inferred from the PORT keys and sharpened by treehouse.toml through the same
// name-keyed Merge [[deps]], [[seed]] and [[signature]] use.
//
// Never reachable from `th ls`, the same constraint migrations live under: the
// fleet table exists to be glanced at, and a network fan-out per row is not a
// glance. A dial is cheap enough that a services column is viable later; it is
// not free enough to add one without asking.
func serviceChecks(d check.Doctor, wt check.Worktree, cfg config.File) []check.Check {
	// Declared is set HERE rather than read from the file: it is not a key a
	// human writes, it is the fact that a human wrote the entry at all — and
	// that fact is what earns the FAIL tier over an inferred row's WARN.
	declared := make([]check.Service, len(cfg.Service))
	for i, s := range cfg.Service {
		s.Declared = true
		declared[i] = s
	}
	services := config.Merge(check.InferServices(wt), declared, check.ServiceName)
	if len(services) == 0 {
		return nil // nothing to check is not the same as nothing wrong
	}
	return d.CheckServices(services, check.DialServices(services))
}

// baseChecks reports how far behind the main branch this worktree has drifted,
// reusing the same count `th ls` shows in its BEHIND column.
//
// Not emitted for the main checkout itself, and that is a correctness rule
// rather than tidiness: the count is HEAD..<main branch>, which in main is
// always zero, so a row there would print "up to date" over a main that is ten
// commits behind origin. Reporting green for something nobody measured is the
// one thing this report may not do.
//
// ponytail: measured against the LOCAL main branch, and doctor does not fetch —
// a stale local main under-reports. The fix line fetches; `th new` already does.
func baseChecks(d check.Doctor, refs []check.Ref, root, mainRoot string) []check.Check {
	branch := mainBranch(refs)
	if branch == "" || samePath(root, mainRoot) {
		return nil // no main branch to be behind, or standing on it
	}
	return []check.Check{d.CheckBase(check.Behind(root, branch), branch)}
}

// loadConfig reads main's treehouse.toml and turns a parse error into a CHECK
// rather than an abort. Every command that reads the file goes through here,
// because a broken one has to do two things at once that are easy to separate
// by accident: degrade to the built-in defaults everywhere, and be reported
// everywhere.
//
// The empty File on error is deliberate. toml.Unmarshal fills what it managed
// to read before it gave up, and half a curated `required` list is a judgment
// nobody made.
func loadConfig(mainRoot string) (config.File, []check.Check) {
	cfg, err := config.Load(mainRoot)
	if err == nil {
		return cfg, nil
	}
	path := filepath.Join(mainRoot, "treehouse.toml")
	return config.File{}, []check.Check{check.Doctor{}.CheckConfig(path, oneLine(err))}
}

// dbChecks asks Postgres the one question doctor always wants answered: does
// this worktree have its own database, and is its .env pointed at it.
//
// Postgres is asked exactly once, and only when main's .env names a database at
// all — so `th doctor` in a repo with no Postgres never shells out, the same
// bargain hydrate makes.
func dbChecks(d check.Doctor, refs []check.Ref, root, mainRoot string, wt, source check.Worktree, cfg config.File) []check.Check {
	template := check.EnvDB(source)
	if template == "" {
		return nil // no database in this repo: no row, not an empty one
	}
	names, err := pg.Databases()
	if err != nil {
		return unreachable("postgres is not reachable: " + oneLine(err))
	}

	state := check.DBState{
		Plan:  d.PlanDB(check.DBInput{Template: template, Existing: names, Slug: branchSlug(refs, root)}),
		EnvDB: check.EnvDB(wt),
		// samePath, not within: a worktree nested under the main checkout is not
		// the main checkout, and calling it one makes CheckDB report the shared
		// database as legitimate — silencing A2's whole fail tier.
		Main: samePath(root, mainRoot),
	}
	checks := []check.Check{d.CheckDB(state)}
	return append(checks, dataChecks(d, root, mainBranch(refs), wt, state.Plan, cfg)...)
}

// unreachable is what doctor reports when it could not reach the cluster at
// all. Every row a caller ASKED FOR still appears, saying it was not checked
// and why — the migration and seed rows used to be dropped entirely here, which
// answered `th doctor --db` with silence on the two things the flag exists to
// report. Silence is indistinguishable from "fine" to whoever reads it next,
// and running the project's migration-status command against a dead cluster
// would be worse: it exits non-zero, which this reads as "migrations pending".
func unreachable(why string) []check.Check {
	checks := []check.Check{{Name: "db", Status: "skip", Detail: why}}
	if !doctorDB {
		return checks
	}
	return append(checks,
		check.Check{Name: "migrations", Status: "skip", Detail: "not checked — " + why},
		check.Check{Name: "seed", Status: "skip", Detail: "not checked — " + why})
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

// secretChecks is the impure half of CheckSecrets: asking the keychain which
// references actually resolve. Beside CheckServices/DialServices, and for the
// same reason — the judgment stays pure and testable, the I/O stays here.
//
// A keychain that will not answer at all reports nothing rather than calling
// every reference dangling. "Nobody could ask" is not "the secret is gone", and
// a FAIL row on every vaulted key because the keychain was locked is exactly
// the kind of confident wrong verdict this report is built to avoid.
func secretChecks(d check.Doctor, wt check.Worktree, mainRoot string) []check.Check {
	vars := wt.EnvVarsByDir()["."]
	var dangling []string
	for key, name := range vault.Refs(vars) {
		if _, err := vault.Get(mainRoot, name); errors.Is(err, vault.ErrNotFound) {
			dangling = append(dangling, key)
		}
	}
	return d.CheckSecrets(vars, dangling)
}

// runIn runs one of the project's own commands in dir, with this worktree's
// root .env in the environment. check.Env is the one builder — see the ceiling
// documented there.
func runIn(dir string, wt check.Worktree, command string) ([]byte, error) {
	c := exec.Command("sh", "-c", command)
	c.Dir = dir
	c.Env = check.Env(wt, resolveVault(wt))
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
//
// It and printReport take a writer rather than printing, for one reason: the
// TUI's drill-in must show what `th doctor` shows, and the cheapest way to
// guarantee that forever is to render into a buffer with the same code. A
// second narrative renderer would drift the first time either changed.
func printChecks(w io.Writer, checks []check.Check) {
	for _, c := range checks {
		fmt.Fprintf(w, "%s %s: %s\n", checkMark(c.Status), c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(w, "    fix: %s\n", c.Fix)
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
func printReport(w io.Writer, findings []check.Finding, checks []check.Check, root string) {
	problems, failed := 0, 0
	for _, f := range findings {
		rel := relDir(root, f.Dir)
		switch {
		case f.NoEnv:
			problems++
			fmt.Fprintf(w, "✗ %s: .env missing entirely (%d keys expected)\n", rel, f.Keys)
		case f.Drifted():
			problems++
			if len(f.Missing) > 0 {
				fmt.Fprintf(w, "! %s: %d expected keys missing from .env:\n", rel, len(f.Missing))
				for _, k := range f.Missing {
					fmt.Fprintf(w, "    %s\n", k)
				}
			}
			if len(f.Empty) > 0 {
				fmt.Fprintf(w, "! %s: %d keys present but empty:\n", rel, len(f.Empty))
				for _, k := range f.Empty {
					fmt.Fprintf(w, "    %s\n", k)
				}
			}
		default:
			fmt.Fprintf(w, "✓ %s: .env has all %d expected keys\n", rel, f.Keys)
		}
		if f.Fails() {
			failed++
			fmt.Fprintf(w, "    required by treehouse.toml: %s\n", strings.Join(f.Failed, ", "))
		}
	}

	printChecks(w, checks)
	skipped := 0
	for _, c := range checks {
		switch c.Status {
		case "fail":
			failed++
			problems++
		case "warn":
			problems++
		case "skip":
			skipped++
		}
	}

	switch {
	case problems == 0 && skipped == 0:
		fmt.Fprintln(w, "\nall clear")
	case problems == 0:
		// "all clear" over a report that could not run half its checks is the
		// exact lie this whole pass exists to remove.
		fmt.Fprintf(w, "\nno problems found, but %d check(s) could not be run — see the • lines above\n", skipped)
	case failed > 0:
		fmt.Fprintf(w, "\n%d problem(s), %d of them failures\n", problems, failed)
	default:
		fmt.Fprintf(w, "\n%d problem(s) (inferred requirements → warnings only)\n", problems)
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
