# Pi Durable integration plan

Status: **stage 1 verified; interactive WT core and separate WT-owned delegation v1 reviewed/fixed. Modern-new-Pi SOURCE default now durable after private core gates and primary exit proof; final independent acceptance review pending. No production activation.**

Fresh fix writer resolved both initial reports' six unique P1 findings. Modern
`worktree new`, fresh setup-menu roots and modern-root agents-create now default
to durable; explicit native, stored legacy/native and legacy shell/non-Pi paths
remain unchanged. Existing setup/resume never migrates or relaunches agents.
Low-level createNamedRoot stays default-neutral. See the authoritative fix-stage
section below; retained initial implementation/failed diagnostic history describes
prior stages, not the current rollout gate. Base WT remains
`823410f2069dce43e040b75bcfe3940360e5e4d8`.

## Approved durable-first product

Pi Durable is the eventual standard **interactive** backend for newly created WT
Pi agents, not a worker-only runner. Do not change default launch until the
interactive lifecycle, tools, inbox and minimal delegation gates below pass.
Old native Pi JSONL sessions remain launchable through the existing exact legacy
adapter; no migration and no accidental durable loading of `pi-subagents`.

WT retains roots, independent named agents, assigned borrowed checkouts, managed
windows/views/presentations, supervision and resource protection. One store and
one owner process per WT agent initially; **no daemon**. User-approved exceptions
to [worktree-model.md](worktree-model.md) and [agent-first.md](agent-first.md):
this backend may have a replacement durable frontend and automatically resume
unfinished work on **validated relaunch**. The existing native backend retains
its idle-recovery contract. Those older documents describe today's behavior,
not a prohibition on the approved durable-specific direction.

Initial scope: useful chat/steering, model/thinking selection, coding tools,
WT orientation/skills/views/presentation/peer tools, durable inbox, then
minimal durable-native single-child/parallel delegation with permission ceilings
and acceptance/review/result guarantees. No arbitrary ordinary extension
compatibility, transcript branching, nested fanout, native/headless delegation
fallback, or root service. A milestone prototype is not completion of this scope.

## Stage 1 implementation and evidence

Development-only package: [tests/pi-durable](../tests/pi-durable/README.md).
Exact direct pins: `@earendil-works/pi-durable`, `pi-ai`, `chord` **1.0.3**;
transitives locked (`typebox` 1.3.27, `diff` 8.0.4). Node **22.22.0** meets
`>=22.19.0`. npm release gitHead and tag v1.0.3 both resolve to
`d78dc83d633229d12f8b79631384c4c2717c399f`.
Installed coding-agent and TUI inspected: **0.87.1**, not silently upgraded.

- `npm --prefix tests/pi-durable test`: **17 pass**, including **8 actual
  SIGKILL/reopen cases** on real published SQLite/harness/tool implementations.
  Faux provider is upstream's real scripted provider, not a mocked harness.
- Bounded real Anthropic OAuth smoke on `claude-haiku-4-5-20251001`: two requests,
  an actual read of a disposable fixture, exact `WT_DURABLE_OK` answer, closed
  and reopened settled submission, unchanged auth file. 1,454 input / 65 output
  tokens; catalog cost ~$0.001779. Public 0.87.1 `readStoredCredential` fed
  pi-ai 1.0.3's read-only CredentialStore; login/refresh/logout forbidden.
- Headless public API probe verified component exports; no terminal started.
- `./test.sh`: 189 pass / 10 fail with macOS default TMPDIR; canonical TMPDIR
  makes Go pass, leaving 190 pass / 9 fail. Isolated `git archive HEAD` baseline
  reproduces the **same nine** failures. No unrelated repairs made.
- No default launch, running Pi reload, real-HOME install, production config,
  Go/native-provider changes, commit or staging of files.
- Managed checkout diff creation was attempted after first edit in the worker's
  own window; it timed out after 120s. Safe edits continued without extra panes.

