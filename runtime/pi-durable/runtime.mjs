import { existsSync, lstatSync, realpathSync, readFileSync } from 'node:fs';
import { DatabaseSync } from 'node:sqlite';
import { dirname, join, resolve, relative, isAbsolute } from 'node:path';
import { fileURLToPath } from 'node:url';
import { homedir } from 'node:os';
import { promisify } from 'node:util';
import { execFile } from 'node:child_process';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { builtinModels } from '@earendil-works/pi-ai/providers/all';
import { clampThinkingLevel } from '@earendil-works/pi-ai/models';
import { Type, cleanupSessionResources } from '@earendil-works/pi-ai';
import { Harness, createRegistry, defineExtension, defineTool, defineDoc, section, GenerationTask, hook, ProviderDoc } from '@earendil-works/pi-durable';
import { CodingTools } from '@earendil-works/pi-durable/tools';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
import { wtHostContext } from '../../config/pi-wt/orientation.js';
import { resourcePrompt, readOnlyCredentials } from './resources.mjs';
import { wtTools } from './wt-tools.mjs';
import { observeJobTool, JobDoc } from './jobs.mjs';

export { context };
export const definition = 'wt-durable-v1';
export const dependencies = 'pi-durable=1.0.3;pi-ai=1.0.3;chord=1.0.3;pi-tui=0.87.1';
export const BridgeDoc = defineDoc({ kind: 'wt.bridge', version: 1, scope: 'conversation', history: 'latest', fork: 'initial', initial: () => ({ admissions: {}, notifications: {} }) });
const executeFile = promisify(execFile);
let hostCapability;
// Entrypoint consumes an inherited private FD. Never place this in tool/env data.
export function setHostCapability(value) { hostCapability = value; }
export async function stateCommand(args, input) {
  if (input && args[0] === '_durable-jobs' && ['host','finish'].includes(args[1])) input = { ...input, capability: hostCapability };
  const { stdout } = input === undefined
    ? await executeFile(process.env.WT_STATE, ['worktree', ...args], { timeout: 10000, maxBuffer: 1024 * 1024, env: process.env })
    : await new Promise((resolve, reject) => {
      const child = execFile(process.env.WT_STATE, ['worktree', ...args], { timeout: 20000, maxBuffer: 1024 * 1024, env: process.env }, (error, stdout) => error ? reject(error) : resolve({ stdout }));
      child.stdin.end(JSON.stringify(input));
    });
  if (!stdout.trim()) return undefined;
  try { return JSON.parse(stdout); } catch { return stdout; }
}
export async function humanSelfStop(launch) {
  return stateCommand(['_durable-human-stop'], { root: launch.root, agent: launch.agent, runtime: launch.runtime, capability: hostCapability });
}
export function identityValue(launch) {
  const d = launch.identity;
  if (!launch.root || !launch.agent || !d || d.conversation !== 1 || !/^[a-f0-9]{32}$/.test(d.uuid) || d.definition !== definition || d.dependencies !== dependencies || realpathSync(d.cwd) !== d.cwd || realpathSync(dirname(d.store)) !== dirname(d.store)) throw new Error('Incompatible durable launch identity');
  return { root: launch.root, agent: launch.agent, store: d.store, uuid: d.uuid, conversation: d.conversation, cwd: d.cwd, writer_lock: d.writer_lock, read_only: d.read_only === true, ...(d.job ? { job: d.job, role: d.role, tools: d.tools } : {}), definition, dependencies };
}
/** Validate the application envelope before Harness.open can reconcile running tasks. */
export function validateStore(launch, bootstrap = false) {
  const identity = identityValue(launch), path = launch.identity.store;
  if (!bootstrap && (!launch.identity.initialized || !existsSync(path))) throw new Error('Missing/uninitialized exact durable store');
  if (existsSync(path) && realpathSync(path) !== path) throw new Error('Noncanonical durable store');
  const existed = existsSync(path);
  const db = new DatabaseSync(path, { readOnly: !bootstrap });
  try {
    if (bootstrap && existed && !db.prepare("SELECT name FROM sqlite_master WHERE type='table' AND name='wt_identity'").get()) throw new Error('Refusing to adopt an unidentified existing store');
    if (bootstrap) {
      db.exec('BEGIN IMMEDIATE; CREATE TABLE IF NOT EXISTS wt_identity (singleton INTEGER PRIMARY KEY CHECK(singleton=1), value TEXT NOT NULL)');
      db.prepare('INSERT OR IGNORE INTO wt_identity VALUES(1,?)').run(JSON.stringify(identity));
    }
    const got = db.prepare('SELECT value FROM wt_identity WHERE singleton=1').get();
    if (!got || JSON.stringify(JSON.parse(got.value)) !== JSON.stringify(identity)) throw new Error('Exact durable store identity mismatch');
    if (bootstrap) db.exec('COMMIT');
  } finally { db.close(); }
  return identity;
}
export async function wtFence(launch) {
  const a = await stateCommand(['agents', 'show', launch.root, launch.agent]);
  if (a.stopped || !launch.runtime || a.runtime !== launch.runtime || a.cwd !== launch.identity.cwd || a.adapter?.backend !== 'durable' || JSON.stringify(a.adapter.durable) !== JSON.stringify(launch.identity)) throw new Error('Stale WT durable runtime');
  if (launch.identity.job) await stateCommand(['_durable-jobs','host'], { root: launch.root, child: launch.agent, runtime: launch.runtime });
  return a;
}
const mutations = new Set(['exec','writeFile','appendFile','truncateFile','flushFile','renameFile','createDir','remove','createTempDir','createTempFile']);
/** A host boundary independent of selected extensions: a reader cannot mutate/spawn. */
export function readOnlyEnvironment(environment) {
  return new Proxy(environment, { get(target,key) {
    if (mutations.has(key)) return async () => { throw new Error('Read-only durable host prohibits mutation/process execution'); };
    const value = Reflect.get(target,key);
    return typeof value === 'function' ? value.bind(target) : value;
  }, set() { throw new Error('Read-only durable host environment is immutable'); } });
}

