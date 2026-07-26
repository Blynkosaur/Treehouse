package check_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
)

// mkdir/write helpers build a real dir tree in tmp so PlanDeps walks actual FS.
func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// eval resolves symlinks so tmp-path equality survives macOS /var -> /private/var.
func eval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", p, err)
	}
	return r
}

func uvPresent() bool {
	_, err := exec.LookPath("uv")
	return err == nil
}

// findPlan returns the plan whose Dst basename-or-relpath matches rel under root.
func findPlan(plans []check.DepPlan, dst string) (check.DepPlan, bool) {
	for _, p := range plans {
		if p.Dst == dst {
			return p, true
		}
	}
	return check.DepPlan{}, false
}

func TestPlanDepsClone(t *testing.T) {
	source := t.TempDir()
	w := t.TempDir()
	writeFile(t, filepath.Join(source, "node_modules", "pkg.js"), "module.exports={}")

	plans := check.Doctor{}.PlanDeps(
		check.Worktree{Root: w},
		check.Worktree{Root: source},
		check.DefaultDepRules(),
	)

	if len(plans) != 1 {
		t.Fatalf("got %d plans, want exactly 1: %+v", len(plans), plans)
	}
	p := plans[0]
	if p.Action != check.Clone {
		t.Errorf("Action = %v, want Clone", p.Action)
	}
	if eval(t, filepath.Dir(p.Src)) != eval(t, source) || filepath.Base(p.Src) != "node_modules" {
		t.Errorf("Src = %q, want <source>/node_modules", p.Src)
	}
	if eval(t, filepath.Dir(p.Dst)) != eval(t, w) || filepath.Base(p.Dst) != "node_modules" {
		t.Errorf("Dst = %q, want <w>/node_modules", p.Dst)
	}
	if p.Skip != "" {
		t.Errorf("Skip = %q, want empty", p.Skip)
	}
}

func TestPlanDepsRecreateExplicitCommand(t *testing.T) {
	source := t.TempDir()
	w := t.TempDir()
	writeFile(t, filepath.Join(source, ".venv", "pyvenv.cfg"), "home = /x")

	rules := []check.DepRule{{Name: ".venv", Action: check.Recreate, Command: "echo rebuilt"}}
	plans := check.Doctor{}.PlanDeps(check.Worktree{Root: w}, check.Worktree{Root: source}, rules)

	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1: %+v", len(plans), plans)
	}
	p := plans[0]
	if p.Command != "echo rebuilt" {
		t.Errorf("Command = %q, want %q", p.Command, "echo rebuilt")
	}
	// firstWord is "echo", not "uv", so no uv-presence check applies.
	if p.Skip != "" {
		t.Errorf("Skip = %q, want empty (no uv check for non-uv command)", p.Skip)
	}
}

func TestPlanDepsManifestLadder(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		wantCmd  string
	}{
		{"requirements", "requirements.txt", "uv venv && uv pip install -r requirements.txt"},
		{"uvlock", "uv.lock", "uv venv && uv sync"},
		{"pyproject", "pyproject.toml", "uv venv && uv sync"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := t.TempDir()
			w := t.TempDir()
			writeFile(t, filepath.Join(source, ".venv", "pyvenv.cfg"), "home = /x")
			// The manifest is a tracked file, so it lives in the target checkout w
			// (resolveVenvCommand reads it from the venv's dest parent, not source).
			writeFile(t, filepath.Join(w, tc.manifest), "x")

			plans := check.Doctor{}.PlanDeps(
				check.Worktree{Root: w},
				check.Worktree{Root: source},
				check.DefaultDepRules(),
			)
			if len(plans) != 1 {
				t.Fatalf("got %d plans, want 1: %+v", len(plans), plans)
			}
			p := plans[0]
			if p.Command != tc.wantCmd {
				t.Errorf("Command = %q, want %q", p.Command, tc.wantCmd)
			}
			// PlanDeps sets Skip to a uv-missing reason when uv is off PATH.
			if uvPresent() {
				if p.Skip != "" {
					t.Errorf("uv present: Skip = %q, want empty", p.Skip)
				}
			} else {
				if !strings.Contains(p.Skip, "uv") {
					t.Errorf("uv absent: Skip = %q, want mention of uv", p.Skip)
				}
			}
		})
	}
}

func TestPlanDepsManifestlessVenvSkips(t *testing.T) {
	source := t.TempDir()
	w := t.TempDir()
	writeFile(t, filepath.Join(source, ".venv", "pyvenv.cfg"), "home = /x")
	// No uv.lock / pyproject.toml / requirements.txt beside it.

	plans := check.Doctor{}.PlanDeps(
		check.Worktree{Root: w},
		check.Worktree{Root: source},
		check.DefaultDepRules(),
	)
	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1: %+v", len(plans), plans)
	}
	p := plans[0]
	if p.Skip == "" || !strings.Contains(p.Skip, "manifest") {
		t.Errorf("Skip = %q, want non-empty mentioning manifest", p.Skip)
	}
}

func TestPlanDepsIdempotentSkip(t *testing.T) {
	source := t.TempDir()
	w := t.TempDir()
	writeFile(t, filepath.Join(source, "node_modules", "pkg.js"), "x")
	// Target already has node_modules → no plan for it.
	writeFile(t, filepath.Join(w, "node_modules", "existing.js"), "y")

	plans := check.Doctor{}.PlanDeps(
		check.Worktree{Root: w},
		check.Worktree{Root: source},
		check.DefaultDepRules(),
	)
	if len(plans) != 0 {
		t.Errorf("got %d plans, want 0 (target already provisioned): %+v", len(plans), plans)
	}
}

func TestPlanDepsNewLanguageRuleViaMerge(t *testing.T) {
	source := t.TempDir()
	w := t.TempDir()
	writeFile(t, filepath.Join(source, "vendor", "bundle", "gem.rb"), "puts 1")

	rules := config.Merge(
		check.DefaultDepRules(),
		[]check.DepRule{{Name: "vendor/bundle", Action: check.Clone}},
		func(r check.DepRule) string { return r.Name },
	)
	plans := check.Doctor{}.PlanDeps(check.Worktree{Root: w}, check.Worktree{Root: source}, rules)

	wantDst := filepath.Join(w, "vendor", "bundle")
	p, ok := findPlan(plans, wantDst)
	if !ok {
		t.Fatalf("no plan for vendor/bundle, got: %+v", plans)
	}
	if p.Action != check.Clone {
		t.Errorf("Action = %v, want Clone", p.Action)
	}
	if eval(t, filepath.Dir(p.Src)) != eval(t, filepath.Join(source, "vendor")) {
		t.Errorf("Src = %q, want under <source>/vendor", p.Src)
	}
}
