import { afterEach, describe, expect, it, vi } from 'vitest'
import type { HostContextShape } from '../src/host/dsh.ts'
import { MemoryPluginManagement } from '../src/host/plugin-management.ts'
import type { MemoryViewConfigurationRequest } from '../src/host/view-protocol.ts'
import { viewManagementFixture } from './fixtures/view-management.ts'

type Fixture = Awaited<ReturnType<typeof viewManagementFixture>>
const fixtures: Fixture[] = []
afterEach(async () => { for (const value of fixtures.splice(0)) await value.dispose() })

async function fixture() {
  const f = await viewManagementFixture()
  fixtures.push(f)
  const mutate = vi.mocked(f.settings.mutate).getMockImplementation()!
  // Match the public ConfigEditor contract: reconcile the complete profile
  // before validation and again after persisting a settings edit.
  vi.mocked(f.settings.mutate).mockImplementation(async (...args) => {
    await f.reconcileProfile()
    await mutate(...args)
    await f.reconcileProfile()
  })
  return f
}

const scope = (f: Fixture) => ({ storage: 'custom' as const, workspaceId: f.workspace, sessionId: 'root', agentId: 'root' })
async function apply(f: Fixture, entries: MemoryViewConfigurationRequest['entries']) {
  return f.management.apply(f.config, scope(f), {
    expectedRevision: (await f.management.catalog()).revision,
    strategyTypeId: 'default-three-tier', entries,
  })
}

