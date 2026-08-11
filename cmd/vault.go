package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/envfile"
	"github.com/Blynkosaur/treehouse/internal/vault"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

var (
	vaultCmd = &cobra.Command{
		Use:   "vault",
		Short: "Keep a secret out of .env and hand it to `th run` instead",
		Long: `The value moves to the macOS keychain and .env keeps a reference:

  STRIPE_SECRET=th:STRIPE_SECRET

An agent reading .env learns the key names and nothing else, while
` + "`th run -- <cmd>`" + ` still gives the command the real value.

This covers ACCIDENTAL exposure — a cat, a grep, a stack trace, a file read into
a context window. It is not a wall against a hostile process running as you:
security(1) is the same binary for every caller.`,
	}
	vaultAddCmd = &cobra.Command{
		Use:   "add <KEY>",
		Short: "Move a key's value into the keychain and leave a reference behind",
		Args:  cobra.ExactArgs(1),
		RunE:  runVaultAdd,
	}
	vaultLsCmd = &cobra.Command{
		Use:   "ls",
		Short: "List the vaulted keys, and whether each one still resolves",
		Args:  cobra.NoArgs,
		RunE:  runVaultLs,
	}
	vaultRmCmd = &cobra.Command{
		Use:   "rm <KEY>",
		Short: "Delete a secret from the keychain",
		Args:  cobra.ExactArgs(1),
		RunE:  runVaultRm,
	}
)

func init() {
	rootCmd.AddCommand(vaultCmd)
	vaultCmd.AddCommand(vaultAddCmd, vaultLsCmd, vaultRmCmd)
}

// vaultScope is the repo a secret belongs to and this worktree's root .env.
//
// The root .env only, matching check.Env's ceiling: `th run` injects that file,
// so vaulting a key in a subdirectory's .env would move a value nothing would
// ever put back.
func vaultScope() (mainRoot, envPath string, vars map[string]string, err error) {
	root, err := worktreeRoot()
	if err != nil {
		return "", "", nil, err
	}
	if mainRoot, err = check.MainWorktree(root); err != nil {
		return "", "", nil, fmt.Errorf("the vault is scoped to a repo, and this is not a git worktree: %w", err)
	}
	wt, err := check.Discover(root)
	if err != nil {
		return "", "", nil, err
	}
	vars = wt.EnvVarsByDir()["."]
	if vars == nil {
		vars = map[string]string{}
	}
	return mainRoot, filepath.Join(root, ".env"), vars, nil
}

func runVaultAdd(cmd *cobra.Command, args []string) error {
	key := args[0]
	mainRoot, envPath, vars, err := vaultScope()
	if err != nil {
		return err
	}
	if err := vault.Available(); err != nil {
		return err
	}

	value, from, err := valueToStore(key, vars)
	if err != nil {
		return err
	}

	// Store BEFORE rewriting. The other order loses the secret outright when the
	// keychain refuses: .env would already be a reference to nothing, and the
	// only copy of the value would have been the line we just overwrote.
	if err := vault.Set(mainRoot, key, value); err != nil {
		return err
	}
	if err := envfile.Set(envPath, map[string]string{key: vault.Prefix + key}); err != nil {
		return fmt.Errorf("%s is in the keychain, but .env still holds the value (%v) — fix .env by hand", key, err)
	}
	say("✓ %s: stored (%s), .env now reads %s%s\n", key, from, vault.Prefix, key)
	say("  run commands as `th run -- <cmd>` so they still get the value\n")
	return nil
}

// valueToStore decides what to put in the keychain: the live .env value when it
// is still a literal, otherwise whatever was piped in.
//
// Piping is what makes rotation and a not-yet-declared key possible. It is only
// consulted when stdin is NOT a terminal, so an interactive `th vault add` on a
// key that is already vaulted says what to do instead of hanging on a read
// nobody is going to satisfy.
func valueToStore(key string, vars map[string]string) (value, from string, err error) {
	current, declared := vars[key]
	_, isRef := vault.IsRef(current)

	if declared && !isRef {
		if current == "" {
			return "", "", fmt.Errorf("%s is empty in .env — there is nothing to store", key)
		}
		return current, "from .env", nil
	}
	if isatty.IsTerminal(os.Stdin.Fd()) {
		switch {
		case isRef:
			return "", "", fmt.Errorf("%s is already vaulted — pipe a new value to rotate it: printf %%s '<value>' | th vault add %s", key, key)
		default:
			return "", "", fmt.Errorf("%s is not in .env — pipe the value in: printf %%s '<value>' | th vault add %s", key, key)
		}
	}
	piped, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", "", err
	}
	// Read as given, minus one trailing newline: `echo` adds one, and a secret
	// that genuinely ends in a newline is not expressible on a pipe anyway.
	if n := len(piped); n > 0 && piped[n-1] == '\n' {
		piped = piped[:n-1]
	}
	if len(piped) == 0 {
		return "", "", fmt.Errorf("nothing on stdin — there is no value to store for %s", key)
	}
	return string(piped), "from stdin", nil
}

func runVaultLs(cmd *cobra.Command, args []string) error {
	mainRoot, _, vars, err := vaultScope()
	if err != nil {
		return err
	}
	refs := vault.Refs(vars)
	if len(refs) == 0 {
		sayln("no vaulted keys in this worktree's .env — `th vault add <KEY>` to move one")
		return nil
	}
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Names and status, never values. A `vault ls` that printed the secrets
	// would undo the whole command.
	bad := false
	for _, key := range keys {
		switch _, err := vault.Get(mainRoot, refs[key]); {
		case err == nil:
			say("✓ %s → %s%s\n", key, vault.Prefix, refs[key])
		case errors.Is(err, vault.ErrNotFound):
			bad = true
			say("✗ %s → %s%s: no such secret — `th vault add %s` to store it\n", key, vault.Prefix, refs[key], key)
		default:
			bad = true
			say("✗ %s → %s%s: %v\n", key, vault.Prefix, refs[key], oneLine(err))
		}
	}
	if bad {
		// The same code doctor uses for a broken worktree: a dangling reference
		// means every command that needs that key is going to refuse to start.
		return exitCode(2)
	}
	return nil
}

func runVaultRm(cmd *cobra.Command, args []string) error {
	key := args[0]
	mainRoot, _, vars, err := vaultScope()
	if err != nil {
		return err
	}
	if err := vault.Delete(mainRoot, key); err != nil {
		return err
	}
	say("✓ %s: removed from the keychain\n", key)

	// Deliberately NOT rewriting .env: putting the value back would mean having
	// kept a copy of it, which is the thing this command exists to avoid. So say
	// the worktree is now broken rather than quietly leaving it that way.
	if name, ok := vault.IsRef(vars[key]); ok && name == key {
		say("! .env still reads %s=%s%s, so `th run` will refuse until you set a value or re-add it\n",
			key, vault.Prefix, key)
	}
	return nil
}
