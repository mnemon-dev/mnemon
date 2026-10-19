import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import type { MemoryProviderConnection, MemoryProviderId } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { SupermemoryProvider, descriptor } from '../src/index.ts'

const temporaryDirectories: string[] = []
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

async function providerBody(providerId: MemoryProviderId, connection: MemoryProviderConnection) {
  if (providerId !== descriptor.id) throw new Error('Wrong Provider fixture')
  const dataDir = mkdtempSync(join(tmpdir(), 'mnemon-supermemory-driver-'))
  temporaryDirectories.push(dataDir)
  const { authority: registry, body } = createMemorySpaceProviderFixture(descriptor, connection, { dataDir, instanceId: 'work-account' })
  return { registry, body }
}

function response(payload: unknown, status = 200): Response {
  return new Response(payload === undefined ? null : JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('standalone supermemory data plane', () => {
  it('does not treat a returned document id as completed memory extraction', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => response({ id: 'accepted-document' }))
    const { registry, body } = await providerBody('supermemory', { endpoint: 'https://supermemory.example', apiKey: 'fixture', containerTag: 'notes' })
    await expect(new SupermemoryProvider(registry, { fetch: fetchMock }).remember(body, { content: 'Synthetic input.' }))
      .resolves.toMatchObject({ action: 'queued', id: 'accepted-document' })
  })

  it('discovers its native namespaces independently', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => response([{ id: 'space-1', name: 'Team space', containerTag: 'team', description: 'Supermemory source metadata.' }]))
    const supermemory = await providerBody('supermemory', { endpoint: 'https://supermemory.example', apiKey: 'key', containerTag: 'old', searchMode: 'hybrid' })
    await expect(new SupermemoryProvider(supermemory.registry, { fetch: fetchMock }).discover({ endpoint: 'https://supermemory.example', apiKey: 'key' })).resolves.toEqual([
      expect.objectContaining({ externalId: 'space-1', name: 'Team space', description: 'Supermemory source metadata.' }),
    ])
  })

  it('maps Supermemory v4 recall/list/forget and v3 document ingestion', async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      requests.push({ url: String(url), ...(init === undefined ? {} : { init }) })
      const path = new URL(String(url)).pathname
      if (path === '/v4/search') return response({ results: [{ id: 'sm-1', memory: 'Alice uses dark mode.', similarity: 0.88, metadata: { category: 'preference' } }] })
      if (path === '/v4/memories/list') return response({ memoryEntries: [{ id: 'sm-1', memory: 'Alice uses dark mode.', createdAt: '2026-08-16T00:00:00Z' }] })
      if (path === '/v3/documents/documents') return response({ documents: [] })
      if (path === '/v3/documents') return response({ id: 'doc-1', status: 'queued' })
      if (path === '/v4/memories' && init?.method === 'DELETE') return response({ id: 'sm-1', forgotten: true })
      throw new Error(`unexpected ${init?.method ?? 'GET'} ${path}`)
    })
    const { registry, body } = await providerBody('supermemory', {
      endpoint: 'https://api.supermemory.ai', apiKey: 'sm-secret', containerTag: 'alice', searchMode: 'hybrid',
    })
    const provider = new SupermemoryProvider(registry, { fetch: fetchMock })

    await expect(provider.search(body, { query: 'theme', limit: 6 })).resolves.toEqual({
      results: [expect.objectContaining({ id: 'sm-1', category: 'preference', score: 0.88 })],
    })
    await expect(provider.list(body, { limit: 10 })).resolves.toEqual([expect.objectContaining({ id: 'sm-1' })])
    await expect(provider.remember(body, { content: 'Alice prefers dark mode.', category: 'preference' })).resolves.toMatchObject({ id: 'doc-1', status: 'queued' })
    await expect(provider.forget(body, 'sm-1')).resolves.toMatchObject({ forgotten: true })

    expect(JSON.parse(String(requests[0]?.init?.body))).toMatchObject({ q: 'theme', containerTag: 'alice', searchMode: 'hybrid', limit: 6 })
    expect(new Headers(requests[0]?.init?.headers).get('Authorization')).toBe('Bearer sm-secret')
    expect(new Headers(requests[0]?.init?.headers).get('x-sm-source')).toBe('dsh-mnemon')
    expect(JSON.parse(String(requests.at(-1)?.init?.body))).toEqual({ id: 'sm-1', containerTag: 'alice', reason: 'Deleted from dsh-mnemon' })
  })

  it('merges Supermemory documents with extracted entries and falls back for document deletion', async () => {
    const requests: string[] = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      const path = new URL(String(url)).pathname
      requests.push(`${init?.method ?? 'GET'} ${path}`)
      if (path === '/v4/memories/list') return response({ memoryEntries: [{ id: 'memory-1', memory: 'An extracted memory is browseable.' }] })
      if (path === '/v3/documents/documents') {
        return response({ documents: [{
          id: 'doc-1',
          content: 'A retained document is still browseable.',
          createdAt: '2026-08-16T00:00:00Z',
          metadata: { category: 'context' },
        }] })
      }
      if (path === '/v4/memories' && init?.method === 'DELETE') return response({ message: 'memory not found' }, 404)
      if (path === '/v3/documents/doc-1' && init?.method === 'DELETE') return response({ deleted: true })
      throw new Error(`unexpected ${init?.method ?? 'GET'} ${path}`)
    })
    const { registry, body } = await providerBody('supermemory', {
      endpoint: 'https://api.supermemory.ai', apiKey: 'sm-secret', containerTag: 'alice', searchMode: 'hybrid',
    })
    const provider = new SupermemoryProvider(registry, { fetch: fetchMock })

    await expect(provider.list(body, { limit: 10 })).resolves.toEqual([
      expect.objectContaining({ id: 'memory-1', content: 'An extracted memory is browseable.' }),
      expect.objectContaining({ id: 'doc-1', content: 'A retained document is still browseable.', category: 'context' }),
    ])
    await expect(provider.forget(body, 'doc-1')).resolves.toMatchObject({ action: 'deleted', document: true })
    expect(requests).toEqual([
      'POST /v4/memories/list',
      'POST /v3/documents/documents',
      'DELETE /v4/memories',
      'DELETE /v3/documents/doc-1',
    ])
  })
})
