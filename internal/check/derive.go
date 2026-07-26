package check

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Slug turns a branch name into one identifier usable everywhere a worktree
// needs a name: COMPOSE_PROJECT_NAME ([a-z0-9][a-z0-9_-]*), a Postgres database
// name, a directory. The shared alphabet is [a-z0-9_].
//
// A hash of the original is appended whenever the mapping lost information —
// any character rewritten, or the name truncated. That suffix is load-bearing,
// not decoration: without it feat/a-b and feat-a-b slug identically, and two
// worktrees end up sharing one database. Budget: "app_wt_" + 40 + 7 = 54 bytes,
// inside Postgres's 63-byte identifier cap.
func Slug(branch string) string {
	var b strings.Builder
	lossy := false
	for _, r := range branch {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			// Case-folding is a collision too: git lets Feat and feat coexist.
			lossy = true
			b.WriteRune(r + ('a' - 'A'))
		default:
			lossy = true
			if !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_') // runs of junk collapse to a single _
			}
		}
	}

	s := strings.Trim(b.String(), "_")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "_")
		lossy = true
	}
	if !lossy {
		return s
	}

	sum := sha256.Sum256([]byte(branch))
	suffix := hex.EncodeToString(sum[:])[:6]
	if s == "" {
		return suffix // a name of pure punctuation: the hash IS the name
	}
	return s + "_" + suffix
}
