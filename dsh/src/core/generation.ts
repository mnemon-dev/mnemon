import type {
  MemoryCompositionDiagnostic,
  MemoryCompositionEvaluationReport,
} from "./contracts/index.ts"
import type { MemoryContributionSnapshot } from './contributions.ts'
import {
  MemoryCompositionGeneration,
  type CompileMemoryGenerationOptions,
} from './composition.ts'

interface GenerationRecord {
  generation: MemoryCompositionGeneration
  leases: number
  state: 'serving' | 'draining'
}

export interface MemoryGenerationLease {
  readonly id: string
  readonly generation: MemoryCompositionGeneration
  release(): void
}

export interface MemoryGenerationHostSnapshot {
  servingGenerationId?: string
  drainingGenerationIds: string[]
  evaluation: MemoryCompositionEvaluationReport
}

/** No generation serves new work: the composition is incomplete or was rejected. */
export class MemoryCompositionUnavailableError extends Error {
  readonly code = 'memory-composition-unavailable'

  constructor(readonly evaluation: MemoryCompositionEvaluationReport) {
    const reason = evaluation.diagnostics.map(diagnostic => diagnostic.message).join('; ')
    super(`no Serving memory generation is available${reason === '' ? '' : `: ${reason}`}`)
    this.name = 'MemoryCompositionUnavailableError'
  }
}

/** Matched by code, so a copy of Core bundled into another entry point still qualifies. */
export function isMemoryCompositionUnavailable(error: unknown): error is MemoryCompositionUnavailableError {
  return error instanceof Error && (error as { code?: unknown }).code === 'memory-composition-unavailable'
}

function report(
  state: 'incomplete' | 'rejected',
  snapshot: MemoryContributionSnapshot,
  diagnostics: MemoryCompositionDiagnostic[],
): MemoryCompositionEvaluationReport {
  return Object.freeze({
    state,
    contributionRevision: snapshot.revision,
    sourceInstanceKeys: snapshot.sources.map(source => source.instanceKey),
    diagnostics: Object.freeze(diagnostics.map(diagnostic => Object.freeze({ ...diagnostic }))) as unknown as MemoryCompositionDiagnostic[],
  })
}

/**
 * Owns candidate → serving → draining generation transitions.
 * Cordis owns definition registrations; this host owns factory-created runtime
 * objects and keeps a retired generation alive only while turn leases exist.
 */
export class MemoryGenerationHost {
  private readonly records = new Map<string, GenerationRecord>()
  private serving: GenerationRecord | undefined
  private evaluation: MemoryCompositionEvaluationReport = Object.freeze({
    state: 'incomplete',
    contributionRevision: 0,
    sourceInstanceKeys: [],
    diagnostics: [{ code: 'not-composed', message: 'Memory contributions have not been composed.' }],
  })
  private closed = false
  private closing: Promise<void> | undefined
  private onDrained: (() => void) | undefined
  private readonly pendingDisposals = new Set<Promise<void>>()
  private readonly disposalFailures: unknown[] = []

  constructor(private readonly options: CompileMemoryGenerationOptions = {}) {}

  reconcile(snapshot: MemoryContributionSnapshot): MemoryCompositionEvaluationReport {
    this.assertOpen()
    if (snapshot.sources.length === 0 || snapshot.strategies.length === 0) {
      const diagnostics: MemoryCompositionDiagnostic[] = []
      if (snapshot.sources.length === 0) diagnostics.push({ code: 'missing-source', message: 'No Memory Source contribution is installed.' })
      if (snapshot.strategies.length === 0) diagnostics.push({ code: 'missing-strategy', message: 'No Memory Strategy contribution is installed.' })
      this.evaluation = report('incomplete', snapshot, diagnostics)
      if (this.removesServingContribution(snapshot) || this.removesServingExtension(snapshot)) this.retireServing()
      return this.evaluation
    }

    let candidate: MemoryCompositionGeneration
    try {
      candidate = new MemoryCompositionGeneration(snapshot, this.options)
    } catch (error) {
      this.evaluation = report('rejected', snapshot, [{
        code: 'composition-rejected',
        message: error instanceof Error ? error.message : String(error),
      }])
      if (this.removesServingContribution(snapshot) || this.removesServingExtension(snapshot)) this.retireServing()
      return this.evaluation
    }

    const previous = this.serving
    const existing = this.records.get(candidate.id)
    if (existing !== undefined) {
      this.disposeGeneration(candidate)
      existing.state = 'serving'
      this.serving = existing
      this.evaluation = existing.generation.report
      if (previous !== undefined && previous !== existing) this.retire(previous)
      return this.evaluation
    }
    const record: GenerationRecord = { generation: candidate, leases: 0, state: 'serving' }
    this.records.set(candidate.id, record)
    this.serving = record
    this.evaluation = candidate.report
    if (previous !== undefined) this.retire(previous)
    return this.evaluation
  }

