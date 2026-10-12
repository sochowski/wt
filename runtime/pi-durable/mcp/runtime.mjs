import { readFileSync, existsSync, realpathSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { homedir } from 'node:os';
import { Harness, createRegistry, defineExtension, defineTool, section, hook, GenerationTask, ProviderDoc } from '@earendil-works/pi-durable';
import { CodingTools } from '@earendil-works/pi-durable/tools';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
import { builtinModels } from '@earendil-works/pi-ai/providers/all';
import { cleanupSessionResources } from '@earendil-works/pi-ai';
import { context, identityValue, validateStore, wtFence, stateCommand, configuredSettings, configuredModel, configuredThinking } from '../runtime.mjs';
import { resourcePrompt, readOnlyCredentials } from '../resources.mjs';
import { wtTools } from '../wt-tools.mjs';
import { wtHostContext } from '../../../config/pi-wt/orientation.js';
import { initializeMcpBoundary, preflightMcpBoundary, reopenMcpBoundary, normalizeConfig } from './boundary.mjs';
import { mcpDefinition, mcpDependencies } from './contract.mjs';

export async function openMcpRuntime(launch,options={}) {
  const d=launch.identity,fence=options.fence||(()=>wtFence(launch));
  if(d.definition!==mcpDefinition||d.dependencies!==mcpDependencies||d.read_only||d.job||!Array.isArray(d.mcp_tools)||!d.mcp_tools.length)throw new Error('MCP profile/role/ceiling mismatch');
  if(!options.bootstrap)await fence();
  const file=d.mcp_config_file;
  if(typeof file!=='string'||realpathSync(file)!==file)throw new Error('MCP config path changed');
  const bytes=readFileSync(file);if(bytes.length>16384||createHash('sha256').update(bytes).digest('hex')!==d.mcp_config_digest)throw new Error('Immutable MCP config file changed');
  const config=normalizeConfig(JSON.parse(bytes));
  if(JSON.stringify(JSON.parse(bytes))!==JSON.stringify(d.mcp_config))throw new Error('MCP admission config mismatch');
  let preflight,bootstrapIntent;
  if(options.bootstrap){
    if(existsSync(d.store))throw new Error('MCP bootstrap cannot adopt/retry an existing store');
    bootstrapIntent=JSON.stringify(launch);
    const db=new DatabaseSync(d.store);
    try{db.exec('CREATE TABLE wt_mcp_bootstrap(singleton INTEGER PRIMARY KEY CHECK(singleton=1),value TEXT NOT NULL)');db.prepare('INSERT INTO wt_mcp_bootstrap VALUES(1,?)').run(bootstrapIntent);}finally{db.close();}
  }
  else {validateStore(launch);preflight=await preflightMcpBoundary(launch,config);if(preflight.descriptor.source!==d.mcp_source||preflight.descriptor.contract!==d.mcp_contract)throw new Error('MCP persisted contract does not match admitted WT identity');}
  let mcp,ready=false,closed=false,ui,approval;
  const deny=()=>approval?.('deny');
  const runtime={launch,fence,get mcp(){return mcp;},continuationHeld:Boolean(preflight?.unfinishedRun),interruptedRun:preflight?.unfinishedRun,
    async approve(request){
      if(options.mcpApprove)return options.mcpApprove(request);
      if(!ui||closed||mcp?.held)return 'deny';
      return new Promise(resolve=>{
        if(approval){resolve('deny');return;}
        let timer;
        approval=decision=>{clearTimeout(timer);approval=undefined;ui?.clearApproval();resolve(decision);};
        timer=setTimeout(()=>approval?.('deny'),30000);
        ui.showApproval(request,decision=>approval?.(decision));
      });
    },
    attachMcpUi(value){ui=value;},detachMcpUi(){deny();ui=undefined;},
    async input(content,mode='followUp',requestId){await fence();if(mcp?.held||runtime.continuationHeld)throw new Error('Held MCP store cannot admit input or continuation');return runtime.root.submit({type:'input',content,whenBusy:mode,requestId},context);},
    async resume(){await fence();if(!mcp?.held&&!runtime.continuationHeld)runtime.harness.resume();},
    async abort(){await fence();if(mcp?.held||runtime.continuationHeld)throw new Error('Held MCP store cannot mutate retained work');deny();await runtime.root.abort(context);},
    async reportStatus(status){if(options.reportStatus)return options.reportStatus(status);await fence();await stateCommand(['hook',status]);},
    async close(){if(closed)return;closed=true;deny();const provider=runtime.harness?await runtime.harness.snapshot(ProviderDoc,runtime.root.id,context):undefined;
      try{await mcp?.close();}finally{try{await runtime.harness?.close(context);}finally{if(provider?.sessionId)cleanupSessionResources(provider.sessionId);await runtime.environment?.cleanup(context);}}
    },
  };
  try {
    if(preflight?.held){
      mcp={held:true,inspection:preflight,descriptor:preflight.descriptor,notice:`MCP read-only recovery: ${preflight.reason}. No input/model/config/inbox/job effects or automatic remote replay. /mcp-inspect`,extension:defineExtension({name:'wt-mcp-boundary',tools:(preflight.descriptor.catalog||[]).map(tool=>defineTool({...tool,replay:'unsafe',execute:async()=>{throw new Error('MCP retained descriptor is disabled by uncertain recovery');}}))}),async close(){}};
    }else if(!options.bootstrap)mcp=await reopenMcpBoundary(runtime,{config,approve:request=>runtime.approve(request),observer:options.mcpObserver},preflight);
    const agentDir=options.agentDir||process.env.PI_CODING_AGENT_DIR||join(homedir(),'.pi','agent');
    const models=options.models||builtinModels({credentials:readOnlyCredentials(process.env.WT_PI_AUTH_PATH||join(agentDir,'auth.json'))});runtime.models=models;
    const settings=options.bootstrap?configuredSettings(agentDir):undefined,model=options.bootstrap?(options.model||configuredModel(agentDir,settings)):undefined;
    const selected=model&&models.getModel(model.provider,model.modelId);if(model&&!selected)throw new Error('Configured MCP model unsupported; no provider fallback');
    const thinking=selected?configuredThinking(settings,selected,options.reportDiagnostic):undefined;
    runtime.resources=resourcePrompt(d.cwd,agentDir,process.env.WT_DURABLE_TRUST_PROJECT==='1');
    const env=options.environment||new NodeExecutionEnv({cwd:d.cwd});runtime.environment=env;
    const registry=createRegistry(),coding=CodingTools.tools.filter(tool=>!d.tools||d.tools.includes(tool.name)).map(tool=>defineTool({...tool,replay:'unsafe',execute:async(args,api,ctx)=>{await fence();if(mcp?.held||runtime.continuationHeld||realpathSync((await api.agent(ctx)).cwd)!==d.cwd)throw new Error('Held or incompatible MCP coding owner');return tool.execute(args,api,ctx);}}));
    const managed=options.bootstrap?[]:wtTools(launch,fence,stateCommand).filter(tool=>tool.name!=='wt_delegate').map(tool=>({...tool,execute:async(...args)=>{await fence();if(mcp?.held||runtime.continuationHeld)throw new Error('Held MCP owner cannot execute managed operations');return tool.execute(...args);}}));
    registry.install(defineExtension({name:'wt-host',tools:[...coding,...managed],sections:[
      section('wt-instructions',()=>`You are a coding assistant in WT. Work only on assigned resources; obey project instructions. Tool mutations/bash are not exactly-once across crashes.\n${runtime.resources.prompt}`),
      section('wt-orientation',async()=>options.bootstrap?undefined:wtHostContext({cwd:d.cwd,tools:[...coding,...managed,...(mcp?.extension.tools||[])].map(tool=>tool.name),systemPrompt:''},process.env,root=>stateCommand(['workspace',root])),{tag:false}),
    ],hooks:[hook(GenerationTask,{beforeRequest:async()=>{await fence();if(mcp?.held)throw new Error('MCP held owner');}})]}));
    if(mcp)registry.install(mcp.extension);
    // Bootstrap and held stores have no provider effect authority. Live boundary
    // wraps the actual public model registry, never a native inference owner.
    const guarded=new Proxy(models,{get(target,key){const value=Reflect.get(target,key);if(['stream','streamSimple'].includes(key))return(...args)=>{if(!ready||closed||mcp?.held||runtime.continuationHeld)throw new Error('MCP provider effect blocked');return value.apply(target,args);};return typeof value==='function'?value.bind(target):value;}});
    const activeModels=mcp&&!mcp.held?mcp.guardModels(guarded):guarded;
    runtime.harness=await Harness.open(await openNodeSqliteStorage(d.store),{models:activeModels,registry,env:()=>env,settings:{retry:{maxRetries:0},compaction:{enabled:false},stream:{maxRetries:0,timeoutMs:60000}}},context);
    runtime.root=options.bootstrap?await runtime.harness.root(context,{agent:{cwd:d.cwd,model,thinkingLevel:thinking}}):await runtime.harness.conversation(d.conversation,context);
    if(!runtime.root)throw new Error('Missing exact MCP conversation');
    if(options.bootstrap){
      mcp=await initializeMcpBoundary(runtime,{config,approve:request=>runtime.approve(request),observer:options.mcpObserver});
      d.mcp_source=mcp.descriptor.source;d.mcp_contract=mcp.descriptor.contract;
      // Bind the real catalog for persistence; bootstrap never requests inference.
      registry.install(mcp.extension);await runtime.root.configure({tools:[...coding,...mcp.extension.tools]},context);
      // Seal only the store created by this invocation's exact durable intent.
      // The general validator's unidentified-existing-store rejection stays intact.
      const db=new DatabaseSync(d.store);
      try{
        db.exec('BEGIN IMMEDIATE');
        if(db.prepare('SELECT value FROM wt_mcp_bootstrap WHERE singleton=1').get()?.value!==bootstrapIntent)throw new Error('MCP bootstrap intent changed');
        db.exec('CREATE TABLE wt_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),value TEXT NOT NULL)');db.prepare('INSERT INTO wt_identity VALUES(1,?)').run(JSON.stringify(identityValue(launch)));
        db.exec('DROP TABLE wt_mcp_bootstrap; COMMIT');
      }finally{db.close();}
      validateStore({...launch,identity:{...d,initialized:true}});
    }
    const agent=await runtime.root.agent(context);if(agent.cwd!==d.cwd||!models.getModel(agent.model.provider,agent.model.modelId))throw new Error('MCP retained cwd/model differs from admitted runtime');
    ready=!options.bootstrap&&!mcp.held;
    runtime.mcpCommand=async(name,args)=>{
      if(name==='mcp-inspect')return JSON.stringify(mcp.inspection||await preflightMcpBoundary(launch,config),null,2);
      if(name!=='mcp')return;
      const [action='list',tool]=args.trim().split(/\s+/);
      if(action==='list')return (mcp.descriptor.catalog||[]).map(item=>`${item.name}: ${item.description}`).join('\n');
      if(action==='describe'){const item=(mcp.descriptor.catalog||[]).find(item=>item.name===tool);if(!item)throw new Error('Unknown MCP tool');return JSON.stringify(item,null,2);}
      throw new Error('Unsupported MCP command. Use chat for approved tool calls; /mcp list or describe NAME for the real cached catalog.');
    };
    return runtime;
  }catch(error){await runtime.close();throw error;}
}
