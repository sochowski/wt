# Agent-first worktrees

A worktree is one named tmux session, with a persistent SQLite identity. It does
not need a Git checkout. New agent-first worktrees start with one interactive Pi
pane, no editor. Existing `wt new REPO BRANCH` and non-Pi agent profiles remain.

See [Named session setup](session-setup.md) for isolated multi-repo creation,
fetch/cached policy, durable partial recovery and shared naming/search.

## Everyday CLI

```bash
wt setup                             # named multi-repo menu; fetch on confirmed Create
wt repositories investigation        # Add repositories without replacing agents
wt new investigation                 # scratch/scratch-2 if omitted; cwd defaults to HOME
wt new investigation --cwd /some/directory --switch
wt new draft --offline               # persist a blank root without starting tmux
wt roots investigation               # shared JSON search: roots/repos/peers/tasks
wt ls                                # includes offline and no-repository roots

wt checkout attach investigation app /src/app
wt checkout attach investigation api /src/api
wt checkout list investigation
wt checkout detach investigation app # rejects view/agent-cwd references; preserves files

wt agents create investigation reviewer --parent main --cwd api --task 'Review the API'
wt agents list investigation
wt agents show investigation reviewer
wt agents open investigation reviewer # open the real interactive Pi window
wt agents read investigation reviewer # last 200 lines of live pane output
wt agents reparent investigation reviewer main
wt agents reparent investigation reviewer - # no supervisor
wt agents stop investigation reviewer # never cascades to descendants
wt agents resume investigation reviewer # human-only; exact conversation, fresh runtime

wt message send investigation human reviewer 'Please check the tests'
wt message send investigation main reviewer 'What did you find?' --id question-1
wt message send investigation reviewer main 'FYI: checking tests' --notify
wt message list investigation reviewer

wt view create investigation editor app --file src/main.go --placement split --anchor caller --direction stack
wt view place investigation VIEW_ID --placement split --anchor focused --direction stack
wt view place investigation VIEW_ID --placement window # detached window again
wt view create investigation diff app --base HEAD
wt view create investigation shell api --command 'make test'
wt view create investigation presentation app --deck /tmp/deck.json
wt view list investigation
wt view show investigation VIEW_ID
wt view open investigation VIEW_ID
wt view pin investigation VIEW_ID
wt view unpin investigation VIEW_ID   # human-only; original manager retained
wt view resume investigation VIEW_ID  # human-only retry after resource repair
wt view park investigation VIEW_ID
wt view close investigation VIEW_ID

wt snapshot investigation
# After loss of the tmux server:
wt restore investigation
wt message wake investigation --budget 32 # explicitly enable cooperative work again
```

Root arguments accept names or IDs. Agent arguments accept session-local names
or IDs; `main` is the original agent. Checkout targets accept aliases or IDs;
`root` selects the root's working directory. Views are addressed by ID. APIs
return JSON, except pane `read` and the traditional `wt ls` formats.

Every agent-first WT window uses mandatory master-stack: a left master (60%
default), with support panes stacked vertically on the right. There is no toggle
or free-layout escape. Single-pane windows fill their space. Editor/diff/presentation
create/place defaults to joining the caller window stack; `--placement window`
retains background placement. `--anchor caller|focused|VIEW_ID|PANE_ID` selects a
root-local window, not arbitrary geometry. Focused requires agreeing attached
clients. `--direction stack` (`right` compatibility alias) means stack insertion;
other directions and `--size` are rejected with master-width guidance.

`wt view promote ROOT VIEW` selects a stable master. `wt view master-width ROOT
VIEW 65` sets 20..80 percent (signed deltas also work). Pi exposes promote and
master-width (`percent`) through wt_view. Master identity, stack order and width
are checkpointed and restored; removing the master chooses the first survivor.
Creation, moves (both windows), parking, closing, presentation and restoration
reflow automatically. A session-local tmux layout hook also reconciles raw human
splits/closes/resizes; ordinary sessions are untouched. Legacy layouts normalize
on reconciliation. No process is restarted to enforce layout. Tiny windows that
cannot hold the stack report an error: enlarge or park support views.

Reflow preserves focus and ownership even for pinned or peer-managed panes;
source move/park/close guards still protect human/pinned/peer/active views.
Human prefix j/k cycles, h/l adjusts width by five points, Enter promotes. These
WT keys also apply on mobile; ordinary tmux keeps its mobile/raw fallback.

