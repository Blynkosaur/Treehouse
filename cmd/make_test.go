package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// thBin is the built `th` binary, compiled once in TestMain.
var thBin string

// raceBuild is set by race_test.go when the suite itself is built with -race.
var raceBuild bool

func TestMain(m *testing.M) {
	// Skip the whole suite gracefully if the toolchain isn't here; the run()
	// helper re-checks per test so `go test` still reports skips, not failures.
	if _, err := exec.LookPath("go"); err != nil {
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "th-bin")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	repoRoot, err := filepath.Abs("..") // test cwd is cmd/; module root is its parent
	if err != nil {
		panic(err)
	}
	thBin = filepath.Join(dir, "th")
	args := []string{"build", "-o", thBin, "."}
	if raceBuild {
		args = append([]string{"build", "-race"}, args[1:]...)
	}
	build := exec.Command("go", args...)
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		panic("build failed: " + err.Error() + "\n" + string(out))
	}
	os.Exit(m.Run())
}

// runMakeIn runs `th make` in dir and returns combined output plus whether it
// exited 0.
func runMakeIn(t *testing.T, dir string) (string, bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	cmd := exec.Command(thBin, "make")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestMake(t *testing.T) {
	t.Run("generate blanks values", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "svc_a", ".env"), "PORT=3000\nDB_URL=secret\n")

		out, ok := runMakeIn(t, dir)
		if !ok {
			t.Fatalf("expected exit 0, output:\n%s", out)
		}
		if !strings.Contains(out, "wrote .env.example") {
			t.Errorf("missing success line in output:\n%s", out)
		}

		got, err := os.ReadFile(filepath.Join(dir, "svc_a", ".env.example"))
		if err != nil {
			t.Fatalf(".env.example not created: %v", err)
		}
		body := string(got)
		for _, key := range []string{"DB_URL=", "PORT="} {
			if !strings.Contains(body, key) {
				t.Errorf("expected key %q in example, got:\n%s", key, body)
			}
		}
		// The whole point: no values, and certainly no secret, leaked through.
		if strings.Contains(body, "secret") || strings.Contains(body, "3000") {
			t.Errorf("example leaked a value:\n%s", body)
		}
		for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
			if _, val, _ := strings.Cut(line, "="); val != "" {
				t.Errorf("expected empty value, got line %q", line)
			}
		}
	})

	t.Run("skip existing example", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "svc_b", ".env"), "PORT=3000\n")
		examplePath := filepath.Join(dir, "svc_b", ".env.example")
		original := "# hand written, do not touch\nPORT=9999\n"
		write(t, examplePath, original)

		out, ok := runMakeIn(t, dir)
		if !ok {
			t.Fatalf("expected exit 0, output:\n%s", out)
		}
		if !strings.Contains(out, "already exists, skipped") {
			t.Errorf("expected skip line, got:\n%s", out)
		}
		got, err := os.ReadFile(examplePath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != original {
			t.Errorf("existing example was overwritten:\n%s", got)
		}
	})

	t.Run("fallback to main worktree", func(t *testing.T) {
		main := t.TempDir()
		gitInit(t, main)
		write(t, filepath.Join(main, "svc_a", ".env"), "PORT=3000\n")
		write(t, filepath.Join(main, "svc_b", ".env"), "DB_URL=secret\n")
		git(t, main, "add", "-A", "-f") // -f: .env may be gitignored elsewhere; not here, but explicit
		git(t, main, "commit", "-m", "init")

		linked := filepath.Join(t.TempDir(), "linked")
		git(t, main, "worktree", "add", linked)

		// Empty the linked worktree of all env files so make must fall back.
		for _, svc := range []string{"svc_a", "svc_b"} {
			os.Remove(filepath.Join(linked, svc, ".env"))
			os.Remove(filepath.Join(linked, svc, ".env.example"))
		}

		out, ok := runMakeIn(t, linked)
		if !ok {
			t.Fatalf("expected exit 0, output:\n%s", out)
		}
		for _, svc := range []string{"svc_a", "svc_b"} {
			got, err := os.ReadFile(filepath.Join(linked, svc, ".env.example"))
			if err != nil {
				t.Fatalf("%s/.env.example not created from main: %v\noutput:\n%s", svc, err, out)
			}
			body := string(got)
			if strings.Contains(body, "secret") || strings.Contains(body, "3000") {
				t.Errorf("%s example leaked a value:\n%s", svc, body)
			}
		}
	})

	t.Run("nothing anywhere", func(t *testing.T) {
		dir := t.TempDir() // plain, non-git, no env files
		out, ok := runMakeIn(t, dir)
		if !ok {
			t.Fatalf("expected exit 0, output:\n%s", out)
		}
		if !strings.Contains(out, "no .env files found here or in the main worktree") {
			t.Errorf("expected honest empty message, got:\n%s", out)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Errorf("expected no files created, found: %v", entries)
		}
	})
}
