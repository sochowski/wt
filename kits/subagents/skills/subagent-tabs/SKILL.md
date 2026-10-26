---
name: subagent-tabs
description: Run subagents as full agent sessions in their own Rex tabs (windows), each in this workspace or a fresh wt workspace, with the brief and the result passed through files. Use when the human has chosen tabs over ephemeral subagents, or asks to "spawn", "fan out" or "run in a tab" an agent they can watch and talk to.
---

# Subagents in Rex tabs

Each subagent is an interactive agent session in a new window of the current Rex session. The human can switch to it, watch it, and steer it. You brief it through a file and read its result from a file. Everything here is non-interactive; never run `wt new` without a name and repos, since it would open a picker.

`$REX_SESSION` is set when you run inside Rex. If it's empty, you aren't in Rex: say so and offer built-in subagents instead.

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
- this instruction, verbatim with the path filled in: "When you finish, or get stuck, write `<parent>/.wt/agents/<name>/result.md`: what you did, what you found, what's left, and the branch and commits if any. Keep working with the human in this tab afterwards if they ask."

## 3. Open the tab

Start the agent with a one-line prompt pointing at the brief, with an absolute path. That way no long text goes through the shell:

```sh
rex window new -s "$REX_SESSION" <name> --focus=false --keep-open --json -c "$ws" -- \
  claude -n <name> --add-dir "$parent" "Read $parent/.wt/agents/<name>/brief.md and do what it says."
```

- `-c` is the subagent's workspace (`$parent` when it shares this one; then drop `--add-dir`).
- `--add-dir` lets it write its result into the parent workspace.
- Use the harness the human asked for. With none named, use the one you're running in, e.g. `codex "Read …"` for Codex.
- `--focus=false` keeps the human where they are. Tell them the tab's name.
- Keep `block_ids[0]` from the JSON output; it's what `rex wait` and `rex capture` take.

Open every tab in a batch before waiting on any of them.

## 4. Collect results

- The session is interactive, so its process doesn't exit when the work is done. Watch for `result.md` instead:

  ```sh
  until [[ -f "$parent/.wt/agents/<name>/result.md" ]]; do sleep 15; done
  ```

  Run that in the background, or in your harness's own wait or monitor tool, with a time limit, not as a blocking foreground loop.
- To see what a subagent is doing now: `rex capture -s "$REX_SESSION" -b <block> --trim`.
- If the human is talking to a subagent in its tab, don't send it input yourself.
- Report each result to the human in a few lines: what it did, where (workspace and branch), and anything that needs a decision. Merging a fresh workspace's branch is the human's call; ask first.

## 5. Clean up

Leave the tabs open; the human closes them. Don't delete fresh workspaces either, since they hold the subagent's branch. List them at the end so the human can remove them when they're done.
