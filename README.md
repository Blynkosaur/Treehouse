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
| `th ls` | One table: every worktree × branch × env × db × behind-main × dirty. Exits 2 on a FAIL fleet, like `doctor`. `--json`. |
| `th triage -- <cmd>` | Runs the command, streams it, and afterwards says whether the failure was the environment or the code. `--stdin`, `--hook`. |
| `th hook session` | Claude Code `SessionStart`: hands the agent this worktree's env and database state. |
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
| 0 | healthy, warnings only, or checks that could not be run |
| 1 | treehouse itself failed (usage, git, IO) |
| 2 | a FAIL finding: a curated required key missing or empty, a `.env` targeting the shared database while its own clone exists, a database name Postgres cannot hold, or a `treehouse.toml` that will not parse |

`th doctor` and `th ls` both answer this way, so an agent can gate on the fleet
without parsing a table. Two documented exceptions, both in
[Triage](#triage-environment-or-code): `th triage -- <cmd>` passes the **wrapped
command's** code through verbatim, because a wrapper that changes it is a
wrapper you cannot put in front of anything; and `th triage --hook` always exits
0, because it runs after every Bash call.

Requirements inferred from `.env.example` are warnings. *Env* failures come only
from a human-curated list:

```toml
# treehouse.toml, committed
[env]
required = ["DATABASE_URL"]
```

`--json` emits an object, never a bare array:
`{"schema": 2, "root": …, "status": ok|warn|fail|skip, "findings": […], "checks": […]}`.
`findings` is env drift per service; `checks` is the config, database, migration
and seed rows. `th ls --json` is the same envelope with `worktrees` in place of
`findings`, and every row carries its own `status` computed the same way — the
two commands cannot call one worktree ok and fail.

**`skip` is not a synonym for ok, and it never satisfies a green.** It means the
question could not be answered here: Postgres was unreachable, no
`[migrations] status` command is configured, this worktree is detached. A report
whose worst word is `skip` says `skip`, not `ok`, because "nobody asked" and
"verified fine" are the two answers an agent must never confuse. `th doctor --db`
against a dead cluster emits `skip` rows *for the migration and seed checks the
flag asked for* rather than dropping them — silence is the worst possible
answer. `skip` still exits 0: nothing is known to be broken.

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

**Identifiers are quoted, not refused.** Every database name treehouse puts in
SQL is wrapped in double quotes with any embedded `"` doubled — the well-defined
Postgres rule — so `app-db`, `APPDB` and a name with a space all work. Names go
to `psql` as argv or on stdin, never through a shell.

A name is still refused when quoting cannot rescue it: empty, longer
than 63 bytes (Postgres truncates silently, and two branches truncated alike
would share one database), or carrying a NUL, a newline or a carriage return.
Those are a **FAIL-level `db` check with a fix line**, not a quiet skip — a repo
that wants clones and will never get one has to hear about it.

*(Earlier versions refused anything outside `^[a-z_][a-z0-9_]*$`, so a repo whose
database was called `app-db` got no clones at all, permanently. That is fixed.)*

**`th ls` reports the same database state `th doctor` does.** The column answers
`main` (the checkout that legitimately uses the template), `ok`, `missing`,
`shared`, `adrift`, `unusable` or `skip` — not just clone-exists. `shared` is a
worktree whose clone exists while its `.env` still names the template, and it is
red here and a FAIL in the fleet verdict, exactly as in doctor's report. Nothing
about that state looks wrong from inside the app, and it is the one state in the
table that costs data.

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

## Triage: environment or code?

An agent that reads `connection refused` and starts debugging its own code burns
twenty minutes on nothing. `th triage` correlates the failure output with this
worktree's doctor state and says which it was.

```
th triage -- pytest -q
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

**A known gap:** `connection refused` needs a service check, and treehouse does
not have one yet (C2). So that signature can never be corroborated and always
lands on `unknown` with regex-only evidence, which the verdict says out loud.
It is not faked, and when the dead-service check lands it starts reaching
`environment` on its own.

### The three modes, and their exit codes

| Mode | Output | Exit code |
| --- | --- | --- |
| `th triage -- <cmd>` | the command's own streams, untouched; verdict on **stderr**, ≤10 lines | **the wrapped command's, verbatim** |
| `th triage --stdin` | verdict JSON on stdout | 0, or **2** for `environment` |
| `th triage --hook` | `hookSpecificOutput` JSON on stdout | **always 0** |

The wrapper is transparent on purpose — `time`, `env` and `nice` all pass the
code through, and `th triage -- pytest` has to keep failing a Makefile. That is
why the verdict goes to stderr: stdout belongs to the wrapped command, and a
verdict printed into it would corrupt every pipeline. (127 if the command could
not be started at all; 128 if it was killed by a signal.)

`--stdin` is for scripts and pipes, where a non-zero code is the useful signal;
there is no fourth exit code, because `environment` is a verdict about the
worktree and that is exactly what doctor's 2 already means.

**`--hook` always exits 0**, and its verdict rides in the JSON payload alone.
PostToolUse's protocol is stdout-JSON plus exit 0; exit 2 is the legacy
*blocking* shape and may read as a blocked tool call. Blocking an agent's tool
call because its environment looks broken is far worse than failing to hint at
it. Nothing else can make the hook exit non-zero either — an unreadable payload,
a broken `treehouse.toml` or a cwd that no longer exists all print one line to
stderr (visible in `claude --debug`) and exit 0. It runs after *every* Bash call;
it may never fail one for a reason unrelated to it.

### Your own signatures

```toml
# treehouse.toml, committed
[[signature]]
name = "kafka-down"
match = "NoBrokersAvailable"          # regex, matched line by line
cause = "kafka is not reachable"
fix = "docker compose up -d kafka"
needs = "service"                     # env | db | migration | service
```

`needs` names the doctor fact that must agree before triage says `environment`.
Entries merge by name, like `[[deps]]` and `[[seed]]`: reuse a built-in's name
to replace it, use a new one to add.

### When `treehouse.toml` will not parse

Every command degrades to the built-in defaults and keeps working — the file is
optional, so a broken one must not be worse than an absent one. It is also a
**FAIL-level `config` check** naming the file and the parse error, in `doctor`,
in `ls` and in both JSON outputs, because the file is nothing but human judgment
(required keys, seeds, signatures) and silently not applying it is how a
`required = [...]` list stops being enforced without anybody noticing. `th seed`
is the one exception: it cannot work without the file at all, so it errors.

Neither hook ever fails over it. `th triage --hook` and `th hook session` exit 0.

## Claude Code hooks (UNVERIFIED — read this before pasting)

Two hooks: one hands an agent the worktree's state at session start, the other
explains a failing Bash command while it is still on screen.

**The code is tested; the wiring is not.** The block below was written from
first-party hook source, not from a run that was watched end to end. Four things
are unconfirmed, and the first one decides whether the `PostToolUse` half works
at all:

1. **Does `PostToolUse` fire when a Bash tool call *errors*?** If it only fires
   on success, the failure hook is dead as designed and has to move to a
   `UserPromptSubmit` shape. Check this first.
2. Does `additionalContext` from `PostToolUse` actually reach the model, or only
   the transcript?
3. The exact `.claude/settings.json` nesting below.
4. Which `SessionStart` `source` values should trigger the session hook —
   probably `startup` only, but `resume`/`clear`/`compact` are untested. That is
   why `th hook session` does not filter by source itself: the matcher below is
   where you decide, and you can see it.

One more thing that IS verified and worth knowing: **a Bash `tool_response`
carries no exit code** — only `stdout`, `stderr` and `interrupted`. There is no
"did it fail" to branch on, so the signature map *is* the failure detector. The
hook runs after every Bash call and exits 0 in silence when nothing matches,
which is also what makes it silent when the verdict is `code`. It never re-runs
the command: the output is already in the payload, and re-running would re-run
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

### Verify it yourself, in three steps

```sh
# 1. the payload path works, with no Claude Code in the loop. Run it from your
#    own repo — triage answers about the worktree you are standing in.
printf '%s' '{"hook_event_name":"PostToolUse","tool_name":"Bash","cwd":"'"$PWD"'",
  "tool_input":{"command":"pytest -q"},
  "tool_response":{"stdout":"","stderr":"KeyError: '"'"'DATABASE_URL'"'"'","interrupted":false}}' \
  | th triage --hook
