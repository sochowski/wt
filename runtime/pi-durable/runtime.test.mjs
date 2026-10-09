import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, realpath, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { fork } from 'node:child_process';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { createModels } from '@earendil-works/pi-ai/models';
import { fauxProvider, fauxAssistantMessage } from '@earendil-works/pi-ai/providers/faux';
import { context, openRuntime, definition, dependencies, admitMessage, BridgeDoc, validateStore, readOnlyEnvironment, configuredModel, configuredThinking } from './runtime.mjs';
import { interactive } from './tui.mjs';
import { getKeybindings, setKeybindings, KeybindingsManager, TUI_KEYBINDINGS } from '@earendil-works/pi-tui';
import { appActions, loadPresentation } from './presentation.mjs';

function presentationWith(bindings) {
  return { ...loadPresentation(process.env.PI_CODING_AGENT_DIR), keybindings: new KeybindingsManager({ ...TUI_KEYBINDINGS, ...appActions }, bindings) };
}

async function fixture(t, responses=[fauxAssistantMessage('OK')], options={}) {
  const directory = await realpath(await mkdtemp(join(tmpdir(),'wt-runtime-'))); t.after(()=>rm(directory,{recursive:true,force:true}));
  const launch = { root:'test-root',agent:'test-agent',runtime:'test-runtime',identity:{store:join(directory,'session.sqlite'),uuid:randomBytes(16).toString('hex'),conversation:1,definition,dependencies,cwd:directory,initialized:false,read_only:options.readOnly===true} };
  const faux = fauxProvider(options); faux.setResponses(responses); const models = createModels(); models.setProvider(faux.provider);
  await writeFile(join(directory,'settings.json'),JSON.stringify(options.settings ?? {defaultThinkingLevel:'off'}));
  const runtime = await openRuntime(launch,{bootstrap:true,models,agentDir:directory,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{},reportDiagnostic:options.reportDiagnostic}); await runtime.close(); launch.identity.initialized=true;
  const open = async (extra={}) => { const r = await openRuntime(launch,{models,agentDir:directory,fence:async()=>{},reportStatus:async()=>{},...extra}); t.after(()=>r.close()); return r; };
  return {launch,models,faux,open};
}
async function until(check) { for(let i=0;i<500;i++) { if(await check()) return; await delay(10); } throw new Error('condition timeout'); }

test('exact envelope rejects store confusion, missing stores, cwd, definition and stale runtime BEFORE open/reconciliation', async t => {
  const f = await fixture(t); const runtime = await f.open(); await runtime.close();
  assert.throws(()=>validateStore({...f.launch,agent:'another'}),/identity mismatch/);
  assert.throws(()=>validateStore({...f.launch,identity:{...f.launch.identity,definition:'changed'}}),/Incompatible/);
  await assert.rejects(f.open({fence:async()=>{throw new Error('stale');}}),/stale/);
  await rm(f.launch.identity.store); assert.throws(()=>validateStore(f.launch),/Missing/);
  await assert.rejects(f.open(),/Missing/);
});

test('immutable inbox admission reconciles submission ID without second provider call; notifications never wake', async t => {
  const f=await fixture(t); const r=await f.open();
  const message={id:'message',root_id:f.launch.root,recipient:f.launch.agent,sender:'human',body:'hello',request:true};
  const id=await admitMessage(r,message); await (await r.harness.submission(id,context)).wait(context);
  assert.equal(await admitMessage(r,message),id); assert.equal(f.faux.state.callCount,1);
  await assert.rejects(admitMessage(r,{...message,body:'changed'}),/immutable/);
  await assert.rejects(admitMessage(r,{...message,recipient:'other'}),/Cross-recipient/);
  await admitMessage(r,{...message,id:'notify',body:'info',request:false});
  await delay(100); assert.equal(f.faux.state.callCount,1);
  assert.equal(Object.keys((await r.harness.snapshot(BridgeDoc,r.root.id,context)).notifications).length,1);
});

