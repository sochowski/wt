# wt integration for Pi

This directory is symlinked to `~/.pi/agent/extensions/wt` by `install.sh`.
Pi auto-discovers `index.js`; no package install or build step is required.

The extension is deliberately inert unless `WT_SESSION` is present. Managed
launches export that stable tmux session identity through `wt-agent-launch`.

## Status integration

Native Pi lifecycle events are translated into the agent-neutral `wt-hook`
status protocol. Pi's native session id is saved so `wt revive` can launch
`pi --session <id>`.

Accurate settled status requires Pi 0.80.4 or newer. Waiting-for-input status
for blocking extension UI requires Pi 0.84.4 or newer.

## Presentations

The extension registers one `present` tool that lets Pi show a navigable deck
through the managed Neovim pane. A deck contains one or more scenes; each scene
combines narration with one visual artifact:

- `file` — existing source with an emphasized line range
- `diff` — source with its Unified.nvim diff against the wt branch base
- `tree` — a project tree or a focused set of labelled paths; `view: "explorer"`
  uses `nvim-tree.nvim` when available
- `markdown` — generated non-code material in a temporary buffer

Neovim owns deck navigation locally: `H`/`L` move between slides, arrow keys also
work, and `q` exits. The extension is only the agent adapter. It sends versioned
deck JSON through the agent-neutral `bin/wt-present` transport to
`config/wt-present.lua`, where Neovim renderers own the visual behavior. This
keeps future presentation surfaces independent of Pi and allows other agents to
use the same transport.

Use `/presentation-end` to clear an active scene and restore the buffer that was
visible before the presentation began. `WT_PRESENT` can override the transport
binary, and `WT_PRESENT_TIMEOUT_MS` controls its per-operation timeout (15s by
default).
