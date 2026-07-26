package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
)

func writeToml(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "treehouse.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadParsesDeps(t *testing.T) {
	dir := t.TempDir()
	writeToml(t, dir, `
[[deps]]
name = "vendor/bundle"
action = "clone"

[[deps]]
name = ".venv"
action = "recreate"
command = "poetry install"
`)

	f, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []check.DepRule{
		{Name: "vendor/bundle", Action: check.Clone},
		{Name: ".venv", Action: check.Recreate, Command: "poetry install"},
	}
	if !reflect.DeepEqual(f.Deps, want) {
		t.Errorf("Deps = %+v, want %+v", f.Deps, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	f, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load on dir without treehouse.toml: %v", err)
	}
	if len(f.Deps) != 0 {
		t.Errorf("Deps = %+v, want empty", f.Deps)
	}
}

func TestLoadBadAction(t *testing.T) {
	dir := t.TempDir()
	writeToml(t, dir, `
[[deps]]
name = "x"
action = "bogus"
`)
	if _, err := config.Load(dir); err == nil {
		t.Error("expected error for bogus action, got nil")
	}
}

func TestMerge(t *testing.T) {
	defaults := check.DefaultDepRules()
	defaultsBefore := make([]check.DepRule, len(defaults))
	copy(defaultsBefore, defaults)

	override := []check.DepRule{
		{Name: ".venv", Action: check.Recreate, Command: "poetry install"}, // replaces default
		{Name: "target", Action: check.Recreate, Command: "cargo build"},   // new → appends
	}
	merged := config.Merge(defaults, override, ruleName)

	// Override by Name replaces the default's command.
	var venv check.DepRule
	found := false
	for _, r := range merged {
		if r.Name == ".venv" {
			venv = r
			found = true
		}
	}
	if !found || venv.Command != "poetry install" {
		t.Errorf(".venv rule = %+v, want Command=poetry install", venv)
	}

	// New name appended.
	hasTarget := false
	for _, r := range merged {
		if r.Name == "target" {
			hasTarget = true
		}
	}
	if !hasTarget {
		t.Error("merged missing appended 'target' rule")
	}

	// Original defaults slice not mutated.
	if !reflect.DeepEqual(defaults, defaultsBefore) {
		t.Errorf("defaults mutated: %+v, want %+v", defaults, defaultsBefore)
	}
}

func ruleName(r check.DepRule) string { return r.Name }

// TestMergeIsGeneric: seeds need the same name-keyed overlay dep rules do, and
// phase 4's triage signatures will be the third. One function, not three.
func TestMergeSeeds(t *testing.T) {
	merged := config.Merge(nil, []check.Seed{
		{Name: "ramp", Command: "old"},
		{Name: "sondermind", Command: "b"},
		{Name: "ramp", Command: "new"}, // a duplicated entry: last wins, not both
	}, func(s check.Seed) string { return s.Name })

	want := []check.Seed{{Name: "ramp", Command: "new"}, {Name: "sondermind", Command: "b"}}
	if !reflect.DeepEqual(merged, want) {
		t.Errorf("Merge = %+v, want %+v", merged, want)
	}
}

func TestLoadParsesConfigSchema(t *testing.T) {
	dir := t.TempDir()
	writeToml(t, dir, `
[database]
psql = "docker compose exec -T db psql"

[migrations]
status = "alembic current"
dir = "db/migrate"

[[seed]]
name = "ramp"
command = "python manage.py loaddata ramp"
`)
	f, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Database.Psql != "docker compose exec -T db psql" {
		t.Errorf("Database.Psql = %q", f.Database.Psql)
	}
	if f.Migrations.Status != "alembic current" || f.Migrations.Dir != "db/migrate" {
		t.Errorf("Migrations = %+v", f.Migrations)
	}
	want := []check.Seed{{Name: "ramp", Command: "python manage.py loaddata ramp"}}
	if !reflect.DeepEqual(f.Seed, want) {
		t.Errorf("Seed = %+v, want %+v", f.Seed, want)
	}
}

func TestLoadParsesRequired(t *testing.T) {
	dir := t.TempDir()
	writeToml(t, dir, `
[env]
required = ["DATABASE_URL", "STRIPE_KEY"]
`)
	f, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"DATABASE_URL", "STRIPE_KEY"}
	if !reflect.DeepEqual(f.Env.Required, want) {
		t.Errorf("Env.Required = %v, want %v", f.Env.Required, want)
	}
}
