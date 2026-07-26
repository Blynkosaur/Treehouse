package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckMigrations(t *testing.T) {
	cases := []struct {
		name       string
		in         MigrationInput
		want       string
		wantDetail string
	}{
		{
			name:       "unconfigured says so instead of guessing",
			in:         MigrationInput{},
			want:       skip,
			wantDetail: "nothing tool-agnostic to ask",
		},
		{
			// A typo in treehouse.toml exits 127, and reading that as "pending"
			// would report a config mistake as a permanent database problem.
			name:       "a command that could not run is not a pending migration",
			in:         MigrationInput{Command: "alembik current", Err: "sh: alembik: command not found"},
			want:       "warn",
			wantDetail: "could not run",
		},
		{
			name:       "exit zero is applied",
			in:         MigrationInput{Command: "alembic current"},
			want:       "ok",
			wantDetail: "applied",
		},
		{
			// The honest version of "ahead — expected".
			name:       "pending with new files on this branch is your own work",
			in:         MigrationInput{Command: "alembic current", Pending: true, Dir: "alembic/versions", Added: 2},
			want:       "warn",
			wantDetail: "this branch adds 2 migration file(s)",
		},
		{
			name:       "pending with no new files is main moving ahead",
			in:         MigrationInput{Command: "alembic current", Pending: true, Dir: "alembic/versions"},
			want:       "warn",
			wantDetail: "main moved ahead",
		},
		{
			name:       "pending with no migrations dir reports only what it knows",
			in:         MigrationInput{Command: "alembic current", Pending: true},
			want:       "warn",
			wantDetail: "reports migrations pending",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (Doctor{}).CheckMigrations(c.in)
			if got.Name != "migrations" || got.Status != c.want {
				t.Errorf("= %+v, want status %q", got, c.want)
			}
			if !strings.Contains(got.Detail, c.wantDetail) {
				t.Errorf("detail = %q, want it to mention %q", got.Detail, c.wantDetail)
			}
		})
	}
}

func TestMigrationsDir(t *testing.T) {
	root := t.TempDir()
	if got := MigrationsDir(root, ""); got != "" {
		t.Errorf("a repo with no migrations dir = %q, want empty", got)
	}
	if got := MigrationsDir(root, "custom/place"); got != "custom/place" {
		t.Errorf("config must win even when nothing is inferable: %q", got)
	}
	if err := os.MkdirAll(filepath.Join(root, "alembic", "versions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := MigrationsDir(root, ""); got != "alembic/versions" {
		t.Errorf("= %q, want alembic/versions", got)
	}
	if got := MigrationsDir(root, "db/migrate"); got != "db/migrate" {
		t.Errorf("config must sharpen a wrong guess: %q", got)
	}
}

func TestCheckSeed(t *testing.T) {
	configured := []Seed{{Name: "ramp"}, {Name: "sondermind"}}

	cases := []struct {
		name       string
		available  []Seed
		present    []string
		want       string
		wantDetail string
	}{
		{"nothing configured", nil, nil, skip, "no [[seed]] datasets"},
		{"configured but none loaded", configured, nil, "warn", "ramp, sondermind"},
		{"loaded", configured, []string{"ramp"}, "ok", "ramp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := (Doctor{}).CheckSeed(c.available, c.present)
			if got.Name != "seed" || got.Status != c.want {
				t.Errorf("= %+v, want status %q", got, c.want)
			}
			if !strings.Contains(got.Detail, c.wantDetail) {
				t.Errorf("detail = %q, want it to mention %q", got.Detail, c.wantDetail)
			}
		})
	}
}
