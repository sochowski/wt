# Ordinary WT native execution — source implementation checkpoint

**INCOMPLETE delivery; disabled pending independent review and rollout approval.**
The supported native route and exact public continuation now exist in source and
have passed isolated real-SDK/TUI workflow proofs. Normal WT bootstrap/settings
have deliberately **not** been changed. Existing ordinary staging sessions still
use their previous configuration; scratch-3 and its children were not replayed.

## Implemented boundary

- The package exposes a versioned, owner-scoped required-native-provider handshake,
  independent bootstrap tool guard, and durable parent-session requirement marker.
  Missing/incompatible registration rejects rather than selecting headless execution.
- Ordinary `subagent` and workflow `runs.all` retain the package's normal role
  resolution and package-owned runner. WT places that runner in a genuine terminal.
  The package creates one SDK session and adopts that **same** session into
  `AgentSessionRuntime` / `InteractiveMode`; it does not attach a log observer or
  create a second transcript writer.
- WT's experimental `config/pi-wt/native-provider.js` connects indexed reservation,
  queued turns, one-shot dispatch, native checkpoints and observed completion to
  internal `_delegation` commands. It is not imported by the normal WT extension.
  Detached placement preserves parent focus. Concurrent root placement uses a
  bounded lock retry and revalidates ownership after acquiring the lock.
- Admission binds the resolved runner configuration, exact owner/provider/parent,
  cwd, job and turn. Launch validates the stored contract, preserving null versus
  empty lists. Output/error UTF-8 validation is independent, with a combined bound.
- Public `resume` recognizes retained native ownership, verifies the exact current
  job/turn/native/file/host binding and immutable conversation contract, and queues
  a new turn to the existing host. It never takes an SDK revival lease. The retained
  host refreshes turn-local hooks on the same `AgentSession`/`SessionManager`, with
  the same supervisor and a new child control channel.
- A native sidecar fences ordinary transcript revival, including a last-moment
  check before opening a file. Tracking disposal leaves the TUI available. Explicit
  tracking-release evidence frees runner/workflow capacity without falsely claiming
  process-terminal observation. Human follow-up updates WT's native checkpoint.

## Supported subset and limits

Fresh persistent single-child runners, including individual children launched by
ordinary `runs.all`, and exact retained single-child public resume are supported.
Normal writer and reviewer profiles are not renamed or weakened. Existing package
model/thinking/tool/extension/skill resolution, current/inherited ceilings, hook
acknowledgements, event capture and checked acceptance remain in the runner.

Fork/in-memory/reopen launch, nested subagents, fast/external execution, managed
worktree creation, model fallback lists, structured-output-only completion and
unsupported management operations reject before child spawn. Resume rejects
profile/ceiling, context, skill, checkout and cwd changes instead of silently
rewriting them. Cancellation, host replacement and automatic crash replay are not
advertised. A failed/uncertain startup or queued continuation can require explicit
reconciliation; no recovery path invents completion or opens another writer.

Store bounds remain: 8 retained agents per root, 64 turns per conversation,
256 KiB contract, 128 KiB prompt, 128 KiB combined result/error, and bounded command
I/O. Ownership tokens fence stale cooperative processes, not hostile same-UID code.
WT never writes the native transcript; observed native completion is not the
package acceptance verdict. Session homes and explicitly attached checkout cwds
remain workspace guidance, not a filesystem sandbox.

## Concrete evidence

Task evidence root: `/tmp/native-host-continuation-1788743476`.
Final positive private experiment:
`/home/aidan/.cache/wt-real-staging.p_4ewjvd/experiments/native-workflow-1788743476-g`.
Its `assert-proof.py` and `authoritative-native-mapping.json` establish:

- ordinary two-worker `runs.all` in distinct attached checkouts; an additional
  normal reviewer has actual `read,grep,find,ls,contact_supervisor` tools;
- all child turns have fresh checked evidence; workers honestly remain
  `review-required`, while the read-only reviewer's acceptance is `checked`;
- public alpha resume reuses WT job
  `430b2e0ca5c6165cfaafae8c2c78941bca653c3d975393d08357d4c01c19f9c5`,
  native `01a079c0-b28f-70ac-a89f-18dc8b46800b`, transcript and host PID 296;
- distinct completed durable turns and turn-local read events; same supervisor,
  new child control channel; retained keyboard follow-up advances the native leaf;
- unchanged parent focus/host identities; unavailable-provider ordinary invocation
  returns an actionable no-fallback error and creates zero new jobs/children.

This uses genuine Pi SDK 0.84.4 and TUI, with a bounded synthetic HTTP model and
real read tools, not a fake SDK or real-provider credentials. Its private
PID/IPC/tmux/DB/home/config namespace was torn down. Earlier -d also proved the
exact two-worker workflow and resume. -e deliberately demonstrated that a readonly
ceiling cannot silently weaken a normal implementation-worker profile. -f exposed
a fixture misuse of `runs.run`; it is not counted as successful workflow evidence.

Final focused package checks: typecheck passed; 63 tests passed, one platform
start-probe test skipped. Focused WT `TestDelegation` tests, vet and build passed
from frozen disposable sources. The broad unchanged suites were not rerun.

## Remaining gate

Independent source/runtime review is required before any ordinary-session rollout.
Whole-tree lint is not clean: task-start baseline 10,397 diagnostics, current
10,468 (**+71 task diagnostics**, separately captured; no rule suppressions or
configuration changes). The inherited prior debt remains part of the baseline.
Crash/partial-publication/capacity cleanup, arbitrary configured extensions/skills
and real-provider failure modes need further coverage. The positive proof predates
only the final ownership recheck, trailing-JSON rejection, canonical parent-marker
lookup and last-moment native sidecar recheck; those received focused checks, not another live workflow replay.
No production/staging installation, live binary rebuild, old-job replay, source
staging, commits or push were performed.
