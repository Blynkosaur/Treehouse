package check

import (
	"strings"
	"testing"
)

// envSig leans on the one doctor fact that is always available, so the
// correlation table can be exercised without dragging in a database.
var envSig = Signature{
	Name: "missing-env", Match: `KeyError: 'DATABASE_URL'`,
	Cause: "a required environment variable is unset", Fix: "th hydrate", Needs: needsEnv,
}

var (
	envRed   = []Finding{{Dir: "/w", Keys: 2, Missing: []string{"DATABASE_URL"}}}
	envGreen = []Finding{{Dir: "/w", Keys: 2}}
	dbRed    = []Check{{Name: "db", Status: "fail", Detail: ".env still targets the SHARED database app_dev", Fix: "th hydrate"}}
)

// TestTriageTable is B1's whole specification: what a matched signature is worth
// depends entirely on whether doctor agrees with it.
func TestTriageTable(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		findings   []Finding
		checks     []Check
		sigs       []Signature
		want       string
		wantSig    string
		inEvidence []string // substrings the evidence must carry, all of them
		wantFixes  []string
	}{
		{
			name:       "matched and doctor red is the only route to environment",
			output:     "Traceback\nKeyError: 'DATABASE_URL'\n",
			findings:   envRed,
			sigs:       []Signature{envSig},
			want:       "environment",
			wantSig:    "missing-env",
			inEvidence: []string{"KeyError: 'DATABASE_URL'", "doctor agrees", "missing DATABASE_URL"},
			wantFixes:  []string{"th hydrate"},
		},
		{
			// The row the correlation exists for. Regex alone would have said
			// "environment" here and sent the reader to fix a healthy .env.
			name:       "matched but doctor green is unknown, and says why",
			output:     "KeyError: 'DATABASE_URL'\n",
			findings:   envGreen,
			sigs:       []Signature{envSig},
			want:       "unknown",
			wantSig:    "missing-env",
			inEvidence: []string{"KeyError", "doctor reports env healthy", "more likely code"},
		},
		{
			name:       "nothing matched but doctor is red offers the red row as a hint",
			output:     "assert 1 == 2\n",
			findings:   envRed,
			sigs:       []Signature{envSig},
			want:       "unknown",
			inEvidence: []string{"no known failure signature matched", "possibly related"},
		},
		{
			name:       "nothing matched and doctor green is code",
			output:     "assert 1 == 2\n",
			findings:   envGreen,
			sigs:       []Signature{envSig},
			want:       "code",
			inEvidence: []string{"doctor reports the environment healthy"},
		},
		{
			// A repo that declares no port has no service fact, so the signature
			// still cannot be corroborated. Absent is not green.
			name:       "connection refused with nothing to check stays unknown",
			output:     "psycopg2.OperationalError: connection refused\n",
			findings:   envGreen,
			sigs:       DefaultSignatures(),
			want:       "unknown",
			wantSig:    "connection-refused",
			inEvidence: []string{"connection refused", "declares no PORT keys"},
			wantFixes:  []string{"start the service (docker compose up -d), then re-run"},
		},
		{
			// C2's payoff, and the reason the service check exists at all. B1
			// shipped with this row landing on `unknown` because `service` mapped
			// onto nothing; a dead listener is a real fact now.
			name:     "a dead service corroborates connection refused",
			output:   "Error: connect ECONNREFUSED 127.0.0.1:4000\n",
			findings: envGreen,
			checks: []Check{{Name: "service", Status: "fail",
				Detail: "api/PORT — nothing is listening on 127.0.0.1:4000", Fix: "docker compose up -d api"}},
			sigs:       DefaultSignatures(),
			want:       "environment",
			wantSig:    "connection-refused",
			inEvidence: []string{"ECONNREFUSED", "doctor agrees", "nothing is listening on 127.0.0.1:4000"},
			wantFixes:  []string{"start the service (docker compose up -d), then re-run", "docker compose up -d api"},
		},
		{
			name:     "every service up contradicts connection refused",
			output:   "Error: connect ECONNREFUSED 127.0.0.1:4000\n",
			findings: envGreen,
			checks: []Check{{Name: "service", Status: "ok",
				Detail: "api/PORT is listening on 127.0.0.1:4000"}},
			sigs:       DefaultSignatures(),
			want:       "unknown",
			wantSig:    "connection-refused",
			inEvidence: []string{"doctor reports service healthy", "more likely code"},
		},
		{
			// One row per detected port, so the fact has to be the worst of them.
			// Order must not decide it either way.
			name:     "one dead service among healthy ones is still a dead service",
			output:   "connection refused\n",
			findings: envGreen,
			checks: []Check{
				{Name: "service", Status: "ok", Detail: "web/PORT is listening on 127.0.0.1:3000"},
				{Name: "service", Status: "warn", Detail: "api/PORT — nothing is listening on 127.0.0.1:4000", Fix: "docker compose up -d"},
				{Name: "service", Status: "ok", Detail: "admin/PORT is listening on 127.0.0.1:5000"},
			},
			sigs:       DefaultSignatures(),
			want:       "environment",
			wantSig:    "connection-refused",
			inEvidence: []string{"doctor agrees", "127.0.0.1:4000"},
		},
		{
			// skip means "the question does not apply", which is exactly as
			// uninformative as never asking — it must not read as green.
			name:     "a skipped check is not a green check",
			output:   `django.db.utils.ProgrammingError: relation "users" does not exist`,
			findings: envGreen,
			checks: []Check{{Name: "migrations", Status: skip,
				Detail: "no [migrations] status command configured"}},
			sigs:       DefaultSignatures(),
			want:       "unknown",
			wantSig:    "missing-relation",
			inEvidence: []string{"unconfirmed", "no [migrations] status command"},
		},
		{
			name:       "an absent check is not a green check either",
			output:     `relation "users" does not exist`,
			findings:   envGreen,
			sigs:       DefaultSignatures(),
			want:       "unknown",
			wantSig:    "missing-relation",
			inEvidence: []string{"did not report on migrations"},
		},
		{
			name:       "a red migrations check corroborates the relation signature",
			output:     `relation "users" does not exist`,
			findings:   envGreen,
			checks:     []Check{{Name: "migrations", Status: "warn", Detail: "this branch adds 2 migration file(s)", Fix: "alembic upgrade head"}},
			sigs:       DefaultSignatures(),
			want:       "environment",
			wantSig:    "missing-relation",
			inEvidence: []string{"doctor agrees", "adds 2 migration"},
			wantFixes:  []string{"run your migrations against this worktree's database, then `th seed <name>`", "alembic upgrade head"},
		},
		{
			// A red row in a different area still gets carried, because the
			// matched line and that row may be the same story.
			name:       "a red area the signature did not name rides along as a hint",
			output:     "KeyError: 'DATABASE_URL'\n",
			findings:   envGreen,
			checks:     dbRed,
			sigs:       []Signature{envSig},
			want:       "unknown",
			wantSig:    "missing-env",
			inEvidence: []string{"doctor reports env healthy", "possibly related", "SHARED database"},
			wantFixes:  []string{"th hydrate"},
		},
		{
			// From a hand-edited treehouse.toml. It must not take out the
			// signatures that do compile.
			name:       "a signature whose regex does not compile is skipped, not fatal",
			output:     "KeyError: 'DATABASE_URL'\n",
			findings:   envRed,
			sigs:       []Signature{{Name: "broken", Match: "([unclosed", Needs: needsEnv}, envSig},
			want:       "environment",
			wantSig:    "missing-env",
			inEvidence: []string{"KeyError"},
		},
		{
			name:       "no env files at all is unknown, not green",
			output:     "KeyError: 'DATABASE_URL'\n",
			sigs:       []Signature{envSig},
			want:       "unknown",
			wantSig:    "missing-env",
			inEvidence: []string{"found no env files"},
		},
		{
			// Order is the tiebreak, and Merge appends overrides — so a repo can
			// take over a built-in name and be believed.
			name:       "the first matching signature wins",
			output:     "KeyError: 'DATABASE_URL'\n",
			findings:   envRed,
			sigs:       []Signature{{Name: "repo-specific", Match: "KeyError", Cause: "ours", Fix: "make setup", Needs: needsEnv}, envSig},
			want:       "environment",
			wantSig:    "repo-specific",
			inEvidence: []string{"ours"},
			wantFixes:  []string{"make setup", "th hydrate"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Triage(c.output, c.findings, c.checks, c.sigs)
			if got.Cause != c.want {
				t.Errorf("cause = %q, want %q\nevidence: %v", got.Cause, c.want, got.Evidence)
			}
			if got.Signature != c.wantSig {
				t.Errorf("signature = %q, want %q", got.Signature, c.wantSig)
			}
			joined := strings.Join(got.Evidence, "\n")
			for _, want := range c.inEvidence {
				if !strings.Contains(joined, want) {
					t.Errorf("evidence missing %q:\n%s", want, joined)
				}
			}
			if c.wantFixes != nil && strings.Join(got.Fixes, "|") != strings.Join(c.wantFixes, "|") {
				t.Errorf("fixes = %v, want %v", got.Fixes, c.wantFixes)
			}
			if got.Cause == "environment" && len(got.Fixes) == 0 {
				t.Error("an environment verdict with no fix is a verdict the reader has to solve twice")
			}
		})
	}
}

