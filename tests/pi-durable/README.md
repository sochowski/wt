# Real Pi Durable contract probes (stage 1 only)

These exercise the published **1.0.3** harness, SQLite backend, scheduler, tools,
and faux provider. They are not a WT backend or a mocked harness. Node >=22.19
is required; verified on 22.22.0. The lockfile pins the dependency closure.

```sh
cd tests/pi-durable
npm ci --ignore-scripts
npm test
```

`run-tests.mjs` launches with a disposable HOME, no provider credentials, and
isolated WT paths. Fixtures have separate temporary SQLite stores. Child workers
are actually SIGKILLed, not gracefully closed. Bash/write/edit perform tiny local
fixture effects, then a wrapper blocks after committed progress, before a result.
The read replay test explicitly wraps upstream read with `replay: "safe"`; bundled
read, like the other CodingTools, defaults to unsafe. Test-only effect counters
are observations, not an exactly-once implementation.

Coverage: admission concurrency and same-type mismatched retries; conversation
scoping; close/reopen vs abort; changed model/cwd/thinking/provider session ID;
read-only snapshots and late watch updates; cancelled waits; durable steering,
follow-ups and writes including crash/reopen; doc-only non-waking notifications;
passive submit(write) scheduler side effect; foreground/background/ownerless
ownership; both stored and current replay policies; interrupted generation;
unsafe bash/write/edit; absence of process-exclusive storage locking.

The negative lock probe opens the same store from a second process but does not
run two writers. It demonstrates why WT must fence ownership, not that concurrent
writer use is safe. Tests do not promise power-failure durability or cover every
crash instruction boundary. Cleanup removes fixtures, including recovered DBs.

## Public API investigation (no terminal started)

With an explicitly selected installed coding-agent SDK, under temporary HOME:

```sh
D=$(mktemp -d)
HOME="$D" WT_PI_SDK_PATH=/absolute/path/to/pi-coding-agent \
  node tests/pi-durable/api-probe.mjs
rm -rf "$D"
```

The probe records public exports and renders a generic `Text` component headlessly.
It asserts the observed 0.87.1 export contract; it is intentionally not advertised
as a version-independent TUI compatibility test. The upstream durable frontend
is source-only, not a public exported frontend.

## Opt-in real provider smoke

Not part of `npm test`. Requires authorization to spend tiny provider usage and a
fresh existing Anthropic OAuth credential. Uses public `readStoredCredential`
from the chosen SDK, supplies a read-only pi-ai CredentialStore, and rejects all
credential modifications (including refresh). Never logs tokens, headers,
provider error objects, or auth-file hashes. Does not discover extensions/settings.

```sh
D=$(mktemp -d)
HOME="$D" WT_DURABLE_REAL_SMOKE=1 \
  WT_PI_SDK_PATH=/absolute/path/to/pi-coding-agent \
  WT_PI_AUTH_PATH=/absolute/path/to/auth.json \
  node tests/pi-durable/smoke.mjs
rm -rf "$D"
```

At most three model requests, 256 output tokens/request, retries/compaction off,
20-second request timeout and 60-second total deadline. Only read is offered;
the tool cwd is a disposable fixture. Asserts actual read, exact answer,
close/reopen settlement and unchanged auth file. Verified smoke used two requests
on `anthropic/claude-haiku-4-5-20251001`. Other providers, expired-token refresh,
network interruption and a full interactive terminal are not verified.

## Evidence

- `evidence/contracts.tap`: 17 passing contract tests, eight actual SIGKILL cases.
- `evidence/api.json`: installed SDK/TUI 0.87.1 public export probe.
- `evidence/smoke.json`: bounded live provider success (no secrets).
- `evidence/versions.json`: installed exact dependency tree of relevant packages.
- `evidence/regression.txt`: WT regression result and base HEAD comparison.
- [Implementation plan](../../docs/pi-durable-plan.md): verified constraints and
  the next-stage implementation map. No WT launch/default changes in this stage.

Upstream source was audited at v1.0.3,
`d78dc83d633229d12f8b79631384c4c2717c399f`, matching npm `gitHead`.
Relevant executable examples: durable 13, 14, 20–24; experimental durable
runtime/sessions/prompt/TUI; public package declarations and compiled sources.
