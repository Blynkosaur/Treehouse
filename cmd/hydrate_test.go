package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runSplit is runCode with stdout and stderr kept apart, because --json's
// contract is about stdout specifically.
func runSplit(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	cmd := exec.Command(thBin, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("running th: %v", err)
	}
	return out.String(), errb.String(), code
}

// gitignoredEnvRepo is the layout treehouse actually exists for: .env is
// gitignored, so a fresh worktree starts with NO env files at all and every
// value has to come from hydrate.
func gitignoredEnvRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".gitignore"), ".env\n")
	write(t, filepath.Join(dir, "compose.yml"), "services: {}\n")
	write(t, filepath.Join(dir, ".env.example"), "PORT=\nADMIN_PORT=\nDB_URL=\n")
	write(t, filepath.Join(dir, ".env"), "PORT=3000\nADMIN_PORT=3001\nDB_URL=postgres://local\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "init")
	return dir
}

// TestNewHydratesFromNothing is the stale-Discover regression: `wt` is walked
// BEFORE the fill phase writes .env, so a derive that doesn't re-Discover finds
// no file to rewrite and silently no-ops on exactly the happy path.
func TestNewHydratesFromNothing(t *testing.T) {
	main := gitignoredEnvRepo(t)
	out, _, code := runSplit(t, main, "new", "feat/login", "--skip-deps")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wt := filepath.Join(filepath.Dir(main), "app-feat_login_d668f0")

	body := readEnv(t, filepath.Join(wt, ".env"))
	vars := parseEnv(body)
	if vars["DB_URL"] != "postgres://local" {
		t.Errorf("fill phase: DB_URL = %q\n%s", vars["DB_URL"], body)
	}
	if vars["COMPOSE_PROJECT_NAME"] != "app_feat_login_d668f0" {
		t.Errorf("derive phase no-opped on a just-created .env: COMPOSE_PROJECT_NAME = %q\n%s",
			vars["COMPOSE_PROJECT_NAME"], body)
	}
	if vars["PORT"] == "" || vars["PORT"] == "3000" {
		t.Errorf("derive phase left PORT at main's value: %q\n%s", vars["PORT"], body)
	}
	if atoi(t, vars["ADMIN_PORT"])-atoi(t, vars["PORT"]) != 1 {
		t.Errorf("service spacing lost: %s / %s", vars["PORT"], vars["ADMIN_PORT"])
	}

	// And the file is a fixed point: re-hydrating must not produce a diff.
	before := readEnv(t, filepath.Join(wt, ".env"))
	for i := 0; i < 3; i++ {
		if out, _, code := runSplit(t, wt, "hydrate", "--skip-deps"); code != 0 {
			t.Fatalf("re-hydrate %d: exit %d\n%s", i, code, out)
		}
	}
	if after := readEnv(t, filepath.Join(wt, ".env")); after != before {
		t.Errorf("hydrate is not a fixed point\n  before: %q\n  after:  %q", before, after)
	}
}

// TestHydrateInMainIsInert: main IS the base. Deriving there rewrites the values
// every other worktree offsets from — ports walked one offset further up per run
// and COMPOSE_PROJECT_NAME grew another "_main" each time.
func TestHydrateInMainIsInert(t *testing.T) {
	main := gitignoredEnvRepo(t)
	before := readEnv(t, filepath.Join(main, ".env"))

	for i := 0; i < 3; i++ {
		if out, _, code := runSplit(t, main, "hydrate", "--skip-deps"); code != 0 {
			t.Fatalf("hydrate %d: exit %d\n%s", i, code, out)
		}
	}
	if after := readEnv(t, filepath.Join(main, ".env")); after != before {
		t.Errorf("hydrate rewrote the canonical .env — every worktree derives from this file\n  before: %q\n  after:  %q", before, after)
	}
}