// Match the pinned tool normalization BEFORE authorization, never after it.
export function normalizedToolPath(path) {
  const value = path.replace(/[\u00A0\u2000-\u200A\u202F\u205F\u3000]/g, ' ');
  return value.startsWith('@') ? value.slice(1) : value;
}
export function assignedToolPath(cwd, path) {
  if (path === '~') path = homedir();
  else if (path.startsWith('~/')) path = join(homedir(), path.slice(2));
  else if (path.startsWith('file://')) { try { path = fileURLToPath(path); } catch {} }
  const target = resolve(cwd, path);
  let existing = target;
  while (true) {
    try { lstatSync(existing); break; } catch (error) { if (error.code !== 'ENOENT' && error.code !== 'ENOTDIR') throw error; }
    const parent = dirname(existing);
    if (parent === existing) throw new Error('Missing assigned path');
    existing = parent;
  }
  const canonical = resolve(realpathSync(existing), relative(existing, target));
  const rel = relative(cwd, canonical);
  if (isAbsolute(rel) || rel === '..' || rel.startsWith('../')) throw new Error('Tool path escapes assigned durable checkout');
  return canonical;
}

// Check final I/O paths too: pinned read tries NFD/curly-quote/AM-PM variants.
// No claim of a race-proof OS sandbox against concurrent hostile symlink changes.
export function assignedEnvironment(environment, cwd) {
  const paths = new Set(['absolutePath','exists','canonicalPath','fileInfo','openBinaryReader','openTextLineReader','readTextFile','readTextLines','readBinaryFile','writeFile','appendFile','truncateFile','flushFile','createDir','remove','listDir','openDirReader']);
  return new Proxy(environment, { get(target, key) {
    const value = Reflect.get(target, key);
    if (paths.has(key)) return async (path, ...rest) => value.call(target, assignedToolPath(cwd, path), ...rest);
    if (key === 'renameFile') return async (a, b, ...rest) => value.call(target, assignedToolPath(cwd, a), assignedToolPath(cwd, b), ...rest);
    return typeof value === 'function' ? value.bind(target) : value;
  } });
}
function exactPathEnvironment(environment, cwd, expected) {
  return new Proxy(environment, { get(target,key) {
    // The argument was already normalized, resolved (including read fallbacks),
    // and authorized. Do not let the tool transform a canonical filename again.
    if (key === 'absolutePath') return async () => ({ok:true,value:expected});
    const value = Reflect.get(target,key);
    if (['openBinaryReader','fileInfo','readTextFile','writeFile'].includes(key)) return async (path,...rest) => {
      if (assignedToolPath(cwd,path) !== expected) throw new Error('Tool execution path differs from authorized receipt');
      return value.call(target,expected,...rest);
    };
    return typeof value === 'function' ? value.bind(target) : value;
  } });
}
async function jobToolArguments(env, cwd, name, args) {
  if (typeof args.path !== 'string') return args;
  let path = assignedToolPath(cwd, normalizedToolPath(args.path));
  if (name === 'read') {
    for (const variant of new Set([path, path.replace(/ (AM|PM)\./gi, '\u202F$1.'), path.normalize('NFD'), path.replace(/'/g, '\u2019'), path.normalize('NFD').replace(/'/g, '\u2019')])) {
      const checked = assignedToolPath(cwd, variant);
      const exists = await env.exists(checked, context);
      if (!exists.ok) throw new Error('Unable to validate read path');
      if (exists.value) { path = checked; break; }
    }
  }
  return { ...args, path };
}

