package cmd

import (
	"fmt"
	"os"
	"os/exec"
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
	mainRoot := refs[0].Path
	if target.Path == mainRoot {
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

	// Before the directory goes, not after: the project name lives in the .env
	// inside it, and compose has more to work with while the tree still exists.
	composeDown(target.Path, mainRoot)

	remove := []string{"worktree", "remove", target.Path}
	if rmForce {
		remove = []string{"worktree", "remove", "--force", target.Path}
	}
	if out, err := gitOut(cwd, remove...); err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, out)
	}
	say("✓ removed worktree %s\n", target.Path)

	// L3's whole promise: remove without corpses. The worktree is gone, so its
	// clone is a corpse by gc's own definition — so run gc's plan against a fleet
	// this worktree is no longer in, and act on the one row that is about this
	// branch. Same ownership rule (a provenance comment naming this repo), same
	// refusal to drop a database somebody is connected to. No clone, no comment,
	// no Postgres: nothing to say.
	dropClone(refs, target, mainRoot)

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

// composeDown stops and removes the containers of the compose project this
// worktree owned — L3's other half of "remove without corpses". E2 wrote the
// project name into the .env of every dir holding a compose file, so it is
// known rather than guessed, and check.ComposeProjects is what refuses to touch
// main's.
//
// Every failure is silent. No docker, no daemon, a project that was never
// started: none of those are reasons to fail a `th rm` whose actual job — the
// worktree and the branch — has nothing to do with containers. A stopped
// project also costs nothing but disk, unlike the database clone.
//
// `down`, never `down -v`: volumes are data, and deleting somebody's database
// volume because they removed a branch is not a cleanup, it is a loss.
func composeDown(targetPath, mainRoot string) {
	if _, err := exec.LookPath("docker"); err != nil {
		return
	}
	wt, err := check.Discover(targetPath)
	if err != nil {
		return
	}
	source, err := check.Discover(mainRoot)
	if err != nil {
		return
	}

	for _, project := range check.ComposeProjects(wt, source) {
		c := exec.Command("docker", "compose", "-p", project, "down")
		// Deliberately run where there is NO compose file. With one, compose
		// removes what that file declares; without one it works purely from the
		// project label, which catches every container the project ever started —
		// including those from a compose file that has changed since. It also
		// cannot fail on YAML we are about to delete anyway.
		c.Dir = os.TempDir()
		if err := c.Run(); err != nil {
			return // daemon down, most likely: nothing here may fail `th rm`
		}
		say("✓ tore down compose project %s\n", project)
	}
}

// dropClone removes the database clone that belonged to the worktree just
// removed — and nothing else. gone is excluded from the fleet so its clone
// becomes a candidate; every OTHER corpse in the plan is left for `th gc`,
// because `th rm feat/a` deleting feat/b's database would be a surprise.
func dropClone(fleet []check.Ref, gone *check.Ref, mainRoot string) {
	live := make([]check.Ref, 0, len(fleet))
	for _, ref := range fleet {
		if ref.Path != gone.Path {
			live = append(live, ref)
		}
	}
	drops, _, err := planGC(live, mainRoot)
	if err != nil {
		return // already reported; the worktree is gone either way
	}
	for _, d := range drops {
		if d.Branch == gone.Branch {
			collect([]check.DBDrop{d})
		}
	}
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
