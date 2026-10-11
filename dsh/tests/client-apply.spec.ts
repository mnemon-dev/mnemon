import { afterEach, describe, expect, it, vi } from 'vitest'

const { mountBetterSidebar } = vi.hoisted(() => ({
  mountBetterSidebar: vi.fn((
    _ctx: unknown,
    _translate: unknown,
    _seat: unknown,
  ) => vi.fn()),
}))
vi.mock('../src/client/better-sidebar.tsx', () => ({ mountBetterSidebarTab: mountBetterSidebar }))

import { apply, inject } from '../src/client/index.ts'
import { en, zh } from '../src/client/locales.ts'
import type { MnemonActionSeat } from '../src/client/action-seat.ts'
import { MnemonPluginActions } from '../src/client/MnemonPluginActions.tsx'
import { MnemonComponentRowHost, MnemonSettingsHost } from '../src/client/MnemonSettingsHost.tsx'
import { MnemonSettingsScope } from '../src/client/settings.ts'
import { MnemonBuiltinWorkspaceHost } from '../src/client/workspace-mount.tsx'
import type { Config } from '../src/host/protocol.ts'
import { STARTER_COMPONENT_ROWS } from '../src/client/starter-rows.ts'

const disposers: Array<() => void> = []
afterEach(() => {
  for (const stop of disposers.splice(0).reverse()) stop()
  vi.clearAllMocks()
})

function workspaceContext(initialValue: Record<string, unknown>, load: () => Promise<Record<string, unknown>> = async () => initialValue) {
  let value = initialValue
  let revision = 0
  let active: 'zh' | 'en' = 'zh'
  const slots: Record<string, unknown>[] = []
  const workspaceStops: ReturnType<typeof vi.fn>[] = []
  /** Scoped `ctx.inject` calls; a test provides a service by applying the matching callbacks. */
  const injected: Array<{ deps: string[]; apply: (inner: Record<string, unknown>) => void }> = []
  const context = {
    uiSession: { adapter: { current: { getSnapshot: () => ({ key: undefined }), subscribe: () => () => {} } } },
    connection: { rpc: { call: vi.fn(async (_channel: string, endpoint: string, payload: { namespace?: string; ops?: Array<{ path: string[]; value?: unknown }> }) => {
      if (payload.namespace === 'mnemon') {
        if (endpoint === 'get') value = await load()
        else { value = { ...value, ...Object.fromEntries((payload.ops ?? []).map(op => [op.path[0], op.value])) }; revision += 1 }
      }
      return { ok: true, value: { status: 'ready', value: payload.namespace === 'mnemon-ui' ? {} : value, base: {}, user: value, revision, writable: true, mode: 'host' } }
    }) }, isLoopback: true },
    effect: vi.fn((callback: () => unknown) => {
      const dispose = callback()
      if (typeof dispose === 'function') disposers.push(dispose as () => void)
    }),
    inject: vi.fn((deps: string[], applyScoped: (inner: Record<string, unknown>) => void) => { injected.push({ deps, apply: applyScoped }) }),
    layout: { selectPanel: vi.fn() },
    locale: {
      register: vi.fn(() => () => {}),
      bind: vi.fn(() => (key: keyof typeof zh) => (active === 'zh' ? zh : en)[key]),
      getSnapshot: vi.fn(() => ({ active, locales: [], revision: 0 })),
      subscribe: vi.fn(() => () => {}),
    },
    slots: {
      inject: vi.fn((_name: string, factory: () => unknown) => factory()),
      entries: vi.fn(() => []),
      subscribe: vi.fn(() => () => {}),
      register: vi.fn((options: Record<string, unknown>) => {
        slots.push(options)
        const stop = vi.fn()
        if (options.name === 'shell.overlay' || options.name === 'conversation.view') workspaceStops.push(stop)
        return stop
      }),
    },
  }
  apply(context)
  const settingsEntry = slots.find(options => options.name === 'plugins.bundle.config')!
  const scope = (settingsEntry.inject as () => { scope: MnemonSettingsScope<Config> })().scope
  const provide = (name: string, service: unknown): void => {
    for (const entry of injected) if (entry.deps.includes(name)) entry.apply({ [name]: service, effect: context.effect })
  }
  return { context, slots, scope, settingsEntry, workspaceStops, provide, setLocale: (locale: 'zh' | 'en') => { active = locale } }
}

