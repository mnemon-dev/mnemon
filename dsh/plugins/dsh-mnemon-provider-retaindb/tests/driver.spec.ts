import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import type { MemoryProviderConnection, MemoryProviderId } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { RetainDbProvider, descriptor } from '../src/index.ts'

const temporaryDirectories: string[] = []
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

async function providerBody(providerId: MemoryProviderId, connection: MemoryProviderConnection) {
  if (providerId !== descriptor.id) throw new Error('Wrong Provider fixture')
  const dataDir = mkdtempSync(join(tmpdir(), 'mnemon-retaindb-driver-'))
  temporaryDirectories.push(dataDir)
  const { authority: registry, body } = createMemorySpaceProviderFixture(descriptor, connection, { dataDir, instanceId: 'work-account' })
  return { registry, body }
}

function response(payload: unknown, status = 200): Response {
  return new Response(payload === undefined ? null : JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('standalone retaindb data plane', () => {
  it('discovers its native namespaces independently', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => response({ projects: [{ id: 'project-1', name: 'Launch', slug: 'launch', description: 'RetainDB source metadata.' }] }))
    const retain = await providerBody('retaindb', { endpoint: 'https://retain.example', apiKey: 'key', project: 'old', userId: 'user' })
    await expect(new RetainDbProvider(retain.registry, { fetch: fetchMock }).discover({ endpoint: 'https://retain.example', apiKey: 'key' })).resolves.toEqual([
      expect.objectContaining({ externalId: 'project-1', name: 'Launch', description: 'RetainDB source metadata.', connection: { project: 'launch', userId: '*' } }),
    ])
  })

  it('preserves RetainDB project/user/session scope and current-to-legacy fallbacks', async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      requests.push({ url: String(url), ...(init === undefined ? {} : { init }) })
      const path = new URL(String(url)).pathname
      if (path === '/v1/memory/search') return response({ memories: [{ id: 'ret-1', content: 'Use staged rollout.', score: 0.8, memory_type: 'decision' }] })
      if (path === '/v1/memory') return response({ message: 'missing' }, 404)
      if (path === '/v1/memories' && init?.method === 'POST') return response({ id: 'ret-2' })
      if (path === '/v1/memory/ret-1') return response({ message: 'missing' }, 404)
      if (path === '/v1/memories/ret-1') return response({ deleted: true })
      throw new Error(`unexpected ${init?.method ?? 'GET'} ${path}`)
    })
    const { registry, body } = await providerBody('retaindb', {
      endpoint: 'https://api.retaindb.com', apiKey: 'retain-secret', project: 'launch', userId: 'alice',
    })
    const provider = new RetainDbProvider(registry, { fetch: fetchMock })

    await expect(provider.search(body, { query: 'rollout', limit: 4 })).resolves.toEqual({
      results: [expect.objectContaining({ id: 'ret-1', category: 'decision', score: 0.8 })],
    })
    await expect(provider.remember(body, { content: 'Canary before production.', category: 'decision' })).resolves.toMatchObject({ id: 'ret-2' })
    await expect(provider.forget(body, 'ret-1')).resolves.toMatchObject({ action: 'deleted' })

    expect(JSON.parse(String(requests[0]?.init?.body))).toMatchObject({ project: 'launch', user_id: 'alice', top_k: 4 })
    expect(new Headers(requests[0]?.init?.headers).get('Authorization')).toBe('Bearer retain-secret')
    expect(new Headers(requests[0]?.init?.headers).get('X-API-Key')).toBe('retain-secret')
    expect(requests.map(request => new URL(request.url).pathname)).toContain('/v1/memories/ret-1')
  })
})
