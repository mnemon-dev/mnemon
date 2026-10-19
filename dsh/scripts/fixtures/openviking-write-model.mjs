import assert from 'node:assert/strict'
import { delegatedCompletion } from './delegated-completion.mjs'

export const openVikingCanary = 'Issue 233 canary: release approval requires a completed staged rollout.\n合成回归资料：保留原文与换行。'

function messageText(message) {
  return typeof message?.content === 'string' ? message.content
    : (message?.content ?? []).map(item => item.text ?? '').join('\n')
}

/** Only model decisions are scripted. Host tools and the selected backend are real. */
export function openVikingWriteModel(report) {
  const stages = new Map()
  const selectBody = catalog => {
    assert(catalog, 'Memory Space catalog must be returned by the Host')
    const candidates = catalog.items.filter(item => (item.provider?.typeId ?? item.provider?.id) === 'openviking'
      && /default|issue 233/iu.test(item.name) && item.providerEnabled !== false)
    assert.equal(candidates.length, 1, 'Configure one enabled default-user OpenViking namespace for this fixture')
    return candidates[0]
  }
  const results = messages => messages.filter(message => message.role === 'tool').map(message => {
    try { return JSON.parse(messageText(message)) } catch { return undefined }
  }).filter(Boolean)
  return request => {
    if ((request.tools ?? []).length === 0) return 'OpenViking write verification'
    const terminal = delegatedCompletion(request)
    if (terminal !== undefined) {
      const persona = (request.messages ?? []).filter(message => message.role === 'system').map(messageText).join('\n')
      if (!persona.includes('supervised durable-memory writer')) return { name: terminal.name, args: terminal.wrap({ action: 'skipped', summary: 'No automatic maintenance in this synthetic write fixture.', memoryBodyIds: [] }) }
      const stage = stages.get(terminal.key) ?? 0
      stages.set(terminal.key, stage + 1)
      assert(stage < 3, 'Delegated fixture writer must finish after one write')
      if (stage === 0) return { name: 'mnemon_memory_bodies', args: {} }
      const receipts = results(request.messages ?? [])
      const body = selectBody(receipts.findLast(value => Array.isArray(value.items)))
      if (stage === 1) return { name: 'mnemon_remember', args: { memoryBodyId: body.id, content: openVikingCanary, category: 'decision', importance: 4 } }
      const written = receipts.findLast(value => ['stored', 'skipped', 'failed'].includes(value.action))
      assert(written, 'Delegated writer requires an actual Provider receipt')
      report({ event: 'provider-receipt', receipt: written })
      return { name: terminal.name, args: terminal.wrap({ action: written.action, summary: `Synthetic OpenViking Provider receipt: ${JSON.stringify(written)}`, memoryBodyIds: [body.id] }) }
    }
    const messages = request.messages ?? []
    const commandIndex = messages.findLastIndex(message => message.role === 'user' && messageText(message).includes('openviking-write-233'))
    if (commandIndex < 0) return 'OpenViking issue 233 fixture ready. Send openviking-write-233 to write the synthetic canary through Host tools.'
    const names = (request.tools ?? []).map(tool => tool.function?.name)
    assert(names.includes('mnemon_memory_bodies') && names.includes('mnemon_remember'), 'OpenViking fixture requires real Mnemon tools')
    const receipts = results(messages.slice(commandIndex + 1))
    if (receipts.length === 0) return { name: 'mnemon_memory_bodies', args: {} }
    const catalog = receipts.find(value => Array.isArray(value.items))
    const body = selectBody(catalog)
    if (receipts.length === 1) {
      report({ event: 'remember-requested', memoryBodyId: body.id, content: openVikingCanary })
      return { name: 'mnemon_remember', args: { memoryBodyId: body.id, content: openVikingCanary, category: 'decision', importance: 4 } }
    }
    assert.equal(receipts.length, 2, 'Only one write is allowed for each fixture turn')
    const receipt = receipts[1]
    report({ event: 'remember-receipt', receipt })
    if (receipt.action === 'stored') return `Host write receipt: stored. Verify the backend file count and full readback. Receipt: ${JSON.stringify(receipt)}`
    return `Host write did not produce a stored receipt: ${JSON.stringify(receipt)}`
  }
}
