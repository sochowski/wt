import { createHash } from 'node:crypto';
import { realpathSync, existsSync } from 'node:fs';
import { DatabaseSync } from 'node:sqlite';
import { dirname } from 'node:path';
import { defineDoc } from '@earendil-works/pi-durable';
import { validateToolArguments } from '@earendil-works/pi-ai/utils/validation';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
export const McpContractDoc = defineDoc({kind:'wt.mcp-contract.v1',version:1,scope:'session',initial:()=>({})});
export const McpCallsDoc = defineDoc({kind:'wt.mcp-calls.v1',version:1,scope:'conversation',history:'latest',fork:'initial',initial:()=>({calls:{}})});
export const json = value => JSON.parse(JSON.stringify(value));
export function digest(value) {
  const canonical = value => Array.isArray(value)?value.map(canonical):value&&typeof value==='object'?Object.fromEntries(Object.keys(value).sort().map(key=>[key,canonical(value[key])])):value;
  return createHash('sha256').update(JSON.stringify(canonical(value))).digest('hex');
}
export function bounded(value, bytes=128*1024) {
  const text=JSON.stringify(value);if(typeof text!=='string'||Buffer.byteLength(text)>bytes)throw new Error('MCP journal/result bound exceeded');
  return JSON.parse(text);
}
export function ownerIdentity(launch) {
  const d=launch.identity;
  if(!launch.root||!launch.agent||d?.profile!=='mcp-boundary-v1'||d.conversation!==1||!/^[a-f0-9]{32}$/.test(d.uuid)||d.read_only||d.job||!Array.isArray(d.tools)||d.tools.length>64||d.tools.some(name=>typeof name!=='string'||name.length>128)||new Set(d.tools).size!==d.tools.length||realpathSync(d.cwd)!==d.cwd||realpathSync(dirname(d.store))!==dirname(d.store))throw new Error('MCP boundary requires exact fresh-profile owner and explicit nondelegated tool ceiling');
  const owner=bounded({root:launch.root,agent:launch.agent,store:d.store,uuid:d.uuid,conversation:d.conversation,cwd:d.cwd,profile:d.profile,readOnly:false,tools:[...d.tools].sort()},16384);
  if(existsSync(d.store)){
    const db=new DatabaseSync(d.store,{readOnly:true});
    try {
      if(db.prepare("SELECT name FROM sqlite_master WHERE type='table' AND name='wt_identity'").get()){
        const envelope=JSON.parse(db.prepare('SELECT value FROM wt_identity WHERE singleton=1').get()?.value??'null');
        if(!envelope||['root','agent','store','uuid','conversation','cwd','profile'].some(key=>envelope[key]!==owner[key])||envelope.read_only===true)throw new Error('MCP cannot adopt a different WT application profile/owner');
      }
    } finally {db.close();}
  }
  return owner;
}
export function resultValue(result) {
  return {content:result.content,details:result.details??{},isError:Boolean(result.isError||result.details?.error)};
}
export function ambiguousResult(result) {
  return ['may_have_run','invalid_result','server_error'].includes(result?.details?.error)||result?.details?.error==='host_error'&&result?.details?.sent===true;
}
export function verifyRequest(id, record, entries, catalog) {
  const tool=catalog.find(tool=>tool.name===record.name);
  const assistant=entries.find(entry=>entry.id===record.assistant);
  const call=assistant?.model?.find(message=>message.role==='assistant')?.content.find(block=>block.type==='toolCall'&&block.id===id&&block.name===record.name);
  if(!tool||!call||digest(validateToolArguments(tool,call))!==record.argumentsDigest)throw new Error('MCP request differs from authoritative assistant call');
}
export function classifyCalls(doc, entries, contract, catalog) {
  if(Object.keys(doc?.calls||{}).length>128)throw new Error('MCP operation capacity exceeded');
  bounded(doc??{},4*1024*1024);
  const uncertain=[],receipted=[];
  for(const[id,call]of Object.entries(doc?.calls||{})){
    if(!id||Buffer.byteLength(id)>256||call.contract!==contract||digest(call.arguments)!==call.argumentsDigest||!contract||!Number.isSafeInteger(call.taskId)||call.taskId<1||catalog&&!catalog.some(tool=>tool.name===call.name)||!['admitted','dispatch-intent','remote-result','candidate'].includes(call.phase)||call.candidate&&digest(call.candidate)!==call.candidateDigest)throw new Error('MCP immutable operation mismatch');
    if(catalog)verifyRequest(id,call,entries,catalog);
    const receipt=call.candidate&&entries.find(entry=>entry.kind==='pi.tool-result'&&entry.byTaskId===call.taskId&&entry.model?.some(message=>message.role==='toolResult'&&message.toolCallId===id&&message.toolName===call.name&&digest(resultValue(message))===digest(resultValue(call.candidate))));
    const value={id,...call};
    if(!receipt||call.ambiguous||ambiguousResult(call.candidate))uncertain.push(value);
    else receipted.push({...value,receipt:receipt.id,failed:Boolean(call.candidate.isError||call.candidate.details?.error)});
  }
  return {uncertain,receipted};
}
/** Public committed storage reads only; invoke BEFORE factory/transport/model effects. */
export async function inspectMcpStore(launch, expected) {
  const owner=ownerIdentity(launch);
  if(realpathSync(owner.store)!==owner.store)throw new Error('MCP store is not canonical');
  const storage=await openNodeSqliteStorage(owner.store);
  try {
    const read=async(kind,scope)=>{
      const record=await storage.findDocument({kind,scope},'current',context);if(!record)return;
      const document=await storage.document(record.id,'current',context);if(document.version!==1)throw new Error('MCP preflight document version mismatch');return document.value;
    };
    const descriptor=await read('wt.mcp-contract.v1',{kind:'session'});
    if(!descriptor||digest(descriptor.owner)!==digest(owner)||descriptor.source!==expected.source||digest(descriptor.config)!==digest(expected.config))throw new Error('MCP immutable owner/source/config mismatch');
    if(descriptor.phase==='initializing')return {descriptor,held:true,reason:'initialization-uncertain',uncertain:[],receipted:[]};
    if(descriptor.phase!=='ready'||descriptor.contract!==digest({owner:descriptor.owner,source:descriptor.source,config:descriptor.config,catalog:descriptor.catalog})||!Array.isArray(descriptor.catalog)||descriptor.catalog.length>64)throw new Error('MCP immutable catalog mismatch');
    const entries=[];let cursor;
    do {const page=await storage.scanEntries({conversationId:owner.conversation},256,cursor,context);entries.push(...page.items);cursor=page.next;if(entries.length>4096)throw new Error('MCP preflight history limit exceeded');}while(cursor);
    const calls=await read('wt.mcp-calls.v1',{kind:'conversation',conversationId:owner.conversation});
    const classified=classifyCalls(calls,entries,descriptor.contract,descriptor.catalog);
    for(const [id,call]of Object.entries(calls?.calls??{})){
      const task=await storage.task(call.taskId,context);
      if(!task||task.kind!=='pi.tool'||task.version!==1||task.conversationId!==owner.conversation||task.input.assistant!==call.assistant||task.input.callId!==id)throw new Error('MCP task/call admission identity mismatch');
    }
    const live=await read('pi.live',{kind:'conversation',conversationId:owner.conversation});
    let unfinishedRun;
    if(live?.run){
      const task=await storage.task(live.run.taskId,context);
      if(!task||task.conversationId!==owner.conversation||task.version!==1||!['pending','running','waiting','completing'].includes(task.state.status))throw new Error('MCP unfinished run identity mismatch');
      unfinishedRun={taskId:task.id,kind:task.kind,status:task.state.status};
    }
    return {descriptor,...classified,unfinishedRun,held:Boolean(classified.uncertain.length||unfinishedRun),reason:classified.uncertain.length?'dispatch-uncertain':unfinishedRun?'continuation-unavailable':undefined};
  } finally {await storage.close(context);}
}
