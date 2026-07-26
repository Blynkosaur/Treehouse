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
	{
		// Parse's half of the export rule. Set matches on the same function, so
		// the two cannot drift apart and disagree about what a line declares.
		name:  "export prefix declares the bare key",
		input: "export FOO=bar\nexport  SIX = seven \n",
		want:  map[string]string{"FOO": "bar", "SIX": "seven"},
	},
	{
		name:  "export is only stripped as a prefix word",
		input: "exported=1\nexport=2",
		want:  map[string]string{"exported": "1", "export": "2"},
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
			// Appending instead of rewriting here is what would leave an app on
			// the shared database while doctor read the appended line and agreed.
			name:    "export prefix is rewritten in place, and stays exported",
			initial: "export PORT=3000\n",
			vars:    map[string]string{"PORT": "3001"},
			want:    "export PORT=3001\n",
		},
		{
			name:    "export with extra spacing",
			initial: "export  PORT = 3000 \n",
			vars:    map[string]string{"PORT": "3001"},
			want:    "export PORT=3001\n",
		},
		{
			name:    "a key that merely starts with export is untouched",
			initial: "exported=1\n",
			vars:    map[string]string{"PORT": "3001"},
			want:    "exported=1\nPORT=3001\n",
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

// setCases are the values a derived key can plausibly carry plus the ones that
// have historically broken naive .env writers. Each asserts the same three
// things: the bytes on disk, what Parse reads back, and that a second identical
// call changes nothing (non-idempotence means every hydrate makes a diff).
func TestSetHostileValues(t *testing.T) {
	cases := []struct {
		name    string
		initial string
		key     string
		val     string
		want    string
	}{
		{"value containing equals", "A=1\n", "A", "b=c", "A=b=c\n"},
		{"value that is a url", "A=1\n", "A", "postgres://u:p@h/db?x=1", "A=postgres://u:p@h/db?x=1\n"},
		{"empty value", "A=1\n", "A", "", "A=\n"},
		{"padded value keeps its spaces", "A=1\n", "A", " pad ", "A=\" pad \"\n"},
		{"value with a hash", "A=1\n", "A", "a#b", "A=\"a#b\"\n"},
		{"value with a tab", "A=1\n", "A", "a\tb", "A=\"a\tb\"\n"},
		{"value that looks quoted", "A=1\n", "A", `"q"`, "A=\"\\\"q\\\"\"\n"},
		{"value with an apostrophe", "A=1\n", "A", "it's", "A=\"it's\"\n"},
		{"value with backslashes", "A=1\n", "A", `c:\p\x`, `A=c:\p\x` + "\n"},
		// PORT must not match PORTAL: a prefix rewrite would silently move a
		// service nobody asked about.
		{"key that prefixes another", "PORT=1\nPORTAL=2\n", "PORT", "9", "PORT=9\nPORTAL=2\n"},
		{"key that is suffixed by another", "PORT=1\nPORTAL=2\n", "PORTAL", "9", "PORT=1\nPORTAL=9\n"},
		{"whitespace around the key", "  PORT = 3000  \n", "PORT", "9", "PORT=9\n"},
		{"blank lines between keys survive", "A=1\n\n\nB=2\n", "A", "9", "A=9\n\n\nB=2\n"},
		{"file of only comments is not truncated", "# a\n# b\n", "A", "1", "# a\n# b\nA=1\n"},
		{"empty file", "", "A", "1", "A=1\n"},
		// A BOM belongs to the editor, not the key: without stripping it the
		// first key parses as "\ufeffA" and Set appends a duplicate A.
		{"leading BOM", "\ufeffA=1\n", "A", "2", "A=2\n"},
		{"quoted value in file is replaced whole", `A="old val"` + "\n", "A", "new", "A=new\n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(c.initial), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Set(path, map[string]string{c.key: c.val}); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got := readFile(t, path)
			if got != c.want {
				t.Errorf("Set()\n  got:  %q\n  want: %q", got, c.want)
			}
			if vars, _ := Parse(got); vars[c.key] != c.val {
				t.Errorf("round trip: Parse gave %q, want %q\n  file: %q", vars[c.key], c.val, got)
			}
			// Five more calls: a writer that is not a fixed point makes every
			// `th hydrate` produce a spurious diff.
			for i := 0; i < 5; i++ {
				if err := Set(path, map[string]string{c.key: c.val}); err != nil {
					t.Fatalf("repeat Set %d: %v", i, err)
				}
			}
			if again := readFile(t, path); again != got {
				t.Errorf("Set is not a fixed point\n  after 1: %q\n  after 6: %q", got, again)
			}
		})
	}
}

// TestSetParseAgree is the property the whole design rests on: Set's match rule
// is Parse's rule. For any file, setting a key Parse reported must change THAT
// key's parsed value and nothing else.
func TestSetParseAgree(t *testing.T) {
	corpus := []string{
		"A=1\nB=2\n",
		"# lead\nA=1\n\nB = 2 \n# trail\n",
		"A=1\r\nB=2\r\n",
		"A=1",
		"export A=1\nA=2\n",
		"A=\nB=\"quoted val\"\nC='single'\n",
		"\ufeffA=1\nB=2\n",
		"A=1\nA=2\nA=3\n",
		"garbage line\nA=1\n",
		"PORT=3000\nPORTAL=x\nADMIN_PORT=3001\n",
		"A=b=c\nB=#notacomment\n",
	}
	for _, content := range corpus {
		before, err := Parse(content)
		if err != nil {
			t.Fatalf("Parse(%q): %v", content, err)
		}
		for key := range before {
			t.Run(content+"/"+key, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), ".env")
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := Set(path, map[string]string{key: "SENTINEL"}); err != nil {
					t.Fatalf("Set(%q): %v", key, err)
				}
				after, err := Parse(readFile(t, path))
				if err != nil {
					t.Fatal(err)
				}
				if after[key] != "SENTINEL" {
					t.Errorf("Set(%q) did not take: Parse reads %q\n  file: %q", key, after[key], readFile(t, path))
				}
				for k, v := range before {
					if k != key && after[k] != v {
						t.Errorf("Set(%q) collaterally changed %q: %q -> %q", key, k, v, after[k])
					}
				}
				if len(after) != len(before) {
					t.Errorf("Set(%q) changed the key set: %v -> %v", key, before, after)
				}
			})
		}
	}
}

