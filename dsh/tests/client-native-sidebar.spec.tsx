// @vitest-environment jsdom
import { SlotCore } from '@deepseek-ai/dsh-client-ui-slots'
import { Context } from '@deepseek-ai/cordis'
import { createContext, createElement, useContext, useSyncExternalStore, type ComponentType } from 'react'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../src/client/MnemonWorkbench.tsx', () => ({
  MnemonWorkbench: ({ active, onClose, renderSlot }: { active?: boolean; onClose?: () => void; renderSlot?: (name: string, props: unknown, options: unknown) => unknown }) => <div data-testid="native-memory" data-active={active}>
    <input aria-label="Retained editor" defaultValue="draft" />
    <button onClick={onClose}>Return to conversation</button>
    {renderSlot?.('mnemon.source.page', {}, {}) as never}
  </div>,
}))

import { MnemonNativeSidebarSeat } from '../src/client/native-sidebar-seat.ts'
import { MNEMON_MAIN_PANEL_ID, mountMnemonSidebarNavigation } from '../src/client/native-sidebar.tsx'
import { MnemonSidebarWorkspaceHost } from '../src/client/workspace-mount.tsx'
import { MnemonWorkspaceController } from '../src/client/workspace-controller.ts'
import { inject } from '../src/client/index.ts'

const releases: Array<() => void> = []
afterEach(() => {
  act(() => { for (const release of releases.splice(0).reverse()) release() })
  cleanup()
  document.body.replaceChildren()
  for (const name of ['mnemon', 'ssh', 'taskboard']) document.documentElement.removeAttribute(`data-dsh-${name}-active`)
})

const sourceOwner = createContext('missing-owner')
function SourceProbe() { return <span>{useContext(sourceOwner)}</span> }

/** Exercise the real released slot ledger with a panel owner obeying its public contract. */
function fixture(initiallyDeclared = true) {
  const core = new SlotCore()
  const controller = new MnemonWorkspaceController()
  const seat = new MnemonNativeSidebarSeat()
  let activePanel: string | null = null
  let label = 'Memory System'
  const panelListeners = new Set<() => void>()
  const subscribePanel = (listener: () => void) => { panelListeners.add(listener); return () => panelListeners.delete(listener) }
  const getPanel = () => activePanel
  const selectPanel = vi.fn((id: string | null) => {
    if (id !== null && !core.entriesOfSlot('main').some(entry => entry.options.key === id)) throw new Error('Unknown main panel')
    activePanel = id
    for (const listener of panelListeners) listener()
  })
  const layout = { selectPanel }
  const register = core.register.bind(core)
  const declare = () => register({ name: 'root', children: {
    main: { kind: 'keyed', scope: 'root' },
    'sidebar.panellist': { kind: 'list', scope: 'root' },
  } } as never, (() => null) as never)
  let stopOwner = initiallyDeclared ? declare() : undefined
  const ctx = {
    layout,
    slots: {
      register,
      inject: (name: string, factory: () => () => void) => {
        let stop: (() => void) | undefined
        const sync = () => {
          if (core.specDynamic(name) !== undefined) stop ??= factory()
          else { const previous = stop; stop = undefined; previous?.() }
        }
        const unsubscribe = core.subscribeDeclaration(name, sync)
        try { sync() } catch (error) { unsubscribe(); throw error }
        return () => { unsubscribe(); stop?.() }
      },
    },
    locale: { subscribe: () => () => {} },
  }
  const mount = () => mountMnemonSidebarNavigation(ctx as never, () => label, controller, seat)
  const navigation = mount()
  releases.push(() => { navigation.dispose(); stopOwner?.() })

  function NativeFrame({ collapsed = false }: { collapsed?: boolean }) {
    useSyncExternalStore(listener => core.subscribe('sidebar.panellist', listener), () => core.getVersion('sidebar.panellist'))
    useSyncExternalStore(listener => core.subscribe('main', listener), () => core.getVersion('main'))
    const selected = useSyncExternalStore(subscribePanel, getPanel)
    const main = core.entriesOfSlot('main').find(entry => entry.options.key === selected)
    return <>
      <nav aria-label="Native panels">
        <button onClick={() => selectPanel(null)}>New Session</button>
        {core.entriesOfSlot('sidebar.panellist').map(entry => {
          const title = typeof entry.options.label === 'function' ? entry.options.label() : entry.options.label
          const Icon = entry.component as ComponentType<{ size: number; active: boolean }>
          return <button key={entry.options.id} aria-label={title as string} aria-current={selected === entry.options.id ? 'page' : undefined} onClick={() => selectPanel(entry.options.id!)}>
            <Icon size={collapsed ? 18 : 16} active={selected === entry.options.id} />
            {!collapsed && <span>{title as string}</span>}
          </button>
        })}
      </nav>
      <main data-testid="native-main">
        {main === undefined ? <p>Conversation</p> : createElement(main.component as ComponentType, { key: selected, ...main.inject?.() })}
      </main>
    </>
  }
  const locale = { active: 'en', locales: [], revision: 0 }
  const sessions = { byId: {} }
  const workspaces = { items: [] }
  const current = { key: undefined, hooks: {}, keyedHooks: {}, props: {} }
  function Host() {
    return <sourceOwner.Provider value="shell-source-owner"><MnemonSidebarWorkspaceHost
      controller={controller} nativeSidebarSeat={seat} navigation={navigation}
      connection={{} as never} settingsScope={{} as never}
      sessions={{ list: { getSnapshot: () => sessions, subscribe: () => () => {} } } as never}
      workspaces={{ list: { getSnapshot: () => workspaces, subscribe: () => () => {} } } as never}
      currentSession={{ getSnapshot: () => current, subscribe: () => () => {} }}
      localeRuntime={{ getSnapshot: () => locale, subscribe: () => () => {} } as never}
      sourcePageDirectory={{ getSnapshot: () => [], subscribe: () => () => {} }}
      renderSlot={() => <SourceProbe />} t={() => label}
    /></sourceOwner.Provider>
  }
  const registerPeer = (id = 'plugins') => {
    const stopMain = register({ name: 'main', key: id }, () => <p>{id} panel</p>)
    const stopIcon = register({ name: 'sidebar.panellist', id, label: id, order: 0 }, () => <svg />)
    return () => { stopIcon(); stopMain() }
  }
  return {
    core, ctx, controller, seat, navigation, mount, selectPanel, getPanel, NativeFrame, Host, registerPeer,
    rename: (value: string) => { label = value },
    declare: () => { stopOwner = declare() },
    removeOwner: () => { stopOwner?.(); stopOwner = undefined },
  }
}

