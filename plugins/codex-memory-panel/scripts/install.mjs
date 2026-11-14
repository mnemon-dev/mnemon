// Install a personal local plugin source. GitHub publication is a separate step.
import { cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const run = promisify(execFile);
const source = fileURLToPath(new URL('../', import.meta.url));
const installRoot = path.join(homedir(), '.codex', 'plugins', 'local', 'mnemon-memory-panel');
const catalogPath = path.join(homedir(), '.agents', 'plugins', 'marketplace.json');
const codex = process.env.CODEX_CLI_PATH || 'codex';
// Read settings before copying the distributable manifests over the local copy.
let previousEnv = {};
for (const name of ['mcp.json', '.mcp.json']) {
  try {
    const previous = JSON.parse(await readFile(path.join(installRoot, name), 'utf8'));
    previousEnv = previous.mcpServers?.['mnemon-memory-panel']?.env || {};
    break;
  } catch (error) { if (error.code !== 'ENOENT') throw error; }
}
const store = process.argv[2] || process.env.MNEMON_UI_STORE || previousEnv.MNEMON_UI_STORE || 'default';
if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(store)) throw new Error('Invalid store name.');
const settings = { MNEMON_UI_STORE: store };
for (const name of ['MNEMON_UI_PYTHON', 'MNEMON_UI_COMMAND', 'MNEMON_UI_DATA_DIR', 'MNEMON_UI_HOST_STATE']) {
  const value = process.env[name] !== undefined ? process.env[name] : previousEnv[name];
  if (typeof value === 'string') settings[name] = value;
}

await mkdir(installRoot, { recursive: true });
await cp(source, installRoot, { recursive: true, filter: file => !file.split(path.sep).some(part => ['.local', 'build'].includes(part)) });
// Resolve the source once; installed startup never relies on the active project cwd.
const mcp = { mcpServers: { 'mnemon-memory-panel': {
  command: process.execPath, args: [path.join(installRoot, 'server', 'index.mjs')], cwd: installRoot,
  env: settings,
  env_vars: ['MNEMON_UI_DATA_DIR', 'MNEMON_UI_COMMAND', 'MNEMON_UI_PYTHON'], tool_timeout_sec: 60,
} } };
await writeFile(path.join(installRoot, '.mcp.json'), JSON.stringify(mcp, null, 2) + '\n');
await writeFile(path.join(installRoot, 'mcp.json'), JSON.stringify({
  $schema: 'https://agent-plugins.org/schemas/1.0.0/mcp.schema.json',
  mcpServers: { 'mnemon-memory-panel': {
    type: 'stdio', command: 'node', args: ['${PLUGIN_ROOT}/server/index.mjs'], cwd: './',
    env: mcp.mcpServers['mnemon-memory-panel'].env,
  } },
}, null, 2) + '\n');
let catalog;
try { catalog = JSON.parse(await readFile(catalogPath, 'utf8')); }
catch (error) { if (error.code !== 'ENOENT') throw error; catalog = { name: 'personal-local', interface: { displayName: 'Personal Local Plugins' }, plugins: [] }; }
if (!Array.isArray(catalog.plugins)) throw new Error('Existing marketplace plugins field is invalid.');
if (!catalog.plugins.some(item => item.name === 'mnemon-memory-panel')) catalog.plugins.push({
  name: 'mnemon-memory-panel', source: { source: 'local', path: './.codex/plugins/local/mnemon-memory-panel' },
  policy: { installation: 'AVAILABLE', authentication: 'ON_INSTALL' }, category: 'Developer Tools',
});
await mkdir(path.dirname(catalogPath), { recursive: true });
await writeFile(catalogPath, JSON.stringify(catalog, null, 2) + '\n');
// The host may remove old cache files while an existing conversation still
// references that version. Retain those runtime files through local upgrades.
const cacheRoot = path.join(homedir(), '.codex', 'plugins', 'cache', catalog.name, 'mnemon-memory-panel');
let versions = [];
try { versions = (await readdir(cacheRoot, { withFileTypes: true })).filter(item => item.isDirectory() && /^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]+)?$/.test(item.name)).map(item => item.name); }
catch (error) { if (error.code !== 'ENOENT') throw error; }
const retained = versions.length ? await mkdtemp(path.join(tmpdir(), 'mnemon-panel-upgrade-')) : undefined;
try {
  for (const version of versions) await cp(path.join(cacheRoot, version), path.join(retained, version), { recursive: true });
  try {
    const { stdout } = await run(codex, ['plugin', 'add', `mnemon-memory-panel@${catalog.name}`, '--json'], { encoding: 'utf8', timeout: 45000, windowsHide: true });
    process.stdout.write(stdout);
  } finally {
    for (const version of versions) await cp(path.join(retained, version), path.join(cacheRoot, version), { recursive: true, force: false, errorOnExist: false });
  }
} catch (error) {
  if (retained) process.stderr.write(`Retained previous runtime files at ${retained}\n`);
  throw error;
}
if (retained) await rm(retained, { recursive: true, force: true });
process.stdout.write(`Installed Mnemon for store '${store}'. Reload plugins or restart the desktop app to discover entrypoints.\n`);
