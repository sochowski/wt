#!/usr/bin/env node
import { readFileSync, fstatSync, statSync, closeSync, writeFileSync } from 'node:fs';
import { openRuntime, dependencies, stateCommand, setHostCapability } from './runtime.mjs';
import { runJob } from './jobs.mjs';

function verifyRuntime() {
  const [major, minor] = process.versions.node.split('.').map(Number);
  if (major !== 22 || minor < 22) throw new Error('Requires Node 22.22+ (22.x)');
  for (const [name, version] of [['pi-durable','1.0.3'],['pi-ai','1.0.3'],['chord','1.0.3'],['pi-tui','0.87.1']]) {
    const file = new URL(`./node_modules/@earendil-works/${name}/package.json`, import.meta.url);
    if (JSON.parse(readFileSync(file, 'utf8')).version !== version) throw new Error(`Dependency mismatch: ${dependencies}`);
  }
}
function inheritedLock(fd, path) {
  const inherited = fstatSync(fd), expected = statSync(path);
  if (inherited.ino !== expected.ino || inherited.dev !== expected.dev || !inherited.isFile()) throw new Error('Missing inherited process lifetime lock');
}
try {
  verifyRuntime();
  const mode = process.argv[2];
  let launch;
  if (mode === 'bootstrap') launch = JSON.parse(readFileSync(0, 'utf8'));
  else if (mode === 'interactive' || mode === 'job') launch = { root: process.env.WT_ROOT_ID, agent: process.env.WT_AGENT_ID, runtime: process.env.WT_RUNTIME_ID, identity: JSON.parse(process.argv[3]) };
  else throw new Error('Only explicit WT bootstrap/interactive launch is supported');
  inheritedLock(3, `${process.env.WT_DB}.agent-${launch.agent}.lock`);
  inheritedLock(4, `${launch.identity.store}.wt-lock`);
  if (mode === 'bootstrap') {
    const runtime = await openRuntime(launch, { bootstrap: true, fence: async () => {} });
    await runtime.close(); // Never resume/bootstrap generation.
    if (runtime.plugins) console.log(JSON.stringify({ plugin_source: launch.identity.plugin_source, plugin_contract: launch.identity.plugin_contract }));
  } else {
    if (!launch.identity.read_only) inheritedLock(5, launch.identity.writer_lock);
    {
      const capability = readFileSync(6, 'utf8'); closeSync(6);
      if (!/^[a-f0-9]{64}$/.test(capability)) throw new Error('Missing private host capability');
      setHostCapability(capability);
    }
    if (!process.stdin.isTTY || !process.stdout.isTTY) throw new Error('Durable WT requires a managed terminal');
    const runtime = await openRuntime(launch);
    if (mode === 'job') {
      try {
        console.log(`WT durable ${launch.identity.role} task ${launch.identity.job}`);
        const result = await runJob(runtime, stateCommand);
        console.log(result.success ? 'Host-observed result published; writer requires independent review.' : 'Non-success: failed, missing or uncertain acceptance/review evidence.');
      } finally { await runtime.close(); }
    } else {
      if (launch.identity.job) throw new Error('Delegated task cannot launch as ordinary interactive peer');
      const { interactive } = await import('./tui.mjs');
      await interactive(runtime, process.env.WT_DURABLE_SMOKE === '1' ? { stateObserver: state => writeFileSync(`${launch.identity.store}.smoke-status.json`, JSON.stringify(state), { mode: 0o600 }) } : undefined);
    }
  }
} catch (error) {
  if (error?.code === 'WT_PRIMARY_MODEL') console.error('wt durable: configured primary provider/model missing or unsupported by the pinned catalog; configure ordinary Pi first. No silent model/provider fallback.');
  if (error?.code === 'WT_PRIMARY_THINKING') console.error('wt durable: invalid configured defaultThinkingLevel/modelThinkingLevels; use off, minimal, low, medium, high, xhigh or max. Configure ordinary Pi first.');
  if(error?.code==='WT_JOB_EVIDENCE_FILES')console.error('wt durable: assigned checkout exceeds the bounded 4096-path evidence limit; task cannot run with this admission. Host is paused, not retried automatically.');
  if(error?.code==='WT_JOB_EVIDENCE_SIZE')console.error('wt durable: assigned checkout exceeds the bounded evidence byte limit; task cannot run with this admission. Host is paused, not retried automatically.');
  // Provider/credential errors can include secret material. Never print them.
  console.error('wt durable: launch failed (identity, locks, dependency, terminal or authentication); no replacement/native fallback.');
  process.exitCode = 1;
}
