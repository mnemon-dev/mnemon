import { describe, expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import { COMPOSABLE_MEMORY_API_VERSION, type MemoryJsonValue, type MemorySourceDefinition, type MemoryStrategyDefinition, type MemoryStrategyExtensionDefinition } from 'dsh-mnemon/contracts'
import { defineMemorySource, defineMemoryStrategy, defineMemoryStrategyExtension, installMemory, createMemoryMutationReceipt } from 'dsh-mnemon/extension-sdk'
import { DEFAULT_MEMORY_VIEW_BUDGET, MemoryCompositionRunner } from 'dsh-mnemon/testing'

function plugin(contribution: Parameters<typeof installMemory>[1]) {
  return { inject: ['mnemonMemory'], apply(ctx: Context) { installMemory(ctx, contribution) } }
}

function source(onWrite = vi.fn(), failFactory = () => false): MemorySourceDefinition {
  return defineMemorySource({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'notes', packageName: 'dsh-mnemon-source-notes',
      role: 'notes', capabilities: ['project', 'write'], consistency: 'exact-snapshot',
      actions: [{ id: 'append', capability: 'write', description: 'Append a note.', inputSchema: { type: 'string' } }] },
    create(context) {
      if (failFactory()) throw new Error('factory unavailable')
      return {
        facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'notes', role: 'notes', availability: 'ready',
          revision: 'r1', capabilities: ['project', 'write'], routeIds: [], actionIds: ['append'] }),
        project: request => ({ fragments: request.includeProjection ? [{ id: 'note', sourceInstanceKey: context.sourceInstanceKey,
          mode: request.mode, text: 'A durable note.'.slice(0, request.maxCharacters), revision: 'r1' }] : [] }),
        mutate: async request => {
          onWrite(request.input)
          return createMemoryMutationReceipt(request.view.id, request.offer.id, context.sourceInstanceKey, 'r2', request.input, 'committed')
        },
      }
    },
  })
}

const base: MemoryStrategyDefinition = defineMemoryStrategy({
  manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'base', packageName: 'dsh-mnemon-strategy-base',
    deterministic: true, supportedSourceRoles: ['notes'], maxSources: 10, maxRoutes: 10, maxActions: 10,
    extensionSlots: ['selection', 'projection', 'guidance'] },
  compose(request, sources, contributions = []) {
    const selection = contributions.find(item => item.slot === 'selection')?.value
    const selected = sources.filter(item => !Array.isArray(selection) || selection.includes(item.sourceInstanceKey))
    const projection = contributions.find(item => item.slot === 'projection')?.value
    const guidance = contributions.find(item => item.slot === 'guidance')?.value
    const characters = Math.floor(Math.min(request.budget.maxProjectionCharacters, typeof projection === 'number' ? projection : Infinity) / Math.max(1, selected.length))
    return { strategyTypeId: 'base', explanation: 'One View from independent, Strategy-owned slots.',
      sources: selected.map(item => ({ sourceInstanceKey: item.sourceInstanceKey,
        ...(characters > 0 ? { projection: { mode: 'eager' as const, maxCharacters: characters } } : {}), actionIds: item.actionIds })),
      ...(typeof guidance === 'string' ? { guidance: { routing: guidance } } : {}),
    }
  },
})

function extension(typeId: string, slot: string, value: MemoryJsonValue | (() => MemoryJsonValue), target = 'base'): MemoryStrategyExtensionDefinition {
  return defineMemoryStrategyExtension({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy-extension', typeId,
      packageName: `dsh-mnemon-strategy-${typeId}`, strategyTypeId: target, slot, deterministic: true },
    contribute: typeof value === 'function' ? value : () => value,
  })
}

async function fixture(options: ConstructorParameters<typeof MemoryCompositionRunner>[0] = {}, definition = source()) {
  const runner = new MemoryCompositionRunner({ strategyTypeId: 'base', ...options })
  await runner.mount(plugin({ sources: [definition] }), { instanceId: 'notes' })
  await runner.mount(plugin({ strategies: [base] }), { instanceId: 'base' })
  return runner
}