test('validated relaunch resumes unfinished work; explicit abort stays cancelled and model/thinking persist', async t => {
  const f=await fixture(t,[fauxAssistantMessage('long '.repeat(100))],{tokensPerSecond:20,models:[{id:'faux-1'},{id:'faux-2',reasoning:true}]});
  let r=await f.open(); const s=await r.input('work'); await until(()=>f.faux.state.callCount>0); await r.close();
  f.faux.setResponses([fauxAssistantMessage('recovered')]); const beforeRecovery=f.faux.state.callCount; r=await f.open(); assert.equal(f.faux.state.callCount,beforeRecovery); await r.resume();
  assert.equal((await (await r.harness.submission(s.id,context)).wait(context)).status,'done');
  await r.root.configure({model:{provider:'faux',modelId:'faux-2'},thinkingLevel:'low'},context);
  f.faux.setResponses([fauxAssistantMessage('long '.repeat(100))]); const cancelled=await r.input('cancel'); await until(()=>f.faux.state.callCount>0); await r.abort(); await r.close();
  f.faux.setResponses([fauxAssistantMessage('must not call')]); const beforeAbortReopen=f.faux.state.callCount; r=await f.open(); await r.resume(); await delay(100);
  assert.equal(f.faux.state.callCount,beforeAbortReopen); assert.equal((await (await r.harness.submission(cancelled.id,context)).status(context)).reason,'aborted');
  const agent=await r.root.agent(context); assert.equal(agent.model.modelId,'faux-2'); assert.equal(agent.thinkingLevel,'low');
});

test('actual SIGKILL/reopen through production runtime retains and retries unfinished submission', async t => {
  const f=await fixture(t); const path=join(f.launch.identity.cwd,'launch.json'); await writeFile(path,JSON.stringify(f.launch));
  const child=fork(new URL('./crash-worker.mjs',import.meta.url),[path],{stdio:['ignore','pipe','pipe','ipc']}); t.after(()=>{if(child.exitCode===null)child.kill('SIGKILL');});
  const [ready]=await once(child,'message'); assert.ok(ready.submission); child.kill('SIGKILL'); const [,sig]=await once(child,'exit'); assert.equal(sig,'SIGKILL');
  f.faux.setResponses([fauxAssistantMessage('after kill')]); const r=await f.open(); assert.equal(f.faux.state.callCount,0); await r.resume(); assert.equal((await (await r.harness.submission(ready.submission,context)).wait(context)).status,'done');
});

class Terminal {
  columns=80; rows=24; kittyProtocolActive=false; output=''; started=false; stopped=false;
  start(input,resize){this.input=input;this.resize=resize;this.started=true;} stop(){this.stopped=true;}
  write(s){this.output+=s;} hideCursor(){} showCursor(){} moveBy(){} clearLine(){} clearFromCursor(){} clearScreen(){} setTitle(){} setProgress(){} async drainInput(){}
  type(text){for(const c of text)this.input(c);}
}
test('public TUI real editor input, committed render, model/thinking and terminal close lifecycle',async t=>{
  const f=await fixture(t,[fauxAssistantMessage('terminal answer')],{models:[{id:'faux-1'},{id:'faux-2'}]}); const r=await f.open(); const terminal=new Terminal(), states=[]; const ui=interactive(r,{terminal,poll:false,stateObserver:async state=>states.push(state)});
  await until(()=>terminal.started); terminal.type('hello');terminal.input('\r'); await until(()=>terminal.output.includes('terminal answer'));
  terminal.type('/thinking low');terminal.input('\r'); await until(async()=>(await r.root.agent(context)).thinkingLevel==='low');
  terminal.type('/model faux/faux-2');terminal.input('\r'); await until(async()=>(await r.root.agent(context)).model.modelId==='faux-2');
  terminal.columns=24;terminal.resize();await delay(50); assert.ok(terminal.output.includes('faux/faux-2'));
  terminal.type('/quit');terminal.input('\r');await ui;assert.equal(terminal.stopped,true);
  assert.ok(states.some(s=>s.submission?.status==='done' && !s.live.run && !s.live.generation && s.live.tools===0 && s.tasks.length===0));
  assert.equal(states.at(-1).stage,'closed');
});

test('settled close releases exact public provider-session resources and owner exits without forced process.exit',async t=>{
  const f=await fixture(t);const path=join(f.launch.identity.cwd,'close.json');await writeFile(path,JSON.stringify(f.launch));
  const child=fork(new URL('./close-worker.mjs',import.meta.url),[path],{stdio:['ignore','pipe','pipe','ipc']});
  t.after(()=>{if(child.exitCode===null)child.kill('SIGKILL');});
  let output='';child.stdout.on('data',b=>output+=b);
  let timer;
  try {
    const [code,signal]=await Promise.race([once(child,'exit'),new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error('provider resource handle prevented owner exit')),3000);})]);
    assert.equal(code,0);assert.equal(signal,null);assert.ok(output.includes('EXACT_PROVIDER_RESOURCE_CLOSE_OK'));
  } finally { clearTimeout(timer); }
});

