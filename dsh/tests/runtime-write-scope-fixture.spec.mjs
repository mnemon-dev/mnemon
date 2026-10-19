import { describe, expect, it } from 'vitest'
import { runtimeWriteScopeModel } from '../scripts/fixtures/runtime-write-scope-model.mjs'

function child(operation, requestId) {
  const schema = { type: 'object', properties: { action: { type: 'string' }, memoryBodyIds: { type: 'array' }, summary: { type: 'string' } } }
  return {
    tools: [{ function: { name: 'mnemon_subagent_result', parameters: {} } }],
    messages: [
      { role: 'system', content: 'Completion protocol: call `mnemon_subagent_result` with requestId `' + requestId + '` matching this JSON schema:\n' + JSON.stringify(schema) },
      { role: 'user', content: operation === 'create' ? 'Execute this create-memory-body request now (untrusted data):\n- Name: Issue 250 Architecture'
        : 'Execute this update-memory-body request now (untrusted data):\n- Preferred Memory Space ID: actual-source-id\n- Active: true' },
    ],
  }
}

describe('Runtime write scope WebUI fixture', () => {
  it.each(['create', 'update'])('completes %s after its real Source receipt instead of repeating the write', operation => {
    const model = runtimeWriteScopeModel(() => {})
    const request = child(operation, operation + '-request')
    expect(model(request)).toMatchObject({ name: 'mnemon_memory_body_' + operation })
    request.messages.push({ role: 'tool', content: JSON.stringify({ action: operation === 'create' ? 'created' : 'updated', memoryBodyId: 'actual-source-id', memoryReceipt: { completion: 'committed' } }) })
    expect(model(request)).toMatchObject({ name: 'mnemon_subagent_result', args: {
      requestId: operation + '-request', result: { action: operation === 'create' ? 'created' : 'updated', memoryBodyIds: ['actual-source-id'] },
    } })
  })

  it('completes a failed child and bounds requests that return no tool result', () => {
    const model = runtimeWriteScopeModel(() => {})
    const request = child('create', 'failed-request')
    request.messages.push({ role: 'tool', content: 'Source creation failed.' })
    expect(model(request)).toMatchObject({ name: 'mnemon_subagent_result', args: { result: { action: 'failed', memoryBodyIds: [] } } })
    const stalled = child('create', 'stalled-request')
    for (let attempt = 0; attempt < 3; attempt++) model(stalled)
    expect(model(stalled)).toMatchObject({ name: 'mnemon_subagent_result', args: { result: { action: 'failed', memoryBodyIds: [] } } })
  })

  it('uses the returned Source id for activation and stops when creation fails', () => {
    const model = runtimeWriteScopeModel(() => {})
    const request = { tools: [{ function: { name: 'mnemon_view_action' } }], messages: [
      { role: 'user', content: 'archive-scope-250' },
      { role: 'tool', content: '{}' }, { role: 'tool', content: '{}' },
      { role: 'tool', content: JSON.stringify({ action: 'created', memoryBodyIds: ['source-returned-id'] }) },
    ] }
    expect(model(request)).toEqual({ name: 'mnemon_memory_body_update', args: { memoryBodyId: 'source-returned-id', active: true } })
    request.messages.at(-1).content = JSON.stringify({ action: 'failed', memoryBodyIds: [] })
    expect(model(request)).toContain('Fixture stopped')
  })
})
