import type { MemoryPluginDescriptor, MemorySourceDefinition, MemoryStrategyDefinition, MemoryStrategyExtensionDefinition } from '../core/contracts/index.ts'

export interface MemoryInstallContribution {
  /** Optional peer-plugin identity and dependency metadata, available to Core. */
  plugin?: MemoryPluginDescriptor
  /** One package may supply any contribution roles; installation/disposal is atomic. */
  sources?: readonly MemorySourceDefinition[]
  strategies?: readonly MemoryStrategyDefinition[]
  strategyExtensions?: readonly MemoryStrategyExtensionDefinition[]
}

export interface InstallMemoryOptions {
  /** Required for a direct ctx.plugin() mount that has no stable Loader Entry. */
  instanceId?: string
  artifactDigest?: string
  /** Source-private dependency digest, opaque to Mnemon core. */
  effectiveDigest?: string
}

/**
 * The only Mnemon-wide Cordis service: accept plugin definitions, not engine
 * objects. Use installMemory(ctx, ...) to resolve the calling Entry identity
 * and bind the returned registration disposer to its Fiber.
 */
export interface MnemonMemoryService {
  installContributions(
    contribution: MemoryInstallContribution,
    options: InstallMemoryOptions & { instanceId: string },
  ): () => void
}

declare module '@deepseek-ai/cordis' {
  interface Context {
    mnemonMemory: MnemonMemoryService
  }
}
