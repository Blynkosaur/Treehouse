package cmd

import (
	"fmt"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/spf13/cobra"
)

var pathCmd = &cobra.Command{
	Use:   "path <branch>",
	Short: "Print the worktree path for <branch>",
	Long: `Resolves a branch to the directory its worktree lives in.

A binary cannot change its parent shell's directory, so there is no ` + "`th cd`" + `.
This is the half a binary CAN do; the README carries the three-line shell
function that wraps it.`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeBranches,
	RunE:              runPath,
}

func init() {
	rootCmd.AddCommand(pathCmd)
}

// runPath prints the path and nothing else. Not through say(): the output IS
// the answer here, it is being read by `cd "$(th path x)"`, and a decorated
// line or a suppressed one would both break the caller. An unknown branch is an
// error on stderr and a non-zero exit, so the `cd` never happens.
func runPath(cmd *cobra.Command, args []string) error {
	cwd, err := worktreeRoot()
	if err != nil {
		return err
	}
	refs, err := check.Worktrees(cwd)
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.Branch == args[0] {
			fmt.Println(ref.Path)
			return nil
		}
	}
	return fmt.Errorf("no worktree has branch %q checked out", args[0])
}

// completeBranches makes `th path <TAB>` offer the branches that actually have
// a worktree — the completion half of L4, free from cobra's own machinery.
func completeBranches(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
	cwd, err := worktreeRoot()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	refs, err := check.Worktrees(cwd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, ref := range refs {
		if ref.Branch != "" {
			out = append(out, ref.Branch)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
