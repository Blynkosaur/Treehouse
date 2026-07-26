package pg

import (
	"os/exec"
	"strings"
	"testing"
)

// TestRefusesNonIdentifiers proves the guard runs BEFORE any subprocess does.
// No Postgres is required to show it: with the cluster unreachable, a name that
// reached exec would come back as a connection failure, so a refusal message is
// itself the evidence that nothing was ever handed to psql.
func TestRefusesNonIdentifiers(t *testing.T) {
	bad := []string{
		"",
		"app; DROP DATABASE postgres",
		"app db",
		"app-db",   // legal in quoted SQL, refused on purpose: we never quote
		"App",      // upper case would need quoting to survive a round trip
		"1app",     // identifiers cannot open with a digit
		"app'--",   // the classic
		"app\ndb",  // a newline inside an argv element
		`app"; --`, // an attempt to close a quoted identifier
		"täst",     // non-ASCII: legal to Postgres, outside the rule we enforce
	}

	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			// Every entry point that interpolates an identifier must refuse.
			calls := map[string]error{
				"Create":      Create(name, "template1"),
				"Create/tmpl": Create("app_wt_x", name),
				"Sessions":    errOf(Sessions(name)),
				"Terminate":   Terminate(name),
				"Comment":     Comment(name, "treehouse:/tmp:main"),
			}
			for entry, err := range calls {
				if err == nil {
					t.Fatalf("%s(%q) was allowed", entry, name)
				}
				if !strings.Contains(err.Error(), "refusing") {
					t.Errorf("%s(%q) failed for the wrong reason: %v", entry, name, err)
				}
			}
		})
	}
}

// TestAcceptsSlugShapedNames is the other half: the guard must not reject what
// check.Slug and check.DBName actually produce, or the whole feature refuses
// itself. Reachability is irrelevant here — we only assert the error is NOT the
// refusal, so this passes with or without a running cluster.
func TestAcceptsSlugShapedNames(t *testing.T) {
	for _, name := range []string{"app_wt_main", "myproject_wt_feat_a_b_8664d8", "_wt_x", "a"} {
		if err := Comment(name, "treehouse:/tmp:main"); err != nil &&
			strings.Contains(err.Error(), "refusing") {
			t.Errorf("%q is the shape DBName produces, but the guard refused it", name)
		}
	}
}

// TestCloneRoundTrip is the only test that needs a real cluster, and it skips
// cleanly without one — the same bargain internal/deps makes with `cp`.
func TestCloneRoundTrip(t *testing.T) {
	if err := exec.Command("pg_isready").Run(); err != nil {
		t.Skip("no postgres server responding — skipping the live clone")
	}

	const name = "treehouse_selftest_wt_x"
	_ = exec.Command("dropdb", "--if-exists", name).Run()
	t.Cleanup(func() { _ = exec.Command("dropdb", "--if-exists", name).Run() })

	// template1 exists in every cluster and nothing connects to it, so this
	// exercises the happy path without depending on the developer's own dbs.
	if err := Create(name, "template1"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	names, err := Databases()
	if err != nil {
		t.Fatalf("Databases: %v", err)
	}
	if !contains(names, name) {
		t.Fatalf("Databases did not report %q it had just created: %v", name, names)
	}

	// The comment is what makes `th gc` safe to run, so it has to survive the
	// round trip verbatim — including the colons it uses as separators.
	const provenance = "treehouse:/Users/x/repo:feat/a-b"
	if err := Comment(name, provenance); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	got, err := psql("-c", "SELECT shobj_description(oid, 'pg_database') FROM pg_database WHERE datname = '"+name+"'")
	if err != nil {
		t.Fatalf("reading the comment back: %v", err)
	}
	if strings.TrimSpace(got) != provenance {
		t.Errorf("comment round trip: got %q, want %q", strings.TrimSpace(got), provenance)
	}

	// Sessions must answer for a database nobody is using, rather than erroring.
	if _, err := Sessions(name); err != nil {
		t.Errorf("Sessions on an idle database: %v", err)
	}
}

func errOf[T any](_ T, err error) error { return err }

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
