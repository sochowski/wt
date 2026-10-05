import test from 'node:test';
import assert from 'node:assert/strict';
import { fork } from 'node:child_process';
import { once } from 'node:events';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { Type } from '@earendil-works/pi-ai';
import { fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { createRegistry, defineExtension, defineTool, defineTask, defineDoc, InboxDoc, LiveDoc, ProviderDoc, watchEvents } from '@earendil-works/pi-durable';
import { CodingTools } from '@earendil-works/pi-durable/tools';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { context, model, delay, until, fixture, entries, fauxAssistantMessage as answer } from './fixture.mjs';

const input = (content, requestId, whenBusy) => ({ type: 'input', content, requestId, whenBusy });
const toolAnswer = name => answer([fauxToolCall(name, {}, { id: 'call-1' })], { stopReason: 'toolUse' });

test('requestId is conversation-scoped, deduplicates concurrent retries, but does not validate input body/mode', { timeout: 10000 }, async t => {
  const f = await fixture(t);
  const { harness, root, faux } = await f.open();
  const submissions = await Promise.all(Array.from({ length: 12 }, () => root.submit(input('first', 'same'), context)));
  assert.equal(new Set(submissions.map(s => s.id)).size, 1);
  assert.equal((await submissions[0].wait(context)).status, 'done');
  assert.equal((await root.submit(input('DIFFERENT', 'same', 'steer'), context)).id, submissions[0].id);
  await assert.rejects(root.submit({ type: 'write', entry: { kind: 'probe.note' }, requestId: 'same' }, context), /already identifies/);
  assert.equal(faux.state.callCount, 1);
  const child = await harness.createConversation({ ownership: { kind: 'ownerless' }, agent: { model } }, context);
  faux.appendResponses([answer('child')]);
  const independent = await child.submit(input('child', 'same'), context);
  assert.notEqual(independent.id, submissions[0].id);
  await independent.wait(context);
  const note = await root.submit({ type: 'write', requestId: 'note', entry: { kind: 'probe.note', data: 'first' } }, context);
  const changedNote = await root.submit({ type: 'write', requestId: 'note', entry: { kind: 'probe.changed', data: 'DIFFERENT' } }, context);
  assert.equal(changedNote.id, note.id);
});

test('close is recoverable; late structural/event snapshots and persisted choices do not schedule; wait resumes', { timeout: 10000 }, async t => {
  const f = await fixture(t, { faux: { tokensPerSecond: 20, tokenSize: { min: 1, max: 1 }, models: [{ id: 'faux-1' }, { id: 'faux-2', reasoning: true }] } });
  let { harness, root } = await f.open([answer('long partial '.repeat(100))]);
  const selectedModel = { provider: 'faux', modelId: 'faux-2' };
  await root.configure({ model: selectedModel, cwd: f.directory, thinkingLevel: 'low' }, context);
  const s = await root.submit(input('work', 'recover'), context);
  await until(async () => (await harness.snapshot(LiveDoc, root.id, context))?.generation?.message?.content?.some(b => b.text?.length));
  const providerId = (await harness.snapshot(ProviderDoc, root.id, context)).sessionId;
  await harness.close(context);
  const reopened = await f.open([answer('resumed')]);
  ({ harness, root } = reopened);
  const view = await root.viewState(context);
  const watch = await root.watch(context);
  const events = await watchEvents(harness, root.id, context);
  assert.ok(view.value.docs['pi.live'].run);
  assert.ok(events.snapshot.generation);
  assert.deepEqual(watch.value.entries, view.value.entries);
  const agent = await root.agent(context);
  assert.equal(agent.cwd, f.directory);
  assert.equal(agent.thinkingLevel, 'low');
  assert.deepEqual(agent.model, selectedModel);
  assert.equal((await harness.snapshot(ProviderDoc, root.id, context)).sessionId, providerId);
  await delay(120);
  assert.equal(reopened.faux.state.callCount, 0);
  const old = await harness.submission(s.id, context);
  assert.equal((await old.status(context)).status, 'placed');
  assert.equal((await old.wait(context)).status, 'done');
  assert.equal(reopened.faux.state.callCount, 1);
  view.dispose(); await watch.stop(); await events.stop();
});

test('busy queues persist steers/followUps/writes; tool boundary places steer before followUp', { timeout: 10000 }, async t => {
  const f = await fixture(t);
  let release;
  const held = new Promise(r => { release = r; });
  const registry = createRegistry();
  registry.install(defineExtension({ name: 'hold', tools: [defineTool({ name: 'hold', description: 'hold', parameters: Type.Object({}),
    execute: async (_, api, ctx) => { await api.details({ holding: true }, ctx); await held; return { content: [{ type: 'text', text: 'released' }] }; },
  })] }));
  const { harness, root } = await f.open([toolAnswer('hold'), answer('steered answer'), answer('follow-up answer')], { registry });
  const first = await root.submit(input('first', 'first'), context);
  await until(async () => (await harness.snapshot(LiveDoc, root.id, context))?.tools?.[0]?.details?.holding);
  const follow = await root.submit(input('follow', 'follow'), context);
  const steer = await root.submit(input('steer', 'steer', 'steer'), context);
  const note = await root.submit({ type: 'write', entry: { kind: 'probe.note', data: 'note' } }, context);
  await assert.rejects(root.submit(input('reject', 'reject', 'reject'), context), /busy/);
  const withdrawn = await root.submit(input('withdraw', 'withdraw'), context);
  assert.equal(await withdrawn.abort(context), 'aborted');
  const inbox = await harness.snapshot(InboxDoc, root.id, context);
  assert.deepEqual(inbox.items.map(x => x.mode), ['followUp', 'steer', 'write']);
  const watch = await root.watch(context);
  assert.deepEqual(watch.value.docs['pi.inbox'], inbox);
  release();
  for (const s of [first, steer, follow, note]) assert.equal((await s.wait(context)).status, 'done');
  assert.equal((await withdrawn.wait(context)).reason, 'aborted');
  const users = (await entries(root)).filter(e => e.kind === 'pi.user').map(e => e.model[0].content);
  assert.deepEqual(users, ['first', 'steer', 'follow']);
  await watch.stop();
});

test('notifications via commit(doc) do not wake; even passive submit(write) starts recovered scheduler', { timeout: 10000 }, async t => {
  const f = await fixture(t, { faux: { tokensPerSecond: 20 } });
  let { harness, root } = await f.open([answer('long '.repeat(100))]);
  await root.submit(input('pending', 'pending'), context);
  await until(async () => (await harness.snapshot(LiveDoc, root.id, context))?.generation?.message);
  await harness.close(context);
  const second = await f.open([answer('recovered')]);
  ({ harness, root } = second);
  const Notices = defineDoc({ kind: 'wt.probe-notices', version: 1, scope: 'conversation', history: 'latest', fork: 'initial', initial: () => ({ items: [] }) });
  await root.commit(async tx => { (await tx.doc(Notices, root.id)).items.push('notify'); }, context);
  await delay(100);
  assert.equal(second.faux.state.callCount, 0);
  await root.submit({ type: 'write', entry: { kind: 'probe.note', data: 'passive' } }, context);
  await until(() => second.faux.state.callCount === 1);
});

test('explicit abort persists cancellation and does not restart on reopen', { timeout: 10000 }, async t => {
  const f = await fixture(t, { faux: { tokensPerSecond: 20 } });
  const first = await f.open([answer('long '.repeat(100))]);
  const s = await first.root.submit(input('abort', 'abort'), context);
  await until(() => first.faux.state.callCount > 0);
  await first.root.abort(context);
  assert.equal((await s.status(context)).reason, 'aborted');
  await first.harness.close(context);
  const second = await f.open();
  second.harness.resume(); await delay(100);
  assert.equal(second.faux.state.callCount, 0);
  assert.equal((await (await second.harness.submission(s.id, context)).status(context)).reason, 'aborted');
});

for (const background of [false, true]) {
  test(`${background ? 'background' : 'foreground'} task ownership bounds parent abort; ownerless peer survives`, { timeout: 10000 }, async t => {
    const f = await fixture(t, { faux: { tokensPerSecond: 20 } });
    const Anchor = defineTask({ name: 'probe.anchor', version: 1, initial: () => ({ phase: 'hold' }),
      phases: { hold: async (_, runtime, ctx) => { await runtime.sleep(Date.now() + 60000, ctx); } },
      abort: async (_, runtime, ctx) => runtime.commit(() => ({ status: 'terminal', outcome: { status: 'aborted' } }), ctx),
    });
    const registry = createRegistry(); registry.install(defineExtension({ name: 'anchor', tasks: [Anchor] }));
    const { harness, root } = await f.open([answer('child '.repeat(100)), answer('peer '.repeat(100))], { registry });
    const childId = await root.commit(async tx => {
      const taskId = await tx.createTask(Anchor, null, { ownership: { kind: 'conversation' }, background });
      return (await tx.createConversation({ ownership: { kind: 'task', taskId } })).id;
    }, context);
    const child = await harness.conversation(childId, context);
    const peer = await harness.createConversation({ ownership: { kind: 'ownerless' }, agent: { model } }, context);
    const childS = await child.submit(input('child'), context);
    const peerS = await peer.submit(input('peer'), context);
    await root.abort(context);
    assert.equal((await childS.status(context)).status, background ? 'placed' : 'unanswered');
    assert.equal((await peerS.status(context)).status, 'placed');
    if (background) { await root.abort(context, { background: true }); assert.equal((await childS.status(context)).reason, 'aborted'); }
    await peer.abort(context);
  });
}

for (const { mode, currentSafe, safeResult, label = mode } of [
  { mode: 'generation' }, { mode: 'queued' },
  { mode: 'safe-read', currentSafe: true, safeResult: true },
  { mode: 'safe-read', currentSafe: false, label: 'safe-read with current policy veto' },
  { mode: 'read', currentSafe: true, label: 'unsafe-read with attempted policy upgrade' },
  { mode: 'unsafe-bash' }, { mode: 'write' }, { mode: 'edit' },
]) {
  test(`SIGKILL/reopen real SQLite during ${label}`, { timeout: 15000 }, async t => {
    const f = await fixture(t);
    const child = fork(new URL('./crash-worker.mjs', import.meta.url), [f.directory, mode], {
      env: { PATH: process.env.PATH, HOME: f.directory, WT_STATUS_DIR: join(f.directory, 'wt-state'), WT_CONFIG_DIR: join(f.directory, 'config'), WT_BASE_DIR: join(f.directory, 'worktrees'), PI_CODING_AGENT_DIR: join(f.directory, 'pi') },
      stdio: ['ignore', 'ignore', 'pipe', 'ipc'],
    });
    t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); });
    let stderr = ''; child.stderr.on('data', b => { stderr += b; });
    let admitted, queued, reached = false;
    child.on('message', m => {
      if (m.type === 'admitted') admitted = m.id;
      if (m.type === 'queued') queued = m.ids;
      if (m.type === (mode === 'generation' ? 'partial' : mode === 'queued' ? 'queued' : 'effect')) reached = true;
    });
    await until(() => { if (child.exitCode !== null) throw new Error(`worker exited: ${stderr}`); return reached && admitted !== undefined; }, 8000);
    const exit = once(child, 'exit'); child.kill('SIGKILL');
    assert.equal((await exit)[1], 'SIGKILL');
    const registry = createRegistry(); registry.install(CodingTools);
    if (currentSafe) {
      const read = CodingTools.tools.find(x => x.name === 'read');
      registry.install(defineExtension({ name: 'probe-safe-read', tools: [{ ...read, replay: 'safe' }] }));
    }
    const env = new NodeExecutionEnv({ cwd: f.directory }); t.after(() => env.cleanup(context));
    const { harness, root, faux } = await f.open([answer('recovered'), answer('queued completed')], { registry, env: () => env });
    const s = await harness.submission(admitted, context);
    assert.equal((await s.status(context)).status, 'placed');
    if (mode === 'queued') assert.deepEqual((await harness.snapshot(InboxDoc, root.id, context)).items.map(x => x.id), queued);
    const events = await watchEvents(harness, root.id, context);
    assert.ok(events.snapshot.run);
    await delay(50); assert.equal(faux.state.callCount, 0);
    assert.equal((await s.wait(context)).status, 'done');
    if (queued) for (const id of queued) assert.equal((await (await harness.submission(id, context)).wait(context)).status, 'done');
    assert.equal((await root.submit(input('tiny crash probe', 'crash-input'), context)).id, admitted);
    const transcript = await entries(root);
    assert.equal(transcript.filter(e => e.kind === 'pi.user').length, mode === 'queued' ? 3 : 1);
    if (mode === 'generation') {
      const interrupted = transcript.find(e => e.kind === 'pi.assistant' && e.model[0].stopReason === 'aborted');
      assert.ok(interrupted?.model[0].content.some(b => b.text?.length > 0));
    }
    if (mode !== 'generation' && mode !== 'queued') {
      assert.equal((await readFile(join(f.directory, 'effects'), 'utf8')).trim().split('\n').length, 1);
      const result = transcript.find(e => e.kind === 'pi.tool-result');
      assert.equal(result.model[0].isError, !safeResult);
      if (!safeResult) {
        assert.ok(result.data.diagnostics.some(d => d.code === 'interrupted'));
        assert.ok(result.model[0].content.some(b => b.text?.includes('committed partial output')));
      } else assert.ok(result.model[0].content.some(b => b.text?.includes('fixture')));
      if (mode === 'unsafe-bash') assert.equal(await readFile(join(f.directory, 'mutation.txt'), 'utf8'), 'mutation');
      if (mode === 'write') assert.equal(await readFile(join(f.directory, 'mutation.txt'), 'utf8'), 'written');
      if (mode === 'edit') assert.equal(await readFile(join(f.directory, 'input.txt'), 'utf8'), 'edited');
    }
    await events.stop();
  });
}

