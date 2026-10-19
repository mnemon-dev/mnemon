import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, writeFile, appendFile, mkdir, symlink, realpath, readdir } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { resolve, join } from 'node:path';
import { compatibilityModel, sendModelReply, normalizeRequest, responseProtocol } from './compatibility-model.mjs';

const args = new Map();
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1]);
assert(args.has('--consumer') && args.has('--output'), 'supply --consumer and --output');
const consumer = await realpath(resolve(args.get('--consumer')));
const output = resolve(args.get('--output'));
const nativeInput = args.get('--native-cli') ?? process.env.MNEMON_CLI_PATH;
assert(nativeInput, 'supply --native-cli or MNEMON_CLI_PATH');
const nativeCli = await realpath(resolve(nativeInput));
const sourceManifest = JSON.parse(await readFile(join(consumer, 'package.json'), 'utf8'));
const lock = JSON.parse(await readFile(join(consumer, 'package-lock.json'), 'utf8'));
const dshManifest = JSON.parse(await readFile(join(consumer, 'node_modules/@deepseek-ai/dsh/package.json'), 'utf8'));
const version = dshManifest.version;
assert(['0.1.5-rc.2', '0.1.6-alpha.2'].includes(version));
const dshPackages = Object.entries(lock.packages).flatMap(([path, entry]) => /(?:^|\/)node_modules\/@deepseek-ai\/dsh(?:-[^/]+)?$/.test(path) ? [{ path, version: entry.version }] : []);
assert(dshPackages.length > 200 && dshPackages.every(entry => entry.version === version), 'headless must reuse one exact published DSH cohort');
const mnemonPackages = Object.entries(lock.packages).filter(([path]) => /^node_modules\/dsh-mnemon(?:-[^/]+)?$/.test(path));
assert.equal(mnemonPackages.length, 17);
assert(mnemonPackages.every(([, entry]) => /^http:\/\/127\.0\.0\.1:\d+\/artifacts\//.test(entry.resolved) && typeof entry.integrity === 'string'), 'all Mnemon packages must have packed loopback provenance');
const home = join(output, 'dsh-home');
const profile = join(home, 'profiles/headless');
const workspace = join(output, 'workspace');
const data = join(output, 'mnemon-data');
await Promise.all([profile, workspace, data].map(path => mkdir(path, { recursive: true })));
const profileManifest = { name: 'issue261-packed-headless', private: true, type: 'module', dependencies: sourceManifest.dependencies, dsh: { profile: { bundles: ['@deepseek-ai/dsh-base', '@deepseek-ai/dsh-headless', 'dsh-mnemon'] } } };
await writeFile(join(profile, 'package.json'), JSON.stringify(profileManifest, null, 2) + '\n');
await symlink(join(consumer, 'node_modules'), join(profile, 'node_modules'), 'dir');
const disabled = ['subprocess', 'bash-sandbox', 'pwsh-sandbox', 'tool-bash', 'tool-pwsh', 'permission', 'tool-fs-search'];
const extensions = ['mnemon-strategy-scoped', 'mnemon-strategy-light-context', 'mnemon-strategy-auto-capture'];
const patch = disabled.map(id => `- id: ${id}\n  disabled: true\n`).join('') + extensions.map(id => `- id: ${id}\n  disabled: false\n`).join('');
const patchFile = join(profile, 'cordis.patch.yml');
await writeFile(patchFile, patch);
await writeFile(join(home, 'settings.yaml'), `mnemon:\n  cliPath: ${JSON.stringify(nativeCli)}\n  idleReview:\n    enabled: false\n  persistenceStrategy:\n    mode: manual\n`);
await writeFile(join(workspace, 'README.md'), '# Issue 261 isolated Headless fixture\n');
const requests = [];
let phase = 'setup';
const decide = compatibilityModel(event => { void appendFile(join(output, 'model-events.jsonl'), JSON.stringify({ phase, ...event }) + '\n'); });
const model = createServer(async (request, response) => {
  try {
    let raw = '';
    for await (const chunk of request) raw += chunk;
    const body = JSON.parse(raw);
    const number = requests.push({ phase, path: request.url, body });
    await appendFile(join(output, 'model-requests.jsonl'), JSON.stringify({ phase, number, path: request.url, protocol: responseProtocol(body, request.url), body }) + '\n');
    const reply = decide(body);
    sendModelReply(response, body, request.url, reply, `issue261-headless-${number}`);
  } catch (error) {
    await appendFile(join(output, 'model-errors.log'), String(error.stack ?? error) + '\n');
    response.writeHead(500, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ error: { message: String(error) } }));
  }
});
await new Promise(done => model.listen(0, '127.0.0.1', done));
const env = { ...process.env, DSH_HOME: home, DSH_TELEMETRY_DISABLED: '1', DSH_TELEMETRY_MODE: 'DISABLED', MNEMON_DATA_DIR: data, MNEMON_CLI_PATH: nativeCli, DEEPSEEK_API_KEY: 'isolated-issue261-headless-key', DEEPSEEK_BASE_URL: `http://127.0.0.1:${model.address().port}` };
for (const key of ['NODE_PATH', 'NODE_OPTIONS', 'npm_config_legacy_peer_deps', 'NPM_CONFIG_LEGACY_PEER_DEPS']) delete env[key];
const cli = join(consumer, 'node_modules/@deepseek-ai/dsh/lib/bin.js');
function run(label, argv, executable = process.execPath) {
  return new Promise((done, reject) => {
    const child = spawn(executable, argv, { cwd: workspace, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', chunk => { stdout += chunk; void appendFile(join(output, `${label}.stdout.log`), chunk); });
    child.stderr.on('data', chunk => { stderr += chunk; void appendFile(join(output, `${label}.stderr.log`), chunk); });
    const timer = setTimeout(() => { child.kill('SIGTERM'); reject(new Error(`${label} timed out after 90000 ms`)); }, 90000);
    child.once('error', reject);
    child.once('exit', code => { clearTimeout(timer); code === 0 ? done({ code, stdout, stderr }) : reject(new Error(`${label} exited ${code}: ${stderr}`)); });
  });
}
async function files(root) {
  const output = [];
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const path = join(root, entry.name);
    if (entry.isDirectory()) output.push(...await files(path));
    else if (entry.isFile()) output.push(path);
  }
  return output;
}
const inventories = {};
const required = ['mnemon_status', 'mnemon_recall', 'mnemon_document_search', 'mnemon_document_create', 'mnemon_runtime_memory', 'mnemon_remember', 'mnemon_view_route', 'mnemon_view_action'];
function inventory(label, requireMnemon) {
  const request = requests.find(item => item.phase === label && Array.isArray(item.body.tools));
  assert(request, `${label} reached the local model with a tool inventory`);
  const names = normalizeRequest(request.body).toolNames.sort();
  if (requireMnemon) for (const name of required) assert(names.includes(name), `${label} missing ${name}`);
  else assert(!names.some(name => name.startsWith('mnemon_')), 'disabled root leaked Mnemon tools');
  inventories[label] = names;
  return request.body;
}

try {
  const nativeVersion = await run('native-version', ['--version'], nativeCli);
  const dshVersion = await run('dsh-version', [cli, '--version']);
  const config = await run('effective-config', [cli, '--profile', 'headless', '--dump-config']);
  for (const [path] of mnemonPackages) assert(config.stdout.includes(path.slice('node_modules/'.length)), `Root composition missing ${path}`);
  for (const id of extensions) assert(new RegExp(`- id: ${id}\\n[\\s\\S]*?disabled: false`).test(config.stdout), `strategy extension not enabled: ${id}`);
  phase = 'enabled';
  const enabled = await run('enabled', [cli, '--profile', 'headless', 'compatibility-261']);
  assert(enabled.stdout.includes('Issue 261 compatibility complete'), 'real runtime add/status sequence must complete');
  const enabledRequest = inventory('enabled', true);
  const prompt = normalizeRequest(enabledRequest).system;
  for (const phrase of ['Source order expresses preference', 'MNEMON OPTIONAL AUTO CAPTURE']) assert(prompt.includes(phrase), `enabled strategy extension missing: ${phrase}`);
  const runtimeFiles = (await files(data)).filter(path => path.endsWith('/runtime/memories.json'));
  assert(runtimeFiles.length > 0, 'real Runtime storage was initialized');
  const before = new Map();
  const content = 'Issue 261 compatibility: the isolated published DSH cohort uses real Mnemon runtime memory.';
  for (const path of runtimeFiles) before.set(path, await readFile(path, 'utf8'));
  assert([...before.values()].some(text => text.includes(content)), 'real Runtime add persisted the fixture memory');
  phase = 'restart';
  const restarted = await run('restart', [cli, '--profile', 'headless', 'Verify the isolated Headless restart.']);
  assert(restarted.stdout.includes('Isolated Issue 261 compatibility fixture ready'));
  inventory('restart', true);
  const persistence = [];
  for (const [path, bytes] of before) {
    const after = await readFile(path, 'utf8');
    assert.equal(after, bytes, 'restart must preserve Runtime bytes');
    persistence.push({ path, sha256: createHash('sha256').update(after).digest('hex') });
  }
  await writeFile(patchFile, patch + '- id: mnemon\n  disabled: true\n');
  phase = 'disabled';
  const disabledRun = await run('disabled', [cli, '--profile', 'headless', 'Verify the disabled Root tool surface.']);
  assert(disabledRun.stdout.includes('Isolated Issue 261 compatibility fixture ready'));
  inventory('disabled', false);
  const result = { result: 'pass', consumer, output, version, dshPackageCount: dshPackages.length, mnemonPackageCount: mnemonPackages.length, enabledStrategyExtensions: extensions, nativeCli, nativeVersion: nativeVersion.stdout.trim(), dshVersion: dshVersion.stdout.trim(), inventories, persistence, completedAt: new Date().toISOString() };
  await writeFile(join(output, 'result.json'), JSON.stringify(result, null, 2) + '\n');
  console.log(JSON.stringify({ result: result.result, output, version, nativeVersion: result.nativeVersion, enabledMnemonTools: inventories.enabled.filter(name => name.startsWith('mnemon_')).length, disabledMnemonTools: inventories.disabled.filter(name => name.startsWith('mnemon_')).length }));
} catch (error) {
  await writeFile(join(output, 'result.json'), JSON.stringify({ result: 'fail', consumer, output, version, phase, error: String(error.stack ?? error), inventories, completedAt: new Date().toISOString() }, null, 2) + '\n');
  console.error(error);
  process.exitCode = 1;
} finally {
  model.closeAllConnections();
  await new Promise(done => model.close(done));
}
