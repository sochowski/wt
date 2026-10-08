# Native-style UI for WT Pi Durable

New Durable sessions use the native-style presentation controller composed from
public `pi-tui` 0.87.1 widgets. The existing Harness/SQLite runtime remains the
sole inference/state owner: this is not native InteractiveMode, a native SDK
session, or a second agent engine.

## Included

- Read-only native theme/settings/keybinding loading, including custom themes
  such as `github-dark-hc`, editor padding and thinking/tool visibility.
- Native-style conversation Markdown, thinking/tool output, bordered editor and
  footer with cwd, usage, model/thinking and durable busy/idle status.
- Editor history and autocomplete; model and thinking selectors with filtering,
  paging and focus restoration.
- Action precedence for interrupt, empty-editor exit and autocomplete handling;
  real Durable abort, recoverable close, busy steering and explicit follow-up.
- Protected cleanup restores global keybindings and attempts all owned watcher,
  terminal and runtime teardown even after initialization/cleanup errors.

`Ctrl+L` opens **Model (pinned catalog)**. `Shift+Tab` cycles thinking, `Ctrl+T`
toggles thinking, `Ctrl+O` expands tool output, and `Alt+Enter` submits follow-up.
Custom native keybindings override these defaults. `/help` lists Durable commands.

Unsupported native actions (external editor, image paste, suspend, dequeue,
clipboard message copy and saved model profiles) fail visibly rather than being
silently represented as supported.

## Rollout and boundaries

No new launch/profile flag is required. This is the normal UI for the existing
`wt-durable-v1` definition; native sessions stay native and stores are not migrated.
Already running processes retain the UI code loaded at startup. Do not restart
others' sessions or modify identities to refresh it; new processes load the update.

This change deliberately excludes the unreviewed `native-compat-v2` questionnaire/
todo integration. Web/MCP plugins and custom Durable subagent workflows remain
separate implementation/review work. Native-style appearance is not plugin parity.

The twelve implementation/test/theme files were recovered byte-for-byte from the
independently accepted M1 snapshot (47 runtime tests plus managed private PTY
proof). No runtime engine, package/dependency lock, WT state/control, permissions,
identity or recovery contracts changed to ship that snapshot onto current main.

Validation: `node runtime/pi-durable/run-tests.mjs`, production npm audit,
`cd state && go test ./...`, and the explicit private compiled WT/PTY tests under
isolated HOME/state as required by `AGENTS.md`. No live/paid inference is needed.
