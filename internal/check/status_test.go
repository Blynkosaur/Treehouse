package check

import (
	"testing"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

func TestStatus(t *testing.T) {
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"A": "1", "B": "2"}},
	}}

	cases := []struct {
		name     string
		doctor   Doctor
		wt       Worktree
		ref      Ref
		wantEnv  string
		wantName string
	}{
		{
			name:     "healthy",
			wt:       Worktree{Root: "/wt", EnvFiles: []envfile.File{{Path: "/wt/.env", Vars: map[string]string{"A": "1", "B": "2"}}}},
			ref:      Ref{Path: "/wt", Branch: "feat"},
			wantEnv:  "ok",
			wantName: "feat",
		},
		{
			name:     "inferred drift warns",
			wt:       Worktree{Root: "/wt", EnvFiles: []envfile.File{{Path: "/wt/.env", Vars: map[string]string{"A": "1"}}}},
			ref:      Ref{Path: "/wt", Branch: "feat"},
			wantEnv:  "warn",
			wantName: "feat",
		},
		{
			name:     "curated key fails",
			doctor:   Doctor{Required: []string{"B"}},
			wt:       Worktree{Root: "/wt", EnvFiles: []envfile.File{{Path: "/wt/.env", Vars: map[string]string{"A": "1"}}}},
			ref:      Ref{Path: "/wt", Branch: "feat"},
			wantEnv:  "fail",
			wantName: "feat",
		},
		{
			name:     "detached head still gets a row",
			wt:       Worktree{Root: "/wt", EnvFiles: []envfile.File{{Path: "/wt/.env", Vars: map[string]string{"A": "1", "B": "2"}}}},
			ref:      Ref{Path: "/wt", Detached: true},
			wantEnv:  "ok",
			wantName: "(detached)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.ref.Behind, c.ref.Dirty = 3, true
			got := c.doctor.Status(c.wt, c.ref, source)
			if got.Env != c.wantEnv {
				t.Errorf("Env = %q, want %q", got.Env, c.wantEnv)
			}
			if got.Branch != c.wantName {
				t.Errorf("Branch = %q, want %q", got.Branch, c.wantName)
			}
			// git's answers pass through untouched — Status asks git nothing.
			if got.Behind != 3 || !got.Dirty || got.Path != "/wt" {
				t.Errorf("git columns mangled: %+v", got)
			}
		})
	}
}