Saved evidence: `tests/pi-durable/evidence/{contracts.tap,api.json,smoke.json,
versions.json,regression.txt}`. Full research sources and regression logs are
in isolated `/tmp/wt-durable-research` (temporary, not required for reproduction).

### Verified release behavior

| Contract | Observation / required WT handling |
| --- | --- |
| Stable admission | Concurrent same requestId inputs return one submission; key is scoped to conversation. |
| Mismatched retries | Different **type** rejects; different body, write kind/data or busy mode with the same type **silently returns original ID**. WT must atomically store/validate its own body/recipient/mode digest. |
| Late attach | `viewState`, `watch`, `watchEvents` hydrate committed state, including partial/tool output/inbox; structural watch callbacks serialize. Attachment does not schedule. |
| Read-only reopen | Open/root/read/snapshot/status/watch remain unscheduled; unfinished placed submissions remain visible. Open reconciles running tasks to pending, so it is not a read-only database connection. |
| Scheduler start | `resume`, input submit, **passive write submit**, waits start scheduling across the harness. A write does not itself create generation, but can restart already pending work. |
| Notifications | `commit(tx.doc(...))` alone does not wake. Third-party docs are not automatically mounted in ConversationView: render through documentState/watchDoc. Do not use submit(write) for a guaranteed non-waking notification. |
| Queues | Steer/followUp/write admissions survive SIGKILL. Post-tool boundary places steer/writes before followUp; final boundary places pending user modes. Withdrawn input is durably aborted. |
| Generation interruption | Committed partial survives SIGKILL; resumed request converts it to an aborted assistant entry and retries from pinned request. It is not token-level provider continuation; costs may repeat. |
| Replay | Tool intent commits before execute. Omitted replay is unsafe. **All bundled CodingTools, including read, omit replay.** Explicit reviewed safe-read wrapper reruns; bash/write/edit effects stay single in probes and produce visible interrupted results with committed output. |
| Replay policy change | Both stored and current policy must say safe. Current unsafe vetoes stored safe; current safe never upgrades stored unsafe. Recovery skips beforeTool; replay-safe execute must itself revalidate current authorization/protection where needed. |
| Close versus abort | Close writes no cancellation outcome; reopen/wait resumes. Explicit conversation abort durably settles input as unanswered/aborted; resume after reopen performs no new provider call. Cancelling one wait leaves shared work running. |
| Ownership | Foreground task-owned conversations cancel with parent; background boundaries survive ordinary parent abort but explicit background:true crosses them. Ownerless peers survive. Named WT supervision must **not** use foreground task ownership. |
| Model/cwd/thinking | A changed model selection, cwd and thinking level survive reopen; provider session UUID survives. No cwd-latest selection is needed. Tools use current env/cwd on recovery; validate against assigned checkout. |
| Exclusive owner | A second actual process can open a live store. SQLite transaction serialization is **not** harness ownership fencing. Acquire WT locks before opening. |

Safe read reruns may observe newer files. The tests' effect counter and wrapper
pause are fault-injection instrumentation, not a deduplication implementation.
Unsafe probes cut after tiny real bash/write/edit effects and committed output,
before terminal receipt; they do not enumerate every possible crash boundary.

SQLite adapter source sets WAL / synchronous NORMAL. This verifies process-crash
recovery, **not host-power-loss durability**. JSONL fsync is not a shortcut to a
stronger guarantee: v1.0.3 source flushes sidecars before appending the main
marker but does not ordinarily flush that marker. Its README shorthand is
weaker than an acknowledged-commit host-failure guarantee. Do not advertise one.

## Identity, ownership and recovery boundaries

- `wt.db` remains authoritative for roots, agents, supervision, borrowed
  checkouts, views, inbox admission/wake budget, delegation and permissions.
- Durable conversations, submissions, checkpoints and documents use a separate
  per-agent database, proposed `$WT_STATUS_DIR/durable/<root>/<agent>/session.sqlite`.
  Persist canonical store path, application store UUID, exact conversation ID,
  backend/schema/definition/dependency version metadata and assigned cwd.
- Durable numeric IDs (root conversation is a reserved ID) identify records
  **within a store**, not globally. WT agent ID and store UUID are also required.