// TestHydrateFromSubdirectory: every command took os.Getwd as the worktree
// root, so running hydrate from svc_a planned main's svc_a/.env at
// svc_a/svc_a/.env — a second env file nothing loads, and the real one left
// unhydrated. The root is git's answer, not the shell's.
func TestHydrateFromSubdirectory(t *testing.T) {
	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, "svc_a", ".env"), "TOKEN=abc\n")

	if out, _, code := runSplit(t, main, "new", "feat", "--skip-deps"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wt := filepath.Join(filepath.Dir(main), "app-feat")
	// Wipe the worktree's copy so this hydrate has real work to do, then run it
	// from the subdirectory rather than the root.
	if err := os.Remove(filepath.Join(wt, "svc_a", ".env")); err != nil {
		t.Fatal(err)
	}
	if out, _, code := runSplit(t, filepath.Join(wt, "svc_a"), "hydrate", "--skip-deps"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}

	if got := parseEnv(readEnv(t, filepath.Join(wt, "svc_a", ".env")))["TOKEN"]; got != "abc" {
		t.Errorf("svc_a/.env TOKEN = %q, want abc", got)
	}
	if _, err := os.Stat(filepath.Join(wt, "svc_a", "svc_a")); err == nil {
		t.Error("hydrate treated the subdirectory as the worktree root: svc_a/svc_a exists")
	}
}

// TestHydrateCreatesMissingDirs: a gitignored dir holding nothing but a .env
// (secrets/, infra/) does not exist in a fresh checkout. Aborting there left the
// worktree half-built with exit 1.
func TestHydrateCreatesMissingDirs(t *testing.T) {
	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, ".gitignore"), ".env\nsecrets/\n")
	write(t, filepath.Join(main, "secrets", ".env"), "TOKEN=abc\n")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-m", "ignore secrets")

	out, _, code := runSplit(t, main, "new", "feat", "--skip-deps")
	if code != 0 {
		t.Fatalf("exit %d — a service dir main only holds a .env for:\n%s", code, out)
	}
	wt := filepath.Join(filepath.Dir(main), "app-feat")
	if got := parseEnv(readEnv(t, filepath.Join(wt, "secrets", ".env")))["TOKEN"]; got != "abc" {
		t.Errorf("secrets/.env TOKEN = %q, want abc", got)
	}
}

// TestHydrateWithoutADatabase is the orphan-prevention strategy, end to end: a
// repo that names no database anywhere gets no clone — and, because creation is
// conditional on having somewhere to point the result, never even asks Postgres
// a question. Env fill and deps still run in full. (gitignoredEnvRepo declares
// DB_URL, not DATABASE_URL, so nothing here resolves a template.)
func TestHydrateWithoutADatabase(t *testing.T) {
	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, "node_modules", "left-pad", "index.js"), "//\n")

	out, _, code := runSplit(t, main, "new", "nodb")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wt := filepath.Join(filepath.Dir(main), "app-nodb")

	if strings.Contains(out, "cloned "+"app") || strings.Contains(out, "createdb") {
		t.Errorf("a repo with no database got one anyway:\n%s", out)
	}
	if !strings.Contains(out, "nothing pointing at it") {
		t.Errorf("the db phase should say why it did nothing:\n%s", out)
	}
	if strings.Contains(out, "postgres is not reachable") {
		t.Errorf("shelled out to psql for a repo that declares no database:\n%s", out)
	}

	vars := parseEnv(readEnv(t, filepath.Join(wt, ".env")))
	for _, key := range []string{"DATABASE_URL", "POSTGRES_DB"} {
		if _, ok := vars[key]; ok {
			t.Errorf("%s written with no database behind it: %q", key, vars[key])
		}
	}
	// The other phases are untouched by any of this.
	if vars["DB_URL"] != "postgres://local" {
		t.Errorf("env fill did not run: DB_URL = %q", vars["DB_URL"])
	}
	if vars["COMPOSE_PROJECT_NAME"] != "app_nodb" {
		t.Errorf("derive did not run: COMPOSE_PROJECT_NAME = %q", vars["COMPOSE_PROJECT_NAME"])
	}
	if _, err := os.Stat(filepath.Join(wt, "node_modules", "left-pad", "index.js")); err != nil {
		t.Errorf("deps did not run: %v\n%s", err, out)
	}
}

