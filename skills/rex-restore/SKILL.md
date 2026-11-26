---
name: rex-restore
description: Rebuild the user's Rex sessions and tabs after the Rex server crashed or restarted, resume the Claude Code conversations that were running in them, and diagnose why it went down. Use when the user says Rex crashed, the server restarted or was unreachable, their sessions or tabs are gone, asks to set their old sessions back up, or asks what caused a crash.
---

# Restoring Rex after a crash

Rex keeps no session state on disk. When the server dies, every session, tab and process goes with it. What survives is:

- **The old server's journal** (`journalctl --user -u rex`). It shows which sessions were created and never destroyed, with their creation labels, and which blocks each session was still serving near the end.
- **Claude Code transcripts** (`~/.claude/projects/*/*.jsonl`). Each one has the conversation's id, its `cwd` and its `-n` name, and tells you whether it stopped mid-turn or waiting on the human.
- **Orchestrator block maps** in `.wt/agents/_blocks*.txt` of a fanned-out workspace. These map tab names to block ids, and the newest file has the current names.

## 1. Survey

```sh
python3 -I ~/.agents/skills/rex-restore/survey.py
```

By default it treats the current server's `started_at` as the crash time. Pass `--until 2026-10-27T23:20:00Z` if the server restarted more than once. The survey prints:

- the sessions alive at the crash, and the blocks each one used in the last two hours;
- every conversation written in the last 30 hours, marked `MID-TURN` or `idle`;
- the last message of each idle conversation.

Rename your own session (`rex-crash-recovery`) while you're at it.

## 2. Decide what goes where

- **Restore a session only if it had active blocks near the end.** Sessions with none were idle, often finished reviews. List them and offer them, but don't restore them unasked.
- **Block mentions in the survey are hints.** An orchestrator mentions all of its children's blocks. To pin a tab to a conversation, prefer, in order:
  1. the orchestrator's `_blocks*.txt`;
  2. a `declare -A` or `rex window new` in the orchestrator's transcript;
  3. the conversation's `cwd` and name.
- **A tab can outlive its first name.** Orchestrators reuse a subagent's process for a later round and rename the tab, as when the `auth` conversation became the `access` tab. Name the tab from the newest block map, and resume the conversation that ran in it.
- **Skip older conversations in the same worktree.** Use the newest one that was active before the crash, and skip ones whose blocks weren't active.
- **Reuse the session labels the user had.** A label in the journal is the one the session was created with. If an agent renamed it later, the transcripts show the new name (search for `rex session rename` or `"label"` in tool output).

Write the plan down as a short table: session, then each tab's name, cwd and conversation id. Then do it.

## 3. Relaunch

Resume each conversation in its own tab, in its original directory. For a subagent, pass `--add-dir` for its parent workspace again, so it can still write `result.md` there.

```sh
sid=$(rex new LABEL -c "$cwd" --window TAB --json -- claude --resume "$id" | jq -r .session_id)
rex window new -s "$sid" TAB -c "$cwd" --focus=false --json -- claude --resume "$id" --add-dir "$parent"
```

Put all of it in one script in your scratchpad and run it once. `claude --resume` only reopens a conversation; it sends nothing, so no agent redoes any work.

## 4. Verify

For every block, capture the pane and check that the Claude prompt (`❯`) is up. In one pane, also check that the old history is visible.

```sh
rex capture -s "$sid" -b "$b" --trim | tail -12
```

## 5. Report

Tell the user, in their terms:

- **What came back:** a table of session and tabs.
- **What didn't, and why:**
  - nvim panel panes: ask that agent to reopen its panel;
  - shells nobody mentioned;
  - dev or preview servers: their agent can restart them;
  - idle sessions you skipped.
- **Interrupted:** `MID-TURN` conversations stopped mid-task, and resuming doesn't restart that work. They need a nudge such as "the Rex server crashed, pick up where you left off".
- **Dead watchers:** idle agents that were waiting on a background watcher ("I'll DM you when…", "waiting for `rebased.txt`") lost it in the crash. Tell them to start waiting again.
- **Waiting on the human:** go through the last messages. List each agent that ended with a question or a decision for the user, with the question itself. Group agents that ask the same thing.

Offer to send the nudges; don't send them unasked. New sessions have new ids, so the app may need the user to pick them in the sidebar.

## Gotchas

- Directory names under `~/.claude/projects` start with `-`, which breaks `ls`, `stat` and `grep` without `--`. Use Python or `./`-prefixed paths.
- Resumed conversations append to their transcripts. Judge their state only from entries before the crash; the survey's `--until` does this.
- Don't restart the Rex server or kill Rex processes. The user already brought it back.

## Diagnose why it went down

Do this after the restore, or on its own when the user asks what happened. "Rex crashed" usually means "I couldn't reach Rex". That can be any of four things, and each needs a different fix. Work out which one it was from the evidence, not from the user's guess, and say which line of evidence settles it.

### 1. Did Rex exit, or did the machine reboot?

```sh
journalctl --list-boots --no-pager | tail -4
journalctl --user -b -1 -u rex --no-pager -o short-iso | tail -20
```

