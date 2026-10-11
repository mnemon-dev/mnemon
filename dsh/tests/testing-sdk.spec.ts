import type { Context } from '@deepseek-ai/cordis'
import { afterEach, describe, expect, expectTypeOf, it, vi } from 'vitest'
import { COMPOSABLE_MEMORY_API_VERSION, type MemorySourceRuntime, type MemorySourceRuntimeContext } from 'dsh-mnemon/contracts'
import { defineMemorySource, defineMemoryStrategy, installMemory } from 'dsh-mnemon/extension-sdk'
import { DEFAULT_MEMORY_VIEW_BUDGET, MemoryCompositionRunner, type MemoryTestOptions, type MemoryTestTurn } from 'dsh-mnemon/testing'

const runners: MemoryCompositionRunner[] = []
afterEach(async () => { for (const runner of runners.splice(0)) await runner.dispose() })

function gate() {
  let resolve!: () => void
  const promise = new Promise<void>(done => { resolve = done })
  return { promise, resolve }
}

async function fixture(options: MemoryTestOptions = {}, customize: (context: MemorySourceRuntimeContext) => Partial<MemorySourceRuntime> = () => ({})) {
  const runner = new MemoryCompositionRunner(options)
  runners.push(runner)
  const source = defineMemorySource({
    manifest: {
      apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'notes', packageName: 'test-source-notes',
      role: 'notes', capabilities: ['project', 'read', 'write'], consistency: 'exact-snapshot',
      routes: [{ id: 'read', description: 'Read the captured note', capability: 'read', inputSchema: { type: 'object' }, maxCalls: 2 }],
      actions: [{ id: 'write', description: 'Write a note', capability: 'write', inputSchema: { type: 'object' } }],
      management: { label: 'Notes', description: 'Test notes' },
    },
    create: context => ({
      facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'notes', role: 'notes', availability: 'ready',
        revision: 'r1', capabilities: ['project', 'read', 'write'], routeIds: ['read'], actionIds: ['write'] }),
      project: request => ({ fragments: [{ id: 'note', sourceInstanceKey: context.sourceInstanceKey, mode: request.mode, text: 'A note', revision: 'r1' }],
        readGrant: { id: 'grant', sourceInstanceKey: context.sourceInstanceKey, schema: 'test/notes-v1', value: null, revision: 'r1', consistency: 'exact-snapshot' } }),
      query: ({ view, route }) => ({ id: 'evidence', viewId: view.id, routeId: route.id, sourceInstanceKey: context.sourceInstanceKey,
        observedAt: '2026-08-30T00:00:00.000Z', items: [{ id: 'note', text: 'long captured evidence', provenance: { source: 'fixture' } }], truncated: false }),
      mutate: ({ view, offer }) => ({ id: 'receipt', viewId: view.id, offerId: offer.id, sourceInstanceKey: context.sourceInstanceKey,
        status: 'succeeded', completion: 'committed', committedAt: '2026-08-30T00:00:00.000Z' }),
      manage: request => ({ revision: 'r1', value: request.input }),
      ...customize(context),
    }),
  })
  const strategy = defineMemoryStrategy({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'notes-view', packageName: 'test-strategy-notes',
      deterministic: true, supportedSourceRoles: ['notes'], maxSources: 1, maxRoutes: 1, maxActions: 1 },
    compose: (_request, sources) => ({ strategyTypeId: 'notes-view', explanation: 'Test notes', sources: sources.map(item => ({
      sourceInstanceKey: item.sourceInstanceKey, projection: { mode: 'eager', maxCharacters: 100 }, routeIds: item.routeIds, actionIds: item.actionIds,
    })) }),
  })
  const sourcePlugin = { inject: ['mnemonMemory'], apply(ctx: Context) { installMemory(ctx, { sources: [source] }) } }
  const unmount = await runner.mount(sourcePlugin, { instanceId: 'work' })
  await runner.mount({ inject: ['mnemonMemory'], apply(ctx: Context) { installMemory(ctx, { strategies: [strategy] }) } }, { instanceId: 'view' })
  return { runner, unmount, sourcePlugin }
}

