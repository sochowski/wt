# wt

Worktree manager for multi-agent CLI workflows. Each worktree gets a tmux session with agent + nvim + shell windows, live status tracking, and a native tmux UI.

Supports **Claude**, **Codex**, **Gemini**, and **opencode** — any session can use any agent.

## Features

- One worktree = one tmux session (agent, nvim, shell windows)
- Multi-agent: Claude, Codex, Gemini, opencode (auto-detects installed agents)
- MCP profiles: per-session MCP server configuration
- Env file sync: auto-copy/symlink `.env` files via `.wt/sync` config
- Native tmux UI: `choose-tree` switcher with live status icons
- Action menu via `display-menu` for quick session management
- fzf picker with status info and agent conversation preview
- Auto-connects Claude to Neovim via claudecode.nvim (`--ide`)
- Status tracking via dual-write (files + tmux session options)
- PR lookup: find sessions by GitHub PR number
- Master orchestrator session with its own MCP profile
- Desktop notifications when agent needs attention
- Cross-platform (macOS / Linux)

## Install

```bash
git clone https://github.com/youruser/wt.git ~/src/wt
~/src/wt/install.sh
```

The installer will:
- Symlink scripts to `~/bin/`
- Set up tmux config at `~/.config/wt/tmux-wt.conf`
- Add tmux source line and status bar to `~/.tmux.conf`
- Merge hooks for detected agents:
  - Claude: `~/.claude/settings.json`
  - Gemini: `~/.gemini/settings.json`
  - Codex: `~/.codex/config.toml`
  - opencode: `~/.config/opencode/plugins/wt-status.js`

Requires: git, tmux, fzf, jq, and at least one agent CLI ([Claude Code](https://github.com/anthropics/claude-code), [Codex](https://github.com/openai/codex), [Gemini CLI](https://github.com/google/gemini-cli), or [opencode](https://opencode.ai/))

Optional: [gh](https://cli.github.com/) (for PR lookup), [claudecode.nvim](https://github.com/anthropics/claudecode.nvim) (for Claude IDE integration)

## Usage

```bash
# Session management
wt new                                # Interactive: pick repo, branch, agent
wt new ~/code feature-x              # Direct: create worktree
wt new ~/code feature-x --agent codex # Use specific agent
wt pick                               # fzf picker (? to toggle preview)
wt ls                                 # List sessions with status
wt delete <session>                   # Delete session and worktree

# Master session
wt master                             # Create/attach master session
wt master --force                     # Replace existing master
wt master --agent gemini              # Master with specific agent

# PR integration
wt pr 42                              # Find session for PR #42
wt pr-pick                            # Interactive PR picker (fzf + gh)

# Status
wt sync-tmux                          # Restore tmux options after restart
```

## Tmux Keybindings

| Key | Action |
|-----|--------|
| `prefix + W` | Session switcher (native choose-tree with status icons) |
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

## Env File Sync

Worktrees don't inherit gitignored files like `.env`. Add a `.wt/sync` file to any repo to automatically copy or symlink env files into each new worktree on creation.

```
# .wt/sync
copy .env
copy .env.local
copy backend/.env
symlink webapp/.env
```

- `copy` — copies the file from the primary repo checkout (isolated per worktree)
- `symlink` — symlinks to the primary checkout (stays in sync, changes affect the source)

Files missing from the primary repo are skipped with a warning. Files that already exist in the worktree are left untouched. `.wt/sync` itself is safe to commit — it contains no secrets.

## Example Setup

Here's how one setup looks in practice — a master orchestrator that handles customer support triage and dispatches coding tasks to child worktree sessions.

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
  ├── wt-hook writes ~/.local/state/wt/<session>.status
  ├── wt-hook writes tmux @wt-* session options
  └── wt-hook calls tmux refresh-client -S (instant redraw)

prefix+W → choose-tree reads @wt-icon, @wt-status, @wt-message
prefix+w → display-menu launches wt subcommands in popups
Status bar → wt-tmux-status aggregates counts from .status files
```

## Files

| Path | Purpose |
|------|---------|
| `bin/wt` | Main CLI |
| `bin/wt-hook` | Agent hook handler |
| `bin/wt-tmux-status` | Tmux status bar segment |
| `bin/wt-agent-launch` | Multi-agent launcher with per-agent logic |
| `bin/claude-ide` | Backward-compat wrapper for wt-agent-launch |
| `config/tmux-wt.conf` | Tmux keybindings and hooks |
| `config/claude-hooks.json` | Claude Code hook configuration |
| `config/hooks-gemini.json` | Gemini CLI hook configuration |
| `config/opencode-wt-plugin.js` | opencode status plugin |

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `WT_BASE_DIR` | `~/worktrees` | Base directory for worktrees |
| `WT_STATUS_DIR` | `~/.local/state/wt` | Status file directory |
| `WT_DEFAULT_AGENT` | `claude` | Default agent CLI (claude, codex, gemini, opencode) |

## License

GPL-3.0
