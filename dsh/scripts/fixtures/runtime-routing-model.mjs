import assert from 'node:assert/strict'

/** Return chunk-local indexes to exercise Host validation; storage and tools stay real. */
export function runtimeRoutingModel(report) {
  return request => {
    const messages = request.messages ?? []
    const messageText = message => typeof message.content === 'string' ? message.content
      : (message.content ?? []).map(item => item.text ?? '').join('\n')
    const system = messages.filter(message => message.role === 'system').map(messageText).join('\n')
    const name = system.match(/Completion protocol: call `([^`]+)`/u)?.[1]
    const text = messages.map(messageText).join('\n')
    if (name === undefined || !text.includes('<runtime-memory-routing-excerpts>')) return 'Runtime routing fixture ready.'
    const terminal = (request.tools ?? []).find(tool => tool.function?.name === name)?.function
    assert(terminal, 'completion tool must be in the child allowlist')
    const requestId = system.match(/requestId `([^`]+)`/u)?.[1]
    const excerpt = text.split('<runtime-memory-routing-excerpts>')[1].split('</runtime-memory-routing-excerpts>')[0]
    const indexes = [...excerpt.matchAll(/^(\d+)\. \[importance=/gm)].map(match => Number(match[1]))
    const memoryBodyId = text.match(/^\d+\. id=(.+)$/m)?.[1].trim()
    assert(indexes.length > 0 && memoryBodyId, 'routing fixture requires real sources and destinations')
    const sourceIndexes = indexes.map((_value, index) => index + 1)
    report({ allowedIndexes: indexes, returnedIndexes: sourceIndexes, memoryBodyId })
    const result = { action: 'planned', summary: 'Fixture router restarts numbering in each chunk.', routes: [{ sourceIndexes, memoryBodyId }] }
    return { name: terminal.name, args: requestId === undefined ? result : { requestId, result } }
  }
}
