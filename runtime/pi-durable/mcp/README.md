# Durable MCP boundary — staged, not activated

This directory is the actual-package foundation for issue #73. It is not yet a
WT profile or an exposed MCP tool. No ordinary/native/v1/v2 launch imports it,
no existing store migrates, and the normal installer does not provision it.

`loader.mjs` loads only the public `pi-mcp-adapter/host-managed` entry from the
actual pinned 5.1.0 package. It never loads the ordinary extension, auth/keyring
profile, native SDK session engine, or a tool lookalike. The embedding host owns
transport, approval, dispatch records, and adapter lifetime. No broker means
refusal. One `dispatch()` handle can send only once, but that alone is **not**
Durable deduplication or remote exactly-once execution.

The only SDK alias is `sdk.mjs`, four public output-export semantics checked
against disposable published Pi 1.0.4 public exports. It does not emulate a
session manager or silently unsupported APIs. `public-output-golden.json` tests
empty/trailing-newline/line/byte/Unicode/oversized-line boundaries and native
format-size behavior. No native SDK is in this directory's locked closure.

## Dependencies and tests

MCP client/core are exact-overridden to 2.2.0 rather than the adapter's vulnerable
2.0.0 pins (GHSA-6qxp-vccf-f47h). Root Durable packages and their v1/v2 dependency
strings remain unchanged. The standalone lock is intentional: adding this
closure to an existing v2 store would violate its immutable contract.

```sh
cd runtime/pi-durable/mcp
npm ci --ignore-scripts
npm test
npm audit --omit=dev
```

The SDK 1.31.0 **dev dependency** hosts private synthetic loopback servers only;
production uses the real 2.2.0 client/core and actual adapter. The test runner
clears ambient credentials/proxy/state variables and gives every test a private
HOME/config/TMPDIR. Tests exercise real discovery/schema/dispatch, broker refusal,
errors, cancellation, post-effect disconnect uncertainty, teardown, and actual
bounded output/spill behavior through WT's narrow bridge. No paid inference,
native session or production MCP server is involved.

## Harness receipt library (still not a WT profile)

`boundary.mjs` and `store.mjs` now compose the actual adapter with public
Harness documents/tasks/entries. They persist initialization intent before
transport discovery, immutable owner/source/config/catalog descriptors, and
bounded tool admission/dispatch/remote-result/candidate journals. A candidate
is not success: preflight verifies the authoritative assistant request, exact
public tool task and matching committed `pi.tool-result`. Failed and ambiguous
outcomes remain distinct. Rehashing a changed candidate cannot create a receipt.

`preflightMcpBoundary` runs committed storage reads before the caller opens the
Harness. Incomplete initialization, unreceipted/ambiguous operations, or unfinished
core runs are held. `reopenMcpBoundary` refuses these stores before any actual
factory, approval, transport or model effect. It never resumes retained work;
owner-wide Harness.resume is not run-scoped continuation authority. Calling code
must not admit input/config changes or poll inbox/jobs for a held store.

A **required** `guardModels(models)` preserves the actual public model registry
and fences its stream entrypoints, leaving Harness the sole inference owner.
Ordinary hook exceptions are reported and can be ignored by the scheduler;
they are not a hard model-effect fence. The guard refuses further provider
requests after live ambiguity or a stale owner hook. Do not omit it when binding
the extension. No abort mutation of retained work is used as a workaround.

Library tests use an explicit private `mcp-boundary-v1` owner contract and real
Harness/SQLite/faux inference, not WT CLI admission. This name is **not yet an
accepted CLI profile**. Existing v1/v2 WT application envelopes cannot be adopted
by changing a claimed profile. Read-only/delegated roles, process/auth/proxy
options, ambient output-guard overrides, unlisted tools and stale identities
reject. Current transport scope is explicit credential-free HTTP(S) server URLs,
no redirects or automatic reconnects; broader auth/server modes are pending.

Six actual process-kill checkpoints cover initialization intent, admission,
dispatch intent, remote result, normalized candidate and committed result before
provider continuation. Tests verify no factory/transport/approval/request replay,
including a fully receipted tool whose core run is still unfinished. These are
owned-process SIGKILL proofs, not exhaustive power-loss or remote exactly-once
claims. Capacity: four servers, sixteen selected tools/server, 128 operations,
256-byte call IDs, 16 KiB arguments, 128 KiB candidate, 4 MiB journal, 4096 history
entries. Source scanning is cold-start-only; model refusal is an O(1) gate.

## Remaining activation gates

1. Add bounded gateway discovery/describe/call, caller/server/tool permission
   ceilings and a separately defined scripting authority contract. Unknown
   APIs/options must fail visibly; never fabricate native owner identity.
2. Complete role/budget/output/PTY/lifecycle gates and actual
   native independent review before product acceptance. Read-only native review
   is still unavailable in the current Durable parent; writer tests are not it.
3. Explicit fresh-store CLI profile, wire the mandatory model guard and all
   admission/inbox/job barriers, separate activation approval, no silent
   credential refresh/login, settings writes, dependency repair or store migration.

Merging this library does not claim full #73 completion or install authorization.
