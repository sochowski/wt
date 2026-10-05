import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, symlink, writeFile, readFile, rm } from 'node:fs/promises';
import { realpathSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { execFile, spawn } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';
const exec=promisify(execFile),source=fileURLToPath(new URL('../..',import.meta.url));
test('private compiled WT: real durable managed presentation reuse/protections and attached pinned human self-stop', {timeout:60000},async t=>{
 const home=realpathSync(await mkdtemp('/tmp/wt-durable-controls-')),bin=join(home,'bin'),config=join(home,'source','config'),runtime=join(home,'source','runtime','pi-durable');
 const socket=`wt-durable-controls-${process.pid}`,tmux=(await exec('which',['tmux'])).stdout.trim();let env,client,store,phase='fixture';
 t.after(async()=>{
  t.diagnostic(`phase=${phase}`);
  if(store)t.diagnostic(await readFile(`${store}.self-stop-error`,'utf8').catch(()=> 'No self-stop error receipt'));
  if(env&&phase!=='completed') {
   const panes=await exec(tmux,['-L',socket,'list-panes','-a','-F','#{pane_id}'],{env}).catch(()=>({stdout:''}));
   for(const pane of panes.stdout.trim().split('\n').filter(Boolean))t.diagnostic((await exec(tmux,['-L',socket,'capture-pane','-p','-t',pane],{env}).catch(()=>({stdout:''}))).stdout);
  }
  client?.stdin.end();client?.kill();if(env)await exec(tmux,['-L',socket,'kill-server'],{env}).catch(()=>{});await rm(home,{recursive:true,force:true,maxRetries:10,retryDelay:100});
 });
 await mkdir(bin);await mkdir(config,{recursive:true});await mkdir(runtime,{recursive:true});await mkdir(join(home,'state'));await mkdir(join(home,'pi'));await mkdir(join(home,'parent'));await mkdir(join(home,'assigned'));
 await writeFile(join(home,'parent','input.txt'),'before');
 for(const file of ['wt-view.lua','wt-present.lua','wt-stack.lua','wt-shell.lua'])await symlink(join(source,'config',file),join(config,file));
 await symlink(join(source,'runtime','pi-durable','private-job-fixture.mjs'),join(runtime,'main.mjs'));
 await writeFile(join(bin,'tmux'),`#!/bin/sh\nexec '${tmux}' -L '${socket}' "$@"\n`,{mode:0o700});
 const binary=join(bin,'wt-state');await exec('go',['build','-o',binary,'.'],{cwd:join(source,'state'),timeout:60000});
 env={PATH:`${bin}:${process.env.PATH}`,HOME:home,TERM:'xterm-256color',LANG:'en_US.UTF-8',SHELL:'/bin/bash',TMPDIR:'/private/tmp',WT_DB:join(home,'state','wt.db'),WT_STATUS_DIR:join(home,'state'),WT_BASE_DIR:join(home,'worktrees'),WT_CONFIG_DIR:join(home,'config'),WT_SOURCE_CONFIG:config,WT_STATE:binary,PI_CODING_AGENT_DIR:join(home,'pi')};
 const tm=async args=>(await exec(join(bin,'tmux'),args,{env,timeout:10000})).stdout.trim();
 const wt=async args=>{const out=(await exec(binary,['worktree',...args],{env,timeout:15000})).stdout.trim();return out?JSON.parse(out):undefined;};
 const until=async check=>{const deadline=Date.now()+15000;while(Date.now()<deadline){if(await check())return;await delay(50);}throw new Error(`controls deadline ${phase}`);};
 await tm(['-f','/dev/null','new-session','-d','-s','sandbox','-c',home]);
 const root=await wt(['new','controls','--cwd',join(home,'parent')]);const parent=root.agents[0];assert.equal(parent.adapter.backend,'durable');store=parent.adapter.durable.store;const view=root.views.find(v=>v.target===parent.id);
 await until(async()=>(await tm(['capture-pane','-p','-t',view.pane])).includes('/help'));
 await wt(['view','pin',root.id,view.id]);
 const peer=await wt(['agents','create',root.id,'peer','--cwd','root','--read-only']);
 assert.equal(peer.adapter.backend,'durable');
 client=spawn(tmux,['-L',socket,'-C','attach-session','-t','=controls'],{env,stdio:['pipe','pipe','pipe']});client.stdout.on('data',()=>{});client.stderr.on('data',()=>{});
 await until(async()=>(await tm(['list-clients','-F','#{client_session}'])).includes('controls'));
 const active=await tm(['display-message','-p','-t',view.pane,'#{window_id}:#{pane_id}']);
 const input=async text=>{await tm(['send-keys','-H','-t',view.pane,...[...Buffer.from(`${text}\r`)].map(n=>n.toString(16))]);};
 const present=async index=>{
  await input('Show a managed deck');await until(async()=>(await tm(['capture-pane','-p','-t',view.pane])).includes(`PRESENT_PROOF_${index}`));
  return (await wt(['view','list',root.id])).filter(v=>v.kind==='presentation');
 };
 phase='present-create';const first=await present(0);assert.equal(first.length,1);assert.equal(first[0].target,root.id);assert.equal(first[0].manager,parent.id);assert.equal(first[0].state.deck.version,1);
 phase='present-reuse';const reused=await present(1);assert.equal(reused.length,1);assert.equal(reused[0].id,first[0].id);
 await wt(['view','pin',root.id,first[0].id]);phase='present-pin-protection';const pinned=await present(2);assert.equal(pinned.length,2);assert.equal(pinned.find(v=>v.id===first[0].id).pinned,true);
 assert.equal(await tm(['display-message','-p','-t',view.pane,'#{window_id}:#{pane_id}']),active);
 assert.ok((await tm(['list-clients','-F','#{client_session}:#{pane_id}'])).includes(`controls:${view.pane}`),'human client focus moved');
 // The ordinary model-origin route remains rejected even for self.
 const actorEnv={...env,WT_ROOT_ID:root.id,WT_AGENT_ID:parent.id,WT_RUNTIME_ID:parent.runtime};
 await exec(binary,['set','controls','--message','live nonjob fixture remains admitted'],{env:actorEnv,timeout:10000});
 await exec(binary,['agents','install-hooks','--home',join(home,'nonjob-hooks'),'--state-dir',join(home,'nonjob-hook-state'),'--template-dir',join(source,'config')],{env:actorEnv,timeout:10000});
 await readFile(join(home,'nonjob-hooks','.pi','agent','extensions','wt','extension.js'));
 await assert.rejects(exec(binary,['worktree','agents','stop',root.id,parent.id],{env:actorEnv,timeout:10000}),/protected/);
 phase='human-self-stop';await input('/stop');await until(async()=>(await wt(['agents','show',root.id,parent.id])).stopped);
 await wt(['restore',root.id]);const stopped=await wt(['agents','show',root.id,parent.id]);assert.equal(stopped.runtime,'');assert.equal(stopped.stopped,true);
 assert.equal((await wt(['view','show',root.id,view.id])).pinned,true);assert.equal((await wt(['agents','show',root.id,peer.id])).runtime,peer.runtime);assert.equal((await wt(['agents','show',root.id,peer.id])).stopped,false);
 phase='fresh-menu-default/override-and-existing-backend-preservation';
 await writeFile(join(bin,'pi'),'#!/bin/sh\nexec sleep 60\n',{mode:0o700});
 await writeFile(join(bin,'fzf'),`#!/bin/sh
for arg in "$@"; do
 case "$arg" in --print-query) printf '%s\\n' "$WT_MENU_FIXTURE_NAME"; exit 0;; --header=Repositories*) printf 'none:0\\n'; exit 0;; esac
done
IFS= read -r line
printf '%s\\n' "$line"
`,{mode:0o700});
 for(const backend of ['durable','native']) {
  env.WT_MENU_FIXTURE_NAME=`menu-${backend}`;
  await wt(['setup-menu','--offline',...(backend==='native'?['--backend','native']:[])]);
  const menu=(await wt(['roots',`menu-${backend}`])).find(r=>r.name===`menu-${backend}`);assert.ok(menu);
  const adapter=menu.agents[0].adapter;
  if(backend==='durable')assert.equal(adapter.backend,'durable');else assert.equal(adapter.durable,undefined);
  const before=await wt(['agents','show',menu.id,menu.agents[0].id]);
  await wt(['setup-menu','--root',menu.id,'--offline']);
  assert.deepEqual((await wt(['agents','show',menu.id,menu.agents[0].id])).adapter,before.adapter);
 }
 phase='completed';
});