function configuredSettings(agentDir) {
  const path = join(agentDir, 'settings.json');
  return existsSync(path) ? JSON.parse(readFileSync(path, 'utf8')) : {};
}
export function configuredThinking(settings, model, report = console.warn) {
  // Native Pi precedence and fallback; never read defaults on reopen.
  const requested = settings.modelThinkingLevels?.[`${model.provider}/${model.id}`] ?? settings.defaultThinkingLevel ?? 'medium';
  if (!['off','minimal','low','medium','high','xhigh','max'].includes(requested)) throw Object.assign(new Error('Invalid configured thinking level'), { code: 'WT_PRIMARY_THINKING' });
  const level = clampThinkingLevel(model, requested);
  if (level !== requested) report(`wt durable: configured thinking ${requested} is unsupported by the selected model; using ${level} (native Pi capability clamp, model unchanged).`);
  return level;
}
export function configuredModel(agentDir, settings = configuredSettings(agentDir)) {
  if (typeof settings.defaultProvider !== 'string' || !settings.defaultProvider || typeof settings.defaultModel !== 'string' || !settings.defaultModel) throw Object.assign(new Error('Durable WT requires explicit configured defaultProvider/defaultModel; no silent provider fallback'), { code: 'WT_PRIMARY_MODEL' });
  return { provider: settings.defaultProvider, modelId: settings.defaultModel };
}

