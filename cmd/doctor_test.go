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
	if envelope.Schema != 2 || envelope.Status != "fail" {
		t.Errorf("envelope = %+v, want schema 2 / status fail", envelope)
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

func TestLs(t *testing.T) {
	main := mainRepo(t)
	if out, ok := runTh(t, main, "new", "feat", "--skip-deps"); !ok {
		t.Fatalf("%s", out)
	}
	linked := filepath.Join(filepath.Dir(main), "app-feat")

	out, code := runCode(t, linked, "ls")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{"app", "app-feat", "feat", "WORKTREE", "BRANCH", "ENV", "DB"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}

	raw, code := runCode(t, linked, "ls", "--json")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, raw)
	}
	var envelope struct {
		Schema    int    `json:"schema"`
		Status    string `json:"status"`
		Worktrees []struct {
			Branch  string `json:"branch"`
			Env     string `json:"env"`
			Current bool   `json:"current"`
		} `json:"worktrees"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("ls --json is not clean JSON (%v):\n%s", err, raw)
	}
	if envelope.Schema != 2 || len(envelope.Worktrees) != 2 {
		t.Fatalf("envelope = %+v, want schema 2 and 2 worktrees", envelope)
	}
	current := 0
	for _, w := range envelope.Worktrees {
		if w.Current {
			current++
			if w.Branch != "feat" {
				t.Errorf("current row is %q, want the worktree we ran in", w.Branch)
			}
		}
	}
	if current != 1 {
		t.Errorf("%d rows marked current, want exactly 1", current)
	}
}

// TestEveryFaceCarriesTheChecks is the phase-3/phase-5 seam. Phase 3 added a
// second list beside findings (the db/migrations/seed checks) while phase 5 was
// rewriting the same two printers to take an io.Writer, in a different
// worktree. A merge that took one side's function body would drop a whole
// column of the report from one face and nothing would fail.
//
// [database] psql = "false" is the cluster-free way to get a check row: the
// repo names a database, so doctor asks, and the ask fails.
func TestEveryFaceCarriesTheChecks(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".env.example"), "DATABASE_URL=\n")
	write(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgres://localhost/appdb\n")
	write(t, filepath.Join(dir, "treehouse.toml"), "[database]\npsql = \"false\"\n")

	for _, face := range [][]string{{"doctor"}, {"doctor", "--ls"}, {"doctor", "--db"}} {
		t.Run(strings.Join(face, " "), func(t *testing.T) {
			out, code := runCode(t, dir, face...)
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if !strings.Contains(out, "db:") {
				t.Errorf("the db check never reached this face:\n%s", out)
			}
		})
	}

	t.Run("json", func(t *testing.T) {
		out, _ := runCode(t, dir, "doctor", "--json")
		var envelope struct {
			Checks []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"checks"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("not JSON (%v):\n%s", err, out)
		}
		if len(envelope.Checks) != 1 || envelope.Checks[0].Name != "db" ||
			envelope.Checks[0].Status != "skip" {
			t.Errorf("checks = %+v, want one skipped db row", envelope.Checks)
		}
	})

	// The drill-in is the fourth face, and it exists only because printReport
	// takes a writer. If it ever stops calling the same function, this is what
	// notices.
	t.Run("the TUI drill-in", func(t *testing.T) {
		msg, ok := detailCmd(0, dir)().(detailMsg)
		if !ok {
			t.Fatalf("detailCmd returned %T", detailCmd(0, dir)())
		}
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		for _, want := range []string{"db:", "expected keys"} {
			if !strings.Contains(msg.report, want) {
				t.Errorf("the drill-in is missing %q from doctor's report:\n%s", want, msg.report)
			}
		}
	})
}

// TestDoctorDBSaysSkipRatherThanNothing is ITEM 2. `th doctor --db` dropped the
// migrations and seed rows entirely when Postgres could not be reached — the
// user asked for exactly those two things by name, and got silence. Silence is
// indistinguishable from "fine" to whoever reads the report next, and it is the
// same failure `gc --json` was fixed for.
//
// [database] psql = "false" is the cluster-free way to make the ask fail.
func TestDoctorDBSaysSkipRatherThanNothing(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".env.example"), "DATABASE_URL=\n")
	write(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgres://localhost/appdb\n")
	write(t, filepath.Join(dir, "treehouse.toml"),
		"[database]\npsql = \"false\"\n[migrations]\nstatus = \"true\"\n")

	out, stderr, code := runSplit(t, dir, "doctor", "--db", "--json")
	if code != 0 {
		t.Fatalf("exit %d — an unreachable cluster is a skip, not a failure\n%s%s", code, out, stderr)
	}
	var envelope struct {
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("not clean JSON on stdout (%v):\n%s", err, out)
	}
	// The envelope must not claim green over three checks nobody could run.
	if envelope.Status != "skip" {
		t.Errorf("status = %q, want skip — nothing was verified", envelope.Status)
	}
	byName := map[string]string{}
	for _, c := range envelope.Checks {
		byName[c.Name] = c.Status
		if c.Status == "skip" && c.Detail == "" {
			t.Errorf("%s skipped without saying why", c.Name)
		}
	}
	for _, name := range []string{"db", "migrations", "seed"} {
		if byName[name] != "skip" {
			t.Errorf("checks[%s] = %q, want skip — --db asked for this row", name, byName[name])
		}
	}

	// And the human face must not print "all clear" over the same three rows.
	human, _, _ := runSplit(t, dir, "doctor", "--db")
	if strings.Contains(human, "all clear") {
		t.Errorf("the report claims all clear over three unrun checks:\n%s", human)
	}
	for _, want := range []string{"db:", "migrations:", "seed:", "could not be run"} {
		if !strings.Contains(human, want) {
			t.Errorf("the human report is missing %q:\n%s", want, human)
		}
	}
}
