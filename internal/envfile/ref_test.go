package envfile

import "testing"

// errRef is what a broken reference rule looks like when it is reported.
type errRef struct{ value, want, got string }

func (e errRef) Error() string {
	return "IsRef(" + e.value + "): want " + e.want + ", got " + e.got
}

// TestIsRef pins the whole-value rule. The substring cases are the ones that
// matter: a value that merely CONTAINS "th:" is a secret, and reading it as a
// pointer would silently swap somebody's password for a keychain lookup.
func TestIsRef(t *testing.T) {
	for _, c := range []struct {
		value string
		name  string // "" means: not a reference
	}{
		{"th:STRIPE_SECRET", "STRIPE_SECRET"},
		{"th:KEY", "KEY"}, // three characters: a floor of 4 would break this
		{"th:a", "a"},     // one is legal too; anchoring is the safety rule
		{"th:A_1", "A_1"},

		{"", ""},
		{"th:", ""}, // prefix alone points at nothing
		{"sk-live-abcdef", ""},
		{"postgres://user:th:pass@host/db", ""}, // contains th:, is not a reference
		{"xth:KEY", ""},                         // must START with the prefix
		{"th:KEY ", ""},                         // trailing space: a value, not a name
		{"th:KEY=v", ""},
		{"th:a-b", ""},                           // - is not in the alphabet
		{"th:" + string(make([]byte, 0, 0)), ""}, // same as bare prefix
		{"th:" + longName(64), longName(64)},
		{"th:" + longName(65), ""}, // one over the cap
	} {
		got, ok := IsRef(c.value)
		want, wantOK := c.name, c.name != ""
		if ok != wantOK || got != want {
			t.Error(errRef{c.value, describe(want, wantOK), describe(got, ok)})
		}
	}
}

func longName(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'A'
	}
	return string(b)
}

func describe(name string, ok bool) string {
	if !ok {
		return "not a reference"
	}
	return "reference to " + name
}

// TestRefs: only the referenced keys come back, and a literal is left alone.
func TestRefs(t *testing.T) {
	got := Refs(map[string]string{
		"STRIPE_SECRET": "th:STRIPE_SECRET",
		"PORT":          "3000",
		"DATABASE_URL":  "postgres://localhost/app",
		"OTHER":         "th:shared_token",
	})
	want := map[string]string{"STRIPE_SECRET": "STRIPE_SECRET", "OTHER": "shared_token"}
	if len(got) != len(want) {
		t.Fatalf("Refs: want %v, got %v", want, got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("Refs[%s]: want %q, got %q", k, v, got[k])
		}
	}
}

// TestValidRefNameGuardsTheStore: `th vault add` asks this BEFORE it moves a
// value out of .env. A name IsRef will later refuse would leave the secret
// stored under a key nothing can point at and the .env holding a dead pointer —
// and the value in .env was the only other copy.
func TestValidRefName(t *testing.T) {
	long := longName(64)
	for _, c := range []struct {
		name string
		want bool
	}{
		{"STRIPE_SECRET", true},
		{long, true},
		{long + "A", false}, // 65: one over, and a real key name this long exists
		{"", false},
		{"has-a-dash", false},
		{"has.a.dot", false},
	} {
		if got := ValidRefName(c.name); got != c.want {
			t.Errorf("ValidRefName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMalformedRef: a value that opens with the prefix but is not a legal
// reference must never be mistaken for a literal — .env files travel, so a file
// written by a future treehouse gets read by this one.
func TestMalformedRef(t *testing.T) {
	for _, val := range []string{"th:", "th:v2:STRIPE_SECRET", "th:my-key", "th:" + longName(65)} {
		if !MalformedRef(val) {
			t.Errorf("MalformedRef(%q) = false — it would be passed to a child as the secret", val)
		}
	}
	for _, val := range []string{"th:STRIPE_SECRET", "sk-live-abc", "", "postgres://u:th:p@h/d"} {
		if MalformedRef(val) {
			t.Errorf("MalformedRef(%q) = true, want false", val)
		}
	}
}
