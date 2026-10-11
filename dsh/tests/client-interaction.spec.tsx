// @vitest-environment jsdom
import { SlotCore } from '@deepseek-ai/dsh-client-ui-slots'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitFor } from '@testing-library/react'
import { apply } from '../src/client/index.ts'
import { consumeMnemonAnchor, dispatchMnemonAnchor } from '../src/client/anchor.ts'

const mountedEffects: Array<() => void> = []

interface SlotOptions {
  name: string
  key?: string
  id?: string
  order?: number
  priority?: number
  label?: unknown
  locale?: string
  children?: Record<string, unknown>
  inject?: (...args: unknown[]) => Record<string, unknown>
}

function makeCtx(initialValue: unknown, coreValue: Record<string, unknown> = {}) {
  const core = new SlotCore()
  // Exercise the published registry with the DSH 0.1.7 slot declarations.
  const registerSlot = (options: SlotOptions, component: unknown = () => null): (() => void) =>
    (core.register as (options: SlotOptions, component: unknown) => () => void)(options, component)
  const disposeOwner = registerSlot({
    name: 'root',
    children: {
      'conversation.chat.turnTail': { kind: 'list', scope: 'session' },
      'conversation.chat.assistant-actions': { kind: 'list', scope: 'session' },
      'conversation.session.header.lineage': { kind: 'single', scope: 'session' },
      'conversation.view': { kind: 'list', scope: 'session' },
      'shell.overlay': { kind: 'list', scope: 'root' },
      'plugins.bundle.config': { kind: 'keyed', scope: 'root' },
      'plugins.detail.actions': { kind: 'list', scope: 'root' },
    },
  })
  const injects: string[] = []
  /** Registrations that have not been disposed yet. */
  const active = new Set<SlotOptions>()
  const registeredOptions: SlotOptions[] = []
  let uiValue = initialValue as Record<string, unknown>
  let revision = 1
  const localeSnapshot = { active: 'zh' as const, locales: [] as const, revision: 0 }
  // The current conversation is a listed session unless a test starts from an unsent draft.
  let sessionList = { current: 'session-a', byId: { 'session-a': { id: 'session-a' } } as Record<string, unknown> }
  const sessionListeners = new Set<() => void>()
  const setSessions = (byId: Record<string, unknown>): void => {
    sessionList = { ...sessionList, byId }
    for (const listener of [...sessionListeners]) listener()
  }

  const ctx = {
    get: vi.fn(() => undefined),
    on: vi.fn(() => () => {}),
    // Plugins page navigation and the DSH settings mirror are not provided here.
    inject: vi.fn(),
    sessions: { list: {
      getSnapshot: () => sessionList,
      subscribe: (listener: () => void) => { sessionListeners.add(listener); return () => { sessionListeners.delete(listener) } },
    } },
    uiSession: { adapter: { current: { getSnapshot: () => ({ key: 'session-a' }), subscribe: () => () => {} } } },
    layout: { selectPanel: vi.fn() },
    slots: {
      inject: (slot: string, factory: () => unknown) => {
        injects.push(slot)
        // Like DSH, wait for the declaration: this root leaves the native Sidebar seats undeclared.
        let dispose = core.specDynamic(slot) === undefined ? undefined : factory() as (() => void) | undefined
        const disposer = () => { dispose?.(); dispose = undefined }
        injectDisposers.set(slot, disposer)
        return disposer
      },
      entries: (slot: string) => core.entries(slot),
      subscribe: (slot: string, listener: () => void) => core.subscribe(slot, listener),
      register: (options: SlotOptions, component: unknown) => {
        const dispose = registerSlot(options, component)
        registeredOptions.push(options)
        active.add(options)
        return () => { dispose(); active.delete(options) }
      },
    },
    connection: {
      rpc: {
        call: vi.fn(async (channel: string, endpoint: string, rawPayload: unknown) => {
          if (channel === '/dsh-mnemon-settings') {
            const payload = rawPayload as { namespace?: string; ops?: Array<{ op: string; path: string[]; value?: unknown }> }
            const namespace = payload.namespace
            if (endpoint === 'mutate' && namespace === 'mnemon-ui') {
              for (const op of payload.ops ?? []) {
                if (op.op === 'set') uiValue = { ...uiValue, [op.path[0]!]: op.value }
                else {
                  uiValue = { ...uiValue }
                  delete uiValue[op.path[0]!]
                }
              }
              revision += 1
            }
            return { ok: true, value: { status: 'ready', value: namespace === 'mnemon-ui' ? uiValue : coreValue, base: {}, user: namespace === 'mnemon-ui' ? uiValue : coreValue, revision, writable: true, mode: 'host' } }
          }
          return { ok: false, error: { code: 'internal', message: 'unexpected', details: {} } }
        }),
      },
      isLoopback: true,
    },
    locale: {
      register: vi.fn(() => () => {}),
      bind: vi.fn(() => (key: string) => key),
      getSnapshot: vi.fn(() => localeSnapshot),
      subscribe: vi.fn(() => () => {}),
    },
    effect: vi.fn((callback: () => unknown) => {
      const dispose = callback()
      if (typeof dispose === 'function') {
        effectDisposers.push(dispose as () => void)
      }
      return () => {}
    }),
  }

  const injectDisposers = new Map<string, () => void>()
  const effectDisposers: Array<() => void> = []
  const activeRegistrations = () => [...active].map(options => options.key ?? options.id ?? options.name)
  const dispose = () => {
    for (const cleanup of effectDisposers.splice(0).reverse()) cleanup()
    for (const cleanup of [...injectDisposers.values()].reverse()) cleanup()
    injectDisposers.clear()
  }
  mountedEffects.push(() => { dispose(); disposeOwner() })
  return { ctx, core, registerSlot, dispose, injects, injectDisposers, registeredOptions, activeRegistrations, effectDisposers, setSessions }
}

