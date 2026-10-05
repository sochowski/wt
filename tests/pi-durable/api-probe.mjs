// Public-export investigation only: never starts a renderer or native Pi session.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
import * as durable from '@earendil-works/pi-durable';
if (!process.env.WT_PI_SDK_PATH) throw new Error('Supply WT_PI_SDK_PATH (installed package root)');
const path = resolve(process.env.WT_PI_SDK_PATH);
const sdk = await import(pathToFileURL(join(path, 'dist/index.js')));
const require = createRequire(join(path, 'package.json'));
const tui = await import(pathToFileURL(require.resolve('@earendil-works/pi-tui')));
const present = ['AssistantMessageComponent', 'UserMessageComponent', 'ToolExecutionComponent', 'CustomEditor', 'DynamicBorder',
  'initTheme', 'getMarkdownTheme', 'ModelRuntime', 'SettingsManager', 'loadProjectContextFiles', 'loadSkills', 'formatSkillsForPrompt', 'readStoredCredential'];
const missing = ['KeybindingsManager', 'WorkingStatusIndicator', 'InteractiveThemeController', 'createAllToolRenderers', 'buildSystemPromptSections', 'runDurableTui'];
for (const name of present) assert.equal(typeof sdk[name], 'function', name);
for (const name of missing) assert.equal(sdk[name], undefined, name);
for (const name of ['TuiAltScreen', 'TuiMainScreen', 'ProcessTerminal', 'Editor', 'Markdown', 'ScrollView', 'SelectList', 'Container', 'matchesKey']) assert.equal(typeof tui[name], 'function', name);
for (const name of ['Harness', 'createRegistry', 'defineTool', 'defineTask', 'defineDoc', 'watchEvents']) assert.ok(durable[name], name);
assert.deepEqual(new tui.Text('headless public TUI component', 0, 0).render(80).map(line => line.trimEnd()), ['headless public TUI component']);
const sdkPackage = JSON.parse(await readFile(join(path, 'package.json'), 'utf8'));
const tuiPackage = JSON.parse(await readFile(require.resolve('@earendil-works/pi-tui/package.json'), 'utf8'));
console.log(JSON.stringify({ sdk: sdkPackage.version, tui: tuiPackage.version, durable: '1.0.3', publicSdkExports: present,
  notPublicSdkExports: missing, headlessRender: 'passed', terminalStarted: false, durableFrontendExported: false }, null, 2));
