package vault

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

// service is the keychain service every treehouse entry is filed under. The
// account carries the rest of the identity.
const service = "treehouse"

// encTag marks a value treehouse encoded on the way in.
//
// It exists because `security find-generic-password -w` silently switches to
// printing HEX when the stored bytes are not all printable, and there is no
// flag to turn that off and no way to tell the hex apart from a password that
// happens to look like hex. A value with a tab in it came back as
// "7461620968657265" — a secret that round-trips into a different secret, which
// is the worst failure this package could have. So the bytes we store are
// always printable and the switch never fires.
//
// An untagged entry is passed through verbatim: somebody may have filed one by
// hand with security(1), and reading that is more useful than refusing it.
const encTag = "b64:"

// ErrNotFound is what a missing entry answers, so callers can tell "you never
// stored this" from "the keychain would not talk to us". security(1) exits 44
// for the first and something else for the second, and collapsing them would
// make a locked keychain look like an empty one.
var ErrNotFound = errors.New("no such secret")

// Account is the keychain identity of one key in one repo:
// <main worktree path>:<KEY>. It reuses the scheme the Postgres provenance
// comment already uses (treehouse:<main worktree path>:<branch>) for the same
// reason — the path is what makes two repos that both declare STRIPE_SECRET
// different, and it is the one identifier that survives a branch rename.
//
// Deliberately NOT keyed by branch: a secret belongs to the repo, so every
// worktree resolves the same value, `th rm` orphans nothing, and there is no
// state for `th gc` to chase. The same bargain E3's port registry and A4's seed
// marker already make.
// ponytail: keychain entries outlive the repo, and nothing can enumerate them.
// Deleting the repo directory leaves them forever; MOVING it makes every one of
// them unreachable, because the path IS the identity. `th gc` cannot help with
// either — security(1) offers no way to list by service short of
// dump-keychain. A stable repo identity that survives both a move and a clone
// does not exist cheaply, so this is the trade; what it costs is named in the
// not-found message, which tells the user where the value still is.
func Account(mainRoot, key string) string { return mainRoot + ":" + key }

// Available reports whether this machine has somewhere to put a secret.
// Everything else in treehouse degrades to a skip line; the vault cannot,
// because "we could not store it" must never read as "stored".
func Available() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("the vault needs the macOS keychain, and this is %s", runtime.GOOS)
	}
	if _, err := exec.LookPath("security"); err != nil {
		return fmt.Errorf("security(1) is not on PATH: %w", err)
	}
	return nil
}

// Has reports whether an entry exists WITHOUT reading it.
//
// Existence is all doctor and `th vault ls` need, and they run constantly —
// doctor is reached from ls, new, why, hook session and triage. Asking Get
// would pull every secret's cleartext into the address space of a process whose
// whole job is to not have it, on commands that have no business touching a
// value. The exit code alone answers the question.
func Has(mainRoot, key string) error {
	if err := Available(); err != nil {
		return err
	}
	// No -w: security prints the metadata and never the password.
	err := exec.Command("security", "find-generic-password",
		"-s", service, "-a", Account(mainRoot, key)).Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("keychain: %s", oneLine(err, exit))
	}
	return nil
}

// Get reads one secret.
func Get(mainRoot, key string) (string, error) {
	if err := Available(); err != nil {
		return "", err
	}
	out, err := exec.Command("security", "find-generic-password",
		"-s", service, "-a", Account(mainRoot, key), "-w").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 44 {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("keychain: %s", oneLine(err, exit))
	}
	// -w prints the stored bytes and a newline; a value may legitimately end in
	// whitespace, so trim the ONE terminator security added and nothing else.
	return decode(strings.TrimSuffix(string(out), "\n"))
}

// Set stores one secret, replacing any value already there (-U).
//
// ponytail: the value goes to the keychain on argv, so it is visible to `ps`
// for the few milliseconds the call takes. security(1) offers no way to read it
// from stdin. The upgrade path is Go keychain bindings, which is a new
// dependency bought for a window in which an attacker who could watch `ps`
// could equally read the .env the value came out of.
func Set(mainRoot, key, value string) error {
	if err := Available(); err != nil {
		return err
	}
	cmd := exec.Command("security", "add-generic-password",
		"-s", service, "-a", Account(mainRoot, key),
		"-l", service+": "+key, // the label a human sees in Keychain Access
		"-U", "-w", encTag+base64.StdEncoding.EncodeToString([]byte(value)))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain: %s", firstLine(string(out), err))
	}
	return nil
}

