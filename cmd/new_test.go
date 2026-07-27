package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runTh runs the built binary in dir and returns combined output plus exit-0-ness.
func runTh(t *testing.T, dir string, args ...string) (string, bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	cmd := exec.Command(thBin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// mainRepo builds a one-commit repo with a root .env holding two packed ports
// and a compose file, which is the shape every derive assertion below needs.
func mainRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	write(t, filepath.Join(dir, "compose.yml"), "services: {}\n")
	write(t, filepath.Join(dir, ".env"), "PORT=3000\nADMIN_PORT=3001\nDB_URL=postgres://local\n")
	write(t, filepath.Join(dir, ".env.example"), "PORT=\nADMIN_PORT=\nDB_URL=\n")
	git(t, dir, "add", "-A", "-f")
	git(t, dir, "commit", "-m", "init")
	return dir
}

func TestNew(t *testing.T) {
	t.Run("born ready", func(t *testing.T) {
		main := mainRepo(t)

		out, ok := runTh(t, main, "new", "feat/login", "--skip-deps")
		if !ok {
			t.Fatalf("expected exit 0, output:\n%s", out)
		}

		// Placement: sibling of main, never inside it (a nested worktree would
		// be indexed as a service of main).
		wt := filepath.Join(filepath.Dir(main), "app-"+"feat_login_d668f0")
		if _, err := os.Stat(wt); err != nil {
			t.Fatalf("worktree not at the sibling path %s: %v\noutput:\n%s", wt, err, out)
		}

		body := readEnv(t, filepath.Join(wt, ".env"))
		vars := parseEnv(body)

		// Phase 1: canonical fill.
		if vars["DB_URL"] != "postgres://local" {
			t.Errorf("DB_URL not filled from main: %q\n%s", vars["DB_URL"], body)
		}
		// Phase 3: derive — must run on a .env phase 1 only just created.
		if vars["COMPOSE_PROJECT_NAME"] != "app_feat_login_d668f0" {
			t.Errorf("COMPOSE_PROJECT_NAME = %q\n%s", vars["COMPOSE_PROJECT_NAME"], body)
		}
		if vars["PORT"] == "3000" || vars["PORT"] == "" {
			t.Errorf("PORT not offset from main's 3000: %q\n%s", vars["PORT"], body)
		}
		// Spacing between services survives the shift.
		if atoi(t, vars["ADMIN_PORT"])-atoi(t, vars["PORT"]) != 1 {
			t.Errorf("PORT/ADMIN_PORT spacing lost: %s / %s", vars["PORT"], vars["ADMIN_PORT"])
		}

		// Re-hydrating is a no-op on the derived values: same branch, same ports.
		if out, ok := runTh(t, wt, "hydrate", "--skip-deps"); !ok {
			t.Fatalf("second hydrate failed:\n%s", out)
		}
		if again := parseEnv(readEnv(t, filepath.Join(wt, ".env"))); again["PORT"] != vars["PORT"] {
			t.Errorf("ports moved on re-hydrate: %s -> %s", vars["PORT"], again["PORT"])
		}
	})

	t.Run("second worktree gets different ports", func(t *testing.T) {
		main := mainRepo(t)
		if out, ok := runTh(t, main, "new", "one", "--skip-deps"); !ok {
			t.Fatalf("%s", out)
		}
		if out, ok := runTh(t, main, "new", "two", "--skip-deps"); !ok {
			t.Fatalf("%s", out)
		}
		one := parseEnv(readEnv(t, filepath.Join(filepath.Dir(main), "app-one", ".env")))
		two := parseEnv(readEnv(t, filepath.Join(filepath.Dir(main), "app-two", ".env")))
		if one["PORT"] == two["PORT"] {
			t.Errorf("two worktrees landed on the same PORT %q", one["PORT"])
		}
	})

	t.Run("branch already checked out", func(t *testing.T) {
		main := mainRepo(t)
		if out, ok := runTh(t, main, "new", "taken", "--skip-deps"); !ok {
			t.Fatalf("%s", out)
		}
		out, ok := runTh(t, main, "new", "taken", "--skip-deps")
		if ok {
			t.Fatalf("expected failure for an already-checked-out branch:\n%s", out)
		}
		if !strings.Contains(out, "already checked out at") {
			t.Errorf("error should name the owning worktree, got:\n%s", out)
		}
		if strings.Contains(out, "Usage:") {
			t.Errorf("runtime error dumped usage into stdout:\n%s", out)
		}
	})

	// L1's open hook. The command runs with the WORKTREE as cwd — an editor
	// opened on the main checkout is the one mistake this feature can make that
	// looks like it worked.
	t.Run("open runs in the new worktree", func(t *testing.T) {
		main := mainRepo(t)
		write(t, filepath.Join(main, "treehouse.toml"), "[open]\ncommand = \"pwd > opened.txt\"\n")

		out, ok := runTh(t, main, "new", "openme", "--skip-deps")
		if !ok {
			t.Fatalf("expected exit 0:\n%s", out)
		}
		wt := filepath.Join(filepath.Dir(main), "app-openme")
		got, err := os.ReadFile(filepath.Join(wt, "opened.txt"))
		if err != nil {
			t.Fatalf("the [open] command did not run in the worktree: %v\n%s", err, out)
		}
		if !strings.Contains(string(got), filepath.Base(wt)) {
			t.Errorf("[open] ran in %q, want the worktree %s", strings.TrimSpace(string(got)), wt)
		}
	})

	t.Run("--no-open wins over a configured command", func(t *testing.T) {
		main := mainRepo(t)
		write(t, filepath.Join(main, "treehouse.toml"), "[open]\ncommand = \"pwd > opened.txt\"\n")

		out, ok := runTh(t, main, "new", "quiet", "--skip-deps", "--no-open")
		if !ok {
			t.Fatalf("%s", out)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(main), "app-quiet", "opened.txt")); err == nil {
			t.Error("--no-open still ran the open command")
		}
	})

	// "Born ready" means never handing somebody a worktree that is not.
	t.Run("a failing doctor is not handed to an editor", func(t *testing.T) {
		main := mainRepo(t)
		// SECRET is expected but nowhere to be sourced from, so hydrate writes it
		// empty and the curated required list turns that into a FAIL. Committed,
		// because the new worktree checks its reference out of git.
		write(t, filepath.Join(main, ".env.example"), "PORT=\nADMIN_PORT=\nDB_URL=\nSECRET=\n")
		git(t, main, "commit", "-am", "expect a secret")
		write(t, filepath.Join(main, "treehouse.toml"),
			"[env]\nrequired = [\"SECRET\"]\n\n[open]\ncommand = \"pwd > opened.txt\"\n")

		out, ok := runTh(t, main, "new", "broken", "--skip-deps")
		if ok {
			t.Fatalf("expected exit 2 for a missing required key:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(main), "app-broken", "opened.txt")); err == nil {
			t.Error("handed over a worktree doctor called broken")
		}
		if !strings.Contains(out, "not handing over a broken worktree") {
			t.Errorf("the skip should say why:\n%s", out)
		}
	})

	t.Run("explicit path", func(t *testing.T) {
		main := mainRepo(t)
		want := filepath.Join(t.TempDir(), "elsewhere")
		if out, ok := runTh(t, main, "new", "custom", "--path", want, "--skip-deps"); !ok {
			t.Fatalf("%s", out)
		}
		if _, err := os.Stat(filepath.Join(want, ".env")); err != nil {
			t.Errorf("worktree not created at --path: %v", err)
		}
	})
}

func readEnv(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func parseEnv(body string) map[string]string {
	vars := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			vars[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return vars
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not a number: %q", s)
	}
	return n
}
