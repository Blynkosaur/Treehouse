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
			// The documented gap: no service check exists, so `connection
			// refused` can never be corroborated and never reaches environment.
			name:       "a signature needing a fact treehouse does not have stays unknown",
			output:     "psycopg2.OperationalError: connection refused\n",
			findings:   envGreen,
			sigs:       DefaultSignatures(),
			want:       "unknown",
			wantSig:    "connection-refused",
			inEvidence: []string{"connection refused", "no service check"},
			wantFixes:  []string{"start the service (docker compose up -d), then re-run"},
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
