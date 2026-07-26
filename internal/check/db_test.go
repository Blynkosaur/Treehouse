package check

import (
	"strings"
	"testing"
)

func TestDBName(t *testing.T) {
	cases := []struct {
		base, slug, want string
	}{
		{"app", "feat_x", "app_wt_feat_x"},
		{"myproject_development", "feat_x", "myproject_de_wt_feat_x"}, // base truncated to 12
		{"app_", "feat_x", "app_wt_feat_x"},                           // no doubled separator
		{"", "feat_x", "_wt_feat_x"},                                  // still a legal identifier
		{"___", "feat_x", "_wt_feat_x"},
	}
	for _, c := range cases {
		if got := DBName(c.base, c.slug); got != c.want {
			t.Errorf("DBName(%q, %q) = %q, want %q", c.base, c.slug, got, c.want)
		}
	}
}

// TestDBNameFitsPostgres is the reason DBName exists. Postgres truncates an
// over-long name silently, and two branches truncated to the same 63 bytes
// share one database — the exact failure Slug's hash suffix exists to prevent.
func TestDBNameFitsPostgres(t *testing.T) {
	longBase := strings.Repeat("d", 60)
	for _, branch := range []string{
		"main", "feat/login", strings.Repeat("a", 200), "功能/登录", "",
		"feature/" + strings.Repeat("b", 60),
	} {
		for _, base := range []string{"app", "myproject_development", longBase, ""} {
			name := DBName(base, Slug(branch))
			if len(name) > 63 {
				t.Errorf("DBName(%q, Slug(%q)) is %d bytes — Postgres truncates at 63: %q", base, branch, len(name), name)
			}
			if !Ident(name) {
				t.Errorf("DBName(%q, Slug(%q)) = %q — not a legal identifier", base, branch, name)
			}
		}
	}

	// Truncation must never reach the slug: that is what keeps two branches apart.
	a := DBName(longBase, Slug("feat/a-b"))
	b := DBName(longBase, Slug("feat-a-b"))
	if a == b {
		t.Fatalf("two branches collided on one database name: %q", a)
	}
}

func TestIdent(t *testing.T) {
	// Everything Postgres will hold is usable, because Quote handles the shape.
	// The dashed and upper-case names are the ones this used to refuse, at the
	// cost of every clone that repo would ever have had.
	good := []string{
		"app", "app_wt_x", "_x", "a1", "myproject_development",
		"App", "app-dev", "app db", "1app", "приложение",
		"app;DROP DATABASE x", `app"`, "app'", "app$", "app--",
		strings.Repeat("d", 63),
	}
	bad := map[string]string{
		"":                      "empty",
		"app\x00db":             "NUL",
		"app\ndb":               "newline",
		"app\rdb":               "newline",
		strings.Repeat("d", 64): "63",
	}
	for _, s := range good {
		if !Ident(s) {
			t.Errorf("Ident(%q) = false — Postgres would hold this name: %s", s, IdentReason(s))
		}
	}
	for s, want := range bad {
		if Ident(s) {
			t.Errorf("Ident(%q) = true — nothing can rescue this name", s)
		}
		if !strings.Contains(IdentReason(s), want) {
			t.Errorf("IdentReason(%q) = %q, want it to mention %q", s, IdentReason(s), want)
		}
	}
}

