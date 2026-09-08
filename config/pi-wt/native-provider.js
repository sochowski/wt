// WT transport for the acknowledged pi-subagents native-provider v1 contract.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, writeFileSync, linkSync, unlinkSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createNativeProtocol } from "./native-protocol.js";
import { wtHostContext } from "./orientation.js";

const digest = (text) => createHash("sha256").update(text).digest("hex");
function command(operation, request, env = process.env) {
  if (!env.WT_STATE) throw new Error("WT_STATE is required for native host control");
  return JSON.parse(execFileSync(env.WT_STATE, ["worktree", "_delegation", operation], {
    input: JSON.stringify(request), encoding: "utf8", env, timeout: 10000, maxBuffer: 1024 * 1024,
  }));
}

export function createWtNativeProvider(env = process.env, io = { command, probe: pid => process.kill(pid, 0), write: writeFileSync, link: linkSync, unlink: unlinkSync }) {
  const name = "wt-interactive-v1";
  const pending = new Map();
  if (!env.WT_ROOT_ID || !env.WT_AGENT_ID || !env.WT_RUNTIME_ID) throw new Error("WT native provider requires a live exact parent runtime");
  return {
    name, version: 1,
    prepare(input) {
      const step = input.config.steps[0];
      if (step.task.includes("{previous}")) throw new Error("WT native single-child launch does not support previous-output substitution");
      const contract = JSON.stringify(input.config);
      if (digest(contract) !== input.configDigest) throw new Error("WT native contract differs from package admission");
      const owner = { root_id: env.WT_ROOT_ID, parent_id: env.WT_AGENT_ID, parent_native_id: input.parentSessionId, owner_session_id: input.ownerSessionId, provider: name };
      const prompt = `Task: ${step.task}`;
      if (input.previous) {
        const previous = input.previous;
        const status = io.command("status", { owner, runtime: env.WT_RUNTIME_ID, job: previous.jobId, turn: previous.turnId }, env);
        if (status.job.native_id !== previous.nativeId || status.job.session_file !== previous.sessionFile || status.job.contract_digest !== (previous.conversationDigest ?? previous.configDigest)) throw new Error("WT continuation native/contract identity mismatch");
        const turn = io.command("queue", { owner, runtime: env.WT_RUNTIME_ID, job: previous.jobId, request: { run_id: input.runId, step_index: 0, request_id: input.runId, previous_turn_id: previous.turnId, contract_digest: status.job.contract_digest, prompt_digest: digest(prompt), prompt } }, env);
        if (turn.state !== "queued") throw new Error("Prepared turn is not queued; refusing re-publication of an existing turn.");
        const binding = { ...previous, runId: input.runId, previousTurnId: previous.turnId, turnId: turn.id, configDigest: input.configDigest, conversationDigest: status.job.contract_digest };
        pending.set(binding.jobId, { binding, owner });
        return binding;
      }
      const response = io.command("reserve", {
        admission: { version: 1, ...owner, run_id: input.runId, step_index: 0, cwd: step.cwd ?? input.config.cwd, role: step.agent, label: `${step.agent.replace(/[^a-zA-Z0-9_-]/g, "-").slice(0,20)}-${input.runId.slice(0,6)}`, contract_digest: input.configDigest, contract: input.config },
        runtime: env.WT_RUNTIME_ID,
        request: { run_id: input.runId, step_index: 0, request_id: input.runId, previous_turn_id: "", contract_digest: input.configDigest, prompt_digest: digest(prompt), prompt },
      }, env);
      if (response.turn.state !== "queued") throw new Error("Prepared turn is not queued; refusing re-publication of an existing turn.");
      const binding = { version: 1, provider: name, ownerSessionId: input.ownerSessionId, parentSessionId: input.parentSessionId, runId: input.runId, configDigest: input.configDigest, jobId: response.job.id, turnId: response.turn.id, driverModule: fileURLToPath(import.meta.url), controlPath: `${input.config.asyncDir}/native-control.json` };
      pending.set(binding.jobId, { binding, owner });
      return binding;
    },
    cancelPrepared(binding, error) {
      const prepared = pending.get(binding.jobId);
      if (!prepared || JSON.stringify(prepared.binding) !== JSON.stringify(binding)) throw new Error("WT prepared turn identity mismatch");
      io.command("cancel-unpublished", { owner: prepared.owner, runtime: env.WT_RUNTIME_ID, job: binding.jobId, turn: binding.turnId, error: String(error) }, env);
      pending.delete(binding.jobId);
    },
    continue(input) {
      const prepared = pending.get(input.binding.jobId);
      if (!prepared || JSON.stringify(prepared.binding) !== JSON.stringify(input.binding) || !input.binding.controlPath || !input.binding.hostPid) throw new Error("WT continuation does not match its queued turn");
      pending.delete(input.binding.jobId); // One publication attempt, including uncertain outcomes.
      const temp = `${input.binding.controlPath}.${input.binding.turnId}.tmp`;
      let publishing = false;
      try {
        io.probe(input.binding.hostPid);
        io.write(temp, JSON.stringify(input.config), { flag: "wx", mode: 0o600 });
        publishing = true;
        io.link(temp, input.binding.controlPath);
      } catch (error) {
        // Atomic link errors known to precede publication can release the queued
        // permit. EIO/unknown outcomes retain ownership for inspection, no replay.
        const definite = !publishing || ["ENOENT", "EACCES", "EPERM", "ENOSPC", "EROFS", "EXDEV"].includes(error.code);
        if (definite) {
          try { io.command("cancel-unpublished", { owner: prepared.owner, runtime: env.WT_RUNTIME_ID, job: input.binding.jobId, turn: input.binding.turnId, error: String(error) }, env); }
          catch (cleanup) { return { pid: input.binding.hostPid, publication: "uncertain", error: `Queued-turn cleanup unconfirmed: ${cleanup}` }; }
          pending.delete(input.binding.jobId);
        }
        return { pid: input.binding.hostPid, publication: definite ? "not-published" : "uncertain", error: String(error) };
      } finally {
        try { io.unlink(temp); } catch { /* A linked mailbox is authoritative, temp cleanup cannot undo publication. */ }
      }
      pending.delete(input.binding.jobId);
      return { pid: input.binding.hostPid, publication: "published" };
    },
    launch(input) {
      const prepared = pending.get(input.binding.jobId);
      if (!prepared || JSON.stringify(prepared.binding) !== JSON.stringify(input.binding)) throw new Error("WT native launch does not match its reservation");
      pending.delete(input.binding.jobId);
      return io.command("launch", { owner: prepared.owner, runtime: env.WT_RUNTIME_ID, job: input.binding.jobId, turn: input.binding.turnId, command: input.command, args: input.args, cwd: input.cwd, env: input.env }, env);
    },
  };
}

