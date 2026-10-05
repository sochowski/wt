// Deterministic regression for provider-owned handles surviving a settled run.
import { readFileSync } from 'node:fs';
import { createModels } from '@earendil-works/pi-ai/models';
import { registerSessionResourceCleanup } from '@earendil-works/pi-ai';
import { fauxProvider, fauxAssistantMessage } from '@earendil-works/pi-ai/providers/faux';
import { ProviderDoc } from '@earendil-works/pi-durable';
import { openRuntime, context } from './runtime.mjs';
const launch=JSON.parse(readFileSync(process.argv[2],'utf8'));
const faux=fauxProvider();faux.setResponses([fauxAssistantMessage('settled')]);const models=createModels();models.setProvider(faux.provider);
const r=await openRuntime(launch,{models,fence:async()=>{}});
await (await r.input('settle')).wait(context);
const provider=await r.harness.snapshot(ProviderDoc,r.root.id,context);
const retainedHandle=setInterval(()=>{},1000);
const unregister=registerSessionResourceCleanup(id=>{if(id===provider.sessionId)clearInterval(retainedHandle);});
await r.close();unregister();
console.log('EXACT_PROVIDER_RESOURCE_CLOSE_OK');
// Must exit naturally; no process.exit to conceal retained provider resources.
