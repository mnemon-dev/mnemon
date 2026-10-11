// @vitest-environment jsdom
import type { StandardSourceBinding } from '@deepseek-ai/dsh-client-ui-slots'
import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ComponentType } from 'react'

vi.mock('../src/client/MnemonWorkbench.tsx', () => ({
  MnemonWorkbench: ({ sessionId, workspaceSelection }: { sessionId?: string; workspaceSelection?: { effectiveWorkspaceId?: string } }) =>
    <div data-testid="workspace" data-session={sessionId} data-effective-workspace={workspaceSelection?.effectiveWorkspaceId} />,
}))
vi.mock('../src/client/MnemonSettingsCard.tsx', () => ({
  MnemonSettingsCard: ({ sessionId, workspaceId, workspaceLabel }: { sessionId?: string; workspaceId?: string; workspaceLabel?: string }) =>
    <div data-testid="settings" data-session={sessionId} data-workspace={workspaceId} data-label={workspaceLabel} />,
}))
vi.mock('../src/client/better-sidebar.tsx', () => ({ mountBetterSidebarTab: () => () => {} }))

import { apply } from '../src/client/index.ts'
import { consumeMnemonAnchor, dispatchMnemonAnchor } from '../src/client/anchor.ts'
import { MnemonBuiltinWorkspaceHost, MnemonWorkspaceHost } from '../src/client/workspace-mount.tsx'
import type { Config } from '../src/host/protocol.ts'
import { settingsScope } from './helpers/settings-scope.ts'

const disposers: Array<() => void> = []
afterEach(() => {
  cleanup()
  for (const dispose of disposers.splice(0).reverse()) dispose()
  document.body.replaceChildren()
  consumeMnemonAnchor('session-a')
  consumeMnemonAnchor('session-b')
  vi.restoreAllMocks()
})

