package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/envfile"
	"github.com/Blynkosaur/treehouse/internal/vault"
	"github.com/spf13/cobra"
)

var (
	runNoRedact bool
	runCmd      = &cobra.Command{
		Use:   "run -- <cmd>...",
		Short: "Run a command with this worktree's env, without handing over the values",
		Long: `Runs the command with this worktree's .env in its environment, resolving any
th: reference out of the vault on the way. The command works; whoever asked for
it never sees the values.

  th run -- npm start       the child gets the env, you get the output
  th run -- pytest -q       and a triage verdict if it fails

Secret values are replaced with $KEY in the output, so a stack trace that prints
a connection string does not leak it either. --no-redact turns that off.`,
		// A wrapper takes its command after --, and cobra must not try to parse
		// the child's own flags as ours: `th run -- pytest -q` is one -q too many.
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: false,
		RunE:               runRun,
	}
)

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().BoolVar(&runNoRedact, "no-redact", false, "stream the child's output verbatim, secrets included")
}

func runRun(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return errors.New("nothing to run — use `th run -- <cmd>`")
	}
	root, err := worktreeRoot()
	if err != nil {
		return err
	}
	wt, err := check.Discover(root)
	if err != nil {
		return err
	}

	// Resolved BEFORE the child starts, so a missing secret is one clear error
	// instead of an app that boots half-configured and dies somewhere else.
	resolved, err := resolveVaultErr(root, wt)
	if err != nil {
		return err
	}

	out, errW := io.Writer(os.Stdout), io.Writer(os.Stderr)
	if !runNoRedact {
		out, errW = vault.NewRedactor(out, resolved), vault.NewRedactor(errW, resolved)
		// The child can exit mid-line — a bare prompt, or a kill. Flushing after
		// wrapCommand is safe because exec.Cmd.Run has already waited for both
		// copies to finish by the time it returns.
		defer func() {
			_ = vault.Flush(out)
			_ = vault.Flush(errW)
		}()
	}
	return wrapCommand("th run", args, check.Env(wt, resolved), out, errW)
}

// resolveVaultErr reads this worktree's referenced secrets, or says why it could
// not. The main worktree's path is the identity every entry is filed under, so
// every worktree of one repo resolves the same value.
func resolveVaultErr(root string, wt check.Worktree) (map[string]string, error) {
	vars := wt.EnvVarsByDir()["."]
	// AnyRef, not Refs: a MALFORMED reference is not in Refs, and a guard
	// written on Refs would short-circuit straight past the value that has to be
	// refused — handing the child "th:v2:KEY" as its secret.
	if !envfile.AnyRef(vars) {
		return nil, nil // nothing vaulted: no keychain, no prompt, no cost
	}
	mainRoot, err := check.MainWorktree(root)
	if err != nil {
		// Outside a repo there is no repo identity to file a secret under, and
		// guessing one would read somebody else's entry.
		return nil, fmt.Errorf("this .env references the vault, but this is not a git worktree: %w", err)
	}
	return vault.Resolve(mainRoot, vars)
}

// resolveVault is the same, for the callers that must not fail over the vault.
// `th doctor`'s migration-status command and `th seed` run the project's own
// tooling: a keychain that will not answer is worth a broken command and its
// own error, not a doctor that refuses to report anything at all.
func resolveVault(wt check.Worktree) map[string]string {
	resolved, _ := resolveVaultErr(wt.Root, wt)
	return resolved
}
