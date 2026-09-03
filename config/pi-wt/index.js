import { spawn } from "node:child_process"

// This extension is installed globally so Pi can discover it before project
// trust is resolved, but it must stay inert outside a wt-managed launch.
const wtSession = process.env.WT_SESSION
const wtHook = process.env.WT_HOOK || `${process.env.HOME}/bin/wt-hook`

function invokeWtHook(cwd, args) {
  return new Promise((resolve) => {
    const child = spawn(wtHook, args, {
      cwd,
      env: process.env,
      stdio: "ignore",
    })
    child.once("error", resolve)
    child.once("close", resolve)
  })
}

export default function WtExtension(pi) {
  if (!wtSession) return

  // Serialize writes even for Pi's notification-only UI prompt events, whose
  // handlers are deliberately not awaited by the extension runner.
  let hookQueue = Promise.resolve()
  const emit = (ctx, ...args) => {
    hookQueue = hookQueue.then(() => invokeWtHook(ctx.cwd, args))
    return hookQueue
  }

  pi.on("session_start", async (_event, ctx) => {
    const sessionId = ctx.sessionManager.getSessionId()
    if (sessionId) await emit(ctx, "session-id", sessionId)

    // Creating/loading a Pi session does not imply that an agent turn is
    // running. An initial CLI prompt will immediately emit agent_start.
    if (ctx.isIdle()) await emit(ctx, "idle", "Pi ready")
  })

  pi.on("agent_start", async (_event, ctx) => {
    await emit(ctx, "working", "Pi working")
  })

  pi.on("tool_execution_start", async (event, ctx) => {
    await emit(ctx, "working", `Using ${event.toolName}`)
  })

  // agent_end can be followed by an automatic retry, compaction recovery, or
  // queued follow-up. agent_settled is the first reliable idle boundary.
  pi.on("agent_settled", async (_event, ctx) => {
    await emit(ctx, "idle", "Pi finished")
  })

  pi.on("ui_prompt_start", async (event, ctx) => {
    const detail = event.title || event.kind || "input"
    await emit(ctx, "input", `Waiting: ${detail}`)
  })

  pi.on("ui_prompt_end", async (_event, ctx) => {
    if (ctx.isIdle()) {
      await emit(ctx, "idle", "Pi ready")
    } else {
      await emit(ctx, "working", "Pi working")
    }
  })

  pi.on("session_shutdown", async (_event, ctx) => {
    await emit(ctx, "idle", "Pi stopped")
  })
}
