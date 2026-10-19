import { describe, expect, it } from 'vitest'
import { DEFAULT_MEMORY_VIEW_BUDGET, type MemoryAvailableSource, type MemoryStrategyContribution, type MemoryViewRequest } from 'dsh-mnemon/contracts'
import { DEFAULT_THREE_TIER_VIEW_STRATEGY as strategy } from '../src/index.ts'
import { threeTierActionWorkflow, validateThreeTierExtension } from '../src/extension-sdk.ts'

const request: MemoryViewRequest = { scope: { storage: 'custom' }, scenario: 'test', budget: { ...DEFAULT_MEMORY_VIEW_BUDGET } }
const roles = ['working-context', 'narrative', 'durable-evidence']
function sources(count = 3): MemoryAvailableSource[] {
  return Array.from({ length: count }, (_, index) => ({ sourceInstanceKey: `source:${index}`, sourceTypeId: `notes-${index}`,
    role: roles[index % 3]!, availability: 'ready', revision: 'r1', capabilities: ['project', 'write'], routeIds: [], actionIds: ['append'], routes: [],
    actions: [{ id: 'append', description: 'Append.', capability: 'write', inputSchema: { type: 'object' } }] }))
}
function contribution(slot: string, value: MemoryStrategyContribution['value']): MemoryStrategyContribution {
  return { instanceKey: `strategy-extension:${slot}`, typeId: slot, slot, value }
}

describe('three-tier owned extension contracts', () => {
  it('owns only the Runtime capacity workflow of the selected default Strategy', () => {
    expect(threeTierActionWorkflow('default-three-tier', 'runtime', 'mutate')).toBe('runtime-capacity')
    expect(threeTierActionWorkflow('custom', 'runtime', 'mutate')).toBeUndefined()
    expect(threeTierActionWorkflow('default-three-tier', 'external-source', 'mutate')).toBeUndefined()
    expect(threeTierActionWorkflow('default-three-tier', 'runtime', 'import')).toBeUndefined()
    expect(threeTierActionWorkflow('default-three-tier', 'documents', 'create')).toBeUndefined()
  })
  it('retains default composition exactly when no extension is active', () => {
    const facts = sources()
    expect(strategy.compose(request, facts)).toEqual(strategy.compose(request, facts, []))
    const result = strategy.compose(request, facts)
    expect(result.sources.map(source => source.projection?.maxCharacters)).toEqual([58982, 3277, 3277])
    expect(result.sources.map(source => source.projection?.mode)).toEqual(['eager', 'routed', 'routed'])
    expect(result.sources.every(source => source.actionIds?.[0] === 'append')).toBe(true)
  })

  it('combines selection, projection and capture without replacing each other', () => {
    const facts = sources(6)
    const policies = [contribution('selection', { sourceKeys: facts.map(source => source.sourceInstanceKey), writableSourceKeys: ['source:5'] }),
      contribution('projection', { maxProjectionCharacters: 4096 }), contribution('capture', { instruction: 'Save qualified user facts.', actionIds: ['append'] })]
    const result = strategy.compose(request, facts, policies)
    expect(result.sources).toHaveLength(6)
    expect(result.sources.flatMap(source => source.actionIds ?? [])).toEqual(['append'])
    expect(result.sources.reduce((sum, source) => sum + (source.projection?.maxCharacters ?? 0), 0)).toBe(4096)
    expect(result.guidance?.system).toContain('Eligible Source instances: source:5')
    expect(result).toEqual(strategy.compose(request, [...facts].reverse(), [...policies].reverse()))
  })

  it('does not add capture instructions when selection or Host capabilities forbid writes', () => {
    const facts = sources()
    const capture = contribution('capture', { instruction: 'Save facts.', actionIds: ['append'] })
    const readonly = contribution('selection', { sourceKeys: facts.map(source => source.sourceInstanceKey), writableSourceKeys: [] })
    expect(strategy.compose(request, facts, [readonly, capture]).guidance?.system).toBeUndefined()
    expect(strategy.compose(request, facts.map(source => ({ ...source, actionIds: [], actions: [] })), [capture]).guidance?.system).toBeUndefined()
  })

  it('keeps runtime semantics without claiming default, narrowed or multi-Source projections are the entire store', () => {
    const facts = sources().map((source, index) => ({ ...source, sourceTypeId: ['runtime', 'documents', 'memory-spaces'][index]! }))
    const original = strategy.compose(request, facts)
    expect(original.guidance?.system).toContain('budget-limited projection')
    expect(original.guidance?.system).not.toContain('complete projection')
    const limited = strategy.compose(request, facts, [contribution('projection', { maxProjectionCharacters: 100 })])
    expect(limited.guidance?.system).toContain('budget-limited projection')
    expect(limited.guidance?.system).not.toContain('complete projection')
    const scoped = strategy.compose(request, facts, [contribution('selection', { sourceKeys: facts.map(source => source.sourceInstanceKey) })])
    expect(scoped.guidance?.system).toContain('only that Source')
    expect(scoped.guidance?.system).toContain('Read-only Sources stay read-only')
  })

  it('keeps all allocation sums finite and bounded, including tiny budgets and 32 Sources', () => {
    for (const count of [0, 1, 2, 3, 7, 16, 32]) for (const budget of [1, 2, 3, 7, 100, 4096, 10000000]) {
      const facts = sources(count)
      const value = strategy.compose({ ...request, budget: { ...request.budget, maxProjectionCharacters: budget } }, facts,
        [contribution('selection', { sourceKeys: facts.map(source => source.sourceInstanceKey) })])
      const allocations = value.sources.flatMap(source => source.projection === undefined ? [] : [source.projection.maxCharacters])
      expect(allocations.every(value => Number.isSafeInteger(value) && value > 0)).toBe(true)
      expect(allocations.reduce((sum, value) => sum + value, 0)).toBeLessThanOrEqual(budget)
      if (count > 0) expect(allocations.reduce((sum, value) => sum + value, 0)).toBe(budget)
    }
  })

  it('rejects unknown slots, fields, duplicate policies and missing explicit Sources', () => {
    expect(() => strategy.compose(request, sources(), [contribution('unknown', {})])).toThrow('unsupported')
    expect(() => validateThreeTierExtension('projection', { maxProjectionCharacters: 10, writes: true })).toThrow('unsupported')
    const policy = contribution('capture', { instruction: 'Capture.', actionIds: ['append'] })
    expect(() => strategy.compose(request, sources(), [policy, policy])).toThrow('duplicate')
    expect(() => strategy.compose(request, sources(), [contribution('selection', { sourceKeys: ['source:missing'] })])).toThrow('not installed')
  })
})
