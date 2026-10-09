import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, realpath, rm, mkdir, writeFile, readFile, cp, readdir, symlink, rename } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { randomBytes } from 'node:crypto';
import { fork } from 'node:child_process';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { createModels, StringEnum } from '@earendil-works/pi-ai';
import { fauxProvider, fauxAssistantMessage, fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { openRuntime, context, validateStore } from './runtime.mjs';
import { pluginProfile, pluginDefinition, pluginDependencies } from './plugin-contract-v2.mjs';
import { PluginDoc } from './plugins-v2.mjs';
import { interactive } from './tui.mjs';
import { loadPresentation } from './presentation.mjs';
import { bindPluginTheme, DynamicBorder, getMarkdownTheme, SettingsManager } from './plugin-sdk-v2.mjs';
class Terminal {
  columns=100;rows=30;kittyProtocolActive=false;output='';started=false;stopped=false;
  start(input,resize){this.input=input;this.resize=resize;this.started=true;}stop(){this.stopped=true;}
  write(s){this.output+=s;}hideCursor(){}showCursor(){}moveBy(){}clearLine(){}clearFromCursor(){}clearScreen(){}setTitle(){}setProgress(){}async drainInput(){}
  type(text){for(const c of text)this.input(c);}
}
async function until(fn){for(let i=0;i<700;i++){if(await fn())return;await delay(10);}throw new Error('Plugin fixture timeout');}
async function fixture(t,responses,extra={}){
  const dir=await realpath(await mkdtemp(join(tmpdir(),'wt-plugin-v2-')));t.after(()=>rm(dir,{recursive:true,force:true}));
  const launch={root:'test-root',agent:'test-agent',runtime:'test-runtime',identity:{store:join(dir,'session.sqlite'),uuid:randomBytes(16).toString('hex'),conversation:1,definition:pluginDefinition,dependencies:pluginDependencies,profile:pluginProfile,cwd:dir,initialized:false,...extra}};
  const models=createModels(),faux=fauxProvider();models.setProvider(faux.provider);faux.setResponses(responses);
  const bootstrap=await openRuntime(launch,{bootstrap:true,models,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{}});await bootstrap.close();launch.identity.initialized=true;
  const open=async(extra={})=>{const r=await openRuntime(launch,{models,fence:async()=>{},reportStatus:async()=>{},...extra});t.after(()=>r.close());return r;};
  return{launch,models,faux,open};
}
const todo=(action,fields={},id=randomBytes(6).toString('hex'))=>fauxAssistantMessage(fauxToolCall('todo',{action,...fields},{id}),{stopReason:'toolUse'});
const question={questions:[{question:'Which actual option?',header:'Choice',options:[{label:'Alpha',description:'First option'},{label:'Beta',description:'Second option'}]}]};
test('actual pinned todo factory executes through durable scheduler; details, overlay, command and restart state are real',async t=>{
  const f=await fixture(t,[todo('create',{subject:'ACTUAL_TODO_SUBJECT'},'create-one'),fauxAssistantMessage('CREATED'),todo('update',{id:1,status:'in_progress',activeForm:'working'},'update-one'),fauxAssistantMessage('UPDATED'),todo('delete',{id:1},'delete-one'),fauxAssistantMessage('DELETED')]);
  let r=await f.open();const terminal=new Terminal(),ui=interactive(r,{terminal,poll:false});await until(()=>terminal.started);
  terminal.type('create');terminal.input('\r');await until(()=>terminal.output.includes('CREATED'));await r.root.waitForIdle(context);await r.plugins.update();
  assert.ok(terminal.output.includes('ACTUAL_TODO_SUBJECT'));terminal.input('\x1b[116;6u');await until(()=>terminal.output.includes('ctrl+shift+t'));terminal.input('\x1b[116;6u');terminal.type('/todos');terminal.input('\r');await delay(50);assert.ok(r.plugins.notice.includes('ACTUAL_TODO_SUBJECT'));
  terminal.type('update');terminal.input('\r');await until(()=>terminal.output.includes('UPDATED'));await r.root.waitForIdle(context);await r.plugins.update();terminal.input('\x04');await ui;
  r=await f.open();const doc=await r.harness.snapshot(PluginDoc,r.root.id,context);assert.equal(doc.calls['create-one'].state,'receipted');assert.equal(doc.calls['update-one'].state,'receipted');assert.equal(r.plugins.recoveryHeld,false);
  const second=new Terminal(),ui2=interactive(r,{terminal:second,poll:false});await until(()=>second.started);await until(()=>second.output.includes('ACTUAL_TODO_SUBJECT'));
  second.type('/todos');second.input('\r');await until(()=>r.plugins.notice.includes('In Progress'));
  second.type('delete');second.input('\r');await until(()=>second.output.includes('DELETED'));await r.root.waitForIdle(context);await r.plugins.update();
  const final=await r.root.context(context);assert.equal(final.entries.flatMap(entry=>entry.model||[]).filter(m=>m.role==='toolResult'&&m.toolName==='todo').at(-1).details.tasks[0].status,'deleted');
  second.input('\x04');await ui2;assert.equal(second.stopped,true);assert.equal(r.launch.identity.uuid,f.launch.identity.uuid);
});
test('actual questionnaire custom component choice and Esc produce real plugin results with durable journals and focus cleanup',async t=>{
  const f=await fixture(t,[fauxAssistantMessage(fauxToolCall('ask_user_question',question,{id:'question-choice'}),{stopReason:'toolUse'}),fauxAssistantMessage('CHOICE_DONE'),fauxAssistantMessage(fauxToolCall('ask_user_question',question,{id:'question-cancel'}),{stopReason:'toolUse'}),fauxAssistantMessage('CANCEL_DONE')]);
  const r=await f.open(),terminal=new Terminal(),ui=interactive(r,{terminal,poll:false});await until(()=>terminal.started);
  terminal.type('ask');terminal.input('\r');await until(()=>terminal.output.includes('Which actual option?'));terminal.input('\r');await until(()=>terminal.output.includes('CHOICE_DONE'));await r.root.waitForIdle(context);await r.plugins.update();
  let doc=await r.harness.snapshot(PluginDoc,r.root.id,context);assert.equal(doc.calls['question-choice'].candidate.details.cancelled,false);assert.equal(doc.calls['question-choice'].candidate.details.answers[0].answer,'Alpha');assert.ok(doc.calls['question-choice'].journal.length>0);
  terminal.type('ask again');terminal.input('\r');await until(async()=>Boolean((await r.harness.snapshot(PluginDoc,r.root.id,context))?.calls['question-cancel']));await delay(150);terminal.input('\x1b');await until(()=>terminal.output.includes('CANCEL_DONE'));await r.root.waitForIdle(context);await r.plugins.update();
  doc=await r.harness.snapshot(PluginDoc,r.root.id,context);assert.equal(doc.calls['question-cancel'].candidate.details.cancelled,true);assert.equal(doc.calls['question-cancel'].state,'receipted');terminal.input('\x04');await ui;assert.equal(terminal.stopped,true);
});
test('v2 source/contract identity and unknown APIs fail closed; actual public schema and read-only ceiling remain bounded',async t=>{
  assert.deepEqual(StringEnum(['create','update']),{type:'string',enum:['create','update']});
  const f=await fixture(t,[todo('create',{subject:'DENIED'}),fauxAssistantMessage('DONE')],{read_only:true});const r=await f.open();
  assert.ok(!(await r.root.agent(context)).tools.some(tool=>['todo','ask_user_question'].includes(tool.name)));
  assert.throws(()=>r.plugins.api.registerTool({name:'unknown'}),/unsupported/);assert.throws(()=>r.plugins.api.exec('touch forbidden'),/unsupported/);assert.throws(()=>r.plugins.api.setActiveTools(['todo']),/ceiling/);
  const terminal=new Terminal(),ui=interactive(r,{terminal,poll:false});await until(()=>terminal.started);await until(()=>terminal.output.includes('disabled by host read-only/task ceiling'));terminal.input('\x04');await ui;
  await r.close();await assert.rejects(openRuntime({...f.launch,identity:{...f.launch.identity,plugin_source:'0'.repeat(64)}},{models:f.models,fence:async()=>{}}),/identity mismatch/);
  assert.throws(()=>validateStore({...f.launch,identity:{...f.launch.identity,plugin_contract:'0'.repeat(64)}}),/identity mismatch/);
});

test('WT public-export adapter matches disposable published 0.87.1 rendering including actual syntax engine, and rejects external execution',async()=>{
  const golden=JSON.parse(await readFile(new URL('./test-fixtures/public-sdk-rendering-0.87.1.json',import.meta.url),'utf8'));
  const p=loadPresentation(process.env.PI_CODING_AGENT_DIR,{trueColor:true});bindPluginTheme(p.theme);const md=getMarkdownTheme();
  assert.equal(golden.sdk,'0.87.1');assert.equal(golden.highlight,'10.7.3');
  for(const[name,expected]of Object.entries(golden.md))assert.equal(md[name]('sample'),expected,name);
  for(const sample of golden.codes)assert.deepEqual(md.highlightCode(sample.code,sample.lang),sample.lines,sample.lang);
  assert.deepEqual([0,1,12].map(width=>new DynamicBorder(text=>`<${text}>`).render(width)),golden.borders);
  assert.throws(()=>SettingsManager.create(),/does not support external-editor/);
});

for(const boundary of ['pending','answered','candidate','receipted'])test(`actual SIGKILL plugin ${boundary}: committed receipt versus uncertainty without tool/dialog/model replay`,{timeout:20000},async t=>{
  const f=await fixture(t,[]),file=join(f.launch.identity.cwd,'launch.json');await writeFile(file,JSON.stringify(f.launch));
  const child=fork(new URL('./plugin-crash-worker.mjs',import.meta.url),[file,boundary],{stdio:['ignore','pipe','pipe','ipc']});let stderr='';child.stderr.on('data',data=>stderr+=data);child.stdout.on('data',()=>{});
  t.after(()=>{if(child.exitCode===null)child.kill('SIGKILL');});
  const [message]=await Promise.race([once(child,'message'),once(child,'exit').then(()=>{throw new Error(stderr||'Crash worker exited before boundary');})]);assert.equal(message.stage,boundary);
  child.kill('SIGKILL');const[,signal]=await once(child,'exit');assert.equal(signal,'SIGKILL');
  const before=await readFile(`${f.launch.identity.store}.model-calls`,'utf8');
  const invocations=[],r=await f.open({pluginObserver:event=>invocations.push(event)}),doc=await r.harness.snapshot(PluginDoc,r.root.id,context);assert.equal(doc.calls['crash-call'].state,boundary);
  if(boundary!=='receipted'){
   assert.equal(r.plugins.recoveryHeld,true);assert.equal(r.plugins.inspection.uncertainRecords[0].state,'uncertain');assert.deepEqual(invocations,[],'ZERO actual module/factory/lifecycle invocations permitted');
   await r.resume();await assert.rejects(r.input('must not admit new work'),/read-only inspection/);await delay(80);assert.equal(f.faux.state.callCount,0);
   const terminal=new Terminal(),ui=interactive(r,{terminal,poll:false});await until(()=>terminal.started);await until(()=>terminal.output.includes('read-only plugin recovery'));terminal.type('/plugin-inspect');terminal.input('\r');await until(()=>terminal.output.includes('uncertainRecords'));terminal.input('\x04');await ui;
   assert.deepEqual(invocations,[],'inspection/cleanup must not load any plugin');
  }
  else {
   assert.equal(r.plugins.recoveryHeld,false);assert.equal(r.continuationHeld,true);assert.ok(r.interruptedRun.taskId);assert.equal(invocations.filter(event=>event.stage==='factory').length,2);assert.equal(invocations.filter(event=>event.stage==='module').length,2);
   await r.resume();await assert.rejects(r.input('old queued identity','followUp','retained-queued-input'),/continuation unavailable/);await assert.rejects(r.input('new input','followUp','new-control-not-authorized'),/continuation unavailable/);
   const terminal=new Terminal(),transports=[],ui=interactive(r,{terminal,poll:true,commandTransport:async args=>{transports.push(args);return[];}});await until(()=>terminal.started);await until(()=>terminal.output.includes('human-authorized continuation'));await until(()=>terminal.output.includes('CRASH_TODO'));terminal.type('/todos');terminal.input('\r');await until(()=>r.plugins.notice.includes('CRASH_TODO'));await delay(1150);assert.deepEqual(transports,[]);assert.equal(f.faux.state.callCount,0);terminal.input('\x04');await ui;
  }
  assert.equal(await readFile(`${f.launch.identity.store}.model-calls`,'utf8'),before,'reopen must not rerun original package/provider');
  if(boundary==='answered')assert.equal(doc.calls['crash-call'].answer.answers[0].answer,'Alpha');
  if(boundary==='candidate')assert.equal(doc.calls['crash-call'].candidate.details.tasks[0].subject,'CRASH_TODO');
  assert.equal(r.launch.identity.uuid,f.launch.identity.uuid);await r.close();
});

test('actual custom-answer editor rejects external-editor path and retains draft/focus without native execution',async t=>{
 const f=await fixture(t,[fauxAssistantMessage(fauxToolCall('ask_user_question',question,{id:'custom-question'}),{stopReason:'toolUse'}),fauxAssistantMessage('CUSTOM_DONE')]);
 const r=await f.open(),terminal=new Terminal(),ui=interactive(r,{terminal,poll:false});await until(()=>terminal.started);terminal.type('custom question');terminal.input('\r');await until(()=>terminal.output.includes('Which actual option?'));
 terminal.input('\x1b[B');terminal.input('\x1b[B');await delay(80);terminal.type('GENUINE_CUSTOM_DRAFT');terminal.input('\x07');
 await until(()=>r.plugins.notice.includes('does not support external-editor'));terminal.input('\r');await until(()=>terminal.output.includes('CUSTOM_DONE'));await r.root.waitForIdle(context);await r.plugins.update();
 const doc=await r.harness.snapshot(PluginDoc,r.root.id,context);assert.equal(doc.calls['custom-question'].candidate.details.answers[0].answer,'GENUINE_CUSTOM_DRAFT');assert.equal(doc.calls['custom-question'].candidate.details.answers[0].kind,'custom');terminal.input('\x04');await ui;
});

test('actual schema/reducer errors are terminal errors, not mutations or accepted success; configured identity changes reject reopen',async t=>{
 const f=await fixture(t,[todo('nonsense',{},'bad-schema'),fauxAssistantMessage('SCHEMA_DONE'),todo('update',{id:999,status:'completed'},'bad-reducer'),fauxAssistantMessage('REDUCER_DONE')]);const r=await f.open();
 const first=await r.input('bad schema');await first.wait(context);assert.equal((await r.harness.snapshot(PluginDoc,r.root.id,context))?.calls['bad-schema'],undefined);
 const second=await r.input('bad reducer');await second.wait(context);await r.plugins.update();const view=await r.root.context(context),result=view.entries.flatMap(entry=>entry.model||[]).find(message=>message.toolCallId==='bad-reducer');assert.equal(result.isError,false);assert.match(result.details.error,/999/);
 const doc=await r.harness.snapshot(PluginDoc,r.root.id,context);assert.equal(doc.calls['bad-reducer'].state,'failed');assert.equal(doc.calls['bad-reducer'].candidate.details.tasks.length,0);await r.close();
 const configDir=join(process.env.XDG_CONFIG_HOME,'rpiv-todo');await mkdir(configDir,{recursive:true});const config=join(configDir,'config.json');await writeFile(config,'{"maxWidgetLines":4}');t.after(()=>rm(config,{force:true}));
 await assert.rejects(f.open(),/fingerprint mismatch/);await rm(config);const reopened=await f.open();assert.equal(reopened.launch.identity.uuid,f.launch.identity.uuid);
});

test('disposable actual-package module/factory/lifecycle sentinels remain ZERO on uncertain reopen, including UI inspection and attempted automatic poll', {timeout:30000},async t=>{
 const f=await fixture(t,[]),root=join(f.launch.identity.cwd,'source'),dir=join(root,'runtime','pi-durable'),original=dirname(fileURLToPath(import.meta.url));await mkdir(dir,{recursive:true});
 for(const file of await readdir(original))if(file.endsWith('.mjs'))await cp(join(original,file),join(dir,file));await cp(join(original,'themes'),join(dir,'themes'),{recursive:true});await mkdir(join(root,'config'),{recursive:true});await symlink(join(original,'../../config/pi-wt'),join(root,'config/pi-wt'));
 const deps=join(dir,'node_modules');await mkdir(deps);for(const name of await readdir(join(original,'node_modules'))){if(name.startsWith('.'))continue;if(['@juicesharp','highlight.js','typebox','jiti'].includes(name))await cp(join(original,'node_modules',name),join(deps,name),{recursive:true});else await symlink(join(original,'node_modules',name),join(deps,name));}
 const sentinel=join(f.launch.identity.cwd,'actual-factory-sentinel.log');
 for(const name of ['rpiv-ask-user-question','rpiv-todo']){
  const path=join(deps,'@juicesharp',name,'index.ts'),source=await readFile(path,'utf8');
  const prefix=`import { appendFileSync as wtSentinel } from 'node:fs';\nconst wtMark=(kind:string)=>wtSentinel(${JSON.stringify(sentinel)},kind+'\\n');\nwtMark('module:${name}');\n`;
  const marker=name==='rpiv-todo'?'export default function (pi: ExtensionAPI, importOverlay: TodoOverlayImporter = () => import("./todo-overlay.js")) {':'export default function (pi: ExtensionAPI) {';
  assert.ok(source.includes(marker));await writeFile(path,prefix+source.replace(marker,marker+`\nwtMark('factory:${name}');\nconst wtOn=pi.on.bind(pi);pi={...pi,on:(event,handler)=>wtOn(event,(...args)=>{wtMark('lifecycle:'+event);return handler(...args);})};\n`));
 }
 // Instrumented source exists ONLY in this disposable private copy; original pinned files stay unchanged.
 const copied=await import(pathToFileURL(join(dir,'runtime.mjs'))),launch={...f.launch,identity:{...f.launch.identity,store:join(f.launch.identity.cwd,'sentinel.sqlite'),initialized:false}};delete launch.identity.plugin_source;delete launch.identity.plugin_contract;
 const bootstrap=await copied.openRuntime(launch,{bootstrap:true,models:f.models,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{}});await bootstrap.close();launch.identity.initialized=true;
 assert.match(await readFile(sentinel,'utf8'),/module:rpiv-todo/);assert.match(await readFile(sentinel,'utf8'),/factory:rpiv-ask/);assert.match(await readFile(sentinel,'utf8'),/lifecycle:session_start/);
 const file=join(f.launch.identity.cwd,'sentinel-launch.json');await writeFile(file,JSON.stringify(launch));const child=fork(join(dir,'plugin-crash-worker.mjs'),[file,'candidate'],{stdio:['ignore','ignore','pipe','ipc']});let error='';child.stderr.on('data',data=>error+=data);t.after(()=>{if(child.exitCode===null)child.kill('SIGKILL');});
 await Promise.race([once(child,'message'),once(child,'exit').then(()=>{throw new Error(error);})]);child.kill('SIGKILL');await once(child,'exit');await writeFile(sentinel,'');
 const r=await copied.openRuntime(launch,{models:f.models,fence:async()=>{},reportStatus:async()=>{}}),terminal=new Terminal(),commands=[];assert.equal(r.plugins.recoveryHeld,true);
 const ui=interactive(r,{terminal,poll:true,commandTransport:async args=>{commands.push(args);return[];}});await until(()=>terminal.started);await until(()=>terminal.output.includes('read-only plugin recovery'));terminal.type('/plugin-inspect');terminal.input('\r');await until(()=>terminal.output.includes('CRASH_TODO'));await delay(1150);assert.deepEqual(commands,[],'no inbox/job poll admission in recovery');
 await assert.rejects(r.input('new work'),/read-only inspection/);assert.equal(await readFile(sentinel,'utf8'),'');terminal.input('\x04');await ui;assert.equal(await readFile(sentinel,'utf8'),'','NO module/factory/lifecycle even during inspection/close');assert.equal(f.faux.state.callCount,0);
 const manifest=join(deps,'@juicesharp/rpiv-todo/package.json'),manifestText=await readFile(manifest,'utf8');await writeFile(manifest,manifestText.replace('"version": "2.9.0"','"version": "2.9.1"'));
 await assert.rejects(copied.openRuntime(launch,{models:f.models,fence:async()=>{}}),/dependency mismatch/);await writeFile(manifest,manifestText);
 const packageDir=join(deps,'@juicesharp/rpiv-todo');await rename(packageDir,`${packageDir}.missing`);await assert.rejects(copied.openRuntime(launch,{models:f.models,fence:async()=>{}}),/ENOENT/);await rename(`${packageDir}.missing`,packageDir);
 assert.equal(await readFile(sentinel,'utf8'),'','changed/missing packages fail before actual module/factory execution');
 const cleanLaunch={...launch,identity:{...launch.identity,uuid:randomBytes(16).toString('hex'),store:join(f.launch.identity.cwd,'clean-receipt.sqlite'),initialized:false}};delete cleanLaunch.identity.plugin_source;delete cleanLaunch.identity.plugin_contract;
 const cleanBootstrap=await copied.openRuntime(cleanLaunch,{bootstrap:true,models:f.models,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{}});await cleanBootstrap.close();cleanLaunch.identity.initialized=true;
 const cleanFile=join(f.launch.identity.cwd,'clean-launch.json');await writeFile(cleanFile,JSON.stringify(cleanLaunch));const cleanChild=fork(join(dir,'plugin-crash-worker.mjs'),[cleanFile,'receipted'],{stdio:['ignore','ignore','pipe','ipc']});let cleanError='';cleanChild.stderr.on('data',data=>cleanError+=data);t.after(()=>{if(cleanChild.exitCode===null)cleanChild.kill('SIGKILL');});await Promise.race([once(cleanChild,'message'),once(cleanChild,'exit').then(()=>{throw new Error(cleanError);})]);cleanChild.kill('SIGKILL');await once(cleanChild,'exit');await writeFile(sentinel,'');
 const clean=await copied.openRuntime(cleanLaunch,{models:f.models,fence:async()=>{},reportStatus:async()=>{}});assert.equal(clean.plugins.recoveryHeld,false);assert.equal(clean.continuationHeld,true);assert.match(await readFile(sentinel,'utf8'),/factory:rpiv-todo/);
 await clean.resume();const cleanTerminal=new Terminal(),cleanTransports=[],cleanUI=interactive(clean,{terminal:cleanTerminal,poll:true,commandTransport:async args=>{cleanTransports.push(args);return[];}});await until(()=>cleanTerminal.started);await until(()=>cleanTerminal.output.includes('CRASH_TODO'));await until(()=>cleanTerminal.output.includes('human-authorized continuation'));await delay(1150);assert.deepEqual(cleanTransports,[]);assert.equal(f.faux.state.callCount,0);assert.equal((await clean.plugins.uncertain()).length,0);await assert.rejects(clean.input('retained','followUp','retained-queued-input'),/continuation unavailable/);cleanTerminal.input('\x04');await cleanUI;assert.equal(f.faux.state.callCount,0,'clean initialization, UI, lifecycle, poll and close must NOT restart provider');
});

test('v2 delegation ceiling cannot offer or activate plugins; oversized arguments fail before package mutations',async t=>{
 const job=await fixture(t,[],{job:'private-job',role:'writer',tools:['read','write','bash','wt_workspace']});const child=await job.open();assert.ok(!(await child.root.agent(context)).tools.some(tool=>['todo','ask_user_question'].includes(tool.name)));assert.throws(()=>child.plugins.api.setActiveTools(['todo']),/ceiling/);await child.close();
 const f=await fixture(t,[todo('create',{subject:'OVERSIZED_DENIED',description:'x'.repeat(65537)},'oversized-call'),fauxAssistantMessage('BOUNDED_DONE')]);const r=await f.open();const input=await r.input('oversized arguments');await input.wait(context);
 assert.equal((await r.harness.snapshot(PluginDoc,r.root.id,context))?.calls['oversized-call'],undefined);const view=await r.root.context(context),result=view.entries.flatMap(entry=>entry.model||[]).find(message=>message.toolCallId==='oversized-call');assert.equal(result.isError,true);assert.match(result.content[0].text,/argument limit/);
});

test('reconciled v2 bootstrap honors configured thinking defaults and reopen preserves conversation-local thinking',async t=>{
  const dir=await realpath(await mkdtemp(join(tmpdir(),'wt-plugin-v2-thinking-')));t.after(()=>rm(dir,{recursive:true,force:true}));
  const settings=join(dir,'settings.json');
  await writeFile(settings,JSON.stringify({defaultThinkingLevel:'low',modelThinkingLevels:{'faux/faux-1':'high'}}));
  const models=createModels(),faux=fauxProvider({models:[{id:'faux-1',reasoning:true}]});models.setProvider(faux.provider);
  const launch={root:'test-root',agent:'test-agent',runtime:'test-runtime',identity:{store:join(dir,'session.sqlite'),uuid:randomBytes(16).toString('hex'),conversation:1,definition:pluginDefinition,dependencies:pluginDependencies,profile:pluginProfile,cwd:dir,initialized:false}};
  const options={models,agentDir:dir,fence:async()=>{}};
  const bootstrap=await openRuntime(launch,{...options,bootstrap:true,model:{provider:'faux',modelId:'faux-1'}});
  try { assert.equal((await bootstrap.root.agent(context)).thinkingLevel,'high'); }
  finally { await bootstrap.close(); }
  launch.identity.initialized=true;
  const runtime=await openRuntime(launch,options);
  try { await runtime.root.configure({thinkingLevel:'low'},context); }
  finally { await runtime.close(); }
  await writeFile(settings,'broken JSON');
  const reopened=await openRuntime(launch,options);
  try { assert.equal((await reopened.root.agent(context)).thinkingLevel,'low');assert.equal(faux.state.callCount,0); }
  finally { await reopened.close(); }
});
