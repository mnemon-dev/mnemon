// @vitest-environment jsdom
import { cleanup, configure, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { translateEn } from 'dsh-mnemon/client'
import copy from '../presentation/locales.json' with { type: 'json' }

// Load the installed Core's actual DSH browser artifact; no repository source alias.
vi.mock('dsh-mnemon/client', async () => {
  const { createRequire } = await import('node:module')
  const { loadMemoryClientArtifact } = await import('dsh-mnemon/testing')
  return loadMemoryClientArtifact(createRequire(import.meta.url).resolve('dsh-mnemon/client'), {
    react: await vi.importActual('react'),
    'react/jsx-runtime': await vi.importActual('react/jsx-runtime'),
    'react-dom': await vi.importActual('react-dom'),
    '@deepseek-ai/dsh-client-ui-primitives': await vi.importActual('@deepseek-ai/dsh-client-ui-primitives'),
  })
})
afterEach(cleanup)
// The choice to find related memories lasts a browser session; each test starts collapsed.
afterEach(() => sessionStorage.clear())
// CI's packed-plugin job runs these tests in four parallel standalone installs, where this file
// measured 13 times slower than in the workspace (40.5 s against 3.1 s). Allow for that load.
configure({ asyncUtilTimeout: 5_000 })

import { MemorySpacesSourcePage } from '../src/client.ts'
import type { EntityMemoriesView, EntityRelatedView, EntityView, Insight } from '../src/contracts.ts'

/** The Source's own copy first, then the Core's, as the page resolves it. */
function t(key: string, params: Record<string, unknown> = {}): string {
  const own = (copy.en as Record<string, string>)[key]
  const text = own ?? translateEn(key as Parameters<typeof translateEn>[0], params)
  return own === undefined ? text : text.replace(/\{(\w+)\}/g, (match, name: string) => name in params ? String(params[name]) : match)
}

const CAPABILITIES = { search: true, browse: true, graph: true, entities: true, related: true, remember: true, link: true, forget: true, writeMode: 'exact', deletionMode: 'soft' } as const
const SPACE = {
  id: 'work', name: 'Work', description: 'Project memory', active: true, providerEnabled: true, dbPath: '', mnemonDefault: true, healthy: false, statusLoading: true,
  createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
  provider: { id: 'mnemon-native', label: 'Mnemon Native', kind: 'local', location: '', apiKeyConfigured: false, settings: {}, configuredSecrets: [], capabilities: CAPABILITIES },
}
const SOURCE = { memoryBodyId: 'work', memoryBodyName: 'Work', providerId: 'mnemon-native', providerLabel: 'Mnemon Native', mode: 'entities', status: 'ready', itemCount: 2, memoryCount: 70, complete: true } as const

function memory(id: string, content: string): Insight {
  return { id, content, importance: 3, entities: ['Atlas'], memoryBodyId: 'work', memoryBodyName: 'Work', memoryProviderId: 'mnemon-native', memoryProviderLabel: 'Mnemon Native', memoryCapabilities: CAPABILITIES }
}
const atlas = Array.from({ length: 60 }, (_, index) => memory(`a${index + 1}`, `Atlas memory ${index + 1}`))
const rail: EntityView = { items: [{ entity: 'Atlas', count: 60 }, { entity: 'SQLite', count: 2 }], insights: [], total: 2, complete: true, sources: [SOURCE] }

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason: unknown) => void } {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail })
  return { promise, resolve, reject }
}

