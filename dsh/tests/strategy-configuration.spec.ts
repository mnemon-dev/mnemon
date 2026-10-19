import { describe, expect, it, vi } from 'vitest'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { defineMemoryStrategy, defineMemoryStrategyConfiguration, type MemoryStrategyConfiguration } from 'dsh-mnemon/extension-sdk'

const label = { en: 'Test', 'zh-CN': '测试' }
const strategy = defineMemoryStrategy({
  manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'test', packageName: 'dsh-mnemon-strategy-test',
    deterministic: true, supportedSourceRoles: ['working-context'], maxSources: 1, maxRoutes: 1, maxActions: 1 },
  compose: () => ({ strategyTypeId: 'test', explanation: 'Test configuration.', sources: [] }),
})
const definition = (): Omit<MemoryStrategyConfiguration, 'apiVersion'> => ({
  kind: 'strategy', typeId: 'test', label: { ...label }, description: { ...label },
  fields: [
    { key: 'budget', input: 'number', label: { ...label }, defaultValue: 512, minimum: 1, maximum: 4096 },
    { key: 'sources', input: 'source-list', label: { ...label } },
    { key: 'instruction', input: 'textarea', label: { ...label } },
  ],
  create: () => ({ strategies: [strategy] }),
})

describe('Strategy configuration author contract', () => {
  it.each([
    { label: { en: { invalid: true }, 'zh-CN': '错误' } },
    { fields: [{ key: 'v', input: 'text', label: { en: 'Missing Chinese label' } }] },
    { fields: [{ key: 'v', input: 'source-list', label, sourceRoles: {} }] },
    { fields: [{ key: 'v', input: 'number', label, defaultValue: 'invalid' }] },
    { fields: [{ key: 'v', input: 'number', label, defaultValue: () => 100 }] },
    { fields: [{ key: 'v', input: 'number', label, defaultValue: 3.5 }] },
    { fields: [{ key: 'v', input: 'text', label }, { key: 'v', input: 'text', label }] },
  ])('rejects malformed metadata at definition time (%#)', overrides => {
    expect(() => defineMemoryStrategyConfiguration({ ...definition(), ...overrides } as never)).toThrow()
  })

  it.each([
    { budget: 0 }, { budget: 1.5 }, { budget: 4097 },
    { sources: ['source:work', 'source:work'] },
    { sources: [''] }, { sources: Array.from({ length: 33 }, (_, i) => 'source:' + i) },
    { instruction: 'x'.repeat(4001) }, { unknown: true },
  ])('rejects invalid input before calling the factory (%#)', input => {
    const create = vi.fn(definition().create)
    const configured = defineMemoryStrategyConfiguration({ ...definition(), create })
    expect(() => configured.create(input)).toThrow()
    expect(create).not.toHaveBeenCalled()
  })

  it('validates a snapshot without applying defaults or invoking a factory during discovery', () => {
    const original = definition()
    const create = vi.fn(original.create)
    const configured = defineMemoryStrategyConfiguration({ ...original, create })
    expect(create).not.toHaveBeenCalled()
    original.label.en = 'Changed later'
    original.fields[0]!.maximum = 0
    expect(configured.label.en).toBe('Test')
    expect(() => { configured.fields[0]!.maximum = 0 }).toThrow()
    expect(configured.create({})).toEqual({ strategies: [strategy] })
    expect(create).toHaveBeenCalledWith({})
    expect(configured.create({ budget: 512 })).toEqual({ strategies: [strategy] })
  })

  it.each([
    null,
    {},
    { strategies: [strategy, strategy] },
    { strategyExtensions: [{ manifest: { kind: 'strategy-extension', typeId: 'test' } }] },
    { strategies: [{ ...strategy, manifest: { ...strategy.manifest, typeId: 'another' } }] },
    { strategies: [strategy], sources: [{}] },
  ])('rejects a factory that does not return its declared contribution (%#)', contribution => {
    const configured = defineMemoryStrategyConfiguration({ ...definition(), create: () => contribution as never })
    expect(() => configured.create({})).toThrow('Plugin factory')
  })
})
