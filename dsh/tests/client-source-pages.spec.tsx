// @vitest-environment jsdom
import { SlotCore } from '@deepseek-ai/dsh-client-ui-slots'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import type { Config } from "../src/host/config.ts"
import type { StatusView } from "../src/host/protocol.ts"
import type { MnemonSourcePageOwnerProps } from "../src/client/dsh-context.ts"
import { MnemonWorkbench } from '../src/client/MnemonWorkbench.tsx'
import { MNEMON_COMPONENT_STATUS_SLOT } from '../src/client/component-ui.tsx'
import { translateEn } from '../src/client/locales.ts'
import { PageHeader } from '../src/client/page-kit.tsx'
import { dispatchMnemonAnchor } from '../src/client/anchor.ts'
import {
  createMemorySourcePageDirectory,
  installMemorySourceUI,
  MNEMON_SOURCE_PAGE_SLOT,
  type MemorySourcePageProps,
} from '../src/client/source-pages.tsx'
import { settingsScope } from './helpers/settings-scope.ts'

class TestSlots {
  readonly core = new SlotCore()

  register(options: unknown, component: unknown): () => void {
    return (this.core.register as (options: unknown, component: unknown) => () => void)(options, component)
  }

  inject(_name: string, factory: () => (() => void)): () => void {
    let active: (() => void) | undefined
    let disposed = false
    const reconcile = (): void => {
      if (disposed) return
      const declared = this.core.specDynamic(MNEMON_SOURCE_PAGE_SLOT) !== undefined
      if (!declared) {
        active?.()
        active = undefined
      } else if (active === undefined) {
        active = factory()
      }
    }
    const unsubscribe = this.core.subscribeDeclaration(MNEMON_SOURCE_PAGE_SLOT, reconcile)
    try {
      reconcile()
    } catch (error) {
      unsubscribe()
      throw error
    }
    return () => {
      if (disposed) return
      disposed = true
      unsubscribe()
      active?.()
      active = undefined
    }
  }

  getVersion(key: string): number { return this.core.getVersion(key) }
  entriesOfSlot(key: string) { return this.core.entriesOfSlot(key) }
  subscribe(key: string, listener: () => void): () => void { return this.core.subscribe(key, listener) }
}

function declareSourcePageSlot(slots: TestSlots): () => void {
  return slots.register({
    name: 'root',
    children: { [MNEMON_SOURCE_PAGE_SLOT]: { kind: 'list', scope: 'root' } },
  }, (_props: unknown) => null)
}

function Page(_props: MemorySourcePageProps): ReactNode { return null }

const staticLocale = { getSnapshot: () => 'zh', subscribe: () => () => {} }

const settingsSnapshot = { status: 'ready' as const, value: {}, revision: 1, writable: true, mode: 'host' as const }
const settings = settingsScope<Config>(settingsSnapshot)

const status: StatusView = {
  healthy: true,
  dshMnemonVersion: '0.3.5',
  cliPath: '/usr/local/bin/mnemon',
  commandFound: true,
  dataDir: '/tmp/mnemon',
  store: 'project',
  mnemonDefaultStore: 'project',
  dshActiveStores: ['project'],
  writeEnabled: true,
  timeoutMs: 10_000,
  defaultRecallLimit: 10,
  recallQuality: {
    policy: 'strict-v1', lowScoreThreshold: 0.25, highScoreThreshold: 0.6,
    candidateMultiplier: 3, maxMediumResults: 4, maxUnknownResults: 2,
  },
  memoryBodyDirectory: '/tmp/mnemon/data',
  memoryBodies: [],
}

