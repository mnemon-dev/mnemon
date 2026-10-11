import { afterEach, describe, expect, it, vi } from 'vitest'
import { AgentMemoryTurn } from '../src/host/agent-memory-turn.ts'
import type { HostAgent } from '../src/host/dsh.ts'
import { LiveMnemonRuntime } from '../src/host/runtime.ts'
import { memoryGraphFixture } from './helpers/memory-graph.ts'
import { sessionLog } from './fixtures/session-log.ts'

const releases: Array<() => void> = []
afterEach(() => { for (const release of releases.splice(0).reverse()) release() })

function fixture() {
  const memory = memoryGraphFixture()
  const live = new LiveMnemonRuntime(memory.graph, undefined, undefined, memory.extensions)
  const agent = { id: 'root', session: { header: {}, ...sessionLog() } } as unknown as HostAgent
  const owner = new AgentMemoryTurn(agent, live)
  releases.push(() => { owner.dispose(); live.dispose() })
  return { ...memory, live, agent, owner, signal: new AbortController().signal }
}

describe('Host memory execution ownership', () => {
  it('supports the Node 20 Promise API advertised by the package', async () => {
    const f = fixture()
    const unsupported = vi.spyOn(Promise, 'withResolvers').mockImplementation(() => { throw new Error('unavailable on Node 20') })
    try { await f.owner.begin(1); expect(f.owner.current).toBeDefined() }
    finally { unsupported.mockRestore() }
  })

  it('does not acquire resources for an already canceled operation', async () => {
    const f = fixture()
    const bind = vi.spyOn(f.live, 'bindAgentRuntime')
    await expect(f.live.executions.workflow(f.agent, 'read', AbortSignal.abort(new Error('canceled')))).rejects.toThrow('canceled')
    expect(bind).not.toHaveBeenCalled()
    expect(f.snapshot).not.toHaveBeenCalled()
  })

  it('releases a failed preparation so the next operation can own the Agent', async () => {
    const f = fixture()
    vi.spyOn(f.views, 'beginTurn').mockRejectedValueOnce(new Error('cannot prepare'))
    await expect(f.live.executions.workflow(f.agent, 'read', f.signal)).rejects.toThrow('cannot prepare')
    await f.owner.begin(1)
    expect(f.owner.current?.context.turnId).toBe('root:1')
  })

  it('keeps shared preparation alive when only one waiting caller cancels', async () => {
    const f = fixture()
    const gate = Promise.withResolvers<void>()
    const prepare = f.views.beginTurn.bind(f.views)
    const begin = vi.spyOn(f.views, 'beginTurn').mockImplementationOnce(async (...args) => {
      await gate.promise
      return prepare(...args)
    })
    const canceled = new AbortController()
    const first = f.live.executions.workflow(f.agent, 'read', canceled.signal)
    const second = f.live.executions.workflow(f.agent, 'write', f.signal)
    const failed = expect(first).rejects.toThrow('one caller canceled')
    await vi.waitFor(() => expect(begin).toHaveBeenCalledOnce())
    canceled.abort(new Error('one caller canceled'))
    await failed
    expect(begin.mock.calls[0]![3]?.aborted).toBe(false)
    gate.resolve()
    const execution = await second
    expect(f.views.activeTurn('root')).toBe(execution.context)
    execution.release()
    expect(f.views.activeTurn('root')).toBeUndefined()
  })

  it('waits for canceled preparation cleanup before a replacement turn binds', async () => {
    const f = fixture()
    const gate = Promise.withResolvers<void>()
    const prepare = f.views.beginTurn.bind(f.views)
    const begin = vi.spyOn(f.views, 'beginTurn').mockImplementationOnce(async (...args) => {
      await gate.promise
      return prepare(...args)
    })
    const canceled = new AbortController()
    const background = f.live.executions.workflow(f.agent, 'read', canceled.signal)
    const failed = expect(background).rejects.toThrow('canceled')
    await vi.waitFor(() => expect(begin).toHaveBeenCalledOnce())
    canceled.abort(new Error('canceled'))
    await failed
    const foreground = f.owner.begin(1)
    expect(begin).toHaveBeenCalledOnce()
    gate.resolve()
    await foreground
    expect(f.views.activeTurn('root')).toBe(f.owner.current?.context)
    expect(begin).toHaveBeenCalledTimes(2)
  })

  it('reserves foreground preparation and lends its View without transferring ownership', async () => {
    const f = fixture()
    const gate = Promise.withResolvers<void>()
    const prepare = f.views.beginTurn.bind(f.views)
    const begin = vi.spyOn(f.views, 'beginTurn').mockImplementationOnce(async (...args) => {
      await gate.promise
      return prepare(...args)
    })
    const foreground = f.owner.begin(1)
    const borrowed = f.live.executions.workflow(f.agent, 'read', f.signal)
    gate.resolve()
    await foreground
    const execution = await borrowed
    expect(begin).toHaveBeenCalledOnce()
    expect(execution.context).toBe(f.owner.current?.context)
    execution.release()
    expect(f.views.activeTurn('root')).toBe(execution.context)
    f.owner.end()
    expect(f.views.activeTurn('root')).toBeUndefined()
    expect(execution.signal.aborted).toBe(false)
  })

  it('resolves the new runtime after maintenance drains, including a canceled handoff', async () => {
    const f = fixture()
    const replacement = memoryGraphFixture(['replacement'])
    const background = await f.live.executions.workflow(f.agent, 'write', f.signal)
    const first = f.owner.begin(1).catch(error => error as Error)
    const next = f.owner.begin(2)
    f.live.swap(replacement.graph)
    expect(f.live.forAgent(f.agent)).toBe(f.graph)
    background.release()
    expect(await first).toBeInstanceOf(Error)
    await next
    expect(f.owner.current?.graph).toBe(replacement.graph)
    expect(f.owner.current?.context.turnId).toBe('root:2')
  })

  it('aborts owned maintenance on runtime disposal and rejects new operations', async () => {
    const f = fixture()
    const execution = await f.live.executions.workflow(f.agent, 'write', f.signal)
    f.live.dispose()
    expect(execution.signal.aborted).toBe(true)
    execution.release()
    await expect(f.live.executions.workflow(f.agent, 'read', f.signal)).rejects.toThrow('disposed')
  })
})
