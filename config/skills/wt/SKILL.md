---
name: wt
description: Manage wt worktree environments, interactive peer agents, persistent shells, editor splits and Mermaid-first presentations. Use for window/layout changes, long-running processes, walkthroughs, reviews and repository orientation. Prefer wt operations over raw tmux.
---

# WT environment

Optional recipes and troubleshooting complement the always-present WT HOST
orientation. Skills and orientation do not grant tools or expand assignments.
Use only active tools or explicitly permitted CLI operations; if neither can
perform a managed operation, ask the supervisor rather than bypass restrictions.

One root is one tmux session, with separate conversations and attached checkouts.
Use supported wt tools/commands for all managed windows and splits. Never work
around a protection error with raw tmux: explain it and choose an allowed action.
Inspect existing resources before creating duplicates. Preserve human focus,
pinned views and other managers' resources. Never install/reload production to test.

## Orient and delegate

`WT_ROOT_ID` and `WT_AGENT_ID` identify your root and conversation.
Work within this session’s workspace and assigned checkouts unless explicitly
directed elsewhere. This is guidance, not an access sandbox or approval framework.
Run `wt workspace "$WT_ROOT_ID"` for the current root cwd, owned workspace/scratch
(if provisioned), and alias/path map; `wt checkout list "$WT_ROOT_ID"` also lists
attached repositories. New managed roots use `$WT_BASE_DIR/.sessions/NAME` with
`repos/` shortcuts, `scratch/`, and a data-only `WORKSPACE.md`. Old roots and
explicit `--cwd` sessions are not moved or written into. Readable aliases use
repository basenames when unique; collisions add parent name and a source hash.
Explicit aliases remain stable. Your cwd
and its context files are independent of attachments; explicitly inspect a
repository's instructions before working there.

Use `wt_agent` to list/create/read/message peers. Each peer has its own chat and
receives its task, not your full conversation. Creation is background by default.
Use an explicit checkout alias `target` for a checkout-scoped peer (`--cwd ALIAS`
on the CLI). Repo shortcuts do not bypass WT view containment: target the checkout
alias, not `root` with a `repos/` path.
Supervision follows the current parent, not creator history; stopping does not
cascade. Requests have a finite wake budget; notifications do not wake peers.

## Managed layouts

Every agent-first WT window has a mandatory large-left master and vertically
stacked right support views. One pane fills the window. There is no free-layout
mode. WT handles all geometry; never use raw tmux for managed layouts.
Inspect `wt_view` list/show before creating duplicates.

```bash
# Replace api with the assigned checkout alias (not the session workspace).
wt view create "$WT_ROOT_ID" editor api --file .
wt view create "$WT_ROOT_ID" diff api --base HEAD
wt view place "$WT_ROOT_ID" VIEW_ID --anchor ANCHOR_VIEW_ID
wt view place "$WT_ROOT_ID" VIEW_ID --placement window
wt view promote "$WT_ROOT_ID" VIEW_ID
wt view master-width "$WT_ROOT_ID" VIEW_ID 65
```

Create/place defaults to `--placement split`: append to the anchor window's
stack, preserving its master and focus. `--placement window` creates a background
window. Conversations and named shells keep their separate-window defaults.
Target (`root` or checkout alias) is independent of anchor (`caller`, `focused`,
view ID or root-local pane ID). Caller means the calling conversation, or the
human's TMUX_PANE (original conversation when unset). Focused requires attached
clients in this root to agree; otherwise use an explicit anchor.

`wt_view` supports create/place/park/close/promote/master-width; master-width uses
`percent` (20..80, default 60). CLI also accepts signed width deltas. Direction
`stack` (compatibility alias `right`) means stack insertion, never anchor-relative
geometry. Other directions and per-pane sizes are rejected; use master-width.

Master view ID, stack order and percentage survive snapshot/restore. Closing a
master chooses the first surviving stack view. Reflow may resize/reorder pinned,
human or peer panes without changing their identity, ownership or focus. Moving,
parking or closing a protected source remains forbidden to agents. Only humans
may open/focus views, pin/unpin or resume placeholders. Human prefix j/k cycles
focus, h/l changes master width by 5 points, Enter promotes the focused view.
These WT bindings also apply on mobile; ordinary tmux retains its fallback.
No processes or unsaved buffers are restarted to enforce shape.

## Preferred view UX

Default to **diffs for edits and repository-rooted browsing for exploration**,
not bare/empty Neovim views or isolated source buffers without repository context.
This is proactive agent guidance, not an automatic edit hook or permission change.

- Inspect existing views first. Reuse a suitable view for your checkout/task;
  don't create a new pane for every file or edit. Respect managers and pins.
