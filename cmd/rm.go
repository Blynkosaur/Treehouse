package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/spf13/cobra"
)

var (
	rmForce bool
	rmCmd   = &cobra.Command{
		Use:   "rm <branch>",
		Short: "Remove a worktree and its branch",
		Args:  cobra.ExactArgs(1),
		RunE:  runRm,
	}
)

func init() {
	rootCmd.AddCommand(rmCmd)
	rmCmd.Flags().BoolVar(&rmForce, "force", false, "remove even with uncommitted or unpushed work")
}

// runRm removes the worktree holding <branch>, then the branch itself.
//
// No --merged sweep: a squash-merged branch is invisible to `git branch
// --merged`, so a sweep that silently misses exactly the branches people squash
// is worse than no sweep at all.
func runRm(cmd *cobra.Command, args []string) error {
	branch := args[0]
	cwd, err := worktreeRoot()
	if err != nil {
		return err
	}
	refs, err := check.Worktrees(cwd)
	if err != nil {
		return err
	}

	var target *check.Ref
	for i := range refs {
		if refs[i].Branch == branch {
			target = &refs[i]
		}
	}
	if target == nil {
		return fmt.Errorf("no worktree has branch %q checked out", branch)
	}
	// Not overridable by --force: removing the directory you are standing in
	// leaves the shell in a path that no longer exists.
	if within(cwd, target.Path) {
		return fmt.Errorf("you are inside %s — cd out of it first", target.Path)
	}
	if target.Path == refs[0].Path {
		return fmt.Errorf("%s is the main worktree, not a removable one", target.Path)
	}

	if !rmForce {
		if out, _ := gitOut(target.Path, "status", "--porcelain"); strings.TrimSpace(out) != "" {
			return fmt.Errorf("%s has uncommitted changes — commit them or pass --force", branch)
		}
		if n := unpushed(target.Path, branch); n > 0 {
			return fmt.Errorf("%s has %d commit(s) on no remote — push them or pass --force", branch, n)
		}
	}

	remove := []string{"worktree", "remove", target.Path}
	if rmForce {
		remove = []string{"worktree", "remove", "--force", target.Path}
	}
	if out, err := gitOut(cwd, remove...); err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, out)
	}
	say("✓ removed worktree %s\n", target.Path)

	// -d refuses to drop unmerged work; that refusal is a feature, so report it
	// and leave the branch rather than escalating on the user's behalf.
	del := "-d"
	if rmForce {
		del = "-D"
	}
	if _, err := gitOut(cwd, "branch", del, branch); err != nil {
		say("• worktree gone, branch %s kept (not merged — `th rm %s --force` to drop it)\n", branch, branch)
		return nil
	}
	say("✓ deleted branch %s\n", branch)
	return nil
}

// unpushed counts commits on branch that exist on no remote. Repos with no
// remote at all are exempt: there is nowhere to push to, so nothing is at risk
// beyond the branch delete, which git itself guards.
func unpushed(path, branch string) int {
	if remotes, err := gitOut(path, "remote"); err != nil || strings.TrimSpace(remotes) == "" {
		return 0
	}
	out, err := gitOut(path, "rev-list", "--count", branch, "--not", "--remotes")
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// within reports whether child is dir or lives under it, through symlinks.
func within(child, dir string) bool {
	if samePath(child, dir) {
		return true
	}
	rel, err := filepath.Rel(resolve(dir), resolve(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
