package vault

import (
	"bytes"
	"io"
	"sort"
	"strings"
)

// Redactor copies a child process's output through, replacing every resolved
// secret with $KEY on the way.
//
// This closes the leak the vault itself cannot. Taking the value out of .env
// stops the agent READING the secret; it does nothing about a program that
// prints its own connection string in a stack trace, or a `docker compose
// config` that dumps the whole environment back to a terminal the agent is
// reading. Both land in the context window exactly as if the agent had cat'd
// the file.
//
// Only values that came out of the vault are replaced. That is what lets `th
// run` skip the entire question of which keys "look like" secrets: a key is
// secret because you vaulted it, not because it is spelled a certain way.
type Redactor struct {
	w      io.Writer
	subs   []sub
	buf    []byte
	maxLen int // the longest secret, so a boundary match needs no more held back
}

type sub struct {
	value []byte
	with  []byte
}

// NewRedactor wraps w. resolved is key → secret value, the map Resolve returns.
// With nothing to hide it returns w untouched, so the common case adds no
// buffering and no copy at all.
func NewRedactor(w io.Writer, resolved map[string]string) io.Writer {
	subs := subsFor(resolved)
	if len(subs) == 0 {
		return w
	}
	// Longest first: when one secret is a prefix of another — a password and the
	// DATABASE_URL that embeds it — replacing the short one first would leave
	// the rest of the long one on screen, which is the half that matters.
	sort.Slice(subs, func(i, j int) bool { return len(subs[i].value) > len(subs[j].value) })
	return &Redactor{w: w, subs: subs, maxLen: len(subs[0].value)}
}

// Scrub is the one-shot form, for output treehouse has already COLLECTED rather
// than streamed — a migration command's CombinedOutput, a seed failure.
//
// It exists because taking the value out of .env only stops an agent reading
// the file. Once treehouse resolves a secret into a child, that child's output
// is treehouse's problem: it ends up in a doctor Detail, in --json, and in the
// SessionStart context handed to the agent.
func Scrub(s string, resolved map[string]string) string {
	subs := subsFor(resolved)
	sort.Slice(subs, func(i, j int) bool { return len(subs[i].value) > len(subs[j].value) })
	for _, sub := range subs {
		s = strings.ReplaceAll(s, string(sub.value), string(sub.with))
	}
	return s
}

// subsFor builds the replacement set, dropping what cannot safely be one.
func subsFor(resolved map[string]string) []sub {
	var subs []sub
	for key, val := range resolved {
		// An empty secret would match at every position and blank the stream.
		// A one-byte one would too, near enough.
		if len(val) < 2 {
			continue
		}
		subs = append(subs, sub{[]byte(val), []byte("$" + key)})
	}
	return subs
}

// Write scrubs what it has, then emits everything a future byte could no longer
// change: only the last maxLen-1 bytes are held back, because nothing shorter
// than the longest secret can straddle that cut.
//
// Scrubbing BEFORE choosing the cut is the load-bearing order. Cutting first
// and scrubbing the halves lets a secret split across the boundary escape
// whole, or match only a shorter secret contained in it — printing the
// structure of a connection string with just the password replaced.
//
// This is also what keeps the wrapper a wrapper. Buffering to the last NEWLINE
// meant a \r progress bar, or a `Password: ` prompt, sat in the buffer for the
// whole run, and a chatty newline-free child grew it without bound.
//
// ponytail: output shorter than maxLen-1 with no newline — a bare prompt — is
// still withheld until Flush, because there is no prefix that can be emitted
// safely. --no-redact is the escape hatch.
func (r *Redactor) Write(p []byte) (int, error) {
	r.buf = r.scrub(append(r.buf, p...))

	cut := len(r.buf) - (r.maxLen - 1)
	if cut <= 0 {
		return len(p), nil
	}
	if _, err := r.w.Write(r.buf[:cut]); err != nil {
		return 0, err
	}
	r.buf = append(r.buf[:0], r.buf[cut:]...)
	// Always the caller's own count: a short count from an io.Writer means a
	// write error, and io.MultiWriter turns one into ErrShortWrite. What we
	// hand the underlying writer is a different length by design.
	return len(p), nil
}

// Flush writes the held-back tail. Callers must call it once the child has
// exited, or the last maxLen-1 bytes are silently dropped.
func (r *Redactor) Flush() error {
	if len(r.buf) == 0 {
		return nil
	}
	_, err := r.w.Write(r.scrub(r.buf))
	r.buf = r.buf[:0]
	return err
}

func (r *Redactor) scrub(b []byte) []byte {
	for _, s := range r.subs {
		b = bytes.ReplaceAll(b, s.value, s.with)
	}
	return b
}

// Flush flushes w if it is a Redactor, and is a no-op for the bare writer
// NewRedactor hands back when there is nothing to hide.
func Flush(w io.Writer) error {
	if r, ok := w.(*Redactor); ok {
		return r.Flush()
	}
	return nil
}
