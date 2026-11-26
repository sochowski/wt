---
name: subagent-tabs
description: Run subagents as full agent sessions in their own tabs (Rex tabs or tmux windows), each in this workspace or a fresh wt workspace, with the brief and the result passed through files. Use when the human has chosen tabs over ephemeral subagents, or asks to "spawn", "fan out" or "run in a tab" an agent they can watch and talk to.
---

# Subagents in tabs

Each subagent is an interactive agent session in a new tab of the multiplexer you're in: a window of the current Rex session, or of the current tmux session. The human can switch to it, watch it, and steer it. You brief it through a file and read its result from a file. Everything here is non-interactive; never run `wt new` without a name and repos, since it would open a picker.

`.wt/bin/wt-pane` drives the tabs in either (`wt pane --help`). If `.wt/bin/wt-pane backend` prints `none`, you aren't in Rex or tmux: say so and offer built-in subagents instead.

## 1. Name and place each subagent

- Pick a short name per subagent, `[a-z0-9-]`, e.g. `api-tests`. It labels the tab and names its files.
- Decide where it works:
  - **This workspace** (`$(wt root)`) when it only reads, or when its edits can't collide with yours or another subagent's.
  - **A fresh workspace** when it edits code in parallel with others. It gets its own branch per repo:

    ```sh
    parent=$(wt root)
    ws=$(wt new "$(basename "$parent")-<name>" <Repo>...)     # path on stdout, progress on stderr
    (cd "$ws" && wt kit $(grep -vxF subagents "$parent/.wt/kits" 2>/dev/null))
    ```

    The second line primes it with this workspace's kits, minus this one, so it doesn't spawn agents of its own. Skip it if `$parent/.wt/kits` doesn't exist. Pass `Repo@REF` to start from something other than the default branch.

## 2. Write the brief

Write `$parent/.wt/agents/<name>/brief.md` (with `parent=$(wt root)`, the workspace you're in). The subagent starts with no context, so include:

- the goal, and what done looks like;
- what you've already found or ruled out, and the files worth reading;
- the scope: which checkout to work in, what not to touch, and whether it may commit (never push unless the human said so);
- what it may decide alone and what it must bring back. Local, reversible choices it makes and lists. Anything that sets a public contract, a security posture or a team convention, or crosses into another subagent's scope, it stops on and writes up. So does a flip-flop: a review asking it to undo what an earlier run asked for.
- this instruction, verbatim with the path filled in: "When you finish, or get stuck, write `<parent>/.wt/agents/<name>/result.md`: what you did and why, decisions you made and why, what's left, and the branch and commits if any. End it with **Needs human decision**, each item with its background, the question, the options and what each would change, and your pick. Keep working with the human in this tab afterwards if they ask."

When several subagents share context (the goal, links, a roster of who owns what, ground rules), put it once in `$parent/.wt/agents/_common.md` and have each brief point to it.

## 3. Open the tab

Start the agent with a one-line prompt pointing at the brief, with an absolute path. That way no long text goes through the shell:

```sh
ref=$(.wt/bin/wt-pane window <name> --keep-open --cwd "$ws" -- \
  claude -n <name> "Read $parent/.wt/agents/<name>/brief.md and do what it says." --add-dir "$parent")
echo "$ref" > "$parent/.wt/agents/<name>/pane"
```

- `--cwd` is the subagent's workspace (`$parent` when it shares this one; then drop `--add-dir`).
- `--add-dir` lets it write its result into the parent workspace. It takes every path after it, so the prompt goes before it.
- Use the harness the human asked for. With none named, use the one you're running in, e.g. `codex "Read …"` for Codex.
- The tab opens in the background, so the human stays where they are. Tell them the tab's name.
- The printed ref names the agent's pane from anywhere; it's saved to `pane` beside the brief because every shell call starts fresh.

Open every tab in a batch before waiting on any of them.

## 4. Collect results

- The session is interactive, so its process doesn't exit when the work is done. Watch for `result.md` instead:

  ```sh
  until [[ -f "$parent/.wt/agents/<name>/result.md" ]]; do sleep 15; done
  ```

  Run that in the background, or in your harness's own wait or monitor tool, with a time limit, not as a blocking foreground loop.
- To see what a subagent is doing now: `.wt/bin/wt-pane capture "$(cat "$parent/.wt/agents/<name>/pane")" | tail -40`.
- If the human is talking to a subagent in its tab, don't send it input yourself.
- Report each result to the human in a few lines: what it did, and where (workspace and branch). Its **Needs human decision** items go to the human as asks: with the `drilldown` skill when it's available, otherwise in chat, each with its background. Merging a fresh workspace's branch is the human's call; ask first.

## 5. Rounds

When the human's answers change what running subagents should do, send them down as a round, not as one-off chat to each tab:

- Append a `## Round N` section to `_common.md` (or write `_round<N>.md`). Open with "The human made these decisions; they override your brief where they conflict." List each decision under its ask ID, as an instruction. A change mid-round is a new lettered section (`Round 3b`) that says what it supersedes.
- Then send each affected subagent a one-line pointer, and what it means for that one: `ref=$(cat "$parent/.wt/agents/<name>/pane"); .wt/bin/wt-pane send "$ref" '<text>' && .wt/bin/wt-pane key "$ref" enter`. Capture the tab first; if the human is talking to it there, leave it to them.
- A subagent that needs another round's work gets a new brief, `brief-2.md`, and writes `result-2.md`. Earlier files stay as they were.
- When subagents depend on each other (a stack of branches, say), they coordinate through marker files: `.wt/agents/<name>/<marker>.txt` holding a single SHA or token, overwritten when it changes, e.g. `rebased.txt` for "on my parent's tip, tests pass" and `stack-ready.txt` for "my review loop is done". Dependents wait on the file, as with `result.md`.

## 6. Clean up

Leave the tabs open; the human closes them. Don't delete fresh workspaces either, since they hold the subagent's branch. List them at the end so the human can remove them when they're done.
