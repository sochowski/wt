import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, realpath, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { randomBytes } from 'node:crypto';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { fauxAssistantMessage, fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { initializeMcpBoundary, preflightMcpBoundary, reopenMcpBoundary, normalizeConfig } from './boundary.mjs';
import { McpCallsDoc, McpContractDoc } from './store.mjs';
import { privateMcpServer } from './fixture.mjs';

import { rawRuntime } from './harness-fixture.mjs';

const response=(id='call')=>[fauxAssistantMessage(fauxToolCall('private_echo',{text:'ACTUAL_REMOTE_EFFECT'},{id}),{stopReason:'toolUse'}),fauxAssistantMessage('DONE')];
async function fixture(t, options={}){
  const dir=await realpath(await mkdtemp(join(tmpdir(),'wt-mcp-dispatch-')));t.after(()=>rm(dir,{recursive:true,force:true}));
  const remote=await privateMcpServer();t.after(()=>remote.close());
  const launch={root:'private-root',agent:'private-agent',runtime:'private-runtime',identity:{store:join(dir,'session.sqlite'),cwd:dir,uuid:randomBytes(16).toString('hex'),conversation:1,profile:'mcp-boundary-v1',read_only:false,tools:['private_echo']}};
  const config={servers:[{name:'private',url:remote.url.toString(),tools:['echo']}]};
  const runtime=await rawRuntime(launch,response()),events=[];t.after(()=>runtime.close());
  const boundary=await initializeMcpBoundary(runtime,{config,approve:async()=>options.approve??'allow_once',observer:event=>events.push(event)});t.after(()=>boundary.close());
  await runtime.install(boundary);
  return {dir,remote,launch,config,runtime,boundary,events};
}

test('actual adapter call has genuine Harness receipt; clean reopen preserves owner/catalog and does not redispatch',async t=>{
  const f=await fixture(t),submission=await f.runtime.root.submit({type:'input',content:'perform private fixture'},context);
  assert.equal((await submission.wait(context)).status,'done');assert.equal(f.remote.calls.length,1);
  const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context);
  assert.equal(doc.calls.call.phase,'candidate');assert.ok(doc.calls.call.remoteResultDigest);assert.ok(doc.calls.call.connectionId);
  await f.boundary.close();await f.runtime.close();
  const preflight=await preflightMcpBoundary(f.launch,f.config);
  assert.equal(preflight.held,false);assert.equal(preflight.receipted.length,1);assert.equal(preflight.receipted[0].failed,false);
  const runtime=await rawRuntime(f.launch,[]);t.after(()=>runtime.close());
  const boundary=await reopenMcpBoundary(runtime,{config:f.config,approve:async()=>{throw new Error('must not ask on reopen');}},preflight);t.after(()=>boundary.close());
  assert.equal(boundary.descriptor.contract,f.boundary.descriptor.contract);assert.equal(f.remote.calls.length,1);assert.equal(runtime.faux.state.callCount,0);
});

test('approval refusal is a failed authentic result, not a remote dispatch or accepted success',async t=>{
  const f=await fixture(t,{approve:'deny'}),submission=await f.runtime.root.submit({type:'input',content:'denied'},context);
  await submission.wait(context);assert.equal(f.remote.calls.length,0);
  const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context);assert.equal(doc.calls.call.candidate.isError,true);assert.equal(doc.calls.call.candidate.details.error,'approval_denied');assert.equal(doc.calls.call.connectionId,undefined);
  await f.boundary.close();await f.runtime.close();const preflight=await preflightMcpBoundary(f.launch,f.config);
  assert.equal(preflight.held,false);assert.equal(preflight.receipted[0].failed,true);
});

