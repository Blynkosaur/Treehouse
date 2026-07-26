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
| `th new <branch>` | Cuts a worktree beside the main checkout and runs the full hydrate pipeline, then prints a doctor report. `--from <ref>`, `--path <dir>`, `--skip-deps`. |
| `th hydrate` | Fills this worktree's `.env` files from the main checkout, provisions heavy dep dirs, then writes derived values. `--dry`, `--skip-deps`. |
| `th doctor` | Reports env drift per service. `--ls` table, `--json`, `--quiet`. |
| `th ls` | One table: every worktree × branch × env × behind-main × dirty. `--json`. |
| `th rm <branch>` | Removes a worktree and its branch. Refuses dirty or unpushed work without `--force`, and always refuses the worktree you're standing in. |
| `th make` | Generates `.env.example` from each service's `.env`, values blanked. |
| `th init` | Writes a commented `treehouse.toml` scaffold. |

Hydrate runs three phases in order — **fill canonical → provision deps → derive** —
so a derived value never points at a broken env. Dependency failures are reported
red but don't abort; env and git failures do.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | healthy, or warnings only |
| 1 | treehouse itself failed (usage, git, IO) |
| 2 | a curated required key is missing or empty |

Requirements inferred from `.env.example` are warnings. Failures come only from a
human-curated list:

```toml
# treehouse.toml, committed
[env]
required = ["DATABASE_URL"]
```

## Per-worktree isolation

Each worktree gets `COMPOSE_PROJECT_NAME=<app>_<slug>` in the `.env` of every
directory that actually holds a compose file, and a deterministic port offset
applied to every `PORT`/`*_PORT` key the main checkout declares. Same branch, same
ports, every run: the registry is the sibling `.env` files themselves, so there is
no state file to garbage-collect.

**Caveat:** this shifts the ports your app processes bind. A compose file's own
`ports: "3000:3000"` host mapping is **not** rewritten — parameterize it
(`"${PORT}:3000"`) if you run compose in more than one worktree at a time.
