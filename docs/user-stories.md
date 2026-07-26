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

## Build status — 2026-07-20

Commands shipped: `doctor`, `hydrate`, `make`, `init`. (`th` is an alias for the `treehouse` binary.)

| Status | Stories |
| --- | --- |
| ✅ **Done** | **E1** (instant deps), **C1** (hydrate fills `.env`) |
| 🟡 **Partial** | **C2** (doctor: env drift + `--ls` only — no services/db/seed/stale, no `--json`/`--quiet`/exit codes), **C5** (`init` scaffold — no `.env.example`/compose scan) |
| ⬜ **Not started** | **L1–L4** (new/ls/rm/cd), **A1–A6** (db-per-worktree), **B1–B3** (triage/why), **C3** (snapshot), **C4** (SessionStart hook), **E2** (compose namespace), **E3** (port offsets), **T1** (TUI) |

Foundations in place that unblock the above: `Discover`, `MainWorktree`, `EnvVarsByDir` (worktree/env model), the plan-then-apply pattern (`Finding`/`Repair`/`DepPlan`), and `treehouse.toml` config parsing.

**Pain coverage so far:** pain 1 (missing `.env`) via C1/hydrate; pains 2 & 9 (deps reinstall + disk) via E1. Pains 3–8, 10–13 still open.

---

## Epic L — Lifecycle: the manager commands 🎯

**L1. `treehouse new <branch>` — born ready.** As a Dev, one command gives me a worktree that is immediately workable: fetches origin, cuts the worktree from _fresh_ base (kills pain 6 at the root), then runs the full hydrate pipeline — env fill from canonical, db clone + `DATABASE_URL` pointing, CoW deps, compose/port namespacing, seed steps — and finishes with a doctor report.

- AC: single command, ends with green doctor or a clear list of what's still red; `--from <ref>` overrides base; helpful error when the branch is already checked out elsewhere (pain 7); optional `open` hook (editor/agent command) after green.

**L2. `treehouse ls` — one table, everything.** As a Dev with 4 worktrees, I see worktree × branch × {env, services, db, seed, behind-main, dirty} at a glance, so I spot the broken one before assigning an agent to it.

- AC: `--json` for tooling; current worktree highlighted; state columns reuse doctor (no second implementation). (Absorbs the old "fleet view" epic. `wt`/`wtdb status` show git/db columns only — the state columns are the differentiator.)

**L3. `treehouse rm <branch>` — remove without corpses.** As a Dev, removing a worktree also drops its db clone and its compose project, so nothing accumulates.

- AC: refuses when dirty/unpushed unless `--force`; `treehouse rm --merged` sweeps every worktree whose branch is merged (pain 8, agent-corpse cleanup).

**L4. Shell niceties ➕.** `treehouse cd <branch>` jump with completion, like wtp.

---

## Epic A — A database per worktree

**A1. Isolated db on hydrate. 🧱** As a Dev with 3 worktrees, each gets its own Postgres database cloned from the shared dev db (`CREATE DATABASE app_wt_<slug> TEMPLATE app_dev`), so one branch's migration can never break the others.

- AC: near-instant (template copy); branch names with `/` slugged; re-running hydrate reuses the existing clone.
- Prior art (verified 2026-07-02): **wtdb** does clone + `DATABASE_URL` rewrite (Postgres-only, creation-time, copies env verbatim — no validation/repair). A1+A2 = table stakes to match; decision: absorb (reimplement, ~30 lines), don't depend.

**A2. Env points at my clone automatically. 🧱** The worktree's `.env` is rewritten (`POSTGRES_DB`/`DATABASE_URL`) to the clone — _after_ canonical repair, so we never point a broken env at a fresh db.

- AC: `doctor` fails loudly if `per_worktree = true` but `.env` targets the shared db.

**A3. Migration-state awareness. 🎯** `doctor` says "db exists but 2 migrations behind this branch" (or "ahead of main — expected"), with the fix command.

- AC: configurable `migration_status` cmd (alembic/django/prisma-agnostic); verdicts: behind / ahead / diverged.

**A4. Named re-seed per worktree. 🎯** Re-seed _my_ clone with a named dataset ("ramp", "sondermind") without remembering the incantation; `doctor` shows which datasets are present.

- AC: seed steps run against the worktree's db; a check query reports loaded datasets by name.

**A5. Clone garbage collection.** `treehouse gc` lists db clones whose worktrees are gone and drops them after confirmation. (L3 prevents; gc cures.)

**A6. Redis isolation ➕ (stretch).** Each worktree gets its own Redis logical db (`redis://localhost:6379/<n>`), so one worktree's cache flush doesn't nuke another's session.

---

## Epic B — Failure triage: "environment or code?" 🎯

**B1. Structured verdict on failure.** As an Agent, when a command fails, `treehouse triage -- <cmd>` correlates the failure output with doctor state and returns `{cause: environment|code|unknown, evidence, fixes}` — so I repair the environment instead of debugging phantom code bugs.

