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
- Don't take focus. Open the pane without focusing it (`--focus=false` in Rex, `-d` in tmux), and never `:q` or `:qa` the panel; open over what's there.
- After showing something, say in one line what's up and where to look (`path:line`).
- In a review workspace (`.wt/review.sock` exists), use `.wt/bin/review-show` for slides and docs instead. It drives the same kind of panel.

## The panel

One nvim listening on a socket. Find it, or start it in a split of whichever multiplexer you're in, then send it commands. The socket is the same either way, so everything after the split doesn't depend on the multiplexer.

```bash
root=$(command wt root 2>/dev/null || git rev-parse --show-toplevel 2>/dev/null || pwd)
sock="${TMPDIR:-/tmp}/nvim-panel-$(printf %s "$root" | cksum | cut -d' ' -f1).sock"
[[ -S "$root/.wt/review.sock" ]] && sock="$root/.wt/review.sock"   # a review panel already up
alive() { [[ -S "$sock" ]] && perl -e 'alarm 2; exec @ARGV' nvim --server "$sock" --remote-expr 1 >/dev/null 2>&1; }

start=(nvim -i NONE --listen "$sock")
if ! alive; then
  rm -f "$sock"
  if [[ -n "${REX_SESSION:-}" && -n "${REX_BLOCK:-}" ]]; then        # Rex
    rex split -s "$REX_SESSION" -b "$REX_BLOCK" --split=right --ratio 50 --label nvim \
      --focus=false -c "$root" -- "${start[@]}" >/dev/null
  elif [[ -n "${TMUX:-}" && -n "${TMUX_PANE:-}" ]]; then             # tmux
    tmux split-window -h -d -l 50% -t "$TMUX_PANE" -c "$root" "${start[@]}"
  else
    echo "No Rex or tmux pane to split. Ask the user to run: cd $root && ${start[*]}"
  fi
  for _ in $(seq 50); do alive && break; sleep 0.1; done
fi

# Run Ex commands in it. execute() returns, so nothing hangs.
panel() { perl -e 'alarm 5; exec @ARGV' nvim --server "$sock" --remote-expr "execute('$1')" >/dev/null; }
```

Every Bash call is a new shell, so define these in the same call that uses them. If you're in neither multiplexer, ask the user to start the panel with the printed command, then carry on once `alive` succeeds. If the panel doesn't come up, or a call times out, tell the user. Don't fall back to another viewer.

## Rex

- **Pane IDs:** `REX_SESSION` and `REX_BLOCK` are set in your shell; `REX_BLOCK` is your own pane, which the panel splits from. If a command can't find them, run `rex whoami --json` to see where you're connected.
- **Label:** the panel is labelled `nvim`, so Rex commands can target it with `-b nvim` in your session.
- **Stuck panel:** if a call times out, `rex capture -s "$REX_SESSION" -b nvim --trim` prints its screen. If nvim is at a prompt, tell the user what it says. Don't send keys to it unless they ask.
- **Focus:** the panel opens with `--focus=false`. If the user asks to jump to it, run `rex focus -s "$REX_SESSION" nvim`.
- **Placement:** the panel goes to the right of your pane at 50%. Only use `rex resize`, `rex move` or `rex zoom` on it when asked.
- **More:** for anything beyond this, read the `rex` skill (or `rex skill show`).

## tmux

- **Pane ID:** `TMUX_PANE` is your own pane. `split-window -d` opens the panel to its right without focusing it.
- **Stuck panel:** if a call times out, `tmux capture-pane -p -t <pane>` prints its screen. Get the pane ID from `tmux list-panes -F '#{pane_id} #{pane_current_command}'`.

## What to send

Paths are absolute, or relative to `$root`. Inside the `execute('…')` string, double any single quote.

| To show | Command |
|---|---|
| A file at a line | `panel "tabfirst \| edit +120 src/app.py \| normal! zz"` |
| A doc you keep editing | `panel "tabfirst \| edit docs/plan.md \| setlocal wrap linebreak autoread"`, then `panel "checktime"` after each write |
| A second file beside it | `panel "tabfirst \| rightbelow vsplit +40 src/other.py"` |
| The branch's changes | `panel "tabnew \| tcd $repo \| edit $repo/$first_changed_file \| Unified $base"` |
| Close a diff | `panel 'exe "Unified reset" \| tabclose'` (`:Unified` takes no `\|`, so it has to go through `exe`) |

For diffs, `$base` is usually `git -C "$repo" merge-base origin/HEAD HEAD`, and `$first_changed_file` comes from `git -C "$repo" diff --name-only $base`. Unified shows the working tree against one ref, uncommitted edits included, with a file tree to step through the rest. A range `A..B` where `B` isn't the checkout's HEAD (a lower PR in a stack) is the one case for `DiffviewOpen -C$repo A..B`.
