#!/usr/bin/env node
// Real DSH WebUI with disposable state and a loopback-only model stub.
// Run after pnpm build && pnpm --workspace-concurrency=4 -r build; stop with Ctrl-C to remove the fixture.
// Set MNEMON_E2E_PORT to keep one WebUI address across SIGUSR2 restarts.
// --live-model answers with the real DeepSeek API instead: it reads DEEPSEEK_API_KEY
// (and DEEPSEEK_BASE_URL when set) from the caller's environment and never stores them.
import { spawn } from 'node:child_process'
import { createServer } from 'node:http'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { documentProtectionModel } from './fixtures/document-protection-model.mjs'
import { documentArchiveModel } from './fixtures/document-archive-model.mjs'
import { runtimeRoutingModel } from './fixtures/runtime-routing-model.mjs'
import { runtimeWriteScopeModel } from './fixtures/runtime-write-scope-model.mjs'
import { resultToolCacheModel } from './fixtures/result-tool-cache-model.mjs'
import { legacySessionReplayModel } from './fixtures/legacy-session-replay-model.mjs'
import { reviewEvidenceModel, scopedOverviewPlugin } from './fixtures/review-evidence-model.mjs'
import { openVikingWriteModel } from './fixtures/openviking-write-model.mjs'
import { idleReviewModel } from './fixtures/idle-review-model.mjs'
import { reviewLayersModel } from './fixtures/review-layers-model.mjs'
import { strictTemplateModel } from './fixtures/strict-template-model.mjs'
import { generalStrategyModel } from './fixtures/general-strategy-model.mjs'
import { DOCS_DEMO_LANGUAGES, docsDemoAssistant, docsDemoModel, seedDocsDemo } from './fixtures/docs-demo.mjs'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const flags = new Set(process.argv.slice(2))
let betterSidebarRoot
let electronExecutable
let trustedHost
// Documentation media: a seeded fictional project and a model that reads it through the real tools.
let docsDemo
for (const flag of flags) {
  if (flag === '--strategy-extensions') continue
  if (flag === '--document-protection') continue
  if (flag === '--document-archive') continue
  if (flag === '--review-failure') continue
  if (flag === '--runtime-archive') continue
  if (flag === '--runtime-routing') continue
  if (flag === '--runtime-write-scope') continue
  if (flag === '--result-tool-cache') continue
  if (flag === '--legacy-session-replay') continue
  if (flag === '--review-evidence') continue
  if (flag === '--openviking-write') continue
  if (flag === '--idle-review') continue
  if (flag === '--review-layers') continue
  if (flag === '--strict-template') continue
  if (flag === '--general-strategy') continue
  if (flag === '--without-mnemon-cli') continue
  if (flag === '--remote-management') continue
  if (flag === '--live-model') continue
  if (flag === '--docs-demo' || flag.startsWith('--docs-demo=')) {
    docsDemo = flag === '--docs-demo' ? 'zh-CN' : flag.slice('--docs-demo='.length)
    if (!DOCS_DEMO_LANGUAGES.includes(docsDemo)) throw new Error('--docs-demo accepts ' + DOCS_DEMO_LANGUAGES.join(' or '))
    continue
  }
  if (flag.startsWith('--electron=')) {
    const value = flag.slice('--electron='.length)
    if (value === '') throw new Error('--electron requires an Electron executable')
    electronExecutable = resolve(value)
    continue
  }
  if (flag.startsWith('--better-sidebar=')) {
    const value = flag.slice('--better-sidebar='.length)
    if (value === '') throw new Error('--better-sidebar requires a package directory')
    betterSidebarRoot = resolve(value)
    continue
  }
  // A remote page: DSH's browser-trust fence accepts this extra authority.
  if (flag.startsWith('--trusted-host=')) {
    trustedHost = flag.slice('--trusted-host='.length)
    if (!/^[a-z0-9.-]+(?::\d+)?$/iu.test(trustedHost)) throw new Error('--trusted-host requires a host or host:port authority')
    continue
  }
  throw new Error('Unknown option: ' + flag)
}
const extensionNames = ['dsh-mnemon-strategy-scoped', 'dsh-mnemon-strategy-light-context', 'dsh-mnemon-strategy-auto-capture']
// Strategy packages that also register themselves as bundles; the Starter row owns them.
const selfRegistering = new Set([...extensionNames, 'dsh-mnemon-strategy-general'])
const extensionsEnabled = flags.has('--strategy-extensions')
const liveModel = flags.has('--live-model')
if (liveModel && !process.env.DEEPSEEK_API_KEY?.trim()) throw new Error('--live-model reads the model key from DEEPSEEK_API_KEY')
const runtimeArchive = flags.has('--runtime-archive')
const fixture = await mkdtemp(join(tmpdir(), 'mnemon-web-e2e-'))
const dshHome = join(fixture, 'dsh-home')
const dataDir = join(fixture, 'data')
const workspace = join(fixture, 'workspace')
await Promise.all([dshHome, dataDir, workspace].map(path => mkdir(path)))
let modelRequests = 0
let archiveWrites = 0
const archiveProvider = runtimeArchive ? createServer(async (request, response) => {
  let input = ''
  for await (const chunk of request) input += chunk
  const path = request.url ?? ''
  let value = {}
  if (path === '/v1/default/banks') value = { banks: [{ bank_id: 'archive-fixture', name: 'Async archive fixture' }] }
  else if (path.endsWith('/memories') && request.method === 'POST') {
    archiveWrites += JSON.parse(input).items.length
    console.log('Runtime archive accepted writes: ' + archiveWrites)
    value = { operation_id: 'fixture-operation-' + archiveWrites }
  } else if (path.endsWith('/stats')) value = { total_nodes: archiveWrites, total_links: 0 }
  else if (path.includes('/memories/recall')) value = { results: [] }
  else if (path.includes('/entities') || path.includes('/memories/list')) value = { items: [] }
  response.writeHead(200, { 'content-type': 'application/json' })
  response.end(JSON.stringify(value))
}) : undefined
if (archiveProvider) await new Promise(resolveListen => archiveProvider.listen(0, '127.0.0.1', resolveListen))
const protectionModel = flags.has('--document-protection') ? documentProtectionModel(event => console.log('Document protection: ' + JSON.stringify(event))) : undefined
const reviewModel = flags.has('--review-evidence') ? reviewEvidenceModel(event => console.log('Review evidence: ' + JSON.stringify(event))) : undefined
const scriptedModel = liveModel ? undefined : flags.has('--runtime-routing') ? runtimeRoutingModel(event => console.log('Runtime routing: ' + JSON.stringify(event)))
  : flags.has('--openviking-write') ? openVikingWriteModel(event => console.log('OpenViking write: ' + JSON.stringify(event)))
  : flags.has('--idle-review') ? idleReviewModel(event => console.log('Idle review: ' + JSON.stringify(event)))
  : flags.has('--review-layers') ? reviewLayersModel(event => console.log('Review layers: ' + JSON.stringify(event)))
  : flags.has('--strict-template') ? strictTemplateModel(event => console.log('Strict template: ' + JSON.stringify(event)))
  : flags.has('--general-strategy') ? generalStrategyModel(event => console.log('General strategy: ' + JSON.stringify(event)))
  : flags.has('--runtime-write-scope') ? runtimeWriteScopeModel(event => console.log('Runtime write scope: ' + JSON.stringify(event)))
  : flags.has('--result-tool-cache') ? resultToolCacheModel(event => console.log('Result tool cache: ' + JSON.stringify(event)))
  : flags.has('--legacy-session-replay') ? legacySessionReplayModel(event => console.log('Legacy replay: ' + JSON.stringify(event)))
  : flags.has('--document-archive') ? documentArchiveModel(event => console.log('Document archive: ' + JSON.stringify(event)))
  : docsDemo !== undefined ? docsDemoModel(docsDemo, event => console.log('Docs demo: ' + JSON.stringify(event))) : reviewModel ?? protectionModel
