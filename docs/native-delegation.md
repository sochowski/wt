# Native Pi delegation source contract

Ordinary WT Pi startup (`wt new`, named agents, and the menu) requires native
`pi-subagents:required-native-provider:v1` acknowledgement with
`wtNativeProviderContract: 1`. Older v1 listeners lacking this paired-source
contract marker are disposed and refused before delegation. `subagent`, including
`runs.all` of single children, runs package-owned interactive children in detached
WT windows. Missing, old, disabled or failed-to-load packages block delegation;
there is no headless fallback. Restricted children get tool-free host protocols,
not the ambient WT tools. Unsupported nested fanout is rejected before reservation.

## Paired source installation (pinned fork)

WT uses our [`sochowski/pi-subagents`](https://github.com/sochowski/pi-subagents)
fork, maintained on `wt-provider-contract`. The reviewed dependency pin is
**`4077d3b432bfbfc326191fb0fe05fbccba665484`**, not the moving branch tip.
The npm package version string **does not identify a compatible published
release**.

For a fresh persistent checkout:

```sh
revision=4077d3b432bfbfc326191fb0fe05fbccba665484
source_dir="$HOME/.local/share/wt/pi-subagents/$revision"
git clone --single-branch --branch wt-provider-contract --no-checkout \
  https://github.com/sochowski/pi-subagents.git "$source_dir"
git -C "$source_dir" checkout --detach "$revision"
(cd "$source_dir" && npm install --no-audit --no-fund --package-lock=false)
WT_PI_SUBAGENTS_SOURCE="$source_dir" ./install.sh
```

If that checkout is already installed, verify its HEAD matches the pin and use
only the final installer command. Do not reset or overwrite an existing modified
checkout. Keep Pi's package entry pointed at this same commit-specific local
path; it will not move when the fork branch or npm package is updated.

The installer adds that local package path to Pi user settings and installs the
ordinary WT extension. Local Pi packages are references, not copies: keep both
checkouts and their dependencies available. Do not use temporary/staging paths.
Unrelated settings are preserved. Remove an old pi-subagents package entry first;
do not enable multiple copies. The runtime event acknowledgement, not the
manifest or an npm version guess, is the final compatibility check.

No PR to `nicobailon/pi-subagents` or upstream package release is required for
this fork-backed WT change. Future dependency updates should select a newly
reviewed commit in a new persistent directory and revalidate the paired contract.
Adopting an upstream release can be a separate decision later; nothing here
publishes an npm package or asserts that 0.66.0 on npm has this contract.

For development run install only in a disposable source copy and fake HOME,
with isolated WT paths and real Go caches passed through. Do not activate or
reload existing managed Pi sessions to test it.

## Continuation and failure semantics

Public native resume continues the exact retained conversation/PID/writer with
fresh per-turn hooks and result evidence. Default acceptance preserves the
original admitted requirement (including independent review); changed acceptance,
role, resources or current/inherited ceilings still fail immutable admission.

`native-publication.json` records exact owner, job, turn, host PID and process
instance before continuation publication. `not-published` means WT confirmed the
matching queued turn failed and package capacity can be released. `uncertain`
means inspect that exact retained host and WT turn; never republish the mailbox,
reopen its transcript or infer failure merely from a transport exception. Durable
host identity permits dead-host reconciliation without inventing another writer.
