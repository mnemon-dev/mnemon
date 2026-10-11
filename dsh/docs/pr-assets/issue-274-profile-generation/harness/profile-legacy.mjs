import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFile, writeFile } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const args = new Map()
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1])
for (const key of ['--resources', '--run-root', '--package', '--pnpm-entry', '--native-cli']) assert(args.get(key), `supply ${key}`)
const preparation = spawnSync(process.execPath, [fileURLToPath(new URL('./desktop-generation.mjs', import.meta.url)), ...process.argv.slice(2), '--profile', 'headless', '--serve', 'false'], { encoding: 'utf8', timeout: 120_000 })
assert.equal(preparation.status, 0, preparation.stderr)
const runRoot = resolve(args.get('--run-root'))
const state = JSON.parse(await readFile(join(runRoot, 'server.json'), 'utf8'))
const modules = join(state.resources, 'app/node_modules')
const profile = state.profileDir
const manifestPath = join(profile, 'package.json')
const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
manifest.dsh.profile.bundles = ['@deepseek-ai/dsh-base', 'dsh-mnemon']
await writeFile(manifestPath, JSON.stringify(manifest, null, 2))
const settingsPath = join(state.dshHome, 'settings.yaml')
await writeFile(settingsPath, await readFile(settingsPath, 'utf8') + '\nmnemon-ui:\n  turnBar: false\n')
Object.assign(process.env, { DSH_HOME: state.dshHome, MNEMON_DATA_DIR: state.data, MNEMON_CLI_PATH: state.nativeCli, DSH_TELEMETRY_DISABLED: '1', DSH_TELEMETRY_MODE: 'DISABLED', DEEPSEEK_API_KEY: 'fixture', DEEPSEEK_BASE_URL: 'http://127.0.0.1:9' })
process.chdir(state.workspace)
const cli = join(modules, '@deepseek-ai/dsh/lib/bin.js')
const official = path => import(pathToFileURL(path).href)
const { registerHostModuleFallback } = await official(join(state.resources, 'host-module-fallback.mjs'))
registerHostModuleFallback(cli)
// Use the unchanged entry exported by the release's own CLI import. The older
// release bundles this public runtime file with a content hash in its name.
const importPath = (await readFile(cli, 'utf8')).match(/import\("(\.\/profile-boot-[^"]+\.js)"\)/)?.[1]
assert(importPath, 'the official CLI must expose its profile runner')
const { runProfile } = await import(new URL(importPath, pathToFileURL(cli)).href)
const { createLaunchEnvironmentSnapshot } = await official(join(modules, '@deepseek-ai/dsh-launch-environment/lib/index.js'))
const options = { profile: 'headless', patchFiles: [], args: [], environment: createLaunchEnvironmentSnapshot([{ source: 'process', values: Object.fromEntries(Object.entries(process.env).filter(([, value]) => typeof value === 'string')) }]) }
const evidence = { framework: JSON.parse(await readFile(join(modules, '@deepseek-ai/dsh/package.json'), 'utf8')).version, node: process.version, generation: state.generation.id, checks: [] }
const booted = await runProfile(options)
try {
  const entries = [...booted.ctx.loader.entries()]
  const core = entries.find(entry => entry.options.id === 'mnemon')
  assert.equal(typeof core.fiber.config.defaultRecallLimit, 'number')
  assert.equal(booted.ctx.settings.get('mnemon').defaultRecallLimit, 7)
  assert.equal(booted.ctx.settings.get('mnemon-ui').turnBar, false)
  const descriptor = booted.ctx.settings.describe().find(item => item.ns === 'mnemon')
  assert(!JSON.stringify(descriptor.schema).includes('"volatile":true'))
  for (const id of ['scoped', 'light-context', 'auto-capture']) assert.equal(entries.find(entry => entry.options.id === `mnemon-strategy-${id}`).disabled, false)
  await booted.ctx.settings.update('mnemon', { defaultRecallLimit: 11 }, descriptor.revision)
  assert.equal(booted.ctx.settings.get('mnemon').defaultRecallLimit, 11)
  assert.match(await readFile(settingsPath, 'utf8'), /defaultRecallLimit: 11/)
  evidence.checks.push('the old host receives PlainConfig and retains the mnemon and mnemon-ui settings namespaces', 'all three Strategy extensions activate', 'legacy Settings.update validates and persists an ordinary value')
} finally { await booted.shutdown.shutdown(0) }
const saved = await readFile(settingsPath, 'utf8')
const restarted = await runProfile(options)
try {
  assert.equal(restarted.ctx.settings.get('mnemon').defaultRecallLimit, 11)
  assert.equal(restarted.ctx.settings.get('mnemon-ui').turnBar, false)
  assert.equal(await readFile(settingsPath, 'utf8'), saved)
  evidence.checks.push('restart retains the saved settings and comment bytes')
} finally { await restarted.shutdown.shutdown(0) }
const patchPath = join(profile, 'cordis.patch.yml')
await writeFile(patchPath, await readFile(patchPath, 'utf8') + '\n- id: mnemon\n  disabled: true\n')
const disabled = await runProfile(options)
try {
  const entries = [...disabled.ctx.loader.entries()].filter(entry => entry.options.id === 'mnemon' || entry.options.id.startsWith('mnemon-source-') || entry.options.id.startsWith('mnemon-strategy-'))
  assert.equal(entries.length, 8)
  assert(entries.every(entry => entry.disabled && !entry.fiber))
  assert.equal(disabled.ctx.settings.get('mnemon'), undefined)
  evidence.checks.push('the original root gate disposes all eight entries and the owned settings namespace')
} finally { await disabled.shutdown.shutdown(0) }
await writeFile(join(runRoot, 'verification.json'), JSON.stringify(evidence, null, 2))
console.log(JSON.stringify(evidence, null, 2))
