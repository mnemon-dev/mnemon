// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { createComponentRegionDirectory, createComponentSettingsDirectory, installMemoryComponentUI, MNEMON_COMPONENT_SETTINGS_SLOT, MNEMON_COMPONENT_STATUS_SLOT, type MemoryComponentSettingsComponent } from '../src/client/component-ui.tsx'
import { DOCUMENTS_PACKAGE, installShippedComponentStatus } from '../src/client/component-status.tsx'
import { installShippedComponentSettings, MEMORY_SPACES_PACKAGE, RUNTIME_PACKAGE, THREE_TIER_PACKAGE } from '../src/client/component-settings.tsx'
import { translateZh } from '../src/client/locales.ts'
import { settingsScope } from './helpers/settings-scope.ts'
import type { Config } from '../src/host/config.ts'

/** DSH's slot capability, reduced to what the component regions use. */
function slots() {
  const registered: Array<{ options: { name: string; key?: string }; component: MemoryComponentSettingsComponent }> = []
  const listeners = new Set<() => void>()
  let version = 0
  const waiting = new Set<() => void>()
  let declared = false
  const context = {
    slots: {
      inject(_name: string, setup: () => () => void) {
        // Registration waits for the region to be declared, as DSH's inject does.
        let release: (() => void) | undefined
        const run = () => { release = setup() }
        if (declared) run()
        else waiting.add(run)
        return () => { waiting.delete(run); release?.(); release = undefined }
      },
      register(options: { name: string; key: string }, component: MemoryComponentSettingsComponent) {
        const entry = { options, component }
        registered.push(entry)
        version += 1
        for (const listener of listeners) listener()
        return () => {
          registered.splice(registered.indexOf(entry), 1)
          version += 1
          for (const listener of listeners) listener()
        }
      },
      getVersion: () => version,
      entriesOfSlot: () => registered,
      subscribe(_name: string, listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } },
    },
  }
  return {
    context, registered,
    declare() { declared = true; for (const run of waiting) run(); waiting.clear() },
  }
}

describe('component regions', () => {
  it('registers a component\'s settings under its package name once the region exists', () => {
    const { context, registered, declare } = slots()
    const settings: MemoryComponentSettingsComponent = () => null
    const dispose = installMemoryComponentUI(context, { packageName: '@acme/memory-notes', settings })
    expect(registered).toHaveLength(0)
    declare()
    expect(registered.map(entry => entry.options)).toEqual([{ name: MNEMON_COMPONENT_SETTINGS_SLOT, key: '@acme/memory-notes' }])
    const directory = createComponentSettingsDirectory(context)
    const changed = vi.fn()
    directory.subscribe(changed)
    expect(directory.getSnapshot().has('@acme/memory-notes')).toBe(true)
    dispose()
    expect(changed).toHaveBeenCalled()
    expect(directory.getSnapshot().has('@acme/memory-notes')).toBe(false)
  })

  it('registers a component\'s Status card and settings into their own regions', () => {
    const { context, registered, declare } = slots()
    declare()
    const dispose = installMemoryComponentUI(context, { packageName: '@acme/memory-notes', settings: () => null, status: () => null })
    expect(registered.map(entry => entry.options)).toEqual([
      { name: MNEMON_COMPONENT_SETTINGS_SLOT, key: '@acme/memory-notes' },
      { name: MNEMON_COMPONENT_STATUS_SLOT, key: '@acme/memory-notes' },
    ])
    dispose()
    expect(registered).toEqual([])
  })

  it('registers the shipped Sources\' Status cards the way an installed component does', () => {
    const { context, registered, declare } = slots()
    declare()
    const dispose = installShippedComponentStatus(context)
    expect(registered.map(entry => [entry.options.name, entry.options.key])).toEqual([
      [MNEMON_COMPONENT_STATUS_SLOT, RUNTIME_PACKAGE], [MNEMON_COMPONENT_STATUS_SLOT, DOCUMENTS_PACKAGE], [MNEMON_COMPONENT_STATUS_SLOT, MEMORY_SPACES_PACKAGE],
    ])
    dispose()
    expect(registered).toEqual([])
    expect(createComponentRegionDirectory(context, MNEMON_COMPONENT_STATUS_SLOT).getSnapshot().size).toBe(0)
  })

  it('refuses a contribution without a package name or without anything to add', () => {
    const { context } = slots()
    expect(() => installMemoryComponentUI(context, { packageName: 'Not A Package', settings: () => null })).toThrow(/package name/u)
    expect(() => installMemoryComponentUI(context, { packageName: 'acme-memory-notes' })).toThrow(/contributes nothing/u)
  })

  it('registers the shipped components\' settings the way an installed component does', () => {
    const { context, registered, declare } = slots()
    declare()
    const dispose = installShippedComponentSettings(context, { scope: settingsScope<Config>({ status: 'ready', value: {}, writable: true, mode: 'host' }), t: translateZh })
    expect(registered.map(entry => entry.options.key)).toEqual([RUNTIME_PACKAGE, MEMORY_SPACES_PACKAGE, THREE_TIER_PACKAGE])
    dispose()
    expect(registered).toEqual([])
  })
})
