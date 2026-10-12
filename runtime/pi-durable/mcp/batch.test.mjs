import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,realpath,rm} from 'node:fs/promises';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {randomBytes} from 'node:crypto';
import {context} from '../runtime.mjs';
import {fauxAssistantMessage,fauxToolCall} from '@earendil-works/pi-ai/providers/faux';
import {rawRuntime} from './harness-fixture.mjs';
import {privateMcpServer} from './fixture.mjs';
import {initializeMcpBoundary,preflightMcpBoundary} from './boundary.mjs';
import {McpCallsDoc,digest} from './store.mjs';
async function fixture(t,name,args,approval=()=> 'allow_once'){
 const cwd=await realpath(await mkdtemp(join(tmpdir(),'wt-mcp-batch-')));t.after(()=>rm(cwd,{recursive:true,force:true}));
 const remote=await privateMcpServer();t.after(()=>remote.close());
 const launch={root:'private-root',agent:'private-agent',runtime:'runtime',identity:{cwd,store:join(cwd,'store.sqlite'),uuid:randomBytes(16).toString('hex'),conversation:1,profile:'mcp-boundary-v1',read_only:false,mcp_tools:['private_echo','mcp','mcpScript']}};
 const config={servers:[{name:'private',url:remote.url.toString(),tools:['echo']}]};
 const runtime=await rawRuntime(launch,[fauxAssistantMessage(fauxToolCall(name,args,{id:'outer'}),{stopReason:'toolUse'}),fauxAssistantMessage('DONE')]);t.after(()=>runtime.close());
 const approvals=[];const boundary=await initializeMcpBoundary(runtime,{config,approve:async request=>{approvals.push(request);return approval(request);}});t.after(()=>boundary.close());await runtime.install(boundary);
 return {runtime,boundary,remote,launch,config,approvals};
}
test('real narrowed gateway calls actual adapter; query/describe do not bypass remote approval',async t=>{
 const f=await fixture(t,'mcp',{tool:'private_echo',args:{text:'GATEWAY_ACTUAL'}});
 await(await f.runtime.root.submit({type:'input',content:'gateway'},context)).wait(context);
 assert.equal(f.remote.calls.length,1);assert.equal(f.approvals.length,1);assert.equal(f.approvals[0].batch.tool,'mcp');
 const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context);assert.equal(doc.calls.outer.subcalls.length,1);assert.equal(doc.calls.outer.subcalls[0].phase,'candidate');
 await f.boundary.close();await f.runtime.close();const inspected=await preflightMcpBoundary(f.launch,f.config);assert.equal(inspected.held,false);assert.equal(inspected.receipted.length,1);
});
test('actual MCP QuickJS script chains selected tools; every subcall has immutable intent/result and allow-once',async t=>{
 const code='const found=await tools.search({query:"echo"}); const info=await tools.describe({path:found.items[0].path}); const a=await tools.call(info.path,{text:"FIRST"}); const b=await tools.call(info.path,{text:"SECOND"}); emit([a,b]);';
 const f=await fixture(t,'mcpScript',{code,timeoutMs:3000});await(await f.runtime.root.submit({type:'input',content:'script'},context)).wait(context);
 assert.equal(f.remote.calls.length,2);assert.equal(f.approvals.length,2);assert.deepEqual(f.approvals.map(a=>a.batch.index),[0,1]);
 const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context),call=doc.calls.outer;
 assert.equal(call.arguments.code,code);assert.equal(call.subcalls.length,2);assert.ok(call.subcalls.every(sub=>sub.phase==='candidate'&&sub.remoteResultDigest));assert.notEqual(call.subcalls[0].id,call.subcalls[1].id);assert.match(JSON.stringify(call.candidate),/FIRST/);
 await f.boundary.close();await f.runtime.close();assert.equal((await preflightMcpBoundary(f.launch,f.config)).held,false);
});
test('script denial is actual no-effect error and cannot activate an unlisted target',async t=>{
 const f=await fixture(t,'mcpScript',{code:'return await tools.call("private_echo",{text:"DENIED"});'},()=> 'deny');await(await f.runtime.root.submit({type:'input',content:'deny'},context)).wait(context);assert.equal(f.remote.calls.length,0);
 const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context);assert.equal(doc.calls.outer.subcalls[0].candidate.details.error,'approval_denied');
 const other=await fixture(t,'mcpScript',{code:'return await tools.call("unlisted",{});'});await(await other.runtime.root.submit({type:'input',content:'unlisted'},context)).wait(context);assert.equal(other.remote.calls.length,0);assert.equal(other.approvals.length,0);
});
test('partial script remote ambiguity blocks later calls/provider and retained replay, even with a matching outer failure receipt',async t=>{
 const f=await fixture(t,'mcpScript',{code:'await tools.call("private_echo",{text:"FIRST"}); await tools.call("private_echo",{text:"MUST_NOT_RUN"});',timeoutMs:3000});f.remote.setMode('drop');
 await(await f.runtime.root.submit({type:'input',content:'partial'},context)).wait(context);assert.equal(f.remote.calls.length,1);assert.equal(f.runtime.faux.state.callCount,1);assert.equal(f.boundary.held,true);
 const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context);assert.equal(doc.calls.outer.subcalls[0].ambiguous,true);
 await f.boundary.close();await f.runtime.close();const requests=f.remote.requests.length;assert.equal((await preflightMcpBoundary(f.launch,f.config)).held,true);assert.equal(f.remote.requests.length,requests);
});
test('subcall identity and argument digest cannot be forged in retained script journals',async t=>{
 const f=await fixture(t,'mcpScript',{code:'return await tools.call("private_echo",{text:"ACTUAL"});'});await(await f.runtime.root.submit({type:'input',content:'actual'},context)).wait(context);
 await f.runtime.root.commit(async tx=>{const sub=(await tx.doc(McpCallsDoc,1)).calls.outer.subcalls[0];sub.id='0'.repeat(64);},context);await f.boundary.close();await f.runtime.close();await assert.rejects(preflightMcpBoundary(f.launch,f.config),/subcall ID/);
});

