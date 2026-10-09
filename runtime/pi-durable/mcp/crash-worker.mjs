// Owned private fixture process. Never reads production WT state or credentials.
import { readFile } from 'node:fs/promises';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { fauxAssistantMessage, fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { rawRuntime } from './harness-fixture.mjs';
import { initializeMcpBoundary, preflightMcpBoundary, reopenMcpBoundary } from './boundary.mjs';
const {launch,config}=JSON.parse(await readFile(process.argv[2],'utf8')),stage=process.argv[3];
setInterval(()=>{},1000); // A paused checkpoint must remain available for SIGKILL.
const observer=async event=>{
  if(event.stage===stage){process.send({stage});await new Promise(()=>{});}
};
const runtime=await rawRuntime(launch,[fauxAssistantMessage(fauxToolCall('private_echo',{text:'CRASH_EFFECT'},{id:'crash-call'}),{stopReason:'toolUse'}),fauxAssistantMessage('DONE')]);
const options={config,approve:async()=> 'allow_once',observer};
const boundary=stage==='initialization-intent'
  ? await initializeMcpBoundary(runtime,options)
  : await reopenMcpBoundary(runtime,options,await preflightMcpBoundary(launch,config));
await runtime.install(boundary);
const submission=await runtime.root.submit({type:'input',content:'private crash proof'},context);
await submission.wait(context);
throw new Error('Expected crash boundary was not observed');
