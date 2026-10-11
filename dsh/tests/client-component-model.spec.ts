import { describe, expect, it } from 'vitest'
import { componentModel, conflicts, enhancementApplies, mainPlan, sourceOf, switchPlan, unmetRequirements, type SwitchPlan } from '../src/client/component-model.ts'
import type { MemoryPluginEntryView, MemoryViewDashboard } from '../src/host/view-protocol.ts'

function entry(entryId: string, roles: MemoryPluginEntryView['roles'], overrides: Partial<MemoryPluginEntryView> = {}): MemoryPluginEntryView {
  return {
    entryId, packageName: 'dsh-mnemon-' + entryId, roles, label: { en: entryId, 'zh-CN': entryId }, description: { en: '', 'zh-CN': '' },
    fields: [], provides: [], requires: [], requiredBy: [], enabled: true, active: true, writable: true, config: {}, ...overrides,
  }
}

function dashboard(entries: MemoryPluginEntryView[], strategyTypeId = 'default-three-tier'): MemoryViewDashboard {
  return { revision: 'r1', writable: true, strategyTypeId, entries, sources: [], diagnostics: [], pluginInstallation: { supported: false, suggestions: [] } }
}

const main = (typeId: string, overrides: Partial<MemoryPluginEntryView> = {}) => entry('strategy-' + typeId, ['strategy'], { typeId, provides: [{ id: 'strategy', exclusive: false }], ...overrides })
const off = { enabled: false, active: false }

describe('memory component model', () => {
  it('composes with the selected main Strategy and marks the others as idle', () => {
    const model = componentModel(dashboard([main('default-three-tier'), main('general')]))
    expect(model.composing?.typeId).toBe('default-three-tier')
    expect(model.idle.map(value => value.typeId)).toEqual(['general'])
    expect(model.contenders).toEqual([])
  })

  it('falls back to the only running main Strategy, as the Host does', () => {
    const model = componentModel(dashboard([main('default-three-tier', off), main('general')]))
    expect(model.selected?.typeId).toBe('default-three-tier')
    expect(model.composing?.typeId).toBe('general')
    expect(model.idle).toEqual([])
  })

  it('composes with nothing while several others run and the selected one does not', () => {
    const model = componentModel(dashboard([main('default-three-tier', off), main('general'), main('focus')]))
    expect(model.composing).toBeUndefined()
    expect(model.contenders.map(value => value.typeId)).toEqual(['general', 'focus'])
  })

  it('composes with nothing while no main Strategy runs', () => {
    const model = componentModel(dashboard([main('default-three-tier', off), main('general', off)]))
    expect(model.composing).toBeUndefined()
    expect(model.contenders).toEqual([])
    // An entry that is on but has not registered does not compose either.
    expect(componentModel(dashboard([main('default-three-tier', { active: false })])).composing).toBeUndefined()
  })

  it('finds the Source of a memory layer, including a shipped one that is off and has no type', () => {
    const documents = entry('source-documents', ['source'], { typeId: 'documents' })
    const spaces = entry('source-memory-spaces', ['source'], off)
    const value = dashboard([documents, spaces])
    expect(sourceOf(value, 'documents')).toBe(documents)
    expect(sourceOf(value, 'memory-spaces')).toBe(spaces)
    expect(sourceOf(value, 'runtime')).toBeUndefined()
  })

  it('names the requirements no switched-on component provides, with the ones that could', () => {
    const spaces = entry('source-memory-spaces', ['source'], { ...off, provides: [{ id: 'source.durable-evidence', exclusive: false }] })
    const capture = entry('strategy-auto-capture', ['strategy-extension'], { requires: ['strategy', 'source.durable-evidence'] })
    const value = dashboard([main('default-three-tier'), capture, spaces])
    expect(unmetRequirements(value, capture)).toEqual([{ requirement: 'source.durable-evidence', providers: [spaces] }])
    expect(unmetRequirements(dashboard([main('default-three-tier'), capture]), capture)).toEqual([{ requirement: 'source.durable-evidence', providers: [] }])
  })

  it('applies an enhancement to the Strategy it targets or to any Strategy', () => {
    const composing = main('general')
    expect(enhancementApplies(entry('any', ['strategy-extension'], { strategyTypeId: '*' }), composing)).toBe(true)
    expect(enhancementApplies(entry('general-only', ['strategy-extension'], { strategyTypeId: 'general' }), composing)).toBe(true)
    expect(enhancementApplies(entry('three-tier-only', ['strategy-extension'], { strategyTypeId: 'default-three-tier' }), composing)).toBe(false)
    expect(enhancementApplies(entry('any', ['strategy-extension']), undefined)).toBe(false)
  })
})

