package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MigrationInput is what cmd learned by running the project's own tooling. The
// exit code is the load-bearing field, and that is not a shortcut — it is the
// only tool-agnostic signal that exists.
//
// A3's original AC asked for behind / ahead / diverged. Two of those are not
// obtainable from a generic status command: `alembic current` prints a revision
// hash (deciding "ahead" needs the migration DAG), Django's `showmigrations`
// prints [X]/[ ] and cannot express "ahead" at all, `prisma migrate status`
// prints prose. Parsing three formats to synthesise a verdict none of them
// reports would be a confident lie. What all three DO agree on is exiting
// non-zero when migrations are pending — so that is what we ask, and the second
// source is git, which we can read honestly.
type MigrationInput struct {
	Command string // [migrations] status; "" = unconfigured, and then nothing runs
	Pending bool   // the command exited non-zero
	Err     string // it could not be run at all (typo, missing tool)
	Dir     string // the migrations dir we diffed; "" = none found
	Added   int    // migration files THIS branch adds over the main branch
}

// CheckMigrations combines the exit code with what git says this branch added,
// which is what turns "pending" into a sentence a human can act on: pending
// with new files on this branch is your own work waiting to run, pending with
// none is main having moved ahead of you. Those want opposite reactions, and
// the exit code alone can't tell them apart.
func (d Doctor) CheckMigrations(in MigrationInput) Check {
	c := Check{Name: "migrations"}
	switch {
	case in.Command == "":
		c.Status = skip
		c.Detail = "no [migrations] status command configured — there is nothing tool-agnostic to ask"
		c.Fix = `treehouse.toml: [migrations] status = "alembic current"`
	case in.Err != "":
		c.Status = "warn"
		c.Detail = fmt.Sprintf("could not run %q: %s", in.Command, in.Err)
	case !in.Pending:
		c.Status, c.Detail = "ok", "migrations are applied"
	case in.Added > 0:
		// The honest version of "ahead — expected": we can't prove ahead, but we
		// can prove this branch adds files main doesn't have.
		c.Status = "warn"
		c.Detail = fmt.Sprintf("this branch adds %d migration file(s) under %s that have not run against its database", in.Added, in.Dir)
	case in.Dir != "":
		c.Status = "warn"
		c.Detail = "migrations are pending and this branch adds none — main moved ahead"
	default:
		// No migrations dir found, so there is nothing to diff and no way to say
		// which side moved. Report the one fact we have rather than guessing.
		c.Status = "warn"
		c.Detail = fmt.Sprintf("%q reports migrations pending", in.Command)
	}
	return c
}

// migrationDirs are the conventional locations, one per ecosystem treehouse has
// met. Inference covers the common repo; [migrations] dir sharpens it.
//
// ponytail: probed at the worktree root only, so a monorepo's
// backend/alembic/versions is invisible — that repo sets the config key. A walk
// would find it and would also find every node_modules copy of somebody else's
// migrations, which is a worse answer than none.
var migrationDirs = []string{"migrations", "alembic/versions", "prisma/migrations", "db/migrate"}

// MigrationsDir picks the directory whose git history says whether this branch
// added migrations. "" means we could not tell, and then CheckMigrations says
// so rather than inventing a verdict.
func MigrationsDir(root, configured string) string {
	if configured != "" {
		return configured
	}
	for _, rel := range migrationDirs {
		if info, err := os.Stat(filepath.Join(root, rel)); err == nil && info.IsDir() {
			return rel
		}
	}
	return ""
}

// Seed is one named dataset and the command that loads it — the un-inferable
// half of A4, and the whole of it: there is deliberately no per-seed `check`
// query. The story asked for one, on the assumption that projects track their
// own seed state; almost none do, so the config key would have been unfillable
// for nearly everybody. treehouse keeps the marker itself, in a table inside
// the worktree's own database (pg.MarkSeed), which rides the TEMPLATE copy —
// so a clone correctly inherits main's datasets — and is dropped with the
// database. No state file, nothing to garbage-collect: the same principle the
// port registry follows.
type Seed struct {
	Name    string `toml:"name"`
	Command string `toml:"command"`
}

// CheckSeed reports which of the configured datasets are actually loaded here.
func (d Doctor) CheckSeed(available []Seed, present []string) Check {
	c := Check{Name: "seed"}
	switch {
	case len(available) == 0:
		c.Status, c.Detail = skip, "no [[seed]] datasets configured"
		return c
	case len(present) == 0:
		c.Status = "warn"
		c.Detail = "no datasets loaded into this worktree's database (available: " + names(available) + ")"
		c.Fix = "th seed " + available[0].Name
		return c
	}
	c.Status, c.Detail = "ok", "datasets loaded: "+strings.Join(present, ", ")
	return c
}

func names(seeds []Seed) string {
	out := make([]string, len(seeds))
	for i, s := range seeds {
		out[i] = s.Name
	}
	return strings.Join(out, ", ")
}
