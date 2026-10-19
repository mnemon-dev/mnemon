import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryPlugin, installMemory, memoryConfigurationDigest } from 'dsh-mnemon/extension-sdk'
import { Config } from './config.ts'
import { createDocumentsMemorySource } from './source.ts'

export const name = 'dsh-mnemon-source-documents'
export const inject = ['mnemonMemory']
export { Config }

export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'Project Documents', 'zh-CN': '项目档案' },
  description: { en: 'Versioned narrative documents searched first and read in full on demand.', 'zh-CN': '可版本化的叙事文档，先检索，再按需阅读全文。' },
  roles: ['source'],
  provides: [{ id: 'source' }, { id: 'source.narrative' }],
})

export function apply(ctx: Context, config: Config = {}): void {
  installMemory(ctx, { plugin: memoryPlugin, sources: [createDocumentsMemorySource(config)] }, { effectiveDigest: memoryConfigurationDigest(config) })
}

export { createDocumentsMemorySource, DOCUMENTS_MEMORY_SOURCE } from './source.ts'
export type * from './contracts.ts'
