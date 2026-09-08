import assert from 'node:assert/strict'
import test from 'node:test'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import registerAgents, { diskEntries, nativeSnapshot } from './agents.js'
import { formatInbox } from './inbox.js'

function harness(options = {}) {
  process.env.WT_ROOT_ID = 'root'
  process.env.WT_AGENT_ID = 'peer'
  process.env.WT_RUNTIME_ID = 'runtime'
  const hooks = new Map(), commands = new Map(), tools = new Map(), renderers = new Map(), sent = [], calls = []
  let disk = [], inbox = [], updates = []
  const pi = {
    getActiveTools() { return [...tools.keys()] },
    on(name, fn) { hooks.set(name, fn) }, registerCommand(name, spec) { commands.set(name, spec) },
    registerTool(spec) { tools.set(spec.name, spec) }, sendMessage(...args) { sent.push(args) },
    appendEntry() { disk.push({ type: 'custom', id: 'checkpoint' }) },
    registerMessageRenderer(name, renderer) { renderers.set(name, renderer) },
  }
  const ctx = {
    cwd: '/cwd', hasUI: false, isIdle: () => true, model: { id: 'model', provider: 'provider' }, thinkingLevel: 'high',
    sessionManager: { getSessionFile: () => '/native.jsonl', getSessionId: () => 'native', getLeafId: () => 'leaf' },
  }
  const run = async (args, input) => {
    calls.push(args)
    if (args[0] === 'update') { updates.push(input); return null }
    if (args[0] === 'message') {
      if (args[1] === 'poll') {
        const after = Number(args[5])
        const page = inbox.map((m, i) => ({ m, row: i + 1 })).filter(({m, row}) => m.state !== 'delivered' && row > after).slice(0, 8)
        return { messages: page.map(({m}) => m), next: page.at(-1)?.row || 0 }
      }
      if (args[1] === 'recover') { for (const m of inbox) if (m.state === 'claimed') m.state = 'uncertain'; return null }
      const m = inbox.find(m => m.id === args[3])
      if (args[1] === 'claim') { assert.equal(m.state, 'pending'); m.state = 'claimed' }
      if (args[1] === 'ack') m.state = 'delivered'
    }
    return []
  }
  const integration = registerAgents(pi, { run, entries: async () => disk, interval: 60000, ...options })
  return { hooks, commands, tools, renderers, sent, calls, updates, ctx, integration, setDisk(value) { disk = value }, setInbox(value) { inbox = value } }
}

