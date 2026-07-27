package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Blynkosaur/treehouse/internal/check"
	"github.com/spf13/cobra"
)

var whyCmd = &cobra.Command{
	Use:   "why",
	Short: "Say in one line what changed since everything was last green",
	RunE:  runWhy,
}

func init() {
	rootCmd.AddCommand(whyCmd)
	whyCmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output; suppresses all human text")
	// ponytail: the same flag doctor has, because the journal remembers whatever
	// the last run reported. A migrations row recorded by `th doctor --db` and
	// absent from a plain `th why` honestly reads as "nobody is asking any more";
	// pass --db here to compare the same shape of run.
	whyCmd.Flags().BoolVar(&doctorDB, "db", false, "also compare the migration and seed checks")
}

// runWhy is a pure adapter: run the same diagnosis doctor runs, read the
// journal, hand both to check.Explain, print the sentence it returns.
//
// It always exits 0, and that is the point rather than an oversight: `why`
// answers what CHANGED, not whether the worktree is healthy — doctor and ls
// already gate on that. An explainer that exits non-zero would make "ask why
// this broke" itself a failure in any script that runs it after a failure.
func runWhy(cmd *cobra.Command, args []string) error {
	root, err := worktreeRoot()
	if err != nil {
		return err
	}
	findings, checks, err := diagnose(root)
	if err != nil {
		return err
	}
	w := check.Explain(readJournal(root), check.Snapshot(root, findings, checks), currentBranch(root), time.Now())

	if jsonOut {
		changes := w.Changes
		if changes == nil {
			changes = []string{} // [] not null — consumers shouldn't special-case
		}
		return printJSON(struct {
			Schema   int      `json:"schema"`
			Root     string   `json:"root"`
			Status   string   `json:"status"`
			Answer   string   `json:"answer"`
			Changes  []string `json:"changes"`
			Baseline bool     `json:"baseline"`
		}{1, root, check.Verdict(findings, checks), w.Answer, changes, w.Baseline})
	}

	sayln(w.Answer)
	if len(w.Changes) > 1 {
		for _, line := range w.Changes {
			say("  %s\n", line)
		}
	}
	return nil
}

// recordJournal writes this run's state to the journal, and swallows every
// error on the way. That is the whole discipline: the journal is an
// optimization for `th why` and never a dependency, so a doctor that failed —
// or printed a warning — because a state file could not be written would be the
// exact thing this project refused to build.
func recordJournal(root string, findings []check.Finding, checks []check.Check) {
	path, err := journalPath(root)
	if err != nil {
		return // outside a git repo there is no .git to keep it in, and no fleet either
	}
	j := readJournal(root).Record(check.Snapshot(root, findings, checks), currentBranch(root), time.Now())
	_ = writeJournal(path, j)
}

// journalPath is the state journal, inside this worktree's own git directory.
//
// --absolute-git-dir, not <root>/.git: in a linked worktree that is a FILE
// pointing at <main>/.git/worktrees/<name>, and writing beside it would put the
// journal in the working tree where it shows up in `git status` and gets
// committed by somebody's `git add -A`. Asking git yields the per-worktree
// directory that `git worktree remove` deletes — which is what makes this the
// one state file with nothing to garbage-collect.
func journalPath(root string) (string, error) {
	out, err := gitOut(root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(strings.TrimSpace(out), "treehouse-state.json"), nil
}

// readJournal returns what the journal says, or an empty one. Missing,
// unreadable, truncated, hand-edited and written by an older schema all answer
// the same way — no baseline yet — because every one of them is a reason to
// stop trusting the file and none of them is a reason to stop working.
func readJournal(root string) check.Journal {
	path, err := journalPath(root)
	if err != nil {
		return check.Journal{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return check.Journal{}
	}
	var j check.Journal
	if err := json.Unmarshal(data, &j); err != nil || j.Schema != check.JournalSchema {
		return check.Journal{}
	}
	return j
}

// writeJournal writes temp-then-rename in the same directory, the discipline
// envfile.Set uses: a half-written journal is indistinguishable from a corrupt
// one, and while readJournal survives that, surviving it every run is not the
// same as never causing it.
func writeJournal(path string, j check.Journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".treehouse-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// currentBranch names the branch for "after you switched to feat/login".
// Detached HEAD answers "" — there is no name to blame, and "HEAD" as a branch
// name would compare unequal to every real one and blame every switch.
func currentBranch(root string) string {
	out, err := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	if branch := strings.TrimSpace(out); branch != "HEAD" {
		return branch
	}
	return ""
}
