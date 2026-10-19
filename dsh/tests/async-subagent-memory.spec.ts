import { resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { HostAgent, HostContextShape, HostSessionEvent } from '../src/host/dsh.ts'
import { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { LiveMnemonRuntime } from '../src/host/runtime.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { memoryGraphFixture as graphFixture } from './helpers/memory-graph.ts'
import { sessionLog } from './fixtures/session-log.ts'

type Listener = (...args: unknown[]) => unknown
const disposers: Array<() => void> = []

afterEach(() => {
  for (const dispose of disposers.splice(0).reverse()) dispose()
})

function events() {
  const listeners = new Map<string, Listener[]>()
  const effects = new Set<() => void>()
  return {
    on(name: string, listener: Listener, options?: { prepend?: boolean }) {
      const list = listeners.get(name) ?? []
      if (options?.prepend) list.unshift(listener)
      else list.push(listener)
      listeners.set(name, list)
      return () => {
        const index = list.indexOf(listener)
        if (index >= 0) list.splice(index, 1)
      }
    },
    effect(callback: () => (() => void) | void) {
      const cleanup = callback()
      const stop = () => {
        if (!effects.delete(stop)) return
        cleanup?.()
      }
      effects.add(stop)
      return stop
    },
    emit(name: string, ...args: unknown[]) {
      for (const listener of [...listeners.get(name) ?? []]) listener(...args)
    },
    async waterfall(name: string, args: unknown[], terminal: () => unknown): Promise<unknown> {
      const list = [...listeners.get(name) ?? []]
      const next = async (index: number): Promise<unknown> => index === list.length
        ? terminal()
        : list[index]!(...args, () => next(index + 1))
      return next(0)
    },
    dispose() {
      for (const stop of [...effects].reverse()) stop()
    },
  }
}

function fixture() {
  const memory = graphFixture()
  const runtime = new LiveMnemonRuntime(memory.graph, undefined, undefined, memory.extensions)
  const registry = new Map<string, HostAgent>()
  const controls = new Map<HostAgent, ReturnType<typeof events>>()
  const logs = new Map<HostAgent, HostSessionEvent[]>()
  const hostEvents = events()
  const host = {
    ...hostEvents,
    agents: {
      get: (id: string) => registry.get(id),
      roots: () => [...registry.values()].filter(agent => agent.session.header?.origin !== 'subagent'),
    },
  } as unknown as HostContextShape
  const coordinator = new MnemonSubagentCoordinator({} as never, runtime)
  const lifecycle = new MnemonLifecycle(host, coordinator, memory.graph.config, runtime)
  const stop = lifecycle.start()
  const create = (id: string, parent?: HostAgent, configure?: (ctx: ReturnType<typeof events>) => void) => {
    const ctx = events()
    const log: HostSessionEvent[] = []
    configure?.(ctx)
    const agent = {
      id,
      status: 'idle',
      session: {
        header: { cwd: resolve('/workspace/project'), ...(parent === undefined ? {} : { origin: 'subagent', parentSession: parent.id }) },
        ...sessionLog(log),
      },
      ctx,
      followup: vi.fn(),
      steer: vi.fn(),
      inject: vi.fn(),
    } as unknown as HostAgent
    controls.set(agent, ctx)
    logs.set(agent, log)
    registry.set(id, agent)
    hostEvents.emit('agent/created', { agent, source: 'startup' })
    return agent
  }
  const append = (agent: HostAgent, type: string, turn: number) => {
    const log = logs.get(agent)!
    const event = { seq: log.length, type, data: { turn } }
    log.push(event)
    controls.get(agent)!.emit('session/event', agent.session, event)
  }
  const begin = async (agent: HostAgent, turn: number, signal = new AbortController().signal) => {
    agent.status = 'running'
    append(agent, 'turn/start', turn)
    const assembly = { sections: [], contexts: [] }
    await controls.get(agent)!.waterfall('system-prompt/assemble', [assembly, { agent, signal }], () => assembly)
  }
  const end = async (agent: HostAgent, turn: number) => {
    append(agent, 'turn/end', turn)
    agent.status = 'idle'
  }
  const dispose = (agent: HostAgent) => {
    controls.get(agent)!.dispose()
    if (registry.get(agent.id) === agent) registry.delete(agent.id)
  }
  disposers.push(() => {
    stop()
    for (const agent of controls.keys()) dispose(agent)
    runtime.dispose()
  })
  const root = create('root')
  const recall = (agent: HostAgent, query: string) => coordinator.recall(agent, { query }, new AbortController().signal, { requirePinnedView: true })
  return { ...memory, runtime, coordinator, lifecycle, root, create, begin, end, dispose, recall, controls, logs }
}

describe('asynchronous child memory authority', () => {
  it('keeps recall available after the dispatching parent turn ends', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const child = value.create('child', value.root)
    await value.end(value.root, 1)
    await value.begin(child, 1)
    expect((await value.recall(child, 'task query')).results.map(row => row.memoryBodyId)).toEqual(['project'])
  })

  it('retains the dispatch View instead of adding a later parent turn\'s spaces', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const child = value.create('child', value.root)
    await value.end(value.root, 1)
    value.setIds(['project', 'space-added-later'])
    await value.begin(value.root, 2)
    await value.end(value.root, 2)
    await value.begin(child, 1)
    expect((await value.recall(child, 'original task')).results.map(row => row.memoryBodyId)).toEqual(['project'])
    expect(() => value.coordinator.scopeRecallRequest(child, { query: 'outside', memoryBodyIds: ['space-added-later'] }, true)).toThrow('outside pinned Source')
  })

  it('does not share exhausted budgets between children dispatched in different parent turns', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const first = value.create('first-child', value.root)
    await value.end(value.root, 1)
    await value.begin(first, 1)
    await value.recall(first, 'first task initial query')
    await value.recall(first, 'first task refined query')
    await value.end(first, 1)
    await value.begin(value.root, 2)
    const second = value.create('second-child', value.root)
    await value.end(value.root, 2)
    await value.begin(second, 1)
    const result = await value.recall(second, 'second task new query')
    expect({ calls: value.search.mock.calls.length, query: result.query }).toEqual({ calls: 3, query: 'second task new query' })
  })

  it('never replays another task\'s evidence after its authorized Source changes', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const first = value.create('first-child', value.root)
    await value.end(value.root, 1)
    await value.begin(first, 1)
    await value.recall(first, 'release history')
    await value.end(first, 1)
    value.setIds(['replacement'])
    await value.begin(value.root, 2)
    const second = value.create('second-child', value.root)
    await value.end(value.root, 2)
    await value.begin(second, 1)
    const result = await value.recall(second, 'release history')
    expect({ calls: value.search.mock.calls.length, spaces: result.results.map(row => row.memoryBodyId) }).toEqual({ calls: 2, spaces: ['replacement'] })
  })

  it('starts a fresh retrieval budget for each real child turn, even with the same View', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const child = value.create('child', value.root)
    await value.end(value.root, 1)
    await value.begin(child, 1)
    await value.recall(child, 'release history')
    await value.end(child, 1)
    await value.begin(child, 2)
    await value.recall(child, 'release history')
    expect(value.search).toHaveBeenCalledTimes(2)
  })

  it('keeps the delegated runtime after the parent releases it and settings swap', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const child = value.create('child', value.root)
    await value.end(value.root, 1)
    const replacement = graphFixture(['replacement'])
    value.runtime.swap(replacement.graph)
    await value.begin(child, 1)
    expect(value.runtime.forAgent(child)).toBe(value.graph)
    expect((await value.recall(child, 'old task')).results.map(row => row.memoryBodyId)).toEqual(['project'])
    expect(replacement.search).not.toHaveBeenCalled()
  })

  it('keeps a delegated View alive under collection pressure until its last child releases it', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const original = value.views.activeTurn(value.root.id)!.view.id
    const child = value.create('child', value.root)
    await value.end(value.root, 1)
    for (let turn = 2; turn <= 6; turn += 1) {
      value.setIds([`space-${turn}`])
      await value.begin(value.root, turn)
      await value.end(value.root, turn)
    }
    expect(value.views.get(original)).toBeDefined()
    await value.begin(child, 1)
    expect((await value.recall(child, 'old task')).results.map(row => row.memoryBodyId)).toEqual(['project'])
    value.dispose(child)
    value.setIds(['final-space'])
    await value.begin(value.root, 7)
    expect(value.views.get(original)).toBeUndefined()
  })

  it('lets an explicitly created background child pin a fresh View without a parent model turn', async () => {
    const value = fixture()
    const child = value.create('background-child', value.root)
    await value.begin(child, 1)
    expect((await value.recall(child, 'background task')).results.map(row => row.memoryBodyId)).toEqual(['project'])
  })

  it('does not reuse cached evidence when a disposed Agent identity and turn number are recreated', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    await value.recall(value.root, 'release history')
    await value.end(value.root, 1)
    value.dispose(value.root)
    value.setIds(['replacement'])
    const recreated = value.create('root')
    await value.begin(recreated, 1)
    const result = await value.recall(recreated, 'release history')
    expect({ calls: value.search.mock.calls.length, spaces: result.results.map(row => row.memoryBodyId) }).toEqual({ calls: 2, spaces: ['replacement'] })
  })

  it('isolates sibling budgets even when both inherit the exact same View', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const first = value.create('first', value.root)
    const second = value.create('second', value.root)
    await value.begin(first, 1)
    await value.begin(second, 1)
    expect(value.views.activeTurn(first.id)!.view.id).toBe(value.views.activeTurn(second.id)!.view.id)
    await value.recall(first, 'release history')
    await value.recall(second, 'release history')
    expect(value.search).toHaveBeenCalledTimes(2)
    expect((await value.coordinator.documentQuery(first, { query: 'probe' }, new AbortController().signal) as { notRun?: boolean }).notRun === true).toBe(false)
    expect((await value.coordinator.documentQuery(second, { query: 'probe' }, new AbortController().signal) as { notRun?: boolean }).notRun === true).toBe(false)
    expect((await value.coordinator.documentQuery(first, { query: 'probe' }, new AbortController().signal) as { notRun?: boolean }).notRun === true).toBe(true)
  })

  it('retains nested delegation after both ancestors finish and the parent is disposed', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const child = value.create('child', value.root)
    await value.begin(child, 1)
    const grandchild = value.create('grandchild', child)
    await value.end(value.root, 1)
    await value.end(child, 1)
    value.dispose(child)
    value.setIds(['later'])
    await value.begin(value.root, 2)
    await value.begin(grandchild, 1)
    expect((await value.recall(grandchild, 'nested task')).results.map(row => row.memoryBodyId)).toEqual(['project'])
  })

  it('does not release a sibling\'s delegated View when another sibling is disposed', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const first = value.create('first', value.root)
    const second = value.create('second', value.root)
    await value.end(value.root, 1)
    value.dispose(first)
    value.dispose(first)
    for (let turn = 2; turn <= 5; turn += 1) {
      value.setIds([`space-${turn}`])
      await value.begin(value.root, turn)
      await value.end(value.root, turn)
    }
    await value.begin(second, 1)
    expect((await value.recall(second, 'surviving task')).results.map(row => row.memoryBodyId)).toEqual(['project'])
  })

  it('fails closed after a child turn ends even if the parent still has an active pin', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const child = value.create('child', value.root)
    await value.begin(child, 1)
    await value.end(child, 1)
    await expect(value.recall(child, 'outside a turn')).rejects.toThrow('pinned to the current turn')
    expect(value.search).not.toHaveBeenCalled()
  })

  it('does not derive authority from an orphaned parentSession string', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    await value.end(value.root, 1)
    value.dispose(value.root)
    const orphan = value.create('orphan', value.root)
    await value.begin(orphan, 1)
    await expect(value.recall(orphan, 'orphan query')).rejects.toThrow('pinned to the current turn')
    expect(value.search).not.toHaveBeenCalled()
  })

  it('keeps the delegated workspace scope when a child header is changed', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const child = value.create('child', value.root)
    child.session.header!.cwd = resolve('/workspace/unrelated')
    await value.end(value.root, 1)
    await value.begin(child, 1)
    expect(value.views.activeTurn(child.id)!.scope).toMatchObject({ workspaceId: resolve('/workspace/project'), agentId: child.id, sessionId: child.id })
  })

  it('releases a rejected child step pin but keeps its delegation for a later turn', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const child = value.create('child', value.root)
    await value.begin(child, 1)
    await value.controls.get(child)!.waterfall('agent/pre-step', [{ agent: child, turn: 1, step: 1, signal: new AbortController().signal }], () => ({ kind: 'reject' }))
    expect(value.views.activeTurn(child.id)).toBeUndefined()
    await value.end(child, 1)
    await value.end(value.root, 1)
    await value.begin(child, 2)
    expect((await value.recall(child, 'retry')).results.map(row => row.memoryBodyId)).toEqual(['project'])
  })

  it('cleans a pending background View if the child is disposed during compilation', async () => {
    const value = fixture()
    const child = value.create('background', value.root)
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    value.snapshot.mockImplementationOnce(async () => {
      await gate
      return { revision: 'delayed', wake: 'Delayed source.', state: { memoryBodyIds: ['project'] } }
    })
    const pending = value.begin(child, 1)
    const rejected = expect(pending).rejects.toThrow('turn ended during View preparation')
    await vi.waitFor(() => expect(value.snapshot).toHaveBeenCalledOnce())
    value.dispose(child)
    release()
    await rejected
    expect(value.views.activeTurn(child.id)).toBeUndefined()
    const replacement = graphFixture(['replacement'])
    value.runtime.swap(replacement.graph)
    expect(value.runtime.forAgent(child)).toBe(replacement.graph)
  })

  it('cleans an aborted View compilation and permits a subsequent child turn', async () => {
    const value = fixture()
    const child = value.create('background', value.root)
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    value.snapshot.mockImplementationOnce(async () => {
      await gate
      return { revision: 'delayed', wake: 'Delayed source.', state: { memoryBodyIds: ['project'] } }
    })
    const controller = new AbortController()
    const pending = value.begin(child, 1, controller.signal)
    const rejected = expect(pending).rejects.toThrow('cancelled compilation')
    await vi.waitFor(() => expect(value.snapshot).toHaveBeenCalledOnce())
    controller.abort(new Error('cancelled compilation'))
    release()
    await rejected
    expect(value.views.activeTurn(child.id)).toBeUndefined()
    await value.end(child, 1)
    await value.begin(child, 2)
    expect((await value.recall(child, 'retry')).results.map(row => row.memoryBodyId)).toEqual(['project'])
  })

  it('shares one pending pin for concurrent assemblies of the same child turn', async () => {
    const value = fixture()
    const child = value.create('background', value.root)
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    value.snapshot.mockImplementationOnce(async () => {
      await gate
      return { revision: 'delayed', wake: 'Delayed source.', state: { memoryBodyIds: ['project'] } }
    })
    const first = value.begin(child, 1)
    const second = value.begin(child, 1)
    await vi.waitFor(() => expect(value.snapshot).toHaveBeenCalledOnce())
    release()
    await Promise.all([first, second])
    await value.recall(child, 'duplicate query')
    await value.recall(child, 'duplicate query')
    expect(value.search).toHaveBeenCalledOnce()
  })

  it('does not evict an active turn budget when more than 128 children are running', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const first = value.create('first', value.root)
    await value.begin(first, 1)
    await value.recall(first, 'initial')
    await value.recall(first, 'refined')
    for (let index = 0; index < 130; index += 1) {
      const child = value.create(`parallel-${index}`, value.root)
      await value.begin(child, 1)
      expect((await value.coordinator.documentQuery(child, { query: 'probe' }, new AbortController().signal) as { notRun?: boolean }).notRun === true).toBe(false)
    }
    const exhausted = await value.recall(first, 'third query')
    expect(exhausted.hint).toContain('budget is exhausted')
    expect(value.search).toHaveBeenCalledTimes(2)
    expect(value.lifecycle.snapshot().activeAgents).toBe(1)
  })

  it('rolls back a delegated View and runtime when child hook installation fails', async () => {
    const value = fixture()
    await value.begin(value.root, 1)
    const original = value.views.activeTurn(value.root.id)!.view.id
    expect(() => value.create('broken-child', value.root, ctx => {
      const on = ctx.on
      ctx.on = (name, listener, options) => {
        if (name === 'system-prompt/assemble') throw new Error('hook installation failed')
        return on(name, listener, options)
      }
    })).toThrow('hook installation failed')
    await value.end(value.root, 1)
    for (let turn = 2; turn <= 5; turn += 1) {
      value.setIds([`space-${turn}`])
      await value.begin(value.root, turn)
      await value.end(value.root, turn)
    }
    expect(value.views.get(original)).toBeUndefined()
    const replacement = graphFixture(['replacement'])
    value.runtime.swap(replacement.graph)
    const child = [...value.controls.keys()].find(agent => agent.id === 'broken-child')!
    expect(value.runtime.forAgent(child)).toBe(replacement.graph)
    await expect(value.recall(child, 'no authority')).rejects.toThrow('pinned to the current turn')
  })
})
