package cmd

import (
	"encoding/json"
	"os"
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

// TestLsSaysSkipWhenPostgresIsUnreachable is a regression, and it is the same
// bug this pass set out to remove, hiding one layer up.
//
// `th doctor` answers an unreachable cluster with a `skip` row and a `skip`
// verdict. `th ls` folded "asked and got nothing" into the same nil that means
// "this repo declares no database", so the row came back ok, the fleet came back
// ok, and the two commands published contradicting `status` fields under the
// same schema number — the exact disagreement item 1 exists to make impossible.
func TestLsSaysSkipWhenPostgresIsUnreachable(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	// A database this repo genuinely declares, and a psql that can never answer
	// about it. Nothing else is wrong, so the verdict is about the cluster alone.
	write(t, filepath.Join(dir, ".env.example"), "DATABASE_URL=\n")
	write(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgres://localhost/appdb\n")
	write(t, filepath.Join(dir, "treehouse.toml"), "[database]\npsql = \"false\"\n")

	lsOut, _, lsCode := runSplit(t, dir, "ls", "--json")
	docOut, _, docCode := runSplit(t, dir, "doctor", "--json")

	var fleet fleetEnvelope
	if err := json.Unmarshal([]byte(lsOut), &fleet); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, lsOut)
	}
	var doc struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(docOut), &doc); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, docOut)
	}

	if doc.Status != "skip" {
		t.Fatalf("doctor status = %q, want skip — the premise of this test", doc.Status)
	}
	if fleet.Status != doc.Status {
		t.Errorf("ls says %q and doctor says %q about the same unreachable cluster", fleet.Status, doc.Status)
	}
	// skip is not a failure, so neither command may exit 2 — it is the WORD that
	// has to be honest, not the code.
	if lsCode != 0 || docCode != 0 {
		t.Errorf("ls exit %d, doctor exit %d — a cluster nobody could reach is not a FAIL", lsCode, docCode)
	}

	// And the human face has to say it out loud, or the blank db column reads as
	// "this repo has no database" to whoever is looking at the table.
	human, _, _ := runSplit(t, dir, "ls")
	if !strings.Contains(human, "postgres is not reachable") {
		t.Errorf("the table never says why the db column is blank:\n%s", human)
	}
}

// TestFleetCellsCoverTheWholeVocabulary pins the table against the one failure
// its renderer can have: a database state the switch does not know falls through
// to the dash, and the dash means "this repo declares no database at all". A new
// DBWord landing there would render a broken worktree as one that never had a
// database — silently, in the view people use to pick which worktree to hand an
// agent.
func TestFleetCellsCoverTheWholeVocabulary(t *testing.T) {
	// Every word check.DBWord can return. "" is the only one that may be a dash.
	for _, word := range []string{"main", "ok", "missing", "shared", "adrift", "unusable", "skip"} {
		if got := dbCell(word); !strings.Contains(got, word) {
			t.Errorf("dbCell(%q) = %q — the state is not on screen", word, got)
		}
	}
	if got := dbCell(""); strings.TrimSpace(got) != "—" {
		t.Errorf("dbCell(\"\") = %q, want the dash that means nobody was asked", got)
	}
	for _, word := range []string{"fail", "warn", "ok"} {
		if got := envCell(word); !strings.Contains(got, word) {
			t.Errorf("envCell(%q) = %q", word, got)
		}
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

// TestPath is L4's command half: `cd "$(th path <branch>)"` has to be safe to
// paste into a shell, which means exactly one line on stdout and a non-zero
// exit — never a path — when the branch has no worktree.
func TestPath(t *testing.T) {
	main := mainRepo(t)
	if out, ok := runTh(t, main, "new", "jump", "--skip-deps"); !ok {
		t.Fatalf("%s", out)
	}

	out, errOut, code := runSplit(t, main, "path", "jump")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// Resolved on both sides: git reports /private/var where the test's TempDir
	// says /var, and that difference is the OS's, not the command's.
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(out))
	want, _ := filepath.EvalSymlinks(filepath.Join(filepath.Dir(main), "app-jump"))
	if got != want || want == "" {
		t.Errorf("path = %q, want %q", got, want)
	}
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("stdout must be the path alone, got:\n%s", out)
	}

	// The failure that matters: a `cd` to a printed nothing lands in $HOME.
	out, _, code = runSplit(t, main, "path", "nope")
	if code == 0 {
		t.Errorf("an unknown branch must not exit 0")
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("an unknown branch printed %q on stdout", out)
	}
}

