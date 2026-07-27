package check

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

func TestSlug(t *testing.T) {
	cases := []struct {
		branch string
		want   string
	}{
		{"main", "main"},
		{"feat", "feat"},
		{"wt2", "wt2"},
		// Lossy mappings all carry a hash; the point is only that they differ.
		{"feat/login", "feat_login_d668f0"},
		{"Feat", "feat_8664d8"},
		{"---", "cb3f91"},
		{"/lead/and/trail/", "lead_and_trail_07f422"},
		{"a//b", "a_b_7a9acf"},
	}
	for _, c := range cases {
		if got := Slug(c.branch); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.branch, got, c.want)
		}
	}
}

func TestSlugCollision(t *testing.T) {
	// THE reason the hash suffix exists: these two differ only in a character
	// the alphabet can't keep. Colliding here means two worktrees sharing a db.
	if Slug("feat/a-b") == Slug("feat-a-b") {
		t.Fatalf("feat/a-b and feat-a-b both slug to %q", Slug("feat/a-b"))
	}
}

func TestSlugShape(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)
	long := "feature/" + strings.Repeat("a", 60)
	for _, branch := range []string{"main", "feat/login", "RELEASE-1.2.3", long, "---", "_x_"} {
		s := Slug(branch)
		if !valid.MatchString(s) {
			t.Errorf("Slug(%q) = %q — not a legal compose/postgres identifier", branch, s)
		}
		if len(s) > 47 {
			t.Errorf("Slug(%q) = %q (%d bytes) — over the 40+7 budget", branch, s, len(s))
		}
	}
}

// mainWithPorts is the canonical checkout used by the derive tests: one root
// service on 3000 and an admin port packed right beside it on 3001.
func mainWithPorts() Worktree {
	return Worktree{
		Root: "/main",
		EnvFiles: []envfile.File{
			{Path: "/main/.env", Vars: map[string]string{"PORT": "3000", "ADMIN_PORT": "3001", "DB": "x"}},
			{Path: "/main/svc_a/.env", Vars: map[string]string{"PORT": "4000", "NOT_A_PORT": "hello"}},
		},
	}
}

func TestPlanDeriveCompose(t *testing.T) {
	w := Worktree{Root: "/wt", ComposeDirs: []string{"/wt", "/wt/svc_a"}}
	repairs := Doctor{}.PlanDerive(w, Worktree{Root: "/main"}, DeriveInput{App: "app", Slug: "feat_x"})

	if len(repairs) != 2 {
		t.Fatalf("got %d repairs, want one per compose dir: %+v", len(repairs), repairs)
	}
	for _, r := range repairs {
		if !r.Overwrite {
			t.Errorf("%s: derived values must overwrite, not append", r.EnvPath)
		}
		if r.Add["COMPOSE_PROJECT_NAME"] != "app_feat_x" {
			t.Errorf("%s: COMPOSE_PROJECT_NAME = %q", r.EnvPath, r.Add["COMPOSE_PROJECT_NAME"])
		}
	}
	if repairs[0].EnvPath != "/wt/.env" || repairs[1].EnvPath != "/wt/svc_a/.env" {
		t.Errorf("repairs not path-sorted: %+v", repairs)
	}
}

func TestPlanDeriveNoComposeNoKey(t *testing.T) {
	// No compose file in the repo → E2 is a no-op, no key written anywhere.
	w := Worktree{Root: "/wt"}
	for _, r := range (Doctor{}).PlanDerive(w, mainWithPorts(), DeriveInput{App: "app", Slug: "feat_x"}) {
		if _, ok := r.Add["COMPOSE_PROJECT_NAME"]; ok {
			t.Errorf("%s: compose key written into a repo with no compose file", r.EnvPath)
		}
	}
}