#    → {"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"th triage: …"}}
#    (a fuller payload lives at cmd/testdata/posttooluse.json)

# 2. the session hook works
th hook session

# 3. the wiring works — in a scratch repo with the settings block above
claude --debug
#    …then run a command that fails with `KeyError: 'SOMETHING'`, and grep the
#    debug log for BOTH: the hook firing, and the context landing in the model's
#    turn. The first without the second is unknown 2 above.
```

Step 3 no longer risks the blocked-tool-call failure: `--hook` exits 0
unconditionally, so the only question left is whether `additionalContext`
reaches the model.

## Per-worktree isolation

Each worktree gets `COMPOSE_PROJECT_NAME=<app>_<slug>` in the `.env` of every
directory that actually holds a compose file, and a deterministic port offset
applied to every `PORT`/`*_PORT` key the main checkout declares. The registry is
the sibling `.env` files themselves, so there is no state file to
garbage-collect.

The offset is derived from the branch name, so the same branch normally lands on
the same ports — but that is **not** a guarantee over time. The offset has to
dodge ports already claimed by main and by every sibling worktree, so adding a
worktree that happens to want yours will move you on the next `hydrate`, and
your containers come back on new ports. Deterministic given the fleet; not
stable across changes to it.

**Caveat:** this shifts the ports your app processes bind. A compose file's own
`ports: "3000:3000"` host mapping is **not** rewritten — parameterize it
(`"${PORT}:3000"`) if you run compose in more than one worktree at a time.
