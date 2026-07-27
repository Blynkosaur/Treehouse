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

// TestPlanOpen is L1's hand-off rule: born ready means never handing somebody a
// worktree that isn't.
func TestPlanOpen(t *testing.T) {
	cases := []struct {
		name    string
		command string
		verdict string
		force   bool
		refuse  bool
		want    string // the command that runs; "" = nothing does
		inSkip  string // substring the skip line must carry; "" = say nothing
	}{
		{name: "healthy and configured", command: "cursor .", verdict: "ok", want: "cursor ."},
		// Warnings are the normal state of a repo nobody has run yet. A hand-off
		// that almost never fires is one nobody configures.
		{name: "warnings do not block", command: "cursor .", verdict: "warn", want: "cursor ."},
		{name: "a skipped check does not block either", command: "cursor .", verdict: skip, want: "cursor ."},
		{name: "a failure does", command: "cursor .", verdict: "fail", inSkip: "not handing over a broken worktree"},
		{name: "--open overrides the failure", command: "cursor .", verdict: "fail", force: true, want: "cursor ."},
		{name: "--no-open wins, silently", command: "cursor .", verdict: "ok", refuse: true},
		{name: "--no-open wins over --open's own override", command: "cursor .", verdict: "fail", force: true, refuse: true},
		// The common case by far: most repos have no opinion, and a nag per
		// `th new` is how a good flag gets turned off.
		{name: "unconfigured says nothing at all", verdict: "ok"},
		{name: "unless --open asked for it", verdict: "ok", force: true, inSkip: "no [open] command configured"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PlanOpen(c.command, c.verdict, c.force, c.refuse)
			if got.Command != c.want {
				t.Errorf("Command = %q, want %q", got.Command, c.want)
			}
			switch {
			case c.inSkip == "" && got.Skip != "":
				t.Errorf("Skip = %q, want silence", got.Skip)
			case c.inSkip != "" && !strings.Contains(got.Skip, c.inSkip):
				t.Errorf("Skip = %q, want it to mention %q", got.Skip, c.inSkip)
			}
		})
	}
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
