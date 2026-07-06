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

// Dig the opencode session id (ses_...) out of a session.created event. The
// event payload shape has shifted across versions, so probe the known spots
// defensively; an empty result just leaves wt to fall back to cwd-latest
// resume. Passed as an argv element to wt-hook, never interpolated into a
// shell string, so a malformed id can't inject.
function getSessionId(event) {
  const p = event?.properties ?? {}
  return p.sessionID || p.session?.id || p.info?.id || p.id || ""
}

export const WtStatusPlugin = async ({ directory, worktree }) => {
  const cwd = worktree || directory || process.cwd()

  return {
    event: async ({ event }) => {
      switch (event?.type) {
        case "session.created": {
          const sid = getSessionId(event)
          await runWtHook(cwd, sid ? ["session-start", sid] : ["session-start"])
          break
        }
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
