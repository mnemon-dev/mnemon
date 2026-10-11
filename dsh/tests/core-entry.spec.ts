import { Context } from '@deepseek-ai/cordis'
import { describe, expect, it, vi } from 'vitest'
import * as core from "../src/core/plugin.ts"
import { COMPOSABLE_MEMORY_API_VERSION } from "../src/core/contracts/index.ts"
import { defineMemorySource, defineMemoryStrategy, installMemory } from "../src/sdk/index.ts"
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'

describe('Source-neutral Core entry', () => {
  it('mounts on plain Cordis without a DSH default service or built-in contribution', async () => {
    const ctx = new Context()
    const fiber = ctx.plugin(core)
    await fiber.await()
    const service = ctx.mnemonMemory
    expect(core.inject).toEqual([])
    expect(Object.keys(core).sort()).toEqual(['apply', 'inject', 'name', 'provide'])
    expect(Object.keys(service)).toEqual(['installContributions'])
    expect(Object.isFrozen(service)).toBe(true)
    await fiber.dispose()
    expect(() => service.installContributions({}, { instanceId: 'closed' })).toThrow('disposed')
    expect(ctx.get('mnemonMemory', false)).toBeUndefined()
  })

  it('supports third-party Source/Strategy turns without the default bundle and owns shutdown', async () => {
    const runner = new MemoryCompositionRunner()
    const disposed = vi.fn()
    const source = defineMemorySource({
      manifest: {
        apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'external-notes',
        packageName: 'dsh-mnemon-external-notes', role: 'notes', capabilities: ['project'], consistency: 'exact-snapshot',
      },
      create: context => ({
        facts: () => ({
          sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'external-notes', role: 'notes',
          availability: 'ready', revision: 'r1', capabilities: ['project'], routeIds: [], actionIds: [],
        }),
        project: request => ({ fragments: [{
          id: 'note', sourceInstanceKey: context.sourceInstanceKey, mode: request.mode,
          text: 'Only external notes', revision: 'r1',
        }] }),
        dispose: disposed,
      }),
    })
    const strategy = defineMemoryStrategy({
      manifest: {
        apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'notes-view', packageName: 'dsh-mnemon-external-notes-view',
        deterministic: true, supportedSourceRoles: ['notes'], maxSources: 1, maxRoutes: 1, maxActions: 1,
      },
      compose: (_request, facts) => ({ strategyTypeId: 'notes-view', explanation: 'Only selected notes', sources: facts.map(fact => ({
        sourceInstanceKey: fact.sourceInstanceKey, projection: { mode: 'eager', maxCharacters: 1_000 }, routes: [], actions: [],
      })) }),
    })
    try {
      await runner.mount({ inject: ['mnemonMemory'], apply(child: Context) {
        installMemory(child, { sources: [source] })
      } }, { instanceId: 'notes' })
      await runner.mount({ inject: ['mnemonMemory'], apply(child: Context) {
        installMemory(child, { strategies: [strategy] })
      } }, { instanceId: 'view' })
      const turn = await runner.beginTurn()
      try { expect(turn.view.projection.map(fragment => fragment.text)).toEqual(['Only external notes']) }
      finally { turn.release() }
    } finally { await runner.dispose() }
    expect(disposed).toHaveBeenCalledOnce()
  })
})