describe('Mnemon Web client composition', () => {
  it('releases an acquired shell slot when a later Sidebar mount step throws', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mountBetterSidebar.mockImplementationOnce(() => { throw new Error('broken Sidebar integration') })

    const { scope, workspaceStops } = workspaceContext({ displayMode: 'sidebar' })
    await vi.waitFor(() => expect(scope.getSnapshot().status).toBe('ready'))

    expect(workspaceStops).toHaveLength(1)
    expect(workspaceStops[0]).toHaveBeenCalledOnce()
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('keeps a locale-bound Sidebar with Source child-render authority and conversation actions', async () => {
    const { context, slots, scope, settingsEntry, setLocale } = workspaceContext({})
    expect(inject).toEqual(['slots', 'sessions', 'workspaces', 'uiSession', 'connection', 'locale', 'layout'])
    expect(context.locale.register).toHaveBeenCalledWith('mnemon', { zh, en })
    await vi.waitFor(() => expect(slots).toEqual(expect.arrayContaining([
      expect.objectContaining({ name: 'shell.overlay', id: 'mnemon', children: { 'mnemon.source.page': { kind: 'list', scope: 'root' }, 'mnemon.component.status': { kind: 'keyed', scope: 'root' } } }),
      expect.objectContaining({ name: 'conversation.chat.assistant-actions', id: 'mnemon-save' }),
    ])))
    const props = (settingsEntry.inject as () => { t: (key: keyof typeof zh) => string })()
    expect(props.t('config.storageTitle')).toBe('存储')
    const save = slots.find(options => options.name === 'conversation.chat.assistant-actions')!
    expect((save.inject as (id: string) => { settingsScope: unknown })('session-1').settingsScope).toBe(scope)
    setLocale('en')
    expect(props.t('config.storageTitle')).toBe('Storage')
    expect(slots.some(options => options.name === 'conversation.view')).toBe(false)
  })

  it('edits the whole configuration on the dsh-mnemon page under Plugins, not in Settings', () => {
    const { context, slots, scope } = workspaceContext({})
    expect(slots.some(options => options.name === 'settings.section')).toBe(false)
    const entry = slots.find(options => options.name === 'plugins.bundle.config')!
    expect(entry).toMatchObject({ key: 'dsh-mnemon', locale: 'mnemon' })
    expect(context.slots.register).toHaveBeenCalledWith(entry, MnemonSettingsHost)
    const props = (entry.inject as () => Record<string, unknown> & { t: (key: keyof typeof zh) => string })()
    expect(props).toMatchObject({
      scope, connection: context.connection, currentSession: context.uiSession.adapter.current, localeRuntime: context.locale,
    })
    expect(props.interactionScope).toBeInstanceOf(MnemonSettingsScope)
    expect(props.interactionScope).not.toBe(scope)
    expect(props.t('config.strategyTitle')).toBe('主策略')
    // Each component row DSH lists under the bundle opens that component's page, with the same services.
    const rows = slots.filter(options => options.name === 'plugins.row.config')
    expect(rows.map(row => row.key)).toEqual(STARTER_COMPONENT_ROWS.map(({ rowId }) => `dsh-mnemon#${rowId}`))
    for (const row of rows) {
      // Only the configuration declares the settings region; a row page renders what was registered there.
      expect(row).toMatchObject({ locale: 'mnemon' })
      expect(row.children).toBeUndefined()
      expect(context.slots.register).toHaveBeenCalledWith(row, MnemonComponentRowHost)
      const injected = (row.inject as () => Record<string, unknown>)()
      expect(injected).toMatchObject({ scope, component: STARTER_COMPONENT_ROWS.find(({ rowId }) => `dsh-mnemon#${rowId}` === row.key)!.packageName })
      expect(injected.renderContributed).toBeTypeOf('function')
    }
  })

  it('links the memory workspace and its configuration through the Plugins page', async () => {
    const { context, slots, scope, provide, workspaceStops } = workspaceContext({ displayMode: 'sidebar' })
    await vi.waitFor(() => expect(workspaceStops).toHaveLength(1))
    const action = slots.find(options => options.name === 'plugins.detail.actions')!
    expect(action).toMatchObject({ id: 'dsh-mnemon/open-workspace', locale: 'mnemon' })
    expect(context.slots.register).toHaveBeenCalledWith(action, MnemonPluginActions)
    const workspace = (action.inject as () => { workspace: MnemonActionSeat })().workspace
    expect(workspace.getSnapshot()).toBeTypeOf('function')

    const shell = slots.find(options => options.name === 'shell.overlay')!
    const configuration = (shell.inject as () => { configuration: MnemonActionSeat })().configuration
    expect(configuration.getSnapshot()).toBeUndefined()
    const openBundle = vi.fn()
    provide('pluginNavigation', { openBundle })
    configuration.getSnapshot()!()
    expect(openBundle).toHaveBeenCalledWith('dsh-mnemon')

    // Hiding the workspace withdraws its entry from the Plugins page.
    await scope.mutate([{ op: 'set', path: ['tabEnabled'], value: false }])
    expect(workspace.getSnapshot()).toBeUndefined()
  })

  it('re-reads Mnemon settings when DSH reports a newer revision of the mnemon entry', async () => {
    const { context, scope, provide } = workspaceContext({})
    await vi.waitFor(() => expect(scope.getSnapshot().status).toBe('ready'))
    let revision: number | undefined
    const listeners = new Set<() => void>()
    const form = { getSnapshot: () => ({ revision }), subscribe: (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener) } }
    const get = vi.fn(() => form)
    provide('configForms', { get })
    expect(get).toHaveBeenCalledWith('mnemon')
    const reads = () => context.connection.rpc.call.mock.calls.filter(([, endpoint]) => endpoint === 'get').length
    const before = reads()
    // The revision this page already holds is not a change.
    revision = scope.getSnapshot().revision
    for (const listener of listeners) listener()
    await Promise.resolve()
    expect(reads()).toBe(before)
    revision = 7
    for (const listener of listeners) listener()
    await vi.waitFor(() => expect(reads()).toBe(before + 2))
    expect(context.connection.rpc.call).toHaveBeenCalledWith('/dsh-mnemon-settings', 'get', { namespace: 'mnemon' }, expect.anything())
    expect(context.connection.rpc.call).toHaveBeenCalledWith('/dsh-mnemon-settings', 'get', { namespace: 'mnemon-ui' }, expect.anything())
  })

  it.each([undefined, 'sidebar'])('keeps one complete Sidebar for displayMode=%s with live visibility', async displayMode => {
    const { slots, scope, workspaceStops } = workspaceContext({ displayMode })
    await vi.waitFor(() => expect(workspaceStops).toHaveLength(1))
    expect(mountBetterSidebar).toHaveBeenCalledTimes(1)
    const shellEntry = slots.find(options => options.name === 'shell.overlay')!
    const shellProps = (shellEntry.inject as () => Record<string, unknown>)()
    expect(mountBetterSidebar.mock.calls[0]![2]).toBe(shellProps.betterSidebarSeat)
    const firstBetterSidebarStop = mountBetterSidebar.mock.results[0]!.value
    await scope.mutate([{ op: 'set', path: ['displayMode'], value: 'sidebar' }])
    expect(workspaceStops).toHaveLength(1)
    expect(workspaceStops[0]).not.toHaveBeenCalled()
    await scope.mutate([{ op: 'set', path: ['tabEnabled'], value: false }])
    await scope.mutate([{ op: 'set', path: ['tabEnabled'], value: false }])
    expect(workspaceStops[0]).toHaveBeenCalledOnce()
    expect(firstBetterSidebarStop).toHaveBeenCalledOnce()
    await scope.mutate([{ op: 'set', path: ['tabEnabled'], value: true }])
    expect(workspaceStops).toHaveLength(2)
    expect(mountBetterSidebar).toHaveBeenCalledTimes(2)
    expect(slots.some(options => options.name === 'conversation.view')).toBe(false)
  })

  it.each(['builtin', 'buildin'])('mounts displayMode=%s through the owning session and the same Source page slot', async displayMode => {
    const { context, slots, scope, workspaceStops } = workspaceContext({ displayMode })
    await vi.waitFor(() => expect(workspaceStops).toHaveLength(1))
    expect(slots.some(options => options.name === 'shell.overlay')).toBe(false)
    expect(mountBetterSidebar).not.toHaveBeenCalled()
    const entry = slots.find(options => options.name === 'conversation.view')!
    expect(entry).toMatchObject({ id: 'mnemon', order: 30, children: { 'mnemon.source.page': { kind: 'list', scope: 'root' } } })
    expect(context.slots.register).toHaveBeenCalledWith(entry, MnemonBuiltinWorkspaceHost)
    const props = (entry.inject as (sessionId: string) => Record<string, unknown>)('session-2')
    expect(props).toMatchObject({ connection: context.connection, settingsScope: scope, sessionId: 'session-2', localeRuntime: context.locale })
    for (const key of ['workspaceId', 'workspaceSelection', 'sessions', 'workspaces', 'onClose']) expect(props).not.toHaveProperty(key)

    await scope.mutate([{ op: 'set', path: ['displayMode'], value: 'builtin' }])
    await scope.mutate([{ op: 'set', path: ['storageScope'], value: 'workspace' }])
    expect(workspaceStops).toHaveLength(1)
    expect(workspaceStops[0]).not.toHaveBeenCalled()
    await scope.mutate([{ op: 'set', path: ['displayMode'], value: 'sidebar' }])
    expect(workspaceStops[0]).toHaveBeenCalledOnce()
    expect(workspaceStops).toHaveLength(2)
    expect(mountBetterSidebar).toHaveBeenCalledTimes(1)
    expect(slots.at(-1)).toMatchObject({ name: 'shell.overlay' })
    await scope.mutate([{ op: 'set', path: ['displayMode'], value: 'builtin' }])
    expect(workspaceStops[1]).toHaveBeenCalledOnce()
    expect(mountBetterSidebar.mock.results[0]!.value).toHaveBeenCalledOnce()
    expect(workspaceStops).toHaveLength(3)
    expect(slots.at(-1)).toMatchObject({ name: 'conversation.view' })
    await scope.mutate([{ op: 'set', path: ['tabEnabled'], value: false }])
    await scope.mutate([{ op: 'set', path: ['tabEnabled'], value: false }])
    expect(workspaceStops[2]).toHaveBeenCalledOnce()
    await scope.mutate([{ op: 'set', path: ['tabEnabled'], value: true }])
    expect(workspaceStops).toHaveLength(4)
    expect(slots.at(-1)).toMatchObject({ name: 'conversation.view' })
  })

  it('does not flash an entry while persisted visibility is loading', async () => {
    const ready = Promise.withResolvers<Record<string, unknown>>()
    const { scope, workspaceStops } = workspaceContext({}, () => ready.promise)
    expect(workspaceStops).toHaveLength(0)
    ready.resolve({ displayMode: 'buildin', tabEnabled: false })
    await vi.waitFor(() => expect(scope.getSnapshot().status).toBe('ready'))
    expect(workspaceStops).toHaveLength(0)
    await scope.mutate([{ op: 'set', path: ['tabEnabled'], value: true }])
    expect(workspaceStops).toHaveLength(1)
  })

  it('does not flash Sidebar before a saved Builtin preference loads', async () => {
    const ready = Promise.withResolvers<Record<string, unknown>>()
    const { slots, workspaceStops } = workspaceContext({}, () => ready.promise)
    expect(workspaceStops).toHaveLength(0)
    ready.resolve({ displayMode: 'builtin' })
    await vi.waitFor(() => expect(workspaceStops).toHaveLength(1))
    expect(slots.some(options => options.name === 'shell.overlay')).toBe(false)
    expect(slots.some(options => options.name === 'conversation.view')).toBe(true)
  })

  it('keeps the default Sidebar available when settings cannot be loaded', async () => {
    const { slots, scope, workspaceStops } = workspaceContext({}, async () => { throw new Error('offline') })
    await vi.waitFor(() => expect(scope.getSnapshot().status).toBe('unavailable'))
    expect(workspaceStops).toHaveLength(1)
    expect(slots.some(options => options.name === 'shell.overlay')).toBe(true)
  })
})