func TestNewBranchResolution(t *testing.T) {
	t.Run("no origin at all", func(t *testing.T) {
		main := gitignoredEnvRepo(t)
		out, _, code := runSplit(t, main, "new", "offline", "--skip-deps")
		if code != 0 {
			t.Fatalf("a repo with no remote must still work offline: exit %d\n%s", code, out)
		}
	})

	t.Run("origin configured but unreachable", func(t *testing.T) {
		main := gitignoredEnvRepo(t)
		git(t, main, "remote", "add", "origin", filepath.Join(t.TempDir(), "nope.git"))
		out, _, code := runSplit(t, main, "new", "offline2", "--skip-deps")
		if code != 0 {
			t.Fatalf("a failed fetch must be a warning, not a failure: exit %d\n%s", code, out)
		}
		if !strings.Contains(out, "could not fetch origin") {
			t.Errorf("the fetch failure was not reported:\n%s", out)
		}
	})

	t.Run("branch only on origin is tracked", func(t *testing.T) {
		upstream := gitignoredEnvRepo(t)
		git(t, upstream, "checkout", "-q", "-b", "remote-only")
		git(t, upstream, "commit", "--allow-empty", "-m", "x")
		git(t, upstream, "checkout", "-q", "-")

		clone := filepath.Join(t.TempDir(), "app")
		git(t, upstream, "clone", "-q", upstream, clone)
		git(t, clone, "config", "user.email", "test@example.com")
		git(t, clone, "config", "user.name", "test")

		out, _, code := runSplit(t, clone, "new", "remote-only", "--skip-deps")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		wt := filepath.Join(filepath.Dir(clone), "app-remote_only_455b80")
		up, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "@{u}").Output()
		if err != nil {
			t.Fatalf("branch has no upstream: %v\n%s", err, out)
		}
		if strings.TrimSpace(string(up)) != "origin/remote-only" {
			t.Errorf("upstream = %q, want origin/remote-only", strings.TrimSpace(string(up)))
		}
	})

	t.Run("--from picks the base", func(t *testing.T) {
		main := gitignoredEnvRepo(t)
		write(t, filepath.Join(main, "marker.txt"), "here\n")
		git(t, main, "add", "-A")
		git(t, main, "commit", "-m", "second")
		git(t, main, "tag", "base-tag", "HEAD~1")

		out, _, code := runSplit(t, main, "new", "fromtag", "--from", "base-tag", "--skip-deps")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		wt := filepath.Join(filepath.Dir(main), "app-fromtag")
		if _, err := os.Stat(filepath.Join(wt, "marker.txt")); !os.IsNotExist(err) {
			t.Errorf("--from ignored: the worktree has the newer commit's file")
		}
	})

	t.Run("--from that does not exist is exit 1", func(t *testing.T) {
		main := gitignoredEnvRepo(t)
		out, _, code := runSplit(t, main, "new", "bad", "--from", "no-such-ref", "--skip-deps")
		if code != 1 {
			t.Errorf("exit %d, want 1 (treehouse-level error)\n%s", code, out)
		}
	})

	t.Run("target dir exists and is not empty", func(t *testing.T) {
		main := gitignoredEnvRepo(t)
		occupied := filepath.Join(t.TempDir(), "occupied")
		write(t, filepath.Join(occupied, "someone-elses-file"), "x\n")

		out, _, code := runSplit(t, main, "new", "boom", "--path", occupied, "--skip-deps")
		if code == 0 {
			t.Fatalf("clobbered a non-empty directory:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(occupied, "someone-elses-file")); err != nil {
			t.Errorf("the existing file is gone: %v", err)
		}
	})

	t.Run("bare main worktree", func(t *testing.T) {
		upstream := gitignoredEnvRepo(t)
		bare := filepath.Join(t.TempDir(), "app.git")
		git(t, upstream, "clone", "-q", "--bare", upstream, bare)

		out, _, code := runSplit(t, bare, "new", "frombare", "--skip-deps")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if !strings.Contains(out, "bare") {
			t.Errorf("a bare main has no canonical env; say so instead of a silent no-op:\n%s", out)
		}
	})
}

