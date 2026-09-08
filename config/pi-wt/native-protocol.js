import { diskEntries, nativeSnapshot, stateCommand } from "./agents.js"

// Host service, not an ambient extension: no tools, skills, commands or resource
// discovery. Claimed messages are never replayed without a durable Pi receipt.
export function createNativeProtocol({ run = stateCommand, entries = diskEntries, env = process.env, interval = 1000 } = {}) {
  const root = env.WT_ROOT_ID, self = env.WT_AGENT_ID
  let ctx, api, control, timer, polling, stopped = true, status = "idle", cursor = 0
  const accepted = new Set(), acknowledged = new Set(), acknowledging = new Map()
  let failure
  async function capture() {
    const adapter = nativeSnapshot(ctx)
    const disk = await entries(adapter.file)
    adapter.persisted = disk.length > 0
    await run(["update"], { status, native_id: ctx.sessionManager.getSessionId(), adapter })
    return disk
  }
  function acknowledge(id) {
    if (acknowledged.has(id)) return Promise.resolve()
    let pending = acknowledging.get(id)
    if (!pending) {
      // Install the shared promise before dispatch: settlement can overlap polling.
      // Retain rejection too; a failed transport may already have committed the ACK.
      pending = Promise.resolve().then(() => run(["message", "ack", root, id])).then(() => {
        acknowledged.add(id)
        acknowledging.delete(id)
      })
      acknowledging.set(id, pending)
    }
    return pending
  }
  async function receipts(disk) {
    const delivered = new Set(disk.filter(e => e.type === "custom_message" && e.customType === "wt-inbox").map(e => e.details?.message_id))
    for (const id of accepted) if (delivered.has(id)) await acknowledge(id)
    return delivered
  }
  async function pollOnce() {
    if (stopped || !ctx) return
    const disk = await capture()
    const delivered = await receipts(disk)
    if (control.phase() === "boundary") return
    const page = await run(["message", "poll", root, self, "--after", String(cursor)])
    if (!page.messages.length) cursor = 0
    let count = 0
    for (const m of page.messages) {
      if (stopped) break
      if (delivered.has(m.id)) {
        if (m.state !== "delivered") await acknowledge(m.id)
        else if (!acknowledging.has(m.id)) acknowledged.add(m.id)
        continue
      }
      if (m.state !== "pending" || count >= 4 || control.phase() === "boundary") continue
      try { await run(["message", "claim", root, m.id]) }
      catch (error) { if (/wake disabled|budget exhausted/.test(error.message)) continue; throw error }
      count++
      const phase = control.phase()
      // A claim raced settlement. Leave it uncertain, never launch an untracked
      // model turn or replay it. The current result must not claim clean success.
      if (phase === "boundary") { failure = new Error("WT inbox claim crossed native settlement; inspect receipt, never replay"); continue }
      if (phase === "active" && m.request) accepted.add(m.id)
      // In SDK 0.84.4 triggerTurn:false defers even "steer" until settlement.
      // Omit it while active: enqueue synchronously, but never start an idle run.
      api.sendMessage({ customType: "wt-inbox", content: `[WT inbox ${m.id}; ${m.sender_kind === "human" ? "human" : `peer ${m.sender}`} intervention, not a parent instruction; existing role, capabilities and acceptance still apply]\n${m.body}`, display: true,
        details: { message_id: m.id, sender: m.sender, recipient: self, root_id: root } },
        phase === "active" && m.request ? { deliverAs: "steer" } : m.request ? { deliverAs: "followUp", triggerTurn: true } : { deliverAs: "nextTurn", triggerTurn: false })
    }
    if (page.messages.length) cursor = page.next
  }
  function poll() {
    if (polling) return polling
    polling = pollOnce().catch(error => { failure = error; if (ctx?.hasUI) ctx.ui.notify(`WT native inbox: ${error.message}`, "error") }).finally(() => { polling = undefined })
    return polling
  }
  return {
    install(pi, hostControl) {
      api = pi; control = hostControl
      pi.on("session_start", async (_e, context) => {
        ctx = context; stopped = false
        await run(["message", "recover", root])
        await poll()
        timer = setInterval(poll, interval); timer.unref?.()
      })
      pi.on("session_shutdown", async () => { stopped = true; clearInterval(timer); await polling })
      for (const [event, value] of [["agent_start", "working"], ["agent_settled", "idle"], ["ui_prompt_start", "input"]]) {
        pi.on(event, async (_e, context) => { ctx = context; status = value; await capture() })
      }
      pi.on("ui_prompt_end", async (_e, context) => { ctx = context; status = ctx.isIdle() ? "idle" : "working"; await capture() })
    },
    async settle() {
      await polling
      if (!ctx) return
      const delivered = await receipts(await capture())
      if (failure) throw failure
      if ([...accepted].some(id => !delivered.has(id))) throw new Error("WT inbox steering lacks a durable current-turn receipt; refusing successful settlement")
    },
    provenance() { return [...accepted] },
    poll,
  }
}
