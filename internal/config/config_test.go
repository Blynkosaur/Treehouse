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

func TestMergeRules(t *testing.T) {
	defaults := check.DefaultDepRules()
	defaultsBefore := make([]check.DepRule, len(defaults))
	copy(defaultsBefore, defaults)

	override := []check.DepRule{
		{Name: ".venv", Action: check.Recreate, Command: "poetry install"}, // replaces default
		{Name: "target", Action: check.Recreate, Command: "cargo build"},   // new → appends
	}
	merged := config.MergeRules(defaults, override)

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
