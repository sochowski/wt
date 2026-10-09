// Private actual-Harness fixture; not a production WT profile launch.
import { Harness, createRegistry } from '@earendil-works/pi-durable';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
import { NodeExecutionEnv } from '@earendil-works/pi-durable/env/node';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { createModels } from '@earendil-works/pi-ai';
import { fauxProvider } from '@earendil-works/pi-ai/providers/faux';
export async function rawRuntime(launch, responses) {
  const registry=createRegistry(),models=createModels(),faux=fauxProvider();faux.setResponses(responses);models.setProvider(faux.provider);
  const harness=await Harness.open(await openNodeSqliteStorage(launch.identity.store),{models,registry,env:()=>new NodeExecutionEnv({cwd:launch.identity.cwd}),settings:{retry:{maxRetries:0},compaction:{enabled:false}}},context);
  const root=await harness.conversation(1,context)||await harness.root(context,{agent:{cwd:launch.identity.cwd,model:{provider:'faux',modelId:'faux-1'},thinkingLevel:'off'}});
  return {launch,harness,root,registry,faux,async fence(){},async install(boundary){
    registry.install(boundary.extension);
    // Harness snapshots hooks at open. Bootstrap has no live generation; reopen
    // with the actual extension installed BEFORE accepting any input.
    await this.harness.close(context);
    this.harness=await Harness.open(await openNodeSqliteStorage(launch.identity.store),{models:boundary.guardModels(models),registry,env:()=>new NodeExecutionEnv({cwd:launch.identity.cwd}),settings:{retry:{maxRetries:0},compaction:{enabled:false}}},context);
    this.root=await this.harness.conversation(1,context);
    await this.root.configure({tools:registry.snapshot().tools().map(item=>item.tool)},context);
  },async close(){await this.harness.close(context);}};
}