// TestWritersAgreeOnBytes: hydrate writes DATABASE_URL with Create or Append,
// derive later rewrites it with Set. Only Set quoted, so the same value round
// tripped through one writer and not the other — and a value with a `#` in a
// password came back truncated depending on which phase happened to write it.
func TestWritersAgreeOnBytes(t *testing.T) {
	values := []string{
		"plain",
		"postgres://u:p@localhost/db?sslmode=require",
		"postgres://u:pa#ss@localhost/db", // # opens a comment unless quoted
		"has spaces",
		`has "quotes"`,
		"has'single",
		"trailing ",
		"",
	}
	writers := map[string]func(string, map[string]string) error{
		"Create": Create, "Append": Append, "Set": Set,
	}
	for _, v := range values {
		t.Run(v, func(t *testing.T) {
			bytesFor := map[string]string{}
			for name, write := range writers {
				path := filepath.Join(t.TempDir(), ".env")
				if err := write(path, map[string]string{"DATABASE_URL": v}); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				body := readFile(t, path)
				if got := parse(t, body)["DATABASE_URL"]; got != v {
					t.Errorf("%s: round trip lost the value: %q -> %q\n  file: %q", name, v, got, body)
				}
				// Append's marker block is its own; compare the declaration line.
				for _, line := range strings.Split(body, "\n") {
					if strings.HasPrefix(line, "DATABASE_URL=") {
						bytesFor[name] = line
					}
				}
			}
			if bytesFor["Create"] != bytesFor["Set"] || bytesFor["Append"] != bytesFor["Set"] {
				t.Errorf("the three writers disagree on the bytes for %q: %#v", v, bytesFor)
			}
		})
	}
}

func parse(t *testing.T, body string) map[string]string {
	t.Helper()
	vars, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	return vars
}

// TestSetRejectsUnrepresentable: a pair the format cannot hold must be an error
// with the file untouched, never a best-effort write. A newline in a value used
// to inject a second key and re-append it on every run.
func TestSetRejectsUnrepresentable(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
	}{
		{"newline in value", map[string]string{"A": "x\nEVIL=1"}},
		{"carriage return in value", map[string]string{"A": "x\rEVIL=1"}},
		{"empty key", map[string]string{"": "1"}},
		{"key containing equals", map[string]string{"A=B": "1"}},
		{"key that is a comment", map[string]string{"# A": "1"}},
		{"key with surrounding space", map[string]string{" A ": "1"}},
		{"newline in key", map[string]string{"A\nB": "1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			const original = "# precious\nA=1\n"
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			for name, write := range map[string]func(string, map[string]string) error{"Set": Set, "Append": Append} {
				if err := write(path, c.vars); err == nil {
					t.Errorf("%s accepted %v", name, c.vars)
				}
				if got := readFile(t, path); got != original {
					t.Fatalf("%s touched the file anyway: %q", name, got)
				}
			}
		})
	}
}

// TestSetFollowsSymlink: a .env symlinked at a shared secrets file is a real
// layout. temp+rename replaces the LINK, which would freeze the file every other
// tool still reads at its old values while treehouse believes it wrote.
func TestSetFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shared.env")
	link := filepath.Join(dir, ".env")
	if err := os.WriteFile(target, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Set(link, map[string]string{"A": "2"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != "A=2\n" {
		t.Errorf("symlink target = %q, want A=2 — the write went to the link instead", got)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error(".env is no longer a symlink — rename replaced it")
	}
}

// TestSetLeavesNoDebris: the temp file is an implementation detail. One that
// survives a failure would be picked up by nothing, but committed by someone.
func TestSetLeavesNoDebris(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, map[string]string{"A": "2"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".env" {
		t.Errorf("directory holds %v, want just .env", entries)
	}
}

// TestSetFailureIsAtomic: an unwritable directory must leave the original file
// exactly as it was, not a truncated one.
func TestSetFailureIsAtomic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	const original = "# hand-filled\nA=1\nSECRET=x\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755) //nolint // t.TempDir cleanup needs it writable again

	if err := Set(path, map[string]string{"A": "2"}); err == nil {
		t.Fatal("Set into a read-only directory reported success")
	}
	if got := readFile(t, path); got != original {
		t.Errorf("original damaged by a failed write:\n%q", got)
	}
}

func TestParseUnquote(t *testing.T) {
	cases := []struct{ line, want string }{
		{`A="abc"`, "abc"},
		{`A='abc'`, "abc"},
		{`A=abc`, "abc"},
		{`A=""`, ""},
		{`A="\"q\""`, `"q"`},   // exactly what quote() writes for the value `"q"`
		{`A="abc`, `"abc`},     // unbalanced: not a quoted value, leave it alone
		{`A=abc"`, `abc"`},     //
		{`A="a" "b"`, `a" "b`}, // one pair off the ends, not every quote
		{`A='it''s'`, `it''s`},
	}
	for _, c := range cases {
		vars, err := Parse(c.line)
		if err != nil {
			t.Fatal(err)
		}
		if vars["A"] != c.want {
			t.Errorf("Parse(%q)[A] = %q, want %q", c.line, vars["A"], c.want)
		}
	}
}
