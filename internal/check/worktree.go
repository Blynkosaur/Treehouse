// Package check examines a worktree's environment state and reports findings.
package check

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/envfile"
)

// Worktree is one checked-out working directory and every env file found in it.
type Worktree struct {
	Root        string
	EnvFiles    []envfile.File
	ComposeDirs []string // dirs holding a compose file (sorted); PlanDerive namespaces each
}

// skipDirs are directory names never worth descending into: huge, generated,
// or private to tooling. Skipping them keeps Discover fast on real monorepos.
var skipDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	"vendor":        true,
	"dist":          true,
	"build":         true,
	".mypy_cache":   true,
	".ruff_cache":   true,
	".pytest_cache": true,
}

// envFileNames are the exact filenames Discover collects. Deliberately NOT a
// prefix match: .env.dev, .env.local, .env.backup.* would turn the doctor
// into a noise machine. Variants become a config option later, on purpose.
var envFileNames = map[string]bool{
	".env":         true,
	".env.example": true,
}

// composeFileNames are the filenames Docker Compose itself looks for. A dir
// holding one is a compose project root, and gets its own project name.
var composeFileNames = map[string]bool{
	"compose.yml":         true,
	"compose.yaml":        true,
	"docker-compose.yml":  true,
	"docker-compose.yaml": true,
}

// Discover walks root and returns a Worktree holding every parsed env file.
// It is deliberately lenient: unreadable directories and unparsable files are
// skipped, never fatal — a doctor that faints mid-examination helps nobody.
func Discover(root string) (Worktree, error) {
	wt := Worktree{Root: root}
	composeSeen := map[string]bool{} // a dir can hold both compose.yml and docker-compose.yml

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				// The root itself is unreadable or missing — that's not
				// leniency territory, that's a broken invocation.
				return err
			}
			// Deeper entries (permissions, vanished mid-walk): skip, keep walking.
			return nil
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				// fs.SkipDir returned from a directory prunes its ENTIRE
				// subtree — this one line is the node_modules immunity.
				return fs.SkipDir
			}
			if path != root && isRepo(path) {
				// Another checkout's files are not this worktree's services. A
				// worktree nested under main is a real layout (this repo keeps its
				// own under .claude/worktrees/), and without this main's env map
				// grows a "service" per nested worktree — which hydrate then
				// materialises in EVERY other worktree, writing main's secrets into
				// invented directories.
				return fs.SkipDir
			}
			return nil
		}
		if composeFileNames[d.Name()] {
			// Recorded during the same walk rather than probed later: PlanDerive
			// is pure, so the filesystem question has to be answered here.
			if dir := filepath.Dir(path); !composeSeen[dir] {
				composeSeen[dir] = true
				wt.ComposeDirs = append(wt.ComposeDirs, dir)
			}
			return nil
		}
		if !envFileNames[d.Name()] {
			return nil
		}
		f, err := envfile.LoadPath(path)
		if err != nil {
			return nil // lenient: a broken file is not a broken walk
		}
		wt.EnvFiles = append(wt.EnvFiles, f)
		return nil
	})
	if walkErr != nil {
		// Only reachable for catastrophic cases (e.g. root doesn't exist),
		// since our callback never returns errors of its own.
		return Worktree{}, walkErr
	}
	return wt, nil
}

