import assert from 'node:assert/strict'
import { lstat, readFile, unlink, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'

const args = new Map()
for (let i = 2; i < process.argv.length; i += 2) args.set(process.argv[i], process.argv[i + 1])
assert(args.get('--state'), 'supply the private desktop-generation server.json')
const state = JSON.parse(await readFile(args.get('--state'), 'utf8'))
const mode = args.get('--mode') ?? 'import'
assert(['direct', 'import', 'grouped', 'flat'].includes(mode))
const modules = join(state.resources, 'app/node_modules')
Object.assign(process.env, { DSH_HOME: state.dshHome, MNEMON_DATA_DIR: state.data })
const official = path => import(pathToFileURL(path).href)
const { registerHostModuleFallback } = await official(join(state.resources, 'host-module-fallback.mjs'))
registerHostModuleFallback(join(modules, '@deepseek-ai/dsh/lib/bin.js'))
const { boot, loadProfileDirectory, healProfilesModuleFallback } = await official(join(modules, '@deepseek-ai/dsh-app-boot/lib/index.js'))
const anchor = join(modules, '@deepseek-ai/dsh/package.json')
const loaded = loadProfileDirectory('issue274', state.profileDir, anchor)
const shared = join(state.dshHome, 'profiles/node_modules')
// The public healer replaces the installer's host symlink with its normal
// fallback directory, including real transitive Source/Provider projections.
if ((await lstat(shared)).isSymbolicLink()) await unlink(shared)
await healProfilesModuleFallback({ installAnchor: anchor, profile: loaded, home: state.dshHome })
if (mode === 'direct') {
  const entry = createRequire(join(state.profileDir, 'package.json')).resolve('dsh-mnemon')
  const plugin = await official(entry)
  assert.equal(plugin.name, 'dsh-mnemon')
  console.log('Direct generation import succeeded')
} else if (mode === 'import') {
  const { Context } = await official(join(modules, '@deepseek-ai/cordis/lib/index.js'))
  const { Loader, EntryTree } = await official(join(modules, '@deepseek-ai/cordis-plugin-loader/lib/index.js'))
  const ctx = new Context()
  try {
    ctx.baseUrl = pathToFileURL(state.profileDir + '/').href
    await ctx.plugin(Loader)
    // --no-addons makes the real release choose its documented JS fallback.
    // Do not replace internal loader state, package resolution or exports.
    assert.equal(ctx.loader.internal, undefined, 'invoke this probe with --no-addons')
    const plugin = await new EntryTree(ctx).import('dsh-mnemon')
    assert.equal(plugin.name, 'dsh-mnemon')
    assert.equal(plugin.Config({ defaultRecallLimit: 7 }).defaultRecallLimit, 7)
    console.log('Official Desktop fallback imported the complete root with PlainConfig')
  } finally { await ctx.fiber.dispose() }
} else {
  const config = join(state.profileDir, 'probe.cordis.yml')
  await writeFile(config, '[]\n')
  const patches = loaded.layers.find(layer => layer.packageName === 'dsh-mnemon').patches
  const group = patches.find(patch => patch.insert).insert[0]
  // These two diagnostic variants only isolate the failing import. A complete
  // successful Host is exercised separately with the untouched grouped bundle.
  const selected = mode === 'grouped' ? patches : [{ insert: group.config }]
  const ctx = await boot('issue274', config, selected)
  await ctx.fiber.dispose()
}