func TestPlanDerivePorts(t *testing.T) {
	source := mainWithPorts()
	// A sibling worktree already sitting on offset 7.
	fleet := []Worktree{{
		Root: "/wt2",
		EnvFiles: []envfile.File{
			{Path: "/wt2/.env", Vars: map[string]string{"PORT": "3007", "ADMIN_PORT": "3008"}},
			{Path: "/wt2/svc_a/.env", Vars: map[string]string{"PORT": "4007"}},
		},
	}}
	w := Worktree{Root: "/wt"}

	repairs := Doctor{}.PlanDerive(w, source, DeriveInput{App: "app", Slug: "feat_x", Fleet: fleet})

	ports := map[string]string{}
	for _, r := range repairs {
		if r.Skip != "" {
			t.Fatalf("unexpected skip: %s", r.Skip)
		}
		for k, v := range r.Add {
			ports[r.EnvPath+" "+k] = v
		}
	}
	if len(ports) != 3 {
		t.Fatalf("got %d derived ports, want 3 (PORT, ADMIN_PORT, svc_a PORT): %v", len(ports), ports)
	}
	if _, ok := ports["/wt/.env NOT_A_PORT"]; ok {
		t.Error("a non-port key was rewritten")
	}

	// Spacing is preserved: every service moved by the SAME offset.
	root, _ := strconv.Atoi(ports["/wt/.env PORT"])
	offset := root - 3000
	if got := ports["/wt/.env ADMIN_PORT"]; got != strconv.Itoa(3001+offset) {
		t.Errorf("ADMIN_PORT = %s, want base+%d — inter-service spacing lost", got, offset)
	}
	if got := ports["/wt/svc_a/.env PORT"]; got != strconv.Itoa(4000+offset) {
		t.Errorf("svc_a PORT = %s, want base+%d", got, offset)
	}

	// Disjoint from every port any neighbour declares. Offset 1 would satisfy
	// the cross-worktree test but self-collide with main's ADMIN_PORT=3001.
	taken := map[string]bool{"3000": true, "3001": true, "4000": true, "3007": true, "3008": true, "4007": true}
	for k, v := range ports {
		if taken[v] {
			t.Errorf("%s = %s collides with a port a neighbour already declares", k, v)
		}
	}
}

func TestPlanDeriveStable(t *testing.T) {
	// Same branch → same ports, every run. The registry is the sibling .env
	// files, so there is no state to drift.
	source := mainWithPorts()
	w := Worktree{Root: "/wt"}
	in := DeriveInput{App: "app", Slug: "feat_x"}

	first := Doctor{}.PlanDerive(w, source, in)
	second := Doctor{}.PlanDerive(w, source, in)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("PlanDerive is not deterministic\n  1: %+v\n  2: %+v", first, second)
	}
	if got := (Doctor{}).PlanDerive(w, source, DeriveInput{App: "app", Slug: "other"}); reflect.DeepEqual(first, got) {
		t.Error("two different branches derived the same ports")
	}
}

func TestPlanDerivePortsExhausted(t *testing.T) {
	// A neighbour holding all 200 offsets: report, never fail the hydrate.
	crowded := map[string]string{}
	for n := 1; n <= 200; n++ {
		crowded["S"+strconv.Itoa(n)+"_PORT"] = strconv.Itoa(3000 + n)
	}
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"PORT": "3000"}},
	}}
	fleet := []Worktree{{Root: "/wt2", EnvFiles: []envfile.File{
		{Path: "/wt2/.env", Vars: crowded},
	}}}

	repairs := Doctor{}.PlanDerive(Worktree{Root: "/wt"}, source, DeriveInput{App: "app", Slug: "feat_x", Fleet: fleet})
	if len(repairs) != 1 || repairs[0].Skip == "" {
		t.Fatalf("want a single skip repair, got %+v", repairs)
	}
	if len(repairs[0].Add) != 0 {
		t.Errorf("a skipped repair must plan no writes: %v", repairs[0].Add)
	}
}

// TestSlugAlwaysAnIdentifier: the shape contract, over the inputs a branch name
// can actually take. An empty slug is not a name — it makes COMPOSE_PROJECT_NAME
// "app_" and a worktree directory called "app-".
func TestSlugAlwaysAnIdentifier(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)
	branches := []string{
		"", " ", "-", "---", "___", "...", "/", "//", "_x_", "-lead", "trail-",
		"main", "feat/login", "RELEASE-1.2.3", "bug#123", "feat/login+2",
		"功能/登录", "café/naïve", "日本語",
		strings.Repeat("a", 40), strings.Repeat("a", 41), strings.Repeat("a", 200),
		"feature/" + strings.Repeat("b", 60), strings.Repeat("/", 50) + "x",
		"user@host", "a b c", "\t", "\n",
	}
	for _, branch := range branches {
		s := Slug(branch)
		if !valid.MatchString(s) {
			t.Errorf("Slug(%q) = %q — not a legal compose/postgres identifier", branch, s)
		}
		if strings.HasSuffix(s, "_") {
			t.Errorf("Slug(%q) = %q — trailing separator", branch, s)
		}
		if len(s) > 47 {
			t.Errorf("Slug(%q) = %q (%d bytes) — over the 40+7 budget", branch, s, len(s))
		}
		if s != Slug(branch) {
			t.Errorf("Slug(%q) is not deterministic", branch)
		}
	}
}

