import { createJiti } from 'jiti';
import { readFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

/** Public host-managed entry only. Never load the native extension or auth profile. */
export async function loadHostManagedAdapter() {
  for (const [name, version] of [['pi-mcp-adapter','5.1.0'],['@modelcontextprotocol/client','2.2.0'],['@modelcontextprotocol/core','2.2.0'],['jiti','2.7.0'],['typebox','1.3.27']]) {
    const manifest = JSON.parse(readFileSync(new URL(`./node_modules/${name}/package.json`, import.meta.url), 'utf8'));
    if (manifest.name !== name || manifest.version !== version) throw new Error('Pinned MCP boundary dependency mismatch');
  }
  for (const base of ['./node_modules/', '../node_modules/']) {
    for (const name of ['@earendil-works/pi-coding-agent','@earendil-works/pi-agent-core']) {
      if (existsSync(new URL(`${base}${name}`, import.meta.url))) throw new Error('Native session engines are forbidden in the MCP boundary');
    }
  }
  const sdk = fileURLToPath(new URL('./sdk.mjs', import.meta.url));
  const loader = createJiti(import.meta.url, {
    fsCache: false, moduleCache: false,
    alias: { '@earendil-works/pi-coding-agent': sdk },
    nativeModules: ['typebox', '@modelcontextprotocol/client', '@modelcontextprotocol/core', sdk],
  });
  const module = await loader.import('pi-mcp-adapter/host-managed');
  if (typeof module.createHostManagedMcpAdapter !== 'function') throw new Error('Missing actual public host-managed MCP factory');
  return module.createHostManagedMcpAdapter;
}
