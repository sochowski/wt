import assert from "node:assert/strict"
import test from "node:test"
import { createNativeProtocol } from "./native-protocol.js"

for (const restriction of ["extensions:[]", "denyExtensions"]) {
  test(`tool-free native protocol under ${restriction}: steering, provenance, receipt settlement`, async () => {
    const hooks = new Map(), sent = [], calls = [], disk = []
    let phase = "boundary", inbox = []
    const ctx = { hasUI: false, isIdle: () => true, sessionManager: { getSessionId: () => "native", getSessionFile: () => "/native.jsonl", getLeafId: () => "leaf" } }
    const protocol = createNativeProtocol({ env: { WT_ROOT_ID: "root", WT_AGENT_ID: "child" }, interval: 60000, entries: async () => disk, run: async (args, input) => {
      calls.push([args, input])
      if (args[0] === "message" && args[1] === "poll") return { messages: inbox, next: 0 }
      if (args[1] === "claim") inbox.find(m => m.id === args[3]).state = "claimed"
      if (args[1] === "ack") {
        const message = inbox.find(m => m.id === args[3])
        assert.ok(message && ["pending", "claimed", "uncertain"].includes(message.state), "message not eligible for receipt")
        message.state = "delivered"
      }
      return {}
    } })
    protocol.install({ on(name, fn) { hooks.set(name, fn) }, sendMessage(...args) { sent.push(args) }, registerTool() { assert.fail("ambient tools forbidden") } }, { phase: () => phase })
    await hooks.get("session_start")({}, ctx)
    inbox = [{ id: "message", sender_kind: "human", sender: "human", body: "steer", state: "pending", request: true }]
    await protocol.poll()
    assert.equal(sent.length, 0, "no delivery across startup/publication boundary")
    phase = "active"
    await hooks.get("agent_start")({}, ctx)
    assert.equal(calls.at(-1)[1].status, "working")
    await protocol.poll()
    assert.deepEqual(sent[0][1], { deliverAs: "steer" })
    assert.match(sent[0][0].content, /not a parent instruction/)
    assert.deepEqual(protocol.provenance(), ["message"])
    phase = "boundary"
    await assert.rejects(protocol.settle(), /durable current-turn receipt/)
    disk.push({ type: "custom_message", customType: "wt-inbox", details: { message_id: "message" } })
    await protocol.settle()
    assert.ok(calls.some(([args]) => args[1] === "ack" && args[3] === "message"))
    await protocol.poll()
    await protocol.poll()
    await protocol.settle()
    phase = "idle"
    inbox.push({ id: "idle-message", sender_kind: "human", sender: "human", body: "follow up", state: "pending", request: true })
    await protocol.poll()
    assert.equal(sent.length, 2, "polling must continue after a confirmed receipt")
    assert.deepEqual(sent[1][1], { deliverAs: "followUp", triggerTurn: true })
    disk.push({ type: "custom_message", customType: "wt-inbox", details: { message_id: "idle-message" } })
    await protocol.poll()
    await protocol.poll()
    await protocol.settle()
    assert.equal(calls.filter(([args]) => args[1] === "ack" && args[3] === "message").length, 1)
    assert.equal(calls.filter(([args]) => args[1] === "ack" && args[3] === "idle-message").length, 1)
    await hooks.get("agent_settled")({}, ctx)
    assert.equal(calls.at(-1)[1].status, "idle")
    await hooks.get("session_shutdown")()
  })
}
