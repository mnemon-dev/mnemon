import { randomUUID } from 'node:crypto'
import type { MessageSourceMap } from '@deepseek-ai/dsh-llm'
import type { HostUserMessage } from './dsh.ts'

declare module '@deepseek-ai/dsh-llm' {
  interface MessageSourceMap {
    'dsh-mnemon': {
      kind: 'dsh-mnemon'
      form: 'recall' | 'instructions'
    }
  }
}

export const MNEMON_PLUGIN_SOURCE = 'dsh-mnemon'

/** A user-role message this plugin adds to a step, marked with its own source. */
export function createPluginMessage(text: string, form: 'recall' | 'instructions'): HostUserMessage {
  return structuredClone({
    id: randomUUID(),
    role: 'user' as const,
    content: [{ type: 'text' as const, text }],
    source: {
      kind: MNEMON_PLUGIN_SOURCE,
      form,
    } satisfies MessageSourceMap['dsh-mnemon'],
  })
}
