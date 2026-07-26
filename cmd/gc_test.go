package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestGCWithoutAReachableCluster: `th gc` is the one command that deletes data,
// so the one thing it must never do is answer a question nobody asked. An empty
// drop list because psql could not connect is NOT "the cluster is clean", and a
// CI job reading status:ok off that concludes there are no orphans when nothing
// was ever looked at.
//
// [database] psql points at a command that always fails, which is the whole
// cluster-free way to reach this path.
func TestGCWithoutAReachableCluster(t *testing.T) {
	main := gitignoredEnvRepo(t)
	write(t, filepath.Join(main, ".env"), "PORT=3000\nDATABASE_URL=postgres://localhost/appdb\n")
	write(t, filepath.Join(main, "treehouse.toml"), "[database]\npsql = \"false\"\n")

	t.Run("json says skip, not ok", func(t *testing.T) {
		out, _, code := runSplit(t, main, "gc", "--json")
		if code != 0 {
			t.Fatalf("exit %d — an unreachable cluster is a report line, not a failure\n%s", code, out)
		}
		var got struct {
			Status string `json:"status"`
			Drops  []any  `json:"drops"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("gc --json is not JSON: %v\n%s", err, out)
		}
		if got.Status != "skip" {
			t.Errorf("status = %q, want skip — nobody looked, so nothing was found is not the answer", got.Status)
		}
		if len(got.Drops) != 0 {
			t.Errorf("drops = %v, want empty", got.Drops)
		}
	})

	t.Run("the human face does not contradict itself", func(t *testing.T) {
		out, _, _ := runSplit(t, main, "gc")
		if !strings.Contains(out, "not reachable") {
			t.Errorf("the skip was never explained:\n%s", out)
		}
		if strings.Contains(out, "nothing to collect") {
			t.Errorf("claimed every clone still has a worktree right after saying it could not ask:\n%s", out)
		}
	})
}
