package pg

import (
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Blynkosaur/treehouse/internal/check"
)

// TestRefusesUnusableNames proves the guard runs BEFORE any subprocess does.
// No Postgres is required to show it: with the cluster unreachable, a name that
// reached exec would come back as a connection failure, so a refusal message is
// itself the evidence that nothing was ever handed to psql.
//
// The list is short on purpose. Quoting handles every name Postgres will hold;
// what is left is only what nothing can rescue.
func TestRefusesUnusableNames(t *testing.T) {
	bad := []string{
		"",                      // no name to quote
		"app\x00db",             // C strings end at the NUL: the server sees "app"
		"app\ndb",               // psql reads stdin line by line
		"app\rdb",               // and a CR is a line break to some of it
		strings.Repeat("d", 64), // Postgres truncates at 63, silently
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
				"Drop":        Drop(name),
				"MarkSeed":    MarkSeed(name, "ramp"),
				"Seeds":       errOf(Seeds(name)),
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

// TestAcceptsQuotableNames is the other half, and it is the whole point of
// quoting rather than refusing: every one of these was refused before, and a
// repo whose database is called app-db got no clones at all, permanently.
//
// It asserts only that the error is NOT the refusal, so it passes with or
// without a running cluster — including the shapes that look like injection,
// because a doubled quote cannot escape a quoted identifier.
func TestAcceptsQuotableNames(t *testing.T) {
	for _, name := range []string{
		"app_wt_main", "myproject_wt_feat_a_b_8664d8", "_wt_x", "a",
		"app-db", "APPDB", "app db", "täst", "1app",
		`app"; DROP DATABASE postgres; --`,
		"app'--",
	} {
		if err := Comment(name, "treehouse:/tmp:main"); err != nil &&
			strings.Contains(err.Error(), "refusing") {
			t.Errorf("%q is a name Postgres will hold, but the guard refused it", name)
		}
	}
}

// TestQuotedNamesSurviveTheCluster is the live half of the one above: quoting is
// only correct if Postgres agrees, and no unit test can say whether it does.
// Each name here was refused outright before this change.
func TestQuotedNamesSurviveTheCluster(t *testing.T) {
	if err := exec.Command("pg_isready").Run(); err != nil {
		t.Skip("no postgres server responding")
	}
	for _, name := range []string{
		"th-selftest-db",    // a dash: legal, needs quoting
		"THSelfTestDB",      // upper case: quoting is what preserves it
		`th"selftest`,       // an embedded quote, which Quote doubles
		"th selftest space", // a space
		"th;selftest--",     // the shape an injection attempt has
	} {
		t.Run(name, func(t *testing.T) {
			_ = Drop(name)
			t.Cleanup(func() { _ = Drop(name) })

			if err := Create(name, "template1"); err != nil {
				t.Fatalf("Create(%q): %v", name, err)
			}
			names, err := Databases()
			if err != nil {
				t.Fatalf("Databases: %v", err)
			}
			// Verbatim, not case-folded and not mangled: an unquoted CREATE would
			// have made "thselftestdb" and left us reporting a clone that isn't
			// the one .env names.
			if !contains(names, name) {
				t.Fatalf("Create made something other than %q: %v", name, names)
			}
			// postgres must still be there — the injection shapes above would have
			// dropped it if the quoting were wrong.
			if !contains(names, "postgres") {
				t.Fatalf("%q took out the maintenance database", name)
			}
			if err := Comment(name, "treehouse:/tmp:feat/a"); err != nil {
				t.Errorf("Comment(%q): %v", name, err)
			}
			if _, err := Sessions(name); err != nil {
				t.Errorf("Sessions(%q): %v", name, err)
			}
			if err := MarkSeed(name, "ramp"); err != nil {
				t.Errorf("MarkSeed(%q): %v", name, err)
			}
			if err := Drop(name); err != nil {
				t.Errorf("Drop(%q): %v", name, err)
			}
		})
	}
}

// TestHostileNamesNameTheRightDatabase is the security boundary of this change,
// live. Refusing a name is safe by construction; QUOTING one is only safe if
// Postgres reads back exactly the name we meant. So each of these is created,
// then looked for BYTE FOR BYTE in pg_database — a name that came back different
// is a clone treehouse can never find again, and a `th hydrate` that recreates
// it forever.
//
// The maintenance and template databases are re-checked after every single one:
// these are the shapes that close an identifier and run what follows, and the
// only proof that doubling holds is that they are all still standing.
func TestHostileNamesNameTheRightDatabase(t *testing.T) {
	skipWithoutCluster(t)
	for _, name := range []string{
		`"""`,                              // nothing but quotes
		`a"";DROP DATABASE postgres;--`,    // a doubled quote, pre-escaped by the attacker
		`x"; DROP DATABASE "postgres"; --`, // the fully-formed attempt
		`app\db`,                           // a backslash, which quoted identifiers do not escape
		"app$$db",                          // dollar quoting, which is a LITERAL construct
		"app'db",                           // a single quote: a literal delimiter, not an identifier one
		"app--db",                          // a SQL comment opener
		"app;db",                           // a statement separator
		"täst-db",                          // multi-byte
		strings.Repeat("d", 63),            // exactly the cap
		strings.Repeat("ä", 31) + "d",      // 63 BYTES of multi-byte, which is what Postgres counts
	} {
		t.Run(name, func(t *testing.T) {
			_ = Drop(name)
			t.Cleanup(func() { _ = Drop(name) })

			if err := Create(name, "template1"); err != nil {
				t.Fatalf("Create(%q): %v", name, err)
			}
			names, err := Databases()
			if err != nil {
				t.Fatalf("Databases: %v", err)
			}
			// The whole point: what we asked for is what exists. PlanDB decides
			// "this clone already exists" by comparing these strings, so a name that
			// came back mangled means .env is pointed at a database nobody planned.
			if !contains(names, name) {
				t.Fatalf("Create(%q) made something else: %v", name, names)
			}
			for _, survivor := range []string{"postgres", "template1"} {
				if !contains(names, survivor) {
					t.Fatalf("%q took out %q — the quoting did not hold", name, survivor)
				}
			}
			// Every other entry point interpolates the same identifier, so each has
			// to survive it too, not just CREATE.
			if err := Comment(name, "treehouse:/tmp/repo:feat/a"); err != nil {
				t.Errorf("Comment(%q): %v", name, err)
			}
			dbs, err := Commented()
			if err != nil {
				t.Fatalf("Commented: %v", err)
			}
			found := false
			for _, db := range dbs {
				if db.Name == name {
					found = true
					if db.Comment != "treehouse:/tmp/repo:feat/a" {
						t.Errorf("provenance came back %q", db.Comment)
					}
				}
			}
			if !found {
				t.Errorf("Commented lost %q — gc would never see this clone", name)
			}
			if _, err := Sessions(name); err != nil {
				t.Errorf("Sessions(%q): %v", name, err)
			}
			if err := MarkSeed(name, "ramp"); err != nil {
				t.Errorf("MarkSeed(%q): %v", name, err)
			}
			if seeds, err := Seeds(name); err != nil || len(seeds) != 1 {
				t.Errorf("Seeds(%q) = %v, %v — the marker went into another database", name, seeds, err)
			}
			if err := Drop(name); err != nil {
				t.Fatalf("Drop(%q): %v", name, err)
			}
			if names, err = Databases(); err != nil || contains(names, name) {
				t.Errorf("Drop(%q) left it behind", name)
			}
			if !contains(names, "postgres") {
				t.Fatalf("Drop(%q) took out the maintenance database", name)
			}
		})
	}
}

// TestClonedNameSurvivesTheClusterVerbatim closes the loop between the planner
// and the server: check.DBName derives a name, Postgres stores it, and PlanDB
// decides whether to create a clone by comparing those two strings. If the
// server folds or truncates anything, Exists is false forever and hydrate
// re-clones on every run.
func TestClonedNameSurvivesTheClusterVerbatim(t *testing.T) {
	skipWithoutCluster(t)
	for _, template := range []string{"th-selftest-app", "THSelfTest", "täst_selftest"} {
		t.Run(template, func(t *testing.T) {
			name := check.DBName(template, check.Slug("feat/login"))
			_ = Drop(name)
			t.Cleanup(func() { _ = Drop(name) })

			if err := Create(name, "template1"); err != nil {
				t.Fatalf("Create(%q): %v", name, err)
			}
			names, err := Databases()
			if err != nil {
				t.Fatalf("Databases: %v", err)
			}
			plan := check.Doctor{}.PlanDB(check.DBInput{
				Template: template, Existing: names, Slug: check.Slug("feat/login"),
			})
			if plan.Name != name {
				t.Fatalf("PlanDB named %q, we created %q", plan.Name, name)
			}
			if !plan.Exists {
				t.Errorf("PlanDB cannot see the clone it named — hydrate would create it again forever")
			}
		})
	}
}

func skipWithoutCluster(t *testing.T) {
	t.Helper()
	if err := exec.Command("pg_isready").Run(); err != nil {
		t.Skip("no postgres server responding")
	}
}

// TestCloneRoundTrip is the only test that needs a real cluster, and it skips
// cleanly without one — the same bargain internal/deps makes with `cp`.
func TestCloneRoundTrip(t *testing.T) {
	if err := exec.Command("pg_isready").Run(); err != nil {
		t.Skip("no postgres server responding — skipping the live clone")
	}

	const name = "treehouse_selftest_wt_x"
	_ = Drop(name)
	t.Cleanup(func() { _ = Drop(name) })

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
	got, err := psql(maintenanceDB, "SELECT shobj_description(oid, 'pg_database') FROM pg_database WHERE datname = '"+name+"'")
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

	// Commented is gc's entire input, so the row it produces has to be complete:
	// no size means the confirmation prompt can't say what a drop buys back.
	dbs, err := Commented()
	if err != nil {
		t.Fatalf("Commented: %v", err)
	}
	var row *check.Database
	for i := range dbs {
		if dbs[i].Name == name {
			row = &dbs[i]
		}
	}
	if row == nil {
		t.Fatalf("Commented did not report %q, which we just commented: %+v", name, dbs)
	}
	if row.Comment != provenance || row.Size == "" {
		t.Errorf("Commented row = %+v", *row)
	}

	// A clone with no seed run against it has no marker table, and that is the
	// state every clone starts in — an empty answer, never an error.
	seeds, err := Seeds(name)
	if err != nil || len(seeds) != 0 {
		t.Errorf("Seeds on a fresh clone = %v, %v — want empty and no error", seeds, err)
	}
	if err := MarkSeed(name, "ramp"); err != nil {
		t.Fatalf("MarkSeed: %v", err)
	}
	if err := MarkSeed(name, "ramp"); err != nil {
		t.Errorf("MarkSeed is not idempotent: %v", err) // re-seeding is the normal case
	}
	if seeds, err = Seeds(name); err != nil || !reflect.DeepEqual(seeds, []string{"ramp"}) {
		t.Errorf("Seeds after MarkSeed = %v, %v", seeds, err)
	}

	if err := Drop(name); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if names, err = Databases(); err != nil || contains(names, name) {
		t.Errorf("Drop left %q behind: %v", name, err)
	}
	// gc lists first and drops second; a database that vanished in between is
	// gc's goal, not gc's problem.
	if err := Drop(name); err != nil {
		t.Errorf("Drop of an already-absent database: %v", err)
	}
}

// TestDropRefusesTheBusy proves the error a caller has to distinguish survives:
// gc reports live connections rather than killing them, and it can only do that
// if the busy case is separable from a real failure. Our own psql session is the
// connection that blocks the drop.
func TestDropRefusesTheBusy(t *testing.T) {
	if err := exec.Command("pg_isready").Run(); err != nil {
		t.Skip("no postgres server responding")
	}
	const name = "treehouse_selftest_busy"
	_ = Drop(name)
	t.Cleanup(func() { _ = Drop(name) })
	if err := Create(name, "template1"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// pg_sleep inside the target database holds a connection open while we drop.
	psql := Command()
	held := exec.Command(psql[0], append(psql[1:],
		"-d", name, "-At", "--no-psqlrc", "-c", "SELECT pg_sleep(10)")...)
	if err := held.Start(); err != nil {
		t.Skipf("could not hold a session open: %v", err)
	}
	defer func() { _ = held.Process.Kill() }()
	waitForSession(t, name)

	err := Drop(name)
	if !errors.Is(err, ErrBusy) {
		t.Errorf("Drop of a database in use = %v, want ErrBusy", err)
	}
}

// waitForSession blocks until the held connection is visible in pg_stat_activity
// — starting a subprocess is not the same as it having connected.
func waitForSession(t *testing.T, db string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if s, err := Sessions(db); err == nil && len(s) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Skip("the held session never appeared — cannot test the busy path")
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
