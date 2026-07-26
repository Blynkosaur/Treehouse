package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	refs, err := check.Worktrees(cwd)
	if err != nil {
		return err
	}
	mainRoot := refs[0].Path
	source, _ := check.Discover(mainRoot)
	cfg, _ := config.Load(mainRoot)
	d := check.Doctor{Required: cfg.Env.Required}

	rows := make([]check.Status, 0, len(refs))
	for _, ref := range refs {
		ref.Dirty = gitDirty(ref.Path)
		ref.Behind = gitBehind(ref.Path, refs[0].Branch)
		wt, _ := check.Discover(ref.Path) // unreadable worktree: still list it
		row := d.Status(wt, ref, source)
		row.Current = samePath(ref.Path, cwd)
		rows = append(rows, row)
	}

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

// gitDirty reports uncommitted changes. A bare or unreadable worktree answers
// "clean" — ls lists what it can and never fails over one broken row.
func gitDirty(path string) bool {
	out, err := gitOut(path, "status", "--porcelain")
	return err == nil && strings.TrimSpace(out) != ""
}

// gitBehind counts commits the main branch has that this worktree doesn't.
func gitBehind(path, mainBranch string) int {
	if mainBranch == "" {
		return 0 // detached or bare main: nothing to be behind
	}
	out, err := gitOut(path, "rev-list", "--count", "HEAD.."+mainBranch)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}
