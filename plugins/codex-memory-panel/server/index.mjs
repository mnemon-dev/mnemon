import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import { z } from 'zod';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { MnemonAdapter } from './adapter.mjs';
import { LiveMemoryService } from './live.mjs';
import { MemoryScopes, validScope } from './scopes.mjs';
import { GLOBAL_STORE } from './projects.mjs';
import { fileURLToPath } from 'node:url';

const resourceUri = 'ui://mnemon/memory-panel/v1';
const uiDir = new URL('../ui/', import.meta.url);
const adapter = new MnemonAdapter();
const scopes = new MemoryScopes(adapter);
const service = new LiveMemoryService(scopes);
const icon = 'data:image/svg+xml;base64,' + Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="none" stroke="#777" stroke-width="1.4"><path d="M10 4C7 1 3 4 4 7c-3 2-1 6 1 6-1 3 3 5 5 2 2 3 6 1 5-2 3-1 4-4 1-6 1-3-3-6-6-3Z"/><path d="M10 4v11M6 7l4 2 4-2M6 12l4-1 4 1"/></svg>').toString('base64');

async function html() {
  const [template, css, js] = await Promise.all(['index.html', 'style.css', 'app.js'].map(file => readFile(new URL(file, uiDir), 'utf8')));
  return template.replace('<!--STYLE-->', `<style>${css}</style>`).replace('<!--SCRIPT-->', `<script>${js}</script>`);
}

// An installed package may be replaced while an existing host session still
// uses this process. Keep its UI resource available without reopening files.
const packagedHtml = await html();

function result(snapshot) {
  return { content: [{ type: 'text', text: snapshot.connection?.message || `Mnemon ${snapshot.store}: ${snapshot.stats?.total ?? 'unchanged'} memories.` }], structuredContent: snapshot };
}

const server = new McpServer({ name: 'mnemon-memory-panel', title: 'Mnemon', version: '0.2.3', icons: [{ src: icon, mimeType: 'image/svg+xml' }] });
scopes.registry.roots = async () => {
  if (!server.server.getClientCapabilities()?.roots) return [];
  const value = await server.server.listRoots({}, { timeout: 2000 });
  return value.roots.filter(root => root.uri.startsWith('file:')).map(root => ({ name: root.name, path: fileURLToPath(root.uri) }));
};
const scopeSchema = z.string().refine(validScope, 'Invalid memory scope');
async function context({ directory, query }) {
  const stores = await adapter.stores();
  const project = await scopes.registry.resolve(directory, stores);
  const targets = [...new Set([project?.store, GLOBAL_STORE].filter(Boolean))];
  const memories = [];
  for (const store of targets) if (stores.includes(store)) {
    const value = await adapter.read(store, query);
    memories.push(...value.memories.map(memory => ({ ...memory, store, scope: store === GLOBAL_STORE ? 'global' : 'project' })));
  }
  return { project, globalStore: GLOBAL_STORE, memories, unassociated: !project?.associated };
}
server.registerResource('memory-panel', resourceUri, { mimeType: 'text/html;profile=mcp-app' }, async () => ({ contents: [{
  uri: resourceUri, mimeType: 'text/html;profile=mcp-app', text: packagedHtml,
  _meta: { ui: { prefersBorder: false, csp: { connectDomains: [], resourceDomains: [] } } },
}] }));
server.registerTool('open_mnemon', {
  title: 'Mnemon', description: 'Open the local Mnemon memory panel. Inspect real memories, search and see updates automatically.', inputSchema: {},
  icons: [{ src: icon, mimeType: 'image/svg+xml' }], annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  _meta: { ui: { resourceUri, visibility: ['app', 'model'] }, 'openai/outputTemplate': resourceUri, 'openai/widgetAccessible': true,
    'openai/ui': { entrypoints: [{ type: 'global' }, { type: 'thread' }] } },
}, async (_, extra) => result(await service.snapshot({}, extra.signal)));
server.registerTool('mnemon_snapshot', {
  title: 'Read live Mnemon memories', description: 'Read a normalized memory snapshot, or wait for its revision to change. Read-only; no recall counters or source data are modified.',
  inputSchema: { store: scopeSchema.optional(), revision: z.string().max(128).optional(), waitMs: z.number().int().min(0).max(20000).optional() },
  annotations: { readOnlyHint: true, destructiveHint: false, openWorldHint: false },
  _meta: { ui: { visibility: ['app', 'model'] }, 'openai/widgetAccessible': true },
}, async (args, extra) => result(await service.snapshot(args, extra.signal)));
server.registerTool('mnemon_projects', {
  title: 'List available memory projects', description: 'Discover local projects, project-store associations, global memory and existing Mnemon stores.', inputSchema: {},
  annotations: { readOnlyHint: true }, _meta: { ui: { visibility: ['app', 'model'] }, 'openai/widgetAccessible': true },
}, async () => result(await scopes.catalog()));
server.registerTool('mnemon_associate_project', {
  title: 'Associate a project with memory', description: 'Save a local project-directory association. Does not migrate memories, change hooks or switch the engine active store.',
  inputSchema: { directory: z.string().min(1).max(4096), name: z.string().min(1).max(100), store: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/).optional() },
  annotations: { readOnlyHint: false, destructiveHint: false, openWorldHint: false }, _meta: { ui: { visibility: ['app', 'model'] }, 'openai/widgetAccessible': true },
}, async args => result(await scopes.registry.associate(args)));
server.registerTool('mnemon_context', {
  title: 'Recall project and global experience', description: 'Before development, recall relevant verified experience for the current directory and the global library. Returns project/store information for explicit later writes.',
  inputSchema: { directory: z.string().min(1).max(4096), query: z.string().min(1).max(500) },
  annotations: { readOnlyHint: true, openWorldHint: false },
}, async args => result(await context(args)));
server.registerTool('mnemon_remember', {
  title: 'Remember verified experience', description: 'Explicitly save verified knowledge to a selected project or global library after development. Project-specific pitfalls stay in that project; transferable lessons may go to global. Inspect action=skipped for duplicates; never write to an aggregate view.',
  inputSchema: { scope: scopeSchema, content: z.string().min(1).max(8000), category: z.enum(['insight', 'decision', 'preference', 'context', 'fact', 'general']).optional(), source: z.enum(['user', 'agent']).optional() },
  annotations: { readOnlyHint: false, destructiveHint: false, openWorldHint: false }, _meta: { ui: { visibility: ['app', 'model'] }, 'openai/widgetAccessible': true },
}, async args => result(await scopes.remember(args)));

