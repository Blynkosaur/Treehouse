// Package pg creates and removes the database clones check decides about — the
// mutating "doer" half, mirroring internal/deps.
//
// It shells out to psql rather than opening a connection with database/sql.
// That is zero new dependencies, and libpq already understands
// PGHOST/PGPORT/PGUSER/PGPASSWORD, ~/.pgpass and every connstring form the
// user's own tooling works with — so treehouse reaches the same cluster the app
// does without reimplementing any of it.
//
// Everything goes through psql, including the create and the drop, rather than
// through createdb/dropdb. Those two wrappers buy nothing here (they quote the
// identifiers, and check.Ident has already refused anything that would need
// quoting) and they cost the one thing that matters: a repo whose Postgres only
// exists inside compose sets [database] psql and gets ALL of treehouse, not just
// the read-only half.
package pg

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/Blynkosaur/treehouse/internal/check"
)

// ErrBusy is Postgres refusing to clone or drop a database somebody is
// connected to. This is the COMMON case, not an edge one: a running dev server
// holds a connection. Retrying is useless — the app reconnects instantly — so
// it surfaces as its own error for cmd to explain rather than something to
// paper over.
var ErrBusy = errors.New("database is in use")

// maintenanceDB is the database we connect TO in order to talk about the
// others. Never the template and never a clone: a connection of our own is
// exactly the thing that makes cloning or dropping it fail.
const maintenanceDB = "postgres"

// psqlCmd is how treehouse reaches the cluster — argv, never a shell string.
// The default is the local client, which is what the overwhelming majority of
// dev setups have. Use points it somewhere else.
//
// Guarded, and not defensively: the TUI answers a keypress by starting a
// goroutine, so `enter` (drill in, which calls diagnose) followed by `r`
// (refresh, which calls fleet) puts two Use calls in flight at once. The lock
// lives here rather than in the caller because every caller would otherwise
// need one, and the next one to be written would forget.
var (
	psqlMu  sync.RWMutex
	psqlCmd = []string{"psql"}
)

// Use routes every query through a different psql, for the repo whose Postgres
// only exists inside compose ("docker compose exec -T db psql"). An empty
// command leaves the default alone — an absent config key must not disarm us.
//
// ponytail: split on whitespace, so an argument with a space in it can't be
// expressed. Every real prefix is bare words; reach for a shell-word splitter
// when someone's isn't.
func Use(command string) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return
	}
	psqlMu.Lock()
	defer psqlMu.Unlock()
	psqlCmd = fields
}

// Command is the psql argv in force, copied — callers build argv on top of it,
// and handing out the slice itself would put an append one cap away from
// writing into the shared one.
func Command() []string {
	psqlMu.RLock()
	defer psqlMu.RUnlock()
	return append([]string(nil), psqlCmd...)
}

