import type { Context } from '@deepseek-ai/cordis'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  COMPOSABLE_MEMORY_API_VERSION, type MemorySourceRuntime, type MemorySourceRuntimeContext,
  type MemoryViewContribution,
} from 'dsh-mnemon/contracts'
import { defineMemorySource, defineMemoryStrategy, installMemory } from 'dsh-mnemon/extension-sdk'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'

const runners: MemoryCompositionRunner[] = []
afterEach(async () => {
  for (const runner of runners.splice(0)) await runner.dispose()
  vi.useRealTimers()
})

async function fixture(required: string[] = [], timeoutMs = 50) {
  const runner = new MemoryCompositionRunner({ sourceTimeoutMs: timeoutMs })
  runners.push(runner)
  await runner.mount({ inject: ['mnemonMemory'], apply(ctx: Context) {
    installMemory(ctx, { strategies: [defineMemoryStrategy({
      manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'test', packageName: 'test-strategy',
        deterministic: true, supportedSourceRoles: ['notes'], maxSources: 4, maxRoutes: 4, maxActions: 4 },
      compose(request, sources) {
        expect(Object.keys(request).sort()).toEqual(['budget', 'scenario', 'scope'])
        expect(() => JSON.stringify(request)).not.toThrow()
        return { strategyTypeId: 'test', explanation: 'Explicit required and optional Sources.',
          sources: sources.map(source => ({ sourceInstanceKey: source.sourceInstanceKey,
            required: required.includes(source.sourceInstanceKey), projection: { mode: 'eager', maxCharacters: 100 },
            routeIds: source.routeIds, actionIds: source.actionIds })) }
      },
    })] })
  } }, { instanceId: 'strategy' })
  async function mount(id: string, overrides: (context: MemorySourceRuntimeContext) => Partial<MemorySourceRuntime> = () => ({})) {
    await runner.mount({ inject: ['mnemonMemory'], apply(ctx: Context) {
      installMemory(ctx, { sources: [defineMemorySource({
        manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'notes', packageName: 'test-source',
          role: 'notes', capabilities: ['project', 'read', 'write'], consistency: 'exact-snapshot',
          routes: [{ id: 'read', description: 'Read', capability: 'read', inputSchema: { type: 'object' }, maxCalls: 1 }],
          actions: [{ id: 'write', description: 'Write', capability: 'write', inputSchema: { type: 'object' } }],
          management: { label: 'Notes', description: 'Test Source' } },
        create(context) {
          return {
            facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'notes', role: 'notes',
              availability: 'ready', revision: 'r1', capabilities: ['project', 'read', 'write'], routeIds: ['read'], actionIds: ['write'] }),
            project: request => ({ fragments: [{ id: 'note', sourceInstanceKey: context.sourceInstanceKey, mode: request.mode, text: id, revision: 'r1' }],
              readGrant: { id: context.sourceInstanceKey + '/grant', sourceInstanceKey: context.sourceInstanceKey,
                schema: 'notes/v1', value: null, revision: 'r1', consistency: 'exact-snapshot' } }),
            manage: () => ({ revision: 'r1', value: id }),
            ...overrides(context),
          }
        },
      })] })
    } }, { instanceId: id })
  }
  await mount('good')
  return { runner, mount }
}

