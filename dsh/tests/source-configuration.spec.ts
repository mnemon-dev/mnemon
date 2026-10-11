import type { Context } from '@deepseek-ai/cordis'
import { describe, expect, it } from 'vitest'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { defineMemorySource, defineMemoryStrategy, installMemory } from 'dsh-mnemon/extension-sdk'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'

describe('generation-owned Source configuration', () => {
  it('captures JSON defaults, digests changes and preserves a pinned generation', async () => {
    const defaults = { label: 'before', nested: { limit: 3 } }
    const captured: unknown[] = []
    const runner = new MemoryCompositionRunner({ sourceConfiguration: () => defaults })
    try {
      const sourcePlugin = { inject: ['mnemonMemory'], apply(ctx: Context) {
        installMemory(ctx, { sources: [defineMemorySource({
          manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'config', packageName: 'test-source-config', role: 'test', capabilities: ['project'], consistency: 'exact-snapshot' },
          create(context) {
            const configuration = context.configuration!
            captured.push(configuration)
            return {
              facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'config', role: 'test', availability: 'ready', revision: String(configuration.label), capabilities: ['project'], routeIds: [], actionIds: [] }),
              project: request => ({ fragments: [{ id: 'label', sourceInstanceKey: context.sourceInstanceKey, mode: request.mode, text: String(configuration.label), revision: String(configuration.label) }] }),
            }
          },
        })] })
      } }
      const unmountSource = await runner.mount(sourcePlugin, { instanceId: 'source' })
      await runner.mount({ inject: ['mnemonMemory'], apply(ctx: Context) {
        installMemory(ctx, { strategies: [defineMemoryStrategy({
          manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'test', packageName: 'test-strategy', deterministic: true, supportedSourceRoles: ['test'], maxSources: 1, maxRoutes: 1, maxActions: 1 },
          compose: (_request, sources) => ({ strategyTypeId: 'test', sources: sources.map(source => ({ sourceInstanceKey: source.sourceInstanceKey, projection: { mode: 'eager', maxCharacters: 100 } })), explanation: 'Test only.' }),
        })] })
      } }, { instanceId: 'strategy' })
      const pinned = await runner.beginTurn()
      expect(pinned.view.projection[0]?.text).toBe('before')
      expect(Object.isFrozen(captured[0])).toBe(true)
      expect(Object.isFrozen((captured[0] as typeof defaults).nested)).toBe(true)
      defaults.label = 'after'
      await unmountSource()
      await runner.mount(sourcePlugin, { instanceId: 'source' })
      const next = await runner.beginTurn()
      expect(next.view.projection[0]?.text).toBe('after')
      expect(next.view.runtimeGeneration).not.toBe(pinned.view.runtimeGeneration)
      expect(pinned.view.projection[0]?.text).toBe('before')
      expect(runner.inspect().drainingGenerationIds).toContain(pinned.view.runtimeGeneration)
      pinned.release()
      next.release()
    } finally { await runner.dispose() }
  })
})
