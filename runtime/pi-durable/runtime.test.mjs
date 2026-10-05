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
import { context, openRuntime, definition, dependencies, admitMessage, BridgeDoc, validateStore, readOnlyEnvironment, configuredModel } from './runtime.mjs';
import { interactive } from './tui.mjs';

async function fixture(t, responses=[fauxAssistantMessage('OK')], options={}) {
  const directory = await realpath(await mkdtemp(join(tmpdir(),'wt-runtime-'))); t.after(()=>rm(directory,{recursive:true,force:true}));
  const launch = { root:'test-root',agent:'test-agent',runtime:'test-runtime',identity:{store:join(directory,'session.sqlite'),uuid:randomBytes(16).toString('hex'),conversation:1,definition,dependencies,cwd:directory,initialized:false,read_only:options.readOnly===true} };
  const faux = fauxProvider(options); faux.setResponses(responses); const models = createModels(); models.setProvider(faux.provider);
  const runtime = await openRuntime(launch,{bootstrap:true,models,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{}}); await runtime.close(); launch.identity.initialized=true;
  const open = async (extra={}) => { const r = await openRuntime(launch,{models,fence:async()=>{},reportStatus:async()=>{},...extra}); t.after(()=>r.close()); return r; };
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
