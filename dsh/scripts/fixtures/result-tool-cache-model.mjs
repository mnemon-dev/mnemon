import { createHash } from 'node:crypto'
import { delegatedCompletion } from './delegated-completion.mjs'

const text = message => typeof message?.content === 'string' ? message.content : (message?.content ?? []).map(block => block.text ?? '').join('\n')

/** Deterministic model only: DSH still runs real delegated calls and tool filtering. */
export function resultToolCacheModel(report) {
  let childCount = 0
  return request => {
    const tools = request.tools ?? []
    if (tools.length === 0) return 'Result tool cache regression'
    const terminal = delegatedCompletion(request)
    const names = tools.map(tool => tool.function?.name)
    report({ event: terminal === undefined ? 'parent-tools' : 'child-tools', names, sha256: createHash('sha256').update(JSON.stringify(tools)).digest('hex') })
    if (terminal !== undefined) {
      const result = { summary: 'Synthetic cache checkpoint ' + ++childCount + ': no active space, skipped safely.', action: 'skipped', memoryBodyIds: [] }
      return { name: terminal.name, args: terminal.wrap(result) }
    }
    const messages = request.messages ?? []
    const lastUser = messages.findLastIndex(message => message.role === 'user' && text(message).includes('cache-round-239'))
    if (lastUser < 0) return 'Send cache-round-239 first, then cache-round-239 second.'
    if (messages.slice(lastUser + 1).some(message => message.role === 'tool')) return 'Delegated cache checkpoint completed. No memory was written because the test profile has no active space.'
    return { name: 'mnemon_remember', args: { content: 'Synthetic bounded cache checkpoint for issue 239.' } }
  }
}
