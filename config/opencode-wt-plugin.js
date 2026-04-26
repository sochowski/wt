import { spawn } from "node:child_process"

const wtHook = process.env.WT_HOOK || `${process.env.HOME}/bin/wt-hook`

function runWtHook(cwd, args) {
  return new Promise((resolve) => {
    const child = spawn(wtHook, args, { cwd, stdio: "ignore" })
    child.on("error", resolve)
    child.on("close", resolve)
  })
}

function getToolName(input) {
  return input?.tool || input?.name || "tool"
}

export const WtStatusPlugin = async ({ directory, worktree }) => {
  const cwd = worktree || directory || process.cwd()

  return {
    event: async ({ event }) => {
      switch (event?.type) {
        case "session.created":
          await runWtHook(cwd, ["session-start"])
          break
        case "session.idle":
          await runWtHook(cwd, ["stop"])
          break
        case "session.error":
          await runWtHook(cwd, ["error"])
          break
        case "permission.asked":
          await runWtHook(cwd, ["notification", "permission_prompt"])
          break
      }
    },

    "tool.execute.before": async (input) => {
      await runWtHook(cwd, ["pre-tool", getToolName(input)])
    },

    "tool.execute.after": async (input) => {
      await runWtHook(cwd, ["post-tool", getToolName(input)])
    },
  }
}
