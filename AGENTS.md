# Working on `wt`

`wt` is a tmux + git-worktree manager for multi-agent CLI workflows. You are
very likely running *inside* a `wt`-managed session right now. That is the core
hazard: **`wt` installs into `$HOME` and mutates live tmux/state, so testing a
change carelessly can clobber the very environment you're working in.**

Never run `./install.sh` or drive `wt`/`wt-state` against your real state to
test a change. Use one of the two isolated harnesses below.

## How the tool is laid out

- `bin/wt` — main CLI (bash). All paths are env-overridable with `$HOME` defaults:
  `WT_BASE_DIR` (worktrees), `WT_STATUS_DIR` (state), `WT_CONFIG_DIR`, `WT_LOG_FILE`,
  `WT_STATE` (path to the state binary), `WT_DEFAULT_AGENT`.
- `bin/wt-agent-launch`, `bin/wt-hook`, `bin/wt-bind-menu`, `bin/wt-tmux-status` — helpers.
- `state/` — Go `wt-state` binary: SQLite session store **and** the agent
  registry. `agents.go` is the single source of truth for each agent (binary,
  hook install format, launch style, per-session setup); the `agent`/`agents`
  subcommands expose it to the bash callers. Adding an agent = one entry there
  plus its hook template under `config/`. Honors `WT_STATUS_DIR` / `WT_DB`.
  Built into `bin/wt-state` (gitignored) by `install.sh`.
- `install.sh` — symlinks `bin/*` into `~/bin`, merges hooks into `~/.claude`,
  `~/.gemini`, `~/.codex`, and `~/.pi` via `wt-state agents install-hooks`, edits
  `~/.tmux.conf`, builds `wt-state`. Derives **everything** from `$HOME` — the
  basis of the staging harness below.
- `config/` — tmux config, menu config, agent hook templates (read by
  `install-hooks`; the OpenCode plugin and Pi extension stay live symlinks into
  the checkout).

## Testing changes e2e

Three tiers, cheapest first. Use the highest one that covers what you changed.

### 1. `./test.sh` — automated regression suite

Re-execs itself inside a private throwaway tmux server with isolated state; it
never touches your real sessions, `wt.db`, or worktrees. Run it after any change
and before opening a PR:

```bash
./test.sh
```

### 2. `source ./dev.sh` — interactive inner loop (behavior)

Runs `wt` straight from `./bin` with all state redirected to a throwaway dir and
a private tmux socket. No install, so it does **not** exercise `install.sh`,
hook merging, or tmux.conf integration. Best for iterating on `wt` subcommand
behavior:

```bash
source ./dev.sh        # exports WT_* into your shell, builds wt-state, seeds a demo repo
wt ls                  # runs from ./bin against the sandbox
wt new                 # ...
tmux -L wt-dev attach  # private server; keybindings not loaded (see tier 3)
wt-dev-reset           # wipe sandbox state
wt-dev-off             # restore your shell (undo PATH/env changes)
```

### 3. `./staging.sh` — full end-to-end (the whole install path)

Runs the **real `install.sh` under a throwaway `$HOME`**, then drops you into an
isolated tmux server. This is the only tier that tests symlinking, hook merge,
`.tmux.conf` integration, keybindings (`prefix + w`), and the `wt-state` build —
with zero contact with your real setup. Because `$STAGE/bin/wt` symlinks back to
this checkout, **edits here apply live; no reinstall between changes.**

```bash
./staging.sh           # install into fake HOME + attach to isolated tmux
# inside: wt new / wt pick / wt master, prefix+w menu — all for real, all disposable
./staging.sh --clean   # kill the server and remove the sandbox HOME
```

## Rules that keep testing safe

- **Never** point a test at your real `~/.local/state/wt`, `~/worktrees`, or
  `~/.tmux.conf`. Use the harnesses.
- A tmux **server's environment is sticky** — sessions inherit the env the
  server was *started* with. When launching a sandbox server, pin the `WT_*`
  paths explicitly (staging.sh does this) rather than relying on `$HOME` alone,
  or a stale server will silently write to the wrong paths.
- The sandboxes use a **stub agent** (`staging/stub-agent`) that shadows
  `claude`/`codex`/`gemini`/`opencode`/`pi`: `wt` drives it like a real agent but it
  launches no real CLI (no creds, no API calls). Keep it that way unless you're
  specifically testing real-agent launch.
- `wt-state` writes SQLite with WAL; concurrent writers are fine, but always let
  it own the DB path via `WT_STATUS_DIR`/`WT_DB` — don't edit `wt.db` by hand.
- Go's `GOPATH`/caches follow `$HOME`. Under a fake `$HOME`, pass your real
  `GOMODCACHE`/`GOCACHE` through (staging.sh does) so builds don't re-download
  and don't drop a read-only module cache in the sandbox.

## Before opening a PR

1. `./test.sh` passes.
2. Behavior changes verified via `source ./dev.sh`, or full-path changes
   (install/hooks/tmux/keybindings) verified via `./staging.sh`.
3. If you changed `state/`, `go build ./state` succeeds and `store_test.go` passes
   (`cd state && go test ./...`).
