import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import { OpenVikingProvider, descriptor } from '../src/index.ts'

const temporaryDirectories: string[] = []
afterEach(() => { for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true }) })

async function bodyAndRegistry(fetchMock: typeof fetch) {
  const dataDir = mkdtempSync(join(tmpdir(), 'mnemon-openviking-driver-'))
  temporaryDirectories.push(dataDir)
  const { authority, body } = createMemorySpaceProviderFixture(descriptor, {
    endpoint: 'https://memory.example.com', targetUri: 'viking://user/team/memories', apiKey: 'private-key',
    account: 'acme', user: 'grivn', actorPeerId: 'dsh-workbench',
  }, { dataDir, instanceId: 'work-account' })
  return { body, provider: new OpenVikingProvider(authority, { fetch: fetchMock, requestTimeoutMs: 1_000 }) }
}

function ok(result: unknown): Response { return new Response(JSON.stringify({ status: 'ok', result }), { status: 200, headers: { 'Content-Type': 'application/json' } }) }

describe('standalone OpenViking data plane', () => {
  it('does not start discovery after cancellation', async () => {
    const fetchMock = vi.fn<typeof fetch>()
    const controller = new AbortController()
    controller.abort(new Error('workspace changed'))
    const { provider } = await bodyAndRegistry(fetchMock)

    await expect(provider.discover({ endpoint: 'https://memory.example.com', apiKey: 'private-key', account: 'acme' }, controller.signal)).rejects.toThrow('workspace changed')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('discovers every registered user namespace in the configured account', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (url) => {
      expect(new URL(String(url)).pathname).toBe('/api/v1/admin/accounts/acme/users')
      return ok([
        { user_id: 'alice', display_name: 'Alice', description: 'Product lead memory.' },
        { user_id: 'bob', name: 'Bob', role: 'Engineer' },
        { user_id: '../foreign', display_name: 'Unsafe namespace' },
        { user_id: '%2e%2e', display_name: 'Encoded unsafe namespace' },
        { user_id: 'a@b@c', display_name: 'Invalid identity' },
      ])
    })
    const { provider } = await bodyAndRegistry(fetchMock)

    await expect(provider.discover({ endpoint: 'https://memory.example.com', apiKey: 'private-key', account: 'acme' })).resolves.toEqual([
      { externalId: 'acme:alice', name: 'Alice', description: 'Product lead memory.', connection: { targetUri: 'viking://user/alice/memories', user: 'alice', actorPeerId: 'dsh' } },
      { externalId: 'acme:bob', name: 'Bob', description: 'Engineer', connection: { targetUri: 'viking://user/bob/memories', user: 'bob', actorPeerId: 'dsh' } },
    ])
  })

  it('scopes semantic retrieval to the configured memory URI and keeps credentials in host headers', async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      requests.push({ url: String(url), ...(init === undefined ? {} : { init }) })
      if (new URL(String(url)).pathname === '/api/v1/content/read') return ok('用户偏好简洁中文回答。')
      return ok({
        memories: [{
          uri: 'viking://user/team/memories/preferences/style.md',
          abstract: '用户偏好简洁中文回答。',
          score: 0.86,
        }],
      })
    })
    const { body, provider } = await bodyAndRegistry(fetchMock)

    const result = await provider.search(body, { query: '回答风格', limit: 8 })

    expect(result.results).toEqual([expect.objectContaining({
      id: 'viking://user/team/memories/preferences/style.md',
      externalUri: 'viking://user/team/memories/preferences/style.md',
      content: '用户偏好简洁中文回答。',
      category: 'preferences',
      source: 'external',
      score: 0.86,
    })])
    expect(requests[0]?.url).toBe('https://memory.example.com/api/v1/search/find')
    expect(JSON.parse(String(requests[0]?.init?.body))).toMatchObject({
      query: '回答风格',
      target_uri: 'viking://user/team/memories',
      context_type: ['memory'],
      limit: 8,
    })
    expect(new Headers(requests[0]?.init?.headers).get('Authorization')).toBe('Bearer private-key')
    expect(new Headers(requests[0]?.init?.headers).get('X-OpenViking-Account')).toBe('acme')
    expect(new Headers(requests[0]?.init?.headers).get('X-OpenViking-Actor-Peer')).toBe('dsh-workbench')
  })

  it('browses remote memory markdown without exposing OpenViking system files', async () => {
    const fetchMock = vi.fn<typeof fetch>(async url => new URL(String(url)).pathname === '/api/v1/content/read' ? ok('偏好简洁回答。') : ok([
      { uri: 'viking://user/team/memories/preferences/style.md', isDir: false, abstract: '偏好简洁回答。', modTime: '2026-08-16T00:00:00Z' },
      { uri: 'viking://user/team/memories/preferences', isDir: true },
      { uri: 'viking://user/team/memories/preferences/.abstract.md', isDir: false, abstract: 'system summary' },
      { uri: 'viking://user/team/memories/.meta.json', isDir: false },
    ]))
    const { body, provider } = await bodyAndRegistry(fetchMock)

    await expect(provider.list(body, { limit: 10 })).resolves.toEqual([
      expect.objectContaining({ content: '偏好简洁回答。', category: 'preferences' }),
    ])
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('recursive=true')
  })

  it('writes the memory through the content API and returns an exact write receipt', async () => {
    const requests: Array<{ path: string; body: Record<string, unknown> }> = []
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      const path = new URL(String(url)).pathname
      if (path === '/api/v1/content/read') return ok('发布必须先通过灰度验证。')
      const input = JSON.parse(String(init?.body)) as Record<string, unknown>
      requests.push({ path, body: input })
      if (path === '/api/v1/content/write') {
        return ok({
          uri: input.uri,
          root_uri: 'viking://user/team/memories/experiences',
          context_type: 'memory',
          mode: 'create',
          written_bytes: Buffer.byteLength(String(input.content)),
          content_updated: true,
          semantic_status: 'skipped',
          vector_status: 'complete',
        })
      }
      throw new Error(`unexpected path ${path}`)
    })
    const { body, provider } = await bodyAndRegistry(fetchMock)

    await expect(provider.remember(body, { content: '发布必须先通过灰度验证。', category: 'decision', importance: 4 })).resolves.toMatchObject({
      action: 'stored',
      provider: 'openviking',
      vectorStatus: 'complete',
      writtenBytes: Buffer.byteLength('发布必须先通过灰度验证。'),
    })

    expect(requests).toHaveLength(1)
    expect(requests[0]?.path).toBe('/api/v1/content/write')
    expect(requests[0]?.body).toMatchObject({
      mode: 'create',
      wait: true,
      content: '发布必须先通过灰度验证。',
      tags: ['source=mnemon', 'category=decision', 'importance=4'],
    })
    expect(String(requests[0]?.body.uri)).toMatch(/^viking:\/\/user\/team\/memories\/experiences\/.*\.md$/u)
  })

  it('routes each memory category into its OpenViking memory folder', async () => {
    const uris: string[] = []
    const files = new Map<string, string>()
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      const parsed = new URL(String(url))
      if (parsed.pathname === '/api/v1/content/read') return ok(files.get(parsed.searchParams.get('uri')!))
      const request = JSON.parse(String(init?.body)) as { uri: string; content: string }
      uris.push(request.uri)
      files.set(request.uri, request.content)
      return ok({ uri: request.uri, mode: 'create', context_type: 'memory', content_updated: true,
        written_bytes: Buffer.byteLength(request.content), semantic_status: 'skipped', vector_status: 'complete' })
    })
    const { body, provider } = await bodyAndRegistry(fetchMock)

    await provider.remember(body, { content: '偏好简洁回答。', category: 'preference' })
    await provider.remember(body, { content: '发布必须先通过灰度验证。', category: 'decision' })
    await provider.remember(body, { content: 'ServerKit 使用 Go 后端。', category: 'fact' })
    await provider.remember(body, { content: 'Context', category: 'context' })
    await provider.remember(body, { content: 'Insight', category: 'insight' })
    await provider.remember(body, { content: 'General', category: 'general' })

    expect(uris.map(uri => uri.split('/').at(-2))).toEqual(['preferences', 'experiences', 'entities', 'events', 'experiences', 'entities'])
  })

  it('forgets only an exact non-generated memory file inside the configured root', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => ok({ uri: 'viking://user/team/memories/preferences/style.md', estimated_deleted_count: 1 }))
    const { body, provider } = await bodyAndRegistry(fetchMock)

    await expect(provider.forget(body, 'viking://user/team/memories/preferences/style.md')).resolves.toMatchObject({
      action: 'deleted',
      provider: 'openviking',
      estimatedDeletedCount: 1,
    })
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/api/v1/fs?')
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe('DELETE')
    await expect(provider.forget(body, 'viking://user/team/memories/.overview.md')).rejects.toThrow(/exact non-generated/u)
    await expect(provider.forget(body, 'viking://user/other/memories/fact.md')).rejects.toThrow(/inside this Memory Space/u)
  })
})
