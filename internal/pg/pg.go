// Package pg creates the database clones check.PlanDB decides — the mutating
// "doer" half, mirroring internal/deps.
//
// It shells out to psql and createdb rather than opening a connection with
// database/sql. That is zero new dependencies, and libpq already understands
// PGHOST/PGPORT/PGUSER/PGPASSWORD, ~/.pgpass and every connstring form the
// user's own tooling works with — so treehouse reaches the same cluster the app
// does without reimplementing any of it. `createdb -T tmpl name` also quotes the
// identifiers itself and sidesteps the trap that CREATE DATABASE cannot run
// inside a transaction block.
package pg

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Blynkosaur/treehouse/internal/check"
)

// ErrTemplateBusy is createdb refusing to clone a database somebody is
// connected to. This is the COMMON case, not an edge one: a running dev server
// holds a connection. Retrying is useless — the app reconnects instantly — so
// it surfaces as its own error for cmd to explain rather than something to
// paper over.
var ErrTemplateBusy = errors.New("template database is in use")

// maintenanceDB is the database we connect TO in order to talk about the
// others. Never the template: a connection of our own is exactly the thing that
// makes cloning it fail.
const maintenanceDB = "postgres"

// Databases lists every database in the cluster — check.DBInput's Existing,
// asked once. Its error doubles as the "is Postgres even reachable" answer.
func Databases() ([]string, error) {
	out, err := psql("SELECT datname FROM pg_database")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// Create clones template into name. Near-instant: Postgres copies the template
// at the file level, so a clone costs what a copy-on-write node_modules does.
func Create(name, template string) error {
	if err := checkIdent(name, template); err != nil {
		return err
	}
	out, err := exec.Command("createdb", "-T", template, name).CombinedOutput()
	if err == nil {
		return nil
	}
	text := strings.TrimSpace(string(out))
	// ponytail: matched on the server's English message — createdb prints no
	// SQLSTATE. A localized server reports this as a plain failure instead, which
	// is a worse message, not a wrong action.
	if strings.Contains(text, "is being accessed by other users") {
		return fmt.Errorf("%w: %s", ErrTemplateBusy, text)
	}
	return fmt.Errorf("createdb %s: %v: %s", name, err, text)
}

// Sessions names what is connected to db — who is blocking a clone. It only
// reports: deciding to disconnect somebody's running app is not a thing a
// hydrate gets to do on its own.
func Sessions(db string) ([]string, error) {
	if err := checkIdent(db); err != nil {
		return nil, err
	}
	out, err := psql(
		`SELECT pid || '  ' || coalesce(nullif(application_name, ''), '?') || '  ' || coalesce(host(client_addr), 'local')
		 FROM pg_stat_activity WHERE datname = :'db'`, "--set=db="+db)
	if err != nil {
		return nil, err
	}
	var out2 []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out2 = append(out2, line)
		}
	}
	return out2, nil
}

// Terminate disconnects everything using db. Only ever reached through
// --force-db, and only ever aimed at the template: it is a kill switch the
// human asked for by name, never an automatic recovery step.
func Terminate(db string) error {
	if err := checkIdent(db); err != nil {
		return err
	}
	_, err := psql(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		 WHERE datname = :'db' AND pid <> pg_backend_pid()`, "--set=db="+db)
	return err
}

// Comment records where a clone came from, as "treehouse:<main worktree>:<branch>".
// It stores the ORIGINAL branch name because Slug is one-way: without it a later
// `th gc` can see a stray database but not say whose it was, and a collector
// that can't explain itself is not one anybody should run.
func Comment(db, text string) error {
	if err := checkIdent(db); err != nil {
		return err
	}
	// db is validated and interpolated (an identifier cannot be a bind
	// parameter); text goes through psql's :'c', which quotes it as a literal.
	_, err := psql("COMMENT ON DATABASE "+db+" IS :'c'", "--set=c="+text)
	return err
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

// psql runs one statement and returns its bare rows. args carries the --set
// pairs the statement reads back as :'name'. Everything is passed as argv —
// never through a shell, which is the other half of the guard above.
//
// The statement goes in on STDIN rather than through -c, and that is not a
// style choice: psql hands a -c string straight to the server without
// substituting its own variables, so `IS :'c'` reached Postgres verbatim and
// came back a syntax error. Stdin is the path that interpolates — and quotes
// the value while it does it, which is the whole reason we use :'name' instead
// of building the SQL ourselves.
func psql(sql string, args ...string) (string, error) {
	full := append([]string{
		"-d", maintenanceDB,
		"-At",         // bare tuples: no headers, no alignment to parse around
		"--no-psqlrc", // a user's .psqlrc must not shape what we read back
		"-v", "ON_ERROR_STOP=1",
	}, args...)
	cmd := exec.Command("psql", full...)
	cmd.Stdin = strings.NewReader(sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("psql: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