test('post-effect transport ambiguity blocks the next provider call and reopen before factories',async t=>{
  const f=await fixture(t);f.remote.setMode('drop');
  const submission=await f.runtime.root.submit({type:'input',content:'effect with dropped response'},context);
  await submission.wait(context);assert.equal(f.remote.calls.length,1);assert.equal(f.runtime.faux.state.callCount,1);
  const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context);
  assert.equal(doc.calls.call.candidate.details.error,'may_have_run');assert.equal(doc.calls.call.ambiguous,true);
  await f.boundary.close();await f.runtime.close();const before=f.remote.requests.length,preflight=await preflightMcpBoundary(f.launch,f.config);
  assert.equal(preflight.held,true);assert.equal(preflight.reason,'dispatch-uncertain');assert.equal(preflight.uncertain.length,1);assert.equal(f.remote.requests.length,before);
  const runtime=await rawRuntime(f.launch,[]);t.after(()=>runtime.close());const effects=[];
  await assert.rejects(reopenMcpBoundary(runtime,{config:f.config,observer:event=>effects.push(event)},preflight),/held store/);
  assert.deepEqual(effects,[]);assert.equal(f.remote.requests.length,before);assert.equal(runtime.faux.state.callCount,0);
});

test('source/config/owner/ceiling tampering fails preflight without transport effects; no v1/v2 adoption',async t=>{
  const f=await fixture(t);await f.boundary.close();await f.runtime.close();const requests=f.remote.requests.length;
  await assert.rejects(preflightMcpBoundary({...f.launch,agent:'other'},f.config),/immutable/);
  await assert.rejects(preflightMcpBoundary({...f.launch,identity:{...f.launch.identity,tools:[]}},f.config),/immutable/);
  await assert.rejects(preflightMcpBoundary(f.launch,{servers:[{...f.config.servers[0],url:'http://127.0.0.1:1/mcp'}]}),/immutable/);
  await assert.rejects(preflightMcpBoundary({...f.launch,identity:{...f.launch.identity,profile:'native-compat-v2'}},f.config),/fresh-profile/);
  assert.equal(f.remote.requests.length,requests);
  const runtime=await rawRuntime(f.launch,[]);t.after(()=>runtime.close());
  await runtime.root.commit(async tx=>{(await tx.doc(McpContractDoc)).source='0'.repeat(64);},context);await runtime.close();
  await assert.rejects(preflightMcpBoundary(f.launch,f.config),/immutable/);assert.equal(f.remote.requests.length,requests);
});

test('incompatible role/options refuse before any factory, credentials, transport or model effects',async t=>{
  const f=await fixture(t);const requests=f.remote.requests.length,events=[];
  await assert.rejects(initializeMcpBoundary({...f.runtime,launch:{...f.launch,identity:{...f.launch.identity,read_only:true}}},{config:f.config,observer:event=>events.push(event)}),/fresh-profile/);
  assert.throws(()=>normalizeConfig({servers:[{...f.config.servers[0],command:'unapproved process'}]}),/unsupported/);
  assert.throws(()=>normalizeConfig({servers:[{...f.config.servers[0],url:'http://user:secret@127.0.0.1/mcp'}]}),/authentication/);
  assert.deepEqual(events,[]);assert.equal(f.remote.requests.length,requests);assert.equal(f.runtime.faux.state.callCount,0);
});

test('journal arguments cannot be rehashed into a different request; public task and assistant admission stay authoritative',async t=>{
  const f=await fixture(t),submission=await f.runtime.root.submit({type:'input',content:'actual request'},context);await submission.wait(context);
  const {digest}=await import('./store.mjs');
  await f.runtime.root.commit(async tx=>{
    const record=(await tx.doc(McpCallsDoc,1)).calls.call;
    record.arguments.text='FORGED_REQUEST';record.argumentsDigest=digest(record.arguments);
  },context);
  await f.boundary.close();await f.runtime.close();const requests=f.remote.requests.length;
  await assert.rejects(preflightMcpBoundary(f.launch,f.config),/authoritative assistant/);
  assert.equal(f.remote.requests.length,requests);
});

