import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createRegistry, createService } from './providers.ts'
import { resolveMemorySpacesConfig } from '../src/config.ts'
import { createRunner } from '../src/runner.ts'
import { MemoryProviderAdapterRegistry } from '../src/providers/registry.ts'
import { NORMALIZED_RELEVANCE_SCORE, type MemoryProviderAdapter } from '../src/providers/adapter.ts'
import { RecallQualityPolicyRegistry, STRICT_RECALL_QUALITY_POLICY } from '../src/recall-quality/index.ts'

const directories: string[] = []
afterEach(() => { vi.restoreAllMocks(); for (const path of directories.splice(0)) rmSync(path, { recursive: true, force: true }) })

function fixture(n: number, adapter: MemoryProviderAdapter, quality?: RecallQualityPolicyRegistry) {
  const dataDir = mkdtempSync(join(tmpdir(), 'mnemon-service-algorithm-'))
  directories.push(dataDir)
  const config = resolveMemorySpacesConfig({ dataDir, cliPath: join(dataDir, 'missing-cli'), recallQuality: { policy: quality === undefined ? 'strict-v1' : 'counted-v1' } })
  const runner = createRunner(config, async () => { throw new Error('unexpected external process') })
  const registry = createRegistry(runner, false)
  registry.syncProviderService('holographic', {}, Array.from({ length: n }, (_, i) => ({ externalId: `space-${i}`, name: `Space ${i}`, description: 'Synthetic', connection: {} })))
  return createService(runner, config, registry, quality, new MemoryProviderAdapterRegistry([{ id: adapter.id, create: () => adapter }]))
}

function adapter(): MemoryProviderAdapter {
  return {
    id: 'holographic', scoreSemantics: NORMALIZED_RELEVANCE_SCORE,
    status: async () => ({ healthy: true }), search: async () => ({ results: [] }),
    list: async () => [], graph: async () => ({ nodes: [], edges: [], generatedAt: '' }),
    remember: async (_body, request) => ({ content: request.content, action: 'skipped' }),
  }
}

