package check

import (
	"strings"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

func TestCheckDB(t *testing.T) {
	clone := DBPlan{Name: "app_wt_feat", Template: "app_dev", Exists: true}

	cases := []struct {
		name       string
		state      DBState
		want       string
		wantDetail string // a substring the line must carry
	}{
		{
			name:       "pointed at its own clone",
			state:      DBState{Plan: clone, EnvDB: "app_wt_feat"},
			want:       "ok",
			wantDetail: "app_wt_feat",
		},
		{
			// A2's acceptance criterion, and the only fail this check can produce.
			// Nothing about this state looks wrong from inside the app.
			name:       "clone exists but .env still names the shared database",
			state:      DBState{Plan: clone, EnvDB: "app_dev"},
			want:       "fail",
			wantDetail: "SHARED",
		},
		{
			name:       "clone exists, .env names something else entirely",
			state:      DBState{Plan: clone, EnvDB: "someones_scratch"},
			want:       "warn",
			wantDetail: "someones_scratch",
		},
		{
			name:       "clone exists, .env names no database at all",
			state:      DBState{Plan: clone, EnvDB: ""},
			want:       "warn",
			wantDetail: "names no database",
		},
		{
			name:       "no clone yet",
			state:      DBState{Plan: DBPlan{Name: "app_wt_feat", Template: "app_dev"}, EnvDB: "app_dev"},
			want:       "warn",
			wantDetail: "no database clone",
		},
		{
			name:       "the planner declined, and says why",
			state:      DBState{Plan: DBPlan{Skip: "detached HEAD — no stable name for a database clone"}},
			want:       "skip",
			wantDetail: "detached HEAD",
		},
		{
			// Main legitimately talks to the template. Flagging it would train
			// people to ignore the one row that matters.
			name:       "the main checkout is not a worktree missing a clone",
			state:      DBState{Plan: DBPlan{Name: "app_wt_main", Template: "app_dev"}, EnvDB: "app_dev", Main: true},
			want:       "ok",
			wantDetail: "app_dev",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (Doctor{}).CheckDB(c.state)
			if got.Name != "db" {
				t.Errorf("name = %q", got.Name)
			}
			if got.Status != c.want {
				t.Errorf("status = %q, want %q (%s)", got.Status, c.want, got.Detail)
			}
			if !strings.Contains(got.Detail, c.wantDetail) {
				t.Errorf("detail = %q, want it to mention %q", got.Detail, c.wantDetail)
			}
			if (got.Status == "fail" || got.Status == "warn") && got.Fix == "" {
				t.Error("a problem with no fix line is a problem the reader has to solve twice")
			}
		})
	}
}

func TestVerdict(t *testing.T) {
	drifted := []Finding{{Dir: "/a", Missing: []string{"K"}}}
	failed := []Finding{{Dir: "/a", Missing: []string{"K"}, Failed: []string{"K"}}}

	cases := []struct {
		name     string
		findings []Finding
		checks   []Check
		want     string
	}{
		{"nothing wrong", nil, nil, "ok"},
		{"env drift alone warns", drifted, nil, "warn"},
		{"a curated key fails", failed, nil, "fail"},
		{"a failing check fails an otherwise clean worktree", nil, []Check{{Status: "fail"}}, "fail"},
		{"a warning check warns", nil, []Check{{Status: "warn"}}, "warn"},
		// The rule this whole pass turns on: nobody asked, so nobody may report
		// green. Triage already refuses to read a skipped check as corroboration.
		{"a skipped check is not a passed one", nil, []Check{{Status: "skip"}}, "skip"},
		{"an ok check does not cover for a skipped one", nil, []Check{{Status: "ok"}, {Status: "skip"}}, "skip"},
		{"a known problem outranks an unknown one", nil, []Check{{Status: "skip"}, {Status: "warn"}}, "warn"},
		{"and the order it is folded in cannot change that", nil, []Check{{Status: "warn"}, {Status: "skip"}}, "warn"},
		{"a failing check outranks env drift", drifted, []Check{{Status: "fail"}}, "fail"},
		{"an ok check does not rescue a failing env", failed, []Check{{Status: "ok"}}, "fail"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Verdict(c.findings, c.checks); got != c.want {
				t.Errorf("Verdict = %q, want %q", got, c.want)
			}
		})
	}
}

// tiers is the ordering written out independently of checks.go's rank map, so a
// re-ranking has to be made in two places rather than sliding through one. skip
// sits between ok and warn: "nobody asked" is not a pass, and a known problem
// beats an unknown one.
var tiers = []string{"ok", skip, "warn", "fail"}