- AC: default signature map (`connection refused :PORT` → service; `relation does not exist` → migration/seed; `KeyError`/undefined env → env); repo config adds custom signatures.

**B2. Automatic verdict injection.** A Claude Code `PostToolUse` hook feeds the triage verdict to the agent whenever a Bash command fails — no more agents doing "a whole bunch of nothing" for 20 minutes because Redis was down.

- AC: copy-paste hook in README; quiet output ≤ 10 lines; **silent when verdict is `code`** (don't spam the agent).

**B3. Human one-liner.** `treehouse why` answers in one line what changed since everything was last green.

- AC: state journal records last-green per check; `why` diffs current vs last-green.

---

## Epic C — Core doctor/hydrator 🟡 (partial — status corrected 2026-07-20 to match code)

**C1.** `hydrate` fills `.env` from canonical without overwriting local values. ✅ **Done** — append-only writes from the main worktree; no backup needed since it never overwrites (present-but-empty keys deferred to v2).
**C2.** `doctor` reports missing/empty required keys, dead services, unseeded data, stale base — each with a fix line; `--json`, `--quiet`, exit codes. 🟡 **Partial** — env-key drift + `--ls` table shipped (and it now falls back to the main worktree's `.env` when no `.env.example`). Still missing: dead-service/unseeded/stale-base checks, fix lines, `--json`, `--quiet`, non-zero exit codes.
**C3.** `snapshot` captures the current working `.env` as canonical. ⬜ **Not started** — no `snapshot` command exists.
**C4.** `SessionStart` hook: agent starts with env state in context. ⬜ **Not started** — no treehouse hook exists.
**C5.** `init` scans `.env.example` + docker-compose and generates `treehouse.toml`. 🟡 **Partial** — `init` writes a commented `treehouse.toml` scaffold; it does **not** yet scan `.env.example`/docker-compose to pre-populate rules.

**Also shipped, not in the original stories:** `make` — generates `.env.example` from each service's `.env` (values blanked), with a main-worktree fallback for empty worktrees.

---

## Epic E — Runtime isolation & fast setup 🎯

**E1. Instant dependencies.** ✅ **Done (2026-07-20).** `hydrate` clones declared heavy dirs (`node_modules`, …) from the main checkout via copy-on-write (`cp -c` on APFS) — instant, near-zero disk, isolated. Python `.venv` is _recreated_, never copied (absolute paths): `uv venv && uv sync`, command resolved from the manifest, reports if uv/manifest is missing. Rules are built-in defaults (node/python) extended by `treehouse.toml [[deps]]` — the agent extension point. `--skip-deps` opts out.

- Evidence: 5 × 2GB node_modules = 10GB; ~10GB burned in 20 min of agent worktrees (reported).
- Shipped as: `internal/check/deps.go` (planner), `internal/deps` (CoW doers), `internal/config` (toml), wired into `cmd/hydrate.go`. Unit + E2E tested.

**E2. Compose namespace per worktree.** `hydrate` writes `COMPOSE_PROJECT_NAME=<app>_<slug>` into each worktree's `.env` — containers/networks/volumes never clobber across worktrees.

**E3. Cheap port offsets.** `hydrate` assigns a deterministic per-worktree port offset into `.env` (`PORT=3001`, `3002`…). Full proxy/subdomain routing stays punted to portree.

---

## Epic T — Live TUI dashboard (Bryan's call, 2026-07-13) 🎯

**T1. Health board.** Running `treehouse` with no args opens a bubbletea TUI: worktrees × {env, services, db, git} as a live grid, checks streaming in concurrently with spinners, drill into any worktree for doctor detail, `h` triggers hydrate and cells flip green in place.

- Stack: bubbletea + lipgloss + bubbles (Charm). The TUI is a _renderer over `[]Result`_ — checkers are unchanged; text/JSON outputs remain first-class (hooks/agents need them).
- Sequencing rule: built AFTER the plain CLI core works (sessions 4–5, lipgloss styling as the session-3 bridge). The TUI is the roof, not the foundation.
- Differentiation: nothing in the space has a live health dashboard (workz = static table). This is the README GIF.

## Design decision — Progressive configuration (Bryan's call, 2026-07-14) 🎯

**Zero-config mode is the default.** With no `treehouse.toml`, doctor still works end to end: required env keys inferred from `.env.example` (reported as WARN, not FAIL — inferred requirements get softer teeth), services inferred from docker-compose, git staleness needs nothing. Useful in any repo ten seconds after install.

**`treehouse.toml` is a sharpener, not a gatekeeper.** It upgrades inferred warnings to curated failures (the human-judged required list) and carries the un-inferable: data/seed checks, fix commands, hydrate steps, migration commands. `init` bridges the two — generates the config from the inferences as a starting point.

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