describe('Source composition failure boundaries', () => {
  it('isolates optional facts failures and sanitizes View and management diagnostics', async () => {
    const { runner, mount } = await fixture()
    await mount('offline', () => ({ facts() { throw new Error('https://private.example?token=DO-NOT-EXPOSE') } }))
    const turn = await runner.beginTurn()
    expect(turn.view.projection.map(value => value.text)).toEqual(['good'])
    expect(turn.view.diagnostics).toEqual([{ code: 'source-facts-failed', contributionInstanceKey: 'source:offline',
      message: 'Memory Source source:offline facts() failed' }])
    const catalog = await runner.managementCatalog()
    expect(catalog.sources.map(source => source.availability)).toEqual(['ready', 'unavailable'])
    expect(catalog.diagnostics).toEqual(turn.view.diagnostics)
    expect(JSON.stringify({ view: turn.view, catalog })).not.toContain('DO-NOT-EXPOSE')
    await expect(runner.beginTurn()).resolves.toHaveProperty('view')
  })

  it('fails closed when the Strategy requires a Source with unavailable facts', async () => {
    const { runner, mount } = await fixture(['source:offline'])
    await mount('offline', () => ({ facts() { throw new Error('private reason') } }))
    await expect(runner.beginTurn()).rejects.toThrow('unavailable required Source runtime: source:offline')
    expect(runner.inspect().evaluation.state).toBe('ready')
  })

  it.each([false, true])('handles a project failure according to required=%s', async required => {
    const { runner, mount } = await fixture(required ? ['source:offline'] : [])
    await mount('offline', () => ({ project() { throw new Error('private reason') } }))
    if (required) {
      await expect(runner.beginTurn()).rejects.toThrow('Memory Source source:offline project() failed')
    } else {
      const { view } = await runner.beginTurn()
      expect(view.projection.map(value => value.text)).toEqual(['good'])
      expect(view.routes.map(value => value.sourceInstanceKey)).toEqual(['source:good'])
      expect(view.actionOffers.map(value => value.sourceInstanceKey)).toEqual(['source:good'])
      expect(view.readGrants.map(value => value.sourceInstanceKey)).toEqual(['source:good'])
      expect(view.diagnostics?.[0]?.code).toBe('source-project-failed')
    }
  })

  it('runs independent facts concurrently and times out hung Sources without leaking timers', async () => {
    vi.useFakeTimers()
    const { runner, mount } = await fixture()
    const started: string[] = []
    const signals: AbortSignal[] = []
    for (const id of ['slow-a', 'slow-b']) await mount(id, () => ({ facts(_request, signal) {
      started.push(id); signals.push(signal!)
      return new Promise(() => {})
    } }))
    const pending = runner.beginTurn()
    await vi.advanceTimersByTimeAsync(0)
    expect(started).toEqual(['slow-a', 'slow-b'])
    await vi.advanceTimersByTimeAsync(50)
    const { view } = await pending
    expect(view.projection.map(value => value.text)).toEqual(['good'])
    expect(view.diagnostics?.map(value => value.code)).toEqual(['source-facts-timeout', 'source-facts-timeout'])
    expect(signals.every(signal => signal.aborted)).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('discards an optional late projection instead of mutating an already-published View', async () => {
    vi.useFakeTimers()
    const { runner, mount } = await fixture()
    const late = Promise.withResolvers<MemoryViewContribution>()
    let signal: AbortSignal | undefined
    await mount('slow', () => ({ project(_request, abort) { signal = abort; return late.promise } }))
    const pending = runner.beginTurn()
    await vi.advanceTimersByTimeAsync(50)
    const { view } = await pending
    const snapshot = JSON.stringify(view)
    expect(view.diagnostics?.[0]?.code).toBe('source-project-timeout')
    expect(signal?.aborted).toBe(true)
    late.resolve({ fragments: [] })
    await vi.advanceTimersByTimeAsync(0)
    expect(JSON.stringify(view)).toBe(snapshot)
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['facts', 'project'] as const)('propagates cancellation during %s instead of degrading it', async phase => {
    const { runner, mount } = await fixture([], 10_000)
    const started = Promise.withResolvers<void>()
    let seen: AbortSignal | undefined
    const pendingRead = (_request: unknown, signal?: AbortSignal): Promise<never> => {
      seen = signal; started.resolve(); return new Promise(() => {})
    }
    await mount('slow', () => ({ [phase]: pendingRead }))
    const cancellation = new AbortController()
    const pending = runner.beginTurn({}, cancellation.signal)
    await started.promise
    const rejected = expect(pending).rejects.toThrow('stop composition')
    cancellation.abort(new Error('stop composition'))
    await rejected
    expect(seen?.aborted).toBe(true)
  })

  it('aborts a pending read when the public runner closes, without waiting for its deadline', async () => {
    const { runner, mount } = await fixture([], 10_000)
    const started = Promise.withResolvers<void>()
    await mount('slow', () => ({ facts() { started.resolve(); return new Promise(() => {}) } }))
    const pending = runner.beginTurn()
    await started.promise
    const rejected = expect(pending).rejects.toThrow('disposed')
    await runner.dispose()
    await rejected
  })

  it('does not turn malformed Source authority claims into a recoverable outage', async () => {
    const { runner, mount } = await fixture()
    await mount('invalid', context => ({ facts: () => ({ sourceInstanceKey: context.sourceInstanceKey,
      sourceTypeId: 'notes', role: 'notes', availability: 'ready', revision: 'r1',
      capabilities: ['forget'], routeIds: [], actionIds: [],
    }) }))
    await expect(runner.beginTurn()).rejects.toThrow('expand manifest capability')
  })
})
