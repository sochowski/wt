# WT-backed pi-subagents: isolated staging prototype

## Current status

The local upstream provider-contract patch and an opt-in WT consumer have passed
an actual interactive Pi end-to-end experiment. Nothing was pushed, committed,
installed into production, or reloaded in production.

**This is an experimental staging consumer, not a shipped WT provider.** Existing
`worker`/`reviewer` profiles are unchanged. The `wt-peer` profile exists only in the
isolated demo checkout.

## Try the retained demo

From a terminal:

```sh
/home/aidan/.cache/wt-real-staging.p_4ewjvd/enter-provider-demo
```

This attaches the private tmux server's `wt-provider-demo` root. Its main agent
loads the locally patched package, while the previous `staging-ready` root keeps
its existing configuration and conversations.

Inside staging, these commands focus the retained conversations:

```sh
wt agents open wt-provider-demo main
wt agents open wt-provider-demo job-2441fb3ef1a2df47
```

Ask the main agent to use `subagent` with agent `wt-peer`, `async: true`, and
`context: fresh`, omitting native execution options. Alternatively, the child
window remains an ordinary interactive Pi conversation. Do not type into it
while a delegated job is active.

## Observed proof

The main Pi used the regular `subagent` tool, not direct provider callbacks, to
launch a child and then `action: resume` the resulting run. Both results came
from the same WT child and native conversation:

- First output began `WT_CONTRACT_FIRST_OK`.
- Follow-up output began `WT_CONTRACT_SECOND_OK`.
- Pi-subagents appended automatic acceptance-report instructions; returned
  outputs include those reports rather than being bare tokens.
- Exactly one child was created; both agents remained alive and idle afterward.
- No headless duplicate or second transcript writer was created.

Evidence is in the staging directory:

- `experiments/provider-workflow-evidence.json`
- `experiments/provider-contract-local.patch`
- `state/provider-contract-demo/`

Two preliminary attempts failed before creating a child: the adapter used a root
ID with WT's name-search API, and a reload retained the old imported module. The
root lookup was corrected and a new module path loaded before the successful
experiment. Their failure records remain visible; they were not erased.

## Local upstream patch

Checkout: `/home/aidan/worktrees/pi-subagents/wt-provider-contract`.
Base: `6c1af3a2ad84c2c58f6ccec10aabe70b634fd74e` (0.66.0).

Versioned provider admission transports resolved restrictions and digest-bound,
durable launch/recovery context. Public resume verifies the durable parent job
binding. Effective inherited chain skills are rejected before external dispatch.

Latest checks after those two review fixes:

- Unit: 3,004 passed, 14 skipped.
- Integration, concurrency 4: 1,012 passed, 6 skipped.
- Typecheck passed.
- Policy lint is **not green**: the worker measured 1,136 diagnostics versus
  1,110 on the corresponding unchanged source seams. These are not all baseline
  diagnostics; the delta still needs resolution. No merge-ready claim is made.

Logs: `/tmp/provider-parent-{unit,integration,typecheck,focused}.log` and
`/tmp/provider-contract-lint-{baseline,final}.log`.

## Boundaries and unfinished work

- The consumer rejects requested native tool/extension/ceiling and other native
  execution options it cannot enforce. Only provider-default execution is offered.
- Provider default permissions are not a process sandbox.
- Stop/timeout ends local tracking; it does **not** terminate the interactive child.
  Provider cancellation/steering parity is not implemented.
- The consumer still uses the earlier prototype's file-based dispatch intent,
  inbox scan, and prompt-directed completion receipt. A bounded, transactional WT
  job/result primitive and hardened recovery tests remain needed before shipping.
- The real demonstration exercised start and exact follow-up, not a crash/restart
  recovery scenario or all workflow orchestration combinations.
- The patch is local and uncommitted; no PR, issue, push, or upstream contact was
  made. The installed production pi-subagents package remains untouched.
