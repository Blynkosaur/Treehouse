package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runTri runs th with stdin attached and stdout/stderr kept APART, because
// which stream a triage verdict lands on is part of its contract.
func runTri(t *testing.T, dir, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	cmd := exec.Command(thBin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		code = exit.ExitCode()
	default:
		t.Fatalf("running th: %v", err)
	}
	return out.String(), errBuf.String(), code
}

// TestTriageWrapperIsTransparent: the wrapper contract. A wrapper that changes
// the exit code or eats the output is a wrapper nobody can put in front of a
// test command.
func TestTriageWrapperIsTransparent(t *testing.T) {
	dir := driftedRepo(t)

	t.Run("the wrapped command's exit code passes through verbatim", func(t *testing.T) {
		_, _, code := runTri(t, dir, "", "triage", "--", "sh", "-c", "exit 3")
		if code != 3 {
			t.Errorf("exit %d, want 3 — `th triage -- pytest` must still fail a Makefile", code)
		}
	})

	t.Run("stdout stays stdout and the verdict stays off it", func(t *testing.T) {
		out, errOut, code := runTri(t, dir, "", "triage", "--",
			"sh", "-c", `echo "regular output"; echo "KeyError: 'KEY'" >&2; exit 1`)
		if code != 1 {
			t.Errorf("exit %d, want 1", code)
		}
		if strings.TrimSpace(out) != "regular output" {
			t.Errorf("stdout = %q — the verdict must not corrupt a piped stdout", out)
		}
		if !strings.Contains(errOut, "th triage:") {
			t.Errorf("no verdict on stderr:\n%s", errOut)
		}
		if n := len(strings.Split(strings.TrimSpace(errOut), "\n")); n > 12 {
			t.Errorf("verdict is %d lines; the cap is 10 plus the wrapped stderr", n)
		}
	})

	t.Run("a command that succeeds is not triaged", func(t *testing.T) {
		_, errOut, code := runTri(t, dir, "", "triage", "--", "sh", "-c", "exit 0")
		if code != 0 || strings.Contains(errOut, "th triage:") {
			t.Errorf("exit %d, stderr %q — nothing failed, so there is nothing to say", code, errOut)
		}
	})

	// 128+n is what a Makefile or a CI runner reads to tell a ^C (130) from an
	// OOM kill (137). A flat 128 for every signal loses exactly the distinction
	// anyone consults this number for.
	t.Run("a signalled command is 128+signal", func(t *testing.T) {
		for _, tc := range []struct {
			signal string
			want   int
		}{{"INT", 130}, {"TERM", 143}, {"KILL", 137}} {
			_, _, code := runTri(t, dir, "", "triage", "--", "sh", "-c", "kill -"+tc.signal+" $$")
			if code != tc.want {
				t.Errorf("SIG%s: exit %d, want %d", tc.signal, code, tc.want)
			}
		}
	})

	// The wrapper is meant to sit in front of a test run, so its output has to
	// arrive AS it happens — a buffer flushed at the end turns a ten-minute suite
	// into ten minutes of silence.
	t.Run("output streams rather than landing at the end", func(t *testing.T) {
		cmd := exec.Command(thBin, "triage", "--", "sh", "-c", "echo first; sleep 3; exit 1")
		cmd.Dir = dir
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cmd.Wait() }()

		got := make(chan string, 1)
		go func() {
			buf := make([]byte, 64)
			n, _ := pipe.Read(buf)
			got <- string(buf[:n])
		}()
		select {
		case line := <-got:
			if !strings.Contains(line, "first") {
				t.Errorf("first read was %q, want the wrapped command's first line", line)
			}
		case <-time.After(2 * time.Second):
			t.Error("nothing on stdout after 2s — the wrapper is buffering, not streaming")
		}
	})

	// tailMax is 256 KiB, and the evidence in a failing run is always at the
	// END: the traceback, the summary line. A head-capped buffer would hold
	// nothing but the tests that passed before it broke.
	t.Run("the tail buffer keeps the end of a large output", func(t *testing.T) {
		script := `awk 'BEGIN{for(i=0;i<8000;i++)print "filler line padding padding padding padding"}'` +
			`; echo "KeyError: 'KEY'"; exit 1`
		out, errOut, code := runTri(t, dir, "", "triage", "--", "sh", "-c", script)
		if code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if len(out) < tailMax {
			t.Fatalf("test produced %d bytes, less than the %d-byte buffer it must overflow", len(out), tailMax)
		}
		if !strings.Contains(errOut, "missing-env") {
			t.Errorf("the last line was not triaged — the buffer kept the head:\n%s", errOut)
		}
	})

	t.Run("a command that cannot be started is 127, not a verdict", func(t *testing.T) {
		_, _, code := runTri(t, dir, "", "triage", "--", "definitely-not-a-real-binary-xyz")
		if code != 127 {
			t.Errorf("exit %d, want 127 (the shell's own code for command not found)", code)
		}
	})
}