describe('Memory Spaces algorithm work bounds', () => {
  it('resolves pinned reads once while preserving request order, deduplication and live authority checks', async () => {
    const provider = adapter()
    const search = vi.spyOn(provider, 'search').mockImplementation(async () => ({ results: [{ id: 'same-id', content: 'Evidence', score: 0.9 }] }))
    const service = fixture(10, provider)
    const bodies = service.memorySpaces.active()
    const ids = [bodies[9]!.id, bodies[1]!.id]
    const list = vi.spyOn(service.memorySpaces, 'list')
    try {
      const result = await service.search({ query: 'evidence', mode: 'basic', memoryBodyIds: [` ${ids[0]} `, ids[1]!, ids[0]!] })
      expect(list).toHaveBeenCalledOnce()
      expect(result.results.map(row => row.memoryBodyId)).toEqual(ids)
      expect(search.mock.calls.map(([body]) => body.id)).toEqual(ids)
      service.memorySpaces.update(ids[0]!, { active: false })
      search.mockClear()
      await expect(service.search({ query: 'evidence', memoryBodyIds: ids })).rejects.toThrow('not active')
      expect(search).not.toHaveBeenCalled()
      await expect(service.search({ query: 'evidence', memoryBodyIds: ['missing'] })).rejects.toThrow('unknown memory space')
      await expect(service.search({ query: 'evidence', memoryBodyIds: ['../invalid'] })).rejects.toThrow()
      service.memorySpaces.updateProviderService('holographic', {}, [], false)
      await expect(service.search({ query: 'evidence', memoryBodyIds: [ids[1]!] })).rejects.toThrow('unknown memory space')
    } finally { await service.dispose() }
  })

  it.each([1, 10, 100])('aggregates all quality counters with linear visits at N=%i spaces', async n => {
    let visits = 0
    const policies = new RecallQualityPolicyRegistry()
    policies.register({
      ...STRICT_RECALL_QUALITY_POLICY, id: 'counted-v1',
      evaluate(candidate, context) {
        const id = candidate.memoryBodyId
        Object.defineProperty(candidate, 'memoryBodyId', { get() { visits += 1; return id } })
        return STRICT_RECALL_QUALITY_POLICY.evaluate(candidate, context)
      },
    })
    const provider = adapter()
    provider.search = async () => ({ results: [0.9, 0.4, 0.1, 0, NaN, undefined, 2].map((score, i) => ({ id: `r${i}`, content: `Synthetic ${i}`, ...(score === undefined ? {} : { score }) })) })
    const service = fixture(n, provider, policies)
    try {
      const result = await service.search({ query: 'evidence', mode: 'basic', limit: 10 })
      expect(result.sources).toHaveLength(n)
      for (const source of result.sources) expect(source).toMatchObject({
        status: 'ready', itemCount: 4,
        quality: { policyId: 'counted-v1', fetched: 7, retained: 4,
          selected: result.results.filter(row => row.memoryBodyId === source.memoryBodyId).length,
          droppedLowScore: 1, droppedNonPositiveScore: 1, droppedInvalidScore: 1, unscored: 1, unscaled: 1 },
      })
      expect(visits).toBeLessThanOrEqual(n * 7 + 10)
      expect(result.results.every(row => row.id !== 'r2' && row.id !== 'r3' && row.id !== 'r4')).toBe(true)
    } finally { await service.dispose() }
  })

  it('retains empty counters and failure status for unavailable/unsupported spaces', async () => {
    const provider = adapter()
    provider.search = async () => { throw new Error('Synthetic unavailable') }
    const service = fixture(2, provider)
    const bodies = service.memorySpaces.active()
    vi.spyOn(service.memorySpaces, 'active').mockReturnValue([bodies[0]!, { ...bodies[1]!, provider: { ...bodies[1]!.provider, capabilities: { ...bodies[1]!.provider.capabilities, search: false } } }])
    try {
      const result = await service.search({ query: 'evidence', mode: 'basic' })
      expect(result.results).toEqual([])
      expect(result.sources.map(source => source.status)).toEqual(['unavailable', 'unsupported'])
      for (const source of result.sources) expect(source.quality).toEqual({ policyId: 'strict-v1', fetched: 0, retained: 0, selected: 0, droppedLowScore: 0, droppedNonPositiveScore: 0, droppedInvalidScore: 0, unscored: 0, unscaled: 0 })
    } finally { await service.dispose() }
  })

  it('groups interleaved writes once per destination and restores original receipt order', async () => {
    const provider = adapter()
    const batch = vi.fn<NonNullable<MemoryProviderAdapter['rememberMany']>>(async (_body, requests) => requests.map(request => ({ content: request.content, action: 'skipped' })))
    provider.rememberMany = batch
    const single = vi.spyOn(provider, 'remember')
    const service = fixture(3, provider)
    const bodies = service.memorySpaces.active()
    const requests = Array.from({ length: 100 }, (_, i) => ({ content: `Content ${i}`, memoryBodyId: bodies[i % 3]!.id }))
    const get = vi.spyOn(service.memorySpaces, 'get')
    try {
      const receipts = await service.rememberMany(requests)
      expect(receipts).toEqual(requests.map(request => expect.objectContaining(request)))
      expect(batch).toHaveBeenCalledTimes(3)
      expect(get).toHaveBeenCalledTimes(3)
      expect(batch.mock.calls.map(([body]) => body.id)).toEqual(bodies.map(body => body.id))
      expect(single).not.toHaveBeenCalled()
      batch.mockClear()
      await expect(service.rememberMany([...requests, { content: '', memoryBodyId: bodies[0]!.id }])).rejects.toThrow('content')
      expect(batch).not.toHaveBeenCalled()
      provider.rememberMany = async () => []
      await expect(service.rememberMany(requests)).rejects.toThrow('one receipt per request')
      expect(single).not.toHaveBeenCalled()
      service.memorySpaces.updateProviderService('holographic', {}, [], false)
      await expect(service.rememberMany(requests)).rejects.toThrow('unknown memory space')
    } finally { await service.dispose() }
  })
})
