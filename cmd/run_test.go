package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Blynkosaur/treehouse/internal/vault"
)

// errRun reports what `th run` did instead of what it had to do.
type errRun struct{ what, want, got string }

func (e errRun) Error() string { return e.what + ": want " + e.want + ", got " + e.got }

// vaultRepo is a repo with one literal secret in .env, plus the keychain entry
// backing it. It returns the dir and the secret value.
func vaultRepo(t *testing.T) (dir, secret string) {
	t.Helper()
	if err := vault.Available(); err != nil {
		t.Skip(err)
	}
	dir = filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	// A value that is also a substring of another, to exercise longest-first,
	// and one that a naive shell round trip would mangle.
	secret = `sk-live-"h u#nter2"`
	write(t, filepath.Join(dir, ".env"), "PORT=3000\nSTRIPE_SECRET="+secret+"\n")
	return dir, secret
}

// TestRunInjectsWithoutHandingOver is the whole feature in one test: the child
// gets the value, the .env stops holding it, and the output never shows it.
func TestRunInjectsWithoutHandingOver(t *testing.T) {
	dir, secret := vaultRepo(t)
	envPath := filepath.Join(dir, ".env")

	// Before the vault: a literal in .env reaches the child unchanged. `th run`
	// has to be useful in a repo that has never heard of the vault.
	out, _, code := runSplit(t, dir, "run", "--", "sh", "-c", "printf %s \"$STRIPE_SECRET\"")
	if code != 0 || out != secret {
		t.Fatal(errRun{"literal .env value", secret, out})
	}

	// Vault it.
	if _, _, code := runSplit(t, dir, "vault", "add", "STRIPE_SECRET"); code != 0 {
		t.Fatal("th vault add failed")
	}
	t.Cleanup(func() { _, _, _ = runSplit(t, dir, "vault", "rm", "STRIPE_SECRET") })

	// The file an agent reads no longer carries it.
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), secret) {
		t.Fatal(errRun{".env after vault add", "no secret", string(env)})
	}
	if !strings.Contains(string(env), "STRIPE_SECRET=th:STRIPE_SECRET") {
		t.Fatal(errRun{".env after vault add", "a th: reference", string(env)})
	}

	// The child still gets the real value, byte for byte.
	out, _, code = runSplit(t, dir, "run", "--no-redact", "--", "sh", "-c", "printf %s \"$STRIPE_SECRET\"")
	if code != 0 || out != secret {
		t.Fatal(errRun{"resolved from the vault", secret, out})
	}

	// And a child that prints its own secret does not leak it either.
	out, _, code = runSplit(t, dir, "run", "--", "sh", "-c", "printf %s \"$STRIPE_SECRET\"")
	if code != 0 || out != "$STRIPE_SECRET" {
		t.Fatal(errRun{"redacted output", "$STRIPE_SECRET", out})
	}
}

// TestRunIsATransparentWrapper: the exit code and the two streams are the
// contract that lets `th run` sit in front of anything. Redaction must not cost
// any of it.
func TestRunIsATransparentWrapper(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".env"), "PORT=3000\n")

	if _, _, code := runSplit(t, dir, "run", "--", "sh", "-c", "exit 3"); code != 3 {
		t.Fatal(errRun{"exit code", "3, passed through verbatim", strconv.Itoa(code)})
	}
	if _, _, code := runSplit(t, dir, "run", "--", "definitely-not-a-binary"); code != 127 {
		t.Fatal(errRun{"a command that could not start", "127", strconv.Itoa(code)})
	}
	// The child's own flags are its own: cobra must not eat -q.
	out, _, _ := runSplit(t, dir, "run", "--", "sh", "-c", `printf %s "$*"`, "_", "-q", "--json")
	if out != "-q --json" {
		t.Fatal(errRun{"the child's flags", "-q --json", out})
	}
	// Streams stay apart, and a tail with no trailing newline still arrives —
	// the case Redactor buffers for.
	out, errOut, _ := runSplit(t, dir, "run", "--", "sh", "-c", `printf out; printf err >&2`)
	if out != "out" || errOut != "err" {
		t.Fatal(errRun{"stream separation", `"out" / "err"`, out + " / " + errOut})
	}
}

