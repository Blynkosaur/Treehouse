package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/spf13/cobra"
)

var (
	hookCmd = &cobra.Command{
		Use:   "hook",
		Short: "Claude Code hook entry points",
	}
	hookSessionCmd = &cobra.Command{
		Use:   "session",
		Short: "SessionStart: hand the agent this worktree's env and database state",
		RunE:  runHookSession,
	}
)

func init() {
	rootCmd.AddCommand(hookCmd)
	hookCmd.AddCommand(hookSessionCmd)
}

// runHookSession is C4: an agent should not have to discover that Redis is down
// by failing at it for twenty minutes. It starts the session already knowing.
//
// It does not read stdin. SessionStart sends a `source` (startup/resume/clear/
// compact) and only some of those are worth spending context on, but that
// filter belongs in settings.json's matcher, where a human can see and change
// it — not buried in a binary. See the README hook block.
func runHookSession(cmd *cobra.Command, args []string) error {
	// Claude Code names the project directory outright, which beats whatever
	// cwd a hook subprocess inherited.
	root, err := hookRoot(os.Getenv("CLAUDE_PROJECT_DIR"))
	if err != nil {
		return err
	}
	findings, checks, err := diagnose(root)
	if err != nil {
		// A hook that fails loudly at session start is worse than one that says
		// nothing: the session is not about treehouse. Stay quiet, exit 0.
		return nil
	}
	return emitContext("SessionStart", strings.Join(sessionLines(findings, checks, root), "\n"))
}

// sessionLines is the context an agent is handed before it has done anything.
// It is PREPENDED TO A CONTEXT WINDOW, not printed to a terminal — so it is
// short, it names the fix rather than explaining it, and it says nothing about
// the rows that are fine.
func sessionLines(findings []check.Finding, checks []check.Check, root string) []string {
	lines := []string{"treehouse — " + filepath.Base(root) + ": environment " + check.Verdict(findings, checks)}

	for _, f := range findings {
		if len(lines) == 4 {
			break // three services is enough to say "the env is broken"
		}
		if f.Drifted() {
			lines = append(lines, "  "+relDir(root, f.Dir)+": "+f.Summary())
		}
	}
	for _, c := range checks {
		if len(lines) == 7 {
			break
		}
		if c.Status == "fail" || c.Status == "warn" {
			line := "  " + c.Name + ": " + c.Detail
			if c.Fix != "" {
				line += " — fix: " + c.Fix
			}
			lines = append(lines, line)
		}
	}

	if len(lines) == 1 {
		lines[0] += " (all clear)"
	} else {
		lines = append(lines, "  run `th doctor` for the full report, `th hydrate` to repair")
	}
	// The one thing worth telling an agent that it cannot infer from the state
	// above: there is a tool for the next failure.
	return append(lines, "  `th triage -- <cmd>` says whether a failure is the environment or the code")
}
