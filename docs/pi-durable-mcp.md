# Actual MCP tools in a fresh Durable profile

WT can now create an explicit `mcp-boundary-v1` conversation with actual
`pi-mcp-adapter@5.1.0` host-managed tools. This is usable direct-tool support,
not merely registered names: tools are discovered from the selected servers,
normalized by the actual adapter, and executed through its broker/transport.
Harness/SQLite remains the sole inference/state owner. No native SDK/session
engine, guessed native identity, ambient extension or credential replacement.

## Create

Provision the locked closure with the normal installer/bootstrap (script-disabled).
Choose a **canonical absolute** JSON file and exact model-facing tool ceiling:

```json
{
  "servers": [
    { "name": "example", "url": "https://mcp.example.com/mcp",
      "tools": ["lookup"] }
  ]
}
```

```sh
wt new mcp-work --durable-profile mcp-boundary-v1 \
  --mcp-config /absolute/canonical/mcp.json --mcp-tools example_lookup
```

This is human-only fresh-store admission. `--backend native`, `--read-only`,
delegated child roles, MCP flags on another profile, unsupported config fields,
credentials in URLs, query/fragment tokens, duplicated/oversized selections and
noncanonical config paths reject. Existing native/v1/v2 stores do not migrate.
An incorrect selected tool/model-facing name fails actual catalog admission;
WT never silently substitutes another tool. Original MCP names containing dots
are normalized by the adapter; supply its exact emitted tool name in the ceiling.

The config file is captured and SHA256-sealed read-only. Later edits, source or
schema changes refuse reopen. Root/model/thinking, instructions/skills, role
ceilings, writer/store leases and WT runtime fencing remain authoritative.
Model/thinking selection is conversation-local; native settings are untouched.

## Use

- `/mcp list` displays the real, immutable discovered catalog.
- `/mcp describe NAME` displays its actual description and input schema.
- Ask the model to call a configured tool. Each remote call opens a WT-owned
  **deny-by-default, allow-once** dialog showing server/tool/arguments. Escape,
  Enter on the default deny choice, close, or timeout refuses the call. No UI
  means deny, not implied consent; no persistent/session-wide grant is offered.
- `/mcp-inspect` reads retained contract/receipts; it never sends a remote request.

Human approval is the adapter's actual approval-broker event, not a fabricated
native tool. This is cooperative tool fencing, not an OS/network sandbox around
ordinary coding tools such as bash.

## Supported / explicitly not supported

| Capability | Current behavior |
| --- | --- |
| Actual discovery/schema/direct call | Supported via real host-managed adapter |
| Explicit server/tool ceiling | Immutable, checked before execution |
| Cancellation, errors, output bounds, teardown | Actual package behavior + WT receipts |
| Credential-free HTTP(S) | Supported, redirects/reconnects refused |
| URL credentials, OAuth/login/refresh/keyring writes | Refused |
| Stdio/process/package auto-install | Refused |
| MCP Apps/browser UI and sampling | Not exposed in this profile |
| Model gateway `mcp` / `mcpScript` | Pending; do not advertise parity |
| Custom subagent/council integration | Separate milestone |

Auth, transport and scripting extensions need their own authority/replay contract,
not silent native SDK API emulation. Direct support does not close issue #73 in
its entirety.

## Uncertainty and recovery

Bootstrap commits an exact WT intent before discovery and seals only the store
created by that invocation. A failed bootstrap is not permission to adopt/retry
an existing unidentified store. Operation journals distinguish admission,
dispatch intent, remote result, candidate and authentic committed tool result.
The actual assistant request/tool task/result identities, immutable source/config/
catalog and output bounds are checked. Recorded candidate/answer/result hash is
not accepted success without a genuine matching Harness entry.

Preflight runs **before** factories, transport, approval and provider effects.
Uncertain effects or unfinished core runs open only disabled retained tool
descriptors and a read-only inspector: no new input/config/abort mutation, model
request, inbox claim/ack, job wake or remote replay. Even a fully receipted tool
cannot resume an unfinished provider run without real run-scoped authorization;
owner-wide Harness.resume is not that authority. Close/private human WT stop
remain available. No remote exactly-once or automatic effect rollback claim.

The mandatory public Models guard refuses provider entrypoints after live
ambiguity/stale ownership; ordinary hook exceptions alone are not a hard fence.
Default/legacy runtime behavior is unchanged; this guard is scoped to MCP.

## Validation / acceptance

Private actual-package tests cover all receipt boundaries, six owned-process
SIGKILL cases, owner/config/task tampering, broker denial, cancellation, ambiguity,
provider refusal, held input/config/inbox/job barriers and real terminal approval.
Compiled WT/private PTY tests exercise the real Go admission, inherited locks,
actual loopback call, denial, exact reopen and private human stop. No live servers,
credentials, provider probes, production-state tests or restarts used for tests.

Native independent source review remains outstanding in the current Durable
parent. Owner-authorized merge/install exceptions are not independent acceptance.
