package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/pg"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "One table: every worktree and its state",
	RunE:  runLs,
}

func init() {
	rootCmd.AddCommand(lsCmd)
	lsCmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output; suppresses all human text")
}

// fleet is everything both fleet views need before they can ask about a single
// worktree: git's list, the main checkout as reference, and the one psql round
// trip. Shared by `ls` and the TUI on purpose — the dashboard is a renderer over
// the same rows, and two copies of this setup would be two places for the
// database column to start disagreeing with itself.
func fleet(cwd string) ([]check.Ref, check.Worktree, check.Doctor, error) {
	refs, err := check.Worktrees(cwd)
	if err != nil {
		return nil, check.Worktree{}, check.Doctor{}, err
	}
	// git always reports at least the main checkout, so an empty list means the
	// porcelain said something we don't understand — say so instead of indexing
	// [0] and panicking in the user's face.
	if len(refs) == 0 {
		return nil, check.Worktree{}, check.Doctor{}, errors.New("no worktrees found")
	}
	mainRoot := refs[0].Path
	source, _ := check.Discover(mainRoot)
	cfg, cfgChecks := loadConfig(mainRoot)
	pg.Use(cfg.Database.Psql)
	d := check.Doctor{Required: cfg.Env.Required, MainBranch: refs[0].Branch, Repo: cfgChecks}

	// ONE psql round trip for the whole fleet, and only when main's .env names a
	// database at all. The column answers clone-exists / clone-missing and
	// nothing more: `ls` already pays a pair of git round trips and a tree walk
	// per row, and a table people run to glance at cannot also run somebody's
	// migration tooling. Unreachable Postgres leaves the column blank rather than
	// failing the command.
	if check.EnvDB(source) != "" {
		d.Databases, _ = pg.Databases()
	}
	return refs, source, d, nil
}

// runLs is an adapter: ask git for the fleet, ask git the two per-worktree
// questions check.Status can't (behind, dirty), then let check decide each row.
func runLs(cmd *cobra.Command, args []string) error {
	cwd, err := worktreeRoot()
	if err != nil {
		return err
	}
	refs, source, d, err := fleet(cwd)
	if err != nil {
		return err
	}
	mainRoot := refs[0].Path

	// Every row is an independent pair of git round trips plus a tree walk, and
	// they share nothing — so they run at once. Each goroutine owns exactly one
	// preallocated slot, which is what buys concurrency with no mutex and no
	// append race, and keeps the output in git's order (main first).
	rows := make([]check.Status, len(refs))
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i] = d.Row(ref, source)
			rows[i].Current = samePath(ref.Path, cwd)
		}()
	}
	wg.Wait()

	// Schema stays 2. The change is that `status` now means what doctor's means
	// — check.Verdict, folded over every row — rather than the env column alone.
	// Nothing consumes this field yet, and the two agreeing is worth more than a
	// version bump announcing that they used to disagree.
	if jsonOut {
		if err := printJSON(struct {
			Schema    int            `json:"schema"`
			Root      string         `json:"root"`
			Status    string         `json:"status"`
			Worktrees []check.Status `json:"worktrees"`
			Checks    []check.Check  `json:"checks"`
		}{2, mainRoot, check.Fleet(rows, d.Repo), rows, repoChecks(d)}); err != nil {
			return err
		}
		return fleetVerdict(rows, d.Repo)
	}
	// Repo-wide first: a treehouse.toml nobody could parse explains every column
	// under it, and printing it after the table reads as a footnote.
	printChecks(os.Stdout, d.Repo)
	printFleet(rows)
	return fleetVerdict(rows, d.Repo)
}

// repoChecks is d.Repo as [] rather than null — consumers should not have to
// special-case the healthy shape.
func repoChecks(d check.Doctor) []check.Check {
	if d.Repo == nil {
		return []check.Check{}
	}
	return d.Repo
}

// fleetVerdict gives `th ls` doctor's exit contract. An agent that has to parse
// a table to find out whether the fleet is broken will eventually parse it
// wrong; 2 means the same thing here it means everywhere else.
func fleetVerdict(rows []check.Status, repo []check.Check) error {
	if check.Fleet(rows, repo) == "fail" {
		return exitCode(2)
	}
	return nil
}

// printFleet renders the fleet with the same table doctor --ls uses.
func printFleet(rows []check.Status) {
	t := table.New().
		Border(lipgloss.NormalBorder()).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle.Padding(0, 1)
			}
			return lipgloss.NewStyle().Padding(0, 1)
		}).
		Headers("WORKTREE", "BRANCH", "ENV", "DB", "BEHIND", "DIRTY")

	for _, r := range rows {
		name := filepath.Base(r.Path)
		if r.Current {
			name = headerStyle.Render("* " + name) // where the human is standing
		}
		t.Row(name, r.Branch, envCell(r.Env), dbCell(r.DB), behindCell(r.Behind), dirtyCell(r.Dirty))
	}
	fmt.Println(t)
}

// behindCell and dirtyCell are git's two columns. They live beside the env and
// db cells so the table and the dashboard render a row identically — the TUI is
// a renderer over check.Status, not a second opinion about it.
func behindCell(behind int) string {
	if behind > 0 {
		return emptyStyle.Render(strconv.Itoa(behind))
	}
	return "—"
}

func dirtyCell(dirty bool) string {
	if dirty {
		return emptyStyle.Render("yes")
	}
	return "—"
}

// dbCell renders the clone column. Blank means the question was never asked —
// this repo declares no database — which is not the same as "missing" and must
// not be coloured like it; "skip" is the louder version, where we asked and
// could not get an answer.
//
// "shared" is red because it is the one state in this table that costs data: the
// clone exists, .env still names the template, and a migration run here lands on
// every other worktree. It used to render green, next to the word "ok", in the
// view people use to decide which worktree to hand an agent.
func dbCell(db string) string {
	switch db {
	case "ok", "main":
		return okStyle.Render(db)
	case "missing", "adrift":
		return emptyStyle.Render(db)
	case "shared", "unusable":
		return missingStyle.Render(db)
	case "skip":
		return "skip"
	default:
		return "—"
	}
}

func envCell(env string) string {
	switch env {
	case "fail":
		return missingStyle.Render("fail")
	case "warn":
		return emptyStyle.Render("warn")
	default:
		return okStyle.Render("ok")
	}
}
