// Official DSH WebUI + packed Mnemon acceptance for issue 281, with disposable
// state. Only model choices are scripted; tools, adapters and persistence are real.
// Keep private.json and the raw host/install logs outside published evidence.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { createReadStream } from 'node:fs'
import { createServer } from 'node:http'
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { basename, join, resolve } from 'node:path'
import { parseArgs } from 'node:util'
import { pathToFileURL } from 'node:url'

const { values } = parseArgs({ options: {
  profile: { type: 'string' }, artifacts: { type: 'string' }, state: { type: 'string' },
  port: { type: 'string', default: '0' },
} })
for (const name of ['profile', 'artifacts', 'state']) assert(values[name], `Missing --${name}`)
const webPort = Number(values.port)
assert(Number.isInteger(webPort) && webPort >= 0 && webPort <= 65535, '--port must be an integer from 0 through 65535')
assert(process.env.MNEMON_CLI_PATH, 'Set MNEMON_CLI_PATH to the verified official disposable-test CLI')
const state = resolve(values.state)
const profile = resolve(values.profile)
const artifacts = resolve(values.artifacts)
const workspace = join(state, 'workspace')
const dshHome = join(state, 'dsh-home')
await mkdir(state, { recursive: true, mode: 0o700 })
assert.equal((await readdir(state)).length, 0, 'Use an empty disposable --state directory; SIGUSR2 restarts the running host')
for (const directory of [workspace, dshHome, join(state, 'data')]) await mkdir(directory, { recursive: true, mode: 0o700 })
const privateFile = (name, data) => writeFile(join(state, name), data, { mode: 0o600 })
const run = (command, args, options = {}) => new Promise((fulfill, reject) => {
  const child = spawn(command, args, { ...options, stdio: ['ignore', 'pipe', 'pipe'] })
  let output = ''
  child.stdout.on('data', chunk => { output += chunk })
  child.stderr.on('data', chunk => { output += chunk })
  child.on('error', reject)
  child.on('close', async code => {
    if (code === 0) fulfill(output)
    else { await privateFile('subprocess-failure.log', output); reject(new Error(`Fixture subprocess exited ${code}; inspect private subprocess-failure.log`)) }
  })
})
const require = createRequire(join(profile, 'package.json'))
const publishedVersion = JSON.parse(await readFile(require.resolve('@deepseek-ai/dsh/package.json'))).version
assert.equal(publishedVersion, '0.1.7-rc.2', 'Install the exact official rc2-profile.json cohort')
const packages = new Map()
for (const filename of await readdir(artifacts)) {
  if (!filename.endsWith('.tgz')) continue
  const path = join(artifacts, filename)
  const manifest = JSON.parse(await run('tar', ['-xOf', path, 'package/package.json']))
  const bytes = await readFile(path)
  assert(!packages.has(manifest.name), `Duplicate packed package: ${manifest.name}`)
  packages.set(manifest.name, { manifest, path,
    integrity: `sha512-${createHash('sha512').update(bytes).digest('base64')}`,
    sha256: createHash('sha256').update(bytes).digest('hex'),
  })
}
assert.equal(packages.size, 17, 'Use Root and all sixteen packed plugin artifacts')
assert(packages.has('dsh-mnemon'), 'The packed Root is required')
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

