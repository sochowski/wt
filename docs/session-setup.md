# Named session setup

`wt setup` (prefix+n; prefix+N for background) is the interactive entry point.
`wt new NAME` remains noninteractive, and `wt new REPO BRANCH` retains its legacy
behavior. Bare `wt new` allocates `scratch`, `scratch-2`, … without renaming any
existing session. All agent-first sessions start with one Pi conversation, not
an eager editor.

1. Name the task. Select repositories with Tab, then Enter to review. The list
   shows full source paths and always offers manual paths, including paths with
   spaces, colons and quotes. Choose **Start without repositories** for no repo.
2. Review all repositories in one scrollable preview. Optional edits select a
   repository's alias, remote, base or fetch policy; they are not a routine
   per-repository wizard. Esc cancels without creating a root, fetching or
   changing Git branches/checkouts.
3. **Create** fetches each selected remote's default branch, resolves its exact
   commit, and creates a NEW worktree and branch. Branch and directory basename
   both equal the session name. The reviewed `<repo-key>` combines a readable
   source basename and a hash of the canonical Git common-directory identity,
   disambiguating duplicate basenames. Destinations are
   `$WT_BASE_DIR/<repo-key>/<session-name>`.

Before confirmation the default reference is remote HEAD, with a local cached
hint if available. Remote HEAD is resolved again on Create, so custom defaults
and changes to a remote's default are honored. A fetched base override is a
remote branch name. A cached override is an explicitly chosen commit/ref.
`wt setup --offline` starts with cached-only policy and never networks during
preview. Missing caches require an explicit override, fetch, skip or cancellation.
Setup requires Git support for `--no-lazy-fetch`; older Git fails closed with a
Git error. Implicit promisor-object fetching is disabled during inspection and
checkout, so incomplete partial clones can fail rather than silently contact a
remote. Only the explicit confirmed fetch step invokes Git transport.

Begin revalidates any supplied preview provenance and rejects a changed source
identity or destination (including changed base-directory symlinks). Review the
new plan rather than silently using a different location. If accepting a cached
base fails, the recovery choices remain open; that failure does not authorize a
network retry.

Fetch updates shared Git objects and remote-tracking refs, **not** original
working files, indexes or local branches. No pull/reset/stash/clean/switch occurs
in the source checkout. No existing path/branch is adopted and no random suffix
is added. The selected base SHA is the initial commit, not a future branch lock
or Neovim view pin. New checkouts contain committed files; the legacy repo-first
`.wt/sync` environment-file workflow is not run on the originals by this flow.

## Add repositories and recovery

Prefix+R → **Add repositories** reuses setup with the current session's name.
Outside a managed session it offers a root picker; `wt repositories NAME` targets
an explicit root. Adding repositories does not replace, restart or move existing
conversations. Existing `wt checkout attach ROOT ALIAS PATH` remains a borrowed,
no-Git-mutation advanced command.

Preparation is sequential. The popup shows each phase and any error, with
retry/reconcile, explicitly accepted cached base, skip, proceed with a successful
subset, or cancel. Initial launch waits for success or explicit subset consent.
Cancellation after Create preserves the root and every created resource; a new
root stays offline. There is **no destructive rollback or automatic cleanup**.
Use `wt restore NAME` to launch/resume a preserved offline root once ready.

The durable journal is available through WT-owned APIs:

```bash
wt setup-state list NAME
wt setup-state show SETUP_ID
wt setup --resume SETUP_ID             # same popup outcome/recovery UI
wt setup-state run SETUP_ID            # JSON outcome; progress on stderr
wt setup-state cached SETUP_ID 0       # explicit cached consent, zero-based index
wt setup-state skip SETUP_ID 0         # only before checkout creation
```

`run` returns a JSON plan even for partial Git failure: consumers must inspect
**each repository's `state` and `problem`**, not only the process exit code.
`not_attempted`, `fetching`, `creating`, `created`, `attached`, and `skipped`
identify the last durable boundary; a nonempty `problem` marks failure there.
Exact paths, source identity, selected ref, SHA, alias and checkout ID remain
recorded. `created` means Git succeeded but attachment may still be pending.

Interrupted creation is never blindly repeated. Reconciliation verifies the
recorded Git common directory, registered path, branch and WT token in that
worktree's Git administrative directory. Unattached resources must still have
the recorded initial SHA. Missing/mismatched tokens or uncertain Git operations
require manual inspection; the UI cannot safely adopt an unrelated checkout.
Attachments retain the conservative borrowed lifecycle: provenance is **not**
permission to delete them. Journals are bounded to 32 batches per root, each with
at most 32 repositories. Git operations are noninteractive and time-bounded.

For automation, `wt setup-state preview` accepts JSON on stdin:

```json
{"name":"billing-migration","repos":[{"source":"/src/api"},{"source":"/src/web"}]}
```

Preview is read-only with respect to root/Git resources. After explicit approval,
create an offline root (`wt new billing-migration --offline`), set its returned
`id` as `root_id` in the reviewed plan and submit to `wt setup-state begin`.
Then run the returned setup ID. Begin validates again before journaling intent.

## Shared names and search

`wt roots QUERY` (JSON), `wt ls`, `wt pick`, prefix+s and mobile use the same root
projection: one row per persistent session, label, repository aliases/branches,
agent count and state. Search includes **all** attached repositories, names,
aliases, branches, source/destination paths, peer names and the latest 32 request
summaries (512 characters each), even when the row abbreviates repositories.
Search terms are case-insensitive and ANDed; the picker reloads this projection
rather than searching fzf's abbreviated display. Preview shows full details.
Explicit legacy `wt ls simple` / `wt ls session` remain available to old callers;
`pick-list` now transports opaque IDs separated from escaped display text by a
tab, not colon-delimited paths/messages.

`wt label ROOT 'Meaningful display label'` repairs a human-facing label without
changing the stable ID, tmux name, branch, directory or conversations. Labels
are human-only, printable, and at most 120 characters. Peer creation already
accepts human role/task names; external provider-generated labels are outside
this repository's generator. No runtime rename or deterministic auto-open view
hooks are introduced; view guidance remains skill-first.
