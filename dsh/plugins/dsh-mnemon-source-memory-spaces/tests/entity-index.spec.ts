import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { resolveMemorySpacesConfig } from '../src/config.ts'
import type { ProcessRunner } from '../src/providers/process.ts'
import { createRunner } from '../src/runner.ts'
import type { MemorySpacesService } from '../src/service.ts'
import { buildSpaceEntityIndex, mergeEntityCounts, memoriesWithEntity } from '../src/entity-index.ts'
import { createRegistry, createService, installedCliStub } from './providers.ts'

const FAKE_CLI = installedCliStub()
const temporaryDirectories: string[] = []

afterEach(() => {
  vi.useRealTimers()
  for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

/** A Native store whose memories tag entities with mixed spellings, plus one only mentioning Atlas. */
const MEMORIES = [
  { id: 'm1', content: 'Atlas stores its index in SQLite.', category: 'decision', importance: 5, created_at: '2026-01-03T00:00:00Z', entities: ['Atlas', 'SQLite'] },
  { id: 'm2', content: 'The atlas release gate is green.', category: 'fact', importance: 3, created_at: '2026-01-05T00:00:00Z', entities: ['atlas'] },
  { id: 'm3', content: 'Atlas dashboard has one owner.', category: 'fact', importance: 3, created_at: '2026-01-04T00:00:00Z', entities: ['Atlas', 'Dashboard', 'ATLAS'] },
  { id: 'm4', content: 'SQLite runs in WAL mode.', category: 'fact', importance: 4, created_at: '2026-01-02T00:00:00Z', entities: ['SQLite'] },
  { id: 'm5', content: 'The release checklist mentions Atlas.', category: 'context', importance: 2, created_at: '2026-01-01T00:00:00Z', entities: ['Release'] },
  { id: 'm6', content: 'An unrelated note.', category: 'general', importance: 1, created_at: '2026-01-01T00:00:00Z' },
]

/** Recall's ENTITY ranking for Atlas: the tagged memories score highest. */
const RECALL = [
  { id: 'm1', content: MEMORIES[0]!.content, score: 0.9 },
  { id: 'm2', content: MEMORIES[1]!.content, score: 0.55 },
  { id: 'm3', content: MEMORIES[2]!.content, score: 0.5 },
  { id: 'm5', content: MEMORIES[4]!.content, score: 0.45 },
  { id: 'm4', content: MEMORIES[3]!.content, score: 0.3 },
  { id: 'm6', content: MEMORIES[5]!.content, score: 0.1 },
]

function fixture(): { service: MemorySpacesService; process: ReturnType<typeof vi.fn<ProcessRunner>>; state: { oplog: number; healthy: boolean } } {
  const state = { oplog: 8, healthy: true }
  const process = vi.fn<ProcessRunner>(async (_command, args) => {
    if (args.includes('--version')) return { stdout: 'mnemon version 0.2.9\n', stderr: '', exitCode: 0 }
    if (args.includes('status')) {
      if (!state.healthy) return { stdout: '', stderr: 'database locked', exitCode: 1 }
      return { stdout: JSON.stringify({ total_insights: MEMORIES.length, deleted_insights: 0, edge_count: 4, oplog_count: state.oplog, db_size_bytes: 4096, top_entities: [{ entity: 'Atlas', count: 3 }] }), stderr: '', exitCode: 0 }
    }
    if (args.includes('recall') && args.includes('--readonly')) return { stdout: JSON.stringify({ results: MEMORIES }), stderr: '', exitCode: 0 }
    if (args.includes('recall')) return { stdout: JSON.stringify({ results: RECALL }), stderr: '', exitCode: 0 }
    if (args.includes('remember')) return { stdout: JSON.stringify({ id: 'm7', action: 'added' }), stderr: '', exitCode: 0 }
    return { stdout: '{}', stderr: '', exitCode: 0 }
  })
  const dataDir = mkdtempSync(join(tmpdir(), 'dsh-mnemon-entities-'))
  temporaryDirectories.push(dataDir)
  mkdirSync(join(dataDir, 'data', 'work'), { recursive: true })
  writeFileSync(join(dataDir, 'data', 'work', 'mnemon.db'), 'fixture database')
  writeFileSync(join(dataDir, 'active'), 'work\n')
  const config = resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir, store: 'work', timeoutMs: 4321, defaultRecallLimit: 7, writeEnabled: true })
  const runner = createRunner(config, process)
  return { service: createService(runner, config, createRegistry(runner, true)), process, state }
}

