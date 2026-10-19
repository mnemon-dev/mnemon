import { describe, expect, it, vi } from 'vitest'
import { createMemorySpaceProviderFixture } from 'dsh-mnemon-source-memory-spaces/testing'
import { OpenVikingProvider, descriptor } from '../src/index.ts'

function ok(result: unknown): Response {
  return new Response(JSON.stringify({ status: 'ok', result }), { status: 200 })
}

function writeFixture(options: {
  response?: Record<string, unknown>; readback?: string; targetUri?: string; user?: string;
  requestTimeoutMs?: number; fetch?: typeof fetch;
} = {}) {
  const files = new Map<string, string>()
  const fetchMock = vi.fn<typeof fetch>(options.fetch ?? (async (url, init) => {
    const path = new URL(String(url))
    if (path.pathname === '/api/v1/system/status') return ok({ initialized: true, user: 'resolved-owner' })
    if (path.pathname === '/api/v1/content/read') return ok(options.readback ?? files.get(path.searchParams.get('uri')!))
    const input = JSON.parse(String(init?.body)) as { uri: string; content: string }
    files.set(input.uri, input.content)
    return ok({ uri: input.uri, mode: 'create', context_type: 'memory', content_updated: true,
      written_bytes: Buffer.byteLength(input.content), semantic_status: 'skipped', vector_status: 'complete',
      ...options.response })
  }))
  const { authority, body } = createMemorySpaceProviderFixture(descriptor, {
    endpoint: 'https://memory.example', targetUri: options.targetUri ?? 'viking://user/team/memories',
    user: options.user ?? '', account: 'work', apiKey: 'synthetic-only-key', actorPeerId: 'synthetic-actor',
  }, { dataDir: '/unused', instanceId: 'work-account' })
  return { body, authority, files, fetchMock, provider: new OpenVikingProvider(authority, { fetch: fetchMock, requestTimeoutMs: options.requestTimeoutMs ?? 1_000 }) }
}