func TestRmRefusals(t *testing.T) {
	t.Run("unpushed commits need --force", func(t *testing.T) {
		upstream := gitignoredEnvRepo(t)
		clone := filepath.Join(t.TempDir(), "app")
		git(t, upstream, "clone", "-q", upstream, clone)
		git(t, clone, "config", "user.email", "test@example.com")
		git(t, clone, "config", "user.name", "test")

		path := filepath.Join(filepath.Dir(clone), "wt-unpushed")
		git(t, clone, "worktree", "add", "-b", "unpushed", path)
		git(t, path, "commit", "--allow-empty", "-m", "local work")

		// A refusal is an error, so it belongs on stderr, not stdout.
		out, errOut, code := runSplit(t, clone, "rm", "unpushed")
		if code == 0 {
			t.Fatalf("dropped a branch with unpushed commits:\n%s", out)
		}
		if !strings.Contains(errOut, "no remote") {
			t.Errorf("refusal should name the reason:\nstdout: %s\nstderr: %s", out, errOut)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("refused but removed anyway: %v", err)
		}
	})

	t.Run("no remote at all is not unpushed", func(t *testing.T) {
		main := gitignoredEnvRepo(t) // no remotes configured
		path := addWorktree(t, main, "local")
		git(t, path, "commit", "--allow-empty", "-m", "local work")

		out, _, code := runSplit(t, main, "rm", "local")
		if code != 0 {
			t.Fatalf("nowhere to push means nothing is at risk: exit %d\n%s", code, out)
		}
	})

	t.Run("the main worktree is not removable", func(t *testing.T) {
		main := gitignoredEnvRepo(t)
		// Stand in a linked worktree so the "you are inside it" guard isn't
		// what does the refusing.
		other := addWorktree(t, main, "elsewhere")
		branch := strings.TrimSpace(gitStdout(t, main, "rev-parse", "--abbrev-ref", "HEAD"))

		out, _, code := runSplit(t, other, "rm", branch)
		if code == 0 {
			t.Fatalf("removed the main worktree:\n%s", out)
		}
		if _, err := os.Stat(filepath.Join(main, ".git")); err != nil {
			t.Errorf("main worktree damaged: %v", err)
		}
	})
}

// TestJSONIsOnlyJSON: an agent parses stdout. Anything a human line leaks onto
// it breaks every consumer, so assert on stdout alone, not combined output.
func TestJSONIsOnlyJSON(t *testing.T) {
	main := gitignoredEnvRepo(t)
	if out, _, code := runSplit(t, main, "new", "feat", "--skip-deps"); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	wt := filepath.Join(filepath.Dir(main), "app-feat")

	for _, c := range []struct {
		name string
		args []string
	}{
		{"doctor clean", []string{"doctor", "--json"}},
		{"ls", []string{"ls", "--json"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			stdout, _, _ := runSplit(t, wt, c.args...)
			var envelope struct {
				Schema   int             `json:"schema"`
				Root     string          `json:"root"`
				Status   string          `json:"status"`
				Findings json.RawMessage `json:"findings"`
			}
			if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
				t.Fatalf("stdout is not clean JSON (%v):\n%s", err, stdout)
			}
			if envelope.Schema != 2 || envelope.Root == "" || envelope.Status == "" {
				t.Errorf("envelope missing schema/root/status: %+v", envelope)
			}
		})
	}

	// findings must be [] and never null: a consumer shouldn't special-case the
	// healthy path.
	stdout, _, _ := runSplit(t, wt, "doctor", "--json")
	if !strings.Contains(stdout, `"findings"`) {
		t.Fatalf("no findings key:\n%s", stdout)
	}
	if strings.Contains(stdout, `"findings": null`) {
		t.Errorf("findings is null, want []:\n%s", stdout)
	}
}

// TestExitCodesAreDistinguishable: 1 means treehouse broke, 2 means the env is
// broken. An agent that can't tell them apart retries the wrong thing.
func TestExitCodesAreDistinguishable(t *testing.T) {
	main := gitignoredEnvRepo(t)

	if _, _, code := runSplit(t, main, "doctor"); code != 0 {
		t.Errorf("healthy repo: exit %d, want 0", code)
	}

	// Drift with no curated list stays a warning.
	write(t, filepath.Join(main, ".env"), "PORT=3000\n")
	if _, _, code := runSplit(t, main, "doctor"); code != 0 {
		t.Errorf("inferred drift: exit %d, want 0", code)
	}

	write(t, filepath.Join(main, "treehouse.toml"), "[env]\nrequired = [\"DB_URL\"]\n")
	stdout, _, code := runSplit(t, main, "doctor", "--json")
	if code != 2 {
		t.Errorf("curated required key missing: exit %d, want 2", code)
	}
	if !strings.Contains(stdout, `"status": "fail"`) {
		t.Errorf("exit 2 without status fail:\n%s", stdout)
	}

	for _, args := range [][]string{
		{"new"},                      // usage
		{"rm", "no-such-branch"},     // no such worktree
		{"doctor", "--no-such-flag"}, // unknown flag
	} {
		if _, _, code := runSplit(t, main, args...); code != 1 {
			t.Errorf("th %v: exit %d, want 1 (treehouse failing, not a FAIL verdict)", args, code)
		}
	}
}