function observable<T>(initial: T) {
  let value = initial
  const listeners = new Set<() => void>()
  const store = {
    getSnapshot() { expect(this).toBe(store); return value },
    subscribe(listener: () => void) {
      expect(this).toBe(store)
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
    publish(next: T) { value = next; for (const listener of listeners) listener() },
    get subscribers() { return listeners.size },
  }
  return store
}

function binding(key: string | undefined): StandardSourceBinding {
  return { key, hooks: {}, keyedHooks: {}, props: {} }
}

function fixture() {
  const current = observable(binding('session-a'))
  const catalog = observable<{ byId: Record<string, { cwd?: string }>; current?: string }>({
    byId: { 'session-a': { cwd: '/tmp/workspace-a' }, 'session-b': { cwd: '/tmp/workspace-b' } },
  })
  const workspaces = observable({ items: [
    { workspaceId: 'workspace-a', title: 'Workspace A', path: '/tmp/workspace-a' },
    { workspaceId: 'workspace-b', title: 'Workspace B', path: '/tmp/workspace-b' },
  ] })
  const locale = observable({ active: 'en' as const, locales: [], revision: 0 })
  const entries: Array<{ options: { name: string; inject?: () => Record<string, unknown> }; component: ComponentType<Record<string, unknown>> }> = []
  const ctx = {
    uiSession: { adapter: { current } },
    sessions: { list: catalog },
    workspaces: { list: workspaces },
    // One runtime object, as in DSH: its store methods must keep their receiver.
    locale: Object.assign(locale, { register: () => () => {}, bind: () => (key: string) => key }),
    connection: { rpc: { call: vi.fn(async () => ({ ok: true, value: { status: 'ready', value: { displayMode: 'builtin' }, writable: true, mode: 'host' } })) }, isLoopback: true },
    effect(callback: () => unknown) { const dispose = callback(); if (typeof dispose === 'function') disposers.push(dispose as () => void) },
    // Plugins page navigation and the DSH settings mirror are not provided here.
    inject: () => {},
    layout: { selectPanel: () => {} },
    slots: {
      inject(_name: string, factory: () => (() => void)) { const dispose = factory(); disposers.push(dispose); return dispose },
      entries: () => [],
      entriesOfSlot: () => [],
      getVersion: () => 0,
      subscribe: () => () => {},
      register(options: unknown, component: unknown) { entries.push({ options, component } as typeof entries[number]); return () => {} },
    },
  }
  const settings = settingsScope<Config>({ status: 'ready', value: {}, writable: true, mode: 'host' })
  const workspaceProps = {
    connection: ctx.connection as never,
    settingsScope: settings,
    sessions: ctx.sessions as never,
    workspaces: ctx.workspaces as never,
    currentSession: current,
    localeRuntime: locale as never,
    sourcePageDirectory: { getSnapshot: () => [] as const, subscribe: () => () => {} },
    t: (key: string) => key,
  }
  function renderSettings() {
    apply(ctx)
    const entry = entries.find(candidate => candidate.options.name === 'plugins.bundle.config')!
    // A root slot caches its injected props: session updates must come from
    // subscriptions inside the mounted component, not another inject call.
    const props = entry.options.inject!()
    const Component = entry.component
    // The Plugins page renders a bundle's configuration as its `page` view.
    return render(<Component {...props} view="page" />)
  }
  return { ctx, current, catalog, workspaces, workspaceProps, renderSettings }
}

describe('published UI session binding', () => {
  it('updates Sidebar session scope when the main binding changes with an unchanged catalog', () => {
    const f = fixture()
    const catalogSnapshot = f.catalog.getSnapshot()
    const view = render(<MnemonWorkspaceHost {...f.workspaceProps} />)
    expect(screen.getByTestId('workspace').dataset.session).toBe('session-a')
    expect(screen.getByTestId('workspace').dataset.effectiveWorkspace).toBe('workspace-a')

    act(() => f.current.publish(binding('session-b')))
    expect(f.catalog.getSnapshot()).toBe(catalogSnapshot)
    expect(screen.getByTestId('workspace').dataset.session).toBe('session-b')
    expect(screen.getByTestId('workspace').dataset.effectiveWorkspace).toBe('workspace-b')
    act(() => f.current.publish(binding(undefined)))
    expect(screen.getByTestId('workspace').dataset.session).toBeUndefined()
    expect(screen.getByTestId('workspace').dataset.effectiveWorkspace).toBeUndefined()

    view.unmount()
    expect(f.current.subscribers).toBe(0)
    expect(f.catalog.subscribers).toBe(0)
    expect(f.workspaces.subscribers).toBe(0)
  })

  it('keeps an explicit workspace seat and Builtin owner separate from the main binding', () => {
    const f = fixture()
    const view = render(<MnemonWorkspaceHost {...f.workspaceProps} sessionId="session-b" />)
    expect(screen.getByTestId('workspace').dataset.session).toBe('session-b')
    act(() => f.current.publish(binding(undefined)))
    expect(screen.getByTestId('workspace').dataset.session).toBe('session-b')
    view.unmount()

    render(<MnemonBuiltinWorkspaceHost {...f.workspaceProps} sessionId="session-b" />)
    act(() => f.current.publish(binding('session-a')))
    expect(screen.getByTestId('workspace').dataset.session).toBe('session-b')
  })

  it('does not replace an explicitly absent session scope with the main session', () => {
    const f = fixture()
    render(<MnemonWorkspaceHost {...f.workspaceProps} sessionId={undefined} />)
    expect(screen.getByTestId('workspace').dataset.session).toBeUndefined()
    act(() => f.current.publish(binding('session-b')))
    expect(screen.getByTestId('workspace').dataset.session).toBeUndefined()
  })

  it('updates cached Settings props from the binding, catalog, and workspace sources', () => {
    const f = fixture()
    const view = f.renderSettings()
    expect(screen.getByTestId('settings').dataset).toMatchObject({ session: 'session-a', workspace: 'workspace-a', label: 'Workspace A' })
    act(() => f.current.publish(binding('session-b')))
    expect(screen.getByTestId('settings').dataset).toMatchObject({ session: 'session-b', workspace: 'workspace-b', label: 'Workspace B' })
    act(() => f.workspaces.publish({ items: [{ workspaceId: 'workspace-b', title: 'Renamed B', path: '/tmp/workspace-b' }] }))
    expect(screen.getByTestId('settings').dataset.label).toBe('Renamed B')
    view.unmount()
    expect(f.current.subscribers).toBe(0)
    expect(f.catalog.subscribers).toBe(0)
    expect(f.workspaces.subscribers).toBe(0)
  })

  it('waits for a selected session workspace and never borrows another workspace while its catalog loads', () => {
    const f = fixture()
    f.current.publish(binding('session-b'))
    f.catalog.publish({ byId: { 'session-a': { cwd: '/tmp/workspace-a' } }, current: 'session-a' })
    f.renderSettings()
    expect(screen.getByTestId('settings').dataset.session).toBe('session-b')
    expect(screen.getByTestId('settings').dataset.workspace).toBeUndefined()
    act(() => f.catalog.publish({ byId: { 'session-b': { cwd: '/tmp/workspace-b' } }, current: 'session-a' }))
    expect(screen.getByTestId('settings').dataset.workspace).toBe('workspace-b')
    act(() => f.current.publish(binding(undefined)))
    expect(screen.getByTestId('settings').dataset.session).toBeUndefined()
  })

  it('uses the live main binding for Builtin anchor matching and removes the listener on disposal', async () => {
    const f = fixture()
    const button = document.createElement('button')
    button.setAttribute('role', 'tab')
    button.textContent = 'tab.label'
    const click = vi.fn()
    button.addEventListener('click', click)
    const conversation = document.createElement('div')
    conversation.dataset.slot = 'main.conversation'
    conversation.append(button)
    document.body.append(conversation)
    apply(f.ctx)
    await act(async () => {})
    f.current.publish(binding('session-b'))
    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-a' })
    expect(click).not.toHaveBeenCalled()
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'status', sessionId: 'session-a' })
    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-b' })
    expect(click).toHaveBeenCalledTimes(1)
    for (const dispose of disposers.splice(0).reverse()) dispose()
    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-b' })
    expect(click).toHaveBeenCalledTimes(1)
  })

  it('leaves Builtin anchors pending for ambiguous tabs or an absent main session', async () => {
    const f = fixture()
    const first = document.createElement('button')
    first.setAttribute('role', 'tab')
    first.textContent = 'tab.label'
    const second = first.cloneNode(true) as HTMLButtonElement
    const firstClick = vi.fn()
    const secondClick = vi.fn()
    first.addEventListener('click', firstClick)
    second.addEventListener('click', secondClick)
    const conversation = document.createElement('div')
    conversation.dataset.slot = 'main.conversation'
    conversation.append(first, second)
    document.body.append(conversation)
    apply(f.ctx)
    await act(async () => {})

    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-a' })
    expect(firstClick).not.toHaveBeenCalled()
    expect(secondClick).not.toHaveBeenCalled()
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'status' })

    second.remove()
    f.current.publish(binding(undefined))
    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-a' })
    expect(firstClick).not.toHaveBeenCalled()
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'status' })

    f.current.publish(binding('session-a'))
    first.disabled = true
    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-a' })
    expect(firstClick).not.toHaveBeenCalled()
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'status' })
  })

  it('never opens a foreign label-matching tab outside the main conversation outlet', async () => {
    const f = fixture()
    const foreign = document.createElement('button')
    foreign.setAttribute('role', 'tab')
    foreign.textContent = 'tab.label'
    const foreignClick = vi.fn()
    foreign.addEventListener('click', foreignClick)
    const conversation = document.createElement('div')
    conversation.dataset.slot = 'main.conversation'
    document.body.append(foreign, conversation)
    apply(f.ctx)
    await act(async () => {})

    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-a' })
    expect(foreignClick).not.toHaveBeenCalled()
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'status' })

    const own = foreign.cloneNode(true) as HTMLButtonElement
    const ownClick = vi.fn()
    own.addEventListener('click', ownClick)
    conversation.append(own)
    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-a' })
    expect(foreignClick).not.toHaveBeenCalled()
    expect(ownClick).toHaveBeenCalledTimes(1)

    const otherConversation = conversation.cloneNode(true)
    document.body.append(otherConversation)
    dispatchMnemonAnchor({ page: 'status', sessionId: 'session-a' })
    expect(ownClick).toHaveBeenCalledTimes(1)
    expect(consumeMnemonAnchor('session-a')).toMatchObject({ page: 'status' })
  })
})
