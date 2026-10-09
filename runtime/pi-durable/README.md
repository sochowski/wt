# WT interactive Pi Durable (modern new-session source default; final review pending)

Modern `wt new`, fresh setup-menu roots and modern-root `wt agents create`
now select durable. `--backend native` explicitly retains the native path.
Stored native/legacy sessions and legacy Git/non-Pi launches are not migrated.
This is a source change after private core gates, **not production activation**;
final independent review/product acceptance remains required.

Node 22.22.x; exact public durable/ai/chord 1.0.3 and pi-tui 0.87.1. No full
coding-agent SDK, shrinkwrap patch, private frontend imports, ordinary extension
host, native pi-subagents admission or ambient MCP. Production audit reports
zero vulnerabilities. `bin/wt-durable-bootstrap` installs only this checkout's
locked dependencies; install requires it when Pi is detected, before HOME
installation mutations. `WT_INSTALL_DURABLE=1` can request it without Pi.

## Validation (isolated; no provider credentials used)

```sh
npm --prefix runtime/pi-durable ci --ignore-scripts
npm --prefix runtime/pi-durable test
node --test runtime/pi-durable/private-jobs.test.mjs
node --test runtime/pi-durable/private-controls.test.mjs
(cd state && TMPDIR=/private/tmp go test ./...)
```

Use a canonical temporary directory appropriate to your platform for Go.
The integration test builds WT into a temporary directory, explicitly pins WT
paths and HOME in its private tmux server, and injects the published faux provider
through a **test-only** entrypoint. Production has no environment switch to load
that fixture. It drives production runtime/tools/admission/result/review/inbox
code with independent processes/stores, real writes and bash, SIGKILL at result
publication and inbox admission/ack boundaries, notifications, budgets, peers,
pins and focus preservation. Go build uses the caller's caches; if invoking the
runner under an additional fake HOME, pass GOMODCACHE/GOCACHE through.

`evidence/` retains runtime and private-integration TAP, production-entrypoint
Haiku proof, primary-provider diagnostic, audit and fake-HOME staging logs.
Stage-one package/evidence under `tests/pi-durable` remains unchanged.

## Commands

Use these only in an authorized isolated sandbox first (see repository AGENTS).
The CLI does not migrate old native transcripts:

```text
wt new NAME --cwd CANONICAL_DIRECTORY --backend durable
wt agents create ROOT PEER --parent PARENT_ID --cwd ATTACHED_ALIAS --backend durable
wt agents create ROOT READER --parent PARENT_ID --cwd ATTACHED_ALIAS --backend durable --read-only
wt agents durable-init ROOT FRESH_NEVER_LAUNCHED_PI_AGENT_ID
wt message wake ROOT --budget REMAINING_BUDGET
```

Fresh bootstrap requires explicit `defaultProvider` and `defaultModel` in the
normal Pi agent settings (or the existing `PI_CODING_AGENT_DIR` override) and a
model in the pinned public catalog. It **never silently selects Haiku instead of
an unavailable primary**. Credentials are read-only: fresh OAuth and ordinary
stored keys; no command keys, refresh, login, logout, or settings writes. Missing,
expired or unsupported auth must be configured separately using ordinary Pi.
Custom models.json/catalogs are not imported. Relaunch retains the stored model,
thinking, cwd, UUID and exact conversation, not a changed default or cwd-latest.

Fresh conversations read `modelThinkingLevels["provider/model"]` first, then
`defaultThinkingLevel` from that same agent `settings.json`, falling back to
`medium` when unset (native Pi semantics). Values are `off`, `minimal`, `low`,
`medium`, `high`, `xhigh`, or `max`; invalid values fail bootstrap with a visible
configuration diagnostic. Unsupported levels are clamped using the pinned Pi
model capability rules, with a visible warning and **no model change** (e.g. a
non-reasoning model uses `off`). Project settings are not imported. `/thinking`,
its picker and Shift+Tab change only the stored conversation, never global
settings. Resume/reopen does not read these defaults, even if settings have
subsequently changed or become invalid. Admitted job contracts and tool ceilings
are unchanged; worker reopen likewise uses its stored agent state.

Interactive input: Enter chats or queues followUp; `/steer TEXT`, `/followup TEXT`,
`/model PROVIDER/MODEL`, `/thinking LEVEL`, `/skill:NAME [TEXT]`, `/notifications`,
`/abort`, `/quit`, `/stop`, `/help`. Abort durably cancels; quit/close/crash leaves
recoverable work; WT stop prevents relaunch. Validated relaunch resumes pending
work. Named peers have independent stores/processes and survive parent stop.

Tools: coding read/write/edit/bash, explicit WT workspace/views/presentation/
independent-peer transports, and `wt_delegate`. Project context files load as
nonexecuting instructions. Skills use bounded scalar frontmatter in Pi/user
`.agents` directories; protected current-project skill directories require
`WT_DURABLE_TRUST_PROJECT=1`. No skill/extension/package scripts autoexecute.

