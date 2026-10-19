// Disposable published-DSH acceptance. Script only model choices; retain the
// real Coding preset, adapters, native tools, Client bundles and persistence.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { createReadStream } from 'node:fs'
import { createServer } from 'node:http'
import { mkdir, readFile, readdir, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { basename, dirname, join, resolve } from 'node:path'
import { parseArgs } from 'node:util'
import { pathToFileURL } from 'node:url'

export const wireText = content => typeof content === 'string' ? content : Array.isArray(content) ? content.map(block => block.text ?? '').join('\n') : ''
const parseResult = content => { const text = wireText(content); try { return JSON.parse(text) } catch { return { text } } }
export const actualReceipt = receipt => `${receipt?.isError !== true && receipt?.result?.success === true ? 'Tool succeeded' : 'Tool did not report success'}. Actual receipt:\n\n${JSON.stringify(receipt?.result, null, 2)}`

export async function servePackedWebFixture(scenario) {
  const { values } = parseArgs({ options: {
    profile: { type: 'string' }, artifacts: { type: 'string' }, state: { type: 'string' }, port: { type: 'string', default: '0' },
    ...scenario.options,
  } })
  for (const key of ['profile', 'artifacts', 'state']) assert(values[key], `Missing --${key}`)
  assert(process.env.MNEMON_CLI_PATH, 'Set MNEMON_CLI_PATH to the verified official test CLI')
  const port = Number(values.port)
  assert(Number.isInteger(port) && port >= 0 && port <= 65535, '--port must be 0 through 65535')
  const state = resolve(values.state), profile = resolve(values.profile), artifacts = resolve(values.artifacts)
  const workspace = join(state, 'workspace'), dshHome = join(state, 'dsh-home'), data = join(state, 'data')
  await mkdir(state, { recursive: true, mode: 0o700 })
  assert.equal((await readdir(state)).length, 0, 'Use an empty disposable --state directory')
  for (const directory of [workspace, dshHome, data]) await mkdir(directory, { recursive: true, mode: 0o700 })
  const privateFile = (name, content) => writeFile(join(state, name), content, { mode: 0o600 })
  const run = (command, args, options = {}) => new Promise((fulfill, reject) => {
    const child = spawn(command, args, { env: { ...process.env, PATH: `${dirname(process.execPath)}:${process.env.PATH ?? ''}` }, ...options, stdio: ['ignore', 'pipe', 'pipe'] })
    let output = ''
    child.stdout.on('data', chunk => { output += chunk }); child.stderr.on('data', chunk => { output += chunk })
    child.on('error', reject)
    child.on('close', async code => {
      if (code === 0) fulfill(output)
      else { await privateFile('subprocess-failure.log', output); reject(new Error(`Subprocess exited ${code}; inspect private subprocess-failure.log`)) }
    })
  })
  const require = createRequire(join(profile, 'package.json'))
  const version = JSON.parse(await readFile(require.resolve('@deepseek-ai/dsh/package.json'))).version
  assert.equal(version, '0.1.7-rc.2', 'Use the exact official DSH 0.1.7-rc.2 profile')
  const packages = new Map()
  for (const filename of await readdir(artifacts)) {
    if (!filename.endsWith('.tgz')) continue
    const path = join(artifacts, filename), bytes = await readFile(path)
    const manifest = JSON.parse(await run('tar', ['-xOf', path, 'package/package.json']))
    assert(!packages.has(manifest.name), `Duplicate packed package: ${manifest.name}`)
    packages.set(manifest.name, { manifest, path, integrity: `sha512-${createHash('sha512').update(bytes).digest('base64')}`, sha256: createHash('sha256').update(bytes).digest('hex') })
  }
  assert(packages.has('dsh-mnemon'), 'Packed Root is required')
  const expected = ['dsh-mnemon', ...Object.keys(packages.get('dsh-mnemon').manifest.dependencies)
    .filter(name => name.startsWith('dsh-mnemon-'))].sort()
  assert.deepEqual([...packages.keys()].sort(), expected, 'Use the complete packed Starter composition')
  for (const name of expected.filter(name => name !== 'dsh-mnemon')) {
    assert.equal(packages.get(name).manifest.version, packages.get('dsh-mnemon').manifest.dependencies[name],
      `${name}: packed version must match the Starter pin`)
  }
  let registryUrl
  const registry = createServer((request, response) => {
    const name = decodeURIComponent(new URL(request.url, 'http://localhost').pathname).slice(1)
    if (name.startsWith('tarballs/')) {
      const pkg = packages.get(name.slice(9))
      if (!pkg) { response.writeHead(404); response.end(); return }
      response.writeHead(200, { 'content-type': 'application/octet-stream' }); createReadStream(pkg.path).pipe(response)
    } else if (packages.has(name)) {
      const { manifest, integrity } = packages.get(name)
      response.writeHead(200, { 'content-type': 'application/json' })
      response.end(JSON.stringify({ name, 'dist-tags': { latest: manifest.version }, versions: { [manifest.version]: { ...manifest, dist: { integrity, tarball: `${registryUrl}/tarballs/${name}` } } } }))
    } else { response.writeHead(307, { location: `https://registry.npmjs.org${request.url}` }); response.end() }
  })
  await new Promise(done => registry.listen(0, '127.0.0.1', done))
  registryUrl = `http://127.0.0.1:${registry.address().port}`
  const prepared = await scenario.prepare?.({ values, state, workspace, dshHome, data, privateFile, run }) ?? {}
  const redact = value => JSON.parse(JSON.stringify(value).replaceAll(state, '<fixture-state>').replaceAll(profile, '<official-profile>').replaceAll(artifacts, '<packed-artifacts>').replace(/([?&](?:token|key)=)[^\s"&]+/gu, '$1<redacted>'))
  const observations = []
  let sequence = 0, smoke = true, observationWrite = Promise.resolve()
  const model = createServer(async (request, response) => {
    let input = ''
    for await (const chunk of request) input += chunk
    try {
      const wire = JSON.parse(input), messagesApi = request.url?.endsWith('/messages') === true
      const messages = [...(messagesApi && wire.system ? [{ role: 'system', content: wire.system }] : []), ...(wire.messages ?? [])]
      const tools = (wire.tools ?? []).map(tool => messagesApi ? { name: tool.name, description: tool.description, parameters: tool.input_schema } : tool.function)
      const title = tools.length === 0 && messages.some(message => message.role === 'system' && wireText(message.content).includes('Create a concise title for an AI coding-assistant session'))
        && messages.some(message => message.role === 'user' && wireText(message.content).startsWith('Generate the session title from this JSON array of human messages:'))
      const userIndex = messages.findLastIndex(message => message.role === 'user' && wireText(message.content).trim() !== '')
      const user = userIndex < 0 ? '' : wireText(messages[userIndex].content)
      const receipts = messages.slice(userIndex + 1).flatMap(message => message.role === 'tool' ? [{ callId: message.tool_call_id, result: parseResult(message.content) }]
        : (Array.isArray(message.content) ? message.content : []).filter(block => block.type === 'tool_result').map(block => ({ callId: block.tool_use_id, isError: block.is_error === true, result: parseResult(block.content) })))
      const runtimeProjections = messages.flatMap(message => [...wireText(message.content).matchAll(/MNEMON RUNTIME MEMORY SNAPSHOT[^]*?<runtime-memory-file name="MEMORY\.md">[^]*?<\/runtime-memory-file>/gu)].map(match => match[0]))
      const requestId = ++sequence, id = `issue${scenario.issue}-${requestId}`
      const result = smoke ? { reply: user === 'fixture-tool-smoke' ? { name: 'fixture_ping', args: {} } : 'Protocol smoke complete.' }
        : title ? { reply: `Issue ${scenario.issue} acceptance` } : await scenario.complete({ messages, tools, user, receipts, runtimeProjections })
      const reply = result.reply
      assert(typeof reply === 'string' || (reply && typeof reply.name === 'string' && reply.args), 'Scenario must return text or a native tool choice')
      const call = typeof reply !== 'string'
      if (!smoke) {
        observations.push(redact({ request: requestId, activity: title ? 'session-title' : 'agent', protocol: messagesApi ? 'messages' : 'chat-completions',
          advertisedTools: tools.map(tool => tool.name), runtimeTool: tools.find(tool => tool.name === 'mnemon_runtime_memory'), runtimeProjections, receipts,
          ...result.observation, ...(call ? { call: { id, ...reply } } : { reply }),
        }))
        observationWrite = observationWrite.then(() => privateFile('observations.json', JSON.stringify(observations, null, 2) + '\n'))
        await observationWrite
      }
      response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' })
      if (messagesApi) {
        const event = value => response.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`)
        event({ type: 'message_start', message: { id, type: 'message', role: 'assistant', model: wire.model, content: [], usage: { input_tokens: 10, output_tokens: 0 } } })
        event({ type: 'content_block_start', index: 0, content_block: call ? { type: 'tool_use', id, name: reply.name, input: {} } : { type: 'text', text: '' } })
        event({ type: 'content_block_delta', index: 0, delta: call ? { type: 'input_json_delta', partial_json: JSON.stringify(reply.args) } : { type: 'text_delta', text: reply } })
        event({ type: 'content_block_stop', index: 0 }); event({ type: 'message_delta', delta: { stop_reason: call ? 'tool_use' : 'end_turn' }, usage: { output_tokens: 5 } }); event({ type: 'message_stop' }); response.end()
      } else {
        const delta = call ? { role: 'assistant', tool_calls: [{ index: 0, id, type: 'function', function: { name: reply.name, arguments: JSON.stringify(reply.args) } }] } : { role: 'assistant', content: reply }
        for (const choice of [{ index: 0, delta, finish_reason: null }, { index: 0, delta: {}, finish_reason: call ? 'tool_calls' : 'stop' }]) response.write(`data: ${JSON.stringify({ id, choices: [choice] })}\n\n`)
        response.end('data: [DONE]\n\n')
      }
    } catch (error) {
      await privateFile('model-error.txt', String(error)); response.writeHead(500, { 'content-type': 'application/json' }); response.end(JSON.stringify({ error: { message: 'Fixture model failed; inspect private model-error.txt' } }))
    }
  })
  await new Promise(done => model.listen(0, '127.0.0.1', done))
  const modelUrl = `http://127.0.0.1:${model.address().port}`
  const { DeepSeekAdapter, resolveAdapterOptions } = await import(pathToFileURL(require.resolve('@deepseek-ai/dsh-llm-deepseek')).href)
  const adapter = new DeepSeekAdapter({ options: () => resolveAdapterOptions({ baseURL: modelUrl, thinking: 'disabled' }), resolveAuth: async () => ({ headers: { 'x-api-key': 'isolated-test-key' } }), resolveUserId: () => `synthetic-issue-${scenario.issue}`, prepareExtensions: async () => ({ fields: {}, accept: async () => {} }) })
  for (const [input, type] of [['fixture-text-smoke', 'text'], ['fixture-tool-smoke', 'tool-call']]) {
    const chunks = []
    for await (const chunk of adapter.stream({ provider: 'deepseek-official', model: 'deepseek-flash', messages: [{ role: 'user', content: [{ type: 'text', text: input }] }], tools: [{ name: 'fixture_ping', description: 'Protocol smoke only.', parameters: { type: 'object' } }], signal: AbortSignal.timeout(5000) })) chunks.push(chunk)
    assert(chunks.some(chunk => chunk.type === 'block-end' && chunk.block.type === type), `Published adapter must parse ${type}`)
    assert(chunks.some(chunk => chunk.type === 'finish'), 'Published adapter must finish')
  }
  smoke = false
  await privateFile('protocol-smoke.json', JSON.stringify({ publishedAdapter: version, text: true, toolCall: true }) + '\n')
  await privateFile('fixture.json', JSON.stringify({ issue: scenario.issue, dsh: version, node: process.version, idleReview: false, preset: 'standard', toolPresentation: 'native', optionalStrategies: ['scoped', 'light-context', 'auto-capture'], ...prepared.metadata,
    artifacts: [...packages.values()].map(pkg => ({ name: pkg.manifest.name, version: pkg.manifest.version, filename: basename(pkg.path), sha256: pkg.sha256 })),
  }, null, 2) + '\n')
  await writeFile(join(workspace, 'README.md'), `# Synthetic issue ${scenario.issue} project\n\nDisposable acceptance data only.\n`)
  await run('git', ['init', '--initial-branch=main'], { cwd: workspace }); await run('git', ['add', 'README.md'], { cwd: workspace })
  await run('git', ['-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', 'commit', '-m', `Synthetic issue ${scenario.issue} fixture`], { cwd: workspace })
  const env = { ...process.env, PATH: `${dirname(process.execPath)}:${process.env.PATH ?? ''}`, DSH_HOME: dshHome, DSH_TELEMETRY_DISABLED: '1', DSH_TOOLS_MODE: 'native', DEEPSEEK_API_KEY: 'isolated-test-key', DEEPSEEK_BASE_URL: modelUrl, MNEMON_DATA_DIR: data, npm_config_registry: registryUrl, NPM_CONFIG_REGISTRY: registryUrl }
  const dshBin = require.resolve('@deepseek-ai/dsh/package.json').replace(/package\.json$/u, 'lib/bin.js')
  await privateFile('install.log', await run(process.execPath, [dshBin, 'plugin', '--profile', 'web', 'add', `file:${packages.get('dsh-mnemon').path}`, ...(prepared.packages ?? []).map(path => `file:${path}`), '--registry', registryUrl], { cwd: workspace, env }))
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
${prepared.patch ?? ''}`)
  let web, hostLog = '', stopping = false, restarting = false
  const info = { pid: process.pid, state, workspace, dshHome, port, restartCount: 0, launchCount: 0 }
  const saveInfo = () => privateFile('private.json', JSON.stringify(info, null, 2) + '\n')
  const launch = async () => {
    delete info.url; info.launchCount += 1; info.startedAt = new Date().toISOString()
    web = spawn(process.execPath, [dshBin, 'web', '--no-open', '--host', '127.0.0.1', '--port', String(port)], { cwd: workspace, env, stdio: ['ignore', 'pipe', 'pipe'] })
    info.webPid = web.pid
    const capture = chunk => {
      hostLog += chunk.toString()
      const url = (hostLog.replace(/\x1b\[[0-9;]*m/gu, '').match(/http:\/\/127\.0\.0\.1:\d+[^\s]*/gu) ?? []).findLast(value => /[?&](?:token|key)=/u.test(value))
      if (url) info.url = url
      void privateFile('host.log', hostLog); void saveInfo()
    }
    web.stdout.on('data', capture); web.stderr.on('data', capture)
    web.on('exit', code => { if (!stopping && !restarting) { process.exitCode = code ?? 1; void stop() } })
    await saveInfo()
  }
  const stop = async () => {
    if (stopping) return
    stopping = true
    if (web?.exitCode === null) { web.kill('SIGTERM'); await new Promise(done => web.once('exit', done)) }
    model.closeAllConnections(); registry.closeAllConnections()
    await Promise.all([new Promise(done => model.close(done)), new Promise(done => registry.close(done))])
  }
  process.once('SIGINT', () => { void stop() }); process.once('SIGTERM', () => { void stop() })
  process.on('SIGUSR2', async () => {
    if (stopping || restarting || web?.exitCode !== null) return
    restarting = true; web.kill('SIGTERM'); await new Promise(done => web.once('exit', done))
    await privateFile(`host-before-restart-${++info.restartCount}.log`, hostLog); hostLog = ''; await launch(); restarting = false
  })
  await launch()
  console.log('Fixture started. Private connection metadata: ' + join(state, 'private.json'))
}