describe('OpenViking issue 233 durable writes', () => {
  it('stores the requested text even when one-turn extraction makes no durable change', async () => {
    const files = new Map<string, string>()
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      const parsed = new URL(String(url))
      const request = JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
      if (parsed.pathname === '/api/v1/content/write') {
        files.set(String(request.uri), String(request.content))
        return ok({ uri: request.uri, mode: 'create', context_type: 'memory', content_updated: true, written_bytes: Buffer.byteLength(String(request.content)), semantic_status: 'skipped', vector_status: 'complete' })
      }
      if (parsed.pathname === '/api/v1/content/read') return ok(files.get(parsed.searchParams.get('uri') ?? ''))
      // Issue 233: successful extraction can report a cosmetic update without
      // ever persisting the caller's candidate. Counters are not write proof.
      if (parsed.pathname === '/api/v1/sessions') return ok({ session_id: request.session_id })
      if (parsed.pathname.endsWith('/messages')) return ok({ message_id: 'message-1' })
      if (parsed.pathname.endsWith('/commit')) return ok({ task_id: 'task-1' })
      if (parsed.pathname === '/api/v1/tasks/task-1') return ok({ status: 'completed', result: { memories_extracted: { memory_update: 1 } } })
      throw new Error(`Unexpected request: ${parsed.pathname}`)
    })
    const { authority, body } = createMemorySpaceProviderFixture(descriptor, {
      endpoint: 'https://memory.example', targetUri: 'viking://user/team/memories', user: 'team',
    }, { dataDir: '/unused', instanceId: 'work-account' })
    const provider = new OpenVikingProvider(authority, { fetch: fetchMock })
    const content = 'Release approvals require a completed canary.\n保留原文和换行。'
    const receipt = await provider.remember(body, { content, category: 'decision' })

    expect(receipt).toMatchObject({ action: 'stored' })
    expect([...files.values()]).toEqual([content])
    expect(receipt).toMatchObject({ id: [...files.keys()][0], uri: [...files.keys()][0] })
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes('/sessions'))).toBe(false)
  })

  it.each([
    ['pending vectors', { vector_status: 'queued' }],
    ['failed vectors', { vector_status: 'failed' }],
    ['missing vector evidence', { vector_status: undefined }],
    ['failed semantics', { semantic_status: 'failed' }],
    ['missing semantic evidence', { semantic_status: undefined }],
    ['no content update', { content_updated: false }],
    ['wrong byte count', { written_bytes: 0 }],
    ['foreign URI', { uri: 'viking://user/other/memories/entities/foreign.md' }],
    ['another file in the same root', { uri: 'viking://user/team/memories/entities/other.md' }],
    ['replacement instead of creation', { mode: 'replace' }],
    ['wrong content type', { context_type: 'resource' }],
    ['queue errors despite complete flags', { queue_status: { Embedding: { error_count: 1, errors: [] } } }],
    ['reported queue error', { queue_status: { Semantic: { error_count: 0, errors: ['synthetic failure'] } } }],
  ])('does not report a committed write for %s', async (_name, response) => {
    const f = writeFixture({ response })
    await expect(f.provider.remember(f.body, { content: 'Exact canary', category: 'fact' })).rejects.toThrow(/not verified.*remote content may remain/u)
    expect(f.fetchMock).toHaveBeenCalledTimes(1)
    // An ambiguous response must never delete an unrelated target or retry.
    expect(f.fetchMock.mock.calls[0]?.[1]?.method).toBe('POST')
  })

  it('requires exact readback even after completed indexing', async () => {
    const f = writeFixture({ readback: 'A summary is not the exact original.' })
    await expect(f.provider.remember(f.body, { content: 'Exact original', category: 'fact' })).rejects.toThrow('stored content does not match')
    expect(f.fetchMock).toHaveBeenCalledTimes(2)
  })

  it.each(['constructor', '__proto__'])('maps inherited object names to the safe default category folder: %s', async category => {
    const f = writeFixture()
    const request = { content: 'Unknown category', category } as unknown as Parameters<OpenVikingProvider['remember']>[1]
    expect(await f.provider.remember(f.body, request)).toMatchObject({ id: expect.stringContaining('/memories/entities/') })
  })

  it.each([404, 409, 500])('does not retry, extract, or delete after HTTP %s', async status => {
    const f = writeFixture({ fetch: async () => new Response(JSON.stringify({ status: 'error', error: { message: `Synthetic HTTP ${status}` } }), { status }) })
    await expect(f.provider.remember(f.body, { content: 'Canary', category: 'fact' })).rejects.toThrow(`Synthetic HTTP ${status}`)
    expect(f.fetchMock).toHaveBeenCalledTimes(1)
  })

  it('rejects an empty successful HTTP response', async () => {
    const f = writeFixture({ fetch: async () => new Response('', { status: 200 }) })
    await expect(f.provider.remember(f.body, { content: 'Canary', category: 'fact' })).rejects.toThrow('did not confirm')
  })

  it('honors the configured timeout without extending it to a minute', async () => {
    const f = writeFixture({ requestTimeoutMs: 10, fetch: async (_url, init) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
    }) })
    await expect(f.provider.remember(f.body, { content: 'Canary', category: 'fact' })).rejects.toThrow('timed out after 10ms')
    expect(f.fetchMock).toHaveBeenCalledTimes(1)
    expect(JSON.parse(String(f.fetchMock.mock.calls[0]?.[1]?.body))).toMatchObject({ timeout: 0.01, mode: 'create', wait: true })
  })

  it('does not begin a write after cancellation', async () => {
    const f = writeFixture()
    const signal = AbortSignal.abort(new Error('Turn was cancelled'))
    await expect(f.provider.remember(f.body, { content: 'Canary', category: 'fact' }, signal)).rejects.toThrow('Turn was cancelled')
    expect(f.fetchMock).not.toHaveBeenCalled()
  })

  it.each(['configured-owner', ''])('resolves legacy shorthand using configured or authenticated user %j', async user => {
    const f = writeFixture({ targetUri: 'viking://user/memories', user })
    const owner = user || 'resolved-owner'
    const receipt = await f.provider.remember(f.body, { content: 'Canary', category: 'fact' })
    expect(receipt).toMatchObject({ id: expect.stringContaining(`viking://user/${owner}/memories/entities/`) })
    const requests = f.fetchMock.mock.calls
    expect(requests.some(([url]) => String(url).includes('/system/status'))).toBe(user === '')
    for (const [, init] of requests) {
      expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer synthetic-only-key')
      expect(new Headers(init?.headers).get('X-OpenViking-Account')).toBe('work')
      expect(new Headers(init?.headers).get('X-OpenViking-Actor-Peer')).toBe('synthetic-actor')
    }
  })

  it.each(['viking://user/../memories', 'viking://user/%2e%2e/memories', 'viking://user/team?x/memories', 'viking://resources/memories'])('refuses unsafe persisted root %s before network access', async targetUri => {
    const f = writeFixture()
    const connection = f.authority.providerConnection(f.body.id, f.body.provider.id)
    const provider = new OpenVikingProvider({ ...f.authority, providerConnection: () => ({ ...connection, targetUri }) }, { fetch: f.fetchMock })
    await expect(provider.remember(f.body, { content: 'Canary', category: 'fact' })).rejects.toThrow('safe viking://user')
    expect(f.fetchMock).not.toHaveBeenCalled()
  })

  it.each([
    '../foreign.md', 'entities/../../other/memories/foreign.md', 'entities/%2e%2e/foreign.md',
    'entities/%252e%252e/foreign.md', 'entities/..\\foreign.md', 'entities//foreign.md',
    '.private/secret.md', 'entities/.abstract.md', 'entities/foreign.md?query.md', 'entities/foreign.md#hash.md',
  ])('refuses unsafe forget suffix %s before network access', async suffix => {
    const f = writeFixture()
    await expect(f.provider.forget(f.body, `viking://user/team/memories/${suffix}`)).rejects.toThrow('exact non-generated')
    expect(f.fetchMock).not.toHaveBeenCalled()
  })

  it('never accepts another namespace deletion receipt', async () => {
    const f = writeFixture({ fetch: async () => ok({ uri: 'viking://user/other/memories/entities/other.md', estimated_deleted_count: 1 }) })
    await expect(f.provider.forget(f.body, 'viking://user/team/memories/entities/canary.md')).rejects.toThrow('did not confirm deletion')
  })
})
