import { mkdtempSync, rmSync, statSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import { HolographicProvider, descriptor } from '../src/index.ts'

const temporaryDirectories: string[] = []
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

describe('standalone holographic data plane', () => {
  it('preserves exact Jaccard scores for unequal token sets and Unicode', async () => {
    const dataDir = mkdtempSync(join(tmpdir(), 'dsh-mnemon-holographic-score-'))
    temporaryDirectories.push(dataDir)
    const { authority, body } = createMemorySpaceProviderFixture(descriptor, { defaultTrust: 1, minTrust: 0 }, { dataDir })
    const provider = new HolographicProvider(authority)
    await provider.remember(body, { content: 'alpha gamma delta' })
    await provider.remember(body, { content: '存储 查询' })
    expect((await provider.search(body, { query: 'alpha beta' })).results.map(row => row.score)).toEqual([1 / 4])
    expect((await provider.search(body, { query: 'alpha beta gamma delta epsilon' })).results.map(row => row.score)).toEqual([3 / 5])
    expect((await provider.search(body, { query: '存储 beta' })).results.map(row => row.score)).toEqual([3 / 7])
    expect((await provider.search(body, { query: 'unknown terms' })).results).toEqual([])
    expect((await provider.search(body, { query: '' })).results.map(row => row.score)).toEqual([0.9, 0.9])
  })

  it('stores Holographic facts locally with trust, entities, graph projection, related recall, and hard forget', async () => {
    const dataDir = mkdtempSync(join(tmpdir(), 'dsh-mnemon-holographic-'))
    temporaryDirectories.push(dataDir)
    const dataPath = join(dataDir, 'facts', 'memory.json')

    const { authority: registry, body } = createMemorySpaceProviderFixture(descriptor, { dataPath, defaultTrust: 0.7, minTrust: 0.3 }, { dataDir, instanceId: 'work-account' })
    const provider = new HolographicProvider(registry)

    const first = await provider.remember(body, {
      content: 'TypeScript service uses SQLite for durable state.',
      category: 'decision',
      tags: ['architecture'],
      entities: ['TypeScript', 'SQLite'],
    }) as { id: string }
    const second = await provider.remember(body, {
      content: 'SQLite backups run daily before deployment.',
      category: 'fact',
      entities: ['SQLite'],
    }) as { id: string }

    await expect(provider.remember(body, { content: 'TypeScript service uses SQLite for durable state.' })).resolves.toMatchObject({ action: 'skipped', id: first.id })
    await expect(provider.search(body, { query: 'TypeScript storage', limit: 5 })).resolves.toEqual({
      results: [expect.objectContaining({ id: first.id, category: 'decision', entities: ['TypeScript', 'SQLite'] })],
    })
    await expect(provider.related(body, first.id, 2)).resolves.toEqual([expect.objectContaining({ id: second.id })])
    await expect(provider.graph(body)).resolves.toMatchObject({
      nodes: expect.arrayContaining([expect.objectContaining({ id: 'entity:SQLite', kind: 'entity' })]),
      edges: expect.arrayContaining([expect.objectContaining({ sourceId: first.id, targetId: 'entity:SQLite', type: 'entity' })]),
    })
    await expect(provider.status(body)).resolves.toMatchObject({ healthy: true, stats: { totalInsights: 2, edgeCount: 3 } })
    expect(statSync(dataPath).mode & 0o777).toBe(0o600)
    await expect(provider.forget(body, first.id)).resolves.toMatchObject({ action: 'deleted' })
    await expect(provider.list(body, {})).resolves.toHaveLength(1)
  })
})
