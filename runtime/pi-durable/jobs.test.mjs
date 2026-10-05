import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile, realpath, symlink, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { tmpdir } from 'node:os';
import { createHash, randomBytes } from 'node:crypto';
import { execFile, fork } from 'node:child_process';
import { once } from 'node:events';
import { promisify } from 'node:util';
import { createModels } from '@earendil-works/pi-ai/models';
import { fauxProvider, fauxAssistantMessage, fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { openRuntime, context, definition, dependencies, assignedToolPath, assignedEnvironment } from './runtime.mjs';
import { runJob, JobDoc, checkoutEvidence, validateJobResult, changedFiles } from './jobs.mjs';
const exec=promisify(execFile);
const hash=value=>createHash('sha256').update(JSON.stringify(value)).digest('hex');
const criteria=[{id:0,status:'satisfied',evidence:'Read actual fixture and validation receipt.'}];
async function fixture(t,role='writer') {
  const cwd=await realpath(await mkdtemp(join(tmpdir(),'wt-job-fixture-')));t.after(()=>rm(cwd,{recursive:true,force:true}));
  await exec('git',['init',cwd]);await writeFile(join(cwd,'input.txt'),'before');
  await exec('git',['-C',cwd,'add','input.txt']);await exec('git',['-C',cwd,'-c','user.name=Fixture','-c','user.email=fixture@invalid','commit','-m','fixture']);
  const store=await realpath(await mkdtemp(join(tmpdir(),'wt-job-store-')));t.after(()=>rm(store,{recursive:true,force:true}));
  const contract={version:1,operation:'fixture',task:'Change input.txt and verify',cwd,criteria:['Fixture changes correctly'],root:'root',parent:'parent',parent_uuid:'a'.repeat(32),parent_conversation:1,role_version:'wt-builtins-v1',writer_tools:['read','bash','edit','write','wt_workspace'],reviewer_tools:['read','wt_workspace']};
  const job={id:'b'.repeat(64),root:'root',parent:'parent',child:'writer',reviewer:'reviewer',contract,digest:hash(contract),state:role==='writer'?'writer':'review',result_id:'c'.repeat(64),review_id:'d'.repeat(64),result:null,review:null};
  const launch={root:'root',agent:role,runtime:'runtime',identity:{store:join(store,'session.sqlite'),uuid:randomBytes(16).toString('hex'),conversation:1,definition,dependencies,cwd,initialized:false,writer_lock:join(store,'writer.lock'),read_only:role==='reviewer',job:job.id,role,tools:role==='writer'?contract.writer_tools:contract.reviewer_tools}};
  const faux=fauxProvider();const models=createModels();models.setProvider(faux.provider);
  let r=await openRuntime(launch,{bootstrap:true,models,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{}});await r.close();launch.identity.initialized=true;
  const open=async()=>{const r=await openRuntime(launch,{models,fence:async()=>{}});t.after(()=>r.close());return r;};
  return {cwd,job,launch,faux,open};
}
function writerResult(job){return {version:1,taskId:job.id,resultId:job.result_id,status:'succeeded',criteria,risks:[],changedFiles:['input.txt'],commands:[{command:'test "$(cat input.txt)" = after',result:'passed'}]};}
function reviewerResult(job){return {version:1,taskId:job.id,resultId:job.result_id,reviewId:job.review_id,verdict:'accepted',criteria:criteria.map(c=>({...c,reads:['review-read']})),risks:[],findings:[]};}

test('real pinned writer tools + host receipts + immutable publication recovery do not rerun submission',async t=>{
  const f=await fixture(t);
  f.faux.setResponses([
    fauxAssistantMessage([fauxToolCall('write',{path:'input.txt',content:'after'},{id:'write'})],{stopReason:'toolUse'}),
    fauxAssistantMessage([fauxToolCall('bash',{command:'test "$(cat input.txt)" = after',timeout:2},{id:'check'})],{stopReason:'toolUse'}),
    fauxAssistantMessage(JSON.stringify(writerResult(f.job))),
  ]);
  let packet,fail=true;
  const command=async(args,input)=>{if(args[1]==='host')return f.job;if(args[1]==='finish'){packet=input.observation;if(fail)throw new Error('fault after durable result before WT ack');return {ok:true};}throw new Error('unexpected');};
  let r=await f.open();await assert.rejects(runJob(r,command),/fault/);
  assert.equal(packet.success,true,JSON.stringify(packet.evidence));assert.deepEqual(changedFiles(packet.evidence.before,packet.evidence.after),['input.txt']);
  assert.equal(packet.evidence.tools.write.state,'passed');assert.equal(packet.evidence.tools.check.state,'passed');
  assert.equal(await readFile(join(f.cwd,'input.txt'),'utf8'),'after');
  await r.close();const calls=f.faux.state.callCount;fail=false;r=await f.open();
  const again=await runJob(r,command);assert.deepEqual(again,packet);assert.equal(f.faux.state.callCount,calls);
  const doc=await r.harness.snapshot(JobDoc,r.root.id,context);assert.ok(doc.submission);assert.equal(doc.observation.id,f.job.result_id);
});

test('independent reviewer fixed ceiling requires real read and exact writer baseline; uncertainty rejects',async t=>{
  const f=await fixture(t,'reviewer');const baseline=await checkoutEvidence(f.cwd);
  f.job.result={id:f.job.result_id,success:true,evidence:{before:baseline,after:baseline,tools:{},modelText:'writer',validated:{},humanProvenance:'not-observed'}};
  f.faux.setResponses([fauxAssistantMessage([fauxToolCall('read',{path:'input.txt'},{id:'review-read'})],{stopReason:'toolUse'}),fauxAssistantMessage(JSON.stringify(reviewerResult(f.job)))]);
  const r=await f.open();assert.deepEqual((await r.root.agent(context)).tools.map(t=>t.name),['read','wt_workspace']);
  let packet;const command=async(args,input)=>{if(args[1]==='host')return f.job;packet=input.observation;return {ok:true};};
  await runJob(r,command);assert.equal(packet.success,true,JSON.stringify(packet.evidence));assert.equal(packet.evidence.tools['review-read'].state,'passed');
  assert.throws(()=>validateJobResult(f.job,'reviewer',JSON.stringify(reviewerResult(f.job)),{before:baseline,after:{...baseline,digest:'changed'}},packet.evidence.tools),/changed/);
  assert.throws(()=>validateJobResult(f.job,'reviewer',JSON.stringify(reviewerResult(f.job)),{before:baseline,after:baseline},{}),/unobserved/);
});

test('review host evidence disables fsmonitor and rejects executable Git filters before any project program can run',async t=>{
  const f=await fixture(t),marker=join(f.launch.identity.store+'.executed');
  await exec('git',['-C',f.cwd,'config','core.fsmonitor',`touch ${marker}`]);
  await checkoutEvidence(f.cwd);
  await assert.rejects(readFile(marker),e=>e.code==='ENOENT');
  await exec('git',['-C',f.cwd,'config','filter.fixture.clean',`touch ${marker}; cat`]);
  await assert.rejects(checkoutEvidence(f.cwd),/executable filters/);
  await assert.rejects(readFile(marker),e=>e.code==='ENOENT');
});

test('actual SIGKILL after unsafe writer effect does not reexecute and cannot become accepted success',async t=>{
  const f=await fixture(t);
  const path=`${f.launch.identity.store}.launch.json`;await writeFile(path,JSON.stringify({launch:f.launch,job:f.job}));
  const child=fork(new URL('./job-crash-worker.mjs',import.meta.url),[path],{stdio:['ignore','pipe','pipe','ipc']});
  t.after(()=>{if(child.exitCode===null)child.kill('SIGKILL');});
  const ready=await Promise.race([once(child,'message'),once(child,'exit').then(([code])=>{throw new Error(`crash fixture exited ${code}`);})]);
  assert.equal(ready[0].effect,true);child.kill('SIGKILL');const [,signal]=await once(child,'exit');assert.equal(signal,'SIGKILL');
  f.faux.setResponses([fauxAssistantMessage(JSON.stringify(writerResult(f.job)))]);
  const r=await f.open();let packet;
  await runJob(r,async(args,input)=>{if(args[1]==='host')return f.job;packet=input.observation;return {ok:true};});
  assert.equal(packet.success,false);assert.equal(packet.evidence.tools['cut-write'].state,'intent');
  assert.equal((await readFile(`${f.launch.identity.store}.effects`,'utf8')).trim().split('\n').length,1);
  assert.equal(await readFile(join(f.cwd,'input.txt'),'utf8'),'after');
});

test('model JSON alone, incomplete effects, false commands, false paths and acceptance/review omissions are non-success',async t=>{
  const f=await fixture(t);const before=await checkoutEvidence(f.cwd);await writeFile(join(f.cwd,'input.txt'),'after');const after=await checkoutEvidence(f.cwd);
  const result=writerResult(f.job),receipt={check:{name:'bash',arguments:{command:result.commands[0].command},state:'passed'}};
  assert.doesNotThrow(()=>validateJobResult(f.job,'writer',JSON.stringify(result),{before,after},receipt));
  for(const value of [{...result,criteria:[]},{...result,resultId:'wrong'},{...result,risks:['uncertain']},{...result,commands:[]},{...result,changedFiles:[]}]) assert.throws(()=>validateJobResult(f.job,'writer',JSON.stringify(value),{before,after},receipt));
  assert.throws(()=>validateJobResult(f.job,'writer',JSON.stringify(result),{before,after},{}),/Command/);
  assert.throws(()=>validateJobResult(f.job,'writer',JSON.stringify(result),{before,after},{...receipt,effect:{state:'intent'}}),/uncertain/);
  const external=await realpath(await mkdtemp(join(tmpdir(),'wt-job-external-')));t.after(()=>rm(external,{recursive:true,force:true}));
  await symlink(external,join(f.cwd,'escape'));
  assert.throws(()=>assignedToolPath(f.cwd,'escape/secret'),/escapes/);assert.throws(()=>assignedToolPath(f.cwd,'../outside'),/escapes/);
  assert.equal(assignedToolPath(f.cwd,'new/path'),join(f.cwd,'new/path'));
});

test('real pinned read/write/edit cannot escape using @, HOME, file URLs, Unicode spaces or fallback symlinks',async t=>{
  for(const role of ['reviewer','writer']) {
    const f=await fixture(t,role),outside=await realpath(await mkdtemp(join(tmpdir(),'wt-job-outside-')));
    t.after(()=>rm(outside,{recursive:true,force:true}));
    const external=join(outside,'secret.txt');await writeFile(external,'external');
    await symlink(outside,join(f.cwd,'escape dir'));
    await symlink(external,join(f.cwd,'cafe\u0301.txt'));
    await symlink(external,join(f.cwd,'curly\u2019.txt'));
    await symlink(external,join(f.cwd,'clock\u202FAM.txt'));
    const homePath=join(process.env.HOME,'outside-ceiling.txt');await writeFile(homePath,'external');t.after(()=>rm(homePath,{force:true}));
    const attacks=[`@${external}`,pathToFileURL(external).href,'~/outside-ceiling.txt','escape\u00a0dir/secret.txt','café.txt',"curly'.txt",'clock AM.txt'];
    const name=role==='reviewer'?'read':'write';
    const args=path=>name==='read'?{path}:{path,content:'FORBIDDEN'};
    const responses=attacks.map((path,i)=>fauxAssistantMessage([fauxToolCall(i>=4?'read':name,i>=4?{path}:args(path),{id:`attack-${i}`})],{stopReason:'toolUse'}));
    if(role==='writer')responses.push(fauxAssistantMessage([fauxToolCall('edit',{path:`@${external}`,edits:[{oldText:'external',newText:'FORBIDDEN'}]},{id:'edit-escape'})],{stopReason:'toolUse'}));
    // A valid @relative and file URL still execute the exact authorized path.
    responses.push(fauxAssistantMessage([fauxToolCall('read',{path:'@input.txt'},{id:'valid-at'})],{stopReason:'toolUse'}));
    responses.push(fauxAssistantMessage([fauxToolCall('read',{path:pathToFileURL(join(f.cwd,'input.txt')).href},{id:'valid-url'})],{stopReason:'toolUse'}));
    await writeFile(join(f.cwd,'literal\u00a0name.txt'),'intended artifact');
    await writeFile(join(f.cwd,'literal name.txt'),'unrelated sibling');
    await symlink(join(f.cwd,'literal\u00a0name.txt'),join(f.cwd,'literal-alias'));
    responses.push(fauxAssistantMessage([fauxToolCall('read',{path:'literal-alias'},{id:'canonical-unicode'})],{stopReason:'toolUse'}));
    responses.push(fauxAssistantMessage('finished'));
    f.faux.setResponses(responses);const r=await f.open();await (await r.input('Exercise paths')).wait(context);
    const entries=(await r.root.entries({},1000,undefined,context)).items;
    const results=entries.filter(e=>e.kind==='pi.tool-result').map(e=>e.model[0]);
    assert.equal(results.filter(m=>m.isError).length,attacks.length+(role==='writer'?1:0));
    assert.equal(results.filter(m=>!m.isError).length,3);
    assert.ok(results.some(m=>m.content?.some(b=>b.text==='intended artifact')));
    const receipts=await r.harness.snapshot(JobDoc,r.root.id,context);assert.equal(receipts.tools['canonical-unicode'].arguments.path,join(f.cwd,'literal\u00a0name.txt'));
    assert.equal(await readFile(external,'utf8'),'external');assert.equal(await readFile(homePath,'utf8'),'external');
    assert.equal(JSON.stringify(results.filter(m=>m.isError)).includes('external secret content'),false);
    const env=assignedEnvironment(new NodeExecutionEnv({cwd:f.cwd}),f.cwd);
    await assert.rejects(env.openBinaryReader(external,undefined,context),/escapes/);
    await assert.rejects(env.writeFile(external,'FORBIDDEN',context),/escapes/);
  }
});

test('mandatory reviewer requires every changed artifact and criterion-bound host read receipts; ignored output is in exact snapshot',async t=>{
  const f=await fixture(t,'reviewer');await writeFile(join(f.cwd,'.gitignore'),'dist/\n');
  const {mkdir}=await import('node:fs/promises');await mkdir(join(f.cwd,'dist'));
  await writeFile(join(f.cwd,'dist','result.txt'),'before');const before=await checkoutEvidence(f.cwd);
  await writeFile(join(f.cwd,'dist','result.txt'),'after');await writeFile(join(f.cwd,'input.txt'),'after');const after=await checkoutEvidence(f.cwd);
  assert.deepEqual(changedFiles(before,after),['dist/result.txt','input.txt']);
  f.job.result={success:true,evidence:{before,after}};
  const result=reviewerResult(f.job),proof={before:after,after};
  const read=(path,state='passed')=>({name:'read',arguments:{path:join(f.cwd,path)},state});
  assert.throws(()=>validateJobResult(f.job,'reviewer',JSON.stringify(result),proof,{'review-read':read('.gitignore')}),/artifact/);
  assert.throws(()=>validateJobResult(f.job,'reviewer',JSON.stringify(result),proof,{'review-read':read('input.txt')}),/artifact/);
  const tools={'review-read':read('input.txt'),'ignored-read':read('dist/result.txt')};
  assert.doesNotThrow(()=>validateJobResult(f.job,'reviewer',JSON.stringify(result),proof,tools));
  assert.throws(()=>validateJobResult(f.job,'reviewer',JSON.stringify({...result,criteria:criteria.map(c=>({...c,reads:['invented']}))}),proof,tools),/Criterion/);
  assert.throws(()=>validateJobResult(f.job,'reviewer',JSON.stringify(result),proof,{...tools,'ignored-read':read('dist/result.txt','failed')}),/artifact/);
  await writeFile(join(f.cwd,'dist','result.txt'),'tampered');const tampered=await checkoutEvidence(f.cwd);assert.notEqual(after.digest,tampered.digest);
  assert.throws(()=>validateJobResult(f.job,'reviewer',JSON.stringify(result),{before:tampered,after:tampered},tools),/exact writer/);
});

test('actual error/no-content/truncated reads never count as independent review evidence; deleted files require retained host diff read',async t=>{
  const f=await fixture(t,'reviewer');const before=await checkoutEvidence(f.cwd);await rm(join(f.cwd,'input.txt'));const after=await checkoutEvidence(f.cwd);
  f.job.result={success:true,evidence:{before,after}};
  f.faux.setResponses([fauxAssistantMessage([fauxToolCall('read',{path:'wt://review-diff'},{id:'review-read'})],{stopReason:'toolUse'}),fauxAssistantMessage(JSON.stringify(reviewerResult(f.job)))]);
  const r=await f.open();const observation=await runJob(r,async args=>args[1]==='host'?f.job:{ok:true});assert.equal(observation.success,true,JSON.stringify(observation));
  const f2=await fixture(t,'reviewer');await writeFile(join(f2.cwd,'empty.txt'),'');await writeFile(join(f2.cwd,'image.png'),Buffer.from('89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de','hex'));await writeFile(join(f2.cwd,'large.txt'),'line\n'.repeat(3000));
  f2.faux.setResponses(['empty.txt','image.png','large.txt'].map((path,i)=>fauxAssistantMessage([fauxToolCall('read',{path},{id:`no-evidence-${i}`})],{stopReason:'toolUse'})).concat(fauxAssistantMessage('finished')));
  const r2=await f2.open();await(await r2.input('read')).wait(context);const doc=await r2.harness.snapshot(JobDoc,r2.root.id,context);
  assert.ok(Object.values(doc.tools).every(t=>t.state==='failed'));
});

test('real writer git-add/index hiding flags deny success; preserved preexisting staged baseline passes; no transient-stage guarantee',async t=>{
 for(const mode of ['git-add','assume-unchanged','skip-worktree','preserved-human-stage']) {
  const f=await fixture(t);
  if(mode==='preserved-human-stage') {await writeFile(join(f.cwd,'input.txt'),'human staged baseline');await exec('git',['-C',f.cwd,'add','input.txt']);}
  const before=await checkoutEvidence(f.cwd);
  const command=mode==='git-add'?'git add input.txt':mode==='assume-unchanged'?'git update-index --assume-unchanged input.txt':mode==='skip-worktree'?'git update-index --skip-worktree input.txt':'test "$(cat input.txt)" = after';
  f.faux.setResponses([
   fauxAssistantMessage([fauxToolCall('write',{path:'input.txt',content:'after'},{id:'write'})],{stopReason:'toolUse'}),
   fauxAssistantMessage([fauxToolCall('bash',{command,timeout:2},{id:'check'})],{stopReason:'toolUse'}),
   fauxAssistantMessage(JSON.stringify({...writerResult(f.job),commands:[{command,result:'passed'}]})),
  ]);
  const r=await f.open();const observation=await runJob(r,async args=>args[1]==='host'?f.job:{ok:true});
  assert.equal(observation.evidence.tools.check.state,'passed');
  assert.equal(observation.success,mode==='preserved-human-stage',JSON.stringify(observation));
  if(mode==='preserved-human-stage')assert.deepEqual(observation.evidence.after.index,before.index);
  else assert.notDeepEqual(observation.evidence.after.index,before.index);
 }
});

test('retained success revalidates current contract/snapshot without another unsafe execution or publication',async t=>{
 const f=await fixture(t);f.faux.setResponses([
  fauxAssistantMessage([fauxToolCall('write',{path:'input.txt',content:'after'},{id:'write'})],{stopReason:'toolUse'}),
  fauxAssistantMessage([fauxToolCall('bash',{command:writerResult(f.job).commands[0].command,timeout:2},{id:'check'})],{stopReason:'toolUse'}),
  fauxAssistantMessage(JSON.stringify(writerResult(f.job))),
 ]);
 let published=0;const transport=async args=>{if(args[1]==='host')return f.job;published++;throw new Error('cut before publication');};
 let r=await f.open();await assert.rejects(runJob(r,transport),/cut/);await r.close();const calls=f.faux.state.callCount;
 await exec('git',['-C',f.cwd,'add','input.txt']);r=await f.open();await assert.rejects(runJob(r,transport),/snapshot changed/);
 assert.equal(f.faux.state.callCount,calls);assert.equal(published,1);
});
