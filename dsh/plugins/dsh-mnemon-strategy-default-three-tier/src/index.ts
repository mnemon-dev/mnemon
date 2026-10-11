import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryPlugin, installMemory, defineMemoryStrategyConfiguration } from 'dsh-mnemon/extension-sdk'
import { DEFAULT_THREE_TIER_VIEW_STRATEGY } from './strategy.ts'

export const name = 'dsh-mnemon-strategy-default-three-tier'
export const inject = ['mnemonMemory']

export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'Layered strategy', 'zh-CN': '分层策略' },
  description: { en: 'Resident runtime memory, on-demand documents and durable memory.', 'zh-CN': '运行时记忆常驻，档案与长期记忆按需读取。' },
  roles: ['strategy'],
  provides: [{ id: 'strategy' }, { id: 'strategy.default-three-tier' }],
  requires: ['source'],
})

export const memoryStrategyConfiguration = defineMemoryStrategyConfiguration({
  kind: 'strategy', typeId: 'default-three-tier',
  label: memoryPlugin.label,
  description: { en: 'Compose runtime context, documents and durable memory.', 'zh-CN': '组合运行时、档案与长期记忆。' },
  fields: [], create: () => ({ plugin: memoryPlugin, strategies: [DEFAULT_THREE_TIER_VIEW_STRATEGY] }),
})

export function apply(ctx: Context): void {
  installMemory(ctx, memoryStrategyConfiguration.create({}))
}

export { DEFAULT_THREE_TIER_VIEW_STRATEGY }
