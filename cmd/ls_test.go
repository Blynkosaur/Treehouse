package cmd

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fleetEnvelope is `th ls --json`. It carries the same `status` doctor's does,
// which is the whole point of the field.
type fleetEnvelope struct {
	Schema    int    `json:"schema"`
	Status    string `json:"status"`
	Worktrees []struct {
		Branch string `json:"branch"`
		Status string `json:"status"`
		Env    string `json:"env"`
		DB     string `json:"db"`
	} `json:"worktrees"`
}

// TestLsAndDoctorCannotDisagree is ITEM 1. Two commands published the same
// `status` field under the same schema number and meant different things by it:
// ls folded the env column, doctor folded check.Verdict. An agent gating on one
// and acting on the other is exactly the confidently-wrong decision the whole
// tool exists to prevent.
func TestLsAndDoctorCannotDisagree(t *testing.T) {
	dir := driftedRepo(t)
	write(t, filepath.Join(dir, "treehouse.toml"), "[env]\nrequired = [\"KEY\"]\n")

	lsOut, lsErr, lsCode := runSplit(t, dir, "ls", "--json")
	docOut, _, docCode := runSplit(t, dir, "doctor", "--json")

	var fleet fleetEnvelope
	if err := json.Unmarshal([]byte(lsOut), &fleet); err != nil {
		t.Fatalf("ls --json is not clean JSON on stdout (%v):\n%s", err, lsOut)
	}
	if lsErr != "" {
		t.Errorf("ls --json leaked to stderr: %s", lsErr)
	}
	var doc struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(docOut), &doc); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, docOut)
	}

	if fleet.Status != doc.Status {
		t.Errorf("ls says %q and doctor says %q about the same worktree", fleet.Status, doc.Status)
	}
	if fleet.Status != "fail" {
		t.Errorf("fleet status = %q, want fail", fleet.Status)
	}
	// The exit code is what an agent gates on without parsing anything.
	if lsCode != 2 || docCode != 2 {
		t.Errorf("ls exit %d, doctor exit %d — both must be 2 for a FAIL fleet\n%s", lsCode, docCode, lsOut)
	}
}

// TestLsSeesTheSharedDatabase is the state this whole item is about: the clone
// exists, `.env` still names the SHARED database, and a migration run here lands
// on every other worktree. The fleet table used to render that `db: ok`, in
// green.
//
// It needs a real cluster — clone-exists is not something we can fake into
// pg_database — and skips cleanly without one, the same bargain internal/pg
// makes.
func TestLsSeesTheSharedDatabase(t *testing.T) {
	if err := exec.Command("pg_isready").Run(); err != nil {
		t.Skip("no postgres server responding")
	}
	const template = "th_ls_selftest"
	psql := func(sql string) {
		t.Helper()
		c := exec.Command("psql", "-d", "postgres", "-At", "--no-psqlrc", "-v", "ON_ERROR_STOP=1")
		c.Stdin = strings.NewReader(sql)
		if out, err := c.CombinedOutput(); err != nil {
			t.Skipf("cannot prepare a template database (%v): %s", err, out)
		}
	}
	psql(`DROP DATABASE IF EXISTS "` + template + `"`)
	psql(`CREATE DATABASE "` + template + `"`)

	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, ".env.example"), "PORT=\nADMIN_PORT=\nDATABASE_URL=\n")
	write(t, filepath.Join(main, ".env"),
		"PORT=3000\nADMIN_PORT=3001\nDATABASE_URL=postgres:///"+template+"\n")
	// Committed, because the new worktree gets .env.example from git — an
	// uncommitted reference would leave the fresh worktree drifted for a reason
	// that has nothing to do with what this test is about.
	git(t, main, "add", "-A")
	git(t, main, "commit", "-m", "database")

	if out, ok := runTh(t, main, "new", "feat", "--skip-deps"); !ok {
		t.Fatalf("th new: %s", out)
	}
	linked := filepath.Join(filepath.Dir(main), "app-feat")
	clone := "th_ls_selfte_wt_feat"
	t.Cleanup(func() {
		runTh(t, main, "rm", "feat", "--force")
		psql(`DROP DATABASE IF EXISTS "` + clone + `"`)
		psql(`DROP DATABASE IF EXISTS "` + template + `"`)
	})

	// Sanity: hydrate made the clone and pointed .env at it.
	out, _, code := runSplit(t, linked, "ls", "--json")
	var fleet fleetEnvelope
	if err := json.Unmarshal([]byte(out), &fleet); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	if code != 0 || fleet.Status != "ok" {
		t.Fatalf("a freshly hydrated fleet is exit %d / %q, want 0 / ok:\n%s", code, fleet.Status, out)
	}
	for _, w := range fleet.Worktrees {
		want := "ok"
		if w.Branch == "main" || w.Branch == "master" {
			want = "main"
		}
		if w.DB != want {
			t.Fatalf("db column for %q = %q, want %q:\n%s", w.Branch, w.DB, want, out)
		}
	}

	// Now the half-applied hydrate: the clone is still there, .env goes back to
	// naming the template. This is the failure nothing inside the app can see.
	write(t, filepath.Join(linked, ".env"),
		"PORT=3200\nADMIN_PORT=3201\nDATABASE_URL=postgres:///"+template+"\n")

	out, _, code = runSplit(t, linked, "ls", "--json")
	if err := json.Unmarshal([]byte(out), &fleet); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	if fleet.Status != "fail" || code != 2 {
		t.Errorf("fleet is exit %d / %q, want 2 / fail — doctor calls this state a failure\n%s", code, fleet.Status, out)
	}
	found := false
	for _, w := range fleet.Worktrees {
		if w.Branch == "feat" {
			found = true
			if w.DB != "shared" || w.Status != "fail" {
				t.Errorf("the shared row is db=%q status=%q, want shared/fail", w.DB, w.Status)
			}
		}
	}
	if !found {
		t.Fatalf("no row for the worktree we broke:\n%s", out)
	}

	// And doctor, standing in the same worktree, must not disagree.
	_, _, docCode := runSplit(t, linked, "doctor", "--json")
	if docCode != 2 {
		t.Errorf("doctor exit %d while ls said fail", docCode)
	}
}
