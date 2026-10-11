import { Context, type Plugin } from '@deepseek-ai/cordis'
import { readFileSync } from 'node:fs'
import { provideMemoryRuntime } from '../core/runtime.ts'
import type { MemoryGenerationHost } from '../core/generation.ts'
import { jsonClone } from '../core/definitions.ts'
import { DEFAULT_MEMORY_VIEW_BUDGET } from '../core/contracts/index.ts'
import type {
  ComposableMemoryView, MemoryActionOffer, MemoryCapability, MemoryCompositionEvaluationReport,
  MemoryEvidence, MemoryJsonValue, MemoryMutationReceipt, MemoryOperationScope,
  MemoryPackageProvenance, MemorySourceManagementCatalog, MemorySourceManagementRequest,
  MemorySourceManagementResult, MemorySourceManifest, MemoryViewRequest,
} from '../core/contracts/index.ts'

export interface MemoryTestEntry<C> {
  instanceId: string
  config?: C
}

export interface MemoryTestTurn {
  readonly view: ComposableMemoryView
  executeRoute(routeId: string, input: MemoryJsonValue, signal?: AbortSignal): Promise<MemoryEvidence>
  executeAction(offerId: string, input: MemoryJsonValue, authorize: (offer: MemoryActionOffer) => boolean | Promise<boolean>, signal?: AbortSignal): Promise<MemoryMutationReceipt>
  release(): void
}

/** Read-only identity/manifest, never an installed definition or Source handle. */
export interface MemoryTestSource {
  readonly sourceInstanceKey: string
  readonly manifest: MemorySourceManifest
  readonly provenance: MemoryPackageProvenance
}

export interface MemoryTestOptions {
  strategyInstanceKey?: string
  strategyTypeId?: string
  strategyFallback?: 'sole-strategy'
  sourceConfiguration?: (source: MemoryTestSource) => Readonly<Record<string, MemoryJsonValue>>
  sourceCapabilities?: (source: MemoryTestSource) => readonly MemoryCapability[]
  now?: () => Date
  sourceTimeoutMs?: number
}

/** JSON-only observations for composition/replacement tests, not engine access. */
export interface MemoryTestDiagnostics {
  servingGenerationId?: string
  drainingGenerationIds: string[]
  evaluation: MemoryCompositionEvaluationReport
}

export interface MemoryTestManagementClient {
  readonly sourceInstanceKey: string
  readonly revision: string
  read(operation: string, input?: MemoryJsonValue): Promise<MemorySourceManagementResult>
  mutate(operation: string, input: MemoryJsonValue, options: { confirmed: true; expectedRevision?: string }): Promise<MemorySourceManagementResult>
}

/**
 * A test fixture, not a production service or a second Loader. It mounts real
 * Cordis Fibers against the same Runtime/compiler used by the Host. The small
 * Loader identity adapter supplies only stable Entry ids for installMemory.
 * No built-in Source, Provider, database, browser, or private binding is needed.
 */
export class MemoryCompositionRunner {
  readonly context = new Context()
  readonly #runtime = provideMemoryRuntime(this.context)
  readonly #generations: MemoryGenerationHost
  readonly #entryIds = new WeakMap<object, string>()
  readonly #entries = new Map<string, { dispose(): Promise<void> }>()
  readonly #turns = new Set<() => void>()
  readonly #beginnings = new Set<AbortController>()
  #closed = false
  #closing: Promise<void> | undefined

