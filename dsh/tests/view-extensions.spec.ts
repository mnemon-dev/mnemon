import { describe, expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import { ANY_MEMORY_STRATEGY, COMPOSABLE_MEMORY_API_VERSION, type MemoryStrategyDefinition } from 'dsh-mnemon/contracts'
import { defineMemorySource, defineMemoryStrategy, defineMemoryStrategyExtension, defineMemoryViewExtension, installMemory, memoryViewExtensionValues, validateMemoryViewExtension } from 'dsh-mnemon/extension-sdk'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'

function plugin(contribution: Parameters<typeof installMemory>[1]) {
  return { inject: ['mnemonMemory'], apply(ctx: Context) { installMemory(ctx, contribution) } }
}

const notes = defineMemorySource({
  manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'notes', packageName: 'dsh-mnemon-source-notes',
    role: 'notes', capabilities: ['project'], consistency: 'exact-snapshot' },
  create: context => ({
    facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'notes', role: 'notes', availability: 'ready',
      revision: 'r1', capabilities: ['project'], routeIds: [], actionIds: [] }),
    project: request => ({ fragments: request.includeProjection ? [{ id: 'note', sourceInstanceKey: context.sourceInstanceKey,
      mode: request.mode, text: 'A durable note.'.slice(0, request.maxCharacters), revision: 'r1' }] : [] }),
  }),
})

/** A Strategy that accepts the standard projection slot through the public value contract. */
function strategy(typeId: string, extensionSlots?: string[]): MemoryStrategyDefinition {
  return defineMemoryStrategy({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId, packageName: `dsh-mnemon-strategy-${typeId}`,
      deterministic: true, supportedSourceRoles: ['notes'], maxSources: 4, maxRoutes: 4, maxActions: 4,
      ...(extensionSlots === undefined ? {} : { extensionSlots }) },
    compose(request, sources, contributions = []) {
      const { projection } = memoryViewExtensionValues(contributions)
      const characters = Math.min(request.budget.maxProjectionCharacters, projection?.maxProjectionCharacters ?? Infinity)
      return { strategyTypeId: typeId, explanation: 'Project every note within the standard projection ceiling.',
        sources: sources.map(source => ({ sourceInstanceKey: source.sourceInstanceKey, projection: { mode: 'eager' as const, maxCharacters: characters } })) }
    },
  })
}

const light = (maxProjectionCharacters: number, typeId = 'light') => defineMemoryViewExtension({
  typeId, packageName: `dsh-mnemon-strategy-${typeId}`, slot: 'projection', contribute: () => ({ maxProjectionCharacters }),
})

async function fixture(selected: string) {
  const runner = new MemoryCompositionRunner({ strategyTypeId: selected })
  await runner.mount(plugin({ sources: [notes] }), { instanceId: 'notes' })
  await runner.mount(plugin({ strategies: [strategy('first', ['selection', 'projection', 'capture']), strategy('second')] }), { instanceId: 'strategies' })
  return runner
}

