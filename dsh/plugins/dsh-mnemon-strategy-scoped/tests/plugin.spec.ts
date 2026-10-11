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

describe('independent scoped Strategy extension', () => {
  it('selects multiple instances automatically without replacing the base Strategy', async () => {
    const { runner, source } = await fixture()
    try {
      await runner.mount(source, { instanceId: 'other' })
      await expect(runner.beginTurn()).rejects.toThrow('ambiguous')
      await runner.mount(plugin, { instanceId: 'scoped' })
      const turn = await runner.beginTurn()
      expect(turn.view.strategyTypeId).toBe('default-three-tier')
      expect(turn.view.projection.map(item => item.sourceInstanceKey)).toEqual(['source:notes', 'source:other'])
      expect(turn.view.strategyExtensions?.map(item => item.slot)).toEqual(['selection'])
      turn.release()
    } finally { await runner.dispose() }
  })

  it('respects explicit order and narrows writes independently of reading', async () => {
    const { runner, source } = await fixture()
    try {
      await runner.mount(source, { instanceId: 'other' })
      await runner.mount(plugin, { instanceId: 'scoped', config: { sourceKeys: ['source:other', 'source:notes'], writableSourceKeys: ['source:notes'] } })
      const turn = await runner.beginTurn()
      expect(turn.view.projection.map(item => item.sourceInstanceKey)).toEqual(['source:other', 'source:notes'])
      expect(turn.view.actionOffers.map(item => item.sourceInstanceKey)).toEqual(['source:notes'])
      turn.release()
    } finally { await runner.dispose() }
  })

  it('rejects invalid configuration instead of widening the selection', () => {
    expect(() => plugin.createScopedExtension({ sourceKeys: ['notes'] })).toThrow('exact Source')
    expect(() => plugin.createScopedExtension({ sourceKeys: ['source:notes', 'source:notes'] })).toThrow('duplicate')
    expect(() => plugin.createScopedExtension({ sourceKeys: ['source:notes'], writableSourceKeys: ['source:other'] })).toThrow('within selected')
  })
})