- Keep WT process-lifetime node lock; add canonical-store lock across WT DBs.
  Fail closed on missing stores, identity/definition incompatibility, cwd or lock
  problems. Never open/create a replacement store or select cwd-newest on restore.
- Relaunch validates stop state, identities, assignments, runtime token and
  dependencies; then auto-resumes unfinished durable work. Render retained
  interrupted/error state visibly. Unvalidated/read-only inspection never uses
  submit/wait/abort/compact/reset entry points that enable the scheduler.
- **Stop** marks WT stopped and prevents automatic relaunch. **Abort** durably
  cancels selected work. **Close/crash** leaves recoverable tasks. Parent stop
  does not stop independent named peers. Task-owned workflow cancellation may
  be used only when its separately admitted contract promises that lifetime.
- Runtime-token fencing and current resource protections apply to every WT tool
  and reconciliation, not only startup. Serialize writers per assigned checkout
  or admit independent worktrees; upstream per-file edit/write queues are only
  in-process and do not serialize bash or peer processes.

## Cross-store inbox and delegation

No atomic commit spans wt.db and durable storage. Use stable operation IDs and
reconciliation at every claim/admit/ack and reservation/result boundary.

For WT requests derive requestId from root, recipient, WT message ID. Atomically
record its immutable body/recipient/mode identity with durable bridge state,
then submit and record submission ID. Reconcile uncertain claims by that ID
rather than blindly issuing new requests. WT acknowledgement means **admitted**,
not answered or external effects completed. Deduplicated reconciliation must
not consume a wake permit twice. New admissions still need WT budget/permission.
Keep old native uncertain-delivery policy separate; never redeliver through a
new backend.

Notifications use an application document that never calls a scheduler-starting
API; UI observes it separately and model-facing delivery is explicitly selected
at the next authorized turn. Tests prove doc-only non-waking behavior, not a
finished WT notification consumer or exactly-once external effect bridge.

Named peer agents require independent stores/processes. Upstream foreground
subagent examples and background anchors are useful task semantics, but are
**not a replacement for WT's independent named agents**: parent process close
would suspend all same-store background tasks. Durable delegation should reuse
WT admission, roles/tool ceilings, exact retained-child identity, acceptance and
review gates, result/provenance/publication reconciliation and budgets through
a new versioned durable bridge. Do not load ordinary native-provider.js or the
pinned `pi-subagents` host into durable. Ordinary Pi keeps provider contract pin
`4077d3b432bfbfc326191fb0fe05fbccba665484`.

A task receipt/memo before or after bash, writes, commits, view mutation or remote
effects is not exactly-once. Default these unsafe. Only an effect-boundary stable
operation ID with real dedup/reconciliation can justify safe replay. Reviewer
ceilings must be enforced at host/environment boundary, not only a removable
selected extension: upstream hooks are selection-scoped, with no global guard.

## Public interactive API findings

The published durable package exports harness/views/events/tasks, **no TUI**.
Coding-agent's experimental durable frontend is excluded from npm `dist` and
not exported. Its `--continue` picks cwd-newest and its runtime unconditionally
resumes: the latter now matches approved *validated* recovery intent but lacks
WT identity/stop/assignment checks. Do not launch this demo unchanged.

Reusable public installed 0.87.1 APIs verified by import and headless render:

- pi-tui: TuiMainScreen/TuiAltScreen, ProcessTerminal, Editor, Markdown,
  ScrollView, Container, SelectList, matchesKey and generic keybindings.
- coding-agent: AssistantMessageComponent, UserMessageComponent,
  ToolExecutionComponent, CustomEditor, DynamicBorder, initTheme/getMarkdownTheme;
  ModelRuntime, SettingsManager, loadProjectContextFiles, loadSkills,
  formatSkillsForPrompt, readStoredCredential.

