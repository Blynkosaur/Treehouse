package check

import (
	"regexp"
	"strings"
	"testing"
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
