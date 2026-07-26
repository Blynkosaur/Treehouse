// Package config reads treehouse.toml — the optional per-repo file that extends
// the built-in dependency rules. Zero-config is the norm: no file means no
// overrides, not an error.
package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/Blynkosaur/treehouse/internal/check"
)

// File is the parsed treehouse.toml. Everything here is either a judgment
// (which env keys are required) or a fact that cannot be inferred (how to run
// YOUR migrations, YOUR seeds, YOUR psql). Nothing inferable belongs in it:
// zero-config already covers the database clone and the .env repoint end to
// end, because the template name, the connection and the clone name all fall
// out of main's own .env.
type File struct {
	Deps       []check.DepRule `toml:"deps"`
	Env        Env             `toml:"env"`
	Database   Database        `toml:"database"`
	Migrations Migrations      `toml:"migrations"`
	Seed       []check.Seed    `toml:"seed"`
}

// Env carries the human's judgment about env keys. Required is the whole FAIL
// tier: inferred requirements are warnings, a curated list is a failure.
type Env struct {
	Required []string `toml:"required"`
}

// Database is the escape hatch for a cluster the local psql can't reach —
// "docker compose exec -T db psql". There is deliberately no `template` key:
// which database to clone is main's own .env, and a second place to say it is a
// second place for it to be wrong.
type Database struct {
	Psql string `toml:"psql"`
}

// Migrations is the un-inferable half of A3. Status is a command whose EXIT
// CODE means "there are migrations to run" — the one signal alembic, Django and
// Prisma genuinely share. Dir is only for when the glob guesses wrong.
type Migrations struct {
	Status string `toml:"status"`
	Dir    string `toml:"dir"`
}

// Load reads <root>/treehouse.toml. A missing file is normal (empty File, nil
// error); any other read/parse error propagates.
func Load(root string) (File, error) {
	var f File
	data, err := os.ReadFile(filepath.Join(root, "treehouse.toml"))
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	return f, toml.Unmarshal(data, &f)
}

// Merge overlays override onto defaults, keyed by name: an override whose name
// matches a default replaces it; a new name appends. defaults is not mutated.
//
// Generic because there are already two lists that need exactly this (dep
// rules, seed datasets) and phase 4's triage signatures will be the third. One
// function beats three copies that drift on the "does a duplicate replace or
// append" question — and passing the key as a func is what keeps it from
// needing a Named interface with three implementations.
func Merge[T any](defaults, override []T, name func(T) string) []T {
	merged := make([]T, len(defaults))
	copy(merged, defaults)

	for _, o := range override {
		replaced := false
		for i := range merged {
			if name(merged[i]) == name(o) {
				merged[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, o)
		}
	}
	return merged
}
