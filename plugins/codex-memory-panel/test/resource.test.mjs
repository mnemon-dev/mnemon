import test from 'node:test';
import assert from 'node:assert/strict';
import { cp, mkdtemp, rename, rm, symlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

const packageRoot = fileURLToPath(new URL('../', import.meta.url));
test('an active MCP resource survives replacement of its UI directory', async () => {
  const fixture = await mkdtemp(path.join(tmpdir(), 'mnemon-panel-resource-'));
  const client = new Client({ name: 'resource-check', version: '1' });
  try {
    for (const directory of ['server', 'ui']) await cp(path.join(packageRoot, directory), path.join(fixture, directory), { recursive: true });
    await symlink(path.join(packageRoot, 'node_modules'), path.join(fixture, 'node_modules'), process.platform === 'win32' ? 'junction' : 'dir');
    await client.connect(new StdioClientTransport({ command: process.execPath, args: [path.join(fixture, 'server', 'index.mjs')], stderr: 'pipe' }));
    const tools = await client.listTools();
    const open = tools.tools.find(tool => tool.name === 'open_mnemon');
    assert.deepEqual(open._meta['openai/ui'].entrypoints, [{ type: 'global' }, { type: 'thread' }]);
    const uri = open._meta.ui.resourceUri;
    const before = await client.readResource({ uri });
    assert.equal(before.contents[0].mimeType, 'text/html;profile=mcp-app');
    assert.match(before.contents[0].text, /ui\/initialize/);
    await rename(path.join(fixture, 'ui'), path.join(fixture, 'retired-ui'));
    const after = await client.readResource({ uri });
    assert.equal(after.contents[0].text, before.contents[0].text);
  } finally {
    await client.close();
    await rm(fixture, { recursive: true, force: true });
  }
});
