import type { MemoryPackageProvenance, MemoryPluginDescriptor, MemorySourceDefinition, MemoryStrategyDefinition, MemoryStrategyExtensionDefinition } from './contracts/index.ts'
import type { InstallMemoryOptions, MemoryInstallContribution } from '../sdk/service.ts'

/** Core-owned installation records; never part of the plugin author contract. */
export interface InstalledMemorySource {
  kind: 'source'
  instanceKey: string
  provenance: MemoryPackageProvenance
  definition: MemorySourceDefinition
  effectiveDigest?: string
}

export interface InstalledMemoryStrategy {
  kind: 'strategy'
  instanceKey: string
  provenance: MemoryPackageProvenance
  definition: MemoryStrategyDefinition
}

export interface InstalledMemoryStrategyExtension {
  kind: 'strategy-extension'
  instanceKey: string
  provenance: MemoryPackageProvenance
  definition: MemoryStrategyExtensionDefinition
}

export interface InstalledMemoryPlugin {
  kind: 'plugin'
  instanceKey: string
  provenance: MemoryPackageProvenance
  descriptor: MemoryPluginDescriptor
}

export interface MemoryContributionSnapshot {
  revision: number
  sources: InstalledMemorySource[]
  strategies: InstalledMemoryStrategy[]
  strategyExtensions?: InstalledMemoryStrategyExtension[]
  plugins?: InstalledMemoryPlugin[]
}

/** Normalize public definitions before the registry validates and captures them. */
export function prepareMemoryContributions(contribution: MemoryInstallContribution, options: InstallMemoryOptions & { instanceId: string }): Pick<MemoryContributionSnapshot, 'sources' | 'strategies' | 'strategyExtensions' | 'plugins'> {
  const sources = [...(contribution.sources ?? [])]
  const strategies = [...(contribution.strategies ?? [])]
  const extensions = [...(contribution.strategyExtensions ?? [])]
  const entryId = options.instanceId.trim()
  if (entryId === '') throw new Error('installMemory requires a stable Loader Entry id')
  const provenance = (packageName: string): MemoryPackageProvenance => ({
    packageName, entryId,
    ...(options.artifactDigest === undefined ? {} : { artifactDigest: options.artifactDigest }),
  })
  const plugins: InstalledMemoryPlugin[] = contribution.plugin === undefined ? [] : [{
    kind: 'plugin',
    instanceKey: `plugin:${entryId}`,
    provenance: provenance(contribution.plugin.packageName),
    descriptor: contribution.plugin,
  }]
  return {
    sources: sources.map(definition => ({
      kind: 'source',
      instanceKey: `source:${entryId}${sources.length > 1 ? `/${definition.manifest.typeId}` : ''}`,
      provenance: provenance(definition.manifest.packageName),
      definition,
      ...(options.effectiveDigest === undefined ? {} : { effectiveDigest: options.effectiveDigest }),
    })),
    strategies: strategies.map(definition => ({
      kind: 'strategy',
      instanceKey: `strategy:${entryId}${strategies.length > 1 ? `/${definition.manifest.typeId}` : ''}`,
      provenance: provenance(definition.manifest.packageName),
      definition,
    })),
    strategyExtensions: extensions.map(definition => ({
      kind: 'strategy-extension',
      instanceKey: `strategy-extension:${entryId}${extensions.length > 1 ? `/${definition.manifest.typeId}` : ''}`,
      provenance: provenance(definition.manifest.packageName),
      definition,
    })),
    plugins,
  }
}