type Handler = (input: Record<string, unknown>) => unknown | Promise<unknown>
function page(handlers: Partial<Record<string, Handler>>, options: { writable?: boolean } = {}) {
  const read = vi.fn(async (operation: string, input?: unknown) => {
    const handler = handlers[operation]
    const value = handler !== undefined ? await handler((input ?? {}) as Record<string, unknown>)
      : operation === 'status-summary' ? { writeEnabled: false, memoryBodies: [], defaultRecallLimit: 12 }
      : operation === 'body-directory' ? { items: [SPACE], providers: [], total: 1, activeCount: 1, directory: '/data', generatedAt: 'now' }
      : operation === 'entities' ? rail
      : operation === 'entity-memories' ? memoriesPage(input as Record<string, unknown>)
      : operation === 'entity-related' ? { entity: 'Atlas', items: [], sources: [] }
      : []
    return { revision: 'r1', value: value as never }
  })
  const mutate = vi.fn(async () => ({ revision: 'r2', value: {} as never }))
  render(<MemorySpacesSourcePage page="entities" sourceTypeId="memory-spaces" sourceInstanceKey="source:spaces" sourceInstances={[]} locale="en" writable={options.writable === true} management={{ sourceInstanceKey: 'source:spaces', revision: 'r1', read, mutate }} />)
  return read
}

function memoriesPage(input: Record<string, unknown>): EntityMemoriesView {
  const offset = Number(input.offset ?? 0)
  const limit = Number(input.limit ?? 48)
  const items = input.entity === 'SQLite' ? [memory('s1', 'SQLite memory 1'), memory('s2', 'SQLite memory 2')] : input.entity === 'Atlas' ? atlas : []
  return { entity: String(input.entity), total: items.length, offset, items: items.slice(offset, offset + limit), complete: true, sources: [SOURCE] }
}

const skeletons = (container: ParentNode) => container.querySelectorAll('[data-placeholder]').length
const relatedReads = (read: ReturnType<typeof page>) => read.mock.calls.filter(([operation]) => operation === 'entity-related')
const findRelated = () => screen.getByRole('button', { name: t('entities.findRelated') })