const reviewFailure = flags.has('--review-failure')
/**
 * DSH's DeepSeek adapter speaks the Messages protocol. Scripted fixtures read
 * one neutral request: system text as a system message, tool results as tool
 * messages, tool calls on assistant messages and tools as functions.
 */
function fixtureRequest(wire) {
  const blocks = content => typeof content === 'string' ? [{ type: 'text', text: content }] : content ?? []
  const text = content => blocks(content).filter(block => block.type === 'text').map(block => block.text).join('\n')
  const messages = (wire.messages ?? []).flatMap(message => {
    const results = blocks(message.content).filter(block => block.type === 'tool_result')
      .map(block => ({ role: 'tool', tool_call_id: block.tool_use_id, content: text(block.content) }))
    const calls = blocks(message.content).filter(block => block.type === 'tool_use')
      .map(block => ({ id: block.id, type: 'function', function: { name: block.name, arguments: JSON.stringify(block.input ?? {}) } }))
    const body = text(message.content)
    return [...results, ...(body === '' && calls.length === 0 ? [] : [{ role: message.role, content: body, ...(calls.length === 0 ? {} : { tool_calls: calls }) }])]
  })
  return {
    ...wire,
    messages: [...(wire.system === undefined ? [] : [{ role: 'system', content: text(wire.system) }]), ...messages],
    tools: (wire.tools ?? []).map(tool => ({ type: 'function', function: { name: tool.name, description: tool.description, parameters: tool.input_schema } })),
  }
}
const model = createServer(async (request, response) => {
  let input = ''
  for await (const chunk of request) input += chunk
  console.log('Fixture model request: ' + ++modelRequests)
  const wire = input === '' ? {} : JSON.parse(input)
  const reviewPersona = reviewFailure ? fixtureRequest(wire).messages
    .filter(message => message.role === 'system').map(message => message.content).join('\n') : ''
  if (/Completion protocol: call `mnemon_subagent_result(?:_[^`]+)?`/u.test(reviewPersona)) {
    console.log('Review fixture: rejected inherited context with CONTEXT_WINDOW_EXCEEDED')
    response.writeHead(400, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ error: { code: 'CONTEXT_WINDOW_EXCEEDED', message: 'request (145508 tokens) exceeds the available context size (98304 tokens)' } }))
    return
  }
  let reply
  try { reply = scriptedModel?.(fixtureRequest(wire)) ?? 'Isolated Mnemon WebUI test response.' }
  catch (error) {
    console.error(error)
    response.writeHead(500, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ error: { message: error instanceof Error ? error.message : String(error) } }))
    return
  }
  if (typeof reply === 'object' && reply.error !== undefined) {
    response.writeHead(reply.status ?? 400, { 'content-type': 'application/json' })
    // A server failure answers as Ollama's Messages endpoint does.
    response.end(JSON.stringify(reply.status === undefined ? { error: { message: reply.error } } : { type: 'error', error: { type: 'api_error', message: reply.error } }))
    return
  }
  response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' })
  const call = typeof reply !== 'string'
  const event = value => response.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`)
  event({ type: 'message_start', message: { id: 'mnemon-web-e2e-' + modelRequests, type: 'message', role: 'assistant', model: wire.model, content: [], usage: { input_tokens: 10, output_tokens: 0 } } })
  event({ type: 'content_block_start', index: 0, content_block: call ? { type: 'tool_use', id: 'fixture-call-' + modelRequests, name: reply.name, input: {} } : { type: 'text', text: '' } })
  event({ type: 'content_block_delta', index: 0, delta: call ? { type: 'input_json_delta', partial_json: JSON.stringify(reply.args) } : { type: 'text_delta', text: reply } })
  event({ type: 'content_block_stop', index: 0 })
  event({ type: 'message_delta', delta: { stop_reason: call ? 'tool_use' : 'end_turn' }, usage: { output_tokens: 5 } })
  event({ type: 'message_stop' })
  response.end()
})
await new Promise((resolveListen, reject) => {
  model.once('error', reject)
  model.listen(0, '127.0.0.1', resolveListen)
})
const env = {
  ...process.env,
  DSH_HOME: dshHome,
  DSH_TELEMETRY_DISABLED: '1',
  // A live run keeps the caller's key and endpoint; otherwise the loopback stub answers.
  ...(liveModel ? {} : { DEEPSEEK_API_KEY: 'isolated-test-key', DEEPSEEK_BASE_URL: `http://127.0.0.1:${model.address().port}` }),
  MNEMON_DATA_DIR: dataDir,
}
const dshBin = join(root, 'node_modules/@deepseek-ai/dsh/lib/bin.js')
let web
let stopping = false
let restarting = false
function launch() {
  const args = [dshBin, 'web', '--no-open', '--host', '127.0.0.1', '--port', process.env.MNEMON_E2E_PORT ?? '0',
    ...(trustedHost === undefined ? [] : ['--trusted-host', trustedHost])]
  const hostEnv = { ...env }
  if (electronExecutable !== undefined) {
    for (const key of Object.keys(hostEnv)) if (key.toUpperCase() === 'ELECTRON_RUN_AS_NODE') delete hostEnv[key]
    args.unshift('--expose-internals', join(root, 'scripts/fixtures/electron-dsh-host.cjs'))
  }
  web = spawn(electronExecutable ?? process.execPath, args, { env: hostEnv, cwd: workspace, stdio: 'inherit' })
  web.once('error', error => { console.error(error); process.exitCode = 1; void stop() })
  web.once('exit', code => { if (!stopping && !restarting) { if (code) process.exitCode = code; void stop() } })
}
async function restart() {
  if (stopping || restarting || !web || web.exitCode !== null) return
  restarting = true
  web.kill('SIGTERM')
  await new Promise(resolveExit => web.once('exit', resolveExit))
  if (!stopping) { console.log('Restarting the same disposable Profile'); launch() }
  restarting = false
}
async function stop() {
  if (stopping) return
  stopping = true
  if (web && web.exitCode === null) {
    web.kill('SIGTERM')
    await new Promise(resolveExit => web.once('exit', resolveExit))
  }
  model.closeAllConnections()
  await new Promise(resolveClose => model.close(resolveClose))
  if (archiveProvider) {
    archiveProvider.closeAllConnections()
    await new Promise(resolveClose => archiveProvider.close(resolveClose))
  }
  await rm(fixture, { recursive: true, force: true })
  console.log('Removed disposable WebUI fixture: ' + fixture)
}
process.once('SIGINT', () => { void stop() })
process.once('SIGTERM', () => { void stop() })
process.on('SIGUSR2', () => { void restart() })

