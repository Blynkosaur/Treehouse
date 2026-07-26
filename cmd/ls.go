package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
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
	cfg, _ := config.Load(mainRoot)
	pg.Use(cfg.Database.Psql)
	d := check.Doctor{Required: cfg.Env.Required, MainBranch: refs[0].Branch}

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

	if jsonOut {
		return printJSON(struct {
			Schema    int            `json:"schema"`
			Root      string         `json:"root"`
			Status    string         `json:"status"`
			Worktrees []check.Status `json:"worktrees"`
		}{2, mainRoot, worstEnv(rows), rows})
	}
	printFleet(rows)
	return nil
}

func worstEnv(rows []check.Status) string {
	s := "ok"
	for _, r := range rows {
		if r.Env == "fail" {
			return "fail"
		}
		if r.Env == "warn" {
			s = "warn"
		}
	}
	return s
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

// dbCell renders the clone column. Blank means the question wasn't asked — no
// database in this repo, or Postgres wasn't up — which is not the same as
// "missing" and must not be coloured like it.
func dbCell(db string) string {
	switch db {
	case "ok":
		return okStyle.Render("ok")
	case "missing":
		return missingStyle.Render("missing")
	case "shared":
		return okStyle.Render("shared")
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
