import registerViews from "./views.js"
import { wtHostContext } from "./orientation.js"
import { fileURLToPath } from "node:url"
import { execFile } from "node:child_process"
import { readFile } from "node:fs/promises"

// Native Pi JSONL is read-only here. A sendMessage call queues memory, not a
// durable receipt; only a custom_message found on disk acknowledges delivery.
export function nativeSnapshot(ctx) {
  return {
    version: 1, file: ctx.sessionManager.getSessionFile() || "",
    persisted: false, leaf: ctx.sessionManager.getLeafId() || "",
    provider: ctx.model?.provider || "", model: ctx.model?.id || "",
    thinking: ctx.thinkingLevel || "",
  }
}
export async function diskEntries(file) {
  if (!file) return []
  try { return (await readFile(file, "utf8")).trim().split("\n").filter(Boolean).map(JSON.parse) }
  catch (error) { if (error.code === "ENOENT") return []; throw error }
}
function parseOutput(stdout) { if (!stdout.trim()) return null; try { return JSON.parse(stdout) } catch { return stdout } }
export function stateCommand(args, input) {
  return new Promise((resolve, reject) => {
    const child = execFile(process.env.WT_STATE || "wt-state", ["worktree", ...args],
      { env: process.env, maxBuffer: 1024 * 1024, timeout: 10000 },
      (error, stdout, stderr) => error ? reject(new Error(stderr || error.message)) : resolve(parseOutput(stdout)))
    child.stdin.on("error", () => {})
    child.stdin.end(input ? JSON.stringify(input) : undefined)
  })
}

