import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,realpath,writeFile,readFile,symlink,rm} from 'node:fs/promises';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';
import {execFile} from 'node:child_process';
const exec=promisify(execFile),source=fileURLToPath(new URL('../..',import.meta.url));
for(const fault of ['startup-exit-one','startup-exit-zero','bootstrap-failure'])test(`compiled WT ${fault}: pause unpublished host once, status/restore do not loop, human exact resume preserves budget`,{timeout:90000},async t=>{
 const home=await realpath(await mkdtemp('/tmp/wt-failed-host-')),bin=join(home,'bin'),config=join(home,'source/config'),runtime=join(home,'source/runtime/pi-durable'),socket=`wt-failure-${process.pid}-${fault}`;
 let env,phase='setup';t.after(async()=>{t.diagnostic(`phase=${phase}`);if(env)await exec('tmux',['-L',socket,'kill-server'],{env}).catch(()=>{});await rm(home,{recursive:true,force:true,maxRetries:10,retryDelay:100});});
 for(const path of [bin,config,runtime,join(home,'pi'),join(home,'state'),join(home,'parent'),join(home,'assigned')])await mkdir(path,{recursive:true});
 await writeFile(join(home,'assigned/input.txt'),'before');await exec('git',['init',join(home,'assigned')]);await exec('git',['-C',join(home,'assigned'),'add','input.txt']);await exec('git',['-C',join(home,'assigned'),'-c','user.name=Fixture','-c','user.email=fixture@invalid','commit','-m','fixture']);
 const tmux=(await exec('which',['tmux'])).stdout.trim();await writeFile(join(bin,'tmux'),`#!/bin/sh\nexec '${tmux}' -L '${socket}' "$@"\n`,{mode:0o700});
 for(const name of ['wt-view.lua','wt-present.lua','wt-stack.lua','wt-shell.lua'])await symlink(join(source,'config',name),join(config,name));
 await symlink(join(source,'runtime/pi-durable/private-job-fixture.mjs'),join(runtime,'main.mjs'));
 const binary=join(bin,'wt-state');await exec('go',['build','-o',binary,'.'],{cwd:join(source,'state'),timeout:60000});
 env={PATH:`${bin}:${process.env.PATH}`,HOME:home,TMPDIR:'/private/tmp',SHELL:'/bin/bash',TERM:'xterm-256color',LANG:'en_US.UTF-8',WT_STATE:binary,WT_DB:join(home,'state/wt.db'),WT_STATUS_DIR:join(home,'state'),WT_BASE_DIR:join(home,'worktrees'),WT_CONFIG_DIR:join(home,'config'),WT_SOURCE_CONFIG:config,PI_CODING_AGENT_DIR:join(home,'pi'),WT_FIXTURE_FAULT:fault};
 const tm=async args=>(await exec(join(bin,'tmux'),args,{env,timeout:10000})).stdout.trim();
 const wt=async args=>{const out=(await exec(binary,['worktree',...args],{env,timeout:20000})).stdout.trim();return out?JSON.parse(out):undefined;};
 const internal=async(op,body)=>new Promise((resolve,reject)=>{const child=execFile(binary,['worktree','_durable-jobs',op],{env,timeout:20000},(err,stdout)=>err?reject(err):resolve(JSON.parse(stdout)));child.stdin.end(JSON.stringify(body));});
 const until=async fn=>{for(let i=0;i<600;i++){if(await fn())return;await new Promise(r=>setTimeout(r,50));}throw new Error(`private failure deadline ${phase}`);};
 await tm(['-f','/dev/null','new-session','-d','-s','sandbox','-c',home]);
 const root=await wt(['new','failure-proof','--cwd',join(home,'parent')]);const parent=root.agents[0],owner={root:root.id,parent:parent.id,runtime:parent.runtime};
 const parentView=root.views.find(view=>view.target===parent.id);await until(async()=>(await tm(['capture-pane','-p','-t',parentView.pane])).includes('/help'));
 await wt(['checkout','attach',root.id,'assigned',join(home,'assigned')]);
 phase='unpublished-exit';const job=await internal('reserve',{...owner,request:{version:1,operation:fault,task:'Private startup failure',cwd:join(home,'assigned'),criteria:['Must not fabricate completion']}});
 if(fault==='bootstrap-failure')await internal('reconcile',{...owner,job:job.id}).catch(()=>{});else await internal('reconcile',{...owner,job:job.id});await until(async()=>{const a=await wt(['agents','show',root.id,job.child]);return a.stopped&&a.status==='error';});
 const failed=await wt(['agents','show',root.id,job.child]),marker=`${failed.adapter.durable.store}.${fault==='bootstrap-failure'?'bootstrap-failed-starts':'failed-starts'}`;
 assert.equal((await readFile(marker,'utf8')).trim().split('\n').length,1);
 phase='no-automatic-relaunch';for(let i=0;i<5;i++){const status=await internal('reconcile',{...owner,job:job.id});assert.equal(status.state,'blocked');assert.equal(status.phase,'writer');assert.equal(status.result,null);assert.equal(status.review,null);}
 await wt(['restore',root.id]);await new Promise(r=>setTimeout(r,1200));assert.equal((await readFile(marker,'utf8')).trim().split('\n').length,1);
 assert.equal((await wt(['roots'])).find(r=>r.id===root.id).wake_budget,62);
 if(fault==='bootstrap-failure'){await assert.rejects(wt(['agents','resume',root.id,job.child]),/incompatible|incomplete/);assert.equal((await readFile(marker,'utf8')).trim().split('\n').length,1);phase='completed';return;}
 phase='explicit-human-resume';await wt(['agents','resume',root.id,job.child]);await until(async()=>(await readFile(marker,'utf8')).trim().split('\n').length===2);
 await until(async()=>(await wt(['agents','show',root.id,job.child])).stopped);
 const again=await wt(['agents','show',root.id,job.child]);assert.equal(again.adapter.durable.uuid,failed.adapter.durable.uuid);assert.equal(again.adapter.durable.store,failed.adapter.durable.store);assert.equal(again.adapter.durable.job,job.id);assert.equal((await wt(['roots'])).find(r=>r.id===root.id).wake_budget,62);
 phase='completed';
});
