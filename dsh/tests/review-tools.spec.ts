import { describe, expect, it, vi } from 'vitest'
import type { HostAgent, HostSubagentRun, ToolExecution } from '../src/host/dsh.ts'
import { idleReviewBlockReason, reviewLayerPolicy, startGuardedReview, type ReviewToolHost } from '../src/host/review-tools.ts'

function fixture() {
  const listeners = new Set<(event: { agent: HostAgent }) => void>()
  const owners = new Map<string, HostAgent>()
  const observerDispose = vi.fn()
  const host: ReviewToolHost = {
    agents: { isOwnedBy: (id, owner) => owners.get(id) === owner },
    on(_name, listener) {
      const callback = listener as (event: { agent: HostAgent }) => void
      listeners.add(callback)
      return () => { listeners.delete(callback); observerDispose() }
    },
  }
  const parent = { id: 'parent' } as HostAgent
  const child = (id: string) => {
    const guards = new Set<(execution: ToolExecution) => string | undefined>()
    const release = vi.fn()
    const agent = { id, ctx: { tools: { guard(callback: (execution: ToolExecution) => string | undefined) {
      guards.add(callback)
      return () => { guards.delete(callback); release() }
    } } } } as unknown as HostAgent
    const runDispose = vi.fn(async () => {})
    // Third-party public runs may expose their fields as prototype getters.
    const run: HostSubagentRun = new class {
      get id() { return id }
      get localAgent() { return agent }
      get result() { return Promise.resolve({ output: [], stopReason: 'completed' }) }
      dispose() { return runDispose() }
    }()
    return { agent, guards, release, run, runDispose,
      denial(name: string) { return [...guards].map(guard => guard({ name, agent, signal: new AbortController().signal })).find(Boolean) },
    }
  }
  const publish = (agent: HostAgent, owner = parent) => {
    owners.set(agent.id, owner)
    for (const listener of listeners) listener({ agent })
  }
  return { host, parent, child, publish, listeners, observerDispose }
}

describe('review publication and execution boundary', () => {
  it.each([[false, false], [true, false], [false, true], [true, true]])('checks scoped Team tools together with the public service: %s/%s', (service, capability) => {
    const get = vi.fn(() => capability ? {} : undefined)
    const parent = { ctx: { get: (name: string) => service && name === 'agentTeams' ? {} : undefined, tools: { get } } } as unknown as HostAgent
    expect(idleReviewBlockReason(parent)).toBe(service && capability ? 'agent-team' : undefined)
    expect(idleReviewBlockReason(parent, 'pause')).toBe(service && capability ? 'agent-team' : undefined)
    expect(idleReviewBlockReason(parent, 'scoped')).toBeUndefined()
    if (service) expect(get).toHaveBeenCalledWith('spawn_teammate', parent)
  })
  it('separates nested same-parent reviews and leaves other parents alone', async () => {
    const f = fixture()
    const outer = f.child('outer'), inner = f.child('inner'), unrelated = f.child('unrelated')
    let innerRun: HostSubagentRun | undefined
    const outerRun = await startGuardedReview(f.host, f.parent, ['outer-result'], async () => {
      innerRun = await startGuardedReview(f.host, f.parent, ['inner-result'], async () => {
        f.publish(unrelated.agent, { id: 'another-parent' } as HostAgent)
        f.publish(inner.agent)
        expect(inner.guards.size).toBe(1)
        expect(inner.denial('inner-result')).toBeUndefined()
        return inner.run
      })
      f.publish(outer.agent)
      expect(outer.denial('outer-result')).toBeUndefined()
      return outer.run
    })
    expect(outerRun.id).toBe('outer')
    expect(outerRun.localAgent).toBe(outer.agent)
    await expect(outerRun.result).resolves.toMatchObject({ stopReason: 'completed' })
    expect(inner.denial('outer-result')).toContain('Mnemon review')
    expect(outer.denial('inner-result')).toContain('Mnemon review')
    expect(unrelated.guards.size).toBe(0)
    expect(f.listeners.size).toBe(0)
    await innerRun!.dispose()
    await outerRun.dispose()
    expect(inner.guards.size + outer.guards.size).toBe(0)
  })

  it('keeps independently overlapping allowlists separate', async () => {
    const f = fixture()
    const first = f.child('first'), second = f.child('second')
    const release = Promise.withResolvers<void>()
    const firstRun = startGuardedReview(f.host, f.parent, ['first-result'], async () => {
      await release.promise
      f.publish(first.agent)
      return first.run
    })
    const secondRun = await startGuardedReview(f.host, f.parent, ['second-result'], async () => {
      f.publish(second.agent)
      return second.run
    })
    release.resolve()
    const active = await firstRun
    expect(first.guards.size).toBe(1)
    expect(second.guards.size).toBe(1)
    expect(first.denial('second-result')).toContain('Mnemon review')
    expect(second.denial('first-result')).toContain('Mnemon review')
    await active.dispose()
    await secondRun.dispose()
  })

  it('allows the supplied capabilities and PTC transport while retaining guards through child cleanup', async () => {
    const f = fixture(), child = f.child('child')
    const release = Promise.withResolvers<void>()
    child.runDispose.mockImplementation(() => release.promise)
    const run = await startGuardedReview(f.host, f.parent, ['mnemon_document_search', 'mnemon_subagent_result'], async () => {
      f.publish(child.agent)
      return child.run
    })
    expect(child.denial('mnemon_document_search')).toBeUndefined()
    expect(child.denial('mnemon_subagent_result')).toBeUndefined()
    expect(child.denial('run_code')).toBeUndefined()
    expect(child.denial('mcp__aoci__aoci_overview')).toContain('reuse the inherited checkpoint')
    for (const name of ['spawn_teammate', 'send_message', 'team_task_create', 'fork_agent', 'spawn_agent']) {
      expect(child.denial(name)).toContain('Mnemon review cannot execute')
    }
    const disposing = run.dispose()
    expect(child.guards.size).toBe(1)
    release.resolve()
    await disposing
    expect(child.guards.size).toBe(0)
  })

  it('cleans up the owned child if removing the publication observer fails', async () => {
    const f = fixture(), child = f.child('child')
    f.observerDispose.mockImplementation(() => { throw new Error('observer disposer failure') })
    await expect(startGuardedReview(f.host, f.parent, [], async () => {
      f.publish(child.agent)
      return child.run
    })).rejects.toThrow('observer disposer failure')
    expect(child.runDispose).toHaveBeenCalledOnce()
    expect(child.release).toHaveBeenCalledOnce()
    expect(f.listeners.size).toBe(0)
  })

  it('attempts every guard disposer even when one fails', async () => {
    const f = fixture(), first = f.child('first'), second = f.child('second')
    first.release.mockImplementation(() => { throw new Error('guard disposer failure') })
    const run = await startGuardedReview(f.host, f.parent, [], async () => {
      f.publish(first.agent)
      f.publish(second.agent)
      return first.run
    })
    await expect(run.dispose()).rejects.toThrow('guard cleanup failed')
    expect(first.release).toHaveBeenCalledOnce()
    expect(second.release).toHaveBeenCalledOnce()
  })

  it('removes publication state and guards when provider startup rejects', async () => {
    const f = fixture(), child = f.child('child')
    await expect(startGuardedReview(f.host, f.parent, [], async () => {
      f.publish(child.agent)
      throw new Error('provider rolled back')
    })).rejects.toThrow('provider rolled back')
    expect(child.guards.size).toBe(0)
    expect(f.listeners.size).toBe(0)
  })

  it('fails before starting when ownership is unavailable and rejects an unguarded published child', async () => {
    const f = fixture(), child = f.child('child'), start = vi.fn(async () => child.run)
    await expect(startGuardedReview({ on: f.host.on }, f.parent, [], start)).rejects.toThrow('ownership')
    expect(start).not.toHaveBeenCalled()
    await expect(startGuardedReview(f.host, f.parent, [], start)).rejects.toThrow('scoped tool guard')
    expect(child.runDispose).toHaveBeenCalledOnce()
    expect(f.listeners.size).toBe(0)
  })
})

