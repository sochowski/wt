# Bounded native plugin compatibility (explicit new stores only)

This is a review checkpoint, not an installed rollout or full native Pi parity.
Default/stored `wt-durable-v1` identities and dependency strings remain unchanged.
An explicit `wt-state worktree new NAME --durable-profile native-compat-v2` (or
`worktree agents create ROOT NAME --durable-profile native-compat-v2`) requests a
**fresh** `wt-durable-native-compat-v2` store. Native backend plus this flag is
rejected. Existing stores cannot adopt this profile.

Only the actual published `@juicesharp/rpiv-ask-user-question`, `rpiv-todo`, and
`rpiv-config` 2.9.0 packages are loaded. Dependencies are exact-pinned; project
`.npmrc` excludes their native SDK peer. `npm ci --ignore-scripts` is reproducible
without installing the vulnerable coding-agent SDK. The scoped Jiti loader
resolves only the inspected SDK imports to WT's version-2 public-export adapter:
`DynamicBorder`, `getMarkdownTheme`, and an explicitly rejecting
`SettingsManager.create()` external-editor boundary. Markdown/syntax rendering
is checked against disposable published SDK 0.87.1 / highlight.js 10.7.3 output.

Actual factories register schemas, prompt guidelines, tools, `/todos`, widgets,
shortcuts and questionnaire components. The public durable scheduler, real UUID,
linear committed message projection, role/tool ceilings, and WT fencing remain
owners of inference, storage and permissions. There is no native `AgentSession`,
JSONL session identity, competing inference runtime or arbitrary extension
search. The two plugin tools are sequential and replay-unsafe. Native todo
business errors retain the actual package's `details.error` envelope (which does
not set `isError`); WT receipts classify these as failed, not accepted success.

## Receipts and recovery

Fresh bootstrap persists canonical schema/tool/capability descriptors. The
admitted identity includes immutable source/config and descriptor SHA-256
fingerprints. Missing packages, changed metadata/source/config/descriptors,
operation digests, identities or schemas fail closed. Version-1 stores never
load/check these plugins or adopt settings changes.

Before loading **any actual plugin module or factory** on reopen, WT verifies
its application identity and uses public Storage document/entry reads to inspect
operation records and genuine task/call tool-result receipts. A clean reopen
may initialize exact factories and reconstruct genuinely receipted todo state.
A separate startup barrier also inspects the actual public `pi.live` v1 document
and its persisted task state. **Any unfinished v2 core run**, even with entirely
receipted plugin tools, cannot automatically resume model/task scheduling,
accept input/configuration changes or poll inbox/jobs. Clean actual factories,
receipted todo reconstruction and read-only UI remain available; completed tools
are not relabeled uncertain. The notice identifies the interrupted run and warns
that provider continuation is not exactly-once. Explicit human continuation is
**unavailable at this checkpoint**: public `Harness.resume()` is owner-wide,
without run-scoped/deduplicated authorization and can also drain retained queued
inputs. WT does not guess authorization from old queue IDs or receipts.

An interrupted/unreceipted plugin operation instead selects a **WT-owned nonexecuting
read-only inspector**. Exact persisted tool descriptors remain in the registry
as explicitly disabled tools, not an omitted/fabricated native contract.

In that uncertain mode no actual plugin module, factory, lifecycle, native
widget/overlay, input, tool, answer, journal, automatic inbox/job wake or model
request is replayed. `/plugin-inspect` displays separately labeled committed
receipts and uncertain records, including candidate todo state or answers; a
candidate is never promoted merely because it exists. Original records/UUIDs
are preserved. New work, model/thinking changes and abort mutations are blocked;
close and private human WT stop remain available. Separately authorized recovery
initialization/new-work control is **not implemented in this checkpoint**.

Limits: 128 plugin operations/store, 256-byte call IDs, 64 KiB arguments/answers,
128 KiB candidate results, 512 questionnaire input records / 32 KiB journal,
64 KiB / 2000-line model output, and 4096 preflight history entries. Exhaustion
fails explicitly rather than silently pruning or accepting uncertain effects.

## Explicit limitations

- Only the inspected two packages/API subset is supported. Unknown API methods,
  tools, commands/events and unsupported UI placement fail explicitly.
- Read-only/delegated ceilings cannot activate plugin tools/dialogs/commands.
- No external editor, package discovery/reload, native branches/forks, appendEntry
  API, native compaction/tree controls, native per-tool render-hook parity, MCP,
  web or custom subagent integration. WT's existing durable tool history remains
  the result renderer; actual questionnaire and todo widget components are used.
- Uncertain recovery is inspection, **not native overlay restoration**.
- Four owned-process SIGKILL boundaries (plus instrumented actual-package
  factory/module/lifecycle sentinel processes) are tested (pending, answered,
  state/candidate-written, result-receipted), not exhaustive power-loss behavior.
  No external-effect exactly-once, OS sandbox or hostile-same-UID claim.

The rendering golden fixture was generated through the disposable public
`@earendil-works/pi-coding-agent` 0.87.1 exports under fake HOME with true-color
mode, using its dark theme and actual highlighter. No SDK code/dependency was
copied into the production closure.