// TestSlugNoCollisions: under Epic A a shared slug means two worktrees sharing
// one Postgres database. Every pair here differs only in characters the alphabet
// cannot keep, which is exactly what the hash suffix exists to separate.
func TestSlugNoCollisions(t *testing.T) {
	branches := []string{
		"feat/a-b", "feat-a-b", "feat_a_b", "feat.a.b", "feat/a/b", "feat--a--b",
		"Feat/A-B", "FEAT/A-B", "feat/a-b/", "/feat/a-b",
		"main", "Main", "MAIN", "main-", "-main", "ma_in",
		"", "-", "_", "/", "--",
		strings.Repeat("x", 40) + "1", strings.Repeat("x", 40) + "2",
		"release/1.0", "release/1_0", "release-1-0",
	}
	seen := map[string]string{}
	for _, b := range branches {
		s := Slug(b)
		if prev, dup := seen[s]; dup {
			t.Errorf("Slug collision: %q and %q both map to %q", prev, b, s)
		}
		seen[s] = b
	}
}

// TestPlanDeriveExhaustedKeepsCompose: running out of port offsets is a port
// problem. E2's namespacing — the thing that stops one worktree's containers
// clobbering another's — must survive it.
func TestPlanDeriveExhaustedKeepsCompose(t *testing.T) {
	crowded := map[string]string{}
	for n := 1; n <= portSpread; n++ {
		crowded["S"+strconv.Itoa(n)+"_PORT"] = strconv.Itoa(3000 + n)
	}
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"PORT": "3000"}},
	}}
	fleet := []Worktree{{Root: "/wt2", EnvFiles: []envfile.File{{Path: "/wt2/.env", Vars: crowded}}}}
	w := Worktree{Root: "/wt", ComposeDirs: []string{"/wt", "/wt/svc_a"}}

	repairs := Doctor{}.PlanDerive(w, source, DeriveInput{App: "app", Slug: "s", Fleet: fleet})

	skipped := 0
	for _, r := range repairs {
		if r.Skip != "" {
			skipped++
		}
		if r.Add["COMPOSE_PROJECT_NAME"] != "app_s" {
			t.Errorf("%s: COMPOSE_PROJECT_NAME = %q — a port skip dropped E2", r.EnvPath, r.Add["COMPOSE_PROJECT_NAME"])
		}
		for k := range r.Add {
			if k != "COMPOSE_PROJECT_NAME" {
				t.Errorf("%s: wrote %q despite having no free offset", r.EnvPath, k)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("%d repairs report the port skip, want exactly 1", skipped)
	}
}

// TestPlanDeriveNoPortCollisions is E3's whole job: over a real-ish fleet, no
// branch may land on a port main or any sibling already declares, and the same
// branch must always land on the same ones.
func TestPlanDeriveNoPortCollisions(t *testing.T) {
	// main packs two ports one apart, so offset 1 self-collides.
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"PORT": "3000", "ADMIN_PORT": "3001"}},
		{Path: "/main/svc_a/.env", Vars: map[string]string{"PORT": "4000"}},
	}}

	taken := map[string]bool{"3000": true, "3001": true, "4000": true}
	var fleet []Worktree
	// Three siblings already on disk, plus main: a non-trivial registry.
	for i, slug := range []string{"one", "two", "three"} {
		root := "/wt" + strconv.Itoa(i)
		fleet = append(fleet, Worktree{Root: root, EnvFiles: []envfile.File{
			{Path: root + "/.env", Vars: map[string]string{
				"PORT":       strconv.Itoa(3010 + i*10),
				"ADMIN_PORT": strconv.Itoa(3011 + i*10),
			}},
			{Path: root + "/svc_a/.env", Vars: map[string]string{"PORT": strconv.Itoa(4010 + i*10)}},
		}})
		for _, p := range []int{3010 + i*10, 3011 + i*10, 4010 + i*10} {
			taken[strconv.Itoa(p)] = true
		}
		_ = slug
	}

	// Branch names chosen to spread fnv32 across the offset space, including
	// hashes that land at the top of it and so must wrap.
	for _, branch := range []string{
		"feat/login", "feat/logout", "main", "x", "", "release/1.0",
		"a", "b", "c", "d", "e", "f", "g", "h",
		strings.Repeat("z", 60), "功能",
	} {
		in := DeriveInput{App: "app", Slug: Slug(branch), Fleet: fleet}
		repairs := Doctor{}.PlanDerive(Worktree{Root: "/wt"}, source, in)

		got := map[string]string{}
		for _, r := range repairs {
			if r.Skip != "" {
				t.Fatalf("%q: unexpected exhaustion with only 4 worktrees: %s", branch, r.Skip)
			}
			for k, v := range r.Add {
				got[r.EnvPath+" "+k] = v
				if taken[v] {
					t.Errorf("%q: %s %s = %s collides with a port already in the fleet", branch, r.EnvPath, k, v)
				}
			}
		}
		// Determinism is E3's one required property: ports that move between
		// runs make every container restart a new port.
		second := Doctor{}.PlanDerive(Worktree{Root: "/wt"}, source, in)
		if !reflect.DeepEqual(repairs, second) {
			t.Errorf("%q: PlanDerive is not deterministic", branch)
		}
		// Spacing between services is what the offset model buys.
		root := atoiT(t, got["/wt/.env PORT"])
		if admin := atoiT(t, got["/wt/.env ADMIN_PORT"]); admin-root != 1 {
			t.Errorf("%q: PORT/ADMIN_PORT spacing lost: %d / %d", branch, root, admin)
		}
		if svc := atoiT(t, got["/wt/svc_a/.env PORT"]); svc-4000 != root-3000 {
			t.Errorf("%q: svc_a moved by a different offset: %d vs %d", branch, svc-4000, root-3000)
		}
	}
}