// TestRunRefusesADanglingReference: a secret that is not there must stop the
// command, not start it with an empty string. An app booted with
// STRIPE_SECRET="" fails four layers down, where nobody suspects the vault.
func TestRunRefusesADanglingReference(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".env"), "GONE=th:definitely_not_stored_anywhere\n")

	marker := filepath.Join(dir, "the-child-ran")
	_, errOut, code := runSplit(t, dir, "run", "--", "touch", marker)
	if code == 0 {
		t.Fatal(errRun{"a dangling reference", "a non-zero exit", "0"})
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal(errRun{"a dangling reference", "the child never starts", "the child ran"})
	}
	// Refusing is universal; the REASON is not. Off macOS there is no keychain
	// to be missing from, and the refusal says so — which is the documented
	// behaviour for a vaulted repo checked out on Linux, and worth pinning
	// there rather than skipping the whole test.
	if err := vault.Available(); err != nil {
		if !strings.Contains(errOut, "vault") {
			t.Fatal(errRun{"the error with no keychain", "a refusal naming the vault", errOut})
		}
		return
	}
	// The KEY, not the reference name: it is what the user types into
	// `th vault add`, and with ALIAS=th:shared the two differ.
	if !strings.Contains(errOut, "GONE") {
		t.Fatal(errRun{"the error", "the .env key of the missing secret", errOut})
	}
}

// TestVaultLsNeverPrintsValues: `th vault ls` exists so an agent can see WHICH
// keys are vaulted. The moment it prints one, the command has undone itself.
func TestVaultLsNeverPrintsValues(t *testing.T) {
	dir, secret := vaultRepo(t)
	if _, _, code := runSplit(t, dir, "vault", "add", "STRIPE_SECRET"); code != 0 {
		t.Fatal("th vault add failed")
	}
	t.Cleanup(func() { _, _, _ = runSplit(t, dir, "vault", "rm", "STRIPE_SECRET") })

	out, _, code := runSplit(t, dir, "vault", "ls")
	if code != 0 {
		t.Fatal(errRun{"vault ls with a resolvable secret", "0", strconv.Itoa(code)})
	}
	if strings.Contains(out, secret) {
		t.Fatal(errRun{"vault ls", "no values", out})
	}
	if !strings.Contains(out, "STRIPE_SECRET") {
		t.Fatal(errRun{"vault ls", "the key name", out})
	}

	// Delete the secret out from under the reference: the worktree is now
	// broken, and ls has to say so with the code doctor uses for broken.
	if _, _, code := runSplit(t, dir, "vault", "rm", "STRIPE_SECRET"); code != 0 {
		t.Fatal("th vault rm failed")
	}
	out, _, code = runSplit(t, dir, "vault", "ls")
	if code != 2 {
		t.Fatal(errRun{"vault ls with a dangling reference", "exit 2", strconv.Itoa(code)})
	}
	if !strings.Contains(out, "th vault add STRIPE_SECRET") {
		t.Fatal(errRun{"vault ls on a dangling reference", "the fix line", out})
	}
}

// TestVaultAddFromStdin covers rotation and a key .env has never declared —
// neither is reachable from the "take the literal out of .env" path.
func TestVaultAddFromStdin(t *testing.T) {
	dir, _ := vaultRepo(t)
	t.Cleanup(func() { _, _, _ = runSplit(t, dir, "vault", "rm", "API_TOKEN") })

	if _, _, code := pipeTo(t, dir, "tok-one", "vault", "add", "API_TOKEN"); code != 0 {
		t.Fatal(errRun{"vault add from stdin", "0", strconv.Itoa(code)})
	}
	out, _, _ := runSplit(t, dir, "run", "--no-redact", "--", "sh", "-c", `printf %s "$API_TOKEN"`)
	if out != "tok-one" {
		t.Fatal(errRun{"a key .env never declared", "tok-one", out})
	}
	// Rotate: the reference is already there, so the value must come from stdin.
	if _, _, code := pipeTo(t, dir, "tok-two", "vault", "add", "API_TOKEN"); code != 0 {
		t.Fatal(errRun{"rotating a vaulted key", "0", strconv.Itoa(code)})
	}
	out, _, _ = runSplit(t, dir, "run", "--no-redact", "--", "sh", "-c", `printf %s "$API_TOKEN"`)
	if out != "tok-two" {
		t.Fatal(errRun{"after rotation", "tok-two", out})
	}
}