describe('independent plugin testing SDK', () => {
  it('composes a read-only combined plugin while private Fiber maintenance remains independent of View execution', async () => {
    const runner = new MemoryCompositionRunner()
    runners.push(runner)
    let refresh!: (value: string) => void
    let watching = false
    const unmount = await runner.mount({ inject: ['mnemonMemory'], apply(ctx: Context) {
      let content = 'original private data'
      ctx.effect(() => {
        watching = true
        refresh = value => { content = value }
        return () => { watching = false }
      })
      installMemory(ctx, {
        sources: [defineMemorySource({
          manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'private-tree', packageName: 'dsh-mnemon-paired',
            role: 'tree', capabilities: ['read'], consistency: 'exact-snapshot',
            routes: [{ id: 'open-node', description: 'Read the pinned private representation', capability: 'read', inputSchema: { type: 'object' }, maxCalls: 3,
            }],
          },
          create: context => ({
            facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'private-tree', role: 'tree', availability: 'ready',
              revision: content, capabilities: ['read'], routeIds: ['open-node'], actionIds: [] }),
            project: () => ({ fragments: [], readGrant: { id: 'pinned-tree', sourceInstanceKey: context.sourceInstanceKey,
              schema: 'private-tree/v1', value: content, revision: content, consistency: 'exact-snapshot' } }),
            query: ({ view, route, grant }) => ({ id: 'node', viewId: view.id, routeId: route.id, sourceInstanceKey: context.sourceInstanceKey,
              observedAt: '2026-08-31T00:00:00.000Z', truncated: false,
              items: [{ id: 'node', text: String(grant.value), provenance: { source: 'private-tree' } }],
            }),
          }),
        })],
        strategies: [defineMemoryStrategy({
          manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'paired', packageName: 'dsh-mnemon-paired',
            deterministic: true, supportedSourceRoles: ['tree'], maxSources: 1, maxRoutes: 1, maxActions: 1 },
          compose: (_request, sources) => ({ strategyTypeId: 'paired', explanation: 'Use my own Source or a compatible replacement.',
            sources: sources.map(source => ({ sourceInstanceKey: source.sourceInstanceKey,
              routeIds: source.routes.filter(route => route.capability === 'read').map(route => route.id),
            })),
          }),
        })],
      })
    } }, { instanceId: 'paired' })
    expect(watching).toBe(true)
    expect(runner.inspect().evaluation).toMatchObject({ state: 'ready', contributionRevision: 1 })
    const pinned = await runner.beginTurn()
    refresh('index refreshed privately')
    const next = await runner.beginTurn()
    const read = (turn: MemoryTestTurn) => turn.executeRoute(turn.view.routes[0]!.id, {})
    expect((await read(pinned)).items[0]!.text).toBe('original private data')
    expect((await read(next)).items[0]!.text).toBe('index refreshed privately')
    await unmount()
    expect(watching).toBe(false)
    await expect(runner.beginTurn()).rejects.toThrow('no Serving')
    // In-use Views retain their Source runtime/grant, not the private watcher Fiber.
    expect((await read(pinned)).items[0]!.text).toBe('original private data')
  })

  it('exposes operations and immutable diagnostics, not engine handles', async () => {
    const { runner } = await fixture()
    const turn = await runner.beginTurn()
    expect(Object.keys(runner)).toEqual(['context'])
    expect('runtime' in runner).toBe(false)
    expect('generations' in runner).toBe(false)
    expect(Object.keys(turn).sort()).toEqual(['executeAction', 'executeRoute', 'release', 'view'])
    expectTypeOf<keyof MemoryTestTurn>().toEqualTypeOf<'view' | 'executeRoute' | 'executeAction' | 'release'>()
    const diagnostics = runner.inspect()
    expect(diagnostics).toMatchObject({ servingGenerationId: turn.view.runtimeGeneration, evaluation: { state: 'ready', sourceInstanceKeys: ['source:work'] } })
    expect(Object.isFrozen(diagnostics.evaluation.sourceInstanceKeys)).toBe(true)
    expect(JSON.parse(JSON.stringify(diagnostics))).toEqual(diagnostics)
    turn.release()
    turn.release()
    await expect(turn.executeRoute(turn.view.routes[0]!.id, {})).rejects.toThrow('released')
    await expect(turn.executeAction(turn.view.actionOffers[0]!.id, {}, () => true)).rejects.toThrow('released')
  })

  it('preserves View identity, input validation, authority denial, cancellation and call budgets', async () => {
    const { runner } = await fixture()
    const turn = await runner.beginTurn()
    const route = turn.view.routes[0]!.id
    const action = turn.view.actionOffers[0]!.id
    await expect(turn.executeAction(action, {}, () => false)).rejects.toThrow('not currently authorized')
    await expect(turn.executeAction(action, 'wrong schema', () => true)).rejects.toThrow('object')
    const aborted = new AbortController()
    aborted.abort(new Error('test cancelled'))
    await expect(turn.executeRoute(route, {}, aborted.signal)).rejects.toThrow('test cancelled')
    await expect(turn.executeRoute('another-source/read', {})).rejects.toThrow('unavailable')
    const evidence = await turn.executeRoute(route, {})
    expect(evidence).toMatchObject({ viewId: turn.view.id, routeId: route, sourceInstanceKey: 'source:work' })
    const receipt = await turn.executeAction(action, {}, () => true)
    expect(receipt).toMatchObject({ viewId: turn.view.id, offerId: action, status: 'succeeded' })
    await turn.executeRoute(route, {})
    await expect(turn.executeRoute(route, {})).rejects.toThrow('budget is exhausted')
  })

  it('retains the requested evidence budget instead of falling back to global defaults', async () => {
    const { runner } = await fixture()
    const budget = { ...DEFAULT_MEMORY_VIEW_BUDGET, maxEvidenceCharacters: 5 }
    const turn = await runner.beginTurn({ budget })
    budget.maxEvidenceCharacters = 1_000
    const evidence = await turn.executeRoute(turn.view.routes[0]!.id, {})
    expect(evidence.items.reduce((count, item) => count + item.text.length, 0)).toBeLessThanOrEqual(5)
    expect(evidence.truncated).toBe(true)
  })

  it('accepts Host defaults and ceilings through public Source descriptors only', async () => {
    const identities: unknown[] = []
    const configuration = vi.fn()
    const { runner } = await fixture({
      sourceConfiguration(source) { identities.push(source); return { label: 'configured' } },
      sourceCapabilities: () => ['project', 'read'],
    }, context => { configuration(context.configuration); return {} })
    expect(identities[0]).toMatchObject({ sourceInstanceKey: 'source:work', manifest: { typeId: 'notes' }, provenance: { entryId: 'work' } })
    expect(Object.keys(identities[0] as object).sort()).toEqual(['manifest', 'provenance', 'sourceInstanceKey'])
    expect(Object.isFrozen(identities[0])).toBe(true)
    expect(configuration).toHaveBeenCalledWith({ label: 'configured' })
    const turn = await runner.beginTurn()
    expect(turn.view.actionOffers).toEqual([])
  })

  it('keeps management scoped and exercises confirmation/revision rejection through its public protocol', async () => {
    const manage = vi.fn<NonNullable<MemorySourceRuntime['manage']>>(request => ({ revision: 'r1', value: request.scope.workspaceId ?? null }))
    const { runner } = await fixture({}, () => ({ manage }))
    const scope = { storage: 'workspace' as const, workspaceId: '/work' }
    const client = await runner.managementClient('source:work', scope)
    scope.workspaceId = '/personal'
    expect((await client.read('read')).value).toBe('/work')
    await expect(runner.executeManagement({ sourceInstanceKey: 'source:work', scope, mode: 'mutate', operation: 'write', input: {}, confirmed: false, expectedRevision: 'r1' })).rejects.toThrow('confirmation')
    await expect(client.mutate('write', {}, { confirmed: true, expectedRevision: 'old' })).rejects.toThrow('revision conflict')
    await expect(client.mutate('write', {}, { confirmed: true })).resolves.toMatchObject({ revision: 'r1' })
    expect(manage).toHaveBeenCalledTimes(2)
  })

  it('holds a separate lease for an in-flight query after turn release and Source unload', async () => {
    const pending = gate()
    const started = gate()
    const disposed = vi.fn()
    const { runner, unmount } = await fixture({}, context => ({
      dispose: disposed,
      query: async ({ view, route }) => {
        started.resolve()
        await pending.promise
        return { id: 'read', viewId: view.id, routeId: route.id, sourceInstanceKey: context.sourceInstanceKey,
          observedAt: '2026-08-30T00:00:00.000Z', items: [], truncated: false }
      },
    }))
    const turn = await runner.beginTurn()
    const operation = turn.executeRoute(turn.view.routes[0]!.id, {})
    await started.promise
    await unmount()
    turn.release()
    expect(runner.inspect().drainingGenerationIds).toEqual([turn.view.runtimeGeneration])
    const closing = runner.dispose()
    try {
      await Promise.resolve()
      expect(disposed).not.toHaveBeenCalled()
      await expect(runner.beginTurn()).rejects.toThrow('disposed')
    } finally { pending.resolve() }
    await operation
    await closing
    expect(disposed).toHaveBeenCalledOnce()
  })

  it('waits for in-flight action authorization even when the test closes its runner', async () => {
    const pending = gate()
    const started = gate()
    const disposed = vi.fn()
    const { runner } = await fixture({}, () => ({ dispose: disposed }))
    const turn = await runner.beginTurn()
    const operation = turn.executeAction(turn.view.actionOffers[0]!.id, {}, async () => {
      started.resolve()
      await pending.promise
      return true
    })
    await started.promise
    const closing = runner.dispose()
    try { expect(disposed).not.toHaveBeenCalled() }
    finally { pending.resolve() }
    await expect(operation).resolves.toMatchObject({ status: 'succeeded' })
    await closing
    expect(disposed).toHaveBeenCalledOnce()
  })

  it('cleans up retained turns and the real Cordis service on idempotent disposal', async () => {
    const disposed = vi.fn()
    const { runner } = await fixture({}, () => ({ dispose: disposed }))
    const service = runner.context.mnemonMemory
    await runner.beginTurn()
    const closing = runner.dispose()
    expect(runner.dispose()).toBe(closing)
    await closing
    expect(disposed).toHaveBeenCalledOnce()
    expect(runner.context.get('mnemonMemory', false)).toBeUndefined()
    expect(() => service.installContributions({}, { instanceId: 'late' })).toThrow('disposed')
    await expect(runner.managementCatalog()).rejects.toThrow('disposed')
  })

  it('releases a pending composition instead of publishing a turn after disposal', async () => {
    const pending = gate()
    const started = gate()
    const disposed = vi.fn()
    const { runner } = await fixture({}, context => ({
      dispose: disposed,
      project: async request => {
        started.resolve()
        await pending.promise
        return { fragments: [], readGrant: { id: 'grant', sourceInstanceKey: context.sourceInstanceKey,
          schema: 'test/notes-v1', value: null, revision: request.expectedRevision, consistency: 'exact-snapshot' } }
      },
    }))
    const composition = expect(runner.beginTurn()).rejects.toThrow('disposed')
    await started.promise
    const closing = runner.dispose()
    try { expect(disposed).not.toHaveBeenCalled() }
    finally { pending.resolve() }
    await composition
    await closing
    expect(disposed).toHaveBeenCalledOnce()
  })

  it('removes a registration left by a failed plugin and permits a clean retry', async () => {
    const { runner, sourcePlugin } = await fixture()
    await expect(runner.mount({ ...sourcePlugin, apply(ctx: Context) {
      sourcePlugin.apply(ctx)
      throw new Error('plugin startup failed')
    } }, { instanceId: 'failed' })).rejects.toThrow('plugin startup failed')
    expect(runner.inspect().evaluation.sourceInstanceKeys).toEqual(['source:work'])
    const unmount = await runner.mount(sourcePlugin, { instanceId: 'failed' })
    await unmount()
    await unmount()
    expect(runner.inspect().evaluation.sourceInstanceKeys).toEqual(['source:work'])
  })
})