// TestPlanDeriveFleetOrderIrrelevant: the registry is a set. If order mattered,
// two machines listing worktrees differently would derive different ports.
func TestPlanDeriveFleetOrderIrrelevant(t *testing.T) {
	source := mainWithPorts()
	a := Worktree{Root: "/wt2", EnvFiles: []envfile.File{{Path: "/wt2/.env", Vars: map[string]string{"PORT": "3007"}}}}
	b := Worktree{Root: "/wt3", EnvFiles: []envfile.File{{Path: "/wt3/.env", Vars: map[string]string{"PORT": "3009"}}}}

	first := Doctor{}.PlanDerive(Worktree{Root: "/wt"}, source, DeriveInput{App: "app", Slug: "s", Fleet: []Worktree{a, b}})
	second := Doctor{}.PlanDerive(Worktree{Root: "/wt"}, source, DeriveInput{App: "app", Slug: "s", Fleet: []Worktree{b, a}})
	if !reflect.DeepEqual(first, second) {
		t.Errorf("fleet order changed the plan\n  1: %+v\n  2: %+v", first, second)
	}
}

// TestPlanDerivePortCeiling: a base port near 65535 has almost no headroom.
// Every derived port must still be a legal port, or exhaustion must be reported.
func TestPlanDerivePortCeiling(t *testing.T) {
	for _, base := range []string{"65000", "65500", "65535"} {
		source := Worktree{Root: "/main", EnvFiles: []envfile.File{
			{Path: "/main/.env", Vars: map[string]string{"PORT": base}},
		}}
		for _, slug := range []string{"a", "b", "c", "feat_x", "zzzz"} {
			for _, r := range (Doctor{}).PlanDerive(Worktree{Root: "/wt"}, source, DeriveInput{App: "app", Slug: slug}) {
				for k, v := range r.Add {
					if n := atoiT(t, v); n < minPort || n > maxPort {
						t.Errorf("base %s slug %s: %s = %d, outside 1024-65535", base, slug, k, n)
					}
				}
			}
		}
	}
}

