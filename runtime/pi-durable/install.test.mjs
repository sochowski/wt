import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, copyFile, writeFile, readFile, rm, access } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { execFile } from 'node:child_process';
const exec=promisify(execFile),source=fileURLToPath(new URL('../..',import.meta.url));
test('Pi install requires locked durable bootstrap by default and fails before HOME install mutations when Node/npm fail',async t=>{
 for(const failure of ['node','npm']) {
  const dir=await mkdtemp(join(tmpdir(),'wt-durable-install-failure-'));t.after(()=>rm(dir,{recursive:true,force:true}));
  const checkout=join(dir,'source'),home=join(dir,'home'),bin=join(dir,'stubs'),native=join(dir,'native');
  for(const path of [home,bin,join(checkout,'bin'),join(native,'node_modules','jiti')])await mkdir(path,{recursive:true});
  await copyFile(join(source,'install.sh'),join(checkout,'install.sh'));await copyFile(join(source,'bin','wt-durable-bootstrap'),join(checkout,'bin','wt-durable-bootstrap'));
  await writeFile(join(native,'package.json'),JSON.stringify({name:'pi-subagents',wtNativeProviderContract:1,pi:{extensions:['./index.ts']}}));
  await writeFile(join(native,'index.ts'),'export default function() {}');await writeFile(join(native,'node_modules','jiti','package.json'),'{}');
  await writeFile(join(bin,'pi'),'#!/bin/sh\nexit 0\n',{mode:0o700});
  const marker=join(dir,'bootstrap-args');
  await writeFile(join(bin,failure),`#!/bin/sh\nprintf '%s\\n' "$@" > '${marker}'\nexit 1\n`,{mode:0o700});
  const env={PATH:`${bin}:${process.env.PATH}`,HOME:home,WT_PI_SUBAGENTS_SOURCE:native,WT_DB:join(home,'state','wt.db'),WT_STATUS_DIR:join(home,'state'),WT_BASE_DIR:join(home,'worktrees'),WT_CONFIG_DIR:join(home,'config')};
  await assert.rejects(exec('bash',[join(checkout,'install.sh')],{env,timeout:10000}));
  for(const path of ['bin','.tmux.conf','.pi/agent/settings.json','state'])await assert.rejects(access(join(home,path)),e=>e.code==='ENOENT');
  const args=await readFile(marker,'utf8');
  if(failure==='npm') {assert.ok(args.includes('ci\n'));assert.ok(args.includes('--ignore-scripts'));assert.ok(args.includes(join(checkout,'runtime','pi-durable')));}
  else assert.ok(args.includes('process.versions.node'));
 }
});
