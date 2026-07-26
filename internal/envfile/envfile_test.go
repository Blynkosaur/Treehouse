package envfile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type envVariableItem struct {
	name  string
	input string
	want  map[string]string
}

var testItems = []envVariableItem{
	{
		name:  "simple pair",
		input: "FOO=bar\nSIX=seven",
		want:  map[string]string{"FOO": "bar", "SIX": "seven"},
	},
	{
		name:  "comments and blank lines skipped",
		input: "# database config\n\nFOO=bar\n",
		want:  map[string]string{"FOO": "bar"},
	},
	{
		name:  "empty value is present but empty",
		input: "REDIS_URL=",
		want:  map[string]string{"REDIS_URL": ""},
	},
	{
		name:  "quoted value has quotes stripped",
		input: `JWT_SECRET="abc123"`,
		want:  map[string]string{"JWT_SECRET": "abc123"},
	},
	{
		name:  "garbage line without equals is skipped",
		input: "this is not a pair\nFOO=bar",
		want:  map[string]string{"FOO": "bar"},
	},
}

func TestParse(t *testing.T) {
	for _, test := range testItems {
		t.Run(test.name, func(t *testing.T) {
			res, err := Parse(test.input)
			if err != nil {
				t.Fatal("Parse() returned idk something wrong")
			}
			if !reflect.DeepEqual(res, test.want) {
				t.Errorf("Parse(%q)\n  got:  %#v\n  want: %#v", test.input, res, test.want)
			}
		})
	}
}

func TestAppendPreservesOriginal(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	original := "# hand-written comment\nA=1\nB=custom # note\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Append(path, map[string]string{"D": "4", "C": "3"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)

	// THE regression test for the WriteFile-destroys-everything bug:
	// every original byte must still open the file.
	if !strings.HasPrefix(got, original) {
		t.Fatalf("original content damaged!\ngot:\n%s", got)
	}
	if !strings.Contains(got, Marker) {
		t.Error("appended block missing marker comment")
	}
	// Sorted, complete, and re-parseable.
	if !strings.Contains(got, "C=3\nD=4\n") {
		t.Errorf("appended keys wrong or unsorted:\n%s", got)
	}
	vars, _ := Parse(got)
	if vars["A"] != "1" || vars["C"] != "3" || vars["D"] != "4" {
		t.Errorf("round-trip parse lost keys: %v", vars)
	}
}

func TestAppendCreatesWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := Append(path, map[string]string{"A": "1"}); err != nil {
		t.Fatalf("Append to nonexistent file: %v", err)
	}
	vars, _ := Parse(readFile(t, path))
	if vars["A"] != "1" {
		t.Errorf("got %v", vars)
	}
}

func TestCreateRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("PRECIOUS=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Create(path, map[string]string{"A": "1"}); err == nil {
		t.Fatal("Create overwrote an existing file — must refuse")
	}
	if readFile(t, path) != "PRECIOUS=1\n" {
		t.Error("existing file was modified")
	}
}

func TestSet(t *testing.T) {
	cases := []struct {
		name    string
		initial string // written verbatim; absent means no file at all
		absent  bool
		vars    map[string]string
		want    string
	}{
		{
			name:    "overwrite preserves comments and key order",
			initial: "# hand-written\nA=1\nPORT=3000\nB=2\n",
			vars:    map[string]string{"PORT": "3001"},
			want:    "# hand-written\nA=1\nPORT=3001\nB=2\n",
		},
		{
			name:    "every duplicate is rewritten",
			initial: "PORT=3000\nA=1\nPORT=9999\n",
			vars:    map[string]string{"PORT": "3001"},
			want:    "PORT=3001\nA=1\nPORT=3001\n",
		},
		{
			name:    "commented-out key is not a match",
			initial: "# PORT=1\n",
			vars:    map[string]string{"PORT": "3001"},
			want:    "# PORT=1\nPORT=3001\n",
		},
		{
			name:    "export prefix is not a match (Parse reads it as 'export PORT')",
			initial: "export PORT=3000\n",
			vars:    map[string]string{"PORT": "3001"},
			want:    "export PORT=3000\nPORT=3001\n",
		},
		{
			name:    "no trailing newline",
			initial: "A=1",
			vars:    map[string]string{"B": "2"},
			want:    "A=1\nB=2\n",
		},
		{
			name:    "CRLF line endings survive",
			initial: "A=1\r\nPORT=3000\r\n",
			vars:    map[string]string{"PORT": "3001"},
			want:    "A=1\r\nPORT=3001\r\n",
		},
		{
			name:   "absent file is created",
			absent: true,
			vars:   map[string]string{"B": "2", "A": "1"},
			want:   "A=1\nB=2\n",
		},
		{
			name:    "value needing quotes is quoted",
			initial: "A=1\n",
			vars:    map[string]string{"A": "two words"},
			want:    "A=\"two words\"\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if !c.absent {
				if err := os.WriteFile(path, []byte(c.initial), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := Set(path, c.vars); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got := readFile(t, path)
			if got != c.want {
				t.Errorf("Set()\n  got:  %q\n  want: %q", got, c.want)
			}
			// Idempotence: a second identical call must not grow the file.
			if err := Set(path, c.vars); err != nil {
				t.Fatalf("second Set: %v", err)
			}
			if again := readFile(t, path); again != got {
				t.Errorf("Set is not idempotent\n  first:  %q\n  second: %q", got, again)
			}
		})
	}
}

func TestSetPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, map[string]string{"A": "2"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 — a secret-bearing .env must not widen", info.Mode().Perm())
	}
}

func TestLoadPathReportsParseError(t *testing.T) {
	// A line past bufio.Scanner's 64KB cap makes Parse fail. LoadPath used to
	// return the (nil) os error instead, handing callers an empty File and a
	// nil error — silent data loss all the way up into Discover.
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("A="+strings.Repeat("x", 70000)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPath(path); err == nil {
		t.Fatal("LoadPath swallowed a parse error")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
