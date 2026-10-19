import type { Context } from '@deepseek-ai/cordis'
import { describe, expect, it, vi } from 'vitest'
import { defineMemorySource, installMemory, createMemoryMutationReceipt } from 'dsh-mnemon/extension-sdk'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as base from 'dsh-mnemon-strategy-default-three-tier'
import * as plugin from '../src/index.ts'

async function fixture(role = 'working-context', write = vi.fn()) {
  const runner = new MemoryCompositionRunner()
  const source = { inject: ['mnemonMemory'], apply(ctx: Context) {
    installMemory(ctx, { sources: [defineMemorySource({
      manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'notes',
        packageName: 'external-test-notes', role, capabilities: ['project', 'write'], consistency: 'exact-snapshot',
        actions: [{ id: 'append', description: 'Append one note.', capability: 'write', inputSchema: { type: 'object' } }] },
      create: context => ({
        facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'notes', role, availability: 'ready',
          revision: 'r1', capabilities: ['project', 'write'], routeIds: [], actionIds: ['append'] }),
        project: request => ({ fragments: request.includeProjection ? [{ id: 'note', sourceInstanceKey: context.sourceInstanceKey,
          mode: request.mode, revision: 'r1', text: 'A'.repeat(Math.min(100, request.maxCharacters)) }] : [] }),
        mutate: async request => { write(request.input); return createMemoryMutationReceipt(request.view.id, request.offer.id, context.sourceInstanceKey, 'r2', request.input, 'committed') },
      }),
    })] })
  } }
  await runner.mount(source, { instanceId: 'notes' })
  await runner.mount(base, { instanceId: 'base' })
  return { runner, source, write }
}

describe('independent light-context Strategy extension', () => {
  it('caps the shared projection and restores the baseline on unload', async () => {
    const { runner } = await fixture()
    try {
      const before = await runner.beginTurn()
      const unmount = await runner.mount(plugin, { instanceId: 'light', config: { maxProjectionCharacters: 40 } })
      const light = await runner.beginTurn()
      expect(light.view.projection.reduce((sum, item) => sum + item.text.length, 0)).toBeLessThanOrEqual(40)
      expect(light.view.actionOffers).toEqual(before.view.actionOffers)
      expect(light.view.strategyTypeId).toBe(before.view.strategyTypeId)
      await unmount()
      const restored = await runner.beginTurn()
      expect(restored.view.projection).toEqual(before.view.projection)
      before.release(); light.release(); restored.release()
    } finally { await runner.dispose() }
  })

  it('has a bounded no-configuration default', () => {
    const extension = plugin.createLightContextExtension()
    expect(extension.manifest.slot).toBe('projection')
    expect(extension.contribute({ scope: { storage: 'custom' }, scenario: 'test', budget: {} as never }, [])).toEqual({ maxProjectionCharacters: 4096 })
  })

  it('rejects nonfinite, fractional and empty budgets', () => {
    for (const maxProjectionCharacters of [NaN, Infinity, -1, 0, 1.5]) expect(() => plugin.createLightContextExtension({ maxProjectionCharacters })).toThrow()
  })
})