test('configured primary provider/model is exact; missing or unsupported model fails before root creation without a Haiku fallback',async t=>{
  const f=await fixture(t);const agentDir=join(f.launch.identity.cwd,'primary');
  await (await import('node:fs/promises')).mkdir(agentDir);
  assert.throws(()=>configuredModel(agentDir),/explicit configured/);
  await writeFile(join(agentDir,'settings.json'),JSON.stringify({defaultProvider:'openai-codex',defaultModel:'gpt-6.1-sol'}));
  assert.deepEqual(configuredModel(agentDir),{provider:'openai-codex',modelId:'gpt-6.1-sol'});
  await assert.rejects(openRuntime({...f.launch,identity:{...f.launch.identity,store:join(f.launch.identity.cwd,'unsupported.sqlite'),uuid:randomBytes(16).toString('hex'),initialized:false}},{bootstrap:true,models:f.models,model:{provider:'missing-primary',modelId:'unknown'},fence:async()=>{}}),/Configured primary model/);
});

test('fresh thinking follows native settings precedence, medium fallback and visible capability clamp', async t => {
  for (const [settings, model, expected, diagnostic] of [
    [{defaultThinkingLevel:'high'}, {id:'faux-1',reasoning:true}, 'high', false],
    [{}, {id:'faux-1',reasoning:true}, 'medium', false],
    [{defaultThinkingLevel:'low',modelThinkingLevels:{'faux/faux-1':'high'}}, {id:'faux-1',reasoning:true}, 'high', false],
    [{defaultThinkingLevel:'high'}, {id:'faux-1',reasoning:false}, 'off', true],
    [{}, {id:'faux-1',reasoning:false}, 'off', true],
    [{defaultThinkingLevel:'max'}, {id:'faux-1',reasoning:true}, 'high', true],
  ]) {
    const notices=[];
    const f=await fixture(t,undefined,{settings,models:[model],reportDiagnostic:message=>notices.push(message)}), r=await f.open();
    assert.equal((await r.root.agent(context)).thinkingLevel,expected);
    assert.equal((await r.root.agent(context)).model.modelId,'faux-1');
    assert.equal(notices.length,diagnostic?1:0);
    assert.equal(f.faux.state.callCount,0);
    await r.close();
  }
});

test('invalid thinking rejects bootstrap; reopen ignores changed/broken defaults and /thinking stays conversation-local', async t => {
  for (const value of ['invalid', '', 2, {}, false]) {
    assert.throws(()=>configuredThinking({defaultThinkingLevel:value},{provider:'faux',id:'faux-1',reasoning:true}),{code:'WT_PRIMARY_THINKING'});
  }
  const f=await fixture(t,undefined,{settings:{defaultThinkingLevel:'high'},models:[{id:'faux-1',reasoning:true}]});
  const path=join(f.launch.identity.cwd,'settings.json');
  await writeFile(path,JSON.stringify({defaultThinkingLevel:'invalid'}));
  await assert.rejects(openRuntime({...f.launch,identity:{...f.launch.identity,store:join(f.launch.identity.cwd,'invalid.sqlite'),uuid:randomBytes(16).toString('hex'),initialized:false}},{bootstrap:true,models:f.models,agentDir:f.launch.identity.cwd,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{}}),{code:'WT_PRIMARY_THINKING'});
  const r=await f.open(); assert.equal((await r.root.agent(context)).thinkingLevel,'high');
  const terminal=new Terminal(),ui=interactive(r,{terminal,poll:false});
  await until(()=>terminal.started);terminal.type('/thinking low');terminal.input('\r');
  await until(async()=>(await r.root.agent(context)).thinkingLevel==='low');
  terminal.type('/quit');terminal.input('\r');await ui;
  assert.equal(JSON.parse(await (await import('node:fs/promises')).readFile(path,'utf8')).defaultThinkingLevel,'invalid');
  await writeFile(path,'broken JSON');
  const reopened=await f.open();assert.equal((await reopened.root.agent(context)).thinkingLevel,'low');assert.equal(f.faux.state.callCount,0);
});

