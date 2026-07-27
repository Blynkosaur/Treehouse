package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A miniature birdseye: two services, decoy variants, a node_modules trap.
	write("seo_service/.env", "A=1")
	write("seo_service/.env.example", "A=\nB=")
	write("seo_service/.env.backup.20260622", "OLD=1") // exact-name rule: NOT collected
	write("seo_service/.env.dev", "DEV=1")             // exact-name rule: NOT collected
	write("seo_webapp/.env", "C=3")
	write("node_modules/pkg/.env", "EVIL=1") // pruned subtree: NOT collected
	write("README.md", "not an env file")

	wt, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(wt.EnvFiles) != 3 {
		var found []string
		for _, f := range wt.EnvFiles {
			found = append(found, f.Path)
		}
		t.Errorf("found %d env files, want 3:\n%s", len(wt.EnvFiles), strings.Join(found, "\n"))
	}
	for _, f := range wt.EnvFiles {
		if strings.Contains(f.Path, "node_modules") {
			t.Errorf("walker failed to skip node_modules: %s", f.Path)
		}
	}

	// Spot-check that files were actually parsed, not just located.
	for _, f := range wt.EnvFiles {
		if filepath.Base(f.Path) == ".env" && strings.Contains(f.Path, "seo_service") {
			if f.Vars["A"] != "1" {
				t.Errorf("seo_service/.env not parsed: Vars=%v", f.Vars)
			}
		}
	}
}

// TestDiscoverSkipsNestedCheckouts: a worktree may live INSIDE the main
// checkout — this repo keeps its own under .claude/worktrees/ — and a submodule
// always does. Walking into one makes every nested checkout look like a service
// of main's, and hydrate then materialises that phantom service in every OTHER
// worktree: main's real secrets written into a directory nobody asked for.
func TestDiscoverSkipsNestedCheckouts(t *testing.T) {
	root := t.TempDir()
	for _, f := range []struct{ rel, content string }{
		{".env", "A=1"},
		{"svc/.env", "B=2"},                       // an ordinary subdirectory: collected
		{"wt/feat/.git", "gitdir: /elsewhere"},    // a worktree's gitfile
		{"wt/feat/.env", "SECRET=leaked"},         // not ours
		{"sub/.git/HEAD", "ref: refs/heads/main"}, // a nested clone's .git DIR
		{"sub/.env", "ALSO=leaked"},
	} {
		path := filepath.Join(root, f.rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	wt, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var got []string
	for _, f := range wt.EnvFiles {
		got = append(got, f.Path)
		if strings.Contains(f.Path, "wt/feat") || strings.Contains(f.Path, "sub/") {
			t.Errorf("walked into another checkout: %s", f.Path)
		}
	}
	if len(got) != 2 {
		t.Errorf("found %d env files, want 2 (root and svc):\n%s", len(got), strings.Join(got, "\n"))
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	_, err := Discover(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Error("expected error for nonexistent root, got nil")
	}
}

// realpath resolves symlinks so tmp-path comparisons survive macOS's
// /var -> /private/var redirection (git reports the resolved path).
func realpath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", p, err)
	}
	return resolved
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestMainWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	main := t.TempDir()
	git(t, main, "init")
	git(t, main, "config", "user.email", "test@example.com")
	git(t, main, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, main, "add", ".")
	git(t, main, "commit", "-m", "initial")

	// A linked worktree living outside the main checkout.
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, main, "worktree", "add", linked)

	wantMain := realpath(t, main)

	// AC1: from a linked worktree, we still get the MAIN worktree.
	fromLinked, err := MainWorktree(linked)
	if err != nil {
		t.Fatalf("MainWorktree(linked): %v", err)
	}
	if realpath(t, fromLinked) != wantMain {
		t.Errorf("from linked: got %q, want main %q", fromLinked, wantMain)
	}

	// AC2: from the main worktree, we get the main worktree.
	fromMain, err := MainWorktree(main)
	if err != nil {
		t.Fatalf("MainWorktree(main): %v", err)
	}
	if realpath(t, fromMain) != wantMain {
		t.Errorf("from main: got %q, want main %q", fromMain, wantMain)
	}

	// AC4: the returned path exists and is a directory.
	if info, err := os.Stat(fromMain); err != nil || !info.IsDir() {
		t.Errorf("returned path not a directory: stat err=%v", err)
	}

	// AC3: a plain non-git dir errors, doesn't panic.
	if _, err := MainWorktree(t.TempDir()); err == nil {
		t.Error("expected error for non-git dir, got nil")
	}
}

