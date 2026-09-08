#!/usr/bin/env node
// Only the new view adapter tests use this stub; legacy Lua tests use isolated
// real headless Neovim. RPC returns the actual saved typed fixture state.
const fs = require('node:fs')
const path = require('node:path')
const args = process.argv.slice(2)
if (args.includes('--server')) {
  const socket = args[args.indexOf('--server') + 1]
  const seq = Number(fs.readFileSync(socket + '.seq', 'utf8')) + 1
  if (args.at(-1).includes('.clear()')) {
    const state = JSON.parse(fs.readFileSync(socket + '.state', 'utf8'))
    delete state.deck; delete state.slide
    fs.writeFileSync(socket + '.state', JSON.stringify(state))
    fs.writeFileSync(socket + '.seq', String(seq))
    console.log(JSON.stringify({ seq, state }))
  } else if (args.at(-1).includes('.reload()')) {
    const source = fs.readFileSync(socket + '.source', 'utf8')
    fs.writeFileSync(socket + '.state', JSON.stringify(JSON.parse(fs.readFileSync(source, 'utf8')).state))
    fs.writeFileSync(socket + '.seq', String(seq))
    console.log(JSON.stringify({ ok: true, seq, state: JSON.parse(fs.readFileSync(socket + '.state', 'utf8')) }))
  } else console.log(fs.readFileSync(socket + '.state', 'utf8'))
} else {
  const data = JSON.parse(fs.readFileSync(process.env.WT_VIEW_STATE, 'utf8'))
  const socket = args[args.indexOf('--listen') + 1]
  fs.mkdirSync(path.dirname(socket), { recursive: true })
  fs.writeFileSync(socket + '.seq', '0')
  fs.writeFileSync(socket + '.source', process.env.WT_VIEW_STATE)
  fs.writeFileSync(socket + '.state', JSON.stringify(data.state))
  console.log('nvim view stub', data.kind, data.root)
  process.stdin.resume()
  for (const signal of ['SIGHUP', 'SIGTERM']) process.on(signal, () => process.exit(0))
}