// TestPlanDeriveDB is A2: the .env points at the clone hydrate just made. The
// URL cases are the whole reason DBFromURL uses net/url — every one of them is a
// connstring a regex gets wrong.
func TestPlanDeriveDB(t *testing.T) {
	cases := []struct {
		name     string
		vars     map[string]string
		wantURL  string
		wantDB   string // "" = POSTGRES_DB must not be written
		wantSkip bool
		wantWarn bool
	}{
		{
			name:    "query params survive the rewrite",
			vars:    map[string]string{"DATABASE_URL": "postgres://u:p@localhost:5432/app_dev?sslmode=require"},
			wantURL: "postgres://u:p@localhost:5432/app_wt_x?sslmode=require",
		},
		{
			// The @ inside the password is what makes a regex eat the wrong half.
			name:    "password containing an at-sign",
			vars:    map[string]string{"DATABASE_URL": "postgres://u:p@ss@localhost/app_dev"},
			wantURL: "postgres://u:p%40ss@localhost/app_wt_x",
		},
		{
			name:    "non-default port",
			vars:    map[string]string{"DATABASE_URL": "postgresql://localhost:6543/app_dev"},
			wantURL: "postgresql://localhost:6543/app_wt_x",
		},
		{
			name:     "a non-postgres scheme is not ours to rewrite",
			vars:     map[string]string{"DATABASE_URL": "mysql://localhost/app_dev"},
			wantSkip: true,
		},
		{
			name:     "a URL naming no database has nothing to repoint",
			vars:     map[string]string{"DATABASE_URL": "postgres://localhost"},
			wantSkip: true,
		},
		{
			name:     "an unparseable URL is a skip, never a silent pass",
			vars:     map[string]string{"DATABASE_URL": "://nope"},
			wantSkip: true,
		},
		{
			name:    "both keys agreeing",
			vars:    map[string]string{"DATABASE_URL": "postgres://localhost/app_dev", "POSTGRES_DB": "app_dev"},
			wantURL: "postgres://localhost/app_wt_x",
			wantDB:  "app_wt_x",
		},
		{
			// Main contradicting itself: both move to the clone, DATABASE_URL's
			// reading wins, and the human hears about it.
			name:     "both keys disagreeing",
			vars:     map[string]string{"DATABASE_URL": "postgres://localhost/app_dev", "POSTGRES_DB": "something_else"},
			wantURL:  "postgres://localhost/app_wt_x",
			wantDB:   "app_wt_x",
			wantWarn: true,
		},
		{
			name:   "POSTGRES_DB alone",
			vars:   map[string]string{"POSTGRES_DB": "app_dev"},
			wantDB: "app_wt_x",
		},
		{
			name: "neither key: nothing to do",
			vars: map[string]string{"PORT": "3000"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := Worktree{Root: "/main", EnvFiles: []envfile.File{{Path: "/main/.env", Vars: c.vars}}}
			in := DeriveInput{App: "app", Slug: "x", DBName: "app_wt_x"}

			var got Repair
			for _, r := range (Doctor{}).PlanDerive(Worktree{Root: "/wt"}, source, in) {
				if r.EnvPath == "/wt/.env" {
					got = r
				}
			}
			if got.Add["DATABASE_URL"] != c.wantURL {
				t.Errorf("DATABASE_URL = %q, want %q", got.Add["DATABASE_URL"], c.wantURL)
			}
			if got.Add["POSTGRES_DB"] != c.wantDB {
				t.Errorf("POSTGRES_DB = %q, want %q", got.Add["POSTGRES_DB"], c.wantDB)
			}
			if (got.Skip != "") != c.wantSkip {
				t.Errorf("Skip = %q, want a skip: %v", got.Skip, c.wantSkip)
			}
			if (got.Warn != "") != c.wantWarn {
				t.Errorf("Warn = %q, want a warn: %v", got.Warn, c.wantWarn)
			}
			// A skipped URL must not drag POSTGRES_DB along on its own: half a
			// repoint leaves the app on the shared database while doctor reads green.
			if c.wantSkip && len(got.Add) != 0 {
				t.Errorf("a skipped repoint wrote keys anyway: %v", got.Add)
			}
		})
	}
}

// TestPlanDeriveNoDBNameNoKeys: no clone was made, so nothing may claim one.
// This is the ordering that keeps a .env from naming a database that isn't there.
func TestPlanDeriveNoDBNameNoKeys(t *testing.T) {
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"DATABASE_URL": "postgres://localhost/app_dev", "POSTGRES_DB": "app_dev"}},
	}}
	for _, r := range (Doctor{}).PlanDerive(Worktree{Root: "/wt"}, source, DeriveInput{App: "app", Slug: "x"}) {
		for _, key := range []string{"DATABASE_URL", "POSTGRES_DB"} {
			if _, ok := r.Add[key]; ok {
				t.Errorf("%s: wrote %s with no clone to point it at", r.EnvPath, key)
			}
		}
	}
}

// TestPlanDeriveDBPerService: a monorepo's services each get the repoint, and a
// service main declares no database for is left alone.
func TestPlanDeriveDBPerService(t *testing.T) {
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"DATABASE_URL": "postgres://localhost/app_dev"}},
		{Path: "/main/svc_a/.env", Vars: map[string]string{"POSTGRES_DB": "app_dev"}},
		{Path: "/main/svc_b/.env", Vars: map[string]string{"PORT": "4000"}},
	}}
	got := map[string]map[string]string{}
	for _, r := range (Doctor{}).PlanDerive(Worktree{Root: "/wt"}, source, DeriveInput{App: "app", Slug: "x", DBName: "app_wt_x"}) {
		got[r.EnvPath] = r.Add
	}
	if got["/wt/.env"]["DATABASE_URL"] != "postgres://localhost/app_wt_x" {
		t.Errorf("root: DATABASE_URL = %q", got["/wt/.env"]["DATABASE_URL"])
	}
	if got["/wt/svc_a/.env"]["POSTGRES_DB"] != "app_wt_x" {
		t.Errorf("svc_a: POSTGRES_DB = %q", got["/wt/svc_a/.env"]["POSTGRES_DB"])
	}
	if _, ok := got["/wt/svc_b/.env"]["POSTGRES_DB"]; ok {
		t.Errorf("svc_b declares no database but got one: %v", got["/wt/svc_b/.env"])
	}
}

