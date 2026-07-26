package check

import (
	"path/filepath"
	"sort"
)

// Repair is one .env file's worth of planned fixes — computed, never yet
// applied. cmd/hydrate turns Repairs into file writes; this package only
// decides. (Plan-then-apply, same discipline as Finding.)
type Repair struct {
	EnvPath   string            // the .env to create or extend
	Create    bool              // true when no .env exists yet
	Overwrite bool              // true when existing lines must be rewritten (derived values), not appended
	Add       map[string]string // keys to write; value sourced from source worktree ("" when unsourced)
	Unsourced []string          // keys the source couldn't supply a value for (sorted)
	Skip      string            // non-empty => cannot act; human-readable reason (mirrors DepPlan.Skip)
	Warn      string            // acted, but on a judgment call the human should see (mirrors Skip's voice)
}

// PlanHydrate compares w's drift against a source worktree (normally the main
// checkout) and plans repairs for every missing key. Matching is by directory
// relative to each root: <w>/seo_service/.env sources from <source>/seo_service/.env.
//
// v1 scope, deliberately: only MISSING keys are planned — they can be fixed by
// append-only writes, which can never damage an existing file. Present-but-empty
// keys would require in-place edits; they stay doctor's nag until v2.
func (d Doctor) PlanHydrate(w, source Worktree) []Repair {
	// Index the source's real .env vars by relative directory.
	srcVars := source.EnvVarsByDir()

	var repairs []Repair
	for _, finding := range d.CheckEnv(w, source) {
		if len(finding.Missing) == 0 {
			continue // healthy, or only-empty drift (v2 territory)
		}
		rel, err := filepath.Rel(w.Root, finding.Dir)
		if err != nil {
			rel = finding.Dir
		}
		src := srcVars[rel] // nil when source has no .env for this service — fine

		r := Repair{
			EnvPath: filepath.Join(finding.Dir, ".env"),
			Create:  finding.NoEnv,
			Add:     map[string]string{},
		}
		for _, key := range finding.Missing {
			val := src[key] // "" when source lacks the key or has it empty
			r.Add[key] = val
			if val == "" {
				r.Unsourced = append(r.Unsourced, key)
			}
		}
		sort.Strings(r.Unsourced)
		repairs = append(repairs, r)
	}
	return repairs
}
