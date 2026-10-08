import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, copyFile, readFile, writeFile, rm, realpath } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Box, CombinedAutocompleteProvider, Container, CURSOR_MARKER, TuiMainScreen, getKeybindings, setKeybindings, visibleWidth } from '@earendil-works/pi-tui';
import { loadPresentation, themeFromJson, selectTheme } from './presentation.mjs';
import { ActionEditor, SearchSelector } from './editor-ui.mjs';
import { renderConversation, footerText } from './conversation-ui.mjs';

async function fixture(t, settings = {}) {
  const directory = await realpath(await mkdtemp(join(tmpdir(), 'wt-ui-config-'))); t.after(() => rm(directory, { recursive: true, force: true }));
  await mkdir(join(directory, 'themes')); await copyFile(new URL('./test-fixtures/github-dark-hc.json', import.meta.url), join(directory, 'themes', 'github-dark-hc.json'));
  await writeFile(join(directory, 'settings.json'), JSON.stringify({ theme: 'github-dark-hc', editorPaddingX: 0, outputPad: 0, ...settings }));
  return directory;
}
const value = messages => ({ entries: [{ model: messages }], docs: {} });
const rendered = (components, width) => { const c = new Container(); for (const component of components) c.addChild(component); return c.render(width); };

test('native github-dark-hc chained palette, zero padding and named keybinding overrides are read-only', async t => {
  const dir = await fixture(t), before = await readFile(join(dir, 'settings.json'));
  await writeFile(join(dir, 'keybindings.json'), JSON.stringify({ 'app.interrupt': 'ctrl+e', 'app.tools.expand': [], 'app.session.new': 'ctrl+n' }));
  const p = loadPresentation(dir, { trueColor: true });
  assert.equal(p.theme.name, 'github-dark-hc'); assert.equal(p.editorPaddingX, 0); assert.equal(p.outputPad, 0);
  assert.equal(p.theme.fg('accent', 'x'), '\x1b[38;2;113;183;255mx\x1b[39m');
  assert.ok(p.keybindings.matches('\x05', 'app.interrupt')); assert.ok(!p.keybindings.matches('\x1b', 'app.interrupt')); assert.deepEqual(p.keybindings.getKeys('app.tools.expand'), []);
  assert.ok(p.unsupportedActions.includes('app.session.new')); assert.deepEqual(await readFile(join(dir, 'settings.json')), before);
  const palette = JSON.parse(await readFile(join(dir, 'themes', 'github-dark-hc.json'))); delete palette.colors.thinkingMax;
  assert.equal(themeFromJson(palette, false).name, 'github-dark-hc'); assert.match(themeFromJson(palette, false).fg('accent', 'x'), /38;5;/);
  assert.equal(themeFromJson(palette, true).fg('thinkingMax', 'x'), themeFromJson(palette, true).fg('thinkingXhigh', 'x'));
});

test('invalid/missing/circular themes or invalid padding fail visibly; no silent dark/native fallback', async t => {
  const dir = await fixture(t), theme = JSON.parse(await readFile(join(dir, 'themes', 'github-dark-hc.json')));
  assert.throws(() => themeFromJson({ ...theme, vars: { ...theme.vars, blue: 'blue' } }), /circular/);
  const colors = { ...theme.colors }; delete colors.toolPendingBg; assert.throws(() => themeFromJson({ ...theme, colors }), /Missing native theme color/);
  for (const setting of [{ theme: 'missing' }, { theme: '../escape' }, { theme: 'light/dark' }, { outputPad: 2 }, { editorPaddingX: -1 }]) {
    await writeFile(join(dir, 'settings.json'), JSON.stringify(setting)); assert.throws(() => loadPresentation(dir), /Missing|Invalid|Unsupported/);
  }
});

