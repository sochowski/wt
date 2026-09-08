# Agent-first worktrees: implementation contract

This document records the agreed direction and acceptance boundary. The user
has clarified that this is one implementation effort for the complete agreed
core workflow, not a foundation-only slice. It is a design contract, not a claim
that every feature below has shipped.

## Vocabulary and ownership

- A **worktree** is a named, searchable tmux session. It need not be a Git
  worktree or even start in a repository.
- An **agent session** is a persistent agent conversation belonging to that
  worktree. The initial agent and agents it creates use the same record and
  APIs. Pi is the default for new agent-first worktrees. Existing CLI agent
  conversations must remain intact.
- `parent_agent_id` is a changeable, cycle-free supervision relationship, not
  ownership of a child's existence. Optional creator metadata is historical.
- A **view** displays an agent, files, a checkout diff, a presentation or a
  process. Its target and manager are separate from its tmux placement.
- A **checkout** is a resource attached to a worktree by a local alias. Several
  repositories or checkouts may be attached. Attaching does not transfer
  deletion authority: existing/imported checkouts are borrowed by default.
- A **message** is addressed between agents in the same worktree. Durable
  inboxes must not depend on the spawning parent or simulated keyboard input.

User changes to focus and pinned views take precedence over agent layout
requests. Stopping a parent must not cascade to its children. Direct messaging
does not grant control or additional tool capabilities. Agent-created work is
bounded; the first implementation must enforce concrete limits rather than
promise a generic permission framework.

## Persistence and compatibility

Keep SQLite and the existing session APIs. Add versioned transactional schema
migration and child records rather than replacing the whole application.
Existing session rows map to a root, an original agent, and (where present) an
attached checkout. Preserve names, native conversation IDs, paths, config and
PR metadata. IDs are stable; tmux names/pane IDs are runtime bindings.

Legacy hook updates without a node identity target only the original agent.
New agent updates identify both root and agent so one Pi conversation cannot
overwrite another. Keep one authoritative store; compatibility writes must be
translated transactionally rather than maintaining divergent copies.

Never invent a transcript or exact layout that was not recorded. A missing
conversation is unresolved and restore must not silently fall back to cwd-latest
or a fresh unrelated conversation. Existing non-Pi agents retain their launcher
support; unsupported exact restoration must fail visibly without touching them.

## CLI direction

- Preserve `wt new <repo> <branch>` and existing script consumers.
- Add `wt new <name>` / no-repo creation: one Pi pane, no eager editor or Git
  checkout. A generated name is acceptable for bare `wt new`.
- Additional agents are named, fully interactive Pi processes in ordinary
  managed tmux **windows inside the existing worktree session**, like `wt shell
  new/run`. Creation is background/no focus theft by default; open explicitly
  to converse directly. The initial agent remains the main pane. No forced
  checkout, headless-only job, automatic right split or independent tmux session.
  Provide stable list/open/read/control/discovery operations; native conversation
  recovery and durable messaging stay agent-specific, never terminal typing.
- Provide structured agent create/list/show/message/reparent/stop operations
  without ambiguously breaking the legacy `wt agent <session> [task]` form.
- Provide explicit checkout attach/list and lazy view create/show/list/close or
  park operations. Target IDs or aliases are explicit; reject ambiguous paths.
- Add snapshot/restore commands. Restore is independent of any living parent.
- Search/list roots from persistent records, including offline no-repo roots,
  not only directories under WT_BASE_DIR. Metadata filtering is enough for v1.

Exact flag spelling is implementation-owned; document and test it consistently.
Do not add a daemon, a replacement Pi frontend, or a general plugin framework.
Reuse the Go store/registry, bash helpers, Pi extension, tmux and Neovim.

## Completion boundary

The complete core workflow must work without raw tmux repair or deferred basic
operations: create/task/message/stop/resume named agents; attach/detach borrowed
checkouts without deleting them; create/open/park/close/pin/unpin supported views;
and use the existing managed-shell CLI and leader+c inside no-repository roots.
Editor checkpoints should retain supported file/cursor/tab/split state, not
just a list of filenames. Managed shells must retain their recorded cwd and
persistent logs across reconstruction, without replaying arbitrary commands.

