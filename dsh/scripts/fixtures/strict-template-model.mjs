import assert from 'node:assert/strict'
import { delegatedCompletion } from './delegated-completion.mjs'

export const strictTemplateDocument = {
  title: 'Review checkpoint storage',
  description: 'Synthetic project record for issue 327.',
  content: '# Review checkpoint storage\n\n' + Array.from({ length: 24 }, (_, index) =>
    `- Checkpoint rule ${index + 1}: the fixture project writes review checkpoints to SQLite before compaction.`).join('\n'),
}

const parse = text => { try { return JSON.parse(text) } catch { return undefined } }
const tokens = message => Math.ceil(JSON.stringify(message).length / 4)
const userQuery = message => message.role === 'user' && typeof message.content === 'string' && message.content.trim() !== ''

/**
 * What Ollama 0.33 renders: the system prompt and the longest suffix of the
 * other messages that fits num_ctx, always including the last message.
 */
export function ollamaWindow(messages, numCtx) {
  const system = messages.filter(message => message.role === 'system')
  const history = messages.filter(message => message.role !== 'system')
  if (history.length === 0) return { history, start: 0, kept: [] }
  let start = history.length - 1
  let size = system.reduce((sum, message) => sum + tokens(message), 0) + tokens(history[start])
  while (start > 0 && size + tokens(history[start - 1]) <= numCtx) size += tokens(history[--start])
  return { history, start, kept: history.slice(start) }
}

/**
 * Issue #327: Ollama 0.33 serving a model whose chat template requires a user
 * query answers 500 "no user query found in messages" when no user text turn
 * survives its truncation. The window is sized when the idle review sends its
 * third request, to hold that request from the delegated prompt on, so the next
 * tool round pushes the prompt out, as reported. Only model choices and the
 * server's window are scripted; DSH, the review and its tools are real.
 */
export function strictTemplateModel(report) {
  let numCtx = Number.POSITIVE_INFINITY
  const stages = new Map()
  const prompts = new Map()
  return request => {
    const messages = request.messages ?? []
    const { history, start, kept } = ollamaWindow(messages, numCtx)
    const completion = delegatedCompletion(request)
    const stage = completion === undefined ? undefined : stages.get(completion.key) ?? 0
    if (completion !== undefined) {
      if (stage === 0) prompts.set(completion.key, history.findLast(message => message.role === 'user')?.content)
      const prompt = history.findLastIndex(message => message.role === 'user' && message.content === prompts.get(completion.key))
      report({ event: 'review-request', request: stage + 1, last: history.at(-1)?.role, userQuery: kept.some(userQuery), promptKept: prompt >= start })
    }
    if (!kept.some(userQuery)) return { status: 500, error: 'no user query found in messages' }
    if (completion === undefined) return 'Noted: the disposable project keeps its review checkpoints in SQLite. Once the conversation is idle, the review decides whether this belongs in Documents.'
    stages.set(completion.key, stage + 1)
    const created = history.filter(message => message.role === 'tool').map(message => parse(message.content))
      .find(value => value?.action === 'created' && value.document?.title === strictTemplateDocument.title)
    if (stage === 0) return { name: 'mnemon_document_search', args: { query: 'SQLite review checkpoints', limit: 1 } }
    if (stage === 1) return { name: 'mnemon_document_create', args: strictTemplateDocument }
    assert(created?.document?.id, 'review must have a real document creation receipt')
    if (stage === 2) {
      const prompt = history.findLastIndex(message => message.role === 'user' && message.content === prompts.get(completion.key))
      numCtx = messages.filter(message => message.role === 'system').reduce((sum, message) => sum + tokens(message), 0)
        + history.slice(prompt).reduce((sum, message) => sum + tokens(message), 0)
      report({ event: 'window-sized', numCtx, documentId: created.document.id })
      return { name: 'mnemon_document_search', args: { query: strictTemplateDocument.title, limit: 1 } }
    }
    return { name: completion.name, args: completion.wrap({ action: 'created', memoryBodyIds: [], documentIds: [created.document.id], summary: 'Recorded where the fixture project keeps review checkpoints.' }) }
  }
}