const dumps = (process: ReturnType<typeof vi.fn<ProcessRunner>>) => process.mock.calls.filter(([, args]) => args.includes('recall') && args.includes('--readonly')).length
const statuses = (process: ReturnType<typeof vi.fn<ProcessRunner>>) => process.mock.calls.filter(([, args]) => args.includes('status')).length

describe('entity index', () => {
  it('merges spellings and counts each memory once per entity', () => {
    const index = buildSpaceEntityIndex(MEMORIES, MEMORIES.length, true)
    expect(index.memories.map(memory => memory.id)).toEqual(['m1', 'm2', 'm3', 'm4', 'm5'])
    expect(mergeEntityCounts([index]).items).toEqual([
      { entity: 'Atlas', count: 3 }, { entity: 'SQLite', count: 2 }, { entity: 'Dashboard', count: 1 }, { entity: 'Release', count: 1 },
    ])
    const memories = memoriesWithEntity([index], 'atlas').map(memory => memory.id)
    // Importance first, then the newest: the order Mnemon Native lists a store in.
    expect(memories).toEqual(['m1', 'm2', 'm3'])
  })
})

describe('Memory Spaces entity reads', () => {
  it('counts every memory that carries an entity, not the Provider top list', async () => {
    const { service } = fixture()
    const view = await service.entities()
    expect(view).toMatchObject({
      items: [{ entity: 'Atlas', count: 3 }, { entity: 'SQLite', count: 2 }, { entity: 'Dashboard', count: 1 }, { entity: 'Release', count: 1 }],
      insights: [],
      total: 4,
      complete: true,
      sources: [{ memoryBodyId: 'work', mode: 'entities', status: 'ready', itemCount: 4, memoryCount: 6, complete: true }],
    })
  })

  it('lists every memory that carries the entity, matching its count, without relevance fields', async () => {
    const { service } = fixture()
    const page = await service.entityMemories('ATLAS')
    expect(page).toMatchObject({ entity: 'Atlas', total: 3, offset: 0, complete: true })
    expect(page.items.map(item => item.id)).toEqual(['m1', 'm2', 'm3'])
    expect(page.items[0]).toMatchObject({ memoryBodyId: 'work', memoryProviderId: 'mnemon-native', entities: ['Atlas', 'SQLite'] })
    expect(page.items.every(item => item.score === undefined && item.relevanceTier === undefined)).toBe(true)
    await expect(service.entityMemories('Atlas', 1, 1)).resolves.toMatchObject({ total: 3, offset: 1, items: [{ id: 'm2' }] })
    await expect(service.entityMemories('Nobody')).resolves.toMatchObject({ entity: 'Nobody', total: 0, items: [] })
    // The first page also answers the existing entities(entity) call.
    await expect(service.entities('atlas', 2)).resolves.toMatchObject({ selected: 'Atlas', insights: [{ id: 'm1' }, { id: 'm2' }] })
  })

  it('relates other memories to the entity after leaving out those that carry it', async () => {
    const { service, process } = fixture()
    const related = await service.entityRelated('Atlas')
    // Strict recall would spend its places on m1-m3; leaving them out first keeps m5 and m4.
    expect(related.items.map(item => item.id)).toEqual(['m5', 'm4'])
    expect(related.entity).toBe('Atlas')
    expect(process).toHaveBeenCalledWith(FAKE_CLI, expect.arrayContaining(['recall', 'Atlas', '--intent', 'ENTITY']), expect.anything())
  })

  it('reads one status and one listing per space for a rail and a selection together', async () => {
    const { service, process } = fixture()
    await Promise.all([service.entities(), service.entityMemories('Atlas'), service.entityRelated('Atlas')])
    expect(statuses(process)).toBe(1)
    expect(dumps(process)).toBe(1)
  })

  it('keeps a space index while its statistics hold and rebuilds it after a change or a write', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    const { service, process, state } = fixture()
    await service.entities()
    expect([statuses(process), dumps(process)]).toEqual([1, 1])

    // A selection soon after reuses the health check its index was read with.
    vi.setSystemTime(Date.now() + 5_000)
    await service.entityMemories('Atlas')
    expect([statuses(process), dumps(process)]).toEqual([1, 1])

    // The rail always checks again; unchanged statistics keep the index.
    await service.entities()
    expect([statuses(process), dumps(process)]).toEqual([2, 1])

    // A write from outside DSH, such as the Mnemon CLI, moves the operation log.
    state.oplog += 1
    await service.entities()
    expect([statuses(process), dumps(process)]).toEqual([3, 2])

    // A write through this Source drops the index at once, without waiting for statistics.
    await service.remember({ content: 'Atlas ships weekly.', entities: ['Atlas'], memoryBodyId: 'work' })
    await service.entityMemories('Atlas')
    expect([statuses(process), dumps(process)]).toEqual([4, 3])

    // An older health check no longer stands in for a selection.
    vi.setSystemTime(Date.now() + 11_000)
    await service.entityMemories('Atlas')
    expect([statuses(process), dumps(process)]).toEqual([5, 3])
  })

  it('cancels the related read a page view left for a newer selection, and only that one', async () => {
    const { service, process } = fixture()
    await service.entities()
    let release!: () => void
    const blocked = new Promise<void>(resolve => { release = resolve })
    const recall = process.getMockImplementation()!
    process.mockImplementation(async (command, args, options) => {
      if (args.includes('recall') && !args.includes('--readonly') && args.includes('Atlas')) await blocked
      return recall(command, args, options)
    })
    const first = service.entityRelated('Atlas', 20, undefined, 'view-1')
    const other = service.entityRelated('SQLite', 20, undefined, 'view-2')
    const second = service.entityRelated('SQLite', 20, undefined, 'view-1')
    release()
    await expect(first).rejects.toThrow('superseded')
    await expect(second).resolves.toMatchObject({ entity: 'SQLite' })
    await expect(other).resolves.toMatchObject({ entity: 'SQLite' })
  })

  it('shows an unhealthy space as unavailable and lists nothing from it', async () => {
    const { service, state } = fixture()
    state.healthy = false
    await expect(service.entities()).resolves.toMatchObject({
      items: [], total: 0, sources: [{ memoryBodyId: 'work', mode: 'entities', status: 'unavailable' }],
    })
    await expect(service.entityRelated('Atlas')).resolves.toMatchObject({ items: [], sources: [] })
  })

  it('uses a Provider entity index when it has one and marks a partial index', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    const { service } = fixture()
    const entityIndex = vi.fn(async () => ({
      memories: [{ id: 'h1', content: 'Hindsight keeps an Atlas note.', importance: 4, entities: ['Atlas'], score: 0.8 }],
      complete: false,
    }))
    const provider = {
      id: 'hindsight' as const,
      discover: vi.fn(async () => [{ externalId: 'bank-1', name: 'Product bank', description: 'Mapped from Hindsight.', connection: { bankId: 'bank-1', budget: 'mid' } }]),
      status: vi.fn(async () => ({ healthy: true })),
      search: vi.fn(async () => ({ results: [] })),
      graph: vi.fn(async () => ({ nodes: [], edges: [], generatedAt: 'now' })),
      list: vi.fn(async () => []),
      remember: vi.fn(async () => ({ action: 'stored' })),
      entityIndex,
    }
    ;(service as unknown as { providers: Map<string, typeof provider> }).providers.set('hindsight', provider)
    await service.updateProviderService('hindsight', { endpoint: 'http://127.0.0.1:18889', apiKey: 'secret' })
    const view = await service.entities()
    expect(view.items[0]).toEqual({ entity: 'Atlas', count: 4 })
    expect(view.complete).toBe(false)
    expect(view.sources).toEqual(expect.arrayContaining([
      expect.objectContaining({ providerId: 'hindsight', mode: 'entities', status: 'ready', itemCount: 1, complete: false, hint: expect.stringContaining('part') }),
    ]))
    expect(provider.list).not.toHaveBeenCalled()
    const page = await service.entityMemories('Atlas')
    expect(page).toMatchObject({ total: 4, complete: false })
    expect(page.items.find(item => item.id === 'h1')).toMatchObject({ memoryProviderId: 'hindsight', importance: 4 })
    expect(page.items.find(item => item.id === 'h1')?.score).toBeUndefined()
    // Without statistics a finished index is reused only briefly, then read again.
    await service.entities()
    expect(entityIndex).toHaveBeenCalledOnce()
    vi.setSystemTime(Date.now() + 11_000)
    await service.entities()
    expect(entityIndex).toHaveBeenCalledTimes(2)
  })

  it('rejects a missing or oversized entity name', async () => {
    const { service } = fixture()
    await expect(service.entityMemories('  ')).rejects.toThrow('entity is required')
    await expect(service.entityRelated('x'.repeat(201))).rejects.toThrow('entity is too long')
  })
})