describe('one layer per idle review pass (#319)', () => {
  const call = (name: string, args?: unknown): ToolExecution => ({ name, arguments: args, signal: new AbortController().signal })

  it('refuses working memory after a Document, and a Document after working memory', () => {
    const documentsFirst = reviewLayerPolicy()
    expect(documentsFirst(call('mnemon_document_create', { title: 'Boot account resolution' }))).toBeUndefined()
    for (const action of ['add', 'replace', 'remove']) {
      expect(documentsFirst(call('mnemon_runtime_memory', { action, target: 'memory' }))).toContain('already created a Document')
    }
    const memoryFirst = reviewLayerPolicy()
    expect(memoryFirst(call('mnemon_runtime_memory', { action: 'add', target: 'memory', content: 'Run pnpm verify before pushing.' }))).toBeUndefined()
    expect(memoryFirst(call('mnemon_document_create', { title: 'Release checklist' }))).toContain('already changed working memory')
  })

  it('leaves profile changes, reads and the claimed layer alone', () => {
    const policy = reviewLayerPolicy()
    expect(policy(call('mnemon_document_search', { query: 'boot account' }))).toBeUndefined()
    expect(policy(call('mnemon_runtime_memory', { action: 'add', target: 'user', content: 'Prefers short answers.' }))).toBeUndefined()
    expect(policy(call('mnemon_document_create', { title: 'Boot account resolution' }))).toBeUndefined()
    // A retry stays in its layer; "at most one" of each remains the persona's rule.
    expect(policy(call('mnemon_document_create', { title: 'Boot account resolution' }))).toBeUndefined()
    expect(policy(call('mnemon_runtime_memory', { action: 'replace', target: 'user', old_text: 'short' }))).toBeUndefined()
    expect(policy(call('run_code', { code: 'await tools.mnemon_document_search({ query: "boot" })' }))).toBeUndefined()
    expect(policy(call('mnemon_subagent_result', {}))).toBeUndefined()
  })

  it('claims no layer for a runtime call without target=memory', () => {
    const policy = reviewLayerPolicy()
    for (const args of [undefined, null, 'memory', { action: 'add' }, { action: 'add', target: 'project' }]) {
      expect(policy(call('mnemon_runtime_memory', args))).toBeUndefined()
    }
    expect(policy(call('mnemon_document_create', { title: 'Boot account resolution' }))).toBeUndefined()
  })

  it('judges only calls the allowlist admits, including PTC sub-dispatches', async () => {
    const f = fixture(), child = f.child('child')
    const run = await startGuardedReview(f.host, f.parent, ['mnemon_document_create', 'mnemon_runtime_memory'], async () => {
      f.publish(child.agent)
      return child.run
    }, undefined, reviewLayerPolicy())
    const [guard] = child.guards
    expect(guard!(call('run_code', {}))).toBeUndefined()
    expect(guard!({ ...call('mnemon_document_create', { title: 'Boot account resolution' }), parent: Symbol('run_code') })).toBeUndefined()
    expect(guard!(call('mnemon_runtime_memory', { action: 'add', target: 'memory' }))).toContain('already created a Document')
    expect(guard!(call('mnemon_remember', { content: 'Same fact.' }))).toContain('Mnemon review cannot execute')
    await run.dispose()
  })
})
