package vault

import (
	"bytes"
	"io"
	"sort"
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
	w    io.Writer
	subs []sub
	buf  []byte
}

type sub struct {
	value []byte
	with  []byte
}

// NewRedactor wraps w. resolved is key → secret value, the map Resolve returns.
// With nothing to hide it returns w untouched, so the common case adds no
// buffering and no copy at all.
func NewRedactor(w io.Writer, resolved map[string]string) io.Writer {
	var subs []sub
	for key, val := range resolved {
		// An empty secret would match at every position and blank the stream.
		// A one-byte one would too, near enough.
		if len(val) < 2 {
			continue
		}
		subs = append(subs, sub{[]byte(val), []byte("$" + key)})
	}
	if len(subs) == 0 {
		return w
	}
	// Longest first: when one secret is a prefix of another — a password and the
	// DATABASE_URL that embeds it — replacing the short one first would leave
	// the rest of the long one on screen, which is the half that matters.
	sort.Slice(subs, func(i, j int) bool { return len(subs[i].value) > len(subs[j].value) })
	return &Redactor{w: w, subs: subs}
}

// Write buffers up to the last newline and redacts whole lines, so a secret
// split across two Writes by the pipe is still caught.
//
// ponytail: a value CONTAINING a newline (a PEM block pasted into .env) is
// still split across the boundary and survives. Buffering the whole stream to
// fix that would stop output streaming, which is the property `th run` is built
// on — a test run has to look like a test run. `--no-redact` is the escape
// hatch, and doctor is where a multi-line secret should be flagged.
func (r *Redactor) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	cut := bytes.LastIndexByte(r.buf, '\n')
	if cut < 0 {
		return len(p), nil
	}
	if _, err := r.w.Write(r.scrub(r.buf[:cut+1])); err != nil {
		return 0, err
	}
	r.buf = append(r.buf[:0], r.buf[cut+1:]...)
	// Always the caller's own count: a short count from an io.Writer means a
	// write error, and io.MultiWriter turns one into ErrShortWrite. What we
	// hand the underlying writer is a different length by design.
	return len(p), nil
}

// Flush writes the tail a child left without a trailing newline — a bare
// prompt, or a program killed mid-line. Callers must call it once the child has
// exited, or that tail is silently dropped.
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