func TestParseWorktrees(t *testing.T) {
	// Real `git worktree list --porcelain` shapes: normal, detached, bare,
	// locked-with-reason, prunable-with-reason.
	const output = `worktree /repo
HEAD 1111111111111111111111111111111111111111
branch refs/heads/main

worktree /repo-feat
HEAD 2222222222222222222222222222222222222222
branch refs/heads/feat/login

worktree /repo-detached
HEAD 3333333333333333333333333333333333333333
detached

worktree /repo-bare
bare

worktree /repo-locked
HEAD 4444444444444444444444444444444444444444
branch refs/heads/wip
locked on removable media
prunable gitdir file points to non-existent location

`
	want := []Ref{
		{Path: "/repo", Branch: "main", Head: "1111111111111111111111111111111111111111"},
		{Path: "/repo-feat", Branch: "feat/login", Head: "2222222222222222222222222222222222222222"},
		{Path: "/repo-detached", Head: "3333333333333333333333333333333333333333", Detached: true},
		{Path: "/repo-bare", Bare: true},
		{Path: "/repo-locked", Branch: "wip", Head: "4444444444444444444444444444444444444444", Locked: true, Prunable: true},
	}
	if got := parseWorktrees(output); !reflect.DeepEqual(got, want) {
		t.Errorf("parseWorktrees()\n  got:  %+v\n  want: %+v", got, want)
	}
}

func TestEnvVarsByDir(t *testing.T) {
	wt := Worktree{
		Root: "/wt",
		EnvFiles: []envfile.File{
			// svc_a: real .env → indexed under "svc_a"
			{Path: "/wt/svc_a/.env", Vars: map[string]string{"PORT": "3000", "DB": "x"}},
			// svc_a also has an example — must not overwrite or add noise
			{Path: "/wt/svc_a/.env.example", Vars: map[string]string{"PORT": "", "DB": ""}},
			// root-level .env → key "."
			{Path: "/wt/.env", Vars: map[string]string{"ROOT": "1"}},
			// svc_b: only an example → NO entry (AC1)
			{Path: "/wt/svc_b/.env.example", Vars: map[string]string{"A": ""}},
		},
	}

	got := wt.EnvVarsByDir()

	want := map[string]map[string]string{
		"svc_a": {"PORT": "3000", "DB": "x"}, // AC3: values, not just keys
		".":     {"ROOT": "1"},               // AC2: root-level → "."
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EnvVarsByDir() = %v, want %v", got, want)
	}
	if _, ok := got["svc_b"]; ok { // AC1: example-only dir excluded
		t.Error("svc_b has only .env.example but was indexed")
	}
}

func TestEnvVarsByDirEmpty(t *testing.T) {
	// AC4: no real .env files → non-nil map of length 0.
	wt := Worktree{
		Root: "/wt",
		EnvFiles: []envfile.File{
			{Path: "/wt/svc/.env.example", Vars: map[string]string{"A": ""}},
		},
	}
	got := wt.EnvVarsByDir()
	if got == nil {
		t.Fatal("EnvVarsByDir() returned nil, want non-nil empty map")
	}
	if len(got) != 0 {
		t.Errorf("EnvVarsByDir() len = %d, want 0", len(got))
	}
}

// TestComposeProjects is L3's teardown guard. The dangerous case is the third
// one: a half-hydrated worktree still carrying main's project name, where
// tearing "its" project down would stop the containers somebody is working in.
func TestComposeProjects(t *testing.T) {
	at := func(root string, byDir map[string]map[string]string) Worktree {
		w := Worktree{Root: root}
		for rel, vars := range byDir {
			w.EnvFiles = append(w.EnvFiles, envfile.File{
				Path: filepath.Join(root, rel, ".env"), Vars: vars,
			})
		}
		return w
	}
	main := at("/main", map[string]map[string]string{
		".":   {"COMPOSE_PROJECT_NAME": "app"},
		"api": {"COMPOSE_PROJECT_NAME": "app"},
	})

	cases := []struct {
		name string
		wt   Worktree
		want []string
	}{
		{
			name: "the worktree's own project, once, however many dirs declare it",
			wt: at("/w", map[string]map[string]string{
				".":   {"COMPOSE_PROJECT_NAME": "app_feat_a"},
				"api": {"COMPOSE_PROJECT_NAME": "app_feat_a"},
			}),
			want: []string{"app_feat_a"},
		},
		{
			name: "two compose roots that really did get two projects",
			wt: at("/w", map[string]map[string]string{
				".":   {"COMPOSE_PROJECT_NAME": "app_feat_a"},
				"api": {"COMPOSE_PROJECT_NAME": "api_feat_a"},
			}),
			want: []string{"api_feat_a", "app_feat_a"},
		},
		{
			name: "never main's, however it got there",
			wt:   at("/w", map[string]map[string]string{".": {"COMPOSE_PROJECT_NAME": "app"}}),
		},
		{
			name: "a worktree that never hydrated declares nothing",
			wt:   at("/w", map[string]map[string]string{".": {"PORT": "3000"}}),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ComposeProjects(c.wt, main)
			if len(got) == 0 && len(c.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ComposeProjects = %v, want %v", got, c.want)
			}
		})
	}
}
