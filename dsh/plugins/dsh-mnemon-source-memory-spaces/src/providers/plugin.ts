import type { Context, Plugin } from '@deepseek-ai/cordis'
import type { PrivateMemorySpaceProviderHost } from './host.ts'
import type { MemorySpaceProviderEntry } from './definitions.ts'
/**
 * Build an ordinary child plugin whose only capability is a closure-held
 * registration into its Memory Spaces parent. No ctx.mnemonProvider service
 * exists, and core cannot resolve this host.
 */
export function createMemorySpaceProviderPlugin<Config>(
  entry: MemorySpaceProviderEntry<Config>,
  host: PrivateMemorySpaceProviderHost,
): Plugin.Object<Config> {
  const plugin: Plugin.Object<Config> = {
    name: `dsh-mnemon-provider-${entry.module.id}`,
    apply(ctx: Context, config: Config = entry.config) {
      return entry.module.apply(ctx, host.bind(entry.instanceId, entry.module.id, config), config)
    },
  }
  if (entry.module.Config !== undefined) plugin.Config = entry.module.Config
  return plugin
}