func pipeTo(t *testing.T, dir, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(thBin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	var exit *exec.ExitError
	if err := cmd.Run(); err != nil {
		if !errors.As(err, &exit) {
			t.Fatalf("th %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	return o.String(), e.String(), code
}

// TestSessionHookStaysInBudgetWhenVaulted: the SessionStart context is
// PREPENDED TO A CONTEXT WINDOW, and the vault pointer is a second tail line.
// Nine lines is the budget whether or not it is there, so the state rows have
// to give way to it rather than the other way round.
func TestSessionHookStaysInBudgetWhenVaulted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	// Enough drift to overrun the budget on its own: several services missing
	// keys, plus a vaulted key so the second tail line fires.
	write(t, filepath.Join(dir, ".env.example"), "PORT=\nDB_URL=\nAPI_TOKEN=\n")
	write(t, filepath.Join(dir, ".env"), "API_TOKEN=th:API_TOKEN\n")
	for _, svc := range []string{"svc_a", "svc_b", "svc_c", "svc_d"} {
		write(t, filepath.Join(dir, svc, ".env.example"), "PORT=\nDB_URL=\n")
		write(t, filepath.Join(dir, svc, ".env"), "PORT=3000\n")
	}

	out, _, _ := runSplit(t, dir, "hook", "session")
	var payload struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("hook session did not emit JSON: %v\n%s", err, out)
	}
	ctx := payload.HookSpecificOutput.AdditionalContext
	if n := len(strings.Split(ctx, "\n")); n > 9 {
		t.Fatal(errRun{"session context with a vaulted key", "at most 9 lines", strconv.Itoa(n)})
	}
	if !strings.Contains(ctx, "th run -- <cmd>") {
		t.Fatal(errRun{"session context in a vaulted worktree", "the th run pointer", ctx})
	}
	if !strings.Contains(ctx, "th triage -- <cmd>") {
		t.Fatal(errRun{"session context", "the triage pointer, which the vault line must not evict", ctx})
	}
}

// TestRunDoesNotLeakThroughItsOwnVerdict is the regression for the leak this
// feature's own README example produced: the child's line was scrubbed, and
// then treehouse printed the same line back with the real value in it, because
// the triage verdict is built from the RAW tee and used to go to os.Stderr
// directly. The most common trigger there is — a command that failed.
func TestRunDoesNotLeakThroughItsOwnVerdict(t *testing.T) {
	dir, secret := vaultRepo(t)
	if _, _, code := runSplit(t, dir, "vault", "add", "STRIPE_SECRET"); code != 0 {
		t.Fatal("th vault add failed")
	}
	t.Cleanup(func() { _, _, _ = runSplit(t, dir, "vault", "rm", "STRIPE_SECRET") })

	// A failing command whose output both carries the secret and matches a
	// built-in signature, so the verdict quotes the line back as evidence.
	out, errOut, code := runSplit(t, dir, "run", "--", "sh", "-c",
		`echo "could not connect to server: $STRIPE_SECRET" >&2; exit 1`)
	if code != 1 {
		t.Fatal(errRun{"exit code", "1", strconv.Itoa(code)})
	}
	if !strings.Contains(errOut, "connection-refused") && !strings.Contains(errOut, "th run:") {
		t.Fatal(errRun{"stderr", "a triage verdict, so the leak path is actually exercised", errOut})
	}
	if strings.Contains(out+errOut, secret) {
		t.Fatal(errRun{"a failing command's streams", "no secret anywhere", out + errOut})
	}
}

// TestDoctorSaysSkipWithNoKeychain: a vaulted repo on a machine with no
// keychain is born unable to run anything. Reporting nothing would be the
// silent gap `skip` exists to prevent — and `th new` copies references verbatim
// by design, so fleets reach this state.
//
// It runs by making Available() fail the only way a test can: this is a macOS
// box, so instead assert the row's shape through the one path that is testable
// everywhere — a reference plus a keychain that answers ErrNotFound is a FAIL,
// and it is NOT the skip row.
func TestDoctorDistinguishesGoneFromUnaskable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".env"), "GONE=th:definitely_not_stored_anywhere\n")

	out, _, code := runSplit(t, dir, "doctor")
	if err := vault.Available(); err != nil {
		// No keychain: the row must exist, say skip, and not be a failure.
		if !strings.Contains(out, "vault") || code == 2 {
			t.Fatal(errRun{"doctor with no keychain", "a vault skip row, exit != 2", out})
		}
		return
	}
	// Keychain present and the secret genuinely absent: that is a failure.
	if code != 2 || !strings.Contains(out, "vault") {
		t.Fatal(errRun{"doctor with a dangling reference", "a vault FAIL row, exit 2", out})
	}
}

