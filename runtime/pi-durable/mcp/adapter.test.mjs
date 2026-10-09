import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import { loadHostManagedAdapter } from './loader.mjs';
import { privateMcpServer } from './fixture.mjs';

async function fixture(t) {
  const remote=await privateMcpServer();t.after(()=>remote.close());
  const factory=await loadHostManagedAdapter(),records=[];
  let decision='abstain';
  const adapter=factory({servers:{private:{tools:['echo'],createTransport:()=>new StreamableHTTPClientTransport(remote.url,{reconnectionOptions:{maxRetries:0}})}},requestTimeoutMs:1000,
    onToolCall:async call=>{
      assert.ok(Object.isFrozen(call.arguments));
      const record={id:call.toolCallId,connection:call.connectionId,args:call.arguments,state:'intent'};records.push(record);
      const result=await call.dispatch();
      assert.throws(()=>call.dispatch(),/already called/);record.state='result';return result;
    },
  });t.after(()=>adapter.close());
  const tools=[],api={registerTool:tool=>tools.push(tool),events:{emit:(name,request)=>{
    assert.equal(name,'pi-mcp-adapter:tool-approval-request');const current=decision;
    if(current!=='abstain')assert.equal(request.claim(()=>current),true);
  }}};
  assert.throws(()=>adapter.extensionFactory(api),/not ready/);
  await adapter.ready();adapter.extensionFactory(api);assert.equal(tools.length,1);
  return {remote,adapter,tool:tools[0],records,api,allow:value=>{decision=value;}};
}

test('actual host-managed adapter discovery, schema and broker refusals; no native SDK in closure',async t=>{
  const f=await fixture(t);
  assert.match(f.tool.name,/echo/);assert.deepEqual(f.tool.parameters.required,['text']);
  const manifest=JSON.parse(await readFile(new URL('./package-lock.json',import.meta.url),'utf8'));
  assert.ok(!Object.keys(manifest.packages).some(path=>path.includes('pi-coding-agent')||path.includes('pi-agent-core')));
  assert.equal(existsSync(new URL('./node_modules/@earendil-works/pi-coding-agent',import.meta.url)),false);
  let result=await f.tool.execute('missing-broker',{text:'must-not-run'});
  assert.equal(result.details.error,'approval_required');assert.equal(f.remote.calls.length,0);assert.equal(f.records.length,0);
  f.allow('deny');result=await f.tool.execute('denied',{text:'must-not-run'});
  assert.equal(result.details.error,'approval_denied');assert.equal(f.remote.calls.length,0);
});

test('actual MCP call, error envelope, pre-abort, single-dispatch and idempotent teardown',async t=>{
  const f=await fixture(t);f.allow('allow_once');
  let result=await f.tool.execute('allowed',{text:'actual'});
  assert.match(JSON.stringify(result),/PRIVATE_ECHO:actual/);assert.equal(f.remote.calls.length,1);
  assert.equal(f.records[0].state,'result');
  result=await f.tool.execute('error-result',{text:'failure'});
  assert.equal(result.details.error,'tool_error');assert.equal(f.remote.calls.length,2);
  const abort=new AbortController();abort.abort();
  result=await f.tool.execute('pre-abort',{text:'must-not-run'},abort.signal);
  assert.ok(result.details.error);assert.equal(f.remote.calls.length,2);
  await f.adapter.close();await f.adapter.close();assert.throws(()=>f.adapter.extensionFactory(f.api),/closed/);
  result=await f.tool.execute('closed',{text:'must-not-run'});assert.ok(result.details.error);assert.equal(f.remote.calls.length,2);
});

test('actual adapter marks a disconnect after remote effect may_have_run and does not retry it',async t=>{
  const f=await fixture(t);f.allow('allow_once');f.remote.setMode('drop');
  const result=await f.tool.execute('ambiguous',{text:'remote-effect'});
  assert.equal(result.details.error,'may_have_run');assert.equal(f.remote.calls.length,1);
  assert.equal(f.records[0].state,'intent');
  assert.match(JSON.stringify(result),/Do not retry automatically/);
});

test('actual output guard uses narrow public bridge, bounds text and stores spill only in private TMPDIR',async t=>{
  const f=await fixture(t);f.allow('allow_once');
  const result=await f.tool.execute('large',{text:'é'.repeat(30000)});
  assert.equal(result.details.outputGuard.truncated,true);
  assert.ok(Buffer.byteLength(result.content[0].text)<=50*1024);
  const path=result.details.outputGuard.fullOutputPath;
  assert.ok(path.startsWith(process.env.TMPDIR+'/'));
  assert.match(await readFile(path,'utf8'),/PRIVATE_ECHO/);
});
