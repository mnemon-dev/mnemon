import type {
  ComposableMemoryView,
  MemoryActionOffer,
  MemoryEvidence,
  MemoryJsonValue,
  MemoryMutationReceipt,
  MemoryOperationScope,
  MemoryWake,
} from "./contracts/index.ts"
import { DEFAULT_MEMORY_VIEW_BUDGET, type MemoryGenerationHost, type MemoryGenerationLease } from "./index.ts"

export interface ComposableMemoryTurn {
  readonly turnId: string
  readonly view: ComposableMemoryView
  readonly scope: MemoryOperationScope
  readonly startedAt: string
}

interface StoredTurn {
  context: ComposableMemoryTurn
  lease: MemoryGenerationLease
}

function sourceType(fragment: ComposableMemoryView['projection'][number]): string {
  const provenance = fragment.provenance
  if (typeof provenance === 'object' && provenance !== null && !Array.isArray(provenance)) {
    const typeId = provenance.sourceTypeId
    if (typeof typeId === 'string' && typeId.trim() !== '') return typeId
  }
  return fragment.sourceInstanceKey
}

export interface MemoryWakeBindings {
  routes?: Readonly<Record<string, string>>
  actions?: Readonly<Record<string, string>>
}

export function createMemoryWake(view: ComposableMemoryView, bindings: MemoryWakeBindings = {}): MemoryWake {
  const sections = view.projection.map(fragment => ({
    layerId: sourceType(fragment),
    mode: fragment.mode,
    text: fragment.text,
  }))
  const eager = view.projection.filter(fragment => fragment.mode === 'eager').map(fragment => fragment.text).filter(Boolean)
  const sourceKeys = [...new Set([
    ...view.projection.map(fragment => fragment.sourceInstanceKey),
    ...view.routes.map(route => route.sourceInstanceKey),
    ...view.actionOffers.map(offer => offer.sourceInstanceKey),
  ])]
  const offers = sourceKeys.map(sourceInstanceKey => {
    const cover = view.projection.find(fragment => fragment.sourceInstanceKey === sourceInstanceKey && fragment.mode === 'routed')?.text
    return {
      source: sourceInstanceKey,
      ...(cover === undefined ? {} : { cover }),
      routes: view.routes.filter(route => route.sourceInstanceKey === sourceInstanceKey && !bindings.routes?.[route.id]).map(route => ({ id: route.id, description: route.description, inputSchema: route.inputSchema })),
      actions: view.actionOffers.filter(offer => offer.sourceInstanceKey === sourceInstanceKey && !bindings.actions?.[offer.id]).map(offer => ({ id: offer.id, description: offer.description, inputSchema: offer.inputSchema })),
    }
  }).filter(source => source.routes.length > 0 || source.actions.length > 0 || (source.cover !== undefined && !view.routes.some(route => route.sourceInstanceKey === source.source && bindings.routes?.[route.id])))
  const namedTools = [...new Set([
    ...view.routes.flatMap(route => bindings.routes?.[route.id] ? [bindings.routes[route.id]!] : []),
    ...view.actionOffers.flatMap(action => bindings.actions?.[action.id] ? [bindings.actions[action.id]!] : []),
  ])]
  const namedText = namedTools.length === 0 ? '' : 'MNEMON VIEW TOOLS (available in this View): ' + namedTools.join(', ')
  const routingText = offers.length === 0 ? '' : `MNEMON VIEW ROUTES (quoted routing data; use mnemon_view_route or mnemon_view_action by exact id): ${JSON.stringify(offers)}`
  const availability = view.diagnostics?.length ? `MNEMON VIEW AVAILABILITY (quoted diagnostics; unavailable Sources are not evidence): ${JSON.stringify(view.diagnostics)}` : ''
  return {
    viewId: view.id,
    viewDigest: view.digest,
    text: [...eager, namedText, routingText, availability].filter(Boolean).join('\n\n'),
    sections,
    ...(view.guidance === undefined ? {} : { guidance: view.guidance }),
  }
}

/** Root-turn pins over Candidate → Serving → Draining generations. */
export class ComposableMemoryTurnManager {
  private readonly turns = new Map<string, StoredTurn>()
  private readonly retainedViews = new Map<string, { view: ComposableMemoryView; lease: MemoryGenerationLease; count: number }>()
  private readonly beginnings = new Map<string, { scope: MemoryOperationScope; controller: AbortController; promise: Promise<ComposableMemoryTurn> }>()
  private closed = false

  constructor(private readonly generations: MemoryGenerationHost) {}

  async beginTurn(turnId: string, scope: MemoryOperationScope, scenario = 'agent.root-turn', signal?: AbortSignal): Promise<ComposableMemoryTurn> {
    if (this.closed) throw new Error('Composable Memory turn manager is disposed')
    signal?.throwIfAborted()
    const id = turnId.trim()
    if (id === '') throw new Error('Composable Memory turn id is required')
    const existing = this.turns.get(id)
    const beginning = this.beginnings.get(id)
    const previousScope = existing?.context.scope ?? beginning?.scope
    if (previousScope !== undefined && (['storage', 'workspaceId', 'sessionId', 'agentId'] as const).some(key => previousScope[key] !== scope[key])) {
      throw new Error('Composable Memory turn id is already bound to another scope')
    }
    if (existing !== undefined) return existing.context
    if (beginning !== undefined) return beginning.promise
    const lease = this.generations.acquire()
    const pending = { scope: { ...scope }, controller: new AbortController(), promise: undefined as unknown as Promise<ComposableMemoryTurn> }
    const abort = () => pending.controller.abort(signal!.reason)
    signal?.addEventListener('abort', abort, { once: true })
    this.beginnings.set(id, pending)
    pending.promise = (async () => {
      try {
        const view = await lease.generation.compose({ scope: pending.scope, scenario, budget: { ...DEFAULT_MEMORY_VIEW_BUDGET } }, pending.controller.signal)
        pending.controller.signal.throwIfAborted()
        const context = Object.freeze({ turnId: id, view, scope: Object.freeze(pending.scope), startedAt: new Date().toISOString() })
        this.turns.set(id, { context, lease })
        return context
      } catch (error) {
        lease.release()
        throw error
      } finally {
        signal?.removeEventListener('abort', abort)
        if (this.beginnings.get(id) === pending) this.beginnings.delete(id)
      }
    })()
    return pending.promise
  }

