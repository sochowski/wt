// Version-pinned host for the ACTUAL pi-mcp-adapter packaged QuickJS worker.
// No replacement evaluator, native session/inference engine or Node eval.
import {Worker} from 'node:worker_threads';
import {readFile} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
const require=createRequire(import.meta.url);
let compiled;
export async function runActualMcpScript(code,timeoutMs,signal,handle){
 if(typeof code!=='string'||Buffer.byteLength(code)>16384||!Number.isSafeInteger(timeoutMs)||timeoutMs<1||timeoutMs>30000)throw new Error('MCP script code/timeout bound exceeded');
 const wasmPath=require.resolve('quickjs-wasi/quickjs.wasm');
 compiled??=readFile(wasmPath).then(bytes=>WebAssembly.compile(bytes)).catch(error=>{compiled=undefined;throw error;});
 const wasm=await compiled;if(signal?.aborted)throw new Error('MCP script aborted before worker');
 const lifetime=new AbortController(),ownedSignal=signal?AbortSignal.any([signal,lifetime.signal]):lifetime.signal;
 const interrupt=new SharedArrayBuffer(4),view=new Int32Array(interrupt);
 const worker=new Worker(new URL('./node_modules/pi-mcp-adapter/mcp-script-worker.mjs',import.meta.url),{workerData:{code,wasm,quickjsUrl:new URL('./dist/index.js',pathToFileURL(wasmPath)).href,interrupt,outputMaxBytes:65536},env:{},trackUnmanagedFds:false});
 let bytes=0,requests=0,pending=0,chain=Promise.resolve(),settled=false,timer,abort;
 const output=[];
 try{return await new Promise((resolve,reject)=>{
  const fail=message=>{if(settled)return;settled=true;Atomics.store(view,0,1);reject(new Error(message));};
  const retain=block=>{if(!block||block.type!=='text'||typeof block.text!=='string')throw new Error('Unsupported MCP script output block');bytes+=Buffer.byteLength(block.text);if(bytes>65536)throw new Error('MCP script output budget exceeded');output.push(block);};
  timer=setTimeout(()=>fail('MCP script timed out'),timeoutMs);abort=()=>fail('MCP script aborted');signal?.addEventListener('abort',abort,{once:true});
  worker.on('error',()=>fail('Actual MCP script worker failed'));
  worker.on('exit',()=>{if(!settled)fail('MCP script exited without result');});
  worker.on('message',message=>{
   if(settled)return;
   try{
    if(message?.type==='emit'){retain(message.block);return;}
    if(message?.type==='done'){if(pending){fail('MCP script finished with unawaited host work');return;}if(message.returnBlock)retain(message.returnBlock);settled=true;resolve({content:output.length?output:[{type:'text',text:'(empty script result)'}],details:{mode:'script',requests}});return;}
    if(message?.type==='error'){fail('MCP script evaluation failed');return;}
    if(!['call','search','describe','evaluate'].includes(message?.type)||!Number.isSafeInteger(message.id)||++requests>64)throw new Error('MCP script protocol/request budget exceeded');
    pending++;chain=chain.then(async()=>{
     if(settled||signal?.aborted)return;
     if(message.type==='evaluate')throw new Error('MCP script model evaluation is unsupported');
     const result=await handle(message.type,message,ownedSignal);
     if(Buffer.byteLength(JSON.stringify(result))>128*1024)throw new Error('MCP script intermediate budget exceeded');
     if(!settled)worker.postMessage({type:'result',id:message.id,envelope:result});
    }).finally(()=>{pending--;}).catch(()=>fail('MCP script request refused, failed or uncertain'));
   }catch{fail('MCP script output/protocol budget exceeded');}
  });
 });}finally{lifetime.abort();clearTimeout(timer);signal?.removeEventListener('abort',abort);Atomics.store(view,0,1);await worker.terminate();}
}
