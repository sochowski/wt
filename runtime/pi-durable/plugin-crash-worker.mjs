// Private actual-package process-crash fixture; never a production entrypoint.
import { readFileSync, appendFileSync } from 'node:fs';
import { createModels } from '@earendil-works/pi-ai';
import { fauxProvider, fauxAssistantMessage, fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { openRuntime } from './runtime.mjs';
import { interactive } from './tui.mjs';
const launch=JSON.parse(readFileSync(process.argv[2],'utf8')),boundary=process.argv[3];
setInterval(()=>{},1000); // Keep only this owned crash fixture alive at an awaited checkpoint.
const models=createModels(),faux=fauxProvider();models.setProvider(faux.provider);
const question={questions:[{question:'CRASH_QUESTION?',header:'Crash',options:[{label:'Alpha',description:'First'},{label:'Beta',description:'Second'}]}]};
faux.setResponses([fauxAssistantMessage(fauxToolCall(boundary==='candidate'||boundary==='receipted'?'todo':'ask_user_question',boundary==='candidate'||boundary==='receipted'?{action:'create',subject:'CRASH_TODO'}:question,{id:'crash-call'}),{stopReason:'toolUse'}),()=>new Promise(()=>{})]);
const stream=models.streamSimple.bind(models);models.streamSimple=(...args)=>{appendFileSync(`${launch.identity.store}.model-calls`,'1\n');return stream(...args);};
const runtime=await openRuntime(launch,{models,fence:async()=>{},reportStatus:async()=>{},pluginObserver:async event=>{if(event.stage===boundary){if(boundary==='receipted')await runtime.root.submit({type:'input',content:'Retained queue is NOT human continuation authorization',whenBusy:'followUp',requestId:'retained-queued-input'},(await import('./runtime.mjs')).context);process.send(event);await new Promise(()=>{});}}});
class Terminal {
 columns=100;rows=30;kittyProtocolActive=false;output='';started=false;
 start(input,resize){this.input=input;this.resize=resize;this.started=true;}stop(){}write(s){this.output+=s;if(boundary==='answered'&&this.output.includes('CRASH_QUESTION?')&&!this.answered){this.answered=true;setTimeout(()=>this.input('\r'),50);}}
 hideCursor(){}showCursor(){}moveBy(){}clearLine(){}clearFromCursor(){}clearScreen(){}setTitle(){}setProgress(){}async drainInput(){}
}
const terminal=new Terminal(),ui=interactive(runtime,{terminal,poll:false});
while(!terminal.started)await new Promise(resolve=>setTimeout(resolve,10));
for(const c of 'crash request')terminal.input(c);terminal.input('\r');await ui;
