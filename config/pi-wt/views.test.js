import test from 'node:test'
import assert from 'node:assert/strict'
import registerViews from './views.js'

test('wt_view shares create/place options without raw tmux or focus switching', async () => {
  let tool
  const calls = []
  registerViews({ registerTool(t) { tool = t } }, async args => { calls.push(args); return { id: 'view' } }, 'root')
  await tool.execute('1', { action: 'create', kind: 'editor', target: 'app', files: ['a b.js'], placement: 'split', anchor: 'focused' })
  await tool.execute('2', { action: 'place', view: 'view', placement: 'split', anchor: '%4' })
  await tool.execute('3', { action: 'place', view: 'view', placement: 'window' })
  await tool.execute('4', { action: 'place', view: 'view' })
  await tool.execute('5', { action: 'promote', view: 'view' })
  assert.deepEqual(calls, [
    ['view', 'create', 'root', 'editor', 'app', '--file', 'a b.js', '--placement', 'split', '--anchor', 'focused'],
    ['view', 'place', 'root', 'view', '--placement', 'split', '--anchor', '%4'],
    ['view', 'place', 'root', 'view', '--placement', 'window'],
    ['view', 'place', 'root', 'view'],
    ['view', 'promote', 'root', 'view'],
  ])
  await assert.rejects(tool.execute('6', { action: 'place' }), /view required/)
  await tool.execute('7', { action: 'master-width', view: 'view', percent: 65 })
  assert.deepEqual(calls.at(-1), ['view', 'master-width', 'root', 'view', '65'])
  await assert.rejects(tool.execute('8', { action: 'master-width', view: 'view', percent: 99 }), /20..80/)
  assert.equal(tool.parameters.properties.direction, undefined)
  assert.equal(tool.parameters.properties.size, undefined)
})
