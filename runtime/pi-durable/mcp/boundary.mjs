import { readFileSync, readdirSync, lstatSync, existsSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { defineExtension, defineTool, hook, GenerationTask } from '@earendil-works/pi-durable';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import { Type } from 'typebox';
import { formatToolName } from 'pi-mcp-adapter/types';
import { validateToolArguments } from '@earendil-works/pi-ai/utils/validation';
import { runActualMcpScript } from './script-host.mjs';
import { loadHostManagedAdapter } from './loader.mjs';
import { McpContractDoc, McpCallsDoc, ownerIdentity, inspectMcpStore, classifyCalls, ambiguousResult, bounded, digest } from './store.mjs';
const directory=fileURLToPath(new URL('.',import.meta.url));

export function normalizeConfig(input) {
  if(!input||Object.keys(input).some(key=>key!=='servers')||!Array.isArray(input.servers)||!input.servers.length||input.servers.length>4)throw new Error('MCP requires one to four explicit servers');
  const names=new Set();
  const servers=input.servers.map(server=>{
    if(!server||Object.keys(server).some(key=>!['name','url','tools'].includes(key))||!/^[a-zA-Z][a-zA-Z0-9_]{0,63}$/.test(server.name)||names.has(server.name)||!Array.isArray(server.tools)||!server.tools.length||server.tools.length>16||server.tools.some(tool=>typeof tool!=='string'||!tool||tool.length>128)||new Set(server.tools).size!==server.tools.length)throw new Error('MCP server/tool configuration is unsupported');
    names.add(server.name);const url=new URL(server.url);
    if(!['http:','https:'].includes(url.protocol)||url.username||url.password||url.search||url.hash)throw new Error('MCP authentication/proxy/process options are not supported by this boundary');
    return {name:server.name,url:url.toString(),tools:[...server.tools].sort()};
  }).sort((a,b)=>a.name.localeCompare(b.name));
  if(process.env.MCP_OUTPUT_GUARD!==undefined)throw new Error('Ambient MCP output-guard override is unsupported');
  return bounded({servers},16384);
}

/** Fingerprint actual installed production bytes, not just package version claims. */
export function mcpSourceFingerprint() {
  const hash=createHash('sha256');
  const scan=(path,label)=>{
    const stat=lstatSync(path);if(stat.isSymbolicLink())throw new Error('MCP source symlinks are unsupported');
    if(stat.isDirectory()) {
      for(const name of readdirSync(path).sort())if(name!=='node_modules')scan(join(path,name),`${label}/${name}`);
    } else if(stat.isFile()){hash.update(JSON.stringify([label,createHash('sha256').update(readFileSync(path)).digest('hex')]));}
    else throw new Error('Unsupported MCP source file');
  };
  for(const name of ['package.json','package-lock.json','sdk.mjs','loader.mjs','store.mjs','boundary.mjs','contract.mjs','runtime.mjs','script-host.mjs'])scan(join(directory,name),name);
  for(const name of ['runtime.mjs','tui.mjs','inbox.mjs','jobs.mjs','wt-tools.mjs','resources.mjs'])scan(join(directory,'..',name),`host/${name}`);
  const lock=JSON.parse(readFileSync(join(directory,'package-lock.json'),'utf8'));
  for(const [path,metadata] of Object.entries(lock.packages).sort(([a],[b])=>a.localeCompare(b))){
    if(!path||metadata.dev)continue;
    if(!path.startsWith('node_modules/')||path.split('/').includes('..')||path.includes('pi-coding-agent')||path.includes('pi-agent-core'))throw new Error('Unsupported MCP dependency closure');
    const absolute=join(directory,path);
    if(!existsSync(absolute)){if(!metadata.optional)throw new Error('Missing MCP production dependency');hash.update(JSON.stringify([path,'absent']));continue;}
    scan(absolute,path);
  }
  return hash.digest('hex');
}

async function createBoundary(runtime, config, source, saved, approve, observer) {
  const owner=ownerIdentity(runtime.launch),calls=new Map(),registered=[];
  const fence=()=>runtime.fence();
  const create=await loadHostManagedAdapter();await observer?.({stage:'factory'});
  let descriptor=saved,closed=false,blocked=false,approvalContext;
  const adapter=create({
    servers:Object.fromEntries(config.servers.map(server=>[server.name,{tools:server.tools,createTransport:async()=>{
      await fence();await observer?.({stage:'transport',server:server.name});
      return new StreamableHTTPClientTransport(new URL(server.url),{reconnectionOptions:{maxRetries:0},requestInit:{redirect:'error'}});
    }}])),requestTimeoutMs:5000,
    onToolCall:async remote=>{
      const call=calls.get(remote.toolCallId);if(!call||digest(remote.arguments)!==digest(call.arguments))throw new Error('MCP dispatch has no exact admitted tool call');
      await fence();if(closed||blocked)throw new Error('MCP dispatch refused after batch cancellation/hold');
      await call.api.commit(async tx=>{
        const record=(await tx.doc(McpCallsDoc,call.api.conversationId)).calls[call.parentId||remote.toolCallId];
        const target=call.parentId?record.subcalls[call.subIndex]:record;
        if(target.phase!=='admitted'||record.contract!==descriptor.contract)throw new Error('MCP remote dispatch identity already consumed');
        Object.assign(target,{phase:'dispatch-intent',server:remote.server,connectionId:remote.connectionId});
      },call.context);
      await observer?.({stage:'dispatch-intent',id:remote.toolCallId});
      const result=await remote.dispatch();
      await call.api.commit(async tx=>{
        const record=(await tx.doc(McpCallsDoc,call.api.conversationId)).calls[call.parentId||remote.toolCallId];
        const target=call.parentId?record.subcalls[call.subIndex]:record;
        Object.assign(target,{phase:'remote-result',remoteResultDigest:digest(bounded(result))});
      },call.context);
      await observer?.({stage:'remote-result',id:remote.toolCallId});
      return result;
    },
  });
  try {
    await adapter.ready();
    adapter.extensionFactory({registerTool:tool=>{
      if(!owner.tools.includes(tool.name)||registered.some(other=>other.name===tool.name))throw new Error('MCP catalog exceeds exact caller tool ceiling');
      registered.push(tool);
    },events:{emit:(name,request)=>{
      if(name!=='pi-mcp-adapter:tool-approval-request')throw new Error('MCP unsupported lifecycle/event');
      if(approve)request.claim(async()=>{
        await fence();if(closed||blocked||request.signal?.aborted)return 'deny';
        const decision=await approve(bounded({owner,server:request.serverName,tool:request.originalToolName,toolName:request.prefixedToolName,arguments:request.args,origin:request.origin,...(approvalContext?{batch:approvalContext}:{})},65536));
        return decision==='allow_once'?'allow_once':'deny';
      });
    }}});
    const actual=[...registered];
    const serversForTool=new Map(actual.map(tool=>[tool.name,config.servers.filter(server=>server.tools.some(original=>formatToolName(original,server.name,'server')===tool.name)).map(server=>server.name)]));
    function lookup(name,server){const tool=actual.find(tool=>tool.name===name);if(!tool||server&&!serversForTool.get(name)?.includes(server))throw new Error('MCP target is not in exact discovered ceiling');return tool;}
    const discover=()=>actual.map(tool=>({path:tool.name,name:tool.name,server:serversForTool.get(tool.name)[0],description:tool.description,inputSchema:tool.parameters}));
    async function executeNested(parentId,name,args,signal,server){
      await fence();if(closed||blocked)throw new Error('MCP batch owner held/closed');
      const parent=calls.get(parentId);if(!parent)throw new Error('MCP script/gateway has no exact outer admission');
      const tool=lookup(name,server),argumentsValue=bounded(validateToolArguments(tool,{name:tool.name,arguments:args||{}}),16384);
      const doc=await runtime.harness.snapshot(McpCallsDoc,parent.api.conversationId,context),index=doc.calls[parentId].subcalls?.length||0;
      if(index>=16)throw new Error('MCP batch remote-call budget exceeded');
      const id=digest([owner.uuid,parentId,index]);
      await parent.api.commit(async tx=>{const record=(await tx.doc(McpCallsDoc,parent.api.conversationId)).calls[parentId];record.subcalls||=[];if(record.subcalls.length!==index)throw new Error('MCP batch dispatch raced');const sub={id,name:tool.name,arguments:argumentsValue,argumentsDigest:digest(argumentsValue),phase:'admitted'};const receipt=await tx.appendEntry(parent.api.conversationId,{kind:'wt.mcp-subcall-admission.v1',data:{owner:owner.uuid,outer:parentId,index,...sub}});record.subcalls.push({...sub,admission:receipt.id});bounded(await tx.doc(McpCallsDoc,parent.api.conversationId),4*1024*1024);},parent.context);
      calls.set(id,{...parent,arguments:argumentsValue,parentId,subIndex:index});
      await observer?.({stage:'subcall-admitted',id,index});
      try{
        approvalContext={tool:doc.calls[parentId].name,callId:parentId,argumentsDigest:doc.calls[parentId].argumentsDigest,index};
        const result=bounded(await tool.execute(id,argumentsValue,signal),128*1024);
        const ambiguous=ambiguousResult(result);
        await parent.api.commit(async tx=>{const record=(await tx.doc(McpCallsDoc,parent.api.conversationId)).calls[parentId],target=record.subcalls[index];const candidateDigest=digest(result);const receipt=await tx.appendEntry(parent.api.conversationId,{kind:'wt.mcp-subcall-result.v1',data:{owner:owner.uuid,outer:parentId,index,id,candidateDigest,ambiguous}});Object.assign(target,{phase:'candidate',candidate:result,candidateDigest,ambiguous,resultReceipt:receipt.id});if(ambiguous)record.ambiguous=true;bounded(await tx.doc(McpCallsDoc,parent.api.conversationId),4*1024*1024);},parent.context);
        await observer?.({stage:'subcall-candidate',id,index});
        if(ambiguous){blocked=true;throw new Error('MCP batch effect is uncertain');}
        return result;
      }finally{approvalContext=undefined;calls.delete(id);}
    }
    if(owner.tools.includes('mcp'))registered.push({name:'mcp',description:'WT narrowed gateway over the actual immutable MCP adapter catalog: search, describe, or call a selected real tool. Auth/install/process/UI modes are unsupported.',parameters:Type.Object({search:Type.Optional(Type.String({maxLength:256})),describe:Type.Optional(Type.String({maxLength:128})),tool:Type.Optional(Type.String({maxLength:128})),args:Type.Optional(Type.Record(Type.String(),Type.Unknown()))},{additionalProperties:false}),execute:async(id,params,signal)=>{
      if(params.tool)return executeNested(id,params.tool,params.args,signal);
      if(params.describe){const tool=lookup(params.describe);return{content:[{type:'text',text:JSON.stringify({name:tool.name,description:tool.description,inputSchema:tool.parameters})}],details:{mode:'describe'}};}
      const items=discover().filter(item=>!params.search||`${item.path} ${item.description}`.toLowerCase().includes(params.search.toLowerCase()));return{content:[{type:'text',text:JSON.stringify(items)}],details:{mode:'search',count:items.length}};
    }});
    if(owner.tools.includes('mcpScript'))registered.push({name:'mcpScript',description:'Run the actual pi-mcp-adapter QuickJS/WASM script worker over selected real MCP tools. tools.search/describe/call and emit are supported; every remote subcall needs allow-once approval. 16 remote calls, 30s, 64 MiB worker memory; no filesystem/process/native inference/model evaluation.',parameters:Type.Object({code:Type.String({maxLength:16384}),timeoutMs:Type.Optional(Type.Integer({minimum:1,maximum:30000}))},{additionalProperties:false}),execute:async(id,params,signal)=>{
      try{return await runActualMcpScript(params.code,params.timeoutMs||30000,signal,async(operation,message,scriptSignal)=>{
        if(operation==='call'){const result=await executeNested(id,message.path,message.args,scriptSignal,message.server);return result.details?.error?{ok:false,error:{code:result.details.error,message:'Actual MCP call failed or was denied'}}:{ok:true,data:{content:result.content,details:result.details}};}
        if(operation==='describe'){if(!message.input||Object.keys(message.input).some(key=>!['path','server'].includes(key)))throw new Error('Unsupported MCP describe options');const tool=lookup(message.input?.path,message.input?.server);return{path:tool.name,name:tool.name,description:tool.description,inputSchema:tool.parameters};}
        const input=message.input||{},query=typeof input.query==='string'?input.query:'';if(Object.keys(input).some(key=>!['query','server','limit','offset'].includes(key))||query.length>256||input.server&&!config.servers.some(server=>server.name===input.server))throw new Error('Unsupported MCP search options');const offset=input.offset??0,limit=input.limit??64;if(!Number.isSafeInteger(offset)||offset<0||!Number.isSafeInteger(limit)||limit<1||limit>64)throw new Error('MCP search pagination bound');const all=discover().filter(item=>(!input.server||item.server===input.server)&&`${item.path} ${item.description}`.toLowerCase().includes(query.toLowerCase())),items=all.slice(offset,offset+limit);return{items,total:all.length,hasMore:offset+items.length<all.length,...(offset+items.length<all.length?{nextOffset:offset+items.length}:{})};
      });}catch{return{content:[{type:'text',text:'MCP script rejected, aborted, failed or timed out; inspect retained dispatch records before retrying any remote effects.'}],details:{error:'script_failed',mode:'script'}};}
    }});
    const catalog=registered.map(tool=>bounded({name:tool.name,description:tool.description,parameters:tool.parameters}));
    const value=bounded({owner,source,config,catalog},256*1024),contract=digest(value);
    if(saved&&saved.contract!==contract)throw new Error('MCP immutable actual catalog changed');
    descriptor={...value,contract,phase:'ready'};
    if(!saved)await runtime.root.commit(async tx=>{const doc=await tx.doc(McpContractDoc);Object.assign(doc,descriptor);},context);
    const tools=registered.map(tool=>defineTool({name:tool.name,description:tool.description,parameters:tool.parameters,replay:'unsafe',executionMode:'sequential',outputLimits:{maxBytes:128*1024,maxLines:4096},execute:async(args,api,executionContext)=>{
      await fence();if(closed||blocked||!owner.tools.includes(tool.name))throw new Error('MCP owner is closed or outside admitted ceiling');
      const argumentsValue=bounded(args,16384),id=api.callId;
      if(typeof id!=='string'||!id||Buffer.byteLength(id)>256)throw new Error('MCP call identity limit exceeded');
      const task=await api.getTask(api.taskId,executionContext);
      if(!task||task.kind!=='pi.tool'||task.version!==1||task.conversationId!==api.conversationId||task.input.callId!==id)throw new Error('MCP requires exact public tool task admission');
      await api.commit(async tx=>{
        const doc=await tx.doc(McpCallsDoc,api.conversationId);
        if(doc.calls[id]||Object.keys(doc.calls).length>=128)throw new Error('MCP unsafe operation already admitted or capacity exceeded');
        bounded(doc,4*1024*1024);
        doc.calls[id]={name:tool.name,arguments:argumentsValue,argumentsDigest:digest(argumentsValue),taskId:api.taskId,assistant:task.input.assistant,contract,phase:'admitted'};
      },executionContext);
      calls.set(id,{api,arguments:argumentsValue,context:executionContext});
      try {
        await observer?.({stage:'admitted',id});
        const result=bounded(await tool.execute(id,argumentsValue,executionContext.abortSignal),128*1024);
        if(result.details?.error)result.isError=true;
        const existing=await runtime.harness.snapshot(McpCallsDoc,api.conversationId,context);
        const subs=existing.calls[id].subcalls||[];
        const ambiguous=ambiguousResult(result)||existing.calls[id].ambiguous===true||subs.some(call=>call.phase!=='candidate'||call.ambiguous);
        await api.commit(async tx=>{
          const doc=await tx.doc(McpCallsDoc,api.conversationId),record=doc.calls[id];
          Object.assign(record,{phase:'candidate',candidate:result,candidateDigest:digest(result),ambiguous});bounded(doc,4*1024*1024);
        },executionContext);
        if(ambiguous)blocked=true;
        await observer?.({stage:'candidate',id});return result;
      } catch(error){blocked=true;throw error;}
      finally {calls.delete(id);}
    }}));
    let receiptNotified=false;
    const extension=defineExtension({name:'wt-mcp-boundary',tools,hooks:[hook(GenerationTask,{beforeRequest:async()=>{
      try {
      await fence();
      const doc=await runtime.harness.snapshot(McpCallsDoc,runtime.root.id,context),entries=[];let cursor;
      do {const page=await runtime.root.entries({},256,cursor,context);entries.push(...page.items);cursor=page.next;if(entries.length>4096)throw new Error('MCP live history limit exceeded');}while(cursor);
      const classified=classifyCalls(doc,entries,contract,descriptor.catalog,owner);
      if(classified.uncertain.length){
        // Ordinary hook errors are reported, not fatal. The synchronous public
        // Models gate below is the effect fence; never self-abort a live hook.
        blocked=true;
        throw new Error('MCP result is uncertain; provider continuation is blocked');
      }
      if(classified.receipted.length&&!receiptNotified){receiptNotified=true;await observer?.({stage:'receipted'});}
      } catch(error){blocked=true;throw error;}
    }})]});
    return {descriptor:bounded(descriptor,256*1024),extension,get held(){return blocked;},
      guardModels(models){
        // Harness remains the only inference owner. Preserve actual model and
        // provider methods; refuse the two public stream entrypoints BEFORE
        // handing a request to any provider. Hooks alone are not a hard fence.
        return new Proxy(models,{get(target,key){
          const value=Reflect.get(target,key);
          if(['stream','streamSimple'].includes(key))return (...args)=>{
            if(blocked||closed)throw new Error('MCP uncertain/closed owner prohibits provider effects');
            return value.apply(target,args);
          };
          return typeof value==='function'?value.bind(target):value;
        },set(){throw new Error('MCP guarded model registry is immutable');}});
      },
      async close(){if(closed)return;closed=true;await adapter.close();}};
  } catch(error){closed=true;await adapter.close();throw error;}
}

/** Fresh admitted profile only; persist bootstrap intent before discovery effects. */
export async function initializeMcpBoundary(runtime, options) {
  const owner=ownerIdentity(runtime.launch),config=normalizeConfig(options.config),source=mcpSourceFingerprint();
  await runtime.fence();
  const entries=(await runtime.root.context(context)).entries;
  const prior=await runtime.harness.snapshot(McpContractDoc,runtime.root.id,context);
  if(entries.length||prior?.phase)throw new Error('MCP initialization requires a fresh store; no adoption or migration');
  await runtime.root.commit(async tx=>{const doc=await tx.doc(McpContractDoc);Object.assign(doc,{owner,source,config,phase:'initializing'});},context);
  await options.observer?.({stage:'initialization-intent'});
  return createBoundary(runtime,config,source,undefined,options.approve,options.observer);
}

/** Run before Harness.open; a held result must never be resumed or admit input. */
export async function preflightMcpBoundary(launch, config) {
  return inspectMcpStore(launch,{config:normalizeConfig(config),source:mcpSourceFingerprint()});
}
export async function reopenMcpBoundary(runtime, options, preflight) {
  // The caller must preflight before opening the Harness, then use this exact result.
  const expected=await preflightMcpBoundary(runtime.launch,options.config);
  if(digest(expected)!==digest(preflight))throw new Error('MCP preflight changed before binding');
  if(expected.held)throw new Error(`MCP held store cannot execute factories: ${expected.reason}`);
  return createBoundary(runtime,expected.descriptor.config,expected.descriptor.source,expected.descriptor,options.approve,options.observer);
}
