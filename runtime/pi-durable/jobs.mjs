import { defineDoc, defineTool } from '@earendil-works/pi-durable';
import { Type } from '@earendil-works/pi-ai';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { promisify } from 'node:util';
import { execFile } from 'node:child_process';
import { readFile, lstat, readlink } from 'node:fs/promises';
import { join, relative } from 'node:path';
import { createHash } from 'node:crypto';

const exec = promisify(execFile);
const digest = value => createHash('sha256').update(typeof value === 'string' || Buffer.isBuffer(value) ? value : JSON.stringify(value)).digest('hex');
export const JobDoc = defineDoc({kind:'wt.job',version:1,scope:'conversation',history:'latest',fork:'initial',initial:()=>({intent:null,submission:null,tools:{},observation:null})});
const textResult = result => ({content:[{type:'text',text:JSON.stringify(result)}]});
export function jobCommand(command, launch, operation, extra = {}) {
  return command(['_durable-jobs', operation], {root:launch.root,parent:launch.agent,runtime:launch.runtime,...extra});
}
export function delegationTools(launch, fence, command) {
  return [defineTool({name:'wt_delegate',description:'WT-owned durable v1 delegation. Built-in writer plus mandatory independent read-only reviewer; no nested fanout. Assign an attached isolated git checkout, not the parent writer checkout. Parallel at most 4 with root/cumulative/wake limits. Retained task/result/review IDs and stores reconcile crash/relaunch. Missing/failed/uncertain review is never success. Status returns host-observed evidence, not merely model claims.',parameters:Type.Object({action:Type.Union([Type.Literal('start'),Type.Literal('status')]),tasks:Type.Optional(Type.Array(Type.Object({task:Type.String(),cwd:Type.String(),criteria:Type.Array(Type.String())}),{minItems:1,maxItems:4})),job:Type.Optional(Type.String())}),replay:'unsafe',execute:async(p,api)=>{
    await fence();
    if(p.action==='status') {
      if(!p.job) throw new Error('Exact task ID required');
      return textResult(await jobCommand(command,launch,'reconcile',{job:p.job}));
    }
    if(!p.tasks?.length || p.tasks.length>4) throw new Error('One to four tasks required');
    const jobs=[];
    for(let i=0;i<p.tasks.length;i++) {
      const request={version:1,operation:`${launch.identity.uuid}:${api.callId}:${i}`,task:p.tasks[i].task,cwd:p.tasks[i].cwd,criteria:p.tasks[i].criteria};
      const j=await jobCommand(command,launch,'reserve',{request});
      jobs.push(await jobCommand(command,launch,'reconcile',{job:j.id}));
    }
    return textResult(jobs);
  }})];
}

