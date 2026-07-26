package envfile

import (
	"bufio"
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
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, val, found := strings.Cut(line, "=")
		if found {
			key = strings.TrimSpace(key)
			val = strings.TrimSpace(val)
			val = strings.Trim(val, `"'`)
			envmap[key] = val
		}
	}
	return envmap, scanner.Err()
}

// Marker introduces every block of keys treehouse adds to an env file.
const Marker = "# --- added by treehouse hydrate ---"

// Append adds vars to the END of the file at path, under a marker comment,
// preserving every existing byte. The file is created if it doesn't exist.
// Keys are written sorted for deterministic output.
func Append(path string, vars map[string]string) error {
	if len(vars) == 0 {
		return nil
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
		b.WriteString(k + "=" + vars[k] + "\n")
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

// lineKey returns the key a line declares, or "" for none. The rule is Parse's,
// character for character — a Set that matched keys Parse doesn't read (or vice
// versa) would edit a line nobody loads. Consequence: "export PORT=3000" has key
// "export PORT" here too, so Set(PORT) leaves it alone.
func lineKey(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	key, _, found := strings.Cut(line, "=")
	if !found {
		return ""
	}
	return strings.TrimSpace(key)
}

// quote wraps values that Parse would otherwise mangle (it trims whitespace and
// strips outer quotes). ponytail: escaping is one level deep — a value with an
// embedded quote survives the write but not a byte-exact round trip.
func quote(v string) string {
	if !strings.ContainsAny(v, " \t#\"'") {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

// Create writes a brand-new env file from vars. It refuses to overwrite:
// an existing file at path is an error, never a casualty.
func Create(path string, vars map[string]string) error {
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
		if _, err := f.WriteString(k + "=" + vars[k] + "\n"); err != nil {
			return err
		}
	}
	return nil
}
