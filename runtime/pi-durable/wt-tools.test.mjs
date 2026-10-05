import test from 'node:test';
import assert from 'node:assert/strict';
import { wtTools } from './wt-tools.mjs';
test('durable presentation resolves assigned target and versioned managed transport; never legacy wt-present',async()=>{
 const launch={root:'root-id',identity:{cwd:'/assigned/checkout'}},calls=[];
 const command=async(args,input)=>{calls.push({args,input});if(args[0]==='workspace')return {cwd:'/different/root',checkouts:[{id:'checkout-id',path:launch.identity.cwd}]};return {ok:true,view_id:'managed'};};
 let fences=0;const tool=wtTools(launch,async()=>fences++,command).find(t=>t.name==='wt_present_deck');
 const scenes=[{type:'markdown',content:'# Review'}];await tool.execute({title:'Deck',scenes},{});
 assert.equal(fences,1);assert.deepEqual(calls[1],{args:['present','root-id','checkout-id'],input:{version:1,title:'Deck',startIndex:1,scenes}});
 await assert.rejects(wtTools(launch,async()=>{},async()=>({cwd:'/elsewhere',checkouts:[]})).find(t=>t.name==='wt_present_deck').execute({scenes},{}),/no longer attached/);
});
