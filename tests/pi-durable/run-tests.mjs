import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
const home = await mkdtemp(join(tmpdir(), 'wt-durable-test-home-'));
try {
  const child = spawn(process.execPath, ['--test', 'contracts.test.mjs'], {
    cwd: new URL('.', import.meta.url), stdio: 'inherit',
    env: { PATH: process.env.PATH, HOME: home, WT_STATUS_DIR: join(home, 'state'), WT_DB: join(home, 'wt.db'),
      WT_BASE_DIR: join(home, 'worktrees'), WT_CONFIG_DIR: join(home, 'config'), WT_LOG_FILE: join(home, 'wt.log'), PI_CODING_AGENT_DIR: join(home, 'pi') },
  });
  const [code] = await once(child, 'exit');
  process.exitCode = code ?? 1;
} finally { await rm(home, { recursive: true, force: true }); }
