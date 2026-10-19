import { describe, expect, it, vi } from 'vitest'
import type { MnemonClient } from '../src/client/api.ts'
import { describeChange, describePending } from '../src/client/component-feedback.ts'
import { translateEn } from '../src/client/locales.ts'
import { MnemonViewStore } from '../src/client/view-store.ts'
import type { MemoryPluginEntryView, MemoryViewConfigurationRequest, MemoryViewDashboard } from '../src/host/view-protocol.ts'

function entry(entryId: string, roles: MemoryPluginEntryView['roles'], overrides: Partial<MemoryPluginEntryView> = {}): MemoryPluginEntryView {
  return {
    entryId, packageName: 'dsh-mnemon-' + entryId, roles, label: { en: entryId, 'zh-CN': entryId }, description: { en: '', 'zh-CN': '' },
    fields: [], provides: [], requires: [], requiredBy: [], enabled: true, active: true, writable: true, config: {}, ...overrides,
  }
}
const main = (typeId: string, overrides: Partial<MemoryPluginEntryView> = {}) => entry('strategy-' + typeId, ['strategy'], { typeId, ...overrides })
const off = { enabled: false, active: false }
function dashboard(entries: MemoryPluginEntryView[], strategyTypeId = 'default-three-tier', revision = 'r1'): MemoryViewDashboard {
  return { revision, writable: true, strategyTypeId, entries, sources: [], diagnostics: [], pluginInstallation: { supported: false, suggestions: [] } }
}
const name = (value: MemoryPluginEntryView) => value.entryId
const say = (before: MemoryViewDashboard, after: MemoryViewDashboard, origin: 'own' | 'external' = 'own') => describeChange(before, after, origin, name, translateEn)

describe('component change feedback', () => {
  it('leads with what a change does to memory as a whole', () => {
    const running = [main('default-three-tier'), main('general', off)]
    expect(say(dashboard(running), dashboard([main('default-three-tier', off), main('general', off)]), 'external'))
      .toMatchObject({ text: 'Memory is not in use: no main strategy is running', tone: 'warning' })
    expect(say(dashboard([main('default-three-tier', off), main('general', off)]), dashboard(running)))
      .toMatchObject({ text: 'Memory is back, composed by “strategy-default-three-tier”', tone: 'success' })
    expect(say(dashboard([main('default-three-tier'), main('general')]), dashboard([main('default-three-tier', off), main('general')]), 'external'))
      .toMatchObject({ text: '“strategy-default-three-tier” is off; “strategy-general” composes memory for now', tone: 'warning' })
  })

  it('names a main Strategy switch and the Strategies it turned off', () => {
    const before = dashboard([main('default-three-tier'), main('general', off)])
    const after = dashboard([main('default-three-tier', off), main('general')], 'general')
    expect(say(before, after)).toMatchObject({ text: 'Switched to “strategy-general” and turned off “strategy-default-three-tier”', tone: 'success', targets: ['overview', 'strategy'] })
    expect(say(before, after, 'external')).toMatchObject({ text: 'The main strategy is now “strategy-general”', tone: 'info' })
  })

  it('names a second main Strategy that starts running unused', () => {
    expect(say(dashboard([main('default-three-tier'), main('general', off)]), dashboard([main('default-three-tier'), main('general')]), 'external'))
      .toMatchObject({ text: '“strategy-general” is running too, but composes nothing', tone: 'warning' })
  })

  it('says what a Source switch does to its layer, and points at the layer and the Providers', () => {
    const spaces = entry('source-memory-spaces', ['source'], { typeId: 'memory-spaces' })
    const before = dashboard([main('default-three-tier'), spaces])
    const after = dashboard([main('default-three-tier'), { ...spaces, ...off }])
    expect(say(before, after)).toEqual({ text: 'Turned off “source-memory-spaces”; its layer stops reading and writing', tone: 'success', targets: ['overview', 'layer:memory-spaces', 'providers'] })
    expect(say(after, before, 'external')).toMatchObject({ text: '“source-memory-spaces” was turned on; its layer reads and writes again', tone: 'info' })
  })

  it('counts several switches, and says nothing for none', () => {
    const before = dashboard([main('default-three-tier'), entry('capture', ['strategy-extension']), entry('light', ['strategy-extension'])])
    const after = dashboard([main('default-three-tier'), entry('capture', ['strategy-extension'], off), entry('light', ['strategy-extension'], off)])
    expect(say(before, after)?.text).toBe('Updated 2 components')
    expect(say(before, before)).toBeUndefined()
  })

  it('names what a switch took along, and why', () => {
    const light = entry('light', ['strategy-extension'])
    const compact = entry('compact', ['strategy-extension'], off)
    const before = dashboard([main('default-three-tier'), light, compact])
    const after = dashboard([main('default-three-tier'), { ...light, ...off }, { ...compact, enabled: true, active: true }])
    expect(describeChange(before, after, 'own', name, translateEn, 'compact')?.text).toBe('Turned on “compact” and turned off “light”, which cannot run beside it')
    const spaces = entry('spaces', ['source'], { ...off, typeId: 'memory-spaces' })
    const capture = entry('capture', ['strategy-extension'], off)
    const on = { enabled: true, active: true }
    expect(describeChange(dashboard([main('default-three-tier'), spaces, capture]), dashboard([main('default-three-tier'), { ...spaces, ...on }, { ...capture, ...on }]), 'own', name, translateEn, 'capture')?.text)
      .toBe('Turned on “capture” and “spaces”, which it needs')
  })

  it('names the switches a new main Strategy brought along', () => {
    const runtime = entry('runtime', ['source'], { ...off, typeId: 'runtime' })
    const before = dashboard([main('default-three-tier'), main('general', off), runtime])
    const after = dashboard([main('default-three-tier', off), main('general'), { ...runtime, enabled: true, active: true }], 'general')
    expect(say(before, after)?.text).toBe('Switched to “strategy-general”: turned on “runtime” and turned off “strategy-default-three-tier”')
  })

  it('says a change of options, and whose', () => {
    const light = entry('light', ['strategy-extension'])
    const before = dashboard([main('default-three-tier'), light])
    const after = dashboard([main('default-three-tier'), { ...light, config: { maxProjectionCharacters: 2048 } }])
    expect(say(before, after)).toEqual({ text: 'Updated the options of “light”', tone: 'success', targets: ['overview', 'enhancement:light'] })
    expect(say(before, after, 'external')?.text).toBe('The options of “light” changed')
    expect(describePending(before, after, name, translateEn)).toBe('Applying the options of “light”…')
  })

  it('describes a write while it runs', () => {
    const before = dashboard([main('default-three-tier'), main('general', off), entry('capture', ['strategy-extension'], off)])
    expect(describePending(before, dashboard([main('default-three-tier', off), main('general')], 'general'), name, translateEn)).toBe('Switching to “strategy-general”…')
    expect(describePending(before, dashboard([main('default-three-tier'), main('general', off), entry('capture', ['strategy-extension'])]), name, translateEn)).toBe('Turning on “capture”…')
  })
})