const seed = 'USER281_SEED: I prefer concise replies.'
const replacement = 'USER281_REPLACED: I prefer concise replies with examples.'
const extra = 'USER281_EXTRA: I prefer metric units.'
const scoped = 'MEMORY281_SCOPED: The experiment uses SQLite.'
const global = 'MEMORY281_GLOBAL: The project uses SQLite.'
const cases = {
  USER281_PREPARE: { action: 'add', target: 'user', content: seed, importance: 'normal' },
  USER281_REPLACE: { action: 'replace', target: 'user', old_text: seed, content: replacement, importance: 'normal', branches: [] },
  USER281_ADD: { action: 'add', target: 'user', content: extra, importance: 'normal', branches: [] },
  USER281_REMOVE: { action: 'remove', target: 'user', old_text: extra, branches: [] },
  USER281_REJECT: { action: 'add', target: 'user', content: 'USER281_REJECTED: I prefer afternoon reminders.', branches: ['experiment'] },
  MEMORY281_SCOPE: { action: 'add', target: 'memory', content: scoped, importance: 'normal', branches: ['experiment'] },
  MEMORY281_CLEAR: { action: 'replace', target: 'memory', old_text: scoped, content: global, importance: 'normal', branches: [] },
}
const commandPattern = new RegExp(`\\b(${Object.keys(cases).join('|')})\\b`, 'u')
const contentText = content => typeof content === 'string' ? content : Array.isArray(content) ? content.map(block => block.text ?? '').join('\n') : ''
const parseResult = content => {
  const text = contentText(content)
  try { return JSON.parse(text) } catch { return { text } }
}
const redact = value => JSON.parse(JSON.stringify(value).replaceAll(state, '<fixture-state>')
  .replaceAll(profile, '<official-profile>').replaceAll(artifacts, '<packed-artifacts>')
  .replace(/([?&](?:token|key)=)[^\s"&]+/gu, '$1<redacted>'))
const observations = []
let observationWrite = Promise.resolve()
let protocolSmoke = true
let requestNumber = 0
const model = createServer(async (request, response) => {
  let input = ''
  for await (const chunk of request) input += chunk
  try {
    const wire = JSON.parse(input)
    const messagesApi = request.url?.endsWith('/messages') === true
    const messages = [...(messagesApi && wire.system ? [{ role: 'system', content: wire.system }] : []), ...(wire.messages ?? [])]
    const tools = (wire.tools ?? []).map(tool => messagesApi
      ? { name: tool.name, description: tool.description, parameters: tool.input_schema }
      : tool.function)
    const titleRequest = tools.length === 0
      && messages.some(message => message.role === 'system' && contentText(message.content).includes('Create a concise title for an AI coding-assistant session'))
      && messages.some(message => message.role === 'user' && contentText(message.content).startsWith('Generate the session title from this JSON array of human messages:'))
    const userIndex = messages.findLastIndex(message => message.role === 'user' && commandPattern.test(contentText(message.content)))
    const command = titleRequest || userIndex < 0 ? undefined : contentText(messages[userIndex].content).match(commandPattern)?.[1]
    const receipts = messages.slice(userIndex + 1).flatMap(message => message.role === 'tool'
      ? [{ callId: message.tool_call_id, result: parseResult(message.content) }]
      : (Array.isArray(message.content) ? message.content : []).filter(block => block.type === 'tool_result')
        .map(block => ({ callId: block.tool_use_id, isError: block.is_error === true, result: parseResult(block.content) })))
    const runtimeTool = tools.find(tool => tool?.name === 'mnemon_runtime_memory')
    const sequence = ++requestNumber
    const id = `issue281-${sequence}`
    const observation = { request: sequence, activity: titleRequest ? 'session-title' : 'agent', command: command ?? null, protocol: messagesApi ? 'messages' : 'chat-completions',
      advertisedTools: tools.map(tool => tool?.name), runtimeTool,
      // Record only the synthetic Runtime injection, never the full system prompt.
      runtimeInjection: messages.flatMap(message => {
        const text = contentText(message.content)
        return [...text.matchAll(/MNEMON RUNTIME MEMORY SNAPSHOT[^]*?<runtime-memory-file name="MEMORY\.md">[^]*?<\/runtime-memory-file>/gu)].map(match => match[0])
      }), receipts,
    }
    let reply = titleRequest ? 'Issue 281 Runtime USER verification' : `Issue 281 fixture ready. Send one command per turn: ${Object.keys(cases).join(', ')}.`
    if (command !== undefined) {
      if (receipts.length > 0) {
        const receipt = receipts.at(-1)
        const successful = receipt.isError !== true && receipt.result?.success === true
        reply = `${command}: ${successful ? 'tool succeeded' : 'tool did not report success'}. Actual receipt:\n\n${JSON.stringify(redact(receipt.result), null, 2)}`
      } else if (runtimeTool === undefined) {
        reply = `${command}: stopped because the real model request did not advertise mnemon_runtime_memory. Select the official Coding preset with native tools.`
      } else {
        reply = { name: runtimeTool.name, args: cases[command] }
        observation.call = { id, ...reply }
      }
    }
    if (typeof reply === 'string') observation.reply = reply
    if (!protocolSmoke) {
      observations.push(redact(observation))
      observationWrite = observationWrite.then(() => privateFile('observations.json', JSON.stringify(observations, null, 2) + '\n'))
      await observationWrite
    }
    const call = typeof reply !== 'string'
    response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' })
    if (messagesApi) {
      const event = value => response.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`)
      event({ type: 'message_start', message: { id, type: 'message', role: 'assistant', model: wire.model, content: [], usage: { input_tokens: 10, output_tokens: 0 } } })
      event({ type: 'content_block_start', index: 0, content_block: call ? { type: 'tool_use', id, name: reply.name, input: {} } : { type: 'text', text: '' } })
      event({ type: 'content_block_delta', index: 0, delta: call ? { type: 'input_json_delta', partial_json: JSON.stringify(reply.args) } : { type: 'text_delta', text: reply } })
      event({ type: 'content_block_stop', index: 0 })
      event({ type: 'message_delta', delta: { stop_reason: call ? 'tool_use' : 'end_turn' }, usage: { output_tokens: 5 } })
      event({ type: 'message_stop' })
      response.end()
    } else {
      const delta = call ? { role: 'assistant', tool_calls: [{ index: 0, id, type: 'function', function: { name: reply.name, arguments: JSON.stringify(reply.args) } }] } : { role: 'assistant', content: reply }
      for (const choice of [{ index: 0, delta, finish_reason: null }, { index: 0, delta: {}, finish_reason: call ? 'tool_calls' : 'stop' }]) response.write(`data: ${JSON.stringify({ id, choices: [choice] })}\n\n`)
      response.end('data: [DONE]\n\n')
    }
  } catch (error) {
    await privateFile('model-error.txt', String(error))
    response.writeHead(500, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ error: { message: 'Synthetic issue 281 fixture failed; inspect private model-error.txt' } }))
  }
})
await new Promise(done => model.listen(0, '127.0.0.1', done))
const modelUrl = `http://127.0.0.1:${model.address().port}`
// Test text and tool-use SSE through the official rc.2 adapter before browser work.
const { DeepSeekAdapter, resolveAdapterOptions } = await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-llm-deepseek')).href)
const adapter = new DeepSeekAdapter({
  options: () => resolveAdapterOptions({ baseURL: modelUrl, thinking: 'disabled' }),
  resolveAuth: async () => ({ headers: { 'x-api-key': 'isolated-test-key' } }),
  resolveUserId: () => 'synthetic-user-branches-fixture', prepareExtensions: async () => ({ fields: {}, accept: async () => {} }),
})
for (const [input, type] of [['Protocol smoke check', 'text'], ['USER281_PREPARE', 'tool-call']]) {
  const chunks = []
  for await (const chunk of adapter.stream({ provider: 'deepseek-official', model: 'deepseek-flash',
    messages: [{ role: 'user', content: [{ type: 'text', text: input }] }],
    tools: [{ name: 'mnemon_runtime_memory', description: 'Protocol smoke only; no tool execution.', parameters: { type: 'object' } }], signal: AbortSignal.timeout(5000),
  })) chunks.push(chunk)
  assert(chunks.some(chunk => chunk.type === 'block-end' && chunk.block.type === type), `Published adapter must parse ${type}`)
  assert(chunks.some(chunk => chunk.type === 'finish'), 'Published adapter must finish the stream')
}
protocolSmoke = false
await privateFile('protocol-smoke.json', JSON.stringify({ publishedAdapter: publishedVersion, text: true, toolCall: true }) + '\n')
await privateFile('fixture.json', JSON.stringify({ issue: 281, dsh: publishedVersion, node: process.version,
  commands: cases, idleReview: false, toolPresentation: 'native', preset: 'standard',
  optionalStrategies: ['scoped', 'light-context', 'auto-capture'],
  artifacts: [...packages.values()].map(pkg => ({ name: pkg.manifest.name, version: pkg.manifest.version, filename: basename(pkg.path), sha256: pkg.sha256 })),
}, null, 2) + '\n')
await writeFile(join(workspace, 'README.md'), '# Synthetic issue 281 project\n\nThis disposable project tests USER.md mutations and MEMORY.md branch scope.\n')
await run('git', ['init', '--initial-branch=main'], { cwd: workspace })
await run('git', ['add', 'README.md'], { cwd: workspace })
await run('git', ['-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-m', 'Synthetic issue 281 fixture'], { cwd: workspace })
await run('git', ['branch', 'experiment'], { cwd: workspace })
const env = { ...process.env, DSH_HOME: dshHome, DSH_TELEMETRY_DISABLED: '1', DSH_TOOLS_MODE: 'native',
  DEEPSEEK_API_KEY: 'isolated-test-key', DEEPSEEK_BASE_URL: modelUrl,
  MNEMON_DATA_DIR: join(state, 'data'), npm_config_registry: registryUrl, NPM_CONFIG_REGISTRY: registryUrl }
const dshBin = join(profile, 'node_modules/@deepseek-ai/dsh/lib/bin.js')
await privateFile('install.log', await run(process.execPath, [dshBin, 'plugin', '--profile', 'web', 'add',
  `file:${packages.get('dsh-mnemon').path}`, '--registry', registryUrl], { cwd: workspace, env }))
// The official Coding preset and native tools stay intact. Configure only the
// supported model/idle-review settings and replace the OS directory dialog.
await writeFile(join(dshHome, 'profiles/web/cordis.patch.yml'), `- id: directory-picker
  disabled: true
- id: llm-deepseek
  config:
    baseURL: ${JSON.stringify(modelUrl)}
    thinking: disabled
- id: mnemon
  config:
    cliPath: ${JSON.stringify(resolve(process.env.MNEMON_CLI_PATH))}
    idleReview:
      enabled: false
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
let web
let hostLog = ''
let stopping = false
let restarting = false
const info = { pid: process.pid, state, workspace, dshHome, port: webPort, restartCount: 0, launchCount: 0 }
const saveInfo = () => privateFile('private.json', JSON.stringify(info, null, 2) + '\n')
const launch = async () => {
  delete info.url
  info.launchCount += 1
  info.startedAt = new Date().toISOString()
  web = spawn(process.execPath, [dshBin, 'web', '--no-open', '--host', '127.0.0.1', '--port', String(webPort)], { cwd: workspace, env, stdio: ['ignore', 'pipe', 'pipe'] })
  info.webPid = web.pid
  await saveInfo()
  const capture = chunk => {
    hostLog += chunk.toString()
    const plain = hostLog.replace(/\x1b\[[0-9;]*m/gu, '')
    const urls = plain.match(/http:\/\/127\.0\.0\.1:\d+[^\s]*/gu) ?? []
    const url = urls.findLast(value => /[?&](?:token|key)=/u.test(value))
    if (url) info.url = url
    void privateFile('host.log', hostLog)
    void saveInfo()
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
  await privateFile(`host-before-restart-${info.restartCount + 1}.log`, hostLog)
  hostLog = ''
  info.restartCount += 1
  await launch()
  restarting = false
})
await launch()
console.log('Fixture started. Private connection metadata: ' + join(state, 'private.json'))
