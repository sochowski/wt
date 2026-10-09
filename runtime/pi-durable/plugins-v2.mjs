import { readFileSync, readdirSync, lstatSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { join, dirname } from 'node:path';
import { homedir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { createJiti } from 'jiti';
import { defineTool, defineExtension, section, hook, GenerationTask, LiveDoc } from '@earendil-works/pi-durable';
import { matchesKey, Text } from '@earendil-works/pi-tui';
import { bindPluginTheme } from './plugin-sdk-v2.mjs';
import { plain } from './conversation-ui.mjs';
import { context } from './runtime.mjs';
import { pluginProfile } from './plugin-contract-v2.mjs';
import { PluginDoc } from './plugin-store-v2.mjs';
export { PluginDoc } from './plugin-store-v2.mjs';
const rootDir = dirname(fileURLToPath(import.meta.url));
const clone = value => JSON.parse(JSON.stringify(value));
const digest = value => createHash('sha256').update(typeof value==='string'||Buffer.isBuffer(value)?value:JSON.stringify(value)).digest('hex');
const unsupported = name => { throw new Error(`WT native-compat-v2 unsupported capability: ${name}`); };
const closedObject = (name, object) => new Proxy(Object.freeze(object),{get(target,key){if(typeof key==='symbol'||Object.hasOwn(target,key))return target[key];return unsupported(`${name}.${key}`);}});
export function pluginFilesFingerprint() {
  const files=[];
  function scan(path, label) {
    const stat=lstatSync(path);if(stat.isSymbolicLink())throw new Error('Plugin source symlinks are unsupported');
    if(stat.isDirectory())for(const file of readdirSync(path).sort()) {if(file!=='node_modules')scan(join(path,file),`${label}/${file}`);}
    else if(stat.isFile())files.push([label,digest(readFileSync(path))]);else throw new Error('Unsupported plugin source file');
  }
  for(const [name,version] of [['@juicesharp/rpiv-ask-user-question','2.9.0'],['@juicesharp/rpiv-todo','2.9.0'],['@juicesharp/rpiv-config','2.9.0'],['typebox','1.3.27'],['jiti','2.7.0'],['highlight.js','10.7.3']]) {
    const dir=join(rootDir,'node_modules',name),pkg=JSON.parse(readFileSync(join(dir,'package.json'),'utf8'));
    if(pkg.name!==name||pkg.version!==version)throw new Error('Pinned plugin dependency mismatch');scan(dir,name);
  }
  for(const file of ['plugins-v2.mjs','plugin-sdk-v2.mjs','plugin-contract-v2.mjs','plugin-store-v2.mjs','runtime.mjs','wt-tools.mjs'])files.push([file,digest(readFileSync(join(rootDir,file)))]);
  const xdg=process.env.XDG_CONFIG_HOME?.trim(),configDir=xdg?.startsWith('/')?xdg:join(homedir(),'.config');
  for(const name of ['rpiv-ask-user-question','rpiv-todo'])for(const path of new Set([join(configDir,name,'config.json'),join(homedir(),'.config',name,'config.json')])) {
    let content;try{content=digest(readFileSync(path));}catch(error){if(error.code!=='ENOENT')throw error;content='absent';}files.push([path,content]);
  }
  return digest(files);
}

/** Explicit two-package host; never discovers user/project extensions or native sessions. */
export async function createPluginHost(launch, fence, observer, preflight) {
  const sourceFingerprint=pluginFilesFingerprint(),tools=new Map(),commands=new Map(),handlers=new Map(),shortcuts=new Map(),bus=new Map();
  if(launch.identity.plugin_source&&launch.identity.plugin_source!==sourceFingerprint)throw new Error('Immutable plugin source/config fingerprint mismatch');
  if(preflight){
    if(preflight.descriptor.source!==sourceFingerprint||preflight.descriptor.projectTrusted!==(process.env.WT_DURABLE_TRUST_PROJECT==='1')||digest(preflight.descriptor)!==launch.identity.plugin_contract)throw new Error('Immutable persisted plugin descriptor mismatch');
    if(preflight.uncertain.length)return recoveryInspector(launch,fence,preflight);
  }
  let runtime,registry,ui,disposed=false,active=[],pendingActive,projection=[],currentCall,notice='';
  const operations=new Map();
  const restricted=Boolean(launch.identity.read_only||launch.identity.job);
  const projectTrusted=process.env.WT_DURABLE_TRUST_PROJECT==='1';
  const emit = async (name,event={}) => {await observer?.({stage:'lifecycle',name});for(const fn of handlers.get(name)||[])await fn(event,ctx());};
  const sessionManager=closedObject('sessionManager',{getSessionId:()=>launch.identity.uuid,getBranch:()=>clone(projection)});
  const noUI=closedObject('ui',{notify:message=>{throw new Error(`UI unavailable: ${plain(message)}`);}});
  const ctx = signal => closedObject('context',{cwd:launch.identity.cwd,get mode(){return ui&&!restricted?'tui':'print';},get hasUI(){return Boolean(ui)&&!restricted;},get ui(){if(restricted)unsupported('ctx.ui denied by host ceiling');return ui||noUI;},sessionManager,signal,isProjectTrusted:()=>projectTrusted});
  const api=closedObject('api',{
    registerTool(tool){if(!['ask_user_question','todo'].includes(tool.name)||tools.has(tool.name)||typeof tool.execute!=='function')unsupported(`registerTool ${tool.name}`);tools.set(tool.name,tool);},
    registerCommand(name,value){if(name!=='todos'||commands.has(name))unsupported(`registerCommand ${name}`);commands.set(name,value);},
    registerShortcut(key,value){if(shortcuts.has(key))throw new Error('Duplicate plugin shortcut');shortcuts.set(key,value);},
    on(name,fn){if(!['session_start','session_shutdown','session_compact','session_tree','tool_execution_end','agent_start','before_agent_start'].includes(name))unsupported(`event ${name}`);const list=handlers.get(name)||[];list.push(fn);handlers.set(name,list);return()=>handlers.set(name,list.filter(each=>each!==fn));},
    getActiveTools:()=>[...active],
    setActiveTools(names){if(names.some(name=>!active.includes(name)&&!eligible(name)))throw new Error('Plugin tool activation exceeds host ceiling');pendingActive=[...new Set(names)];},
    events:closedObject('events',{on(name,fn){const list=bus.get(name)||[];list.push(fn);bus.set(name,list);return()=>bus.set(name,list.filter(each=>each!==fn));},emit(name,event){for(const fn of bus.get(name)||[])fn(clone(event));}}),
  });
  const eligible = name => ['todo','ask_user_question'].includes(name)&&!launch.identity.read_only&&!launch.identity.job&&(!launch.identity.tools||launch.identity.tools.includes(name));
  const loader=createJiti(import.meta.url,{fsCache:false,moduleCache:false,alias:{'@earendil-works/pi-coding-agent':join(rootDir,'plugin-sdk-v2.mjs')},nativeModules:['@earendil-works/pi-ai','@earendil-works/pi-tui','typebox',join(rootDir,'plugin-sdk-v2.mjs')]});
  for(const name of ['rpiv-ask-user-question','rpiv-todo']) {
    await observer?.({stage:'module',package:name});
    const factory=await loader.import(join(rootDir,'node_modules','@juicesharp',name,'index.ts'),{default:true});
    if(typeof factory!=='function')throw new Error('Plugin has no factory');await observer?.({stage:'factory',package:name});await factory(api);
  }
  if(tools.size!==2||!commands.has('todos'))throw new Error('Actual plugin registration incomplete');
  const descriptor=clone({source:sourceFingerprint,tools:[...tools].map(([name,tool])=>({name,parameters:tool.parameters,description:tool.description,promptSnippet:tool.promptSnippet,promptGuidelines:tool.promptGuidelines})),profile:pluginProfile,capabilities:['nonexecuting-uncertain-recovery','linear-durable-projection','unsafe-tools','custom-ui','widgets','shortcuts','no-external-editor','no-native-branches'],readOnly:launch.identity.read_only===true,projectTrusted,ceiling:launch.identity.tools||null});
  const fingerprint=digest(descriptor);
  async function syncProjection() {
    const view=await runtime.root.context(context);projection=view.entries.flatMap(entry=>(entry.model||[]).map(message=>({type:'message',id:String(entry.id),message:clone(message)})));
    const doc=await runtime.harness.snapshot(PluginDoc,runtime.root.id,context);
    return {view,doc};
  }
  async function receipts(recovery=false) {
    const {view,doc}=await syncProjection();if(!doc)return [];
    const changes=[];
    for(const [id,call] of Object.entries(doc.calls)) {
      if(call.state==='receipted'||call.state==='failed')continue;
      const entry=view.entries.find(entry=>entry.kind==='pi.tool-result'&&entry.byTaskId===call.taskId&&entry.model?.some(message=>message.role==='toolResult'&&message.toolCallId===id&&message.toolName===call.name&&JSON.stringify(message.details)===JSON.stringify(call.candidate?.details)));
      if(entry&&call.candidate)changes.push([id,(entry.model[0].isError||call.candidate.details?.error)?'failed':'receipted',entry.id]);else if(recovery)changes.push([id,'uncertain',null]);
    }
    if(changes.length)await runtime.root.commit(async tx=>{const next=await tx.doc(PluginDoc,runtime.root.id);for(const [id,state,entry]of changes){next.calls[id].state=state;if(entry!==null)next.calls[id].receipt=entry;}},context);
    for(const[id,state]of changes)if(state==='receipted')await observer?.({stage:'receipted',callId:id});
    const next=await runtime.harness.snapshot(PluginDoc,runtime.root.id,context);return Object.values(next?.calls||{}).filter(call=>call.state==='uncertain');
  }
  async function flushTools(){if(!pendingActive||runtime?.continuationHeld)return;await fence();active=pendingActive;pendingActive=undefined;await runtime.root.configure({tools:active.map(name=>registry.snapshot().tools().find(item=>item.tool.name===name)?.tool || unsupported(`unregistered tool ${name}`))},context);}
  let lastRun;
  const registrations=[...tools].filter(([name])=>eligible(name)).map(([name,tool])=>defineTool({name,description:tool.description,parameters:tool.parameters,replay:'unsafe',executionMode:'sequential',outputLimits:{maxBytes:65536,maxLines:2000},execute:async(args,call,executionContext)=>{
    await fence();if(!eligible(name))throw new Error('Plugin invocation exceeds host ceiling');
    await receipts();await syncProjection();
    if(Buffer.byteLength(call.callId)>256||Buffer.byteLength(JSON.stringify(args))>65536)throw new Error('Plugin identity/argument limit exceeded');
    const immutable={name,args:clone(args),argsDigest:digest(args),taskId:call.taskId,contract:fingerprint};
    await call.commit(async tx=>{const doc=await tx.doc(PluginDoc,call.conversationId);if(Object.keys(doc.calls).length>=128)throw new Error('Plugin operation capacity exceeded');if(doc.calls[call.callId])throw new Error('Plugin call identity already exists; unsafe call cannot replay');doc.calls[call.callId]={...immutable,state:'pending',journal:[]};},executionContext);
    currentCall={id:call.callId,api:call,context:executionContext};
    operations.set(call.callId,currentCall);
    try {
      await observer?.({stage:'pending',callId:call.callId});
      const result=await tool.execute(call.callId,args,executionContext.abortSignal,update=>{if(update.details!==undefined)void call.details(clone(update.details),executionContext);},ctx(executionContext.abortSignal));
      await fence();
      if(Buffer.byteLength(JSON.stringify(result))>131072)throw new Error('Plugin result limit exceeded; effect may be uncertain');
      await call.commit(async tx=>{const doc=await tx.doc(PluginDoc,call.conversationId);doc.calls[call.callId].candidate=clone(result);doc.calls[call.callId].candidateDigest=digest(result);doc.calls[call.callId].state='candidate';},executionContext);
      await observer?.({stage:'candidate',callId:call.callId});
      await emit('tool_execution_end',{toolName:name,toolCallId:call.callId,result:clone(result),isError:result.isError===true});
      return result;
    } finally {operations.delete(call.callId);currentCall=undefined;}
  }}));
  const extension=defineExtension({name:'wt-native-compat-v2',tools:registrations,sections:[section('wt-plugin-guidelines',()=>registrations.map(tool=>[tools.get(tool.name).promptSnippet,...tools.get(tool.name).promptGuidelines||[]].join('\n')).join('\n'))],hooks:[hook(GenerationTask,{beforeRequest:async(_request,hookApi,executionContext)=>{
    await fence();const live=await hookApi.snapshot(LiveDoc,hookApi.conversationId,executionContext);
    if(live?.run?.taskId!==lastRun){lastRun=live?.run?.taskId;await emit('before_agent_start');await flushTools();await emit('agent_start');}
  }})]});
  return {
    fingerprint,sourceFingerprint,descriptor,extension,tools,api,recoveryHeld:false,get notice(){return notice;},
    async bind(value,registered){runtime=value;registry=registered;active=(await runtime.root.agent(context)).tools.map(tool=>tool.name);this.recoveryHeld=(await receipts(true)).length>0;await emit('session_start');await emit('before_agent_start');await flushTools();},
    async attach({tui,theme,keybindings,editor,notices,widgets,getToolsExpanded}) {
      if(disposed||ui)throw new Error('Plugin UI lifetime already attached');bindPluginTheme(theme);
      const widgetMap=new Map(),customs=new Set(),removers=new Set();let attached=true;
      const assertLive=()=>{if(disposed||!attached)throw new Error('Plugin context stale after shutdown');};
      const notify=message=>{assertLive();notice=plain(message);notices.setText(notice);tui.requestRender();};
      ui=closedObject('ui',{theme,notify,getToolsExpanded,
        setWidget(key,value,options={placement:'aboveEditor'}){assertLive();if(key!=='rpiv-todos'||options.placement!=='aboveEditor')unsupported('widget placement/key');widgetMap.delete(key);if(value!==undefined)widgetMap.set(key,typeof value==='function'?value(tui,theme):new Text(value.join('\n'),0,0));widgets.clear();for(const widget of widgetMap.values())widgets.addChild(widget);tui.requestRender();},
        onTerminalInput(listener){assertLive();const remove=tui.addInputListener(listener);removers.add(remove);return()=>{removers.delete(remove);remove();};},
        custom:async(factory,options={})=>{
          assertLive();if(!currentCall)unsupported('custom outside registered tool');if(options.overlay!==true)unsupported('non-overlay custom');
          const operation=currentCall,done=Promise.withResolvers();let handle,component,ended=false,inputQueue=Promise.resolve();
          const finish=async result=>{
            if(ended)return;ended=true;
            try{await inputQueue;await operation.api.commit(async tx=>{const doc=await tx.doc(PluginDoc,runtime.root.id);if(Buffer.byteLength(JSON.stringify(result))>65536)throw new Error('Questionnaire answer limit exceeded');doc.calls[operation.id].answer=clone(result);doc.calls[operation.id].answerDigest=digest(result);doc.calls[operation.id].state='answered';},operation.context);await observer?.({stage:'answered',callId:operation.id});done.resolve(result);}catch(error){done.reject(error);}
            finally{handle?.hide();customs.delete(cancel);tui.setFocus(editor);tui.requestRender();}
          };
          const cancel=()=>{if(ended)return;ended=true;handle?.hide();customs.delete(cancel);tui.setFocus(editor);done.reject(new Error('Questionnaire interrupted; pending interaction is not accepted completion'));};
          component=await factory(tui,theme,keybindings,result=>{void finish(result);});
          const wrapper={render:width=>component.render(width),invalidate:()=>component.invalidate(),handleInput:data=>{
            if(ended)return;
            inputQueue=inputQueue.then(async()=>{
              const doc=await runtime.harness.snapshot(PluginDoc,runtime.root.id,context),old=doc.calls[operation.id].journal;
              if(old.length>=512||Buffer.byteLength(JSON.stringify(old))+Buffer.byteLength(data)>32768)throw new Error('Questionnaire journal limit exceeded');
              await fence();await operation.api.commit(async tx=>{(await tx.doc(PluginDoc,runtime.root.id)).calls[operation.id].journal.push(data);},operation.context);
              component.handleInput(data);tui.requestRender();
            }).catch(error=>{cancel();notify(error.message);});
          }};
          customs.add(cancel);handle=tui.showOverlay(wrapper,options.overlayOptions);options.onHandle?.(handle);
          const signal=operation.context.abortSignal;if(signal?.aborted)cancel();else signal?.addEventListener('abort',cancel,{once:true});
          try{return await done.promise;}finally{signal?.removeEventListener('abort',cancel);if(!ended)cancel();}
        },
      });
      const shortcutRemove=tui.addInputListener(data=>{for(const[key,shortcut]of shortcuts)if(matchesKey(data,key)){void Promise.resolve(shortcut.handler(ctx())).catch(error=>notify(error.message));return{consume:true};}});removers.add(shortcutRemove);
      this.detach=async()=>{if(!ui)return;for(const cancel of [...customs])cancel();for(const remove of removers)remove();removers.clear();try{await emit('session_shutdown');}finally{attached=false;widgetMap.clear();widgets.clear();ui=undefined;}};
      await emit('session_start');await emit('before_agent_start');await flushTools();
      if(restricted)notice='Plugin tools/dialogs disabled by host read-only/task ceiling';
    },
    async command(name,args){if(!commands.has(name))return false;if(restricted)unsupported('plugin commands denied by host ceiling');await fence();await commands.get(name).handler(args,ctx());return true;},
    async update(){return receipts();},
    async uncertain(){return receipts(true);},
    async detach(){await emit('session_shutdown');},
    async close(){if(disposed)return;try{await this.detach();}finally{disposed=true;operations.clear();}}, 
  };
}

/** WT-owned inspection only: never invokes any plugin module/factory/lifecycle callback. */
function recoveryInspector(launch,fence,preflight) {
 const descriptor=preflight.descriptor,records=clone(preflight.uncertain),inspection=clone({committedReceipts:preflight.receipted,uncertainRecords:records});let notices,tui,expanded='';
 const offered=descriptor.tools.filter(tool=>!descriptor.readOnly&&!launch.identity.job&&(!descriptor.ceiling||descriptor.ceiling.includes(tool.name)));
 const extension=defineExtension({name:'wt-native-compat-v2',tools:offered.map(tool=>defineTool({name:tool.name,description:tool.description,parameters:tool.parameters,replay:'unsafe',executionMode:'sequential',outputLimits:{maxBytes:65536,maxLines:2000},execute:()=>unsupported('uncertain recovery is inspection-only')})),sections:[section('wt-plugin-guidelines',()=>offered.map(tool=>[tool.promptSnippet,...tool.promptGuidelines||[]].join('\n')).join('\n'))]});
 const notice='WT read-only plugin recovery inspection: interrupted/uncertain operation; actual plugin modules, UI, tools, lifecycle and automatic input/model wake DISABLED. /plugin-inspect';
 return {descriptor,sourceFingerprint:descriptor.source,fingerprint:digest(descriptor),extension,recoveryHeld:true,get notice(){return notice+expanded;},inspection,
  async bind(){},async attach(host){notices=host.notices;tui=host.tui;notices.setText(notice);},async detach(){notices=undefined;tui=undefined;},async close(){await this.detach();},
  async command(name){if(name==='plugin-inspect'){await fence();expanded=`\n${JSON.stringify(inspection,null,2).slice(0,12000)}`;notices?.setText(notice+expanded);tui?.requestRender();return true;}if(name==='todos')unsupported('native todo overlay unavailable in uncertain recovery; use /plugin-inspect');return false;},
  async update(){return records;},async uncertain(){return records;},
 };
}
