package check

import (
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

func atoiT(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not a port number: %q", s)
	}
	return n
}