let preview;
if (process.argv.includes('--preview')) {
  const port = Number(process.env.MNEMON_UI_PORT || 43187);
  preview = createServer(async (req, res) => {
    const base = `http://127.0.0.1:${port}`;
    if (req.headers.host !== `127.0.0.1:${port}` || (req.headers.origin && req.headers.origin !== base)) { res.writeHead(403).end(); return; }
    const url = new URL(req.url, base);
    res.setHeader('Cache-Control', 'no-store');
    res.setHeader('X-Content-Type-Options', 'nosniff');
    try {
      if (req.method === 'POST' && ['/api/associate', '/api/remember'].includes(url.pathname)) {
        if (req.headers.origin !== base || req.headers['content-type'] !== 'application/json') { res.writeHead(403).end(); return; }
        const chunks = []; let bytes = 0;
        for await (const chunk of req) { bytes += chunk.length; if (bytes > 20000) throw new Error('Request too large.'); chunks.push(chunk); }
        const args = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const data = url.pathname === '/api/associate' ? await scopes.registry.associate(args) : await scopes.remember(args);
        res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(data));
      } else if (req.method !== 'GET') { res.writeHead(405).end(); return; }
      else if (url.pathname === '/') {
        res.setHeader('Content-Type', 'text/html; charset=utf-8');
        res.setHeader('Content-Security-Policy', "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src data:; base-uri 'none'; frame-ancestors 'none'");
        res.end(await html());
      } else if (url.pathname === '/api/projects') {
        res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(await scopes.catalog()));
      } else if (url.pathname === '/api/snapshot') {
        const cancellation = new AbortController();
        res.on('close', () => cancellation.abort());
        const waitMs = Math.min(20000, Math.max(0, Number(url.searchParams.get('waitMs')) || 0));
        const data = await service.snapshot({ store: url.searchParams.get('store') || undefined, revision: url.searchParams.get('revision') || undefined, waitMs }, cancellation.signal);
        if (!res.destroyed) { res.setHeader('Content-Type', 'application/json; charset=utf-8'); res.end(JSON.stringify(data)); }
      } else res.writeHead(404).end();
    } catch (error) { res.writeHead(503, { 'Content-Type': 'application/json' }).end(JSON.stringify({ error: error.message })); }
  });
  preview.listen(port, '127.0.0.1', () => process.stderr.write(`Mnemon preview: http://127.0.0.1:${port}\n`));
} else await server.connect(new StdioServerTransport());

async function shutdown() { service.close(); preview?.close(); await server.close(); }
process.once('SIGINT', () => shutdown().finally(() => process.exit(0)));
process.once('SIGTERM', () => shutdown().finally(() => process.exit(0)));
if (!preview) process.stdin.once('end', () => shutdown().catch(() => {}));
