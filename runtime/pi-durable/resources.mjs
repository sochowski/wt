import { existsSync, readFileSync, readdirSync, realpathSync, statSync } from 'node:fs';
import { basename, dirname, join } from 'node:path';
import { homedir } from 'node:os';

const contextNames = ['AGENTS.override.md', 'AGENTS.md', 'AGENTS.MD', 'CLAUDE.md', 'CLAUDE.MD'];
function contextFile(directory) {
  for (const name of contextNames) {
    const path = join(directory, name);
    if (existsSync(path) && statSync(path).isFile()) return { path: realpathSync(path), content: readFileSync(path, 'utf8').replace(/^\uFEFF/, '') };
  }
}
/** Context is nonexecuting input, global first then ancestors. No settings/extension discovery. */
export function projectContext(cwd, agentDir) {
  const ancestors = [];
  for (let d = realpathSync(cwd); ; d = dirname(d)) {
    ancestors.unshift(d);
    if (dirname(d) === d) break;
  }
  const seen = new Set();
  return [agentDir, ...ancestors].map(contextFile).filter(item => item && !seen.has(item.path) && seen.add(item.path));
}
/** Deliberately bounded scalar frontmatter; unsupported YAML is visible, never executed. */
export function discoverSkills(cwd, agentDir, trustProject = false) {
  const skills = [], diagnostics = [], seen = new Set(), names = new Set();
  function scan(directory, depth = 0) {
    if (!existsSync(directory)) return;
    const canonical = realpathSync(directory);
    if (seen.has(canonical)) return;
    seen.add(canonical);
    if (depth > 12 || seen.size > 256) throw new Error('Skill discovery limit exceeded');
    const filePath = join(directory, 'SKILL.md');
    if (existsSync(filePath)) {
      const raw = readFileSync(filePath, 'utf8');
      const header = raw.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/)?.[1];
      const fields = {};
      if (header) for (const line of header.split(/\r?\n/)) {
        const match = line.match(/^([\w-]+):\s*(.*?)\s*$/);
        if (match) fields[match[1]] = match[2].replace(/^(['"])(.*)\1$/, '$2');
      }
      const name = fields.name || basename(directory), description = fields.description;
      if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(name) || name.length > 64 || !description || description.length > 1024 || /^[>|]/.test(description)) {
        diagnostics.push(`Unsupported skill frontmatter: ${filePath}`);
      } else if (names.has(name)) diagnostics.push(`Duplicate skill name: ${name}`);
      else { names.add(name); skills.push({ name, description, filePath, disabled: fields['disable-model-invocation'] === 'true' }); }
      return;
    }
    for (const entry of readdirSync(directory, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
      const child = join(directory, entry.name);
      if (entry.isDirectory() || (entry.isSymbolicLink() && existsSync(child) && statSync(child).isDirectory())) scan(child, depth + 1);
    }
  }
  scan(join(agentDir, 'skills'));
  scan(join(homedir(), '.agents', 'skills')); // Includes install.sh's nonexecuting WT skill symlink.
  // Protected project resources require explicit per-launch consent, not implicit settings.
  if (trustProject) {
    scan(join(cwd, '.pi', 'skills'));
    scan(join(cwd, '.agents', 'skills'));
  }
  return { skills, diagnostics };
}
export function resourcePrompt(cwd, agentDir, trustProject = false) {
  const { skills, diagnostics } = discoverSkills(cwd, agentDir, trustProject);
  const context = projectContext(cwd, agentDir).map(f => `Instructions from ${JSON.stringify(f.path)}:\n${f.content}`).join('\n\n');
  const available = skills.filter(s => !s.disabled);
  return { skills, diagnostics, prompt: `${context}\n\nAvailable skills (read the named file before applying; relative paths resolve from its directory):\n${available.map(s => JSON.stringify(s)).join('\n')}` };
}

/** Public pi-ai CredentialStore; never executes key commands or refreshes credentials. */
export function readOnlyCredentials(authPath) {
  const load = () => {
    if (!existsSync(authPath)) return {};
    try { const data = JSON.parse(readFileSync(authPath, 'utf8')); if (!data || Array.isArray(data) || typeof data !== 'object') throw new Error(); return data; }
    catch { throw new Error('Cannot read Pi credentials (contents suppressed)'); }
  };
  return {
    async read(provider) {
      const c = load()[provider];
      if (!c) return undefined;
      if (c.type === 'api_key' && typeof c.key === 'string' && c.key && !c.key.startsWith('!')) return structuredClone(c);
      if (c.type === 'oauth' && typeof c.access === 'string' && typeof c.refresh === 'string' && Number.isFinite(c.expires) && c.expires > Date.now() + 120000) return structuredClone(c);
      throw new Error('Unsupported or expired Pi credential; use ordinary Pi to configure authentication');
    },
    async list() { return Object.entries(load()).map(([providerId, c]) => ({ providerId, type: c.type })); },
    async modify() { throw new Error('Durable WT authentication is read-only; refresh/login is not supported'); },
    async delete() { throw new Error('Durable WT authentication is read-only; logout is not supported'); },
  };
}
