import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryStrategy, installMemory } from 'dsh-mnemon/extension-sdk'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'

export const strategy = {
  inject: ['mnemonMemory'],
  apply(ctx: Context) {
    installMemory(ctx, { strategies: [defineMemoryStrategy({
      manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'test',
        packageName: 'test-strategy', deterministic: true, supportedSourceRoles: ['working-context'],
        maxSources: 4, maxRoutes: 4, maxActions: 4 },
      compose: (_request, sources) => ({ strategyTypeId: 'test', explanation: 'Test only.',
        sources: sources.map(source => ({ sourceInstanceKey: source.sourceInstanceKey,
          projection: { mode: 'eager', maxCharacters: 2048 }, routeIds: source.routeIds, actionIds: source.actionIds })) }),
    })] })
  },
}
