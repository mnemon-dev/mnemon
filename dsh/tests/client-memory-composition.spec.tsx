// @vitest-environment jsdom
import { useMemo, useRef } from 'react'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MnemonClient } from '../src/client/api.ts'
import { CompositionBoard, type ComponentSettingsRenderer, type LayerSettings } from '../src/client/CompositionBoard.tsx'
import { translateEn, translateZh, type MnemonTranslate } from '../src/client/locales.ts'
import { useViewFeedback } from '../src/client/view-feedback.tsx'
import { useViewStore } from '../src/client/view-store.ts'
import type { ClientConnectionHandle, MemoryCompositionStatus, MemoryPluginEntryView, MemoryViewConfigurationRequest, MemoryViewDashboard } from '../src/host/protocol.ts'

afterEach(cleanup)

const checked = (element: HTMLElement) => element.getAttribute('aria-checked') === 'true'
const disabled = (element: HTMLElement) => (element as HTMLButtonElement).disabled

/** The names every component declares for itself, as the shipped packages do. */
const NAMES: Record<string, [string, string]> = {
  'default-three-tier': ['Layered strategy', '分层策略'], general: ['General strategy', '通用策略'], 'auto-capture': ['Active capture', '主动记录'],
  'light-context': ['Light context', '轻量上下文'], runtime: ['Runtime Memory', '运行时记忆'], documents: ['Project Documents', '项目档案'], 'memory-spaces': ['Memory Spaces', '记忆空间'],
}

function entry(entryId: string, packageName: string, roles: MemoryPluginEntryView['roles'], typeId: string | undefined, overrides: Partial<MemoryPluginEntryView> = {}): MemoryPluginEntryView {
  const [en, zh] = NAMES[typeId ?? ''] ?? [`${typeId} label`, `${typeId} 标签`]
  return {
    entryId, packageName, ...(typeId === undefined ? {} : { typeId }), roles, label: { en, 'zh-CN': zh },
    description: { en: `${typeId} description`, 'zh-CN': `${typeId} 说明` }, fields: [], provides: [], requires: [], requiredBy: [],
    enabled: roles.includes('strategy') && typeId === 'default-three-tier', active: roles.includes('strategy') && typeId === 'default-three-tier',
    writable: true, config: {}, ...overrides,
  }
}

const off = { enabled: false, active: false } as const
const on = { enabled: true, active: true } as const
const main = (typeId: string, overrides: Partial<MemoryPluginEntryView> = {}) => entry('mnemon-strategy-' + typeId, 'dsh-mnemon-strategy-' + typeId, ['strategy'], typeId, {
  provides: [{ id: 'strategy', exclusive: false }, { id: 'strategy.' + typeId, exclusive: false }], requires: ['source'], ...overrides,
})
const threeTier = main('default-three-tier')
const general = main('general')
const capture = entry('mnemon-strategy-auto-capture', 'dsh-mnemon-strategy-auto-capture', ['strategy-extension'], 'auto-capture')
const focus = entry('focus', 'acme-memory-focus', ['strategy-extension'], 'focus')
const source = (layer: string, overrides: Partial<MemoryPluginEntryView> = {}) => entry('mnemon-source-' + layer, 'dsh-mnemon-source-' + layer, ['source'], layer, {
  ...on, provides: [{ id: 'source', exclusive: false }, ...layer === 'memory-spaces' ? [{ id: 'source.durable-evidence', exclusive: false }] : []], ...overrides,
})
/** Active capture as shipped: it keeps durable evidence in Memory Spaces. */
const needy = { ...capture, requires: ['strategy', 'source.durable-evidence'] }

