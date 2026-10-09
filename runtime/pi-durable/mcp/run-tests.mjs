import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
const home = await mkdtemp(join(tmpdir(), 'wt-mcp-tests-'));
try {
  await mkdir(join(home,'pi'));
  const child = spawn(process.execPath, ['--test', 'sdk.test.mjs', 'adapter.test.mjs', 'dispatch.test.mjs', 'crash.test.mjs'], {
    cwd: new URL('.', import.meta.url), stdio:'inherit',
    env: { PATH:process.env.PATH, HOME:home, TMPDIR:home, PI_CODING_AGENT_DIR:join(home,'pi'), XDG_CONFIG_HOME:join(home,'config'), WT_STATUS_DIR:join(home,'state'), WT_DB:join(home,'wt.db'), WT_BASE_DIR:join(home,'worktrees') },
  });
  const [code] = await once(child,'exit'); process.exitCode = code ?? 1;
} finally { await rm(home,{recursive:true,force:true}); }
