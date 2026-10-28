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

`git worktree add` gives you a directory, not a working environment. treehouse
makes worktrees that are **born ready**: env filled, own database, deps present,
ports and compose project namespaced, secrets out of the files an agent reads.

## Install

```sh
go install github.com/Blynkosaur/treehouse@latest
alias th=treehouse    # in .bashrc / .zshrc / config.fish
```

macOS and Linux. Copy-on-write deps need APFS (falls back to a plain copy).
The secret vault needs the macOS keychain (`th run` refuses elsewhere).

## Quickstart

```sh
cd ~/code/my-app
th new feat/login     # cut the worktree, fill it, clone its database, report
tcd feat/login        # jump to it (see below)
th doctor             # what is wrong right now
th ls                 # the whole fleet, one row each
th rm feat/login      # worktree, branch, database and compose project, together
```

No config needed. `treehouse.toml` (`th init`) only sharpens warnings into failures.

## What a new worktree gets

| Problem | Fix |
| --- | --- |
| `.env` is gitignored, so it is missing | Copied from the main checkout; missing/empty keys reported |
| One shared database | Postgres `TEMPLATE` clone per worktree, `DATABASE_URL` repointed |
| Port and compose collisions | Deterministic `*_PORT` offset, own `COMPOSE_PROJECT_NAME` and Redis db |
| `node_modules` per worktree | Copy-on-write clone (`cp -c`); `.venv` rebuilt |
| Secrets in cleartext | Moved to the keychain; `.env` keeps a `th:` reference |
| No idea which worktree is broken | `th doctor`, `th ls`, and a live dashboard (`th`) |

## Commands

| Command | What it does |
| --- | --- |
| `th` | Live dashboard on a terminal; help text anywhere else |
| `th new <branch>` | Create and hydrate a worktree, then run your `[open]` command |
| `th hydrate` | Re-fill env, deps and database for this worktree |
| `th doctor` | Health report: env, db, services, secrets, behind-main |
| `th ls` | One row per worktree |
| `th why` | What changed since it was last green |
| `th run -- <cmd>` | Run with vaulted secrets resolved and scrubbed from output |
| `th vault add\|ls\|rm` | Move a secret into the keychain |
| `th triage -- <cmd>` | Run a command; say if a failure is the environment or the code |
| `th rm <branch>` | Remove worktree, branch, db clone and compose project |
| `th gc` | Drop db clones whose worktrees are gone |
| `th seed <name>` | Load a named dataset into this worktree's db |
| `th path <branch>` | Print a worktree's path |
| `th init` / `th make` | Write `treehouse.toml` / `.env.example` |

`doctor` and `ls` exit 2 on a failure, so agents can gate on them without
parsing. `run` and `triage` pass the wrapped command's exit code through.

## Jumping between worktrees

A process cannot `cd` its parent shell, so wrap `th path`:

```sh
tcd() { cd "$(th path "$1")"; }            # bash / zsh
function tcd; cd (th path $argv[1]); end   # fish
```

## Using it with agents

Add to `CLAUDE.md`:

> When a command fails and you suspect the environment, re-run it as
> `th triage -- <cmd>` and read the verdict before changing any code.
> Run commands that need secrets as `th run -- <cmd>`.

And to `.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [
      { "matcher": "startup", "hooks": [{ "type": "command", "command": "th hook session" }] }
    ],
    "PostToolUse": [
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "th triage --hook" }] }
    ]
  }
}
```

`PostToolUse` does not fire on a failed Bash call, so the `CLAUDE.md` line does
most of the work. See [the hooks notes](docs/reference.md#claude-code-hooks-verified-2026-07-27).

## More

- [docs/reference.md](docs/reference.md): every feature in detail, config, exit codes, caveats
- [docs/user-stories.md](docs/user-stories.md): design decisions and what was cut
- [docs/dotenv-acceptance.md](docs/dotenv-acceptance.md): acceptance criteria, each pinned to a test

**Non-goals:** multi-machine sync, team secrets management (use
[Infisical](https://infisical.com) / [Doppler](https://doppler.com)), port
proxying, editor state, build-cache sharing.
