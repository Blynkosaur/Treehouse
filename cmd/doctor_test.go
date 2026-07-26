package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCode runs the binary and returns output plus the exit code, because the
// exit code IS doctor's answer for agents.
func runCode(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	cmd := exec.Command(thBin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("running th: %v", err)
	}
	return string(out), code
}

// driftedRepo is a git repo whose .env is missing KEY that .env.example expects.
func driftedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".env.example"), "KEY=\nOTHER=\n")
	write(t, filepath.Join(dir, ".env"), "OTHER=1\n")
	return dir
}

func TestDoctorExitCodes(t *testing.T) {
	t.Run("inferred drift stays exit 0", func(t *testing.T) {
		out, code := runCode(t, driftedRepo(t), "doctor")
		if code != 0 {
			t.Errorf("exit %d, want 0 — inferred requirements are warnings\n%s", code, out)
		}
		if !strings.Contains(out, "KEY") {
			t.Errorf("drift not reported:\n%s", out)
		}
	})

	t.Run("required key fails with exit 2", func(t *testing.T) {
		dir := driftedRepo(t)
		write(t, filepath.Join(dir, "treehouse.toml"), "[env]\nrequired = [\"KEY\"]\n")

		out, code := runCode(t, dir, "doctor")
		if code != 2 {
			t.Errorf("exit %d, want 2 — 1 is reserved for treehouse itself failing\n%s", code, out)
		}
		if !strings.Contains(out, "required by treehouse.toml") {
			t.Errorf("failure not attributed to the curated list:\n%s", out)
		}
	})

	t.Run("treehouse's own failure is exit 1", func(t *testing.T) {
		// Not a git repo and no env files anywhere is fine (exit 0); a bad
		// argument is treehouse failing, and must not look like a fail verdict.
		out, code := runCode(t, t.TempDir(), "new")
		if code != 1 {
			t.Errorf("exit %d, want 1 for a usage error\n%s", code, out)
		}
	})
}

func TestDoctorJSON(t *testing.T) {
	dir := driftedRepo(t)
	write(t, filepath.Join(dir, "treehouse.toml"), "[env]\nrequired = [\"KEY\"]\n")

	out, code := runCode(t, dir, "doctor", "--json")
	if code != 2 {
		t.Errorf("exit %d, want 2", code)
	}

	var envelope struct {
		Schema   int    `json:"schema"`
		Root     string `json:"root"`
		Status   string `json:"status"`
		Findings []struct {
			Dir     string   `json:"dir"`
			Missing []string `json:"missing"`
			Failed  []string `json:"failed"`
		} `json:"findings"`
	}
	// The whole point of --json: nothing but JSON on stdout, ever.
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("output is not clean JSON (%v):\n%s", err, out)
	}
	if envelope.Schema != 1 || envelope.Status != "fail" {
		t.Errorf("envelope = %+v, want schema 1 / status fail", envelope)
	}
	if len(envelope.Findings) != 1 || len(envelope.Findings[0].Failed) != 1 {
		t.Errorf("findings = %+v, want one finding failing on KEY", envelope.Findings)
	}
}

func TestDoctorQuiet(t *testing.T) {
	out, code := runCode(t, driftedRepo(t), "doctor", "--quiet")
	if out != "" {
		t.Errorf("--quiet printed:\n%s", out)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

func TestHydrateQuiet(t *testing.T) {
	main := mainRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, main, "worktree", "add", linked)
	os.Remove(filepath.Join(linked, ".env"))

	out, code := runCode(t, linked, "hydrate", "--skip-deps", "--quiet")
	if out != "" || code != 0 {
		t.Errorf("hydrate --quiet: exit %d, output:\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(linked, ".env")); err != nil {
		t.Errorf("quiet hydrate did no work: %v", err)
	}
}
