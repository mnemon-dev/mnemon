import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdir, readFile, writeFile, appendFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'

const args = new Map()
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1])
for (const key of ['--framework-modules', '--desktop-resources', '--package', '--pnpm-entry', '--run-root', '--native-cli']) assert(args.get(key), `supply ${key}`)
const modules = resolve(args.get('--framework-modules'))
const desktopModules = join(resolve(args.get('--desktop-resources')), 'app/node_modules')
const runRoot = resolve(args.get('--run-root'))
const home = join(runRoot, 'dsh-home')
const profile = join(home, 'profiles/probe')
const workspace = join(runRoot, 'workspace')
const data = join(runRoot, 'data')
await Promise.all([profile, workspace, data].map(path => mkdir(path, { recursive: true })))
Object.assign(process.env, { DSH_HOME: home, MNEMON_DATA_DIR: data, MNEMON_CLI_PATH: resolve(args.get('--native-cli')), DSH_TELEMETRY_DISABLED: '1', DEEPSEEK_API_KEY: 'fixture', DEEPSEEK_BASE_URL: 'http://127.0.0.1:9' })
process.chdir(workspace)
const official = (base, file) => import(pathToFileURL(join(base, file)).href)
const { installGeneration } = await official(desktopModules, 'dsh-desktop-market-installer/generations/installer.mjs')
const { writeDesired } = await official(desktopModules, 'dsh-desktop-market-installer/generations/registry.mjs')
const { projectGenerations } = await official(desktopModules, 'dsh-desktop-market-installer/generations/projection.mjs')
const result = await installGeneration({ dshHome: home, profile: 'probe', pluginSpec: args.get('--package'), expectedPluginName: 'dsh-mnemon', registry: 'https://registry.npmjs.org', autoInstallPeers: false, nodeExecutablePath: process.execPath, pnpmEntryPath: resolve(args.get('--pnpm-entry')), onOutput: chunk => { void appendFile(join(runRoot, 'install.log'), chunk) } })
assert.equal(result.ok, true, result.detail)
await writeFile(join(profile, 'package.json'), JSON.stringify({ name: 'issue274-live-profile', private: true, dependencies: {}, dsh: { profile: { bundles: ['@deepseek-ai/dsh-base'], patchReload: 'startup' } } }, null, 2))
await writeDesired(home, [result.generation.id])
await projectGenerations(home, 'probe')
// This settings probe does not execute shell/PTC workflows. Disable their full
// dependency chain rather than leaving unrelated consumers waiting for a PTY.
const disabled = ['subprocess', 'ptc-runtime', 'workflow-ptc', 'tool-workflow', 'bash-sandbox', 'pwsh-sandbox', 'tool-bash', 'tool-pwsh', 'permission', 'tool-fs-search']
const extensions = ['scoped', 'light-context', 'auto-capture'].map(name => `mnemon-strategy-${name}`)
const patchPath = join(profile, 'cordis.patch.yml')
await writeFile(patchPath, disabled.map(id => `- id: ${id}\n  disabled: true\n`).join('') + extensions.map(id => `- id: ${id}\n  disabled: false\n`).join('') + `- id: mnemon\n  config:\n    cliPath: ${JSON.stringify(resolve(args.get('--native-cli')))}\n    idleReviewMs: !!js 15 * 1000\n    idleReview:\n      enabled: false\n`)
const suffix = createHash('sha256').update(JSON.stringify(pathToFileURL(profile + '/').href)).digest('hex').slice(0, 16)
const backup = `# Issue 274 retained settings\nmnemon:\n  defaultRecallLimit: 7\nmnemon-ui:\n  turnBar: false\nmnemon-view-${suffix}:\n  entries:\n    include:mnemon-strategy-light-context:\n      enabled: true\n      config:\n        maxProjectionCharacters: 700\nmnemon-plugins-${suffix}:\n  sources:\n    include:mnemon-source-documents:\n      enabled: false\n`
await writeFile(join(home, 'settings.yaml.imported'), backup)
const { runProfile } = await official(modules, '@deepseek-ai/dsh/lib/profile-boot.js')
const { createLaunchEnvironmentSnapshot } = await official(modules, '@deepseek-ai/dsh-launch-environment/lib/index.js')
const options = { profile: 'probe', patchFiles: [], args: [], environment: createLaunchEnvironmentSnapshot([{ source: 'process', values: Object.fromEntries(Object.entries(process.env).filter(([, value]) => typeof value === 'string')) }]) }
const waitFor = async predicate => { for (let i = 0; i < 100; i++) { if (predicate()) return; await new Promise(done => setTimeout(done, 20)) } throw new Error('Profile state did not settle') }
const { ctx, shutdown } = await runProfile(options)
const evidence = { framework: JSON.parse(await readFile(join(modules, '@deepseek-ai/dsh/package.json'))).version, node: process.version, generation: result.generation.id, checks: [] }
try {
  const entries = () => [...ctx.loader.entries()]
  const core = () => entries().find(entry => entry.options.id === 'mnemon')
  const descriptor = () => ctx.settings.describe().find(value => value.ns === 'mnemon')
  await waitFor(() => core()?.fiber?.config.defaultRecallLimit.get() === 7)
  assert.equal(core().fiber.config.idleReviewMs.get(), 15000)
  assert.equal(core().fiber.config.conversationInteraction.get().turnBar, false)
  assert.equal(core().fiber.config.memoryView.get().entries['include:mnemon-strategy-light-context'].config.maxProjectionCharacters, 700)
  await waitFor(() => entries().find(entry => entry.options.id === 'mnemon-source-documents')?.disabled === true)
  for (const id of extensions) assert.equal(entries().find(entry => entry.options.id === id)?.disabled, false)
  assert.equal(await readFile(join(home, 'settings.yaml.imported'), 'utf8'), backup)
  evidence.checks.push('retained root/UI/View/Source settings recover under the same profile namespaces', 'all three Strategy extensions remain active', 'backup bytes and the dynamic expression are preserved')
  const previous = descriptor()
  const fiber = core().fiber
  await ctx.settings.update('mnemon', { defaultRecallLimit: 11 }, previous.revision)
  assert.equal(core().fiber, fiber)
  assert.equal(core().fiber.config.defaultRecallLimit.get(), 11)
  await assert.rejects(ctx.settings.update('mnemon', { defaultRecallLimit: 12 }, previous.revision), error => error.code === 'SETTINGS_CONFLICT')
  assert.equal(core().fiber.config.defaultRecallLimit.get(), 11)
  const current = descriptor()
  await ctx.settings.update('mnemon', { memoryView: { ...current.value.memoryView, entries: { ...current.value.memoryView.entries, 'include:mnemon-source-documents': { enabled: true, config: {} } } } }, current.revision)
  await waitFor(() => entries().find(entry => entry.options.id === 'mnemon-source-documents')?.disabled === false)
  assert.match(await readFile(patchPath, 'utf8'), /idleReviewMs: !!js 15 \* 1000/)
  evidence.checks.push('live update preserves the owning fiber and rejects stale revisions', 'independent Source re-enable survives profile reconciliation')
} finally { await shutdown.shutdown(0) }
const savedPatch = await readFile(patchPath, 'utf8')
const restarted = await runProfile(options)
try {
  const core = [...restarted.ctx.loader.entries()].find(entry => entry.options.id === 'mnemon')
  assert.equal(core.fiber.config.defaultRecallLimit.get(), 11)
  assert.equal(core.fiber.config.idleReviewMs.get(), 15000)
  assert.equal(await readFile(patchPath, 'utf8'), savedPatch)
  evidence.checks.push('full host restart retains values, expressions and profile bytes')
} finally { await restarted.shutdown.shutdown(0) }
await writeFile(patchPath, savedPatch + '\n- id: mnemon\n  disabled: true\n')
const stopped = await runProfile(options)
try {
  const entries = [...stopped.ctx.loader.entries()].filter(entry => entry.options.id === 'mnemon' || entry.options.id.startsWith('mnemon-source-') || entry.options.id.startsWith('mnemon-strategy-'))
  assert.equal(entries.length, 8)
  assert(entries.every(entry => entry.disabled && !entry.fiber))
  evidence.checks.push('the original root gate disables all eight entries without pending services')
} finally { await stopped.shutdown.shutdown(0) }
await writeFile(join(runRoot, 'verification.json'), JSON.stringify(evidence, null, 2))
console.log(JSON.stringify(evidence, null, 2))