Additional agents are **ordinary named tmux windows in the same session**, like
managed shells: background by default, fully interactive, independently openable.
`agents create --open` opts into focus for human callers. No forced checkout,
right split, separate tmux session, or headless-only job. Pi's `wt_agent` tool
provides create (including `task`), list, read, message, reparent and stop.
The initial and additional agents have the same conversation record type.

`parent_agent_id` is current supervision, not lifetime ownership. Stopping a
parent leaves children running. Agents may change their own parent, and stop
themselves or a peer they currently supervise, subject to human view protection.
Creator metadata does not grant lasting stop authority. Human-managed/pinned
views and panes an attached human is currently using cannot be stopped by an
agent. An agent cannot open/focus windows for the human or drive agent panes
through legacy terminal-control commands. The existing `wt shell` skill remains
available in its current root: create with `new --detach` or background `run`,
observe/read/wait, and control its own unpinned shells. Mutations retain the root
projection lock and reject human-active or other-manager shells. These are cooperative rules,
**not an OS security sandbox**: extensions and agents still have their ordinary
filesystem/process access and can bypass conventions with arbitrary commands.

Limits: eight conversation records per root (stopped records count), 32 views,
16 KiB/message, four inbox deliveries per poll. Polling uses eight-record cursor
pages excluding delivered history, so large histories/backlogs stay bounded. Initial tasks have genuine human
attribution for human callers, or the authenticated agent sender for agent tools.
Peer names are resolved to stable IDs before storing relationships/messages.

## Messaging and recovery safety

Messages are durable, explicitly addressed records in SQLite. Reuse `--id` for
an idempotent send; changing its body/recipient is rejected. Requests (default)
use native Pi `sendMessage` with `deliverAs: "followUp", triggerTurn: true`:
an idle peer starts a turn, and a busy peer receives the request at the native
safe follow-up boundary. Replies can wake the requester without human typing.
`--notify` is informational and uses `nextTurn` without triggering a turn.
There is no simulated keyboard delivery.

A fresh root has a **persisted budget of 64 automatic request deliveries**.
Successful request claims consume it transactionally. Duplicate claims and
notifications do not. This bounds automatic message chains, **not tokens,
tool operations, runtime or provider costs within a turn**. Exhausted requests
remain pending. `wt message wake ROOT --budget N` (human-only, 1..64) re-arms and
refills it. It does not retry uncertain messages.

A claim is not a durable Pi receipt. Only finding the message's stable ID in a
native on-disk `custom_message` acknowledges delivery. Crash/reload between
claim, queue, execution and native persistence is **uncertain**. Such messages
are never automatically repeated. After inspection, a human can explicitly use
`wt message retry ROOT MESSAGE_ID`; duplication of previously executed work is
possible. This is not exactly-once execution.

Server-loss restoration disarms automatic requests and resumes agents **idle**,
without old tasks or queued commands. Pending requests stay pending; uncertain
claims stay uncertain. Explicit `message wake` enables requests afterwards.
Repeated restore does not refill the budget; adding a peer to a live root does
not disarm it. Notifications cannot start model work.

## Views and presentations

Views have a separate target, manager, pin state and runtime pane binding.
Checkouts are borrowed: attach, restore, park, close, sweep and root deletion
never delete repository data. `wt delete` now forgets a root/session and preserves
its checkout; remove an unwanted Git worktree explicitly with Git afterwards.
A durable checkout tombstone suppresses legacy filesystem rediscovery after
forgetting; an explicit new attachment makes that checkout discoverable again.

Editor, diff, presentation and shell views are lazy ordinary windows. Diff views
show `git diff` against the captured commit; press `r` to refresh. The new diff
adapter does not require Unified.nvim. The legacy eager editor/diff launch is
unchanged for `wt new REPO BRANCH`.

The existing Pi **`present` tool remains available** in agent-first mode. Its
optional `target` is `root` or an attached checkout alias/ID. It lazily acquires
an unpinned presentation view for that target and manager; it does not replace
another manager's view or the pane a human is using. Deck creation and slide
navigation are written to SQLite using a fenced, monotonically sequenced view
adapter. Neovim owns H/L/q navigation. Paths, including symlinks, remain confined
to the explicitly selected root. `/presentation-end` clears only this manager's
last successfully published view, selected durably across extension reloads.
Missing/stale selections are refused; pinned or actively human-used views are
protected. Both q and explicit end persist an inactive deck that restores normally.
Legacy `present` behavior is unchanged.