/** The Host's composition status with the saved memory layers. */
function system(layers: Record<string, boolean> = {}, serving = true): MemoryCompositionStatus {
  const participation = { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' } as const
  return {
    serving, strategyTypeId: 'default-three-tier', sources: [],
    evaluation: { state: 'ready', contributionRevision: 1, sourceInstanceKeys: [], diagnostics: serving ? [] : [{ code: 'missing-source', message: 'no Source' }] },
    configuration: { id: 'default', strategyId: 'default-three-tier', layers: Object.fromEntries(Object.entries(layers).map(([id, enabled]) => [id, { enabled, participation: { ...participation }, adapterIds: [] }])) },
  } as MemoryCompositionStatus
}
const allLayers = { runtime: true, documents: true, 'memory-spaces': true }

function fixture(entries: MemoryPluginEntryView[], options: { failApply?: boolean; holdApply?: Promise<void>; sources?: MemoryViewDashboard['sources']; strategyTypeId?: string } = {}) {
  let dashboard: MemoryViewDashboard = {
    revision: 'view-1', writable: true, strategyTypeId: options.strategyTypeId ?? 'default-three-tier', entries, currentUnavailable: 'no-session',
    sources: options.sources ?? [], diagnostics: [], pluginInstallation: { supported: false, reason: 'loader-unavailable', suggestions: [] },
  }
  const applied: MemoryViewConfigurationRequest[] = []
  const call = vi.fn(async (channel: string, endpoint: string, payload: unknown) => {
    if (channel === '/dsh-mnemon-view' && endpoint === 'dashboard') return { ok: true as const, value: structuredClone(dashboard) }
    if (channel === '/dsh-mnemon-view-settings' && endpoint === 'apply') {
      await options.holdApply
      if (options.failApply) return { ok: false as const, error: { code: 'internal' as const, message: 'selected memory Strategy type is unavailable', details: {} } }
      const request = (payload as { configuration: MemoryViewConfigurationRequest }).configuration
      // The Host's own answer to a write prepared against an older View.
      if (request.expectedRevision !== dashboard.revision) return { ok: false as const, error: { code: 'internal' as const, message: 'Memory plugin configuration changed; refresh before saving or previewing.', details: {} } }
      applied.push(request)
      dashboard = { ...dashboard, revision: `view-${Number(dashboard.revision.slice(5)) + 1}`, strategyTypeId: request.strategyTypeId, entries: dashboard.entries.map(value => request.entries[value.entryId] === undefined
        ? value
        : { ...value, enabled: request.entries[value.entryId]!.enabled, active: request.entries[value.entryId]!.enabled, config: request.entries[value.entryId]!.config }) }
      return { ok: true as const, value: { saved: true as const } }
    }
    return { ok: false as const, error: { code: 'internal' as const, message: `unsupported ${channel} ${endpoint}`, details: {} } }
  })
  /** Another writer, such as a second window, changes the View. */
  const external = (strategyTypeId: string) => { dashboard = { ...dashboard, revision: 'view-9', strategyTypeId } }
  /** A switch on DSH's own component list. */
  const switchElsewhere = (entryId: string, enabled: boolean) => {
    dashboard = { ...dashboard, revision: 'view-8', entries: dashboard.entries.map(value => value.entryId === entryId ? { ...value, enabled, active: enabled } : value) }
  }
  return { applied, external, switchElsewhere, call, connection: { rpc: { call }, isLoopback: true } as ClientConnectionHandle }
}

const NO_LAYERS: LayerSettings = { set: async () => {} }

/** The configuration page's wiring: one View store, re-read when `refreshKey` moves, the board and its feedback. */
function Composition(props: { connection: ClientConnectionHandle; language: string; readOnly?: boolean; refreshKey?: number; system?: MemoryCompositionStatus; layers?: LayerSettings; componentSettings?: ComponentSettingsRenderer; page?: string; t: MnemonTranslate }) {
  const client = useMemo(() => new MnemonClient(props.connection), [props.connection])
  const view = useViewStore(client, props.refreshKey ?? 0)
  const root = useRef<HTMLElement | null>(null)
  const feedback = useViewFeedback(view, root, props.t, props.language)
  return <section ref={root}>
    <CompositionBoard view={view} system={props.system ?? null} layers={props.layers ?? NO_LAYERS} readOnly={props.readOnly ?? false} language={props.language} t={props.t}
      {...(props.componentSettings === undefined ? {} : { componentSettings: props.componentSettings })} {...(props.page === undefined ? {} : { page: props.page })} />
    {feedback}
  </section>
}

const board = () => screen.getByRole('region', { name: /^(Memory composition|记忆组合)$/u })
/** The problem line: only there while memory is not composed as chosen. */
const problem = () => board().querySelector<HTMLElement>('[role="status"], [role="alert"]')
const row = (name: string) => screen.getByRole('switch', { name }).closest('[data-mnemon-target]') as HTMLElement
/** The main Strategy selector; its accessible name is the row title followed by the chosen Strategy. */
const mainSelector = (title = 'Main strategy') => screen.getByRole('button', { name: new RegExp(`^${title} `, 'u') })
const selectedMain = (title?: string) => mainSelector(title).textContent
function chooseMain(strategy: string, title?: string): void {
  fireEvent.click(mainSelector(title))
  fireEvent.click(screen.getByRole('menuitem', { name: new RegExp(`^${strategy}`, 'u') }))
}
/** Open a component's page from its name on its row, as DSH's component list opens a row. */
function openDetails(name: string): HTMLElement {
  fireEvent.click(within(row(name)).getByRole('button', { name }))
  return screen.getByRole('dialog', { name })
}

describe('memory composition board', () => {
  it('chooses the main Strategy from a selector, turning the other one off', async () => {
    const { applied, connection } = fixture([threeTier, general, capture])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    await waitFor(() => expect(selectedMain()).toBe('Layered strategy'))
    // Memory is composed as chosen: the board says nothing more.
    expect(problem()).toBeNull()
    chooseMain('General strategy')
    await waitFor(() => expect(applied).toHaveLength(1))
    expect(applied[0]).toEqual({ expectedRevision: 'view-1', strategyTypeId: 'general', entries: {
      'mnemon-strategy-default-three-tier': { enabled: false, config: {} },
      'mnemon-strategy-general': { enabled: true, config: {} },
    } })
    expect((await screen.findByRole('alert')).textContent).toContain('Switched to “General strategy” and turned off “Layered strategy”')
    await waitFor(() => expect(selectedMain()).toBe('General strategy'))
    expect(problem()).toBeNull()
  })

  it('undoes a switch from its toast, and only confirms the undo', async () => {
    const { applied, connection } = fixture([threeTier, general, capture])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    await waitFor(() => expect(selectedMain()).toBe('Layered strategy'))
    chooseMain('General strategy')
    fireEvent.click(await screen.findByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(applied).toHaveLength(2))
    expect(applied[1]).toEqual({ expectedRevision: 'view-2', strategyTypeId: 'default-three-tier', entries: {
      'mnemon-strategy-general': { enabled: false, config: {} },
      'mnemon-strategy-default-three-tier': { enabled: true, config: {} },
    } })
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('Undone; the previous settings are back'))
    expect(within(screen.getByRole('alert')).queryByRole('button')).toBeNull()
    await waitFor(() => expect(selectedMain()).toBe('Layered strategy'))
  })

  it('says nothing beyond the row for a plain switch', async () => {
    const { applied, connection } = fixture([threeTier, capture])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    fireEvent.click(await screen.findByRole('switch', { name: 'Active capture' }))
    await waitFor(() => expect(applied).toHaveLength(1))
    await waitFor(() => expect(within(row('Active capture')).getByText('Running')).toBeTruthy())
    expect(checked(screen.getByRole('switch', { name: 'Active capture' }))).toBe(true)
    expect(screen.queryByRole('alert')).toBeNull()
    // Its name opens its page; without settings of its own, the row has no gear.
    expect(within(row('Active capture')).getByRole('button', { name: 'Active capture' }).getAttribute('aria-haspopup')).toBe('dialog')
    expect(within(row('Active capture')).queryByRole('button', { name: /^Options of/u })).toBeNull()
  })

  it('turns on what an enhancement needs along with it, and says so', async () => {
    const spaces = source('memory-spaces', { ...off })
    const { applied, connection } = fixture([threeTier, needy, source('runtime'), spaces])
    render(<Composition connection={connection} language="en" system={system(allLayers)} t={translateEn} />)
    fireEvent.click(await screen.findByRole('switch', { name: 'Active capture' }))
    await waitFor(() => expect(applied).toHaveLength(1))
    expect(applied[0]!.entries).toEqual({ 'mnemon-strategy-auto-capture': { enabled: true, config: {} }, 'mnemon-source-memory-spaces': { enabled: true, config: {} } })
    const toast = await screen.findByRole('alert')
    expect(toast.textContent).toContain('Turned on “Active capture” and “Memory Spaces”, which it needs')
    expect(within(toast).getByRole('button', { name: 'Undo' })).toBeTruthy()
    await waitFor(() => expect(checked(screen.getByRole('switch', { name: 'Memory Spaces' }))).toBe(true))
  })

  it('turns off what depends on a Source along with it, and puts both back on Undo', async () => {
    const { applied, connection } = fixture([threeTier, { ...needy, ...on }, source('runtime'), source('documents'), source('memory-spaces')])
    render(<Composition connection={connection} language="en" system={system(allLayers)} t={translateEn} />)
    await waitFor(() => expect(within(row('Memory Spaces')).getByText('Running')).toBeTruthy())
    fireEvent.click(screen.getByRole('switch', { name: 'Memory Spaces' }))
    await waitFor(() => expect(applied).toHaveLength(1))
    expect(applied[0]!.entries).toEqual({ 'mnemon-source-memory-spaces': { enabled: false, config: {} }, 'mnemon-strategy-auto-capture': { enabled: false, config: {} } })
    const toast = await screen.findByRole('alert')
    expect(toast.textContent).toContain('Turned off “Memory Spaces” and “Active capture”, which depended on it')
    await waitFor(() => expect(within(row('Memory Spaces')).getByText('Off')).toBeTruthy())
    fireEvent.click(within(toast).getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(applied).toHaveLength(2))
    expect(applied[1]!.entries).toEqual({ 'mnemon-source-memory-spaces': { enabled: true, config: {} }, 'mnemon-strategy-auto-capture': { enabled: true, config: {} } })
  })

  it('keeps the last running Source on, and says why', async () => {
    const { connection } = fixture([threeTier, source('runtime', off), source('documents', off), source('memory-spaces')])
    render(<Composition connection={connection} language="en" system={system(allLayers)} t={translateEn} />)
    await waitFor(() => expect(within(row('Memory Spaces')).getByText('Running · the only source')).toBeTruthy())
    expect(disabled(screen.getByRole('switch', { name: 'Memory Spaces' }))).toBe(true)
    expect(disabled(screen.getByRole('switch', { name: 'Runtime Memory' }))).toBe(false)
    const page = openDetails('Memory Spaces')
    expect(within(page).getByText('The only running source; it stays on')).toBeTruthy()
    expect(within(page).getByText('Needed by').nextElementSibling!.textContent).toBe('Layered strategy')
  })

  it('shows on a component\'s page what it relates to and what its switch would move', async () => {
    const { connection } = fixture([threeTier, { ...needy, ...on }, source('runtime'), source('memory-spaces')])
    render(<Composition connection={connection} language="en" system={system({ runtime: true, 'memory-spaces': true })} t={translateEn} />)
    await waitFor(() => expect(within(row('Memory Spaces')).getByText('Running')).toBeTruthy())
    // Each row names what it relates to at a glance; the page says which way.
    expect(within(row('Memory Spaces')).getByTitle('“Active capture” depends on it').textContent).toBe('Active capture')
    expect(within(row('Active capture')).getByTitle('Needs “Memory Spaces”').textContent).toBe('Memory Spaces')
    expect(within(row('Runtime Memory')).queryByTitle(/depends on it|Needs/u)).toBeNull()
    const spaces = openDetails('Memory Spaces')
    expect(within(spaces).getByText('Shipped')).toBeTruthy()
    expect(within(spaces).getByText('dsh-mnemon-source-memory-spaces')).toBeTruthy()
    expect(within(spaces).getByText('Needed by').nextElementSibling!.textContent).toBe('Active capture')
    expect(within(spaces).getByText('Turning off also turns off: “Active capture”')).toBeTruthy()
    // The page's switch is the row's.
    expect(checked(within(spaces).getByRole('switch', { name: 'Memory Spaces' }))).toBe(true)
    fireEvent.click(within(spaces).getByRole('button', { name: 'Close' }))
    // Runtime is not the only provider of anything the main Strategy needs.
    const runtime = openDetails('Runtime Memory')
    expect(within(runtime).queryByText('Needed by')).toBeNull()
  })

  it('turns off what cannot run beside a component it turns on, and names the conflict on its page', async () => {
    const light = entry('mnemon-strategy-light-context', 'dsh-mnemon-strategy-light-context', ['strategy-extension'], 'light-context', { ...on, provides: [{ id: 'strategy.projection', exclusive: true }], requires: ['strategy'] })
    const compact = entry('compact', 'acme-memory-compact', ['strategy-extension'], 'compact', { provides: [{ id: 'strategy.projection', exclusive: true }], requires: ['strategy'] })
    const { applied, connection } = fixture([threeTier, light, compact])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    await waitFor(() => expect(screen.getByRole('switch', { name: 'compact label' })).toBeTruthy())
    const page = openDetails('compact label')
    expect(within(page).getByText('Installed extension')).toBeTruthy()
    expect(within(page).getByText('acme-memory-compact')).toBeTruthy()
    expect(within(page).getByText('Cannot run beside').nextElementSibling!.textContent).toBe('Light context')
    expect(within(page).getByText('Turning on turns off: “Light context”')).toBeTruthy()
    fireEvent.click(within(page).getByRole('switch', { name: 'compact label' }))
    await waitFor(() => expect(applied).toHaveLength(1))
    expect(applied[0]!.entries).toEqual({ 'mnemon-strategy-light-context': { enabled: false, config: {} }, compact: { enabled: true, config: {} } })
    expect((await screen.findByRole('alert')).textContent).toContain('Turned on “compact label” and turned off “Light context”, which cannot run beside it')
  })

  it('turns off what only the previous main Strategy could take when switching', async () => {
    const tiered = entry('tiered', 'acme-memory-tiered', ['strategy-extension'], 'tiered', { ...on, strategyTypeId: 'default-three-tier', requires: ['strategy.default-three-tier'] })
    const { applied, connection } = fixture([threeTier, general, tiered])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    await waitFor(() => expect(selectedMain()).toBe('Layered strategy'))
    chooseMain('General strategy')
    await waitFor(() => expect(applied).toHaveLength(1))
    expect(applied[0]!.entries).toEqual({
      'mnemon-strategy-default-three-tier': { enabled: false, config: {} }, 'mnemon-strategy-general': { enabled: true, config: {} }, tiered: { enabled: false, config: {} },
    })
    expect((await screen.findByRole('alert')).textContent).toContain('Switched to “General strategy” and turned off “Layered strategy”, “tiered label”')
  })

  it('keeps the enhancements for other main Strategies in a closed group', async () => {
    const forGeneral = entry('focus-general', 'acme-memory-focus', ['strategy-extension'], 'focus-general', { strategyTypeId: 'general' })
    const { connection } = fixture([threeTier, general, capture, forGeneral])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    expect(await screen.findByRole('switch', { name: 'Active capture' })).toBeTruthy()
    expect(screen.queryByRole('switch', { name: 'focus-general label' })).toBeNull()
    const group = screen.getByRole('button', { name: 'Enhancements for other main strategies (1)' })
    expect(group.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(group)
    expect(screen.getByRole('switch', { name: 'focus-general label' })).toBeTruthy()
  })

  it('lists a Source component that is off and registered nothing, and turns it on', async () => {
    const notes = entry('notes', 'acme-memory-notes', ['source'], undefined, { label: { en: 'Notes', 'zh-CN': '笔记' }, description: { en: 'Team notes', 'zh-CN': '团队笔记' }, provides: [{ id: 'source', exclusive: false }] })
    const { applied, connection } = fixture([threeTier, source('runtime'), notes])
    render(<Composition connection={connection} language="en" system={system({ runtime: true })} t={translateEn} />)
    await waitFor(() => expect(within(row('Notes')).getByText('Off')).toBeTruthy())
    expect(within(row('Notes')).getByText('Team notes')).toBeTruthy()
    fireEvent.click(screen.getByRole('switch', { name: 'Notes' }))
    await waitFor(() => expect(applied).toEqual([{ expectedRevision: 'view-1', strategyTypeId: 'default-three-tier', entries: { notes: { enabled: true, config: {} } } }]))
  })

  it('gives a role no group knows a group of its own', async () => {
    const index = entry('index', 'acme-memory-index', ['memory-index' as MemoryPluginEntryView['roles'][number]], 'index')
    const { connection } = fixture([threeTier, capture, index])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    expect(await screen.findByRole('switch', { name: 'index label' })).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'memory-index' })).toBeTruthy()
    expect(screen.getByRole('heading', { name: 'Enhancements' })).toBeTruthy()
  })

  it('offers a search once many components are installed', async () => {
    const extras = Array.from({ length: 8 }, (_, index) => entry(`extra-${index}`, `acme-memory-extra-${index}`, ['strategy-extension'], `extra-${index}`))
    const { connection } = fixture([threeTier, capture, ...extras])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    const search = await screen.findByRole('searchbox', { name: 'Search components' })
    fireEvent.change(search, { target: { value: 'extra-3' } })
    expect(screen.getByRole('switch', { name: 'extra-3 label' })).toBeTruthy()
    expect(screen.queryByRole('switch', { name: 'Active capture' })).toBeNull()
    // Package names match too.
    fireEvent.change(search, { target: { value: 'acme-memory-extra-5' } })
    expect(screen.getByRole('switch', { name: 'extra-5 label' })).toBeTruthy()
    fireEvent.change(search, { target: { value: 'nothing like it' } })
    expect(screen.getByText('No component matches “nothing like it”.')).toBeTruthy()
  })

  it('turns a layer an earlier configuration switched off back on through its saved switch', async () => {
    const layers = { set: vi.fn(async () => {}) }
    const { applied, connection } = fixture([threeTier, source('runtime'), source('documents')])
    render(<Composition connection={connection} language="en" system={system({ runtime: true, documents: false })} layers={layers} t={translateEn} />)
    await waitFor(() => expect(within(row('Project Documents')).getByText('Off')).toBeTruthy())
    fireEvent.click(screen.getByRole('switch', { name: 'Project Documents' }))
    await waitFor(() => expect(layers.set).toHaveBeenCalledWith('documents', true))
    // Its component already runs: nothing else to switch.
    expect(applied).toEqual([])
  })

  it('says what a switch on DSH\'s component list did, and points at the board', async () => {
    const { connection, switchElsewhere } = fixture([threeTier, { ...general, ...on }])
    const { rerender } = render(<Composition connection={connection} language="en" t={translateEn} />)
    await waitFor(() => expect(problem()?.textContent).toBe('Also running, composing nothing: “General strategy”Turn off “General strategy”'))
    switchElsewhere('mnemon-strategy-default-three-tier', false)
    // DSH's plugin manager announces the switch; the page re-reads.
    rerender(<Composition connection={connection} language="en" refreshKey={1} t={translateEn} />)
    const toast = await screen.findByRole('alert')
    expect(toast.textContent).toContain('“Layered strategy” is off; “General strategy” composes memory for now')
    expect(within(toast).getByRole('button', { name: 'Show' })).toBeTruthy()
    await waitFor(() => expect(problem()?.textContent).toContain('The selected “Layered strategy” is not running; “General strategy” composes memory for now'))
  })

  it('keeps the previous choice when the switch is refused, and offers to retry', async () => {
    const { connection } = fixture([threeTier, general], { failApply: true })
    render(<Composition connection={connection} language="en" t={translateEn} />)
    await waitFor(() => expect(selectedMain()).toBe('Layered strategy'))
    chooseMain('General strategy')
    const toast = await screen.findByRole('alert')
    expect(toast.textContent).toContain(translateEn('config.strategyFailed'))
    expect(within(toast).getByRole('button', { name: 'Retry' })).toBeTruthy()
    expect(selectedMain()).toBe('Layered strategy')
  })

  it('shows the only installed main Strategy as a value rather than a choice', async () => {
    const { connection } = fixture([threeTier, capture])
    render(<Composition connection={connection} language="zh" t={translateZh} />)
    expect(await screen.findByRole('switch', { name: '主动记录' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: /^主策略 /u })).toBeNull()
    expect(screen.getByText('分层策略')).toBeTruthy()
  })

  it('names every component by its own declaration, in the active language', async () => {
    const { connection } = fixture([threeTier, focus])
    const { unmount } = render(<Composition connection={connection} language="zh-CN" t={translateZh} />)
    expect(await screen.findByRole('switch', { name: 'focus 标签' })).toBeTruthy()
    expect(screen.getByText('focus 说明')).toBeTruthy()
    expect(screen.getByText('分层策略')).toBeTruthy()
    unmount()
    render(<Composition connection={connection} language="en" t={translateEn} />)
    expect(await screen.findByRole('switch', { name: 'focus label' })).toBeTruthy()
  })

  it('says memory is off while no main Strategy runs, and turns the selected one back on', async () => {
    const { applied, connection } = fixture([{ ...threeTier, ...off }, { ...general, ...off }, capture])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    await waitFor(() => expect(problem()?.textContent).toContain('Memory is not in use: the main strategy “Layered strategy” is off'))
    // Components wait: the Host checks every write against the selected main Strategy.
    expect(disabled(screen.getByRole('switch', { name: 'Active capture' }))).toBe(true)
    fireEvent.click(within(problem()!).getByRole('button', { name: 'Turn on “Layered strategy”' }))
    expect((await screen.findByRole('alert')).textContent).toContain('Memory is back, composed by “Layered strategy”')
    expect(applied).toEqual([{ expectedRevision: 'view-1', strategyTypeId: 'default-three-tier', entries: { 'mnemon-strategy-default-three-tier': { enabled: true, config: {} } } }])
    await waitFor(() => expect(problem()).toBeNull())
    expect(disabled(screen.getByRole('switch', { name: 'Active capture' }))).toBe(false)
  })

  it('names the main Strategy composing in place of the selected one and offers both ways out', async () => {
    const { applied, connection } = fixture([{ ...threeTier, ...off }, { ...general, ...on }])
    render(<Composition connection={connection} language="zh" t={translateZh} />)
    await waitFor(() => expect(problem()?.textContent).toContain('所选的“分层策略”没有运行，记忆暂由“通用策略”组合'))
    expect(within(problem()!).getByRole('button', { name: '开启“分层策略”' })).toBeTruthy()
    fireEvent.click(within(problem()!).getByRole('button', { name: '改用“通用策略”' }))
    await waitFor(() => expect(problem()).toBeNull())
    expect(applied).toEqual([{ expectedRevision: 'view-1', strategyTypeId: 'general', entries: { 'mnemon-strategy-general': { enabled: true, config: {} } } }])
    expect(selectedMain('主策略')).toBe('通用策略')
  })

  it('turns off a second main Strategy that runs unused', async () => {
    const { applied, connection } = fixture([threeTier, { ...general, ...on }])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    await waitFor(() => expect(within(problem()!).getByRole('button', { name: 'Turn off “General strategy”' })).toBeTruthy())
    fireEvent.click(within(problem()!).getByRole('button', { name: 'Turn off “General strategy”' }))
    await waitFor(() => expect(problem()).toBeNull())
    expect(applied).toEqual([{ expectedRevision: 'view-1', strategyTypeId: 'default-three-tier', entries: { 'mnemon-strategy-general': { enabled: false, config: {} } } }])
  })

  it('offers the Source to turn on while none runs', async () => {
    const { applied, connection } = fixture([threeTier, source('runtime', off), source('documents', off)])
    render(<Composition connection={connection} language="en" system={system({ runtime: true, documents: true }, false)} t={translateEn} />)
    await waitFor(() => expect(problem()?.textContent).toContain('Memory is not in use: no memory source is running'))
    fireEvent.click(within(problem()!).getByRole('button', { name: 'Turn on “Runtime Memory”' }))
    await waitFor(() => expect(applied).toEqual([{ expectedRevision: 'view-1', strategyTypeId: 'default-three-tier', entries: { 'mnemon-source-runtime': { enabled: true, config: {} } } }]))
  })

  it('keeps every control read-only with a configuration that cannot be saved', async () => {
    const { applied, connection } = fixture([threeTier, general, capture, source('runtime'), source('documents')])
    render(<Composition connection={connection} language="en" readOnly system={system({ runtime: true, documents: true })} t={translateEn} />)
    await waitFor(() => expect(selectedMain()).toBe('Layered strategy'))
    expect(disabled(mainSelector())).toBe(true)
    expect(screen.getAllByRole('switch').every(disabled)).toBe(true)
    fireEvent.click(screen.getByRole('switch', { name: 'Active capture' }))
    expect(applied).toEqual([])
  })

  it('applies a switch once more over a change made elsewhere, keeping that change', async () => {
    const { applied, connection, external } = fixture([threeTier, { ...general, ...on }, capture])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    const toggle = await screen.findByRole('switch', { name: 'Active capture' })
    // Another window, or the component list below, moves the View first.
    external('general')
    fireEvent.click(toggle)
    await waitFor(() => expect(applied).toEqual([{ expectedRevision: 'view-9', strategyTypeId: 'general', entries: { 'mnemon-strategy-auto-capture': { enabled: true, config: {} } } }]))
    await waitFor(() => expect(checked(screen.getByRole('switch', { name: 'Active capture' }))).toBe(true))
    expect(selectedMain()).toBe('General strategy')
  })

  it('reports a write the Host still refuses after the retry', async () => {
    const { applied, connection } = fixture([threeTier, general, capture], { failApply: true })
    render(<Composition connection={connection} language="en" t={translateEn} />)
    fireEvent.click(await screen.findByRole('switch', { name: 'Active capture' }))
    expect((await screen.findByRole('alert')).textContent).toContain(translateEn('feedback.failed'))
    expect(checked(screen.getByRole('switch', { name: 'Active capture' }))).toBe(false)
    expect(applied).toEqual([])
  })

  it('shows a write in progress on its row and locks the rest', async () => {
    let release!: () => void
    const holdApply = new Promise<void>(resolve => { release = resolve })
    const { connection } = fixture([threeTier, general, capture, focus], { holdApply })
    render(<Composition connection={connection} language="en" t={translateEn} />)
    fireEvent.click(await screen.findByRole('switch', { name: 'Active capture' }))
    await waitFor(() => expect(within(row('Active capture')).getByText('Turning on…')).toBeTruthy())
    expect(checked(screen.getByRole('switch', { name: 'Active capture' }))).toBe(true)
    expect(disabled(screen.getByRole('switch', { name: 'focus label' }))).toBe(true)
    release()
    await waitFor(() => expect(disabled(screen.getByRole('switch', { name: 'focus label' }))).toBe(false))
  })

  it('stays usable when the switches it applies are announced while it writes', async () => {
    let release!: () => void
    const holdApply = new Promise<void>(resolve => { release = resolve })
    const { applied, connection } = fixture([threeTier, general, capture], { holdApply })
    const { rerender } = render(<Composition connection={connection} language="en" t={translateEn} />)
    fireEvent.click(await screen.findByRole('switch', { name: 'Active capture' }))
    // DSH's plugin manager announces the switch before the write returns.
    rerender(<Composition connection={connection} language="en" refreshKey={1} t={translateEn} />)
    release()
    await waitFor(() => expect(applied).toHaveLength(1))
    await waitFor(() => expect(disabled(screen.getByRole('switch', { name: 'Active capture' }))).toBe(false))
    expect(checked(screen.getByRole('switch', { name: 'Active capture' }))).toBe(true)
  })

  it('edits the options a component declares on its page, and undoes them', async () => {
    const limit = { key: 'maxProjectionCharacters', input: 'number' as const, defaultValue: 4096, minimum: 1, maximum: 10_000_000, label: { en: 'Projection limit', 'zh-CN': '投影上限' } }
    const light = entry('mnemon-strategy-light-context', 'dsh-mnemon-strategy-light-context', ['strategy-extension'], 'light-context', { fields: [limit] })
    const { applied, connection } = fixture([threeTier, light])
    render(<Composition connection={connection} language="en" t={translateEn} />)
    // A component with options has a gear, which opens its page.
    fireEvent.click(await within(await waitFor(() => row('Light context'))).findByRole('button', { name: 'Options of “Light context”' }))
    const page = screen.getByRole('dialog', { name: 'Light context' })
    const input = within(page).getByRole('textbox', { name: 'Projection limit' }) as HTMLInputElement
    expect(input.value).toBe('4096')
    expect(within(page).getByText('Default')).toBeTruthy()
    // Apply appears once something changed, and waits while a value is refused.
    expect(within(page).queryByRole('button', { name: 'Apply' })).toBeNull()
    fireEvent.change(input, { target: { value: '12.5' } })
    expect(within(page).getByText('Enter a whole number from 1 to 10000000')).toBeTruthy()
    expect(within(page).getByRole('alert').textContent).toBe('Fix the marked options first')
    const apply = () => within(page).getByRole('button', { name: 'Apply' }) as HTMLButtonElement
    expect(apply().disabled).toBe(true)
    fireEvent.change(input, { target: { value: '2048' } })
    expect(apply().disabled).toBe(false)
    fireEvent.click(apply())
    await waitFor(() => expect(applied).toHaveLength(1))
    expect(applied[0]!.entries).toEqual({ 'mnemon-strategy-light-context': { enabled: false, config: { maxProjectionCharacters: 2048 } } })
    const toast = await waitFor(() => screen.getAllByRole('alert').find(alert => alert.textContent?.includes('Updated the options of “Light context”'))!)
    // The page stays open with the saved value and nothing left to apply.
    await waitFor(() => expect(within(screen.getByRole('dialog', { name: 'Light context' })).queryByRole('button', { name: 'Apply' })).toBeNull())
    expect((within(screen.getByRole('dialog', { name: 'Light context' })).getByRole('textbox', { name: 'Projection limit' }) as HTMLInputElement).value).toBe('2048')
    fireEvent.click(within(toast).getByRole('button', { name: 'Undo' }))
    await waitFor(() => expect(applied).toHaveLength(2))
    expect(applied[1]!.entries).toEqual({ 'mnemon-strategy-light-context': { enabled: false, config: {} } })
  })

  it('chooses the Sources an option lists from the running ones, and puts an option back to its default', async () => {
    const resident = { key: 'residentSourceKeys', input: 'source-list' as const, label: { en: 'Resident sources', 'zh-CN': '常驻 Source' } }
    const sources = [
      { sourceInstanceKey: 'runtime/default', sourceTypeId: 'runtime', packageName: 'dsh-mnemon-source-runtime', role: 'hot-context', label: 'Runtime Memory' },
      { sourceInstanceKey: 'memory-spaces/default', sourceTypeId: 'memory-spaces', packageName: 'dsh-mnemon-source-memory-spaces', role: 'durable-evidence', label: 'Memory Spaces' },
    ]
    const { applied, connection } = fixture([{ ...threeTier, ...off }, main('general', { ...on, fields: [resident], config: { residentSourceKeys: ['memory-spaces/default'] } }), source('runtime'), source('memory-spaces')], { sources, strategyTypeId: 'general' })
    render(<Composition connection={connection} language="en" t={translateEn} />)
    // The main Strategy row's gear opens the selected Strategy's page.
    fireEvent.click(await screen.findByRole('button', { name: 'Details of “General strategy”' }))
    const page = screen.getByRole('dialog', { name: 'General strategy' })
    expect(within(page).getByText('Current main strategy')).toBeTruthy()
    expect((within(page).getByRole('checkbox', { name: 'Memory Spaces' }) as HTMLInputElement).checked).toBe(true)
    fireEvent.click(within(page).getByRole('checkbox', { name: 'Runtime Memory' }))
    fireEvent.click(within(page).getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(applied).toHaveLength(1))
    expect(applied[0]).toEqual({ expectedRevision: 'view-1', strategyTypeId: 'general', entries: { 'mnemon-strategy-general': { enabled: true, config: { residentSourceKeys: ['memory-spaces/default', 'runtime/default'] } } } })
    const again = screen.getByRole('dialog', { name: 'General strategy' })
    await waitFor(() => expect(within(again).queryByRole('button', { name: 'Apply' })).toBeNull())
    fireEvent.click(within(again).getByRole('button', { name: 'Reset to default' }))
    expect(within(again).getByText('Default')).toBeTruthy()
    fireEvent.click(within(again).getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(applied).toHaveLength(2))
    expect(applied[1]!.entries).toEqual({ 'mnemon-strategy-general': { enabled: true, config: {} } })
  })

  it('shows the settings a component contributed on its page, behind a gear on its row', async () => {
    const notes = entry('notes', 'acme-memory-notes', ['source'], 'notes', { ...on, label: { en: 'Notes', 'zh-CN': '笔记' }, provides: [{ id: 'source', exclusive: false }] })
    const { connection } = fixture([threeTier, source('runtime'), notes])
    const render_ = vi.fn((component: MemoryPluginEntryView, writable: boolean) => <p>Notes settings for {component.packageName}, writable {String(writable)}</p>)
    const componentSettings: ComponentSettingsRenderer = { has: packageName => packageName === 'acme-memory-notes', render: render_ }
    render(<Composition connection={connection} language="en" system={system({ runtime: true })} componentSettings={componentSettings} t={translateEn} />)
    await waitFor(() => expect(within(row('Notes')).getByRole('button', { name: 'Options of “Notes”' })).toBeTruthy())
    // A component that contributed nothing and declares no options has no gear.
    expect(within(row('Runtime Memory')).queryByRole('button', { name: /^Options of/u })).toBeNull()
    fireEvent.click(within(row('Notes')).getByRole('button', { name: 'Options of “Notes”' }))
    const page = screen.getByRole('dialog', { name: 'Notes' })
    expect(within(page).getByText('Notes settings for acme-memory-notes, writable true')).toBeTruthy()
    expect(render_.mock.calls[0]![0].entryId).toBe('notes')
    // A component that contributed nothing gets the page its declaration gives.
    fireEvent.click(within(page).getByRole('button', { name: 'Close' }))
    expect(within(openDetails('Runtime Memory')).queryByText(/^Notes settings/u)).toBeNull()
  })

  it('opens a related component\'s page from a name, and goes back', async () => {
    const { connection } = fixture([threeTier, needy, source('runtime'), source('memory-spaces')])
    render(<Composition connection={connection} language="en" system={system(allLayers)} t={translateEn} />)
    await waitFor(() => row('Active capture'))
    // A chip on the row opens the component it names.
    fireEvent.click(within(row('Active capture')).getByRole('button', { name: 'Memory Spaces' }))
    expect(screen.getByRole('dialog', { name: 'Memory Spaces' })).toBeTruthy()
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Close' }))
    // On a page, a related name opens that page in place, with a way back.
    const page = openDetails('Active capture')
    fireEvent.click(within(page).getByRole('button', { name: 'Memory Spaces' }))
    const related = screen.getByRole('dialog', { name: 'Memory Spaces' })
    fireEvent.click(within(related).getByRole('button', { name: 'Back to “Active capture”' }))
    expect(screen.getByRole('dialog', { name: 'Active capture' })).toBeTruthy()
    expect(within(screen.getByRole('dialog')).queryByRole('button', { name: /^Back to/u })).toBeNull()
  })

  it('shows one component\'s page alone, as DSH\'s row page does, with related pages over it', async () => {
    const { applied, connection } = fixture([threeTier, needy, source('runtime'), source('memory-spaces')])
    render(<Composition connection={connection} language="en" system={system(allLayers)} page="dsh-mnemon-strategy-auto-capture" t={translateEn} />)
    // The page stands in place of the board: its state, switch and relations.
    const own = await screen.findByRole('switch', { name: 'Active capture' })
    expect(screen.queryByRole('region', { name: 'Memory composition' })).toBeNull()
    expect(screen.queryByRole('switch', { name: 'Runtime memory' })).toBeNull()
    fireEvent.click(own)
    await waitFor(() => expect(applied).toHaveLength(1))
    expect(applied[0]!.entries['mnemon-strategy-auto-capture']).toEqual({ enabled: true, config: {} })
    // A related name opens that component's page over it, without leaving the row page.
    fireEvent.click(screen.getByRole('button', { name: 'Memory Spaces' }))
    const related = screen.getByRole('dialog', { name: 'Memory Spaces' })
    fireEvent.click(within(related).getByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(screen.getByRole('switch', { name: 'Active capture' })).toBeTruthy()
  })
})