// Databases lists every database in the cluster — check.DBInput's Existing,
// asked once. Its error doubles as the "is Postgres even reachable" answer.
func Databases() ([]string, error) {
	out, err := psql(maintenanceDB, "SELECT datname FROM pg_database")
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// Commented lists every database carrying a comment, with its size — the raw
// material for `th gc`. It does NOT filter to treehouse's own: deciding which
// comments are ours is a judgment, and judgments live in check, where they can
// be tested from struct literals instead of from a cluster.
//
// A database with no comment never appears, which is the whole safety property:
// gc cannot consider what it cannot see, so somebody's real database is out of
// scope before any rule runs.
func Commented() ([]check.Database, error) {
	out, err := psql(maintenanceDB,
		`SELECT datname || E'\t' || pg_size_pretty(pg_database_size(datname)) || E'\t'
		        || shobj_description(oid, 'pg_database')
		 FROM pg_database WHERE shobj_description(oid, 'pg_database') IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	var dbs []check.Database
	for _, line := range lines(out) {
		// A comment with a newline in it splits into rows that have no tabs and
		// are dropped here. That is the right failure: an unparseable row is a
		// row gc never sees, and treehouse's own comments are single-line.
		if f := strings.SplitN(line, "\t", 3); len(f) == 3 {
			dbs = append(dbs, check.Database{Name: f[0], Size: f[1], Comment: f[2]})
		}
	}
	return dbs, nil
}

// Create clones template into name. Near-instant in the sense that matters —
// Postgres copies the template's files rather than replaying anything — but it
// is still a copy: a 40 GB template makes a 40 GB clone.
func Create(name, template string) error {
	if err := checkIdent(name, template); err != nil {
		return err
	}
	_, err := psql(maintenanceDB, "CREATE DATABASE "+name+" TEMPLATE "+template)
	return busy(err)
}

// Drop removes a clone. IF EXISTS because gc lists first and drops second, and
// a database that vanished in between is gc's goal, not gc's problem.
//
// It refuses nothing on its own: the "is anybody connected" guard belongs to
// the caller, which has to REPORT the sessions rather than fail on them.
func Drop(name string) error {
	if err := checkIdent(name); err != nil {
		return err
	}
	_, err := psql(maintenanceDB, "DROP DATABASE IF EXISTS "+name)
	return busy(err)
}

// busy re-labels the one server error both Create and Drop have to explain
// rather than retry.
//
// ponytail: matched on the server's English message — there is no SQLSTATE on
// psql's stderr to key off. A localized server reports this as a plain failure
// instead, which is a worse message, not a wrong action.
func busy(err error) error {
	if err != nil && strings.Contains(err.Error(), "is being accessed by other users") {
		return fmt.Errorf("%w: %v", ErrBusy, err)
	}
	return err
}

// Sessions names what is connected to db — who is blocking a clone or a drop.
// It only reports: deciding to disconnect somebody's running app is not a thing
// a hydrate or a gc gets to do on its own.
func Sessions(db string) ([]string, error) {
	if err := checkIdent(db); err != nil {
		return nil, err
	}
	out, err := psql(maintenanceDB,
		`SELECT pid || '  ' || coalesce(nullif(application_name, ''), '?') || '  ' || coalesce(host(client_addr), 'local')
		 FROM pg_stat_activity WHERE datname = :'db'`, "--set=db="+db)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// Terminate disconnects everything using db. Only ever reached through
// --force-db, and only ever aimed at the template: it is a kill switch the
// human asked for by name, never an automatic recovery step.
func Terminate(db string) error {
	if err := checkIdent(db); err != nil {
		return err
	}
	_, err := psql(maintenanceDB,
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		 WHERE datname = :'db' AND pid <> pg_backend_pid()`, "--set=db="+db)
	return err
}

// Comment records where a clone came from — check.Provenance's string. It
// stores the ORIGINAL branch name because Slug is one-way: without it a later
// `th gc` can see a stray database but not say whose it was, and a collector
// that can't explain itself is not one anybody should run.
func Comment(db, text string) error {
	if err := checkIdent(db); err != nil {
		return err
	}
	// db is validated and interpolated (an identifier cannot be a bind
	// parameter); text goes through psql's :'c', which quotes it as a literal.
	_, err := psql(maintenanceDB, "COMMENT ON DATABASE "+db+" IS :'c'", "--set=c="+text)
	return err
}

// seedTable is the marker table treehouse keeps inside each worktree's OWN
// database: which named datasets have been loaded. It lives there rather than
// in a state file for the same reason the port registry does — it rides the
// TEMPLATE copy, so a clone inherits main's datasets, and it is dropped with the
// database, so there is nothing left to garbage-collect.
const seedTable = "treehouse_seed"

// MarkSeed records that a named dataset has been loaded into db.
func MarkSeed(db, name string) error {
	if err := checkIdent(db); err != nil {
		return err
	}
	_, err := psql(db, `CREATE TABLE IF NOT EXISTS `+seedTable+` (
	                      name text PRIMARY KEY,
	                      applied_at timestamptz NOT NULL DEFAULT now());
	                    INSERT INTO `+seedTable+` (name) VALUES (:'n')
	                      ON CONFLICT (name) DO UPDATE SET applied_at = now()`, "--set=n="+name)
	return err
}

// Seeds lists the datasets recorded in db, newest schema or not.
//
// A database with no marker table has simply had no seed run against it, which
// is the state every clone starts in — so it answers empty rather than failing.
// ponytail: "no such table" is recognised from the server's English message,
// the same bargain busy makes; the alternative is a second round trip on every
// doctor run just to ask whether a table exists.
func Seeds(db string) ([]string, error) {
	if err := checkIdent(db); err != nil {
		return nil, err
	}
	out, err := psql(db, "SELECT name FROM "+seedTable+" ORDER BY name")
	if err != nil {
		if strings.Contains(err.Error(), seedTable) && strings.Contains(err.Error(), "does not exist") {
			return nil, nil
		}
		return nil, err
	}
	return lines(out), nil
}

// checkIdent is the injection guard, and it stands in front of every identifier
// that reaches an argv or a SQL string. check.Slug's output is safe by
// construction, but a template name comes out of the user's .env and is
// arbitrary text — so the rule is refuse, never escape. The rule itself is
// check's, so the planner and this boundary cannot drift apart on what "safe"
// means.
func checkIdent(names ...string) error {
	for _, name := range names {
		if !check.Ident(name) {
			return fmt.Errorf("refusing %q as a database name: not a plain identifier", name)
		}
	}
	return nil
}

// lines splits psql's bare tuples, dropping the blank trailing one.
func lines(out string) []string {
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			rows = append(rows, line)
		}
	}
	return rows
}

// psql runs one statement against db and returns its bare rows. args carries
// the --set pairs the statement reads back as :'name'. Everything is passed as
// argv — never through a shell, which is the other half of the guard above.
//
// The statement goes in on STDIN rather than through -c, and that is not a
// style choice: psql hands a -c string straight to the server without
// substituting its own variables, so `IS :'c'` reached Postgres verbatim and
// came back a syntax error. Stdin is the path that interpolates — and quotes
// the value while it does it, which is the whole reason we use :'name' instead
// of building the SQL ourselves. It is also the path that lets CREATE DATABASE
// run at all: psql does not wrap stdin in a transaction block.
func psql(db, sql string, args ...string) (string, error) {
	psql := Command()
	full := append(psql[1:],
		"-d", db,
		"-At",         // bare tuples: no headers, no alignment to parse around
		"--no-psqlrc", // a user's .psqlrc must not shape what we read back
		"-v", "ON_ERROR_STOP=1",
	)
	cmd := exec.Command(psql[0], append(full, args...)...)
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("psql: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
