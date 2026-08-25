# treehouse — user stories

**Product in one line:** a worktree manager where new worktrees are _born ready_ — env filled, own database, deps present, verified green — instead of born broken.

Personas:

- **Dev** — runs 2–4 parallel worktrees (Conductor / `claude --worktree` / terminal) against one laptop's Postgres+Redis.
- **Agent** — a coding agent (Claude Code etc.) working autonomously in one of those worktrees.
- **Teammate** — someone cloning the repo fresh.

Status: ✅ shipped in v0.1 · 🎯 differentiator (unclaimed in prior-art searches, 2026-07-01/02) · 🧱 table stakes (exists elsewhere, must match) · ➕ nice-to-have

---

## Pain map → coverage

Documented worktree pains (research + Bryan's own usage history) and the story that addresses each:

| #   | Pain                                            | Story                    |
| --- | ----------------------------------------------- | ------------------------ |
| 1   | untracked files missing (.env, secrets)         | C1, L1                   |
| 2   | deps reinstall + disk (node_modules × N, .venv) | E1                       |
| 3   | one shared database, migrations collide         | A1–A4                    |
| 4   | port fights on :3000                            | E3                       |
| 5   | docker compose project collisions               | E2                       |
| 6   | stale base                                      | L1, C2                   |
| 7   | worktree confusion ("where is what?")           | L2                       |
| 8   | cruft accumulation, no pruning                  | L3                       |
| 9   | disk usage generally                            | E1 (partial)             |
| 10  | IDE/editor friction                             | non-goal                 |
| 11  | WIP stuck in wrong worktree                     | non-goal (future `move`) |
| 12  | cold build caches                               | non-goal                 |
| 13  | agent fleet visibility & cleanup                | L2, L3, B2               |

---

## Build status — 2026-08-11

Commands shipped: `doctor`, `hydrate`, `make`, `init`, `new`, `ls`, `rm`, `gc`, `seed`, `path`, `triage`, `run`, `vault`, `hook session`, `tui` (and bare `th`, which opens the dashboard on a terminal). (`th` is an alias for the `treehouse` binary.)

| Status | Stories |
| --- | --- |
| ✅ **Done** | **S1–S3** (`th run`, `th vault`, the two A2 guards, the secrets checks), **E1** (instant deps), **C1** (hydrate fills `.env`), **E2** (compose namespace), **E3** (port offsets), **A1** (db clone), **A2** (`.env` points at it, doctor fails when it doesn't), **A4** (named re-seed), **A5** (`gc`), **A6** (redis logical db), **L1** (`new`, `open` hook included), **L2** (`ls`), **L3** (`rm`, database and compose teardown included), **L4** (`th path` + the documented shell function), **B1** (`triage`, three modes, `connection refused` corroborated), **B3** (`th why`), **C4** (`SessionStart` hook — verified firing 2026-07-27), **C2** (doctor, dead-service and stale-base checks included), **T1** (live TUI dashboard) |
| 🟡 **Partial** | **A3** (migration state, with `diverged` cut from the AC — see below), **B2** (built and wired, but Claude Code fires no hook on a *failed* Bash call — verified 2026-07-27; the wrapper `th triage -- <cmd>` covers the case the hook cannot reach) |
| ✂️ **Cut** | **C5**'s `.env.example`/compose scan (inference is live; a generated copy goes stale and wins), **C3** (`snapshot` — `make` is the same command with a different filename) |

Foundations in place that unblock the above: `Discover`, `MainWorktree`, `Worktrees`/`Ref` (one porcelain parser), `EnvVarsByDir`, `Slug` (collision-safe branch → identifier), `Status` (one worktree, no I/O — the row a TUI renders), the plan-then-apply pattern (`Finding`/`Repair`/`DepPlan`/`DBPlan`/`DBDrop`/`OpenPlan`), `Check` (the non-env verdict beside `Finding`), `Triage` (the same correlation, pure), and `treehouse.toml` config parsing with one generic name-keyed `Merge` — now serving four lists (`[[deps]]`, `[[seed]]`, `[[signature]]`, `[[service]]`).

**Pain coverage so far:** pain 1 (missing `.env`) via C1/hydrate; pains 2 & 9 (deps reinstall + disk) via E1; **pain 3 (shared database, colliding migrations) via A1/A2/A3/A4**; pain 4 (port fights) via E3; pain 5 (compose collisions) via E2, and A6 for its cache twin (a flush in one worktree no longer takes out another's sessions); **pain 6 (stale base) via L1's fetch-then-cut and C2's `base` check**; pain 7 (worktree confusion) via L2 and `th path`; **pain 8 (cruft) via L3 + A5**; pain 13's agent half via B1/B2 (an agent no longer debugs phantom code bugs caused by a broken environment, and `connection refused` now reaches a verdict instead of a shrug). Pains 10–12 still open.

---

## Epic L — Lifecycle: the manager commands 🎯

**L1. `treehouse new <branch>` — born ready.** As a Dev, one command gives me a worktree that is immediately workable: fetches origin, cuts the worktree from _fresh_ base (kills pain 6 at the root), then runs the full hydrate pipeline — env fill from canonical, db clone + `DATABASE_URL` pointing, CoW deps, compose/port namespacing, seed steps — and finishes with a doctor report.

- AC: single command, ends with green doctor or a clear list of what's still red; `--from <ref>` overrides base; helpful error when the branch is already checked out elsewhere (pain 7); optional `open` hook (editor/agent command) after green.
- ✅ **Done (2026-07-26).** `th new <branch>` places the worktree as a **sibling** of the main checkout (inside would poison `Discover`/`findDepDirs`), resolves local → remote-tracking → new-branch-from-`origin/HEAD`, warns instead of failing when `git fetch` can't reach origin, then runs the same `hydrate` pipeline and prints the doctor report. Dep failures are red lines, not aborts.
- ✅ **The `open` hook landed (2026-07-26).** `[open] command = "cursor ."` (or `"claude"`) runs with the worktree as cwd once the report is printed; `--open` / `--no-open` force either way; an unconfigured repo says nothing, because a nag on every `th new` is how a good flag gets turned off. **It does not fire on a FAIL verdict** — that guard is the story, not a detail: somebody handed a worktree whose `.env` still targets the shared database starts working in it before they read the report scrolling past above their editor. Warnings do **not** block, because inferred drift and a dead port are the normal state of a repo you haven't started yet, and a hand-off that almost never fires is one nobody configures. It runs in the **foreground and waits**: a GUI launcher (`cursor .`, `code .`) forks and returns immediately so waiting costs nothing, while a terminal agent needs the terminal — backgrounding it would leave it fighting the returning shell prompt for stdin. A failing open command is a report line, never an exit code; `th new`'s exit code is a verdict about the worktree, not about somebody's editor.

**L2. `treehouse ls` — one table, everything.** As a Dev with 4 worktrees, I see worktree × branch × {env, services, db, seed, behind-main, dirty} at a glance, so I spot the broken one before assigning an agent to it.

- AC: `--json` for tooling; current worktree highlighted; state columns reuse doctor (no second implementation). (Absorbs the old "fleet view" epic. `wt`/`wtdb status` show git/db columns only — the state columns are the differentiator.)
- ✅ **Done (2026-07-26).** `th ls` shows worktree × branch × env × db × behind-main × dirty, current row marked, `--json` in the same envelope doctor uses. The row is computed by `check.Doctor.Status` — one worktree, no I/O — so T1's TUI streams rows instead of reimplementing them. A services column is possible now that C2's dialler exists, but `ls` still runs no per-worktree network fan-out; seed state stays in `doctor --db` for the same reason.
- ✅ **Reconciled (2026-07-26, was Open).** Two things disagreed and both are fixed. The db column was clone-exists-only, so a worktree whose clone existed while its `.env` still named the shared database read `db: ok` — green, in the view people use to pick which worktree to hand an agent — while `doctor` called that state a failure and exited 2. It is now `check.DBWord`, the same switch `CheckDB` narrates: `main | ok | missing | shared | adrift | unusable | skip`, with `shared` red and FAIL. And `ls --json`'s `status` was `worstEnv(rows)` where doctor's was `check.Verdict`; both are `check.Verdict` now, folded by `check.Fleet`, and `th ls` exits 2 on a FAIL fleet so an agent can gate on it without parsing a table. Schema stayed 2 — nothing consumes it yet, and the two agreeing beats announcing that they used to differ.
- The full db question is affordable per row because `Row` already walked the worktree to compare `.env` keys: asking that same map which database it names is a lookup, not a psql round trip. Migration state is still deliberately absent — a subprocess per worktree would make the fleet table slow and side-effecting, so it stays in `doctor --db`'s `checks` list. `Status.Migrations` was a declared-but-never-filled field promising otherwise; deleted.

**L3. `treehouse rm <branch>` — remove without corpses.** As a Dev, removing a worktree also drops its db clone and its compose project, so nothing accumulates.

- AC: refuses when dirty/unpushed unless `--force`; ~~`treehouse rm --merged` sweeps every worktree whose branch is merged~~.
- ✅ **Done (2026-07-26)**. `th rm <branch>` refuses dirty or not-on-any-remote work without `--force`, and **flatly refuses the worktree you're standing in** even with it. It now drops that worktree's database clone through A5's own plan — same ownership rule (a provenance comment naming this repo), same refusal when connections are live, silent when there is no clone. Only the removed branch's clone: `th rm feat/a` deleting feat/b's database would be a surprise.
- ✅ **Compose teardown landed (2026-07-26).** `docker compose -p <project> down` runs **before** `git worktree remove`, on the project name E2 already wrote into the worktree's own `.env` — known, not guessed. `check.ComposeProjects` subtracts every name the main checkout also claims, which is the compose version of A2's shared-database guard: a half-hydrated worktree still carries main's project name, and tearing *that* down stops the containers somebody is working in, from a command they ran about a different branch. It runs from a directory holding **no** compose file on purpose — with a file, compose removes what that file declares; without one it works from the project label and catches every container the project ever started, including from a file that has since changed. `down`, never `down -v`: volumes are data, and deleting somebody's database volume because they removed a branch is a loss, not a cleanup. Every failure is silent (no docker, dead daemon, project never started) — none of them may fail a command whose job is a worktree and a branch.
- **Cut: `--merged`.** `git branch --merged` cannot see a squash-merged branch, so the sweep would silently skip exactly the branches most teams produce. A cleanup you can't trust is worse than none; `th rm` stays explicit.

**L4. Shell niceties ➕.** `treehouse cd <branch>` jump with completion, like wtp.

- ✅ **Done (2026-07-26), and deliberately not as a command.** A process cannot change its parent shell's working directory, so `th cd` is impossible to build honestly — every tool that appears to have one ships a shell function, and pretending otherwise would mean a `cd` that silently does nothing. So the product is split: `th path <branch>` is the half a binary *can* do, and the README carries the three-line bash/zsh/fish function that wraps it.
- `th path` prints the path **alone** on stdout, not through `say` — the output is being read by `cd "$(th path x)"`, and a decorated line or a `--quiet`-suppressed one breaks the caller either way. An unknown branch prints nothing and exits non-zero, because a `cd` to an empty string lands in `$HOME`. Completion comes free from cobra's `ValidArgsFunction`, which covers the AC's "with completion".

---

## Epic A — A database per worktree

**A1. Isolated db on hydrate. 🧱** As a Dev with 3 worktrees, each gets its own Postgres database cloned from the shared dev db (`CREATE DATABASE app_wt_<slug> TEMPLATE app_dev`), so one branch's migration can never break the others.

- AC: near-instant (template copy); branch names with `/` slugged; re-running hydrate reuses the existing clone.
- ✅ **Done (2026-07-26)**. `check.PlanDB` decides, `internal/pg` does. No template resolvable from main's `.env` → nothing is created and psql is never asked, so a non-Postgres repo leaves no orphans; detached HEAD skips, because a directory name keys the clone to a path. A template somebody is connected to is the COMMON case: hydrate names the sessions and stops, and only `--force-db` ever disconnects anyone.
- Prior art (verified 2026-07-02): **wtdb** does clone + `DATABASE_URL` rewrite (Postgres-only, creation-time, copies env verbatim — no validation/repair). A1+A2 = table stakes to match; decision: absorb (reimplement, ~30 lines), don't depend.

**A2. Env points at my clone automatically. 🧱** The worktree's `.env` is rewritten (`POSTGRES_DB`/`DATABASE_URL`) to the clone — _after_ canonical repair, so we never point a broken env at a fresh db.

- AC: `doctor` fails loudly if `per_worktree = true` but `.env` targets the shared db.
- ✅ **Done (2026-07-26)**, doctor check included. `doctor` **fails (exit 2)** when a clone exists and this worktree's `.env` still names the shared database — the state a half-applied hydrate leaves, which looks perfectly healthy from inside the app right up until the migration lands on every other worktree at once. Main is exempt: it IS the template. (The AC's `per_worktree = true` gate was dropped — there is no such config key and no need for one; the condition is simply "a clone exists for this worktree".) A fourth derived value beside E2/E3: `DATABASE_URL` is rewritten through `net/url` (a regex loses to `?sslmode=require`, to an `@` in the password, and to a non-default port), `POSTGRES_DB` alongside it. Both keys move together or neither does — half a repoint leaves the app on the shared db while doctor reads green. Set only after the clone is confirmed to exist.

**A3. Migration-state awareness. 🎯** `doctor --db` says whether migrations are pending, and which side moved.

- AC: configurable `[migrations] status` cmd (alembic/django/prisma-agnostic); verdicts: behind / ahead / ~~diverged~~.
- 🟡 **Partial (2026-07-26) — `diverged` is CUT from the AC, and `ahead` is reported honestly rather than claimed.** A single generic status command cannot yield behind/ahead/diverged: `alembic current` prints a revision hash (deciding "ahead" needs the migration DAG), Django's `showmigrations` prints `[X]`/`[ ]` and cannot express "ahead" at all, `prisma migrate status` prints prose. Parsing three formats to synthesise a verdict none of them reports would be a confident lie. The genuinely shared signal is the **exit code** — all three exit non-zero when migrations are pending — so v1 combines it with a second source we CAN read honestly: `git diff --name-only <mainBranch>...HEAD -- <migrationsDir>`. Pending + this branch adds files → "your branch adds N migrations that haven't run" (the honest version of "ahead — expected"); pending + no new files → "main moved ahead". Exit 126/127 is a config typo, never a pending migration. The dir is inferred from `migrations/`, `alembic/versions/`, `prisma/migrations/`, `db/migrate/` at the worktree root; `[migrations] dir` sharpens it.
- **Never runs from `th ls`.** A status command is seconds of somebody else's tooling per worktree; the fleet table exists to be glanced at. Opt in with `th doctor --db`.

**A4. Named re-seed per worktree. 🎯** Re-seed _my_ clone with a named dataset ("ramp", "sondermind") without remembering the incantation; `doctor` shows which datasets are present.

- AC: seed steps run against the worktree's db; ~~a check query reports loaded datasets by name~~ → **a marker table treehouse writes itself**.
- ✅ **Done (2026-07-26).** `th seed <name>` runs the `[[seed]]` command against this worktree's clone; `th doctor --db` reports which datasets are present.
- **Cut: the per-seed `check` config key.** It assumed projects track their own seed state; almost none do, so the key would have been unfillable for nearly everybody — and a config key most people can't fill is a feature most people don't get. Instead treehouse writes `treehouse_seed(name, applied_at)` into the worktree's **own** database. That choice pays three ways: it rides the `TEMPLATE` copy, so a clone correctly inherits main's datasets; it is dropped with the database; and there is **no state file to garbage-collect** — the same principle the port registry follows. `th seed` refuses to run against the shared database, because seeding it through a half-applied hydrate is not recoverable by re-running anything.

**Identifier handling across Epic A (2026-07-26, was a ceiling):** every database name treehouse puts in SQL goes through `check.Quote` — wrapped in double quotes, embedded `"` doubled, the well-defined Postgres rule — and reaches `psql` as argv or on stdin, never through a shell. The earlier rule refused anything outside `^[a-z_][a-z0-9_]*$`, which is not Postgres's rule but the rule for names needing no quoting: a repo whose database was called `app-db` or `APPDB` got **no clones at all, permanently**, with one skip line. Refuse-don't-escape was the right instinct about injection and the wrong trade about blast radius. What remains refused is only what quoting cannot rescue — empty, over 63 bytes, a NUL, a newline or a CR — and that is a **FAIL-level check with a fix line**, not a quiet skip. Verified against a throwaway cluster: `app-db`, `APPDB`, an embedded quote, a space and `th;selftest--` all round-trip through CREATE/COMMENT/Sessions/MarkSeed/DROP.

**A5. Clone garbage collection.** `treehouse gc` lists db clones whose worktrees are gone and drops them after confirmation. (L3 prevents; gc cures.)

- ✅ **Done (2026-07-26).** **Ownership is by provenance comment, never by name prefix** — a prefix can match somebody's real database, and `Slug` is one-way, so a name can't be reversed to the branch a human needs to see before approving a drop. Anything without a `treehouse:<mainWorktreePath>:<branch>` comment naming THIS repo is not a candidate, full stop. Liveness is checked two ways and either one spares a database: the branch (the honest test) and the derived name (the belt, so renaming the shared database can't turn the whole live fleet into candidates). The template is name-checked as well. A database it cannot **prove** is idle is reported and kept — open connections, and equally an error from `pg_stat_activity`, because dropping out from under a running process creates exactly the corpse gc exists to remove. (Until 2026-07-26 an *error* fell through to the drop: the guard switched itself off precisely when the cluster was misbehaving. `keepReason` is the decision now, and it is unit-tested.) A third liveness test was added at the same time: the database each live worktree's `.env` actually names, which is the only one that survives `git checkout --detach` and `git branch -m`. List-then-confirm by default, `-y` for scripts, `--json` in the same envelope — and `--json` never prompts, so a scripted caller cannot hang. An unreachable cluster answers `status: "skip"`, never `ok`: "nobody looked" is not "there is nothing to collect". `check.PlanGC` is the pure decision; `cmd/gc.go` does the psql and the prompt.

**A6. Redis isolation ➕ (stretch).** Each worktree gets its own Redis logical db (`redis://localhost:6379/<n>`), so one worktree's cache flush doesn't nuke another's session.

- ✅ **Done (2026-07-26).** A fourth derived value in the same `PlanDerive` pass as E2/E3/A2, not a phase of its own. Discovery is main's `REDIS_URL`/`REDIS_DB`; assignment is the **same predicate E3 uses for ports** — derived from the slug, so a branch always lands on the same db, and disjoint from every db main and its siblings already declare. The registry is `DeriveInput.Fleet`, the sibling `.env` files themselves; there is no second registry and no state file. `REDIS_URL` goes through `net/url`, never a regex: credentials before the host and `?query` after the db are exactly what a pattern mangles, and an empty path is db 0, which is what a client defaults to. Nothing connects to Redis.
- **The ceiling is real and it is 16.** Redis ships `databases 16` (0–15) and main holds one, so the fleet tops out around 15 worktrees — unlike E3's 200 port offsets, which no laptop exhausts. Exhaustion is a **skip line, never a failed hydrate**, because this is a cache; the upgrade path is a separate Redis instance per worktree (on a port E3 already derives) or a key prefix, and both are bigger changes than treehouse should make unasked. A related deliberate collapse: **one db per worktree, not per service**, so services main splits across dbs are merged — 16 cannot afford the alternative.
- **A repo with no Redis gets no rows and no skip.** "Nothing to check" is not "couldn't check", the same rule that keeps a repo declaring no database from growing a `db` row.

---

## Epic B — Failure triage: "environment or code?" 🎯

**B1. Structured verdict on failure.** As an Agent, when a command fails, `treehouse triage -- <cmd>` correlates the failure output with doctor state and returns `{cause: environment|code|unknown, evidence, fixes}` — so I repair the environment instead of debugging phantom code bugs.

- AC: default signature map (`connection refused :PORT` → service; `relation does not exist` → migration/seed; `KeyError`/undefined env → env); repo config adds custom signatures.
- ✅ **Done (2026-07-26).** `check.Triage` is pure — output in, doctor's own `[]Finding` and `[]Check` in, verdict out — so the whole table below is tested from struct literals. Three invocation modes share it: `th triage -- <cmd>` (transparent wrapper), `--stdin` (pipe), `--hook` (B2). `treehouse.toml [[signature]]` extends the built-ins through the same name-keyed `Merge` that `[[deps]]` and `[[seed]]` use.

  **The correlation is the story, not the regex.** A pattern that matches proves the output _looks_ environmental; only doctor can say whether the environment is actually broken.

  | signature | doctor for that area | verdict |
  | --- | --- | --- |
  | matched | red | `environment` — evidence and fixes from both |
  | matched | green | `unknown`, evidence names the contradiction |
  | none | red somewhere | `unknown`, red row offered as possibly related |
  | none | green | `code` |

  Row 2 is the entire value of correlating. A confident wrong "it's your environment" sends an agent to reinstall Postgres for twenty minutes over a typo in its own code — strictly worse than no verdict at all. `skip` and _absent_ checks are treated as unknown, never as green, for the same reason: "we never asked" must not read as "we verified it".

- ✅ **The `connection refused` gap is closed (2026-07-26).** It was the one signature that could never reach `environment`: `Needs: env` mapped onto `CheckEnv` and `db`/`migration` onto the `Check` list, but **`service` mapped onto nothing**, so B1's own headline example degraded to regex-only evidence with cause `unknown`. Faking it (dialling the port from inside triage) was refused on purpose — that is a new kind of check smuggled in through the back door — and the honest fix was always C2's dead-service check. It landed, and the entry filled itself in: `connection refused` plus a dead listener is now `cause: environment`, with the matched line and the dead service side by side in the evidence and both fixes carried. Every service up still lands on `unknown` with the contradiction spelled out, which is row 2 of the table doing its job. One implementation detail is load-bearing: services produce **one row per detected port**, so `areas()` folds the **worst** of them rather than the last one walked — a healthy sibling must not vote a dead service green.
- **Exit codes:** the wrapper passes the **wrapped command's code through verbatim** — `time`/`env`/`nice` all do, and `th triage -- pytest` has to keep failing a Makefile — so its verdict goes to **stderr**, where it cannot corrupt a piped stdout. `--stdin` uses the existing 0/2, because that mode is for scripts and pipes where a non-zero code is the useful signal. `--hook` always exits 0 — see B2. No fourth code was added: `environment` is a verdict about the worktree, which is what doctor's 2 already means.

**B2. Automatic verdict injection.** A Claude Code `PostToolUse` hook feeds the triage verdict to the agent whenever a Bash command fails — no more agents doing "a whole bunch of nothing" for 20 minutes because Redis was down.

- AC: copy-paste hook in README; quiet output ≤ 10 lines; **silent when verdict is `code`** (don't spam the agent).
- 🟡 **Code done, wiring unverified (2026-07-26).** `th triage --hook` reads the payload, renders ≤10 lines of `additionalContext`, and ships with a `PostToolUse` fixture (`cmd/testdata/posttooluse.json`) so it is testable without Claude Code in the loop.
- ✅ **`--hook` always exits 0 (2026-07-26).** It used to exit 2 on an `environment` verdict, per an earlier spec that was wrong: PostToolUse's protocol is stdout-JSON plus exit 0, and 2 is the legacy **blocking** shape. Blocking an agent's tool call because its environment looks broken is far worse than failing to hint at it, so the verdict rides in the payload alone. Nothing else can make it non-zero either — an unreadable payload, a `treehouse.toml` that will not parse, a cwd that vanished: one line to stderr (still visible in `claude --debug`) and exit 0. Anything wired into a per-Bash-call hook must be silent when it has nothing to say and must never fail the call for a reason unrelated to the call. The broken-config path in particular used to fail **every Bash call for the whole session**.
- **The AC as written is not implementable, and the design absorbs it.** "Whenever a Bash command fails" assumes the hook can tell. It cannot: a Bash `tool_response` carries only `stdout`, `stderr` and `interrupted` — **no exit code**. (Verified against first-party shipping hook code, which infers failure by regex over the output text for exactly this reason. Same source corrected the stdin field: it is `tool_response`, not the `tool_result` a local SKILL.md claims — the wrong name ships a hook that silently never fires.) The resolution is that **the signature map IS the failure detector**: run on every Bash `PostToolUse`, exit 0 in silence when nothing matches. That collapses the AC's "silent when the verdict is `code`" into the same code path, with no extra machinery — a passing command matches nothing, and neither does a code bug.
- **A hook never re-runs the command.** It would re-run `git push`, `rm`, a migration — and it is unnecessary, since the output is already in the payload. There is a test asserting it.
- **Unverified, in the README and worth checking before trusting it:** whether `PostToolUse` fires at all when a Bash call _errors_ (if not, B2 is dead as designed and must move to a `UserPromptSubmit` shape — **check this first**); whether `additionalContext` reaches the model or only the transcript; the exact `.claude/settings.json` nesting; (the blocked-tool-call question is gone: the hook exits 0 unconditionally now). The README carries the block, marked unverified, plus the verification recipe.

**B3. Human one-liner.** `treehouse why` answers in one line what changed since everything was last green.

- AC: state journal records last-green per check; `why` diffs current vs last-green.
- ✅ **Done (2026-07-26).** `th doctor` records the journal, `th why` diffs the live report against it and answers in one line: `env went from ok to warn since 14:02: REDIS_URL missing`, `db stopped being checked after you switched to feat/login: postgres is not reachable`. Several changes fall back to a headline over a short list. `--json` in the existing envelope, and it always exits 0 — `why` answers what CHANGED, not whether the worktree is healthy, and doctor and `ls` already gate on that.

**Why the objection dissolved.** It was deferred twice on where the journal lives, and that is the only thing that changed: **it lives in the worktree's own git directory**, `<main>/.git/worktrees/<name>/treehouse-state.json`, found with `git rev-parse --absolute-git-dir` (a linked worktree's `.git` is a *file*, so `<root>/.git/` would have put it in the working tree — committed by somebody's `git add -A`, and visible in `git status` forever). There:

- it is never committed and `git status` never sees it;
- **`git worktree remove` deletes it**, along with `th rm`, which calls it — so there is still nothing for `th gc` to chase, the same bargain E3's port registry (the sibling `.env` files) and A4's seed marker (a table inside the database, dropped with it) already make;
- it is per-worktree by construction, so it cannot answer about the wrong branch.

**And it is an optimization, never a dependency** — the other half of the original objection, that a state file goes stale and lies after a manual fix:

- missing, unreadable, truncated, hand-edited, or written by an older schema all answer the same way, `no baseline yet — run th doctor first`, exit 0. There is no repair path and no error, because every one of those is a reason to stop trusting the file and none of them is a reason to stop working;
- `doctor` writes it with every error swallowed. A doctor that failed — or even warned — because a state file could not be written is precisely what this project refused to build;
- written temp-then-rename in the same directory, `envfile.Set`'s discipline: surviving a torn file every run is not the same as never causing one;
- it stores only **when each row was last ok and on which branch**. Everything about what is wrong *now* comes from the live report, so there is no stored detail line that can go stale or contradict a manual fix.
- `check.Snapshot` flattens findings and checks into one flat vocabulary of `Check`s (env rows become `env` / `env (api)`) rather than a second near-identical struct, and `check.Explain` is a pure function over it — every sentence above is tested from struct literals with the clock and the branch handed in.
- **`ok` → `skip` gets its own sentence**, per the rule that runs through the whole report: a check that stopped being *asked* has not stayed fine, and it is usually the actual story (Postgres went down, so the db check stopped running). Phrasing it as "went from ok to skip" would bury that in a failure's wording.

---

## Epic C — Core doctor/hydrator ✅ (C1, C2 done; C3 and C5's scan cut; C4's wiring unverified)

**C1.** `hydrate` fills `.env` from canonical without overwriting local values. ✅ **Done** — append-only writes from the main worktree; no backup needed since it never overwrites (present-but-empty keys deferred to v2).
**C2.** `doctor` reports missing/empty required keys, dead services, unseeded data, stale base — each with a fix line; `--json`, `--quiet`, exit codes. ✅ **Done (2026-07-26)** — env-key drift, the database check, `--db` migration and seed checks, the dead-service check, the stale-base check, `--ls` table, main-worktree fallback, `--json` (an object envelope: `schema`/`root`/`status`/`findings`/`checks`, **schema 2**), `--quiet`, and exit codes 0/1/2. Five things fire the FAIL tier: `[env] required`; a worktree whose `.env` targets the shared database while its clone exists; a template database name Postgres cannot hold; a `treehouse.toml` that will not parse; and a `[[service]]` entry with nothing listening. **`skip` is first-class (2026-07-26):** it never satisfies `Verdict` as passing — a report whose worst word is `skip` says `skip`, not `ok` — and `doctor --db` against a dead cluster emits `skip` rows for the migration and seed checks it was asked for rather than dropping them, which is what it used to do.

  **The dead-service check reuses discovery rather than adding it (2026-07-26).** There is no docker-compose YAML parser, and there is deliberately never going to be one: `derive.go` already finds every `PORT`/`*_PORT` key with a plausible value, because E3 has to shift them, and **those keys are the service list** — a key named `PORT` in `svc_a/.env` *is* the statement that svc_a should have a listener. A second discovery path would be a second source of truth, disagreeing with the one hydrate acts on. `CheckServices` is pure and takes dial results in the way `DBInput.Existing` takes database names; `DialServices` is the impure half beside it, `net.DialTimeout` at **250 ms, concurrently** — a doctor that takes five seconds because three services are down is a doctor nobody runs. Inferred rows are **WARN**, `[[service]]` rows are **FAIL**, which is the progressive-configuration tier the env checks already follow. **No ports and no config produces no rows at all**, the same rule that gives a repo with no database no `db` row: nothing to check is not the same sentence as checked and fine. A `[[service]]` with no `addr` is `skip`, never `ok` and never `fail` — a typo in a TOML file is not a dead service, and it is not a healthy one either.

  **It is not reachable from `th ls`**, the same constraint migrations live under: the fleet table exists to be glanced at, and a network fan-out per row is not a glance. A dial is cheap enough that a services column is genuinely viable later — that is a decision to make deliberately, not a side effect of adding the checker.

  **The stale-base check reuses `gitBehind`** (now exported as `check.Behind`) rather than shelling git a second time, so doctor's `base` row and `th ls`'s BEHIND column can never disagree. **WARN, never FAIL:** a stale branch is a smell, the code still runs, and an exit 2 on a worktree that works is how a checker teaches people to stop reading it. Zero commits behind is an `ok` row, consistent with how the `db` check reports a healthy state — but **the main checkout gets no row at all**, and that is correctness rather than tidiness: the count is `HEAD..<main branch>`, which in main is always zero, so a row there would print "up to date" over a main that is ten commits behind origin. *ponytail:* the count is against the **local** main branch and doctor does not fetch, so a stale local main under-reports; the fix line fetches, and `th new` already does.

  **Why `Check` is a sibling of `Finding`, not a wider `Finding`:** a `Finding` is shaped around env keys (`Missing`/`Empty`/`NoEnv`/`Keys`). Database, migration and seed results share none of that shape, and widening the struct would give every env row a pile of nil db fields to carry and every consumer a pile to skip. So the envelope carries two flat lists — `{schema: 2, root, status, findings: […], checks: […]}` — each with its own shape. The version bumped because that is what the field is for: a consumer reading `findings` for the whole story is wrong now, and should be told at the envelope rather than by silently missing the database row.
**C3.** `snapshot` captures the current working `.env` as canonical. ✂️ **Cut (2026-07-26).** `th make` already does this: it reads each service's live `.env` and writes the key set out to `.env.example`. `snapshot` is the same walk over the same files with a different output filename, and the only thing it would add is *values* — which is precisely what `make` blanks, because the file it writes is committed. A command that captures a working `.env` **with** its values is a secrets-writing command, and this project's non-goals say secrets go to varlock/Doppler. So: same command, or a bad idea. Neither is worth a subcommand. **(Epic S reopened the underlying question and answered it differently: the way to keep values safely is not to write them to a second file, it is to take them out of the first one. See S1.)**
**C4.** `SessionStart` hook: agent starts with env state in context. 🟡 **Code done, wiring unverified** — `th hook session` emits this worktree's env and database state as `additionalContext`, capped at nine lines because it is prepended to a context window, not printed as a report. A green worktree costs one line plus a pointer to `th triage`, and a worktree whose checks could not be **run** says so rather than reading as green. It deliberately does **not** filter on the `source` value (`startup`/`resume`/`clear`/`compact`): which of those are worth spending context on is unverified, and the settings.json matcher is where a human can see and change that decision. See B2 for the rest of the unverified list.
**C5.** `init` scans `.env.example` + docker-compose and generates `treehouse.toml`. ✂️ **Scan cut (2026-07-26); the scaffold stays.** `init` writes a commented `treehouse.toml` covering every table, now including `[[service]]` and `[open]`. What it will not do is bake the inferences into the file, and the reason is the same one that keeps `[database] template` out of the config: **inference already happens live, on every run.** Required keys are inferred from `.env.example`, services from the `PORT` keys, the database from main's own `.env` — all of it re-derived each time `doctor` runs, against the repo as it is *now*. Writing those inferences into a committed file creates a second source of truth that is correct on the day it is generated and wrong the first time somebody adds a service, and — worse — the stale copy would *win*, because `treehouse.toml` is the sharpener that overrides what was inferred. A generated `[[service]]` list is how a repo ends up failing doctor over a service it deleted six months ago. The scaffold is comments; comments cannot go stale into a verdict.

**Also shipped, not in the original stories:** `make` — generates `.env.example` from each service's `.env` (values blanked), with a main-worktree fallback for empty worktrees.

---

## Epic E — Runtime isolation & fast setup 🎯

**E1. Instant dependencies.** ✅ **Done (2026-07-20).** `hydrate` clones declared heavy dirs (`node_modules`, …) from the main checkout via copy-on-write (`cp -c` on APFS) — instant, near-zero disk, isolated. Python `.venv` is _recreated_, never copied (absolute paths): `uv venv && uv sync`, command resolved from the manifest, reports if uv/manifest is missing. Rules are built-in defaults (node/python) extended by `treehouse.toml [[deps]]` — the agent extension point. `--skip-deps` opts out.

- Evidence: 5 × 2GB node_modules = 10GB; ~10GB burned in 20 min of agent worktrees (reported).
- Shipped as: `internal/check/deps.go` (planner), `internal/deps` (CoW doers), `internal/config` (toml), wired into `cmd/hydrate.go`. Unit + E2E tested.

**E2. Compose namespace per worktree.** ✅ **Done (2026-07-26).** `hydrate` writes `COMPOSE_PROJECT_NAME=<app>_<slug>` into the `.env` of every directory that actually holds a compose file — a repo with no compose file gets no key anywhere. `<app>` is main's own `COMPOSE_PROJECT_NAME` if it has one, else Compose's default rule (the main checkout's directory name).

**E3. Cheap port offsets.** ✅ **Done (2026-07-26).** `hydrate` shifts every `PORT`/`*_PORT` key main declares by one offset derived from the branch slug, checked against the ports every sibling worktree declares. One offset for all services, so inter-service spacing survives; same branch → same ports *given the same fleet*, because the registry is the sibling `.env` files and there is no state file to garbage-collect — but adding a worktree that wants your offset moves you on the next hydrate, so it is deterministic, not stable over time. Ceilings, on purpose: detection is by key name (`SERVER_ADDR=:3000` is invisible), "free" means undeclared rather than unbound on the host, and a compose file's `ports:` host mapping is **not** rewritten — parameterize it. Full proxy/subdomain routing stays punted to portree.

---

## Epic T — Live TUI dashboard (Bryan's call, 2026-07-13) 🎯

**T1. Health board.** Running `treehouse` with no args opens a bubbletea TUI: worktrees × {env, services, db, git} as a live grid, checks streaming in concurrently with spinners, drill into any worktree for doctor detail, `h` triggers hydrate and cells flip green in place.

- Stack: bubbletea + lipgloss + bubbles (Charm). The TUI is a _renderer over `[]Result`_ — checkers are unchanged; text/JSON outputs remain first-class (hooks/agents need them).
- Sequencing rule: built AFTER the plain CLI core works (sessions 4–5, lipgloss styling as the session-3 bridge). The TUI is the roof, not the foundation.
- Differentiation: nothing in the space has a live health dashboard (workz = static table). This is the README GIF.
- ✅ **Done (2026-07-26).** Bare `th` opens the board on a terminal; `th tui` is the explicit door. **The renderer premise held literally: `cmd/tui.go` adds no judgment.** Every cell is a field of `check.Status`, rendered by the same `envCell`/`dbCell`/`behindCell`/`dirtyCell` the `ls` table uses; the drill-in calls `diagnose` and `printReport`, the two functions `th doctor` calls, so it is a second _face_ on one answer and never a second answer. `printReport`/`printChecks` grew an `io.Writer` parameter purely to make that reuse possible, and `ls`'s fleet setup was extracted so the two views cannot disagree about which databases exist.
- **Streaming is one `check.Row` per worktree per `tea.Cmd`.** Bubbletea runs each batched command on its own goroutine and delivers each result as it lands, so it _is_ the WaitGroup — the grid shows path and branch from git's porcelain immediately and spins only the cells no checker has answered for yet. Verified race-clean against a live pty.
- **The TTY guard is load-bearing, not politeness.** Bubbletea cannot start without a controlling terminal, so without `isatty(os.Stdout)` every hook, CI job and agent capturing stdout would go from "prints help" to "exits 1". Non-TTY falls through to cobra's help, verbatim; `cmd/tui_test.go` pins it.
- **`h` shells out to `th hydrate` via `tea.ExecProcess`, deliberately.** In-process it would write straight to `os.Stdout` (`say`/`sayln`, and `deps.RunRecreate` wires `cmd.Stdout` itself) and shred the frame, and it would set the package-level flag globals that are only safe because exactly one command runs per process. As a subprocess hydrate's output scrolls normally, the frame is restored, and only the affected row is re-asked — cells flip in place.
- Keys: `q`/`ctrl-c` quit, `↑`/`↓`/`j`/`k` select, `enter` drill in, `esc` back, `h` hydrate the selected worktree, `r` refresh the fleet.
- **Ceilings, on purpose.** The drill-in never passes `--db`, so it shows the env and clone checks but never runs the project's migration-status command — the same bargain `ls` makes, for the same reason. The board does not auto-poll; `r` and `h` are the refresh path. There is still no services column: C2's dead-service check exists now, but it is wired into `doctor` only, and `th ls`/the TUI grid deliberately run no per-worktree network fan-out — the same bargain migrations make. A dial is cheap enough that the column is viable, and it is a decision to take on its own merits rather than one that happens by default. `--json` and the text tables remain the first-class outputs for hooks and agents; the TUI is unreachable from them by construction.

## Design decision — Progressive configuration (Bryan's call, 2026-07-14) 🎯

**Zero-config mode is the default.** With no `treehouse.toml`, doctor still works end to end: required env keys inferred from `.env.example` (reported as WARN, not FAIL — inferred requirements get softer teeth), services inferred from the `PORT` keys the `.env` files already declare and dialled as WARN rows, git staleness needs nothing. Useful in any repo ten seconds after install.

**`treehouse.toml` is a sharpener, not a gatekeeper.** It upgrades inferred warnings to curated failures — the human-judged `[env] required` list, and a `[[service]]` entry that must be up — and carries the un-inferable: seed commands, migration commands, a Docker psql prefix, the `[open]` command. `init` scaffolds it as commented reference; it deliberately does **not** generate rules from the inferences (see C5), because the inference is live on every run and a generated copy would go stale into a verdict.

**Zero-config covers A1 and A2 completely** (2026-07-26). The template name, the connection and the clone name all fall out of main's own `.env`, so a repo with no `treehouse.toml` still gets a database per worktree with its `.env` pointed at it. The whole additive schema is three keys, each genuinely non-inferable:

```toml
[database]
psql = "docker compose exec -T db psql"   # ONLY when Postgres isn't a local psql
[migrations]
status = "alembic current"                # exit code = "migrations pending"
dir    = "db/migrate"                     # only when the glob guesses wrong
[[seed]]
name = "ramp"
command = "python manage.py loaddata ramp"
[[service]]                               # sharpens an inferred WARN into a FAIL
name = "redis"
addr = "127.0.0.1:6379"
[open]
command = "cursor ."                      # what `th new` hands the worktree to
```

There is deliberately **no `[database] template`**: which database to clone is main's `.env`, and a second place to say it is a second place for it to be wrong.

Build-order consequence: env checker v1 reads `.env.example` directly; the config package moves later (arrives with data checks).

## Epic S — Secrets an agent cannot read (2026-08-11)

**S1. `th run` — the proxy.** As a Dev handing a worktree to an Agent, the agent
can run anything that needs the environment without ever seeing a value in it.

- AC: `th vault add <KEY>` moves the value to the keychain and leaves
  `KEY=th:KEY` in `.env`; `th run -- <cmd>` resolves it into the child; the
  wrapper stays transparent (streams live, exit code verbatim); output is
  scrubbed; a dangling reference stops the command instead of starting it empty.
- ✅ **Done (2026-08-11).** `th run` is `wrapCommand` with an environment, so it
  inherits the exit-code contract rather than copying it, and `check.Env` is the
  one env builder `th seed` and the migration-status command also use.

**The threat model is accidental exposure, and the README says so plainly.** A
`cat .env`, a `grep -r`, a stack trace, a file read into a context window to
answer an unrelated question — the secret lands in a transcript permanently, for
no benefit, because the agent never needed the *value*, it needed the command to
work. What treehouse deliberately does **not** claim is a defence against a
hostile process running as you: `/usr/bin/security` is the same binary for every
caller. Enforcing more needs a container with locked-down egress, which is a
different security boundary and one this project should not grow.

**Prior art (Infisical Agent Vault, read 2026-08-11).** Same idea one layer up:
their agent holds `__anthropic_api_key__` and a TLS-intercepting forward proxy
swaps in the real key on outbound HTTP, matched by host. Three things transfer —
the `run --` shape, dummy values standing in for secrets, and their own stated
limitation that "`HTTPS_PROXY` alone is insufficient; the network must be locked
down", which translates exactly to "`th run` alone is insufficient if the value
is still in `.env`". That is the argument for references over gating, from the
people who shipped the gating version.

What does **not** transfer is the network layer: `DATABASE_URL` speaks the
Postgres wire protocol and `REDIS_URL` speaks RESP, so an HTTP proxy cannot
touch either, and half a `.env` is not a secret at all. Agent Vault intercepts
the socket because its secret lives in the agent's process env; treehouse
intercepts the `exec` because its secret lives in a file. Their `mitm`, `ca`,
`netguard`, `oauth` and management UI are the wrong rung for a repo with no
daemon. The two compose: `th run -- <cmd>` inside `agent-vault run -- claude`.

**Why the placeholder goes in the file rather than gating access to it.**
Gating (`th run` injects, a hook denies reading `.env`) is walked around with
`python -c "open('.env')"`. With the value gone there is nothing to walk around
to — and `hydrate` needed **zero changes**, because it copies `.env` values
verbatim, so a reference copies as a reference and a new worktree is born
already pointed at the vault.

**Decisions worth keeping:**

- The reference match is **whole-value**, never a substring: `th:` appears
  inside `postgres://user:th:pass@host/db` and inside plenty of JWTs, and a
  substring rule would silently treat a real password as a pointer. Anchoring is
  also why there is no minimum-length floor — Agent Vault needs one because
  their placeholders are substituted *into* strings, and a floor of 4 here would
  misread `th:KEY`.
- Identity is `<main worktree path>:<KEY>`, the scheme the Postgres provenance
  comment already uses. **Repo-scoped, never branch-scoped**, so every worktree
  resolves the same value and there is nothing for `th gc` to chase — the same
  bargain E3's port registry and A4's seed marker make.
- Stored bytes are tagged base64. `security find-generic-password -w` switches
  to printing **hex** when the stored bytes are not all printable, with no flag
  to stop it and no way to tell that hex from a password that looks like hex: a
  value containing a tab round-tripped into a *different* value. Found by the
  test, not by the design.
- Redaction closes the leak the vault cannot: a program printing its own
  connection string. Longest match first, because a password is usually a
  substring of the `DATABASE_URL` embedding it. **Ceiling:** a value containing
  a newline survives the line boundary, because buffering the whole stream would
  stop output streaming, which is what a wrapper is for.
- **`th vault rm` does not put the value back.** That would mean having kept a
  copy. It says the worktree is now broken instead of quietly leaving it so.

**S2. The two guards A2 needed.** A `th:` reference is a pointer, and neither
half of the database repoint may treat it as a name.

- ✅ **Done (2026-08-11).** `EnvDB` returned `vars["POSTGRES_DB"]`
  unconditionally, so a vaulted key answered `th:POSTGRES_DB` to "which database
  does this worktree use" — and `Quote` accepts that string, so the cluster
  would have been asked to `CREATE DATABASE "th:POSTGRES_DB"` with nothing
  downstream to catch it. One guard in the shared reader, not one per caller.
  `repointDB` is the writer's half: a derived literal written over a reference
  orphans the keychain entry and destroys the only record of where the value
  lived, so it skips with a reason — never an error, the rule the exhausted-port
  case already follows.

**S4. The bypass is explained, not just discouraged.** Nothing forces an agent
through `th run`; the SessionStart line is a hint, and Epic S is honest that
gating is walked around. The design assumed the failure would at least be
legible. It was not: the app got the literal `th:STRIPE_SECRET`, and triage
answered `code` — actively wrong, and it sends somebody to debug an SDK over an
invocation mistake, with the natural repair being to paste the value back into
`.env` and undo the feature.

- ✅ **Done (2026-08-12).** One built-in signature, and a fourth area for it to
  cite. **The vault is the one area where PRESENCE corroborates rather than
  redness:** a `th:` string in a program's output is only possible when this
  worktree has references AND the command bypassed `th run`. Doctor cannot see
  the second half, but the signature that cites the area already did — so any
  vault row, healthy or broken, confirms it. `skip` counts here for the same
  reason, which is the one place in this codebase it does not read as "nobody
  asked".
- **It is deliberately absent from `areaOrder`.** A fact that is red in every
  vaulted worktree would otherwise attach itself to every unrelated verdict as
  a "possibly related" hint and a stray fix. It is reachable only by a signature
  that names it, and there is a test asserting a vaulted worktree still answers
  `code` for a `TypeError`.
- **It outranks `connection-refused`**, which is why it is first in the list: a
  program handed a pointer instead of a password usually also fails to connect,
  and that would be the less useful of the two answers.
- The match is shaped like the env key a reference is named after
  (`th:[A-Z][A-Z0-9_]{2,}`), the same discipline that keeps `missing-env` off an
  ordinary dict `KeyError` — and off a URL that merely starts with `th:`.
- `CheckSecrets` gained an **`ok`** row for a healthy vault, where it used to
  emit nothing. It earns the line twice: it tells a reader the vault is in use,
  and without a row there is no area for triage to cite.

**S3. Reporting.** `doctor` warns on an inferred secret in cleartext, fails on a
curated one, and fails on a reference that resolves to nothing.

- ✅ **Done (2026-08-11).** The same progressive-configuration tier the env and
  service checks use: a name heuristic infers and warns, `[secrets] keys`
  curates and fails. Nothing to say produces **no rows**, and a keychain that
  will not answer at all reports nothing rather than calling every reference
  dangling — "nobody could ask" is not "the secret is gone". The
  false-alarm side is unit-tested, because a WARN that fires on `PORT` in every
  repo is how a report teaches people to stop reading it.
- The `SessionStart` hook gains the line that makes any of this reachable, but
  **only in a worktree that has something vaulted**: an agent told to use
  `th run` in a repo with nothing vaulted has been handed a rule with no reason.
  The nine-line budget now reserves for it rather than overrunning.

---

## Non-goals (state in README)

- Multi-machine sync (the original "Dropbox for devs" — dead)
- Full secrets management → varlock / Agent Vault / Doppler / Infisical. **Narrowed 2026-08-11 (Epic S).** `th vault` covers exactly one thing those do not: keeping one laptop's `.env` values out of the files an agent reads. Team-wide rotation, sharing, audit logs and anything server-side stay out of scope, and the README says so.
- Port proxying & subdomain routing → portree / dockportless
- IDE/editor state management (pain 10)
- Moving uncommitted WIP between worktrees (pain 11 — future `treehouse move`, not v1)
- Build-cache sharing (pain 12)
- Docker volume permission edge cases
- Being a seeding/migration framework — treehouse wraps _your_ commands

---
