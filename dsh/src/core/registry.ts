import type {
  InstalledMemorySource,
  InstalledMemoryPlugin,
  InstalledMemoryStrategy,
  InstalledMemoryStrategyExtension,
  MemoryContributionSnapshot,
} from './contributions.ts'
import { captureMemoryContributionSnapshot } from './composition.ts'

export interface MemoryContributionInstall {
  plugins?: readonly InstalledMemoryPlugin[]
  sources?: readonly InstalledMemorySource[]
  strategies?: readonly InstalledMemoryStrategy[]
  strategyExtensions?: readonly InstalledMemoryStrategyExtension[]
}

export type MemoryContributionListener = (snapshot: MemoryContributionSnapshot) => void

/**
 * Scope-local definition registry behind ctx.mnemonMemory.
 *
 * It owns definitions only. Runtime objects are created and drained by a
 * generation host attached by dsh-mnemon core.
 */
export class MemoryContributionRegistry {
  private readonly sources = new Map<string, InstalledMemorySource>()
  private readonly strategies = new Map<string, InstalledMemoryStrategy>()
  private readonly extensions = new Map<string, InstalledMemoryStrategyExtension>()
  private readonly plugins = new Map<string, InstalledMemoryPlugin>()
  private readonly listeners = new Set<MemoryContributionListener>()
  private revision = 0

  install(value: MemoryContributionInstall): () => void {
    const incomingSources = [...(value.sources ?? [])]
    const incomingStrategies = [...(value.strategies ?? [])]
    const incomingExtensions = [...(value.strategyExtensions ?? [])]
    const incomingPlugins = [...(value.plugins ?? [])]
    if (incomingSources.length + incomingStrategies.length + incomingExtensions.length === 0) throw new Error('installMemory requires one Source, Strategy or Strategy extension contribution')
    for (const source of incomingSources) {
      if (this.sources.has(source.instanceKey)) throw new Error(`memory Source instance is already installed: ${source.instanceKey}`)
    }
    for (const strategy of incomingStrategies) {
      if (this.strategies.has(strategy.instanceKey)) throw new Error(`memory Strategy instance is already installed: ${strategy.instanceKey}`)
    }
    for (const extension of incomingExtensions) {
      if (this.extensions.has(extension.instanceKey)) throw new Error(`memory Strategy extension instance is already installed: ${extension.instanceKey}`)
    }
    for (const plugin of incomingPlugins) {
      if (this.plugins.has(plugin.instanceKey)) throw new Error(`memory plugin instance is already installed: ${plugin.instanceKey}`)
    }
    const candidate = captureMemoryContributionSnapshot({
      revision: this.revision + 1,
      sources: [...this.sources.values(), ...incomingSources],
      strategies: [...this.strategies.values(), ...incomingStrategies],
      strategyExtensions: [...this.extensions.values(), ...incomingExtensions],
      plugins: [...this.plugins.values(), ...incomingPlugins],
    })
    const capturedSources = candidate.sources.filter(source => incomingSources.some(incoming => incoming.instanceKey === source.instanceKey))
    const capturedStrategies = candidate.strategies.filter(strategy => incomingStrategies.some(incoming => incoming.instanceKey === strategy.instanceKey))
    const capturedExtensions = (candidate.strategyExtensions ?? []).filter(extension => incomingExtensions.some(incoming => incoming.instanceKey === extension.instanceKey))
    const capturedPlugins = (candidate.plugins ?? []).filter(plugin => incomingPlugins.some(incoming => incoming.instanceKey === plugin.instanceKey))
    for (const source of capturedSources) this.sources.set(source.instanceKey, source)
    for (const strategy of capturedStrategies) this.strategies.set(strategy.instanceKey, strategy)
    for (const extension of capturedExtensions) this.extensions.set(extension.instanceKey, extension)
    for (const plugin of capturedPlugins) this.plugins.set(plugin.instanceKey, plugin)
    this.revision = candidate.revision
    this.notify(candidate)

    let active = true
    return () => {
      if (!active) return
      active = false
      let changed = false
      for (const source of capturedSources) {
        if (this.sources.get(source.instanceKey) !== source) continue
        this.sources.delete(source.instanceKey)
        changed = true
      }
      for (const strategy of capturedStrategies) {
        if (this.strategies.get(strategy.instanceKey) !== strategy) continue
        this.strategies.delete(strategy.instanceKey)
        changed = true
      }
      for (const extension of capturedExtensions) {
        if (this.extensions.get(extension.instanceKey) !== extension) continue
        this.extensions.delete(extension.instanceKey)
        changed = true
      }
      for (const plugin of capturedPlugins) {
        if (this.plugins.get(plugin.instanceKey) !== plugin) continue
        this.plugins.delete(plugin.instanceKey)
        changed = true
      }
      if (!changed) return
      this.revision += 1
      this.notify(this.snapshot())
    }
  }

  snapshot(): MemoryContributionSnapshot {
    return captureMemoryContributionSnapshot({
      revision: this.revision,
      sources: [...this.sources.values()],
      strategies: [...this.strategies.values()],
      strategyExtensions: [...this.extensions.values()],
      plugins: [...this.plugins.values()],
    })
  }

  subscribe(listener: MemoryContributionListener): () => void {
    this.listeners.add(listener)
    let active = true
    return () => {
      if (!active) return
      active = false
      this.listeners.delete(listener)
    }
  }

  private notify(snapshot: MemoryContributionSnapshot): void {
    for (const listener of [...this.listeners]) listener(snapshot)
  }
}
