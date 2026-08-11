// Package vault keeps a worktree's secrets out of the files an agent reads.
//
// A .env holds a REFERENCE where the secret used to be — `STRIPE_SECRET=th:STRIPE_SECRET`
// — and the value itself lives in the macOS keychain. `th run` resolves the
// reference into the child process's environment at exec time, so the command
// works and the agent never sees the value.
//
// The threat this covers is accidental exposure: `cat .env`, a `grep -r`, a
// stack trace, a file read into a context window to answer an unrelated
// question. It is NOT a defence against a hostile process running as you —
// /usr/bin/security is the same binary for every caller, so anything running as
// you can ask the keychain the same question `th` does. Saying otherwise would
// be the kind of confident wrong claim this project refuses to make.
package vault

import "strings"

// Prefix marks a .env value as a reference to a stored secret rather than the
// secret itself.
const Prefix = "th:"

// maxName is generous for an env key and short enough that a truncated or
// corrupted line cannot become a plausible reference.
const maxName = 64

// IsRef reports whether a .env value is a reference, and names what it points at.
//
// The match is WHOLE-VALUE, and that is the entire safety rule. A substring
// match would find `th:` inside `postgres://user:th:pass@host/db` and inside
// any URL, base64 blob or JWT that happens to contain those three bytes, and
// the failure would be silent — a real secret quietly treated as a pointer to
// a keychain entry that does not exist.
//
// Anchoring is why there is no minimum-length floor here. Agent Vault needs one
// (their placeholders are substituted INTO paths and query strings, where a
// short token collides with real text); anchored on the whole value there is
// nothing to collide with, and a floor of 4 would misread `th:KEY` — a
// perfectly ordinary key name — as a literal value.
func IsRef(value string) (name string, ok bool) {
	if !strings.HasPrefix(value, Prefix) {
		return "", false
	}
	name = value[len(Prefix):]
	if name == "" || len(name) > maxName {
		return "", false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			// Anything else is a value that merely starts with "th:", not a
			// reference. A scheme-ish URL is the common case.
			return "", false
		}
	}
	return name, true
}

// Refs picks the referenced keys out of one directory's .env vars: key → the
// name it points at. Pure, so the decision is testable without a keychain.
func Refs(vars map[string]string) map[string]string {
	out := map[string]string{}
	for key, val := range vars {
		if name, ok := IsRef(val); ok {
			out[key] = name
		}
	}
	return out
}