test('read-only host has fixed tools, no generic bash/WT mutations and an environment mutation/spawn veto',async t=>{
  const f=await fixture(t,undefined,{readOnly:true}); const r=await f.open();
  assert.deepEqual((await r.root.agent(context)).tools.map(t=>t.name),['read','wt_workspace']);
  let touched=false;const environment=readOnlyEnvironment({cwd:f.launch.identity.cwd,exec(){touched=true;},writeFile(){touched=true;},readTextFile(){return 'allowed';}});
  await assert.rejects(environment.exec('touch forbidden'),/Read-only/);
  await assert.rejects(environment.writeFile('forbidden','x'),/Read-only/);
  assert.equal(touched,false);assert.equal(environment.readTextFile('fixture'),'allowed');
  assert.throws(()=>{environment.cwd='escape';},/immutable/);
});

test('explicit TUI human /stop calls only private self control and closes; model tools do not expose human control',async t=>{
  const f=await fixture(t);const r=await f.open(),terminal=new Terminal();let stopped;
  assert.ok(!(await r.root.agent(context)).tools.some(t=>t.name.includes('human') || t.name.includes('self_stop')));
  const ui=interactive(r,{terminal,poll:false,selfStop:async launch=>{stopped={root:launch.root,agent:launch.agent,runtime:launch.runtime};}});
  await until(()=>terminal.started);terminal.type('/stop');terminal.input('\r');await ui;
  assert.deepEqual(stopped,{root:f.launch.root,agent:f.launch.agent,runtime:f.launch.runtime});assert.equal(terminal.stopped,true);
});

test('production TUI busy steering and followUp inputs reach real pinned durable scheduler and committed transcript',async t=>{
 const f=await fixture(t,[fauxAssistantMessage('initial '.repeat(20)),fauxAssistantMessage('steering answer'),fauxAssistantMessage('followup answer'),fauxAssistantMessage('final answer')],{tokensPerSecond:80});
 const r=await f.open(),terminal=new Terminal(),ui=interactive(r,{terminal,poll:false});
 await until(()=>terminal.started);terminal.type('initial user turn');terminal.input('\r');await until(()=>f.faux.state.callCount>0);
 terminal.type('/steer STEERING_INPUT_PROOF');terminal.input('\r');
 terminal.type('/followup FOLLOWUP_INPUT_PROOF');terminal.input('\r');
 await until(()=>terminal.output.includes('STEERING_INPUT_PROOF') && terminal.output.includes('FOLLOWUP_INPUT_PROOF'));
 await r.root.waitForIdle(context);assert.ok(f.faux.state.callCount>=2);
 terminal.type('/quit');terminal.input('\r');await ui;assert.equal(terminal.stopped,true);
});

test('native action clear does not abort or send a draft; double clear closes recoverably and restores keybinding lifetime',async t=>{
  const f=await fixture(t),r=await f.open(),terminal=new Terminal();let aborted=0,inputs=0;
  const originalAbort=r.abort,originalInput=r.input;r.abort=async()=>{aborted++;return originalAbort();};r.input=async(...args)=>{inputs++;return originalInput(...args);};
  const {getKeybindings}=await import('@earendil-works/pi-tui'),previous=getKeybindings();const ui=interactive(r,{terminal,poll:false});
  await until(()=>terminal.started);terminal.type('unsent draft');terminal.input('\x04');await delay(30);assert.equal(terminal.stopped,false);
  terminal.input('\x03');terminal.input('\x03');await ui;assert.equal(terminal.stopped,true);assert.equal(aborted,0);assert.equal(inputs,0);assert.equal(f.faux.state.callCount,0);assert.equal(getKeybindings(),previous);
});

test('native model/search and thinking selector/action keys persist only exact durable conversation and restore editor focus',async t=>{
  const f=await fixture(t,[fauxAssistantMessage('focus restored')],{models:[{id:'faux-1'},{id:'faux-2',reasoning:true}]});const r=await f.open(),terminal=new Terminal(),ui=interactive(r,{terminal,poll:false});
  await until(()=>terminal.started);terminal.input('\x0c');await until(()=>terminal.output.includes('Model (pinned catalog'));
  terminal.type('faux-2');terminal.input('\r');await until(async()=>(await r.root.agent(context)).model.modelId==='faux-2');
  terminal.input('\x1b[Z');await until(async()=>(await r.root.agent(context)).thinkingLevel==='minimal');
  terminal.type('/thinking');terminal.input('\r');await until(()=>terminal.output.includes('Thinking level (durable conversation only)'));
  terminal.type('high');terminal.input('\r');await until(async()=>(await r.root.agent(context)).thinkingLevel==='high');
  terminal.input('\x0c');await delay(30);terminal.input('\x1b');await delay(30);terminal.type('hello after selector');terminal.input('\r');await until(()=>terminal.output.includes('focus restored'));
  terminal.type('/quit');terminal.input('\r');await ui;const reopened=await f.open(),agent=await reopened.root.agent(context);assert.equal(agent.model.modelId,'faux-2');assert.equal(agent.thinkingLevel,'high');assert.equal(reopened.launch.identity.uuid,f.launch.identity.uuid);assert.equal(reopened.root.id,1);
});

