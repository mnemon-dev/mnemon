import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import type { MemoryProviderConnection, MemoryProviderId } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { HonchoProvider, descriptor } from '../src/index.ts'

const temporaryDirectories: string[] = []
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

async function providerBody(providerId: MemoryProviderId, connection: MemoryProviderConnection) {
  if (providerId !== descriptor.id) throw new Error('Wrong Provider fixture')
  const dataDir = mkdtempSync(join(tmpdir(), 'mnemon-honcho-driver-'))
  temporaryDirectories.push(dataDir)
  const { authority: registry, body } = createMemorySpaceProviderFixture(descriptor, connection, { dataDir, instanceId: 'work-account' })
  return { registry, body }
}

function response(payload: unknown, status = 200): Response {
  return new Response(payload === undefined ? null : JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('standalone honcho data plane', () => {
  it('discovers its native namespaces independently', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => response({ items: [{ id: 'workspace-1', metadata: { title: 'Product workspace', description: 'Honcho source metadata.' } }] }))
    const honcho = await providerBody('honcho', { endpoint: 'https://honcho.example', workspace: 'old', userId: 'user', agentId: 'agent' })
    await expect(new HonchoProvider(honcho.registry, { fetch: fetchMock }).discover({ endpoint: 'https://honcho.example' })).resolves.toEqual([
      expect.objectContaining({ externalId: 'workspace-1', name: 'Product workspace', description: 'Honcho source metadata.', connection: { workspace: 'workspace-1', userId: '*', agentId: '*' } }),
    ])
  })

  it('does not start generic Provider HTTP requests after cancellation', async () => {
    const fetchMock = vi.fn<typeof fetch>()
    const controller = new AbortController()
    controller.abort(new Error('navigation cancelled'))
    const honcho = await providerBody('honcho', { endpoint: 'https://honcho.example', workspace: 'old', userId: 'user', agentId: 'agent' })

    await expect(new HonchoProvider(honcho.registry, { fetch: fetchMock }).discover({ endpoint: 'https://honcho.example' }, controller.signal)).rejects.toThrow('navigation cancelled')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('uses Honcho v3 conclusion scope for recall, explicit writes, and deletion', async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      requests.push({ url: String(url), ...(init === undefined ? {} : { init }) })
      const path = new URL(String(url)).pathname
      if (path.endsWith('/conclusions/query')) return response([{ id: 'hon-1', content: 'Alice prefers short answers.', level: 'peer', observer_id: 'dsh', observed_id: 'alice' }])
      if (path.endsWith('/conclusions/list')) return response({ items: [{ id: 'hon-1', content: 'Alice prefers short answers.', created_at: '2026-08-16T00:00:00Z' }] })
      if (path.endsWith('/conclusions') && init?.method === 'POST') return response({ conclusions: [{ id: 'hon-2', content: 'Alice is testing memory.' }] })
      if (path.endsWith('/conclusions/hon-1') && init?.method === 'DELETE') return response(undefined, 204)
      throw new Error(`unexpected ${init?.method ?? 'GET'} ${path}`)
    })
    const { registry, body } = await providerBody('honcho', {
      endpoint: 'https://api.honcho.dev', apiKey: 'honcho-secret', workspace: 'product team', userId: 'alice', agentId: 'dsh',
    })
    const provider = new HonchoProvider(registry, { fetch: fetchMock })

    await expect(provider.search(body, { query: 'answer style', limit: 7 })).resolves.toEqual({
      results: [expect.objectContaining({ id: 'hon-1', content: 'Alice prefers short answers.', entities: ['dsh', 'alice'] })],
    })
    await expect(provider.list(body, { limit: 20 })).resolves.toEqual([expect.objectContaining({ id: 'hon-1' })])
    await expect(provider.remember(body, { content: 'Alice is testing memory.' })).resolves.toMatchObject({ action: 'stored', provider: 'honcho' })
    await expect(provider.forget(body, 'hon-1')).resolves.toMatchObject({ action: 'deleted', id: 'hon-1' })

    expect(new URL(requests[0]!.url).pathname).toBe('/v3/workspaces/product%20team/conclusions/query')
    expect(new Headers(requests[0]?.init?.headers).get('Authorization')).toBe('Bearer honcho-secret')
    expect(JSON.parse(String(requests[0]?.init?.body))).toEqual({
      query: 'answer style', top_k: 7, filters: { observer_id: 'dsh', observed_id: 'alice' },
    })
    expect(JSON.parse(String(requests[2]?.init?.body))).toEqual({
      conclusions: [{ content: 'Alice is testing memory.', observer_id: 'dsh', observed_id: 'alice', session_id: null }],
    })
  })

  it('supplies stable Honcho peers when a discovered workspace uses wildcard scope', async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      requests.push({ url: String(url), ...(init === undefined ? {} : { init }) })
      return response([])
    })
    const { registry, body } = await providerBody('honcho', {
      endpoint: 'https://api.honcho.dev', workspace: 'dsh-lab', userId: '*', agentId: '*',
    })

    await new HonchoProvider(registry, { fetch: fetchMock }).search(body, { query: 'provider routing', limit: 5 })

    expect(JSON.parse(String(requests[0]?.init?.body))).toEqual({
      query: 'provider routing', top_k: 5, filters: { observer_id: 'dsh', observed_id: 'dsh-user' },
    })
  })
})
