# Explicit settled-native cold recovery

This source change pairs with the `sochowski/pi-subagents` cold-recovery change
based on `wt-provider-contract`. It does not install/activate either checkout,
automatically replace native hosts, migrate Durable stores, or complete Durable
plugin/subagent compatibility.

## Operation and ownership

The package's explicit `resume` operation with `nativeColdRecovery: true` admits
one new instruction for an existing settled native conversation. Ordinary warm
continuation never silently launches a new process after ESRCH.

WT schema 7 retains an exclusive operation lease, original admission and actual
checkpoint fingerprints. Private bounded control operations prepare/claim/queue
that lease, allocate a new physical ownership epoch under root and original node
locks, authorize **one** SDK open, and bind the actual SDK identity. This is not a
new logical agent/view/worktree slot and never impersonates the old PID.

Only successful settled turns are eligible. A definitely unpublished failed
suffix may be retained without erasing history. Pending, interrupted, claimed or
uncertain model/tool work is not replayed. Declared legacy finite budgets without
authoritative retained accounting refuse; no counter guesses or reset.

The public SDK verifies UUID, file, committed leaf and cwd on a private byte-copy
before opening the original. WT and the actual SDK enforce identity/model/thinking
and original role/resources/permissions/acceptance contract preservation.

## Placement and publication

Reuse only the same original managed **dead, unfocused** pane. A live repair shell,
unknown death/focus, pinned view, human focus or another manager refuses. Placement
is rechecked before respawn; protection drift after claim retains uncertainty.
An absent original pane can be placed in a detached background window without
creating a substitute logical view.

`runner-startup-ready.json` observes the exact real runner at its barrier. It is
not SDK creation or dispatch authority. The startup proceed token must match.
Only the actual SDK owner, after binding and one-shot WT dispatch claim, records
`native-publication-observed.json`; launch/readiness alone never means published.
Prepared unconsumed operations may be cancelled. Claimed/uncertain operations do
not expire, permit automatic reaping, or construct a second SDK writer.

## Private validation

Default regression:

```sh
cd state
GOPROXY=off go test -count=1 -timeout=95s ./...
```

Run under an isolated HOME, WT_DB, WT_STATUS_DIR and WT_CONFIG_DIR, passing Go
caches explicitly as described in `AGENTS.md`. Never point tests at live state.

Actual SDK/compiled WT/PTY/private tmux gates (in that isolated environment):

```sh
export WT_NATIVE_SDK_TEST_MODULE=/path/to/published/pi/dist/index.js
export WT_COLD_SDK_FORK=/path/to/paired/pi-subagents/checkout
export WT_NATIVE_TMUX_TEST=1
go test -count=1 -timeout=120s -v \
  -run 'TestNativeRecoveryFullHostedRunner|TestNativeRecoveryCompiledControlsWithActualSDKPTY|TestColdNativePlacement' ./...
```

The full hosted test uses the actual package runner, compiled launcher, private
server/socket, same original pane, root/node locks, real public SDK, loopback-only
synthetic inference and actual publication/result receipts. It tests success and
SIGKILL at the observed startup barrier. Additional four SDK/control SIGKILL cases
cover post-open, post-bind, post-claim and model-request boundaries. Reopened
ledgers refuse consumed SDK permits/epochs and preserve the old settled turn.

Genesis is labelled settled SDK fixture data, not a live retained session. These
proofs do not assert external-effect exactly-once, interrupted-writer recovery,
hostile-same-UID isolation or independent review of the frozen M2 work.