// TestPlanDeriveDBKeepsPorts: the port skip and the database repoint both land
// on the root .env. Neither reason may erase the other.
func TestPlanDeriveDBKeepsPorts(t *testing.T) {
	crowded := map[string]string{}
	for n := 1; n <= portSpread; n++ {
		crowded["S"+strconv.Itoa(n)+"_PORT"] = strconv.Itoa(3000 + n)
	}
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"PORT": "3000", "DATABASE_URL": "mysql://localhost/app"}},
	}}
	fleet := []Worktree{{Root: "/wt2", EnvFiles: []envfile.File{{Path: "/wt2/.env", Vars: crowded}}}}

	repairs := Doctor{}.PlanDerive(Worktree{Root: "/wt"}, source, DeriveInput{App: "app", Slug: "x", Fleet: fleet, DBName: "app_wt_x"})
	if len(repairs) != 1 {
		t.Fatalf("want one repair, got %+v", repairs)
	}
	if !strings.Contains(repairs[0].Skip, "port offset") || !strings.Contains(repairs[0].Skip, "DATABASE_URL") {
		t.Errorf("one reason erased the other: %q", repairs[0].Skip)
	}
}

// TestPlanDeriveRedis is A6: this worktree's Redis declarations point at a
// logical db of its own. The URL cases are why redisIndex uses net/url — an @ in
// the password and a ?query after the db are both things a regex gets wrong.
func TestPlanDeriveRedis(t *testing.T) {
	cases := []struct {
		name    string
		vars    map[string]string
		wantURL string // <db> is the derived index; "" = REDIS_URL must not be written
		wantDB  bool   // REDIS_DB must be written
	}{
		{
			name:    "a url naming a db",
			vars:    map[string]string{"REDIS_URL": "redis://localhost:6379/0"},
			wantURL: "redis://localhost:6379/<db>",
		},
		{
			// The @ inside the password is what makes a regex eat the wrong half;
			// the query is what it mistakes for part of the db number.
			name:    "auth and query params survive the rewrite",
			vars:    map[string]string{"REDIS_URL": "redis://user:p@ss@localhost:6379/0?dial_timeout=5s"},
			wantURL: "redis://user:p%40ss@localhost:6379/<db>?dial_timeout=5s",
		},
		{
			name:    "no path is db 0, and still gets its own",
			vars:    map[string]string{"REDIS_URL": "redis://localhost:6379"},
			wantURL: "redis://localhost:6379/<db>",
		},
		{
			name:    "tls scheme",
			vars:    map[string]string{"REDIS_URL": "rediss://cache.internal:6380/1"},
			wantURL: "rediss://cache.internal:6380/<db>",
		},
		{
			name:   "REDIS_DB alone",
			vars:   map[string]string{"REDIS_DB": "0"},
			wantDB: true,
		},
		{
			name:    "both keys present move together",
			vars:    map[string]string{"REDIS_URL": "redis://localhost:6379/0", "REDIS_DB": "0"},
			wantURL: "redis://localhost:6379/<db>",
			wantDB:  true,
		},
		{
			name: "not a redis URL: nothing to derive",
			vars: map[string]string{"REDIS_URL": "http://localhost:6379/0"},
		},
		{
			// Nothing to check is not the same as couldn't check: a repo with no
			// Redis gets no rows AND no skip line.
			name: "neither key: nothing to do",
			vars: map[string]string{"PORT": "3000"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := Worktree{Root: "/main", EnvFiles: []envfile.File{{Path: "/main/.env", Vars: c.vars}}}
			in := DeriveInput{App: "app", Slug: "feat_x"}

			var got Repair
			for _, r := range (Doctor{}).PlanDerive(Worktree{Root: "/wt"}, source, in) {
				if r.EnvPath == "/wt/.env" {
					got = r
				}
				if strings.Contains(r.Skip, "Redis") {
					t.Errorf("%s: unexpected Redis skip: %s", r.EnvPath, r.Skip)
				}
			}

			n, ok := redisOut(t, got)
			if want := c.wantURL != "" || c.wantDB; ok != want {
				t.Fatalf("wrote Redis keys: %v, want %v (%v)", ok, want, got.Add)
			}
			if !ok {
				return
			}
			if n < 0 || n >= redisSpread {
				t.Errorf("derived db %d is outside Redis's 0–15", n)
			}
			// main is in the registry, so whatever main sits on is off limits.
			for key, val := range c.vars {
				if m, isRedis := redisIndex(key, val); isRedis && m == n {
					t.Errorf("derived db %d is the one main declares in %s", n, key)
				}
			}
			if want := strings.ReplaceAll(c.wantURL, "<db>", strconv.Itoa(n)); c.wantURL != "" && got.Add["REDIS_URL"] != want {
				t.Errorf("REDIS_URL = %q, want %q", got.Add["REDIS_URL"], want)
			}
			if _, wrote := got.Add["REDIS_URL"]; wrote != (c.wantURL != "") {
				t.Errorf("REDIS_URL written: %v, want %v", wrote, c.wantURL != "")
			}
			if _, wrote := got.Add["REDIS_DB"]; wrote != c.wantDB {
				t.Errorf("REDIS_DB written: %v, want %v", wrote, c.wantDB)
			}
		})
	}
}

