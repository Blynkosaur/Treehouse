package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a starter treehouse.toml in the current directory",
	RunE:  runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

// treehouseTOML is the scaffold init writes. It carries no active rules — the
// built-in node_modules/.venv defaults already work zero-config — so it leaves
// behavior unchanged and serves as a ready-to-edit reference for extending deps.
const treehouseTOML = `# treehouse.toml — shared, committed project config (like .env.example, not .env).
# treehouse works with no config; this file only *sharpens* behavior. It carries
# no secrets, so commit it: teammates and every worktree get the same setup.

# Built-in defaults, already active (no need to repeat them here):
#   node_modules  -> clone      copy-on-write from main; isolated per worktree
#   .venv / venv  -> recreate   uv, from uv.lock / pyproject.toml / requirements.txt

# Teach hydrate about another language by uncommenting / editing:

# [[deps]]
# name = "vendor/bundle"     # Ruby — dir name or worktree-relative path to find in main
# action = "clone"           # copy-on-write clone (contents are path-independent)

# [[deps]]
# name = "target"            # Rust — rebuild instead of copy
# action = "recreate"
# command = "cargo build"    # shell command, run in the dir's parent

# Override a built-in (e.g. poetry instead of uv for the venv):
# [[deps]]
# name = ".venv"
# action = "recreate"
# command = "poetry install"

# Keys treehouse must not merely warn about. Anything inferred from .env.example
# is a warning (exit 0); a key listed here missing or empty makes doctor exit 2.
# [env]
# required = ["DATABASE_URL"]

# The database clone and the .env repoint need NO config: the template, the
# connection and the clone name all come from main's own .env. Set this only if
# your Postgres isn't reachable by a plain local psql.
# [database]
# psql = "docker compose exec -T db psql"

# Migration state (th doctor --db). treehouse reads the EXIT CODE — alembic,
# Django and prisma all exit non-zero when migrations are pending, and that is
# the only signal all three agree on. dir is inferred from migrations/,
# alembic/versions/, prisma/migrations/, db/migrate/; set it if that guesses wrong.
# [migrations]
# status = "alembic current"
# dir = "db/migrate"

# Named datasets for "th seed <name>", run against this worktree's own clone.
# treehouse records what it loaded in the database itself, so there is no state
# file to keep in sync and none to garbage-collect.
# [[seed]]
# name = "ramp"
# command = "python manage.py loaddata ramp"

# Failure signatures for "th triage". The built-ins cover connection refused,
# a missing relation, and an unset env var; add the ones only your stack says.
# "needs" names the doctor fact that must AGREE before triage blames the
# environment — env | db | migration | service. Reusing a built-in's name
# replaces it.
# [[signature]]
# name = "kafka-down"
# match = "NoBrokersAvailable"
# cause = "kafka is not reachable"
# fix = "docker compose up -d kafka"
# needs = "service"
`

// runInit writes treehouseTOML to the current directory, refusing to overwrite
// an existing file (O_EXCL) — an existing config is never a casualty.
func runInit(cmd *cobra.Command, args []string) error {
	root, err := worktreeRoot()
	if err != nil {
		return err
	}
	path := filepath.Join(root, "treehouse.toml")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("treehouse.toml already exists — leaving it untouched")
		}
		return err
	}
	defer f.Close()

	if _, err := f.WriteString(treehouseTOML); err != nil {
		return err
	}
	fmt.Println("✓ wrote treehouse.toml — commit it (shared config, like .env.example)")
	return nil
}
