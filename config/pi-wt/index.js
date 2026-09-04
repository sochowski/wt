import registerPresentation from "./presentation.js"
import registerStatusIntegration from "./status.js"

// This extension is installed globally so Pi can discover it before project
// trust is resolved, but it must stay inert outside a wt-managed launch.
const wtSession = process.env.WT_SESSION

export default function WtExtension(pi) {
  if (!wtSession) return

  registerStatusIntegration(pi)
  registerPresentation(pi, { session: wtSession })
}