// redisOut reads the logical db a repair landed on, and reports whether it wrote
// any Redis key at all. Both keys must agree — half a repoint points the cache
// client at one db and the session store at another.
func redisOut(t *testing.T, r Repair) (int, bool) {
	t.Helper()
	n, ok := -1, false
	if v, wrote := r.Add["REDIS_DB"]; wrote {
		n, ok = atoiT(t, v), true
	}
	if v, wrote := r.Add["REDIS_URL"]; wrote {
		u, err := url.Parse(v)
		if err != nil {
			t.Fatalf("REDIS_URL = %q, which does not parse", v)
		}
		m := atoiT(t, strings.TrimPrefix(u.Path, "/"))
		if ok && m != n {
			t.Errorf("REDIS_URL names db %d but REDIS_DB names %d", m, n)
		}
		n, ok = m, true
	}
	return n, ok
}

// TestPlanDeriveRedisDisjoint is A6's whole job: over a real fleet no branch may
// land on a logical db main or any sibling already declares, and the same branch
// must always land on the same one.
func TestPlanDeriveRedisDisjoint(t *testing.T) {
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"REDIS_URL": "redis://localhost:6379/0"}},
		{Path: "/main/svc_a/.env", Vars: map[string]string{"REDIS_DB": "0"}},
	}}

	taken := map[int]bool{0: true}
	var fleet []Worktree
	// Three siblings already hydrated onto dbs 4, 5 and 6.
	for i, n := range []int{4, 5, 6} {
		root := "/wt" + strconv.Itoa(i)
		taken[n] = true
		fleet = append(fleet, Worktree{Root: root, EnvFiles: []envfile.File{
			{Path: root + "/.env", Vars: map[string]string{"REDIS_URL": "redis://localhost:6379/" + strconv.Itoa(n)}},
			{Path: root + "/svc_a/.env", Vars: map[string]string{"REDIS_DB": strconv.Itoa(n)}},
		}})
	}

	for _, branch := range []string{"feat/login", "feat/logout", "main", "x", "", "release/1.0", "a", "b", "功能"} {
		in := DeriveInput{App: "app", Slug: Slug(branch), Fleet: fleet}
		repairs := Doctor{}.PlanDerive(Worktree{Root: "/wt"}, source, in)

		var dbs []int
		for _, r := range repairs {
			if r.Skip != "" {
				t.Fatalf("%q: unexpected exhaustion with only 4 worktrees: %s", branch, r.Skip)
			}
			if n, ok := redisOut(t, r); ok {
				if taken[n] {
					t.Errorf("%q: %s landed on db %d, which the fleet already declares", branch, r.EnvPath, n)
				}
				dbs = append(dbs, n)
			}
		}
		if len(dbs) != 2 {
			t.Fatalf("%q: %d dirs got a Redis db, want 2", branch, len(dbs))
		}
		if dbs[0] != dbs[1] {
			t.Errorf("%q: services split across dbs %d and %d — one worktree, one db", branch, dbs[0], dbs[1])
		}
		// Determinism: a db that moves between runs invalidates the cache it was
		// supposed to protect, every hydrate.
		if second := (Doctor{}).PlanDerive(Worktree{Root: "/wt"}, source, in); !reflect.DeepEqual(repairs, second) {
			t.Errorf("%q: the Redis db is not deterministic", branch)
		}
	}
}