// TestDefaultSignatures: each built-in fires on the real-world line B1 names it
// for, and on nothing else in the set.
func TestDefaultSignatures(t *testing.T) {
	lines := map[string]string{
		"connection-refused": `psycopg2.OperationalError: connection to server at "localhost", port 5432 failed: Connection refused`,
		"missing-relation":   `django.db.utils.ProgrammingError: relation "django_session" does not exist`,
		"missing-env":        `KeyError: 'DATABASE_URL'`,
	}
	extra := []string{
		"Error: environment variable REDIS_URL is not set",
		"ECONNREFUSED 127.0.0.1:6379",
		"sqlite3.OperationalError: no such table: users",
	}

	for name, line := range lines {
		sig, matched := MatchSignature(line, DefaultSignatures())
		if sig.Name != name {
			t.Errorf("%q matched %q, want %q", line, sig.Name, name)
		}
		if matched != strings.TrimSpace(line) {
			t.Errorf("evidence = %q, want the whole line", matched)
		}
	}
	for _, line := range extra {
		if sig, _ := MatchSignature(line, DefaultSignatures()); sig.Name == "" {
			t.Errorf("no default signature matched %q", line)
		}
	}

	// Ordinary failures must NOT match, or every code bug gets an environment
	// story attached to it.
	for _, line := range []string{
		"AssertionError: assert 1 == 2",
		"KeyError: 'user_id'",
		"TypeError: cannot read property 'x' of undefined",
		"FAILED tests/test_api.py::test_login",
	} {
		if sig, _ := MatchSignature(line, DefaultSignatures()); sig.Name != "" {
			t.Errorf("%q matched %q — a false positive is worse than no signature", line, sig.Name)
		}
	}
}