// isRepo reports whether dir is the root of some other checkout. A worktree and
// a submodule carry a .git FILE, a plain clone a .git directory — one Lstat
// covers all three, and it is the only cheap question that separates "our
// subdirectory" from "somebody else's repository".
func isRepo(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// EnvVarsByDir indexes this worktree's real .env files by directory relative to
// its root: rel-dir → (key → value). Matching a service across worktrees (svc_a
// here ↔ svc_a in main) is just a lookup on the shared relative key. Only .env
// is indexed — .env.example is a reference, not a value source. Read by CheckEnv
// (fallback reference), PlanHydrate (fill values), and make (generate examples).
func (w Worktree) EnvVarsByDir() map[string]map[string]string {
	byDir := map[string]map[string]string{}
	for _, f := range w.EnvFiles {
		if filepath.Base(f.Path) != ".env" {
			continue
		}
		if rel, err := filepath.Rel(w.Root, filepath.Dir(f.Path)); err == nil {
			byDir[rel] = f.Vars
		}
	}
	return byDir
}

// Env is the environment one of the project's own commands runs in: this
// process's environment with the worktree's ROOT .env overlaid on top. Later
// entries win in os/exec, so .env is the override and the ambient shell is the
// default — that is what makes `alembic current` answer about THIS worktree's
// clone rather than whatever the shell happened to export.
//
// One builder for every caller, so `th run`, the migration-status command and
// `th seed` cannot disagree about what a project command is handed.
//
// resolved replaces the value of the keys it names — the vault references, whose
// .env value is a pointer rather than the secret. Nil when nothing is vaulted,
// which is the common case and costs nothing.
//
// A reference that resolved to NOTHING is dropped, never passed through. Handing
// a child POSTGRES_PASSWORD=th:POSTGRES_PASSWORD produces "password
// authentication failed", which names nothing and points at nothing; leaving the
// key unset produces "environment variable POSTGRES_PASSWORD is not set", which
// the missing-env signature already recognises — so the failure routes itself
// into the machinery built to explain it.
//
// ponytail: the root .env only. A monorepo whose tooling lives in a subdirectory
// with its own .env gets os.Environ and its own dotenv loading, same as today.
func Env(wt Worktree, resolved map[string]string) []string {
	env := os.Environ()
	for key, val := range wt.EnvVarsByDir()["."] {
		if secret, ok := resolved[key]; ok {
			val = secret
		} else if _, isRef := envfile.IsRef(val); isRef {
			continue
		}
		env = append(env, key+"="+val)
	}
	return env
}

// ComposeProjects names the compose projects this worktree owns: the
// COMPOSE_PROJECT_NAME E2 wrote into its .env files, minus every name the main
// checkout also claims.
//
// That subtraction is the whole guard, and it is the compose version of the
// shared-database failure. A worktree hydrate only half-finished still carries
// main's project name in its .env; tearing THAT down on `th rm` would stop the
// containers somebody is working in, from a command they ran about a different
// branch. Same rule the database side follows: only what this worktree owns.
func ComposeProjects(w, source Worktree) []string {
	shared := map[string]bool{}
	for _, vars := range source.EnvVarsByDir() {
		shared[vars["COMPOSE_PROJECT_NAME"]] = true
	}

	seen := map[string]bool{}
	var out []string
	for _, vars := range w.EnvVarsByDir() {
		name := vars["COMPOSE_PROJECT_NAME"]
		if name == "" || shared[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Ref is git's view of one worktree — the checkout as the repository knows it,
// independent of what env files live inside it. new, ls and rm all need this;
// one porcelain parser serves them all.
type Ref struct {
	Path     string
	Branch   string // short name ("feat/x"), empty when detached or bare
	Head     string // commit sha
	Detached bool
	Bare     bool
	Locked   bool
	Prunable bool

	// Filled by cmd, not by Worktrees: porcelain doesn't carry these, and
	// asking git for them once per worktree is cmd's job, not the planner's.
	Behind int  // commits in the main branch this one doesn't have
	Dirty  bool // uncommitted changes in the working tree
}

// Worktrees lists every worktree of the repository containing cwd, in git's
// order — the main worktree first, guaranteed.
func Worktrees(cwd string) ([]Ref, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = cwd
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseWorktrees(string(output)), nil
}

// parseWorktrees reads `git worktree list --porcelain`: blank-line-separated
// blocks, each opening with "worktree <path>", the rest either "key value" or a
// bare flag word. Split out from Worktrees so it can be tested without a repo.
func parseWorktrees(output string) []Ref {
	var refs []Ref
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			refs = append(refs, Ref{Path: path})
			continue
		}
		if len(refs) == 0 {
			continue // stray line before any block: nothing to attach it to
		}
		r := &refs[len(refs)-1]
		switch {
		case strings.HasPrefix(line, "HEAD "):
			r.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			r.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			r.Detached = true
		case line == "bare":
			r.Bare = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			r.Locked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			r.Prunable = true
		}
	}
	return refs
}

// MainWorktree returns the path of the repository's primary worktree — the
// first entry `git worktree list` reports (git guarantees the main checkout
// comes first). hydrate sources canonical env values from it. cwd selects the
// repository to ask; it need not be the main worktree itself.
func MainWorktree(cwd string) (string, error) {
	refs, err := Worktrees(cwd)
	if err != nil {
		return "", err
	}
	if len(refs) == 0 {
		return "", errors.New("no worktrees found")
	}
	return refs[0].Path, nil
}
