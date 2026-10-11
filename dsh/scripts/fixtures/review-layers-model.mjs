import assert from 'node:assert/strict'
import { delegatedCompletion } from './delegated-completion.mjs'

export const reviewLayersDocument = {
  title: 'Boot account resolution',
  description: 'Synthetic project record for issue 319.',
  content: '# Boot account resolution\n\nA boot entry never runs the panel as root. The boot script resolves the service account from the configured user, then from the owner of the data directory, and warns when neither is set.',
}
export const reviewLayersDuplicate = 'Boot entries never run the panel as root; the boot script resolves the service account from the configured user, then from the data directory owner.'

const parse = text => { try { return JSON.parse(text) } catch { return undefined } }

/**
 * Issue #319: only model choices are scripted. The idle review searches, creates
 * one Document, then repeats the same project fact in working memory when it has
 * the runtime tool; the host decides whether that second write lands.
 */
export function reviewLayersModel(report) {
  const stages = new Map()
  return request => {
    const completion = delegatedCompletion(request)
    if (completion === undefined) return 'Noted for the disposable project. Once the conversation is idle, the review decides where this knowledge belongs; compare Runtime memory with Documents afterwards.'
    const stage = stages.get(completion.key) ?? 0
    stages.set(completion.key, stage + 1)
    const results = (request.messages ?? []).filter(message => message.role === 'tool')
      .map(message => typeof message.content === 'string' ? message.content : JSON.stringify(message.content))
    const created = results.map(parse).find(value => value?.action === 'created' && value.document?.title === reviewLayersDocument.title)
    const complete = summary => ({ name: completion.name, args: completion.wrap({ action: 'created', memoryBodyIds: [], documentIds: [created.document.id], summary }) })
    if (stage === 0) return { name: 'mnemon_document_search', args: { query: reviewLayersDocument.title, limit: 1 } }
    if (stage === 1) return { name: 'mnemon_document_create', args: reviewLayersDocument }
    assert(created?.document?.id, 'review must have a real document creation receipt')
    if (stage === 2) {
      report({ event: 'document-created', documentId: created.document.id })
      if (!(request.tools ?? []).some(tool => tool.function?.name === 'mnemon_runtime_memory')) {
        report({ event: 'runtime-memory-unavailable' })
        return complete('Recorded the boot account rule in one Document; this review has no runtime memory tool.')
      }
      return { name: 'mnemon_runtime_memory', args: { action: 'add', target: 'memory', content: reviewLayersDuplicate, importance: 'normal' } }
    }
    const last = results.at(-1) ?? ''
    const written = parse(last)?.success === true && typeof parse(last)?.added === 'string'
    report({ event: written ? 'working-memory-written' : 'working-memory-refused', result: last.slice(0, 300) })
    return complete(written ? 'Recorded the boot account rule in a Document and in working memory.' : 'Recorded the boot account rule in one Document; working memory was refused in the same pass.')
  }
}
