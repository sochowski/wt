import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { BACKGROUND_CONTEXT } from '@earendil-works/chord/context';
import { createModels } from '@earendil-works/pi-ai/models';
import { fauxProvider, fauxAssistantMessage } from '@earendil-works/pi-ai/providers/faux';
import { Harness, createRegistry } from '@earendil-works/pi-durable';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
export const context = BACKGROUND_CONTEXT;
export const model = { provider: 'faux', modelId: 'faux-1' };
export { delay, fauxAssistantMessage };
export async function until(check, timeout = 5000) {
  const deadline = Date.now() + timeout;
  while (!await check()) {
    if (Date.now() >= deadline) throw new Error('Probe condition timed out');
    await delay(10);
  }
}
export async function fixture(t, options = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'wt-durable-probe-'));
  const database = join(directory, 'session.sqlite');
  const handles = [];
  t.after(async () => {
    for (const h of handles) await h.close(context);
    await rm(directory, { recursive: true, force: true });
  });
  const open = async (responses = [fauxAssistantMessage('OK')], extra = {}) => {
    const faux = fauxProvider(options.faux);
    faux.setResponses(responses);
    const models = createModels();
    models.setProvider(faux.provider);
    const harness = await Harness.open(await openNodeSqliteStorage(database), {
      models, registry: createRegistry(), settings: { compaction: { enabled: false }, retry: { maxRetries: 0 } }, ...extra,
    }, context);
    handles.push(harness);
    const root = await harness.root(context, { agent: { model } });
    return { harness, root, faux };
  };
  return { directory, database, open };
}
export async function entries(root) {
  return [...(await root.entries({}, 100, undefined, context)).items].reverse();
}
