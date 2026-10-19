import { isDefaultSourceInstance } from './protocol.ts'
import type { MemoryActionOffer, MemoryEvidence, MemoryJsonValue, MemoryMutationReceipt, MemoryOperationScope, MemorySourceManagementRequest } from '../core/contracts/index.ts'
import type { MemoryGenerationHost } from '../core/index.ts'
import type { ComposableMemoryTurn, ComposableMemoryTurnManager } from '../core/turns.ts'
import type { MemoryCompositionGeneration } from '../core/composition.ts'

/** Host-side caller of a Source's JSON protocol, never its implementation. */
export class SourceSession {
  constructor(
    private readonly generations: MemoryGenerationHost,
    private readonly turns: ComposableMemoryTurnManager,
    readonly typeId: string,
    readonly scope: MemoryOperationScope,
    private readonly pinnedTurn?: ComposableMemoryTurn,
    private readonly instanceKey?: string,
    private readonly generation?: MemoryCompositionGeneration,
  ) {}

  /** Capture execution identity before awaiting work; never borrow a later turn. */
  forTurn(turn: ComposableMemoryTurn): SourceSession {
    if (turn.scope.agentId !== this.scope.agentId || turn.scope.sessionId !== this.scope.sessionId
      || turn.scope.storage !== this.scope.storage || turn.scope.workspaceId !== this.scope.workspaceId) throw new Error('Source session scope does not match the pinned turn')
    return new SourceSession(this.generations, this.turns, this.typeId, this.scope, turn, this.instanceKey, this.generation)
  }

  /** Exact instance identity; never fall back to a different Source of the same type. */
  forInstance(instanceKey: string): SourceSession {
    return new SourceSession(this.generations, this.turns, this.typeId, this.scope, this.pinnedTurn, instanceKey, this.generation)
  }

  /** The caller owns this generation's lease for the entire operation. */
  forGeneration(generation: MemoryCompositionGeneration): SourceSession {
    return new SourceSession(this.generations, this.turns, this.typeId, this.scope, this.pinnedTurn, this.instanceKey, generation)
  }

  read<T>(operation: string, input: unknown = null, signal?: AbortSignal): Promise<T> {
    return this.execute<T>('read', operation, input, signal)
  }

  identity() {
    return this.selected(this.activeTurn())
  }
  mutate<T>(operation: string, input: unknown, signal?: AbortSignal): Promise<T> {
    return this.execute<T>('mutate', operation, input, signal)
  }

  mutateResult<T>(operation: string, input: unknown, signal?: AbortSignal): Promise<{ revision: string; value: T }> {
    return this.executeResult<T>('mutate', operation, input, signal)
  }

  /** Model tools always use the offered Route, never the management channel. */
  async route(routeId: string, input: unknown, signal?: AbortSignal): Promise<MemoryEvidence> {
    const turn = this.requireTurn()
    const source = await this.selected(turn)
    const route = turn.view.routes.find(item => item.sourceInstanceKey === source.sourceInstanceKey && item.sourceRouteId === routeId)
    if (route === undefined) throw new Error('Source Route is not offered by the current View: ' + this.typeId + '/' + routeId)
    this.assertTurn(turn)
    return this.turns.executeRoute(turn.turnId, route.id, json(input), signal)
  }
  async action(actionId: string, input: unknown, authorize: (offer: MemoryActionOffer) => boolean, signal?: AbortSignal): Promise<MemoryMutationReceipt> {
    const turn = this.requireTurn()
    const offer = await this.offeredAction(turn, actionId)
    return this.turns.executeAction(turn.turnId, offer.id, json(input), authorize, signal)
  }
  /** Preflight Host-coordinated maintenance before any management side effect. */
  async assertActionOffered(actionId: string, authorize: (offer: MemoryActionOffer) => boolean): Promise<void> {
    const offer = await this.offeredAction(this.requireTurn(), actionId)
    if (!authorize(offer)) throw new Error('memory ActionOffer is not currently authorized: ' + offer.id)
  }
  private async offeredAction(turn: ComposableMemoryTurn, actionId: string): Promise<MemoryActionOffer> {
    const source = await this.selected(turn)
    const offer = turn.view.actionOffers.find(item => item.sourceInstanceKey === source.sourceInstanceKey && item.sourceActionId === actionId)
    if (offer === undefined) throw new Error('Source Action is not offered by the current View: ' + this.typeId + '/' + actionId)
    this.assertTurn(turn)
    return offer
  }
  private activeTurn(): ComposableMemoryTurn | undefined {
    if (this.pinnedTurn !== undefined) {
      this.assertTurn(this.pinnedTurn)
      return this.pinnedTurn
    }
    return this.scope.agentId === undefined ? undefined : this.turns.activeTurn(this.scope.agentId)
  }
  private assertTurn(turn: ComposableMemoryTurn): void {
    if (this.turns.turn(turn.turnId) !== turn) throw new Error('Memory operation belongs to an ended turn')
  }
  private requireTurn(): ComposableMemoryTurn {
    const turn = this.activeTurn()
    if (turn === undefined) throw new Error('Memory operation requires the View pinned to the current turn')
    return turn
  }
  private async selected(turn?: ComposableMemoryTurn) {
    if (this.generation !== undefined) return this.select(this.generation)
    const lease = this.generations.acquire(turn?.view.runtimeGeneration)
    try { return await this.select(lease.generation) } finally { lease.release() }
  }
  private select(generation: import('../core/composition.ts').MemoryCompositionGeneration) {
    const candidates = generation.sourceInstances().filter(source => source.sourceTypeId === this.typeId)
    const source = this.instanceKey !== undefined ? candidates.find(source => source.sourceInstanceKey === this.instanceKey)
      : candidates.find(source => isDefaultSourceInstance(source.sourceInstanceKey, this.typeId))
      ?? (candidates.length === 1 ? candidates[0] : undefined)
    if (source === undefined) throw new Error('Source ' + this.typeId + ' is ' + (candidates.length === 0 ? 'not installed' : 'ambiguous; select an explicit instance'))
    return source
  }
  private async execute<T>(mode: MemorySourceManagementRequest['mode'], operation: string, input: unknown, signal?: AbortSignal): Promise<T> {
    return (await this.executeResult<T>(mode, operation, input, signal)).value
  }

  private async executeResult<T>(mode: MemorySourceManagementRequest['mode'], operation: string, input: unknown, signal?: AbortSignal): Promise<{ revision: string; value: T }> {
    const turn = this.activeTurn()
    const lease = this.generation === undefined ? this.generations.acquire(turn?.view.runtimeGeneration) : undefined
    const generation = this.generation ?? lease!.generation
    try {
      const source = await this.select(generation)
      const expectedRevision = mode === 'mutate' ? await generation.managementRevision(source.sourceInstanceKey, this.scope, signal) : undefined
      if (turn !== undefined) this.assertTurn(turn)
      const result = await generation.executeManagement({
        sourceInstanceKey: source.sourceInstanceKey, scope: this.scope, mode, operation, input: json(input),
        confirmed: mode === 'mutate',
        ...(expectedRevision === undefined ? {} : { expectedRevision }),
        ...(signal === undefined ? {} : { signal }),
      })
      return { revision: result.revision, value: result.value as T }
    } finally { lease?.release() }
  }
}

function json(input: unknown): MemoryJsonValue {
  return JSON.parse(JSON.stringify(input)) as MemoryJsonValue
}

/** Error codes are part of the Source protocol; its private Error classes are not. */
export function sourceFailure<T extends { code: string }>(value: unknown, code: T['code']): value is Error & T {
  return value instanceof Error && 'code' in value && value.code === code
}
