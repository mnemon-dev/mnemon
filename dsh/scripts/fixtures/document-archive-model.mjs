import assert from 'node:assert/strict'
import { delegatedCompletion } from './delegated-completion.mjs'

function messageText(message) {
  if (typeof message?.content === 'string') return message.content
  return Array.isArray(message?.content) ? message.content.map(item => item.text ?? '').join('\n') : ''
}

function results(request) {
  return (request.messages ?? []).filter(message => message.role === 'tool').flatMap(message => {
    try { return [JSON.parse(messageText(message))] } catch { return [] }
  })
}

/** Script only model decisions; DSH tools, Native writes and Documents remain real. */
export function documentArchiveModel(report) {
  const stages = new Map()
  const respond = request => {
    const tools = request.tools ?? []
    const terminal = delegatedCompletion(request)
    if (terminal === undefined) {
      const user = (request.messages ?? []).findLast(message => message.role === 'user' && messageText(message).includes('archive-tool-222'))
      report({ event: 'root-tools', names: tools.map(tool => tool.function?.name), archiveRequested: messageText(user).includes('archive-tool-222') })
      if (!messageText(user).includes('archive-tool-222') || !tools.some(tool => tool.function?.name === 'mnemon_document_manage')) {
        return 'Document archive regression fixture ready.'
      }
      const phase = messageText(user).includes(' prepare') ? 'prepare' : messageText(user).includes(' update') ? 'update' : 'archive'
      const receipts = results(request)
      const created = receipts.find(value => value.action === 'created' && value.document?.title === 'Issue 222 Tool Roundtrip')
      if (created === undefined) return { name: 'mnemon_document_manage', args: { action: 'create', title: 'Issue 222 Tool Roundtrip', content: '# Tool archive\n\nPreserve this synthetic original.' } }
      if (phase === 'prepare') return 'The synthetic document is ready. Send archive-tool-222 update in the next turn.'
      const updated = receipts.find(value => value.action === 'updated' && value.document?.id === created.document.id)
      if (phase === 'update' && updated === undefined) return { name: 'mnemon_document_manage', args: { action: 'update', id: created.document.id, content: '# Tool archive\n\nRevision two: preserve this synthetic original and its exact cold reference.' } }
      if (phase === 'update') return 'Revision two is saved. Send archive-tool-222 to archive it in the next turn.'
      const archived = receipts.find(value => value.action === 'archived' && value.document?.id === created.document.id)
      if (archived === undefined) return { name: 'mnemon_document_manage', args: { action: 'archive', id: created.document.id } }
      assert.equal(archived.document.status, 'archived')
      assert.equal(archived.lineage?.length, 1, 'archive tool must return Host-built lineage')
      assert.equal(archived.lineage[0].source.digest, (updated ?? created).document.contentHash)
      report({ event: 'tool-archive-completed', revision: archived.document.revision, lineage: archived.lineage })
      return 'The revision-two document is archived. Its original content is preserved and the tool returned verified Host lineage.'
    }
    const prompt = (request.messages ?? []).map(messageText).join('\n')
    if (!prompt.includes('Future cold path:')) return { name: terminal.name, args: { action: 'skipped', summary: 'No idle maintenance in this fixture.', memoryBodyIds: [] } }
    const field = name => {
      const value = prompt.match(new RegExp('^' + name + ': (.+)$', 'm'))?.[1]
      assert(value, 'Missing archive field: ' + name)
      return value.trim()
    }
    const title = field('Document title')
    const summary = 'Cold archive index for ' + title + '. Preserve the original implementation and release decisions.'
    if (terminal.parameters.properties.action.enum.includes('planned')) {
      assert.equal(tools.length, 1, 'archive planner must have no data-plane tools')
      const id = prompt.match(/^\d+\. id=(.+)$/m)?.[1]
      assert(id, 'planner requires a host-filtered destination')
      report({ event: 'archive-plan', title, dataPlaneTools: 0 })
      return { name: terminal.name, args: { action: 'planned', summary, memoryBodyId: title.includes('REJECT') ? 'not-an-eligible-space' : id.trim() } }
    }
    const receipts = results(request)
    const stage = stages.get(terminal.key) ?? 0
    stages.set(terminal.key, stage + 1)
    if (stage === 0) return { name: 'mnemon_memory_bodies', args: {} }
    const catalog = receipts.find(value => Array.isArray(value.items))
    const body = catalog?.items.find(item => item.active && item.provider?.capabilities?.remember)
    assert(body, 'legacy archive needs an active writable space')
    if (stage === 1) return { name: 'mnemon_recall', args: { query: 'issue222-no-existing-cold-index', memoryBodyIds: [body.id] } }
    if (stage === 2) return { name: 'mnemon_remember', args: { memoryBodyId: body.id, content: `${summary}\nCold path: ${field('Future cold path')}\nContent SHA-256: ${field('Content SHA-256')}` } }
    const written = receipts.findLast(value => ['added', 'stored', 'skipped'].includes(value.action))
    assert(written?.id, 'legacy archive must actually write an index')
    report({ event: 'legacy-lineage-mismatch', destinationId: written.id, memoryBodyId: body.id, indexed: true })
    return { name: terminal.name, args: {
      action: 'archived', summary, memoryBodyIds: [body.id],
      lineage: [{ sourceIndex: 1, sourceDigest: field('Source digest'), destinationReceiptIndex: 1, destinationMemoryBodyId: body.id, destinationId: written.id }],
    } }
  }
  return request => {
    const reply = respond(request)
    const terminal = delegatedCompletion(request)
    return terminal !== undefined && typeof reply === 'object' && reply.name === terminal.name ? { ...reply, args: terminal.wrap(reply.args) } : reply
  }
}