A limitation in generic process-memory, unsaved-buffer or third-party extension
recovery is expected. A missing normal wt workflow is incomplete implementation,
not something to silently relabel as a future slice. Existing non-Pi launch
compatibility remains required; unsupported exact adapters fail visibly.

## Recovery contract

Recovery after loss of the tmux server reconstructs persisted state; it does
not checkpoint process memory or replay incomplete tool operations.

Persist structural changes at creation/update, not only on shutdown. Capture
layout changes and supported view state while alive (explicit snapshot is the
minimum reliable v1 checkpoint for user-driven layout changes). Store restart
policies separately from saved commands. Persist messages with stable IDs,
explicit delivery state, and deduplication; do not claim exactly-once execution
across an uncertain crash boundary.

Restore should:

1. Validate roots, working directories, adapter state and transcript references.
2. Reconcile existing live identities before launching replacements; prevent
   simultaneous restorers/duplicate writers for a saved conversation.
3. Recreate supported windows/panes and bind new tmux IDs to stable records.
4. Resume saved agent conversations initially idle, without rerunning old tasks.
5. Reopen editor files/diffs and saved decks using type-specific state.
6. Reopen shells; commands require an explicit safe restart policy, otherwise
   leave a usable pane with a clear stopped/interrupted notice.
7. Report unavailable components and preserve their records for repair.

For Pi, record the exact native file/ID through its session lifecycle. Blank
sessions may not yet have a transcript; preserve that distinction. Completed
messages are durable only to Pi's actual persistence boundary. Model/config,
selected branch, pending input and in-flight operations may need more than a
filename; document limitations not covered by the first adapter.

Neovim session/layout files do not guarantee unsaved buffer recovery. Diff
restoration should retain its checkout and comparison base. Presentation state
must include the deck and slide if restoration is claimed. Hiding a live agent
or service means parking its pane rather than killing it.

Destructive checkout removal is not a side effect of archiving/restoring a
worktree or closing a view. Do not replay unknown shell commands, auto-resolve
missing transcripts to other chats, or treat old `working` status as proof of a
surviving process.

## Required validation

All development uses fake HOME/WT paths, private tmux sockets and stub agents.
Never install or drive the checkout against real state. Fix sandbox inheritance
of WT_DB, config/log/socket paths and stable identities; ensure pi is stubbed in
all harnesses before making it the default. See AGENTS.md.

Unit/integration coverage must include:

- Upgrade old SQLite rows, repeat migration, and preserve legacy fields.
- Per-agent identity/status independence and cycle/cross-root validation.
- Borrowed checkout preservation; unsupported/missing resources remain visible.
- Durable addressed messaging and restart/deduplication behavior.
- Agent-first root and lazy view creation without focus theft.
- Two agents, two repositories, a diff and a shell in one worktree; snapshot;
  stop only the private tmux server; restore; verify stable identities, exact
  resume arguments, relationships, targets and no unsafe command replay.
- Repeated/concurrent restore does not create duplicate agent processes.
- Legacy ./test.sh and state Go tests/build pass.

Real provider calls are not needed for this acceptance test: stubs record
launch arguments and mock Pi extension callbacks test message integration.
Document which recovery behavior remains unverified with real agent CLIs.


## Implemented CLI and bounded cooperative delivery

The first implementation is described in [agent-first.md](agent-first.md),
including exact commands, validation and native persistence limitations.
Additional agents accept root-local names as well as stable IDs. `agents create
--task` and Pi's `wt_agent` task parameter create a durable initial request with
human or authenticated agent attribution. Requests wake idle peers and replies
can wake their requesters using native `followUp` delivery, never keyboard input.

A persisted root budget starts at 64 automatic request deliveries. Successful
request claims alone decrement it transactionally; each poll delivers at most
four messages. This is a delivery-chain bound, not a token/cost bound. Full
server-loss restoration disarms automatic work and preserves pending requests;
human `message wake --budget N` re-arms explicitly. Notifications use next-turn
non-triggering delivery; uncertain claims require explicit human retry. Repeated
restore never refills the budget. Current supervision, not creator history,
governs agent stop control, with pinned/human-managed/actively used pane protection.

The existing `present` tool also remains available in agent-first mode. It lazily
acquires an explicitly root/checkout-targeted, manager-owned Neovim presentation
view. Deck and slide checkpoints are runtime-fenced and sequenced in SQLite;
legacy presentation behavior and explicit-root path confinement remain intact.