// TestFoldsAgreeOnEveryCombination is the exhaustive version of TestVerdict.
// skip was inserted into an ordering every status fold in the program depends
// on, so every pair has to be checked rather than the handful anyone thought of
// — including the pairs that cross the two lists Verdict folds and the two
// Fleet folds.
func TestFoldsAgreeOnEveryCombination(t *testing.T) {
	// The three words findings can produce. skip is deliberately absent: an env
	// finding is always answerable, which is why EnvStatus has only three tiers.
	envAt := map[string][]Finding{
		"ok":   {{Dir: "/a", Keys: 1}},
		"warn": {{Dir: "/a", Missing: []string{"K"}}},
		"fail": {{Dir: "/a", Missing: []string{"K"}, Failed: []string{"K"}}},
	}

	for i, a := range tiers {
		for j, b := range tiers {
			want := tiers[max(i, j)]

			// Two checks in one list, both orders — a fold that took the LAST word
			// instead of the worst would pass one and fail the other.
			for _, checks := range [][]Check{
				{{Name: "x", Status: a}, {Name: "y", Status: b}},
				{{Name: "y", Status: b}, {Name: "x", Status: a}},
			} {
				if got := Verdict(nil, checks); got != want {
					t.Errorf("Verdict(nil, %v) = %q, want %q", checks, got, want)
				}
			}
			// Across Fleet's two lists, and across its rows.
			if got := Fleet([]Status{{Status: a}}, []Check{{Name: "config", Status: b}}); got != want {
				t.Errorf("Fleet(row %q, repo %q) = %q, want %q", a, b, got, want)
			}
			if got := Fleet([]Status{{Status: a}, {Status: b}}, nil); got != want {
				t.Errorf("Fleet(rows %q, %q) = %q, want %q", a, b, got, want)
			}
		}
	}

	// Findings crossed with checks: the env half has no skip tier, so this is
	// where a skipped check has to survive next to a green env.
	for env, findings := range envAt {
		for j, c := range tiers {
			want := tiers[max(slicesIndex(tiers, env), j)]
			if got := Verdict(findings, []Check{{Name: "db", Status: c}}); got != want {
				t.Errorf("Verdict(env %q, check %q) = %q, want %q", env, c, got, want)
			}
		}
	}

	// An empty report is the one case that may answer ok, because there was
	// nothing to be unsure about.
	if got := Verdict(nil, nil); got != "ok" {
		t.Errorf("Verdict(nil, nil) = %q, want ok", got)
	}
	if got := Fleet(nil, nil); got != "ok" {
		t.Errorf("Fleet(nil, nil) = %q, want ok", got)
	}
}

// TestNothingMadeOfSkipsIsGreen is the property stated on its own, because it is
// the one an agent's exit-code gate depends on: a report where every single
// answer was "could not ask" must never come back ok, however many of them there
// are and whichever list they arrive in.
func TestNothingMadeOfSkipsIsGreen(t *testing.T) {
	for n := 1; n <= 4; n++ {
		checks := make([]Check, n)
		rows := make([]Status, n)
		for i := range checks {
			checks[i] = Check{Name: "db", Status: skip}
			rows[i] = Status{Status: skip}
		}
		if got := Verdict(nil, checks); got != skip {
			t.Errorf("Verdict over %d skips = %q, want skip", n, got)
		}
		if got := Fleet(rows, checks); got != skip {
			t.Errorf("Fleet over %d skipped rows = %q, want skip", n, got)
		}
		// A clean env beside them must not vote the report green either.
		if got := Verdict([]Finding{{Dir: "/a", Keys: 1}}, checks); got != skip {
			t.Errorf("Verdict(clean env, %d skips) = %q, want skip", n, got)
		}
	}
}

func slicesIndex(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}