export default function registerAgents(pi, { run = stateCommand, entries = diskEntries, interval = 1000, renderInbox } = {}) {
  if (renderInbox) pi.registerMessageRenderer("wt-inbox", renderInbox)
  const root = process.env.WT_ROOT_ID, self = process.env.WT_AGENT_ID
  registerViews(pi, run, root)
  pi.on("resources_discover", () => ({ skillPaths: [fileURLToPath(new URL("../skills/wt/SKILL.md", import.meta.url))] }))
  pi.on("before_agent_start", async (event, context) => {
    const text = await wtHostContext({ cwd: context.cwd, tools: pi.getActiveTools(), systemPrompt: event.systemPrompt }, process.env, root => run(["workspace", root]))
    if (text) return { systemPrompt: `${event.systemPrompt}\n\n${text}` }
  })
  let ctx, timer, stopped = true, busy = false, status = "idle", warned = false, cursor = 0
  let queue = Promise.resolve()
  const serial = fn => { const next = queue.then(fn); queue = next.catch(() => {}); return next }
  async function captureRaw() {
    const adapter = nativeSnapshot(ctx)
    const disk = await entries(adapter.file)
    adapter.persisted = disk.length > 0
    await run(["update"], { status, native_id: ctx.sessionManager.getSessionId(), adapter })
    return disk
  }
  const capture = () => serial(captureRaw)
  async function poll() {
    if (stopped || busy || !ctx) return
    busy = true
    try {
      await serial(async () => {
      const disk = await captureRaw()
      const delivered = new Set(disk.filter(e => e.type === "custom_message" && e.customType === "wt-inbox").map(e => e.details?.message_id))
      const page = await run(["message", "poll", root, self, "--after", String(cursor)])
      let claimed = 0
      // Continue bounded pages across polls; wrap to revisit pending/uncertain
      // receipts. Never download the delivered history or an unbounded backlog.
      if (page.messages.length === 0) cursor = 0
      for (const m of page.messages) {
        if (stopped) break
        if (delivered.has(m.id)) {
          if (m.state !== "delivered") await run(["message", "ack", root, m.id])
          continue
        }
        if (m.state !== "pending") continue
        if (claimed >= 4) continue
        try { await run(["message", "claim", root, m.id]) }
        catch (error) { if (/wake disabled|budget exhausted/.test(error.message)) continue; throw error }
        claimed++
        pi.sendMessage({ customType: "wt-inbox", content: `${m.sender_kind === "human" ? "Human" : `Peer ${m.sender}`}:\n${m.body}`, display: true,
          details: { message_id: m.id, sender: m.sender, recipient: self, root_id: root } },
          m.request ? { deliverAs: "followUp", triggerTurn: true } : { deliverAs: "nextTurn", triggerTurn: false })
      }
      if (page.messages.length) cursor = page.next
      })
    } catch (error) {
      if (!warned && ctx.hasUI) ctx.ui.notify(`wt inbox/checkpoint: ${error.message}`, "error")
      warned = true
    } finally { busy = false }
  }
  pi.on("session_start", async (_event, context) => {
    ctx = context; stopped = false; status = "idle"
    clearInterval(timer)
    // A reload loses Pi's in-memory nextTurn queue, too. Never replay a claim
    // automatically just because the native receipt is missing.
    await run(["message", "recover", root])
    await poll()
    timer = setInterval(poll, interval); timer.unref?.()
  })
  pi.on("session_shutdown", async () => {
    stopped = true; clearInterval(timer)
    if (ctx) { status = "idle"; await capture() }
  })
  for (const [event, value] of [["agent_start", "working"], ["agent_settled", "idle"], ["ui_prompt_start", "input"]]) {
    pi.on(event, async (_e, context) => { ctx = context; status = value; await capture() })
  }
  pi.on("ui_prompt_end", async (_e, context) => { ctx = context; status = ctx.isIdle() ? "idle" : "working"; await capture() })
  for (const event of ["session_tree", "model_select", "thinking_level_select"]) pi.on(event, async (_e, context) => { ctx = context; await capture() })
  // A node is one native conversation. Replacing it in-place would evade the
  // transcript writer lock. Create a new named agent instead.
  for (const event of ["session_before_switch", "session_before_fork"]) pi.on(event, async (_e, context) => {
    if (context.hasUI) context.ui.notify("wt: create a named peer for a new conversation; this node keeps its exact transcript", "warning")
    return { cancel: true }
  })
  pi.registerCommand("wt-checkpoint", {
    description: "Persist the selected Pi branch checkpoint (after first assistant response)",
    handler: async (_args, context) => { ctx = context; pi.appendEntry("wt-checkpoint", { version: 1 }); await capture() },
  })
  pi.registerTool({
    name: "wt_agent", label: "Worktree agents",
    description: "Named interactive peer agents in background WT windows. Create with an explicit assigned checkout alias target for checkout-scoped work; attachments alone are not assignments. Peers receive their task, not your full conversation. Max 8 agents. Requests wake peers at safe follow-up boundaries; notifications wait for the next user turn. No keyboard input. A root has a finite persisted automatic-delivery budget, not a token/cost budget. Parent is supervision, not ownership. Output capped at 32KB.",
    parameters: { type: "object", additionalProperties: false, required: ["action"], properties: {
      action: { type: "string", enum: ["list", "create", "read", "message", "reparent", "stop"] },
      task: { type: "string" }, notify: { type: "boolean" }, name: { type: "string" }, agent: { type: "string" }, parent: { type: "string" },
      target: { type: "string" }, body: { type: "string" }, message_id: { type: "string" },
    } },
    async execute(callID, p) {
      let args
      switch (p.action) {
        case "list": args = ["agents", "list", root]; break
        case "create": if (!p.name) throw new Error("name required"); args = ["agents", "create", root, p.name, "--parent", p.parent || self, "--cwd", p.target || "root"]; if (p.task) args.push("--task", p.task); break
        case "message": if (!p.agent || !p.body) throw new Error("agent and body required"); args = ["message", "send", root, self, p.agent, p.body, "--id", p.message_id || callID]; if (p.notify) args.push("--notify"); break
        case "reparent": if (!p.parent) throw new Error("parent ID or '-' required"); args = ["agents", "reparent", root, self, p.parent]; break
        case "read": case "stop": if (!p.agent) throw new Error("agent required"); args = ["agents", p.action, root, p.agent]; break
        default: throw new Error("unknown action")
      }
      const result = await run(args)
      const text = typeof result === "string" ? result : JSON.stringify(result)
      return { content: [{ type: "text", text: text.length > 32768 ? text.slice(0, 32768) + "\n[truncated; use wt CLI for full output]" : text }], details: {} }
    },
  })
  return { poll }
}
