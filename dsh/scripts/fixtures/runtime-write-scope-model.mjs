import assert from 'node:assert/strict'
import { delegatedCompletion } from './delegated-completion.mjs'

export const archiveScopeSaved = [
  'Issue 250 architecture checkpoint. '.repeat(6).trim(),
  'Issue 250 release checkpoint. '.repeat(6).trim(),
]
export const archiveScopePending = 'Issue 250 pending checkpoint. '.repeat(9).trim()
const text = message => typeof message?.content === 'string' ? message.content : (message?.content ?? []).map(block => block.text ?? '').join('\n')
const parseResult = message => { try { return JSON.parse(text(message)) } catch { return { error: text(message) } } }

/** Script model decisions only; the pinned View, tool dispatch and Native writes remain real. */
export function runtimeWriteScopeModel(report) {
  const childRequests = new Map()
  return request => {
    const messages = request.messages ?? []
    const terminal = delegatedCompletion(request)
    if (terminal !== undefined) {
      const prompt = messages.map(text).join('\n')
      const attempt = (childRequests.get(terminal.key) ?? 0) + 1
      childRequests.set(terminal.key, attempt)
      const completed = (action, memoryBodyIds, summary) => ({ name: terminal.name, args: terminal.wrap({ action, memoryBodyIds, summary }) })
      if (attempt > 3) return completed('failed', [], 'Fixture child exceeded its bounded request count; stop and inspect the first tool result.')
      const receipts = messages.filter(message => message.role === 'tool').map(parseResult)
      if (/Execute this (?:create|update)-memory-body request now/u.test(prompt) && receipts.length > 0) {
        const result = receipts.at(-1)
        if (!['created', 'updated'].includes(result?.action) || typeof result.memoryBodyId !== 'string') {
          return completed('failed', [], 'Fixture Source action failed: ' + JSON.stringify(result))
        }
        report({ event: 'space-action', action: result.action, memoryBodyId: result.memoryBodyId })
        return completed(result.action, [result.memoryBodyId], 'Completed the requested synthetic space action.')
      }
      if (prompt.includes('Execute this create-memory-body request now')) {
        const name = prompt.match(/^- Name: (.+)$/m)?.[1]
        assert(name?.startsWith('Issue 250 '), 'create worker requires the fixture name')
        return { name: 'mnemon_memory_body_create', args: { name, description: 'Disposable synthetic archive destination for issue 250.' } }
      }
      if (prompt.includes('Execute this update-memory-body request now')) {
        const memoryBodyId = prompt.match(/^- Preferred Memory Space ID: (.+)$/m)?.[1]
        assert(memoryBodyId, 'update worker requires the actual Source-created id')
        return { name: 'mnemon_memory_body_update', args: { memoryBodyId, active: true } }
      }
      if (!prompt.includes('<runtime-memory-routing-excerpts>')) return { name: terminal.name, args: terminal.wrap({ action: 'skipped', summary: 'No idle maintenance in this fixture.', memoryBodyIds: [] }) }
      const destinations = [...prompt.matchAll(/^\d+\. id=(.+)$/gm)].map(match => match[1].trim())
      const excerpt = prompt.split('<runtime-memory-routing-excerpts>')[1].split('</runtime-memory-routing-excerpts>')[0]
      const indexes = [...excerpt.matchAll(/^(\d+)\. \[importance=/gm)].map(match => Number(match[1]))
      assert(destinations.length === 2 && indexes.length === 2, 'fixture requires two real destinations and two committed entries')
      const routes = indexes.map((sourceIndex, index) => ({ sourceIndexes: [sourceIndex], memoryBodyId: destinations[index] }))
      report({ event: 'archive-routes', routes })
      return { name: terminal.name, args: terminal.wrap({ action: 'planned', summary: 'Split the two synthetic checkpoints between their authorized destinations.', routes }) }
    }
    const userIndex = messages.findLastIndex(message => message.role === 'user' && text(message).includes('archive-scope-250'))
    if (userIndex < 0 || !(request.tools ?? []).some(tool => tool.function?.name === 'mnemon_view_action')) return 'Send archive-scope-250 in a fresh fixture to exercise same-View archive destinations.'
    const calls = messages.slice(userIndex + 1).filter(message => message.role === 'tool')
    const results = calls.map(parseResult)
    const retry = text(messages[userIndex]).includes('retry')
    const add = content => ({ name: 'mnemon_runtime_memory', args: { action: 'add', target: 'memory', content } })
    if (retry) {
      if (calls.length === 0) return add(archiveScopePending)
    } else {
      if (calls.length < 2) return add(archiveScopeSaved[calls.length])
      if (calls.length === 2 || calls.length === 4) return { name: 'mnemon_memory_body_create', args: {
        name: calls.length === 2 ? 'Issue 250 Architecture' : 'Issue 250 Release', description: 'Disposable synthetic archive destination for issue 250.',
      } }
      if (calls.length === 3 || calls.length === 5) {
        const memoryBodyId = results.at(-1)?.memoryBodyIds?.[0]
        if (!memoryBodyId) return 'Fixture stopped because the Source create Action failed: ' + JSON.stringify(results.at(-1))
        return { name: 'mnemon_memory_body_update', args: { memoryBodyId, active: true } }
      }
      if (calls.length === 6) return add(archiveScopePending)
    }
    const result = results.at(-1)
    report({ event: 'capacity-result', retry, result })
    return result?.maintenance?.kind === 'mnemon-archive'
      ? 'The pending checkpoint committed after both exact saved checkpoints were archived to the two Native spaces. Inspect Runtime and both spaces to verify preservation.'
      : 'The capacity write failed. The two active Native spaces were created after this View was pinned. The pending checkpoint remains in the tool input; send archive-scope-250 retry in the next turn. Result: ' + JSON.stringify(result)
  }
}
