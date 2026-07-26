package check

import (
	"reflect"
	"testing"
)

func TestProvenanceRoundTrip(t *testing.T) {
	cases := []struct{ root, branch string }{
		{"/Users/x/repo", "main"},
		{"/Users/x/repo", "feat/a-b"},
		{"/Users/x/we:ird", "feat/x"}, // a colon is legal in a path, never in a refname
		{"/r", "a"},
	}
	for _, c := range cases {
		root, branch, ok := ParseProvenance(Provenance(c.root, c.branch))
		if !ok || root != c.root || branch != c.branch {
			t.Errorf("round trip of (%q, %q) gave (%q, %q, %v)", c.root, c.branch, root, branch, ok)
		}
	}
}

// TestParseProvenanceRejects: everything that is not a comment treehouse wrote
// must come back false, because false is what keeps gc away from it.
func TestParseProvenanceRejects(t *testing.T) {
	for _, comment := range []string{
		"",
		"a scratch database, do not drop",
		"treehouse",
		"treehouse:",
		"treehouse:/Users/x/repo",  // no branch
		"treehouse:/Users/x/repo:", // empty branch
		"treehouse::main",          // no path
		"not treehouse:/repo:main",
	} {
		if _, _, ok := ParseProvenance(comment); ok {
			t.Errorf("ParseProvenance(%q) accepted a comment we did not write", comment)
		}
	}
}

func TestPlanGC(t *testing.T) {
	const main = "/repo"
	owned := func(name, root, branch string) Database {
		return Database{Name: name, Comment: Provenance(root, branch), Size: "8 MB"}
	}
	fleet := []Ref{{Path: main, Branch: "main"}, {Path: "/repo-feat", Branch: "feat/a"}}

	cases := []struct {
		name string
		in   GCInput
		want []string
	}{
		{
			name: "a clone whose worktree is gone",
			in: GCInput{
				Owned: []Database{owned("app_wt_feat_gone", main, "feat/gone")},
				Fleet: fleet, Template: "app", MainRoot: main,
			},
			want: []string{"app_wt_feat_gone"},
		},
		{
			name: "a clone whose worktree is still checked out",
			in: GCInput{
				Owned: []Database{owned(DBName("app", Slug("feat/a")), main, "feat/a")},
				Fleet: fleet, Template: "app", MainRoot: main,
			},
		},
		{
			name: "no provenance comment at all — never a candidate",
			in: GCInput{
				Owned: []Database{{Name: "app_wt_feat_gone", Comment: "my scratch db"}},
				Fleet: fleet, Template: "app", MainRoot: main,
			},
		},
		{
			name: "another repo's clone is out of scope",
			in: GCInput{
				Owned: []Database{owned("other_wt_x", "/elsewhere", "feat/x")},
				Fleet: fleet, Template: "app", MainRoot: main,
			},
		},
		{
			name: "the template is spared even if somebody commented it as ours",
			in: GCInput{
				Owned: []Database{owned("app", main, "feat/gone")},
				Fleet: fleet, Template: "app", MainRoot: main,
			},
		},
		{
			// The one way this could delete live work: rename the shared database
			// and every derived live name stops matching. The branch is the honest
			// test and it still spares them.
			name: "the template was renamed since the clones were cut",
			in: GCInput{
				Owned: []Database{
					owned(DBName("app", Slug("feat/a")), main, "feat/a"),
					owned(DBName("app", Slug("feat/gone")), main, "feat/gone"),
				},
				Fleet: fleet, Template: "app_development", MainRoot: main,
			},
			want: []string{DBName("app", Slug("feat/gone"))},
		},
		{
			name: "a detached worktree never had a clone and spares nothing",
			in: GCInput{
				Owned:    []Database{owned("app_wt_feat_gone", main, "feat/gone")},
				Fleet:    []Ref{{Path: main, Branch: "main"}, {Path: "/repo-x"}},
				Template: "app", MainRoot: main,
			},
			want: []string{"app_wt_feat_gone"},
		},
		{
			// Both name-based tests read the BRANCH, and two everyday git commands
			// break that link while the worktree carries on existing. gc offering
			// to drop the database a checkout is actively pointed at is the one
			// unrecoverable thing in this repo, so the .env gets the last word.
			name: "a live worktree that detached its HEAD keeps its clone",
			in: GCInput{
				Owned:    []Database{owned("app_wt_feat_a", main, "feat/a")},
				Fleet:    []Ref{{Path: main, Branch: "main"}, {Path: "/repo-feat"}},
				InUse:    []string{"app_wt_feat_a"},
				Template: "app", MainRoot: main,
			},
		},
		{
			name: "a live worktree whose branch was renamed keeps its clone",
			in: GCInput{
				Owned:    []Database{owned("app_wt_feat_a", main, "feat/a")},
				Fleet:    []Ref{{Path: main, Branch: "main"}, {Path: "/repo-feat", Branch: "feat/a-renamed"}},
				InUse:    []string{"app_wt_feat_a"},
				Template: "app", MainRoot: main,
			},
		},
		{
			name: "sorted, so the confirmation list is stable",
			in: GCInput{
				Owned: []Database{
					owned("app_wt_c", main, "c"), owned("app_wt_a", main, "a"), owned("app_wt_b", main, "b"),
				},
				Fleet: fleet, Template: "app", MainRoot: main,
			},
			want: []string{"app_wt_a", "app_wt_b", "app_wt_c"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, d := range (Doctor{}).PlanGC(c.in) {
				got = append(got, d.Name)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("PlanGC = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPlanGCCarriesTheBranch: the drop list is a confirmation prompt, and a
// prompt that can only show a slugged name asks the human to approve something
// they cannot identify. Slug is one-way; the comment is the only way back.
func TestPlanGCCarriesTheBranch(t *testing.T) {
	drops := (Doctor{}).PlanGC(GCInput{
		Owned:    []Database{{Name: "app_wt_x_8664d8", Comment: Provenance("/repo", "feat/a-b"), Size: "112 MB"}},
		Fleet:    []Ref{{Path: "/repo", Branch: "main"}},
		Template: "app",
		MainRoot: "/repo",
	})
	if len(drops) != 1 || drops[0].Branch != "feat/a-b" || drops[0].Size != "112 MB" {
		t.Errorf("got %+v", drops)
	}
}