/** Host-side callbacks observe the package's sole SDK session; they register no tools. */
export function createNativeHostDriver(binding) {
  const protocol = createNativeProtocol();
  const identity = () => ({ job: binding.jobId, turn: binding.turnId, child: process.env.WT_AGENT_ID, runtime: process.env.WT_RUNTIME_ID });
  const checkpoint = (child, operation) => { const [provider, ...model] = (child.modelId ?? "").split("/"); return command(operation, { ...identity(), native: child.sessionId, snapshot: { version: 1, file: child.sessionFile, leaf: child.nativeLeaf ?? "", persisted: existsSync(child.sessionFile), provider, model: model.join("/"), thinking: child.thinkingLevel } }); };
  return {
    version: 1,
    protocol: (pi, control) => protocol.install(pi, control),
    settle: () => protocol.settle(),
    context(input) {
      return wtHostContext(input, process.env, (root) => JSON.parse(execFileSync(process.env.WT_STATE, ["worktree", "workspace", root], {
        encoding: "utf8", env: process.env, timeout: 10000, maxBuffer: 1024 * 1024,
      })));
    },
    async failStartup(error) { command("fail-host-startup", { ...identity(), error: String(error) }); },
    async bind(child) { checkpoint(child, "bind"); },
    async checkpoint(child) { checkpoint(child, "checkpoint"); },
    async claim(child, prompt) {
      if (child.sessionFile === undefined) throw new Error("WT native host has no transcript");
      const request = command("claim", identity());
      if (request.prompt !== prompt || request.prompt_digest !== digest(prompt) || request.contract_digest !== (binding.conversationDigest ?? binding.configDigest)) throw new Error("Native prompt does not match its one-shot WT dispatch permit; turn will not be replayed");
    },
    async finish(child, error) {
      checkpoint(child, "checkpoint");
      const last = [...child.messages].reverse().find((message) => message.role === "assistant");
      const nativeOutput = last?.content?.filter((part) => part.type === "text").map((part) => part.text).join("") ?? "";
      const intervention = child.humanIntervention;
      const inbox = protocol.provenance();
      const provenance = inbox.length ? `[WT inbox intervention during this delegated turn: ${inbox.join(", ")}; not an untouched delegation.]\n` : "";
      const output = provenance + (intervention ? `[Human intervention: ${intervention.accepted} accepted, ${intervention.delivered} delivered during this delegated turn; not an untouched delegation.]\n${nativeOutput}` : nativeOutput);
      const failure = error ? String(error) : last?.errorMessage ?? (last?.stopReason === "error" || last?.stopReason === "aborted" ? `Native model ended: ${last.stopReason}` : "");
      command("finish", { ...identity(), result: { state: failure || !nativeOutput.trim() ? "failed" : "completed", native_id: child.sessionId, session_file: child.sessionFile, leaf: child.nativeLeaf, output, error: failure || (!nativeOutput.trim() ? "Native turn produced no text output" : "") } });
    },
  };
}