/** Fixed git invocations, no shell, hooks, external diff/textconv or project executable. */
export async function checkoutEvidence(cwd) {
  const gitArgs=['--no-optional-locks','--no-pager','-c','core.fsmonitor=false','-c','core.hooksPath=/dev/null','-C',cwd];
  const gitOptions={timeout:10000,maxBuffer:1024*1024,env:{...process.env,GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:'/dev/null'}};
  const git=async args => (await exec('git',[...gitArgs,...args],gitOptions)).stdout;
  // Working-tree diff can invoke clean/process filters even with textconv and
  // external diff disabled. A reviewer must never execute repository programs.
  const filters=await exec('git',[...gitArgs,'config','--get-regexp','^filter\\..*\\.(clean|process|smudge)$'],gitOptions).catch(error=>{if(error.code===1)return {stdout:''};throw error;});
  if(filters.stdout.trim()) throw new Error('Repository executable filters are unsupported by nonexecuting host evidence');
  const top=(await git(['rev-parse','--show-toplevel'])).trim();
  if(top!==cwd) throw new Error('Delegation requires exact canonical git checkout root');
  const head=(await git(['rev-parse','HEAD'])).trim();
  const paths=[...new Set((await git(['ls-files','-z','--cached','--others'])).split('\0').filter(Boolean))].sort();
  if(paths.length>4096) throw new Error('Host evidence file limit');
  const files={}; let bytes=0;
  for(const path of paths) {
    if(relative(cwd,join(cwd,path)).startsWith('..')) throw new Error('Evidence path escapes checkout');
    let stat;try{stat=await lstat(join(cwd,path));}catch(e){if(e.code==='ENOENT'){files[path]=null;continue;}throw e;}
    if(stat.isSymbolicLink()) files[path]={kind:'link',hash:digest(await readlink(join(cwd,path)))};
    else if(stat.isFile()) {
      bytes+=stat.size;if(stat.size>16*1024*1024 || bytes>64*1024*1024) throw new Error('Host evidence size limit');
      files[path]={kind:'file',mode:stat.mode & 0o777,hash:digest(await readFile(join(cwd,path)))};
    } else throw new Error('Unsupported evidence file type');
  }
  const diff=await git(['diff','--no-ext-diff','--no-textconv','--binary','HEAD','--']);
  if(diff.length>32768) throw new Error('Host diff exceeds bounded review envelope');
  // -v preserves semantic assume-unchanged/skip-worktree flags; --stage
  // records mode/blob/path/stage. No cache timestamps/raw index bytes.
  const indexPath=(await git(['rev-parse','--path-format=absolute','--git-path','index'])).trim();
  if(!(await lstat(indexPath)).isFile()) throw new Error('Missing/unreadable regular Git index');
  const index=(await git(['ls-files','--stage','-v','-z'])).split('\0').filter(Boolean).sort();
  const value={head,files,diff,index};return {...value,digest:digest(value)};
}
export function changedFiles(before,after) {
  return [...new Set([...Object.keys(before.files),...Object.keys(after.files)])].filter(p=>JSON.stringify(before.files[p]??null)!==JSON.stringify(after.files[p]??null)).sort();
}