test('native busy Enter steers, Alt-Enter explicitly follows up through pinned scheduler without changing identity',async t=>{
  const f=await fixture(t,[fauxAssistantMessage('initial '.repeat(30)),fauxAssistantMessage('steered answer'),fauxAssistantMessage('followup answer'),fauxAssistantMessage('final answer')],{tokensPerSecond:80});
  const r=await f.open(),terminal=new Terminal(),calls=[],original=r.input;r.input=async(...args)=>{calls.push(args);return original(...args);};
  const ui=interactive(r,{terminal,poll:false});await until(()=>terminal.started);terminal.type('initial');terminal.input('\r');await until(()=>f.faux.state.callCount>0);
  terminal.type('NATIVE_ENTER_STEER');terminal.input('\r');terminal.type('NATIVE_ALT_FOLLOWUP');terminal.input('\x1b[13;3u');
  await until(()=>calls.length===3);assert.equal(calls[1][1],'steer');assert.equal(calls[2][1],'followUp');
  await r.root.waitForIdle(context);const view=await r.root.watch(context);assert.ok(JSON.stringify(view.value.entries).includes('NATIVE_ENTER_STEER'));assert.ok(JSON.stringify(view.value.entries).includes('NATIVE_ALT_FOLLOWUP'));await view.stop();
  terminal.type('/quit');terminal.input('\r');await ui;
});

test('native Escape beats conflicting history and durably aborts while Ctrl-C only clears; reopen never resumes cancelled work',async t=>{
  const f=await fixture(t,[fauxAssistantMessage('long '.repeat(100))],{tokensPerSecond:20});const r=await f.open(),terminal=new Terminal();let submission;
  const original=r.input;r.input=async(...args)=>{submission=await original(...args);return submission;};const ui=interactive(r,{terminal,poll:false,presentation:presentationWith({'tui.editor.historyPrevious':'escape'})});
  await until(()=>terminal.started);terminal.type('work');terminal.input('\r');await until(()=>f.faux.state.callCount>0);
  terminal.type('draft');terminal.input('\x03');await delay(30);assert.notEqual((await submission.status(context)).reason,'aborted');terminal.input('\x1b');
  await until(async()=>(await submission.status(context)).reason==='aborted');terminal.input('\x04');await ui;
  const before=f.faux.state.callCount,reopened=await f.open();await reopened.resume();await delay(50);assert.equal(f.faux.state.callCount,before);assert.equal((await (await reopened.harness.submission(submission.id,context)).status(context)).reason,'aborted');
});

test('empty exit beats history/clear collisions and live close remains recoverable rather than cancellation', async t => {
  const f=await fixture(t,[fauxAssistantMessage('long '.repeat(100))],{tokensPerSecond:20});
  for (const bindings of [{'tui.editor.historyPrevious':'ctrl+d'}, {'app.clear':'ctrl+d'}]) {
    const r=await f.open(),terminal=new Terminal(),ui=interactive(r,{terminal,poll:false,presentation:presentationWith(bindings)});
    await until(()=>terminal.started);terminal.input('\x04');await ui;assert.equal(terminal.stopped,true);assert.equal(f.faux.state.callCount,0);
  }
  const r=await f.open(),terminal=new Terminal();let submission;
  const original=r.input;r.input=async(...args)=>{submission=await original(...args);return submission;};
  const ui=interactive(r,{terminal,poll:false,presentation:presentationWith({'app.clear':'ctrl+d'})});await until(()=>terminal.started);
  terminal.type('recoverable work');terminal.input('\r');await until(()=>f.faux.state.callCount>0);
  terminal.type('unsent draft');terminal.input('\x04');await delay(30);assert.equal(terminal.stopped,false);assert.notEqual((await submission.status(context)).reason,'aborted');
  terminal.input('\x04');await ui;
  f.faux.setResponses([fauxAssistantMessage('recovered')]);const reopened=await f.open(),before=f.faux.state.callCount;await reopened.resume();
  assert.equal((await (await reopened.harness.submission(submission.id,context)).wait(context)).status,'done');assert.ok(f.faux.state.callCount>before);
  assert.equal(reopened.launch.identity.uuid,f.launch.identity.uuid);assert.equal(reopened.root.id,1);
});

