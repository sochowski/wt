import assert from "node:assert/strict"
import test from "node:test"
import { createNativeProtocol } from "./native-protocol.js"

for (const rejectAck of [false, true]) {
  test(`overlapping settlement/poll coalesces ACK and propagates rejection=${rejectAck}`, async t => {
    const hooks = new Map(), disk = [], sent = []
    const inbox = [{ id: "steering", state: "pending", sender_kind: "human", sender: "human", body: "steer", request: true }]
    let phase = "active", ackCalls = 0, release, started
    const gate = new Promise(resolve => { release = resolve })
    const ackStarted = new Promise(resolve => { started = resolve })
    const ctx = { hasUI: false, isIdle: () => true, sessionManager: { getSessionId: () => "native", getSessionFile: () => "/native.jsonl", getLeafId: () => "leaf" } }
    const protocol = createNativeProtocol({
      env: { WT_ROOT_ID: "root", WT_AGENT_ID: "child" }, interval: 60000,
      entries: async () => disk,
      run: async args => {
        if (args[0] !== "message") return {}
        if (args[1] === "poll") return { messages: inbox, next: 0 }
        const message = inbox.find(m => m.id === args[3])
        if (args[1] === "claim") message.state = "claimed"
        if (args[1] === "ack") {
          ackCalls++
          started()
          await gate
          if (rejectAck) throw new Error("injected ACK transport failure")
          assert.ok(["pending", "claimed", "uncertain"].includes(message.state), "message not eligible for receipt")
          message.state = "delivered"
        }
        return {}
      },
    })
    protocol.install({ on(name, fn) { hooks.set(name, fn) }, sendMessage(...args) { sent.push(args) } }, { phase: () => phase })
    t.after(async () => { release(); await hooks.get("session_shutdown")() })
    await hooks.get("session_start")({}, ctx)
    await protocol.poll()
    assert.equal(sent.length, 1)
    disk.push({ type: "custom_message", customType: "wt-inbox", details: { message_id: "steering" } })
    phase = "boundary"
    const settling = protocol.settle()
    await ackStarted
    const polling = protocol.poll()
    await new Promise(resolve => setImmediate(resolve))
    const concurrentCalls = ackCalls
    release()
    const results = await Promise.allSettled([settling, polling])
    assert.equal(concurrentCalls, 1, "only one ACK may be in flight for the durable ID")
    if (rejectAck) {
      assert.equal(results[0].status, "rejected")
      assert.match(results[0].reason.message, /injected ACK transport failure/)
      await assert.rejects(protocol.settle(), /injected ACK transport failure/)
      await protocol.poll()
      assert.equal(ackCalls, 1, "uncertain ACK must not be retried or cached as success")
      assert.equal(inbox[0].state, "claimed")
    } else {
      assert.deepEqual(results.map(result => result.status), ["fulfilled", "fulfilled"])
      await protocol.poll()
      await protocol.settle()
      phase = "idle"
      inbox.push({ id: "idle", state: "pending", sender_kind: "human", sender: "human", body: "follow up", request: true })
      await protocol.poll()
      assert.equal(sent.length, 2, "idle delivery continues after overlapping settlement")
      assert.equal(ackCalls, 1)
    }
  })
}