describe('additive Strategy plugins through the public Cordis SDK', () => {
  it('enables three independent contributions without changing the selected Strategy and removes only its own effect', async () => {
    const write = vi.fn()
    const runner = await fixture({}, source(write))
    try {
      const baseline = await runner.beginTurn()
      await runner.mount(plugin({ strategyExtensions: [extension('scope', 'selection', ['source:notes'])] }), { instanceId: 'scope' })
      const unloadLight = await runner.mount(plugin({ strategyExtensions: [extension('light', 'projection', 4)] }), { instanceId: 'light' })
      await runner.mount(plugin({ strategyExtensions: [extension('capture', 'guidance', 'Capture qualified facts through authorized actions.')] }), { instanceId: 'capture' })
      const composed = await runner.beginTurn()
      expect(composed.view.strategyTypeId).toBe('base')
      expect(composed.view.strategyExtensions).toHaveLength(3)
      expect(composed.view.projection[0]?.text).toBe('A du')
      expect(composed.view.guidance?.routing).toContain('Capture qualified')
      expect(write).not.toHaveBeenCalled()
      await unloadLight()
      const after = await runner.beginTurn()
      expect(after.view.strategyExtensions?.map(item => item.slot).sort()).toEqual(['guidance', 'selection'])
      expect(after.view.projection).toEqual(baseline.view.projection)
      expect(after.view.guidance).toEqual(composed.view.guidance)
      expect(composed.view.projection[0]?.text).toBe('A du')
      expect(runner.inspect().drainingGenerationIds).toContain(composed.view.runtimeGeneration)
      composed.release()
      after.release()
      baseline.release()
    } finally { await runner.dispose() }
  })

  it('produces the same View regardless of plugin installation order', async () => {
    const values = [extension('scope', 'selection', ['source:notes']), extension('light', 'projection', 8), extension('capture', 'guidance', 'Capture.')]
    async function view(order: number[]) {
      const runner = await fixture()
      try {
        for (const index of order) await runner.mount(plugin({ strategyExtensions: [values[index]!] }), { instanceId: `policy-${index}` })
        const turn = await runner.beginTurn()
        const { createdAt: _, ...result } = turn.view
        turn.release()
        return result
      } finally { await runner.dispose() }
    }
    expect(await view([0, 1, 2])).toEqual(await view([2, 0, 1]))
  })

  it('accepts contributions mounted before their owning Strategy', async () => {
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(plugin({ strategyExtensions: [extension('light', 'projection', 3)] }), { instanceId: 'light' })
      expect(runner.inspect().evaluation.state).toBe('incomplete')
      await runner.mount(plugin({ sources: [source()], strategies: [base] }), { instanceId: 'product' })
      const turn = await runner.beginTurn()
      expect(turn.view.projection[0]?.text).toBe('A d')
      turn.release()
    } finally { await runner.dispose() }
  })

  it('rejects a colliding batch atomically and leaves the active plugin and Serving generation intact', async () => {
    const runner = await fixture()
    try {
      await runner.mount(plugin({ strategyExtensions: [extension('first', 'projection', 5)] }), { instanceId: 'first' })
      const serving = runner.inspect().servingGenerationId
      await expect(runner.mount(plugin({ strategyExtensions: [extension('second', 'projection', 2), extension('also', 'guidance', 'Must not leak.')] }), { instanceId: 'collision' })).rejects.toThrow('slot conflict')
      expect(runner.inspect().servingGenerationId).toBe(serving)
      const turn = await runner.beginTurn()
      expect(turn.view.strategyExtensions?.map(item => item.typeId)).toEqual(['first'])
      expect(turn.view.guidance).toBeUndefined()
      turn.release()
    } finally { await runner.dispose() }
  })

  it('reports unsupported slots without replacing the previously valid generation', async () => {
    const runner = await fixture()
    try {
      const serving = runner.inspect().servingGenerationId
      const unload = await runner.mount(plugin({ strategyExtensions: [extension('unknown', 'unrecognized', null)] }), { instanceId: 'unknown' })
      expect(runner.inspect()).toMatchObject({ servingGenerationId: serving, evaluation: { state: 'rejected', diagnostics: [{ message: expect.stringContaining('does not support extension slot') }] } })
      await unload()
      expect(runner.inspect().evaluation.state).toBe('ready')
    } finally { await runner.dispose() }
  })

  it('does not run or silently retarget contributions intended for a different complete Strategy', async () => {
    const contribute = vi.fn(() => 'Not active here.')
    const runner = await fixture()
    try {
      await runner.mount(plugin({ strategyExtensions: [extension('other', 'guidance', contribute, 'another')] }), { instanceId: 'other' })
      expect(runner.inspect().evaluation.diagnostics).toMatchObject([{ code: 'strategy-extension-inactive', contributionInstanceKey: 'strategy-extension:other' }])
      const turn = await runner.beginTurn()
      expect(turn.view.strategyExtensions).toBeUndefined()
      expect(contribute).not.toHaveBeenCalled()
      turn.release()
    } finally { await runner.dispose() }
  })

  it('rejects non-deterministic and non-JSON contributions before projecting a View', async () => {
    const runner = await fixture()
    let count = 0
    try {
      const unload = await runner.mount(plugin({ strategyExtensions: [extension('random', 'projection', () => ++count)] }), { instanceId: 'random' })
      await expect(runner.beginTurn()).rejects.toThrow('not deterministic')
      await unload()
      await runner.mount(plugin({ strategyExtensions: [extension('invalid', 'projection', () => new Date() as never)] }), { instanceId: 'invalid' })
      await expect(runner.beginTurn()).rejects.toThrow('non-JSON object')
    } finally { await runner.dispose() }
  })

  it('keeps one projection budget across multiple Sources', async () => {
    const runner = await fixture()
    try {
      await runner.mount(plugin({ sources: [source()] }), { instanceId: 'second' })
      await runner.mount(plugin({ strategyExtensions: [extension('light', 'projection', 100)] }), { instanceId: 'light' })
      const turn = await runner.beginTurn({ budget: { ...DEFAULT_MEMORY_VIEW_BUDGET, maxProjectionCharacters: 7 } })
      expect(turn.view.projection).toHaveLength(2)
      expect(turn.view.projection.reduce((sum, item) => sum + item.text.length, 0)).toBe(6)
      turn.release()
    } finally { await runner.dispose() }
  })

  it('cannot grant writes through guidance and still requires actual Host authorization', async () => {
    const write = vi.fn()
    const runner = await fixture({}, source(write))
    const readonly = await fixture({ sourceCapabilities: () => ['project'] }, source(write))
    try {
      for (const target of [runner, readonly]) await target.mount(plugin({ strategyExtensions: [extension('capture', 'guidance', 'Write a note.')] }), { instanceId: 'capture' })
      const turn = await runner.beginTurn()
      const readOnlyTurn = await readonly.beginTurn()
      expect(readOnlyTurn.view.actionOffers).toEqual([])
      await expect(turn.executeAction(turn.view.actionOffers[0]!.id, 'new fact', () => false)).rejects.toThrow()
      expect(write).not.toHaveBeenCalled()
      expect((await turn.executeAction(turn.view.actionOffers[0]!.id, 'new fact', () => true)).completion).toBe('committed')
      expect(write).toHaveBeenCalledExactlyOnceWith('new fact')
      turn.release()
      readOnlyTurn.release()
    } finally { await runner.dispose(); await readonly.dispose() }
  })

  it('does not revive a removed policy if the replacement Source generation cannot start', async () => {
    let fail = false
    const runner = await fixture({}, source(vi.fn(), () => fail))
    try {
      const unload = await runner.mount(plugin({ strategyExtensions: [extension('light', 'projection', 3)] }), { instanceId: 'light' })
      const pinned = await runner.beginTurn()
      fail = true
      await unload()
      expect(runner.inspect().servingGenerationId).toBeUndefined()
      await expect(runner.beginTurn()).rejects.toThrow('no Serving')
      expect(pinned.view.strategyExtensions).toHaveLength(1)
      pinned.release()
    } finally { await runner.dispose() }
  })

  it('rejects oversized contributions and releases failed composition leases', async () => {
    const runner = await fixture()
    try {
      await runner.mount(plugin({ strategyExtensions: [extension('large', 'guidance', 'x'.repeat(64_001))] }), { instanceId: 'large' })
      await expect(runner.beginTurn()).rejects.toThrow('exceeds 64000')
      expect(runner.inspect().drainingGenerationIds).toEqual([])
    } finally { await runner.dispose() }
  })

  it('passes immutable, permission-filtered facts instead of Source objects to extensions', async () => {
    const runner = await fixture({ sourceCapabilities: () => ['project'] })
    const contribute = vi.fn((request, facts) => {
      expect(Object.isFrozen(request)).toBe(true)
      expect(Object.isFrozen(facts)).toBe(true)
      expect(facts[0].actionIds).toEqual([])
      expect(facts[0].actions).toEqual([])
      expect('create' in facts[0]).toBe(false)
      expect('mutate' in facts[0]).toBe(false)
      return 6
    })
    try {
      const definition = defineMemoryStrategyExtension({ ...extension('bounded', 'projection', 6), contribute })
      await runner.mount(plugin({ strategyExtensions: [definition] }), { instanceId: 'bounded' })
      const turn = await runner.beginTurn()
      expect(contribute).toHaveBeenCalledTimes(2)
      turn.release()
    } finally { await runner.dispose() }
  })

  it('captures returned values before replay so a reused mutable result cannot hide nondeterminism', async () => {
    const runner = await fixture()
    const value: MemoryJsonValue[] = []
    try {
      await runner.mount(plugin({ strategyExtensions: [extension('mutable', 'selection', () => { value.push('source:notes'); return value })] }), { instanceId: 'mutable' })
      await expect(runner.beginTurn()).rejects.toThrow('not deterministic')
    } finally { await runner.dispose() }
  })
})