// TestTriageStdin: the exit code is the verdict in this mode, and 2 is the code
// doctor already uses for "this worktree is broken" — not a fourth code.
func TestTriageStdin(t *testing.T) {
	dir := driftedRepo(t) // .env is missing KEY, so the env area is red

	t.Run("matched signature plus red doctor is environment, exit 2", func(t *testing.T) {
		out, _, code := runTri(t, dir, "KeyError: 'KEY'\n", "triage", "--stdin")
		v := decodeVerdict(t, out)
		if v.Cause != "environment" || code != 2 {
			t.Errorf("cause %q exit %d, want environment/2\n%s", v.Cause, code, out)
		}
		if len(v.Fixes) == 0 {
			t.Error("an environment verdict with no fix makes the reader solve it twice")
		}
	})

	t.Run("nothing matched and a green doctor is code, exit 0", func(t *testing.T) {
		out, _, code := runTri(t, cleanRepo(t), "AssertionError: assert 1 == 2\n", "triage", "--stdin")
		if v := decodeVerdict(t, out); v.Cause != "code" || code != 0 {
			t.Errorf("cause %q exit %d, want code/0\n%s", v.Cause, code, out)
		}
	})

	t.Run("nothing matched but a red doctor is unknown, not code", func(t *testing.T) {
		// The red row is offered as possibly related and nothing more: no line
		// of this output connects it to the failure.
		out, _, code := runTri(t, dir, "AssertionError: assert 1 == 2\n", "triage", "--stdin")
		v := decodeVerdict(t, out)
		if v.Cause != "unknown" || code != 0 {
			t.Errorf("cause %q exit %d, want unknown/0\n%s", v.Cause, code, out)
		}
		if !strings.Contains(strings.Join(v.Evidence, "\n"), "possibly related") {
			t.Errorf("the red row was not offered:\n%s", out)
		}
	})
}

// cleanRepo is a git repo whose .env has everything .env.example declares —
// the green-doctor half of the correlation table.
func cleanRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitInit(t, dir)
	write(t, filepath.Join(dir, ".env.example"), "KEY=\n")
	write(t, filepath.Join(dir, ".env"), "KEY=1\n")
	return dir
}

