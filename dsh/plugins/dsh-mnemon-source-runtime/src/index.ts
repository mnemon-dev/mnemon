import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryPlugin, installMemory, memoryConfigurationDigest } from 'dsh-mnemon/extension-sdk'
import { Config } from './config.ts'
import { createRuntimeMemorySource } from './source.ts'

export const name = 'dsh-mnemon-source-runtime'
export const inject = ['mnemonMemory']
export { Config }

export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'Runtime memory', 'zh-CN': '运行时记忆' },
  description: { en: 'Bounded, deterministic hot memory injected directly into every eligible turn.', 'zh-CN': '有界且确定的热记忆，在每个符合条件的回合中直接注入。' },
  roles: ['source'],
  provides: [{ id: 'source' }, { id: 'source.working-context' }],
})

export function apply(ctx: Context, config: Config = {}): void {
  installMemory(ctx, { plugin: memoryPlugin, sources: [createRuntimeMemorySource(config)] }, { effectiveDigest: memoryConfigurationDigest(config) })
}

export { createRuntimeMemorySource, RUNTIME_MEMORY_SOURCE } from './source.ts'
export type * from './contracts.ts'