The demo additionally imports non-root-exported application KeybindingsManager,
WorkingStatusIndicator, InteractiveThemeController, createAllToolRenderers,
buildSystemPromptSections, theme/getEditorTheme. Proposed next-stage path: build
a small WT-owned view/controller on public pi-tui and selected public components,
with explicit durable identity and local UI helpers; do not vendor the entire
frontend or depend on unstable internal imports. Pin and typecheck the eventual
production SDK/TUI closure separately; this stage proves 0.87.1 export/auth
compatibility, not full 0.87.1 ModelRuntime with 1.0.3 harness or TUI behavior.

## Concrete next-stage map and gates

1. **Locked interactive runtime; no default flip yet.** Add private production
   Node package/entrypoint distinct from `tests/pi-durable`, with exact pins and
   durable runtime, view/controller, public TUI composition, coding tools and
   provider credential adapter. Add backend-discriminated snapshot metadata at
   `state/worktree_store.go` (`AgentSession.Adapter` is currently PiSnapshot),
   backend launch selection in `state/agents.go` / `bin/wt`, and exact launch in
   `state/worktree_runtime.go` (`strictPiArgs`, `viewCommand`, `runAgent`). Retain
   native JSONL path validation; durable does not take `--session-dir` or
   `PI_SUBAGENT_REQUIRED_NATIVE_PROVIDER=wt-interactive-v1`. Handle bootstrap
   store creation/adoption with stable ID reconciliation before scheduling.
   Gates: exact store relaunch, two-store identity confusion, missing/corrupt
   store, incompatible definition, canonical lock race, model/cwd persistence,
   stopped/no-native-ID durable placeholder, runtime fencing and auto-resume.
2. **Stop/abort and tools.** Extend `state/worktree_lifecycle.go` resume/stop
   semantics by backend, not native ID presence. Keep stop non-cascading. Add
   durable WT orientation via existing `config/pi-wt/orientation.js` pure helper;
   extract transport/schema seams from `agents.js`, `views.js`, `presentation.js`
   without loading their ordinary Pi extension host. Add skill/project-context
   prompt sections with trust policy, managed peer/view/presentation tools and
   unsafe defaults. Gate actual interactive chat/steer/followUp/model/thinking,
   coding read/edit/write/bash, skill use, restricted reviewer protections,
   focus/pin/presentation preservation and independent peer runtime survival.
3. **Durable inbox bridge.** New backend-specific receipt/reconcile path around
   `state/worktree_inbox.go` and claim/ack/budget routines in worktree store.
   Do not weaken ordinary native disk-receipt policy in `config/pi-wt/agents.js`.
   Gate claim-before-admit, admit-before-ack, reconnect retries, mismatched IDs,
   duplicate-budget accounting, non-waking notifications and stale runtime.
4. **Durable-native delegation.** New admission/host/result adapter around
   `state/worktree_jobs*.go`, separate from `config/pi-wt/native-provider.js`.
   Minimal single-child and bounded parallel jobs, no nested fanout. Independent
   child processes/stores and assigned checkout writer admission. Parent reporter
   tasks reconcile WT job results with stable IDs; WT relationship is not task
   lifetime ownership. Gate permissions, acceptance and mandatory review,
   retained-child resume, visible human intervention, stopped parent/live peers,
   crash at publication boundaries and no silent native/headless fallback.
5. **Standard-new-Pi gate.** Only after those interactive/delegation gates,
   full private staging install path (`install.sh`, `staging.sh`, stubs), preserved
   native pin tests, Go and regression suites pass (or independently resolved
   baseline failures), update default new-agent backend. Old native sessions
   continue with original adapter and idle restore; no migration.

Unverified **at the end of stage 1** (see current integration evidence below): WT integration, actual interactive terminal input/render lifecycle,
production cross-store fencing/reconciliation, all-provider/OAuth refresh,
compaction/deferred requests, hostile/non-cooperative tools, slow-watch overflow,
full crash-point enumeration, host failure. These are remaining gates, not
claims satisfied by this spike.

## Sources audited

- Local Pi README and complete relevant docs: SDK, TUI, providers, custom-provider,
  extensions, skills, models, settings, configuration, security, message-types,
  session-format; linked SDK examples 02/04/05/06/09/12 and exported declarations.
