package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
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

// runLs is an adapter: ask git for the fleet, ask git the two per-worktree
// questions check.Status can't (behind, dirty), then let check decide each row.
func runLs(cmd *cobra.Command, args []string) error {
	cwd, err := worktreeRoot()
	if err != nil {
		return err
	}
	refs, err := check.Worktrees(cwd)
	if err != nil {
		return err
	}
	// git always reports at least the main checkout, so an empty list means the
	// porcelain said something we don't understand — say so instead of indexing
	// [0] and panicking in the user's face.
	if len(refs) == 0 {
		return errors.New("no worktrees found")
	}
	mainRoot := refs[0].Path
	source, _ := check.Discover(mainRoot)
	cfg, _ := config.Load(mainRoot)
	d := check.Doctor{Required: cfg.Env.Required, MainBranch: refs[0].Branch}

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
		}{1, mainRoot, worstEnv(rows), rows})
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
		Headers("WORKTREE", "BRANCH", "ENV", "BEHIND", "DIRTY")

	for _, r := range rows {
		name := filepath.Base(r.Path)
		if r.Current {
			name = headerStyle.Render("* " + name) // where the human is standing
		}
		behind := "—"
		if r.Behind > 0 {
			behind = emptyStyle.Render(strconv.Itoa(r.Behind))
		}
		dirty := "—"
		if r.Dirty {
			dirty = emptyStyle.Render("yes")
		}
		t.Row(name, r.Branch, envCell(r.Env), behind, dirty)
	}
	fmt.Println(t)
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
