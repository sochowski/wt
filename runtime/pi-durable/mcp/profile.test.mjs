import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp,realpath,writeFile,readFile,rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { randomBytes,createHash } from 'node:crypto';
import { createModels } from '@earendil-works/pi-ai';
import { fauxProvider,fauxAssistantMessage,fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { openRuntime,context,validateStore } from '../runtime.mjs';
import { interactive } from '../tui.mjs';
import { inboxPump } from '../inbox.mjs';
import { jobPump } from '../jobs.mjs';
import { McpCallsDoc } from './store.mjs';
import { mcpDefinition,mcpDependencies } from './contract.mjs';
import { privateMcpServer } from './fixture.mjs';
class Terminal {
 columns=100;rows=30;kittyProtocolActive=false;output='';
 start(input,resize){this.input=input;this.resize=resize;this.started=true;}stop(){this.stopped=true;}
 write(s){this.output+=s;}hideCursor(){}showCursor(){}moveBy(){}clearLine(){}clearFromCursor(){}clearScreen(){}setTitle(){}setProgress(){}async drainInput(){}
 type(text){for(const char of text)this.input(char);}
}
async function until(fn){for(let i=0;i<1000;i++){if(await fn())return;await new Promise(r=>setTimeout(r,10));}throw new Error('Private MCP profile deadline');}
async function fixture(t){
 const dir=await realpath(await mkdtemp(join(tmpdir(),'wt-mcp-profile-')));t.after(()=>rm(dir,{recursive:true,force:true}));
 const remote=await privateMcpServer();t.after(()=>remote.close());
 const file=join(dir,'mcp.json'),config={servers:[{name:'private',url:remote.url.toString(),tools:['echo']}]},bytes=JSON.stringify(config);await writeFile(file,bytes);
 const launch={root:'private-root',agent:'private-agent',runtime:'private-runtime',identity:{store:join(dir,'session.sqlite'),uuid:randomBytes(16).toString('hex'),conversation:1,profile:'mcp-boundary-v1',definition:mcpDefinition,dependencies:mcpDependencies,cwd:dir,writer_lock:join(dir,'writer.lock'),read_only:false,initialized:false,mcp_config_file:file,mcp_config_digest:createHash('sha256').update(bytes).digest('hex'),mcp_config:config,mcp_tools:['private_echo']}};
 const models=createModels(),faux=fauxProvider();models.setProvider(faux.provider);
 const options={models,agentDir:dir,fence:async()=>{},reportStatus:async()=>{}};
 const bootstrap=await openRuntime(launch,{...options,bootstrap:true,model:{provider:'faux',modelId:'faux-1'}});await bootstrap.close();launch.identity.initialized=true;
 const open=async extra=>{const runtime=await openRuntime(launch,{...options,...extra});t.after(()=>runtime.close());return runtime;};
 return {dir,remote,file,launch,models,faux,open};
}
test('real profile bootstrap seals WT identity and catalog; TUI allow-once reaches actual remote tool',async t=>{
 const f=await fixture(t);assert.equal(f.launch.identity.mcp_source.length,64);assert.equal(f.launch.identity.mcp_contract.length,64);validateStore(f.launch);
 f.faux.setResponses([fauxAssistantMessage(fauxToolCall('private_echo',{text:'ACTUAL_PROFILE_CALL'},{id:'ui-approved'}),{stopReason:'toolUse'}),fauxAssistantMessage('MCP_PROFILE_DONE')]);
 const runtime=await f.open(),terminal=new Terminal(),ui=interactive(runtime,{terminal,poll:false});await until(()=>terminal.started);
 terminal.type('call the configured tool');terminal.input('\r');await until(()=>terminal.output.includes('Approve MCP private/echo once?'));
 assert.equal(f.remote.calls.length,0);terminal.input('\x1b[B');terminal.input('\r');
 await until(()=>terminal.output.includes('MCP_PROFILE_DONE'));assert.equal(f.remote.calls.length,1);
 await runtime.root.waitForIdle(context);const doc=await runtime.harness.snapshot(McpCallsDoc,1,context);assert.equal(doc.calls['ui-approved'].candidate.isError,undefined);
 terminal.type('/mcp describe private_echo');terminal.input('\r');await until(()=>terminal.output.includes('Private synthetic echo'));
 terminal.type('/quit');terminal.input('\r');await ui;assert.equal(terminal.stopped,true);
});
test('TUI default denial/cancel produces no remote call, and config change blocks before discovery',async t=>{
 const f=await fixture(t);f.faux.setResponses([fauxAssistantMessage(fauxToolCall('private_echo',{text:'DENIED'},{id:'denied'}),{stopReason:'toolUse'}),fauxAssistantMessage('DENIAL_DONE')]);
 const runtime=await f.open(),terminal=new Terminal(),ui=interactive(runtime,{terminal,poll:false});await until(()=>terminal.started);terminal.type('request');terminal.input('\r');await until(()=>terminal.output.includes('Approve MCP'));terminal.input('\x1b');await until(()=>terminal.output.includes('DENIAL_DONE'));assert.equal(f.remote.calls.length,0);terminal.type('/quit');terminal.input('\r');await ui;
 const requests=f.remote.requests.length;await writeFile(f.file,(await readFile(f.file,'utf8'))+'\n');await assert.rejects(f.open(),/config file changed/);assert.equal(f.remote.requests.length,requests);
});
test('ambiguous live MCP blocks next provider and held reopen UI/input/model/inbox/job effects',async t=>{
 const f=await fixture(t);f.remote.setMode('drop');f.faux.setResponses([fauxAssistantMessage(fauxToolCall('private_echo',{text:'AMBIGUOUS'},{id:'uncertain'}),{stopReason:'toolUse'}),fauxAssistantMessage('MUST_NOT_RUN')]);
 let runtime=await f.open({mcpApprove:async()=> 'allow_once'});const submission=await runtime.input('effect');await submission.wait(context);assert.equal(f.faux.state.callCount,1);assert.equal(f.remote.calls.length,1);assert.equal(runtime.mcp.held,true);await runtime.close();
 const requests=f.remote.requests.length;runtime=await f.open();assert.equal(runtime.mcp.held,true);assert.equal(f.remote.requests.length,requests);await runtime.resume();await assert.rejects(runtime.input('new work'),/Held/);await assert.rejects(runtime.abort(),/Held/);
 const commands=[];await inboxPump(runtime,async a=>commands.push(a)).poll();await jobPump(runtime,async a=>commands.push(a)).poll();assert.deepEqual(commands,[]);assert.equal(f.faux.state.callCount,1);
 const terminal=new Terminal(),ui=interactive(runtime,{terminal,poll:true,commandTransport:async a=>{commands.push(a);return{};}});await until(()=>terminal.started);await until(()=>terminal.output.includes('read-only recovery'));terminal.type('/thinking high');terminal.input('\r');await new Promise(r=>setTimeout(r,50));assert.equal((await runtime.root.agent(context)).thinkingLevel,'off');terminal.type('/mcp-inspect');terminal.input('\r');await new Promise(r=>setTimeout(r,1200));assert.deepEqual(commands,[]);assert.equal(f.remote.requests.length,requests);terminal.type('/quit');terminal.input('\r');await ui;
});
