package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/envfile"
	"github.com/spf13/cobra"
)

var makeCmd = &cobra.Command{
	Use:   "make",
	Short: "Generate .env.example from each service's .env (keys only, values blanked)",
	RunE:  runMake,
}

func init() {
	rootCmd.AddCommand(makeCmd)
}

// runMake writes a .env.example beside every .env in this worktree, copying the
// keys but blanking the values — a committable template that can't leak a
// secret. It reuses Worktree.EnvVarsByDir (same index doctor/hydrate read) and
// never clobbers an existing .env.example.
func runMake(cmd *cobra.Command, args []string) error {
	root, err := worktreeRoot()
	if err != nil {
		return err
	}
	wt, err := check.Discover(root)
	if err != nil {
		return err
	}

	// Keys come from this worktree's .env files, backfilled from the main
	// checkout for any service this worktree lacks — so a fresh/empty worktree
	// still generates examples from the source of truth. Outside a git repo
	// there's simply no fallback.
	byDir := wt.EnvVarsByDir()
	if mainRoot, err := check.MainWorktree(root); err == nil {
		if main, derr := check.Discover(mainRoot); derr == nil {
			for rel, vars := range main.EnvVarsByDir() {
				if _, ok := byDir[rel]; !ok {
					byDir[rel] = vars
				}
			}
		}
	}
	if len(byDir) == 0 {
		fmt.Println("nothing to make — no .env files found here or in the main worktree")
		return nil
	}

	dirs := make([]string, 0, len(byDir))
	for rel := range byDir {
		dirs = append(dirs, rel)
	}
	sort.Strings(dirs) // stable output

	made := 0
	for _, rel := range dirs {
		blank := make(map[string]string, len(byDir[rel]))
		for k := range byDir[rel] {
			blank[k] = ""
		}
		path := filepath.Join(root, rel, ".env.example")
		display := relDir(root, filepath.Dir(path))

		switch err := envfile.Create(path, blank); {
		case os.IsExist(err):
			fmt.Printf("• %s: .env.example already exists, skipped\n", display)
		case err != nil:
			return fmt.Errorf("%s: %w", display, err)
		default:
			fmt.Printf("✓ %s: wrote .env.example (%s)\n", display, nKeys(len(blank)))
			made++
		}
	}

	if made == 0 {
		fmt.Println("nothing to make — every service already has a .env.example")
	}
	return nil
}
