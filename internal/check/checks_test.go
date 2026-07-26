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
		{"skip never moves the verdict", nil, []Check{{Status: "skip"}}, "ok"},
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

// TestStatusDBColumn: the fleet column answers clone-exists and nothing else,
// and stays blank when the question was never asked — blank is not "missing",
// and colouring it as one would send people looking for a database that this
// repo has no concept of.
func TestStatusDBColumn(t *testing.T) {
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"DATABASE_URL": "postgres://h/app_dev"}},
	}}
	live := []string{"app_dev", DBName("app_dev", Slug("feat/a"))}

	cases := []struct {
		name   string
		d      Doctor
		source Worktree
		ref    Ref
		want   string
	}{
		{"clone present", Doctor{Databases: live}, source, Ref{Path: "/w", Branch: "feat/a"}, "ok"},
		{"main is the template, never a missing clone", Doctor{Databases: live, MainBranch: "main"}, source, Ref{Path: "/main", Branch: "main"}, "shared"},
		{"clone absent", Doctor{Databases: live}, source, Ref{Path: "/w", Branch: "feat/b"}, "missing"},
		{"postgres never asked", Doctor{}, source, Ref{Path: "/w", Branch: "feat/a"}, ""},
		{"repo declares no database", Doctor{Databases: live}, Worktree{Root: "/main"}, Ref{Path: "/w", Branch: "feat/a"}, ""},
		{"detached worktree never had one", Doctor{Databases: live}, source, Ref{Path: "/w"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.d.Status(Worktree{Root: "/w"}, c.ref, c.source).DB; got != c.want {
				t.Errorf("DB = %q, want %q", got, c.want)
			}
		})
	}
}