  constructor(options: MemoryTestOptions = {}) {
    this.context.provide('loader', {
      locate: (fiber: object) => this.#entryIds.get(fiber),
      import: (specifier: string) => import(specifier),
      unwrapExports: (module: Record<string, unknown>) => module.default ?? module,
    })
    const { sourceConfiguration, sourceCapabilities, ...selection } = options
    this.#generations = this.#runtime.attachGeneration({
      ...selection,
      ...(sourceConfiguration === undefined ? {} : { sourceConfiguration: source => sourceConfiguration(Object.freeze({
        sourceInstanceKey: source.instanceKey, manifest: source.definition.manifest, provenance: source.provenance,
      })) }),
      ...(sourceCapabilities === undefined ? {} : { sourceCapabilities: source => sourceCapabilities(Object.freeze({
        sourceInstanceKey: source.instanceKey, manifest: source.definition.manifest, provenance: source.provenance,
      })) }),
    }).host
  }

  async mount<C>(plugin: Plugin.Object<C>, entry: MemoryTestEntry<C>): Promise<() => Promise<void>> {
    this.#assertOpen()
    const id = entry.instanceId.trim()
    if (id === '' || this.#entries.has(id)) throw new Error(`duplicate or empty test Entry id: ${id}`)
    const boundPlugin: Plugin.Object<C | undefined> = {
      ...plugin,
      apply: (ctx: Context, config: C | undefined) => {
        this.#entryIds.set(ctx.fiber, id)
        return plugin.apply(ctx, config as C)
      },
    }
    const fiber = this.context.plugin(boundPlugin, entry.config)
    this.#entries.set(id, fiber)
    try {
      await fiber.await()
      this.#assertOpen()
    } catch (error) {
      if (this.#entries.get(id) === fiber) this.#entries.delete(id)
      await fiber.dispose()
      throw error
    }
    return async () => {
      if (this.#entries.get(id) !== fiber) return
      this.#entries.delete(id)
      await fiber.dispose()
    }
  }

  async beginTurn(request: Partial<MemoryViewRequest> = {}, signal?: AbortSignal): Promise<MemoryTestTurn> {
    this.#assertOpen()
    signal?.throwIfAborted()
    const budget = { ...(request.budget ?? DEFAULT_MEMORY_VIEW_BUDGET) }
    const lease = this.#generations.acquire()
    const controller = new AbortController()
    const abort = () => controller.abort(signal!.reason)
    signal?.addEventListener('abort', abort, { once: true })
    this.#beginnings.add(controller)
    try {
      const view = await lease.generation.compose({
        scope: request.scope ?? { storage: 'custom' },
        scenario: request.scenario ?? 'plugin-test',
        budget,
      }, controller.signal)
      this.#assertOpen()
      let active = true
      const assertActive = () => {
        this.#assertOpen()
        if (!active) throw new Error('Memory test turn is released')
      }
      const release = () => {
        if (!active) return
        active = false
        this.#turns.delete(release)
        lease.release()
      }
      this.#turns.add(release)
      return Object.freeze({
        view,
        executeRoute: async (routeId: string, input: MemoryJsonValue, signal?: AbortSignal) => {
          assertActive()
          const operation = this.#generations.acquire(lease.id)
          try { return await operation.generation.executeRoute(view, routeId, input, signal, budget) }
          finally { operation.release() }
        },
        executeAction: async (offerId: string, input: MemoryJsonValue, authorize: (offer: MemoryActionOffer) => boolean | Promise<boolean>, signal?: AbortSignal) => {
          assertActive()
          const operation = this.#generations.acquire(lease.id)
          try { return await operation.generation.executeAction(view, offerId, input, authorize, signal) }
          finally { operation.release() }
        },
        release,
      })
    } catch (error) {
      lease.release()
      throw error
    } finally {
      signal?.removeEventListener('abort', abort)
      this.#beginnings.delete(controller)
    }
  }

  inspect(): MemoryTestDiagnostics {
    return jsonClone(this.#generations.inspect(), 'memory test diagnostics')
  }

  async managementCatalog(scope: MemoryOperationScope = { storage: 'custom' }): Promise<MemorySourceManagementCatalog> {
    this.#assertOpen()
    const lease = this.#generations.acquire()
    try { return await lease.generation.managementCatalog(scope) }
    finally { lease.release() }
  }

  /** Exercise the public protocol, including rejected confirmation/revision cases. */
  async executeManagement(request: MemorySourceManagementRequest): Promise<MemorySourceManagementResult> {
    this.#assertOpen()
    const lease = this.#generations.acquire()
    try { return await lease.generation.executeManagement(request) }
    finally { lease.release() }
  }

  /** Same scoped management contract a Source page receives, backed by real generations. */
  async managementClient(sourceInstanceKey: string, scopeValue: MemoryViewRequest['scope'] = { storage: 'custom' }): Promise<MemoryTestManagementClient> {
    const scope = structuredClone(scopeValue)
    const instance = (await this.managementCatalog(scope)).sources.find(source => source.sourceInstanceKey === sourceInstanceKey)
    if (instance === undefined) throw new Error(`No managed test Source: ${sourceInstanceKey}`)
    let revision = instance.revision
    const execute = async (mode: 'read' | 'mutate', operation: string, input: MemoryJsonValue, options?: { confirmed: true; expectedRevision?: string }) => {
      const result = await this.executeManagement({
        sourceInstanceKey, scope, mode, operation, input, confirmed: options?.confirmed === true,
        ...(mode === 'read' ? {} : { expectedRevision: options?.expectedRevision ?? revision }),
      })
      revision = result.revision
      return result
    }
    return {
      sourceInstanceKey, get revision() { return revision },
      read: (operation, input = null) => execute('read', operation, input),
      mutate: (operation, input, options) => execute('mutate', operation, input, options),
    }
  }

  dispose(): Promise<void> {
    if (this.#closing !== undefined) return this.#closing
    this.#closed = true
    for (const controller of this.#beginnings) controller.abort(new Error('MemoryCompositionRunner is disposed'))
    for (const release of this.#turns) release()
    this.#closing = (async () => {
      const failures: unknown[] = []
      for (const fiber of [...this.#entries.values()].reverse()) {
        try { await fiber.dispose() } catch (error) { failures.push(error) }
      }
      this.#entries.clear()
      try { await this.context.fiber.dispose() } catch (error) { failures.push(error) }
      if (failures.length > 0) throw new AggregateError(failures, 'MemoryCompositionRunner cleanup failed')
    })()
    return this.#closing
  }

  #assertOpen(): void {
    if (this.#closed) throw new Error('MemoryCompositionRunner is disposed')
  }
}

export { DEFAULT_MEMORY_VIEW_BUDGET }

/**
 * Evaluate a trusted, already-built DSH Client artifact in a test. Dependencies
 * are explicit (normally the test's React and UI primitives), so no production
 * Loader, global registry, source alias, or second copy of React is required.
 * This is a test fixture, not another Client Loader implementation.
 */
export function loadMemoryClientArtifact<T extends object = Record<string, unknown>>(
  path: string | URL,
  dependencies: Readonly<Record<string, unknown>>,
): T {
  let declaration: { id: string; factory(require: (id: string) => unknown): unknown } | undefined
  const loader = { load(value: typeof declaration) {
    if (declaration !== undefined || value === undefined || typeof value.id !== 'string' || typeof value.factory !== 'function') {
      throw new Error('Expected exactly one DSH Client artifact declaration')
    }
    declaration = value
  } }
  const actualWindow = (globalThis as unknown as { window?: object }).window ?? {}
  const window = new Proxy(actualWindow, {
    get(target, key) {
      if (key === '__ModuleLoader__') return loader
      const value = Reflect.get(target, key, target) as unknown
      return typeof value === 'function' ? value.bind(target) : value
    },
  })
  new Function('window', readFileSync(path, 'utf8'))(window)
  if (declaration === undefined) throw new Error('No DSH Client declaration in artifact')
  return declaration.factory(id => {
    if (!Object.hasOwn(dependencies, id)) throw new Error(`Missing Client test dependency: ${id}`)
    return dependencies[id]
  }) as T
}
