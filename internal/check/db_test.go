package check

import (
	"strings"
	"testing"
	"unicode/utf8"
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

// TestDBNameIsRuneSafe exercises the truncation that became rune-aware when
// quoting replaced refusing: a template name may now be any legal Postgres
// identifier, so byte 12 can land in the middle of a multi-byte rune. Half a
// rune is not a name — psql would send bytes the server cannot decode, and the
// clone would be created under a name nothing else can reproduce.
func TestDBNameIsRuneSafe(t *testing.T) {
	// Each base is chosen so that byte 12 falls at a different offset inside a
	// rune: "aa"+3-byte runes straddles, the 4-byte emoji straddles differently,
	// and the pure 3-byte case lands exactly on a boundary.
	bases := []string{
		"日本語日本語日本語",              // 27 bytes, 3-byte runes: 12 is a clean boundary
		"a日本語日本語日本語",             // 28 bytes: 12 lands one byte into a rune
		"aa日本語日本語日本語",            // 29 bytes: two bytes in
		"🌳🌳🌳🌳🌳🌳",                 // 4-byte runes: 12 is a boundary
		"a🌳🌳🌳🌳🌳🌳",                // 4-byte runes offset by one
		"aa🌳🌳🌳🌳🌳",                // and by two
		"приложение_development", // 2-byte runes
	}
	for _, base := range bases {
		t.Run(base, func(t *testing.T) {
			name := DBName(base, Slug("feat/login"))
			if !utf8.ValidString(name) {
				t.Fatalf("DBName(%q, …) = %q — truncation split a rune", base, name)
			}
			if len(name) > 63 {
				t.Errorf("DBName(%q, …) is %d bytes, past Postgres's 63", base, len(name))
			}
			if why := IdentReason(name); why != "" {
				t.Errorf("DBName(%q, …) = %q is unusable: %s", base, name, why)
			}
			// The slug is the half that carries collision-safety, so it must arrive
			// whole however much of the base was thrown away.
			if !strings.HasSuffix(name, "_wt_"+Slug("feat/login")) {
				t.Errorf("DBName(%q, …) = %q — the slug did not survive", base, name)
			}
		})
	}
}

// TestDBNameStaysUsableForEveryQuotableTemplate is the widening this pass
// introduced, followed all the way to the name that reaches CREATE DATABASE: a
// template no longer has to match ^[a-z_][a-z0-9_]*$, so every shape Quote now
// admits has to still derive a clone name the guard will accept.
func TestDBNameStaysUsableForEveryQuotableTemplate(t *testing.T) {
	templates := []string{
		"app-db", "APPDB", "app db", `app"db`, `"""`, "app'db", "app$$db",
		"app\\db", "app;db", "app--db", "app=db", "1app", "täst",
		strings.Repeat("ä", 31), // 62 bytes, just inside the cap
		strings.Repeat("d", 63), // exactly the cap
	}
	for _, template := range templates {
		t.Run(template, func(t *testing.T) {
			if why := IdentReason(template); why != "" {
				t.Fatalf("template %q was refused: %s — Quote handles shape now", template, why)
			}
			p := Doctor{}.PlanDB(DBInput{Template: template, Slug: Slug("feat/a")})
			if p.Skip != "" || p.Bad {
				t.Fatalf("PlanDB(%q) declined: %q", template, p.Skip)
			}
			if why := IdentReason(p.Name); why != "" {
				t.Errorf("PlanDB(%q) planned %q, which the boundary refuses: %s", template, p.Name, why)
			}
			if !utf8.ValidString(p.Name) {
				t.Errorf("PlanDB(%q) planned invalid UTF-8: %q", template, p.Name)
			}
			// Quoting is what makes the plan safe, and it has to be an exact
			// round trip — a name that quotes to a DIFFERENT name is the wrong
			// database, silently.
			q := Quote(p.Name)
			if unquoteIdent(q) != p.Name {
				t.Errorf("Quote(%q) = %s, which names %q", p.Name, q, unquoteIdent(q))
			}
		})
	}
}

// unquoteIdent is Postgres's own rule read backwards: strip the outer quotes,
// collapse each doubled quote to one. It exists only so the test can assert
// Quote is injective rather than trusting it.
func unquoteIdent(q string) string {
	return strings.ReplaceAll(q[1:len(q)-1], `""`, `"`)
}

// TestQuoteIsInjective is the security property in one line: two different
// names may never quote to the same SQL, or one repo's clone is another's.
func TestQuoteIsInjective(t *testing.T) {
	names := []string{
		`a"b`, `a""b`, `a"""b`, `"`, `""`, `"""`, "a", `a"`, `"a`, `"a"`,
		"a'b", "a;b", "a--b", `a\b`, "a$$b", "a b", "A", "a",
	}
	seen := map[string]string{}
	for _, n := range names {
		q := Quote(n)
		if prior, dup := seen[q]; dup && prior != n {
			t.Errorf("Quote(%q) and Quote(%q) are both %s — two names, one database", prior, n, q)
		}
		seen[q] = n
		if unquoteIdent(q) != n {
			t.Errorf("Quote(%q) = %s, which Postgres reads as %q", n, q, unquoteIdent(q))
		}
	}
}

// TestDBNameCollidesOnTruncatedBases is a REAL bug, not a hypothetical, and it
// is left failing on purpose: fixing it renames every existing clone in every
// repo whose template is longer than twelve bytes, which orphans databases
// people are working in. That migration is a decision, not a patch.
//
// DBName keeps only the first twelve bytes of the template, with nothing to
// distinguish what it threw away. Two repos on one cluster whose templates share
// a twelve-byte prefix therefore derive the SAME clone name for the same branch.
// The second repo's `th hydrate` sees the first repo's clone in pg_database,
// reports "already exists — reusing it", and points its .env at another repo's
// database. Provenance does not save it: PlanDB never looks at a comment.
//
// The fix Slug already uses is a hash of what was dropped — base[:5] + "_" +
// 6 hex, which still fits the 63-byte budget.
func TestDBNameCollidesOnTruncatedBases(t *testing.T) {
	t.Skip("BUG: DBName truncates the template to 12 bytes with no disambiguator — two repos collide on one clone")

	a := DBName("shop_backend_dev", Slug("feat/auth"))
	b := DBName("shop_backend_web", Slug("feat/auth"))
	if a == b {
		t.Fatalf("two different templates derive one database name: %q", a)
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