// TestTriageHook drives the real PostToolUse payload shape from testdata, so
// `--hook` is testable without Claude Code in the loop.
func TestTriageHook(t *testing.T) {
	dir := driftedRepo(t)

	t.Run("the fixture payload produces additionalContext", func(t *testing.T) {
		out, _, code := runTri(t, dir, hookPayloadJSON(t, dir, nil), "triage", "--hook")
		var got struct {
			HookSpecificOutput struct {
				HookEventName     string `json:"hookEventName"`
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("hook stdout is not the output protocol: %v\n%s", err, out)
		}
		if got.HookSpecificOutput.HookEventName != "PostToolUse" {
			t.Errorf("hookEventName = %q", got.HookSpecificOutput.HookEventName)
		}
		if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "DATABASE_URL") {
			t.Errorf("context does not carry the evidence:\n%s", got.HookSpecificOutput.AdditionalContext)
		}
		if n := len(strings.Split(got.HookSpecificOutput.AdditionalContext, "\n")); n > 10 {
			t.Errorf("additionalContext is %d lines; the cap is 10", n)
		}
		// ITEM 4: the verdict rides in the payload, never in the exit code.
		// PostToolUse is stdout-JSON + exit 0; 2 is the legacy BLOCKING shape,
		// and blocking an agent's tool call is far worse than failing to hint.
		if code != 0 {
			t.Errorf("exit %d, want 0 — an `environment` verdict must not read as a blocked tool call", code)
		}
	})

	t.Run("no signature match is silent", func(t *testing.T) {
		// Bash's tool_response carries no exit code, so the signature map is the
		// only failure detector there is. Nothing matched = nothing to say.
		payload := hookPayloadJSON(t, dir, map[string]any{
			"tool_response": map[string]any{"stdout": "3 passed in 0.4s\n", "stderr": "", "interrupted": false},
		})
		out, errOut, code := runTri(t, dir, payload, "triage", "--hook")
		if out != "" || code != 0 {
			t.Errorf("stdout %q exit %d — a quiet run must be completely quiet\n%s", out, code, errOut)
		}
	})

	t.Run("a non-Bash tool is ignored", func(t *testing.T) {
		payload := hookPayloadJSON(t, dir, map[string]any{"tool_name": "Read"})
		if out, _, code := runTri(t, dir, payload, "triage", "--hook"); out != "" || code != 0 {
			t.Errorf("stdout %q exit %d", out, code)
		}
	})

	t.Run("an interrupted call is not a failure to explain", func(t *testing.T) {
		payload := hookPayloadJSON(t, dir, map[string]any{
			"tool_response": map[string]any{"stdout": "", "stderr": "KeyError: 'KEY'", "interrupted": true},
		})
		if out, _, code := runTri(t, dir, payload, "triage", "--hook"); out != "" || code != 0 {
			t.Errorf("stdout %q exit %d", out, code)
		}
	})

	// The one thing a hook must never do. It already has the output; re-running
	// would re-run `git push`, `rm -rf`, a migration.
	t.Run("the hook never re-runs the command", func(t *testing.T) {
		sentinel := filepath.Join(dir, "RE-RAN")
		payload := hookPayloadJSON(t, dir, map[string]any{
			"tool_input": map[string]any{"command": "touch " + sentinel},
		})
		runTri(t, dir, payload, "triage", "--hook")
		if _, err := os.Stat(sentinel); err == nil {
			t.Fatal("the hook re-ran tool_input.command")
		}
	})
}

