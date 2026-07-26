package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	lsMode    bool
	jsonOut   bool
	quiet     bool
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
}

// runDoctor is pure adapter: gather input, call internal/check, pick a face.
// All judgment lives in check.Doctor — this file just talks to the terminal.
func runDoctor(cmd *cobra.Command, args []string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	findings, err := diagnose(root)
	if err != nil {
		return err
	}

	switch {
	case jsonOut:
		if err := printJSON(findings, root); err != nil {
			return err
		}
	case quiet:
	case lsMode:
		printTable(findings, root)
	default:
		printReport(findings, root)
	}
	return verdict(findings)
}

// diagnose is doctor's gathering half: this worktree, the main checkout as
// fallback reference, and main's curated config. `new` and `ls` call it too, so
// the verdict is computed in exactly one place.
func diagnose(root string) ([]check.Finding, error) {
	wt, err := check.Discover(root)
	if err != nil {
		return nil, err
	}

	// Reference for expected keys: a service's own .env.example, else the main
	// checkout's .env. Outside a git repo there's no fallback — doctor still
	// works from .env.example alone, and with no curated required list.
	var source check.Worktree
	var d check.Doctor
	if mainRoot, err := check.MainWorktree(root); err == nil {
		source, _ = check.Discover(mainRoot)
		cfg, _ := config.Load(mainRoot) // absent/broken config: inferred-only
		d.Required = cfg.Env.Required
	}
	return d.CheckEnv(wt, source), nil
}

// status folds findings into the single word --json reports and the exit code
// encodes: inferred drift is a warning, a curated required key is a failure.
func status(findings []check.Finding) string {
	s := "ok"
	for _, f := range findings {
		if f.Fails() {
			return "fail"
		}
		if f.Drifted() {
			s = "warn"
		}
	}
	return s
}

// verdict turns findings into this process's exit code.
func verdict(findings []check.Finding) error {
	if status(findings) == "fail" {
		return exitCode(2)
	}
	return nil
}

// printJSON emits an object, never a bare array: hooks that key off "status"
// keep working when findings grow fields or the envelope grows keys.
func printJSON(findings []check.Finding, root string) error {
	if findings == nil {
		findings = []check.Finding{} // [] not null — consumers shouldn't special-case
	}
	out, err := json.MarshalIndent(struct {
		Schema   int             `json:"schema"`
		Root     string          `json:"root"`
		Status   string          `json:"status"`
		Findings []check.Finding `json:"findings"`
	}{1, root, status(findings), findings}, "", "  ")
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

// printReport is the narrative face: multi-line, explanatory, human-paced.
func printReport(findings []check.Finding, root string) {
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

	switch {
	case problems == 0:
		fmt.Println("\nall clear")
	case failed > 0:
		fmt.Printf("\n%d service(s) with env drift, %d missing a required key\n", problems, failed)
	default:
		fmt.Printf("\n%d service(s) with env drift (inferred requirements → warnings only)\n", problems)
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