test('public editor preserves cursor/history, native action precedence, nonempty Ctrl-D and autocomplete Escape', async t => {
  const dir = await fixture(t), p = loadPresentation(dir), previous = getKeybindings(); setKeybindings(p.keybindings); t.after(() => setKeybindings(previous));
  const terminal = { rows: 24, columns: 80, write(){},hideCursor(){},showCursor(){},moveBy(){},clearLine(){},clearFromCursor(){},clearScreen(){} }, tui = new TuiMainScreen(terminal), editor = new ActionEditor(tui, { borderColor: s => s, selectList: selectTheme(p.theme) }, p.keybindings, { paddingX: p.editorPaddingX });
  let exited = 0, aborted = 0; editor.onAction('app.exit', () => exited++); editor.onAction('app.interrupt', () => aborted++);
  assert.equal(editor.getPaddingX(), 0); editor.focused = true; editor.setText('你好 👩‍💻 draft');
  editor.handleInput('\x04'); assert.equal(exited, 0); assert.ok(editor.render(12).some(line => line.includes(CURSOR_MARKER)));
  editor.handleInput('\x1b'); assert.equal(aborted, 1); editor.setText(''); editor.handleInput('\x04'); assert.equal(exited, 1);
  editor.setAutocompleteProvider(new CombinedAutocompleteProvider([{name:'help'}],dir,null)); editor.handleInput('/'); await new Promise(resolve=>setTimeout(resolve,80)); assert.equal(editor.isShowingAutocomplete(),true); editor.handleInput('\x1b'); assert.equal(aborted,1); assert.equal(editor.isShowingAutocomplete(),false);
  await writeFile(join(dir, 'keybindings.json'), JSON.stringify({ 'tui.editor.historyPrevious': 'ctrl+p' }));
  const custom = loadPresentation(dir); setKeybindings(custom.keybindings);
  const historyEditor = new ActionEditor(tui, { borderColor: s => s, selectList: selectTheme(p.theme) }, custom.keybindings);
  let cycled = 0; historyEditor.onAction('app.model.cycleForward', () => cycled++); historyEditor.addToHistory('previous'); historyEditor.handleInput('\x10'); assert.equal(historyEditor.getText(), 'previous'); assert.equal(cycled, 0);
});

test('native special interrupt/empty exit precede conflicting history and generic clear actions', async t => {
  const dir = await fixture(t), previous = getKeybindings(); t.after(() => setKeybindings(previous));
  await writeFile(join(dir, 'keybindings.json'), JSON.stringify({ 'tui.editor.historyPrevious': 'escape', 'tui.editor.historyNext': 'ctrl+d', 'app.clear': 'ctrl+d' }));
  const p = loadPresentation(dir); setKeybindings(p.keybindings);
  const terminal = { rows: 24, columns: 80, write(){}, hideCursor(){}, showCursor(){}, moveBy(){}, clearLine(){}, clearFromCursor(){}, clearScreen(){} };
  const editor = new ActionEditor(new TuiMainScreen(terminal), { borderColor: s => s, selectList: selectTheme(p.theme) }, p.keybindings);
  const actions = []; // Deliberately register generic clear first, as the production controller does.
  editor.onAction('app.clear', () => actions.push('clear')); editor.onAction('app.exit', () => actions.push('exit')); editor.onAction('app.interrupt', () => actions.push('interrupt'));
  editor.addToHistory('previous'); editor.handleInput('\x1b'); assert.deepEqual(actions, ['interrupt']); assert.equal(editor.getText(), '');
  editor.handleInput('\x04'); assert.deepEqual(actions, ['interrupt', 'exit']);
  editor.setText('draft'); editor.handleInput('\x04'); assert.deepEqual(actions, ['interrupt', 'exit'], 'explicit history still precedes generic clear when exit is ineligible');
  editor.setText(''); editor.setAutocompleteProvider(new CombinedAutocompleteProvider([{name:'help'}], dir, null));
  editor.handleInput('/'); await new Promise(resolve => setTimeout(resolve, 80)); assert.equal(editor.isShowingAutocomplete(), true);
  editor.handleInput('\x1b'); assert.equal(editor.isShowingAutocomplete(), false); assert.deepEqual(actions, ['interrupt', 'exit'], 'native interrupt defers to parent autocomplete handling even with a history collision');
});

