# Ordinary WT interactive delegation — corrective integration

User reconfirmed the original approved goal after observing `scratch-3` spawn two
headless `worker` children: ordinary `subagent` orchestration must create retained,
interactive WT child conversations. The special opt-in demo was not delivery of
this requirement. No further skill-only fix or special-profile-only proof counts.

## Evidence and existing implementation

- WT checkout: `/home/aidan/worktrees/wt/aidan-wt-rearchitecture`; large existing
  dirty redesign/menu/workspace changes must be preserved.
- Local upstream: `/home/aidan/worktrees/pi-subagents/wt-provider-contract`, base
  `6c1af3a2ad84c2c58f6ccec10aabe70b634fd74e`; existing uncommitted contract patch.
- Read `docs/provider-staging.md`: opt-in adapter only, no production deployment.
- Staging: `/home/aidan/.cache/wt-real-staging.p_4ewjvd`, private namespace/tmux.
- Staged `bin/pi` selects `experiments/provider-pi` ONLY when cwd equals
  `experiments/wt-provider-demo`. New session homes use ordinary `pi/` settings,
  which still load stock pi-subagents.
- Patched plugin copy: `experiments/pi-subagents-patched`; provider extension:
  `experiments/provider-pi/extensions/wt-provider.js`; adapter:
  `experiments/delegation-prototype/contract-provider-v2.mjs` and `provider.mjs`.
- Scratch-3 root `60373e851eb522e68bb52385b59beec2`, main agent
  `7972872d5e0cf21755bd09195e0b2939`, native
  `01a07937-922c-75df-b79e-1788da3754c5`. Root has main + two diff views, no WT peers.
  Actual normal workflow `416ad303-6e90-4b71-9b07-e8b512473b20` called runs.all
  with two builtin workers, explicit separate attached-checkout cwd, outer
  context:fresh, async:true, checked acceptance. This reproduced the integration
  gap, not a missing/parked pane problem. Preserve all that work and its runs.

## Required outcome

1. Ordinary WT staging sessions, including new workspace cwd sessions, use normal
   `subagent` / runs.run / runs.all orchestration with interactive WT children.
   No need to ask for `wt-peer`, switch to direct wt_agent calls, use a demo cwd,
   or run a bespoke one-off launch script. Support ordinary writer and read-only
   role profiles without weakening their actual effective contracts.
2. WT-required execution is an explicit enforced routing/admission choice. Missing
   provider, unsupported runner/options, recovery mismatch or child-start failure
   must produce an actionable rejection — never silent headless fallback. Don't
   solve this with tool-description text or an agent-name suggestion.
3. Keep profile identity/instructions, effective cwd, model/tool/extension/skill/
   capability ceilings, parent supervision and digest-bound job identity. Null
   provider-default tools and an empty allowlist are different. Never drop tools,
   restrictions, acceptance requirements, or fork context just to pass admission.
   If a requested feature is not supported, say so before spawning. An advertised
   supported subset is acceptable; unrestricted wt-peer relabeled worker is not.
4. Each accepted job binds exactly one WT child and native conversation, in that
   session and intended checkout, with a readable peer label separate from stable
   job identity. Completion and exact follow-up/resume reuse that child; no replay,
   duplicate writer, cwd-latest restore or headless shadow. Demonstrate live panes
   and authoritative mappings, not an observer log pane pretending to be a child.
5. Use bounded durable completion/admission/recovery plumbing. Existing prototype
   file intent + whole inbox scan + prompt-only completion is not shipped parity.
   Implement the bounded job/result seam required for this integration rather than
   making agents responsible for forged/guessed completion receipts. Report any
   remaining prototype limitations explicitly. Do not claim cancellation parity:
   reject unsupported operations or accurately expose tracking-vs-child lifetime.
6. Preserve master-stack/focus/ownership, independent per-agent views, workspace
   guidance (not a sandbox), readable checkout aliases, no-lazy-fetch/plan binding,
   exact native recovery, inbox and all prior menu fixes.

## Authority and process

One writer across the two authorized local source checkouts. Capture task-start
baselines and narrow deltas separately. Public upstream-facing contract changes
are preferred over private permanent forks; no publication is authorized.

No commits/staging/push/PR, production install/reload/state/tmux changes, credential
printing, real source-repo fetches, or edits to any existing staging conversation,
checkout, global routing, settings, or agent process. In particular never restart,
replay or stop scratch-3 or its headless jobs to make the outcome appear correct.

The writer may use new uniquely named experimental directories and test-only roots
inside isolated staging for real-Pi proofs after isolated tests. Use a dedicated
private DB/socket as well, so new schema/code cannot migrate or affect the shared
staging server before parent rollout. Copied configs, private local-repo fixtures
and explicit namespace.py are required; don't change
existing globals or real-agent/stub routing for other sessions. Parent owns final
ordinary-staging rollout, user-focus protection and delivery claims.

Read AGENTS.md, pi-subagents guidance and complete relevant Pi docs/examples before
API changes. WT test.sh rebuilds bin/wt-state: run it from a frozen disposable
SOURCE COPY, not the live checkout. Tests use private HOME/config/state/socket,
local Git remotes and stub agents except specifically authorized staging proofs.

## Acceptance

- Exact scratch-3-style ordinary API: two normal workers, runs.all, distinct cwd,
  fresh context and checked acceptance -> two live interactive WT child views,
  no headless duplicate, proper output reports and one authoritative job binding.
- Ordinary read-only profile retains its actual tool restrictions; no incidental
  registration of mutating tools or ambient extensions expands capabilities.
- Explicit unsupported features/missing provider fail closed with zero children.
  Include ambient/profile/session-owner ceilings and empty-tool-list cases.
- Resume/reattach verifies exact parent/provider/job/native identity, no redispatch;
  same child stays interactive after completion. Preserve existing parent cwd,
  native identity and attached-client focus. Test default new session home and
  attached checkout cwd, not only the original demo directory.
- Upstream focused/unit/integration/typecheck and lint delta relative to current
  task baseline; existing +26 policy diagnostics remain explicit, not ignored.
  WT Go/vet/temp build, Node, regression, relevant lifecycle/Neovim/keyboard tests.
- Independent fresh contract/safety and integration/UX reviews after the writer;
  source-backed results and bounded logs. Parent handles blocker disposition.

Escalate genuine architecture/authority choices before widening scope. Send a
short milestone once routing/profile enforcement strategy is established, rather
than spending an entire run building another opt-in demonstration.
