import assert from "node:assert/strict"
import test from "node:test"
import registerNativeBootstrap from "./native-bootstrap.js"

for (const dependency of ["missing", "old", "unloaded", "import-failed", "compatible-late"]) {
  test(`ordinary independent bootstrap: ${dependency}`, async () => {
    const hooks = new Map(), env = { WT_ROOT_ID: "root", WT_AGENT_ID: "parent", WT_RUNTIME_ID: "runtime" }
    let compatible = false, sideEffects = 0, disposed = 0
    const provider = { name: "wt-interactive-v1", version: 1 }
    const pi = { on(name, hook) { hooks.set(name, hook) }, events: { emit(name, request) {
      assert.equal(name, "pi-subagents:required-native-provider:v1")
      if (compatible || dependency === "old") request.result = { ok: true, ...(compatible ? { wtNativeProviderContract: 1 } : {}), dispose() { disposed++ } }
    } } }
    await registerNativeBootstrap(pi, { env, loadProvider: async () => {
      if (dependency === "import-failed") throw new Error("injected import failure")
      return { createWtNativeProvider: () => provider }
    } })
    assert.equal(env.PI_SUBAGENT_REQUIRED_NATIVE_PROVIDER, provider.name)
    hooks.get("session_start")()
    const call = () => { const result = hooks.get("tool_call")({ toolName: "subagent", input: { runs: { all: [] } } }); if (!result?.block) sideEffects++; return result }
    assert.equal(call().block, true)
    assert.equal(sideEffects, 0)
    if (dependency === "compatible-late") { compatible = true; assert.equal(call(), undefined); assert.equal(sideEffects, 1) }
    hooks.get("session_shutdown")()
    if (dependency === "old") assert.ok(disposed >= 1, "dispose an older acknowledged registration before refusing delegation")
    else assert.equal(disposed, dependency === "compatible-late" ? 1 : 0)
  })
}
test("restricted package children receive neither bootstrap nor ambient WT tools", async () => {
  let called = false
  await registerNativeBootstrap({ on() { called = true } }, { env: { WT_ROOT_ID: "root", WT_AGENT_ID: "child", WT_RUNTIME_ID: "runtime", PI_SUBAGENT_CHILD: "1" } })
  assert.equal(called, false)
})
