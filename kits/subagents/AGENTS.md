## Subagents

Before you hand substantial work to subagents (anything that edits code, runs for more than a few minutes, or that the human may want to watch or steer), ask once per batch, in one question: should they run **ephemeral**, as your harness's built-in subagents, or **in their own Rex tabs**, as full agent sessions the human can switch to? Don't ask for quick lookups or searches; use built-in subagents for those. Don't ask if the human has already said "tabs" or "inline" (or similar) for this task.

For tabs, follow the `subagent-tabs` skill. With built-in subagents, don't use a harness's own worktree isolation (such as Claude Code's `isolation: "worktree"`): it makes worktrees outside wt's layout. If a subagent needs its own checkout, give it a wt workspace as the skill describes.