/** Tool receipts are host-owned. An intent without a result fails acceptance on recovery. */
export async function observeJobTool(api, ctx, name, args, phase, success) {
  await api.commit(async tx=>{
    const doc=await tx.doc(JobDoc,api.conversationId), id=api.callId;
    if(phase==='intent') {
      if(doc.tools[id]) throw new Error('Host tool intent already exists; never duplicate unsafe execution');
      doc.tools[id]={name,arguments:name==='bash'?{command:args.command}:{path:args.path, ...(args.offset === undefined ? {} : {offset:args.offset}), ...(args.limit === undefined ? {} : {limit:args.limit})},state:'intent'};
    } else {
      const receipt=doc.tools[id];if(!receipt || receipt.state!=='intent') throw new Error('Missing host tool intent');
      receipt.state=success?'passed':'failed';
    }
  },ctx);
}
function parseResult(text) {
  const match=text.trim().match(/^```(?:json|acceptance-report)?\s*\n([\s\S]*?)\n```$/);
  const value=JSON.parse(match?match[1]:text);
  if(!value || Array.isArray(value) || typeof value!=='object') throw new Error('Structured object required');
  return value;
}
export function validateJobResult(job,role,modelText,evidence,tools) {
  const value=parseResult(modelText);
  if(value.version!==1 || value.taskId!==job.id || value.resultId!==job.result_id) throw new Error('Immutable task/result identity mismatch');
  if(!Array.isArray(value.criteria) || value.criteria.length!==job.contract.criteria.length) throw new Error('Acceptance criteria missing');
  for(let i=0;i<value.criteria.length;i++) {
    const c=value.criteria[i];if(c.id!==i || c.status!=='satisfied' || typeof c.evidence!=='string' || !c.evidence.trim()) throw new Error('Unsatisfied criterion');
  }
  if(!Array.isArray(value.risks) || value.risks.length) throw new Error('Unresolved risks prevent success');
  if(!Array.isArray(evidence.before.index) || !Array.isArray(evidence.after.index)) throw new Error('Missing semantic Git index evidence');
  const receipts=Object.values(tools);
  if(receipts.some(t=>t.state==='intent')) throw new Error('Interrupted host effect is uncertain');
  if(role==='writer') {
    if(JSON.stringify(evidence.before.index)!==JSON.stringify(evidence.after.index)) throw new Error('Writer changed semantic Git index state (staging/flags)');
    if(value.status!=='succeeded' || evidence.before.head!==evidence.after.head) throw new Error('Writer status/baseline HEAD mismatch');
    const changed=changedFiles(evidence.before,evidence.after);
    if(JSON.stringify(value.changedFiles)!==JSON.stringify(changed)) throw new Error('Claimed files differ from host-observed changes');
    const commands=receipts.filter(t=>t.name==='bash').map(t=>({command:t.arguments.command,result:t.state}));
    if(!commands.some(c=>c.result==='passed') || commands.some(c=>c.result==='failed') || JSON.stringify(value.commands)!==JSON.stringify(commands)) throw new Error('Command claims differ from host receipts or checks failed/missing');
  } else {
    if(value.reviewId!==job.review_id || value.verdict!=='accepted' || !Array.isArray(value.findings) || value.findings.length || !receipts.some(t=>t.name==='read' && t.state==='passed')) throw new Error('Independent review missing/failed/unobserved');
    if(evidence.before.digest!==evidence.after.digest) throw new Error('Checkout changed during read-only review');
    const writer=job.result;
    if(!writer?.success || evidence.before.digest!==writer.evidence.after.digest) throw new Error('Review did not observe exact writer result baseline');
    const changed=changedFiles(writer.evidence.before,writer.evidence.after);
    const reads=Object.entries(tools).filter(([,t])=>t.name==='read' && t.state==='passed');
    const covered=new Set(reads.map(([,t])=>t.arguments.path));
    for(const path of changed) {
      if(writer.evidence.after.files[path] == null) {
        if(!covered.has('wt://review-diff') || !writer.evidence.after.diff.includes(`diff --git a/${path} b/${path}`)) throw new Error('Deleted artifact not independently reviewed');
      } else if(!covered.has(join(job.contract.cwd,path))) throw new Error('Changed artifact not independently reviewed');
    }
    const relevant=new Set(reads.filter(([,t])=>!changed.length || changed.some(path=>t.arguments.path===join(job.contract.cwd,path) || (writer.evidence.after.files[path]==null && t.arguments.path==='wt://review-diff'))).map(([id])=>id));
    for(const criterion of value.criteria) {
      if(!Array.isArray(criterion.reads) || !criterion.reads.length || criterion.reads.some(id=>!relevant.has(id))) throw new Error('Criterion must reference successful relevant host read receipts');
    }
  }
  return value;
}
function taskPrompt(job,role) {
  const common=`WT-owned durable v1 task. You are the trusted built-in ${role}; role version ${job.contract.role_version}. No delegation, MCP or ordinary extension execution. Assigned checkout: ${JSON.stringify(job.contract.cwd)}. Task ID ${job.id}; result ID ${job.result_id}; review ID ${job.review_id}. Acceptance criteria (zero-based IDs): ${JSON.stringify(job.contract.criteria)}. Return ONLY a JSON object, no extra prose. Common fields: version:1, taskId, resultId, criteria:[{id,status:"satisfied",evidence:string}], risks:[] (list any unresolved risks instead; then success is denied). Never claim human review/provenance.\nTask: ${job.contract.task}\n`;
  if(role==='writer') return common+'Writer fields: status:"succeeded", changedFiles:sorted array of exactly the paths you changed, commands:[{command:exact bash command,result:"passed"}] in execution order. Run at least one meaningful validation command. Any failed/uncertain check denies success. Do not commit, stage, change HEAD/index semantics or affect other resources; preserve any preexisting human staged baseline. Mandatory independent review follows; your own claim is not final acceptance.';
  return common+`Reviewer fields: reviewId, verdict:"accepted" or "rejected", findings:[] (list blockers instead if any). Independently read ALL changed artifacts completely (no offset/limit/truncation; unsupported binary/empty artifacts fail closed). For deletions read path wt://review-diff to inspect the retained host diff. Each criterion must include reads:[toolCallId,...] referencing your successful relevant reads. Assess EVERY criterion against host evidence; you have no execution/mutation/spawn tools. Missing evidence or uncertainty is rejection. Writer host observation: ${JSON.stringify(job.result)}.`;
}

