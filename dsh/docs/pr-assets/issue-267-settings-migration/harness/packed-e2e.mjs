import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { readFile, writeFile, appendFile, mkdir, realpath } from 'node:fs/promises';
import { createReadStream } from 'node:fs';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { resolve, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { compatibilityModel, sendModelReply, responseProtocol } from '../../issue-261-dsh-slots/harness/compatibility-model.mjs';
import { frameworkPins } from '../../issue-261-dsh-slots/harness/framework-pins.mjs';

const args = new Map();
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1]);
const mode = args.get('--mode');
const version = args.get('--framework-version') ?? '0.1.7-alpha.1';
const cohort = version.includes('alpha') ? 'alpha' : 'rc';
const configSettings = version === '0.1.7-alpha.1';
assert(['baseline', 'fixed'].includes(mode), '--mode must be baseline or fixed');
assert(['0.1.5-rc.2', '0.1.6-alpha.2', '0.1.7-alpha.1'].includes(version), 'unsupported --framework-version');
assert(args.has('--framework-root'), 'supply --framework-root from a clean normal-peer public framework install');
const artifactsRoot = resolve(args.get('--artifacts'));
const runRoot = resolve(args.get('--run-root') ?? `./e2e/runs/${mode}-${version}`);
const serve = args.get('--serve') === 'true';
const legacySettings = args.get('--legacy-settings') ?? 'none';
assert(['none', 'retained', 'pending'].includes(legacySettings), 'unsupported --legacy-settings');
assert(legacySettings === 'none' || configSettings, 'legacy migration fixture requires alpha7');
const dshHome = join(runRoot, 'dsh-home');
const consumer = join(dshHome, 'profiles/web');
const workspace = join(runRoot, 'workspace');
const data = join(runRoot, 'mnemon-data');
const nativeCliInput = args.get('--native-cli') ?? process.env.MNEMON_CLI_PATH;
assert(nativeCliInput, 'supply --native-cli or MNEMON_CLI_PATH');
const nativeCli = resolve(nativeCliInput);
await Promise.all([consumer, workspace, data].map(path => mkdir(path, { recursive: true })));
const artifactSet = JSON.parse(await readFile(join(artifactsRoot, 'artifacts.json'), 'utf8'));
assert.equal(artifactSet.artifacts.length, 17);
const localPackages = new Map(artifactSet.artifacts.map(item => [item.name, item]));
const artifactPath = item => join(item.artifactRoot ?? artifactsRoot, item.filename);
for (const item of localPackages.values()) {
  const bytes = await readFile(artifactPath(item));
  assert.equal('sha512-' + createHash('sha512').update(bytes).digest('base64'), item.integrity);
  assert.equal(createHash('sha256').update(bytes).digest('hex'), item.sha256);
}
await writeFile(join(runRoot, 'artifact-provenance.json'), JSON.stringify({ artifactsRoot, ...artifactSet.provenance, artifacts: artifactSet.artifacts.map(({ name, version, filename, integrity, sha256, source }) => ({ name, version, filename, integrity, sha256, source })) }, null, 2) + '\n');
const legacyPeers = mode === 'baseline' && configSettings;
const frameworkPeers = await frameworkPins(resolve(args.get('--framework-root')), version);
await writeFile(join(runRoot, 'framework-pins.json'), JSON.stringify(frameworkPeers, null, 2) + '\n');
for (const [name, pinned] of Object.entries(frameworkPeers)) if (name.startsWith('@deepseek-ai/dsh')) assert.equal(pinned, version);
const env = { ...process.env, DSH_HOME: dshHome, DSH_TELEMETRY_DISABLED: '1', DSH_TELEMETRY_MODE: 'DISABLED', MNEMON_DATA_DIR: data, MNEMON_CLI_PATH: nativeCli, DEEPSEEK_API_KEY: 'isolated-issue267-fixture-key' };
for (const key of ['NODE_PATH', 'NODE_OPTIONS', 'npm_config_legacy_peer_deps', 'NPM_CONFIG_LEGACY_PEER_DEPS', 'OPENAI_API_KEY', 'ANTHROPIC_API_KEY']) delete env[key];
const registry = createServer(async (request, response) => {
  try {
    const path = new URL(request.url, 'http://127.0.0.1').pathname;
    if (path.startsWith('/artifacts/')) {
      const file = decodeURIComponent(path.slice('/artifacts/'.length));
      const item = [...localPackages.values()].find(candidate => candidate.filename === file);
      if (!item) { response.writeHead(404); response.end(); return; }
      response.writeHead(200, { 'content-type': 'application/octet-stream' });
      createReadStream(artifactPath(item)).pipe(response);
      await appendFile(join(runRoot, 'registry.log'), JSON.stringify({ at: new Date().toISOString(), kind: 'local-tarball', package: item.name, version: item.version, integrity: item.integrity }) + '\n');
      return;
    }
    const name = decodeURIComponent(path.slice(1));
    const item = localPackages.get(name);
    if (item) {
      const base = `http://127.0.0.1:${registry.address().port}`;
      const manifest = { ...item.manifest, dist: { tarball: `${base}/artifacts/${encodeURIComponent(item.filename)}`, integrity: item.integrity, shasum: item.shasum } };
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify({ _id: name, name, 'dist-tags': { latest: item.version }, versions: { [item.version]: manifest } }));
      return;
    }
    if (name.startsWith('dsh-mnemon')) { response.writeHead(404); response.end(JSON.stringify({ error: 'All Mnemon packages must come from the selected artifacts' })); return; }
    const upstream = await fetch('https://registry.npmjs.org' + path, { headers: { accept: request.headers.accept ?? 'application/json' } });
    response.writeHead(upstream.status, { 'content-type': upstream.headers.get('content-type') ?? 'application/json' });
    response.end(Buffer.from(await upstream.arrayBuffer()));
  } catch (error) { response.writeHead(500); response.end(String(error)); }
});
await new Promise(resolveListen => registry.listen(0, '127.0.0.1', resolveListen));
const registryUrl = `http://127.0.0.1:${registry.address().port}`;
env.npm_config_registry = registryUrl;
env.NPM_CONFIG_REGISTRY = registryUrl;