  acquire(generationId?: string): MemoryGenerationLease {
    this.assertOpen()
    const record = generationId === undefined ? this.serving : this.records.get(generationId)
    if (record === undefined || (generationId === undefined && record.state !== 'serving')) {
      throw new MemoryCompositionUnavailableError(this.evaluation)
    }
    record.leases += 1
    let active = true
    return Object.freeze({
      id: record.generation.id,
      generation: record.generation,
      release: () => {
        if (!active) return
        active = false
        record.leases -= 1
        this.collect(record)
      },
    })
  }

  current(): MemoryCompositionGeneration | undefined {
    return this.serving?.state === 'serving' ? this.serving.generation : undefined
  }

  generation(id: string): MemoryCompositionGeneration | undefined {
    return this.records.get(id)?.generation
  }

  inspect(): MemoryGenerationHostSnapshot {
    return Object.freeze({
      ...(this.serving?.state === 'serving' ? { servingGenerationId: this.serving.generation.id } : {}),
      drainingGenerationIds: Object.freeze([...this.records.values()]
        .filter(record => record.state === 'draining')
        .map(record => record.generation.id)) as unknown as string[],
      evaluation: this.evaluation,
    })
  }

  dispose(): Promise<void> {
    if (this.closing !== undefined) return this.closing
    this.closed = true
    this.serving = undefined
    const drained = new Promise<void>(resolve => { this.onDrained = resolve })
    // In-flight management and tool requests retain their existing lease.
    // New acquisitions stop immediately; Source clients close after the last request.
    for (const record of [...this.records.values()]) this.retire(record)
    if (this.records.size === 0) this.onDrained?.()
    this.closing = (async () => {
      await drained
      await Promise.all([...this.pendingDisposals])
      if (this.disposalFailures.length > 0) throw new AggregateError(this.disposalFailures, 'memory generation host disposal failed')
    })()
    return this.closing
  }

  private disposeGeneration(generation: MemoryCompositionGeneration): void {
    const task = generation.dispose().catch(error => { this.disposalFailures.push(error) })
    this.pendingDisposals.add(task)
    void task.finally(() => this.pendingDisposals.delete(task))
  }

  private removesServingContribution(snapshot: MemoryContributionSnapshot): boolean {
    if (this.serving === undefined) return false
    const available = new Set([
      ...snapshot.sources.map(source => source.instanceKey),
      ...snapshot.strategies.map(strategy => strategy.instanceKey),
    ])
    const required = [
      ...this.serving.generation.report.sourceInstanceKeys,
      ...(this.serving.generation.report.strategyInstanceKey === undefined ? [] : [this.serving.generation.report.strategyInstanceKey]),
    ]
    return required.some(key => !available.has(key))
  }

  /** A failed replacement must not keep applying an explicitly disabled policy. */
  private removesServingExtension(snapshot: MemoryContributionSnapshot): boolean {
    const available = new Set((snapshot.strategyExtensions ?? []).map(extension => extension.instanceKey))
    return this.serving?.generation.report.strategyExtensionInstanceKeys?.some(key => !available.has(key)) ?? false
  }

  private retireServing(): void {
    if (this.serving === undefined) return
    const previous = this.serving
    this.serving = undefined
    this.retire(previous)
  }

  private retire(record: GenerationRecord): void {
    record.state = 'draining'
    this.collect(record)
  }

  private collect(record: GenerationRecord): void {
    if (record.state !== 'draining' || record.leases !== 0) return
    if (this.records.get(record.generation.id) !== record) return
    this.records.delete(record.generation.id)
    this.disposeGeneration(record.generation)
    if (this.closed && this.records.size === 0) this.onDrained?.()
  }

  private assertOpen(): void {
    if (this.closed) throw new Error('memory generation host is disposed')
  }
}