/** Dedicated retained-store host, never native-provider/headless fallback. */
export async function runJob(runtime,command, {evidence=checkoutEvidence}={}) {
  const launch=runtime.launch, role=launch.identity.role;
  const host=()=>command(['_durable-jobs','host'],{root:launch.root,child:launch.agent,runtime:launch.runtime});
  const job=await host();
  if(job.digest!==digest(JSON.stringify(job.contract).replace(/[<>&\u2028\u2029]/g, c => `\\u${c.charCodeAt(0).toString(16).padStart(4,'0')}`)) || job.contract.role_version!=='wt-builtins-v1') throw new Error('Incompatible resolved role contract');
  let doc=await runtime.harness.snapshot(JobDoc,runtime.root.id,context);
  const retainedObservation=Boolean(doc?.observation);
  if(!doc?.intent) {
    const before=await evidence(launch.identity.cwd);
    const intent={task:job.id,digest:job.digest,role,prompt:taskPrompt(job,role),before, ...(role==='reviewer'?{reviewDiff:job.result?.evidence?.after?.diff}: {})};
    await runtime.root.commit(async tx=>{const d=await tx.doc(JobDoc,runtime.root.id);if(!d.intent)d.intent=intent;},context);
  }
  doc=await runtime.harness.snapshot(JobDoc,runtime.root.id,context);
  if(doc.intent.task!==job.id || doc.intent.digest!==job.digest || doc.intent.role!==role) throw new Error('Retained task intent mismatch');
  if(!doc.observation) {
    // WT identity plus immutable intent fences same-type package retry weakness.
    const submission=await runtime.input(doc.intent.prompt,'followUp',`wt-job:${job.id}:${role}`);
    await runtime.root.commit(async tx=>{(await tx.doc(JobDoc,runtime.root.id)).submission=submission.id;},context);
    const settled=await submission.wait(context);
    const after=await evidence(launch.identity.cwd);
    const entries=(await runtime.root.entries({},1000,undefined,context)).items;
    const messages=entries.flatMap(e=>e.model||[]).filter(m=>m.role==='assistant');
    const last=messages[0], modelText=last?.content?.filter(b=>b.type==='text').map(b=>b.text).join('\n') || '';
    const current=await runtime.harness.snapshot(JobDoc,runtime.root.id,context);
    const proof={before:doc.intent.before,after,tools:current.tools,modelText,humanProvenance:'not-observed'};
    let success=false,validated=null;
    try{if(settled.status!=='done' || last?.stopReason==='error' || last?.stopReason==='aborted')throw new Error('Unsettled task');validated=validateJobResult(job,role,modelText,proof,current.tools);success=true;}catch{ /* bounded visible non-success; never infer acceptance from prose */ }
    const observation={id:role==='writer'?job.result_id:job.review_id,success,evidence:{...proof,validated}};
    if(JSON.stringify(observation).length>128*1024) throw new Error('Host result exceeds publication bound');
    await runtime.root.commit(async tx=>{(await tx.doc(JobDoc,runtime.root.id)).observation=observation;},context);
  }
  doc=await runtime.harness.snapshot(JobDoc,runtime.root.id,context);
  if(retainedObservation && doc.observation.success) {
    // Recovered host attestations must still satisfy today's contract and exact
    // observed snapshot; do not rerun unsafe tools or rewrite immutable IDs.
    validateJobResult(job,role,doc.observation.evidence.modelText,doc.observation.evidence,doc.observation.evidence.tools);
    if((await evidence(launch.identity.cwd)).digest!==doc.observation.evidence.after.digest) throw new Error('Retained success snapshot changed before publication');
  }
  await command(['_durable-jobs','finish'],{root:launch.root,child:launch.agent,runtime:launch.runtime,observation:doc.observation});
  return doc.observation;
}

/** Parent reporter reconciles retained IDs; never admits a new task or spends again. */
export function jobPump(runtime,command) {
  let stopped=false;
  return {stop(){stopped=true;},async poll(){
    if(stopped || runtime.launch.identity.read_only || runtime.launch.identity.job) return;
    const jobs=await jobCommand(command,runtime.launch,'list');
    for(const job of jobs) {
      let current;
      try { current=await jobCommand(command,runtime.launch,'reconcile',{job:job.id}); }
      catch { current={state:'reconciliation-pending',result:job.result,review:job.review}; }
      await runtime.root.commit(async tx=>{
        const doc=await tx.doc(JobDoc,runtime.root.id);
        doc.results ||= {};doc.results[job.id]={state:current.state,result:current.result,review:current.review};
      },context); // Commit-only notification: parent work is not awakened.
    }
  }};
}
