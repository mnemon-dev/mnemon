import { spawn } from 'node:child_process'
import { createServer } from 'node:http'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const dshBin = join(root, 'node_modules', '@deepseek-ai', 'dsh', 'lib', 'bin.js')
const marker = 'HEADLESS_MNEMON_READY'
const arguments_ = process.argv.slice(2)
const options = new Map()
for (let index = 0; index < arguments_.length; index += 2) {
  if (!['--package', '--registry', '--strategy-extensions', '--upgrade-from', '--upgrade-registry'].includes(arguments_[index]) || !arguments_[index + 1]) throw new Error('Expected package, registry, upgrade, and/or strategy-extension options in key/value pairs')
  options.set(arguments_[index], arguments_[index + 1])
}
if (options.has('--package') !== options.has('--registry')) throw new Error('--package and --registry must be supplied together')
if (options.has('--upgrade-from') !== options.has('--upgrade-registry')) throw new Error('--upgrade-from and --upgrade-registry must be supplied together')
if (options.has('--upgrade-from') && !options.has('--package')) throw new Error('--upgrade-from requires a target --package')
if (options.has('--strategy-extensions') && options.get('--strategy-extensions') !== 'true') throw new Error('--strategy-extensions expects true')
const extensionNames = ['dsh-mnemon-strategy-scoped', 'dsh-mnemon-strategy-light-context', 'dsh-mnemon-strategy-auto-capture']
const extensionNameSet = new Set(extensionNames)
const extensionsEnabled = options.has('--strategy-extensions')

function run(args, { cwd = root, env = process.env, timeoutMs = 30_000 } = {}) {
  return new Promise((resolveRun, reject) => {
    const child = spawn(process.execPath, [dshBin, ...args], {
      cwd,
      env,
      shell: false,
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    let stdout = ''
    let stderr = ''
    child.stdout.setEncoding('utf8').on('data', chunk => { stdout += chunk })
    child.stderr.setEncoding('utf8').on('data', chunk => { stderr += chunk })
    const timeout = setTimeout(() => {
      child.kill('SIGTERM')
      reject(new Error(`dsh Headless verification timed out after ${timeoutMs}ms`))
    }, timeoutMs)
    child.on('error', (error) => {
      clearTimeout(timeout)
      reject(error)
    })
    child.on('exit', (code, signal) => {
      clearTimeout(timeout)
      resolveRun({ code, signal, stdout, stderr })
    })
  })
}

function assertSuccess(label, result) {
  if (result.code === 0) return
  throw new Error([
    `${label} failed with code ${String(result.code)}${result.signal === null ? '' : ` (${result.signal})`}`,
    result.stdout.trim(),
    result.stderr.trim(),
  ].filter(Boolean).join('\n'))
}

const requests = []
const server = createServer(async (request, response) => {
  try {
    let body = ''
    request.setEncoding('utf8')
    for await (const chunk of request) body += chunk
    requests.push(JSON.parse(body))
    response.writeHead(200, {
      'content-type': 'text/event-stream',
      'cache-control': 'no-cache',
      connection: 'keep-alive',
    })
    // DSH's DeepSeek adapter speaks the Messages streaming protocol.
    const model = requests.at(-1).model
    const event = value => response.write(`event: ${value.type}\ndata: ${JSON.stringify(value)}\n\n`)
    event({ type: 'message_start', message: { id: `msg-${requests.length}`, type: 'message', role: 'assistant', model, content: [], usage: { input_tokens: 10, output_tokens: 0 } } })
    event({ type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } })
    event({ type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: marker } })
    event({ type: 'content_block_stop', index: 0 })
    event({ type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 3 } })
    event({ type: 'message_stop' })
    response.end()
  } catch (error) {
    response.writeHead(500, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ error: { message: error instanceof Error ? error.message : String(error) } }))
  }
})

const temporaryRoot = await mkdtemp(join(tmpdir(), 'dsh-mnemon-headless-'))
const dshHome = join(temporaryRoot, 'dsh-home')
const storageRoot = join(temporaryRoot, 'mnemon-data')
const workspaceRoot = join(temporaryRoot, 'workspace')
await Promise.all([mkdir(dshHome), mkdir(storageRoot), mkdir(workspaceRoot)])

