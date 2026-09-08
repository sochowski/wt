#!/usr/bin/env node
// Private-harness Pi stand-in. Owns its fixture transcript, never calls a model.
const fs = require('node:fs')
const path = require('node:path')
const { execFileSync } = require('node:child_process')
const args = process.argv.slice(2)
const arg = name => args[args.indexOf(name) + 1]
const dir = arg('--session-dir')
fs.mkdirSync(dir, { recursive: true })
const file = args.includes('--session') ? arg('--session') : path.join(dir, 'native.jsonl')
let native
if (args.includes('--session')) {
  native = JSON.parse(fs.readFileSync(file, 'utf8').split('\n')[0]).id
} else {
  native = `stub-${process.env.WT_AGENT_ID}`
  fs.writeFileSync(file, JSON.stringify({ type: 'session', version: 3, id: native, cwd: process.cwd() }) + '\n' +
    JSON.stringify({ type: 'message', id: 'completed', parentId: null, message: { role: 'assistant', content: [{ type: 'text', text: 'Stub ready' }] } }) + '\n')
}
fs.appendFileSync(path.join(process.env.WT_STATUS_DIR, 'stub-launches.jsonl'), JSON.stringify({ root: process.env.WT_ROOT_ID, agent: process.env.WT_AGENT_ID, runtime: process.env.WT_RUNTIME_ID, args, native, file }) + '\n')
execFileSync(process.env.WT_STATE, ['worktree', 'update'], { input: JSON.stringify({ status: 'idle', native_id: native, adapter: { version: 1, file, persisted: true, leaf: 'completed' } }) })
console.log('Interactive Pi stub ready; no provider calls', process.env.WT_AGENT_ID)
process.stdin.resume()
process.stdin.on('data', data => process.stdout.write(data))
for (const event of ['SIGHUP', 'SIGTERM', 'SIGINT']) process.on(event, () => process.exit(0))
