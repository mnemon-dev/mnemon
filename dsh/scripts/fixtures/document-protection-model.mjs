import assert from 'node:assert/strict'
import { delegatedCompletion } from './delegated-completion.mjs'

export const originalDocument = {
  title: '发布实现原文',
  description: '用户保存的完整实现，用于验证后台审查不会压缩或覆盖原文。',
  content: '# 发布实现原文\n\n这段实现由用户保存，后台整理应保留代码和操作细节。\n\n```ts\nexport async function deploy(build: Build) {\n  await verifySignature(build)\n  await deployCanary(build, { traffic: 0.05 })\n  await observeErrorRate({ minutes: 15, limit: 0.01 })\n  await promote(build)\n}\n```\n\n## 回滚步骤\n\n1. 停止扩大流量。\n2. 恢复上一版本。\n3. 保存观测数据，保留原始实现。\n\n保留标记：ORIGINAL_IMPLEMENTATION_MUST_SURVIVE。',
}

function toolResults(request) {
  return (request.messages ?? []).filter(message => message.role === 'tool').flatMap(message => {
    const content = typeof message.content === 'string' ? message.content : JSON.stringify(message.content)
    try { return [JSON.parse(content)] } catch { return [] }
  })
}

/** Only the model is scripted; DSH runs the real tools, review and persistence. */
export function documentProtectionModel(report) {
  let originalId
  let rootCreated = false
  const reviews = new Map()
  const respond = request => {
    const names = (request.tools ?? []).map(tool => tool.function?.name)
    // Session-title generation may run concurrently with the first tool call.
    if (names.length === 0) return '文档保护回归'
    const completion = delegatedCompletion(request)
    const terminal = completion?.name
    const receipts = toolResults(request)
    if (terminal !== undefined) {
      assert(names.includes('mnemon_document_create'), 'review must offer create-only documents')
      for (const forbidden of ['mnemon_document_manage', 'mnemon_view_action', 'mnemon_forget']) {
        assert(!names.includes(forbidden), 'review unexpectedly offers ' + forbidden)
      }
      assert(originalId, 'create the original before triggering review')
      const stage = reviews.get(completion.key) ?? 0
      reviews.set(completion.key, stage + 1)
      if (stage === 0) {
        report({ event: 'review-allowlist', allowed: names.filter(name => name !== terminal), forbiddenExcluded: true })
        // Deliberately hallucinate a forbidden tool to exercise DSH's dispatcher.
        return { name: 'mnemon_document_manage', args: { action: 'update', id: originalId, content: 'UNAUTHORIZED_CONDENSED_REPLACEMENT' } }
      }
      if (stage === 1) {
        const denied = (request.messages ?? []).findLast(message => message.role === 'tool'
          && String(message.content).includes('unknown tool "mnemon_document_manage"'))
        assert(denied, 'DSH must reject the attempted review overwrite')
        report({ event: 'review-overwrite-denied', tool: 'mnemon_document_manage' })
        report({ event: 'review-create', originalId })
        return { name: 'mnemon_document_create', args: {
          title: '发布观测补充', description: '独立保存新增观测，不替换已有实现。',
          content: '# 发布观测补充\n\n关联原档案：' + originalId + '\n\n新增经验：扩大流量前同时检查错误率与队列积压。原始代码及回滚步骤继续保留在原档案中。',
        } }
      }
      const created = receipts.findLast(value => value.action === 'created' && value.document?.title === '发布观测补充')
      assert(created?.document?.id, 'review creation must have a real receipt')
      report({ event: 'review-completed', originalId, createdId: created.document.id })
      return { name: terminal, args: { summary: '已创建独立补充档案；原始实现保持完整。', action: 'created', memoryBodyIds: [], documentIds: [created.document.id] } }
    }
    if (!rootCreated) {
      rootCreated = true
      return { name: 'mnemon_document_manage', args: { action: 'create', ...originalDocument } }
    }
    const original = receipts.find(value => value.action === 'created' && value.document?.title === originalDocument.title)
    if (original?.document?.id) {
      originalId = original.document.id
      report({ event: 'original-created', id: originalId, revision: original.document.revision })
    }
    assert(originalId, 'root creation must have a real receipt')
    return '原始发布实现已保存。后台审查可以把新增观测保存为独立补充档案。'
  }
  return request => {
    const reply = respond(request)
    const terminal = delegatedCompletion(request)
    return terminal !== undefined && typeof reply === 'object' && reply.name === terminal.name ? { ...reply, args: terminal.wrap(reply.args) } : reply
  }
}