// TestQuote is the rule itself: doubling is what makes an embedded quote
// harmless, and it is the only escaping treehouse does anywhere.
func TestQuote(t *testing.T) {
	cases := map[string]string{
		"app":                           `"app"`,
		"app-db":                        `"app-db"`,
		"APPDB":                         `"APPDB"`,
		`app"db`:                        `"app""db"`,
		`app"; DROP DATABASE postgres;`: `"app""; DROP DATABASE postgres;"`,
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
	// The property the doubling exists for: after quoting, the only unescaped
	// quotes in the result are the two we put there. Anything else means a name
	// could close its own identifier and run what follows as SQL.
	for _, in := range []string{`a"b`, `"`, `""`, `a""""b`, `";--`} {
		q := Quote(in)
		body := q[1 : len(q)-1]
		if strings.Count(body, `"`)%2 != 0 {
			t.Errorf("Quote(%q) = %s — an odd quote escapes the identifier", in, q)
		}
	}
}

func TestPlanDB(t *testing.T) {
	cases := []struct {
		name     string
		in       DBInput
		wantName string
		wantSkip string // substring
		wantBad  bool   // the skip is a failure: the name is unusable, not absent
		exists   bool
	}{
		{
			name:     "clone planned",
			in:       DBInput{Template: "app_dev", Slug: "feat_x"},
			wantName: "app_dev_wt_feat_x",
		},
		{
			// A1: hydrate is re-runnable, so an existing clone is a no-op, not an
			// error and not a second CREATE.
			name:     "existing clone is a no-op",
			in:       DBInput{Template: "app_dev", Slug: "feat_x", Existing: []string{"postgres", "app_dev", "app_dev_wt_feat_x"}},
			wantName: "app_dev_wt_feat_x",
			exists:   true,
		},
		{
			name:     "detached HEAD names nothing",
			in:       DBInput{Template: "app_dev"},
			wantSkip: "detached HEAD",
		},
		{
			// The dependency runs this way round on purpose: no database is created
			// unless something in the repo would point at it.
			name:     "no template means no database at all",
			in:       DBInput{Slug: "feat_x"},
			wantSkip: "nothing pointing at it",
		},
		{
			// The name this used to refuse outright, costing the repo every clone
			// it would ever have had. Quoting makes it work.
			name:     "template with a dash gets a clone",
			in:       DBInput{Template: "app-dev", Slug: "feat_x"},
			wantName: "app-dev_wt_feat_x",
		},
		{
			name:     "upper case survives",
			in:       DBInput{Template: "APPDB", Slug: "feat_x"},
			wantName: "APPDB_wt_feat_x",
		},
		{
			// Not refused for being hostile — Quote makes it inert — but it is 28
			// bytes of somebody's confusion, and it clones like any other name.
			name:     "hostile template is quoted, not refused",
			in:       DBInput{Template: `app"; DROP DATABASE prod; --`, Slug: "feat_x"},
			wantName: `app"; DROP D_wt_feat_x`,
		},
		{
			name:     "a template Postgres cannot hold is a FAILURE, not an absence",
			in:       DBInput{Template: strings.Repeat("d", 64), Slug: "feat_x"},
			wantSkip: "truncates identifiers at 63",
			wantBad:  true,
		},
		{
			name:     "a newline in the template is a failure too",
			in:       DBInput{Template: "app\ndev", Slug: "feat_x"},
			wantSkip: "newline",
			wantBad:  true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Doctor{}.PlanDB(c.in)
			if c.wantSkip != "" {
				if !strings.Contains(got.Skip, c.wantSkip) {
					t.Fatalf("Skip = %q, want it to mention %q", got.Skip, c.wantSkip)
				}
				if got.Name != "" {
					t.Errorf("a skipped plan named a database anyway: %q", got.Name)
				}
				if got.Bad != c.wantBad {
					t.Errorf("Bad = %v, want %v — an unusable name must be loud, not a shrug", got.Bad, c.wantBad)
				}
				return
			}
			if got.Skip != "" {
				t.Fatalf("unexpected skip: %s", got.Skip)
			}
			if got.Name != c.wantName {
				t.Errorf("Name = %q, want %q", got.Name, c.wantName)
			}
			if got.Template != c.in.Template {
				t.Errorf("Template = %q, want %q", got.Template, c.in.Template)
			}
			if got.Exists != c.exists {
				t.Errorf("Exists = %v, want %v", got.Exists, c.exists)
			}
		})
	}
}

// TestPlanDBNameIsUsable: whatever PlanDB returns goes straight into a createdb
// argv, so a plan that names something Postgres won't take is a plan that fails
// at the point of no return.
func TestPlanDBNameIsUsable(t *testing.T) {
	for _, branch := range []string{"main", "feat/login", "功能", strings.Repeat("z", 90), "-"} {
		p := Doctor{}.PlanDB(DBInput{Template: "myproject_development", Slug: Slug(branch)})
		if p.Skip != "" {
			t.Fatalf("%q: unexpected skip %q", branch, p.Skip)
		}
		if !Ident(p.Name) || len(p.Name) > 63 {
			t.Errorf("%q: planned name %q (%d bytes) is not usable", branch, p.Name, len(p.Name))
		}
	}
}
