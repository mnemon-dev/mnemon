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

describe('independent in-turn auto-capture Strategy extension', () => {
  it('adds instructions automatically but performs no writes during composition', async () => {
    const { runner, write } = await fixture('durable-evidence')
    try {
      const before = await runner.beginTurn()
      await runner.mount(plugin, { instanceId: 'capture', config: { actionIds: ['append'] } })
      const turn = await runner.beginTurn()
      expect(turn.view.guidance?.system).toContain('MNEMON OPTIONAL AUTO CAPTURE')
      expect(turn.view.guidance?.system).toContain('source:notes')
      expect(turn.view.projection).toEqual(before.view.projection)
      expect(turn.view.actionOffers).toEqual(before.view.actionOffers)
      expect(write).not.toHaveBeenCalled()
      await expect(turn.executeAction(turn.view.actionOffers[0]!.id, { content: 'qualified fact' }, () => false)).rejects.toThrow()
      expect(write).not.toHaveBeenCalled()
      const receipt = await turn.executeAction(turn.view.actionOffers[0]!.id, { content: 'qualified fact' }, () => true)
      expect(receipt.completion).toBe('committed')
      expect(write).toHaveBeenCalledExactlyOnceWith({ content: 'qualified fact' })
      before.release(); turn.release()
    } finally { await runner.dispose() }
  })

  it('refuses activation when its durable Source dependency is absent', async () => {
    const { runner } = await fixture()
    try {
      await runner.mount(plugin, { instanceId: 'capture' })
      expect(runner.inspect().evaluation).toMatchObject({ state: 'rejected', diagnostics: [{ message: expect.stringContaining('source.durable-evidence') }] })
      const turn = await runner.beginTurn()
      expect(turn.view.strategyExtensions).toBeUndefined()
      turn.release()
    } finally { await runner.dispose() }
  })

  it('validates instruction and target configuration before installation', () => {
    expect(() => plugin.createAutoCaptureExtension({ instruction: '' })).toThrow()
    expect(() => plugin.createAutoCaptureExtension({ sourceKeys: ['unsafe-key'] })).toThrow()
  })
})
