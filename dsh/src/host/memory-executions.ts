import { randomUUID } from 'node:crypto'
import type { ComposableMemoryTurn } from '../core/turns.ts'
import type { DelegatedMemoryView } from './agent-memory-turn.ts'
import type { HostAgent } from './dsh.ts'
import { agentScope, type MnemonAgentRuntimeSource, type MnemonRuntimeGraph } from './runtime.ts'

interface MemoryExecution {
  readonly graph: MnemonRuntimeGraph
  readonly context: ComposableMemoryTurn
  release(): void
}

interface ExecutionSlot {
  kind: 'turn' | 'workflow'
  users: number
  closing: boolean
  controller: AbortController
  ready: Promise<MemoryExecution>
  done: Promise<void>
  close(): void
}

/** One Host owner pairs each Core turn with its runtime binding. */
export class MemoryExecutions {
  private readonly slots = new Map<string, ExecutionSlot>()
  private readonly pending = new Set<ExecutionSlot>()
  private closed = false

  constructor(private readonly runtime: Pick<MnemonAgentRuntimeSource, 'forAgent' | 'bindAgentRuntime'>) {}

  async turn(agent: HostAgent, turn: number, signal: AbortSignal, delegation?: DelegatedMemoryView): Promise<MemoryExecution> {
    signal.throwIfAborted()
    const slot = this.open(agent, 'turn', `${agent.id}:${turn}`, 'agent.root-turn', delegation)
    const abort = () => slot.controller.abort(signal.reason)
    signal.addEventListener('abort', abort, { once: true })
    try {
      const execution = await slot.ready
      signal.throwIfAborted()
      return { ...execution, release: () => { signal.removeEventListener('abort', abort); slot.close() } }
    } catch (error) {
      signal.removeEventListener('abort', abort)
      slot.close()
      throw error
    }
  }

  async workflow(agent: HostAgent, operation: string, signal: AbortSignal): Promise<MemoryExecution & { signal: AbortSignal }> {
    this.assertOpen()
    signal.throwIfAborted()
    let slot = this.slots.get(agent.id)
    if (slot?.closing) slot = undefined
    if (slot === undefined) {
      const graph = this.runtime.forAgent(agent)
      const context = graph.composableTurns.activeTurn(agent.id)
      // A directly pinned/delegated turn remains owned by its caller.
      if (!this.slots.has(agent.id) && context !== undefined) return { graph, context, signal, release() {} }
      slot = this.open(agent, 'workflow', 'workflow:' + randomUUID(), 'agent.' + operation)
    }
    const current = slot
    if (current.kind === 'turn') {
      const execution = await untilAborted(current.ready, signal)
      return { ...execution, signal, release() {} }
    }
    current.users += 1
    let released = false
    const release = () => {
      if (released) return
      released = true
      if (--current.users === 0) current.close()
    }
    try {
      const execution = await untilAborted(current.ready, signal)
      return { ...execution, signal: AbortSignal.any([signal, current.controller.signal]), release }
    } catch (error) { release(); throw error }
  }

  retain(agent: HostAgent, delegation: DelegatedMemoryView): () => void {
    this.assertOpen()
    const releaseView = delegation.viewId === undefined ? undefined : delegation.graph.composableTurns.retainView(delegation.viewId)
    try {
      const releaseRuntime = this.runtime.bindAgentRuntime(agent.id, delegation.graph)
      return () => { try { releaseView?.() } finally { releaseRuntime() } }
    } catch (error) { releaseView?.(); throw error }
  }

  dispose(): void {
    this.closed = true
    for (const slot of this.pending) {
      slot.controller.abort(new Error('Mnemon runtime is disposed'))
      if (slot.kind === 'turn') slot.close()
    }
  }

  private open(agent: HostAgent, kind: ExecutionSlot['kind'], turnId: string, purpose: string, delegation?: DelegatedMemoryView): ExecutionSlot {
    this.assertOpen()
    const previous = this.slots.get(agent.id)
    const done = deferred<void>()
    const ready = deferred<MemoryExecution>()
    let execution: MemoryExecution | undefined
    const finish = () => {
      if (this.slots.get(agent.id) === slot) this.slots.delete(agent.id)
      this.pending.delete(slot)
      done.resolve()
    }
    const slot: ExecutionSlot = {
      kind, users: 0, closing: false, controller: new AbortController(), done: done.promise,
      ready: ready.promise,
      close: () => {
        if (slot.closing) return
        slot.closing = true
        slot.controller.abort(new Error('memory execution ended'))
        if (execution !== undefined) { try { execution.release() } finally { finish() } }
        // Preparation owns cleanup until it actually settles, even after abort.
      },
    }
    this.slots.set(agent.id, slot)
    this.pending.add(slot)
    void (async () => {
      await previous?.done
      slot.controller.signal.throwIfAborted()
      this.assertOpen()
      const graph = delegation?.graph ?? this.runtime.forAgent(agent)
      const manager = graph.composableTurns
      const scope = delegation === undefined ? agentScope(agent, graph.config)
        : { ...delegation.scope, sessionId: agent.id, agentId: agent.id }
      const releaseRuntime = delegation === undefined ? this.runtime.bindAgentRuntime(agent.id, graph) : undefined
      let context: ComposableMemoryTurn | undefined
      const release = () => {
        try { if (context !== undefined && manager.turn(context.turnId) === context) manager.endTurn(context.turnId) }
        finally { releaseRuntime?.() }
      }
      try {
        context = delegation?.viewId === undefined
          ? await manager.beginTurn(turnId, scope, purpose, slot.controller.signal)
          : manager.pinTurn(turnId, scope, delegation.viewId)
        slot.controller.signal.throwIfAborted()
        execution = { graph, context, release }
        return execution
      } catch (error) { release(); throw error }
    })().then(ready.resolve, error => { finish(); ready.reject(error) })
    // A queued slot can outlive a canceled caller while its predecessor drains.
    void slot.ready.catch(() => {})
    return slot
  }

  private assertOpen(): void {
    if (this.closed) throw new Error('Mnemon runtime is disposed')
  }
}

async function untilAborted<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  signal.throwIfAborted()
  const aborted = deferred<never>()
  const abort = () => aborted.reject(signal.reason)
  signal.addEventListener('abort', abort, { once: true })
  try { return await Promise.race([promise, aborted.promise]) }
  finally { signal.removeEventListener('abort', abort) }
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
