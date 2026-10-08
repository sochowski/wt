import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { getCapabilities, KeybindingsManager, TUI_KEYBINDINGS } from '@earendil-works/pi-tui';

// These are the native 0.87.1 action IDs, not a second session/application engine.
export const appActions = {
  'app.interrupt': { defaultKeys: 'escape', description: 'Durably abort current work' },
  'app.clear': { defaultKeys: 'ctrl+c', description: 'Clear editor; repeat within 500ms to close' },
  'app.exit': { defaultKeys: 'ctrl+d', description: 'Recoverably close when editor empty' },
  'app.model.select': { defaultKeys: 'ctrl+l', description: 'Select persisted conversation model' },
  'app.model.cycleForward': { defaultKeys: 'ctrl+p', description: 'Cycle model forward' },
  'app.model.cycleBackward': { defaultKeys: 'shift+ctrl+p', description: 'Cycle model backward' },
  'app.thinking.cycle': { defaultKeys: 'shift+tab', description: 'Cycle thinking level' },
  'app.thinking.toggle': { defaultKeys: 'ctrl+t', description: 'Hide/show thinking' },
  'app.tools.expand': { defaultKeys: 'ctrl+o', description: 'Expand/collapse tool output' },
  'app.message.followUp': { defaultKeys: 'alt+enter', description: 'Submit explicit follow-up' },
};
const unsupportedActions = {
  'app.editor.external': { defaultKeys: 'ctrl+g' }, 'app.clipboard.pasteImage': { defaultKeys: 'ctrl+v' },
  'app.suspend': { defaultKeys: 'ctrl+z' }, 'app.message.copy': { defaultKeys: 'ctrl+x' },
  'app.message.dequeue': { defaultKeys: 'alt+up' }, 'app.models.save': { defaultKeys: 'ctrl+s' },
};
const fallbackColors = { scrollbarTrack: 'muted', scrollbarThumb: 'text', searchMatchBg: 'selectedBg', searchMatchText: 'text', thinkingMax: 'thinkingXhigh' };
const builtIn = name => JSON.parse(readFileSync(new URL(`./themes/${name}.json`, import.meta.url), 'utf8'));
const requiredColors = Object.keys(builtIn('dark').colors).filter(k => !Object.hasOwn(fallbackColors, k));
const json = path => JSON.parse(readFileSync(path, 'utf8'));

