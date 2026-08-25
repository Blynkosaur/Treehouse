package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

// secretish are the substrings that make a key name look like it holds a
// secret. Deliberately a name heuristic and nothing cleverer: a value cannot be
// inspected for secret-ness without reading it, and entropy scoring would flag
// every hash and miss every short password.
//
// This is the INFERRED tier, so it warns. `[secrets] keys` is the curated tier
// and fails, the same progressive-configuration split `[env] required` makes
// over the keys inferred from .env.example.
var secretish = []string{"SECRET", "PASSWORD", "PASSWD", "TOKEN", "API_KEY", "PRIVATE_KEY", "CREDENTIAL"}

// looksSecret reports whether a key name suggests the value is worth hiding.
func looksSecret(key string) bool {
	upper := strings.ToUpper(key)
	for _, s := range secretish {
		if strings.Contains(upper, s) {
			return true
		}
	}
	// A bare _KEY suffix, which API_KEY/PRIVATE_KEY do not cover on their own.
	return strings.HasSuffix(upper, "_KEY")
}

// CheckSecrets reports this worktree's root .env against the vault: references
// that resolve to nothing, and values still sitting in the file in cleartext.
//
// Pure. dangling is what cmd learned by asking the keychain — the same shape
// CheckServices takes dial results and DBInput takes database names, so the
// whole table below is tested from struct literals.
//
// No rows at all when there is nothing to say. A repo with no secret-looking
// keys and no [secrets] list gets silence, not a green row: "nothing to check"
// and "checked and fine" are the two sentences this report keeps apart.
func (d Doctor) CheckSecrets(vars map[string]string, dangling []string) []Check {
	var checks []Check

	// A reference to a secret that is not there. FAIL, because every command
	// that needs the key is going to refuse to start — and the file gives no
	// hint, since a dangling reference looks exactly like a working one.
	if len(dangling) > 0 {
		sort.Strings(dangling)
		checks = append(checks, Check{
			Name:   "vault",
			Status: "fail",
			Detail: fmt.Sprintf("%s in .env, but no such secret in the keychain", nList(dangling, "reference")),
			Fix:    "th vault add " + dangling[0],
		})
	}

	// Values still in the file. Curated keys fail; inferred ones warn.
	var curated, inferred []string
	required := map[string]bool{}
	for _, key := range d.Secrets {
		required[key] = true
	}
	for key, val := range vars {
		if val == "" {
			continue // nothing to leak, and CheckEnv already nags about empties
		}
		if _, isRef := envfile.IsRef(val); isRef {
			continue
		}
		switch {
		case required[key]:
			curated = append(curated, key)
		case looksSecret(key):
			inferred = append(inferred, key)
		}
	}
	sort.Strings(curated)
	sort.Strings(inferred)

	if len(curated) > 0 {
		checks = append(checks, Check{
			Name:   "secrets",
			Status: "fail",
			Detail: fmt.Sprintf("%s in cleartext in .env, and treehouse.toml says they must not be", nList(curated, "key")),
			Fix:    "th vault add " + curated[0],
		})
	}
	if len(inferred) > 0 {
		checks = append(checks, Check{
			Name:   "secrets",
			Status: "warn",
			Detail: fmt.Sprintf("%s in cleartext in .env, readable by anything with the worktree", nList(inferred, "key")),
			Fix:    "th vault add " + inferred[0],
		})
	}
	return checks
}

// nList renders "3 keys (A, B, C)", capped so a repo with thirty of them does
// not print a paragraph into a report meant to be read at a glance.
func nList(names []string, noun string) string {
	shown, rest := names, 0
	if len(shown) > 4 {
		shown, rest = names[:4], len(names)-4
	}
	list := strings.Join(shown, ", ")
	if rest > 0 {
		list += fmt.Sprintf(", and %d more", rest)
	}
	if len(names) == 1 {
		return fmt.Sprintf("1 %s (%s) is", noun, list)
	}
	return fmt.Sprintf("%d %ss (%s) are", len(names), noun, list)
}
