// Explicit bounded REAL-provider proof through compiled WT, a private tmux and
// the production interactive entrypoint. Read-only host; auth is never modified.
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { realpathSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { openRuntime, context } from './runtime.mjs';

if(process.env.WT_DURABLE_REAL_SMOKE!=='1' || !process.env.WT_PI_AUTH_PATH)throw new Error('Opt-in and explicit read-only WT_PI_AUTH_PATH required');
const exec=promisify(execFile),source=fileURLToPath(new URL('../..',import.meta.url));
const auth=process.env.WT_PI_AUTH_PATH;
const provider=process.env.WT_DURABLE_SMOKE_PROVIDER || 'anthropic';
const model=process.env.WT_DURABLE_SMOKE_MODEL || 'claude-haiku-4-5-20251001';
const startupOnly=process.env.WT_DURABLE_STARTUP_ONLY==='1';
const digest=async()=>createHash('sha256').update(await readFile(auth)).digest('hex');
const before=await digest();
const home=realpathSync(await mkdtemp('/tmp/wt-durable-live-')),fixture=join(home,'fixture'),bin=join(home,'bin');
const binary=join(bin,'wt-state');const socket=`wt-durable-${process.pid}`;
const tmux=(await exec('which',['tmux'])).stdout.trim();
let launched,usage,phase='fixture',ownerStatus;
const started=Date.now(),checkpoints=[];
function checkpoint(status) { checkpoints.push({phase,elapsedMs:Date.now()-started,...status}); }
let env;
try{
  await mkdir(bin);await mkdir(fixture);await mkdir(join(home,'pi'));await mkdir(join(home,'state'));
  await writeFile(join(fixture,'input.txt'),'WT_DURABLE_INTERACTIVE_OK');
  await writeFile(join(home,'pi','settings.json'),JSON.stringify({defaultProvider:provider,defaultModel:model}));
  await writeFile(join(bin,'tmux'),`#!/bin/sh\nexec '${tmux}' -L '${socket}' "$@"\n`,{mode:0o700});
  await exec('go',['build','-o',binary,'.'],{cwd:join(source,'state'),timeout:60000});
  env={PATH:`${bin}:${process.env.PATH}`,HOME:home,TERM:'xterm-256color',LANG:'en_US.UTF-8',SHELL:'/bin/bash',TMPDIR:'/tmp',WT_DB:join(home,'state','wt.db'),WT_STATUS_DIR:join(home,'state'),WT_BASE_DIR:join(home,'worktrees'),WT_CONFIG_DIR:join(home,'config'),WT_LOG_FILE:join(home,'wt.log'),WT_SOURCE_CONFIG:join(source,'config'),WT_STATE:binary,PI_CODING_AGENT_DIR:join(home,'pi'),WT_PI_AUTH_PATH:auth,WT_DURABLE_SMOKE:'1'};
  const tm=async args=>(await exec(join(bin,'tmux'),args,{env,timeout:10000})).stdout.trim();
  const wt=async args=>JSON.parse((await exec(binary,['worktree',...args],{env,timeout:20000})).stdout);
  await tm(['-f','/dev/null','new-session','-d','-s','sandbox','-c',fixture]);
  phase='WT bootstrap/launch';
  launched=await wt(['new','durable-proof','--cwd',fixture,'--backend','durable','--read-only']);
  const view=launched.views.find(v=>v.kind==='agent');
  const pane=view.pane;
  const overallDeadline=started+90000;
  async function until(check,limit=15000){const deadline=Math.min(overallDeadline,Date.now()+limit);while(!await check()){if(Date.now()>deadline)throw new Error('bounded proof phase deadline');await delay(50);}}
  const statusPath=`${launched.agents[0].adapter.durable.store}.smoke-status.json`;
  async function observeOwner(){
    try {
      const value=JSON.parse(await readFile(statusPath,'utf8'));
      if(JSON.stringify(value)!==JSON.stringify(ownerStatus)) { ownerStatus=value; checkpoint({owner:value}); }
      return value;
    } catch { return undefined; }
  }
  phase='interactive startup';
  await until(async()=>{const capture=await tm(['capture-pane','-p','-t',pane]);if(capture.includes('launch failed'))throw new Error('production launch failed');return capture.includes('/help');});
  checkpoint({host:'interactive-started'});
  if(!startupOnly){
    phase='bounded provider input/committed settlement';
    await tm(['send-keys','-l','-t',pane,'Use read on input.txt, then reply with only its exact contents.']);await tm(['send-keys','-t',pane,'Enter']);
    await until(async()=>{
      const value=await observeOwner();
      return value?.submission?.status==='done' && !value.live?.run && !value.live?.generation && value.live?.tools===0 && value.tasks?.length===0;
    },60000);
    checkpoint({host:'committed-submission-done/no-live-work'});
  }
  phase='interactive close';
  await tm(['send-keys','-l','-t',pane,'/quit']);await tm(['send-keys','-t',pane,'Enter']);
  await until(async()=>{await observeOwner();return (await tm(['display-message','-p','-t',pane,'#{pane_dead}']))==='1';},15000);
  checkpoint({host:'owner-exited'});
  phase='settled store inspection';
  const a=await wt(['agents','show',launched.id,launched.agents[0].id]);
  // Owner has exited; nonexecuting real-package inspection in this private test.
  const inspect=await openRuntime({root:launched.id,agent:a.id,runtime:a.runtime,identity:a.adapter.durable},{fence:async()=>{}});
  try{
    const entries=(await inspect.root.entries({},100,undefined,context)).items;
    if(!startupOnly)assert.ok(entries.some(e=>e.kind==='pi.tool-result' && e.model?.[0]?.toolName==='read' && !e.model[0].isError));
    if(!startupOnly)assert.ok(entries.some(e=>e.kind==='pi.assistant' && e.model?.[0]?.content?.some(b=>b.text?.trim()==='WT_DURABLE_INTERACTIVE_OK')));
    usage=await inspect.harness.usage(context);
    assert.equal((await inspect.harness.inspect(context)).submissions.length,0);
  }finally{await inspect.close();}
  assert.equal(await digest(),before);
  console.log(JSON.stringify({status:'passed',provider,model,actualProductionWT:true,privateTmux:true,interactiveInputRenderClose:true,readOnlyHost:true,actualRead:!startupOnly,answer:startupOnly?null:'WT_DURABLE_INTERACTIVE_OK',authUnchanged:true,limits:{requests:3,maxOutputTokensPerRequest:256,requestSeconds:20,generationSeconds:60,closeSeconds:15,overallSeconds:90},checkpoints,usage},null,2));
}catch(error){
  if(phase==='WT bootstrap/launch')console.error(error.stderr || 'WT bootstrap JSON/command failure');
  // Do not log arbitrary provider error text, auth material or pane dumps.
  console.error(`Private WT real-provider proof failed at ${phase} (details suppressed; auth unchanged).`);
  console.log(JSON.stringify({status:'failed',provider,model,phase,authUnchanged:await digest()===before,checkpoints,ownerStatus,limits:{requests:3,maxOutputTokensPerRequest:256,requestSeconds:20,generationSeconds:60,closeSeconds:15,overallSeconds:90}},null,2));process.exitCode=1;
}finally{
  if(env)await exec(tmux,['-L',socket,'kill-server'],{env,timeout:10000}).catch(()=>{});
  assert.equal(await digest(),before);
  await rm(home,{recursive:true,force:true});
}
