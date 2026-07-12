---
name: wt-shells
description: Control persistent managed shells in wt worktree sessions. Use when an agent needs to start, inspect, interact with, wait on, or stop a long-running development server, test watcher, log tail, REPL, debugger, or other process that should remain visible to the user and survive agent tool calls. Also use to discover an existing managed shell before starting a duplicate service. Do not use for ordinary one-shot commands whose output is needed immediately.
---

# WT managed shells

Use the native shell tool for finite commands such as searches, Git operations,
builds, and one-shot tests. Use `wt shell` for persistent or interactive
processes the user may want to inspect directly.

## Discover first

Run:

```bash
wt shell ls --json
```

Reuse a suitable existing shell instead of starting a duplicate server or
watcher. Managed shells use stable names; never rely on tmux window indexes.

## Start processes

Start a named persistent command without stealing the user's focus:

```bash
wt shell run server -- npm run dev
wt shell run tests -- npm test -- --watch
```

Create an interactive shell only when the process requires an ongoing terminal,
REPL, or debugger:

```bash
wt shell new repl --detach
```

Use short descriptive names containing letters, digits, dots, underscores, or
hyphens. Do not create or remove the user's generic `shell` window unless asked.

## Observe and synchronize

Wait for a readiness message, process exit, or a quiet period:

```bash
wt shell wait server --match 'Listening on' --timeout 30s
wt shell wait tests --exit --timeout 5m
wt shell wait server --quiet 2s --timeout 30s
```

Read normalized recent or unread output:

```bash
wt shell read server --lines 100
wt shell read server --new
wt shell read server --peek
```

Use `--raw` only when diagnosing terminal control behavior. Normal reads remove
ANSI/OSC control sequences and commands previously sent through `wt shell`.

## Interact safely

Keep literal text and special keys separate:

```bash
wt shell send repl --text 'reload()'
wt shell send repl --key Enter
wt shell send server --key C-c
```

Never send secrets unless the user explicitly requests it and understands that
managed-shell output is persisted under `WT_STATUS_DIR`.

## Watch for attention

Register watches for future output or command failure:

```bash
wt shell watch server --on error
wt shell watch server --match 'connection refused|panic'
wt shell watch tests --on exit-failure
wt shell events --json
```

Watches queue events; they do not wake the agent automatically. Inspect queued
events without consuming them unless the task owns the whole session event
queue. Stop an obsolete watch with `wt shell unwatch NAME`.

## Stop and clean up

Interrupt a running process, then verify its exit:

```bash
wt shell stop server
wt shell wait server --exit --timeout 10s
```

Remove only shells created for the current task:

```bash
wt shell rm server
```

If a command reports that the current session is not wt-managed, fall back to
the native shell tool rather than creating raw tmux resources.
