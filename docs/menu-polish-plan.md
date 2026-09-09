# Approved menu polish and guided workspaces

Status: approved for implementation after hands-on staging feedback. This revises
picker and workspace UX from session-setup-plan.md without changing its Git
safety guarantees. No enforcement/sandbox feature is requested.

## 1. Consistent Vim-style menus

Staging has WT_FZF_VIM=1. The Bash session picker uses emulated modal bindings,
but the new Go setup menus bypass them. Fix the inconsistency, not just the
header text. Use one coherent reusable keymap contract across both paths.

- Lists open in NORMAL when Vim mode is enabled, with a visible mode prompt.
- j/k move. / or i enters filtering. Esc from input returns to NORMAL and keeps
  the query; q/Esc in NORMAL cancels/backs out. Enter confirms the stated action.
- Name/path text fields open ready to type in INSERT. Clearly distinguish their
  text-entry behavior; do not claim full Vim text-editor operators are supported.
- Historical note: the h/l policy below was superseded; NORMAL h now aborts and
  NORMAL l accepts, restoring the established WT_FZF_VIM contract. Destructive
  operations remain out of normal navigation controls; Ctrl-D must not delete a
  session in Vim mode.
  Deletion remains an explicit confirmed action, not an accidental paging key.
- Preserve non-Vim use. Test actual PTYs with both WT_FZF_VIM=1 and off.

## 2. Quiet name-only session switching

Prefix+s should show and search only session names/human display labels. Remove
repo lists, agent counts, idle/active badges and status summaries from this
surface. No large details preview by default; details can be explicitly opened.
Stable IDs remain internal selection tokens, not searchable user vocabulary.

This hidden-preview decision was later superseded: rich details are visible by
default in both responsive layouts and `?` toggles them (`Ctrl-P` is an alias).

Load a light name list once and filter locally in fzf. Do not spawn WT/Git status
scans on every keypress. Keep results stable while navigating. Rich repository,
agent and task information may remain in explicit inspection APIs/commands; do
not needlessly remove durable metadata or break agent discovery.

Preserve safe opaque identity resolution, ambiguous name/ID rejection, offline
root handling, labels and original-name compatibility. Label updates must not
rename/recreate runtime roots or Git branches.

## 3. One live repository selection state

Remove the static hand-authored [ ]/[x] column. It currently disagrees with fzf's
real Tab selection until fzf returns. Use one real selection indicator/count.

- NORMAL Space toggles current repo without moving. Tab may remain an alias but
  must not toggle+jump. INSERT Space is ordinary search input.
- Enter reviews exactly the marked set. Zero marked repos means zero, never an
  implicit selection of the highlighted repo.
- Manual path and Start without repositories are explicit actions, not rows that
  can accidentally be multi-selected as if they were repositories.
- Preserve marks across filtering, manual path entry and returning to selection;
  the visible marks/count and review contents must always agree.
- Stay with the existing tmux/fzf approach rather than adding a new frontend.

## 4. Dedicated workspace for NEW sessions

For new managed sessions, propose the default home:
`$WT_BASE_DIR/.sessions/<session-name>/` with:

- `repos/<readable-alias>` shortcuts to the actual isolated checkouts;
- `scratch/` for task notes/experiments;
- `WORKSPACE.md`, a small readable data map, not executable Pi/project config.

Actual working trees remain `$WT_BASE_DIR/<repo-key>/<session-name>`, with branch
and checkout basename equal to session name. Keep source-identity repo keys for
collision safety but use readable aliases such as api/web when unambiguous.
Explicit user aliases remain respected; duplicate basenames need understandable
and stable disambiguation.

Launch the parent in its new session home. Checkout-scoped peers already support
an explicit target/cwd; preserve that and describe it in guidance. WT view path
containment still applies: repo shortcuts do not authorize bypassing checkout
alias resolution when targeting views.

Refresh the owned map/shortcuts after Add repositories, without replacing an
agent. Do not silently relocate existing sessions or explicit --cwd sessions.
Do not write WORKSPACE.md or repos/ into an existing HOME/project cwd, adopt an
unrelated pre-existing directory, overwrite a user file, or follow a substituted
symlink while publishing a workspace. Use bounded durable ownership/provenance
and fail safely on conflicts. Existing sessions retain the checkout map through
WT discovery even when they lack a newly provisioned home.

## Guidance, not enforcement

The user's chosen rule is: work within this session's workspace and assigned
checkouts unless explicitly told otherwise. State that in the unified WT skill
and session orientation. Provide the current cwd, workspace/scratch and alias/path
map accurately; the agent should inspect each repository's instructions before
editing it. No per-command approval framework, tool capability widening, process
sandbox, daemon, or claims that symlinks/cwd enforce access boundaries.

## Preserve prior fixes

Keep no-implicit-lazy-fetch, reviewed-plan binding, exact fetched SHAs,
HEAD/branch validation, safe cached recovery, private Git fixtures, conservative
setup reconciliation/borrowed preservation, and root-ID ambiguity guards.
Retain the reviewed compact inbox renderer, dead-pane parsing and skill-first
view UX. No automatic view-selection/promotion/autoplay hooks.

## Execution and validation

One source writer in the existing dirty WT checkout. Capture a task-start baseline
and narrow diff so reviews exclude the previous redesign/setup work. Independent
fresh UX and safety reviews follow; parent owns deployment and blocker decisions.

Run tests on a fixed disposable source copy: test.sh rebuilds bin/wt-state.
All WT/state/tmux/Git tests use private HOME/config/state/socket, local remotes and
stub agents. No edits to production or running staging, installs, real repo fetch,
Pi reload, commits/staging/push/PR. Run Go tests/vet/temp build, Node tests,
regression and affected lifecycle/master-stack/Neovim tests.

Actual keyboard acceptance must cover Vim-mode navigation/filter/cancel and text
entry; safe Ctrl-D; no-jump selection; exact zero selection; manual-path selection
persistence; quiet name-only search that does not match unrelated repo/status
terms; new workspace mapping/cwd; unchanged old/explicit cwd; Add preserving native
identity; conflicting files and symlink substitutions. Show unsupported keyboard
features honestly rather than papering over test failures.

After review and validation, parent updates only isolated staging and leaves a
fresh demo ready for manual UI fuzzing. Existing conversations are not restarted.
