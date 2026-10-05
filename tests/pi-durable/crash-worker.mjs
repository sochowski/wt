// Child of contracts.test.mjs only. Uses real package storage/scheduler/tools.
import { appendFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { createModels } from '@earendil-works/pi-ai/models';
import { fauxProvider, fauxAssistantMessage, fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { Harness, createRegistry, defineExtension } from '@earendil-works/pi-durable';
import { CodingTools } from '@earendil-works/pi-durable/tools';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
const [directory, mode] = process.argv.slice(2);
process.on('message', () => {}); // Ref IPC while deliberately blocked.
const faux = fauxProvider({ tokensPerSecond: 20, tokenSize: { min: 1, max: 1 } });
const models = createModels();
models.setProvider(faux.provider);
const registry = createRegistry();
const env = new NodeExecutionEnv({ cwd: directory });
if (mode === 'generation' || mode === 'queued') {
  faux.setResponses([fauxAssistantMessage('Interrupted generation '.repeat(100))]);
} else {
  const name = mode === 'safe-read' ? 'read' : mode === 'unsafe-bash' ? 'bash' : mode;
  const original = CodingTools.tools.find(t => t.name === name);
  registry.install(defineExtension({ name: 'probe-coding', tools: [{ ...original,
    ...(mode === 'safe-read' ? { replay: 'safe' } : {}),
    execute: async (args, api, ctx) => {
      const result = await original.execute(args, api, ctx);
      await appendFile(join(directory, 'effects'), `${name}\n`);
      api.output('committed partial output');
      await api.details({ reachedEffect: true }, ctx);
      process.send({ type: 'effect' });
      await new Promise((_, reject) => ctx.abortSignal.addEventListener('abort', () => reject(new Error('closed')), { once: true }));
      return result;
    },
  }] }));
  const args = name === 'read' ? { path: 'input.txt' } : name === 'bash' ? { command: 'printf mutation >> mutation.txt' }
    : name === 'write' ? { path: 'mutation.txt', content: 'written' }
    : { path: 'input.txt', oldText: 'fixture', newText: 'edited' };
  faux.setResponses([fauxAssistantMessage([fauxToolCall(name, args, { id: 'probe-call' })], { stopReason: 'toolUse' })]);
}
const harness = await Harness.open(await openNodeSqliteStorage(join(directory, 'session.sqlite')), {
  models, registry, env: () => env,
  settings: { progress: { partialIntervalMs: 10, outputIntervalMs: 10 }, compaction: { enabled: false } },
}, context);
const root = await harness.root(context, { agent: { model: { provider: 'faux', modelId: 'faux-1' }, cwd: directory } });
await writeFile(join(directory, 'input.txt'), 'fixture');
const submission = await root.submit({ type: 'input', content: 'tiny crash probe', requestId: 'crash-input' }, context);
process.send({ type: 'admitted', id: submission.id });
if (mode === 'queued') {
  const ids = [];
  for (const draft of [
    { type: 'input', content: 'steer after crash', whenBusy: 'steer', requestId: 'crash-steer' },
    { type: 'input', content: 'follow after crash', requestId: 'crash-follow' },
    { type: 'write', entry: { kind: 'probe.note', data: 'queued note' }, requestId: 'crash-note' },
  ]) ids.push((await root.submit(draft, context)).id);
  process.send({ type: 'queued', ids });
}
if (mode === 'generation') {
  const watch = await root.watch(context);
  watch.start(async value => {
    if (value.docs['pi.live']?.generation?.message?.content?.some(b => b.type === 'text' && b.text.length > 0)) {
      process.send({ type: 'partial' });
      await watch.stop();
    }
  });
}
// Keep IPC alive until the test sends an actual SIGKILL.
