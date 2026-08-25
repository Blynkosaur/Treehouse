package vault

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAccountIsRepoScopedNotBranchScoped: two repos declaring the same key must
// not share an entry, and two branches of one repo must.
func TestAccount(t *testing.T) {
	a := Account("/Users/x/app", "STRIPE_SECRET")
	b := Account("/Users/x/other", "STRIPE_SECRET")
	if a == b {
		t.Fatal("two repos share one keychain account — one repo's secret would answer for the other")
	}
	if a != Account("/Users/x/app", "STRIPE_SECRET") {
		t.Fatal("Account is not stable, so a secret stored once could not be read back")
	}
}

// TestRedactor is the leak the vault itself cannot close: a program printing
// its own secret. The split-write case is the reason it buffers at all.
func TestRedactor(t *testing.T) {
	secrets := map[string]string{"PASSWORD": "hunter2horse", "URL": "postgres://u:hunter2horse@h/d"}

	t.Run("replaces the value with the key", func(t *testing.T) {
		var out bytes.Buffer
		w := NewRedactor(&out, secrets)
		mustWrite(t, w, "connecting with hunter2horse now\n")
		mustFlush(t, w)
		if got := out.String(); got != "connecting with $PASSWORD now\n" {
			t.Fatalf("want the value replaced, got %q", got)
		}
	})

	t.Run("a value split across two writes is still caught", func(t *testing.T) {
		var out bytes.Buffer
		w := NewRedactor(&out, secrets)
		mustWrite(t, w, "token=hunt")
		if out.Len() != 0 {
			t.Fatalf("wrote a partial line through unredacted: %q", out.String())
		}
		mustWrite(t, w, "er2horse\n")
		mustFlush(t, w)
		if got := out.String(); got != "token=$PASSWORD\n" {
			t.Fatalf("want the split value replaced, got %q", got)
		}
	})

	t.Run("the longest secret wins", func(t *testing.T) {
		// The password is a substring of the URL. Replacing the short one first
		// would leave postgres://u:$PASSWORD@h/d on screen — still a leak of the
		// host, the user and the shape of the credential.
		var out bytes.Buffer
		w := NewRedactor(&out, secrets)
		mustWrite(t, w, "dial postgres://u:hunter2horse@h/d failed\n")
		mustFlush(t, w)
		if got := out.String(); got != "dial $URL failed\n" {
			t.Fatalf("want the longest match replaced, got %q", got)
		}
	})

	t.Run("a tail with no newline needs Flush", func(t *testing.T) {
		var out bytes.Buffer
		w := NewRedactor(&out, secrets)
		mustWrite(t, w, "prompt hunter2horse>")
		mustFlush(t, w)
		if got := out.String(); got != "prompt $PASSWORD>" {
			t.Fatalf("want the tail flushed and redacted, got %q", got)
		}
	})

	// A wrapper that stops streaming is not a wrapper. Buffering to the last
	// NEWLINE meant a \r progress bar, or a "Password: " prompt, sat in the
	// buffer for the whole run — and a chatty newline-free child grew it without
	// bound. Only maxLen-1 bytes are ever held back now.
	t.Run("output with no newline still streams", func(t *testing.T) {
		var out bytes.Buffer
		w := NewRedactor(&out, secrets)
		longest := len("postgres://u:hunter2horse@h/d") // 29
		mustWrite(t, w, strings.Repeat("progress...", 20))
		if held := 220 - out.Len(); held >= longest {
			t.Fatalf("held back %d bytes with no newline in sight; only the longest secret (%d) minus one may be withheld", held, longest)
		}
		if strings.Contains(out.String(), "hunter2horse") {
			t.Fatal("a secret escaped while streaming")
		}
	})

	// The reason for the hold-back, at its exact boundary.
	t.Run("a secret split one byte at a time is still caught", func(t *testing.T) {
		var out bytes.Buffer
		w := NewRedactor(&out, secrets)
		for _, b := range []byte("xx hunter2horse yy") {
			mustWrite(t, w, string(b))
		}
		mustFlush(t, w)
		if got := out.String(); got != "xx $PASSWORD yy" {
			t.Fatalf("want the byte-by-byte value replaced, got %q", got)
		}
	})

	t.Run("nothing to hide is the same writer", func(t *testing.T) {
		var out bytes.Buffer
		var plain io.Writer = &out
		if NewRedactor(&out, nil) != plain {
			t.Fatal("with no secrets the stream should not be buffered at all")
		}
		// A one-byte secret would match nearly everywhere; it is dropped, not
		// honoured, because blanking the whole stream is not redaction.
		if NewRedactor(&out, map[string]string{"X": "a"}) != plain {
			t.Fatal("a one-byte secret must not become a redaction rule")
		}
	})
}

func mustFlush(t *testing.T, w io.Writer) {
	t.Helper()
	if err := Flush(w); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, w io.Writer, s string) {
	t.Helper()
	n, err := w.Write([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	if n != len(s) {
		t.Fatalf("short count %d for %d bytes — io.MultiWriter turns that into ErrShortWrite", n, len(s))
	}
}

// TestResolveNeverGuesses: an unresolvable reference is an error naming the key,
// never an empty value. An app booted with an empty secret fails somewhere far
// away, and nobody reading that stack trace suspects the vault.
func TestResolveNeverGuesses(t *testing.T) {
	if _, err := Resolve(t.TempDir(), map[string]string{"PORT": "3000"}); err != nil {
		t.Fatalf("no references means the keychain is never asked: %v", err)
	}
	if runtime.GOOS != "darwin" {
		t.Skip("keychain is macOS only")
	}
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("security(1) not on PATH")
	}
	root := filepath.Join(t.TempDir(), "repo")
	_, err := Resolve(root, map[string]string{"GONE": "th:definitely_not_stored_" + filepath.Base(root)})
	if err == nil {
		t.Fatal("a dangling reference resolved — the child would have booted with an empty secret")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("the error should name the key and the fix, not be a bare sentinel")
	}
}

// TestKeychainRoundTrip is the live one: store, read back byte for byte, delete.
func TestKeychainRoundTrip(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir() // scopes the account to this run, so nothing collides
	const key = "TREEHOUSE_SELFTEST"
	// Values a naive implementation loses. "tab\there" is the regression: an
	// untagged store made security(1) print the password back as hex, so the
	// secret round-tripped into a DIFFERENT secret, silently.
	for _, val := range []string{"plain", "with space", "tab\there", `qu"ote'd`, "$(id)", "sk-#hash", "trailing ", "über"} {
		if err := Set(root, key, val); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = Delete(root, key) })
		got, err := Get(root, key)
		if err != nil {
			t.Fatal(err)
		}
		if got != val {
			t.Fatalf("round trip: stored %q, read back %q", val, got)
		}
	}
	if err := Delete(root, key); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(root, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after Delete, want ErrNotFound, got %v", err)
	}
	// Deleting twice is not an error: the caller asked for it to be gone.
	if err := Delete(root, key); err != nil {
		t.Fatalf("deleting an absent secret should be a no-op: %v", err)
	}
}