// Delete removes one secret. A secret that was never there is not an error:
// the caller asked for it to be gone, and it is.
func Delete(mainRoot, key string) error {
	if err := Available(); err != nil {
		return err
	}
	out, err := exec.Command("security", "delete-generic-password",
		"-s", service, "-a", Account(mainRoot, key)).CombinedOutput()
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 44 {
		return nil
	}
	return fmt.Errorf("keychain: %s", firstLine(string(out), err))
}

// Resolve turns one directory's .env vars into the values a child process
// should actually get: key → real value, for the referenced keys only.
//
// A reference that cannot be resolved is an ERROR, never an empty string. An
// app handed STRIPE_SECRET="" does not fail here, it fails four layers down in
// somebody else's stack trace, and the person reading that trace has no reason
// to suspect the vault. Every missing key is named at once, because finding out
// about them one re-run at a time is its own small hell.
func Resolve(mainRoot string, vars map[string]string) (map[string]string, error) {
	// A value that opens with the prefix but is not a legal reference is refused
	// before anything else. Passing it through as a literal would hand a child
	// the string "th:v2:STRIPE_SECRET" as its secret — failing open on the one
	// field in the file that is load-bearing for secrets.
	if malformed := envfile.MalformedRefs(vars); len(malformed) > 0 {
		return nil, fmt.Errorf("%s: not a vault reference this treehouse understands (expected %s<NAME>) — upgrade treehouse, or fix the value",
			strings.Join(malformed, ", "), envfile.RefPrefix)
	}

	refs := envfile.Refs(vars)
	if len(refs) == 0 {
		return nil, nil // nothing referenced: the store is never asked
	}
	if err := Available(); err != nil {
		return nil, fmt.Errorf("this .env references the vault, but %w", err)
	}

	resolved := make(map[string]string, len(refs))
	var missing, failed []string
	for key, name := range refs {
		switch val, err := Get(mainRoot, name); {
		case err == nil:
			resolved[key] = val
		case errors.Is(err, ErrNotFound):
			// The KEY, not the name: it is what the user types into
			// `th vault add`, and with ALIAS=th:shared the two differ.
			missing = append(missing, key)
		default:
			failed = append(failed, fmt.Sprintf("%s (%v)", key, err))
		}
	}
	sort.Strings(missing)
	sort.Strings(failed)

	switch {
	case len(failed) > 0:
		return nil, fmt.Errorf("could not read %s from the keychain", strings.Join(failed, ", "))
	case len(missing) > 0:
		return nil, fmt.Errorf("%s: no such secret in the vault — `th vault add %s` to store it%s",
			strings.Join(missing, ", "), missing[0], movedHint)
	}
	return resolved, nil
}

// movedHint is the other half of the not-found story, and it matters because
// this feature took the value out of .env: the user being told to re-add a
// secret usually does not have one to re-add. Entries are keyed by the main
// worktree's PATH, so moving or renaming the repo directory makes every one of
// them unreachable while they are all still sitting there.
const movedHint = " (if this repo moved, the value is still in Keychain Access under `treehouse: <KEY>`)"

// decode reverses Set's encoding. A stored value that is not tagged was not
// written by us, so it is returned as it stands.
//
// ponytail: that passthrough re-opens the hex hazard encTag exists to close,
// for hand-filed entries only — `security` will have printed a tab-bearing
// password as "7461620968657265" and we cannot tell that from a password that
// looks like hex. Refusing hand-filed entries outright would be worse: reading
// one somebody added in Keychain Access is the recovery path when a repo has
// moved. Store through `th vault add` and the hazard cannot arise.
func decode(stored string) (string, error) {
	if !strings.HasPrefix(stored, encTag) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encTag))
	if err != nil {
		// Tagged but unreadable: refuse rather than hand back the tag itself as
		// if it were the secret.
		return "", fmt.Errorf("keychain entry is corrupt: %w", err)
	}
	return string(raw), nil
}

// oneLine keeps a keychain failure to a single line. exec.ExitError's own
// message is useless on its own ("exit status 51"), so the stderr it captured
// is preferred when there is any.
func oneLine(err error, exit *exec.ExitError) string {
	if exit != nil && len(exit.Stderr) > 0 {
		return firstLine(string(exit.Stderr), err)
	}
	return err.Error()
}

func firstLine(out string, fallback error) string {
	if line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]); line != "" {
		return line
	}
	return fallback.Error()
}
