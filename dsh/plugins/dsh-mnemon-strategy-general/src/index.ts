import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryPlugin, defineMemoryStrategyConfiguration, installMemory } from 'dsh-mnemon/extension-sdk'
import { createGeneralStrategy, GENERAL_STRATEGY_TYPE_ID, GENERAL_VIEW_STRATEGY, type GeneralStrategyConfig } from './strategy.ts'

export type Config = GeneralStrategyConfig
export const name = 'dsh-mnemon-strategy-general'
export const inject = ['mnemonMemory']

export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'General strategy', 'zh-CN': '通用策略' },
  description: { en: 'Every source available; the model decides how to use each one.', 'zh-CN': '全部来源可用，由模型决定如何使用每一个。' },
  roles: ['strategy'],
  provides: [{ id: 'strategy' }, { id: 'strategy.general' }],
  requires: ['source'],
})

export const memoryStrategyConfiguration = defineMemoryStrategyConfiguration({
  kind: 'strategy', typeId: GENERAL_STRATEGY_TYPE_ID,
  label: memoryPlugin.label,
  description: {
    en: 'Model-driven use of every admitted Source. No automatic background review or Runtime capacity maintenance.',
    'zh-CN': '由模型自主使用每个已接入的 Source；不启用自动后台审查或运行时容量维护。',
  },
  fields: [
    { key: 'residentSourceKeys', input: 'source-list',
      label: { en: 'Resident Sources', 'zh-CN': '常驻 Source' },
      description: { en: 'Projected into every turn. Unset keeps working-context Sources resident; the others stay on demand.', 'zh-CN': '每轮直接投影到上下文。未设置时常驻工作上下文类 Source，其余按需读取。' } },
    { key: 'instruction', input: 'textarea', maximum: 4000,
      label: { en: 'Additional guidance', 'zh-CN': '附加指引' },
      description: { en: 'Optional guidance appended to the memory protocol for every turn.', 'zh-CN': '可选，每轮追加到记忆协议的指引。' } },
  ],
  create: config => ({ plugin: memoryPlugin, strategies: [createGeneralStrategy(config as GeneralStrategyConfig)] }),
})

export function apply(ctx: Context, config: Config = {}): void {
  installMemory(ctx, memoryStrategyConfiguration.create(config as never))
}

export { createGeneralStrategy, GENERAL_STRATEGY_TYPE_ID, GENERAL_VIEW_STRATEGY }