describe('Source Client presentation conformance', () => {
  it('rejects duplicate page owners and lets a replacement install after unload', () => {
    const slots = new TestSlots()
    const owner = declareSourcePageSlot(slots)
    const first = installMemorySourceUI({ slots } as never, { sourceTypeId: 'runtime', pages: [{ id: 'entries', label: 'First', component: Page }] })
    const directory = createMemorySourcePageDirectory({ slots, locale: staticLocale } as never)
    expect(() => installMemorySourceUI({ slots } as never, { sourceTypeId: 'runtime', pages: [{ id: 'entries', label: 'Duplicate', component: Page }] })).toThrow()
    expect(directory.getSnapshot().map(entry => entry.label)).toEqual(['First'])
    first()
    const second = installMemorySourceUI({ slots } as never, { sourceTypeId: 'runtime', pages: [{ id: 'entries', label: 'Second', component: Page }] })
    expect(directory.getSnapshot().map(entry => entry.label)).toEqual(['Second'])
    second()
    expect(directory.getSnapshot()).toEqual([])
    owner()
  })

  afterEach(cleanup)

  it('waits for one parent declaration, commits all pages transactionally, and disposes with the Client Fiber', () => {
    const slots = new TestSlots()
    const disposeContribution = installMemorySourceUI({ slots } as never, {
      sourceTypeId: 'git',
      pages: [
        { id: 'repository', label: 'Repository', component: Page },
        { id: 'refs', label: 'Refs', component: Page },
      ],
    })
    expect(slots.core.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT)).toEqual([])

    const disposeOwner = declareSourcePageSlot(slots)
    expect(slots.core.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT).map(entry => entry.options.id)).toEqual([
      'git/repository', 'git/refs',
    ])

    disposeContribution()
    disposeContribution()
    expect(slots.core.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT)).toEqual([])
    disposeOwner()
  })

  it('rolls back earlier pages when one registration conflicts and keeps the existing contribution intact', () => {
    const slots = new TestSlots()
    const disposeOwner = declareSourcePageSlot(slots)
    const disposeExisting = slots.register({ name: MNEMON_SOURCE_PAGE_SLOT, id: 'git/refs' }, Page)

    expect(() => installMemorySourceUI({ slots } as never, {
      sourceTypeId: 'git',
      pages: [
        { id: 'repository', label: 'Repository', component: Page },
        { id: 'refs', label: 'Refs', component: Page },
      ],
    })).toThrow(/already occupied|already registered|priority/u)
    expect(slots.core.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT).map(entry => entry.options.id)).toEqual(['git/refs'])

    disposeExisting()
    disposeOwner()
  })

  it('derives a stable directory from the Slot ledger and contains one page metadata/render failure', async () => {
    const slots = new TestSlots()
    const disposeOwner = declareSourcePageSlot(slots)
    const disposeGit = slots.register({ name: MNEMON_SOURCE_PAGE_SLOT, id: 'git/repository', label: () => { throw new Error('bad label') } }, Page)
    const disposeNotion = slots.register({ name: MNEMON_SOURCE_PAGE_SLOT, id: 'notion/notes', label: 'Notes' }, Page)
    const directory = createMemorySourcePageDirectory({ slots, locale: staticLocale } as never)
    const first = directory.getSnapshot()
    expect(first).toEqual([
      expect.objectContaining({ id: 'git/repository', label: 'repository' }),
      expect.objectContaining({ id: 'notion/notes', label: 'Notes' }),
    ])
    expect(directory.getSnapshot()).toBe(first)

    const listener = vi.fn()
    const unsubscribe = directory.subscribe(listener)
    const gitEntry = slots.core.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT)[0]!
    slots.core.reportEntryError(MNEMON_SOURCE_PAGE_SLOT, gitEntry, new Error('render failed'), { abdicate: true })
    await Promise.resolve()
    expect(listener).toHaveBeenCalledOnce()
    expect(directory.getSnapshot().map(entry => entry.id)).toEqual(['notion/notes'])

    unsubscribe()
    disposeNotion()
    disposeGit()
    disposeOwner()
  })

  it('refreshes plugin-owned labels on locale changes without replacing Source Fibers', () => {
    const slots = new TestSlots(), owner = declareSourcePageSlot(slots)
    let current = { active: 'zh', revision: 0 }
    const listeners = new Set<() => void>()
    const locale = { getSnapshot: () => current, subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } } }
    const page = installMemorySourceUI({ slots } as never, { sourceTypeId: 'git', pages: [{ id: 'repo', label: () => current.active === 'zh' ? '仓库' : 'Repository', navigation: { detail: () => current.active }, component: Page }] })
    const directory = createMemorySourcePageDirectory({ slots, locale } as never)
    const first = directory.getSnapshot(), entry = slots.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT)[0]
    expect(first[0]?.label).toBe('仓库')
    const listener = vi.fn(), stop = directory.subscribe(listener)
    current = { active: 'en', revision: 1 }
    for (const changed of listeners) changed()
    expect(listener).toHaveBeenCalledOnce()
    const second = directory.getSnapshot()
    expect(second[0]).toMatchObject({ label: 'Repository', navigation: { detail: 'en' } })
    expect(second).not.toBe(first)
    expect(directory.getSnapshot()).toBe(second)
    expect(slots.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT)[0]).toBe(entry)
    stop(); expect(listeners.size).toBe(0)
    page(); owner()
  })

  it('routes one type-level page across authorized instances without exposing the raw transport', async () => {
    const calls: Array<{ channel: string; endpoint: string; payload: Record<string, unknown> }> = []
    const sourceCatalog = {
      generationId: 'generation:one',
      sources: [
        {
          sourceInstanceKey: 'source:git-work', sourceTypeId: 'git', packageName: 'dsh-mnemon-source-git', role: 'repository',
          availability: 'ready' as const, revision: 'work-r1', capabilities: ['read' as const],
          management: { label: 'Work repository', description: 'Work Git memory.' },
        },
        {
          sourceInstanceKey: 'source:git-personal', sourceTypeId: 'git', packageName: 'dsh-mnemon-source-git', role: 'repository',
          availability: 'degraded' as const, revision: 'personal-r2', capabilities: ['read' as const],
          management: { label: 'Personal repository', description: 'Personal Git memory.' },
        },
      ],
    }
    const connection = { rpc: { call: vi.fn(async (channel: string, endpoint: string, payload: Record<string, unknown>) => {
      calls.push({ channel, endpoint, payload })
      if (endpoint === 'status-summary' || endpoint === 'status') return { ok: true, value: status }
      if (endpoint === 'source-management-catalog') return { ok: true, value: sourceCatalog }
      if (endpoint === 'source-management-read') return { ok: true, value: { revision: 'read-r1', value: { branch: 'main' } } }
      if (endpoint === 'source-management-mutate') return { ok: true, value: { revision: 'write-r2', value: { updated: true } } }
      return { ok: false, error: { code: 'bad-request', message: `unexpected ${endpoint}`, details: { issues: [] } } }
    }) }, isLoopback: true }
    const directorySnapshot = [{ id: 'git/repository', sourceTypeId: 'git', pageId: 'repository', label: 'Repository', order: 1 }] as const
    const directory = {
      getSnapshot: () => directorySnapshot,
      subscribe: () => () => {},
    }
    let lastProps: MnemonSourcePageOwnerProps | undefined
    const CustomPage = (props: MnemonSourcePageOwnerProps): JSX.Element => {
      lastProps = props
      return <div>
        <strong data-testid="selected-source">{props.management?.sourceInstanceKey}</strong>
        <button type="button" onClick={() => void props.management?.read('inspect', { ref: 'main' })}>Inspect source</button>
        <button type="button" onClick={() => void props.management?.mutate('refresh', {}, { confirmed: true })}>Refresh source</button>
      </div>
    }
    const renderSlot = ((_name: string, owner: MnemonSourcePageOwnerProps, options: { only?: string; fallback?: ReactNode }) => (
      options.only === 'git/repository' ? <CustomPage {...owner} /> : options.fallback
    )) as never

    render(<MnemonWorkbench
      connection={connection as never}
      settingsScope={settings}
      sessionId="session-1"
      workspaceId="workspace-1"
      t={translateEn}
      locale="en"
      sourcePageDirectory={directory}
      renderSlot={renderSlot}
    />)

    fireEvent.click(await screen.findByRole('tab', { name: 'Repository' }))
    expect((await screen.findByTestId('selected-source')).textContent).toBe('source:git-work')
    expect(lastProps).toMatchObject({
      sourceTypeId: 'git', sourceInstanceKey: 'source:git-work', sessionId: 'session-1', workspaceId: 'workspace-1', locale: 'en-US',
    })
    expect(lastProps).not.toHaveProperty('connection')

    fireEvent.click(screen.getByRole('button', { name: /^Select Source instance/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: /source:git-personal/ }))
    await waitFor(() => expect(screen.getByTestId('selected-source').textContent).toBe('source:git-personal'))
    fireEvent.click(screen.getByRole('button', { name: 'Inspect source' }))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh source' }))

    await waitFor(() => expect(calls).toEqual(expect.arrayContaining([
      expect.objectContaining({
        endpoint: 'source-management-read',
        payload: expect.objectContaining({ sourceInstanceKey: 'source:git-personal', sessionId: 'session-1', workspaceId: 'workspace-1', operation: 'inspect' }),
      }),
      expect.objectContaining({
        endpoint: 'source-management-mutate',
        payload: expect.objectContaining({ sourceInstanceKey: 'source:git-personal', expectedRevision: 'personal-r2', confirmed: true, operation: 'refresh' }),
      }),
    ])))
  })

  it('names tabs, Status cards and the header from the components, and shows the cards they contribute', async () => {
    const management = (label: string) => ({ label })
    const sources = [
      { sourceInstanceKey: 'mnemon-source-runtime', sourceTypeId: 'runtime', packageName: 'dsh-mnemon-source-runtime', role: 'working-context', availability: 'ready', revision: 'r1', capabilities: ['read'], management: management('Runtime') },
      { sourceInstanceKey: 'source:notes', sourceTypeId: 'notes', packageName: 'acme-memory-notes', role: 'notes', availability: 'ready', revision: 'r1', capabilities: ['read'], management: management('notes') },
    ]
    const component = (entryId: string, packageName: string, roles: string[], typeId: string, label: { en: string; 'zh-CN': string }, enabled = true) => ({
      entryId, packageName, roles, ...enabled ? { typeId } : {}, label, description: { en: '', 'zh-CN': '' }, fields: [], provides: [], requires: [], requiredBy: [], enabled, active: enabled, writable: true, config: {},
    })
    const dashboard = {
      revision: 'view-1', writable: true, strategyTypeId: 'general', sources: [], diagnostics: [], pluginInstallation: { supported: false, suggestions: [] },
      entries: [
        component('mnemon-strategy-general', 'dsh-mnemon-strategy-general', ['strategy'], 'general', { en: 'General strategy', 'zh-CN': '通用策略' }),
        component('mnemon-source-runtime', 'dsh-mnemon-source-runtime', ['source'], 'runtime', { en: 'Runtime memory', 'zh-CN': '运行时记忆' }),
        component('notes', 'acme-memory-notes', ['source'], 'notes', { en: 'Notes', 'zh-CN': '笔记' }),
        // A Source switched off has registered nothing, yet still has its card.
        component('drafts', 'acme-memory-drafts', ['source'], 'drafts', { en: 'Drafts', 'zh-CN': '草稿' }, false),
      ],
    }
    const participation = { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' }
    const layer = { enabled: true, participation, adapterIds: [] }
    const memorySystem = {
      serving: true, strategyTypeId: 'general', sources: [],
      evaluation: { state: 'ready', contributionRevision: 1, sourceInstanceKeys: [], diagnostics: [] },
      configuration: { id: 'general', strategyId: 'general', layers: { runtime: layer, notes: layer } },
    }
    const connection = { rpc: { call: vi.fn(async (_channel: string, endpoint: string) => ({
      ok: true, value: endpoint === 'source-management-catalog' ? { generationId: 'g1', sources } : endpoint === 'dashboard' ? dashboard : { ...status, memorySystem },
    })) }, isLoopback: true }
    const pages = [
      { id: 'runtime/entries', sourceTypeId: 'runtime', pageId: 'entries', label: 'Runtime', order: 100 },
      { id: 'notes/list', sourceTypeId: 'notes', pageId: 'list', label: 'Notes list', order: 1000 },
    ]
    // Notes contributes its card; Runtime memory contributes none here and says that it runs.
    const renderSlot = ((name: string, _owner: unknown, options: { entryKey?: string; fallback?: ReactNode }) => name !== MNEMON_COMPONENT_STATUS_SLOT ? <div>page</div>
      : options.entryKey === 'acme-memory-notes' ? <><strong>12 notes</strong><p>Synced a minute ago</p></> : options.fallback) as never
    render(<MnemonWorkbench connection={connection as never} settingsScope={settings} t={translateEn} locale="en" sourcePageDirectory={{ getSnapshot: () => pages, subscribe: () => () => {} }} renderSlot={renderSlot} />)

    // A Source's first page carries the component's declared name.
    expect(await screen.findByRole('tab', { name: 'Notes' })).toBeTruthy()
    expect(screen.getByRole('tab', { name: 'Runtime memory' })).toBeTruthy()
    const strip = screen.getByRole('region', { name: 'Mnemon runtime status' })
    await waitFor(() => expect(within(strip).getByText('12 notes')).toBeTruthy())
    const card = (name: string) => within(strip).getByText(name).closest('article') as HTMLElement
    expect(within(card('Notes')).getByText('Synced a minute ago')).toBeTruthy()
    expect(within(card('Runtime memory')).getByText('Running')).toBeTruthy()
    expect(within(card('Drafts')).getByText('Off')).toBeTruthy()
    expect(card('Drafts').getAttribute('data-component')).toBe('acme-memory-drafts')
    // The header names the composing main Strategy as it declares itself.
    expect(screen.getByRole('button', { name: 'Connected · General strategy' })).toBeTruthy()
  })

  it('reveals only a connected Source element inside the owning canvas', async () => {
    const source = { sourceInstanceKey: 'source:git', sourceTypeId: 'git', packageName: 'dsh-mnemon-source-git', role: 'repository', availability: 'ready', revision: 'r1', capabilities: ['read'], management: { label: 'Repository' } }
    const connection = { rpc: { call: vi.fn(async (_channel: string, endpoint: string) => ({ ok: true, value: endpoint === 'source-management-catalog' ? { generationId: 'g1', sources: [source] } : status })) }, isLoopback: true }
    const pages = [{ id: 'git/repository', sourceTypeId: 'git', pageId: 'repository', label: 'Repository', order: 1 }]
    let owner: MemorySourcePageProps | undefined
    const renderSlot = ((_name: string, props: MemorySourcePageProps) => { owner = props; return <div data-testid="source-target">Source content</div> }) as never
    render(<div data-testid="host"><div data-testid="peer" /><MnemonWorkbench connection={connection as never} settingsScope={settings} t={translateEn} locale="en" sourcePageDirectory={{ getSnapshot: () => pages, subscribe: () => () => {} }} renderSlot={renderSlot} /></div>)
    fireEvent.click(await screen.findByRole('tab', { name: 'Repository' }))
    const target = await screen.findByTestId('source-target'), canvas = screen.getByTestId('mnemon-canvas')
    const host = screen.getByTestId('host'), peer = screen.getByTestId('peer')
    canvas.scrollTop = 500; host.scrollTop = 200; peer.scrollTop = 300
    vi.spyOn(canvas, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 80, 800, 600))
    vi.spyOn(target, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 100, 400, 100))
    expect(owner?.onRevealElement).toBeTypeOf('function')
    owner?.onRevealElement?.(peer, 120)
    owner?.onRevealElement?.(document.createElement('div'), 120)
    expect(canvas.scrollTop).toBe(500)
    owner?.onRevealElement?.(target, 120)
    expect(canvas.scrollTop).toBe(400)
    expect(host.scrollTop).toBe(200)
    expect(peer.scrollTop).toBe(300)
    fireEvent.click(screen.getByRole('tab', { name: 'Status' }))
    canvas.scrollTop = 250
    owner?.onRevealElement?.(target, 120)
    expect(canvas.scrollTop).toBe(250)
  })

  it('reveals an element below the locked page header unless the Source measures its own', async () => {
    const source = { sourceInstanceKey: 'source:git', sourceTypeId: 'git', packageName: 'dsh-mnemon-source-git', role: 'repository', availability: 'ready', revision: 'r1', capabilities: ['read'], management: { label: 'Repository' } }
    const connection = { rpc: { call: vi.fn(async (_channel: string, endpoint: string) => ({ ok: true, value: endpoint === 'source-management-catalog' ? { generationId: 'g1', sources: [source] } : status })) }, isLoopback: true }
    const pages = [{ id: 'git/repository', sourceTypeId: 'git', pageId: 'repository', label: 'Repository', order: 1 }]
    let owner: MemorySourcePageProps | undefined
    const renderSlot = ((_name: string, props: MemorySourcePageProps) => { owner = props; return <div><PageHeader title="Repository" description="Refs and history." /><div data-testid="source-target">Source content</div></div> }) as never
    render(<MnemonWorkbench connection={connection as never} settingsScope={settings} t={translateEn} locale="en" sourcePageDirectory={{ getSnapshot: () => pages, subscribe: () => () => {} }} renderSlot={renderSlot} />)
    fireEvent.click(await screen.findByRole('tab', { name: 'Repository' }))
    const target = await screen.findByTestId('source-target'), canvas = screen.getByTestId('mnemon-canvas')
    const header = screen.getByRole('heading', { name: 'Repository', level: 2 }).parentElement!.parentElement!
    expect(canvas.hasAttribute('data-lock-page-header')).toBe(true)
    vi.spyOn(canvas, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 80, 800, 600))
    vi.spyOn(header, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 80, 800, 60))
    vi.spyOn(target, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 400, 400, 100))
    canvas.scrollTop = 500
    owner?.onRevealElement?.(target)
    expect(canvas.scrollTop).toBe(500 + 400 - 80 - 72)
    canvas.scrollTop = 500
    owner?.onRevealElement?.(target, 20)
    expect(canvas.scrollTop).toBe(500 + 400 - 80 - 20)
  })

  it('carries a conversation anchor into its page, while a tab opens the page fresh', async () => {
    const source = { sourceInstanceKey: 'source:git', sourceTypeId: 'git', packageName: 'dsh-mnemon-source-git', role: 'repository', availability: 'ready', revision: 'r1', capabilities: ['read'], management: { label: 'Repository' } }
    const connection = { rpc: { call: vi.fn(async (_channel: string, endpoint: string) => ({ ok: true, value: endpoint === 'source-management-catalog' ? { generationId: 'g1', sources: [source] } : status })) }, isLoopback: true }
    const pages = [
      { id: 'git/repository', sourceTypeId: 'git', pageId: 'repository', label: 'Repository', order: 1 },
      { id: 'git/refs', sourceTypeId: 'git', pageId: 'refs', label: 'Refs', order: 2, navigation: { primary: false } },
    ]
    const received: Array<{ page: string; input: unknown }> = []
    const renderSlot = ((_name: string, props: MemorySourcePageProps, options: { only: string }) => { received.push({ page: options.only, input: props.navigationInput }); return <div>Source content</div> }) as never
    render(<MnemonWorkbench connection={connection as never} settingsScope={settings} t={translateEn} locale="en" sourcePageDirectory={{ getSnapshot: () => pages, subscribe: () => () => {} }} renderSlot={renderSlot} />)
    await screen.findByRole('tab', { name: 'Repository' })
    act(() => dispatchMnemonAnchor({ page: 'git/repository', seed: 'main' }))
    await waitFor(() => expect(received.at(-1)).toMatchObject({ page: 'git/repository', input: { seed: 'main' } }))
    fireEvent.click(screen.getByRole('tab', { name: 'Status' }))
    fireEvent.click(screen.getByRole('tab', { name: 'Repository' }))
    await waitFor(() => expect(received.at(-1)).toEqual({ page: 'git/repository', input: undefined }))
  })

  it('selects additional built-in Source instances without injecting a hidden default page', async () => {
    const sources = ['source:extra-runtime', 'source:mnemon-source-runtime'].map(sourceInstanceKey => ({
      sourceInstanceKey, sourceTypeId: 'runtime', packageName: 'dsh-mnemon-source-runtime', role: 'working-context',
      availability: 'ready', revision: 'r1', capabilities: ['read', 'write'], management: { label: sourceInstanceKey },
    }))
    const connection = { rpc: { call: vi.fn(async (_channel: string, endpoint: string) => ({
      ok: true, value: endpoint === 'source-management-catalog' ? { generationId: 'g1', sources } : status,
    })) }, isLoopback: true }
    const runtimePages = [{ id: 'runtime/entries', sourceTypeId: 'runtime', pageId: 'entries', label: translateEn('nav.runtime'), order: 100 }]
    const renderSlot = ((_name: string, owner: MnemonSourcePageOwnerProps, options: { only?: string }) => options.only !== 'runtime/entries' ? null : <div>
      <span data-testid="runtime-selected-instance">{owner.management?.sourceInstanceKey}</span>
      <span data-testid="runtime-hidden-child">{!('children' in owner) ? 'no' : 'yes'}</span>
    </div>) as never
    render(<MnemonWorkbench connection={connection as never} settingsScope={settings} sessionId="s1" t={translateEn} locale="en" sourcePageDirectory={{ getSnapshot: () => runtimePages, subscribe: () => () => {} }} renderSlot={renderSlot} />)
    fireEvent.click(await screen.findByRole('tab', { name: translateEn('nav.runtime') }))
    await waitFor(() => expect(screen.getByTestId('runtime-selected-instance').textContent).toBe('source:mnemon-source-runtime'))
    expect(screen.getByTestId('runtime-hidden-child').textContent).toBe('no')
    fireEvent.click(screen.getByRole('button', { name: /^Select Source instance/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: /source:extra-runtime/ }))
    expect(screen.getByTestId('runtime-selected-instance').textContent).toBe('source:extra-runtime')
    expect(screen.getByTestId('runtime-hidden-child').textContent).toBe('no')
  })

  it('keeps a descriptor-driven status/config/diagnostics path when a Source has no client module', async () => {
    const calls: Array<{ endpoint: string; payload: Record<string, unknown> }> = []
    const source = {
      sourceInstanceKey: 'source:health-main', sourceTypeId: 'health', packageName: 'dsh-mnemon-source-health', role: 'health-records',
      availability: 'ready' as const, revision: 'health-r4', capabilities: ['read' as const, 'write' as const],
      management: {
        label: 'Health records',
        description: 'Sanitized health observations.',
        diagnostics: ['Last sync completed with two redacted observations.'],
        fields: [
          { key: 'endpoint', label: 'Endpoint', description: 'FHIR-compatible endpoint.', input: 'url' as const, required: true },
          { key: 'token', label: 'Token', input: 'secret' as const, required: true, secret: true },
        ],
      },
    }
    const connection = { rpc: { call: vi.fn(async (_channel: string, endpoint: string, payload: Record<string, unknown>) => {
      calls.push({ endpoint, payload })
      if (endpoint === 'status-summary' || endpoint === 'status') return { ok: true, value: status }
      if (endpoint === 'source-management-catalog') return { ok: true, value: { generationId: 'generation:health', sources: [source] } }
      if (endpoint === 'source-management-read') return { ok: true, value: { revision: 'health-r4', value: { values: { endpoint: 'https://health.example.test', token: 'server-secret-must-not-render' } } } }
      if (endpoint === 'source-management-mutate') return { ok: true, value: { revision: 'health-r5', value: { configured: true } } }
      return { ok: false, error: { code: 'bad-request', message: `unexpected ${endpoint}`, details: { issues: [] } } }
    }) }, isLoopback: true }
    const emptyPages = [] as const

    render(<MnemonWorkbench
      connection={connection as never}
      settingsScope={settings}
      sessionId="session-health"
      workspaceId="workspace-health"
      t={translateEn}
      locale="en"
      sourcePageDirectory={{ getSnapshot: () => emptyPages, subscribe: () => () => {} }}
    />)

    fireEvent.click(await screen.findByRole('tab', { name: 'Health records' }))
    expect(await screen.findByText('Sanitized health observations.')).not.toBeNull()
    expect(screen.getByText('Last sync completed with two redacted observations.')).not.toBeNull()
    const endpoint = await screen.findByLabelText(/^Endpoint/u)
    await waitFor(() => expect((endpoint as HTMLInputElement).value).toBe('https://health.example.test'))
    expect(screen.queryByDisplayValue('server-secret-must-not-render')).toBeNull()
    expect((screen.getByLabelText(/^Token/u) as HTMLInputElement).value).toBe('')

    fireEvent.change(endpoint, { target: { value: 'https://new-health.example.test' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save configuration' }))
    await waitFor(() => expect(calls).toEqual(expect.arrayContaining([
      expect.objectContaining({
        endpoint: 'source-management-mutate',
        payload: expect.objectContaining({
          sourceInstanceKey: 'source:health-main', sessionId: 'session-health', workspaceId: 'workspace-health',
          operation: 'configuration', expectedRevision: 'health-r4', confirmed: true,
          input: { endpoint: 'https://new-health.example.test' },
        }),
      }),
    ])))
  })
})
