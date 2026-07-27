package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// brokenConfigRepo is a healthy repo whose treehouse.toml will not parse. The
// env half is deliberately clean, so every failure below is attributable to the
// config file and nothing else.
func brokenConfigRepo(t *testing.T) string {
	t.Helper()
	dir := cleanRepo(t)
	write(t, filepath.Join(dir, "treehouse.toml"), "[env\nrequired = [\"KEY\"\n")
	return dir
}

// TestBrokenConfigDegradesToNothing is the half of ITEM 3 the end-to-end tests
// cannot see. toml.Unmarshal fills every field it managed to read BEFORE the
// syntax error and then returns one, so the natural `cfg, _ := config.Load(…)`
// leaves a partially-populated File behind — half a curated `required` list is a
// judgment nobody made, and a half-read `[database] psql` points every psql
// round trip at the wrong cluster while the check that would explain it prints
// as one line above the table.
//
// Each case puts VALID, load-bearing keys ahead of the break, so a File that
// comes back zero can only mean loadConfig threw the partial read away.
func TestBrokenConfigDegradesToNothing(t *testing.T) {
	cases := map[string]string{
		"required list read before the break": "" +
			"[env]\nrequired = [\"KEY\", \"OTHER\"]\n[oops\n",
		"psql prefix read before the break": "" +
			"[database]\npsql = \"docker compose exec -T db psql\"\n[env\n",
		"seed and migrations read before the break": "" +
			"[[seed]]\nname = \"ramp\"\ncommand = \"true\"\n" +
			"[migrations]\nstatus = \"alembic current\"\n" +
			"[signature]]\n",
		"a whole valid file with one trailing junk line": "" +
			"[env]\nrequired = [\"KEY\"]\n\n[[deps]]\nname = \"node\"\n\nthis is not toml\n",
	}

	for name, toml := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "treehouse.toml"), toml)

			cfg, checks := loadConfig(dir)
			if len(checks) != 1 || checks[0].Name != "config" || checks[0].Status != "fail" {
				t.Fatalf("checks = %+v, want one failing config check", checks)
			}
			// Field by field, because "empty" is the whole contract and a struct
			// comparison would not say which half leaked.
			switch {
			case len(cfg.Env.Required) != 0:
				t.Errorf("required = %v — half a curated list is a judgment nobody made", cfg.Env.Required)
			case cfg.Database.Psql != "":
				t.Errorf("psql = %q — a half-read prefix aims every round trip at the wrong cluster", cfg.Database.Psql)
			case cfg.Migrations.Status != "" || cfg.Migrations.Dir != "":
				t.Errorf("migrations = %+v", cfg.Migrations)
			case len(cfg.Seed) != 0:
				t.Errorf("seed = %+v", cfg.Seed)
			case len(cfg.Deps) != 0:
				t.Errorf("deps = %+v", cfg.Deps)
			case len(cfg.Signature) != 0:
				t.Errorf("signature = %+v", cfg.Signature)
			}
		})
	}
}

// TestSeedStillErrorsOnABrokenConfig is the one command that may NOT degrade.
// Everything else works on built-in defaults; `th seed` is nothing but the
// file's contents, so running it against defaults would silently do nothing and
// report success.
func TestSeedStillErrorsOnABrokenConfig(t *testing.T) {
	dir := brokenConfigRepo(t)
	out, errOut, code := runSplit(t, dir, "seed", "ramp")
	if code != 1 {
		t.Errorf("exit %d, want 1 — treehouse cannot do this job at all\n%s%s", code, out, errOut)
	}
	if !strings.Contains(errOut, "treehouse.toml") {
		t.Errorf("the error never names the file:\n%s", errOut)
	}
}

