import { prepareMemoryContributions, type MemoryContributionSnapshot } from './contributions.ts'
import { MemoryGenerationHost } from './generation.ts'
import type { CompileMemoryGenerationOptions } from './composition.ts'
import { MemoryContributionRegistry, type MemoryContributionInstall, type MemoryContributionListener } from './registry.ts'
import type { MnemonMemoryService } from '../sdk/service.ts'

interface MemoryCoreContext {
  provide(name: string, service: MnemonMemoryService): unknown
  effect(factory: () => () => Promise<void>, label?: string): unknown
}

/** Only the owning Core/Host receives the engine; plugins receive its service. */
export function provideMemoryRuntime(context: MemoryCoreContext): MemoryRuntime {
  const runtime = new MemoryRuntime()
  context.provide('mnemonMemory', runtime.service)
  context.effect(() => () => runtime.dispose(), 'dsh-mnemon.core()')
  return runtime
}

/** The single Cordis-owned registry of Source and Strategy definitions. */
export class MemoryRuntime {
  readonly service: MnemonMemoryService = Object.freeze<MnemonMemoryService>({
    installContributions: (contribution, options) => {
      if (this.closed) throw new Error('Memory Runtime is disposed')
      return this.installContributions(prepareMemoryContributions(contribution, options))
    },
  })
  private readonly contributions = new MemoryContributionRegistry()
  private readonly generationAttachments = new Set<MemoryGenerationAttachment>()
  private closed = false
  private disposal: Promise<void> | undefined
  private readonly disposalFailures: unknown[] = []
  private batchDepth = 0
  private batchSnapshot: MemoryContributionSnapshot | undefined
  private readonly reconciliations = new Map<MemoryGenerationHost, MemoryContributionSnapshot>()

  /** Host assembly transaction: existing leases stay served until all Fibers settle. */
  async batch<T>(operation: () => Promise<T>): Promise<T> {
    if (this.closed) throw new Error('Memory Runtime is disposed')
    if (this.batchDepth++ === 0) this.batchSnapshot = this.contributions.snapshot()
    try { return await operation() }
    finally {
      if (--this.batchDepth === 0) {
        this.batchSnapshot = undefined
        const pending = [...this.reconciliations]
        this.reconciliations.clear()
        for (const [host, snapshot] of pending) host.reconcile(snapshot)
      }
    }
  }

  installContributions(value: MemoryContributionInstall): () => void {
    if (this.closed) throw new Error('Memory Runtime is disposed')
    return this.contributions.install(value)
  }

  contributionSnapshot(): MemoryContributionSnapshot {
    return this.contributions.snapshot()
  }

  onContributionsChanged(listener: MemoryContributionListener): () => void {
    return this.contributions.subscribe(listener)
  }

  /** Attach one Host runtime graph to the current definition set. */
  attachGeneration(options: CompileMemoryGenerationOptions = {}): MemoryGenerationAttachment {
    if (this.closed) throw new Error('Memory Runtime is disposed')
    const host = new MemoryGenerationHost(options)
    host.reconcile(this.batchSnapshot ?? this.contributions.snapshot())
    // A workspace first opened during a settings transaction also starts from
    // the committed baseline, then joins the final reconciliation.
    if (this.batchSnapshot !== undefined) this.reconciliations.set(host, this.contributions.snapshot())
    const unsubscribe = this.contributions.subscribe(snapshot => {
      if (this.batchDepth) this.reconciliations.set(host, snapshot)
      else host.reconcile(snapshot)
    })
    let attached = true
    const release = (): void => {
      if (!attached) return
      attached = false
      unsubscribe()
      this.reconciliations.delete(host)
    }
    let disposal: Promise<void> | undefined
    const attachment: MemoryGenerationAttachment = {
      host,
      release,
      dispose: () => {
        if (disposal !== undefined) return disposal
        release()
        disposal = host.dispose().finally(() => { this.generationAttachments.delete(attachment) })
        // Retired graphs may release in a synchronous Host callback. Observe
        // rejection now and report it at Core shutdown, including detached graphs.
        void disposal.catch(error => { this.disposalFailures.push(error) })
        return disposal
      },
    }
    this.generationAttachments.add(attachment)
    return attachment
  }

  /** Core-Fiber shutdown also closes attachments left by a host adapter. */
  dispose(): Promise<void> {
    if (this.disposal !== undefined) return this.disposal
    this.closed = true
    this.disposal = Promise.allSettled([...this.generationAttachments].map(attachment => attachment.dispose())).then(() => {
      if (this.disposalFailures.length > 0) throw new AggregateError(this.disposalFailures, 'Memory Runtime cleanup failed')
    })
    return this.disposal
  }

}

export interface MemoryGenerationAttachment {
  readonly host: MemoryGenerationHost
  /** Stop following definition changes while preserving leased generations. */
  release(): void
  /** Final-dispose every generation after the owning runtime graph is unused. */
  dispose(): Promise<void>
}