## WT-owned delegation v1

`wt_delegate start` accepts one to four `{task,cwd,criteria}` entries. Cwd must be
an explicitly attached canonical git checkout root. An occupied canonical writer
lease is rejected: use an isolated checkout, not the parent's active writer cwd.
Root limit remains eight retained agents (writer **and** mandatory reviewer count),
so it can be tighter than parallel<=4. Each new task atomically reserves both
children, two wake permits and immutable task/result/review IDs. Retries with a
changed payload are rejected; retained reconciliation does not spend again.

The role/version/digest, parent store UUID/conversation, child stores/IDs, assigned
cwd and resolved tool ceilings are persisted. Writer tools intersect the parent's
ceiling. Reviewer has read and optionally wt_workspace, a read-only environment,
no generic bash, mutations, MCP, spawn or nested delegation. Job coding paths are
normalized to the pinned tool's actual semantics before authorization. Canonical
paths are executed through a per-call exact-path environment; final I/O and read
fallback variants are confined too. This is not a race-proof OS sandbox.

Writer acceptance requires structured criteria/result identity plus actual
host-observed baseline/after file hashes, unchanged HEAD, exact changed-path
claims and successful command receipts. Evidence includes ignored outputs, under the existing 4096-file/64-MiB limits.
Independent reviewer must read every changed artifact fully and bind each
criterion's `reads` references to successful relevant host receipts. Deleted
tracked artifacts use read path `wt://review-diff` over the retained host diff.
Error/empty/partial/truncated or unsupported binary reads do not count; such
artifacts cannot currently receive successful acceptance.
Host evidence also records semantic Git index mode/blob/path/stage entries and
assume-unchanged/skip-worktree flags. Writer success requires unchanged semantic
index state while preserving a preexisting staged baseline. This observes final
state only, not absence of transient staging. Recovered cached success revalidates
the current contract and exact current snapshot without rerunning unsafe tools
or rewriting immutable publication IDs. Reviewer must accept
all criteria against that exact writer snapshot; failed, absent,
uncertain or stale review is never success. Results/receipts are immutable.
A separate inherited-FD capability, not a tool-inherited environment token, is
required for host attestation. Human provenance is explicitly `not-observed`;
showing a deck is not human review. Durable task ceilings are enforced at
both worktree and top-level WT-state command admission, including ordinary writer
bash invoking legacy state/registry routes. Human `/stop` uses a separate private
self-only/current-runtime control; model tools/CLI still cannot override pins or
human focus, and the private capability is never inherited by tool subprocesses. Parent results arrive through commit-only
documents/UI and on the next authorized model turn, not a waking submission.

Unsafe tool effects are never promised exactly-once. SIGKILL after an effect but
before its host receipt leaves uncertainty and denies success. Replay-safe WT
reads revalidate the current fence in execute. Process ownership uses inherited
node/store/canonical-writer locks; this is not an OS sandbox against hostile
same-UID code. SQLite WAL/NORMAL does not promise host-power-loss durability.

## Fix-stage acceptance boundary

All six initial review blockers were corrected: transformed paths, human self-stop,
ordinary CLI task ceilings, meaningful changed-artifact review coverage, ignored
artifact snapshots and managed presentation transport. `evidence/fixes-*` records
24 runtime tests, compiled private delegation/crash/inbox and actual Neovim
presentation/attached-pinned self-stop proofs, full Go/build, install/audit,
native keyboard acceptance and the single approved primary smoke.

Post-fix production-entrypoint **openai-codex/gpt-6.1-sol** proof succeeded:
actual read, exact `WT_DURABLE_INTERACTIVE_OK`, committed done/no live work at
8.864s, runtime closed 8.973s, natural owner exit 11.993s, settled-store inspection,
auth unchanged. 1,491 input / 29 output tokens, $0.003272 catalog cost. This
supersedes the retained earlier exit diagnostic; no additional paid run occurred.

Modern default creation/menu/default peers and bootstrap/dependency failure tests
pass. Existing setup roots/resume never select another backend or relaunch agents;
bootstrap failure preserves stopped durable intent and setup journals. Native
stub fixtures explicitly request native. Final independent review is still the
acceptance gate; no production installation/reload/activation, commits or staging.

Known full-regression base failures remain outside scope. Focused native
agent-first/master-stack/native-install scripts also fail on the unchanged base
HEAD (count formatting, repeat navigation, package-shape assertion); their
unchanged assertions were not repaired. The canonical native keyboard fixture
passes after making its generated fresh-root bindings explicitly native.

Other providers/custom catalogs, expired OAuth refresh, human provenance,
hostile same-UID access, exhaustive crash points and host-power-loss durability
remain unsupported/unverified. Bounded evidence can reject large/ignored trees
and artifacts that cannot be fully read; do not label this universal Pi parity.