func gitStdout(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// TestNestedWorktreeIsNotMain is the layout this repo itself uses — worktrees
// under .claude/worktrees/ — and it was silently broken end to end. "am I main?"
// asked whether root lived UNDER main, which a nested worktree does, so:
// derive never ran (no compose namespace, no port offset) and, worst, the .env
// was left naming the SHARED database while the clone existed beside it. That is
// the exact half-applied state A2's fail tier exists to catch, and doctor called
// it "the main checkout" instead.
func TestNestedWorktreeIsNotMain(t *testing.T) {
	main := gitignoredEnvRepo(t)
	nested := filepath.Join(main, ".th", "feat")
	git(t, main, "worktree", "add", nested, "-b", "feat")

	out, _, code := runSplit(t, nested, "hydrate", "--skip-deps")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Contains(out, "main worktree") {
		t.Fatalf("a worktree nested under main was treated as main:\n%s", out)
	}

	vars := parseEnv(readEnv(t, filepath.Join(nested, ".env")))
	if vars["COMPOSE_PROJECT_NAME"] == "" {
		t.Error("no compose namespace — its containers would clobber main's")
	}
	if vars["PORT"] == "3000" {
		t.Error("PORT was never offset — it collides with main's")
	}

	// The other half: main's own view must not have grown a service for it.
	// Hydrating a sibling would otherwise write main's secrets into
	// <sibling>/.th/feat/.env, a directory nobody asked for.
	if out, _, code := runSplit(t, main, "new", "other", "--skip-deps"); code != 0 {
		t.Fatalf("new other: exit %d\n%s", code, out)
	}
	phantom := filepath.Join(filepath.Dir(main), "app-other", ".th")
	if _, err := os.Stat(phantom); err == nil {
		t.Errorf("main's env map included the nested worktree: %s was created", phantom)
	}
}

// TestFleetPortsDisjoint drives the real thing: three siblings plus main, and
// no worktree may share a port with any other. Main packs 3000/3001 one apart,
// so offset 1 self-collides and the planner has to keep walking.
func TestFleetPortsDisjoint(t *testing.T) {
	main := gitignoredEnvRepo(t)
	seen := map[string]string{"3000": "main PORT", "3001": "main ADMIN_PORT"}

	for _, branch := range []string{"feat/one", "feat/two", "release-3"} {
		if out, _, code := runSplit(t, main, "new", branch, "--skip-deps"); code != 0 {
			t.Fatalf("new %s: exit %d\n%s", branch, code, out)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(main))
	if err != nil {
		t.Fatal(err)
	}
	worktrees := 0
	for _, e := range entries {
		if e.Name() == "app" || !e.IsDir() {
			continue
		}
		worktrees++
		vars := parseEnv(readEnv(t, filepath.Join(filepath.Dir(main), e.Name(), ".env")))
		for _, key := range []string{"PORT", "ADMIN_PORT"} {
			port := vars[key]
			if port == "" {
				t.Fatalf("%s: %s not derived", e.Name(), key)
			}
			if owner, dup := seen[port]; dup {
				t.Errorf("%s %s = %s already belongs to %s", e.Name(), key, port, owner)
			}
			seen[port] = e.Name() + " " + key
		}
		if vars["COMPOSE_PROJECT_NAME"] == "" {
			t.Errorf("%s: no compose namespace — containers would clobber a sibling's", e.Name())
		}
	}
	if worktrees != 3 {
		t.Fatalf("found %d sibling worktrees, want 3", worktrees)
	}
}
