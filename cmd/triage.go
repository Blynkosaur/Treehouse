package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
	"github.com/Blynkosaur/treehouse/internal/pg"
	"github.com/spf13/cobra"
)

var (
	triageHook  bool
	triageStdin bool
	triageCmd   = &cobra.Command{
		Use:   "triage [--stdin | --hook] [-- <cmd>...]",
		Short: "Say whether a failure is the environment or the code",
		Long: `Correlates a failed command's output with this worktree's doctor state.

  th triage -- pytest -q    run it, stream it, and explain the failure afterwards
  th triage --stdin         triage output piped in
  th triage --hook          triage a Claude Code PostToolUse payload on stdin`,
		RunE: runTriage,
	}
)

func init() {
	rootCmd.AddCommand(triageCmd)
	triageCmd.Flags().BoolVar(&triageHook, "hook", false, "read a Claude Code PostToolUse JSON payload on stdin")
	triageCmd.Flags().BoolVar(&triageStdin, "stdin", false, "read raw command output on stdin")
}

func runTriage(cmd *cobra.Command, args []string) error {
	switch {
	case triageHook:
		return triageFromHook()
	case triageStdin:
		return triageFromStdin()
	case len(args) > 0:
		return triageWrap(args)
	}
	return errors.New("nothing to triage — use `th triage -- <cmd>`, `--stdin`, or `--hook`")
}

// triageWrap is the transparent-wrapper mode, and transparent is the whole
// contract: the wrapped command's stdout and stderr stream live to ours (a test
// run has to look like a test run), and its exit code becomes ours VERBATIM, so
// `th triage -- pytest` still fails a Makefile exactly like `pytest` would.
// time, env and nice all behave this way; a wrapper that swallowed the code
// would be a wrapper nobody could put in front of anything.
//
// That deliberately overloads exitCode, whose 0/1/2 are otherwise a verdict
// about the worktree — so the verdict goes to STDERR instead, where diagnostics
// belong and where it cannot corrupt a piped stdout.
func triageWrap(args []string) error {
	c := exec.Command(args[0], args[1:]...)
	c.Stdin = os.Stdin
	var tee tail
	c.Stdout = io.MultiWriter(os.Stdout, &tee)
	c.Stderr = io.MultiWriter(os.Stderr, &tee)

	err := c.Run()
	if err == nil {
		return nil // it worked; there is nothing to triage and nothing to say
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		// Never started: not a failure of the command, a failure to find it.
		fmt.Fprintf(os.Stderr, "th triage: %v\n", err)
		return exitCode(127) // the shell's own code for "command not found"
	}

	v, vErr := verdictFor("", tee.String())
	if vErr != nil {
		fmt.Fprintf(os.Stderr, "th triage: could not read doctor state: %v\n", oneLine(vErr))
	} else {
		fmt.Fprintln(os.Stderr, strings.Join(triageLines(v), "\n"))
	}

	code := exit.ExitCode()
	if code < 0 {
		// Killed by a signal, so there is no exit code to pass through. 128 is
		// the shell's "signalled" band; ponytail: the signal number itself needs
		// syscall.WaitStatus, which is not worth a build tag.
		code = 128
	}
	return exitCode(code)
}

// triageFromStdin is the pipe mode: `pytest 2>&1 | th triage --stdin`. Verdict
// JSON on stdout, and here the exit code IS the verdict — 2 for environment,
// the same code doctor uses for "your worktree is broken".
func triageFromStdin() error {
	out, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	v, err := verdictFor("", string(out))
	if err != nil {
		return err
	}
	if err := printJSON(v); err != nil {
		return err
	}
	return triageExit(v)
}

// hookPayload is the part of Claude Code's PostToolUse stdin object treehouse
// reads.
//
// The field is tool_response, NOT tool_result — verified against shipping
// first-party hook code, which disagrees with at least one local SKILL.md. The
// wrong name ships a hook that silently never fires, which is the worst
// possible failure for something whose whole job is to speak up.
type hookPayload struct {
	HookEventName string          `json:"hook_event_name"`
	ToolName      string          `json:"tool_name"`
	CWD           string          `json:"cwd"`
	ToolResponse  json.RawMessage `json:"tool_response"`
}

