// Test-only real SIGKILL cut after a real unsafe write, before its host receipt.
import { readFileSync, appendFileSync } from 'node:fs';
import { createModels } from '@earendil-works/pi-ai/models';
import { fauxProvider, fauxAssistantMessage, fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { openRuntime } from './runtime.mjs';
import { runJob } from './jobs.mjs';
const {launch,job}=JSON.parse(readFileSync(process.argv[2],'utf8'));
const faux=fauxProvider();faux.setResponses([fauxAssistantMessage([fauxToolCall('write',{path:'input.txt',content:'after'},{id:'cut-write'})],{stopReason:'toolUse'})]);
const models=createModels();models.setProvider(faux.provider);
const node=new NodeExecutionEnv({cwd:launch.identity.cwd});
const environment=new Proxy(node,{get(target,key){
  if(key==='writeFile')return async(...args)=>{
    const result=await target.writeFile(...args);if(!result.ok)throw new Error('fixture write failed');
    appendFileSync(`${launch.identity.store}.effects`,'1\n');
    process.send({effect:true});setInterval(()=>{},1000);await new Promise(()=>{});
  };
  const value=Reflect.get(target,key);return typeof value==='function'?value.bind(target):value;
}});
const runtime=await openRuntime(launch,{models,environment,fence:async()=>{}});
await runJob(runtime,async args=>{if(args[1]==='host')return job;throw new Error('fixture must stop before publication');});