describe('interaction surfaces binding', () => {
  afterEach(() => {
    for (const dispose of mountedEffects.splice(0).reverse()) dispose()
    document.body.replaceChildren()
    vi.restoreAllMocks()
  })

  it('keeps a stable turn-tail list registration across settings and client reload', async () => {
    const { ctx, core, registerSlot, registeredOptions, dispose } = makeCtx({})
    const slot = 'conversation.chat.turnTail'
    const peerIds = ['other-plugin/first-tail', 'other-plugin/second-tail']
    for (const id of peerIds) registerSlot({ name: slot, id, priority: 1 })
    const peers = [...core.entriesOfSlot(slot)]
    const ids = () => core.entriesOfSlot(slot).map(entry => entry.options.id)

    apply(ctx)
    // The default-on entry must wait for the Host settings snapshot.
    expect(core.entriesOfSlot(slot)).toEqual(peers)
    await waitFor(() => expect(ids()).toEqual(['dsh-mnemon/turn-tail', ...peerIds]))
    // A list entry renders for every Turn; the component itself waits for the closing Turn.
    expect(core.entriesOfSlot(slot)[0]!.select).toBeUndefined()

    const settings = registeredOptions.find(options => options.name === 'plugins.bundle.config')?.inject?.() as {
      interactionScope: { mutate: (ops: unknown[]) => Promise<void> }
    }
    await settings.interactionScope.mutate([{ op: 'set', path: ['turnBar'], value: false }])
    expect(core.entriesOfSlot(slot)).toEqual(peers)
    await settings.interactionScope.mutate([{ op: 'set', path: ['turnBar'], value: true }])
    expect(ids()).toEqual(['dsh-mnemon/turn-tail', ...peerIds])
    expect(core.entries(slot)).toHaveLength(3)

    dispose()
    expect(core.entriesOfSlot(slot)).toEqual(peers)
    apply(ctx)
    await waitFor(() => expect(ids()).toEqual(['dsh-mnemon/turn-tail', ...peerIds]))
    expect(core.entries(slot)).toHaveLength(3)
    dispose()
    expect(core.entriesOfSlot(slot)).toEqual(peers)
  })

  it('registers both remaining interaction surfaces by default', async () => {
    const { ctx, injects, activeRegistrations } = makeCtx({})
    apply(ctx)
    // Sidebar has a DSH-owned seat even before any session exists.
    expect(injects).toContain('plugins.bundle.config')
    await waitFor(() => expect(injects).toContain('shell.overlay'))
    await waitFor(() => expect(activeRegistrations()).toEqual(expect.arrayContaining(['dsh-mnemon/turn-tail', 'mnemon-save'])))
  })

  it('registers explicitly enabled surfaces after settings load', async () => {
    const { ctx, activeRegistrations } = makeCtx({ toolviews: true, turnBar: true, saveAction: true })
    apply(ctx)
    await waitFor(() => expect(activeRegistrations()).toEqual(expect.arrayContaining(['dsh-mnemon/turn-tail', 'mnemon-save'])))
    expect(activeRegistrations()).toEqual(expect.arrayContaining(['dsh-mnemon/turn-tail', 'mnemon-save']))
  })

  it('registers only the enabled surfaces when toggles are mixed', async () => {
    const { ctx, activeRegistrations } = makeCtx({ toolviews: true, turnBar: false, saveAction: true })
    apply(ctx)
    await waitFor(() => expect(activeRegistrations()).toEqual(expect.arrayContaining(['mnemon-save'])))
    expect(activeRegistrations()).toEqual(expect.arrayContaining(['mnemon-save']))
    expect(activeRegistrations()).not.toContain('dsh-mnemon/turn-tail')
  })

  it('opens the default sidebar for a conversation anchor', async () => {
    const { ctx, injects, activeRegistrations } = makeCtx({})
    const tab = document.createElement('button')
    tab.setAttribute('role', 'tab')
    tab.textContent = 'tab.label'
    const clicked = vi.fn()
    tab.addEventListener('click', clicked)
    const conversation = document.createElement('div')
    conversation.dataset.slot = 'main.conversation'
    conversation.append(tab)
    document.body.append(conversation)

    apply(ctx)
    await waitFor(() => expect(injects).toContain('shell.overlay'))
    dispatchMnemonAnchor({ page: 'documents/library', sessionId: 'session-a' })

    expect(clicked).not.toHaveBeenCalled()
    expect(document.documentElement.hasAttribute('data-dsh-mnemon-active')).toBe(true)
    expect(injects).not.toContain('conversation.view')
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'documents/library' })
  })

  it('opens the Builtin tab only for the current session, follows locale labels, and removes its listener on disposal', async () => {
    const { ctx, injects, activeRegistrations, effectDisposers } = makeCtx({}, { displayMode: 'builtin' })
    let label = '记忆系统'
    ctx.locale.bind.mockImplementation(() => () => label)
    const tab = document.createElement('button')
    tab.setAttribute('role', 'tab')
    tab.textContent = label
    const clicked = vi.fn()
    tab.addEventListener('click', clicked)
    const conversation = document.createElement('div')
    conversation.dataset.slot = 'main.conversation'
    conversation.append(tab)
    document.body.append(conversation)
    apply(ctx)
    await waitFor(() => expect(injects).toContain('conversation.view'))
    expect(activeRegistrations().filter(id => id === 'mnemon')).toHaveLength(1)
    expect(activeRegistrations()).toContain('dsh-mnemon')

    dispatchMnemonAnchor({ page: 'documents/library', sessionId: 'session-b' })
    expect(clicked).not.toHaveBeenCalled()
    expect(consumeMnemonAnchor('session-b')).toMatchObject({ page: 'documents/library' })
    dispatchMnemonAnchor({ page: 'memory-spaces/remember', seed: 'Scoped candidate', sessionId: 'session-a' })
    expect(clicked).toHaveBeenCalledTimes(1)
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'memory-spaces/remember', seed: 'Scoped candidate' })
    expect(document.documentElement.hasAttribute('data-dsh-mnemon-active')).toBe(false)

    label = 'Memory System'
    tab.textContent = label
    dispatchMnemonAnchor({ page: 'runtime/entries', sessionId: 'session-a' })
    expect(clicked).toHaveBeenCalledTimes(2)
    consumeMnemonAnchor('session-a')
    for (const dispose of effectDisposers) dispose()
    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-a' })
    expect(clicked).toHaveBeenCalledTimes(2)
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'status' })
  })

  it('offers the Builtin workspace from the Plugins page only for a listed conversation', async () => {
    const { ctx, injects, registeredOptions, setSessions } = makeCtx({}, { displayMode: 'builtin' })
    setSessions({})
    apply(ctx)
    await waitFor(() => expect(injects).toContain('conversation.view'))
    const actions = registeredOptions.find(options => options.id === 'dsh-mnemon/open-workspace')!
    const workspace = actions.inject!().workspace as { getSnapshot(): (() => void) | undefined }
    // An unsent draft has no conversation tabs to open.
    expect(workspace.getSnapshot()).toBeUndefined()
    setSessions({ 'session-a': { id: 'session-a' } })
    expect(workspace.getSnapshot()).toBeTypeOf('function')
    setSessions({})
    expect(workspace.getSnapshot()).toBeUndefined()
  })

  it('keeps Builtin anchors pending without mounting or opening a hidden entry', async () => {
    const { ctx, activeRegistrations } = makeCtx({}, { displayMode: 'builtin', tabEnabled: false })
    apply(ctx)
    await waitFor(() => expect(activeRegistrations()).toContain('mnemon-save'))
    expect(activeRegistrations().filter(id => id === 'mnemon')).toHaveLength(0)
    expect(activeRegistrations()).toContain('dsh-mnemon') // the configuration page only
    dispatchMnemonAnchor({ page: 'documents/library', sessionId: 'session-a' })
    expect(document.documentElement.hasAttribute('data-dsh-mnemon-active')).toBe(false)
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'documents/library' })
  })

  it('registers and disposes interaction surfaces when mnemon-ui changes live', async () => {
    const { ctx, registeredOptions, activeRegistrations } = makeCtx({})
    apply(ctx)
    await waitFor(() => expect(registeredOptions.some(options => options.name === 'plugins.bundle.config')).toBe(true))
    const settingsEntry = registeredOptions.find(options => options.name === 'plugins.bundle.config')
    const injected = settingsEntry?.inject?.() as { interactionScope?: { mutate: (ops: unknown[]) => Promise<void> } } | undefined
    if (injected?.interactionScope === undefined) throw new Error('mnemon-ui settings scope was not injected')

    await injected.interactionScope.mutate([{ op: 'set', path: ['turnBar'], value: true }])
    await waitFor(() => expect(activeRegistrations()).toContain('dsh-mnemon/turn-tail'))

    await injected.interactionScope.mutate([{ op: 'set', path: ['turnBar'], value: false }])
    await waitFor(() => expect(activeRegistrations()).not.toContain('dsh-mnemon/turn-tail'))
  })
})
