import { Context, type Fiber } from '@deepseek-ai/cordis'
import { createVolatile } from '@deepseek-ai/cosmokit'
import z from '@deepseek-ai/schemastery'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Config, InteractionConfig } from '../src/host/config.ts'
import type { HostContextShape, HostSettingsService } from '../src/host/dsh.ts'
import type { SettingsOperation } from '../src/host/protocol.ts'
import { ProfileMnemonSettings } from '../src/host/settings-service.ts'
import { legacySchema, schema as ViewSchema } from '../src/host/view-preferences.ts'

type Descriptor = ReturnType<HostSettingsService['describe']>[number]
type Entry = { id: string; options: { id: string; config: Record<string, unknown> } }
type Mounted = { ctx: Context; fiber: Fiber; entry: Entry; settings: ProfileMnemonSettings }
const roots: Context[] = []
const viewNamespace = 'mnemon-view-0123456789abcdef'
const legacyNamespace = 'mnemon-plugins-0123456789abcdef'

afterEach(async () => {
  for (const root of roots.splice(0).reverse()) await root.fiber.dispose()
})

function fixture(options: { editor?: boolean } = {}) {
  const root = new Context()
  roots.push(root)
  const state = { writable: true, entries: [] as Entry[], descriptors: [] as Descriptor[] }
  const forms = {
    get writable() { return state.writable },
    configure: vi.fn((_presentation: { auto: boolean }, _owner: unknown) => vi.fn()),
    describe: vi.fn((_options?: { redactSecrets?: boolean }) => state.descriptors),
    mutate: vi.fn(async (_namespace: string, _operations: SettingsOperation[], _expectedRevision?: number) => {}),
  }
  const editor = { entries: () => state.entries }
  root.provide('settings', forms)
  if (options.editor !== false) root.provide('configEditor', editor)

  async function mount(id = 'mnemon-instance', config: Record<string, unknown> = {}, attachEntry = true): Promise<Mounted> {
    const entry = { id: `include:${id}`, options: { id, config } }
    state.entries.push(entry)
    let ctx!: Context
    let settings!: ProfileMnemonSettings
    const fiber = await root.plugin({
      inject: ['settings'],
      apply(child: Context, _config: Record<string, unknown>) {
        ctx = child
        if (attachEntry) (child.fiber as unknown as { entry?: Entry }).entry = entry
        settings = new ProfileMnemonSettings(child as unknown as HostContextShape, config)
      },
    }, config)
    return { ctx, fiber, entry, settings }
  }

  function commit(target: Mounted, config: unknown, paths: readonly (readonly string[])[]): void {
    target.fiber.config = config
    const scope = Object.create(target.ctx) as Context & { [Context.filter]: (owner: Context) => boolean }
    scope[Context.filter] = (owner: Context) => owner.fiber === target.fiber
    root.events.emit(scope, 'loader/volatile-update', paths)
  }

  function preflight(target: Mounted, candidate: unknown): unknown {
    return root.events.waterfall(target.fiber, 'internal/config', candidate, () => candidate)
  }

  return { root, state, forms, mount, commit, preflight }
}

function registerNamespaces(target: Mounted): void {
  target.settings.register('mnemon', Config, { base: {}, applies: 'live' })
  target.settings.register('mnemon-ui', InteractionConfig, { base: { turnBar: true, saveAction: true }, applies: 'live' })
  target.settings.register(viewNamespace, ViewSchema, { base: { entries: {} }, applies: 'live' })
  target.settings.register(legacyNamespace, legacySchema, { base: { sources: {} }, applies: 'live' })
}