try {
  await new Promise((resolveListen, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolveListen)
  })
  const address = server.address()
  if (address === null || typeof address === 'string') throw new Error('mock DeepSeek server did not expose a TCP port')
  const env = {
    ...process.env,
    DSH_HOME: dshHome,
    DSH_TELEMETRY_DISABLED: '1',
    DEEPSEEK_API_KEY: 'headless-verification-key',
    DEEPSEEK_BASE_URL: `http://127.0.0.1:${address.port}`,
    MNEMON_DATA_DIR: storageRoot,
    ...(options.has('--registry') ? { npm_config_registry: options.get('--registry'), NPM_CONFIG_REGISTRY: options.get('--registry') } : {}),
  }

  const settingsPath = join(dshHome, 'settings.yaml')
  const legacySettings = '# Legacy placement migration fixture\nmnemon:\n  displayMode: buildin\n  timeoutMs: 25000\n'
  await writeFile(settingsPath, legacySettings)

  let upgradeMemory
  if (options.has('--upgrade-from')) {
    const initialInstall = await run(['plugin', '--profile', 'headless', 'add', options.get('--upgrade-from'), '--registry', options.get('--upgrade-registry')], { env, timeoutMs: 120_000 })
    assertSuccess(`installing ${options.get('--upgrade-from')} before the upgrade`, initialInstall)
    const initialExecution = await run(['--profile', 'headless', 'Initialize isolated memory before upgrading Mnemon.'], { cwd: workspaceRoot, env })
    assertSuccess('running the pre-upgrade Headless profile', initialExecution)
    if (!initialExecution.stdout.includes(marker)) throw new Error(`Pre-upgrade Headless output did not contain ${marker}:\n${initialExecution.stdout}`)
    upgradeMemory = await readFile(join(storageRoot, 'runtime', 'memories.json'), 'utf8')
    requests.length = 0
  }

  // link: skips dependency installation. Explicitly link the same plugin set
  // that a normal install resolves from the Starter's semver dependencies.
  const manifest = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'))
  // Root owns the disabled enhancement Entries. Adding their self-registering
  // bundles as separate Profile layers would intentionally duplicate ids.
  const plugins = Object.keys(manifest.dependencies).filter(name => name.startsWith('dsh-mnemon-') && !extensionNameSet.has(name))
  const packages = options.has('--package')
    ? [options.get('--package')]
    : [`link:${root}`, ...plugins.map(name => `link:${join(root, 'plugins', name)}`)]
  const install = await run(['plugin', '--profile', 'headless', 'add', ...packages,
    ...(options.has('--registry') ? ['--registry', options.get('--registry')] : []),
  ], { env, timeoutMs: 120_000 })
  assertSuccess('installing dsh-mnemon into the Headless profile', install)

  // The verification exercises Mnemon composition, not DSH's native PTY
  // transport. Disable the unrelated shell stack so the check remains
  // hermetic on CI Node/platform combinations without a node-pty prebuild.
  const profilePatchPath = join(dshHome, 'profiles', 'headless', 'cordis.patch.yml')
  const profileOverrides = `
- id: subprocess
  disabled: true
- id: bash-sandbox
  disabled: true
- id: pwsh-sandbox
  disabled: true
- id: tool-bash
  disabled: true
- id: tool-pwsh
  disabled: true
- id: permission
  disabled: true
- id: tool-fs-search
  disabled: true
- id: ptc-runtime
  disabled: true
- id: workflow-ptc
  disabled: true
- id: tool-workflow
  disabled: true
`.trimStart() + (!extensionsEnabled ? '' : extensionNames.map(name =>
    `- id: ${name.slice(4)}\n  disabled: false\n`,
  ).join(''))
  await writeFile(profilePatchPath, profileOverrides)

  const execution = await run(['--profile', 'headless', 'Verify that the Mnemon tool surface is available.'], {
    cwd: workspaceRoot,
    env,
  })
  assertSuccess('running the Headless profile', execution)
  if (!execution.stdout.includes(marker)) throw new Error(`Headless output did not contain ${marker}:\n${execution.stdout}`)

  const toolRequest = requests.find(request => Array.isArray(request.tools) && request.tools.length > 0)
  if (toolRequest === undefined) throw new Error('Headless model request did not expose any tools')
  const toolNames = new Set(toolRequest.tools.map(tool => tool?.name).filter(name => typeof name === 'string'))
  const required = ['mnemon_status', 'mnemon_recall', 'mnemon_document_search', 'mnemon_document_create', 'mnemon_runtime_memory', 'mnemon_remember', 'mnemon_view_route', 'mnemon_view_action']
  const missing = required.filter(name => !toolNames.has(name))
  if (missing.length > 0) throw new Error(`Headless model request is missing Mnemon tools: ${missing.join(', ')}`)
  if (extensionsEnabled) {
    const prompt = JSON.stringify([toolRequest.system, toolRequest.messages])
    for (const expected of ['Source order expresses preference', 'MNEMON OPTIONAL AUTO CAPTURE']) {
      if (!prompt.includes(expected)) throw new Error(`Optional Strategy contribution did not reach the real DSH prompt: ${expected}`)
    }
  }
  if (!existsSync(join(storageRoot, 'runtime', 'memories.json'))) throw new Error('Headless plugin did not initialize isolated runtime memory')
  if (upgradeMemory !== undefined && await readFile(join(storageRoot, 'runtime', 'memories.json'), 'utf8') !== upgradeMemory) throw new Error('Package upgrade changed existing Runtime Memory bytes')

  // DSH imports the legacy settings file into the profile and keeps it unchanged as a backup.
  if (existsSync(settingsPath) || await readFile(`${settingsPath}.imported`, 'utf8') !== legacySettings) throw new Error('DSH did not retain the legacy settings file as its import backup')
  const imported = await readFile(profilePatchPath, 'utf8')
  if (!imported.includes('displayMode: builtin') || imported.includes('displayMode: buildin')) throw new Error('Headless did not persist the canonical builtin displayMode')
  if (!imported.includes('timeoutMs: 25000')) throw new Error('Placement migration changed unrelated configuration')
  // The next start recovers Mnemon's retained preferences once; later starts change nothing.
  let recovered
  for (const label of ['recovering retained Mnemon settings', 'restarting Headless with recovered Mnemon settings']) {
    const restarted = await run(['--profile', 'headless', 'Verify that normalized Mnemon settings survive a restart.'], { cwd: workspaceRoot, env })
    assertSuccess(label, restarted)
    if (!restarted.stdout.includes(marker)) throw new Error('Restarted Headless did not complete its test turn')
    const current = await readFile(profilePatchPath, 'utf8')
    if (recovered === undefined) {
      if (!current.includes('legacySettingsImported: true') || !current.includes('displayMode: builtin')) throw new Error('Mnemon did not recover retained legacy settings on restart')
      recovered = current
    } else if (current !== recovered) throw new Error('Recovered Mnemon settings were rewritten on restart')
  }

  requests.length = 0
  await writeFile(profilePatchPath, profileOverrides + '- id: mnemon\n  disabled: true\n')
  const disabledExecution = await run(['--profile', 'headless', 'Verify that disabling Mnemon leaves the Host available.'], {
    cwd: workspaceRoot,
    env,
  })
  assertSuccess('running Headless with the Mnemon Starter disabled', disabledExecution)
  if (!disabledExecution.stdout.includes(marker)) throw new Error('Mnemon-disabled Headless output did not contain the model marker')
  const pendingEntries = disabledExecution.stderr.split(/\r?\n/u).filter(line => line.includes('waiting for service:'))
  if (pendingEntries.length > 0) throw new Error(`Mnemon-disabled Headless left dependent Entries pending:\n${pendingEntries.join('\n')}`)
  const disabledToolNames = new Set(requests.flatMap(request => Array.isArray(request.tools)
    ? request.tools.map(tool => tool?.name).filter(name => typeof name === 'string')
    : []))
  const leakedMnemonTools = [...disabledToolNames].filter(name => name.startsWith('mnemon_')).sort()
  if (leakedMnemonTools.length > 0) throw new Error(`Mnemon-disabled Headless exposed Mnemon tools: ${leakedMnemonTools.join(', ')}`)

  console.log(`Verified Headless profile activation with ${toolNames.size} total tools and ${required.length} representative Mnemon tools.`)
  if (options.has('--upgrade-from')) console.log(`Verified an isolated ${options.get('--upgrade-from')} to ${options.get('--package')} package upgrade without changing Runtime Memory bytes.`)
  console.log('Verified the legacy settings import with buildin-to-builtin persistence, unrelated settings, retained-backup recovery and an idempotent Headless restart.')
  console.log('Verified that disabling the legacy mnemon Entry disables the complete Starter without blocking DSH startup.')
  if (extensionsEnabled) console.log('Verified simultaneous activation of scoped, light-context and auto-capture Entries without changing the default Strategy.')
} finally {
  await new Promise(resolveClose => server.close(resolveClose))
  await rm(temporaryRoot, { recursive: true, force: true })
}
