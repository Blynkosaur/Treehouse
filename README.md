```text
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢠⣤⠛⠀⣤⣤⡄
⠀⠀⠀⠀⣤⣤⣤⣤⣼⡟⠛⠛⠛⠀⠛⠛⣿⣤⡟⠛⣧⣤⡄
⠀⣤⣿⠛⠛⠃⣤⣤⡄⠀⠛⠃⣤⠀⠀⠛⢻⣿⡟⠛⣧⣤⣿⠃
⢸⣧⠀⠀⢠⣼⡟⠀⠀⢸⣧⣤⠘⣿⣿⣿⣿⡟⠃⠀⢠⣼⣧
⠀⠛⣿⠛⢻⣿⣤⣼⠀⣼⣿⡟⠃⠘⠛⢻⣿⣿⣧⣤⠛⠛⠛⣿⣧⣤
⠀⠀⠀⠛⠛⠛⠛⣧⣤⠘⣿⣿⡇⣤⣤⠘⢻⠛⠛⠃⣤⣿⣿⠛⠛⣿⣼⡄
⣼⡟⣧⣼⡟⢠⣼⣿⣿⠛⣿⣧⣿⡟⢣⣿⣿⢻⣤⣼⡟⣿⣿⠀⣿⣿⡇⡄
⠀⠛⠛⣿⣤⣿⡟⢻⣿⣼⣧⣿⡇⠀⢠⣼⣿⣿⣿⣤⣼⡟⠛⠛⠃
⠀⠀⠀⠀⠀⠘⢻⠛⣧⣤⣤⣼⣿⣿⣿⣿⣿⣿⣿⣧⣿⣿⣼⡄
⠀⠀⠀⠀⠀⠀⢸⠀⠀⠀⠛⡄⠘⣿⠀⢻⡇⣿⣿⡇⣧⠛
⠀⠀⠀⠀⠀⠀⢸⠀⠀⠀⠀⠘⡇⠛⣿⡟⡟⠛⠛⠛
⠀⠀⠀⠀⠀⠀⢸⠀⠀⠀⠀⠀⣼⠀⣿⣿
⠀⠀⠀⠀⠀⠀⠛⠃⠀⠀⠀⠀⡜⡇⣿⣿⡄

╺┳╸┏━┓┏━╸┏━╸╻ ╻┏━┓╻ ╻┏━┓┏━╸
 ┃ ┣┳┛┣╸ ┣╸ ┣━┫┃ ┃┃ ┃┗━┓┣╸
 ╹ ╹┗╸┗━╸┗━╸╹ ╹┗━┛┗━┛┗━┛┗━╸
A better Git Worktree runner for your agents
```

A worktree manager where new worktrees are **born ready** — env filled, database
of their own, deps present, ports and compose project namespaced, secrets kept
out of the files an agent reads — instead of born broken.

## The problem

Git worktrees are the right way to run three or four branches at once, and they
are also how you hand a branch to a coding agent. What nobody tells you is that
`git worktree add` gives you a directory, and a directory is not a working
environment.

What is actually missing, every time:

- **`.env` is gitignored**, so a new worktree has none. The app will not boot,
  and the fix is copying files by hand from wherever the good copy lives.
- **One shared database.** Three worktrees, one `app_dev`, and the first
  migration anybody runs breaks the other two.
- **Port collisions.** Everything wants `:3000`.
- **Compose project collisions.** `docker compose up` in the second worktree
  adopts and restarts the first one's containers.
- **`node_modules` and `.venv`, per worktree.** Five copies of a 2 GB tree is
  10 GB, and twenty minutes of installs.
- **Nothing tells you which one is broken.** Least of all an agent, which will
  cheerfully spend twenty minutes debugging its own code because Redis is down.
- **Secrets in cleartext**, in files an agent reads to answer questions about
  something else entirely, and from there into a transcript for good.

treehouse fixes each of those at the moment a worktree is created, and reports
honestly on the ones it cannot fix.

