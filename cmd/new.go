package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/spf13/cobra"
)

var (
	newFrom   string
	newPath   string
	newOpen   bool
	newNoOpen bool
	newCmd    = &cobra.Command{
		Use:   "new <branch>",
		Short: "Create a worktree for <branch>, born ready",
		Args:  cobra.ExactArgs(1),
		RunE:  runNew,
	}
)

func init() {
	rootCmd.AddCommand(newCmd)
	newCmd.Flags().StringVar(&newFrom, "from", "", "base ref for a brand-new branch (default: origin's default branch)")
	newCmd.Flags().StringVar(&newPath, "path", "", "where to put the worktree (default: sibling of the main checkout)")
	// Deliberately the same variable hydrate binds: one flag, one meaning, and
	// only one command ever runs per process.
	newCmd.Flags().BoolVar(&hydrateSkipDeps, "skip-deps", false, "skip dependency provisioning")
	newCmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing; the exit code is the answer")
	newCmd.Flags().BoolVar(&newOpen, "open", false, "run the [open] command even if doctor reports a failure")
	newCmd.Flags().BoolVar(&newNoOpen, "no-open", false, "do not run the [open] command")
	newCmd.MarkFlagsMutuallyExclusive("open", "no-open")
}

// runNew is an adapter: resolve where the worktree goes, let git make it, then
// run the same hydrate pipeline `th hydrate` runs, and finish with a doctor
// report. Every judgment it makes is about git plumbing, not about env state.
func runNew(cmd *cobra.Command, args []string) error {
	branch := args[0]
	cwd, err := worktreeRoot()
	if err != nil {
		return err
	}
	mainRoot, err := check.MainWorktree(cwd)
	if err != nil {
		return fmt.Errorf("locating main worktree: %w", err)
	}
	refs, err := check.Worktrees(cwd)
	if err != nil {
		return err
	}
	// git's own error for this is cryptic and mentions neither worktree.
	for _, ref := range refs {
		if ref.Branch == branch {
			return fmt.Errorf("branch %q is already checked out at %s", branch, ref.Path)
		}
	}

	path, err := newWorktreePath(mainRoot, branch)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", path)
	}

	// Offline and local-only repos must still work, so a failed fetch is a
	// warning; only an unresolvable base ref is fatal.
	if _, err := gitOut(cwd, "fetch", "origin"); err != nil {
		sayln("! could not fetch origin — cutting from local refs")
	}

	add, err := addArgs(cwd, branch, path)
	if err != nil {
		return err
	}
	if out, err := gitOut(cwd, add...); err != nil {
		return fmt.Errorf("git worktree add: %w\n%s", err, out)
	}
	say("✓ worktree for %s at %s\n", branch, path)

	if refs[0].Bare {
		// Nothing to copy from: a bare repo has no checkout, so no canonical
		// .env exists. Say so instead of reporting a silent no-op hydrate.
		sayln("• main worktree is bare — no canonical env to source from")
	} else if err := hydrateAll(path); err != nil {
		return err
	}

	sayln()
	findings, checks, err := diagnose(path)
	if err != nil {
		return err
	}
	if !quiet {
		printReport(os.Stdout, findings, checks, path)
	}

	cfg, _ := loadConfig(mainRoot) // a broken file is already a check in the report
	openWorktree(path, cfg.Open.Command, check.Verdict(findings, checks))
	return verdict(findings, checks)
}

// openWorktree is L1's hand-off: the worktree came up, so hand it to whatever
// the human works in. All the judgment is in check.PlanOpen.
//
// It runs in the FOREGROUND and waits, which is the right answer for both
// shapes of command and needs saying because it looks like the wrong one. A GUI
// launcher (`cursor .`, `code .`, `idea .`) forks its editor and returns
// immediately, so waiting costs nothing; a terminal agent (`claude`) needs the
// terminal, and backgrounding it would leave it fighting the returning shell
// prompt for stdin. Detaching would be wrong for one and pointless for the
// other.
//
// A command that fails is a report line, never an exit code: the worktree is
// built either way, and `th new`'s exit code is a verdict about the worktree,
// not about somebody's editor.
func openWorktree(path, command, verdict string) {
	plan := check.PlanOpen(command, verdict, newOpen, newNoOpen)
	if plan.Skip != "" {
		say("• open: skipped (%s)\n", plan.Skip)
	}
	if plan.Command == "" {
		return
	}
	say("→ open: %s\n", plan.Command)
	c := exec.Command("sh", "-c", plan.Command)
	c.Dir = path
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		say("✗ open: %s failed: %v\n", plan.Command, err)
	}
}

// newWorktreePath puts the worktree BESIDE the main checkout, never inside it.
// Inside would be a correctness bug, not a taste one: Discover and findDepDirs
// both walk from the root with no skip rule for a worktrees dir, so a nested
// worktree's .env would be indexed as a service OF main — poisoning doctor's
// fallback and making hydrate clone a child's node_modules.
func newWorktreePath(mainRoot, branch string) (string, error) {
	if newPath != "" {
		return filepath.Abs(newPath)
	}
	return filepath.Join(filepath.Dir(mainRoot), filepath.Base(mainRoot)+"-"+check.Slug(branch)), nil
}

// addArgs resolves how the branch comes into being: check out the local branch,
// track the remote one, or cut a new branch from a base. Remote tracking is
// spelled out rather than left to DWIM — worktree.guessRemote defaults false,
// so `worktree add <path> <branch>` on a remote-only branch just fails.
func addArgs(cwd, branch, path string) ([]string, error) {
	switch {
	case refExists(cwd, "refs/heads/"+branch):
		return []string{"worktree", "add", path, branch}, nil
	case refExists(cwd, "refs/remotes/origin/"+branch):
		return []string{"worktree", "add", "-b", branch, "--track", path, "origin/" + branch}, nil
	}
	base, err := resolveBase(cwd)
	if err != nil {
		return nil, err
	}
	return []string{"worktree", "add", "-b", branch, path, base}, nil
}

// resolveBase finds the freshest sane starting point for a brand-new branch:
// --from if given, else what origin calls its default branch, else HEAD for a
// repo with no origin at all.
func resolveBase(cwd string) (string, error) {
	if newFrom != "" {
		if !refExists(cwd, newFrom) {
			return "", fmt.Errorf("base ref %q does not exist", newFrom)
		}
		return newFrom, nil
	}
	if head, err := gitOut(cwd, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(head), "refs/remotes/"), nil
	}
	for _, candidate := range []string{"origin/main", "origin/master", "HEAD"} {
		if refExists(cwd, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no base ref to branch from (no origin/main, origin/master, or HEAD)")
}

func refExists(cwd, ref string) bool {
	_, err := gitOut(cwd, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// gitOut runs git in cwd and returns its combined output.
func gitOut(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	return string(out), err
}
