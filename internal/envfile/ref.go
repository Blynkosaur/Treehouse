package envfile

import (
	"sort"
	"strings"
)

// RefPrefix marks a .env value as a pointer to a stored secret rather than the
// secret itself: STRIPE_SECRET=th:STRIPE_SECRET.
//
// The rule lives here, beside Parse and Set, because it is a fact about what a
// .env VALUE means — and because it has to be readable from the pure judgment
// layer without dragging a keychain along. internal/check asks IsRef on paths
// that must never touch a secret store; keeping the predicate in this package
// is what stops the compiler from letting them.
const RefPrefix = "th:"

// maxRefName is generous for an env key and short enough that a truncated or
// corrupted line cannot become a plausible reference.
const maxRefName = 64

// IsRef reports whether a .env value is a reference, and names what it points at.
//
// The match is WHOLE-VALUE, and that is the entire safety rule. A substring
// match would find "th:" inside postgres://user:th:pass@host/db and inside any
// URL, base64 blob or JWT that happens to contain those three bytes, and the
// failure would be silent — a real secret quietly treated as a pointer to a
// store entry that does not exist.
//
// Anchoring is why there is no minimum-length floor. Agent Vault needs one
// (their placeholders are substituted INTO paths and query strings, where a
// short token collides with real text); anchored on the whole value there is
// nothing to collide with, and a floor of 4 would misread th:KEY — a perfectly
// ordinary key name — as a literal value.
func IsRef(value string) (name string, ok bool) {
	if !strings.HasPrefix(value, RefPrefix) {
		return "", false
	}
	name = value[len(RefPrefix):]
	if !ValidRefName(name) {
		return "", false
	}
	return name, true
}

// ValidRefName reports whether name can be pointed at by a reference at all.
//
// `th vault add` has to ask BEFORE it moves a value, because the value in .env
// is the only copy: storing under a name IsRef will later refuse leaves the
// secret unreachable and the .env holding a pointer nothing resolves.
func ValidRefName(name string) bool {
	if name == "" || len(name) > maxRefName {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

// MalformedRef reports a value that opens with the prefix but is not a legal
// reference — th:v2:KEY, th:my-key, th: alone.
//
// Such a value must never be passed through as a literal. .env files travel:
// hydrate copies them verbatim into new worktrees, so a file written by a
// future treehouse gets read by this one, and handing th:v2:STRIPE_SECRET to a
// child as if it were the secret is failing open on the one field in the file
// that is load-bearing for secrets. This is what reserves the prefix for later
// without spending anything on a version scheme today.
func MalformedRef(value string) bool {
	if !strings.HasPrefix(value, RefPrefix) {
		return false
	}
	_, ok := IsRef(value)
	return !ok
}

// MalformedRefs names every key whose value opens with the prefix but is not a
// legal reference. Callers check this BEFORE deciding there is nothing to
// resolve: Refs skips a malformed value, so a guard written on Refs alone
// short-circuits straight past the thing that has to be refused.
func MalformedRefs(vars map[string]string) []string {
	var out []string
	for key, val := range vars {
		if MalformedRef(val) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// AnyRef reports whether any value even looks like a reference, malformed ones
// included — the guard for "is the vault involved at all".
func AnyRef(vars map[string]string) bool {
	for _, val := range vars {
		if strings.HasPrefix(val, RefPrefix) {
			return true
		}
	}
	return false
}

// Refs picks the referenced keys out of one directory's vars: key → the name it
// points at. Pure, so the decision is testable without a secret store.
func Refs(vars map[string]string) map[string]string {
	out := map[string]string{}
	for key, val := range vars {
		if name, ok := IsRef(val); ok {
			out[key] = name
		}
	}
	return out
}