describe('profile settings facade', () => {
  it('maps virtual namespaces to the exact owning entry and preserves operations and revision fences', async () => {
    const f = fixture()
    const first = await f.mount('first-mnemon')
    const second = await f.mount('second-mnemon')
    registerNamespaces(first)
    registerNamespaces(second)
    const rootOps: SettingsOperation[] = [{ op: 'set', path: ['displayMode'], value: 'builtin' }]
    const uiOps: SettingsOperation[] = [{ op: 'set', path: ['turnBar'], value: false }, { op: 'unset', path: ['saveAction'] }]
    const viewOps: SettingsOperation[] = [{ op: 'set', path: ['entries'], value: {} }]
    await first.settings.mutate('mnemon', rootOps, 7)
    await first.settings.mutate('mnemon-ui', uiOps, 8)
    await first.settings.mutate(viewNamespace, viewOps, 9)
    await second.settings.mutate('mnemon', rootOps, 3)
    expect(f.forms.mutate.mock.calls).toEqual([
      ['first-mnemon', rootOps, 7],
      ['first-mnemon', [
        { op: 'set', path: ['conversationInteraction', 'turnBar'], value: false },
        { op: 'unset', path: ['conversationInteraction', 'saveAction'] },
      ], 8],
      ['first-mnemon', [{ op: 'set', path: ['memoryView', 'entries'], value: {} }], 9],
      ['second-mnemon', rootOps, 3],
    ])
    expect(uiOps.map(op => op.path)).toEqual([['turnBar'], ['saveAction']])
    await expect(first.settings.mutate(legacyNamespace, [], 9)).rejects.toThrow('Unsupported')
    await expect(first.settings.mutate('second-mnemon', rootOps, 9)).rejects.toThrow('Unsupported')
    expect(f.forms.mutate).toHaveBeenCalledTimes(4)
  })

  it('preserves native stale-revision errors without announcing a commit', async () => {
    const f = fixture()
    const target = await f.mount()
    registerNamespaces(target)
    const listener = vi.fn()
    target.settings.onUpdated(listener)
    const conflict = Object.assign(new Error('stale form'), { code: 'SETTINGS_CONFLICT', expected: 4, actual: 5 })
    f.forms.mutate.mockRejectedValueOnce(conflict)
    await expect(target.settings.mutate('mnemon-ui', [{ op: 'set', path: ['turnBar'], value: false }], 4)).rejects.toBe(conflict)
    expect(listener).not.toHaveBeenCalled()
  })

  it.each(['provider', 'editor', 'owner', 'removed', 'replaced', 'disposed'] as const)('refuses writes when the %s makes the entry unavailable', async reason => {
    const f = fixture({ editor: reason !== 'editor' })
    const target = await f.mount('owned-mnemon', {}, reason !== 'owner')
    registerNamespaces(target)
    if (reason === 'provider') f.state.writable = false
    if (reason === 'removed') f.state.entries = []
    if (reason === 'replaced') f.state.entries = [{ ...target.entry, options: { ...target.entry.options } }]
    if (reason === 'disposed') await target.fiber.dispose()
    expect(target.settings.writable).toBe(false)
    await expect(target.settings.mutate('mnemon', [{ op: 'set', path: ['displayMode'], value: 'builtin' }])).rejects.toThrow('read-only')
    expect(f.forms.mutate).not.toHaveBeenCalled()
  })

  it('preflights only its owner after interpolation and rejects before persistence', async () => {
    const f = fixture()
    f.root.on('internal/config', function (_raw, next) {
      const value = next() as { store?: string }
      return value.store === 'fixture-expression' ? { ...value, store: 'evaluated' } : value
    })
    const first = await f.mount('first-mnemon')
    const second = await f.mount('second-mnemon')
    const firstValidate = vi.fn((value: { store?: string }) => {
      if (value.store === 'reject-root') throw new Error('Runtime root rejected')
    })
    const secondValidate = vi.fn()
    first.settings.register('mnemon', Config, { base: {}, applies: 'live', validate: firstValidate })
    second.settings.register('mnemon', Config, { base: {}, applies: 'live', validate: secondValidate })
    firstValidate.mockClear()
    secondValidate.mockClear()
    expect(f.preflight(first, { store: 'fixture-expression' })).toEqual({ store: 'evaluated' })
    expect(firstValidate).toHaveBeenCalledWith(expect.objectContaining({ store: 'evaluated' }))
    expect(secondValidate).not.toHaveBeenCalled()
    let persisted = 0
    f.forms.mutate.mockImplementationOnce(async () => {
      f.preflight(first, { store: 'reject-root' })
      persisted += 1
    })
    await expect(first.settings.mutate('mnemon', [{ op: 'set', path: ['store'], value: 'reject-root' }])).rejects.toThrow('Runtime root rejected')
    expect(persisted).toBe(0)
    expect(secondValidate).not.toHaveBeenCalled()
  })

  it('announces committed values only to the owning facade and affected namespaces', async () => {
    const f = fixture()
    const first = await f.mount('first-mnemon')
    const second = await f.mount('second-mnemon')
    registerNamespaces(first)
    registerNamespaces(second)
    const firstListener = vi.fn()
    const secondListener = vi.fn()
    first.settings.onUpdated(firstListener)
    second.settings.onUpdated(secondListener)
    const config = {
      displayMode: createVolatile('builtin'),
      conversationInteraction: createVolatile({ turnBar: false, saveAction: true }),
      memoryView: createVolatile({ strategyTypeId: 'fixture-strategy', entries: {} }),
    }
    f.root.events.emit('settings/document-updated', first.entry.options.id, 1)
    f.root.events.emit('settings/updated', 'mnemon', {})
    expect(firstListener).not.toHaveBeenCalled()
    f.commit(first, config, [['conversationInteraction', 'turnBar']])
    expect(firstListener.mock.calls.map(([ns]) => ns)).toEqual(['mnemon', 'mnemon-ui'])
    expect(firstListener).toHaveBeenLastCalledWith('mnemon-ui', { turnBar: false, saveAction: true })
    expect(secondListener).not.toHaveBeenCalled()
    firstListener.mockClear()
    f.commit(first, config, [['memoryView']])
    expect(firstListener).toHaveBeenCalledExactlyOnceWith(viewNamespace, { strategyTypeId: 'fixture-strategy', entries: {} })
    firstListener.mockClear()
    f.commit(first, config, [['legacySettingsImported']])
    expect(firstListener).not.toHaveBeenCalled()
    f.commit(first, config, [[]])
    expect(firstListener.mock.calls.map(([ns]) => ns)).toEqual(['mnemon', 'mnemon-ui', viewNamespace])
  })

  it('disposes its presentation and subscriptions with the owning fiber', async () => {
    const f = fixture()
    const target = await f.mount()
    registerNamespaces(target)
    expect(f.forms.configure).toHaveBeenCalledExactlyOnceWith({ auto: false }, target.fiber)
    const disposePresentation = f.forms.configure.mock.results[0]!.value
    const listener = vi.fn()
    const stop = target.settings.onUpdated(listener)
    stop()
    f.commit(target, { displayMode: 'builtin' }, [['displayMode']])
    expect(listener).not.toHaveBeenCalled()
    target.settings.onUpdated(listener)
    await target.fiber.dispose()
    expect(disposePresentation).toHaveBeenCalledTimes(1)
    f.commit(target, { displayMode: 'sidebar' }, [['displayMode']])
    expect(listener).not.toHaveBeenCalled()
  })

  it('projects the owning redacted descriptor and shared revision into virtual namespaces', async () => {
    const f = fixture()
    const target = await f.mount('renamed-mnemon')
    registerNamespaces(target)
    f.state.descriptors = [
      { ns: 'mnemon', value: { displayMode: 'unrelated' }, revision: 999, applies: 'live' },
      {
        ns: 'renamed-mnemon', revision: 12, applies: 'live',
        value: { displayMode: 'builtin', conversationInteraction: { turnBar: false, saveAction: true }, memoryView: { entries: {} } },
        base: { displayMode: 'sidebar', conversationInteraction: { turnBar: true, saveAction: true }, memoryView: { entries: {} } },
        user: { conversationInteraction: { turnBar: false } },
      },
    ]
    const described = target.settings.describe({ redactSecrets: true })
    expect(f.forms.describe).toHaveBeenCalledWith({ redactSecrets: true })
    expect(described.map(item => [item.ns, item.revision])).toEqual([
      ['mnemon', 12], ['mnemon-ui', 12], [viewNamespace, 12], [legacyNamespace, 12],
    ])
    expect(described.find(item => item.ns === 'mnemon-ui')).toEqual({
      ns: 'mnemon-ui', value: { turnBar: false, saveAction: true }, base: { turnBar: true, saveAction: true },
      user: { turnBar: false }, revision: 12, applies: 'live',
    })
    expect(described.find(item => item.ns === 'mnemon')?.value).toMatchObject({ displayMode: 'builtin' })
    expect(described.find(item => item.ns === legacyNamespace)?.value).toEqual({})
  })

  it.each(['missing-form', 'missing-base'] as const)('does not reconstruct secrets in redacted %s fallback values', async missing => {
    const f = fixture()
    const secret = 'synthetic-profile-settings-secret'
    const target = await f.mount('secret-mnemon', { token: secret, label: 'public' })
    const schema = z.object({ token: z.string().role('secret'), label: z.string() })
    target.settings.register('mnemon', schema, { base: { token: secret, label: 'public' }, applies: 'live' })
    if (missing === 'missing-base') f.state.descriptors = [{ ns: 'secret-mnemon', value: { label: 'public' }, revision: 1, applies: 'live' }]
    expect(JSON.stringify(target.settings.describe({ redactSecrets: true }))).not.toContain(secret)
  })

  it.each(['schema', 'validator'] as const)('does not retain a registration rejected by its %s', async rejectedBy => {
    const f = fixture()
    const target = await f.mount('mnemon-instance', { count: rejectedBy === 'schema' ? 'invalid' : 1 })
    const schema = z.object({ count: z.number() })
    const validate = vi.fn(() => { throw new Error('Initial settings rejected') })
    expect(() => target.settings.register('mnemon', schema, {
      base: {}, applies: 'live', ...(rejectedBy === 'validator' ? { validate } : {}),
    })).toThrow()
    expect(target.settings.describe()).toEqual([])
    target.fiber.config = { count: 2 }
    expect(target.settings.register<{ count: number }>('mnemon', schema, { base: {}, applies: 'live' }).get()).toEqual({ count: 2 })
    const attempts = validate.mock.calls.length
    expect(f.preflight(target, { count: 3 })).toEqual({ count: 3 })
    expect(validate).toHaveBeenCalledTimes(attempts)
  })

  it('resolves resets from the current profile without reviving startup overrides', async () => {
    const f = fixture()
    const initial = { store: 'startup-override', conversationInteraction: { turnBar: false, saveAction: false } }
    const target = await f.mount('mnemon-instance', initial)
    const rootScope = target.settings.register('mnemon', Config, { base: initial, applies: 'live' })
    const uiScope = target.settings.register('mnemon-ui', InteractionConfig, { base: initial.conversationInteraction, applies: 'live' })
    expect(rootScope.get()).toMatchObject({ store: 'startup-override' })
    expect(uiScope.get()).toEqual({ turnBar: false, saveAction: false })
    target.fiber.config = {}
    expect(rootScope.get()).not.toHaveProperty('store', 'startup-override')
    expect(uiScope.get()).toEqual({ turnBar: true, saveAction: true })
  })

  it('contains subscriber failures after commit so other namespaces still refresh', async () => {
    const f = fixture()
    const target = await f.mount()
    registerNamespaces(target)
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const listener = vi.fn()
    target.settings.onUpdated(() => { throw new Error('Fixture subscriber failed') })
    target.settings.onUpdated(listener)
    try {
      expect(() => f.commit(target, {}, [[]])).not.toThrow()
      expect(listener.mock.calls.map(([namespace]) => namespace)).toEqual(['mnemon', 'mnemon-ui', viewNamespace])
      expect(warning).toHaveBeenCalled()
    } finally {
      warning.mockRestore()
    }
  })
})
