# Approved session setup and search plan

Status: approved for implementation; not a claim that these features exist yet.

## Product contract

A session is a named task with zero, one, or multiple repositories. Normal menu
setup always creates new isolated Git worktrees for selected repositories. There
is no use-existing-checkout mode in the menu. Existing advanced borrowed-attach
commands remain available and do not gain implicit Git mutations.

For session `billing-migration` and repositories `api` and `web`:

- Both new branches are named exactly `billing-migration`.
- Working directories are `$WT_BASE_DIR/<repo-key>/billing-migration`.
- Repository keys distinguish different repositories with identical basenames;
  display that disambiguation before creation.
- Do not silently add random suffixes, reuse branches, or adopt existing paths.
- Existing worktrees, dirty files, indexes and local branches stay untouched.
  Git object storage and remote-tracking refs are shared; fetch is not read-only.

## Interactive creation

The New session menu prompts for a meaningful name, provides a searchable
multi-select repository list, and always offers manual paths. Show full source
paths. Selecting no repositories starts a normal repo-free session.

For selected repositories, show one review screen with name, repository aliases,
starting refs, new branches, destination paths and fetch policy. Default to fetch
latest remote default branches on Create. Detect main/master/custom default
accurately and expose a per-repo base override without a routine per-repo wizard.
Before confirmation, no root, fetch, branch or checkout mutation.

On Create, run preparation and report progress in the same UI, without repeated
Continue prompts. Start the initial agent only after success or an explicit
choice to proceed with a successful subset.

Repositories → Add repositories reuses the same flow for the selected/current
session, using its name for branches and directory basenames. Do not replace
or restart its existing conversations.

Preserve `wt new NAME` and explicit legacy repo+branch consumers. Use an explicit
interactive entry point for menu setup, avoiding further accidental dispatch
ambiguity. Quick unnamed repo-free creation should use readable scratch/scratch-2
labels rather than timestamps. Never reinterpret cancellation as creation.

## Preparation and recovery

- Validate source working repositories, aliases, session/branch names and path
  collisions before execution; handle spaces and colons in paths correctly.
- Record bounded durable setup intent/provenance through WT-owned state APIs.
- Fetch the selected remote default, resolve its exact commit, and create from
  that SHA. Record selected ref, SHA, branch, source identity and destination.
  The SHA is the initial commit, not a permanent branch lock or Neovim view pin.
- Do not silently choose stale local HEAD or describe failed fetching as current.
- Offline/fetch/auth errors offer retry, explicitly accepted cached base, skip,
  or cancellation. No usable cached commit means an explicit alternative is
  needed. Avoid network during an explicitly offline plan/preview.
- Never pull, merge, rebase, reset, stash, clean or switch the original checkout.
- Partial failure preserves resources and reports created, attached, failed and
  not attempted states. No automatic destructive rollback or deletion.
- Resume only verified recorded resources. Uncertain Git operations require
  reconciliation, not blind recreation or adoption of an unrelated path.
- Checkout creation/provenance is new functionality: do not call the old
  repo-first create_worktree in a loop, because it can adopt existing checkouts
  and creates separate sessions/eager editors.
- Keep additive SQLite compatibility. Creation provenance does not grant
  automatic cleanup authority. Borrowed attachments remain protected.

## Naming and search

Fix the root-oriented metadata projection, not just the keybinding. Prefix+s
already calls `wt pick`; its old repo/branch-only row projection is inadequate.

Show one row per session: meaningful name/display label, repositories, agent
count and state. Show full detail in the preview. Search all attached repository
names, aliases, branches and useful paths, plus peer names and bounded task
summaries. Include keywords from visually abbreviated-away repositories.

Use a shared projection for the relevant CLI lists/search and picker. Use safe
record transport with opaque identity separate from visible/searchable text;
colon-delimited records are not safe for arbitrary paths/messages.

Stable internal IDs remain unchanged. Do not rename/recreate existing tmux roots,
Git branches or checkouts automatically. A display-label mechanism may repair
old generated names without mutating runtime identity. Actual rename requires
separate identity/migration coordination and is not the shortcut here.

Peer labels should be human-readable role/task names rather than provider job
hashes. The staging provider's job-hash generator is outside this repository;
parent owns that bounded label-layer adjustment after the WT changes. Do not
change the upstream provider contract or job lifecycle to accomplish labels.

## View UX remains skill-first

No deterministic auto-open/auto-promote/autoplay hooks. The already implemented
experimental WT skill covers reusable views, task-local diff attribution and
primary view as own-window master without keyboard-focus theft. Preserve it and
the already reviewed dead-pane parser and compact inbox renderer.

## Delegation and validation

One source writer at a time; implementation proceeds in coherent milestones:
checkout preparation/provenance, named menu flow, searchable naming projection.
The writer records a baseline-relative diff because this checkout already has a
large uncommitted/untracked redesign. No commits, staging, pushes or PRs.

After implementation, fresh independent safety and UX reviewers inspect the
actual changes while a validator tests a fixed disposable source copy. Review
findings return to the parent for disposition; fixes remain single-writer.

Acceptance includes multi-repo setup, unchanged dirty originals, fetched exact
bases, collisions, custom default branches, offline/partial failures, reliable
resume, adding repos without replacing conversations, search by every attached
repo, meaningful root/peer labels, duplicate basenames, special-character paths,
cancellation and legacy CLI compatibility.

Use AGENTS.md harnesses. `test.sh` rebuilds checkout/bin/wt-state: run it from a
disposable source copy to avoid replacing a binary shared by live sessions.
Other tests must also use private HOME/state/tmux and local Git remotes/stub
agents. Run Go tests/vet/temp build, relevant Node tests and actual keyboard menu
coverage. Preserve all existing source work and live demo conversations.

Only the parent deploys reviewed, validated artifacts to isolated staging at
`/home/aidan/.cache/wt-real-staging.p_4ewjvd`. No production install/state/tmux
mutation or Pi reload. No fetching real user repositories during implementation.

## Discovery evidence

Read-only source reports from workflow eee4632b-3a84-4884-8709-c8d9b877e3cf:
`research/repository-menu.md` and `research/naming-search.md`, under this parent
session's subagent-artifacts/outputs directory. They document HEAD 2175c1b versus
the pre-implementation working tree. They are evidence, not implementation.
