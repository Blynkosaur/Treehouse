package envfile

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type File struct {
	Path string
	Vars map[string]string
}

func LoadPath(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	content := string(data)
	varPairs, parseError := Parse(content)
	if parseError != nil {
		return File{}, parseError
	}
	return File{path, varPairs}, err
}

func Parse(envContent string) (map[string]string, error) {
	reader := strings.NewReader(envContent)
	scanner := bufio.NewScanner(reader)
	envmap := make(map[string]string)

	for scanner.Scan() {
		if key, val, ok := splitLine(scanner.Text()); ok {
			envmap[key] = val
		}
	}
	return envmap, scanner.Err()
}

// splitLine is THE rule for what a line declares — Parse reads values with it,
// Set matches keys with it. One function rather than two copies: a Set that
// matched keys Parse doesn't read (or vice versa) would edit a line nobody
// loads. A leading BOM is noise an editor left behind, not part of the key.
// bom is the byte-order mark an editor leaves at the head of a file \u2014 noise,
// never part of the first key.
const bom = "\ufeff"

func splitLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(strings.TrimPrefix(line, bom))
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	k, v, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	// `export FOO=bar` declares FOO. Reading the key as "export FOO" made Set
	// append a duplicate instead of rewriting the line — cosmetic for a port,
	// silent corruption for DATABASE_URL: the app keeps reading the original
	// line and stays on the shared database, while doctor reads the appended
	// one back and reports green.
	//
	// ponytail: the prefix is matched with a single space, as a shell writes it.
	// "export\tFOO" stays unrecognised; widen when a real file hits it.
	k = strings.TrimSpace(k)
	if rest, cut := strings.CutPrefix(k, "export "); cut {
		k = strings.TrimSpace(rest)
	}
	// A line that opens with "=" declares nothing. Reporting it as the key ""
	// broke the Set/Parse agreement in the one direction that hurts: Parse handed
	// hydrate a nameless key to fill, checkVars refused to write it, and the whole
	// `th hydrate` aborted over one stray line in somebody's .env.
	if k == "" {
		return "", "", false
	}
	return k, unquote(strings.TrimSpace(v)), true
}

// unquote reverses quote: exactly ONE surrounding pair of matching quotes, plus
// \" inside a double-quoted value. Trimming every quote character off both ends
// instead turned the value `"q"` — which quote had written as "\"q\"" — into
// `\"q\`. quote and Parse have to be inverses or Set corrupts on a round trip.
func unquote(v string) string {
	if len(v) < 2 || v[0] != v[len(v)-1] {
		return v
	}
	switch v[0] {
	case '"':
		return strings.ReplaceAll(v[1:len(v)-1], `\"`, `"`)
	case '\'':
		return v[1 : len(v)-1]
	}
	return v
}

// Marker introduces every block of keys treehouse adds to an env file.
const Marker = "# --- added by treehouse hydrate ---"

// Append adds vars to the END of the file at path, under a marker comment,
// preserving every existing byte. The file is created if it doesn't exist.
// Keys are written sorted for deterministic output.
//
// Values go through quote, exactly as Set's do. The three writers agree on the
// bytes because the same key is written by different phases — hydrate appends
// DATABASE_URL, derive later Sets it — and a value that survives one round trip
// but not the other is a difference no one can see until the app reads it back.
func Append(path string, vars map[string]string) error {
	if len(vars) == 0 {
		return nil
	}
	if err := checkVars(vars); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("\n" + Marker + "\n")
	for _, k := range keys {
		b.WriteString(k + "=" + quote(vars[k]) + "\n")
	}
	_, err = f.WriteString(b.String())
	return err
}

// Set forces vars to the given values: every existing line declaring one of the
// keys is rewritten in place, the rest are appended (sorted) at the end. No
// Marker block — the marker means "hydrate added this", and a set is an override.
//
// Because this is a whole-file rewrite it goes through a temp file and rename:
// Append can only ever damage the tail, Set could lose the entire env, and an
// env a human hand-filled is not regenerable.
func Set(path string, vars map[string]string) error {
	if len(vars) == 0 {
		return nil
	}
	if err := checkVars(vars); err != nil {
		return err
	}
	// A .env symlinked into a shared secrets dir is a real layout, and rename
	// replaces the LINK — leaving the file everything else still reads frozen at
	// the old values. Write through to what the link points at.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	lines := strings.Split(string(data), "\n")
	// A file ending in "\n" splits to a trailing empty element. Dropping it is
	// what keeps a second identical call byte-identical instead of growing a
	// blank line every run.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	var b strings.Builder
	written := map[string]bool{}
	for _, line := range lines {
		// CRLF files: match on the logical line, restore the \r on rewrite.
		text, cr := strings.CutSuffix(line, "\r")
		val, hit := vars[lineKey(text)]
		if !hit {
			b.WriteString(line + "\n")
			continue
		}
		// Rewrite EVERY match, not just the last: which duplicate wins is a
		// property of the loader, and we don't get to assume which loader.
		key := lineKey(text)
		written[key] = true
		// The rewrite rebuilds the line from the key, so the prefix has to be put
		// back: dropping it un-exports the variable for every shell that sources
		// the file, which is a value change disguised as a formatting one.
		if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(text, bom)), "export ") {
			b.WriteString("export ")
		}
		b.WriteString(key + "=" + quote(val))
		if cr {
			b.WriteString("\r")
		}
		b.WriteString("\n")
	}

	keys := make([]string, 0, len(vars))
	for k := range vars {
		if !written[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k + "=" + quote(vars[k]) + "\n")
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".env-set-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// lineKey returns the key a line declares, or "" for none. Sharing splitLine
// with Parse is the whole invariant — the match rule is Parse's rule, character
// for character, so Set can never edit a line nobody loads (or miss one).
func lineKey(line string) string {
	key, _, _ := splitLine(line)
	return key
}

// checkVars rejects pairs that cannot survive the file format. A value with a
// newline in it writes a SECOND line, which Parse then reads back as an extra
// key — and every later Set appends it again, growing the file forever. A key
// that isn't its own lineKey can never be matched on the next pass, so it too
// gets appended on every run. Both are silent corruption of a file no one can
// regenerate, so they are errors, not best-effort writes.
func checkVars(vars map[string]string) error {
	for k, v := range vars {
		if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%q: a newline is not representable in a .env line", k)
		}
		if k == "" || lineKey(k+"=") != k {
			return fmt.Errorf("%q is not a usable env key", k)
		}
	}
	return nil
}

// quote wraps values that Parse would otherwise mangle (it trims whitespace and
// strips outer quotes). unquote is its exact inverse, so Parse(Set(k,v))[k] == v
// for every value the format can hold. ponytail: escaping is one level deep —
// enough for a round trip, not a full shell-quoting implementation.
func quote(v string) string {
	if !strings.ContainsAny(v, " \t#\"'") {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

// Create writes a brand-new env file from vars. It refuses to overwrite:
// an existing file at path is an error, never a casualty. Values are quoted the
// same way Append's and Set's are — see Append.
func Create(path string, vars map[string]string) error {
	if err := checkVars(vars); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if _, err := f.WriteString(k + "=" + quote(vars[k]) + "\n"); err != nil {
			return err
		}
	}
	return nil
}
