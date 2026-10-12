# Whole-checkout v2 delegation evidence

Fresh `wt_delegate` starts now explicitly request `evidence_profile:
"whole-checkout-v2"` and receive immutable `role_version: "wt-builtins-v2"`.
The WT protocol remains version 1; these fields distinguish the evidence/role
contract, not a replacement inference engine. Existing admissions without that
field remain wt-builtins-v1, with their original limits and stores/IDs/budgets.
Changing the profile on the same operation ID is refused, including paused jobs.
A paused v1 job is not automatically upgraded, resumed, or reported successful.

## Coverage, budgets and publication

V2 enumerates **all tracked, untracked and ignored files**, just like v1. It does
not use a selected-path approximation or exclude dependency/build artifacts to
make acceptance easier. Limits are explicit: 65,536 paths, 16 MiB Git inventory,
512 MiB total file content, 64 MiB per file, 64 changed artifacts and 32 KiB review
diff. New-task preflight rejects unsupported inventories before child/wake
admission. Executable Git filters are refused; fsmonitor/hooks/external diff and
textconv are disabled. Files are opened without symlink-following where supported,
large contents are streamed with a 64 KiB buffer, and metadata changes during
hashing fail closed. This is cooperative fencing, not a hostile-same-UID or
atomic-filesystem snapshot claim.

Full before/after hash inventories and semantic index records stay in a separate
private Harness/SQLite `wt.job-evidence.v2` document. They are not copied into the
model's acceptance report or WT result packet. The compact publication contains
whole-checkout digests, counts/byte totals, exact changed-file before/after hashes,
mode/link metadata and the bounded diff. Tool intents/results and exact command
receipts remain authoritative. Changed files are not inferred from model prose.
The 128 KiB result limit remains; large commands/results or too many changes can
still fail closed. The private snapshot document is separate from the per-tool
journal to avoid copying the full inventory on every tool receipt.

## Independent review and crash recovery

The reviewer has a distinct read-only store/UUID, the original role/resource/tool
ceilings, and no execution tools. Its before and after whole-checkout digests must
match the writer's exact after digest. Every changed artifact still needs a
complete genuine `read` receipt, and each acceptance criterion must cite relevant
successful reads. Deletions require reading the retained host diff. Binary, empty
or truncated reads do not become full-review evidence merely because hashes
exist; large changed artifacts can remain unsupported by the read ceiling.

Semantic Git index digests include mode/blob/path/stage and assume-unchanged /
skip-worktree flags, not volatile timestamps. Changing HEAD or staging/hiding
state denies writer success, while a preserved preexisting human staged baseline
is allowed. No guarantee is made about transient changes restored between scans.
Retained successful observations are revalidated against today's exact snapshot
before publication retry, without re-executing unsafe tools or provider turns.
The failed-host pause from PR85 remains in force; unobserved or uncertain work is
never completed by automatic restart.

## Evidence

Synthetic private tests cover ignored-file tampering, semantic index changes,
`__proto__` filenames, compact-proof tampering, old-admission refusal, incomplete
review coverage, real writer/reviewer Harness receipts, and publication retry.
The compiled WT/private PTY proof uses >4,096 files, actual independent child
stores, SIGKILL before writer publication, concurrency/budgets/pins/focus and
supervisor/peer protection. All inference is synthetic; no production-state tests
or paid providers.

A read-only feasibility scan of the actual `public-apis` task checkout covered
16,075 paths / 252,587,875 bytes in about ten seconds, with a 989-byte unchanged
compact receipt. It did not modify a store, start/retry a task, or bypass the
cross-root control guard. That benchmark is not independent acceptance or proof
that the previously paused task has completed.