describe('View store changes', () => {
  function client(initial: MemoryViewDashboard) {
    let current = initial
    const applied: MemoryViewConfigurationRequest[] = []
    let refuse = false
    const value = {
      viewDashboard: vi.fn(async () => structuredClone(current)),
      applyView: vi.fn(async (request: MemoryViewConfigurationRequest) => {
        if (refuse) throw new Error('refused')
        applied.push(request)
        current = { ...current, revision: current.revision + '+', strategyTypeId: request.strategyTypeId,
          entries: current.entries.map(item => request.entries[item.entryId] === undefined ? item : { ...item, enabled: request.entries[item.entryId]!.enabled, active: request.entries[item.entryId]!.enabled, config: request.entries[item.entryId]!.config }) }
        return { saved: true as const }
      }),
    }
    return {
      client: value as unknown as MnemonClient, applied,
      elsewhere: (next: (value: MemoryViewDashboard) => MemoryViewDashboard) => { current = next(current) },
      refuse: (value: boolean) => { refuse = value },
    }
  }
  const capture = entry('capture', ['strategy-extension'], off)

  it('records its own write with both sides, and puts it back on Undo', async () => {
    const fake = client(dashboard([main('default-three-tier'), capture]))
    const store = new MnemonViewStore(fake.client)
    await store.load()
    await store.setEnabled('enhancement:capture', [{ entry: capture, enabled: true }])
    const change = store.getSnapshot().change!
    expect(change).toMatchObject({ origin: 'own', key: 'enhancement:capture' })
    expect(change.after.entries.find(value => value.entryId === 'capture')?.enabled).toBe(true)
    await store.revert(change)
    expect(fake.applied.at(-1)).toEqual({ expectedRevision: 'r1+', strategyTypeId: 'default-three-tier', entries: { capture: { enabled: false, config: {} } } })
  })

  it('tells a change made elsewhere from the echo of its own write', async () => {
    const fake = client(dashboard([main('default-three-tier'), capture]))
    const store = new MnemonViewStore(fake.client)
    await store.load()
    await store.setEnabled('enhancement:capture', [{ entry: capture, enabled: true }])
    const own = store.getSnapshot().change!.seq
    // The plugin manager announces the switch this page applied: nothing new.
    await store.load()
    expect(store.getSnapshot().change?.seq).toBe(own)
    fake.elsewhere(value => ({ ...value, entries: value.entries.map(item => item.entryId === 'strategy-default-three-tier' ? { ...item, ...off } : item) }))
    await store.load()
    expect(store.getSnapshot().change).toMatchObject({ origin: 'external' })
    expect(store.getSnapshot().change!.seq).toBeGreaterThan(own)
  })

  it('writes options with the switch the Host holds, and puts them back on Undo', async () => {
    const light = entry('light', ['strategy-extension'], { config: { maxProjectionCharacters: 4096 } })
    const fake = client(dashboard([main('default-three-tier'), light]))
    const store = new MnemonViewStore(fake.client)
    await store.load()
    await store.configure('options:light', light, { maxProjectionCharacters: 2048 })
    expect(fake.applied.at(-1)).toEqual({ expectedRevision: 'r1', strategyTypeId: 'default-three-tier', entries: { light: { enabled: true, config: { maxProjectionCharacters: 2048 } } } })
    await store.revert(store.getSnapshot().change!)
    expect(fake.applied.at(-1)!.entries).toEqual({ light: { enabled: true, config: { maxProjectionCharacters: 4096 } } })
  })

  it('retries a refused write over the state the Host then holds', async () => {
    const fake = client(dashboard([main('default-three-tier'), capture]))
    const store = new MnemonViewStore(fake.client)
    await store.load()
    fake.refuse(true)
    expect(await store.setEnabled('enhancement:capture', [{ entry: capture, enabled: true }])).toBe(false)
    expect(store.getSnapshot().failure).toMatchObject({ key: 'enhancement:capture', kind: 'apply' })
    fake.refuse(false)
    expect(await store.retry()).toBe(true)
    expect(fake.applied).toEqual([{ expectedRevision: 'r1', strategyTypeId: 'default-three-tier', entries: { capture: { enabled: true, config: {} } } }])
  })
})