Closing an agent or shell view parks its live pane rather than terminating the
conversation/service; `agents stop` is the explicit agent termination operation.
A parked view remains accessible as a normal tmux window. Saved shell commands
run once only when explicitly created; the sole supported recovery policy is
`never`. Restore opens an idle usable shell with a stopped/interrupted notice.
The existing `wt shell --session ROOT new/run` CLI and prefix+c work in blank roots.
Managed shell names, cwd and log references are persisted; snapshots refresh cwd.
Recovery rebinds controls and appends to existing logs, without replaying commands.
Pinning does not transfer management; human-only unpin restores manager control.
Agent-requested close/park refuses panes actively used by a human.

## What is saved, and when

- **Automatically:** roots, nodes, names, relationships, attachments, views,
  managers, messages, delivery budget; Pi native identity/file/model/thinking/
  selected leaf at lifecycle events and serialized polling; presentation deck
  and slide changes.
- **`wt snapshot ROOT`:** actual tmux windows, pane layout and active placement;
  supported editor files, tab/split topology, active placement, cursor/scroll
  positions and shell cwd/log state. Manual panes become
  human-managed stopped-shell placeholders, never captured command replays.
- **`/wt-checkpoint` in Pi:** appends a native extension checkpoint to make a
  selected branch the durable tail, then captures the adapter. Pi may not create
  its initial file until the first completed assistant message.

Exact recovery uses **only the saved native Pi v3 JSONL file and matching native
ID**, never cwd-latest, partial-ID lookup or a fresh unrelated conversation. A
selected leaf that differs from the durable file tail is unresolved, not silently
changed. Blank/unpersisted sessions with a captured native identity also remain
unresolved: there is no durable conversation to invent. A root that has never
launched at all can still perform its initial fresh launch. A previous launch
with uncaptured identity is unresolved rather than replaced.

Restore reconciles marked live views, holds a root projection lock, and keeps a
per-node process-lifetime writer lock (plus an exact-file lock for resumed Pi).
Stale root/node/runtime updates and stale view checkpoints are rejected. Missing
working directories/transcripts, unsupported adapter versions and non-Pi exact
restoration produce retained, visible placeholders. Repeated restore leaves
live placeholders alone; repair the resource, then use `wt agents resume ROOT NAME`
or `wt view resume ROOT VIEW_ID`. These targeted, repeatable human operations
preserve IDs, allocate fresh runtimes, and disarm automatic requests. Leave an
actively used placeholder pane before resuming it; no raw tmux repair is required. Non-Pi legacy launch support remains, but
strict multi-agent restoration does not pretend it can recover unsupported CLIs.

Not saved: model process memory, active tools, incomplete provider streams,
pending editor input, arbitrary custom extension state, environment changes,
unsaved Neovim buffers, terminal/plugin editor windows or shell history. Geometry changes reconcile automatically; missing
snapshot means reconstructing the persisted managed windows, not guessing the
old layout. Root status in legacy surfaces describes the **original agent**;
`wt agents list` is authoritative for each peer's status.

The adapter follows the local Pi documentation/source API (native session v3,
`session_start`/`session_shutdown`, `agent_settled`, read-only SessionManager,
`sendMessage`, `appendEntry`). Tests use stub CLIs and extension callback mocks,
not real providers. Exact launch arguments and Neovim state/path safety are
verified; actual provider/tool recovery and arbitrary third-party Pi extensions
are not claimed. Do not reload an existing user's live extension to test changes.

## Isolated validation

```bash
./test.sh                         # legacy suite + fake-HOME staging install
(cd state && go test ./...)
node --test config/pi-wt/agents.test.js
./test-agent-first.sh             # private tmux server destroyed and restored
./test-view-adapter.sh            # real Neovim, empty sandbox config
```

All agents in the harnesses, including Pi, are stubs. `dev.sh` uses a unique fake
HOME by default and preserves real Go caches; source it only in a separate shell
with no live TMUX identity. Never install into or run test mutations against your
real HOME/state/tmux server.
