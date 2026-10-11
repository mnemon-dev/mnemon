// Published DSH/Teams and packed Mnemon WebUI acceptance with disposable state.
// Only model choices are fixed. Do not print the private URL or raw Host log.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { createReadStream } from 'node:fs'
import { createServer } from 'node:http'
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { join, resolve } from 'node:path'
import { parseArgs } from 'node:util'
import { pathToFileURL } from 'node:url'
import { delegatedCompletion } from './delegated-completion.mjs'

const { values } = parseArgs({ options: {
  profile: { type: 'string' }, artifacts: { type: 'string' }, state: { type: 'string' },
  policy: { type: 'string', default: 'pause' }, provider: { type: 'string', default: 'spawn' },
} })
for (const name of ['profile', 'artifacts', 'state']) assert(values[name], `Missing --${name}`)
assert(['pause', 'scoped'].includes(values.policy))
assert(['spawn', 'fork'].includes(values.provider))
assert(process.env.MNEMON_CLI_PATH, 'Set MNEMON_CLI_PATH to an official disposable-test CLI')
const state = resolve(values.state)
const profile = resolve(values.profile)
const artifacts = resolve(values.artifacts)
const workspace = join(state, 'workspace')
const dshHome = join(state, 'dsh-home')
for (const directory of [state, workspace, dshHome, join(state, 'data')]) await mkdir(directory, { recursive: true, mode: 0o700 })
const privateFile = (name, data) => writeFile(join(state, name), data, { mode: 0o600 })
const run = (command, args, options = {}) => new Promise((fulfill, reject) => {
  const child = spawn(command, args, { ...options, stdio: ['ignore', 'pipe', 'pipe'] })
  let output = ''
  child.stdout.on('data', chunk => { output += chunk })
  child.stderr.on('data', chunk => { output += chunk })
  child.on('error', reject)
  child.on('close', async code => {
    if (code === 0) fulfill(output)
    else { await privateFile('install-failure.log', output); reject(new Error(`Fixture subprocess exited ${code}; inspect private install-failure.log`)) }
  })
})
const packages = new Map()
for (const filename of await readdir(artifacts)) {
  if (!filename.endsWith('.tgz')) continue
  const path = join(artifacts, filename)
  const manifest = JSON.parse(await run('tar', ['-xOf', path, 'package/package.json']))
  const integrity = `sha512-${createHash('sha512').update(await readFile(path)).digest('base64')}`
  packages.set(manifest.name, { manifest, path, integrity })
}
assert.equal(packages.size, 17, 'Use all seventeen packed artifacts')
let registryUrl
const registry = createServer((request, response) => {
  const name = decodeURIComponent(new URL(request.url, 'http://localhost').pathname).slice(1)
  if (name.startsWith('tarballs/')) {
    const pkg = packages.get(name.slice('tarballs/'.length))
    if (!pkg) { response.writeHead(404); response.end(); return }
    response.writeHead(200, { 'content-type': 'application/octet-stream' })
    createReadStream(pkg.path).pipe(response)
  } else if (packages.has(name)) {
    const { manifest, integrity } = packages.get(name)
    response.writeHead(200, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ name, 'dist-tags': { latest: manifest.version }, versions: {
      [manifest.version]: { ...manifest, dist: { integrity, tarball: `${registryUrl}/tarballs/${name}` } },
    } }))
  } else { response.writeHead(307, { location: `https://registry.npmjs.org${request.url}` }); response.end() }
})
await new Promise(done => registry.listen(0, '127.0.0.1', done))
registryUrl = `http://127.0.0.1:${registry.address().port}`
const stages = new Map()
const observations = []
const memory = 'The synthetic Team review project uses SQLite for durable local state.'
const userEvidence = [
  'Synthetic issue 275 acceptance: for this disposable project we explicitly choose SQLite as the durable local state database. Preserve this project decision for later implementation and use the existing scoped memory workflow; no production data or credentials are involved.',
  'Synthetic issue 275 checkpoint: the project decision is final and stable. SQLite remains the durable local state database for all future modules in this disposable workspace. Record this explicit project convention through the bounded idle review after this completed conversation.',
]
const model = createServer(async (request, response) => {
  let input = ''
  for await (const chunk of request) input += chunk
  try {
    const wire = JSON.parse(input)
    const messagesApi = request.url?.endsWith('/messages') === true
    const body = messagesApi ? { ...wire,
      messages: [...(wire.system ? [{ role: 'system', content: wire.system }] : []), ...wire.messages],
      tools: wire.tools?.map(tool => ({ function: { name: tool.name, parameters: tool.input_schema } })),
    } : wire
    const completion = delegatedCompletion(body)
    let reply = 'The synthetic checkpoint is complete. The project uses SQLite for durable local state; this explicit user decision is ready for bounded idle review.'
    if (completion) {
      const inherited = body.messages.map(message => typeof message.content === 'string' ? message.content : (message.content ?? []).map(block => block.text ?? '').join('\n')).join('\n')
      for (const text of userEvidence) assert(inherited.includes(text), 'Review must receive each whole explicit user decision before any fixture mutation')
      const step = stages.get(completion.key) ?? 0
      stages.set(completion.key, step + 1)
      observations.push({ event: 'review-model-step', review: stages.size, step, completeUserMessages: userEvidence.length })
      if (step === 0) reply = { name: 'spawn_teammate', args: { name: 'denied-review-child', description: 'Must remain denied.', prompt: 'Must remain denied.' } }
      else if (step === 1) reply = { name: 'mnemon_runtime_memory', args: { action: 'add', target: 'memory', content: memory, importance: 'normal' } }
      else if (step === 2) reply = { name: 'mnemon_document_search', args: { query: 'Team review project', limit: 1 } }
      else reply = { name: completion.name, args: completion.wrap({ action: 'added', summary: 'Scoped review committed the explicit SQLite decision; Team delegation was denied.', memoryBodyIds: [], documentIds: [] }) }
    } else {
      const check = body.messages.findLastIndex(message => message.role === 'user' && JSON.stringify(message.content).includes('TEAM_CHECK'))
      const alreadyCalled = check >= 0 && body.messages.slice(check + 1).some(message => message.role === 'assistant' && JSON.stringify(message).includes('team_task_create'))
      if (check >= 0 && !alreadyCalled) reply = { name: 'team_task_create', args: { subject: 'Parent Teams preserved after scoped review', description: 'Synthetic official Team tool acceptance.' } }
    }
    await privateFile('observations.json', JSON.stringify(observations, null, 2) + '\n')
    response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' })
    if (messagesApi) {
      const call = typeof reply !== 'string'
      const event = value => response.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`)
      event({ type: 'message_start', message: { id: `fixture-${Date.now()}`, type: 'message', role: 'assistant', model: wire.model, content: [], usage: { input_tokens: 10, output_tokens: 0 } } })
      event({ type: 'content_block_start', index: 0, content_block: call ? { type: 'tool_use', id: `fixture-${Date.now()}`, name: reply.name, input: {} } : { type: 'text', text: '' } })
      event({ type: 'content_block_delta', index: 0, delta: call ? { type: 'input_json_delta', partial_json: JSON.stringify(reply.args) } : { type: 'text_delta', text: reply } })
      event({ type: 'content_block_stop', index: 0 })
      event({ type: 'message_delta', delta: { stop_reason: call ? 'tool_use' : 'end_turn' }, usage: { output_tokens: 5 } })
      event({ type: 'message_stop' })
      response.end()
      return
    }
    const delta = typeof reply === 'string' ? { role: 'assistant', content: reply } : { role: 'assistant', tool_calls: [{ index: 0, id: `fixture-${Date.now()}`, type: 'function', function: { name: reply.name, arguments: JSON.stringify(reply.args) } }] }
    for (const choice of [{ index: 0, delta, finish_reason: null }, { index: 0, delta: {}, finish_reason: typeof reply === 'string' ? 'stop' : 'tool_calls' }]) response.write(`data: ${JSON.stringify({ id: 'team-review-fixture', choices: [choice] })}\n\n`)
    response.end('data: [DONE]\n\n')
  } catch (error) {
    await privateFile('model-error.txt', String(error))
    response.writeHead(500, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ error: { message: 'Synthetic fixture assertion failed' } }))
  }
})
await new Promise(done => model.listen(0, '127.0.0.1', done))
// Fail fixture startup before browser acceptance if the published adapter cannot
// parse either text or tool-use SSE from this model endpoint.
const require = createRequire(join(profile, 'package.json'))
// The Teams profile package must match the DSH release installed in --profile.
const dshVersion = JSON.parse(await readFile(require.resolve('@deepseek-ai/dsh/package.json'), 'utf8')).version
const { DeepSeekAdapter, resolveAdapterOptions } = await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-llm-deepseek')).href)
const adapter = new DeepSeekAdapter({
  options: () => resolveAdapterOptions({ baseURL: `http://127.0.0.1:${model.address().port}`, thinking: 'disabled' }),
  resolveApiKey: async () => 'isolated-test-key', resolveUserId: () => 'synthetic-team-fixture',
  prepareExtensions: async () => ({ fields: {}, accept: async () => {} }),
})
for (const [input, type] of [['Protocol smoke check', 'text'], ['TEAM_CHECK', 'tool-call']]) {
  const chunks = []
  for await (const chunk of adapter.stream({ provider: 'deepseek', model: 'DeepSeek-V41-Flash',
    messages: [{ role: 'user', content: [{ type: 'text', text: input }] }],
    tools: [{ name: 'team_task_create', description: 'Synthetic Team check.', parameters: { type: 'object' } }], signal: AbortSignal.timeout(5000),
  })) chunks.push(chunk)
  assert(chunks.some(chunk => chunk.type === 'block-end' && chunk.block.type === type), `Published adapter must parse ${type}`)
  assert(chunks.some(chunk => chunk.type === 'finish'), 'Published adapter must finish the stream')
}
await privateFile('protocol-smoke.json', JSON.stringify({ publishedAdapter: dshVersion, text: true, toolCall: true }) + '\n')
const env = { ...process.env, DSH_HOME: dshHome, DSH_TELEMETRY_DISABLED: '1',
  DEEPSEEK_API_KEY: 'isolated-test-key', DEEPSEEK_BASE_URL: `http://127.0.0.1:${model.address().port}`,
  MNEMON_DATA_DIR: join(state, 'data'), npm_config_registry: registryUrl, NPM_CONFIG_REGISTRY: registryUrl }
const dshBin = join(profile, 'node_modules/@deepseek-ai/dsh/lib/bin.js')
await privateFile('install.log', await run(process.execPath, [dshBin, 'plugin', '--profile', 'web', 'add',
  `file:${packages.get('dsh-mnemon').path}`, `@deepseek-ai/dsh-experimental-agent-team-profile@${dshVersion}`, '--registry', registryUrl], { cwd: workspace, env }))
// Keep the official Coding preset and all its services, including PTC. Only
// replace the native directory dialog with the published browser picker.
const disabled = ['directory-picker']
await writeFile(join(dshHome, 'profiles/web/cordis.patch.yml'), disabled.map(id => `- id: ${id}\n  disabled: true\n`).join('') + `
- id: mnemon
  config:
    cliPath: ${JSON.stringify(process.env.MNEMON_CLI_PATH)}
    idleReviewMs: 5000
    idleReview:
      agentTeams: ${values.policy}
      provider: ${values.provider}
      minIntervalMs: 5000
      maxPerSession: 1
- id: mnemon-strategy-scoped
  disabled: false
- id: mnemon-strategy-light-context
  disabled: false
- id: mnemon-strategy-auto-capture
  disabled: false
- insert:
    - id: e2e-directory-picker
      name: '@deepseek-ai/dsh-host-directory-picker-browse'
    - id: e2e-directory-picker-ui
      name: '@deepseek-ai/dsh-client-ui-directory-picker-browse'
`)
await writeFile(join(workspace, 'README.md'), '# Synthetic Agent Teams review project\n\nThe project uses SQLite for durable local state. No production memory or credentials.\n')
let web
let hostLog = ''
let stopping = false
let restarting = false
const info = { pid: process.pid, state, workspace, dshHome, policy: values.policy, provider: values.provider }
const launch = () => {
  web = spawn(process.execPath, [dshBin, 'web', '--no-open', '--host', '127.0.0.1', '--port', '0'], { cwd: workspace, env, stdio: ['ignore', 'pipe', 'pipe'] })
  const capture = chunk => {
    hostLog += chunk.toString()
    const plain = hostLog.replace(/\x1b\[[0-9;]*m/gu, '')
    const urls = plain.match(/http:\/\/127\.0\.0\.1:\d+[^\s]*/gu) ?? []
    const url = urls.findLast(value => /[?&](?:token|key)=/u.test(value))
    if (url) info.url = url
    void privateFile('host.log', hostLog)
    void privateFile('private.json', JSON.stringify(info, null, 2) + '\n')
  }
  web.stdout.on('data', capture)
  web.stderr.on('data', capture)
  web.on('exit', code => { if (!stopping && !restarting) { process.exitCode = code ?? 1; void stop() } })
}
const stop = async () => {
  if (stopping) return
  stopping = true
  if (web?.exitCode === null) { web.kill('SIGTERM'); await new Promise(done => web.once('exit', done)) }
  model.closeAllConnections(); registry.closeAllConnections()
  await Promise.all([new Promise(done => model.close(done)), new Promise(done => registry.close(done))])
}
process.once('SIGINT', () => { void stop() })
process.once('SIGTERM', () => { void stop() })
process.on('SIGUSR2', async () => {
  if (stopping || restarting || web?.exitCode !== null) return
  restarting = true
  web.kill('SIGTERM'); await new Promise(done => web.once('exit', done))
  hostLog = ''; launch(); restarting = false
})
await privateFile('private.json', JSON.stringify(info, null, 2) + '\n')
launch()
console.log('Fixture started. Private connection metadata: ' + join(state, 'private.json'))
