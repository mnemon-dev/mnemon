import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import type { MemoryProviderConnection, MemoryProviderId } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { Mem0Provider, descriptor } from '../src/index.ts'

const temporaryDirectories: string[] = []
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

async function providerBody(providerId: MemoryProviderId, connection: MemoryProviderConnection) {
  if (providerId !== descriptor.id) throw new Error('Wrong Provider fixture')
  const dataDir = mkdtempSync(join(tmpdir(), 'mnemon-mem0-driver-'))
  temporaryDirectories.push(dataDir)
  const { authority: registry, body } = createMemorySpaceProviderFixture(descriptor, connection, { dataDir, instanceId: 'work-account' })
  return { registry, body }
}

function response(payload: unknown, status = 200): Response {
  return new Response(payload === undefined ? null : JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('standalone mem0 data plane', () => {
  it.each(['platform', 'self-hosted'])('distinguishes asynchronous ingestion from explicit storage in %s mode without an upstream status', async mode => {
    const fetchMock = vi.fn<typeof fetch>(async () => response({ event_id: 'event-or-record-id' }))
    const { registry, body } = await providerBody('mem0', { endpoint: 'https://mem0.example', apiKey: 'fixture', mode, userId: 'user', agentId: 'agent' })
    const result = await new Mem0Provider(registry, { fetch: fetchMock }).remember(body, { content: 'Synthetic memory.' })
    expect(result).toMatchObject({ action: mode === 'platform' ? 'queued' : 'stored' })
    if (mode === 'self-hosted') expect(JSON.parse(String(fetchMock.mock.calls[0]![1]?.body))).toMatchObject({ infer: false })
  })

  it('discovers its native namespaces independently', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => response([{ id: 'alice', name: 'Alice', type: 'user', total_memories: 4 }]))
    const mem0 = await providerBody('mem0', { endpoint: 'https://mem0.example', mode: 'self-hosted', userId: 'user', agentId: 'agent' })
    await expect(new Mem0Provider(mem0.registry, { fetch: fetchMock }).discover({ endpoint: 'https://mem0.example', mode: 'self-hosted' })).resolves.toEqual([
      expect.objectContaining({ externalId: 'user:alice', name: 'Alice', connection: { userId: 'alice', agentId: '*', rerank: false } }),
    ])
  })

  it('uses Mem0 Platform v3 scoping and keeps the token out of result projections', async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      requests.push({ url: String(url), ...(init === undefined ? {} : { init }) })
      const path = new URL(String(url)).pathname
      if (path === '/v3/memories/search/') return response({ results: [{ id: 'mem-1', memory: 'Alice prefers concise replies.', score: 0.91, categories: ['preference'] }] })
      if (path === '/v3/memories/add/') return response({ status: 'PENDING', event_id: 'event-1' })
      if (path === '/v1/memories/mem-1') return response({ message: 'deleted' })
      throw new Error(`unexpected path ${path}`)
    })
    const { registry, body } = await providerBody('mem0', {
      endpoint: 'https://api.mem0.ai', apiKey: 'mem0-secret', mode: 'platform', userId: 'alice', agentId: 'dsh', rerank: true,
    })
    const provider = new Mem0Provider(registry, { fetch: fetchMock })

    await expect(provider.search(body, { query: 'reply style', limit: 5 })).resolves.toEqual({
      results: [expect.objectContaining({ id: 'mem-1', content: 'Alice prefers concise replies.', category: 'preference', score: 0.91 })],
    })
    await expect(provider.remember(body, { content: 'Alice likes TypeScript.', category: 'preference' })).resolves.toMatchObject({ action: 'queued', eventId: 'event-1', status: 'PENDING' })
    await expect(provider.forget(body, 'mem-1')).resolves.toMatchObject({ action: 'deleted', id: 'mem-1' })

    expect(new Headers(requests[0]?.init?.headers).get('Authorization')).toBe('Token mem0-secret')
    expect(JSON.parse(String(requests[0]?.init?.body))).toMatchObject({ filters: { user_id: 'alice', agent_id: 'dsh' }, rerank: true })
    expect(JSON.stringify((await provider.search(body, { query: 'reply style' })).results)).not.toContain('mem0-secret')
  })
})