// TestVaultAddRefusesAnUnreferenceableName: the .env line is the only other copy
// of the value, so a name IsRef will later reject must be refused BEFORE the
// move. Storing it left the secret unreachable, .env holding a dead pointer,
// `th vault ls` blind to it, and doctor advising the command that broke it.
func TestVaultAddRefusesAnUnreferenceableName(t *testing.T) {
	dir, secret := vaultRepo(t)
	long := "A_VERY_LONG_KEY_NAME_THAT_EXCEEDS_THE_SIXTY_FOUR_CHARACTER_REFERENCE_CAP"
	write(t, filepath.Join(dir, ".env"), long+"="+secret+"\n")

	_, errOut, code := runSplit(t, dir, "vault", "add", long)
	if code == 0 {
		t.Fatal(errRun{"vault add with a 72-character key", "a refusal", "success"})
	}
	if !strings.Contains(errOut, "64") {
		t.Fatal(errRun{"the refusal", "the length rule", errOut})
	}
	// And the value is still where it was.
	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), secret) {
		t.Fatal(errRun{".env after a refused vault add", "the value untouched", string(env)})
	}
}

// TestMalformedReferenceFailsClosed: .env files travel — hydrate copies them
// verbatim into new worktrees — so a file written by a future treehouse gets
// read by this one. Passing th:v2:KEY through as a literal would hand a child
// that string as its secret: failing OPEN on the one field in the file that is
// load-bearing for secrets.
func TestMalformedReferenceFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".env"), "FUTURE=th:v2:STRIPE_SECRET\n")

	marker := filepath.Join(dir, "the-child-ran")
	_, errOut, code := runSplit(t, dir, "run", "--", "touch", marker)
	if code == 0 {
		t.Fatal(errRun{"a th: value this version cannot parse", "a refusal", "success"})
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal(errRun{"a malformed reference", "the child never starts", "the child ran"})
	}
	if !strings.Contains(errOut, "FUTURE") {
		t.Fatal(errRun{"the error", "the offending key", errOut})
	}
	// And doctor must not be silent about it either.
	out, _, code := runSplit(t, dir, "doctor")
	if code != 2 || !strings.Contains(out, "vault") {
		t.Fatal(errRun{"doctor on a malformed reference", "a vault FAIL row, exit 2", out})
	}
}

// TestVaultRmResolvesThroughEnv: `ls` and `add` are indexed by .env KEY, so `rm`
// has to be too. Treating the argument as a keychain name meant
// `th vault rm ALIAS` deleted an entry called ALIAS — which does not exist, and
// a delete of nothing is a no-op by design — then printed a checkmark. Somebody
// rotating a compromised credential was told it was gone while it was still
// readable.
func TestVaultRmResolvesThroughEnv(t *testing.T) {
	dir, _ := vaultRepo(t)
	t.Cleanup(func() { _, _, _ = runSplit(t, dir, "vault", "rm", "SHARED") })

	if _, _, code := pipeTo(t, dir, "the-value", "vault", "add", "SHARED"); code != 0 {
		t.Fatal("th vault add failed")
	}
	// A hand-written alias pointing at the same secret.
	write(t, filepath.Join(dir, ".env"), "PORT=3000\nALIAS=th:SHARED\n")

	if _, _, code := runSplit(t, dir, "vault", "rm", "ALIAS"); code != 0 {
		t.Fatal("th vault rm failed")
	}
	// If rm followed the .env pointer, SHARED is gone and ALIAS now dangles.
	out, _, code := runSplit(t, dir, "vault", "ls")
	if code != 2 {
		t.Fatal(errRun{"vault ls after rm of an alias", "exit 2, the reference now dangles", out})
	}
	if !strings.Contains(out, "ALIAS") {
		t.Fatal(errRun{"vault ls", "the broken alias named", out})
	}
}
