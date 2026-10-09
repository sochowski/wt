import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, realpath, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { randomBytes } from 'node:crypto';
import { fork } from 'node:child_process';
import { once } from 'node:events';
import { privateMcpServer } from './fixture.mjs';
import { rawRuntime } from './harness-fixture.mjs';
import { initializeMcpBoundary, preflightMcpBoundary, reopenMcpBoundary } from './boundary.mjs';

for(const stage of ['initialization-intent','admitted','dispatch-intent','remote-result','candidate','receipted'])test(`actual SIGKILL MCP ${stage}: committed receipt is distinct from candidates and unfinished continuation`,{timeout:30000},async t=>{
  const dir=await realpath(await mkdtemp(join(tmpdir(),'wt-mcp-crash-')));t.after(()=>rm(dir,{recursive:true,force:true}));
  const remote=await privateMcpServer();t.after(()=>remote.close());
  const launch={root:'private-crash-root',agent:'private-crash-agent',runtime:'private-crash-runtime',identity:{store:join(dir,'session.sqlite'),cwd:dir,uuid:randomBytes(16).toString('hex'),conversation:1,profile:'mcp-boundary-v1',read_only:false,tools:['private_echo']}};
  const config={servers:[{name:'private',url:remote.url.toString(),tools:['echo']}]};
  if(stage!=='initialization-intent'){
    const bootstrap=await rawRuntime(launch,[]);
    try {const boundary=await initializeMcpBoundary(bootstrap,{config});await boundary.close();}
    finally {await bootstrap.close();}
  }
  const input=join(dir,'launch.json');await writeFile(input,JSON.stringify({launch,config}));
  const child=fork(new URL('./crash-worker.mjs',import.meta.url),[input,stage],{stdio:['ignore','pipe','pipe','ipc']});let stderr='';
  child.stderr.on('data',chunk=>stderr+=chunk);child.stdout.on('data',()=>{});
  t.after(()=>{if(child.exitCode===null&&child.signalCode===null)child.kill('SIGKILL');});
  const [message]=await Promise.race([once(child,'message'),once(child,'exit').then(()=>{throw new Error(stderr||'Worker exited before checkpoint');})]);
  assert.equal(message.stage,stage);child.kill('SIGKILL');const[,signal]=await once(child,'exit');assert.equal(signal,'SIGKILL');
  const calls=remote.calls.length,requests=remote.requests.length;
  assert.equal(calls,['remote-result','candidate','receipted'].includes(stage)?1:0);
  const inspected=await preflightMcpBoundary(launch,config);assert.equal(inspected.held,true);
  if(stage==='initialization-intent')assert.equal(inspected.reason,'initialization-uncertain');
  else if(stage==='receipted'){
    assert.equal(inspected.uncertain.length,0);assert.equal(inspected.receipted.length,1);
    assert.ok(inspected.unfinishedRun);assert.equal(inspected.reason,'continuation-unavailable');
  }else{assert.equal(inspected.uncertain.length,1);assert.equal(inspected.receipted.length,0);}
  const effects=[];
  await assert.rejects(reopenMcpBoundary({launch,async fence(){throw new Error('must not invoke owner effects');}},{config,observer:event=>effects.push(event),approve:async()=>{throw new Error('must not approve retained work');}},inspected),/held store/);
  assert.deepEqual(effects,[]);assert.equal(remote.requests.length,requests);assert.equal(remote.calls.length,calls);
});
