import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { createServer } from 'node:http'
import { appendFile, mkdir, readFile, writeFile } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { compatibilityModel, sendModelReply } from '../../issue-261-dsh-slots/harness/compatibility-model.mjs'

const args = new Map()
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1])
for (const key of ['--resources', '--run-root', '--package', '--pnpm-entry', '--native-cli']) assert(args.get(key), `supply ${key}`)
const resources = resolve(args.get('--resources'))
const runRoot = resolve(args.get('--run-root'))
const modules = join(resources, 'app/node_modules')
const dshHome = join(runRoot, 'dsh-home')
const profile = args.get('--profile') ?? 'web'
assert(['web', 'headless'].includes(profile))
const profileDir = join(dshHome, 'profiles', profile)
const workspace = join(runRoot, 'workspace')
const data = join(runRoot, 'mnemon-data')
const cli = join(modules, '@deepseek-ai/dsh/lib/bin.js')
const nativeCli = resolve(args.get('--native-cli'))
await Promise.all([profileDir, workspace, data].map(path => mkdir(path, { recursive: true })))
const log = (name, value) => appendFile(join(runRoot, name), value, { mode: 0o600 })
const official = path => import(pathToFileURL(join(modules, path)).href)
const { installGeneration } = await official('dsh-desktop-market-installer/generations/installer.mjs')
const { writeDesired } = await official('dsh-desktop-market-installer/generations/registry.mjs')
const { projectGenerations } = await official('dsh-desktop-market-installer/generations/projection.mjs')
const generation = await installGeneration({
  dshHome, profile, pluginSpec: args.get('--package'), expectedPluginName: 'dsh-mnemon',
  registry: 'https://registry.npmjs.org', autoInstallPeers: false,
  nodeExecutablePath: process.execPath, pnpmEntryPath: resolve(args.get('--pnpm-entry')),
  onTrace: line => { void log('install.log', line + '\n') },
  onOutput: line => { void log('install-output.log', line) },
})
assert.equal(generation.ok, true, generation.detail)
await writeFile(join(profileDir, 'package.json'), JSON.stringify({
  name: `issue274-${profile}`, private: true, dependencies: {},
  dsh: { profile: { bundles: ['@deepseek-ai/dsh-base', profile === 'web' ? '@deepseek-ai/dsh-web-app' : '@deepseek-ai/dsh-headless'], patchReload: 'startup' } },
}, null, 2))
await writeDesired(dshHome, [generation.generation.id])
await projectGenerations(dshHome, profile)
if (profile === 'headless') {
  // Desktop preserves only its own Web bundles during market projection.
  // Add the official Headless bundle to this disposable profile afterwards.
  const path = join(profileDir, 'package.json')
  const manifest = JSON.parse(await readFile(path, 'utf8'))
  manifest.dsh.profile.bundles.push('@deepseek-ai/dsh-headless')
  await writeFile(path, JSON.stringify(manifest, null, 2))
}
const disabled = ['subprocess', 'open-in-app', 'bash-sandbox', 'pwsh-sandbox', 'tool-bash', 'tool-pwsh', 'permission', 'tool-fs-search', 'directory-picker']
let patch = disabled.map(id => `- id: ${id}\n  disabled: true\n`).join('')
patch += ['scoped', 'light-context', 'auto-capture'].map(id => `- id: mnemon-strategy-${id}\n  disabled: false\n`).join('')
if (args.get('--root-disabled') === 'true') patch += '- id: mnemon\n  disabled: true\n'
if (profile === 'web') {
  patch += '- id: agent-presets\n  config:\n    default: mnemon-e2e\n' +
    '- insert:\n    - id: e2e-directory-picker\n      name: "@deepseek-ai/dsh-host-directory-picker-browse"\n    - id: e2e-directory-picker-ui\n      name: "@deepseek-ai/dsh-client-ui-directory-picker-browse"\n'
  const preset = join(dshHome, '.agent-presets/mnemon-e2e')
  await mkdir(preset, { recursive: true })
  await writeFile(join(preset, 'preset.yml'), 'name: Mnemon E2E\ndescription: Disposable Issue 274 generation fixture.\norder: 0\n')
  await writeFile(join(preset, 'agent.cordis.yml'), '- id: persona\n  name: "@deepseek-ai/dsh-persona"\n  config:\n    prefix: You are testing Mnemon with an isolated deterministic model.\n')
}
await writeFile(join(profileDir, 'cordis.patch.yml'), patch)
await writeFile(join(dshHome, 'settings.yaml'), `# Synthetic legacy settings; keep on restart.\nmnemon:\n  cliPath: ${JSON.stringify(nativeCli)}\n  defaultRecallLimit: 7\n  idleReview:\n    enabled: false\n  persistenceStrategy:\n    mode: manual\n`)
await writeFile(join(workspace, 'README.md'), '# Issue 274 disposable workspace\n')
const bytes = []
for (const path of [
  'host-module-fallback.mjs', 'harness-node-entry.mjs',
  'app/node_modules/@deepseek-ai/cordis-plugin-loader/lib/index.js',
  'app/node_modules/@deepseek-ai/dsh-app-boot/lib/index.js',
  'app/node_modules/@deepseek-ai/cosmokit/lib/index.js',
  'app/node_modules/dsh-desktop-market-installer/generations/installer.mjs',
  'app/node_modules/dsh-desktop-market-installer/generations/projection.mjs',
]) bytes.push({ path, sha256: createHash('sha256').update(await readFile(join(resources, path))).digest('hex') })
await writeFile(join(runRoot, 'provenance.json'), JSON.stringify({ node: process.version, generation, bytes }, null, 2))
let state = { pid: process.pid, runRoot, profile, profileDir, dshHome, workspace, data, nativeCli, resources, generation: generation.generation, runtime: process.version, addonsDisabled: args.get('--no-addons') === 'true' }
let writes = Promise.resolve()
function status() { writes = writes.then(() => writeFile(join(runRoot, 'server.json'), JSON.stringify(state, null, 2), { mode: 0o600 })); return writes }
await status()
if (args.get('--serve') !== 'true') { console.log(`Prepared generation: ${join(runRoot, 'server.json')}`); process.exit(0) }
const decide = compatibilityModel(event => { void log('model-events.jsonl', JSON.stringify(event) + '\n') })
let requests = 0
const modelRequests = []
const model = createServer(async (request, response) => {
  try {
    let body = ''
    for await (const chunk of request) body += chunk
    const input = JSON.parse(body)
    modelRequests.push(input)
    await log('model-requests.jsonl', JSON.stringify(input) + '\n')
    const reply = profile === 'headless' ? 'ISSUE274_HEADLESS_READY' : decide(input)
    sendModelReply(response, input, request.url, reply, `issue274-${++requests}`)
  } catch (error) { response.writeHead(500); response.end(String(error)); void log('model-errors.log', String(error) + '\n') }
})
await new Promise(done => model.listen(0, '127.0.0.1', done))
const env = { ...process.env, DSH_HOME: dshHome, MNEMON_DATA_DIR: data, MNEMON_CLI_PATH: nativeCli, DSH_TELEMETRY_DISABLED: '1', DSH_TELEMETRY_MODE: 'DISABLED', DEEPSEEK_BASE_URL: `http://127.0.0.1:${model.address().port}`, DEEPSEEK_API_KEY: 'issue274-disposable-fixture' }
for (const key of ['NODE_PATH', 'NODE_OPTIONS', 'OPENAI_API_KEY', 'ANTHROPIC_API_KEY']) delete env[key]
let web
let stopping = false
let restarting = false
function launch() {
  // The optional no-addons probe exercises Desktop's genuine fallback; full
  // WebUI runs retain the native attachment/sandbox services they require.
  const argv = [...(args.get('--no-addons') === 'true' ? ['--no-addons'] : []), join(resources, 'harness-node-entry.mjs'), cli,
    ...(profile === 'web' ? ['web', '--no-open', '--host', '127.0.0.1', '--port', '0'] : ['--profile', 'headless', 'Verify Mnemon tools.'])]
  web = spawn(process.execPath, argv, { cwd: workspace, env, stdio: ['ignore', 'pipe', 'pipe'] })
  state = { ...state, webPid: web.pid, argv, startedAt: new Date().toISOString(), url: undefined, exitCode: undefined }
  void status()
  let output = ''
  let announced = false
  const record = chunk => {
    output += chunk
    void log('web.log', chunk)
    if (output.includes("Cannot find package 'dsh-mnemon'")) { state.activationFailure = "Cannot find package 'dsh-mnemon'"; void status() }
    const url = [...output.matchAll(/https?:\/\/127\.0\.0\.1:\d+[^\s\x1b]*/g)].map(item => item[0]).find(url => !url.startsWith(env.DEEPSEEK_BASE_URL))
    if (url && state.url !== url) { state.url = url; void status(); if (!announced) { console.log(`WebUI state: ${join(runRoot, 'server.json')}`); announced = true } }
  }
  web.stdout.on('data', record)
  web.stderr.on('data', record)
  web.once('error', error => { state.error = error.message; void stop() })
  web.once('exit', code => {
    state.exitCode = code
    if (profile === 'headless' && code === 0) {
      try {
        const names = new Set(modelRequests.flatMap(request => (request.tools ?? []).map(tool => tool.function?.name)))
        const required = ['mnemon_status', 'mnemon_recall', 'mnemon_document_search', 'mnemon_document_create', 'mnemon_runtime_memory', 'mnemon_remember', 'mnemon_view_route', 'mnemon_view_action']
        if (args.get('--root-disabled') === 'true') assert(![...names].some(name => name?.startsWith('mnemon_')))
        else {
          for (const name of required) assert(names.has(name), `missing ${name}`)
          const prompt = JSON.stringify(modelRequests[0]?.messages)
          for (const marker of ['Source order expresses preference', 'MNEMON OPTIONAL AUTO CAPTURE']) assert(prompt.includes(marker), `missing Strategy contribution: ${marker}`)
        }
        assert(output.includes('ISSUE274_HEADLESS_READY'))
        assert(!output.includes('waiting for service:'))
        state.headlessVerified = true
      } catch (error) { state.verificationError = error.message; process.exitCode = 1 }
    }
    if (code !== 0 && !stopping && !restarting) process.exitCode = code ?? 1
    void status()
    if (!stopping && !restarting) void stop()
  })
}
async function stop() {
  if (stopping) return
  stopping = true
  if (web?.exitCode === null) { web.kill('SIGTERM'); await new Promise(done => web.once('exit', done)) }
  model.closeAllConnections()
  await new Promise(done => model.close(done))
  state.stoppedAt = new Date().toISOString()
  await status()
}
process.once('SIGTERM', () => { void stop() })
process.once('SIGINT', () => { void stop() })
process.on('SIGUSR2', async () => {
  if (stopping || restarting || web?.exitCode !== null) return
  restarting = true
  web.kill('SIGTERM')
  await new Promise(done => web.once('exit', done))
  restarting = false
  launch()
})
console.log(`Starting Desktop generation fixture; private state: ${join(runRoot, 'server.json')}`)
launch()
