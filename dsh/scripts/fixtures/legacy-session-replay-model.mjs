import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'

export const legacyReplayExpected = {
  id: 'provider-existing-id-251',
  name: 'synthetic_lookup',
  arguments: '{}',
  result: 'Synthetic tool response with the original provider ID.',
}

const text = message => typeof message?.content === 'string' ? message.content : (message?.content ?? []).map(block => block.text ?? '').join('\n')

/** Validate the actual continuation wire request; the Session loader and adapter remain real. */
export function legacySessionReplayModel(report, expected = legacyReplayExpected) {
  return request => {
    const messages = request.messages ?? []
    const canary = messages.findLastIndex(message => message.role === 'user' && text(message).includes('legacy-replay-251'))
    if (canary < 0 || (request.tools ?? []).length === 0) return 'Legacy Session replay fixture.'
    const history = messages.slice(0, canary)
    const calls = history.flatMap((message, index) => message.role === 'assistant'
      ? (message.tool_calls ?? []).map(call => ({ call, index })) : [])
    const results = history.flatMap((message, index) => message.role === 'tool' ? [{ message, index }] : [])
    assert(calls.length > 0 && results.length > 0, 'continuation must retain historical tool calls and results')
    const ids = new Set()
    for (const { call, index } of calls) {
      assert(typeof call.id === 'string' && call.id.length > 0 && !ids.has(call.id), 'historical call IDs must be nonempty and unambiguous')
      ids.add(call.id)
      const matches = results.filter(result => result.message.tool_call_id === call.id)
      assert.equal(matches.length, 1, 'every historical call must have its exact result')
      assert(matches[0].index > index, 'historical results must follow their calls')
    }
    assert(results.every(result => ids.has(result.message.tool_call_id)), 'historical results cannot refer to an unknown call')
    const matching = calls.filter(({ call }) => call.id === expected.id)
    assert.equal(matching.length, 1, 'the original recorded provider ID must reach the actual model request')
    assert.equal(matching[0].call.function.name, expected.name, 'historical tool name must be preserved')
    assert.equal(matching[0].call.function.arguments, expected.arguments, 'historical arguments must be preserved')
    const result = results.find(item => item.message.tool_call_id === expected.id)
    assert.equal(text(result.message), expected.result, 'historical tool output must be preserved')
    report({ event: 'legacy-replay-verified', callId: expected.id, toolCallCount: calls.length, toolResultCount: results.length,
      argumentsSha256: createHash('sha256').update(expected.arguments).digest('hex'),
      resultSha256: createHash('sha256').update(expected.result).digest('hex') })
    return 'Legacy replay verified: the recorded provider ID, historical arguments and tool result reached the model unchanged.'
  }
}
