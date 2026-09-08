import assert from "node:assert/strict";
import { test } from "node:test";
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createNativeHostDriver } from "./native-provider.js";

test("native host finish attributes human steering without turning empty output into success", async () => {
  const root = mkdtempSync(join(tmpdir(), "wt-native-human-intervention-"));
  const previous = { ...process.env };
  try {
    const state = join(root, "state-fixture");
    const records = join(root, "commands.jsonl");
    writeFileSync(state, `#!/usr/bin/env node\nconst fs=require('node:fs'); const request=JSON.parse(fs.readFileSync(0,'utf8')); fs.appendFileSync(${JSON.stringify(records)},JSON.stringify({operation:process.argv[4],request})+'\\n'); console.log('{}');\n`, { mode: 0o700 });
    process.env.WT_STATE = state;
    process.env.WT_AGENT_ID = "child";
    process.env.WT_RUNTIME_ID = "runtime";
    const sessionFile = join(root, "session.jsonl");
    writeFileSync(sessionFile, "");
    const child = { sessionId: "native", sessionFile, nativeLeaf: "leaf", modelId: "fixture/model", thinkingLevel: "low", humanIntervention: { source: "interactive", accepted: 1, delivered: 1, firstAcceptedAt: 1, lastAcceptedAt: 1 }, messages: [{ role: "assistant", content: [{ type: "text", text: "steered output" }], stopReason: "stop" }] };
    const driver = createNativeHostDriver({ jobId: "job", turnId: "turn" });
    await driver.finish(child);
    child.messages[0].content = [];
    await driver.finish(child);
    const finished = readFileSync(records, "utf8").trim().split("\n").map(line => JSON.parse(line)).filter(record => record.operation === "finish");
    assert.equal(finished.length, 2);
    assert.equal(finished[0].request.result.state, "completed");
    assert.match(finished[0].request.result.output, /Human intervention: 1 accepted, 1 delivered/);
    assert.match(finished[0].request.result.output, /steered output/);
    assert.equal(finished[1].request.result.state, "failed");
    assert.equal(finished[1].request.result.error, "Native turn produced no text output");
  } finally {
    process.env = previous;
    rmSync(root, { recursive: true, force: true });
  }
});

for (const boundary of ["probe", "write", "link-definite", "link-uncertain", "cleanup", "success"]) {
  test(`continuation publication boundary: ${boundary}`, async () => {
    const { createWtNativeProvider } = await import("./native-provider.js");
    const { createHash } = await import("node:crypto");
    const config = { id: "next", cwd: "/fixture", steps: [{ agent: "worker", task: "follow-up" }] };
    const configDigest = createHash("sha256").update(JSON.stringify(config)).digest("hex");
    const commands = [], operations = [], turns = new Map();
    const fail = code => { throw Object.assign(new Error(`injected ${boundary}`), { code }); };
    const io = {
      command(operation, request) {
        commands.push({ operation, request });
        if (operation === "status") return { job: { native_id: "native", session_file: "/native.jsonl", contract_digest: "original" } };
        if (operation === "queue") {
          const id = `${request.request.run_id}-turn`;
          if (!turns.has(id)) turns.set(id, "queued");
          return { id, state: turns.get(id) };
        }
        if (operation === "cancel-unpublished") turns.set(request.turn, "failed");
        return {};
      },
      probe() { operations.push("probe"); if (boundary === "probe") fail("ESRCH"); },
      write() { operations.push("write"); if (boundary === "write") fail("EACCES"); },
      link() { operations.push("link"); if (boundary === "link-definite") fail("EACCES"); if (boundary === "link-uncertain") fail("EIO"); },
      unlink() { operations.push("unlink"); if (boundary === "cleanup") fail("EACCES"); },
    };
    const provider = createWtNativeProvider({ WT_ROOT_ID: "root", WT_AGENT_ID: "parent", WT_RUNTIME_ID: "runtime" }, io);
    const previous = { version: 1, provider: provider.name, jobId: "job", turnId: "old-turn", nativeId: "native", sessionFile: "/native.jsonl", configDigest: "original", controlPath: "/control.json", hostPid: 123 };
    const binding = provider.prepare({ ownerSessionId: "owner", parentSessionId: "parent-native", runId: "next", configDigest, config, previous });
    const result = provider.continue({ binding, config });
    const definite = ["probe", "write", "link-definite"].includes(boundary);
    assert.equal(result.publication, definite ? "not-published" : boundary === "link-uncertain" ? "uncertain" : "published");
    const cleanup = commands.filter(c => c.operation === "cancel-unpublished");
    assert.equal(cleanup.length, definite ? 1 : 0);
    if (definite) assert.deepEqual([cleanup[0].request.job, cleanup[0].request.turn, cleanup[0].request.runtime], ["job", "next-turn", "runtime"]);
    if (boundary === "probe") assert.equal(operations.includes("write"), false);
    assert.equal(operations.filter(op => op === "link").length, ["probe", "write"].includes(boundary) ? 0 : 1);
    assert.throws(() => provider.continue({ binding, config }), /queued turn/);
    if (definite) {
      assert.throws(() => provider.prepare({ ownerSessionId: "owner", parentSessionId: "parent-native", runId: "next", configDigest, config, previous }), /refusing re-publication/);
      const later = { ...config, id: "later", steps: [{ agent: "worker", task: "a new request, never replay the abandoned prompt" }] };
      const laterDigest = createHash("sha256").update(JSON.stringify(later)).digest("hex");
      const resumed = provider.prepare({ ownerSessionId: "owner", parentSessionId: "parent-native", runId: "later", configDigest: laterDigest, config: later, previous });
      assert.equal(resumed.previousTurnId, "old-turn");
      assert.equal(resumed.turnId, "later-turn");
      assert.equal(resumed.hostPid, previous.hostPid);
      provider.cancelPrepared(resumed, "fixture cleanup before publication");
    }
  });
}