// TestHookSurvivesMalformedPayloads: the hook runs after EVERY Bash tool call,
// so a shape it cannot read must cost the session nothing. The invariant is not
// "never fails" — a payload that isn't JSON deserves to be visible in
// `claude --debug` — it is that stdout is ALWAYS either empty or the output
// protocol, because Claude Code parses whatever lands there. A half-written
// object or a stray line would corrupt the session, and a hang would wedge it.
func TestHookSurvivesMalformedPayloads(t *testing.T) {
	dir := driftedRepo(t)
	fail := `{"stdout":"","stderr":"KeyError: 'KEY'","interrupted":false}`

	for _, tc := range []struct{ name, stdin string }{
		{"empty stdin", ""},
		{"not json at all", "garbage{{{"},
		{"an empty object", "{}"},
		{"an array where an object belongs", "[1,2,3]"},
		{"a bare string", `"hello"`},
		{"null tool_response", `{"tool_name":"Bash","tool_response":null}`},
		{"missing tool_response", `{"tool_name":"Bash"}`},
		{"null tool_name", `{"tool_name":null,"tool_response":` + fail + `}`},
		{"tool_response is a bare string", `{"tool_name":"Bash","tool_response":"KeyError: 'KEY'"}`},
		{"tool_response is a number", `{"tool_name":"Bash","tool_response":123}`},
		{"tool_response is an array", `{"tool_name":"Bash","tool_response":[{"text":"KeyError: 'KEY'"}]}`},
		{"null cwd", `{"tool_name":"Bash","cwd":null,"tool_response":` + fail + `}`},
		{"a cwd that no longer exists", `{"tool_name":"Bash","cwd":"/no/such/dir","tool_response":` + fail + `}`},
		{"empty stdout and stderr", `{"tool_name":"Bash","tool_response":{"stdout":"","stderr":""}}`},
		{"a megabyte of output", `{"tool_name":"Bash","tool_response":{"stdout":"` +
			strings.Repeat("x", 1<<20) + `","stderr":"KeyError: 'KEY'"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, code := runTri(t, dir, tc.stdin, "triage", "--hook")
			if code != 0 {
				t.Errorf("exit %d — the hook may only ever answer 0; it must never fail a Bash call for a reason unrelated to it", code)
			}
			if out == "" {
				return // silence is always allowed
			}
			var got struct {
				HookSpecificOutput struct {
					HookEventName     string `json:"hookEventName"`
					AdditionalContext string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("stdout is not the output protocol: %v\n%s", err, out)
			}
			if got.HookSpecificOutput.HookEventName != "PostToolUse" {
				t.Errorf("stdout is JSON but not ours: %s", out)
			}
		})
	}
}

// hookPayloadJSON loads the checked-in fixture and points it at dir, so the
// shape under test is the one a real session sends.
func hookPayloadJSON(t *testing.T, dir string, override map[string]any) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "posttooluse.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("the checked-in fixture is not valid JSON: %v", err)
	}
	payload["cwd"] = dir
	for k, v := range override {
		payload[k] = v
	}
	out, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// verdictJSON is the contract as an agent sees it — decoded from the wire, not
// from check.TriageVerdict, so a renamed json tag fails here.
type verdictJSON struct {
	Cause     string   `json:"cause"`
	Signature string   `json:"signature"`
	Evidence  []string `json:"evidence"`
	Fixes     []string `json:"fixes"`
}

func decodeVerdict(t *testing.T, out string) verdictJSON {
	t.Helper()
	var v verdictJSON
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("verdict is not JSON: %v\n%s", err, out)
	}
	return v
}

// TestHookSession is C4: the agent starts the session already knowing what is
// broken, in the fewest lines that can say it.
func TestHookSession(t *testing.T) {
	read := func(t *testing.T, out string) string {
		t.Helper()
		var got struct {
			HookSpecificOutput struct {
				HookEventName     string `json:"hookEventName"`
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("not the hook output protocol: %v\n%s", err, out)
		}
		if got.HookSpecificOutput.HookEventName != "SessionStart" {
			t.Errorf("hookEventName = %q", got.HookSpecificOutput.HookEventName)
		}
		return got.HookSpecificOutput.AdditionalContext
	}

	t.Run("a drifted worktree names the drift and the fix", func(t *testing.T) {
		out, _, code := runTri(t, driftedRepo(t), "", "hook", "session")
		ctx := read(t, out)
		if !strings.Contains(ctx, "KEY") || !strings.Contains(ctx, "th hydrate") {
			t.Errorf("context does not say what is broken or how to fix it:\n%s", ctx)
		}
		if n := len(strings.Split(ctx, "\n")); n > 8 {
			t.Errorf("context is %d lines — it is prepended to a context window, not a report", n)
		}
		if code != 0 {
			t.Errorf("exit %d — SessionStart is exit 0 whatever it finds", code)
		}
	})

	t.Run("a green worktree still says so, in one line plus the tool", func(t *testing.T) {
		out, _, code := runTri(t, cleanRepo(t), "", "hook", "session")
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		ctx := read(t, out)
		if !strings.Contains(ctx, "all clear") {
			t.Errorf("context does not report green:\n%s", ctx)
		}
		if n := len(strings.Split(ctx, "\n")); n > 2 {
			t.Errorf("a green worktree costs %d lines of context; it should cost one plus the tool line:\n%s", n, ctx)
		}
	})
}

// TestTriageSignatureFromConfig: [[signature]] is the same name-keyed extension
// point [[deps]] and [[seed]] are — a repo entry with a built-in's name takes
// it over rather than sitting behind it.
func TestTriageSignatureFromConfig(t *testing.T) {
	dir := driftedRepo(t)
	write(t, filepath.Join(dir, "treehouse.toml"), `
[[signature]]
name = "missing-env"
match = "our own weird startup error"
cause = "the app could not read its settings"
fix = "make setup"
needs = "env"
`)
	out, _, code := runTri(t, dir, "boom: our own weird startup error\n", "triage", "--stdin")
	v := decodeVerdict(t, out)
	if v.Cause != "environment" || code != 2 {
		t.Fatalf("cause %q exit %d, want environment/2\n%s", v.Cause, code, out)
	}
	if strings.Join(v.Fixes, " ") != "make setup th hydrate" {
		t.Errorf("fixes = %v, want the repo's fix and doctor's", v.Fixes)
	}

	// Taking over the name must retire the built-in, not shadow it.
	out, _, _ = runTri(t, dir, "KeyError: 'KEY'\n", "triage", "--stdin")
	if v := decodeVerdict(t, out); v.Signature != "" {
		t.Errorf("signature = %q, want none — the overridden built-in should be gone\n%s", v.Signature, out)
	}
}
