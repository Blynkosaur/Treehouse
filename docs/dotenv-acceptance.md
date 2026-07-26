# Dotenv vertical — acceptance criteria

Function-level acceptance criteria for the `.env` commands (`doctor`, `hydrate`,
`make`) and their shared plumbing. These are finer-grained than the product ACs
in [user-stories.md](./user-stories.md) (Epic C, L1, A2) — they map to code and
each is pinned to the test that proves it. Update this table when behavior changes.

## `MainWorktree(cwd)` — `internal/check/worktree.go`

Finds the repo's primary worktree (first `git worktree list --porcelain` entry).

| # | Criterion | Test |
|---|-----------|------|
| 1 | From a **linked** worktree's dir → returns the **main** worktree's path | `TestMainWorktree` |
| 2 | From the main worktree itself → returns the main worktree path | `TestMainWorktree` |
| 3 | Outside any git repo → non-nil error, no panic, no bogus path | `TestMainWorktree` |
| 4 | Returned path exists and is a directory | `TestMainWorktree` |

## `Worktree.EnvVarsByDir()` — `internal/check/worktree.go`

Indexes `.env` files by directory relative to the worktree root (the shared
primitive under doctor's fallback, hydrate's fill, and make's generation).

| # | Criterion | Test |
|---|-----------|------|
| 1 | Indexes only `.env`, never `.env.example` (example-only dir → no entry) | `TestEnvVarsByDir` |
| 2 | Keys are dirs relative to root (`svc_a`; `.` for root-level `.env`) | `TestEnvVarsByDir` |
| 3 | Value map equals the parsed vars (keys **and** values) | `TestEnvVarsByDir` |
| 4 | No `.env` files → empty, non-nil map | `TestEnvVarsByDirEmpty` |

## `doctor` reference resolution — `internal/check/doctor.go` (`CheckEnv`)

Expected keys come from a service's `.env.example`, else the main worktree's `.env`.

| # | Criterion | Test |
|---|-----------|------|
| 1 | With `.env.example` present, it is the reference (missing/empty keys reported) | `TestCheckEnv` |
| 2 | No source worktree → `.env.example`-only behavior; a `.env` with no example is skipped | `TestCheckEnv` |
| 3 | No `.env.example` → falls back to main's `.env` for the key set | `TestCheckEnvMainFallback` |
| 4 | Service present in main but `.env` absent here → flagged `NoEnv` | `TestCheckEnvMainFallback` |

## `hydrate` planning — `internal/check/hydrate.go` (`PlanHydrate`)

Plans append-only repairs for **missing** keys, valued from the source worktree.

| # | Criterion | Test |
|---|-----------|------|
| 1 | Missing key present in source → planned with the source's value | `TestPlanHydrate` |
| 2 | Missing key absent from source → planned empty, listed as `Unsourced` | `TestPlanHydrate` |
| 3 | Healthy service → no repair | `TestPlanHydrate` |
| 4 | `.env` absent entirely → `Create` repair (vs append) | `TestPlanHydrate` |
| 5 | Source lacks the service dir → every key `Unsourced` | `TestPlanHydrateNoSourceService` |

## `envfile.Set(path, vars)` — `internal/envfile/envfile.go`

Forces keys to a value: rewrites every matching line, appends the rest. A whole-file
rewrite, so it writes to a temp file and renames — `Append` can only damage the tail,
`Set` could lose an env a human hand-filled.

| # | Criterion | Test |
|---|-----------|------|
| 1 | Existing key rewritten in place; comments, order and other keys untouched | `TestSet/overwrite_preserves_comments_and_key_order` |
| 2 | **Every** duplicate declaration is rewritten, not just the last (which one wins is the loader's business) | `TestSet/every_duplicate_is_rewritten` |
| 3 | `# PORT=1` is not a match — the rule is `Parse`'s, character for character | `TestSet/commented-out_key_is_not_a_match` |
| 4 | `export PORT=3000` **is** a match — `Parse` reads it as `PORT`, so `Set` rewrites the line in place and keeps it exported (appending a duplicate would leave the app on the old value while doctor read the new one) | `TestSet/export_prefix_is_rewritten_in_place,_and_stays_exported` |
| 5 | New keys appended sorted, with **no** `Marker` block (a set is an override, not a hydrate) | `TestSet/absent_file_is_created` |
| 6 | Absent file created 0644; existing file's mode preserved | `TestSet/absent_file_is_created`, `TestSetPreservesMode` |
| 7 | No trailing newline and CRLF both survive; output always ends in `\n` | `TestSet/no_trailing_newline`, `TestSet/CRLF_line_endings_survive` |
| 8 | A second identical call produces a byte-identical file | asserted in every `TestSet` case |
| 9 | Values containing whitespace, `#` or quotes are quoted so `Parse` round-trips | `TestSet/value_needing_quotes_is_quoted` |
| 10 | `Set`, `Append` and `Create` write the **same bytes** for the same pair — hydrate appends a key that derive later `Set`s, and a password with a `#` must not depend on which phase wrote it | `TestWritersAgreeOnBytes` |

## `envfile.LoadPath(path)` — `internal/envfile/envfile.go`

| # | Criterion | Test |
|---|-----------|------|
| 1 | A file `Parse` can't scan (line over `bufio.Scanner`'s 64KB cap) → non-nil error, never a silent empty `File` | `TestLoadPathReportsParseError` |

## `Slug(branch)` + `PlanDerive` — `internal/check/derive.go`

The derived per-worktree identity: a private compose project (E2) and a private set
of ports (E3). Pure — cmd gathers the fleet and passes it in.

| # | Criterion | Test |
|---|-----------|------|
| 1 | `Slug` appends a hash when the mapping was lossy or truncated, so `feat/a-b` ≠ `feat-a-b` | `TestSlugCollision` |
| 2 | `Slug` output is a legal compose **and** Postgres identifier, ≤ 47 bytes | `TestSlugShape` |
| 3 | E2: every dir holding a compose file gets `COMPOSE_PROJECT_NAME=<app>_<slug>` | `TestPlanDeriveCompose` |
| 4 | E2: repo with no compose file → the key is written nowhere | `TestPlanDeriveNoComposeNoKey` |
| 5 | E3: port keys are `PORT`/`*_PORT` in **main's** `.env` whose value parses as 1024–65535 | `TestPlanDerivePorts` |
| 6 | E3: one offset shifts every service (inter-service spacing preserved) and the whole set is disjoint from every neighbour's declared ports | `TestPlanDerivePorts` |
| 7 | E3: same branch → same ports; a different branch → different ports | `TestPlanDeriveStable` |
| 8 | E3: no free offset → a `Repair` carrying a `Skip` reason, never an error (hydrate never fails over a port) | `TestPlanDerivePortsExhausted` |
| 9 | Derived repairs are `Overwrite` (rewrite existing lines), not append | `TestPlanDeriveCompose` |
| 10 | End to end: `th new` derives into a `.env` phase 1 only just created | `TestNew/born_ready` |

## `PlanGC` — `internal/check/db.go`

Picks the database clones whose worktrees are gone. Pure; `cmd/gc.go` does the
psql calls and the confirmation.

| # | Criterion | Test |
|---|-----------|------|
| 1 | A database with **no** treehouse provenance comment is never a candidate — a name prefix can match somebody's real database | `TestPlanGC/no_provenance_comment_at_all_—_never_a_candidate` |
| 2 | A clone commented for another repo is out of scope | `TestPlanGC/another_repo's_clone_is_out_of_scope` |
| 3 | A clone whose branch is still checked out is spared | `TestPlanGC/a_clone_whose_worktree_is_still_checked_out` |
| 4 | Renaming the shared database cannot turn the live fleet into candidates (branch spares them even when the derived name no longer matches) | `TestPlanGC/the_template_was_renamed_since_the_clones_were_cut` |
| 5 | The template is name-checked and spared even if it carries our comment | `TestPlanGC/the_template_is_spared…` |
| 6 | Each drop carries the ORIGINAL branch and the size — `Slug` is one-way, so the comment is the only way back to a name a human can approve | `TestPlanGCCarriesTheBranch` |
| 7 | `Provenance`/`ParseProvenance` round-trip, including a `:` in the path; everything else is rejected | `TestProvenanceRoundTrip`, `TestParseProvenanceRejects` |
| 8 | Live: `Drop` of a database in use returns `ErrBusy` — gc reports the sessions rather than killing them | `TestDropRefusesTheBusy` |

## `CheckDB` — `internal/check/checks.go`

A2's acceptance criterion: doctor must be loud when a clone exists and `.env`
still names the shared database.

| # | Criterion | Test |
|---|-----------|------|
| 1 | Clone exists + `.env` names the shared database → **fail** (exit 2) | `TestCheckDB/clone_exists_but_.env_still_names_the_shared_database` |
| 2 | Clone exists + `.env` names it → ok | `TestCheckDB/pointed_at_its_own_clone` |
| 3 | No clone yet → warn with a fix line | `TestCheckDB/no_clone_yet` |
| 4 | The main checkout is not a worktree missing a clone → ok | `TestCheckDB/the_main_checkout…` |
| 5 | `PlanDB` declined (detached HEAD, no database in the repo) → `skip`, never a silent gap | `TestCheckDB/the_planner_declined,_and_says_why` |
| 6 | A failing check fails the whole verdict; `skip` never moves it | `TestVerdict` |
| 7 | `ls`'s DB column is clone-exists/missing only, blank when Postgres was never asked, `shared` for main | `TestStatusDBColumn` |

## `CheckMigrations` / `CheckSeed` — `internal/check/data.go`

| # | Criterion | Test |
|---|-----------|------|
| 1 | No `[migrations] status` → `skip` with the config line to add, never a guess | `TestCheckMigrations/unconfigured…` |
| 2 | Exit 126/127 (typo, missing tool) is reported as unrunnable, **not** as pending | `TestCheckMigrations/a_command_that_could_not_run…` |
| 3 | Pending + this branch adds files → "your branch adds N" (the honest "ahead") | `TestCheckMigrations/pending_with_new_files…` |
| 4 | Pending + no new files → "main moved ahead" | `TestCheckMigrations/pending_with_no_new_files…` |
| 5 | Migrations dir inferred from the four conventions; `[migrations] dir` always wins | `TestMigrationsDir` |
| 6 | Seed datasets reported from the marker table in the worktree's own database | `TestCheckSeed`, `TestCloneRoundTrip` (live) |

## `make` command — `cmd/make.go`

Generates `.env.example` from each service's `.env`, keys copied, values blanked.

| # | Criterion | Test |
|---|-----------|------|
| 1 | Each service `.env` → sibling `.env.example`, same keys, **values blanked** (no secret leak) | `TestMake/generate_blanks_values` |
| 2 | Existing `.env.example` is never overwritten (skip reported) | `TestMake/skip_existing_example` |
| 3 | Empty worktree → falls back to main's `.env` files, generates from those | `TestMake/fallback_to_main_worktree` |
| 4 | No `.env` anywhere → honest "no .env files found" message, creates nothing | `TestMake/nothing_anywhere` |

---

Deliberate non-goals (documented in code): present-but-empty keys are doctor's
nag but not hydrated (v2, needs in-place edits); `make` is create-only and won't
sync new keys into an existing `.env.example` (v2 `--sync`); E3 governs
**app-process ports only** — a compose file's `ports:` host mapping is not
rewritten; port detection is by key name (`PORT`, `*_PORT`), so `SERVER_ADDR=:3000`
is invisible; "free" means *not declared in a sibling `.env`*, not *unbound on the
host* — a live probe would be racy, and determinism is what E3 exists for.

Newer ones, same spirit: migration state is the status command's **exit code**
plus a git diff, never a parse of alembic/Django/Prisma output (see A3 in
user-stories.md); the migrations dir is probed at the worktree root only, so a
monorepo sets `[migrations] dir`; `th seed` records datasets in a marker table
inside the worktree's own database, so a dataset loaded by hand outside
treehouse is invisible to doctor; `[database] psql` is split on whitespace, so a
prefix argument containing a space cannot be expressed.

CR-only (classic-Mac) line endings are **deliberately** not handled: `Parse`
can't read that format either, so the two sides are self-consistent, and
"fixing" one recreates a `Set`/`Parse` divergence a previous tester hunted down.
