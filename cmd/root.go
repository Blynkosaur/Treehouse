package cmd

import (
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
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
