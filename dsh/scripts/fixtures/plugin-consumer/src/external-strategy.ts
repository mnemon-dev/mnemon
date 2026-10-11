import type { Context } from '@deepseek-ai/cordis'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { defineMemoryPlugin, defineMemoryStrategy, installMemory } from 'dsh-mnemon/extension-sdk'

export const name = 'dsh-mnemon-strategy-external-focus'
export const inject = ['mnemonMemory']
export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'External focus', 'zh-CN': '外部聚焦' },
  description: { en: 'Select explicit Source instances for one View.', 'zh-CN': '为一个 View 选择明确的 Source 实例。' },
  roles: ['strategy'],
  provides: [{ id: 'strategy' }, { id: 'strategy.external-focus' }],
  requires: ['source'],
})
export interface Config { sourceKeys: string[]; mode: 'eager' | 'routed' }

/** A Strategy knows Source facts and instance identities, never their implementations. */
export function apply(ctx: Context, config: Config): void {
  const selected = new Set(config.sourceKeys)
  const mode = config.mode
  installMemory(ctx, { plugin: memoryPlugin, strategies: [defineMemoryStrategy({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'external-focus', packageName: name,
      deterministic: true, supportedSourceRoles: ['notes', 'working-context', 'narrative', 'durable-evidence'],
      maxSources: 8, maxRoutes: 16, maxActions: 16 },
    compose(request, facts) {
      const chosen = facts.filter(fact => selected.has(fact.sourceInstanceKey) && fact.availability !== 'unavailable')
        .sort((left, right) => left.sourceInstanceKey.localeCompare(right.sourceInstanceKey))
      return { strategyTypeId: 'external-focus', explanation: 'Only explicitly selected Source instances enter this View.',
        sources: chosen.map(source => ({ sourceInstanceKey: source.sourceInstanceKey,
          projection: { mode, maxCharacters: Math.floor(request.budget.maxProjectionCharacters / Math.max(1, chosen.length)) },
          routeIds: source.routeIds, actionIds: source.actionIds })) }
    },
  })] })
}
