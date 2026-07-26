package check

import (
	"reflect"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

// CheckEnv operates on in-memory Worktrees — no disk, no binary, no stdout.
// This is the payoff of returning data instead of printing it.
func TestCheckEnv(t *testing.T) {
	wt := Worktree{
		Root: "/repo",
		EnvFiles: []envfile.File{
			// service a: one key missing, one empty, one fine
			{Path: "/repo/a/.env", Vars: map[string]string{"OK": "1", "EMPTY": ""}},
			{Path: "/repo/a/.env.example", Vars: map[string]string{"OK": "", "EMPTY": "", "GONE": ""}},
			// service b: fully healthy
			{Path: "/repo/b/.env", Vars: map[string]string{"X": "1"}},
			{Path: "/repo/b/.env.example", Vars: map[string]string{"X": ""}},
			// service c: example exists, .env missing entirely
			{Path: "/repo/c/.env.example", Vars: map[string]string{"A": "", "B": ""}},
			// service d: .env with no example — must produce NO finding
			{Path: "/repo/d/.env", Vars: map[string]string{"LONER": "1"}},
		},
	}

	// No source worktree → .env.example is the only reference (old behavior).
	findings := Doctor{}.CheckEnv(wt, Worktree{})

	if len(findings) != 3 {
		t.Fatalf("got %d findings, want 3 (a, b, c — d has no example): %+v", len(findings), findings)
	}

	a, b, c := findings[0], findings[1], findings[2] // sorted by dir

	if !reflect.DeepEqual(a.Missing, []string{"GONE"}) || !reflect.DeepEqual(a.Empty, []string{"EMPTY"}) {
		t.Errorf("service a: Missing=%v Empty=%v, want Missing=[GONE] Empty=[EMPTY]", a.Missing, a.Empty)
	}
	if !a.Drifted() {
		t.Error("service a should be drifted")
	}

	if b.Drifted() {
		t.Errorf("service b should be healthy, got %+v", b)
	}

	if !c.NoEnv || c.Keys != 2 {
		t.Errorf("service c: NoEnv=%v Keys=%d, want NoEnv=true Keys=2", c.NoEnv, c.Keys)
	}
}

// TestCheckEnvMainFallback: a repo with no .env.example anywhere. The main
// checkout's .env is the reference for which keys should exist.
func TestCheckEnvMainFallback(t *testing.T) {
	wt := Worktree{
		Root: "/wt",
		EnvFiles: []envfile.File{
			// svc_a: has .env but is missing DB_URL that main defines
			{Path: "/wt/svc_a/.env", Vars: map[string]string{"PORT": "3000"}},
			// svc_b: no .env at all — main has one, so this is NoEnv
		},
	}
	source := Worktree{
		Root: "/main",
		EnvFiles: []envfile.File{
			{Path: "/main/svc_a/.env", Vars: map[string]string{"PORT": "3000", "DB_URL": "postgres://x"}},
			{Path: "/main/svc_b/.env", Vars: map[string]string{"TOKEN": "abc"}},
		},
	}

	findings := Doctor{}.CheckEnv(wt, source)
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2 (svc_a drift, svc_b NoEnv): %+v", len(findings), findings)
	}
	a, b := findings[0], findings[1] // dir-sorted: svc_a, svc_b

	if !reflect.DeepEqual(a.Missing, []string{"DB_URL"}) {
		t.Errorf("svc_a Missing = %v, want [DB_URL] (from main, no .env.example)", a.Missing)
	}
	if !b.NoEnv || !reflect.DeepEqual(b.Missing, []string{"TOKEN"}) {
		t.Errorf("svc_b: NoEnv=%v Missing=%v, want NoEnv=true Missing=[TOKEN]", b.NoEnv, b.Missing)
	}
}

func TestCheckEnvRequiredFails(t *testing.T) {
	// Inferred drift is a warning; a key the human listed as required is the
	// only thing that turns the same drift into a failure.
	wt := Worktree{
		Root: "/wt",
		EnvFiles: []envfile.File{
			{Path: "/wt/svc/.env", Vars: map[string]string{"A": "1", "EMPTY": ""}},
			{Path: "/wt/svc/.env.example", Vars: map[string]string{"A": "", "EMPTY": "", "GONE": ""}},
		},
	}

	inferred := Doctor{}.CheckEnv(wt, Worktree{})
	if len(inferred) != 1 || inferred[0].Fails() {
		t.Fatalf("no config: drift must stay a warning, got %+v", inferred)
	}

	curated := Doctor{Required: []string{"GONE", "EMPTY", "UNRELATED"}}.CheckEnv(wt, Worktree{})
	if len(curated) != 1 {
		t.Fatalf("got %d findings, want 1", len(curated))
	}
	if !curated[0].Fails() {
		t.Fatal("a missing required key must fail")
	}
	// Both a missing and an empty required key count; a required key that isn't
	// drifted does not.
	if !reflect.DeepEqual(curated[0].Failed, []string{"EMPTY", "GONE"}) {
		t.Errorf("Failed = %v, want [EMPTY GONE]", curated[0].Failed)
	}
}
