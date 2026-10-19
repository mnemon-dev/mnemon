import { describe, expect, it, vi } from 'vitest'
import { planLegacySettingsImport, type LegacySettingsImportOptions } from '../src/host/legacy-settings-import.ts'

const viewNamespace = 'mnemon-view-0123456789abcdef'
const legacyNamespace = 'mnemon-plugins-0123456789abcdef'
const light = 'mnemon-strategy-light-context'
const documents = 'mnemon-source-documents'

function plan(sections: unknown, overrides: Partial<Omit<LegacySettingsImportOptions, 'sections'>> = {}) {
  return planLegacySettingsImport({
    entryId: 'mnemon', viewNamespace, legacyNamespace, sections, currentOverride: {}, entryOverrides: [], sourceEntryIds: [documents], ...overrides,
  })
}

function freeze<T>(value: T): T {
  if (value !== null && typeof value === 'object') {
    for (const item of Object.values(value)) freeze(item)
    Object.freeze(value)
  }
  return value
}

describe('pure legacy settings import planning', () => {
  it('recovers root, UI and exact profile preferences from a parsed imported backup', () => {
    const result = plan({
      mnemon: { storageScope: 'custom', dataDir: '/synthetic/memory', conversationInteraction: { toolviews: false, turnBar: true, saveAction: true } },
      'mnemon-ui': { turnBar: false, saveAction: false },
      [viewNamespace]: { strategyTypeId: 'default-three-tier', entries: { [light]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
      [legacyNamespace]: { sources: { [documents]: { enabled: false } } },
    })
    expect(result).toEqual({ diagnostics: [], patch: {
      storageScope: 'custom', dataDir: '/synthetic/memory',
      conversationInteraction: { toolviews: false, turnBar: false, saveAction: false },
      memoryView: { strategyTypeId: 'default-three-tier', entries: {
        [light]: { enabled: true, config: { maxProjectionCharacters: 700 } }, [documents]: { enabled: false, config: {} },
      } },
      legacySettingsImported: true,
    } })
  })

  it('never imports another profile hash or an unsuffixed fallback into a hashed profile', () => {
    expect(plan({
      'mnemon-view-fedcba9876543210': { entries: { [light]: { enabled: true, config: {} } } },
      'mnemon-plugins-fedcba9876543210': { sources: { [documents]: { enabled: false } } },
      'mnemon-view': { entries: { [light]: { enabled: true, config: {} } } },
      unrelated: ['untouched', { anything: true }],
    })).toEqual({ patch: {}, diagnostics: [] })
  })

  it('supports the exact unsuffixed historical namespace only when requested', () => {
    expect(plan({ 'mnemon-view': { entries: { [light]: { enabled: true } } } }, {
      viewNamespace: 'mnemon-view', legacyNamespace: 'mnemon-plugins',
    })).toEqual({ patch: { memoryView: { entries: { [light]: { enabled: true, config: {} } } }, legacySettingsImported: true }, diagnostics: [] })
  })

  it('preserves explicit new root fields and independently prioritizes current UI flags', () => {
    const result = plan({
      mnemon: { timeoutMs: 25000, embedding: { enabled: false, model: 'old' }, conversationInteraction: { turnBar: false, saveAction: false } },
      'mnemon-ui': { turnBar: true, saveAction: true },
    }, { currentOverride: { timeoutMs: 10000, embedding: {}, conversationInteraction: { turnBar: false } } })
    expect(result).toEqual({ diagnostics: [], patch: {
      conversationInteraction: { turnBar: false, saveAction: true }, legacySettingsImported: true,
    } })
  })

  it('uses unified View preferences before legacy Source preferences for each Entry', () => {
    const result = plan({
      [viewNamespace]: { entries: { [documents]: { enabled: true, config: {} } } },
      [legacyNamespace]: { sources: { [documents]: { enabled: false }, other: { enabled: false } } },
    }, { sourceEntryIds: [documents, 'other'] })
    expect(result.patch.memoryView?.entries).toEqual({
      [documents]: { enabled: true, config: {} }, other: { enabled: false, config: {} },
    })
  })

  it('does not give unknown or non-Source ids new effects through a legacy Source section', () => {
    const result = plan({ [legacyNamespace]: { sources: {
      [documents]: { enabled: false }, [light]: { enabled: true }, missing: { enabled: true },
    } } })
    expect(result.patch.memoryView?.entries).toEqual({ [documents]: { enabled: false, config: {} } })
    expect(result.diagnostics).toEqual([
      expect.stringContaining(light), expect.stringContaining('missing'),
    ])
  })

  it('keeps explicit memoryView fields, including an empty entries reset', () => {
    const result = plan({ [viewNamespace]: { strategyTypeId: 'old-strategy', entries: { [light]: { enabled: true, config: {} } } } }, {
      currentOverride: { memoryView: { strategyTypeId: 'new-strategy', entries: {} } },
    })
    expect(result).toEqual({ patch: { legacySettingsImported: true }, diagnostics: [] })
  })

  it.each([
    [{ strategyTypeId: 'new-strategy' }, { strategyTypeId: 'new-strategy', entries: { [light]: { enabled: true, config: {} } } }],
    [{ entries: {} }, { strategyTypeId: 'old-strategy', entries: {} }],
  ])('fills only absent memoryView fields (%#)', (currentView, expected) => {
    const result = plan({ [viewNamespace]: { strategyTypeId: 'old-strategy', entries: { [light]: { enabled: true, config: {} } } } }, {
      currentOverride: { memoryView: currentView },
    })
    expect(result.patch.memoryView).toEqual(expected)
  })

  it('honors legitimate id-directed strategy patch rows without producing replacement rows', () => {
    const entryOverrides = freeze([
      { id: light, disabled: false, config: { maxProjectionCharacters: 1200 } },
      { id: documents, disabled: true },
      { id: 'unrelated-plugin', disabled: { __jsExpr: 'keepOriginalExpression' }, config: { unrelated: true } },
    ])
    const result = plan({ [viewNamespace]: { entries: {
      [light]: { enabled: false, config: { maxProjectionCharacters: 700 } },
      [documents]: { enabled: true, config: {} },
    } } }, { entryOverrides })
    expect(result).toEqual({ diagnostics: [], patch: { memoryView: { entries: {
      [light]: { enabled: true, config: { maxProjectionCharacters: 1200 } },
      [documents]: { enabled: false, config: {} },
    } }, legacySettingsImported: true } })
    expect(result.patch).not.toHaveProperty('entryOverrides')
    expect(result.patch.memoryView?.entries).not.toHaveProperty('unrelated-plugin')
  })

  it('preserves explicit empty plugin config and the last explicit patch value', () => {
    const result = plan({ [viewNamespace]: { entries: { [light]: { enabled: true, config: { maxProjectionCharacters: 700 } } } } }, {
      entryOverrides: [{ id: light, disabled: true }, { id: light, config: {} }, { id: light, disabled: false }],
    })
    expect(result.patch.memoryView?.entries[light]).toEqual({ enabled: true, config: {} })
  })

  it.each([
    { __jsExpr: 'profile.strategyConfig' },
    { __jsExpr: 'profile.strategyConfig', retainedLiteral: true },
    { maxProjectionCharacters: { __jsExpr: 'profile.lightLimit' } },
    { rules: [{ options: { __jsExpr: 'profile.strategyOptions' } }] },
  ])('skips the entire old strategy overlay when explicit config contains an expression (%#)', config => {
    const entryOverrides = freeze([{ id: light, disabled: true, config }])
    const sections = freeze({ [viewNamespace]: { entries: {
      [light]: { enabled: true, config: { maxProjectionCharacters: 700 } },
      [documents]: { enabled: false, config: {} },
    } } })
    const before = JSON.stringify({ sections, entryOverrides })
    const result = plan(sections, { entryOverrides })
    expect(result.patch).toEqual({
      memoryView: { entries: { [documents]: { enabled: false, config: {} } } }, legacySettingsImported: true,
    })
    expect(result.diagnostics).toEqual([expect.stringMatching(/explicit config contains an expression/iu)])
    expect(result.diagnostics[0]).toContain(light)
    expect(JSON.stringify({ sections, entryOverrides })).toBe(before)
  })

  it.each([{}, { disabled: false }])('retains native Source expressions while migrating only activation (%#)', activation => {
    const entryOverrides = freeze([{ id: documents, ...activation, config: { dataDir: { __jsExpr: 'profile.documentsPath' } } }])
    const result = plan({ [legacyNamespace]: { sources: { [documents]: { enabled: false } } } }, { entryOverrides })
    expect(result).toEqual({ diagnostics: [], patch: {
      memoryView: { entries: { [documents]: { enabled: activation.disabled === false, config: {} } } }, legacySettingsImported: true,
    } })
    expect(entryOverrides[0]!.config).toEqual({ dataDir: { __jsExpr: 'profile.documentsPath' } })
  })

  it('ignores expressions in unrelated profile rows and foreign backup sections', () => {
    const result = plan({
      'mnemon-ui': { turnBar: false },
      [viewNamespace]: { entries: { [light]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
      'mnemon-view-fedcba9876543210': { entries: { foreign: { enabled: true, config: { __jsExpr: 'profile.foreignConfig' } } } },
      unrelated: { __jsExpr: 'profile.unrelated' },
    }, { entryOverrides: [{ id: 'unrelated-plugin', config: { options: [{ __jsExpr: 'profile.unrelatedOptions' }] } }] })
    expect(result).toEqual({ diagnostics: [], patch: {
      conversationInteraction: { turnBar: false },
      memoryView: { entries: { [light]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
      legacySettingsImported: true,
    } })
  })

  it.each([
    { namespace: 'mnemon', value: { embedding: { apiKey: { __jsExpr: 'profile.apiKey' } } } },
    { namespace: 'mnemon-ui', value: { turnBar: false, __jsExpr: 'profile.interaction' } },
    { namespace: viewNamespace, value: { entries: { [light]: { enabled: true, config: { rules: [{ __jsExpr: 'profile.rules', other: true }] } } } } },
    { namespace: legacyNamespace, value: { sources: { [documents]: { enabled: { __jsExpr: 'profile.documentsEnabled' } } } } },
  ])('rejects all recovery when the relevant $namespace backup section contains an expression', ({ namespace, value }) => {
    const sections = freeze({ mnemon: { timeoutMs: 25000 }, [namespace]: value })
    const before = JSON.stringify(sections)
    const result = plan(sections)
    expect(result.patch).toEqual({})
    expect(result.diagnostics).toEqual([expect.stringMatching(/expression/iu)])
    expect(result.diagnostics[0]).toContain(namespace)
    expect(JSON.stringify(sections)).toBe(before)
  })

  it('leaves independent native Source config on its existing profile row', () => {
    const entryOverrides = freeze([{ id: documents, disabled: false, config: { dataDir: '/synthetic/independent-source', limitBytes: 20000 } }])
    const result = plan({ [legacyNamespace]: { sources: { [documents]: { enabled: false } } } }, { entryOverrides })
    expect(result).toEqual({ diagnostics: [], patch: {
      memoryView: { entries: { [documents]: { enabled: true, config: {} } } }, legacySettingsImported: true,
    } })
    expect(entryOverrides[0]!.config).toEqual({ dataDir: '/synthetic/independent-source', limitBytes: 20000 })
  })

  it('skips an old overlay with expression-controlled activation without coercing it', () => {
    const result = plan({ [viewNamespace]: { entries: {
      [light]: { enabled: true, config: { maxProjectionCharacters: 700 } }, [documents]: { enabled: false, config: {} },
    } } }, { entryOverrides: [{ id: light, disabled: { __jsExpr: 'profile.enabled' } }] })
    expect(result.patch.memoryView?.entries).toEqual({ [documents]: { enabled: false, config: {} } })
    expect(result.diagnostics).toEqual([expect.stringContaining('explicit disabled value is not boolean')])
  })

  it('is idempotent and cannot resurrect old choices after a later reset', () => {
    const sections = { 'mnemon-ui': { turnBar: false }, [viewNamespace]: { entries: { [light]: { enabled: true, config: {} } } } }
    const first = plan(sections)
    expect(first.patch.legacySettingsImported).toBe(true)
    expect(plan(sections, { currentOverride: { ...first.patch, memoryView: { entries: {} }, conversationInteraction: { turnBar: true } } }))
      .toEqual({ patch: {}, diagnostics: [] })
    expect(plan('a subsequently damaged backup', { currentOverride: { legacySettingsImported: true } }))
      .toEqual({ patch: {}, diagnostics: [] })
  })

  it('refuses ambiguous noncanonical roots and mismatched or malformed namespace selectors', () => {
    for (const options of [
      { entryId: 'custom-mnemon' },
      { legacyNamespace: 'mnemon-plugins-fedcba9876543210' },
      { viewNamespace: 'mnemon-view-../foreign' },
    ]) {
      const result = plan({ mnemon: { timeoutMs: 25000 } }, options)
      expect(result.patch).toEqual({})
      expect(result.diagnostics).toHaveLength(1)
    }
  })

  it.each([
    null,
    [],
    new Date(0),
    Object.create({ mnemon: { timeoutMs: 25000 } }),
    { mnemon: null },
    { 'mnemon-ui': { turnBar: 'false' } },
    { 'mnemon-ui': { extra: true } },
    { mnemon: { conversationInteraction: [] } },
    { mnemon: { legacySettingsImported: true } },
    { [viewNamespace]: [] },
    { [viewNamespace]: { strategyTypeId: '../invalid' } },
    { [viewNamespace]: { entries: [] } },
    { [viewNamespace]: { entries: { [light]: { enabled: 'yes' } } } },
    { [viewNamespace]: { entries: { [light]: { enabled: true, config: [] } } } },
    { [legacyNamespace]: { sources: { [documents]: { enabled: 1 } } } },
    { 'mnemon-view-invalid': { entries: {} }, mnemon: {} },
  ])('rejects malformed relevant input without a partial patch or completion marker (%#)', sections => {
    const result = plan(sections)
    expect(result.patch).toEqual({})
    expect(result.diagnostics).toHaveLength(1)
  })

  it('rejects all relevant sections together when only one is damaged', () => {
    const result = plan({ mnemon: { timeoutMs: 25000 }, [viewNamespace]: { entries: { [light]: { enabled: true, config: null } } } })
    expect(result.patch).toEqual({})
    expect(result.diagnostics[0]).toContain('config')
  })

  it('does not mistake malformed unknown Source data for a safely skipped preference', () => {
    const result = plan({ mnemon: { timeoutMs: 25000 }, [legacyNamespace]: { sources: { missing: { enabled: 'false' } } } })
    expect(result.patch).toEqual({})
    expect(result.diagnostics[0]).toContain('Source enabled must be boolean')
  })

  it.each([
    { memoryView: { entries: { [light]: { enabled: 1 } } } },
    { conversationInteraction: { turnBar: 'false' } },
    { legacySettingsImported: 'true' },
  ])('refuses malformed current overrides without marking the import complete (%#)', currentOverride => {
    const result = plan({ mnemon: { timeoutMs: 25000 } }, { currentOverride })
    expect(result.patch).toEqual({})
    expect(result.diagnostics).toHaveLength(1)
  })

  it.each(['__proto__', 'constructor', 'prototype'])('rejects unsafe %s keys at every relevant nesting level', key => {
    for (const sections of [
      JSON.parse(`{"${key}":{}}`),
      { mnemon: JSON.parse(`{"embedding":{"${key}":{}}}`) },
      { [viewNamespace]: { entries: JSON.parse(`{"${key}":{"enabled":true,"config":{}}}`) } },
    ]) {
      expect(plan(sections)).toEqual({ patch: {}, diagnostics: [expect.stringContaining('unsafe key')] })
    }
  })

  it('does not invoke accessors or serializers while rejecting non-JSON data', () => {
    const getter = vi.fn(() => true)
    const serialize = vi.fn(() => ({}))
    const cyclic: Record<string, unknown> = {}
    cyclic.self = cyclic
    for (const value of [
      Object.defineProperty({}, 'timeoutMs', { enumerable: true, get: getter }),
      { toJSON: serialize }, { unsupported: undefined }, { number: Infinity }, { cyclic },
    ]) {
      const result = plan({ mnemon: value })
      expect(result.patch).toEqual({})
      expect(result.diagnostics).toHaveLength(1)
    }
    expect(getter).not.toHaveBeenCalled()
    expect(serialize).not.toHaveBeenCalled()
  })

  it('returns independent copies without mutating backup sections, explicit rows or current values', () => {
    const sections = freeze({
      mnemon: { embedding: { enabled: true, model: 'synthetic-model' } },
      [viewNamespace]: { entries: { [light]: { enabled: true, config: { sourceKeys: ['source:a'] } } } },
      'mnemon-view-fedcba9876543210': { entries: { untouched: { enabled: true, config: {} } } },
    })
    const currentOverride = freeze({ conversationInteraction: { turnBar: false } })
    const entryOverrides = freeze([{ id: light, config: { sourceKeys: ['source:b'] } }])
    const before = JSON.stringify({ sections, currentOverride, entryOverrides })
    const result = plan(sections, { currentOverride, entryOverrides })
    expect(result.diagnostics).toEqual([])
    result.patch.memoryView!.entries[light]!.config.sourceKeys = []
    const embedding = result.patch.embedding as { model: string }
    embedding.model = 'changed-result'
    expect(JSON.stringify({ sections, currentOverride, entryOverrides })).toBe(before)
  })
})
