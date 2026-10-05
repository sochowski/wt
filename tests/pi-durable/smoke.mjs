// Explicitly opt-in, bounded real provider smoke. Never login/refresh/write auth.
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, rm, readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { createModels } from '@earendil-works/pi-ai/models';
import { anthropicProvider } from '@earendil-works/pi-ai/providers/anthropic';
import { Harness, createRegistry, defineExtension } from '@earendil-works/pi-durable';
import { CodingTools } from '@earendil-works/pi-durable/tools';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
if (process.env.WT_DURABLE_REAL_SMOKE !== '1') throw new Error('Requires WT_DURABLE_REAL_SMOKE=1 and explicit SDK/auth paths');
if (!process.env.WT_PI_SDK_PATH || !process.env.WT_PI_AUTH_PATH) throw new Error('Supply WT_PI_SDK_PATH (package root) and WT_PI_AUTH_PATH');
const sdkPath = resolve(process.env.WT_PI_SDK_PATH);
const { readStoredCredential } = await import(pathToFileURL(join(sdkPath, 'dist/index.js')));
const sdkVersion = JSON.parse(await readFile(join(sdkPath, 'package.json'), 'utf8')).version;
const authPath = resolve(process.env.WT_PI_AUTH_PATH);
const digest = async () => createHash('sha256').update(await readFile(authPath)).digest('hex');
const before = await digest(); // Never log credential data or its digest.
const credential = readStoredCredential('anthropic', authPath);
if (!credential || credential.type !== 'oauth' || credential.expires < Date.now() + 120000) throw new Error('Requires fresh Anthropic OAuth; no refresh permitted');
const credentials = {
  read: async p => p === 'anthropic' ? structuredClone(credential) : undefined,
  list: async () => [{ providerId: 'anthropic', type: 'oauth' }],
  modify: async () => { throw new Error('Smoke cannot refresh or change credentials'); },
  delete: async () => { throw new Error('Smoke cannot logout'); },
};
const directory = await mkdtemp(join(tmpdir(), 'wt-durable-real-smoke-'));
// SDK auth is explicitly read-only. All execution paths point at this disposable HOME.
process.env.HOME = directory;
process.env.PI_CODING_AGENT_DIR = join(directory, 'pi');
process.env.WT_STATUS_DIR = join(directory, 'wt-state');
process.env.WT_BASE_DIR = join(directory, 'worktrees');
process.env.WT_CONFIG_DIR = join(directory, 'config');
const models = createModels({ credentials }); models.setProvider(anthropicProvider());
const ref = { provider: 'anthropic', modelId: 'claude-haiku-4-5-20251001' };
assert.ok(models.getModel(ref.provider, ref.modelId));
let requests = 0;
const streamSimple = models.streamSimple.bind(models);
models.streamSimple = (m, transcript, options) => {
  if (++requests > 3) throw new Error('Smoke request cap exceeded');
  return streamSimple(m, transcript, { ...options, maxTokens: 256, maxRetries: 0 });
};
const registry = createRegistry();
registry.install(defineExtension({ name: 'smoke-read-only', tools: [CodingTools.tools.find(t => t.name === 'read')] }));
const env = new NodeExecutionEnv({ cwd: directory });
const database = join(directory, 'session.sqlite');
let harness;
const deadline = setTimeout(() => { console.error('Smoke exceeded 60s bound'); process.exit(1); }, 60000);
try {
  await writeFile(join(directory, 'input.txt'), 'WT_DURABLE_OK');
  harness = await Harness.open(await openNodeSqliteStorage(database), {
    models, registry, env: () => env, settings: { stream: { timeoutMs: 20000, maxTokens: 256, maxRetries: 0 }, retry: { maxRetries: 0 }, compaction: { enabled: false } },
  }, context);
  const root = await harness.root(context, { agent: { model: ref, thinkingLevel: 'off', cwd: directory } });
  const s = await root.submit({ type: 'input', content: 'Use read on input.txt, then reply with only its exact contents.', requestId: 'real-smoke-1' }, context);
  const result = await s.wait(context);
  assert.equal(result.status, 'done');
  const transcript = (await root.entries({}, 100, undefined, context)).items;
  assert.ok(transcript.some(e => e.kind === 'pi.tool-result' && e.model[0].toolName === 'read' && !e.model[0].isError));
  const final = transcript.find(e => e.id === result.answer).model[0].content.filter(b => b.type === 'text').map(b => b.text).join('');
  assert.equal(final.trim(), 'WT_DURABLE_OK');
  const usage = await harness.usage(context);
  await harness.close(context);
  harness = await Harness.open(await openNodeSqliteStorage(database), { models, registry }, context);
  const recovered = await harness.submission(s.id, context);
  assert.equal((await recovered.status(context)).status, 'done');
  assert.equal(await digest(), before);
  console.log(JSON.stringify({ status: 'passed', durable: '1.0.3', piAI: '1.0.3', sdk: sdkVersion, node: process.version, ...ref,
    requests, answer: final.trim(), tool: 'read', closeReopen: 'done', authFileUnchanged: true, usage }, null, 2));
} catch {
  // Provider failures can contain headers or tokens: do not print arbitrary error objects.
  console.error('Real provider smoke failed; auth was not modified. Inspect locally without logging credentials.');
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  await harness?.close(context);
  await env.cleanup(context);
  await rm(directory, { recursive: true, force: true });
}