describe('standard View extensions for any main Strategy', () => {
  it('follows whichever selected Strategy declares the standard slot', async () => {
    for (const [selected, text] of [['first', 'A du'], ['second', 'A durable note.']] as const) {
      const runner = await fixture(selected)
      try {
        await runner.mount(plugin({ strategyExtensions: [light(4)] }), { instanceId: 'light' })
        const turn = await runner.beginTurn()
        expect(turn.view.strategyTypeId).toBe(selected)
        expect(turn.view.projection[0]?.text).toBe(text)
        expect(turn.view.strategyExtensions?.map(extension => extension.typeId)).toEqual(selected === 'first' ? ['light'] : undefined)
        if (selected === 'second') {
          expect(runner.inspect().evaluation.diagnostics).toMatchObject([{ code: 'strategy-extension-inactive',
            message: 'Selected Strategy second does not accept the projection slot.' }])
        }
        turn.release()
      } finally { await runner.dispose() }
    }
  })

  it('rejects a second owner of a slot at mount, whichever main Strategy it targets', async () => {
    const runner = await fixture('first')
    try {
      await runner.mount(plugin({ strategyExtensions: [light(4)] }), { instanceId: 'light' })
      const serving = runner.inspect().servingGenerationId
      // The any-Strategy extension would meet either one after a main Strategy switch.
      for (const strategyTypeId of ['first', 'second']) {
        const targeted = defineMemoryStrategyExtension({
          manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy-extension', typeId: 'narrow', packageName: 'dsh-mnemon-strategy-narrow',
            strategyTypeId, slot: 'projection', deterministic: true },
          contribute: () => ({ maxProjectionCharacters: 2 }),
        })
        await expect(runner.mount(plugin({ strategyExtensions: [targeted] }), { instanceId: 'narrow' })).rejects.toThrow(`slot conflict: ${strategyTypeId}/projection`)
      }
      expect(runner.inspect()).toMatchObject({ servingGenerationId: serving, evaluation: { state: 'ready' } })
    } finally { await runner.dispose() }
  })

  it('keeps any-Strategy extensions on standard slots and validates their values', () => {
    expect(() => defineMemoryStrategyExtension({
      manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy-extension', typeId: 'custom', packageName: 'dsh-mnemon-strategy-custom',
        strategyTypeId: ANY_MEMORY_STRATEGY, slot: 'guidance', deterministic: true },
      contribute: () => null,
    })).toThrow('must use a standard slot: guidance')
    expect(light(8).manifest).toMatchObject({ strategyTypeId: '*', slot: 'projection' })
    expect(() => validateMemoryViewExtension('projection', { maxProjectionCharacters: 8, extra: true })).toThrow('unsupported View projection field: extra')
    expect(() => validateMemoryViewExtension('selection', { sourceKeys: ['source:a'], writableSourceKeys: ['source:b'] })).toThrow('within selected sourceKeys')
    expect(() => validateMemoryViewExtension('capture', { instruction: 'Record.', actionIds: [] })).toThrow('capture actionIds')
    const contribution = { instanceKey: 'strategy-extension:light', typeId: 'light', slot: 'projection', value: { maxProjectionCharacters: 8 } }
    expect(memoryViewExtensionValues([contribution])).toEqual({ projection: { maxProjectionCharacters: 8 } })
    expect(() => memoryViewExtensionValues([contribution, contribution])).toThrow('duplicate View extension slot: projection')
  })

  it('falls back to the only installed Strategy only when the Host asks for it', async () => {
    for (const strategyFallback of [undefined, 'sole-strategy'] as const) {
      const runner = new MemoryCompositionRunner({ strategyTypeId: 'first', ...(strategyFallback === undefined ? {} : { strategyFallback }) })
      try {
        await runner.mount(plugin({ sources: [notes] }), { instanceId: 'notes' })
        await runner.mount(plugin({ strategies: [strategy('second')] }), { instanceId: 'second' })
        if (strategyFallback === undefined) {
          expect(runner.inspect().evaluation).toMatchObject({ state: 'rejected', diagnostics: [{ message: expect.stringContaining('selected memory Strategy type is unavailable: first') }] })
          continue
        }
        expect(runner.inspect().evaluation.diagnostics).toEqual([{ code: 'strategy-fallback', contributionInstanceKey: 'strategy:second',
          message: 'Selected Strategy first is unavailable; composing with the only installed Strategy second.' }])
        const turn = await runner.beginTurn()
        expect(turn.view.strategyTypeId).toBe('second')
        turn.release()
      } finally { await runner.dispose() }
    }
  })

  it('runs an any-Strategy extension only for the selected Strategy', async () => {
    const contribute = vi.fn(() => ({ maxProjectionCharacters: 4 }))
    const runner = await fixture('second')
    try {
      await runner.mount(plugin({ strategyExtensions: [defineMemoryViewExtension({ typeId: 'watched', packageName: 'dsh-mnemon-strategy-watched', slot: 'projection', contribute })] }), { instanceId: 'watched' })
      const turn = await runner.beginTurn()
      expect(contribute).not.toHaveBeenCalled()
      turn.release()
    } finally { await runner.dispose() }
  })
})
