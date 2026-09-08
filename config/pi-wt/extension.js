import registerAgents, { stateCommand } from "./agents.js"
import registerPresentation from "./presentation.js"
import registerStatusIntegration from "./status.js"
import registerNativeBootstrap from "./native-bootstrap.js"

// This extension is installed globally so Pi can discover it before project
// trust is resolved, but it must stay inert outside a wt-managed launch.
const wtSession = process.env.WT_SESSION

export default function WtExtension(pi, { renderInbox } = {}) {
  if (!wtSession || process.env.PI_SUBAGENT_CHILD) return

  if (process.env.WT_ROOT_ID && process.env.WT_AGENT_ID && process.env.WT_RUNTIME_ID) {
    const nativeReady = registerNativeBootstrap(pi)
    registerAgents(pi, { renderInbox })
    registerPresentation(pi, { session: wtSession, managedRun: async (_ctx, action, payload, signal) => {
      if (signal?.aborted) throw new Error("Presentation cancelled")
      if (action === "deck-show") {
        const result = await stateCommand(["present", process.env.WT_ROOT_ID, payload.target || "root"], payload)
        return result
      }
      if (action === "clear") return stateCommand(["present-clear", process.env.WT_ROOT_ID])
      return { ok: true }
    } })
    return nativeReady
  }

  registerStatusIntegration(pi)
  registerPresentation(pi, { session: wtSession })
}
