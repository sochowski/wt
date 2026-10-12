import test from 'node:test';
import assert from 'node:assert/strict';
import {runActualMcpScript} from './script-host.mjs';
test('actual packaged QuickJS worker executes search/describe/call chaining without Node globals',async()=>{
 const calls=[];
 const result=await runActualMcpScript('const found=await tools.search({query:"echo"}); const info=await tools.describe({path:found.items[0].path}); const value=await tools.call(info.path,{text:"real"}); emit(value); return typeof process+":"+typeof require+":"+typeof fetch;',2000,undefined,async(op,message)=>{
  calls.push(op);if(op==='search')return{items:[{path:'private_echo'}],total:1,hasMore:false};if(op==='describe')return{path:'private_echo'};return{ok:true,data:{content:[{type:'text',text:message.args.text}]}};
 });
 assert.deepEqual(calls,['search','describe','call']);assert.match(JSON.stringify(result),/real/);assert.match(JSON.stringify(result),/undefined:undefined:undefined/);
});
test('actual worker timeout, unawaited operations and model evaluation reject; no fallback evaluator',async()=>{
 await assert.rejects(runActualMcpScript('while(true){}',100,undefined,async()=>{}),/timed out/);
 await assert.rejects(runActualMcpScript('tools.call("private_echo", {}); return "unawaited";',2000,undefined,async()=>new Promise(resolve=>setTimeout(()=>resolve({ok:true}),100))),/unawaited/);
 await assert.rejects(runActualMcpScript('return await jev.evaluate({});',2000,undefined,async()=>{}),/refused/);
 const abort=new AbortController();abort.abort();await assert.rejects(runActualMcpScript('return 1;',2000,abort.signal,async()=>{}),/aborted/);
});
