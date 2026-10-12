import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,realpath,rm,writeFile,mkdir,open,readFile} from 'node:fs/promises';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createHash,randomBytes} from 'node:crypto';
import {createModels} from '@earendil-works/pi-ai';
import {fauxProvider,fauxAssistantMessage,fauxToolCall} from '@earendil-works/pi-ai/providers/faux';
import {openRuntime,context,definition,dependencies} from './runtime.mjs';
import {checkoutEvidence,validateJobResult,runJob,JobDoc,EvidenceDocV2} from './jobs.mjs';
import {EVIDENCE_V2,checkoutEvidenceV2,compactEvidenceV2,validateCompactEvidenceV2} from './evidence-v2.mjs';
const exec=promisify(execFile),hash=value=>createHash('sha256').update(JSON.stringify(value)).digest('hex');
async function repo(t){const dir=await realpath(await mkdtemp(join(tmpdir(),'wt-evidence-v2-')));t.after(()=>rm(dir,{recursive:true,force:true}));await exec('git',['init',dir]);await writeFile(join(dir,'input.txt'),'before');await exec('git',['-C',dir,'add','input.txt']);await exec('git',['-C',dir,'-c','user.name=Fixture','-c','user.email=fixture@invalid','commit','-m','fixture']);return dir;}
const criterion={id:0,status:'satisfied',evidence:'Read actual changed artifact.'};
function job(cwd){const contract={version:1,evidence_profile:EVIDENCE_V2,operation:'fixture',task:'Change input.txt and validate',cwd,criteria:['Changed file independently verified'],root:'root',parent:'parent',parent_uuid:'a'.repeat(32),parent_conversation:1,role_version:'wt-builtins-v2',writer_tools:['read','bash','edit','write','wt_workspace'],reviewer_tools:['read','wt_workspace']};return{id:'b'.repeat(64),contract,digest:hash(contract),result_id:'c'.repeat(64),review_id:'d'.repeat(64),result:null,review:null};}
function result(j,role){return{version:1,taskId:j.id,resultId:j.result_id,criteria:[{...criterion,...(role==='reviewer'?{reads:['review-read']}: {})}],risks:[],...(role==='writer'?{status:'succeeded',changedFiles:['input.txt'],commands:[{command:'test "$(cat input.txt)" = after',result:'passed'}]}:{reviewId:j.review_id,verdict:'accepted',findings:[]})};}
async function runtime(t,cwd,j,role,faux){const dir=await realpath(await mkdtemp(join(tmpdir(),'wt-v2-store-')));t.after(()=>rm(dir,{recursive:true,force:true}));const models=createModels();models.setProvider(faux.provider);const launch={root:'root',agent:role,runtime:'runtime',identity:{store:join(dir,'session.sqlite'),uuid:randomBytes(16).toString('hex'),conversation:1,definition,dependencies,cwd,initialized:false,writer_lock:join(dir,'writer.lock'),read_only:role==='reviewer',job:j.id,role,tools:role==='writer'?j.contract.writer_tools:j.contract.reviewer_tools}};const bootstrap=await openRuntime(launch,{bootstrap:true,models,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{}});await bootstrap.close();launch.identity.initialized=true;const create=async()=>{const r=await openRuntime(launch,{models,fence:async()=>{}});t.after(()=>r.close());return r;};return{create,launch};}