for (const failure of ['watch', 'setup', 'watch-start', 'terminal-start', 'resume-fence', 'watch-stop', 'terminal-stop', 'runtime-close', 'closing-observer', 'poll-watch-stop']) {
  test(`UI lifecycle restores keybindings and all owned cleanup after ${failure} rejection`, async t => {
    const f=await fixture(t),r=await f.open(),terminal=new Terminal(),previous=getKeybindings();t.after(()=>setKeybindings(previous));
    const error=new Error(`injected ${failure}`);let runtimeClosed=0,watchStopped=0;
    const originalClose=r.close;r.close=async()=>{runtimeClosed++;await originalClose();if(failure==='runtime-close'&&runtimeClosed===1)throw error;};
    const originalWatch=r.root.watch.bind(r.root);r.root.watch=async(...args)=>{
      if(failure==='watch')throw error;
      const watch=await originalWatch(...args);
      return { get value(){return watch.value;}, start: async(...startArgs)=>{if(failure==='watch-start')throw error;return watch.start(...startArgs);}, stop:async()=>{watchStopped++;await watch.stop();if(failure==='watch-stop'||failure==='poll-watch-stop')throw error;} };
    };
    if(failure==='setup')r.resources.skills=[{get name(){throw error;}}];
    if(failure==='terminal-start'){const start=terminal.start.bind(terminal);terminal.start=(...args)=>{start(...args);throw error;};}
    const stop=terminal.stop.bind(terminal);terminal.stop=()=>{stop();if(failure==='terminal-stop'||failure==='watch-stop')throw error;};
    if(failure==='resume-fence')r.resume=async()=>{await r.fence();throw error;};
    const ui=interactive(r,{terminal,poll:failure==='poll-watch-stop',stateObserver:async state=>{if(failure==='closing-observer'&&state.stage==='closing')throw error;}});
    const rejected=assert.rejects(ui,new RegExp(`injected ${failure}`));
    if(['watch-stop','terminal-stop','runtime-close','closing-observer'].includes(failure)){await until(()=>terminal.started);terminal.input('\x04');}
    if(failure==='poll-watch-stop'){await until(()=>terminal.started);r.fence=async()=>{throw error;};}
    await rejected;assert.equal(getKeybindings(),previous);assert.equal(terminal.stopped,true);assert.equal(runtimeClosed,1);
    assert.equal(watchStopped,['watch','setup'].includes(failure)?0:1);assert.equal(f.faux.state.callCount,0);
    const reopened=await f.open();assert.equal(reopened.launch.identity.uuid,f.launch.identity.uuid);assert.equal(reopened.root.id,1);
  });
}

test('native prompt history restores from real durable user entries after close without JSONL/native identity synthesis',async t=>{
  const f=await fixture(t);let r=await f.open();const turn=await r.input('RETAINED_EDITOR_HISTORY');await turn.wait(context);await r.close();
  r=await f.open();const terminal=new Terminal(),before=f.faux.state.callCount,calls=[],original=r.input;
  r.input=async(...args)=>{calls.push(args);return original(...args);};
  const ui=interactive(r,{terminal,poll:false});await until(()=>terminal.started);
  terminal.input('\x1b[A');await delay(50);assert.equal(f.faux.state.callCount,before);assert.equal(calls.length,0,'history navigation must not submit');
  terminal.input('\x05');terminal.type('_RESUBMITTED');terminal.input('\r');await until(()=>calls.length===1);await r.root.waitForIdle(context);
  const view=await r.root.watch(context),users=view.value.entries.flatMap(entry=>entry.model||[]).filter(message=>message.role==='user');await view.stop();
  terminal.input('\x04');await ui;
  assert.equal(calls[0][0],'RETAINED_EDITOR_HISTORY_RESUBMITTED','must submit actual restored draft, not match old transcript output');
  assert.ok(JSON.stringify(users.at(-1)).includes('RETAINED_EDITOR_HISTORY_RESUBMITTED'),'real durable transport must commit the distinguishable restored prompt');
  assert.equal(r.root.id,1);assert.equal(r.launch.identity.uuid,f.launch.identity.uuid);
});