## What it does

| | |
| --- | --- |
| **Env fill** | Copies each service's `.env` from the main checkout without overwriting anything you set. Reports missing and empty keys per service, with a fix line. |
| **A database per worktree** | Clones the database your `.env` names via Postgres `TEMPLATE`, then repoints `DATABASE_URL` and `POSTGRES_DB` at the clone. Dropped when the worktree is. |
| **Port and compose isolation** | One deterministic offset applied to every `PORT`/`*_PORT` key, a private `COMPOSE_PROJECT_NAME`, and a private Redis logical db. |
| **Instant dependencies** | `node_modules` cloned copy-on-write (`cp -c`) — instant and near-zero disk. `.venv` is rebuilt rather than copied, because its paths are absolute. |
| **Secrets** | The value moves to the keychain, `.env` keeps a `th:` reference, and `th run -- <cmd>` gives the command the real value without ever showing it to whoever ran it. |
| **Triage** | Correlates a failed command's output with the worktree's health and says whether the failure was the environment or the code. |
| **A fleet view** | One table, or a live dashboard, of every worktree and what is wrong with it. |
| **Cleanup** | `th rm` takes the worktree, the branch, the database and the compose project together. `th gc` finds what earlier mistakes left behind. |

Everything works with **no configuration at all**. `treehouse.toml` is a
sharpener, not a gatekeeper — see [Configuration](#configuration).

## Install

```sh
go install github.com/Blynkosaur/treehouse@latest
```

The binary is `treehouse`; every example here uses the shorter alias.

```sh
alias th=treehouse    # in .bashrc / .zshrc / config.fish
```

macOS and Linux, with two macOS-only features that behave differently from each
other. Copy-on-write dependency cloning needs APFS and **degrades** — it falls
back to a plain copy. The secret vault needs the macOS keychain and **refuses**:
elsewhere, `th run` will not start a command whose `.env` holds a `th:`
reference, and `th doctor` reports a `vault: skip` row saying why. A repo that
vaults its secrets is a repo the rest of the team cannot run on Linux.

## Quickstart

```sh
cd ~/code/my-app
th new feat/login          # cut the worktree, fill it, clone its database, report
tcd feat/login             # jump to it (see Jumping between worktrees)
th doctor                  # what is wrong right now
th ls                      # the whole fleet, one row each
```

## Commands

| Command | What it does |
| --- | --- |
| `th` | On a terminal, opens the live dashboard. Anywhere else — a pipe, CI, an agent capturing stdout — prints this help instead. |
| `th tui` | The dashboard, explicitly. |
| `th new <branch>` | Cuts a worktree beside the main checkout, runs the full hydrate pipeline, prints a doctor report and hands it to your `[open]` command. `--from <ref>`, `--path <dir>`, `--skip-deps`, `--open`, `--no-open`. |
| `th hydrate` | Fills this worktree's `.env` files from the main checkout, provisions heavy dep dirs, clones this branch's database, then writes derived values. `--dry`, `--skip-deps`, `--force-db`. |
| `th doctor` | Env drift per service, whether this worktree has its own database and is pointed at it, whether declared services are listening, cleartext secrets in the root `.env`, and how far behind main it is. `--db` adds migration and seed state. `--ls`, `--json`, `--quiet`. |
| `th ls` | One table: every worktree × branch × env × db × behind-main × dirty. Exits 2 on a FAIL fleet, like `doctor`. `--json`. |
| `th why` | One line: what changed since everything was last green. Always exits 0. `--json`, `--db`. |
| `th run -- <cmd>` | Runs the command with this worktree's env, resolving vaulted secrets into the child and scrubbing them out of the output. `--no-redact`. |
| `th vault add\|ls\|rm` | Moves a secret out of `.env` and into the keychain, leaving a `th:` reference behind. |
| `th triage -- <cmd>` | Runs the command, streams it, then says whether the failure was the environment or the code. `--stdin`, `--hook`. |
| `th hook session` | Claude Code `SessionStart`: hands the agent this worktree's env and database state. |
| `th rm <branch>` | Removes a worktree, its branch, its database clone and its compose project. Refuses dirty or unpushed work without `--force`, and always refuses the worktree you are standing in. |
| `th gc` | Lists the database clones whose worktrees are gone and drops them after confirmation. `-y`, `--json`. |
| `th seed <name>` | Runs a named `[[seed]]` dataset against this worktree's own database. |
| `th make` | Generates `.env.example` from each service's `.env`, values blanked. |
| `th path <branch>` | Prints that branch's worktree path and nothing else — the resolver behind the [`tcd` shell function](#jumping-between-worktrees). |
| `th init` | Writes a commented `treehouse.toml` scaffold. |

Hydrate runs three phases in order — **fill canonical, provision deps, derive** —
so a derived value never points at a broken env. Dependency failures are reported
red but do not abort; env and git failures do.

## Secrets

An agent does not need to know what your Stripe key is. It needs `npm start` to
work. Those are different requirements, and treating them as the same one is how
a secret ends up in a transcript.

`th vault add` moves the value into the macOS keychain and leaves a reference:

```sh
$ th vault add STRIPE_SECRET
✓ STRIPE_SECRET: stored (from .env), .env now reads th:STRIPE_SECRET

$ cat .env
PORT=3000
STRIPE_SECRET=th:STRIPE_SECRET

$ th run -- npm start        # the child gets the real value
```

The key names stay readable on purpose — an agent still needs to know that
`STRIPE_SECRET` exists to reason about what is missing. What it cannot get is
the value.

**Output is scrubbed too**, because taking the value out of `.env` only stops
the agent *reading* it. A stack trace that prints a connection string puts it in
the context window just as permanently:

```
$ th run -- rails c
ActiveRecord::ConnectionError: could not connect to
  postgres://app:$POSTGRES_PASSWORD@localhost/app_wt_feat
```

`--no-redact` turns that off when the substitution mangles output you need.

`th doctor` reports both halves: a key whose *name* suggests a secret while it
still holds a value is a **warning**, a key named in `[secrets] keys` is a
**failure**, and a reference pointing at a secret that is not there is a
**failure** — that last state is invisible in the file, since a dangling
reference looks exactly like a working one.

**The vault is the worktree root's `.env` only**, because that is the file
`th run` injects. A monorepo's `services/api/.env` is checked for drift like any
other, but its secrets are neither vaulted nor reported — keep the ones worth
hiding in the root file.

```toml
# treehouse.toml, committed
[secrets]
keys = ["STRIPE_SECRET", "DEPLOY_HOOK"]   # these must never sit in .env
```

Vaulting survives `th new`: `hydrate` copies `.env` values verbatim, so a new
worktree is born holding the reference and pointed at the same secret.

### What this does and does not protect against

**It covers accidental exposure**, which is the failure that actually happens
every day: a `cat .env`, a `grep -r`, a stack trace, a file read into a context
window to answer a question about something else.

**It is not a wall against a hostile process running as you.** `security(1)` is
the same binary for every caller, so anything running under your account can ask
the keychain the same question `th` does. Enforcing more than this needs a
different security boundary altogether — a container with locked-down egress,
which is what [Infisical's Agent Vault](https://github.com/Infisical/agent-vault)
builds and what treehouse deliberately does not.

The two are complementary rather than competing. Agent Vault intercepts the
agent's own outbound HTTPS and injects credentials at the socket; treehouse
injects a worktree's environment at `exec`, which is where `DATABASE_URL` and
`REDIS_URL` live and where no HTTP proxy can reach. Run `th run -- <cmd>` inside
`agent-vault run -- claude` if you want both.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | healthy, warnings only, or checks that could not be run |
| 1 | treehouse itself failed (usage, git, IO) |
| 2 | a FAIL finding — see below |

The FAIL tier: a curated required key missing or empty; a `.env` targeting the
shared database while its own clone exists; a database name Postgres cannot
hold; a declared `[[service]]` with nothing listening; a curated secret sitting
in cleartext; a vault reference that resolves to nothing; a `treehouse.toml`
that will not parse.

`th doctor` and `th ls` both answer this way, so an agent can gate on the fleet
without parsing a table. Three commands are documented exceptions:
`th triage -- <cmd>` and `th run -- <cmd>` pass the **wrapped command's** code
through verbatim, because a wrapper that changes it is a wrapper you cannot put
in front of anything; `th triage --hook` always exits 0, because it runs after
every Bash call; and `th why` always exits 0, because it answers what *changed*,
not whether the worktree is healthy.

**`skip` is not a synonym for ok, and it never satisfies a green.** It means the
question could not be answered here: Postgres was unreachable, no
`[migrations] status` command is configured, this worktree is detached. A report
whose worst word is `skip` says `skip`, not `ok`, because "nobody asked" and
"verified fine" are the two answers an agent must never confuse. `skip` still
exits 0 — nothing is *known* to be broken.

`--json` emits an object, never a bare array:

```json
{"schema": 2, "root": "…", "status": "ok|warn|fail|skip", "findings": [], "checks": []}
```

`findings` is env drift per service; `checks` is the config, database, migration,
seed, service and secrets rows. `th ls --json` is the same envelope with
`worktrees` in place of `findings`, and every row carries its own `status`
computed the same way — the two commands cannot call one worktree ok and fail.

## A database per worktree

`th new` / `th hydrate` clone the database main's `.env` points at into
`<app>_wt_<slug>`, then rewrite this worktree's `DATABASE_URL` and `POSTGRES_DB`
to name the clone. One branch's migration can never reach another's. Zero
config: the template, the connection and the clone name all come from main's own
`.env`, and a repo that names no database gets no clone and is never asked about
one.

**"Near-instant" means a filesystem copy, not a snapshot.** Postgres `TEMPLATE`
copies files rather than replaying anything, so it is fast — but it is a full
copy on disk. **A 40 GB dev database makes a 40 GB clone, per worktree.** Four
worktrees is 160 GB. If that is your situation, this feature is not free and you
should know before you run it.

Postgres will not clone a database somebody is connected to, and a running dev
server is a connection. That is the common case, not an edge one, so hydrate
names the sessions and stops rather than retrying. `th hydrate --force-db`
disconnects them, and is the only thing in treehouse that ever does.

Clones carry a provenance comment (`treehouse:<main path>:<branch>`) and
**ownership is by that comment, never by a name prefix** — a prefix can match
somebody's real database. `th rm` drops the clone it just orphaned; `th gc`
lists the rest and asks. Neither will drop a database with open connections.

```
$ th gc
clones whose worktree is gone:
  app_wt_feat_login_d668f0    112 MB     was feat/login
drop 1 database(s)? [y/N]
```

**Identifiers are quoted, not refused.** Every database name treehouse puts in
SQL is wrapped in double quotes with any embedded `"` doubled — the well-defined
Postgres rule — so `app-db`, `APPDB` and a name with a space all work. Names
reach `psql` as argv or on stdin, never through a shell.

A name is still refused when quoting cannot rescue it: empty, longer than 63
bytes (Postgres truncates silently, and two branches truncated alike would share
one database), or carrying a NUL, a newline or a carriage return. Those are a
**FAIL-level `db` check with a fix line**, not a quiet skip — a repo that wants
clones and will never get one has to hear about it.

**`th ls` reports the same database state `th doctor` does.** The column answers
`main`, `ok`, `missing`, `shared`, `adrift`, `unusable` or `skip` — not just
clone-exists. `shared` is a worktree whose clone exists while its `.env` still
names the template; it is red here and a FAIL in the fleet verdict, exactly as in
doctor's report. Nothing about that state looks wrong from inside the app, and it
is the one state in the table that costs data.

## Per-worktree isolation

Each worktree gets `COMPOSE_PROJECT_NAME=<app>_<slug>` in the `.env` of every
directory that holds a compose file, a deterministic port offset applied to every
`PORT`/`*_PORT` key the main checkout declares, and its own Redis logical db
written into every `REDIS_URL`/`REDIS_DB`.

**The registry is the sibling `.env` files themselves**, so there is no state
file to garbage-collect. The offset is derived from the branch name, so the same
branch normally lands on the same ports — but that is **not** a guarantee over
time. The offset has to dodge ports already claimed by main and by every sibling,
so adding a worktree that wants yours will move you on the next `hydrate`.
Deterministic given the fleet; not stable across changes to it.

**Caveat:** this shifts the ports your app processes bind. A compose file's own
`ports: "3000:3000"` host mapping is **not** rewritten — parameterize it
(`"${PORT}:3000"`) if you run compose in more than one worktree at a time.

`th rm` tears the compose project down (`docker compose -p <project> down`)
before removing the worktree, using the project name from that worktree's own
`.env` — known, not guessed. It refuses any name the main checkout also claims: a
half-hydrated worktree still carries main's project name, and tearing *that* down
would stop the containers you are working in, from a command you ran about a
different branch. It is `down`, never `down -v`, because volumes are data. No
docker, no daemon, or a project never started: all silent, and none of them can
fail a `th rm`.

Redis works the same way with one difference that matters: **Redis ships 16
logical dbs (0–15) and that is the whole supply.** Main sits on one, so the fleet
ceiling is roughly 15 worktrees, which a busy laptop can reach. Running out is a
skip line in `hydrate`, never a failure — it is a cache. Past 16, give each
worktree its own Redis instance (on a port treehouse already derives) or prefix
your keys. `REDIS_URL` is parsed as a URL, so credentials, a `rediss://` scheme
and `?query` parameters all survive; only the db number moves. One db covers the
**whole** worktree: services main splits across separate dbs are merged here,
because 16 cannot afford a db per service per worktree.

## Dead services, and a stale base

`th doctor` dials what this repo says should be listening. **Discovery is the
`PORT` keys your `.env` files already carry** — the same keys hydrate shifts. A
key named `PORT` in `svc_a/.env` *is* the statement that svc_a should have a
listener, so nothing here parses a docker-compose file: a second discovery path
would be a second source of truth, disagreeing with the one hydrate acts on.

```
$ th doctor
✓ api: .env has all 7 expected keys
✓ service: api/PORT is listening on 127.0.0.1:4002
! service: worker/PORT — nothing is listening on 127.0.0.1:5002
    fix: docker compose up -d
✗ service: redis — nothing is listening on 127.0.0.1:6379
    fix: docker compose up -d redis
! base: 12 commit(s) behind main
    fix: git fetch && git rebase origin/main
```

Rows are named `<dir>/<KEY>`, or just `<KEY>` at the repo root. Dials are
concurrent with a **250 ms** timeout — a doctor that takes five seconds because
three services are down is a doctor nobody runs — and go to `127.0.0.1`, never
`localhost`, so the answer does not depend on your resolver.

**Inferred services warn; declared ones fail.** A `PORT` key is a guess about
intent, so it is a WARN and exits 0. A `[[service]]` entry is a human saying this
must be up, so it is a FAIL and exits 2.

**A repo that declares no port gets no service rows at all** — not a green one.
Nothing to check is a different sentence from checked and found nothing wrong,
the same rule that gives a repo with no database no `db` row. A `[[service]]`
with no `addr` is `skip`: a typo in a TOML file is not a dead service, and it is
not a healthy one either.

**Never from `th ls`.** The fleet table exists to be glanced at, and a network
fan-out per row is not a glance — the same bargain migrations make.

The `base` row is the stale-base check, over the same count `th ls` shows in its
BEHIND column. **WARN, never FAIL:** a stale branch is a smell, the code still
runs, and an exit 2 on a worktree that works is how a checker teaches people to
stop reading it. The main checkout gets no row, because `HEAD..main` is always
zero there and "up to date" printed over a main that is ten behind origin would
be a green nobody measured. The count is against your **local** main and doctor
does not fetch, so a stale local main under-reports; the fix line fetches.

## Migrations and seeds

Both are opt-in (`th doctor --db`) and never run from `th ls` — a status command
is seconds of your tooling per worktree.

The exit code is the only signal alembic, Django and Prisma genuinely share: all
three exit non-zero when migrations are pending. treehouse pairs it with
`git diff <main>...HEAD -- <migrations dir>` — pending with new files on this
branch is your own work waiting to run, pending with none means main moved ahead.
It does **not** claim "diverged"; see A3 in
[docs/user-stories.md](docs/user-stories.md).

`th seed ramp` runs your command and records the dataset in a marker table inside
that worktree's own database — so it rides the template copy (a new clone
inherits main's datasets), it is dropped with the database, and there is no state
file to keep in sync.

## Triage: environment or code?

An agent that reads `connection refused` and starts debugging its own code burns
twenty minutes on nothing. `th triage` correlates the failure output with this
worktree's doctor state and says which it was.

```
$ th triage -- pytest -q
…pytest's own output, live, unchanged…
th triage: environment (matched missing-env)
  KeyError: 'DATABASE_URL'
  a required environment variable is unset
  doctor agrees: env drift in /repo/api: missing DATABASE_URL
  fix: th hydrate
```

**The correlation is the point, not the regex.** A pattern that matches proves
the output *looks* environmental; only doctor can say whether the environment is
actually broken.

| signature | doctor for that area | verdict |
| --- | --- | --- |
| matched | red | `environment` — evidence and fixes from both |
| matched | green | `unknown`, and the evidence names the contradiction |
| none | red somewhere | `unknown`, with the red row offered as possibly related |
| none | green | `code` |

Row two is why this is not `grep`. A confident wrong "it's your environment"
sends an agent to reinstall Postgres over a typo in its own code, which is
strictly worse than saying nothing.

### The three modes, and their exit codes

| Mode | Output | Exit code |
| --- | --- | --- |
| `th triage -- <cmd>` | the command's own streams, untouched; verdict on **stderr**, ≤10 lines | **the wrapped command's, verbatim** |
| `th triage --stdin` | verdict JSON on stdout | 0, or **2** for `environment` |
| `th triage --hook` | `hookSpecificOutput` JSON on stdout | **always 0** |

The wrapper is transparent on purpose — `time`, `env` and `nice` all pass the
code through, and `th triage -- pytest` has to keep failing a Makefile. That is
why the verdict goes to stderr: stdout belongs to the wrapped command. (127 if
the command could not be started at all; 128+n if it was killed by a signal.)
`th run` shares this contract exactly, and adds the verdict after a failure of
its own.

**`--hook` always exits 0**, and its verdict rides in the JSON payload alone.
PostToolUse's protocol is stdout-JSON plus exit 0; exit 2 is the legacy
*blocking* shape and may read as a blocked tool call. Blocking an agent's tool
call because its environment looks broken is far worse than failing to hint at
it. Nothing else can make the hook exit non-zero either — an unreadable payload,
a broken `treehouse.toml` or a cwd that no longer exists all print one line to
stderr (visible in `claude --debug`) and exit 0.

## What changed since it was green?

`th doctor` tells you what is true now. `th why` tells you what moved.

```
$ th why
env went from ok to warn since 14:02: REDIS_URL missing

$ th why
db stopped being checked after you switched to feat/login: postgres is not reachable
```

A check that went from `ok` to **`skip`** gets its own sentence, because a check
that stopped being *asked* has not stayed fine — that is usually the whole story.

`th doctor` records what each check said; `th why` diffs the live report against
it. **The journal lives in this worktree's own `.git/` directory** — for a linked
worktree, `<main>/.git/worktrees/<branch>/treehouse-state.json`. It is never
committed, `git status` never sees it, and `git worktree remove` (so, `th rm`)
deletes it. Nothing to clean up, the same deal the port assignments and the seed
marker already make.

**It is disposable, and treehouse treats it that way.** Delete it, truncate it,
hand-edit it, keep one from an older version — every case answers `no baseline
yet — run th doctor first` and exits 0, and the next `th doctor` writes a fresh
one. It records only *when* each check was last ok and on which branch; what is
wrong right now always comes from the live run, so the file cannot go stale or
contradict a fix you made by hand.

## The dashboard

`th` with no arguments opens a live board of the whole fleet: one row per
worktree, the row you are standing in marked, and a spinner in every cell no
checker has answered for yet. Path and branch come straight from git, so they are
on screen immediately; env, database, behind-main and dirty fill in independently
as each worktree's checks land.

| Key | Does |
| --- | --- |
| `↑` `↓` / `k` `j` | select a worktree |
| `enter` | drill into that worktree's doctor report |
| `esc` | back to the grid |
| `h` | run `th hydrate` on the selected worktree, then re-check that row in place |
| `r` | re-check the whole fleet |
| `q` / `ctrl-c` | quit |

**The board is a renderer, not a second opinion.** Every cell is a field of
`check.Status` — the same rows `th ls` prints — and the drill-in is literally
`th doctor`'s report.

**It never hijacks a pipe.** The dashboard needs a terminal, so bare `th` checks
for one first; without it you get the help text. Hooks, CI and agents are
unaffected, and `th ls --json` / `th doctor --json` remain the outputs to script
against.

## Jumping between worktrees

There is no `th cd`, and there will not be one: a process cannot change its
parent shell's directory. `th path <branch>` prints the path — one line on
stdout, nothing else, non-zero and silent when the branch has no worktree, so a
`cd` to nothing can never land you in `$HOME`. Wrap it:

```sh
# bash / zsh — in .bashrc or .zshrc
tcd() { cd "$(th path "$1")"; }
```

```fish
# fish — in config.fish
function tcd; cd (th path $argv[1]); end
```

`th path` also completes branch names (`th completion zsh`/`bash`/`fish`).

## Configuration

**Zero-config is the default.** With no `treehouse.toml`, doctor works end to
end: required keys inferred from `.env.example`, services inferred from the
`PORT` keys your `.env` files declare, secrets inferred from key names, git
staleness needing nothing at all. `treehouse.toml` **sharpens** — it upgrades
inferred warnings into curated failures, and carries the handful of things
nothing can infer.

`th init` writes this as a commented scaffold.

```toml
# treehouse.toml — committed. Carries judgment, never secrets.

[env]
required = ["DATABASE_URL"]           # inferred keys warn; these fail

[secrets]
keys = ["STRIPE_SECRET"]              # must never sit in .env in cleartext

[database]
psql = "docker compose exec -T db psql"   # only when Postgres is not a local psql

[migrations]
status = "alembic current"            # exit code means "migrations pending"
dir    = "db/migrate"                 # only when the glob guesses wrong

[[seed]]
name = "ramp"
command = "python manage.py loaddata ramp"

[[service]]                           # sharpens an inferred WARN into a FAIL
name = "redis"
addr = "127.0.0.1:6379"
fix  = "docker compose up -d redis"

[[deps]]                              # heavy dirs to clone copy-on-write
name = "node_modules"

[[signature]]                         # extend triage's failure vocabulary
name  = "kafka-down"
match = "NoBrokersAvailable"          # regex, matched line by line
cause = "kafka is not reachable"
fix   = "docker compose up -d kafka"
needs = "service"                     # env | db | migration | service

[open]
command = "cursor ."                  # what `th new` hands a green worktree to
```

`[[deps]]`, `[[seed]]`, `[[signature]]` and `[[service]]` all merge **by name**:
reuse a built-in's name to replace it, use a new one to add.

There is deliberately **no `[database] template`** key — which database to clone
is main's own `.env`, and a second place to say it is a second place for it to be
wrong.

**When the file will not parse**, every command degrades to the built-in defaults
and keeps working, because an optional file must not be worse broken than absent.
It is also a **FAIL-level `config` check** naming the file and the error, in
`doctor`, in `ls` and in both JSON outputs — silently not applying it is how a
`required = [...]` list stops being enforced without anybody noticing. `th seed`
is the one exception: it cannot work without the file, so it errors. Neither hook
ever fails over it.

### Handing over a finished worktree

`[open] command` runs with the worktree as its working directory, after the
doctor report. Unset means nothing happens and nothing is said. `--open` and
`--no-open` force either way.

**It does not fire when doctor reports a FAIL**, and that guard is the whole
point of *born ready*: hand somebody a worktree whose `.env` still targets the
shared database and they will start working in it before they read the report
that scrolled past above their editor. Warnings do not block — inferred drift and
a dead port are the normal state of a repo you have not started yet.

It runs in the **foreground and waits**, which is right for both shapes of
command: `cursor .` forks its editor and returns immediately, while `claude`
needs the terminal — backgrounding it would leave it fighting your returning
shell prompt for stdin. A command that fails is a report line, never an exit
code.

## Claude Code hooks (verified 2026-07-27)

Two hooks. **One works, one is limited by something Claude Code does not
expose**, and both facts come from a watched `claude --debug` run, not from
reading source.

**`SessionStart` works.** `th hook session` fires on `startup`, its JSON is
parsed and validated, and the context is injected — an agent begins the session
knowing this worktree's env and database state, and knowing to use `th run` when
secrets are vaulted.

**`PostToolUse` never fires when a Bash call errors.** Verified with a control
pair in one session: a call ending `outcome=ok` fires the hook and the verdict is
injected; a call ending `outcome=error` fires nothing at all. There is no hook
event for a failed tool call, so the failure hook cannot see the failures it was
written for.

What still works is not nothing: a command that *succeeds* while printing a
recognised signature — a test runner reporting connection errors and exiting 0, a
script logging `relation does not exist` and carrying on — is triaged normally.

**For the headline case, wrap the command instead.** `th triage -- <cmd>` needs no
hook, sees everything, and passes the exit code through untouched. Tell your
agent so in `CLAUDE.md`:

> When a command fails and you suspect the environment, re-run it as
> `th triage -- <cmd>` and read the verdict before changing any code.
> Run commands that need secrets as `th run -- <cmd>`.

Also worth knowing: a Bash `tool_response` carries **no exit code** — only
`stdout`, `stderr` and `interrupted`. There is no "did it fail" to branch on,
which is why the signature map *is* the failure detector. The hook never re-runs
the command; the output is already in the payload, and re-running would re-run
your `git push`.

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup",
        "hooks": [{ "type": "command", "command": "th hook session" }]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "th triage --hook" }]
      }
    ]
  }
}
```

## Non-goals

- Multi-machine sync
- Full secrets management — `th vault` covers one laptop and one repo's `.env`;
  team-wide rotation, sharing and audit are [varlock](https://varlock.dev) /
  [Doppler](https://doppler.com) / [Infisical](https://infisical.com)
- Port proxying and subdomain routing
- IDE and editor state
- Moving uncommitted work between worktrees
- Build-cache sharing
- Being a migration or seeding framework — treehouse wraps *your* commands

## Design notes

Every decision above, with the reasoning and the things that were cut, lives in
[docs/user-stories.md](docs/user-stories.md). Function-level acceptance criteria,
each pinned to the test that proves it, are in
[docs/dotenv-acceptance.md](docs/dotenv-acceptance.md).