// TestPlanDeriveRedisExhausted: 16 logical dbs is a ceiling a real fleet can hit.
// Hitting it is a skip line — never a failed hydrate, and never at E2's expense.
func TestPlanDeriveRedisExhausted(t *testing.T) {
	source := Worktree{Root: "/main", EnvFiles: []envfile.File{
		{Path: "/main/.env", Vars: map[string]string{"REDIS_URL": "redis://localhost:6379/0"}},
	}}
	// One sibling holding every remaining db, one per service dir.
	var files []envfile.File
	for n := 1; n < redisSpread; n++ {
		dir := "/wt2/s" + strconv.Itoa(n)
		files = append(files, envfile.File{Path: dir + "/.env", Vars: map[string]string{"REDIS_DB": strconv.Itoa(n)}})
	}
	fleet := []Worktree{{Root: "/wt2", EnvFiles: files}}
	w := Worktree{Root: "/wt", ComposeDirs: []string{"/wt"}}

	repairs := Doctor{}.PlanDerive(w, source, DeriveInput{App: "app", Slug: "feat_x", Fleet: fleet})

	skipped := 0
	for _, r := range repairs {
		if strings.Contains(r.Skip, "Redis") {
			skipped++
		}
		for key := range r.Add {
			if strings.HasPrefix(key, "REDIS_") {
				t.Errorf("%s: wrote %s with no free logical db", r.EnvPath, key)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("%d repairs report the Redis skip, want exactly 1", skipped)
	}
	if repairs[0].Add["COMPOSE_PROJECT_NAME"] != "app_feat_x" {
		t.Errorf("a Redis skip dropped E2: %v", repairs[0].Add)
	}
}

func atoiT(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not a port number: %q", s)
	}
	return n
}

// TestRepointRoundTripsEveryQuotableName is the end of the chain quoting opened
// up, and the place a wrong-database bug would actually land.
//
// A template no longer has to be [a-z_][a-z0-9_]*, so the clone name derived
// from it can carry a space, a #, a %, a quote. That name is written into a URL
// (url.String percent-encodes), through envfile's own quoting (which escapes
// #, quotes and padding), and read back by Parse and DBFromURL. Every one of
// those steps can change a string. If any of them does, `.env` names one
// database, doctor compares against another, and the app dials a third — with
// no error anywhere, because every layer succeeded at its own job.
func TestRepointRoundTripsEveryQuotableName(t *testing.T) {
	templates := []string{
		"app-db", "APPDB", "app db", `app"db`, "app'db", "app#db", "app%db",
		"app db#x", "app$$db", `app\db`, "app;db", "täst_db", "app db  ",
		"app=db", "app?db", "app&db", "app/db", "app@db", "app:db",
	}
	for _, template := range templates {
		t.Run(template, func(t *testing.T) {
			db := DBName(template, Slug("feat/login"))
			if why := IdentReason(db); why != "" {
				t.Fatalf("DBName(%q, …) = %q is unusable: %s", template, db, why)
			}

			// Both key shapes, because they move together or not at all.
			for _, vars := range []map[string]string{
				{"DATABASE_URL": "postgres://u:p@localhost:5432/" + template + "?sslmode=require"},
				{"POSTGRES_DB": template},
				{"DATABASE_URL": "postgres://localhost/" + template, "POSTGRES_DB": template},
			} {
				add, _, skip := repointDB(vars, db)
				if skip != "" {
					t.Fatalf("repointDB declined %v: %s", vars, skip)
				}

				dir := t.TempDir()
				path := filepath.Join(dir, ".env")
				if err := envfile.Create(path, add); err != nil {
					t.Fatalf("Create: %v", err)
				}
				// Set is the writer derive actually uses on a second run, and it has
				// to land on the same bytes — otherwise the value drifts every hydrate.
				if err := envfile.Set(path, add); err != nil {
					t.Fatalf("Set: %v", err)
				}

				f, err := envfile.LoadPath(path)
				if err != nil {
					t.Fatalf("LoadPath: %v", err)
				}
				got := EnvDB(Worktree{Root: dir, EnvFiles: []envfile.File{f}})
				if got != db {
					t.Errorf("wrote %q, .env reads back %q (from %v)\n  file: %q",
						db, got, add, readEnvFile(t, path))
				}
			}
		})
	}
}

func readEnvFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