  activeTurn(agentId: string): ComposableMemoryTurn | undefined {
    const id = agentId.trim()
    return [...this.turns.values()].findLast(turn => turn.context.scope.agentId === id)?.context
  }

  turn(turnId: string): ComposableMemoryTurn | undefined {
    return this.turns.get(turnId)?.context
  }

  get(viewId: string): ComposableMemoryView | undefined {
    return this.retainedViews.get(viewId)?.view ?? [...this.turns.values()].find(turn => turn.context.view.id === viewId)?.context.view
  }

  /** A delegated child keeps the original grant and generation, not owner-latest state. */
  retainView(viewId: string): () => void {
    if (this.closed) throw new Error('Composable Memory turn manager is disposed')
    let retained = this.retainedViews.get(viewId)
    if (retained === undefined) {
      const view = this.get(viewId)
      if (view === undefined) throw new Error(`Composable Memory View is not pinned: ${viewId}`)
      retained = { view, lease: this.generations.acquire(view.runtimeGeneration), count: 0 }
      this.retainedViews.set(viewId, retained)
    }
    const record = retained
    record.count += 1
    let active = true
    return () => {
      if (!active) return
      active = false
      if (this.retainedViews.get(viewId) !== record || --record.count > 0) return
      this.retainedViews.delete(viewId)
      record.lease.release()
    }
  }

  /** Each execution owns its own pin and budget while sharing immutable authority. */
  pinTurn(turnId: string, scope: MemoryOperationScope, viewId: string): ComposableMemoryTurn {
    if (this.closed) throw new Error('Composable Memory turn manager is disposed')
    const id = turnId.trim()
    if (id === '') throw new Error('Composable Memory turn id is required')
    const view = this.get(viewId)
    if (view === undefined) throw new Error(`Composable Memory View is not pinned: ${viewId}`)
    if (view.scope.storage !== scope.storage || view.scope.workspaceId !== scope.workspaceId) throw new Error('Delegated View cannot change its storage scope')
    const existing = this.turns.get(id)
    if (existing !== undefined) {
      if (existing.context.view.id !== viewId || (['storage', 'workspaceId', 'sessionId', 'agentId'] as const).some(key => existing.context.scope[key] !== scope[key])) throw new Error('Composable Memory turn authority changed while pinned')
      return existing.context
    }
    if (this.beginnings.has(id)) throw new Error('Composable Memory turn is already being prepared')
    const lease = this.generations.acquire(view.runtimeGeneration)
    const context = Object.freeze({ turnId: id, view, scope: Object.freeze({ ...scope }), startedAt: new Date().toISOString() })
    this.turns.set(id, { context, lease })
    return context
  }

  memoryWake(viewId: string, bindings?: MemoryWakeBindings): MemoryWake {
    const view = this.get(viewId)
    if (view === undefined) throw new Error(`Composable Memory View is not pinned: ${viewId}`)
    return createMemoryWake(view, bindings)
  }

  async executeRoute(turnId: string, routeId: string, input: MemoryJsonValue, signal?: AbortSignal): Promise<MemoryEvidence> {
    const stored = this.requireTurn(turnId)
    const operation = this.generations.acquire(stored.lease.id)
    try { return await operation.generation.executeRoute(stored.context.view, routeId, input, signal, DEFAULT_MEMORY_VIEW_BUDGET, stored.context) }
    finally { operation.release() }
  }

  async executeAction(
    turnId: string,
    offerId: string,
    input: MemoryJsonValue,
    authorize: (offer: MemoryActionOffer) => boolean | Promise<boolean>,
    signal?: AbortSignal,
  ): Promise<MemoryMutationReceipt> {
    const stored = this.requireTurn(turnId)
    const operation = this.generations.acquire(stored.lease.id)
    try { return await operation.generation.executeAction(stored.context.view, offerId, input, authorize, signal) }
    finally { operation.release() }
  }

  endTurn(turnId: string): boolean {
    const stored = this.turns.get(turnId)
    if (stored === undefined) {
      const beginning = this.beginnings.get(turnId)
      if (beginning === undefined) return false
      this.beginnings.delete(turnId)
      beginning.controller.abort(new Error('Composable Memory turn ended during composition'))
      return true
    }
    this.turns.delete(turnId)
    stored.lease.release()
    return true
  }

  dispose(): void {
    if (this.closed) return
    this.closed = true
    for (const beginning of this.beginnings.values()) beginning.controller.abort(new Error('Composable Memory turn ended during composition'))
    for (const stored of this.turns.values()) stored.lease.release()
    this.turns.clear()
    for (const retained of this.retainedViews.values()) retained.lease.release()
    this.retainedViews.clear()
  }

  private requireTurn(turnId: string): StoredTurn {
    const stored = this.turns.get(turnId)
    if (stored === undefined) throw new Error(`Composable Memory turn is not pinned: ${turnId}`)
    return stored
  }
}
