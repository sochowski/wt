import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, symlink, writeFile, readFile, rm } from 'node:fs/promises';
import { realpathSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { execFile } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
const exec=promisify(execFile), source=fileURLToPath(new URL('../..',import.meta.url));

test('compiled WT/private tmux: independent writer/reviewer stores, SIGKILL publication recovery, parallel admission, budgets, peers and focus protections', {timeout:90000},async t=>{
  const home=realpathSync(await mkdtemp('/tmp/wt-durable-jobs-private-'));
  const bin=join(home,'bin'), config=join(home,'source','config'), runtime=join(home,'source','runtime','pi-durable');
  const socket=`wt-durable-jobs-${process.pid}`;const tmux=(await exec('which',['tmux'])).stdout.trim();
  let env, phase='fixture';
  t.after(async()=>{
    t.diagnostic(`Last private-fixture phase: ${phase}`);
    if(env && phase!=='completed') {
      const panes=await exec(tmux,['-L',socket,'list-panes','-a','-F','#{pane_id}'],{env,timeout:10000}).catch(()=>({stdout:''}));
      for(const pane of panes.stdout.trim().split('\n').filter(Boolean))t.diagnostic((await exec(tmux,['-L',socket,'capture-pane','-p','-S','-100','-t',pane],{env,timeout:10000}).catch(()=>({stdout:''}))).stdout);
    }
    if(env)await exec(tmux,['-L',socket,'kill-server'],{env,timeout:10000}).catch(()=>{});await rm(home,{recursive:true,force:true,maxRetries:10,retryDelay:100});
  });
  await mkdir(bin,{recursive:true});await mkdir(config,{recursive:true});await mkdir(runtime,{recursive:true});await mkdir(join(home,'state'));await mkdir(join(home,'pi'));
  for(const file of ['wt-view.lua','wt-present.lua','wt-stack.lua','wt-shell.lua'])if(existsSync(join(source,'config',file)))await symlink(join(source,'config',file),join(config,file));
  await symlink(join(source,'runtime','pi-durable','private-job-fixture.mjs'),join(runtime,'main.mjs'));
  await writeFile(join(bin,'tmux'),`#!/bin/sh\nexec '${tmux}' -L '${socket}' "$@"\n`,{mode:0o700});
  const binary=join(bin,'wt-state');await exec('go',['build','-o',binary,'.'],{cwd:join(source,'state'),timeout:60000});
  env={PATH:`${bin}:${process.env.PATH}`,HOME:home,TERM:'xterm-256color',LANG:'en_US.UTF-8',SHELL:'/bin/bash',TMPDIR:'/private/tmp',WT_DB:join(home,'state','wt.db'),WT_STATUS_DIR:join(home,'state'),WT_BASE_DIR:join(home,'worktrees'),WT_CONFIG_DIR:join(home,'config'),WT_LOG_FILE:join(home,'wt.log'),WT_SOURCE_CONFIG:config,WT_STATE:binary,PI_CODING_AGENT_DIR:join(home,'pi'),WT_FIXTURE_HOOK_TEMPLATES:join(source,'config'),WT_FIXTURE_FAULT:'publication'};
  const tm=async args=>(await exec(join(bin,'tmux'),args,{env,timeout:10000})).stdout.trim();
  const wt=async args=>{const out=await exec(binary,['worktree',...args],{env,timeout:20000});return out.stdout.trim()?JSON.parse(out.stdout):undefined;};
  const internal=async(op,body)=>new Promise((resolve,reject)=>{const child=execFile(binary,['worktree','_durable-jobs',op],{env,timeout:20000},(e,stdout)=>e?reject(e):resolve(JSON.parse(stdout)));child.stdin.end(JSON.stringify(body));});
  const dirs=[];
  for(const name of ['parent','a','b','c']) {
    const dir=join(home,name);await mkdir(dir);await writeFile(join(dir,'input.txt'),'before');await exec('git',['init',dir]);await exec('git',['-C',dir,'add','input.txt']);await exec('git',['-C',dir,'-c','user.name=Fixture','-c','user.email=fixture@invalid','commit','-m','fixture']);dirs.push(dir);
  }
  await tm(['-f','/dev/null','new-session','-d','-s','sandbox','-c',home]);
  let root=await wt(['new','durable-jobs-proof','--cwd',dirs[0],'--backend','durable']);
  const parent=root.agents[0],owner={root:root.id,parent:parent.id,runtime:parent.runtime};
  for(let i=1;i<dirs.length;i++)await wt(['checkout','attach',root.id,['a','b','c'][i-1],dirs[i]]);
  const parentView=root.views.find(v=>v.target===parent.id);
  const until=async(check)=>{const deadline=Date.now()+30000;while(Date.now()<deadline){if(await check())return;await delay(50);}throw new Error(`private bridge 30s deadline at ${phase}`);};
  await until(async()=>(await tm(['capture-pane','-p','-t',parentView.pane])).includes('/help'));
  await wt(['view','pin',root.id,parentView.id]);
  const activeBefore=await tm(['list-windows','-t',`=${root.name}`,'-F','#{window_id}:#{window_active}']);
  const request={version:1,operation:'process-kill-proof',task:'Change input.txt and verify',cwd:dirs[1],criteria:['Fixture changed and independently reviewed']};
  phase='single-child admission/recovery';
  let job=await internal('reserve',{...owner,request});
  let retry=await internal('reserve',{...owner,request});assert.equal(retry.child,job.child);assert.equal(retry.reviewer,job.reviewer);
  await internal('reconcile',{...owner,job:job.id});
  root=await wt(['agents','list',root.id]); // List is an array; fetch exact children independently.
  let writer=await wt(['agents','show',owner.root,job.child]);
  await until(()=>existsSync(`${writer.adapter.durable.store}.kill-ready`));
  const pid=Number(await readFile(`${writer.adapter.durable.store}.kill-ready`,'utf8'));assert.ok(pid>0);
  process.kill(pid,'SIGKILL');
  await until(async()=>{job=await internal('reconcile',{...owner,job:job.id});return job.state==='succeeded';});
  assert.equal(job.result.success,true);assert.equal(job.review.success,true);assert.equal(job.review.evidence.humanProvenance,'not-observed');
  assert.equal((await readFile(`${writer.adapter.durable.store}.calls`,'utf8')).trim().split('\n').length,3,'publication recovery spent provider calls twice');
  const reviewer=await wt(['agents','show',owner.root,job.reviewer]);
  assert.equal((await readFile(`${reviewer.adapter.durable.store}.calls`,'utf8')).trim().split('\n').length,2);
  assert.notEqual(writer.adapter.durable.uuid,reviewer.adapter.durable.uuid);assert.notEqual(writer.adapter.durable.store,reviewer.adapter.durable.store);
  assert.deepEqual(reviewer.adapter.durable.tools,['read','wt_workspace']);
  assert.equal(reviewer.adapter.durable.read_only,true);
  // Native-only admission cannot bind a durable parent, and a runtime token alone
  // cannot submit a forged host result. The dedicated FD capability is not env.
  await assert.rejects(internal('host',{root:owner.root,child:job.child,runtime:writer.runtime}));
  assert.equal((await wt(['roots'])).find(r=>r.id===owner.root).wake_budget,62,'crash recovery spent admission budget twice');
  await wt(['message','wake',owner.root,'--budget','62']); // Explicit sandbox-human authorization after crash pauses new wake admissions.
  env.WT_FIXTURE_FAULT='none';await tm(['set-environment','-g','WT_FIXTURE_FAULT','none']);
  phase='parallel independent writer/reviewer hosts';
  const jobs=[];
  for(let i=2;i<4;i++)jobs.push(await internal('reserve',{...owner,request:{...request,operation:`parallel-${i}`,cwd:dirs[i]}}));
  await Promise.all(jobs.map(j=>internal('reconcile',{...owner,job:j.id}).catch(async()=>internal('reconcile',{...owner,job:j.id}))));
  // Initial environment is sticky in tmux: remaining fault probes must also be
  // killed if the original session environment retains publication fault mode.
  const killed=new Set();
  await until(async()=>{
    for(let i=0;i<jobs.length;i++){
      const a=await wt(['agents','show',owner.root,jobs[i].child]);const marker=`${a.adapter.durable.store}.kill-ready`;
      if(existsSync(marker)&&!killed.has(marker)){killed.add(marker);process.kill(Number(await readFile(marker,'utf8')),'SIGKILL');}
      jobs[i]=await internal('reconcile',{...owner,job:jobs[i].id}).catch(()=>jobs[i]);
    }
    return jobs.every(j=>j.state==='succeeded');
  });
  await assert.rejects(internal('reserve',{...owner,request:{...request,operation:'over-retained-limit'}}));
  phase='independent peer/inbox/focus/stop';
  await wt(['message','wake',owner.root,'--budget','58']);
  await tm(['set-environment','-g','WT_FIXTURE_INBOX_FAULT','before-ack']);
  const peers=await wt(['agents','create',owner.root,'independent-peer','--parent',owner.parent,'--cwd','root','--backend','durable','--read-only']);
  const views=await wt(['view','list',owner.root]);const peerView=views.find(v=>v.target===peers.id);
  await until(async()=>(await tm(['capture-pane','-p','-t',peerView.pane])).includes('/help'));
  const activeAfter=await tm(['list-windows','-t',`=durable-jobs-proof`,'-F','#{window_id}:#{window_active}']);
  assert.equal(activeAfter.split('\n').find(line=>line.endsWith(':1')),activeBefore.split('\n').find(line=>line.endsWith(':1')),'delegation stole focus');
  assert.equal((await wt(['view','show',owner.root,parentView.id])).pinned,true);
  // A notification commits only an application document and must not schedule.
  await wt(['message','send',owner.root,'human',peers.id,'NON_WAKING_NOTICE','--notify','--id','notice-proof']);
  await until(async()=>(await tm(['capture-pane','-p','-t',peerView.pane])).includes('NON_WAKING_NOTICE'));
  assert.equal(existsSync(`${peers.adapter.durable.store}.calls`),false);
  assert.equal((await wt(['roots'])).find(r=>r.id===owner.root).wake_budget,58);
  await wt(['message','send',owner.root,'human',peers.id,'Read this authorized request','--id','request-kill-proof']);
  await until(()=>existsSync(`${peers.adapter.durable.store}.inbox-kill-ready`));
  await until(async()=>(await tm(['capture-pane','-p','-t',peerView.pane])).includes('INBOX_FIXTURE_OK'));
  process.kill(Number(await readFile(`${peers.adapter.durable.store}.inbox-kill-ready`,'utf8')),'SIGKILL');
  await until(async()=>{await wt(['restore',owner.root]);const messages=await wt(['message','poll',owner.root,peers.id]);return !messages.messages.some(m=>m.id==='request-kill-proof');});
  assert.equal((await readFile(`${peers.adapter.durable.store}.calls`,'utf8')).trim().split('\n').length,1,'admit-before-ack recovery duplicated provider turn');
  const beforeStop=(await wt(['agents','show',owner.root,peers.id])).runtime;
  await wt(['agents','stop',owner.root,owner.parent]);await delay(150);
  assert.equal((await wt(['agents','show',owner.root,peers.id])).runtime,beforeStop);
  assert.equal(await tm(['display-message','-p','-t',peerView.pane,'#{pane_dead}']),'0','parent stop killed independent peer');
  assert.equal((await wt(['roots'])).find(r=>r.id===owner.root).wake_budget,57);
  phase='completed';
});