describe('View overlays across full profile reconciliation', () => {
  it('keeps the same profile namespace for the public ctx and legacy context Loader anchors', async () => {
    const f = await fixture()
    const baseUrl = 'file:///isolated/profiles/web/cordis.yml'
    const namespace = (anchor: object) => new MemoryPluginManagement({
      settings: f.settings,
      get: () => ({ entries: () => f.loader.entries(), ...anchor }),
    } as unknown as HostContextShape, f.engine, f.settings).settingsNamespace
    const expected = namespace({ config: { baseUrl } })
    expect(expected).toMatch(/^mnemon-view-[a-f0-9]{16}$/u)
    expect(namespace({ ctx: { baseUrl } })).toBe(expected)
    expect(namespace({ context: { baseUrl } })).toBe(expected)
    expect(namespace({ ctx: { baseUrl }, context: { baseUrl: 'file:///different/' } })).toBe(expected)
    expect(namespace({ config: { baseUrl }, ctx: { baseUrl: 'file:///different/' } })).toBe(expected)
  })

  it('keeps an enabled-by-profile extension disabled and preserves untouched saved choices after a save', async () => {
    const f = await fixture()
    f.profileEntries.find(entry => entry.id === 'light')!.disabled = false
    await f.reconcileProfile()
    await apply(f, {
      capture: { enabled: true, config: { instruction: 'Keep approved preferences.' } },
    })
    await apply(f, { light: { enabled: false, config: { maxProjectionCharacters: 700 } } })

    const catalog = await f.management.catalog()
    expect(catalog.entries.find(entry => entry.entryId === 'light')).toMatchObject({ enabled: false, active: false, config: { maxProjectionCharacters: 700 } })
    expect(catalog.entries.find(entry => entry.entryId === 'capture')).toMatchObject({ enabled: true, active: true, config: { instruction: 'Keep approved preferences.' } })
    expect(f.settingsDocuments.get(f.management.settingsNamespace)!.value).toMatchObject({ entries: {
      light: { enabled: false, config: { maxProjectionCharacters: 700 } },
      capture: { enabled: true, config: { instruction: 'Keep approved preferences.' } },
    } })
    const turn = await f.graph.composableTurns.beginTurn('after-profile-save', scope(f))
    expect(turn.view.strategyExtensions?.map(value => value.typeId)).toEqual(['auto-capture'])
    f.graph.composableTurns.endTurn(turn.turnId)
    expect(f.treeWrite).not.toHaveBeenCalled()
  })

  it.each(['mnemon', 'another-plugin'])('restores saved choices before a catalog reads a %s settings write', async namespace => {
    const f = await fixture()
    await apply(f, { light: { enabled: true, config: { maxProjectionCharacters: 900 } } })
    if (namespace !== 'mnemon') f.settings.register(namespace, {}, { base: { enabled: true }, applies: 'live' })
    const revision = f.settingsDocuments.get(namespace)!.revision
    await f.settings.mutate(namespace, [{ op: 'set', path: ['enabled'], value: false }], revision)

    // No polling or timer wait: the response itself fences the pending restore.
    const catalog = await f.management.catalog()
    expect(catalog.entries.find(entry => entry.entryId === 'light')).toMatchObject({ enabled: true, active: true, config: { maxProjectionCharacters: 900 } })
    expect(catalog.diagnostics).toEqual([])
    expect(f.settings.mutate).toHaveBeenCalledTimes(2)
    const preview = await f.management.preview(f.config, scope(f), { expectedRevision: catalog.revision, strategyTypeId: 'default-three-tier', entries: {} })
    expect(preview.extensions.map(value => value.typeId)).toEqual(['light-context'])
  })

  it('waits for the real Loader activation while a catalog restores a reloaded profile', async () => {
    const f = await fixture()
    await apply(f, { light: { enabled: true, config: {} } })
    const entry = f.loader.resolve('light')
    const update = entry.update.bind(entry)
    let began!: () => void
    const restoring = new Promise<void>(resolve => { began = resolve })
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    vi.spyOn(entry, 'update').mockImplementation(async options => {
      if (options.disabled === false) { began(); await gate }
      await update(options)
    })
    await f.reconcileProfile()
    let returned = false
    const catalog = f.management.catalog().then(value => { returned = true; return value })
    try {
      await restoring
      expect(returned).toBe(false)
    } finally { release() }
    expect((await catalog).entries.find(value => value.entryId === 'light')).toMatchObject({ enabled: true, active: true })
  })

  it('retains a second profile reload arriving during an in-flight restore', async () => {
    const f = await fixture()
    await apply(f, { light: { enabled: true, config: {} } })
    await f.reconcileProfile()
    const entry = f.loader.resolve('light')
    const update = entry.update.bind(entry)
    vi.spyOn(entry, 'update').mockImplementationOnce(async options => {
      await update(options)
      await f.reconcileProfile()
    })
    const catalog = await f.management.catalog()
    expect(catalog.entries.find(value => value.entryId === 'light')).toMatchObject({ enabled: true, active: true })
    expect(catalog.diagnostics).toEqual([])
  })

  it('retries catalog discovery when a reload arrives during an asynchronous module import', async () => {
    const f = await fixture()
    await apply(f, { light: { enabled: true, config: {} } })
    const original = vi.mocked(f.loader.import).getMockImplementation()!
    let began!: () => void
    const importing = new Promise<void>(resolve => { began = resolve })
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    vi.mocked(f.loader.import).mockImplementationOnce(async name => {
      began()
      await gate
      return original(name)
    })
    const catalog = f.management.catalog()
    try {
      await importing
      await f.reconcileProfile()
    } finally { release() }
    expect((await catalog).entries.find(value => value.entryId === 'light')).toMatchObject({ enabled: true, active: true })
  })

  it('compensates durable settings before restoring the live graph if the post-save replay fails', async () => {
    const f = await fixture()
    const entry = f.loader.resolve('light')
    const update = entry.update.bind(entry)
    let enables = 0
    vi.spyOn(entry, 'update').mockImplementation(async options => {
      if (options.disabled === false && ++enables === 2) throw new Error('Post-save activation failed')
      await update(options)
    })

    await expect(apply(f, { light: { enabled: true, config: {} } })).rejects.toThrow('Post-save activation failed')
    expect(f.settings.mutate).toHaveBeenCalledTimes(2)
    expect(f.settingsDocuments.get(f.management.settingsNamespace)!.value).toEqual({ entries: {} })
    expect(entry.disabled).toBe(true)
    expect(f.engine.contributionSnapshot().strategyExtensions ?? []).toEqual([])
    const turn = await f.graph.composableTurns.beginTurn('after-compensation', scope(f))
    expect(turn.view.strategyExtensions ?? []).toEqual([])
    f.graph.composableTurns.endTurn(turn.turnId)
    expect((await f.management.catalog()).diagnostics).toEqual([])
  })

  it('reports an incomplete rollback when the compensating settings write fails', async () => {
    const f = await fixture()
    const entry = f.loader.resolve('light')
    const update = entry.update.bind(entry)
    const mutate = vi.mocked(f.settings.mutate).getMockImplementation()!
    let writes = 0
    vi.mocked(f.settings.mutate).mockImplementation(async (...args) => {
      if (++writes === 2) throw new Error('Compensation disk failure')
      await mutate(...args)
    })
    let enables = 0
    vi.spyOn(entry, 'update').mockImplementation(async options => {
      if (options.disabled === false && ++enables === 2) throw new Error('Post-save activation failed')
      await update(options)
    })

    const error = await apply(f, { light: { enabled: true, config: {} } }).catch(error => error as AggregateError)
    expect(error).toBeInstanceOf(AggregateError)
    expect((error as AggregateError).errors.map(value => value.message)).toEqual(['Post-save activation failed', 'Compensation disk failure'])
    expect((await f.management.catalog()).diagnostics.join(' ')).toContain('rollback was incomplete')
    expect(entry.disabled).toBe(true)
    expect(f.settingsDocuments.get(f.management.settingsNamespace)!.value).toMatchObject({ entries: { light: { enabled: true } } })
    expect(f.settings.mutate).toHaveBeenCalledTimes(2)
  })

  it('does not compensate over a newer saved choice after a replay failure', async () => {
    const f = await fixture()
    const entry = f.loader.resolve('light')
    const update = entry.update.bind(entry)
    const newer = { strategyTypeId: 'default-three-tier', entries: { light: { enabled: true, config: { maxProjectionCharacters: 900 } } } }
    let enables = 0
    vi.spyOn(entry, 'update').mockImplementation(async options => {
      if (options.disabled === false && ++enables === 2) {
        const document = f.settingsDocuments.get(f.management.settingsNamespace)!
        document.value = newer
        document.revision += 1
        throw new Error('Post-save activation failed')
      }
      await update(options)
    })

    const error = await apply(f, { light: { enabled: true, config: {} } }).catch(error => error as AggregateError)
    expect(error).toBeInstanceOf(AggregateError)
    expect((error as AggregateError).errors[1].message).toContain('settings changed before rollback')
    expect(f.settingsDocuments.get(f.management.settingsNamespace)!.value).toEqual(newer)
    expect(f.settings.mutate).toHaveBeenCalledOnce()
    expect(entry.disabled).toBe(true)
  })
})
