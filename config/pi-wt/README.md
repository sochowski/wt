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
- `markdown` — generated non-code material in a temporary buffer; Mermaid fences
  are the preferred representation for architecture, workflows, sequences,
  state transitions, dependencies, lifecycles, data flow, and decision trees

### Mermaid rendering

`present` deliberately sends standard fenced Mermaid inside Markdown rather than
adding a Mermaid-specific artifact or renderer. Markdown remains readable when
no renderer is installed, while the user's Neovim configuration can enhance the
same scratch buffer with Unicode diagrams.

The recommended stack is:

- [`MeanderingProgrammer/render-markdown.nvim`](https://github.com/MeanderingProgrammer/render-markdown.nvim)
- [`cavanaug/render-markdown-mermaid.nvim`](https://github.com/cavanaug/render-markdown-mermaid.nvim)
- [`beautiful-mermaid-cli`](https://www.npmjs.com/package/beautiful-mermaid-cli),
  which supplies the `bm` renderer

Install the renderer with:

```sh
npm install -g beautiful-mermaid-cli
```

After installing the two Neovim plugins with your plugin manager, presentation-
friendly settings are:

```lua
require('render-markdown-mermaid').setup({
  mode = 'unicode',
  replace = true,
  hide_source = true,
  cache = true,
  cmd = { 'bm' },
  cli = {
    border_padding = 1,
    padding_x = 1,
    padding_y = 1,
  },
})
```

`bm` does not expose a maximum width for Unicode output, and Neovim virtual
lines cannot safely soft-wrap a diagram without breaking its geometry. Present
therefore asks agents to design for an 80-column canvas: prefer `TD` flowcharts,
keep horizontal ranks to three short nodes, limit sequences to four participants,
wrap long labels explicitly with `<br/>`, and split large graphs across scenes.
The compact CLI padding above preserves more of that width for useful content.

The canvas names Markdown scratch buffers with an `.md` suffix and sets their
filetype to `markdown`, so filename- and FileType-based plugins can attach. The
buffers keep `buftype=nofile`; `render-markdown.nvim` supports that buffer type.
Use `:checkhealth render-markdown-mermaid` to verify the renderer and Tree-sitter
parsers. wt does not install or configure these optional Neovim dependencies.

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
