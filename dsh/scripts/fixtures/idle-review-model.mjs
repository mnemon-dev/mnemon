import assert from 'node:assert/strict'
import { delegatedCompletion } from './delegated-completion.mjs'

/** Only model choices/failure are scripted; writes, receipts, timers and UI are real. */
export function idleReviewModel(report) {
  const stages = new Map()
  return request => {
    const completion = delegatedCompletion(request)
    if (completion === undefined) return 'Synthetic checkpoint ready. The disposable project uses SQLite and all review evidence is test-owned. Send another substantive turn to reach the unchanged activity threshold.'
    const stage = stages.get(completion.key) ?? 0
    stages.set(completion.key, stage + 1)
    const receipts = (request.messages ?? []).filter(message => message.role === 'tool').flatMap(message => {
      try { return [JSON.parse(message.content)] } catch { return [] }
    })
    if (stage === 0) {
      report({ event: 'review-start', run: stages.size, checkpointChars: (request.messages ?? []).reduce((sum, message) => sum + (typeof message.content === 'string' ? message.content.length : 0), 0) })
      return { name: 'mnemon_document_search', args: { query: 'Idle review reconciliation fixture', limit: 1 } }
    }
    if (stage === 1) return { name: 'mnemon_document_create', args: {
      title: 'Idle review reconciliation fixture', description: 'Synthetic partial-failure receipt evidence.',
      content: '# Synthetic review checkpoint\n\nThe disposable fixture uses SQLite. This document is created before an intentional model failure and must remain available for reconciliation.',
    } }
    const created = receipts.find(value => value.action === 'created' && value.document?.title === 'Idle review reconciliation fixture')
    assert(created?.document?.id, 'review must have a real document creation receipt')
    if (stage === 2) {
      report({ event: 'document-committed', documentId: created.document.id })
      // The Document claims this pass for project knowledge (#319); a profile entry stays independent.
      return { name: 'mnemon_runtime_memory', args: { action: 'add', target: 'user', content: 'The fixture user reviews disposable SQLite checkpoints.', importance: 'low' } }
    }
    const runtime = receipts.find(value => value.success === true && value.target === 'user' && typeof value.added === 'string'
      && value.memoryReceipt?.status === 'succeeded' && value.memoryReceipt?.completion === 'committed')
    assert(runtime, 'review must have a real committed Runtime mutation receipt')
    report({ event: 'partial-failure', documentId: created.document.id, runtimeRevision: runtime.revision, replayed: stage > 3 })
    return { error: 'SYNTHETIC_REVIEW_FAILURE_AFTER_COMMIT: deliberate fixture failure after two confirmed writes' }
  }
}