export function themeFromJson(data, trueColor = getCapabilities().trueColor) {
  if (!data || typeof data.name !== 'string' || !/^[\w.-]+$/.test(data.name) || !data.colors || typeof data.colors !== 'object') throw new Error('Invalid native theme document');
  function resolve(value, seen = new Set()) {
    if (value === '' || (Number.isInteger(value) && value >= 0 && value <= 255) || (typeof value === 'string' && /^#[a-f\d]{6}$/i.test(value))) return value;
    if (typeof value !== 'string' || seen.has(value) || !Object.hasOwn(data.vars || {}, value)) throw new Error('Invalid/circular native theme color');
    return resolve(data.vars[value], new Set([...seen, value]));
  }
  const colors = {};
  for (const key of requiredColors) { if (!Object.hasOwn(data.colors, key)) throw new Error(`Missing native theme color: ${key}`); colors[key] = resolve(data.colors[key]); }
  for (const [key, fallback] of Object.entries(fallbackColors)) colors[key] = Object.hasOwn(data.colors, key) ? resolve(data.colors[key]) : colors[fallback];
  const ansi = (key, background) => {
    const color = colors[key]; if (color === undefined) throw new Error(`Unknown theme color: ${key}`);
    if (color === '') return '';
    if (typeof color === 'number') return `\x1b[${background ? 48 : 38};5;${color}m`;
    const rgb = [1, 3, 5].map(offset => parseInt(color.slice(offset, offset + 2), 16));
    if (trueColor) return `\x1b[${background ? 48 : 38};2;${rgb.join(';')}m`;
    // Nearest RGB cube or grayscale in the xterm-256 palette.
    const steps = [0, 95, 135, 175, 215, 255];
    const cube = rgb.map(value => steps.reduce((best, step, i) => Math.abs(value - step) < Math.abs(value - steps[best]) ? i : best, 0));
    const gray = Math.max(0, Math.min(23, Math.round((rgb.reduce((a, b) => a + b) / 3 - 8) / 10)));
    const cubeDistance = rgb.reduce((sum, value, i) => sum + (value - steps[cube[i]]) ** 2, 0);
    const grayDistance = rgb.reduce((sum, value) => sum + (value - (8 + gray * 10)) ** 2, 0);
    return `\x1b[${background ? 48 : 38};5;${grayDistance < cubeDistance ? 232 + gray : 16 + 36 * cube[0] + 6 * cube[1] + cube[2]}m`;
  };
  return {
    name: data.name,
    fg: (key, text) => `${ansi(key, false)}${text}\x1b[39m`, bg: (key, text) => `${ansi(key, true)}${text}\x1b[49m`,
    bold: text => `\x1b[1m${text}\x1b[22m`, italic: text => `\x1b[3m${text}\x1b[23m`,
    underline: text => `\x1b[4m${text}\x1b[24m`, strikethrough: text => `\x1b[9m${text}\x1b[29m`,
  };
}

/** Read-only user presentation configuration. No package discovery, watchers or settings writes. */
export function loadPresentation(agentDir, { trueColor } = {}) {
  const path = join(agentDir, 'settings.json'), settings = existsSync(path) ? json(path) : {};
  const name = settings.theme ?? 'dark';
  if (typeof name !== 'string' || !/^[\w.-]+$/.test(name)) throw new Error('Unsupported durable theme selection (automatic light/dark not implemented)');
  const themePath = join(agentDir, 'themes', `${name}.json`);
  const theme = themeFromJson(existsSync(themePath) ? json(themePath) : ['dark', 'light'].includes(name) ? builtIn(name) : (() => { throw new Error(`Missing selected native theme: ${name}`); })(), trueColor);
  const integer = (key, fallback, low, high) => { const value = settings[key] ?? fallback; if (!Number.isInteger(value) || value < low || value > high) throw new Error(`Invalid presentation setting: ${key}`); return value; };
  const keyPath = join(agentDir, 'keybindings.json'), bindings = existsSync(keyPath) ? json(keyPath) : {};
  if (!bindings || Array.isArray(bindings) || typeof bindings !== 'object') throw new Error('Invalid native keybindings');
  for (const value of Object.values(bindings)) if (!(typeof value === 'string' || (Array.isArray(value) && value.every(key => typeof key === 'string')))) throw new Error('Invalid native action binding');
  const additional = Object.fromEntries(Object.keys(bindings).filter(key => !Object.hasOwn(TUI_KEYBINDINGS, key) && !Object.hasOwn(appActions, key) && !Object.hasOwn(unsupportedActions, key)).map(key => [key, { defaultKeys: [] }]));
  return {
    theme, themePath: existsSync(themePath) ? themePath : fileURLToPath(new URL(`./themes/${name}.json`, import.meta.url)),
    editorPaddingX: integer('editorPaddingX', 0, 0, 3), outputPad: integer('outputPad', 1, 0, 1), autocompleteMaxVisible: integer('autocompleteMaxVisible', 5, 3, 20),
    hideThinking: settings.hideThinkingBlock === true, showHardwareCursor: settings.showHardwareCursor === true,
    keybindings: new KeybindingsManager({ ...TUI_KEYBINDINGS, ...appActions, ...unsupportedActions, ...additional }, bindings),
    unsupportedActions: [...Object.keys(unsupportedActions), ...Object.keys(additional)],
    diagnostics: settings.tuiMode === 'fullscreen' ? ['Fullscreen requested but not implemented by durable presentation; using regular scrollback.'] : [],
  };
}

export function markdownTheme(theme) {
  return Object.fromEntries([['heading', 'mdHeading'], ['link', 'mdLink'], ['linkUrl', 'mdLinkUrl'], ['code', 'mdCode'], ['codeBlock', 'mdCodeBlock'], ['codeBlockBorder', 'mdCodeBlockBorder'], ['quote', 'mdQuote'], ['quoteBorder', 'mdQuoteBorder'], ['hr', 'mdHr'], ['listBullet', 'mdListBullet']].map(([name, color]) => [name, text => theme.fg(color, text)]).concat(['bold', 'italic', 'strikethrough', 'underline'].map(name => [name, text => theme[name](text)])));
}
export function selectTheme(theme) {
  return { selectedPrefix: text => theme.fg('accent', text), selectedText: text => theme.fg('accent', text), description: text => theme.fg('muted', text), scrollInfo: text => theme.fg('dim', text), noMatch: text => theme.fg('warning', text) };
}
