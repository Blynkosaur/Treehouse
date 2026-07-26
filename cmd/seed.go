package cmd

import (
	"fmt"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
	"github.com/Blynkosaur/treehouse/internal/pg"
	"github.com/spf13/cobra"
)

var seedCmd = &cobra.Command{
	Use:   "seed <name>",
	Short: "Load a named dataset into this worktree's database",
	Args:  cobra.ExactArgs(1),
	RunE:  runSeed,
}

func init() {
	rootCmd.AddCommand(seedCmd)
	seedCmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing")
}

// runSeed runs the project's own seed command and records that it ran, in a
// marker table inside the worktree's OWN database. treehouse is not a seeding
// framework — it wraps yours, and remembers what it wrapped.
func runSeed(cmd *cobra.Command, args []string) error {
	name := args[0]
	root, err := worktreeRoot()
	if err != nil {
		return err
	}
	mainRoot, err := check.MainWorktree(root)
	if err != nil {
		return fmt.Errorf("locating main worktree: %w", err)
	}
	cfg, err := config.Load(mainRoot)
	if err != nil {
		return fmt.Errorf("reading treehouse.toml: %w", err)
	}
	pg.Use(cfg.Database.Psql)

	seeds := config.Merge(nil, cfg.Seed, seedName)
	var seed check.Seed
	for _, s := range seeds {
		if s.Name == name {
			seed = s
		}
	}
	if seed.Command == "" {
		if len(seeds) == 0 {
			return fmt.Errorf("no [[seed]] datasets in treehouse.toml — nothing named %q to run", name)
		}
		return fmt.Errorf("no seed named %q (have: %s)", name, seedNames(seeds))
	}

	wt, err := check.Discover(root)
	if err != nil {
		return err
	}
	// The seed runs against whatever .env says, because the project's own
	// tooling reads .env — so the isolation guarantee IS the repoint, and it has
	// to be verified before we load anything. Seeding the shared database
	// because a hydrate half-applied is the loudest possible version of A2's
	// failure, and it is not recoverable by re-running anything.
	db := check.EnvDB(wt)
	source, _ := check.Discover(mainRoot)
	switch template := check.EnvDB(source); {
	case db == "":
		return fmt.Errorf("this worktree's .env names no database — nothing to seed")
	// samePath, not within: a worktree nested under main is not main, and
	// treating it as one lets `th seed` load a dataset into the SHARED database.
	case db == template && !samePath(root, mainRoot):
		return fmt.Errorf("this worktree's .env still targets the shared database %s — run `th hydrate` first", template)
	}

	say("~ seed %s: %s\n", name, seed.Command)
	if out, err := runIn(root, wt, seed.Command); err != nil {
		return fmt.Errorf("seed %s failed: %v\n%s", name, err, out)
	}

	// Recorded only after it succeeded, and only in the database it was loaded
	// into: the marker rides the TEMPLATE copy, so a clone cut from main
	// correctly inherits main's datasets, and it is dropped with the database,
	// so there is no state file for `th gc` to chase.
	if err := pg.MarkSeed(db, name); err != nil {
		say("! seed %s ran, but was not recorded in %s (%v) — doctor won't show it\n", name, db, oneLine(err))
		return nil
	}
	say("✓ seeded %s into %s\n", name, db)
	return nil
}

func seedNames(seeds []check.Seed) string {
	out := ""
	for i, s := range seeds {
		if i > 0 {
			out += ", "
		}
		out += s.Name
	}
	return out
}
