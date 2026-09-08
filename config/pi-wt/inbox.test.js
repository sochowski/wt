import assert from 'node:assert/strict'
import test from 'node:test'
import { createInboxRenderer, formatInbox } from './inbox.js'

test('ordinary human and peer tasks collapse to bounded prose and expand to the original text', () => {
  for (const sender of ['Human', 'Peer ' + 'a'.repeat(64)]) {
    const message = { content: `${sender}:\nReview this implementation.\n${'long line '.repeat(2000)}\nMore context\nLast detail` }
    const summary = formatInbox(message)
    assert.match(summary, /Review this implementation/)
    assert(summary.length <= 600)
    assert(summary.split('\n').length <= 4)
    assert.equal(formatInbox(message, true), message.content)
  }
})

test('job result envelopes show job identity and leading prose, not raw JSON or acceptance report', () => {
  const output = 'Implemented the requested fix.\nTests passed.\n\n```acceptance-report\n' + JSON.stringify({ validationOutput: ['large evidence'.repeat(5000)] }) + '\n```'
  const message = { content: 'Peer reviewer:\n' + JSON.stringify({ type: 'wt-job-result', job: 'a'.repeat(64), output }) }
  const summary = formatInbox(message)
  assert.match(summary, /WT inbox · Peer reviewer · Job a+… result/)
  assert.match(summary, /Implemented the requested fix\.\nTests passed\./)
  assert.doesNotMatch(summary, /acceptance-report|validationOutput|large evidence|"output"/)
  assert.equal(formatInbox(message, true), message.content)
  assert.equal(formatInbox({ content: 'Human:\n```acceptance-report\n{}\n```' }), 'WT inbox · Human\n(details available on expand)')
})

test('malformed JSON and invalid envelope fields fall back without throwing or unbounded output', () => {
  for (const body of ['{"type":"wt-job-result",oops' + 'x'.repeat(50000), 'null', '[]', '{"type":"wt-job-result","job":{},"output":[]}', '{"type":"other","output":"no"}', '']) {
    const message = { content: 'Peer sender:\n' + body }
    assert(formatInbox(message).length < 600)
    assert.equal(formatInbox(message, true), message.content)
  }
})

test('untrusted control sequences are escaped in both summaries and full text without mutating content', () => {
  const unsafe = '\x1b[2J\x1b]52;c;clipboard\x07\r\t\x9b31m\u202eevil'
  const message = { content: `Peer ${unsafe}:\n` + JSON.stringify({ type: 'wt-job-result', job: unsafe, output: unsafe }) }
  const original = structuredClone(message)
  for (const expanded of [false, true]) {
    const text = formatInbox(message, expanded)
    assert.doesNotMatch(text, /[\x00-\x09\x0b-\x1f\x7f-\x9f\u202a-\u202e]/)
    assert.match(text, /\\u001b/)
  }
  assert.deepEqual(message, original)
})

// A minimal injected Text double exercises the public Component contract;
// the isolated Pi integration check additionally uses the real Text class.
class Text {
  constructor(text, pad) { this.text = text; this.pad = pad }
  setText(text) { this.text = text }
  invalidate() {}
  render(width) {
    const room = Math.max(1, width - 2 * this.pad)
    return this.text.split('\n').flatMap(line => line.match(new RegExp(`.{1,${room}}`, 'g')) || ['']).map(line => ' '.repeat(this.pad) + line)
  }
}
test('renderer honors Pi expanded/outputPad, configurable hint and actual collapsed row limit', () => {
  const keys = []
  const renderer = createInboxRenderer({ Text, keyHint: (key, description) => { keys.push(key); return `custom-key ${description}` } })
  const theme = { fg: (_color, text) => text }
  const message = { content: 'Human:\n' + 'long prose '.repeat(1000) + '\nfinal line' }
  const collapsed = renderer(message, { expanded: false, outputPad: 2 }, theme)
  for (const width of [10, 40, 120]) {
    collapsed.invalidate()
    const lines = collapsed.render(width)
    assert(lines.length <= 6)
    assert(lines.every(line => line.length <= width && line.startsWith('  ')))
  }
  assert.equal(keys[0], 'app.tools.expand')
  assert.match(collapsed.render(120).at(-1), /custom-key for full inbox message/)
  const expanded = renderer(message, { expanded: true, outputPad: 0 }, theme).render(120)
  assert(expanded.length > 6)
  assert.equal(expanded.at(-1), 'final line')
})
