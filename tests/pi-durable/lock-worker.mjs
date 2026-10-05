import { Harness, createRegistry } from '@earendil-works/pi-durable';
import { createModels } from '@earendil-works/pi-ai/models';
import { BACKGROUND_CONTEXT as context } from '@earendil-works/chord/context';
import { openNodeSqliteStorage } from '@earendil-works/pi-durable/storage/sqlite/node';
const harness = await Harness.open(await openNodeSqliteStorage(process.argv[2]), { models: createModels(), registry: createRegistry() }, context);
process.send({ type: 'opened' });
await harness.close(context);
process.disconnect();