test('unified skill and refreshed root orientation expose managed layout tools', async () => {
  const h = harness()
  assert.ok(h.tools.has('wt_view'))
  const resources = h.hooks.get('resources_discover')()
  assert.equal(resources.skillPaths.length, 1)
  assert.match(resources.skillPaths[0], /skills\/wt\/SKILL\.md$/)
  const result = await h.hooks.get('before_agent_start')({ systemPrompt: 'original' }, h.ctx)
  assert.match(result.systemPrompt, /^original/)
  assert.match(result.systemPrompt, /"root":"root","agent":"peer","assigned_cwd":"\/cwd"/)
  assert.deepEqual(h.calls.at(-1), ['workspace', 'root'])
  assert.match(result.systemPrompt, /unless explicitly directed elsewhere/)
  assert.match(result.systemPrompt, /not an assignment, sandbox, or permission change/)
  assert.match(result.systemPrompt, /repository's instructions before edits/)
  assert.match(result.systemPrompt, /files:\["\."\]/)
  assert.match(result.systemPrompt, /At the first edit, proactively reuse\/open a checkout diff/)
  assert.match(result.systemPrompt, /Preserve human focus, pins, active presentations/)
  assert.match(result.systemPrompt, /debounced external refresh/)
  assert.match(result.systemPrompt, /Use wt_view list\/show/)
  assert.doesNotMatch(result.systemPrompt, /Use the unified wt skill|wt_agent|presentations with present/)
  assert.match(h.tools.get('wt_view').description, /checkout-targeted editor/)
  assert.match(h.tools.get('wt_view').description, /including untracked files/)
  assert.doesNotMatch(h.tools.get('wt_view').description, /omit untracked|not live watchers|snapshots refreshed by r/)
})

test('agents lifecycle queues addressed messages idle without keyboard input and deduplicates by durable native receipt', async () => {
  const h = harness(), message = { id: 'message-1', sender: 'sender', recipient: 'peer', body: 'Please review', request: false, state: 'pending' }
  h.setInbox([message])
  await h.hooks.get('session_start')({}, h.ctx)
  assert.equal(h.updates[0].status, 'idle')
  assert.equal(h.updates[0].adapter.persisted, false)
  assert.equal(h.sent.length, 1)
  assert.deepEqual(h.sent[0][1], { deliverAs: 'nextTurn', triggerTurn: false })
  assert.equal(h.sent[0][0].details.recipient, 'peer')
  assert.equal(message.state, 'claimed')
  await h.integration.poll()
  assert.equal(h.sent.length, 1)
  h.setDisk([{ type: 'session', id: 'native' }, { type: 'custom_message', customType: 'wt-inbox', details: { message_id: 'message-1' } }])
  await h.integration.poll()
  assert.equal(message.state, 'delivered')
  assert.equal(h.updates.at(-1).adapter.persisted, true)
  await h.hooks.get('session_shutdown')({}, h.ctx)
  const count = h.calls.length; await h.integration.poll(); assert.equal(h.calls.length, count)
})

test('agents reload/crash claims become uncertain, not automatically repeated', async () => {
  const h = harness(), m = { id: 'lost', sender: 'sender', recipient: 'peer', body: 'unknown', state: 'claimed' }
  h.setInbox([m]); await h.hooks.get('session_start')({ reason: 'reload' }, h.ctx)
  assert.equal(m.state, 'uncertain'); assert.equal(h.sent.length, 0)
  assert.deepEqual(await h.hooks.get('session_before_switch')({}, h.ctx), { cancel: true })
  assert.deepEqual(await h.hooks.get('session_before_fork')({}, h.ctx), { cancel: true })
  await h.hooks.get('session_shutdown')({}, h.ctx)
})

test('wt_agent creates named interactive window peers and sends with explicit self identity and stable dedup IDs', async () => {
  const h = harness(), tool = h.tools.get('wt_agent')
  await tool.execute('spawn-call', { action: 'create', name: 'reviewer', target: 'beta' })
  assert.deepEqual(h.calls.at(-1), ['agents', 'create', 'root', 'reviewer', '--parent', 'peer', '--cwd', 'beta'])
  await tool.execute('message-call', { action: 'message', agent: 'other', body: 'hello' })
  assert.deepEqual(h.calls.at(-1), ['message', 'send', 'root', 'peer', 'other', 'hello', '--id', 'message-call'])
  await assert.rejects(tool.execute('bad', { action: 'create' }), /name required/)
})

test('Pi adapter captures native file/id/leaf/model without treating in-memory persistence setting as a disk receipt', async () => {
  const h = harness(); assert.equal(nativeSnapshot(h.ctx).persisted, false)
  const dir = await mkdtemp(join(tmpdir(), 'wt-native-test-'))
  try {
    const file = join(dir, 'session.jsonl')
    assert.deepEqual(await diskEntries(file), [])
    await writeFile(file, '{"type":"session","id":"exact"}\n{"type":"custom_message","details":{"message_id":"m"}}\n')
    assert.equal((await diskEntries(file))[1].details.message_id, 'm')
    await writeFile(file, '{invalid'); await assert.rejects(diskEntries(file))
  } finally { await rm(dir, { recursive: true, force: true }) }
})


test('idle request recipient wakes and a peer reply wakes requester, with bounded polling and no keyboard input', async () => {
  const recipient = harness()
  const requests = Array.from({ length: 6 }, (_, i) => ({ id: `request-${i}`, sender: 'requester', recipient: 'peer', body: 'work', request: true, state: 'pending' }))
  recipient.setInbox(requests)
  await recipient.hooks.get('session_start')({}, recipient.ctx)
  assert.equal(recipient.sent.length, 4)
  assert.deepEqual(recipient.sent[0][1], { deliverAs: 'followUp', triggerTurn: true })
  await recipient.integration.poll() // end of page range: wraps to outstanding work
  await recipient.integration.poll()
  assert.equal(recipient.sent.length, 6)
  await recipient.hooks.get('session_shutdown')({}, recipient.ctx)
  const requester = harness()
  requester.setInbox([{ id: 'reply', sender: 'reviewer', recipient: 'peer', body: 'review complete', request: true, state: 'pending' }])
  await requester.hooks.get('session_start')({}, requester.ctx)
  assert.deepEqual(requester.sent[0][1], { deliverAs: 'followUp', triggerTurn: true })
  await requester.hooks.get('session_shutdown')({}, requester.ctx)
})

test('managed extension factory retains present, adds explicit targets, and lazily dispatches a typed view instead of legacy renderer', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'wt-present-mock-'))
  const previous = { state: process.env.WT_STATE, session: process.env.WT_SESSION }
  try {
    const command = join(dir, 'state'), log = join(dir, 'log')
    await writeFile(command, `#!/usr/bin/env node
const fs=require('node:fs');let input='';process.stdin.on('data',x=>input+=x);process.stdin.on('end',()=>{fs.writeFileSync(${JSON.stringify(log)},JSON.stringify({args:process.argv.slice(2),input:input ? JSON.parse(input) : null}));console.log(JSON.stringify({ok:true,view_id:'view-1'}))})`, { mode: 0o755 })
    process.env.WT_STATE = command; process.env.WT_SESSION = 'demo'
    const tools = new Map(), hooks = []
    const { default: register } = await import('./extension.js?managed-test')
    register({ on: (...args) => hooks.push(args), registerTool: spec => tools.set(spec.name, spec), registerCommand() {} })
    assert(tools.has('wt_agent') && tools.has('present'))
    const result = await tools.get('present').execute('call', { title: 'review', target: 'beta', scenes: [{ artifact: { kind: 'markdown', content: 'result' } }] }, undefined, undefined, { hasUI: false })
    assert.equal(result.details.editor.view_id, 'view-1')
    const { readFile } = await import('node:fs/promises')
    const called = JSON.parse(await readFile(log, 'utf8'))
    assert.deepEqual(called.args, ['worktree', 'present', 'root', 'beta'])
    assert.equal(called.input.version, 1)
    // Fresh extension instance has no closure-local view identity. End resolves
    // the durable per-agent selection rather than guessing a target or parking.
    const commands = new Map()
    register({ on() {}, registerTool() {}, registerCommand(name, spec) { commands.set(name, spec) } })
    await commands.get('presentation-end').handler('', { hasUI: false })
    const ended = JSON.parse(await readFile(log, 'utf8'))
    assert.deepEqual(ended.args, ['worktree', 'present-clear', 'root'])
  } finally {
    if (previous.state === undefined) delete process.env.WT_STATE; else process.env.WT_STATE = previous.state
    if (previous.session === undefined) delete process.env.WT_SESSION; else process.env.WT_SESSION = previous.session
    await rm(dir, { recursive: true, force: true })
  }
})

