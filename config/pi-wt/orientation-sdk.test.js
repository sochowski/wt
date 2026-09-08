// Opt-in real SDK capture with a headless TUI adapter. No model network or live WT state.
import assert from "node:assert/strict"
import test from "node:test"
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs"
import { join } from "node:path"
import { tmpdir } from "node:os"
import { pathToFileURL } from "node:url"
import { createNativeHostDriver } from "./native-provider.js"

const sdk = process.env.WT_ORIENTATION_TEST_SDK
const upstream = process.env.WT_ORIENTATION_TEST_UPSTREAM
const enabled = sdk && upstream
for (const extensionPolicy of ["extensions-empty", "deny-extensions", "other-provider"]) {
  for (const mode of ["append", "replace"]) {
    for (const tools of [[], ["read", "grep", "find", "ls"], ["read", "bash", "edit", "write"]]) {
      test(`real SDK native HOST orientation/continuation: ${extensionPolicy}/${mode}/${tools.join(",") || "[]"}`, { skip: !enabled }, async t => {
        assert.equal(process.env.WT_ORIENTATION_TEST_ISOLATED, "1", "explicit isolated harness required")
        const dir = mkdtempSync(join(tmpdir(), "wt-orientation-sdk-"))
        const oldEnv = { ...process.env }, oldFetch = globalThis.fetch
        const stdinTTY = Object.getOwnPropertyDescriptor(process.stdin, "isTTY"), stdoutTTY = Object.getOwnPropertyDescriptor(process.stdout, "isTTY")
        let host
        t.after(async () => {
          await host?.close()
          globalThis.fetch = oldFetch; process.env = oldEnv
          for (const [stream, descriptor] of [[process.stdin, stdinTTY], [process.stdout, stdoutTTY]]) {
            if (descriptor) Object.defineProperty(stream, "isTTY", descriptor); else delete stream.isTTY
          }
          rmSync(dir, { recursive: true, force: true })
        })
        const home = join(dir, "home"), agentDir = join(home, ".pi", "agent"), cwd = join(dir, "assigned")
        mkdirSync(agentDir, { recursive: true }); mkdirSync(cwd)
        Object.assign(process.env, { HOME: home, PI_CODING_AGENT_DIR: agentDir, PI_OFFLINE: "1", WT_ROOT_ID: "fixture-root", WT_AGENT_ID: "fixture-child", WT_RUNTIME_ID: "fixture-runtime" })
        delete process.env.PI_SUBAGENT_REQUIRED_NATIVE_PROVIDER
        writeFileSync(join(agentDir, "settings.json"), JSON.stringify({ retry: { enabled: false }, compaction: { enabled: false } }))
        writeFileSync(join(agentDir, "models.json"), JSON.stringify({ providers: { synthetic: { baseUrl: "https://synthetic.invalid/v1", apiKey: "fixture-key", models: [{ id: "orientation", name: "orientation", api: "openai-completions", reasoning: false, input: ["text"], contextWindow: 128000, maxTokens: 128, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }] } } }))
        const mapFile = join(dir, "map.json"), state = join(dir, "state")
        const map = { cwd: home, workspace: { home }, checkouts: [{ alias: "assigned-alias", path: cwd }] }
        writeFileSync(mapFile, JSON.stringify(map))
        writeFileSync(state, `#!/usr/bin/env node\nconst fs=require('node:fs');require('node:assert/strict').deepEqual(process.argv.slice(2),['worktree','workspace','fixture-root']);console.log(fs.readFileSync(${JSON.stringify(mapFile)},'utf8'))`, { mode: 0o755 })
        process.env.WT_STATE = state
        const requests = []
        globalThis.fetch = async (input, init) => {
          assert.equal(input instanceof Request ? input.url : String(input), "https://synthetic.invalid/v1/chat/completions")
          requests.push(JSON.parse(String(init?.body)))
          const chunk = { id: "synthetic", object: "chat.completion.chunk", created: 1, model: "orientation", choices: [{ index: 0, delta: { content: "Fixture result" }, finish_reason: "stop" }], usage: { prompt_tokens: 10, completion_tokens: 1, total_tokens: 11 } }
          return new Response(`data: ${JSON.stringify(chunk)}\n\ndata: [DONE]\n\n`, { headers: { "content-type": "text/event-stream" } })
        }
        const pi = await import(pathToFileURL(sdk).href)
        const { createNativeInteractiveHost } = await import(pathToFileURL(join(upstream, "src/runs/shared/native-interactive-host.ts")).href)
        const { buildInProcessChildLaunch } = await import(pathToFileURL(join(upstream, "src/runs/shared/child-launch.ts")).href)
        Object.defineProperty(process.stdin, "isTTY", { value: true, configurable: true })
        Object.defineProperty(process.stdout, "isTTY", { value: true, configurable: true })
        let creates = 0, session
        const binding = { version: 1, provider: extensionPolicy === "other-provider" ? "other-provider" : "wt-interactive-v1", ownerSessionId: "owner", parentSessionId: "parent", jobId: "job", turnId: "first", configDigest: "unchanged-contract" }
        const driver = { version: 1, context: extensionPolicy === "other-provider" ? undefined : createNativeHostDriver(binding).context, async bind() {}, async claim() {}, async finish() {} }
        host = createNativeInteractiveHost({ binding, driver, loadPiCodingAgent: async () => ({ ...pi,
          async createAgentSession(options) { creates++; const result = await pi.createAgentSession(options); session = result.session; return result },
          InteractiveMode: class {
            constructor(runtime) { this.runtime = runtime }
            async init() { await this.runtime.session.bindExtensions({ mode: "print", onError(error) { throw error.error } }) }
            async run() {}
          },
        }) })
        const input = { cwd, host: "runner", sessionEnabled: true, sessionFile: join(dir, "native.jsonl"), model: "synthetic/orientation", tools,
          ...(extensionPolicy === "deny-extensions" ? { capabilityCeiling: { version: 1, denyExtensions: true, sources: ["fixture"] } } : { extensions: [] }),
          allowNestedSubagents: false, inheritProjectContext: false, inheritGlobalContext: false, inheritSkills: false, waitToolEnabled: false,
          childAgentName: "worker", childIndex: 0, systemPrompt: "EXACT_ROLE_TEXT", systemPromptMode: mode }
        const launch = buildInProcessChildLaunch(input)
        const original = JSON.stringify(launch.session)
        assert.deepEqual(launch.session.tools, tools)
        assert.equal(launch.session.ambientExtensions, false)
        assert.equal(launch.session.noSkills, true)
        const child = await host.create(launch.session)
        await child.prompt("First delegated task")
        await host.dispose()
        writeFileSync(mapFile, JSON.stringify({ ...map, checkouts: [{ alias: "refreshed-alias", path: cwd }] }))
        host.beginContinuation({ ...binding, turnId: "second", previousTurnId: "first", nativeId: child.sessionId, sessionFile: child.sessionFile }, driver)
        const continued = await host.create(buildInProcessChildLaunch(input).session)
        await continued.prompt("Continuation task")
        assert.equal(creates, 1, "one exact SDK writer")
        assert.equal(child.sessionId, continued.sessionId)
        assert.equal(JSON.stringify(launch.session), original)
        assert.deepEqual(session.getActiveToolNames(), tools)
        assert.equal(requests.length, 2)
        for (const [i, request] of requests.entries()) {
          assert.deepEqual((request.tools ?? []).map(tool => tool.function.name), tools)
          const prompt = request.messages.filter(m => ["system", "developer"].includes(m.role)).map(m => m.content).join("\n")
          assert.ok(prompt.includes("EXACT_ROLE_TEXT"))
          if (extensionPolicy === "other-provider") {
            assert.doesNotMatch(prompt, /<wt_host_context>|assigned-alias|refreshed-alias|wt_view|wt view create/)
            continue
          }
          assert.equal(prompt.split("<wt_host_context>").length - 1, 1)
          assert.match(prompt, /At the first edit/)
          assert.ok(prompt.includes(i ? "refreshed-alias" : "assigned-alias"))
          if (i) assert.ok(!prompt.includes("assigned-alias"), "no stale continuation map")
          if (tools.includes("bash")) assert.match(prompt, /wt view create/)
          else { assert.doesNotMatch(prompt, /wt_view|wt view create/); assert.match(prompt, /Ask your supervisor/) }
          assert.ok(!JSON.stringify(request.messages.filter(m => !["system", "developer"].includes(m.role))).includes("<wt_host_context>"), "no persistent briefing messages")
        }
      })
    }
  }
}