// TestSeedRefusesTheSharedDatabase covers the loudest version of the state this
// pass is about. `th ls` and `th doctor` REPORT a worktree still pointed at the
// template; `th seed` would LOAD A DATASET INTO IT — into every other worktree
// at once, unrecoverably, by running the project's own tooling against whatever
// .env names. The guard has to fire before the command runs, not after.
//
// No cluster required, and that is the point: the refusal is a decision about
// two strings, and it must not depend on Postgres being reachable to happen.
func TestSeedRefusesTheSharedDatabase(t *testing.T) {
	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, ".env.example"), "PORT=\nDATABASE_URL=\n")
	write(t, filepath.Join(main, ".env"), "PORT=3000\nDATABASE_URL=postgres:///appdb\n")
	// The command writes a file, so a guard that fired too late leaves evidence.
	marker := filepath.Join(t.TempDir(), "seeded")
	write(t, filepath.Join(main, "treehouse.toml"),
		"[database]\npsql = \"false\"\n\n[[seed]]\nname = \"ramp\"\ncommand = \"touch "+marker+"\"\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-m", "seed config")

	if out, ok := runTh(t, main, "new", "feat", "--skip-deps"); !ok {
		t.Fatalf("th new: %s", out)
	}
	linked := filepath.Join(filepath.Dir(main), "app-feat")
	t.Cleanup(func() { runTh(t, main, "rm", "feat", "--force") })

	// Premise: with no cluster there is no clone, so .env legitimately still
	// names the template — exactly the half-applied state.
	if vars := parseEnv(readEnv(t, filepath.Join(linked, ".env"))); vars["DATABASE_URL"] != "postgres:///appdb" {
		t.Fatalf("DATABASE_URL = %q — this test needs the worktree on the template", vars["DATABASE_URL"])
	}

	out, errOut, code := runSplit(t, linked, "seed", "ramp")
	if code != 1 {
		t.Errorf("exit %d, want 1\n%s%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "shared database") || !strings.Contains(errOut, "th hydrate") {
		t.Errorf("the refusal does not name the state or the fix:\n%s", errOut)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the seed command RAN against the shared database — the guard fired too late")
	}

	// Main itself is allowed to seed the template: that is the one checkout the
	// template legitimately belongs to, and refusing there would make the whole
	// feature unusable.
	if _, _, code := runSplit(t, main, "seed", "ramp"); code != 0 {
		t.Errorf("main cannot seed its own database: exit %d", code)
	}
}

// TestAnUnusableTemplateIsLoud is the other half of quoting-instead-of-refusing.
// Quote rescues every name Postgres will hold; the ones it cannot are now a FAIL
// check with a fix line rather than a skip. A repo that wants clones and will
// never get one has to hear about it — a quiet skip line is how that goes
// unnoticed for a month.
//
// It needs a reachable cluster because the unreachable path answers `skip`
// first, and skip is not the answer being tested here.
func TestAnUnusableTemplateIsLoud(t *testing.T) {
	skipWithoutCluster(t)
	main := gitignoredEnvRepo(t)
	// 64 bytes: one past what Postgres will hold, and it truncates silently, so
	// two branches would quietly share one database.
	long := strings.Repeat("d", 64)
	write(t, filepath.Join(main, ".env.example"), "PORT=\nDATABASE_URL=\n")
	write(t, filepath.Join(main, ".env"), "PORT=3000\nDATABASE_URL=postgres:///"+long+"\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-m", "an impossible template")

	// Both faces have to FAIL on it. Only doctor has room to explain — `th ls`
	// is a one-word-per-cell glance view, so its contract is the word and the
	// exit code, and the explanation lives one command away.
	for _, args := range [][]string{{"doctor"}, {"ls"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, _, code := runSplit(t, main, args...)
			if code != 2 {
				t.Errorf("exit %d, want 2 — this repo will never get a clone\n%s", code, out)
			}
		})
	}
	t.Run("doctor says why and what to do", func(t *testing.T) {
		out, _, _ := runSplit(t, main, "doctor")
		if !strings.Contains(out, "63") || !strings.Contains(out, "fix:") {
			t.Errorf("a repo that will never get a clone is told neither why nor how:\n%s", out)
		}
	})

	out, _, code := runSplit(t, main, "ls", "--json")
	var fleet fleetEnvelope
	if err := json.Unmarshal([]byte(out), &fleet); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	if fleet.Status != "fail" || code != 2 {
		t.Errorf("fleet = %q / exit %d, want fail / 2", fleet.Status, code)
	}
	for _, w := range fleet.Worktrees {
		// "unusable", never "main": the name is a fact about the REPO, and it is
		// just as true standing in the main checkout.
		if w.DB != "unusable" {
			t.Errorf("db column for %q = %q, want unusable", w.Branch, w.DB)
		}
	}
}