describe('switch planning', () => {
  const source = (layer: string, overrides: Partial<MemoryPluginEntryView> = {}) => entry('source-' + layer, ['source'], {
    typeId: layer, provides: [{ id: 'source', exclusive: false }, ...layer === 'memory-spaces' ? [{ id: 'source.durable-evidence', exclusive: false }] : []], ...overrides,
  })
  const threeTier = main('default-three-tier', { requires: ['source'], provides: [{ id: 'strategy', exclusive: false }, { id: 'strategy.default-three-tier', exclusive: false }] })
  const capture = entry('strategy-auto-capture', ['strategy-extension'], { ...off, requires: ['strategy', 'source.durable-evidence'], provides: [{ id: 'strategy.capture', exclusive: true }] })
  const moves = (plan: SwitchPlan) => plan.changes?.map(change => [change.entry.entryId, change.enabled, change.reason ?? 'asked'])

  it('brings what a component needs and nothing already provides', () => {
    const value = dashboard([threeTier, source('runtime'), source('memory-spaces', off), capture])
    expect(moves(switchPlan(value, capture, true))).toEqual([['source-memory-spaces', true, 'needed'], ['strategy-auto-capture', true, 'asked']])
    expect(moves(switchPlan(dashboard([threeTier, source('memory-spaces'), capture]), capture, true))).toEqual([['strategy-auto-capture', true, 'asked']])
  })

  it('takes along what loses a capability it requires, but not what never had it', () => {
    const value = dashboard([threeTier, source('runtime'), source('memory-spaces'), { ...capture, enabled: true, active: true }])
    expect(moves(switchPlan(value, value.entries[2]!, false))).toEqual([['source-memory-spaces', false, 'asked'], ['strategy-auto-capture', false, 'dependent']])
    // Already broken before the switch: not this switch's doing.
    const broken = dashboard([main('default-three-tier', { requires: ['source'] }), entry('focus', ['strategy-extension'], off)])
    expect(moves(switchPlan(broken, broken.entries[1]!, true))).toEqual([['focus', true, 'asked']])
  })

  it('refuses to take the last Source from a main Strategy', () => {
    const value = dashboard([threeTier, source('runtime', off), source('memory-spaces')])
    expect(switchPlan(value, value.entries[2]!, false)).toEqual({ blocked: 'last-source' })
  })

  it('turns off what cannot run beside a component, by claimed capability or by slot', () => {
    const light = entry('light', ['strategy-extension'], { provides: [{ id: 'strategy.projection', exclusive: true }] })
    const compact = entry('compact', ['strategy-extension'], { ...off, provides: [{ id: 'strategy.projection', exclusive: false }] })
    expect(conflicts(light, compact)).toBe(true)
    expect(moves(switchPlan(dashboard([threeTier, light, compact]), compact, true))).toEqual([['light', false, 'conflict'], ['compact', true, 'asked']])
    const slotted = (id: string, strategyTypeId: string | undefined, values: Partial<MemoryPluginEntryView> = {}) => entry(id, ['strategy-extension'], { slot: 'recall', ...strategyTypeId === undefined ? {} : { strategyTypeId }, ...values })
    expect(conflicts(slotted('a', 'general'), slotted('b', 'general'))).toBe(true)
    expect(conflicts(slotted('a', '*'), slotted('b', 'general'))).toBe(true)
    expect(conflicts(slotted('a', 'general'), slotted('b', 'default-three-tier'))).toBe(false)
  })

  it('switches the main Strategy with whatever only the previous one could take', () => {
    const general = main('general', { ...off, requires: ['source'], provides: [{ id: 'strategy', exclusive: false }, { id: 'strategy.general', exclusive: false }] })
    const tiered = entry('tiered', ['strategy-extension'], { requires: ['strategy.default-three-tier'] })
    const value = dashboard([threeTier, general, source('runtime'), tiered])
    expect(moves(mainPlan(value, general))).toEqual([['strategy-default-three-tier', false, 'asked'], ['strategy-general', true, 'asked'], ['tiered', false, 'dependent']])
  })

  it('counts the capabilities of a running Source the plugin manager does not manage', () => {
    const value = { ...dashboard([threeTier, { ...capture }]), sources: [{ sourceInstanceKey: 'notes/default', sourceTypeId: 'notes', packageName: 'acme-notes', role: 'durable-evidence', label: 'Notes' }] }
    expect(unmetRequirements(value, capture)).toEqual([])
    expect(moves(switchPlan(value, capture, true))).toEqual([['strategy-auto-capture', true, 'asked']])
  })
})
