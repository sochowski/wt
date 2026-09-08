// Independent of pi-subagents imports: an absent, old or failed package must
// leave this guard installed. The versioned event is the dependency contract.
export default function registerNativeBootstrap(pi, { env = process.env, loadProvider = () => import("./native-provider.js") } = {}) {
  if (!env.WT_ROOT_ID || !env.WT_AGENT_ID || !env.WT_RUNTIME_ID || env.PI_SUBAGENT_CHILD) return
  const name = "wt-interactive-v1"
  env.PI_SUBAGENT_REQUIRED_NATIVE_PROVIDER = name
  let registration, provider
  let failure = "Compatible pi-subagents required-native-provider v1 acknowledgement missing; headless fallback is forbidden."
  const acknowledge = () => {
    if (registration) return true
    if (!provider) return false
    try {
      const request = { version: 1, provider }
      pi.events.emit("pi-subagents:required-native-provider:v1", request)
      if (!request.result?.ok || typeof request.result.dispose !== "function") return false
      if (request.result.wtNativeProviderContract !== 1) {
        request.result.dispose()
        failure = "pi-subagents acknowledged an older native contract; WT native-provider contract 1 is required. Headless fallback is forbidden."
        return false
      }
      registration = request.result
      return true
    } catch (error) { failure = `${error.message}; headless fallback is forbidden.`; return false }
  }
  pi.on("tool_call", event => {
    if (event.toolName === "subagent" && !acknowledge()) return { block: true, reason: failure }
  })
  pi.on("session_start", () => { acknowledge() })
  pi.on("session_shutdown", () => { registration?.dispose(); registration = undefined })
  // Guard first, optional implementation second. Retry acknowledgement at first
  // tool call because the package's session owner may initialize after WT.
  return loadProvider().then(module => { provider = module.createWtNativeProvider(env) })
    .catch(error => { failure = `${error.message}; headless fallback is forbidden.` })
}
