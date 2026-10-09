import { readFileSync, readdirSync, lstatSync, existsSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { defineExtension, defineTool, hook, GenerationTask } from '@earendil-works/pi-durable';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
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
  for(const name of ['package.json','package-lock.json','sdk.mjs','loader.mjs','store.mjs','boundary.mjs'])scan(join(directory,name),name);
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
  let descriptor=saved,closed=false,blocked=false;
  const adapter=create({
    servers:Object.fromEntries(config.servers.map(server=>[server.name,{tools:server.tools,createTransport:async()=>{
      await fence();await observer?.({stage:'transport',server:server.name});
      return new StreamableHTTPClientTransport(new URL(server.url),{reconnectionOptions:{maxRetries:0},requestInit:{redirect:'error'}});
    }}])),requestTimeoutMs:5000,
    onToolCall:async remote=>{
      const call=calls.get(remote.toolCallId);if(!call||digest(remote.arguments)!==digest(call.arguments))throw new Error('MCP dispatch has no exact admitted tool call');
      await fence();
      await call.api.commit(async tx=>{
        const record=(await tx.doc(McpCallsDoc,call.api.conversationId)).calls[remote.toolCallId];
        if(record.phase!=='admitted'||record.contract!==descriptor.contract)throw new Error('MCP remote dispatch identity already consumed');
        Object.assign(record,{phase:'dispatch-intent',server:remote.server,connectionId:remote.connectionId});
      },call.context);
      await observer?.({stage:'dispatch-intent',id:remote.toolCallId});
      const result=await remote.dispatch();
      await call.api.commit(async tx=>{
        const record=(await tx.doc(McpCallsDoc,call.api.conversationId)).calls[remote.toolCallId];
        Object.assign(record,{phase:'remote-result',remoteResultDigest:digest(bounded(result))});
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
        await fence();if(closed||request.signal?.aborted)return 'deny';
        const decision=await approve(bounded({owner,server:request.serverName,tool:request.originalToolName,toolName:request.prefixedToolName,arguments:request.args,origin:request.origin},65536));
        return decision==='allow_once'?'allow_once':'deny';
      });
    }}});
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
        const ambiguous=ambiguousResult(result);
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
      const classified=classifyCalls(doc,entries,contract,descriptor.catalog);
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
