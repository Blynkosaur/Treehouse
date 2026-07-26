package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:     "th",
	Short:   "Treehouse a Git Worktree manager to make your life easier",
	Aliases: []string{"th"},
	// A runtime failure is not a usage failure. Without this every error dumps
	// the full help text into an agent's stdout, burying the actual message.
	SilenceUsage: true,
	// Execute prints errors itself, so an exitCode never surfaces as text.
	SilenceErrors: true,
}

// exitCode is a verdict about the WORKTREE, returned as an error so commands
// stay pure adapters that return instead of calling os.Exit. Execute is the
// only place that exits.
//
// 0 = healthy or warnings only, 2 = curated checks failed. 1 stays reserved for
// treehouse itself breaking (usage, git, IO) — an agent has to be able to tell
// "your env is broken" from "the tool crashed".
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit status %d", int(c)) }

func Execute() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	var code exitCode
	if errors.As(err, &code) {
		os.Exit(int(code))
	}
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}

// say is the one gate every human-facing line passes through: --quiet asks for
// silence, --json asks for machine output only. Not a third renderer.
func say(format string, args ...any) {
	if !quiet && !jsonOut {
		fmt.Printf(format, args...)
	}
}

func sayln(args ...any) {
	if !quiet && !jsonOut {
		fmt.Println(args...)
	}
}
