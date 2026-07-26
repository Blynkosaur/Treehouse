package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// addWorktree makes a linked worktree with plain git, so rm is tested without
// hydrate's writes making the tree dirty.
func addWorktree(t *testing.T, main, branch string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(main), "wt-"+branch)
	git(t, main, "worktree", "add", "-b", branch, path)
	return path
}

func TestRm(t *testing.T) {
	t.Run("clean worktree goes away with its branch", func(t *testing.T) {
		main := mainRepo(t)
		path := addWorktree(t, main, "gone")

		out, code := runCode(t, main, "rm", "gone")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("worktree still on disk: %v", err)
		}
		if branches, _ := runCode(t, main, "ls"); strings.Contains(branches, "gone") {
			t.Errorf("branch survived:\n%s", branches)
		}
	})

	t.Run("dirty needs --force", func(t *testing.T) {
		main := mainRepo(t)
		path := addWorktree(t, main, "dirty")
		write(t, filepath.Join(path, ".env"), "PORT=9999\n")

		out, code := runCode(t, main, "rm", "dirty")
		if code == 0 {
			t.Fatalf("removed a dirty worktree without --force:\n%s", out)
		}
		if !strings.Contains(out, "uncommitted") {
			t.Errorf("refusal should name the reason:\n%s", out)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("refused but removed anyway: %v", err)
		}

		if out, code := runCode(t, main, "rm", "dirty", "--force"); code != 0 {
			t.Fatalf("--force did not remove it: exit %d\n%s", code, out)
		}
	})

	t.Run("refuses the worktree you are standing in, even forced", func(t *testing.T) {
		main := mainRepo(t)
		path := addWorktree(t, main, "here")

		out, code := runCode(t, path, "rm", "here", "--force")
		if code == 0 {
			t.Fatalf("removed the current worktree:\n%s", out)
		}
		if !strings.Contains(out, "cd out of it first") {
			t.Errorf("unhelpful refusal:\n%s", out)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("current worktree was removed: %v", err)
		}
	})

	t.Run("unknown branch", func(t *testing.T) {
		main := mainRepo(t)
		out, code := runCode(t, main, "rm", "nope")
		if code != 1 {
			t.Errorf("exit %d, want 1 (treehouse-level error)\n%s", code, out)
		}
	})
}
