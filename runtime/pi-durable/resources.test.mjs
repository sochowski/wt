import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { resourcePrompt, readOnlyCredentials } from './resources.mjs';

test('context precedence, scalar skill discovery, trust and no executable/project settings load', async t => {
  const home = await mkdtemp(join(tmpdir(), 'wt-resources-')); t.after(() => rm(home,{recursive:true,force:true}));
  const cwd = join(home,'repo'), agent = join(home,'agent');
  await mkdir(join(cwd,'.pi','skills','local'),{recursive:true}); await mkdir(join(agent,'skills','global'),{recursive:true});
  await writeFile(join(home,'AGENTS.md'),'ancestor instructions');
  await writeFile(join(cwd,'AGENTS.md'),'ordinary instructions'); await writeFile(join(cwd,'AGENTS.override.md'),'override instructions');
  await writeFile(join(agent,'AGENTS.md'),'global instructions');
  await writeFile(join(agent,'skills','global','SKILL.md'),'---\nname: global\ndescription: Global helper\n---\nRun nothing automatically.');
  await writeFile(join(cwd,'.pi','skills','local','SKILL.md'),'---\nname: local\ndescription: Local helper\n---\nLocal body');
  await writeFile(join(cwd,'.pi','settings.json'),'not valid JSON');
  let r = resourcePrompt(cwd,agent);
  assert.ok(r.prompt.indexOf('global instructions') < r.prompt.indexOf('ancestor instructions'));
  assert.ok(r.prompt.indexOf('ancestor instructions') < r.prompt.indexOf('override instructions'));
  assert.ok(!r.prompt.includes('ordinary instructions'));
  assert.deepEqual(r.skills.map(s=>s.name),['global']);
  r = resourcePrompt(cwd,agent,true); assert.deepEqual(r.skills.map(s=>s.name),['global','local']);
});

test('read-only credential store accepts standard keys and fresh OAuth; rejects commands, malformed and expired without writes/secrets', async t => {
  const home = await mkdtemp(join(tmpdir(),'wt-auth-')); t.after(()=>rm(home,{recursive:true,force:true}));
  const path = join(home,'auth.json'), store = readOnlyCredentials(path);
  assert.equal(await store.read('anthropic'),undefined);
  const content = JSON.stringify({ anthropic:{type:'oauth',access:'sensitive-access',refresh:'sensitive-refresh',expires:Date.now()+300000}, openai:{type:'api_key',key:'sensitive-key'}, command:{type:'api_key',key:'!touch never'}, old:{type:'oauth',access:'x',refresh:'y',expires:0} });
  await writeFile(path,content);
  assert.equal((await store.read('anthropic')).type,'oauth'); assert.equal((await store.read('openai')).type,'api_key');
  await assert.rejects(store.read('command'),/Unsupported/); await assert.rejects(store.read('old'),/expired/);
  await assert.rejects(store.modify('anthropic',()=>{throw new Error('must not run');}),/read-only/);
  await assert.rejects(store.delete('anthropic'),/read-only/);
  const { readFile } = await import('node:fs/promises'); assert.equal(await readFile(path,'utf8'),content);
  assert.ok(!(JSON.stringify(await store.list())).includes('sensitive'));
  await writeFile(path,'secret-invalid'); await assert.rejects(store.read('anthropic'),e=>!e.message.includes('secret-invalid'));
});
