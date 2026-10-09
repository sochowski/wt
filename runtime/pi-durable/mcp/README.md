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

## Remaining activation gates

1. Persist exact owner/source/config/catalog identities and remote dispatch
   intents in Harness/SQLite; distinguish candidates from tool-result receipts.
2. Run uncertain-store and unfinished-core preflight **before** factories,
   transport/discovery, credentials, model, inbox and job effects. No automatic
   remote retries or reconnects on uncertain work.
3. Add bounded gateway discovery/describe/call, caller/server/tool permission
   ceilings and a separately defined scripting authority contract. Unknown
   APIs/options must fail visibly; never fabricate native owner identity.
4. Private process-kill/reopen, role/budget/output/PTY/lifecycle gates and actual
   native independent review before product acceptance. Read-only native review
   is still unavailable in the current Durable parent; writer tests are not it.
5. Explicit fresh-store profile, separate activation approval, no silent
   credential refresh/login, settings writes, dependency repair or store migration.

Merging this library does not claim full #73 completion or install authorization.
