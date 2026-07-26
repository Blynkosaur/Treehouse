package cmd

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/check"
	tea "github.com/charmbracelet/bubbletea"
)

// The TUI is the only thing in treehouse that cares who is watching, so the
// non-TTY path is the one that has to be pinned down. exec.Command's
// CombinedOutput gives the child a pipe, not a terminal — exactly what a hook,
// a CI job or an agent hands it.
//
// This is not a style preference: bubbletea cannot open a program without a
// controlling terminal, so a regression here turns `th` from "prints help" into
// "exits 1" for every non-human caller at once.
func TestBareCommandOnAPipePrintsHelp(t *testing.T) {
	out, code := runCode(t, t.TempDir())
	if code != 0 {
		t.Fatalf("bare th on a pipe exited %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"Usage:", "doctor", "hydrate", "tui"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q:\n%s", want, out)
		}
	}
}

// `th tui` is the explicit door, and on a pipe it must fail as a command rather
// than hang waiting for input that will never come.
func TestTUIOnAPipeFailsLoudly(t *testing.T) {
	out, code := runCode(t, t.TempDir(), "tui")
	if code == 0 {
		t.Fatalf("th tui on a pipe exited 0, want a failure\n%s", out)
	}
}

// TestBoardCommandsShareOneCluster is a race regression, and it is reachable by
// hand: `enter` drills in (diagnose), `r` refreshes the fleet (fleet), and
// bubbletea runs every Cmd in its own goroutine — so pressing one then the
// other before the first lands put two writers on internal/pg's psql prefix at
// once. Driving the model directly is how a TUI gets tested without a pty: this
// is exactly what the runtime does with the Cmds Update returns.
func TestBoardCommandsShareOneCluster(t *testing.T) {
	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, "treehouse.toml"), "[database]\npsql = \"psql\"\n")

	refs, err := check.Worktrees(main)
	if err != nil {
		t.Skipf("git worktree list: %v", err)
	}
	m, _ := newBoard(main).Update(fleetMsg{refs: refs})
	m, detail := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
	_, refresh := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))
	if detail == nil || refresh == nil {
		t.Fatal("enter and r must both issue a command")
	}

	// Repeated because a race needs the two goroutines to actually overlap, and
	// one round of subprocess timing rarely delivers that.
	for i := 0; i < 20; i++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, cmd := range []tea.Cmd{detail, refresh} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				cmd()
			}()
		}
		close(start)
		wg.Wait()
	}
}
