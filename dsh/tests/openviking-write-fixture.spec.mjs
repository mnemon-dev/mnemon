import { describe, expect, it } from 'vitest'
import { openVikingCanary, openVikingWriteModel } from '../scripts/fixtures/openviking-write-model.mjs'

const body = { id: 'fixture-space', name: 'default', providerEnabled: true, provider: { id: 'work-openviking', typeId: 'openviking' } }
const catalog = { items: [body, { ...body, id: 'foreign-space', name: 'Other synthetic namespace' }] }
const toolResult = result => ({ role: 'tool', content: JSON.stringify(result) })
const tools = ['mnemon_memory_bodies', 'mnemon_remember', 'mnemon_subagent_result'].map(name => ({ function: { name, parameters: {} } }))

describe('OpenViking WebUI model fixture', () => {
  it('handles title requests without requiring or calling tools', () => {
    const model = openVikingWriteModel(() => {})
    expect(model({ messages: [{ role: 'user', content: 'Create a title for openviking-write-233' }] })).toBe('OpenViking write verification')
  })

  it('drives parent and delegated writer once and reports the actual Provider receipt', () => {
    const events = []
    const model = openVikingWriteModel(event => events.push(event))
    const root = { tools, messages: [{ role: 'user', content: 'openviking-write-233' }] }
    expect(model(root)).toEqual({ name: 'mnemon_memory_bodies', args: {} })
    root.messages.push(toolResult(catalog))
    expect(model(root)).toEqual({ name: 'mnemon_remember', args: { memoryBodyId: body.id, content: openVikingCanary, category: 'decision', importance: 4 } })
    const child = { tools, messages: [{ role: 'system', content: 'You are the supervised durable-memory writer.\nCompletion protocol: call `mnemon_subagent_result` with requestId `synthetic-capability`\nmatching this JSON schema:\n{"properties":{}}' }] }
    expect(model(child)).toEqual({ name: 'mnemon_memory_bodies', args: {} })
    child.messages.push(toolResult(catalog))
    expect(model(child)).toEqual({ name: 'mnemon_remember', args: { memoryBodyId: body.id, content: openVikingCanary, category: 'decision', importance: 4 } })
    const receipt = { action: 'stored', id: 'viking://user/default/memories/experiences/canary.md', memoryBodyId: body.id }
    child.messages.push(toolResult(receipt))
    expect(model(child)).toMatchObject({ name: 'mnemon_subagent_result', args: { requestId: 'synthetic-capability', result: { action: 'stored', memoryBodyIds: [body.id] } } })
    expect(events).toContainEqual({ event: 'provider-receipt', receipt })
    expect(() => model(child)).toThrow('finish after one write')
    root.messages.push(toolResult({ action: 'stored', memoryBodyIds: [body.id] }))
    expect(model(root)).toContain('Host write receipt: stored')
  })

  it('refuses ambiguous default namespaces before issuing a write', () => {
    const model = openVikingWriteModel(() => {})
    expect(() => model({ tools, messages: [{ role: 'user', content: 'openviking-write-233' }, toolResult({ items: [body, { ...body, id: 'second-default' }] })] })).toThrow('Configure one')
  })
})