/** One harness per owner; injected models/fence are for private fixture tests only. */
export async function openRuntime(launch, options = {}) {
  const fence = options.fence || (() => wtFence(launch));
  if (!options.bootstrap) await fence();
  validateStore(launch, options.bootstrap === true);
  const agentDir = options.agentDir || process.env.PI_CODING_AGENT_DIR || join(homedir(), '.pi', 'agent');
  const models = options.models || builtinModels({ credentials: readOnlyCredentials(process.env.WT_PI_AUTH_PATH || join(agentDir, 'auth.json')) });
  if (process.env.WT_DURABLE_SMOKE === '1') {
    let requests = 0;
    const stream = models.streamSimple.bind(models);
    models.streamSimple = (model, transcript, requestOptions) => {
      if (++requests > 3) throw new Error('Bounded smoke request cap exceeded');
      return stream(model, transcript, { ...requestOptions, maxTokens: 256, maxRetries: 0 });
    };
  }
  const settings = options.bootstrap ? configuredSettings(agentDir) : undefined;
  const initialModel = options.bootstrap ? (options.model || configuredModel(agentDir, settings)) : undefined;
  const selectedModel = initialModel && models.getModel(initialModel.provider, initialModel.modelId);
  if (initialModel && !selectedModel) throw Object.assign(new Error('Configured primary model is not supported by the pinned durable catalog; no fallback'), { code: 'WT_PRIMARY_MODEL' });
  const initialThinking = selectedModel ? configuredThinking(settings, selectedModel, options.reportDiagnostic) : undefined;
  const resources = resourcePrompt(launch.identity.cwd, agentDir, process.env.WT_DURABLE_TRUST_PROJECT === '1');
  const nodeEnv = options.environment || new NodeExecutionEnv({ cwd: launch.identity.cwd });
  const boundedEnv = launch.identity.job ? assignedEnvironment(nodeEnv, launch.identity.cwd) : nodeEnv;
  const env = launch.identity.read_only ? readOnlyEnvironment(boundedEnv) : boundedEnv;
  const registry = createRegistry();
  const ceiling = launch.identity.tools;
  const offeredCoding = CodingTools.tools.filter(t => (!launch.identity.read_only || t.name === 'read') && (!ceiling || ceiling.includes(t.name)));
  const guardedTools = offeredCoding.map(tool => defineTool({ ...tool, replay: 'unsafe', execute: async (args, api, ctx) => {
    await fence();
    if (realpathSync((await api.agent(ctx)).cwd) !== launch.identity.cwd) throw new Error('Tool cwd differs from assigned checkout');
    if (!launch.identity.job) return tool.execute(args, api, ctx);
    const reviewDiff = tool.name === 'read' && args.path === 'wt://review-diff' && launch.identity.role === 'reviewer';
    if (!reviewDiff) args = await jobToolArguments(env, launch.identity.cwd, tool.name, args);
    await observeJobTool(api, ctx, tool.name, args, 'intent');
    try {
      const doc = reviewDiff ? await api.snapshot(JobDoc, api.conversationId, ctx) : undefined;
      const result = reviewDiff ? { content: [{ type: 'text', text: doc?.intent?.reviewDiff || '' }] } : await tool.execute(args, typeof args.path === 'string' ? {...api,env:exactPathEnvironment(api.env,launch.identity.cwd,args.path)} : api, ctx);
      const meaningfulRead = tool.name !== 'read' || (result.content?.some(b => b.type === 'text' && b.text.length > 0) && !args.offset && args.limit === undefined && !result.details?.truncation?.truncated);
      await observeJobTool(api, ctx, tool.name, args, 'result', !result.isError && meaningfulRead);
      return result;
    } catch (error) {
      await observeJobTool(api, ctx, tool.name, args, 'result', false);
      throw error;
    }
  } }));
  // Small explicit transport, never an ordinary Pi extension or native provider.
  const wtRead = defineTool({ name: 'wt_workspace', description: 'Read assigned WT workspace/checkouts and independent named agents. Does not change focus.', parameters: Type.Object({}), replay: 'safe', execute: async () => {
    await fence(); // Safe replay bypasses beforeTool, so validate here too.
    return { content: [{ type: 'text', text: JSON.stringify(await stateCommand(['workspace', launch.root])) }] };
  } });
  const managedTools = options.bootstrap || launch.identity.read_only || launch.identity.job ? [] : wtTools(launch, fence, stateCommand);
  const workspaceTools = !ceiling || ceiling.includes('wt_workspace') ? [wtRead] : [];
  registry.install(defineExtension({ name: 'wt-host', tools: [...guardedTools, ...workspaceTools, ...managedTools], sections: [
    section('wt-instructions', () => `You are a coding assistant in WT. Work only on assigned resources; obey project instructions. Tool mutations/bash are not exactly-once across crashes.\n${resources.prompt}`),
    section('wt-delegation-results', async (input, ctx) => {
      if (launch.identity.job) return undefined;
      const doc = await input.read.snapshot(JobDoc, input.conversationId, ctx);
      return doc?.results ? `WT host-observed delegation results (only succeeded includes mandatory independent review):\n${JSON.stringify(doc.results)}` : undefined;
    }),
    section('wt-orientation', async () => options.bootstrap ? undefined : wtHostContext({ cwd: launch.identity.cwd, tools: [...guardedTools.map(t => t.name), ...workspaceTools.map(t => t.name), ...managedTools.map(t => t.name)], systemPrompt: '' }, process.env, root => stateCommand(['workspace', root])), { tag: false }),
  ], hooks: [hook(GenerationTask, { beforeRequest: async () => { await fence(); } })] }));
  const harness = await Harness.open(await openNodeSqliteStorage(launch.identity.store), {
    models, registry, env: () => env, settings: { retry: { maxRetries: 0 }, compaction: { enabled: false }, stream: { timeoutMs: process.env.WT_DURABLE_SMOKE === '1' ? 20000 : 60000, maxRetries: 0, ...(process.env.WT_DURABLE_SMOKE === '1' ? { maxTokens: 256 } : {}) } },
  }, context);
  try {
    const root = options.bootstrap ? await harness.root(context, { agent: { cwd: launch.identity.cwd, model: initialModel, thinkingLevel: initialThinking } }) : await harness.conversation(launch.identity.conversation, context);
    if (!root) throw new Error('Missing exact durable conversation');
    const agent = await root.agent(context);
    if (agent.cwd !== launch.identity.cwd || !models.getModel(agent.model.provider, agent.model.modelId)) throw new Error('Persisted cwd/model incompatible with runtime');
    let closed = false;
    return { launch, harness, root, models, resources, fence,
      async resume() { await fence(); harness.resume(); },
      async reportStatus(status) { if (options.reportStatus) return options.reportStatus(status); await fence(); await stateCommand(['hook',status]); },
      async input(content, mode = 'followUp', requestId) {
        await fence();
        return root.submit({ type: 'input', content, whenBusy: mode, requestId }, context);
      },
      async abort() { await fence(); await root.abort(context); },
      async close() {
        if (closed) return; closed = true;
        const provider = await harness.snapshot(ProviderDoc, root.id, context);
        try { await harness.close(context); }
        finally {
          // Codex retains a websocket/idle timer after generation settles.
          // Release only this conversation's public provider resources.
          try { if (provider?.sessionId) cleanupSessionResources(provider.sessionId); }
          finally { await env.cleanup(context); }
        }
      },
    };
  } catch (error) { await harness.close(context); await env.cleanup(context); throw error; }
}

/** Immutable identity record commits before admission; an uncertain retry reuses requestId. */
export async function admitMessage(runtime, message) {
  if (message.root_id !== runtime.launch.root || message.recipient !== runtime.launch.agent) throw new Error('Cross-recipient inbox admission');
  await runtime.fence();
  const key = `wt:${message.root_id}:${message.recipient}:${message.id}`;
  const immutable = { body: message.body, recipient: message.recipient, root: message.root_id, sender: message.sender, request: message.request, mode: 'followUp' };
  await runtime.root.commit(async tx => {
    const doc = await tx.doc(BridgeDoc, runtime.root.id);
    const old = doc.admissions[key];
    if (old && JSON.stringify(old.payload) !== JSON.stringify(immutable)) throw new Error('Inbox immutable payload mismatch');
    if (!old) doc.admissions[key] = { payload: immutable, submission: null };
    if (!message.request) doc.notifications[key] = immutable;
  }, context);
  if (!message.request) return null; // commit-only: no scheduling API.
  const submission = await runtime.input(message.body, 'followUp', key);
  await runtime.root.commit(async tx => { (await tx.doc(BridgeDoc, runtime.root.id)).admissions[key].submission = submission.id; }, context);
  return submission.id;
}
