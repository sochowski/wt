# 🐧 wt penguin

A small block-art penguin mascot for `wt`, meant to live in the worktree
picker and animate based on the selected worktree's agent state. This directory
saves the **locked designs** — sprite, animations, reference GIFs, and the
generator — plus the [integration plan](PLAN.md).

Nothing here is wired into `wt` yet; this is the design source of truth.

## Contents

| Path | What |
|------|------|
| [`sprite.txt`](sprite.txt) | The locked base sprite + anatomy legend |
| [`frames/idle.txt`](frames/idle.txt) | Idle animation spec (sleepy, `z z`) |
| [`frames/waddle.txt`](frames/waddle.txt) | Waddle animation spec (skippy hop + legs) |
| [`frames/wave.txt`](frames/wave.txt) | Wave animation spec (wing waggle) |
| [`gen.py`](gen.py) | Canonical generator — renders the GIFs below |
| `preview/*.gif` | Reference GIFs (visual source of truth) |
| [`PLAN.md`](PLAN.md) | How this gets wired into `wt` |

## The sprite

```
 ▗▄▄▄▄▄▄▄▖
▟▐ ▬  ▬ ▌▌
 ▐▄▄▄▄▄▄▄▛
   ▝▘ ▝▘
```

`▟`=left arm · `▌`(c8)=right arm · `▌▛`(c9)=back+tail · `▬`/`▘`=eyes · `▝▘ ▝▘`=feet

## Animations → agent state

| State | Icon | Animation | Feel |
|-------|------|-----------|------|
| working | `●` | **waddle** | busy, skippy hop, legs pumping, eyes open |
| idle | `○` | **idle** | sleepy, body bob, `z z` drifting |
| needs input | `⊘` | **wave** | one wing waggles hello |
| error | `✗` | _TODO_ | slumped / `✕` eyes (not yet designed) |

## Regenerating the GIFs

```bash
pip install pillow
python3 gen.py    # writes idle.gif waddle.gif wave.gif in the cwd
```

The generator draws each block glyph as filled rectangles on a cell grid — the
same way a terminal tiles them — so the GIFs faithfully preview terminal output.
See the bash-port note in each `frames/*.txt`: the mockup uses sub-cell pixel
motion that must be quantized to whole/half cells in the real implementation.
