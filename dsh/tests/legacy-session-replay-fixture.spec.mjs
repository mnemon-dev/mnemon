import { describe, expect, it } from 'vitest'
import { legacyReplayExpected, legacySessionReplayModel } from '../scripts/fixtures/legacy-session-replay-model.mjs'

function continuation() {
  return { tools: [{ type: 'function', function: { name: 'mnemon_view_action' } }], messages: [
    { role: 'assistant', tool_calls: [{ id: legacyReplayExpected.id, type: 'function', function: {
      name: legacyReplayExpected.name, arguments: legacyReplayExpected.arguments,
    } }] },
    { role: 'tool', tool_call_id: legacyReplayExpected.id, content: legacyReplayExpected.result },
    { role: 'user', content: 'legacy-replay-251: continue after recovery' },
  ] }
}

describe('legacy Session WebUI continuation fixture', () => {
  it('acknowledges only preserved wire identities, arguments and output', () => {
    const reports = []
    expect(legacySessionReplayModel(value => reports.push(value))(continuation())).toContain('Legacy replay verified:')
    expect(reports).toEqual([expect.objectContaining({ event: 'legacy-replay-verified', callId: legacyReplayExpected.id, toolCallCount: 1, toolResultCount: 1 })])
  })

  it.each([
    request => { request.messages[0].tool_calls[0].id = ''; request.messages[1].tool_call_id = '' },
    request => { request.messages[0].tool_calls[0].id = 'invented-id'; request.messages[1].tool_call_id = 'invented-id' },
    request => { request.messages[1].tool_call_id = 'wrong-id' },
    request => { request.messages[0].tool_calls[0].function.arguments = '{"x":2}' },
    request => { request.messages[1].content = 'lost original output' },
    request => { request.messages.splice(1, 1) },
    request => { request.messages.splice(1, 0, structuredClone(request.messages[1])) },
    request => { request.messages.reverse() },
  ])('rejects changed or incomplete historical replay', mutate => {
    const request = continuation()
    mutate(request)
    expect(() => legacySessionReplayModel(() => {})(request)).toThrow()
  })

  it('keeps non-canary and title requests separate from replay evidence', () => {
    const reports = []
    const model = legacySessionReplayModel(value => reports.push(value))
    expect(model({ messages: [{ role: 'user', content: 'hello' }] })).toBe('Legacy Session replay fixture.')
    expect(model({ ...continuation(), tools: [] })).toBe('Legacy Session replay fixture.')
    expect(reports).toEqual([])
  })
})