- [Pinned Durable README](https://github.com/earendil-works/pi/blob/d78dc83d633229d12f8b79631384c4c2717c399f/packages/durable/README.md),
  linked pi-ai README, spec task/submission/tool/observation/storage sections,
  Chord usage guide; release source/declarations for harness, submission, tool,
  SQLite/JSONL adapters and auth APIs; linked runnable examples 13/14/20–24.
- [Pinned experimental frontend](https://github.com/earendil-works/pi/tree/d78dc83d633229d12f8b79631384c4c2717c399f/packages/coding-agent/src/experimental/durable):
  README, main/runtime/sessions/harness-setup/prompt/TUI and subagent example.
- WT [agent-first](agent-first.md), [worktree model](worktree-model.md),
  [native delegation](native-delegation.md) and named source seams above.

## Current production integration / recovery handoff

Source and reproducible commands: [runtime/pi-durable](../runtime/pi-durable/README.md).
The saved partial writer implementation was preserved and continued, not replaced.
`tests/pi-durable` and its 17 real-package contracts remain intact.

Implemented per-agent exact envelopes/stores and inherited node/store/writer locks,
explicit opt-in bootstrap, validated recovery, abort/stop separation, public
pi-tui chat/steering/followUp/model/thinking/skills, coding and WT transports,
non-waking documents and immutable inbox reconciliation. Native adapters/pin and
idle restore are unchanged. Production depends on public pi-tui, durable/ai/chord
and small WT-owned nonexecuting resources/auth helpers, **not coding-agent SDK**.

Separate WT-owned v1 durable task admission/host/result/review bridge now persists
resolved built-in roles/version/digest, exact parent/child identities and stores,
assigned cwd/tool ceilings and immutable task/result/review IDs. Parallel<=4 plus
root eight-retained-agent and wake budgets; writer lease; no nested fanout.
Reviewer is read-only at tool/environment boundary. Host-private inherited-FD
capability is separate from tool-visible runtime tokens. Writer claims are checked
against actual baseline/change/command receipts and require an independent
reviewer read/acceptance over the exact snapshot. Missing/failed/uncertain review
or interrupted unsafe effects cannot become success. Commit-only result reporter
does not wake the parent. Human provenance is `not-observed`, not fabricated.

Retained current evidence:
- 15 production runtime tests, including actual SIGKILL/reopen, unsafe-effect
  uncertainty, result-publication reconciliation, terminal input/render/close,
  missing/unsupported primary-model failures and natural-exit provider-resource
  cleanup regression and nonexecuting Git-evidence filter/fsmonitor guards.
- Compiled WT/private tmux proof: single-child plus parallel independent
  writers/reviewers/stores, actual writer-publication and inbox-admit-before-ack
  SIGKILLs, no repeated provider calls or wake charges, non-waking notification,
  root/cumulative ceilings, fixed reviewer tools, pins/focus and named-peer
  survival after parent stop. Uses the published faux provider, not live child
  agents or native-provider fallback.
- Production-entrypoint bounded Anthropic Haiku interactive read proof: exact
  WT_DURABLE_INTERACTIVE_OK, quit, settled store inspection, auth unchanged;
  2,641 input / 68 output / $0.002981 catalog cost.
- Actual configured primary openai-codex/gpt-6.1-sol diagnostic: supported catalog
  and fresh read-only credential; committed done/no live work by 7.3s, TUI /
  Harness closed by 7.4s, but process exit exceeded 15s due retained public Codex
  websocket session resources. Fixed via public pi-ai cleanupSessionResources
  keyed to persisted ProviderDoc.sessionId; deterministic process-exit regression
  passes. **No further paid run / no post-fix live primary-exit proof.** Keep as a
  visible rollout gate, not silent Haiku fallback. Missing/unsupported configured
  primary now fails explicitly before conversation creation.
- Fake-HOME staging install with checkout-local locked bootstrap succeeded;
  existing cleanup race required delayed temporary-directory cleanup. A unique
  WT_STAGING_SOCKET avoids touching another staging user's private server.
- Production dependency audit: zero vulnerabilities. Canonical-TMPDIR Go suite
  and build pass. Existing nine full-regression baseline failures remain outside
  scope; do not repeatedly rerun or repair them for this change.

This is core implementation for review with default transition and primary live
exit verification still pending, **not full durable-first product acceptance**.
No production install/reload/global configuration changes or activation, staged
files, repository commits or pushes.

## Fix-stage disposition (current; supersedes prior pending-default/exit status)

- Assigned coding paths now match pinned @/Unicode/home/file-URL resolution before
  authorization; per-call exact canonical path execution and environment I/O
  guards cover read fallback variants, symlinks and transformed writer paths.
- Human TUI `/stop` has private capability-authenticated exact self/current-runtime
  admission even when attached/pinned. Stopped state and uncertain inbox claims
  persist atomically; no model control privilege widening or peer cascade.
- Durable job actors' ordinary WT CLI admission denies all unadmitted mutations,
  including top-level legacy state and registry routes before their old dispatch.
  Private host capabilities remain separate from tool-visible runtime identity.
- Reviewer receipts require meaningful full successful reads of every changed
  artifact; each criterion references relevant observed receipts. Deleted tracked
  artifacts are read via retained `wt://review-diff`. Error/no-content/truncated
  or unsupported artifact reads cannot authorize success.
- Exact bounded writer/reviewer snapshots include ignored outputs as well as
  tracked/untracked files. Changing ignored output changes the snapshot digest.
- Durable presentation uses versioned managed `worktree present` with explicit
  assigned target, preserving reusable views, pins/ownership and human focus.

Checks: 24/24 runtime; compiled private delegation/publication+inbox SIGKILL,
permissions and budgets; separate actual Neovim presentation create/reuse/pin/
focus and attached pinned self-stop/no-relaunch/peer proof; full canonical Go
suite/build; default/menu/bootstrap-failure/journal/resume tests; automatic locked
fake-HOME Pi install bootstrap and zero production audit vulnerabilities. Native
keyboard popup acceptance passes with explicit native fresh-root bindings.
Native agent-first/master-stack/native-install scripts retain failures reproduced
on unchanged base (count formatting/repeat navigation/package-shape assertion);
no unrelated repairs or repeated full ./test.sh runs.

One separately approved bounded post-fix PRIMARY production-entrypoint proof
passed: openai-codex/gpt-6.1-sol, actual read/exact WT_DURABLE_INTERACTIVE_OK,
done/no live work 8.864s, runtime close 8.973s, natural owner exit 11.993s,
auth unchanged; 1491 input/29 output/catalog $0.003272. No second paid repeat.
New source defaults switched only after those core gates. Existing native adapters
and pinned native-provider code/tests/pi-durable remain untouched. No production
activation/install/reload/global config/auth writes, repository commit/push or
staged files. Final independent review remains mandatory for product acceptance.

Evidence/commands: runtime/pi-durable/evidence/fixes-* and its README. Residual
boundaries: no host-power-loss/external-effect exactly-once/hostile same-UID claim;
expired OAuth/custom catalogs/other providers/human provenance not certified;
bounded snapshots/read coverage fail closed for large, empty, truncated or binary
artifacts. This is not arbitrary ordinary Pi extension or universal Pi parity.

Additional supervisor-approved acceptance fix: semantic Git index evidence
(mode/blob/path/stage and assume-unchanged/skip-worktree flags), with optional locks
disabled. Writer success denies new staging/index hiding changes, while preserving
preexisting staged baseline. Missing/unreadable evidence fails closed. Real-package
git-add/hiding negatives and a preexisting-stage-preserved positive pass. Cached
success on recovery revalidates current contract/snapshot before immutable
publication without rerunning unsafe tools. This is final-state evidence, NOT a
no-transient-staging claim, rollback policy or OS sandbox. Final runtime gate now
24/24; compiled private SIGKILL/result/inbox/permissions/budgets proof still passes.
