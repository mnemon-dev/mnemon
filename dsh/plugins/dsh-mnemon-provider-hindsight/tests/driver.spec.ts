import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import type { MemoryProviderConnection, MemoryProviderId } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { HindsightProvider, descriptor } from '../src/index.ts'

const temporaryDirectories: string[] = []
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

async function providerBody(providerId: MemoryProviderId, connection: MemoryProviderConnection) {
  if (providerId !== descriptor.id) throw new Error('Wrong Provider fixture')
  const dataDir = mkdtempSync(join(tmpdir(), 'mnemon-hindsight-driver-'))
  temporaryDirectories.push(dataDir)
  const { authority: registry, body } = createMemorySpaceProviderFixture(descriptor, connection, { dataDir, instanceId: 'work-account' })
  return { registry, body }
}

function response(payload: unknown, status = 200): Response {
  return new Response(payload === undefined ? null : JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('standalone hindsight data plane', () => {
  it('discovers its native namespaces independently', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => response({ banks: [{ bank_id: 'bank-1', name: 'Product bank', mission: 'Hindsight source metadata.' }] }))
    const hindsight = await providerBody('hindsight', { endpoint: 'https://hindsight.example', bankId: 'old', budget: 'mid' })
    await expect(new HindsightProvider(hindsight.registry, { fetch: fetchMock }).discover({ endpoint: 'https://hindsight.example' })).resolves.toEqual([
      expect.objectContaining({ externalId: 'bank-1', name: 'Product bank', description: 'Hindsight source metadata.' }),
    ])
  })

  it('maps Hindsight recall, graph traversal, asynchronous retain, and soft forget', async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      requests.push({ url: String(url), ...(init === undefined ? {} : { init }) })
      const path = new URL(String(url)).pathname
      if (path === '/health/live') return response({ status: 'alive', version: '0.9.1' })
      if (path.endsWith('/stats')) return response({ total_nodes: 3, total_links: 2, nodes_by_fact_type: { world: 2, observation: 1 }, operations_by_status: { completed: 4 } })
      if (path.endsWith('/entities')) return response({ items: [{ canonical_name: 'Alice', mention_count: 3 }], total: 1 })
      if (path.endsWith('/memories/recall')) return response({ results: [{ id: 'hs-1', text: 'Alice uses TypeScript.', type: 'world', entities: ['Alice'], scores: { final: 0.93 } }] })
      if (path.endsWith('/memories/list')) return response({ items: [{ id: 'hs-1', text: 'Alice uses TypeScript.', type: 'world' }], total: 1 })
      if (path.endsWith('/graph')) return response({
        nodes: [
          { data: { id: 'hs-1', text: 'Alice', entities: 'Alice' } },
          { data: { id: 'hs-2', text: 'TypeScript', color: '#42a5f5' } },
          { data: { id: 'hs-3', text: 'Node.js' } },
        ],
        edges: [
          { data: { source: 'hs-1', target: 'hs-2', linkType: 'entity' } },
          { data: { source: 'hs-2', target: 'hs-3', linkType: 'semantic' } },
        ],
      })
      if (path.endsWith('/memories') && init?.method === 'POST') return response({ operation_id: 'op-1', items_count: 1 })
      if (path.endsWith('/memories/hs-1') && init?.method === 'PATCH') return response({ state: 'invalidated' })
      throw new Error(`unexpected ${init?.method ?? 'GET'} ${path}`)
    })
    const { registry, body } = await providerBody('hindsight', {
      endpoint: 'https://api.hindsight.vectorize.io', apiKey: 'hs-secret', bankId: 'alice/profile', budget: 'high',
    })
    const provider = new HindsightProvider(registry, { fetch: fetchMock })

    await expect(provider.search(body, { query: 'language', limit: 3 })).resolves.toEqual({
      results: [expect.objectContaining({ id: 'hs-1', category: 'world', score: 0.93, entities: ['Alice'] })],
    })
    await expect(provider.list(body, { limit: 25 })).resolves.toEqual([expect.objectContaining({ id: 'hs-1' })])
    await expect(provider.related(body, 'hs-1', 2)).resolves.toEqual([
      expect.objectContaining({ id: 'hs-2' }), expect.objectContaining({ id: 'hs-3' }),
    ])
    await expect(provider.status(body)).resolves.toEqual({
      healthy: true,
      stats: expect.objectContaining({ totalInsights: 3, edgeCount: 2, oplogCount: 4, byCategory: { world: 2, observation: 1 }, topEntities: [{ entity: 'Alice', count: 3 }] }),
    })
    await expect(provider.remember(body, { content: 'Alice ships TypeScript.', category: 'decision', tags: ['dsh'], entities: ['Alice'] })).resolves.toMatchObject({ action: 'queued', operationId: 'op-1', itemsCount: 1 })
    await expect(provider.forget(body, 'hs-1')).resolves.toMatchObject({ action: 'invalidated', id: 'hs-1' })

    expect(new URL(requests[0]!.url).pathname).toBe('/v1/default/banks/alice%2Fprofile/memories/recall')
    expect(new Headers(requests[0]?.init?.headers).get('Authorization')).toBe('Bearer hs-secret')
    expect(JSON.parse(String(requests[0]?.init?.body))).toMatchObject({ query: 'language', budget: 'high', types: ['world', 'experience', 'observation'] })
    expect(JSON.parse(String(requests[6]?.init?.body))).toMatchObject({
      items: [{ content: 'Alice ships TypeScript.', context: 'decision', tags: ['dsh'], entities: [{ text: 'Alice' }] }],
      async: true,
    })
    expect(JSON.parse(String(requests[7]?.init?.body))).toEqual({ state: 'invalidated', reason: 'Forgotten from dsh-mnemon' })
  })
})
