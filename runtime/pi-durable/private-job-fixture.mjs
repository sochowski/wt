// Test-only compiled-WT fixture entrypoint. Never used by the production launcher.
// Injects the published faux provider; all runtime/store/tool/job code is production.
import { readFileSync, closeSync, fstatSync, statSync, appendFileSync, existsSync, writeFileSync } from 'node:fs';
import { createModels } from '@earendil-works/pi-ai/models';
import { fauxProvider, fauxAssistantMessage, fauxToolCall } from '@earendil-works/pi-ai/providers/faux';
import { openRuntime, stateCommand, setHostCapability, humanSelfStop } from './runtime.mjs';
import { interactive } from './tui.mjs';
import { runJob } from './jobs.mjs';
const mode=process.argv[2];
const launch=mode==='bootstrap'?JSON.parse(readFileSync(0,'utf8')):{root:process.env.WT_ROOT_ID,agent:process.env.WT_AGENT_ID,runtime:process.env.WT_RUNTIME_ID,identity:JSON.parse(process.argv[3])};
for(const [fd,path] of [[3,`${process.env.WT_DB}.agent-${launch.agent}.lock`],[4,`${launch.identity.store}.wt-lock`],...(mode!=='bootstrap'&&!launch.identity.read_only?[[5,launch.identity.writer_lock]]:[])]) {
  const inherited=fstatSync(fd),file=statSync(path);if(inherited.ino!==file.ino || inherited.dev!==file.dev)throw new Error('fixture missing actual inherited owner lock');
}
if(mode!=='bootstrap'){setHostCapability(readFileSync(6,'utf8'));closeSync(6);}
if(mode==='bootstrap'&&launch.identity.role==='writer'&&process.env.WT_FIXTURE_FAULT==='bootstrap-failure'){appendFileSync(`${launch.identity.store}.bootstrap-failed-starts`,'once\n');console.error('PRIVATE_BOOTSTRAP_FAILURE');process.exit(1);}
const faux=fauxProvider();const models=createModels();models.setProvider(faux.provider);
if(mode==='job'){
  if(launch.identity.role==='writer'&&['startup-exit-one','startup-exit-zero'].includes(process.env.WT_FIXTURE_FAULT)) {
    appendFileSync(`${launch.identity.store}.failed-starts`,'once\n');
    console.error('PRIVATE_UNPUBLISHED_HOST_FAILURE');process.exit(process.env.WT_FIXTURE_FAULT==='startup-exit-one'?1:0);
  }
  const validation = 'set -e; deny() { if "$@" >/dev/null 2>&1; then echo "unadmitted WT mutation succeeded"; exit 1; fi; }; test "$(cat input.txt)" = after; deny "$WT_STATE" worktree agents create "$WT_ROOT_ID" nested --backend native; deny "$WT_STATE" worktree agents create "$WT_ROOT_ID" nested --backend durable; deny "$WT_STATE" worktree agents stop "$WT_ROOT_ID" "$WT_AGENT_ID"; deny "$WT_STATE" worktree view create "$WT_ROOT_ID" editor root; deny "$WT_STATE" worktree _durable-human-stop </dev/null; deny "$WT_STATE" set "$WT_SESSION" --status error; deny "$WT_STATE" delete "$WT_SESSION"; deny "$WT_STATE" migrate; deny "$WT_STATE" agents install-hooks --home "$HOME/forbidden-hooks" --template-dir "$WT_FIXTURE_HOOK_TEMPLATES"; deny "$WT_STATE" agent pi session-setup --dir "$PWD"; test ! -e "$HOME/forbidden-hooks"; test ! -e /dev/fd/6';
  const job=await stateCommand(['_durable-jobs','host'],{root:launch.root,child:launch.agent,runtime:launch.runtime});
  const criteria=job.contract.criteria.map((c,id)=>({id,reads:['review-read'],status:'satisfied',evidence:'Independently read fixture; host command/change evidence checked.'}));
  const common={version:1,taskId:job.id,resultId:job.result_id,criteria,risks:[]};
  if(launch.identity.role==='writer')faux.setResponses([
    fauxAssistantMessage([fauxToolCall('write',{path:'input.txt',content:'after'},{id:'write'})],{stopReason:'toolUse'}),
    fauxAssistantMessage([fauxToolCall('bash',{command:validation,timeout:2},{id:'check'})],{stopReason:'toolUse'}),
    fauxAssistantMessage(JSON.stringify({...common,status:'succeeded',changedFiles:['input.txt'],commands:[{command:validation,result:'passed'}]})),
  ]);
  else faux.setResponses([
    fauxAssistantMessage([fauxToolCall('read',{path:'input.txt'},{id:'review-read'})],{stopReason:'toolUse'}),
    fauxAssistantMessage(JSON.stringify({...common,reviewId:job.review_id,verdict:'accepted',findings:[]})),
  ]);
}
if(mode==='interactive') {
  if (!launch.identity.read_only) {
    const deck={title:'Durable proof',scenes:[{title:'Managed target',narrative:'Private presentation',artifact:{kind:'markdown',content:'# DURABLE_MANAGED_DECK'}}]};
    const responses=Array.from({length:3},(_,i)=>[
      fauxAssistantMessage([fauxToolCall('wt_present_deck',deck,{id:`present-${i}`})],{stopReason:'toolUse'}),fauxAssistantMessage(`PRESENT_PROOF_${i}`),
    ]).flat();
    if(launch.identity.profile==='native-compat-v2')responses.push(
      fauxAssistantMessage(fauxToolCall('ask_user_question',{questions:[{question:'ACTUAL_PRIVATE_PLUGIN_QUESTION?',header:'Private',options:[{label:'Alpha',description:'First'},{label:'Beta',description:'Second'}]}]},{id:'private-question'}),{stopReason:'toolUse'}),fauxAssistantMessage('ACTUAL_PLUGIN_QUESTION_DONE'),
      fauxAssistantMessage(fauxToolCall('todo',{action:'create',subject:'ACTUAL_PRIVATE_TODO'},{id:'private-todo'}),{stopReason:'toolUse'}),fauxAssistantMessage('ACTUAL_PLUGIN_TODO_DONE'));
    faux.setResponses(responses);
  } else faux.setResponses([fauxAssistantMessage('INBOX_FIXTURE_OK')]);
}
if(mode!=='bootstrap') {
  const stream=models.streamSimple.bind(models);
  models.streamSimple=(...args)=>{appendFileSync(`${launch.identity.store}.calls`,'1\n');return stream(...args);};
}
const runtime=await openRuntime(launch,{models,...(mode==='bootstrap'?{bootstrap:true,model:{provider:'faux',modelId:'faux-1'},fence:async()=>{}}:{})});
if(mode==='bootstrap'){await runtime.close();if(runtime.plugins)console.log(JSON.stringify({plugin_source:launch.identity.plugin_source,plugin_contract:launch.identity.plugin_contract}));}
else if(mode==='job'){
  const command=async(args,input)=>{
    if(args[1]==='finish' && launch.identity.role==='writer' && process.env.WT_FIXTURE_FAULT==='publication' && !existsSync(`${launch.identity.store}.kill-seen`)) {
      writeFileSync(`${launch.identity.store}.kill-seen`,'once');
      writeFileSync(`${launch.identity.store}.kill-ready`,String(process.pid));
      setInterval(()=>{},1000);
      await new Promise(()=>{}); // Actual SIGKILL after durable result, before WT publication.
    }
    return stateCommand(args,input);
  };
  try{await runJob(runtime,command);}finally{await runtime.close();}
}
else await interactive(runtime,{selfStop:async launch=>{
  try { return await humanSelfStop(launch); } catch(error) { writeFileSync(`${launch.identity.store}.self-stop-error`,error.stderr || 'host self-stop rejected'); throw error; }
},commandTransport:async(args,input)=>{
  if(args[0]==='message' && args[1]==='ack' && args[3]==='request-kill-proof' && process.env.WT_FIXTURE_INBOX_FAULT==='before-ack' && !existsSync(`${launch.identity.store}.inbox-kill-seen`)) {
    writeFileSync(`${launch.identity.store}.inbox-kill-seen`,'once');
    writeFileSync(`${launch.identity.store}.inbox-kill-ready`,String(process.pid));
    setInterval(()=>{},1000);await new Promise(()=>{});
  }
  return stateCommand(args,input);
}});
