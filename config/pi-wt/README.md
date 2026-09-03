# wt integration for Pi

This directory is symlinked to `~/.pi/agent/extensions/wt` by `install.sh`.
Pi auto-discovers `index.js`; no package install or build step is required.

The extension is deliberately inert unless `WT_SESSION` is present. Managed
launches export that stable tmux session identity through `wt-agent-launch`.
Native Pi lifecycle events are translated into the agent-neutral `wt-hook`
status protocol, and Pi's native session id is saved so `wt revive` can launch
`pi --session <id>`.

Accurate settled status requires Pi 0.80.4 or newer. Waiting-for-input status
for blocking extension UI requires Pi 0.84.4 or newer.
