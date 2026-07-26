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

A worktree manager where new worktrees are _born ready_ — env filled, deps
present, ports and compose project of their own — instead of born broken.

## Commands

| Command | What it does |
| --- | --- |
| `th` | On a terminal, opens the live dashboard. Anywhere else — a pipe, CI, an agent capturing stdout — prints this help instead. |
| `th tui` | The dashboard, explicitly. |
| `th new <branch>` | Cuts a worktree beside the main checkout and runs the full hydrate pipeline, then prints a doctor report. `--from <ref>`, `--path <dir>`, `--skip-deps`. |
| `th hydrate` | Fills this worktree's `.env` files from the main checkout, provisions heavy dep dirs, clones this branch's database, then writes derived values. `--dry`, `--skip-deps`, `--force-db`. |
| `th doctor` | Reports env drift per service, plus whether this worktree has its own database and is pointed at it. `--db` adds migration and seed state. `--ls` table, `--json`, `--quiet`. |
| `th ls` | One table: every worktree × branch × env × db × behind-main × dirty. `--json`. |
| `th rm <branch>` | Removes a worktree, its branch, and its database clone. Refuses dirty or unpushed work without `--force`, and always refuses the worktree you're standing in. |
| `th gc` | Lists the database clones whose worktrees are gone and drops them after confirmation. `-y`, `--json`. |
| `th seed <name>` | Runs a named `[[seed]]` dataset against this worktree's own database. |
| `th make` | Generates `.env.example` from each service's `.env`, values blanked. |
| `th init` | Writes a commented `treehouse.toml` scaffold. |

Hydrate runs three phases in order — **fill canonical → provision deps → derive** —
so a derived value never points at a broken env. Dependency failures are reported
red but don't abort; env and git failures do.

## The dashboard

`th` with no arguments opens a live board of the whole fleet: one row per
worktree, the row you're standing in marked, and a spinner in every cell no
checker has answered for yet. Path and branch come straight from git, so they
are on screen immediately; env, database, behind-main and dirty fill in
independently as each worktree's checks land, rather than all at once when the
slowest one finishes.

| Key | Does |
| --- | --- |
| `↑` `↓` / `k` `j` | select a worktree |
| `enter` | drill into that worktree's doctor report |
| `esc` | back to the grid |
| `h` | run `th hydrate` on the selected worktree, then re-check that row in place |
| `r` | re-check the whole fleet |
| `q` / `ctrl-c` | quit |

`h` runs hydrate as a real subprocess: its output scrolls past normally, then
the board comes back and the row it repaired flips green on its own.

**The board is a renderer, not a second opinion.** Every cell is a field of
`check.Status` — the same rows `th ls` prints — and the drill-in is literally
`th doctor`'s report. Nothing in the TUI decides anything the CLI wouldn't.

**It never hijacks a pipe.** The dashboard needs a terminal, so bare `th`
checks for one first; without it you get the help text, exactly as before.
Hooks, CI and agents are unaffected, and `th ls --json` / `th doctor --json`
remain the outputs to script against.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | healthy, or warnings only |
| 1 | treehouse itself failed (usage, git, IO) |
| 2 | a curated required key is missing or empty, or this worktree's `.env` targets the shared database while its own clone exists |

Requirements inferred from `.env.example` are warnings. Failures come only from a
human-curated list:

```toml
# treehouse.toml, committed
[env]
required = ["DATABASE_URL"]
```

`--json` emits an object, never a bare array:
`{"schema": 2, "root": …, "status": ok|warn|fail, "findings": […], "checks": […]}`.
`findings` is env drift per service; `checks` is the database, migration and seed
rows. Key off `status` — it folds both.

## A database per worktree

`th new` / `th hydrate` clone the database main's `.env` points at into
`<app>_wt_<slug>`, then rewrite this worktree's `DATABASE_URL` and `POSTGRES_DB`
to name the clone. One branch's migration can never reach another's. Zero
config: the template, the connection and the clone name all come from main's own
`.env`, and a repo that names no database gets no clone and is never even asked
about one.

**"Near-instant" means a filesystem copy, not a snapshot.** Postgres `TEMPLATE`
copies the template's files rather than replaying anything, so it is fast — but
it is a full copy on disk. **A 40 GB dev database makes a 40 GB clone, per
worktree.** Four worktrees is 160 GB. If that is your situation, this feature is
not free and you should know before you run it.

Postgres will not clone a database somebody is connected to, and a running dev
server is a connection. That is the common case, not an edge one, so hydrate
names the sessions and stops rather than retrying; `th hydrate --force-db`
disconnects them, and is the only thing in treehouse that ever does.

Clones carry a provenance comment (`treehouse:<main path>:<branch>`) and
**ownership is by that comment, never by a name prefix** — a prefix can match
somebody's real database. `th rm` drops the clone it just orphaned; `th gc`
lists the rest and asks. Neither will drop a database with open connections.

```
th gc
clones whose worktree is gone:
  app_wt_feat_login_d668f0    112 MB     was feat/login
drop 1 database(s)? [y/N]
```

## Migrations and seeds

Both are opt-in (`th doctor --db`) and never run from `th ls` — a status command
is seconds of your tooling per worktree, and the fleet table exists to be glanced
at.

```toml
# treehouse.toml, committed
[migrations]
status = "alembic current"   # treehouse reads the EXIT CODE, not the output

[[seed]]
name = "ramp"
command = "python manage.py loaddata ramp"
```

The exit code is the only signal alembic, Django and Prisma genuinely share —
all three exit non-zero when migrations are pending. treehouse pairs it with
`git diff <main>...HEAD -- <migrations dir>`: pending with new files on this
branch is your own work waiting to run, pending with none means main moved ahead.
It does **not** claim "diverged"; see A3 in [docs/user-stories.md](docs/user-stories.md).

`th seed ramp` runs your command and records the dataset in a marker table inside
that worktree's own database — so it rides the template copy (a new clone
inherits main's datasets), it is dropped with the database, and there is no state
file to keep in sync.

## Per-worktree isolation

Each worktree gets `COMPOSE_PROJECT_NAME=<app>_<slug>` in the `.env` of every
directory that actually holds a compose file, and a deterministic port offset
applied to every `PORT`/`*_PORT` key the main checkout declares. Same branch, same
ports, every run: the registry is the sibling `.env` files themselves, so there is
no state file to garbage-collect.

**Caveat:** this shifts the ports your app processes bind. A compose file's own
`ports: "3000:3000"` host mapping is **not** rewritten — parameterize it
(`"${PORT}:3000"`) if you run compose in more than one worktree at a time.
