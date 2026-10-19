import { resolve } from 'node:path'
import type { MemoryOperationScope } from '../core/contracts/index.ts'
import { isMemoryCompositionUnavailable } from '../core/generation.ts'
import type { ComposableMemoryTurn } from '../core/turns.ts'
import type { HostAgent } from './dsh.ts'
import { agentScope, type MnemonAgentRuntimeSource, type MnemonRuntimeGraph } from './runtime.ts'
import { inspectMemoryView, modelMemoryWake } from './view-presentation.ts'
import type { MemoryViewInspection } from './view-protocol.ts'

/** Host-owned authority captured before a child activation starts executing. */
export interface DelegatedMemoryView {
  readonly graph: MnemonRuntimeGraph
  readonly scope: MemoryOperationScope
  readonly viewId?: string
}

export interface PinnedAgentMemoryTurn {
  readonly turn: number
  readonly graph: MnemonRuntimeGraph
  readonly context: ComposableMemoryTurn
  release(): void
}

/** The durable log, not a parent session id, identifies the executing turn. */
export function openAgentTurn(agent: HostAgent): number | undefined {
  let open: number | undefined
  for (const event of agent.session.snapshotEvents()) {
    const turn = typeof event.data.turn === 'number' ? event.data.turn : undefined
    if (event.type === 'turn/start' && turn !== undefined) open = turn
    else if (event.type === 'turn/end' && turn === open) open = undefined
  }
  return open
}

/**
 * One Agent owns each turn pin. A child additionally retains its delegation
 * across parent completion, collection, and runtime swaps until it is disposed.
 */
export class AgentMemoryTurn {
  private pinned: PinnedAgentMemoryTurn | undefined
  /** A turn that runs without memory because no composition was serving when it began. */
  private unavailable: { turn: number; reason: string } | undefined
  private warned: string | undefined
  private pending: { turn: number; generation: number; controller: AbortController; result: Promise<void> } | undefined
  private generation = 0
  private closed = false
  private lastInspection: MemoryViewInspection | undefined
  private lastWorkspace: string | undefined
  private readonly releaseDelegation: (() => void) | undefined

  constructor(
    private readonly agent: HostAgent,
    private readonly runtime: Pick<MnemonAgentRuntimeSource, 'forAgent' | 'executions'>,
    private readonly delegation?: DelegatedMemoryView,
  ) {
    if (delegation === undefined) return
    this.releaseDelegation = runtime.executions.retain(agent, delegation)
  }

  get current(): PinnedAgentMemoryTurn | undefined {
    return this.pinned
  }

  /** Why the current turn has no View, when memory could not be composed for it. */
  get unavailableReason(): string | undefined {
    return this.unavailable?.reason
  }

  inspect(workspaceRoot?: string): MemoryViewInspection | undefined {
    if (this.lastInspection === undefined) return undefined
    if (workspaceRoot !== undefined && resolve(workspaceRoot) !== this.lastWorkspace) return undefined
    return structuredClone({ ...this.lastInspection, state: this.pinned === undefined ? 'recent' : 'active' })
  }

  clearInspection(): void { this.lastInspection = undefined; this.lastWorkspace = undefined }

  /** Capture now; descendants must not resolve a later parent turn on demand. */
  delegate(): DelegatedMemoryView {
    if (this.closed) throw new Error('memory Agent lifetime has ended')
    if (this.pinned !== undefined) {
      return { graph: this.pinned.graph, scope: this.pinned.context.scope, viewId: this.pinned.context.view.id }
    }
    if (this.delegation !== undefined) return this.delegation
    const graph = this.runtime.forAgent(this.agent)
    // Explicit Host background operations have no model turn to inherit. Their
    // child creates a fresh scoped View, never a historical owner-latest View.
    return { graph, scope: agentScope(this.agent, graph.config) }
  }

  async begin(turn: number, signal?: AbortSignal): Promise<void> {
    if (this.closed) throw new Error('memory Agent lifetime has ended')
    signal?.throwIfAborted()
    if (this.pinned?.turn === turn || this.unavailable?.turn === turn) return
    if (this.pending?.turn === turn) return this.pending.result
    this.end()
    const generation = this.generation
    const controller = new AbortController()
    const abort = () => controller.abort(signal!.reason)
    signal?.addEventListener('abort', abort, { once: true })
    const result = this.pin(turn, generation, controller.signal).catch((error: unknown) => {
      // Memory is optional for a conversation. With no composition serving,
      // the turn runs without a View: nothing revives a disabled policy, and
      // the turn itself does not fail. It stays that way until the turn ends.
      if (!isMemoryCompositionUnavailable(error) || this.closed || this.generation !== generation) throw error
      this.unavailable = { turn, reason: error.message }
      this.clearInspection()
      if (this.warned !== error.message) console.warn(`[dsh-mnemon] this turn runs without memory: ${error.message}`)
      this.warned = error.message
    })
    this.pending = { turn, generation, controller, result }
    try {
      await result
    } finally {
      signal?.removeEventListener('abort', abort)
      if (this.pending?.generation === generation) this.pending = undefined
    }
  }

  end(turn?: number): void {
    if (turn !== undefined && this.pinned?.turn !== turn && this.pending?.turn !== turn && this.unavailable?.turn !== turn) return
    this.generation += 1
    this.unavailable = undefined
    this.pending?.controller.abort(new Error('memory turn ended during View preparation'))
    this.pending = undefined
    const pinned = this.pinned
    this.pinned = undefined
    pinned?.release()
  }

  dispose(): void {
    if (this.closed) return
    this.closed = true
    this.clearInspection()
    try { this.end() } finally { this.releaseDelegation?.() }
  }

  private async pin(turn: number, generation: number, signal: AbortSignal): Promise<void> {
    const execution = await this.runtime.executions.turn(this.agent, turn, signal, this.delegation)
    const { graph, context } = execution
    try {
      if (this.closed || this.generation !== generation) throw new Error('memory turn ended during View preparation')
      signal?.throwIfAborted()
      const inspection = inspectMemoryView(graph.memoryComposition.generation(context.view.runtimeGeneration)!, context.view, 'active', turn, modelMemoryWake(graph, context).text)
      this.pinned = { turn, graph, context, release: execution.release }
      this.warned = undefined
      this.lastInspection = inspection
      this.lastWorkspace = context.scope.workspaceId === undefined ? undefined : resolve(context.scope.workspaceId)
    } catch (error) {
      execution.release()
      throw error
    }
  }
}