test('bounded cursor polling traverses >1MiB backlog without redownloading delivered history or duplicate delivery', async () => {
  const h = harness()
  h.setInbox(Array.from({length: 160}, (_,i) => ({id:`large-${i}`,sender:'human',recipient:'peer',body:'x'.repeat(16384),request:false,state:i<80?'delivered':'pending'})))
  await h.hooks.get('session_start')({},h.ctx)
  for (let i=0;i<100;i++) {
    h.setDisk(h.sent.map(([m]) => ({type:'custom_message',customType:'wt-inbox',details:m.details})))
    await h.integration.poll()
  }
  assert.equal(h.sent.length,80)
  assert.equal(new Set(h.sent.map(([m]) => m.details.message_id)).size,80)
  assert(h.calls.filter(a=>a[1]==='poll').some(a=>Number(a[5])>80))
  assert(!h.calls.some(a=>a[1]==='list'))
  await h.hooks.get('session_shutdown')({},h.ctx)
})

test('TUI renderer registers before delivery and leaves full content, receipts and acknowledgement unchanged', async () => {
  const renderer = (message, options) => formatInbox(message, options.expanded)
  const h = harness({ renderInbox: renderer })
  assert.equal(h.renderers.get('wt-inbox'), renderer)
  h.ctx.mode = 'tui'
  const body = JSON.stringify({ type: 'wt-job-result', job: 'a'.repeat(64), output: 'Review complete\n' + 'evidence\n'.repeat(2000) })
  const message = { id: 'receipt', sender: 'reviewer', recipient: 'peer', body, request: true, state: 'pending' }
  h.setInbox([message])
  await h.hooks.get('session_start')({}, h.ctx)
  try {
    assert.equal(h.renderers.get('wt-inbox'), renderer)
    const [sent, delivery] = h.sent[0]
    const original = structuredClone(sent)
    assert.equal(sent.content, `Peer reviewer:\n${body}`)
    assert.equal(sent.display, true)
    assert.deepEqual(sent.details, { message_id: 'receipt', sender: 'reviewer', recipient: 'peer', root_id: 'root' })
    assert.deepEqual(delivery, { deliverAs: 'followUp', triggerTurn: true })
    assert.match(renderer(sent, { expanded: false }), /Review complete/)
    assert.equal(renderer(sent, { expanded: true }), sent.content)
    assert.deepEqual(sent, original)
    await h.integration.poll()
    assert.equal(message.state, 'claimed') // Rendering/queueing is not a durable ack.
    h.setDisk([{ type: 'custom_message', customType: 'wt-inbox', content: sent.content, details: sent.details }])
    await h.integration.poll()
    assert.equal(message.state, 'delivered')
    assert.equal(h.sent.length, 1)
  } finally { await h.hooks.get('session_shutdown')({}, h.ctx) }
})

test('pure agent factory supports non-TUI startup without native renderer dependencies', async () => {
  for (const mode of ['rpc', 'print', 'json']) {
    const h = harness()
    h.ctx.mode = mode
    await h.hooks.get('session_start')({}, h.ctx)
    assert.equal(h.renderers.size, 0)
    await h.hooks.get('session_shutdown')({}, h.ctx)
  }
})
