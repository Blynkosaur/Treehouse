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

## Build status — 2026-07-26

Commands shipped: `doctor`, `hydrate`, `make`, `init`, `new`, `ls`, `rm`, `gc`, `seed`, `triage`, `hook session`. (`th` is an alias for the `treehouse` binary.)

| Status | Stories |
| --- | --- |
| ✅ **Done** | **E1** (instant deps), **C1** (hydrate fills `.env`), **E2** (compose namespace), **E3** (port offsets), **A1** (db clone), **A2** (`.env` points at it, doctor fails when it doesn't), **A4** (named re-seed), **A5** (`gc`), **L1** (`new`), **L2** (`ls`), **L3** (`rm`, teardown included), **B1** (`triage`, three modes) |
| 🟡 **Partial** | **C2** (doctor: env drift, db/migration/seed checks, `--ls`, `--json` schema 2, `--quiet`, exit codes 0/1/2, curated `[env] required` — no dead-service or stale-base checks), **A3** (migration state, with `diverged` cut from the AC — see below), **C5** (`init` scaffold — no `.env.example`/compose scan), **B2** + **C4** (both hooks built and tested; the Claude Code wiring is unverified — see B2) |
| ⬜ **Not started** | **L4** (`cd`), **A6** (redis), **C3** (snapshot), **T1** (TUI) |
| ✂️ **Deferred** | **B3** (`why` — needs the one state file this project has refused everywhere else; see B3) |

Foundations in place that unblock the above: `Discover`, `MainWorktree`, `Worktrees`/`Ref` (one porcelain parser), `EnvVarsByDir`, `Slug` (collision-safe branch → identifier), `Status` (one worktree, no I/O — the row a TUI renders), the plan-then-apply pattern (`Finding`/`Repair`/`DepPlan`/`DBPlan`/`DBDrop`), `Check` (the non-env verdict beside `Finding`), `Triage` (the same correlation, pure), and `treehouse.toml` config parsing with one generic name-keyed `Merge` — now serving the three lists it was made generic for (`[[deps]]`, `[[seed]]`, `[[signature]]`).

**Pain coverage so far:** pain 1 (missing `.env`) via C1/hydrate; pains 2 & 9 (deps reinstall + disk) via E1; **pain 3 (shared database, colliding migrations) via A1/A2/A3/A4**; pain 4 (port fights) via E3; pain 5 (compose collisions) via E2; pain 6 (stale base) via L1's fetch-then-cut; pain 7 (worktree confusion) via L2; **pain 8 (cruft) via L3 + A5**; pain 13's agent half via B1/B2 (an agent no longer debugs phantom code bugs caused by a broken environment). Pains 10–12 still open.

---

## Epic L — Lifecycle: the manager commands 🎯

**L1. `treehouse new <branch>` — born ready.** As a Dev, one command gives me a worktree that is immediately workable: fetches origin, cuts the worktree from _fresh_ base (kills pain 6 at the root), then runs the full hydrate pipeline — env fill from canonical, db clone + `DATABASE_URL` pointing, CoW deps, compose/port namespacing, seed steps — and finishes with a doctor report.

- AC: single command, ends with green doctor or a clear list of what's still red; `--from <ref>` overrides base; helpful error when the branch is already checked out elsewhere (pain 7); optional `open` hook (editor/agent command) after green.
- ✅ **Done (2026-07-26).** `th new <branch>` places the worktree as a **sibling** of the main checkout (inside would poison `Discover`/`findDepDirs`), resolves local → remote-tracking → new-branch-from-`origin/HEAD`, warns instead of failing when `git fetch` can't reach origin, then runs the same `hydrate` pipeline and prints the doctor report. Dep failures are red lines, not aborts. Still open: the db clone (Epic A), seed steps, and the `open` hook.

**L2. `treehouse ls` — one table, everything.** As a Dev with 4 worktrees, I see worktree × branch × {env, services, db, seed, behind-main, dirty} at a glance, so I spot the broken one before assigning an agent to it.

- AC: `--json` for tooling; current worktree highlighted; state columns reuse doctor (no second implementation). (Absorbs the old "fleet view" epic. `wt`/`wtdb status` show git/db columns only — the state columns are the differentiator.)
- ✅ **Done (2026-07-26).** `th ls` shows worktree × branch × env × behind-main × dirty, current row marked, `--json` in the same envelope doctor uses. The row is computed by `check.Doctor.Status` — one worktree, no I/O — so T1's TUI streams rows instead of reimplementing them. Services/db/seed columns arrive with their epics.

**L3. `treehouse rm <branch>` — remove without corpses.** As a Dev, removing a worktree also drops its db clone and its compose project, so nothing accumulates.

- AC: refuses when dirty/unpushed unless `--force`; ~~`treehouse rm --merged` sweeps every worktree whose branch is merged~~.
- ✅ **Done (2026-07-26)**. `th rm <branch>` refuses dirty or not-on-any-remote work without `--force`, and **flatly refuses the worktree you're standing in** even with it. It now drops that worktree's database clone through A5's own plan — same ownership rule (a provenance comment naming this repo), same refusal when connections are live, silent when there is no clone. Only the removed branch's clone: `th rm feat/a` deleting feat/b's database would be a surprise. Compose teardown is still open (a stopped project costs nothing but disk, unlike a database).
- **Cut: `--merged`.** `git branch --merged` cannot see a squash-merged branch, so the sweep would silently skip exactly the branches most teams produce. A cleanup you can't trust is worse than none; `th rm` stays explicit.

**L4. Shell niceties ➕.** `treehouse cd <branch>` jump with completion, like wtp.

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

**A5. Clone garbage collection.** `treehouse gc` lists db clones whose worktrees are gone and drops them after confirmation. (L3 prevents; gc cures.)

- ✅ **Done (2026-07-26).** **Ownership is by provenance comment, never by name prefix** — a prefix can match somebody's real database, and `Slug` is one-way, so a name can't be reversed to the branch a human needs to see before approving a drop. Anything without a `treehouse:<mainWorktreePath>:<branch>` comment naming THIS repo is not a candidate, full stop. Liveness is checked two ways and either one spares a database: the branch (the honest test) and the derived name (the belt, so renaming the shared database can't turn the whole live fleet into candidates). The template is name-checked as well. A database with **open connections is reported and kept** — dropping out from under a running process creates exactly the corpse gc exists to remove. List-then-confirm by default, `-y` for scripts, `--json` in the same envelope. `check.PlanGC` is the pure decision; `cmd/gc.go` does the psql and the prompt.

**A6. Redis isolation ➕ (stretch).** Each worktree gets its own Redis logical db (`redis://localhost:6379/<n>`), so one worktree's cache flush doesn't nuke another's session.

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

- **Honest gap: `connection refused` cannot reach `environment`.** `Needs: env` maps onto `CheckEnv` and `db`/`migration` map onto the `Check` list — all real. **`service` maps onto nothing**, because C2's dead-service check does not exist. So the first default signature degrades to regex-only evidence with cause `unknown`, and says so in the evidence line. Not faked (dialling the port from triage would be a new check smuggled in through the back door); marked `ponytail:` in `internal/check/triage.go`, and it fills itself in when C2's service check lands.
- **Exit codes:** the wrapper passes the **wrapped command's code through verbatim** — `time`/`env`/`nice` all do, and `th triage -- pytest` has to keep failing a Makefile — so its verdict goes to **stderr**, where it cannot corrupt a piped stdout. `--stdin`/`--hook` use the existing 0/2. No fourth code was added: `environment` is a verdict about the worktree, which is what doctor's 2 already means.

**B2. Automatic verdict injection.** A Claude Code `PostToolUse` hook feeds the triage verdict to the agent whenever a Bash command fails — no more agents doing "a whole bunch of nothing" for 20 minutes because Redis was down.

- AC: copy-paste hook in README; quiet output ≤ 10 lines; **silent when verdict is `code`** (don't spam the agent).
- 🟡 **Code done, wiring unverified (2026-07-26).** `th triage --hook` reads the payload, renders ≤10 lines of `additionalContext`, and ships with a `PostToolUse` fixture (`cmd/testdata/posttooluse.json`) so it is testable without Claude Code in the loop.
- **The AC as written is not implementable, and the design absorbs it.** "Whenever a Bash command fails" assumes the hook can tell. It cannot: a Bash `tool_response` carries only `stdout`, `stderr` and `interrupted` — **no exit code**. (Verified against first-party shipping hook code, which infers failure by regex over the output text for exactly this reason. Same source corrected the stdin field: it is `tool_response`, not the `tool_result` a local SKILL.md claims — the wrong name ships a hook that silently never fires.) The resolution is that **the signature map IS the failure detector**: run on every Bash `PostToolUse`, exit 0 in silence when nothing matches. That collapses the AC's "silent when the verdict is `code`" into the same code path, with no extra machinery — a passing command matches nothing, and neither does a code bug.
- **A hook never re-runs the command.** It would re-run `git push`, `rm`, a migration — and it is unnecessary, since the output is already in the payload. There is a test asserting it.
- **Unverified, in the README and worth checking before trusting it:** whether `PostToolUse` fires at all when a Bash call _errors_ (if not, B2 is dead as designed and must move to a `UserPromptSubmit` shape — **check this first**); whether `additionalContext` reaches the model or only the transcript; the exact `.claude/settings.json` nesting; and whether exit 2 is read as added context or as a blocked tool call. The README carries the block, marked unverified, plus the verification recipe.

**B3. Human one-liner.** `treehouse why` answers in one line what changed since everything was last green.

- AC: state journal records last-green per check; `why` diffs current vs last-green.
- ⬜ **Deferred (2026-07-26) — cut from phase 4, not from the product.** It needs a last-green state journal, and that is the one piece of new persistence this project has refused everywhere else on principle: the port registry is the sibling `.env` files, the seed marker is a table inside the database that gets dropped with it. Both were designed specifically so there is nothing for `th gc` to chase. A journal reintroduces exactly that — a file that goes stale, that lies after a manual fix, and that nothing cleans up. Revisit only with an answer for where it lives and who deletes it.

---

## Epic C — Core doctor/hydrator 🟡 (partial — status corrected 2026-07-20 to match code)

**C1.** `hydrate` fills `.env` from canonical without overwriting local values. ✅ **Done** — append-only writes from the main worktree; no backup needed since it never overwrites (present-but-empty keys deferred to v2).
**C2.** `doctor` reports missing/empty required keys, dead services, unseeded data, stale base — each with a fix line; `--json`, `--quiet`, exit codes. 🟡 **Partial** — env-key drift, the database check, `--db` migration and seed checks, `--ls` table, main-worktree fallback, `--json` (an object envelope: `schema`/`root`/`status`/`findings`/`checks`, **schema 2**), `--quiet`, and exit codes 0/1/2 shipped. Two things now fire the FAIL tier: `[env] required` and a worktree whose `.env` targets the shared database while its clone exists. Still missing: dead-service and stale-base checks.

  **Why `Check` is a sibling of `Finding`, not a wider `Finding`:** a `Finding` is shaped around env keys (`Missing`/`Empty`/`NoEnv`/`Keys`). Database, migration and seed results share none of that shape, and widening the struct would give every env row a pile of nil db fields to carry and every consumer a pile to skip. So the envelope carries two flat lists — `{schema: 2, root, status, findings: […], checks: […]}` — each with its own shape. The version bumped because that is what the field is for: a consumer reading `findings` for the whole story is wrong now, and should be told at the envelope rather than by silently missing the database row.
**C3.** `snapshot` captures the current working `.env` as canonical. ⬜ **Not started** — no `snapshot` command exists.
**C4.** `SessionStart` hook: agent starts with env state in context. 🟡 **Code done, wiring unverified** — `th hook session` emits this worktree's env and database state as `additionalContext`, capped at eight lines because it is prepended to a context window, not printed as a report. A green worktree costs one line plus a pointer to `th triage`. It deliberately does **not** filter on the `source` value (`startup`/`resume`/`clear`/`compact`): which of those are worth spending context on is unverified, and the settings.json matcher is where a human can see and change that decision. See B2 for the rest of the unverified list.
**C5.** `init` scans `.env.example` + docker-compose and generates `treehouse.toml`. 🟡 **Partial** — `init` writes a commented `treehouse.toml` scaffold; it does **not** yet scan `.env.example`/docker-compose to pre-populate rules.

**Also shipped, not in the original stories:** `make` — generates `.env.example` from each service's `.env` (values blanked), with a main-worktree fallback for empty worktrees.

---

## Epic E — Runtime isolation & fast setup 🎯

**E1. Instant dependencies.** ✅ **Done (2026-07-20).** `hydrate` clones declared heavy dirs (`node_modules`, …) from the main checkout via copy-on-write (`cp -c` on APFS) — instant, near-zero disk, isolated. Python `.venv` is _recreated_, never copied (absolute paths): `uv venv && uv sync`, command resolved from the manifest, reports if uv/manifest is missing. Rules are built-in defaults (node/python) extended by `treehouse.toml [[deps]]` — the agent extension point. `--skip-deps` opts out.

- Evidence: 5 × 2GB node_modules = 10GB; ~10GB burned in 20 min of agent worktrees (reported).
- Shipped as: `internal/check/deps.go` (planner), `internal/deps` (CoW doers), `internal/config` (toml), wired into `cmd/hydrate.go`. Unit + E2E tested.

**E2. Compose namespace per worktree.** ✅ **Done (2026-07-26).** `hydrate` writes `COMPOSE_PROJECT_NAME=<app>_<slug>` into the `.env` of every directory that actually holds a compose file — a repo with no compose file gets no key anywhere. `<app>` is main's own `COMPOSE_PROJECT_NAME` if it has one, else Compose's default rule (the main checkout's directory name).

**E3. Cheap port offsets.** ✅ **Done (2026-07-26).** `hydrate` shifts every `PORT`/`*_PORT` key main declares by one offset derived from the branch slug, checked against the ports every sibling worktree declares. One offset for all services, so inter-service spacing survives; same branch → same ports, because the registry is the sibling `.env` files and there is no state file to garbage-collect. Ceilings, on purpose: detection is by key name (`SERVER_ADDR=:3000` is invisible), "free" means undeclared rather than unbound on the host, and a compose file's `ports:` host mapping is **not** rewritten — parameterize it. Full proxy/subdomain routing stays punted to portree.

---

## Epic T — Live TUI dashboard (Bryan's call, 2026-07-13) 🎯

**T1. Health board.** Running `treehouse` with no args opens a bubbletea TUI: worktrees × {env, services, db, git} as a live grid, checks streaming in concurrently with spinners, drill into any worktree for doctor detail, `h` triggers hydrate and cells flip green in place.

- Stack: bubbletea + lipgloss + bubbles (Charm). The TUI is a _renderer over `[]Result`_ — checkers are unchanged; text/JSON outputs remain first-class (hooks/agents need them).
- Sequencing rule: built AFTER the plain CLI core works (sessions 4–5, lipgloss styling as the session-3 bridge). The TUI is the roof, not the foundation.
- Differentiation: nothing in the space has a live health dashboard (workz = static table). This is the README GIF.

## Design decision — Progressive configuration (Bryan's call, 2026-07-14) 🎯

**Zero-config mode is the default.** With no `treehouse.toml`, doctor still works end to end: required env keys inferred from `.env.example` (reported as WARN, not FAIL — inferred requirements get softer teeth), services inferred from docker-compose, git staleness needs nothing. Useful in any repo ten seconds after install.

**`treehouse.toml` is a sharpener, not a gatekeeper.** It upgrades inferred warnings to curated failures (the human-judged required list) and carries the un-inferable: seed commands, migration commands, a Docker psql prefix. `init` bridges the two — generates the config from the inferences as a starting point.

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
```

There is deliberately **no `[database] template`**: which database to clone is main's `.env`, and a second place to say it is a second place for it to be wrong.

Build-order consequence: env checker v1 reads `.env.example` directly; the config package moves later (arrives with data checks).

## Non-goals (state in README)

- Multi-machine sync (the original "Dropbox for devs" — dead)
- Secrets vaulting → varlock / Agent Vault / Doppler
- Port proxying & subdomain routing → portree / dockportless
- IDE/editor state management (pain 10)
- Moving uncommitted WIP between worktrees (pain 11 — future `treehouse move`, not v1)
- Build-cache sharing (pain 12)
- Docker volume permission edge cases
- Being a seeding/migration framework — treehouse wraps _your_ commands

---