- **A new boot at the restore time** means the machine restarted. Ask whether the user rebooted it themselves. They usually did, because Rex was unreachable. Then the real question is why it was unreachable: go on to step 2.
- **The same boot, with Rex exiting** (`Main process exited`, a Go panic, `fatal error`) means Rex itself crashed. Collect the last 50 lines and the version (`rex version`) for a bug report.
- **`binding extra listener 0: this host has no tailnet addresses`** right after boot is Rex starting before Tailscale is up. The unit restarts it 10 s later. It's harmless, and not the cause.

### 2. Was the box frozen, or only the network down?

Use the user's Rex journal. It needs no sudo.

- **Compare when events happened with when they were logged.** Rex stamps each line with its own clock, and the journal stamps it when it arrives (`-o short-iso`). In a healthy run they match. A lag of a minute or more means the whole machine was stalled: memory thrashing, CPU starvation, or the VM paused by its host.
- **Check whether local traffic stopped too.** Agents talk to Rex over the unix socket (`listener=unix`), so their calls don't touch the network. If the unix-socket calls stop at the same moment as the tailnet ones, the box froze. If unix traffic carries on while every tailnet client drops (`stopped answering pings`, `websocket control ended unexpectedly`), only the network failed.
- **Note which device was connected** (`node=` on tailnet lines), and whether that device was online (`tailscale status`). A Mac shown as "offline, last seen …" dropped off from its own side, and rebooting this box can't fix that.

### 3. What do tailscaled and the kernel say?

These need root until the user is in `adm` and `systemd-journal`. Type the command into a pane with `rex send`, without pressing Enter, and let the user run it:

```sh
for b in -2 -1; do echo "=== boot $b"; sudo journalctl -u tailscaled -b $b --no-pager --since "<start of trouble>" | tail -40; sudo journalctl -k -b $b -p warning --no-pager | tail -20; done
```

Read it like this:

- **`monitor: time jump detected (slept Ns)`** or **`subscriber … is slow (1m…)`**: tailscaled itself didn't run for that long. The box froze, and Tailscale was the messenger.
- **`hasn't heard from the … relay server`**, **`map response long-poll timed out`**, **`udp=false`**, failed uploads to `log.tailscale.com`, or DNS timeouts: the box's internet was down, not just the tailnet. If these appear with no time jump, suspect the host's network.
- **A gap in tailscaled's own log while other things were happening**: another sign of a freeze.
- **`Out of memory: Killed process`** or **`blocked for more than 120 seconds`** in the kernel log: memory exhaustion or a hung box. **No kernel messages at all, and nothing in `/var/crash`**, points to a hard reset from outside: the user, the host's console, or a watchdog.

### 4. Memory or the host?

- **Look at memory now.** Compare `free -m`, `/proc/pressure/{memory,cpu,io}` and `ps -eo rss,comm --sort=-rss` with the box's RAM. Agents' processes all run inside `rex.service`, so check `systemctl --user show rex.service -p MemoryCurrent -p MemoryPeak`. Each idle Claude CLI is about 350 MB, and test suites and Postgres containers come on top of that.
- **Look at CPU steal**: the 8th number on the `cpu` line of `/proc/stat`. A rising share means the VM is waiting on its host. Check `/sys/class/dmi/id/sys_vendor` and `product_name`. On a shared-CPU plan (Vultr VC2, for example), a busy or failing host can pause the VM. Tell the user to open a ticket with the exact UTC windows.
- **Check the boxguard recorder, if the box has one:** `~/.local/state/boxguard/<date>.log`, one line every 15 s. Find the last line before the trouble and the first after it:
  - `GAP=Ns` means the box was frozen for that long.
  - `mem_avail` falling, or `swap` and `psi_mem` rising, before the gap means memory.
  - `steal` spiking before the gap means the host.
  - `net=DOWN` or `ts=` not `Running`, while `rex=ok` and memory are fine, means the network.
- **Check the boxguard watchdogs:** `journalctl -u tailscale-watchdog` shows any restarts of tailscaled, and `journalctl -u earlyoom` any processes it killed.

### 5. Report

Give one cause per outage. Several outages in one night can have different causes. For each, give:
- the time window in UTC;
- what the user saw;
- the deciding evidence, quoted;
- your confidence, and what would confirm it.

Then give the prevention that fits the cause:

- **Box froze from memory:** earlyoom, with Rex, tailscaled and sshd on its avoid list and test runners on its prefer list. Add one lock shared by all agents' test runs (`flock /tmp/v2-db-tests.lock`). Close finished agent tabs, or get more RAM.
- **Host paused the VM:** a ticket to the host, then a dedicated-CPU plan.
- **Tailscale or the network:** the tailscale watchdog. Before rebooting, try SSH to the public IP. If SSH works, the box is fine, and `sudo systemctl restart tailscaled` brings Tailscale back without killing the agents.
- **Rex itself crashed:** a bug report with the log tail.

Don't restart Rex, tailscaled or the machine yourself. Each of these drops the user's connection, and restarting Rex or rebooting kills every agent.
