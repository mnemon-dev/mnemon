import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryPlugin, defineMemoryViewExtension, installMemory, defineMemoryStrategyConfiguration, validateMemoryViewExtension } from 'dsh-mnemon/extension-sdk'

export interface Config { sourceKeys?: string[]; actionIds?: string[]; instruction?: string }
export const name = 'dsh-mnemon-strategy-auto-capture'
export const inject = ['mnemonMemory']
export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'Active capture', 'zh-CN': '主动记录' },
  description: { en: 'Identify and retain durable facts from the current conversation.', 'zh-CN': '在当前对话中识别并保存值得长期保留的事实。' },
  roles: ['strategy-extension'],
  provides: [{ id: 'strategy.capture', exclusive: true }],
  requires: ['strategy', 'source.durable-evidence'],
})
const instruction = 'During the current conversation, consider recording at most one new durable user-supplied preference, correction, or established project fact that will help a future task. Compare existing memory first; skip duplicates, secrets, transient progress, guesses, assistant-authored claims and facts merely retrieved from memory. Respect a request not to remember. Prefer adding one concise fact to one eligible durable Source; do not copy the same fact into multiple layers. Continue without writing when no candidate qualifies.'

export function createAutoCaptureExtension(config: Config = {}) {
  const capture = validateMemoryViewExtension('capture', { instruction: config.instruction ?? instruction,
    actionIds: config.actionIds ?? ['remember'],
    ...(config.sourceKeys === undefined ? {} : { sourceKeys: config.sourceKeys }) })
  return defineMemoryViewExtension({ typeId: 'auto-capture', packageName: name, slot: 'capture',
    contribute: (_request, sources) => ({ ...capture, sourceKeys: capture.sourceKeys ?? sources
      .filter(source => source.role === 'durable-evidence').map(source => source.sourceInstanceKey).sort() }),
  })
}

export function apply(ctx: Context, config: Config = {}): void {
  installMemory(ctx, { plugin: memoryPlugin, strategyExtensions: [createAutoCaptureExtension(config)] })
}

export const memoryStrategyConfiguration = defineMemoryStrategyConfiguration({
  kind: 'strategy-extension', typeId: 'auto-capture',
  label: { en: 'Active capture', 'zh-CN': '主动记录' },
  description: { en: 'Guide the current LLM to retain durable user-supplied facts; no background Agent.', 'zh-CN': '引导当前 LLM 保留持久的用户事实，不启动后台 Agent。' },
  fields: [
    { key: 'sourceKeys', input: 'source-list', sourceRoles: ['durable-evidence'], label: { en: 'Recording targets', 'zh-CN': '记录目标' } },
    { key: 'instruction', input: 'textarea', defaultValue: instruction, maximum: 4000, label: { en: 'Recording instruction', 'zh-CN': '记录指引' } },
    { key: 'actionIds', input: 'string-list', defaultValue: ['remember'], label: { en: 'Recording operation IDs', 'zh-CN': '记录操作 ID' }, description: { en: 'Source-local operation IDs; this does not grant write permission.', 'zh-CN': '填写 Source 内部的操作 ID；此配置不会授予写入权限。' } },
  ],
  create: config => ({ plugin: memoryPlugin, strategyExtensions: [createAutoCaptureExtension(config as Config)] }),
})
