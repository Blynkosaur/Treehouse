package cmd

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGCWithoutAReachableCluster: `th gc` is the one command that deletes data,
// so the one thing it must never do is answer a question nobody asked. An empty
// drop list because psql could not connect is NOT "the cluster is clean", and a
// CI job reading status:ok off that concludes there are no orphans when nothing
// was ever looked at.
//
// [database] psql points at a command that always fails, which is the whole
// cluster-free way to reach this path.
func TestGCWithoutAReachableCluster(t *testing.T) {
	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, ".env"), "PORT=3000\nDATABASE_URL=postgres://localhost/appdb\n")
	write(t, filepath.Join(main, "treehouse.toml"), "[database]\npsql = \"false\"\n")

	t.Run("json says skip, not ok", func(t *testing.T) {
		out, _, code := runSplit(t, main, "gc", "--json")
		if code != 0 {
			t.Fatalf("exit %d — an unreachable cluster is a report line, not a failure\n%s", code, out)
		}
		var got struct {
			Status string `json:"status"`
			Drops  []any  `json:"drops"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("gc --json is not JSON: %v\n%s", err, out)
		}
		if got.Status != "skip" {
			t.Errorf("status = %q, want skip — nobody looked, so nothing was found is not the answer", got.Status)
		}
		if len(got.Drops) != 0 {
			t.Errorf("drops = %v, want empty", got.Drops)
		}
	})

	t.Run("the human face does not contradict itself", func(t *testing.T) {
		out, _, _ := runSplit(t, main, "gc")
		if !strings.Contains(out, "not reachable") {
			t.Errorf("the skip was never explained:\n%s", out)
		}
		if strings.Contains(out, "nothing to collect") {
			t.Errorf("claimed every clone still has a worktree right after saying it could not ask:\n%s", out)
		}
	})
}

// TestKeepReason is ITEM 6. collect used to read `err == nil && len(sessions) > 0`,
// which turned the live-connection guard OFF exactly when the cluster was
// misbehaving — the one moment it is least safe to assume nobody is connected.
// gc is the only command in treehouse that destroys data; both belts stay on.
func TestKeepReason(t *testing.T) {
	cases := []struct {
		name     string
		sessions []string
		err      error
		wantKeep bool
	}{
		{"idle and answered: this one may go", nil, nil, false},
		{"somebody is connected", []string{"123  app  local"}, nil, true},
		{"the cluster could not be asked", nil, errors.New("psql: connection refused"), true},
		{"asked and errored is not asked and empty", []string{}, errors.New("boom"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			why := keepReason(c.sessions, c.err)
			if (why != "") != c.wantKeep {
				t.Errorf("keepReason = %q, wantKeep %v", why, c.wantKeep)
			}
			if c.wantKeep && why == "" {
				t.Error("a kept database with no reason is a database somebody has to guess about")
			}
		})
	}
}

// TestGCNeverDropsALiveWorktreesDatabase is the end-to-end version of the one
// property `th gc` cannot get wrong. The planner is tested from struct
// literals; this drives the real thing against a real cluster, because the
// inputs it reads — provenance comments Postgres stored, the .env a worktree
// actually has on disk — are exactly what a unit test has to invent.
//
// The three states below all keep a clone whose worktree is alive. Two of them
// are ordinary git commands that break the BRANCH link the comment records, and
// each one used to be enough to make gc offer somebody's working database.
func TestGCNeverDropsALiveWorktreesDatabase(t *testing.T) {
	skipWithoutCluster(t)
	const template = "th_gc_selftest"
	clusterExec(t, `DROP DATABASE IF EXISTS "`+template+`"`)
	clusterExec(t, `CREATE DATABASE "`+template+`"`)

	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, ".env.example"), "PORT=\nDATABASE_URL=\n")
	write(t, filepath.Join(main, ".env"), "PORT=3000\nDATABASE_URL=postgres:///"+template+"\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-m", "database")

	if out, ok := runTh(t, main, "new", "feat", "--skip-deps"); !ok {
		t.Fatalf("th new: %s", out)
	}
	linked := filepath.Join(filepath.Dir(main), "app-feat")
	clone := "th_gc_selfte_wt_feat"
	t.Cleanup(func() {
		runTh(t, main, "rm", "feat", "--force")
		clusterExec(t, `DROP DATABASE IF EXISTS "`+clone+`"`)
		clusterExec(t, `DROP DATABASE IF EXISTS "`+template+`"`)
	})
	if !clusterHas(t, clone) {
		t.Fatalf("th new made no clone named %q — the rest of this test would be vacuous", clone)
	}

	drops := func(t *testing.T) []string {
		t.Helper()
		out, _, code := runSplit(t, main, "gc", "--json")
		if code != 0 {
			t.Fatalf("gc exit %d\n%s", code, out)
		}
		var got struct {
			Status string                  `json:"status"`
			Drops  []struct{ Name string } `json:"drops"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("gc --json: %v\n%s", err, out)
		}
		if got.Status != "ok" {
			t.Fatalf("gc status = %q — the cluster is right there", got.Status)
		}
		var names []string
		for _, d := range got.Drops {
			names = append(names, d.Name)
		}
		return names
	}

	for _, state := range []struct {
		name string
		do   func()
	}{
		{"the worktree is on the branch its comment names", func() {}},
		// `git checkout --detach` is a bisect or a sha checkout. The worktree is
		// still there, its .env still names the clone, somebody is still in it.
		{"the worktree detached its HEAD", func() { git(t, linked, "checkout", "--detach") }},
		// And a rename breaks the link the other way.
		{"the branch was renamed under it", func() {
			git(t, linked, "checkout", "-b", "feat-renamed")
		}},
	} {
		t.Run(state.name, func(t *testing.T) {
			state.do()
			if names := drops(t); len(names) != 0 {
				t.Fatalf("gc offered to drop %v while a live worktree's .env names %q", names, clone)
			}
			// -y is the door an agent or a script goes through, so the guard has to
			// hold on the path that actually deletes, not just on the listing.
			if out, _, code := runSplit(t, main, "gc", "-y"); code != 0 {
				t.Fatalf("gc -y exit %d\n%s", code, out)
			}
			if !clusterHas(t, clone) {
				t.Fatalf("gc -y dropped %q out from under a live worktree", clone)
			}
		})
	}

	// The positive control: once the worktree is really gone, the clone IS a
	// corpse and gc has to say so — otherwise every assertion above passes for a
	// collector that never collects anything.
	t.Run("and it does collect once the worktree is gone", func(t *testing.T) {
		// git's own removal, not `th rm`: rm tears the database down itself, which
		// would prove nothing about gc.
		git(t, main, "worktree", "remove", "--force", linked)
		if names := drops(t); len(names) != 1 || names[0] != clone {
			t.Fatalf("gc = %v, want just %q — a collector that never collects is not one", names, clone)
		}
	})
}

// clusterExec runs one statement against the cluster the tests are pointed at,
// skipping rather than failing when it cannot — the same bargain internal/pg's
// live tests make.
func clusterExec(t *testing.T, sql string) {
	t.Helper()
	c := exec.Command("psql", "-d", "postgres", "-At", "--no-psqlrc", "-v", "ON_ERROR_STOP=1")
	c.Stdin = strings.NewReader(sql)
	if out, err := c.CombinedOutput(); err != nil {
		t.Skipf("cannot prepare the cluster (%v): %s", err, out)
	}
}

func clusterHas(t *testing.T, name string) bool {
	t.Helper()
	c := exec.Command("psql", "-d", "postgres", "-At", "--no-psqlrc", "-c",
		"SELECT count(*) FROM pg_database WHERE datname = $$"+name+"$$")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("asking the cluster about %q: %v: %s", name, err, out)
	}
	return strings.TrimSpace(string(out)) == "1"
}

func skipWithoutCluster(t *testing.T) {
	t.Helper()
	if err := exec.Command("pg_isready").Run(); err != nil {
		t.Skip("no postgres server responding")
	}
}