test('rehashing subcall arguments/candidates cannot forge immutable dispatch/result witnesses',async t=>{
 const f=await fixture(t,'mcpScript',{code:'return await tools.call("private_echo",{text:"REAL"});'});await(await f.runtime.root.submit({type:'input',content:'actual'},context)).wait(context);
 await f.runtime.root.commit(async tx=>{const sub=(await tx.doc(McpCallsDoc,1)).calls.outer.subcalls[0];sub.arguments.text='FORGED';sub.argumentsDigest=digest(sub.arguments);},context);await f.boundary.close();await f.runtime.close();await assert.rejects(preflightMcpBoundary(f.launch,f.config),/immutable admission/);
 const other=await fixture(t,'mcpScript',{code:'return await tools.call("private_echo",{text:"REAL"});'});await(await other.runtime.root.submit({type:'input',content:'actual'},context)).wait(context);
 await other.runtime.root.commit(async tx=>{const sub=(await tx.doc(McpCallsDoc,1)).calls.outer.subcalls[0];sub.candidate.content[0].text='FORGED';sub.candidateDigest=digest(sub.candidate);},context);await other.boundary.close();await other.runtime.close();await assert.rejects(preflightMcpBoundary(other.launch,other.config),/immutable result/);
});
test('actual script remote-call budget and unawaited dispatch fail closed without further effects',async t=>{
 const f=await fixture(t,'mcpScript',{code:'for(let i=0;i<17;i++)await tools.call("private_echo",{text:String(i)});',timeoutMs:10000});await(await f.runtime.root.submit({type:'input',content:'budget'},context)).wait(context);assert.equal(f.remote.calls.length,16);
 const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context);assert.equal(doc.calls.outer.candidate.details.error,'script_failed');assert.equal(doc.calls.outer.subcalls.length,16);
 const other=await fixture(t,'mcpScript',{code:'tools.call("private_echo",{text:"unawaited"});return "finished";'},async()=>new Promise(resolve=>setTimeout(()=>resolve('allow_once'),200)));
 await(await other.runtime.root.submit({type:'input',content:'unawaited'},context)).wait(context);await new Promise(r=>setTimeout(r,300));assert.equal(other.remote.calls.length,0);
});
