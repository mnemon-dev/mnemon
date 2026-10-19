import type { Context } from '@deepseek-ai/cordis'
import type { PluginPackages, ProfileContext } from '@deepseek-ai/dsh-app-boot'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import manifest from '../package.json' with { type: 'json' }

/** Make the selected Starter's dependency closure visible before its child Entries import. */
export function prepareStarterResolution(ctx: Pick<Context, 'get'>): Promise<void> | undefined {
  const packages = ctx.get('pluginPackages') as PluginPackages | undefined
  const profile = ctx.get('profileContext') as ProfileContext | undefined
  // Direct/custom compositions may use native resolution without a profile table.
  if (profile === undefined || typeof packages?.replace !== 'function') return
  const parent = pathToFileURL(join(profile.dir, 'package.json')).href
  const components = Object.keys(manifest.dependencies).filter(name => name.startsWith('dsh-mnemon-'))
  if (components.every(name => packages.packageOf(name, parent) !== undefined)) return
  return import('@deepseek-ai/dsh-app-boot').then(async ({ createRuntimeResolution, loadProfileDirectory, resolveBundleDir }) => {
    const selected = loadProfileDirectory('dsh-mnemon', profile.dir, profile.installAnchor)
    if (!selected.layers.some(layer => layer.packageName === manifest.name)) return
    // Node retains modules after a bundle is disabled. Keep the startup
    // dependency order, including stopped bundles, so their routes cannot be
    // removed or rebound when this Starter is added later. These extra layers
    // are used only for resolution; their patches are never applied.
    const started = profile.startedBundles.map(packageName => ({
      packageName,
      packageDir: resolveBundleDir('dsh-mnemon', packageName, profile.installAnchor, profile.dir),
      patchPaths: [], patches: [],
    }))
    const layers = [...started, ...selected.layers.filter(layer => !profile.startedBundles.includes(layer.packageName))]
    // The public replacement contract refuses to rebind modules already loaded
    // by another plugin. Do not replace Node hooks, create links, or flatten packages.
    packages.replace(await createRuntimeResolution({
      installAnchor: profile.installAnchor, profile: { ...selected, layers }, home: profile.home,
    }))
  })
}