- When starting an edit task, open/reuse a diff in the editing agent's window.
  If it would initially be empty, create it immediately after the first edit,
  rather than waiting for a final review or an explicit user request. Leave it
  available while working. Each peer should use its own checkout/task context.
- For browsing, target the actual checkout alias with an editor opening `.`:
  `wt_view {"action":"create","kind":"editor","target":"api","files":["."]}`.
  This is the managed equivalent of `nvim .` in that repository. Do not target
  the session home or `root` plus `repos/api`: shortcuts do not bypass containment.
  Individual file views remain useful when a specific location is the point.
- For edits, use `wt_view {"action":"create","kind":"diff","target":"api","base":"HEAD"}`
  (replace api/base as appropriate). Default placement joins the caller's stack
  without stealing keyboard focus. Prefer this diff as the primary task view;
  promote within that window only when it will not disrupt someone reading.
- Do not replace an active presentation, close unrelated views, reshuffle a
  human's reading layout, or use raw tmux to force this preference through a
  protection error. Explain a blocked/unavailable view and keep the work safe.

The managed diff uses Unified's checkout-wide tree and inline diffs, including
untracked files, against an exact resolved commit. External file changes refresh
automatically after a short debounce; unsaved editor buffers are not overwritten.
Selection and reading position are retained during refresh and checkpoint recovery.
`files` does not narrow the checkout diff. Do not recreate the viewer repeatedly
or stage/commit files merely to make them visible. Large selected files can take
time to render. A missing plugin or base is shown as a visible error. Paths
containing newlines can be browsed but cannot be checkpointed for recovery.
Unified is the supported live renderer. `WT_DIFF_TOOL=diffview` retains opt-in
side-by-side opening/recovery, but its refresh can fail on stat-only or
staged-then-restored changes under WT's index-write protection. Errors remain
visible; there is no silent backend fallback.

Prefer task-scoped diffs where a reliable baseline permits. The native view
compares checkout contents with a resolved commit; HEAD on an already
dirty/shared checkout can include unrelated edits. A shared-cwd `git diff` does
not establish authorship, and WT does not provide per-agent baselines. Disclose
pre-existing/shared changes, and use file-scoped presentation diffs or an explicit
summary for focused review when needed. Never label a whole dirty checkout as
solely your changes or overwrite others' work.

For requested hands-off demos, aim for roughly three seconds between visible
steps without repetitive Continue prompts. Use only supported timing and
navigation; do not promise local autoplay that is not implemented. If automatic
advancement is unavailable, explain the limitation rather than bypassing view
protections or inventing controls.

## Persistent shells

Use ordinary bash for finite commands. Use `wt shell` for servers, watchers,
REPLs and debuggers that should remain visible and survive tool calls:

```bash
wt shell ls --json
wt shell run server -- npm run dev
wt shell new repl --detach
wt shell wait server --match 'Listening on' --timeout 30s
wt shell read server --lines 100
wt shell read server --new
wt shell send repl --text 'reload()'
wt shell send repl --key Enter
wt shell stop server
wt shell wait server --exit --timeout 10s
wt shell rm server
```

Reuse stable names rather than window indexes. Keep literal text and keys
separate. Never send secrets without explicit authorization: output persists.
`wt shell watch server --on error` and `wt shell events --json` expose events;
watches do not wake agents. Remove only shells you own for the current task.
Mixed windows and actively used human panes are protected. If outside wt, use
ordinary shell tools rather than constructing unmanaged tmux resources.

## Presentations

Use native `present` for walkthroughs, multi-file reviews and useful visual
explanations. Build one complete deck, not one slide per tool call. Each scene
has a title, narrative and artifact: file, diff, tree or markdown. Source scenes
can highlight startLine/endLine and label; tree scenes can focus important paths.
Use prose for tiny answers rather than opening a canvas unnecessarily.

Treat fenced Mermaid diagrams as the default visual language for relationships.
Use `flowchart`, `sequenceDiagram`, `stateDiagram-v2`, or `classDiagram` according
to the relationship; do not substitute hand-authored ASCII diagrams.
Design for an 80-column terminal canvas: prefer TD, at most three short nodes
across, at most four sequence participants, labels under 24 characters per line;
use `<br/>` for intentional wraps. Split crowded graphs into self-contained
scenes. The user's Neovim Mermaid plugin renders embedded fences; source remains
readable without it.

Neovim navigation: H/L previous/next, ? help, q end and restore editor state.
Without the native tool, pass the complete version-1 JSON deck to
`wt-present deck-show` on stdin; `wt-present clear` ends the selected presentation.
If Neovim is unavailable, answer in text. Use wt_view placement for managed
canvas layout, never raw tmux splitting or killing.
