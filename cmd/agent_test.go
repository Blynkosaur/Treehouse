package cmd

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestJSONNeverBlocksOnAPrompt: `th gc` is the one command that asks a question,
// and an agent that pipes it a stdin nobody ever writes to would wait forever.
// --json is the scripted face: it lists and returns, and -y is the door to
// actually dropping anything.
func TestJSONNeverBlocksOnAPrompt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, "treehouse.toml"), "[database]\npsql = \"false\"\n")

	cmd := exec.Command(thBin, "gc", "--json")
	cmd.Dir = main
	// An open pipe nobody writes to: exactly what a prompt would hang on, and
	// exactly what an agent's subprocess plumbing looks like.
	if _, err := cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("th gc --json blocked on a prompt — a scripted caller would hang forever")
	}
}

// TestJSONGoesToStdoutAlone: --json's whole contract is that stdout is machine
// input and nothing else shares it. An earlier audit could not see a leak
// because the tests read CombinedOutput; these read the two streams apart.
func TestJSONGoesToStdoutAlone(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	// Enough going on to make every face have something to say: env drift with a
	// curated failure, a database the repo names, and a psql that cannot answer.
	write(t, filepath.Join(dir, ".env.example"), "KEY=\nDATABASE_URL=\n")
	write(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgres://localhost/appdb\n")
	write(t, filepath.Join(dir, "treehouse.toml"),
		"[env]\nrequired = [\"KEY\"]\n[database]\npsql = \"false\"\n")

	for _, face := range [][]string{
		{"doctor", "--json"},
		{"doctor", "--db", "--json"},
		{"ls", "--json"},
		{"gc", "--json"},
	} {
		t.Run(strings.Join(face, " "), func(t *testing.T) {
			out, errOut, _ := runSplit(t, dir, face...)
			if errOut != "" {
				t.Errorf("stderr was not empty:\n%s", errOut)
			}
			var any any
			if err := json.Unmarshal([]byte(out), &any); err != nil {
				t.Errorf("stdout is not one clean JSON value (%v):\n%s", err, out)
			}
		})
	}

	// The two hook modes write the output protocol, which Claude Code parses off
	// stdout — a stray line there corrupts a session.
	t.Run("triage --stdin", func(t *testing.T) {
		out, _, _ := runTri(t, dir, "KeyError: 'KEY'\n", "triage", "--stdin")
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("stdout is not clean JSON (%v):\n%s", err, out)
		}
	})

	t.Run("hook session", func(t *testing.T) {
		out, errOut, code := runTri(t, dir, "", "hook", "session")
		if code != 0 {
			t.Errorf("exit %d — a session hook must never fail a session", code)
		}
		if errOut != "" {
			t.Errorf("stderr was not empty:\n%s", errOut)
		}
		var v struct {
			HookSpecificOutput struct {
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("stdout is not the output protocol (%v):\n%s", err, out)
		}
		ctx := v.HookSpecificOutput.AdditionalContext
		// It is prepended to a context window, so it stays short — and it must not
		// imply everything is fine when a check could not run.
		if n := len(strings.Split(ctx, "\n")); n > 9 {
			t.Errorf("session context is %d lines:\n%s", n, ctx)
		}
		if strings.Contains(ctx, "all clear") {
			t.Errorf("claimed all clear over a failing env and an unreachable cluster:\n%s", ctx)
		}
		if !strings.Contains(ctx, "db:") {
			t.Errorf("the agent was never told the database could not be checked:\n%s", ctx)
		}
	})
}

// TestExitCodesAreUniform pins the contract an agent gates on without parsing
// anything: 0 healthy or warn-only, 1 treehouse itself failed, 2 FAIL findings.
func TestExitCodesAreUniform(t *testing.T) {
	failing := driftedRepo(t)
	write(t, filepath.Join(failing, "treehouse.toml"), "[env]\nrequired = [\"KEY\"]\n")
	warning := driftedRepo(t) // inferred drift only
	clean := cleanRepo(t)

	cases := []struct {
		dir  string
		args []string
		want int
	}{
		{clean, []string{"doctor"}, 0},
		{clean, []string{"ls"}, 0},
		{warning, []string{"doctor"}, 0},
		{warning, []string{"ls"}, 0},
		{failing, []string{"doctor"}, 2},
		{failing, []string{"doctor", "--json"}, 2},
		{failing, []string{"doctor", "--quiet"}, 2},
		{failing, []string{"ls"}, 2},
		{failing, []string{"ls", "--json"}, 2},
		{failing, []string{"gc", "--json"}, 0}, // gc reports; it does not judge the worktree
		{failing, []string{"hook", "session"}, 0},
		{clean, []string{"nonsense-subcommand"}, 1},
		{clean, []string{"seed", "nope"}, 1},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " ")+"@"+filepath.Base(c.dir), func(t *testing.T) {
			out, errOut, code := runSplit(t, c.dir, c.args...)
			if code != c.want {
				t.Errorf("exit %d, want %d\n%s%s", code, c.want, out, errOut)
			}
		})
	}
}