describe('Entities page', { timeout: 30_000 }, () => {
  it('lists every memory that carries the selected entity, and finds related memories only when asked', async () => {
    const related: EntityRelatedView = { entity: 'Atlas', items: [memory('r1', 'Release checklist mentions Atlas')], sources: [SOURCE] }
    const read = page({ 'entity-related': () => related })
    fireEvent.click(await screen.findByRole('button', { name: /^Atlas/u }))
    // The count shows at once from the rail, the same number the list then has.
    const heading = await screen.findByRole('heading', { name: 'Atlas', level: 3 })
    expect(within(heading.closest('div')!.parentElement!).getByText('60')).not.toBeNull()
    expect(await screen.findByText('Atlas memory 1')).not.toBeNull()
    expect(screen.queryByText('Atlas memory 7')).toBeNull()
    expect(screen.getByText(t('common.showing', { visible: 6, total: 60 }))).not.toBeNull()
    expect(screen.getByText(t('entities.sourceReady', { count: 2 }))).not.toBeNull()
    expect(read).toHaveBeenCalledWith('entity-memories', { entity: 'Atlas', offset: 0, limit: 48 })
    // Related memories stay collapsed and cost no recall until asked for.
    expect(screen.getByRole('heading', { name: t('entities.relatedTitle'), level: 3 })).not.toBeNull()
    expect(findRelated().getAttribute('aria-expanded')).toBe('false')
    expect(relatedReads(read)).toHaveLength(0)
    fireEvent.click(findRelated())
    expect(await screen.findByText('Release checklist mentions Atlas')).not.toBeNull()
    expect(read).toHaveBeenCalledWith('entity-related', { entity: 'Atlas', limit: 20, view: expect.any(String) })
    const hide = screen.getByRole('button', { name: t('entities.hideRelated') })
    expect(hide.getAttribute('aria-expanded')).toBe('true')
    // Hiding and opening again shows what was found, without another recall.
    fireEvent.click(hide)
    expect(screen.queryByText('Release checklist mentions Atlas')).toBeNull()
    fireEvent.click(findRelated())
    expect(await screen.findByText('Release checklist mentions Atlas')).not.toBeNull()
    expect(relatedReads(read)).toHaveLength(1)
  })

  it('remembers the choice to find related memories for the next entity until it is hidden', async () => {
    const read = page({ 'entity-related': input => ({ entity: String(input.entity), items: [memory(`r-${String(input.entity)}`, `Related to ${String(input.entity)}`)], sources: [] }) })
    fireEvent.click(await screen.findByRole('button', { name: /^Atlas/u }))
    await screen.findByText('Atlas memory 1')
    fireEvent.click(findRelated())
    expect(await screen.findByText('Related to Atlas')).not.toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /^SQLite/u }))
    expect(await screen.findByText('Related to SQLite')).not.toBeNull()
    expect(relatedReads(read)).toHaveLength(2)
    fireEvent.click(screen.getByRole('button', { name: t('entities.hideRelated') }))
    fireEvent.click(screen.getByRole('button', { name: /^Atlas/u }))
    await screen.findByText('Atlas memory 1')
    expect(findRelated().getAttribute('aria-expanded')).toBe('false')
    expect(relatedReads(read)).toHaveLength(2)
  })

  it('finds related memories at once when no memory carries the name', async () => {
    const read = page({ 'entity-related': () => ({ entity: 'Gateway', items: [memory('g1', 'The gateway fronts every Atlas call')], sources: [] }) })
    const input = await screen.findByRole('textbox', { name: t('entities.nameAria') })
    fireEvent.change(input, { target: { value: 'Gateway' } })
    fireEvent.click(screen.getByRole('button', { name: t('entities.action') }))
    expect(await screen.findByText(t('entities.noCarrying'))).not.toBeNull()
    expect(await screen.findByText('The gateway fronts every Atlas call')).not.toBeNull()
    expect(relatedReads(read)).toHaveLength(1)
    // With related memories as the only content, there is nothing to collapse.
    expect(screen.queryByRole('button', { name: t('entities.hideRelated') })).toBeNull()
  })

  it('drops a forgotten memory at once and keeps the rest in place until the refresh replaces them', async () => {
    const gateway = [memory('g1', 'The gateway fronts every Atlas call'), memory('g2', 'The gateway logs every request')]
    const again = deferred<EntityRelatedView>()
    let attempts = 0
    const read = page({
      'status-summary': () => ({ writeEnabled: true, memoryBodies: [], defaultRecallLimit: 12 }),
      'entity-related': () => (attempts += 1) === 1 ? { entity: 'Gateway', items: gateway, sources: [] } : again.promise,
    }, { writable: true })
    fireEvent.change(await screen.findByRole('textbox', { name: t('entities.nameAria') }), { target: { value: 'Gateway' } })
    fireEvent.click(screen.getByRole('button', { name: t('entities.action') }))
    const forgotten = await screen.findByText('The gateway logs every request')
    fireEvent.click(within(forgotten.closest('article')!).getByRole('button', { name: t('card.forget') }))
    fireEvent.click(await screen.findByRole('button', { name: t('card.confirmForget') }))
    await waitFor(() => expect(screen.queryByText('The gateway logs every request')).toBeNull())
    // The refresh reads related memories again; the one left stays shown while it waits.
    await waitFor(() => expect(relatedReads(read)).toHaveLength(2))
    expect(screen.getByText('The gateway fronts every Atlas call')).not.toBeNull()
    again.resolve({ entity: 'Gateway', items: [gateway[0]!], sources: [] })
    const results = screen.getByRole('region', { name: t('entities.relatedTitle') }).closest('section')!
    await waitFor(() => expect(results.getAttribute('aria-busy')).toBeNull())
    expect(screen.getByText('The gateway fronts every Atlas call')).not.toBeNull()
  })

  it('shows the spaces from the directory before their entity indexes arrive', async () => {
    const pending = deferred<EntityView>()
    page({ entities: () => pending.promise })
    expect(await screen.findByText('Work')).not.toBeNull()
    expect(screen.getByText(t('readSources.status.loading'))).not.toBeNull()
    pending.resolve(rail)
    expect(await screen.findByText(t('entities.sourceReady', { count: 2 }))).not.toBeNull()
  })

  it('keeps the latest selection when an earlier read answers late', async () => {
    const late = deferred<EntityMemoriesView>()
    page({ 'entity-memories': input => input.entity === 'Atlas' ? late.promise : memoriesPage(input) })
    fireEvent.click(await screen.findByRole('button', { name: /^Atlas/u }))
    fireEvent.click(screen.getByRole('button', { name: /^SQLite/u }))
    expect(await screen.findByText('SQLite memory 1')).not.toBeNull()
    late.resolve(memoriesPage({ entity: 'Atlas', offset: 0, limit: 48 }))
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(screen.queryByText('Atlas memory 1')).toBeNull()
    expect(screen.getByRole('heading', { name: 'SQLite', level: 3 })).not.toBeNull()
  })

  it('loads the next page only when the revealed list reaches the memories already read', async () => {
    // A Host may answer with fewer memories than asked for; the page reads on from where it is.
    const read = page({ 'entity-memories': input => memoriesPage({ ...input, limit: Math.min(Number(input.limit ?? 48), 12) }) })
    fireEvent.click(await screen.findByRole('button', { name: /^Atlas/u }))
    await screen.findByText('Atlas memory 6')
    fireEvent.click(screen.getByRole('button', { name: t('common.showMore', { count: 6 }) }))
    expect(await screen.findByText('Atlas memory 12')).not.toBeNull()
    expect(read.mock.calls.filter(([operation]) => operation === 'entity-memories')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: t('common.showMore', { count: 6 }) }))
    expect(await screen.findByText('Atlas memory 18')).not.toBeNull()
    expect(read).toHaveBeenCalledWith('entity-memories', { entity: 'Atlas', offset: 12, limit: 48 })
    expect(screen.getByText(t('common.showing', { visible: 18, total: 60 }))).not.toBeNull()
  })

  it('shows placeholders only for a read that takes a while', async () => {
    const slow = deferred<EntityMemoriesView>()
    const related = deferred<EntityRelatedView>()
    page({ 'entity-memories': () => slow.promise, 'entity-related': () => related.promise })
    fireEvent.click(await screen.findByRole('button', { name: /^Atlas/u }))
    const section = screen.getByRole('heading', { name: 'Atlas', level: 3 }).closest('section')!
    expect(skeletons(section)).toBe(0)
    await waitFor(() => expect(skeletons(section)).toBeGreaterThan(0), { timeout: 1_000 })
    slow.resolve(memoriesPage({ entity: 'Atlas', offset: 0, limit: 48 }))
    related.resolve({ entity: 'Atlas', items: [], sources: [] })
    expect(await screen.findByText('Atlas memory 1')).not.toBeNull()
    await waitFor(() => expect(skeletons(section)).toBe(0))
  })

  it('retries a failed related read without dropping the memories already shown', async () => {
    let attempts = 0
    page({ 'entity-related': () => {
      attempts += 1
      if (attempts === 1) throw new Error('recall timed out')
      return { entity: 'Atlas', items: [memory('r1', 'Recovered related memory')], sources: [] }
    } })
    fireEvent.click(await screen.findByRole('button', { name: /^Atlas/u }))
    await screen.findByText('Atlas memory 1')
    fireEvent.click(findRelated())
    expect(await screen.findByText('recall timed out')).not.toBeNull()
    expect(screen.getByText('Atlas memory 1')).not.toBeNull()
    fireEvent.click(screen.getByRole('button', { name: t('entities.retry') }))
    expect(await screen.findByText('Recovered related memory')).not.toBeNull()
    expect(screen.getByText('Atlas memory 1')).not.toBeNull()
  })
})