// TestStatusDBColumn is ITEM 1's regression. The column used to answer
// clone-exists only, so a worktree whose clone existed while its .env still
// named the SHARED database read `db: ok` in the glance view — green, in the
// table people use to pick which worktree to hand an agent — while `th doctor`
// called the same state a failure and exited 2.
//
// It also pins the row's own verdict, because that is the field `ls --json`
// folds and exits on.
func TestStatusDBColumn(t *testing.T) {
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"DATABASE_URL": "postgres://h/app_dev"}},
	}}
	clone := DBName("app_dev", Slug("feat/a"))
	live := []string{"app_dev", clone}

	// at builds the worktree whose .env names db. "" gives a URL with no database
	// in it — present and non-empty, so the env half stays clean and every row
	// below is about the database column alone.
	at := func(db string) Worktree {
		return Worktree{Root: "/w", EnvFiles: []envfile.File{
			{Path: "/w/.env", Vars: map[string]string{"DATABASE_URL": "postgres://h/" + db}},
		}}
	}

	cases := []struct {
		name       string
		d          Doctor
		source     Worktree
		wt         Worktree
		ref        Ref
		want       string
		wantStatus string
	}{
		{"pointed at its own clone", Doctor{Databases: live}, source, at(clone), Ref{Path: "/w", Branch: "feat/a"}, "ok", "ok"},
		{"clone exists, .env still names the shared database", Doctor{Databases: live}, source, at("app_dev"), Ref{Path: "/w", Branch: "feat/a"}, "shared", "fail"},
		{"clone exists, .env names nothing", Doctor{Databases: live}, source, at(""), Ref{Path: "/w", Branch: "feat/a"}, "adrift", "warn"},
		{"main is the template, never a missing clone", Doctor{Databases: live, MainBranch: "main"}, source, at("app_dev"), Ref{Path: "/main", Branch: "main"}, "main", "ok"},
		{"clone absent", Doctor{Databases: live}, source, at("app_dev"), Ref{Path: "/w", Branch: "feat/b"}, "missing", "warn"},
		{"postgres never asked", Doctor{}, source, at(""), Ref{Path: "/w", Branch: "feat/a"}, "", "ok"},
		{"repo declares no database", Doctor{Databases: live}, Worktree{Root: "/main"}, at(""), Ref{Path: "/w", Branch: "feat/a"}, "", "ok"},
		{"detached worktree never had one", Doctor{Databases: live}, source, at(""), Ref{Path: "/w"}, "", "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.d.Status(c.wt, c.ref, c.source)
			if got.DB != c.want {
				t.Errorf("DB = %q, want %q", got.DB, c.want)
			}
			if got.Status != c.wantStatus {
				t.Errorf("Status = %q, want %q — this is the field `ls --json` exits on", got.Status, c.wantStatus)
			}
		})
	}
}

// TestDetachedWorktreeAgreesWithDoctor is left failing, and the reason is that
// two stated intents collide — this is a call for whoever owns the design, not
// a patch.
//
// Standing in a detached worktree, `th doctor` reports `db: skip` ("detached
// HEAD — no stable name for a database clone") and a `skip` verdict. `th ls`
// answers the same worktree with a blank db column and `ok`, because Status
// guards the whole database question behind `ref.Branch != ""` and never asks
// PlanDB at all. Verified end to end against a live cluster.
//
// One intent says the two commands may never publish different `status` words
// for one worktree. The other is the case below marked "detached worktree never
// had one", which says the blank is correct. They cannot both hold.
//
// The fix, if the first intent wins, is to let PlanDB answer instead of
// short-circuiting: pass Slug(ref.Branch) or "" (Slug("") is a HASH, not "",
// which is why the guard is there) and tighten Main to `ref.Branch != "" &&
// ref.Branch == d.MainBranch` — otherwise, with a detached MAIN, MainBranch is
// "" and every detached worktree in the fleet reports itself as the main
// checkout.
func TestDetachedWorktreeAgreesWithDoctor(t *testing.T) {
	t.Skip("BUG: `th ls` says ok for a detached worktree that `th doctor` says skip")

	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"DATABASE_URL": "postgres://h/app_dev"}},
	}}
	d := Doctor{Databases: []string{"app_dev"}, MainBranch: "main"}
	got := d.Status(Worktree{Root: "/w"}, Ref{Path: "/w"}, source)

	// What doctor says about the very same worktree.
	want := Verdict(nil, []Check{d.CheckDB(DBState{Plan: d.PlanDB(DBInput{
		Template: "app_dev", Existing: d.Databases,
	})})})
	if got.Status != want {
		t.Errorf("ls says %q, doctor says %q about one detached worktree", got.Status, want)
	}
}

// TestFleet: one bad worktree is a bad fleet, and a fleet nobody could ask
// about is not a healthy one.
func TestFleet(t *testing.T) {
	cases := []struct {
		name string
		rows []Status
		repo []Check
		want string
	}{
		{"all clear", []Status{{Status: "ok"}, {Status: "ok"}}, nil, "ok"},
		{"one failure fails the fleet", []Status{{Status: "ok"}, {Status: "fail"}}, nil, "fail"},
		{"a warning warns", []Status{{Status: "ok"}, {Status: "warn"}}, nil, "warn"},
		{"a known problem outranks an unknown one", []Status{{Status: "skip"}, {Status: "warn"}}, nil, "warn"},
		{"nobody could be asked is not ok", []Status{{Status: "ok"}, {Status: "skip"}}, nil, "skip"},
		{"an empty fleet is vacuously fine", nil, nil, "ok"},
		// A treehouse.toml nobody could parse belongs to no worktree and breaks
		// them all, so it fails a fleet whose every row is fine.
		{"a repo-wide failure fails a healthy fleet", []Status{{Status: "ok"}}, []Check{{Name: "config", Status: "fail"}}, "fail"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Fleet(c.rows, c.repo); got != c.want {
				t.Errorf("Fleet = %q, want %q", got, c.want)
			}
		})
	}
}
