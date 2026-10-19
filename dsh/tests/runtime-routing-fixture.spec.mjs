import { describe, expect, it, vi } from 'vitest'
import { runtimeRoutingModel } from '../scripts/fixtures/runtime-routing-model.mjs'

function request(protocol, indexes) {
  const name = protocol === 'stable' ? 'mnemon_subagent_result' : 'mnemon_subagent_result_test'
  const completion = protocol === 'stable'
    ? `Completion protocol: call \`${name}\` exactly once with requestId \`routing-request\` and result matching this JSON schema:\n{"type":"object"}`
    : `Completion protocol: call \`${name}\` exactly once with the final result matching its parameter schema.`
  return {
    tools: [{ type: 'function', function: { name } }],
    messages: [
      { role: 'system', content: [{ type: 'text', text: completion }] },
      { role: 'user', content: `Existing eligible Memory Spaces:\n1. id=default\n\n<runtime-memory-routing-excerpts>\n${indexes.map(index => `${index}. [importance=normal] Source ${index}.`).join('\n§\n')}\n</runtime-memory-routing-excerpts>` },
    ],
  }
}

describe('Runtime routing browser fixture', () => {
  it.each(['per-run', 'stable'])('retains chunk-local invalid indexes with the %s completion protocol', protocol => {
    const report = vi.fn()
    const model = runtimeRoutingModel(report)
    for (const indexes of [[1, 2], [3]]) {
      const input = request(protocol, indexes)
      const result = {
        action: 'planned', summary: 'Fixture router restarts numbering in each chunk.',
        routes: [{ sourceIndexes: indexes.map((_index, offset) => offset + 1), memoryBodyId: 'default' }],
      }
      expect(model(input)).toEqual({
        name: input.tools[0].function.name,
        args: protocol === 'stable' ? { requestId: 'routing-request', result } : result,
      })
    }
    expect(report.mock.calls).toEqual([
      [{ allowedIndexes: [1, 2], returnedIndexes: [1, 2], memoryBodyId: 'default' }],
      [{ allowedIndexes: [3], returnedIndexes: [1], memoryBodyId: 'default' }],
    ])
  })

  it.each(['per-run', 'stable'])('ignores parent requests with a visible %s result tool but no completion persona', protocol => {
    const report = vi.fn()
    const input = request(protocol, [3])
    input.messages[0].role = 'user'
    expect(runtimeRoutingModel(report)(input)).toBe('Runtime routing fixture ready.')
    expect(report).not.toHaveBeenCalled()
  })

  it.each(['per-run', 'stable'])('requires the persona-named %s tool in the child allowlist', protocol => {
    const input = request(protocol, [3])
    input.tools[0].function.name = 'mnemon_subagent_result_unrelated'
    expect(() => runtimeRoutingModel(vi.fn())(input)).toThrow('completion tool must be in the child allowlist')
  })
})
