import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
const home = await mkdtemp(join(tmpdir(), 'wt-durable-runtime-tests-'));
try {
  const child = spawn(process.execPath, ['--test', 'runtime.test.mjs', 'resources.test.mjs', 'jobs.test.mjs', 'wt-tools.test.mjs', 'install.test.mjs', 'presentation.test.mjs'], { cwd: new URL('.', import.meta.url), stdio: 'inherit', env: { PATH: process.env.PATH, HOME: home, PI_CODING_AGENT_DIR: join(home,'pi'), WT_STATUS_DIR: join(home,'state'), WT_DB: join(home,'wt.db'), WT_BASE_DIR: join(home,'worktrees'), WT_CONFIG_DIR: join(home,'config') } });
  const [code] = await once(child,'exit'); process.exitCode = code ?? 1;
} finally { await rm(home, { recursive:true, force:true }); }
