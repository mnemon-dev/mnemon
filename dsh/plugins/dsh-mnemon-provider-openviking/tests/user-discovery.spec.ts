import { randomUUID } from 'node:crypto'
import { describe, expect, it, vi } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import { OpenVikingProvider, descriptor } from '../src/index.ts'

const service = { endpoint: 'https://memory.example/openviking', account: 'work', discoveryUser: 'alice', apiKey: randomUUID() }
const root = 'viking://user/alice/memories'
const ok = (result: unknown) => new Response(JSON.stringify({ status: 'ok', result }), { status: 200 })
const denied = () => new Response(JSON.stringify({ status: 'error', error: { message: 'API is currently unavailable due to access restrictions' } }), { status: 403 })
function provider(fetchMock: typeof fetch) {
  return new OpenVikingProvider({ runner: { effectiveDataDir: () => '/unused' }, list: () => [], providerConnection: () => ({}) }, { fetch: fetchMock, requestTimeoutMs: 30 })
}

describe('OpenViking explicit user-key discovery', () => {
  it('uses the same safe owner pattern in Host and modern HTML form validation', () => {
    const field = descriptor.fields.find(item => item.key === 'discoveryUser')!
    for (const flags of ['u', 'v']) {
      const pattern = new RegExp(field.pattern!, flags)
      expect(pattern.test('alice-team@example.com')).toBe(true)
      for (const user of ['..', '../bob', 'a@b@c']) expect(pattern.test(user)).toBe(false)
    }
  })

  it('discovers the explicit owner through the data plane when every admin route is denied', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input))
      if (url.pathname.includes('/admin/')) return denied()
      expect(url.pathname).toBe('/openviking/api/v1/fs/ls')
      expect(url.searchParams.get('uri')).toBe(root)
      expect(url.searchParams.get('recursive')).toBe('false')
      expect(url.searchParams.get('output')).toBe('original')
      const headers = new Headers(init?.headers)
      expect(headers.get('Authorization')).toBe(`Bearer ${service.apiKey}`)
      expect(headers.has('X-OpenViking-Account')).toBe(false)
      expect(headers.has('X-OpenViking-User')).toBe(false)
      return ok([])
    })
    await expect(provider(fetchMock).discover(service)).resolves.toEqual([{
      externalId: 'work:alice', name: 'alice', description: 'OpenViking memory namespace for alice',
      connection: { targetUri: root, user: 'alice', actorPeerId: 'dsh' },
    }])
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it.each([
    { account: '' }, { account: 'work:foreign' }, { discoveryUser: '../foreign' },
    { discoveryUser: 'a@b@c' }, { discoveryUser: '.' }, { discoveryUser: '%2e%2e' }, { apiKey: '' },
  ])('rejects incomplete or unsafe explicit configuration before I/O: %j', async change => {
    const fetchMock = vi.fn<typeof fetch>()
    await expect(provider(fetchMock).discover({ ...service, ...change })).rejects.toThrow(/OpenViking/)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it.each([{}, null, 'healthy', [{ uri: 'viking://user/other/memories/private.md' }], [{ name: 'missing-uri' }]])('rejects an invalid data-plane response: %j', async result => {
    const fetchMock = vi.fn<typeof fetch>(async () => ok(result))
    await expect(provider(fetchMock).discover(service)).rejects.toThrow('OpenViking could not validate the selected memory namespace')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps a denied namespace denied without an admin or health fallback', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => denied())
    await expect(provider(fetchMock).discover(service)).rejects.toThrow('access restrictions')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/fs/ls?')
  })

  it('honors cancellation and timeout on the explicit probe', async () => {
    const fetchMock = vi.fn<typeof fetch>(async (_url, init) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
    }))
    const controller = new AbortController()
    controller.abort(new Error('scope changed'))
    await expect(provider(fetchMock).discover(service, controller.signal)).rejects.toThrow('scope changed')
    expect(fetchMock).not.toHaveBeenCalled()
    await expect(provider(fetchMock).discover(service)).rejects.toThrow('timed out')
  })

  it('retains key-bound headers for browse, search, status, write, readback and exact deletion', async () => {
    let written = ''
    const content = 'Synthetic user-key canary.'
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = new URL(String(input))
      const headers = new Headers(init?.headers)
      expect(headers.get('Authorization')).toBe(`Bearer ${service.apiKey}`)
      expect(headers.has('X-OpenViking-Account')).toBe(false)
      expect(headers.has('X-OpenViking-User')).toBe(false)
      expect(url.pathname).not.toMatch(/admin|system|health/)
      if (url.pathname.endsWith('/content/write')) {
        const request = JSON.parse(String(init?.body))
        written = request.uri
        return ok({ uri: written, content_updated: true, written_bytes: Buffer.byteLength(content), mode: 'create', context_type: 'memory', vector_status: 'complete', semantic_status: 'skipped' })
      }
      if (url.pathname.endsWith('/content/read')) return ok(content)
      if (url.pathname.endsWith('/search/find')) return ok({ memories: [{ uri: written, score: 1 }] })
      if (init?.method === 'DELETE') return ok({ uri: written })
      return ok(written ? [{ uri: written, isDir: false }] : [])
    })
    const { authority, body } = createMemorySpaceProviderFixture(descriptor, { ...service, user: 'alice', targetUri: root }, { dataDir: '/unused', instanceId: 'cloud' })
    const adapter = new OpenVikingProvider(authority, { fetch: fetchMock })
    expect(await adapter.status(body)).toEqual({ healthy: true })
    expect(await adapter.remember(body, { content })).toMatchObject({ action: 'stored', id: written })
    expect((await adapter.list(body, {}))[0]?.content).toBe(content)
    expect((await adapter.search(body, { query: 'canary', limit: 5 })).results[0]?.content).toBe(content)
    expect(await adapter.forget(body, written)).toMatchObject({ action: 'deleted', id: written })
  })

  it.each([
    { targetUri: 'viking://user/bob/memories', user: 'alice' },
    { targetUri: root, user: 'bob' },
  ])('rejects a memory owner different from the explicit service scope: %j', async memory => {
    const fetchMock = vi.fn<typeof fetch>()
    const { authority, body } = createMemorySpaceProviderFixture(descriptor, { ...service, ...memory }, { dataDir: '/unused', instanceId: 'cloud' })
    const adapter = new OpenVikingProvider(authority, { fetch: fetchMock })
    await expect(adapter.search(body, { query: 'canary', limit: 5 })).rejects.toThrow('configured discovery user')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('retains account enumeration and user enumeration when the explicit user is empty', async () => {
    const fetchMock = vi.fn<typeof fetch>(async input => String(input).endsWith('/admin/accounts') ? ok([{ account_id: 'work' }]) : ok([{ user_id: 'alice' }, { user_id: 'bob' }]))
    expect(await provider(fetchMock).discover({ endpoint: service.endpoint, discoveryUser: '' })).toHaveLength(2)
    expect(fetchMock.mock.calls.map(([input]) => new URL(String(input)).pathname)).toEqual([
      '/openviking/api/v1/admin/accounts', '/openviking/api/v1/admin/accounts/work/users',
    ])
  })
})