test('store has no process-exclusive lock: second process can open already owned SQLite', { timeout: 10000 }, async t => {
  const f = await fixture(t); const { harness } = await f.open();
  const child = fork(new URL('./lock-worker.mjs', import.meta.url), [f.database], { stdio: ['ignore', 'ignore', 'pipe', 'ipc'], env: { PATH: process.env.PATH, HOME: f.directory } });
  t.after(() => child.kill('SIGKILL'));
  const [message] = await once(child, 'message'); assert.equal(message.type, 'opened');
  await once(child, 'exit');
  await harness.close(context);
});

test('late watchers receive current tool output then serialized committed updates; cancelled wait does not abort work', { timeout: 10000 }, async t => {
  const { withAbortSignal } = await import('@earendil-works/chord/context');
  const f = await fixture(t);
  let release;
  const held = new Promise(r => { release = r; });
  const registry = createRegistry();
  registry.install(defineExtension({ name: 'observed', tools: [defineTool({ name: 'observed', description: 'observed', parameters: Type.Object({}),
    execute: async (_, api, ctx) => {
      api.output('first\n'); await api.details({ ready: true }, ctx);
      await held; api.output('last\n'); return {};
    },
  })] }));
  const { harness, root } = await f.open([toolAnswer('observed'), answer('done')], { registry });
  const s = await root.submit(input('watch me'), context);
  await until(async () => (await harness.snapshot(LiveDoc, root.id, context))?.tools?.[0]?.details?.ready);
  const watch = await root.watch(context);
  const events = await watchEvents(harness, root.id, context);
  assert.equal(watch.value.docs['pi.live'].tools[0].output, 'first\n');
  assert.equal(events.snapshot.tools[0].output, 'first\n');
  const initial = watch.value;
  let last = initial, inCallback = false, callbacks = 0, ended = false;
  watch.start(async (value, ops) => {
    assert.equal(inCallback, false); inCallback = true;
    assert.ok(ops.length); callbacks++; last = value;
    await delay(1); inCallback = false;
  });
  events.start(async batch => { if (batch.some(e => e.type === 'run_end')) ended = true; });
  const controller = new AbortController();
  const waiting = s.wait(withAbortSignal(controller.signal, context));
  controller.abort(); await assert.rejects(waiting);
  assert.equal((await s.status(context)).status, 'placed');
  release(); assert.equal((await s.wait(context)).status, 'done');
  await until(() => ended && !last.docs['pi.live'].run);
  assert.ok(callbacks > 0);
  assert.equal(initial.docs['pi.live'].tools[0].output, 'first\n'); // immutable attachment snapshot
  assert.ok(last.entries.some(e => e.kind === 'pi.tool-result' && e.model[0].content.some(b => b.text === 'first\nlast\n')));
  await watch.stop(); await events.stop();
});
