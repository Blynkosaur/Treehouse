package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/Blynkosaur/treehouse/internal/config"
	"github.com/Blynkosaur/treehouse/internal/pg"
	"github.com/spf13/cobra"
)

var (
	gcYes bool
	gcCmd = &cobra.Command{
		Use:   "gc",
		Short: "Drop the database clones whose worktrees are gone",
		RunE:  runGC,
	}
)

func init() {
	rootCmd.AddCommand(gcCmd)
	gcCmd.Flags().BoolVarP(&gcYes, "yes", "y", false, "drop without asking")
	gcCmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output; suppresses all human text")
	gcCmd.Flags().BoolVar(&quiet, "quiet", false, "print nothing")
}

// runGC is a pure adapter: ask git for the fleet, ask Postgres which databases
// carry a provenance comment, let check.PlanGC decide, then list and confirm.
func runGC(cmd *cobra.Command, args []string) error {
	root, err := worktreeRoot()
	if err != nil {
		return err
	}
	refs, err := check.Worktrees(root)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return errors.New("no worktrees found")
	}

	drops, asked, err := planGC(refs, refs[0].Path)
	if err != nil {
		return err
	}
	if jsonOut {
		if drops == nil {
			drops = []check.DBDrop{}
		}
		// "skip" is not a synonym for "ok" — the same distinction check.Check
		// draws. An empty drop list because Postgres could not be asked is not the
		// answer "there is nothing to collect", and a consumer told otherwise
		// concludes the cluster is clean when nobody looked.
		status := "ok"
		if !asked {
			status = "skip"
		}
		if err := printJSON(struct {
			Schema int            `json:"schema"`
			Root   string         `json:"root"`
			Status string         `json:"status"`
			Drops  []check.DBDrop `json:"drops"`
		}{2, refs[0].Path, status, drops}); err != nil {
			return err
		}
		if !gcYes {
			return nil // listing is the whole answer; there is no prompt to answer
		}
	}
	if !asked {
		return nil // planGC already said why; claiming "nothing to collect" would contradict it
	}
	if len(drops) == 0 {
		sayln("nothing to collect — every treehouse clone still has a worktree")
		return nil
	}

	// List first, always. The confirmation is the point of the listing: a
	// collector that can't say whose database it is about to drop is not one
	// anybody should run, which is why the branch is carried in the comment.
	if !jsonOut {
		sayln("clones whose worktree is gone:")
		for _, d := range drops {
			say("  %-40s %-10s was %s\n", d.Name, d.Size, d.Branch)
		}
	}
	if !gcYes && !confirm(fmt.Sprintf("drop %d database(s)?", len(drops))) {
		sayln("nothing dropped")
		return nil
	}
	collect(drops)
	return nil
}

// planGC builds the drop list for the repo whose main checkout is mainRoot,
// against fleet as the live set. Shared with `th rm`, which passes a fleet the
// worktree it just removed is no longer in.
//
// asked is false when Postgres could not be reached at all. It is returned
// rather than folded into the empty list because the two are different answers:
// "there is nothing to collect" and "nobody looked".
func planGC(fleet []check.Ref, mainRoot string) (drops []check.DBDrop, asked bool, err error) {
	source, err := check.Discover(mainRoot)
	if err != nil {
		return nil, false, err
	}
	if cfg, err := config.Load(mainRoot); err == nil {
		pg.Use(cfg.Database.Psql)
	}
	owned, err := pg.Commented()
	if err != nil {
		// Unreachable Postgres is a report line, not a failure: `th rm` calls this
		// after the worktree is already gone, and there is nothing to roll back.
		say("• db: skipped (postgres is not reachable: %s)\n", oneLine(err))
		return nil, false, nil
	}
	return check.Doctor{}.PlanGC(check.GCInput{
		Owned:    owned,
		Fleet:    fleet,
		Template: check.EnvDB(source),
		MainRoot: mainRoot,
		InUse:    inUse(fleet),
	}), true, nil
}

// inUse is the database each live worktree's .env actually names — the liveness
// test a branch name cannot give us. One tree walk per worktree, which `th ls`
// already pays per row and gc can certainly afford: it is about to drop
// somebody's data. A worktree that has been removed from disk answers nothing,
// which is exactly what makes `th rm`'s teardown still work.
func inUse(fleet []check.Ref) []string {
	var dbs []string
	for _, ref := range fleet {
		if w, err := check.Discover(ref.Path); err == nil {
			if db := check.EnvDB(w); db != "" {
				dbs = append(dbs, db)
			}
		}
	}
	return dbs
}

// collect drops each planned database, and refuses any that still has
// connections. Dropping out from under a running process creates exactly the
// corpse gc exists to remove, so a live session is reported and skipped — the
// database stays, and the next gc will offer it again.
func collect(drops []check.DBDrop) {
	for _, d := range drops {
		if sessions, err := pg.Sessions(d.Name); err == nil && len(sessions) > 0 {
			say("• %s: kept — %d connection(s) still open\n", d.Name, len(sessions))
			for _, s := range sessions {
				say("    %s\n", s)
			}
			continue
		}
		if err := pg.Drop(d.Name); err != nil {
			say("✗ %s: %v\n", d.Name, oneLine(err))
			continue
		}
		say("✓ dropped %s (was %s)\n", d.Name, d.Branch)
	}
}

// confirm asks before a destructive operation. A prompt nobody can answer — no
// terminal, output redirected — is a "no": the flag for scripts is -y, and the
// default has to be the safe one.
func confirm(question string) bool {
	fmt.Printf("%s [y/N] ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return err == nil && strings.EqualFold(strings.TrimSpace(line), "y")
}

// oneLine flattens a subprocess error for a single report line: psql's
// connection failure is three lines of its own.
func oneLine(err error) string { return strings.Join(strings.Fields(err.Error()), " ") }