// triageFromHook is B2. It runs after EVERY Bash tool call, because Bash's
// tool_response carries no exit code — only stdout, stderr and interrupted —
// so there is no "did it fail" to branch on. The signature map IS the failure
// detector: nothing matched means nothing happened worth reporting, and the
// hook exits 0 without a word. That also satisfies "silent when the verdict is
// code"; the two silences are one code path, not two features.
//
// It NEVER re-runs the command. The output is already in the payload, and
// re-running would re-run `git push`, `rm -rf`, a migration.
func triageFromHook() error {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	var p hookPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		// A parse failure here means the payload shape changed under us. Saying
		// so (visible in `claude --debug`) beats going quiet, which is
		// indistinguishable from "no signature matched" for anyone testing it.
		return fmt.Errorf("reading hook payload: %w", err)
	}
	if p.ToolName != "Bash" {
		return nil // only Bash produces the kind of output signatures know
	}

	var r struct {
		Stdout      string `json:"stdout"`
		Stderr      string `json:"stderr"`
		Interrupted bool   `json:"interrupted"`
	}
	output := string(p.ToolResponse)
	if err := json.Unmarshal(p.ToolResponse, &r); err == nil {
		if r.Interrupted {
			return nil // the human hit escape; that is not a failure to explain
		}
		output = r.Stdout + "\n" + r.Stderr
	}
	// Not the shape we know (a tool error may be a bare string): a regex over
	// the raw JSON reads exactly as well as one over the fields, and going
	// silent because the envelope changed is the failure mode we are avoiding.

	root, err := hookRoot(p.CWD)
	if err != nil {
		return err
	}
	sigs, err := signatures(root)
	if err != nil {
		return err
	}
	if sig, _ := check.MatchSignature(output, sigs); sig.Name == "" {
		return nil // silent, and cheap: Postgres and git were never asked
	}

	v, err := verdictFor(root, output)
	if err != nil {
		return err
	}
	if err := emitContext("PostToolUse", strings.Join(triageLines(v), "\n")); err != nil {
		return err
	}
	return triageExit(v)
}

// triageExit is the shared contract for the two non-wrapper modes: 2 when the
// environment is the answer, 0 otherwise. Deliberately NOT a fourth code —
// `environment` is a worktree verdict, which is what doctor's 2 already means.
//
// UNVERIFIED for --hook: PostToolUse's current protocol is stdout JSON + exit 0,
// and exit 2 is the legacy blocking shape. If an observed run shows Claude Code
// treating this 2 as a blocked tool call, the fix is to return nil here for the
// hook path. See the hook section of the README.
func triageExit(v check.TriageVerdict) error {
	if v.Cause == "environment" {
		return exitCode(2)
	}
	return nil
}

// emitContext writes the PostToolUse/SessionStart output protocol: a JSON
// object on stdout with the text to hand the model, and exit 0. The older
// "write to stderr and exit 2" shape was replaced.
func emitContext(event, text string) error {
	type specific struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	return printJSON(struct {
		HookSpecificOutput specific `json:"hookSpecificOutput"`
	}{specific{event, text}})
}

// verdictFor gathers both halves of the correlation and hands them to the pure
// core. root == "" means "wherever we are standing".
func verdictFor(root, output string) (check.TriageVerdict, error) {
	var v check.TriageVerdict
	if root == "" {
		var err error
		if root, err = worktreeRoot(); err != nil {
			return v, err
		}
	}
	sigs, err := signatures(root)
	if err != nil {
		return v, err
	}
	// doctorDB is off here on purpose: triage must not run the project's own
	// migration-status command. It is somebody else's process, and a hook that
	// spawns one after every Bash call is a tax on every tool call.
	findings, checks, err := diagnose(root)
	if err != nil {
		return v, err
	}
	return check.Triage(output, findings, checks, sigs), nil
}

// signatures are the built-ins extended by main's treehouse.toml, through the
// same name-keyed Merge that [[deps]] and [[seed]] use — a repo entry with a
// built-in's name replaces it, a new name appends.
func signatures(root string) ([]check.Signature, error) {
	mainRoot, err := check.MainWorktree(root)
	if err != nil {
		return check.DefaultSignatures(), nil // outside a repo: built-ins only
	}
	cfg, err := config.Load(mainRoot)
	if err != nil {
		return nil, fmt.Errorf("reading treehouse.toml: %w", err)
	}
	pg.Use(cfg.Database.Psql)
	return config.Merge(check.DefaultSignatures(), cfg.Signature, signatureName), nil
}

func signatureName(s check.Signature) string { return s.Name }

// hookRoot is the worktree the hook is talking about. Claude Code hands us the
// tool call's own cwd, which is more reliable than whatever directory the hook
// subprocess happened to inherit.
func hookRoot(cwd string) (string, error) {
	if cwd == "" {
		return worktreeRoot()
	}
	out, err := gitOut(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return cwd, nil
	}
	return strings.TrimSpace(out), nil
}

// triageLines renders a verdict for whoever has to act on it: the cause first,
// because that is the one word both a human and an agent branch on.
//
// Hard-capped at ten lines. A verdict that scrolls is a verdict nobody reads,
// and in a hook every line is context window spent on a guess.
func triageLines(v check.TriageVerdict) []string {
	head := "th triage: " + v.Cause
	if v.Signature != "" {
		head += " (matched " + v.Signature + ")"
	}
	lines := []string{head}
	for _, e := range v.Evidence {
		if len(lines) == 6 { // 1 head + 5 evidence, leaving room for fixes
			break
		}
		lines = append(lines, "  "+e)
	}
	for _, f := range v.Fixes {
		if len(lines) == 10 {
			break
		}
		lines = append(lines, "  fix: "+f)
	}
	return lines
}

// tail keeps the LAST bytes written to it. The evidence in a failing run is at
// the end — the traceback, the summary line — so a head-capped buffer would
// hold nothing but the tests that passed before it broke.
//
// Locked because exec gives stdout and stderr a copying goroutine each.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

// tailMax is roughly a screenful of scrollback, which is more than any
// signature needs and small enough to never matter for a long build.
const tailMax = 256 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailMax {
		t.buf = t.buf[len(t.buf)-tailMax:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
