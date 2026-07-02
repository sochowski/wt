<div align="center">

# wt

**tmux + git worktrees for running coding agents in parallel.**

<img src="assets/wt-hero.png" alt="wt — run several coding agents side by side, each in its own git worktree and tmux session" width="720">

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
&nbsp;
![Shell: bash](https://img.shields.io/badge/shell-bash-1f425f.svg)
&nbsp;
![Requires: tmux](https://img.shields.io/badge/requires-tmux-1BB91F.svg)

**[Install](#install)** &nbsp;&bull;&nbsp;
**[Usage](#usage)** &nbsp;&bull;&nbsp;
**[Keybindings](#tmux-keybindings)** &nbsp;&bull;&nbsp;
**[MCP Profiles](#mcp-profiles)** &nbsp;&bull;&nbsp;
**[Example Setup](#example-setup)**

</div>

Each agent gets its own worktree and its own tmux session — agent, editor, and
shell windows — plus a live status you can see at a glance, so you always know
which one is working, which is waiting on you, and which is done.

It works with Claude, Codex, Gemini, and opencode: one session on Claude, the
next on Codex. The switcher lists whichever session you touched last first, so
the thing you're waiting on is never buried.

## What you get

- **A session per worktree.** `wt new` cuts the branch, adds the worktree, and
  opens a tmux session with the agent, editor, and shell windows already running.
- **Status at a glance.** Each session reports whether its agent is working,
  idle, waiting for input, or errored. `wt ls`, the fzf picker, and the tmux
  status bar all read the same live state.
- **PR badges.** `wt ls` and `wt pick` mark each worktree merged, open, draft, or
  closed, resolved from `gh` and cached.
- **Any agent, per session.** wt auto-detects which CLIs are installed and you
  choose one when you create the session.
- **Scoped MCP servers.** Worktree sessions get one set of MCP servers, the
  master session another, driven by profiles you keep in `~/.config/wt`.
- **Env files that follow the worktree.** A `.wt/sync` file copies or symlinks
  `.env` and friends into each new worktree, which git won't do for you.
- **Live diff in the editor.** nvim boots into a diffview of your branch against
  its base and refreshes as files change.
- **A master session** with its own MCP profile, for orchestrating work across
  the others.
- **Desktop notifications** when an agent needs your attention.

Runs on macOS and Linux.

## Install

```bash
git clone https://github.com/youruser/wt.git ~/src/wt
~/src/wt/install.sh
```

The installer will:
- Build the `wt-state` store binary (`state/` → `bin/wt-state`) and migrate any existing state
- Symlink scripts to `~/bin/`
- Set up tmux config at `~/.config/wt/tmux-wt.conf`
- Add tmux source line and status bar to `~/.tmux.conf`
- Merge hooks for detected agents:
  - Claude: `~/.claude/settings.json`
  - Gemini: `~/.gemini/settings.json`
  - Codex: `~/.codex/config.toml`
  - opencode: `~/.config/opencode/plugins/wt-status.js`

Requires: git, tmux, fzf, jq, go (to build `wt-state`), and at least one agent CLI ([Claude Code](https://github.com/anthropics/claude-code), [Codex](https://github.com/openai/codex), [Gemini CLI](https://github.com/google/gemini-cli), or [opencode](https://opencode.ai/))

Optional: [gh](https://cli.github.com/) (for PR lookup), [claudecode.nvim](https://github.com/anthropics/claudecode.nvim) (for Claude IDE integration), [diffview.nvim](https://github.com/sindrets/diffview.nvim) + a file watcher ([watchexec](https://github.com/watchexec/watchexec), [entr](https://eradman.com/entrproject/), or `inotifywait`) for the live diff view

## Usage

```bash
# Session management
wt new                                # Interactive: pick repo, branch, agent
wt new ~/code feature-x              # Direct: create worktree (background)
wt new ~/code feature-x --switch      # Create and switch to it
wt new ~/code feature-x --agent codex # Use specific agent
wt pick                               # fzf picker (? to toggle preview)
wt switch <session>                   # Switch to a session (alias: s)
wt ls                                 # List sessions with status
wt delete <session>                   # Delete session and worktree (alias: rm)
wt delete-pick                        # Interactive delete picker (fzf)

# Agent control
wt agent <session> [task]             # Start agent on a task in a session (alias: claude)

# Master session
wt master                             # Create/attach master session
wt master --force                     # Replace existing master
wt master --agent gemini              # Master with specific agent

# PR integration
wt pr 42                              # Find session for PR #42
wt pr-pick                            # Interactive PR picker (fzf + gh)

# Status
wt status <session>                   # Print a session's status
wt sync-tmux                          # Restore tmux options after restart
```

## Tmux Keybindings

| Key | Action |
|-----|--------|
| `prefix + W` | Session switcher (fzf popup, most-recently-active first) |
| `prefix + w` | Action menu (new, switch, delete, PR, master) |
| `prefix + M` | Jump to master orchestrator session |

## Status Icons

| Icon | Status | Description |
|------|--------|-------------|
| `●` | working | Agent is actively using tools (green) |
| `○` | idle | Agent finished, session ready (blue) |
| `⊘` | input | Agent is waiting for your input (yellow) |
| `✗` | error | Agent encountered an error (red) |
| `◆` | master | Master orchestrator session |

## PR Badges

`wt ls` and `wt pick` show a PR-state badge per worktree, resolved via `gh` from
the session's branch and cached in the state store. Requires `gh` installed and
authenticated; sessions with no associated PR show no badge.

| Icon | State | Color |
|------|-------|-------|
| `⬤` | merged | magenta |
| `◆` | open | green |
| `◇` | draft | gray |
| `⊘` | closed (not merged) | red |

Cached badges stay fresh for `WT_PR_TTL` seconds (default 900) before wt
re-queries `gh`.

## MCP Profiles

Sessions get scoped MCP servers from wt profiles in `~/.config/wt/mcp-profiles/`:

- **Worktree sessions**: copies `~/.config/wt/mcp-profiles/default.json` into the worktree as `.mcp.json` if the repo does not already have one.
- **Master session**: copies `~/.config/wt/mcp-profiles/master.json` into `~/worktrees/.master/.mcp.json`.
- **OpenCode sessions**: also generate an OpenCode-native config at `~/.config/wt/opencode-mcp/<session>.json` and launch OpenCode with `OPENCODE_CONFIG` pointing at it.

OpenCode does not read `.mcp.json` directly. wt converts Claude-style `mcpServers` profiles to OpenCode's `mcp` schema, mapping remote servers from `type: "http"` to `type: "remote"` and local command servers to `type: "local"` with a command array.

Create profiles:
```bash
# Default profile (all worktree sessions)
cat > ~/.config/wt/mcp-profiles/default.json << 'EOF'
{ "mcpServers": { "my-server": { "type": "http", "url": "https://..." } } }
EOF

# Master profile (orchestrator session)
cat > ~/.config/wt/mcp-profiles/master.json << 'EOF'
{ "mcpServers": { "my-server": { "type": "http", "url": "https://..." } } }
EOF
```

For OpenCode, wt generates the equivalent runtime config:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "my-server": {
      "type": "remote",
      "url": "https://..."
    }
  }
}
```

## Tool Permissions (Claude)

Seed every new worktree and master session with a default set of allowed tools
so Claude doesn't re-prompt for the same permissions in each session. Create
`~/.config/wt/claude-allowed-tools.json` as a JSON array of tool rules:

```json
["Read", "Edit", "Bash(npm run *)", "Bash(git status)"]
```

wt merges these into the session's `.claude/settings.local.json` on creation,
preserving any keys already present.

## Env File Sync

Worktrees don't inherit gitignored files like `.env`. Add a `.wt/sync` file to any repo to automatically copy or symlink env files into each new worktree on creation.

```
# .wt/sync
copy .env
copy .env.local
copy backend/.env
symlink webapp/.env
```

- `copy` copies the file out of the primary repo checkout, so each worktree gets its own isolated copy.
- `symlink` links back to the primary checkout, so the file stays in sync and edits affect the source.

Files missing from the primary repo are skipped with a warning, and files that already exist in the worktree are left untouched. `.wt/sync` holds no secrets, so it's safe to commit.

## Example Setup

One way to use the master session: point it at your customer-support queue and let it dispatch coding tasks to child worktrees. Here's how that setup fits together.

### MCP profiles

Remove all MCP servers from `~/.claude.json` (global config) so sessions only get what their profile provides:

```bash
# Clear global MCPs (keeps all other settings intact)
jq '.mcpServers = {}' ~/.claude.json > ~/.claude.json.tmp && mv ~/.claude.json.tmp ~/.claude.json
```

Child sessions get a code-tools MCP for code review and search:

```json
// ~/.config/wt/mcp-profiles/default.json
{
  "mcpServers": {
    "code-tools": {
      "type": "http",
      "url": "https://your-mcp-server.example.com/code-tools/mcp"
    }
  }
}
```

Master gets code-tools plus a customer support MCP:

```json
// ~/.config/wt/mcp-profiles/master.json
{
  "mcpServers": {
    "code-tools": {
      "type": "http",
      "url": "https://your-mcp-server.example.com/code-tools/mcp"
    },
    "support": {
      "type": "http",
      "url": "https://your-mcp-server.example.com/support/mcp"
    }
  }
}
```

### Master CLAUDE.md

The master session runs from `~/worktrees/.master/`. Place a `CLAUDE.md` there to give it context:

```markdown
# Master Orchestrator Session

You are the master orchestrator. Your jobs: manage worktree sessions and
handle customer support via the support MCP. Be concise.

## Dispatching Work

Create a worktree and send a task to the agent:
  wt new <repo> <branch> --start-agent --task "<task description>"

Workflow:
1. When work should be dispatched, ask the user what the task should be
2. Confirm the repo, branch name, and task description
3. Only dispatch after explicit approval
```

### Workflow

1. Open master: `wt master`
2. Master checks support queue via MCP, surfaces issues
3. You decide what needs a code fix: "create a session to fix the timeout bug in auth"
4. Master confirms: repo, branch name, task description
5. You approve, master runs: `wt new ~/workspace/myapp fix-auth-timeout --start-agent --task "Fix the 30s timeout..."`
6. Child session starts autonomously with full context
7. You monitor via `wt pick` or `prefix+W`

## Architecture

```
Agent hook event
  ├── wt-hook writes session state via wt-state (SQLite: ~/.local/state/wt/wt.db)
  ├── wt-hook writes tmux @wt-* session options
  └── wt-hook calls tmux refresh-client -S (instant redraw)

wt-state → Go binary, authoritative SQLite session store (status, agent, PR
           state, timestamps); tmux options are a fast-read mirror for the UI

prefix+W → fzf popup (wt switch) lists live sessions, most-recently-active first
prefix+w → display-menu launches wt subcommands in popups
Status bar → wt-tmux-status aggregates status counts from wt-state
```

## Files

| Path | Purpose |
|------|---------|
| `bin/wt` | Main CLI |
| `bin/wt-hook` | Agent hook handler |
| `bin/wt-tmux-status` | Tmux status bar segment |
| `bin/wt-agent-launch` | Multi-agent launcher with per-agent logic |
| `bin/wt-bind-menu` | Builds the `prefix+w` action menu from live sessions |
| `bin/wt-diff-watch` | Watches the worktree and refreshes the nvim diff view |
| `bin/wt-state` | Go+SQLite session store (built from `state/`, gitignored) |
| `bin/claude-ide` | Backward-compat wrapper for wt-agent-launch |
| `state/` | `wt-state` Go source (SQLite store + migrations) |
| `config/tmux-wt.conf` | Tmux keybindings and hooks |
| `config/wt-menu.conf` | `prefix+w` action-menu definition |
| `config/wt-diff.lua` | nvim config for the live diffview |
| `config/claude-hooks.json` | Claude Code hook configuration |
| `config/hooks-gemini.json` | Gemini CLI hook configuration |
| `config/opencode-wt-plugin.js` | opencode status plugin |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `WT_BASE_DIR` | `~/worktrees` | Base directory for worktrees |
| `WT_STATUS_DIR` | `~/.local/state/wt` | State directory (holds `wt.db`, logs, nvim sockets) |
| `WT_CONFIG_DIR` | `~/.config/wt` | Config directory (MCP profiles, allowed-tools) |
| `WT_DB` | `$WT_STATUS_DIR/wt.db` | Path to the SQLite state file |
| `WT_STATE` | `<bin>/wt-state` | Path to the `wt-state` binary |
| `WT_LOG_FILE` | `$WT_STATUS_DIR/wt.log` | Log file path |
| `WT_DEFAULT_AGENT` | `opencode` | Default agent CLI (opencode, claude, codex, gemini) |
| `WT_DIFF_VIEW` | `1` | Boot session nvim into the live diffview.nvim diff; set `0` to disable |
| `WT_NVIM_SOCK_DIR` | `$WT_STATUS_DIR/nvim-sockets` | Where per-session nvim RPC sockets live |
| `WT_FZF_VIM` | `0` | Set `1` for vim-style modal keybindings in the fzf pickers |
| `WT_PR_TTL` | `900` | Seconds a cached PR badge stays fresh before re-querying `gh` |

## License

GPL-3.0