describe('native Sidebar panel integration', () => {
  it('authorizes native layout access through the real Cordis Client inject contract', async () => {
    const f = fixture()
    f.navigation.dispose()
    const root = new Context()
    let navigation: ReturnType<typeof mountMnemonSidebarNavigation> | undefined
    const failures: unknown[] = []
    for (const name of inject) root.provide(name, {})
    root.set('slots', f.ctx.slots)
    root.set('locale', f.ctx.locale)
    // Providing a service does not authorize child access: the exported Client
    // inject list must also contain layout, as on the actual WebUI runtime.
    if (inject.includes('layout')) root.set('layout', f.ctx.layout)
    else root.provide('layout', f.ctx.layout)
    try {
      await root.plugin({ inject, apply(ctx: Context) {
        try { navigation = mountMnemonSidebarNavigation(ctx as never, () => 'Memory System', f.controller, f.seat) }
        catch (error) { failures.push(error) }
      } })
      expect(failures).toEqual([])
      expect(f.core.entriesOfSlot('sidebar.panellist')).toHaveLength(1)
      expect(navigation).toBeDefined()
      act(() => navigation!.open())
      expect(f.selectPanel).toHaveBeenCalledWith(MNEMON_MAIN_PANEL_ID)
    } finally {
      navigation?.dispose()
      await root.fiber.dispose()
    }
  })

  it('adds a native panel beside Plugins, follows native icon sizes and locale labels, and retains Source ownership and editor state', () => {
    const f = fixture()
    releases.push(f.registerPeer())
    const view = render(<><f.NativeFrame /><f.Host /></>)
    const entry = screen.getByRole('button', { name: 'Memory System' })
    expect(document.querySelector('[data-dsh-mnemon-entry]')).toBeNull()
    expect(entry.querySelector('svg')?.getAttribute('width')).toBe('16')
    expect(entry.querySelector('svg')?.getAttribute('height')).toBe('16')
    expect(f.core.entriesOfSlot('main').map(item => item.options.key)).toEqual([MNEMON_MAIN_PANEL_ID, 'plugins'])
    fireEvent.click(entry)
    const content = screen.getByTestId('native-memory')
    expect(entry.getAttribute('aria-current')).toBe('page')
    expect(within(screen.getByTestId('native-main')).getByText('shell-source-owner')).not.toBeNull()
    fireEvent.change(screen.getByLabelText('Retained editor'), { target: { value: 'unsaved draft' } })
    fireEvent.click(screen.getByRole('button', { name: 'plugins' }))
    expect(screen.queryByTestId('native-memory')).toBeNull()
    expect(screen.getByText('plugins panel')).not.toBeNull()
    expect(entry.hasAttribute('aria-current')).toBe(false)
    expect(document.documentElement.hasAttribute('data-dsh-mnemon-active')).toBe(false)
    fireEvent.click(entry)
    expect(screen.getByTestId('native-memory')).toBe(content)
    expect((screen.getByLabelText('Retained editor') as HTMLInputElement).value).toBe('unsaved draft')
    fireEvent.click(entry)
    expect(screen.getByTestId('native-memory')).toBe(content)
    f.rename('记忆系统')
    view.rerender(<><f.NativeFrame collapsed /><f.Host /></>)
    const collapsedEntry = screen.getByRole('button', { name: '记忆系统' })
    expect(collapsedEntry.textContent).toBe('')
    expect(collapsedEntry.querySelector('svg')?.getAttribute('width')).toBe('18')
    expect(collapsedEntry.getAttribute('aria-current')).toBe('page')
  })

  it.each(['Return to conversation', 'New Session', 'Escape'])('returns native selection and the current main panel to Conversation with %s', action => {
    const f = fixture()
    render(<><f.NativeFrame /><f.Host /></>)
    act(() => f.navigation.open())
    expect(screen.getByTestId('native-memory')).not.toBeNull()
    if (action === 'Escape') fireEvent.keyDown(window, { key: 'Escape' })
    else fireEvent.click(screen.getByRole('button', { name: action }))
    expect(f.getPanel()).toBeNull()
    expect(screen.queryByTestId('native-memory')).toBeNull()
    expect(screen.getByText('Conversation')).not.toBeNull()
    expect(screen.getByRole('button', { name: 'Memory System' }).hasAttribute('aria-current')).toBe(false)
  })

  it('coordinates legacy peers and reasserts native navigation before their DOM announcement is observed', () => {
    const f = fixture()
    render(<><f.NativeFrame /><f.Host /></>)
    const entry = screen.getByRole('button', { name: 'Memory System' })
    fireEvent.click(entry)
    act(() => document.dispatchEvent(new CustomEvent('dsh-panel-activate', { detail: 'taskboard' })))
    expect(f.getPanel()).toBeNull()
    fireEvent.click(entry)
    document.documentElement.removeAttribute('data-dsh-mnemon-active')
    document.documentElement.setAttribute('data-dsh-ssh-active', '')
    fireEvent.click(entry)
    expect(f.getPanel()).toBe(MNEMON_MAIN_PANEL_ID)
    expect(document.documentElement.hasAttribute('data-dsh-ssh-active')).toBe(false)
    expect(screen.getByTestId('native-memory')).not.toBeNull()
  })

  it('replaces only its fallback when native seats arrive, and restores it when a replacement layout removes them', () => {
    document.body.innerHTML = '<aside data-pane="sidebar"><div><button class="newSession">New</button></div></aside>'
    const f = fixture(false)
    expect(document.querySelectorAll('[data-dsh-mnemon-entry]')).toHaveLength(1)
    act(() => f.declare())
    expect(document.querySelector('[data-dsh-mnemon-entry]')).toBeNull()
    expect(f.core.entriesOfSlot('sidebar.panellist')).toHaveLength(1)
    act(() => f.removeOwner())
    expect(document.querySelectorAll('[data-dsh-mnemon-entry]')).toHaveLength(1)
    f.navigation.dispose()
    expect(document.querySelector('[data-dsh-mnemon-entry]')).toBeNull()
  })

  it('rejects an occupied native panel id without replacing the peer or leaking a launcher', () => {
    const f = fixture()
    const peers = f.core.entriesOfSlot('main')
    expect(() => f.mount()).toThrow()
    expect(f.core.entriesOfSlot('main')).toEqual(peers)
    expect(f.core.entriesOfSlot('sidebar.panellist')).toHaveLength(1)
    expect(document.querySelector('[data-dsh-mnemon-entry]')).toBeNull()
  })
})
