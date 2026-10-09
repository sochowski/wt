import { createHash } from 'node:crypto';
import { defineDoc } from '@earendil-works/pi-durable';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
export const PluginDoc=defineDoc({kind:'wt.plugins.v2',version:2,scope:'conversation',history:'latest',fork:'initial',initial:()=>({calls:{}})});
export const PluginContractDoc=defineDoc({kind:'wt.plugin-contract.v2',version:2,scope:'session',initial:()=>({})});
/** Public storage reads only, before any factory/lifecycle execution or Harness admission. */
export async function inspectPluginStore(launch) {
 const storage=await openNodeSqliteStorage(launch.identity.store);
 try {
  async function read(kind,scope,version=2){const record=await storage.findDocument({kind,scope},'current',context);if(!record)return;const doc=await storage.document(record.id,'current',context);if(doc.version!==version)throw new Error('Incompatible plugin document version');return doc.value;}
  const descriptor=await read('wt.plugin-contract.v2',{kind:'session'});if(!descriptor)throw new Error('Missing immutable persisted plugin descriptors');
  const doc=await read('wt.plugins.v2',{kind:'conversation',conversationId:launch.identity.conversation});
  const entries=[];let cursor;
  do {const page=await storage.scanEntries({conversationId:launch.identity.conversation},512,cursor,context);entries.push(...page.items);cursor=page.next;if(entries.length>4096)throw new Error('Plugin preflight history limit exceeded');}while(cursor);
  const uncertain=[],receipted=[];
  const hash=value=>createHash('sha256').update(JSON.stringify(value)).digest('hex');
  if(Object.keys(doc?.calls||{}).length>128)throw new Error('Plugin operation capacity exceeded');
  for(const[id,call]of Object.entries(doc?.calls||{})){
   if(call.contract!==launch.identity.plugin_contract||hash(call.args)!==call.argsDigest||(call.candidate&&hash(call.candidate)!==call.candidateDigest)||(call.answer&&hash(call.answer)!==call.answerDigest))throw new Error('Immutable plugin operation digest mismatch');
   const receipt=entries.find(entry=>entry.kind==='pi.tool-result'&&entry.byTaskId===call.taskId&&entry.model?.some(message=>message.role==='toolResult'&&message.toolCallId===id&&message.toolName===call.name&&JSON.stringify(message.details)===JSON.stringify(call.candidate?.details)));
   if(!call.candidate||!receipt)uncertain.push({id,...call,state:'uncertain'});else receipted.push({id,...call,receipt:receipt.id,state:receipt.model[0].isError||call.candidate.details?.error?'failed':'receipted'});
  }
  const live=await read('pi.live',{kind:'conversation',conversationId:launch.identity.conversation},1);let unfinishedRun;
  if(live?.run){const task=await storage.task(live.run.taskId,context);if(!task||task.conversationId!==launch.identity.conversation||task.version!==1||!['pending','running','waiting','completing'].includes(task.state.status))throw new Error('Incompatible persisted unfinished core run');unfinishedRun={taskId:task.id,kind:task.kind,version:task.version,status:task.state.status};}
  return {descriptor,uncertain,receipted,unfinishedRun};
 }finally{await storage.close(context);}
}
