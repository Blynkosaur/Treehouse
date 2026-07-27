package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func journalFile(t *testing.T, dir string) string {
	t.Helper()
	return filepath.Join(dir, ".git", "treehouse-state.json")
}

func TestWhyWithoutAJournal(t *testing.T) {
	// The common first run, and the one that must never look like an error.
	out, code := runCode(t, driftedRepo(t), "why")
	if code != 0 {
		t.Errorf("exit %d, want 0 — no baseline is not a failure\n%s", code, out)
	}
	if !strings.Contains(out, "no baseline yet") {
		t.Errorf("want a plain no-baseline line, got:\n%s", out)
	}
}

func TestDoctorWritesTheJournalIntoGitDir(t *testing.T) {
	dir := cleanRepo(t)
	if _, code := runCode(t, dir, "doctor"); code != 0 {
		t.Fatalf("doctor exit %d", code)
	}

	data, err := os.ReadFile(journalFile(t, dir))
	if err != nil {
		t.Fatalf("journal should live in .git: %v", err)
	}
	var j struct {
		Schema  int `json:"schema"`
		Entries map[string]struct {
			Status string `json:"status"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &j); err != nil {
		t.Fatalf("journal is not valid json: %v\n%s", err, data)
	}
	if j.Schema != 1 || j.Entries["env"].Status != "ok" {
		t.Errorf("journal did not record the env row: %s", data)
	}

	// Never in the working tree: git must not see it, or it gets committed.
	if _, err := os.Stat(filepath.Join(dir, "treehouse-state.json")); err == nil {
		t.Error("the journal must not land in the working tree")
	}
	if status := gitStatus(t, dir); strings.Contains(status, "treehouse-state") {
		t.Errorf("journal shows up in git status:\n%s", status)
	}
}

func TestWhyAnswersInOneLine(t *testing.T) {
	dir := cleanRepo(t)
	if _, code := runCode(t, dir, "doctor"); code != 0 {
		t.Fatalf("doctor exit %d", code)
	}
	write(t, filepath.Join(dir, ".env"), "OTHER=2\n") // KEY goes missing

	out, code := runCode(t, dir, "why")
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if !strings.Contains(out, "KEY missing") || !strings.Contains(out, "went from ok to warn") {
		t.Errorf("want the key named in one line, got:\n%s", out)
	}
	if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 0 {
		t.Errorf("one change is one line, got %d extra:\n%s", lines, out)
	}
}

// A journal nobody can read is a journal we do not have — and that must cost
// exactly one sentence, never a failing doctor.
func TestCorruptJournalBreaksNothing(t *testing.T) {
	for _, junk := range []string{"", "{", `{"schema":99,"entries":{"env":{"status":"ok"}}}`, "not json at all"} {
		dir := driftedRepo(t)
		write(t, journalFile(t, dir), junk)

		out, code := runCode(t, dir, "why")
		if code != 0 || !strings.Contains(out, "no baseline yet") {
			t.Errorf("why on %q: exit %d\n%s", junk, code, out)
		}

		out, code = runCode(t, dir, "doctor")
		if code != 0 || !strings.Contains(out, "expected keys missing") {
			t.Errorf("doctor on %q: exit %d\n%s", junk, code, out)
		}
		// And the run repaired it: doctor rewrote the file it could not read.
		if out, code := runCode(t, dir, "why"); code != 0 || strings.Contains(out, "no baseline") {
			t.Errorf("doctor should have replaced the journal: exit %d\n%s", code, out)
		}
	}
}

func TestWhyJSON(t *testing.T) {
	dir := driftedRepo(t)
	out, code := runCode(t, dir, "why", "--json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var envelope struct {
		Schema   int      `json:"schema"`
		Status   string   `json:"status"`
		Answer   string   `json:"answer"`
		Changes  []string `json:"changes"`
		Baseline bool     `json:"baseline"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("not json: %v\n%s", err, out)
	}
	if envelope.Baseline || envelope.Changes == nil || envelope.Status != "warn" {
		t.Errorf("first run should be baseline:false, changes:[], status:warn — got %+v", envelope)
	}
}

func gitStatus(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	return string(out)
}
