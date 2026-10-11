import { afterEach, describe, expect, it } from 'vitest'
import type { MemoryViewConfigurationRequest, MemoryViewPreferences } from '../src/host/view-protocol.ts'
import { viewManagementFixture, type ViewManagementFixtureOptions } from './fixtures/view-management.ts'

type Fixture = Awaited<ReturnType<typeof viewManagementFixture>>
const fixtures: Fixture[] = []
afterEach(async () => { for (const value of fixtures.splice(0)) await value.dispose() })

async function fixture(saved?: MemoryViewPreferences, options: ViewManagementFixtureOptions = {}) {
  const value = await viewManagementFixture(saved, undefined, {}, { pluginManager: true, ...options })
  fixtures.push(value)
  return value
}
const scope = (f: Fixture) => ({ storage: 'custom' as const, workspaceId: f.workspace, sessionId: 'root', agentId: 'root' })
async function apply(f: Fixture, entries: MemoryViewConfigurationRequest['entries'], strategyTypeId = 'default-three-tier') {
  await f.management.apply(f.config, scope(f), { expectedRevision: (await f.management.catalog()).revision, strategyTypeId, entries })
}
const saved = (f: Fixture) => f.settingsDocuments.get(f.management.settingsNamespace)!.value as MemoryViewPreferences
const entry = async (f: Fixture, entryId: string) => (await f.management.catalog()).entries.find(value => value.entryId === entryId)!

describe('memory plugin enablement owned by the DSH profile patch', () => {
  it('writes enablement through the DSH plugin manager and saves only configuration', async () => {
    const f = await fixture()
    await apply(f, { capture: { enabled: true, config: { instruction: 'Keep approved preferences.' } } })
    expect(f.pluginManager.setPluginEnabled).toHaveBeenCalledExactlyOnceWith('capture', true)
    expect(f.profileEntries.find(row => row.id === 'capture')).toMatchObject({ disabled: false })
    expect(saved(f).entries).toEqual({ capture: { config: { instruction: 'Keep approved preferences.' } } })
    expect(await entry(f, 'capture')).toMatchObject({ enabled: true, active: true, config: { instruction: 'Keep approved preferences.' } })
    expect(f.treeWrite).not.toHaveBeenCalled()
  })

  it('keeps a DSH Plugins page toggle instead of restoring an older choice', async () => {
    const f = await fixture()
    await apply(f, { light: { enabled: true, config: { maxProjectionCharacters: 700 } } })
    // The Plugins page switch writes the same profile row and reconciles.
    await f.pluginManager.setPluginEnabled('light', false)
    expect(await entry(f, 'light')).toMatchObject({ enabled: false, active: false, config: { maxProjectionCharacters: 700 } })
    await f.reconcileProfile()
    expect(await entry(f, 'light')).toMatchObject({ enabled: false, active: false })
    await f.pluginManager.setPluginEnabled('light', true)
    // Saved configuration still applies once the row runs again.
    expect(await entry(f, 'light')).toMatchObject({ enabled: true, active: true, config: { maxProjectionCharacters: 700 } })
  })

  it('moves previously saved enablement into the profile patch once', async () => {
    const f = await fixture({ strategyTypeId: 'default-three-tier', entries: {
      light: { enabled: true, config: { maxProjectionCharacters: 900 } },
      scoped: { enabled: false, config: {} },
    } })
    await f.management.catalog()
    expect(f.pluginManager.setPluginEnabled.mock.calls).toEqual([['light', true], ['scoped', false]])
    expect(saved(f)).toEqual({ strategyTypeId: 'default-three-tier', entries: { light: { config: { maxProjectionCharacters: 900 } }, scoped: { config: {} } } })
    expect(await entry(f, 'light')).toMatchObject({ enabled: true, active: true, config: { maxProjectionCharacters: 900 } })
    await f.reconcileProfile()
    await f.management.catalog()
    expect(f.pluginManager.setPluginEnabled).toHaveBeenCalledTimes(2)
  })

  it('switches between mutually exclusive main Strategies in one transaction', async () => {
    const f = await fixture(undefined, { generalStrategy: true })
    await apply(f, {
      general: { enabled: true, config: {} },
      'mnemon-strategy-default-three-tier': { enabled: false, config: {} },
    }, 'general')
    // The new main Strategy starts before the previous one stops.
    expect(f.pluginManager.setPluginEnabled.mock.calls).toEqual([['general', true], ['mnemon-strategy-default-three-tier', false]])
    expect(saved(f).strategyTypeId).toBe('general')
    const turn = await f.graph.composableTurns.beginTurn('after-switch', scope(f))
    expect(turn.view.strategyTypeId).toBe('general')
    f.graph.composableTurns.endTurn(turn.turnId)
  })

  it('rolls enablement back when the saved View no longer composes', async () => {
    const f = await fixture(undefined, { generalStrategy: true })
    // Selecting a Strategy whose Entry stays off must not leave half-applied rows.
    await expect(apply(f, { capture: { enabled: true, config: {} } }, 'general')).rejects.toThrow('selected memory Strategy type is unavailable: general')
    expect(f.pluginManager.setPluginEnabled).not.toHaveBeenCalled()
    expect(await entry(f, 'capture')).toMatchObject({ enabled: false, active: false })
    expect(saved(f).entries).toEqual({})
  })
})