test('selector pages by one visible page with clamped boundaries, filtering and custom bindings', async t => {
  const dir = await fixture(t), previous = getKeybindings(); t.after(() => setKeybindings(previous));
  const items = Array.from({ length: 29 }, (_, i) => ({ value: `item-${i}`, label: `Model ${i}` }));
  for (const custom of [false, true]) {
    await writeFile(join(dir, 'keybindings.json'), JSON.stringify(custom ? { 'tui.select.pageUp': 'ctrl+b', 'tui.select.pageDown': 'ctrl+f' } : {}));
    const p = loadPresentation(dir); setKeybindings(p.keybindings); let selected;
    const selector = new SearchSelector('Models', items, 'item-0', p.theme, p.keybindings, value => selected = value);
    const up = custom ? '\x02' : '\x1b[5~', down = custom ? '\x06' : '\x1b[6~';
    const current = () => selector.list.getSelectedItem()?.value;
    selector.handleInput(up); assert.equal(current(), 'item-0');
    selector.handleInput(down); assert.equal(current(), 'item-12'); assert.ok(selector.render(80).some(line => line.includes('Model 12')));
    selector.handleInput(down); assert.equal(current(), 'item-24'); selector.handleInput(down); assert.equal(current(), 'item-28');
    selector.handleInput(down); assert.equal(current(), 'item-28'); selector.handleInput(up); assert.equal(current(), 'item-16');
    selector.handleInput('\r'); assert.equal(selected, 'item-16');
    for (const char of 'item-28') selector.handleInput(char);
    selector.handleInput(down); assert.equal(current(), 'item-28'); selector.handleInput(up); assert.equal(current(), 'item-28');
    selector.handleInput('!'); selector.handleInput(down); selector.handleInput(up); assert.equal(current(), undefined);
    if (custom) { const fresh = new SearchSelector('Models', items, 'item-0', p.theme, p.keybindings, () => {}); fresh.handleInput('\x1b[6~'); assert.equal(fresh.list.getSelectedItem().value, 'item-0', 'disabled default must not page'); }
  }
});

test('public searchable selector handles Unicode/narrow resize, input focus, selection/cancel and configurable selection keys', async t => {
  const dir = await fixture(t), p = loadPresentation(dir), previous = getKeybindings(); setKeybindings(p.keybindings); t.after(() => setKeybindings(previous));
  const items = [{ value: 'one', label: 'Model one' }, { value: 'two', label: 'Model two' }]; let selected;
  const selector = new SearchSelector('Select model', items, 'one', p.theme, p.keybindings, value => selected = value);
  selector.focused = true; for (const char of 'two') selector.handleInput(char); selector.handleInput('\r'); assert.equal(selected, 'two');
  for (const width of [12, 24, 80]) assert.ok(selector.render(width).every(line => visibleWidth(line) <= width));
  assert.ok(selector.render(24).some(line => line.includes(CURSOR_MARKER))); selector.handleInput('\x1b'); assert.equal(selected, undefined);
});

test('native-style user/assistant/thinking/tool layout uses actual durable entries, preserves history, handles live callId and suppresses controls', async t => {
  const p = loadPresentation(await fixture(t), { trueColor: true });
  const messages = [
    { role: 'user', content: '你好 user\x1b]0;EVIL\x07' },
    { role: 'assistant', content: [{ type: 'thinking', thinking: 'THINKING_BODY' }, { type: 'text', text: 'assistant body' }, { type: 'toolCall', id: 'read-call', name: 'read', arguments: { path: 'file.txt' } }] },
    { role: 'toolResult', toolCallId: 'read-call', toolName: 'read', content: [{ type: 'text', text: Array.from({ length: 9 }, (_, i) => `line ${i}`).join('\n') }], isError: false },
  ];
  const collapsed = rendered(renderConversation(value(messages), p, { hideThinking: true }), 24).join('\n');
  assert.ok(collapsed.includes('[thinking hidden]')); assert.ok(!collapsed.includes('THINKING_BODY')); assert.ok(collapsed.includes('more lines')); assert.ok(!collapsed.includes('\x1b]0;'));
  const expanded = renderConversation(value(messages), p, { hideThinking: false, expandedTools: true }); assert.ok(rendered(expanded, 80).join('\n').includes('line 8'));
  assert.ok(rendered(expanded, 80).join('\n').includes('THINKING_BODY')); for (const width of [12, 24, 80]) assert.ok(rendered(expanded, width).every(line => visibleWidth(line) <= width));
  const liveValue = { ...value(messages.slice(0, 2)), docs: { 'pi.live': { tools: [{ callId: 'read-call', name: 'read', status: 'running', output: 'partial' }] } } };
  const liveComponents = renderConversation(liveValue, p); assert.equal(liveComponents.filter(component => component instanceof Box).length, 2); assert.ok(rendered(liveComponents, 80).join('\n').includes('running: no completed result yet'));
  const history = { entries: Array.from({ length: 110 }, (_, i) => ({ model: [{ role: 'user', content: `turn ${i}` }] })), docs: {} }; assert.equal(renderConversation(history, p).length, 110);
  const footer = footerText(value([{ role: 'assistant', usage: { input: 4, output: 2, cost: { total: .001 } } }]), { model: { provider: 'p', modelId: 'm' }, thinkingLevel: 'low' }, '/checkout', p.theme); assert.ok(footer.includes('↑4 ↓2 $0.0010')); assert.ok(footer.includes('p/m'));
});
