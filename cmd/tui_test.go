package cmd

import (
	"strings"
	"testing"
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
