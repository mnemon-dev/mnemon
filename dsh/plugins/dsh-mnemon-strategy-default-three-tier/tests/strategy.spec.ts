import type { Context } from '@deepseek-ai/cordis'
import { describe, expect, it } from 'vitest'
import { defineMemorySource, installMemory, type MemoryAvailableSource } from 'dsh-mnemon/extension-sdk'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { MemoryCompositionRunner, DEFAULT_MEMORY_VIEW_BUDGET } from 'dsh-mnemon/testing'
import * as plugin from '../src/index.ts'

function fact(role: string, key = role): MemoryAvailableSource {
  return {
    sourceInstanceKey: `source:${key}`, sourceTypeId: key, role,
    routes: [], actions: [],
    availability: 'ready', revision: 'r1', capabilities: ['project'], routeIds: [], actionIds: [],
  }
}

describe('standalone default three-tier Strategy', () => {
  it('is deterministic and depends only on facts, not Source implementations', () => {
    const request = { scope: { storage: 'custom' as const }, scenario: 'test', budget: { ...DEFAULT_MEMORY_VIEW_BUDGET } }
    const facts = [fact('working-context'), fact('narrative'), fact('durable-evidence')]
    const first = plugin.DEFAULT_THREE_TIER_VIEW_STRATEGY.compose(request, facts)
    expect(first).toEqual(plugin.DEFAULT_THREE_TIER_VIEW_STRATEGY.compose(request, [...facts].reverse()))
    expect(first.sources.map(source => source.sourceInstanceKey)).toEqual(facts.map(source => source.sourceInstanceKey))
    expect(first.sources.reduce((sum, source) => sum + (source.projection?.maxCharacters ?? 0), 0)).toBeLessThanOrEqual(request.budget.maxProjectionCharacters)
  })

  it('rejects ambiguous roles instead of selecting by mount order', () => {
    expect(() => plugin.DEFAULT_THREE_TIER_VIEW_STRATEGY.compose({
      scope: { storage: 'custom' }, scenario: 'test', budget: { ...DEFAULT_MEMORY_VIEW_BUDGET },
    }, [fact('working-context', 'first'), fact('working-context', 'second')])).toThrow('ambiguous')
  })

  it('mounts and composes through real Cordis and the public test runner', async () => {
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(plugin, { instanceId: 'strategy' })
      expect(runner.inspect().evaluation.state).toBe('incomplete')
      const source = defineMemorySource({
        manifest: {
          apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'fixture',
          packageName: 'test-source-fixture', role: 'working-context', capabilities: ['project'], consistency: 'exact-snapshot',
        },
        create: ctx => ({
          facts: () => ({ ...fact('working-context', 'fixture'), sourceInstanceKey: ctx.sourceInstanceKey }),
          project: request => ({ fragments: [{
            id: 'fixture', sourceInstanceKey: ctx.sourceInstanceKey, mode: request.mode,
            revision: 'r1', text: 'independent fixture',
          }] }),
        }),
      })
      const unmount = await runner.mount({
        inject: ['mnemonMemory'], apply(ctx: Context) { installMemory(ctx, { sources: [source] }) },
      }, { instanceId: 'source' })
      const turn = await runner.beginTurn()
      expect(turn.view.projection[0]?.text).toBe('independent fixture')
      await unmount()
      await expect(runner.beginTurn()).rejects.toThrow('no Serving')
      expect(runner.inspect().drainingGenerationIds).toContain(turn.view.runtimeGeneration)
      turn.release()
    } finally { await runner.dispose() }
  })
})
