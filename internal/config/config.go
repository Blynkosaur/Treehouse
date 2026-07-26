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

// File is the parsed treehouse.toml. Deps are extra or overriding DepRules —
// the agent extension point for languages beyond the built-in node/python.
type File struct {
	Deps []check.DepRule `toml:"deps"`
	Env  Env             `toml:"env"`
}

// Env carries the human's judgment about env keys. Required is the whole FAIL
// tier: inferred requirements are warnings, a curated list is a failure.
type Env struct {
	Required []string `toml:"required"`
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

// MergeRules overlays override onto defaults: an override whose Name matches a
// default replaces it; a new Name appends. defaults is not mutated.
func MergeRules(defaults, override []check.DepRule) []check.DepRule {
	merged := make([]check.DepRule, len(defaults))
	copy(merged, defaults)

	for _, o := range override {
		replaced := false
		for i := range merged {
			if merged[i].Name == o.Name {
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