test('v2 covers >4096 paths and >64 MiB including ignored files with compact whole-checkout publication',{timeout:90000},async t=>{
 const cwd=await repo(t);await mkdir(join(cwd,'cache'));await writeFile(join(cwd,'.gitignore'),'cache/\n');
 for(let i=0;i<4100;i++)await writeFile(join(cwd,'cache',`entry-${i}`),'unchanged');
 for(const name of ['large-a.bin','large-b.bin']){const file=await open(join(cwd,'cache',name),'w');try{await file.truncate(40*1024*1024);}finally{await file.close();}}
 const start=performance.now(),before=await checkoutEvidenceV2(cwd);await assert.rejects(checkoutEvidence(cwd),/file limit/);
 await writeFile(join(cwd,'input.txt'),'after');const after=await checkoutEvidenceV2(cwd),compact=compactEvidenceV2(before,after);
 assert.ok(compact.before.fileCount>4096);assert.ok(compact.before.totalBytes>64*1024*1024);assert.deepEqual(validateCompactEvidenceV2(compact),['input.txt']);assert.ok(Buffer.byteLength(JSON.stringify(compact))<16384);assert.ok(!JSON.stringify(compact).includes('entry-4099'));
 await writeFile(join(cwd,'cache','entry-4099'),'tampered ignored output');const tampered=await checkoutEvidenceV2(cwd);assert.notEqual(after.summary.digest,tampered.summary.digest);
 t.diagnostic(`whole-checkout three scans: ${Math.round(performance.now()-start)}ms, ${compact.after.fileCount} files, ${compact.after.totalBytes} bytes, compact ${Buffer.byteLength(JSON.stringify(compact))} bytes`);
});
test('v2 captures __proto__ filenames, mode/index flags and authentic deletion diff; no executable filters',async t=>{
 const cwd=await repo(t);await writeFile(join(cwd,'__proto__'),'actual');const before=await checkoutEvidenceV2(cwd);assert.ok(Object.hasOwn(before.files,'__proto__'));
 await exec('git',['-C',cwd,'update-index','--assume-unchanged','input.txt']);const changed=await checkoutEvidenceV2(cwd);assert.notEqual(before.summary.indexDigest,changed.summary.indexDigest);
 await exec('git',['-C',cwd,'update-index','--no-assume-unchanged','input.txt']);await rm(join(cwd,'input.txt'));const after=await checkoutEvidenceV2(cwd);assert.equal(compactEvidenceV2(before,after).changes.find(c=>c.path==='input.txt').after,null);
 await exec('git',['-C',cwd,'config','filter.fixture.clean','touch SHOULD_NOT_RUN; cat']);await assert.rejects(checkoutEvidenceV2(cwd),/Executable/);
});
test('compact proof cannot omit changes, tamper summaries/diff, cross versions, or accept false commands/index mutations',async t=>{
 const cwd=await repo(t),j=job(cwd),before=await checkoutEvidenceV2(cwd);await writeFile(join(cwd,'input.txt'),'after');const after=await checkoutEvidenceV2(cwd),proof=compactEvidenceV2(before,after),value=result(j,'writer'),tools={check:{name:'bash',arguments:{command:value.commands[0].command},state:'passed'}};
 assert.doesNotThrow(()=>validateJobResult(j,'writer',JSON.stringify(value),proof,tools));
 assert.throws(()=>validateCompactEvidenceV2({...proof,changes:[]}),/contradicts/);assert.throws(()=>validateCompactEvidenceV2({...proof,diff:'forged'}),/digest/);assert.throws(()=>validateCompactEvidenceV2({...proof,after:{...proof.after,totalBytes:0}}),/digest/);
 assert.throws(()=>validateJobResult({...j,contract:{...j.contract,evidence_profile:undefined,role_version:'wt-builtins-v1'}},'writer',JSON.stringify(value),proof,tools),/Old admission/);
 assert.throws(()=>validateJobResult(j,'writer',JSON.stringify({...value,changedFiles:[]}),proof,tools),/Claimed/);assert.throws(()=>validateJobResult(j,'writer',JSON.stringify(value),proof,{}),/Commands/);
 await exec('git',['-C',cwd,'add','input.txt']);const staged=compactEvidenceV2(before,await checkoutEvidenceV2(cwd));assert.throws(()=>validateJobResult(j,'writer',JSON.stringify(value),staged,tools),/index/);
});
test('v2 reviewer must read every changed artifact and match the exact whole checkout after digest',async t=>{
 const cwd=await repo(t),j=job(cwd),before=await checkoutEvidenceV2(cwd);await writeFile(join(cwd,'input.txt'),'after');await writeFile(join(cwd,'second.txt'),'new');const after=await checkoutEvidenceV2(cwd);j.result={success:true,evidence:compactEvidenceV2(before,after)};
 const own=compactEvidenceV2(after,after),value=result(j,'reviewer'),read=path=>({name:'read',arguments:{path:join(cwd,path)},state:'passed'});
 assert.throws(()=>validateJobResult(j,'reviewer',JSON.stringify(value),own,{'review-read':read('input.txt')}),/artifact/);
 const receipts={'review-read':read('input.txt'),other:read('second.txt')};assert.doesNotThrow(()=>validateJobResult(j,'reviewer',JSON.stringify(value),own,receipts));
 assert.throws(()=>validateJobResult(j,'reviewer',JSON.stringify({...value,criteria:[{...criterion,reads:['invented']}]}),own,receipts),/Criterion/);
 await writeFile(join(cwd,'second.txt'),'later tamper');const later=await checkoutEvidenceV2(cwd);assert.throws(()=>validateJobResult(j,'reviewer',JSON.stringify(value),compactEvidenceV2(later,later),receipts),/baseline/);
});
test('real v2 writer/reviewer Harness receipts and publication retry use private snapshots, not inventory-sized result packets',async t=>{
 const cwd=await repo(t),j=job(cwd),faux=fauxProvider(),w=await runtime(t,cwd,j,'writer',faux);faux.setResponses([fauxAssistantMessage(fauxToolCall('write',{path:'input.txt',content:'after'},{id:'write'}),{stopReason:'toolUse'}),fauxAssistantMessage(fauxToolCall('bash',{command:result(j,'writer').commands[0].command},{id:'check'}),{stopReason:'toolUse'}),fauxAssistantMessage(JSON.stringify(result(j,'writer')))]);
 let packet,fail=true;const transport=async(args,input)=>{if(args[1]==='host')return j;packet=input.observation;if(fail)throw new Error('publication fault');return{ok:true};};let r=await w.create();await assert.rejects(runJob(r,transport),/publication fault/);assert.equal(packet.success,true);assert.equal(packet.evidence.version,2);assert.ok(!packet.evidence.before.files);const privateDoc=await r.harness.snapshot(EvidenceDocV2,1,context);assert.ok(privateDoc.before.files['input.txt']);assert.ok(privateDoc.after.files['input.txt']);await r.close();const calls=faux.state.callCount;fail=false;r=await w.create();await runJob(r,transport);assert.equal(faux.state.callCount,calls);await r.close();
 j.result=packet;const reviewer=fauxProvider(),v=await runtime(t,cwd,j,'reviewer',reviewer);reviewer.setResponses([fauxAssistantMessage(fauxToolCall('read',{path:'input.txt'},{id:'review-read'}),{stopReason:'toolUse'}),fauxAssistantMessage(JSON.stringify(result(j,'reviewer')))]);const review=await runJob(await v.create(),async args=>args[1]==='host'?j:{ok:true});assert.equal(review.success,true,JSON.stringify(review));
});
