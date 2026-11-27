---
name: nvim-panel
description: Show the user code, files, markdown docs or diffs in their own nvim, in one reused terminal pane beside you (Rex or tmux). Use whenever you want them to look at something, or they ask to see, open, show or view a file, diff, doc, slides or a walkthrough. Takes precedence over making HTML pages or opening a browser.
---

# Showing things in the nvim panel

The user reads code, docs and diffs in nvim, with their own config, in a terminal pane beside yours. That pane is in Rex or tmux, whichever you're running in. Keep one such pane per workspace and reuse it for everything. Don't open a new pane for each view.

## Rules

- Use their config. Never pass `--clean`, `-u NONE`, `-u NORC` or `--noplugin`, and never use `--headless` for something they're meant to see.
- Always pass `-i NONE`. Their ShaDa file is damaged, and without it nvim stops at "Press ENTER".
- Show diffs in **unified.nvim** (`:Unified <base>`). Don't use diffview, a `.diff` file or `git diff` output, unless it's a range unified can't show (see below).
- Markdown, not HTML. Write docs and slides as `.md` files and open them in the panel. render-markdown renders them, including mermaid blocks.
- Don't take focus (`wt panel` doesn't), and never `:q` or `:qa` the panel; open over what's there.
- After showing something, say in one line what's up and where to look (`path:line`).
- In a review workspace (`.wt/review.sock` exists), use `.wt/bin/review-show` for slides and docs instead. It drives the same kind of panel.
- Always go through `wt panel`. An nvim you start yourself doesn't tell the user when there's something to look at.

## The panel

`wt panel` drives one nvim per workspace, listening on a socket, in a pane beside yours. `wt panel ex CMD` runs an Ex command line in it, opening the pane first with `wt pane split` if it isn't up, in Rex or tmux alike. It prints what the command printed, and exits non-zero with the error if the command failed. In a review workspace with the review panel up, it uses that panel.

You don't need to tell the user to look or ask for their attention. When you show something while they're looking elsewhere, the pane marks itself blocked with what to look at (in Rex; in tmux it rings the bell and sets its title), and that clears when they look.

If you're in neither multiplexer, `wt panel ex` fails and prints the command that starts the panel. Ask the user to run it, then carry on once `wt panel ex 'echo 1'` succeeds. If the panel doesn't come up, or a call times out, tell the user. Don't fall back to another viewer.

`wt panel pane` prints the panel's pane ref. Use it with `wt pane` (`wt pane --help` for the rest):

- **Stuck panel:** if a call times out, `wt pane capture "$(wt panel pane)" | tail -30` shows its screen. If nvim is at a prompt, tell the user what it says. Don't send keys to it unless they ask.
- **Focus:** if the user asks to jump to it, `wt pane focus "$(wt panel pane)"`.
- **Placement:** it opens right of your pane at 50%. Rearrange it only when asked, with the multiplexer's own commands (in Rex, read the `rex` skill).

## What to send

Paths are absolute, or relative to the workspace root (`wt root`). Pass the Ex line as one argument; `wt panel` handles the quoting.

| To show | Command |
|---|---|
| A file at a line | `wt panel ex 'tabfirst \| edit +120 src/app.py \| normal! zz'` |
| A doc you keep editing | `wt panel ex 'tabfirst \| edit docs/plan.md \| setlocal wrap linebreak autoread'`, then `wt panel ex checktime` after each write |
| A second file beside it | `wt panel ex 'tabfirst \| rightbelow vsplit +40 src/other.py'` |
| The branch's changes | `wt panel ex "tabnew \| tcd $repo \| edit $repo/$first_changed_file \| Unified $base"` |
| Close a diff | `wt panel ex 'exe "Unified reset" \| tabclose'` (`:Unified` takes no `\|`, so it has to go through `exe`) |

For diffs, `$base` is usually `git -C "$repo" merge-base origin/HEAD HEAD`, and `$first_changed_file` comes from `git -C "$repo" diff --name-only $base`. Unified shows the working tree against one ref, uncommitted edits included, with a file tree to step through the rest. A range `A..B` where `B` isn't the checkout's HEAD (a lower PR in a stack) is the one case for `DiffviewOpen -C$repo A..B`.