func TestTriageTrimsRunawayLines(t *testing.T) {
	long := "KeyError: 'DATABASE_URL' " + strings.Repeat("x", 5000)
	got := Triage(long, envRed, nil, []Signature{envSig})
	if len(got.Evidence[0]) > 210 {
		t.Errorf("evidence line is %d chars — one minified line would be the whole verdict", len(got.Evidence[0]))
	}
}

// TestTriageUnresolvedSecret is the failure `th run` exists to prevent and
// could not previously explain: an agent runs `npm start` directly, the app's
// own dotenv reads .env, and the program is handed `th:STRIPE_SECRET` where a
// secret belonged. Before this signature the verdict was `code` — actively
// wrong, and it sends somebody to debug an SDK over an invocation mistake.
func TestTriageUnresolvedSecret(t *testing.T) {
	vaulted := []Check{{Name: "vault", Status: "ok", Detail: "1 key (STRIPE_SECRET) is vaulted"}}
	out := `StripeInvalidRequestError: Invalid API Key provided: th:STRIPE_SECRET`

	t.Run("a reference in the output, in a vaulted worktree", func(t *testing.T) {
		v := Triage(out, nil, vaulted, DefaultSignatures())
		if v.Cause != "environment" {
			t.Fatalf("cause = %q, want environment:\n%v", v.Cause, v.Evidence)
		}
		if !containsSub(v.Fixes, "th run") {
			t.Fatalf("no fix naming th run: %v", v.Fixes)
		}
	})

	// A broken vault corroborates just as well as a healthy one — the reference
	// reached the program either way, so skip counts here where it counts as
	// unknown everywhere else.
	t.Run("a vault that could not be checked still corroborates", func(t *testing.T) {
		noKeychain := []Check{{Name: "vault", Status: skip, Detail: "not macOS"}}
		if v := Triage(out, nil, noKeychain, DefaultSignatures()); v.Cause != "environment" {
			t.Fatalf("cause = %q, want environment:\n%v", v.Cause, v.Evidence)
		}
	})

	// No vault in this repo: the string is somebody else's, and a confident
	// "environment" would be exactly the wrong answer.
	t.Run("no vault here means the pattern proves nothing", func(t *testing.T) {
		if v := Triage(out, nil, nil, DefaultSignatures()); v.Cause == "environment" {
			t.Fatalf("cause = environment with no vault in the repo:\n%v", v.Evidence)
		}
	})

	// It outranks connection-refused: a program handed a pointer instead of a
	// password usually also fails to connect, and that is the less useful answer.
	t.Run("it outranks the connection failure it causes", func(t *testing.T) {
		both := "could not connect to server: postgres://u:th:DB_PASSWORD@h/d - connection refused"
		v := Triage(both, nil, append(vaulted,
			Check{Name: "service", Status: "fail", Detail: "nothing listening"}), DefaultSignatures())
		if v.Signature != "unresolved-secret" {
			t.Fatalf("signature = %q, want unresolved-secret", v.Signature)
		}
	})

	// The vault fact must never attach itself to an unrelated verdict as a hint
	// or a stray fix — it is red in every vaulted worktree by construction.
	t.Run("a vaulted worktree does not colour unrelated verdicts", func(t *testing.T) {
		v := Triage("TypeError: undefined is not a function", nil, vaulted, DefaultSignatures())
		if v.Cause != "code" {
			t.Fatalf("cause = %q, want code — the vault leaked into an unrelated verdict:\n%v", v.Cause, v.Evidence)
		}
		if len(v.Fixes) != 0 {
			t.Fatalf("unrelated verdict carries fixes: %v", v.Fixes)
		}
	})
}

func containsSub(all []string, want string) bool {
	for _, s := range all {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