function command(executable, argv, log, cwd = consumer, timeoutMs = 240000) {
  return new Promise((resolveCommand, reject) => {
    const child = spawn(executable, argv, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let out = '';
    child.stdout.on('data', chunk => { out += chunk; void appendFile(join(runRoot, log), chunk); });
    child.stderr.on('data', chunk => { out += chunk; void appendFile(join(runRoot, log), chunk); });
    const timer = setTimeout(() => { child.kill('SIGTERM'); reject(new Error(`${argv[0]} timed out`)); }, timeoutMs);
    child.once('error', reject);
    child.once('exit', code => { clearTimeout(timer); code === 0 ? resolveCommand(out) : reject(new Error(`${executable} ${argv.join(' ')} exited ${code}; see ${join(runRoot, log)}`)); });
  });
}

const manifest = {
  name: `issue267-${mode}-${version}-web`, version: '0.0.0', private: true, type: 'module',
  dependencies: {
    ...frameworkPeers,
    '@deepseek-ai/dsh': version, '@deepseek-ai/dsh-client-ui-primitives': version, '@deepseek-ai/dsh-typert-protocol': version,
    ...Object.fromEntries(['dsh-client-ui-slots', 'dsh-client-ui-chat', 'dsh-client-ui-conversation', 'dsh-client-ui-renderer', 'dsh-client-ui-layout', 'dsh-client-ui-settings', 'dsh-client-store', 'dsh-client-locale'].map(name => [`@deepseek-ai/${name}`, version])),
    '@deepseek-ai/cordis': configSettings ? '4.0.3' : '4.0.2', react: '18.3.1', 'react-dom': '18.3.1',
    ...Object.fromEntries([...localPackages.values()].map(item => [item.name, item.version])),
  },
  dsh: { profile: { bundles: ['@deepseek-ai/dsh-base', '@deepseek-ai/dsh-web-app', 'dsh-mnemon'] } },
};
await writeFile(join(consumer, 'package.json'), JSON.stringify(manifest, null, 2) + '\n');
await writeFile(join(runRoot, 'install-policy.json'), JSON.stringify({ mode, cohort, version, registryUrl, legacyPeers, reusedExistingInstall: args.get('--reuse-install') === 'true', reason: legacyPeers ? `Baseline dsh-mnemon 0.5.12 peer ranges exclude ${version}; only the baseline reproduction uses --legacy-peer-deps. The exact framework was installed normally first. Fixed consumers must normal-install.` : 'Normal npm peer resolution, no legacy-peer-deps.' }, null, 2) + '\n');
console.log(`Installing ${mode} ${cohort} from ${artifactsRoot} at ${consumer}`);
if (args.get('--reuse-install') !== 'true') await command('npm', ['install', '--registry', registryUrl, '--no-audit', '--no-fund', legacyPeers ? '--legacy-peer-deps' : '--legacy-peer-deps=false'], 'install.log');

const lock = JSON.parse(await readFile(join(consumer, 'package-lock.json'), 'utf8'));
const dshPackages = Object.entries(lock.packages).flatMap(([path, info]) => /(?:^|\/)node_modules\/@deepseek-ai\/dsh(?:-[^/]+)?$/.test(path) ? [{ path, version: info.version, resolved: info.resolved, integrity: info.integrity }] : []);
assert(dshPackages.length > 200);
assert(dshPackages.every(item => item.version === version), 'all installed DSH packages must match exact cohort');
for (const item of localPackages.values()) {
  const entry = lock.packages[`node_modules/${item.name}`];
  assert.equal(entry.version, item.version);
  assert.equal(entry.integrity, item.integrity);
  assert(/^http:\/\/127\.0\.0\.1:\d+\/artifacts\//.test(entry.resolved), 'Mnemon must resolve from the test loopback registry');
  assert.equal(decodeURIComponent(new URL(entry.resolved).pathname), `/artifacts/${item.filename}`);
}
const resolutions = [];
for (const item of localPackages.values()) {
  const installed = join(consumer, 'node_modules', item.name, 'package.json');
  const loader = createRequire(installed);
  for (const dependency of ['@deepseek-ai/cordis', '@deepseek-ai/schemastery', '@deepseek-ai/dsh-client-ui-primitives', '@deepseek-ai/dsh-typert-protocol']) {
    const path = await realpath(loader.resolve(dependency));
    assert(path.startsWith(await realpath(consumer) + '/node_modules/'), `external resolution detected: ${path}`);
    resolutions.push({ from: item.name, dependency, path });
  }
}
const bytesChecked = [];
for (const [pkg, file] of [
  ['dsh-settings', 'lib/index.js'], ['dsh-client-ui-slots', 'lib/index.js'],
  ['dsh-client-ui-chat', 'lib/client.js'], ['dsh-client-ui-renderer', 'lib/client.js'],
  ['dsh-client-ui-conversation', 'lib/client.js'], ['dsh-client-ui-settings', 'lib/client.js'],
  ['dsh-base', 'cordis.patch.yml'], ['dsh-web-app', 'cordis.patch.yml'],
  ...(configSettings ? [['dsh-config-editor', 'lib/index.js'], ['dsh-agent-preset-registry', 'lib/index.js']] : []),
]) {
  const publicRoot = join(runRoot, 'public-contracts', version, pkg);
  await mkdir(publicRoot, { recursive: true });
  const metadata = await (await fetch(`https://registry.npmjs.org/@deepseek-ai/${pkg}/${version}`)).json();
  assert.equal(metadata.name, `@deepseek-ai/${pkg}`);
  assert.equal(metadata.version, version);
  assert.equal(lock.packages[`node_modules/@deepseek-ai/${pkg}`].integrity, metadata.dist.integrity);
  const archive = Buffer.from(await (await fetch(metadata.dist.tarball)).arrayBuffer());
  assert.equal('sha512-' + createHash('sha512').update(archive).digest('base64'), metadata.dist.integrity);
  const tarball = join(publicRoot, 'package.tgz');
  await writeFile(tarball, archive);
  await writeFile(join(publicRoot, 'metadata.json'), JSON.stringify(metadata, null, 2) + '\n');
  await command('tar', ['-xzf', tarball, '-C', publicRoot], 'public-contract-extraction.log', runRoot);
  const reference = join(publicRoot, 'package', file);
  const installed = join(consumer, 'node_modules/@deepseek-ai', pkg, file);
  const original = await readFile(reference);
  const actual = await readFile(installed);
  assert(actual.equals(original), `public package bytes changed: ${pkg}`);
  bytesChecked.push({ package: `@deepseek-ai/${pkg}`, file, tarballIntegrity: metadata.dist.integrity, sha256: createHash('sha256').update(actual).digest('hex') });
}
await writeFile(join(runRoot, 'runtime-graph.json'), JSON.stringify({ version, dshPackageCount: dshPackages.length, dshPackages, mnemonPackages: [...localPackages.keys()].filter(name => name.startsWith('dsh-mnemon')), testClientPackages: [...localPackages.keys()].filter(name => !name.startsWith('dsh-mnemon')), resolutions, publicBytesChecked: bytesChecked }, null, 2) + '\n');
const cli = join(consumer, 'node_modules/@deepseek-ai/dsh/lib/bin.js');
await command(process.execPath, [cli, '--version'], 'cli-version.log');
await command(nativeCli, ['--version'], 'mnemon-cli-version.log', workspace);

const disabled = ['subprocess', 'open-in-app', 'bash-sandbox', 'pwsh-sandbox', 'tool-bash', 'tool-pwsh', 'permission', 'tool-fs-search', 'directory-picker'];
const extensionIds = ['mnemon-strategy-scoped', 'mnemon-strategy-light-context', 'mnemon-strategy-auto-capture'];
const commonPatch = disabled.map(id => `- id: ${id}\n  disabled: true\n`).join('') +
  '- insert:\n    - id: e2e-directory-picker\n      name: "@deepseek-ai/dsh-host-directory-picker-browse"\n    - id: e2e-directory-picker-ui\n      name: "@deepseek-ai/dsh-client-ui-directory-picker-browse"\n' +
  extensionIds.map(id => `- id: ${id}\n  disabled: false\n`).join('');
const mnemonConfig = `    cliPath: ${JSON.stringify(nativeCli)}\n    idleReview:\n      enabled: false\n    persistenceStrategy:\n      mode: manual\n`;
let profilePatch = commonPatch;
if (configSettings) {
  // Alpha.7 uses normal profile Config rows and declarative presets; no settings.yaml.
  profilePatch += '- id: mnemon\n  config:\n' + mnemonConfig +
    '- id: agent-preset-registry\n  config:\n    default: mnemon-e2e\n' +
    ['standard', 'minimal', 'cordis', 'ptc'].map(id => `- id: preset-${id}\n  disabled: true\n`).join('') +
    '- insert:\n    - id: preset-mnemon-e2e\n      name: "@deepseek-ai/dsh-agent-preset"\n      config:\n        id: mnemon-e2e\n        name: Mnemon E2E\n        description: Isolated packed-artifact Issue 267 fixture.\n        order: 0\n        plugins:\n          - id: persona\n            name: "@deepseek-ai/dsh-persona"\n            config:\n              prefix: You are testing packed Mnemon compatibility with an isolated deterministic model.\n';
} else {
  profilePatch += '- id: agent-presets\n  config:\n    default: mnemon-e2e\n';
  await writeFile(join(dshHome, 'settings.yaml'), 'mnemon:\n' + mnemonConfig.replace(/^  /gm, ''));
  const preset = join(dshHome, '.agent-presets/mnemon-e2e');
  await mkdir(preset, { recursive: true });
  await writeFile(join(preset, 'preset.yml'), 'name: Mnemon E2E\ndescription: Isolated packed-artifact Issue 267 fixture.\norder: 0\n');
  await writeFile(join(preset, 'agent.cordis.yml'), '- id: persona\n  name: "@deepseek-ai/dsh-persona"\n  config:\n    prefix: You are testing packed Mnemon compatibility with an isolated deterministic model.\n');
}
if (legacySettings !== 'none') {
  const anchor = pathToFileURL(consumer + '/').href;
  const suffix = createHash('sha256').update(JSON.stringify(anchor)).digest('hex').slice(0, 16);
  const backup = `mnemon:
  defaultRecallLimit: 7
  cliPath: ${legacySettings === 'pending' ? JSON.stringify(nativeCli) : '/fixture/old-cli-must-not-win'}
mnemon-ui:
  turnBar: false
  saveAction: false
mnemon-view-${suffix}:
  strategyTypeId: default-three-tier
  entries:
    include:mnemon-strategy-light-context:
      enabled: false
      config:
        maxProjectionCharacters: 700
    include:mnemon-strategy-auto-capture:
      enabled: true
      config: {}
mnemon-plugins-${suffix}:
  sources:
    include:mnemon-source-documents:
      enabled: false
mnemon-view-ffffffffffffffff:
  entries:
    include:mnemon-strategy-scoped:
      enabled: false
      config: {}
`;
  profilePatch = profilePatch.replace('- id: mnemon-strategy-auto-capture\n  disabled: false\n', '');
  profilePatch += '- id: mnemon\n  config:\n    conversationInteraction:\n      turnBar: true\n' +
    '- id: mnemon-strategy-light-context\n  config:\n    maxProjectionCharacters: 1200\n' +
    '- id: mnemon-source-documents\n  disabled: false\n';
  const filename = legacySettings === 'retained' ? 'settings.yaml.imported' : 'settings.yaml';
  await writeFile(join(dshHome, filename), backup);
  await writeFile(join(runRoot, 'legacy-settings-fixture.json'), JSON.stringify({
    mode: legacySettings, backupSha256: createHash('sha256').update(backup).digest('hex'),
    viewNamespace: `mnemon-view-${suffix}`, legacyNamespace: `mnemon-plugins-${suffix}`,
    expected: { defaultRecallLimit: 7, turnBar: true, saveAction: false, lightEnabled: true, lightMaxProjectionCharacters: 1200, autoCaptureEnabled: true, documentsEnabled: true, scopedEnabled: true },
  }, null, 2) + '\n');
}
await writeFile(join(consumer, 'cordis.patch.yml'), profilePatch);
await writeFile(join(workspace, 'README.md'), '# Issue 267 disposable workspace\n\nAll memory and model data in this fixture are test-owned.\n');
await command(process.execPath, [cli, 'web', '--dump-config'], 'effective-config.yml', workspace);
let status = { mode, cohort, version, runRoot, artifactsRoot, consumer, dshHome, workspace, data, cli, nativeCli, registryUrl, legacyPeers, configSettings, pid: process.pid, preparedAt: new Date().toISOString(), serverStarted: false };
let serverWrites = Promise.resolve();
function writeStatus() {
  const bytes = JSON.stringify(status, null, 2) + '\n';
  serverWrites = serverWrites.then(() => writeFile(join(runRoot, 'server.json'), bytes, { mode: 0o600 }));
  return serverWrites;
}
await writeStatus();
if (!serve) {
  await new Promise(done => registry.close(done));
  console.log(`Prepared ${mode} ${version}; server state: ${join(runRoot, 'server.json')}`);
  process.exit(0);
}

const decide = compatibilityModel(event => { void appendFile(join(runRoot, 'model-events.jsonl'), JSON.stringify({ at: new Date().toISOString(), ...event }) + '\n'); });
let modelRequests = 0;
const model = createServer(async (request, response) => {
  try {
    let body = '';
    for await (const chunk of request) body += chunk;
    const input = JSON.parse(body);
    const number = ++modelRequests;
    await appendFile(join(runRoot, 'model-requests.jsonl'), JSON.stringify({ at: new Date().toISOString(), number, path: request.url, protocol: responseProtocol(input, request.url), body: input }) + '\n');
    const reply = decide(input);
    sendModelReply(response, input, request.url, reply, `compatibility-261-${number}`);
  } catch (error) {
    await appendFile(join(runRoot, 'model-errors.log'), String(error.stack ?? error) + '\n');
    response.writeHead(500, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ error: { message: String(error) } }));
  }
});
await new Promise(done => model.listen(0, '127.0.0.1', done));
env.DEEPSEEK_BASE_URL = `http://127.0.0.1:${model.address().port}`;
let web;
let stopping = false;
let restarting = false;
function launch() {
  web = spawn(process.execPath, [cli, 'web', '--no-open', '--host', '127.0.0.1', '--port', '0'], { cwd: workspace, env, stdio: ['ignore', 'pipe', 'pipe'] });
  status = { ...status, serverStarted: true, webPid: web.pid, modelUrl: env.DEEPSEEK_BASE_URL, startedAt: new Date().toISOString() };
  void writeStatus();
  let output = '';
  let reported = false;
  const record = chunk => {
    output += chunk;
    // The bootstrap URL is a credential. Preserve it only in local evidence.
    void appendFile(join(runRoot, 'web.log'), chunk, { mode: 0o600 });
    if (output.includes('ctx.settings.register is not a function') && !status.activationFailure) {
      status = { ...status, activationFailure: 'ctx.settings.register is not a function' };
      void writeStatus();
      console.log('Captured baseline activation failure: ctx.settings.register is not a function');
    }
    const urls = [...output.matchAll(/https?:\/\/127\.0\.0\.1:\d+[^\s\x1b]*/g)].map(match => match[0]);
    const url = urls.find(candidate => !candidate.startsWith(registryUrl) && !candidate.startsWith(env.DEEPSEEK_BASE_URL));
    if (url && status.url !== url) {
      status = { ...status, url };
      if (!reported) { console.log(`Loopback WebUI available; private server state: ${join(runRoot, 'server.json')}`); reported = true; }
      void writeStatus();
    }
  };
  web.stdout.on('data', record);
  web.stderr.on('data', record);
  web.once('error', error => { console.error(error); process.exitCode = 1; void stop(); });
  web.once('exit', code => { status = { ...status, webExitCode: code }; if (!stopping && !restarting) { process.exitCode = code ?? 1; void stop(); } });
}
async function stop() {
  if (stopping) return;
  stopping = true;
  if (web && web.exitCode === null) { web.kill('SIGTERM'); await new Promise(done => web.once('exit', done)); }
  model.closeAllConnections();
  registry.closeAllConnections();
  await Promise.all([new Promise(done => model.close(done)), new Promise(done => registry.close(done))]);
  status = { ...status, stoppedAt: new Date().toISOString() };
  await writeStatus();
}
process.once('SIGINT', () => { void stop(); });
process.once('SIGTERM', () => { void stop(); });
process.on('SIGUSR2', async () => {
  if (stopping || restarting || !web || web.exitCode !== null) return;
  restarting = true;
  web.kill('SIGTERM');
  await new Promise(done => web.once('exit', done));
  restarting = false;
  if (!stopping) launch();
});
console.log(`Starting isolated ${mode} ${version}; private server state: ${join(runRoot, 'server.json')}`);
launch();
