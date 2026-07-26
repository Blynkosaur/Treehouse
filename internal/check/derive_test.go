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