test('rehashed candidate is not a tool receipt, and a forged task identity fails preflight',async t=>{
  const f=await fixture(t),submission=await f.runtime.root.submit({type:'input',content:'actual result'},context);await submission.wait(context);
  const {digest}=await import('./store.mjs');
  await f.runtime.root.commit(async tx=>{
    const record=(await tx.doc(McpCallsDoc,1)).calls.call;
    record.candidate.content[0].text='FORGED_RESULT';record.candidateDigest=digest(record.candidate);
  },context);
  await f.boundary.close();await f.runtime.close();
  const inspected=await preflightMcpBoundary(f.launch,f.config);assert.equal(inspected.held,true);assert.equal(inspected.receipted.length,0);
  const runtime=await rawRuntime(f.launch,[]);t.after(()=>runtime.close());
  await runtime.root.commit(async tx=>{(await tx.doc(McpCallsDoc,1)).calls.call.taskId=999999;},context);await runtime.close();
  await assert.rejects(preflightMcpBoundary(f.launch,f.config),/task\/call/);
});

test('ambiguous receipt cannot become safe by clearing its journal hint',async t=>{
  const f=await fixture(t);f.remote.setMode('drop');
  const submission=await f.runtime.root.submit({type:'input',content:'ambiguous'},context);await submission.wait(context);
  await f.runtime.root.commit(async tx=>{(await tx.doc(McpCallsDoc,1)).calls.call.ambiguous=false;},context);
  await f.boundary.close();await f.runtime.close();
  assert.equal((await preflightMcpBoundary(f.launch,f.config)).held,true);
});

test('stale runtime hook cannot silently allow a provider request',async t=>{
  const f=await fixture(t);f.runtime.fence=async()=>{throw new Error('stale private owner');};
  const submission=await f.runtime.root.submit({type:'input',content:'must not call provider'},context);await submission.wait(context);
  assert.equal(f.runtime.faux.state.callCount,0);assert.equal(f.remote.calls.length,0);assert.equal(f.boundary.held,true);
});

test('a claimed MCP profile cannot adopt an existing WT v1/v2 application envelope',async t=>{
  const f=await fixture(t);await f.boundary.close();await f.runtime.close();
  const {DatabaseSync}=await import('node:sqlite');const db=new DatabaseSync(f.launch.identity.store);
  try {db.exec('CREATE TABLE wt_identity(singleton INTEGER PRIMARY KEY,value TEXT NOT NULL)');db.prepare('INSERT INTO wt_identity VALUES(1,?)').run(JSON.stringify({root:f.launch.root,agent:f.launch.agent,...f.launch.identity,profile:'native-compat-v2'}));}
  finally {db.close();}
  const requests=f.remote.requests.length;
  await assert.rejects(preflightMcpBoundary(f.launch,f.config),/different WT application profile/);assert.equal(f.remote.requests.length,requests);
});

test('duplicate unsafe call IDs and oversized arguments cannot dispatch another remote effect',async t=>{
  const f=await fixture(t);
  let submission=await f.runtime.root.submit({type:'input',content:'first admitted call'},context);await submission.wait(context);assert.equal(f.remote.calls.length,1);
  f.runtime.faux.setResponses(response());
  submission=await f.runtime.root.submit({type:'input',content:'duplicate ID'},context);await submission.wait(context);assert.equal(f.remote.calls.length,1);
  f.runtime.faux.setResponses([fauxAssistantMessage(fauxToolCall('private_echo',{text:'x'.repeat(17000)},{id:'oversized'}),{stopReason:'toolUse'}),fauxAssistantMessage('DONE')]);
  submission=await f.runtime.root.submit({type:'input',content:'oversized args'},context);await submission.wait(context);assert.equal(f.remote.calls.length,1);
  const doc=await f.runtime.harness.snapshot(McpCallsDoc,1,context);assert.deepEqual(Object.keys(doc.calls),['call']);
});
