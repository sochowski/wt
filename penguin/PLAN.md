# Plan — wiring the penguin into `wt`

Goal: a small penguin animates in the **worktree picker** (`prefix + s` →
`wt pick`), reacting to the selected worktree's agent state. Fun, low-risk,
opt-out-able.

## Where it goes

`prefix + s` opens a full-screen `display-popup` running `wt pick` →
`pick_worktree()`, a single `fzf` with:
- left ~40%: the worktree list
- right 60%: `--preview "wt preview-pane {1} {2}"` → `preview_pane()` at
  `bin/wt:1351`

The penguin renders at the **top of the preview pane**; the existing agent
capture + diffstat footer shift down by the penguin's height (~5 rows).

Two hard constraints discovered during design:
1. A tmux `display-popup` is a single pane — you can't split a real sub-pane
   into a corner of it. So the penguin lives *inside* the fzf preview.
2. fzf has no timer/tick; it only redraws the preview as the preview command
   streams stdout. So the preview command must become a **frame loop**.

## Mechanism (compute-once, animate-cheap)

`preview_pane()` today computes git diff + PR state + metadata, prints once,
exits. Change it so that, for the selected row:

1. **Compute once** into shell vars: diffstat, PR badge, session metadata,
   agent capture, and the resolved `state` + `agent`. (Expensive: git/gh.)
2. **Loop** at ~5–7 fps:
   - `printf '\033[H'` (cursor home — NOT `\033[2J`, to avoid flicker)
   - print the current penguin frame (the only moving part)
   - print the cached footer/content, padded to fixed width/height
   - advance frame index; `sleep 0.15`
3. fzf sends SIGTERM when the selection changes (or the picker closes), which
   kills the `sleep` and ends the loop — no lingering processes.

Frame state is read once at loop start, so no per-frame git/gh calls.

## State → animation

Read the selected session's `status` (already loaded via `load_session` at
`bin/wt:1363`) and pick the frame set + speed:

| `wt-state` status | icon | animation | fps |
|-------------------|------|-----------|-----|
| working | `●` | waddle (skippy) | ~5 |
| idle | `○` | idle (bob + `z z`) | ~2 |
| needs input | `⊘` | wave | ~4.5 |
| error | `✗` | error mood (TODO) | ~1 |

Later (optional): tint by `agent` to echo the hero art — Claude=red/orange,
Codex=green, Gemini=blue.

## Rendering in bash

- Frames are bash arrays of block-glyph strings (see `frames/*.txt` and
  `sprite.txt`). ANSI color codes for the body/eyes/feet.
- Terminals render whole cells (+ half/quarter via block glyphs). The Python
  mockup's sub-cell pixel motion (body bob, foot lift) must be **quantized to
  whole/half-cell steps** in bash. Each `frames/*.txt` has a bash-port note.
- Factor a helper `wt penguin-frame <state> <i>` that prints a single frame to
  stdout — this is both what the loop calls and what tests assert on.

## Opt-out / gating

- `WT_PENGUIN=0` disables (penguin band simply not printed). Default on.
- Respect dumb terminals / `NO_COLOR`.

## Blast radius

`preview_pane()` also feeds `switch_picker()` (prefix + W). Decide: same
penguin there too (consistent) — behind the same gate.

## Testing (per CLAUDE.md tiers)

- `./test.sh` can't drive interactive fzf, so add a deterministic unit:
  `wt penguin-frame <state> <i>` prints a known frame → assert bytes.
- Live behavior: `source ./dev.sh`, open `wt pick`, eyeball the loop; verify
  SIGTERM cleanup (no stray processes after closing the picker).
- No `install.sh` needed (this doesn't touch install/hooks/tmux.conf).

## Open questions

- Design the **error** mood (and finalize needs-input as the wave).
- In-place vs. travel-across for waddle in the real pane.
- Color tinting by agent (phase 2).
- Exact penguin band height vs. preserving enough agent-capture rows.