try {
  const manifest = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'))
  // Root owns the disabled enhancement Entries. Adding their self-registering
  // bundles as separate Profile layers would intentionally duplicate ids.
  const plugins = Object.keys(manifest.dependencies).filter(name => name.startsWith('dsh-mnemon-') && !selfRegistering.has(name))
  const installer = spawn(process.execPath, [dshBin, 'plugin', '--profile', 'web', 'add',
    `link:${root}`, ...plugins.map(name => `link:${join(root, 'plugins', name)}`),
    ...(betterSidebarRoot === undefined ? [] : [`link:${betterSidebarRoot}`]),
  ], { env, cwd: workspace, stdio: 'inherit' })
  await new Promise((resolveInstall, reject) => {
    installer.once('error', reject)
    installer.once('exit', code => code === 0 ? resolveInstall() : reject(new Error('DSH installation failed: ' + code)))
  })
  // Leave the entire real WebUI/plugin stack enabled. Only unrelated native
  // PTY/search tools are disabled so a test cannot launch workspace commands.
  const disabled = ['subprocess', 'open-in-app', 'bash-sandbox', 'pwsh-sandbox', 'tool-bash', 'tool-pwsh', 'permission', 'tool-fs-search', 'directory-picker']
  // A test-owned preset uses all Host memory tools without the shipped
  // presets' shell requirements. Never modify a shipped DSH preset. The docs
  // demo names it after its fictional project.
  const assistant = docsDemo === undefined
    ? { name: 'Mnemon E2E', description: 'Isolated memory UI test (no Shell).', persona: 'You are testing the Mnemon memory UI.' }
    : docsDemoAssistant(docsDemo)
  const browsePicker = `- id: agent-preset-registry
  config:
    default: mnemon-e2e
- insert:
    - id: preset-mnemon-e2e
      name: '@deepseek-ai/dsh-agent-preset'
      config:
        id: mnemon-e2e
        name: ${JSON.stringify(assistant.name)}
        description: ${JSON.stringify(assistant.description)}
        order: 0
        plugins:
          - id: persona
            name: '@deepseek-ai/dsh-persona'
            config:
              prefix: ${JSON.stringify(assistant.persona)}
    - id: e2e-directory-picker
      name: '@deepseek-ai/dsh-host-directory-picker-browse'
    - id: e2e-directory-picker-ui
      name: '@deepseek-ai/dsh-client-ui-directory-picker-browse'
`
  const reviewFixture = join(fixture, 'review-evidence-plugin.mjs')
  if (reviewModel !== undefined) await writeFile(reviewFixture, scopedOverviewPlugin)
  await writeFile(join(dshHome, 'profiles/web/cordis.patch.yml'), disabled.map(id => `- id: ${id}\n  disabled: true\n`).join('') + browsePicker
    + (protectionModel === undefined && reviewModel === undefined && !reviewFailure ? '' : '- id: mnemon\n  config:\n    idleReviewMs: 5000\n')
    + (runtimeArchive ? '- id: mnemon\n  config:\n    persistenceStrategy:\n      mode: manual\n    runtimeMemory:\n      memoryLimitBytes: 300\n' : '')
    + (flags.has('--idle-review') || flags.has('--review-layers') || flags.has('--strict-template') ? '- id: mnemon\n  config:\n    idleReviewMs: 5000\n    idleReview:\n      minIntervalMs: 5000\n      maxPerSession: 1\n' : '')
    + (flags.has('--runtime-routing') ? '- id: mnemon\n  config:\n    runtimeMemory:\n      memoryLimitBytes: 1600\n' : '')
    // An explicit cliPath is authoritative, so a missing file hides any installed Mnemon CLI.
    + (flags.has('--without-mnemon-cli') ? '- id: mnemon\n  config:\n    cliPath: ' + JSON.stringify(join(fixture, 'no-mnemon-cli', 'mnemon')) + '\n' : '')
    + (flags.has('--remote-management') ? '- id: mnemon\n  config:\n    remoteAccess: trusted-host\n' : '')
    + (flags.has('--general-strategy') ? '- id: mnemon-strategy-general\n  disabled: false\n- id: mnemon-strategy-default-three-tier\n  disabled: true\n- id: mnemon\n  config:\n    memoryView:\n      strategyTypeId: general\n' : '')
    + (flags.has('--runtime-write-scope') ? '- id: mnemon\n  config:\n    persistenceStrategy:\n      mode: manual\n      providerId: mnemon-native\n    runtimeMemory:\n      memoryLimitBytes: 512\n' : '')
    + (reviewModel === undefined ? '' : '- insert:\n    - id: review-evidence-fixture\n      name: ' + JSON.stringify(reviewFixture) + '\n')
    + (extensionsEnabled ? extensionNames.map(name => `- id: ${name.slice(4)}\n  disabled: false\n`).join('') : ''))
  await writeFile(join(workspace, 'README.md'), '# Mnemon isolated browser test\n\nNo production memory or credentials are used.\n')
  if (docsDemo !== undefined) {
    await seedDocsDemo({ dataDir, workspace, language: docsDemo })
    console.log('Docs demo seeded (' + docsDemo + ')' + (liveModel ? '.' : '; ask about checkout, then ask to remember a new target.'))
  }
  console.log('Fixture: ' + fixture)
  console.log('Workspace: ' + workspace)
  console.log('Fixture PID: ' + process.pid + ' (SIGUSR2 restarts WebUI, retaining test data)')
  console.log('For a conversation, choose the ' + assistant.name + ' preset in the WebUI.')
  if (liveModel) console.log('Live model: the DeepSeek API answers; scripted replies are off.')
  if (archiveProvider) console.log('Runtime archive Hindsight endpoint: http://127.0.0.1:' + archiveProvider.address().port)
  launch()
} catch (error) {
  console.error(error)
  process.exitCode = 1
  await stop()
}
