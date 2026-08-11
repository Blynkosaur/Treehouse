package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/vault"
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
// vaulted reports whether this worktree's root .env actually references the
// vault. Cheap — it reads the file it already walked and asks no keychain.
func vaulted(root string) bool {
	wt, err := check.Discover(root)
	if err != nil {
		return false
	}
	return len(vault.Refs(wt.EnvVarsByDir()["."])) > 0
}

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
	verdict := check.Verdict(findings, checks)
	lines := []string{"treehouse — " + filepath.Base(root) + ": environment " + verdict}

	// The tail is built FIRST so the state rows can be capped around it. These
	// two lines are the ones an agent cannot infer from the state above, so they
	// outrank a fourth failing check when the budget runs out.
	tail := []string{"  `th triage -- <cmd>` says whether a failure is the environment or the code"}
	if vaulted(root) {
		// Only where it is true. An agent told to use `th run` in a repo with
		// nothing vaulted has been handed a rule with no reason, and rules with
		// no reason are the ones ignored when they matter.
		tail = append(tail, "  secrets are vaulted: .env holds `th:` references, so run commands as `th run -- <cmd>`")
	}
	// nine total, minus the tail, minus the one line "run th doctor" may add.
	room := 9 - len(tail) - 1

	for _, f := range findings {
		if len(lines) == room-2 {
			break // three services is enough to say "the env is broken"
		}
		if f.Drifted() {
			lines = append(lines, "  "+relDir(root, f.Dir)+": "+f.Summary())
		}
	}
	for _, c := range checks {
		if len(lines) == room {
			break
		}
		// skip is here too, and that is the point: a check that could not run is
		// the one an agent most needs to know it cannot lean on. Telling it only
		// about the rows that failed leaves "Postgres was never reachable" looking
		// exactly like "the database is fine".
		if c.Status != "ok" {
			line := "  " + c.Name + ": " + c.Detail
			if c.Fix != "" {
				line += " — fix: " + c.Fix
			}
			lines = append(lines, line)
		}
	}

	if verdict == "ok" {
		lines[0] += " (all clear)"
	} else {
		lines = append(lines, "  run `th doctor` for the full report, `th hydrate` to repair")
	}
	return append(lines, tail...)
}