// TestBrokenConfigIsACheck is ITEM 3. A treehouse.toml that would not parse
// made `th triage` exit 1 and `th doctor` say nothing at all — the two worst
// available answers, one of them applied after every Bash tool call for a whole
// session.
func TestBrokenConfigIsACheck(t *testing.T) {
	dir := brokenConfigRepo(t)

	t.Run("doctor reports it and fails", func(t *testing.T) {
		out, _, code := runSplit(t, dir, "doctor")
		if code != 2 {
			t.Errorf("exit %d, want 2 — the file is pure human judgment and it is not being applied\n%s", code, out)
		}
		for _, want := range []string{"config", "treehouse.toml", "fix:"} {
			if !strings.Contains(out, want) {
				t.Errorf("the report never names %q:\n%s", want, out)
			}
		}
	})

	t.Run("doctor --json carries it", func(t *testing.T) {
		out, _, _ := runSplit(t, dir, "doctor", "--json")
		var envelope struct {
			Status string `json:"status"`
			Checks []struct {
				Name, Status, Detail, Fix string
			} `json:"checks"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if envelope.Status != "fail" {
			t.Errorf("status = %q, want fail", envelope.Status)
		}
		found := false
		for _, c := range envelope.Checks {
			if c.Name == "config" {
				found = true
				if c.Status != "fail" || c.Fix == "" || !strings.Contains(c.Detail, "treehouse.toml") {
					t.Errorf("config check = %+v — it must name the file and the parse error", c)
				}
			}
		}
		if !found {
			t.Errorf("no config check in %+v", envelope.Checks)
		}
	})

	t.Run("ls reports it and fails", func(t *testing.T) {
		out, _, code := runSplit(t, dir, "ls")
		if code != 2 || !strings.Contains(out, "config") {
			t.Errorf("exit %d, output:\n%s", code, out)
		}

		raw, _, code := runSplit(t, dir, "ls", "--json")
		var envelope struct {
			Status string                          `json:"status"`
			Checks []struct{ Name, Status string } `json:"checks"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			t.Fatalf("ls --json is not clean JSON: %v\n%s", err, raw)
		}
		if envelope.Status != "fail" || code != 2 {
			t.Errorf("ls --json status %q exit %d, want fail/2", envelope.Status, code)
		}
		if len(envelope.Checks) != 1 || envelope.Checks[0].Name != "config" {
			t.Errorf("checks = %+v, want the config row", envelope.Checks)
		}
	})

	// The one that matters most. This runs after EVERY Bash tool call.
	t.Run("triage --hook never fails a tool call over it", func(t *testing.T) {
		for _, payload := range []string{
			hookPayloadJSON(t, dir, nil),
			hookPayloadJSON(t, dir, map[string]any{
				"tool_response": map[string]any{"stdout": "3 passed\n", "stderr": ""},
			}),
		} {
			out, errOut, code := runTri(t, dir, payload, "triage", "--hook")
			if code != 0 {
				t.Errorf("exit %d — a typo in a TOML file must not fail a Bash call\nstdout: %s\nstderr: %s", code, out, errOut)
			}
		}
	})

	t.Run("hook session degrades quietly", func(t *testing.T) {
		out, _, code := runTri(t, dir, "", "hook", "session")
		if code != 0 {
			t.Errorf("exit %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "config") {
			t.Errorf("the agent was never told its repo config is broken:\n%s", out)
		}
	})

	// Degrading to defaults means the commands that can work without the file
	// still do their work — a broken config is a report line, not a veto.
	t.Run("hydrate still hydrates", func(t *testing.T) {
		main := gitignoredEnvRepo(t)
		write(t, filepath.Join(main, "treehouse.toml"), "[deps\nname = \"x\"\n")
		git(t, main, "add", "-A")
		git(t, main, "commit", "-m", "broken config")

		// Exit 2 is right and expected: the config check is a FAIL finding, and 2
		// is what that means everywhere. What must NOT happen is the pipeline
		// aborting — the .env below is the evidence it ran to the end.
		out, _, code := runSplit(t, main, "new", "feat", "--skip-deps")
		if code != 2 {
			t.Errorf("exit %d, want 2 (a FAIL finding, not a crash)\n%s", code, out)
		}
		linked := filepath.Join(filepath.Dir(main), "app-feat")
		if vars := parseEnv(readEnv(t, filepath.Join(linked, ".env"))); vars["PORT"] == "" {
			t.Errorf("hydrate wrote no ports: %v", vars)
		}
	})
}
