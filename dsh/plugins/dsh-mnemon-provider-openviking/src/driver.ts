import { randomUUID } from 'node:crypto'
import type { JsonValue } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemorySpaceAuthority } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type {
  Insight,
  MemoryBody as MemorySpace,
  MemoryGraphSnapshot,
  MemoryListRequest,
  MemoryProviderConnection,
  OpenVikingBodyConnection as OpenVikingSpaceConnection,
  RememberRequest,
  SearchRequest,
} from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { NORMALIZED_RELEVANCE_SCORE, type MemoryProviderAdapter, type ProviderBodyStatus as ProviderSpaceStatus, type ProviderMemorySpace, type ProviderSearchResult } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

interface OpenVikingEnvelope {
  status?: string
  result?: unknown
  error?: { code?: string; message?: string; trace_id?: string }
  trace_id?: string
}

interface OpenVikingRequestOptions {
  timeoutMs?: number
  signal?: AbortSignal | undefined
}

interface OpenVikingProviderOptions {
  fetch?: typeof fetch
  requestTimeoutMs?: number
}

type OpenVikingConnection = OpenVikingSpaceConnection & { discoveryUser?: string }

function object(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

function string(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined
}

function number(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

// Agent-authored memories use the content API; extraction counters cannot
// prove that a one-turn session stored the caller's text.
const MEMORY_FOLDERS: Record<string, string> = {
  preference: 'preferences',
  insight: 'experiences',
  decision: 'experiences',
  context: 'events',
  fact: 'entities',
  general: 'entities',
}

function memoryUri(root: string, category: string, content: string): string {
  const folder = Object.hasOwn(MEMORY_FOLDERS, category) ? MEMORY_FOLDERS[category] : 'entities'
  const firstLine = content.split('\n').find(line => line.trim().length > 0) ?? 'memory'
  const slug = Array.from(firstLine.replace(/^#+\s*/u, '').replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/gu, '')).slice(0, 48).join('').toLowerCase() || 'memory'
  const stamp = new Date().toISOString().replace(/[-:T]/gu, '').slice(0, 14)
  return `${root}/${folder}/${stamp}-${slug}-${randomUUID()}.md`
}

function safeUser(user: string): boolean {
  return /^[a-zA-Z0-9_.@-]+$/u.test(user) && user !== '.' && user !== '..' && (user.match(/@/gu)?.length ?? 0) <= 1
}

function discoveryUser(connection: MemoryProviderConnection | OpenVikingConnection): string | undefined {
  const user = String(connection.discoveryUser ?? '').trim()
  if (user === '') return undefined
  if (!safeUser(user) || !safeUser(String(connection.account ?? '').trim())) {
    throw new Error('OpenViking user-key discovery requires an explicit account and a safe discovery user')
  }
  if (String(connection.apiKey ?? '').trim() === '') throw new Error('OpenViking user-key discovery requires a user API key')
  return user
}

function isMemoryFile(uri: string, root: string): boolean {
  if (!uri.startsWith(`${root}/`) || !uri.endsWith('.md') || /[%?#\\\u0000-\u001f\u007f]/u.test(uri)) return false
  return uri.slice(root.length + 1).split('/').every(part => part !== '' && !part.startsWith('.') && part.trim() === part)
}

function queueFailed(value: unknown): boolean {
  return Object.values(object(value) ?? {}).some(entry => {
    const queue = object(entry)
    return (number(queue?.error_count) ?? 0) > 0 || (Array.isArray(queue?.errors) && queue.errors.length > 0)
  })
}

function categoryFromUri(uri: string): string {
  const marker = '/memories/'
  const suffix = uri.includes(marker) ? uri.slice(uri.indexOf(marker) + marker.length) : ''
  return suffix.split('/')[0]?.replace(/\.md$/u, '') || 'general'
}

export class OpenVikingProvider implements MemoryProviderAdapter {
  readonly id = 'openviking' as const
  readonly scoreSemantics = NORMALIZED_RELEVANCE_SCORE
  private readonly requestFetch: typeof fetch
  private readonly requestTimeoutMs: number

  constructor(private readonly memorySpaces: MemorySpaceAuthority, options: OpenVikingProviderOptions = {}) {
    this.requestFetch = options.fetch ?? globalThis.fetch
    this.requestTimeoutMs = options.requestTimeoutMs ?? 15_000
  }

  async discover(connection: MemoryProviderConnection, signal?: AbortSignal): Promise<ProviderMemorySpace[]> {
    signal?.throwIfAborted()
    let account = String(connection.account ?? '').trim()
    const selectedUser = discoveryUser(connection)
    if (selectedUser !== undefined) {
      const targetUri = await this.validateUserNamespace(connection, selectedUser, signal)
      return [{
        externalId: `${account}:${selectedUser}`,
        name: selectedUser,
        description: `OpenViking memory namespace for ${selectedUser}`,
        connection: { targetUri, user: selectedUser, actorPeerId: 'dsh' },
      }]
    }
    if (account === '') {
      const accounts = await this.requestConnection(connection, '/api/v1/admin/accounts', {}, { signal })
      const items = Array.isArray(accounts) ? accounts : []
      const ids = items.flatMap(value => {
        const id = string(object(value)?.account_id) ?? string(object(value)?.id)
        return id === undefined ? [] : [id]
      })
      if (ids.length > 1) throw new Error('OpenViking exposes multiple accounts; configure the account to select one discovery scope')
      account = ids[0] ?? 'default'
    }
    const users = await this.requestConnection({ ...connection, account }, `/api/v1/admin/accounts/${encodeURIComponent(account)}/users?limit=100`, {}, { signal })
    const items = Array.isArray(users) ? users : []
    return items.flatMap(value => {
      const item = object(value)
      const user = string(item?.user_id) ?? string(item?.id) ?? string(item?.name)
      if (user === undefined || !safeUser(user)) return []
      return [{
        externalId: `${account}:${user}`,
        name: string(item?.display_name) ?? string(item?.name) ?? user,
        description: string(item?.description) ?? string(item?.role) ?? `OpenViking memory namespace for ${user}`,
        connection: { targetUri: `viking://user/${user}/memories`, user, actorPeerId: 'dsh' },
      }]
    })
  }

  async status(body: MemorySpace, signal?: AbortSignal): Promise<ProviderSpaceStatus> {
    try {
      const connection = this.connection(body)
      const selectedUser = discoveryUser(connection)
      if (selectedUser === undefined) await this.request(body, '/health', {}, { signal, timeoutMs: 5_000 })
      else {
        await this.memoryRoot(body, signal)
        await this.validateUserNamespace(connection, selectedUser, signal)
      }
      return { healthy: true }
    } catch (error) {
      return { healthy: false, error: error instanceof Error ? error.message : String(error) }
    }
  }

  async search(body: MemorySpace, request: SearchRequest, signal?: AbortSignal): Promise<ProviderSearchResult> {
    const memoryRoot = await this.memoryRoot(body, signal)
    const result = await this.request(body, '/api/v1/search/find', {
      method: 'POST',
      body: JSON.stringify({
        query: request.query,
        target_uri: memoryRoot,
        context_type: ['memory'],
        limit: request.limit,
      }),
    }, { signal })
    const root = object(result)
    const entries = Array.isArray(root?.memories) ? root.memories : []
    const matches = entries.flatMap((value): Insight[] => {
        const item = object(value)
        const uri = string(item?.uri)
        if (uri === undefined || !isMemoryFile(uri, memoryRoot)) return []
        const score = number(item?.score)
        return [{
          id: uri,
          externalUri: uri,
          content: string(item?.overview) ?? string(item?.abstract) ?? uri,
          category: string(item?.category) ?? categoryFromUri(uri),
          source: 'external',
          ...(score === undefined ? {} : { score }),
        }]
      }).slice(0, request.limit)
    return { results: await Promise.all(matches.map(async item => ({ ...item, content: await this.readContent(body, item.id, signal) }))) }
  }

  async graph(body: MemorySpace, signal?: AbortSignal): Promise<MemoryGraphSnapshot> {
    // Remote providers are projected as a bounded, disconnected browse view;
    // they do not pretend to expose Mnemon's typed graph relationships.
    const items = await this.list(body, { limit: 200 }, signal)
    return {
      nodes: items.map(item => ({ ...item, color: '#5568d9' })),
      edges: [],
      generatedAt: new Date().toISOString(),
    }
  }

  async list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]> {
    const memoryRoot = await this.memoryRoot(body, signal)
    const query = new URLSearchParams({ uri: memoryRoot, recursive: 'true', output: 'original' })
    const result = await this.request(body, `/api/v1/fs/ls?${query}`, {}, { signal })
    const entries = Array.isArray(result) ? result : []
    const limit = Math.min(Math.max(request.limit ?? 200, 1), 1000)
    const files = entries.flatMap((value): Array<{ item: Record<string, unknown>; uri: string }> => {
      const item = object(value)
      const uri = string(item?.uri)
      return item === undefined || uri === undefined || item.isDir === true || !isMemoryFile(uri, memoryRoot) ? [] : [{ item, uri }]
    }).slice(0, limit)
    return Promise.all(files.map(async ({ item, uri }): Promise<Insight> => {
      const content = await this.readContent(body, uri, signal)
      const createdAt = string(item.modTime)
      return {
        id: uri,
        externalUri: uri,
        content,
        category: categoryFromUri(uri),
        source: 'external',
        ...(createdAt === undefined ? {} : { createdAt }),
      }
    }))
  }

  async remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    const root = await this.memoryRoot(body, signal)
    const category = string(request.category) ?? 'general'
    const uri = memoryUri(root, category, request.content)
    const tags = ['source=mnemon', `category=${category}`]
    const importance = number(request.importance)
    if (importance !== undefined) tags.push(`importance=${importance}`)
    const writtenBytes = Buffer.byteLength(request.content, 'utf8')
    try {
      const written = object(await this.request(body, '/api/v1/content/write', {
        method: 'POST',
        body: JSON.stringify({ uri, content: request.content, mode: 'create', wait: true, timeout: this.requestTimeoutMs / 1000, tags }),
      }, { signal }))
      if (written?.uri !== uri || written.content_updated !== true || written.written_bytes !== writtenBytes
        || written.mode !== 'create' || written.context_type !== 'memory'
        || written.vector_status !== 'complete' || !['complete', 'skipped'].includes(String(written.semantic_status))
        || queueFailed(written.queue_status)) {
        throw new Error('content API did not confirm this exact memory and completed indexing')
      }
      // The public read projection removes OpenViking's reserved metadata.
      // Reject normalization or a stale read rather than promising exactness.
      if (await this.readContent(body, uri, signal) !== request.content) throw new Error('stored content does not match the requested text')
      signal?.throwIfAborted()
    } catch (error) {
      // A server timeout can happen after the file was written. Do not retry,
      // fall back to extraction, or delete a target whose creation is uncertain.
      throw new Error(`OpenViking write was not verified at ${uri}; remote content may remain. Inspect this URI before retrying. ${error instanceof Error ? error.message : String(error)}`, { cause: error })
    }
    return {
      action: 'stored',
      provider: 'openviking',
      id: uri,
      uri,
      externalUri: uri,
      summary: `OpenViking stored the memory at ${uri} (category ${category}).`,
      writtenBytes,
      vectorStatus: 'complete',
    }
  }

  async forget(body: MemorySpace, id: string, signal?: AbortSignal): Promise<JsonValue> {
    const root = await this.memoryRoot(body, signal)
    const uri = id.trim()
    if (!isMemoryFile(uri, root)) {
      throw new Error('OpenViking forget requires an exact non-generated .md memory URI inside this Memory Space')
    }
    const query = new URLSearchParams({ uri, recursive: 'false' })
    const result = object(await this.request(body, `/api/v1/fs?${query}`, { method: 'DELETE' }, { signal })) ?? {}
    if (result.uri !== uri) throw new Error('OpenViking did not confirm deletion of the requested memory URI')
    return {
      action: 'deleted',
      provider: this.id,
      id: uri,
      uri,
      ...(number(result.estimated_deleted_count) === undefined ? {} : { estimatedDeletedCount: number(result.estimated_deleted_count)! }),
    }
  }

  private connection(body: MemorySpace): OpenVikingConnection {
    if ((body.provider.typeId ?? body.provider.id) !== this.id) throw new Error(`OpenViking cannot serve provider ${body.provider.id}`)
    const connection = this.memorySpaces.providerConnection(body.id, body.provider.id)
    return { endpoint: String(connection.endpoint ?? ''), targetUri: String(connection.targetUri ?? ''), apiKey: String(connection.apiKey ?? ''), account: String(connection.account ?? ''), user: String(connection.user ?? ''), actorPeerId: String(connection.actorPeerId ?? ''), discoveryUser: String(connection.discoveryUser ?? '') }
  }

  private async validateUserNamespace(connection: MemoryProviderConnection | OpenVikingConnection, user: string, signal?: AbortSignal): Promise<string> {
    const root = `viking://user/${user}/memories`
    // Health is unauthenticated on some deployments. A read-only, scoped data
    // request must succeed before the Host persists or replaces projections.
    const query = new URLSearchParams({ uri: root, recursive: 'false', output: 'original' })
    const result = await this.requestConnection(connection, `/api/v1/fs/ls?${query}`, {}, { signal })
    if (!Array.isArray(result) || result.some(item => !string(object(item)?.uri)?.startsWith(`${root}/`))) {
      throw new Error('OpenViking could not validate the selected memory namespace')
    }
    return root
  }

  private async memoryRoot(body: MemorySpace, signal?: AbortSignal): Promise<string> {
    signal?.throwIfAborted()
    const connection = this.connection(body)
    const root = connection.targetUri.replace(/\/+$/u, '')
    const match = /^viking:\/\/user(?:\/([^/]+))?\/memories$/u.exec(root)
    if (match === null || (match[1] !== undefined && !safeUser(match[1]))) throw new Error('OpenViking memory URI must be a safe viking://user/<user>/memories root')
    const selectedUser = discoveryUser(connection)
    if (selectedUser !== undefined) {
      if ((match[1] !== undefined && match[1] !== selectedUser) || (connection.user?.trim() && connection.user.trim() !== selectedUser)) {
        throw new Error('OpenViking memory owner must match the configured discovery user')
      }
      return `viking://user/${selectedUser}/memories`
    }
    if (match[1] !== undefined) return root
    // Retain old persisted shorthand without sending the ambiguous URI to
    // newer servers. With no configured user, ask the authenticated backend.
    const user = connection.user?.trim() || string(object(await this.request(body, '/api/v1/system/status', {}, { signal }))?.user) || ''
    if (!safeUser(user)) throw new Error('OpenViking could not resolve the memory owner; configure an explicit user memory URI')
    return `viking://user/${user}/memories`
  }

  private async readContent(body: MemorySpace, uri: string, signal?: AbortSignal): Promise<string> {
    const result = await this.request(body, `/api/v1/content/read?${new URLSearchParams({ uri })}`, {}, { signal })
    if (typeof result !== 'string') throw new Error('OpenViking content read did not return text')
    return result
  }

  private async request(body: MemorySpace, path: string, init: RequestInit = {}, options: OpenVikingRequestOptions = {}): Promise<unknown> {
    const connection = this.connection(body)
    return this.requestConnection(connection, path, init, options)
  }

  private async requestConnection(connection: MemoryProviderConnection | OpenVikingConnection, path: string, init: RequestInit = {}, options: OpenVikingRequestOptions = {}): Promise<unknown> {
    options.signal?.throwIfAborted()
    // In api_key mode the backend binds the account/user to the key and
    // rejects trusted identity overrides. Preserve old discovery by default.
    const keyBound = discoveryUser(connection) !== undefined
    const controller = new AbortController()
    const relay = () => controller.abort(options.signal?.reason)
    options.signal?.addEventListener('abort', relay, { once: true })
    const timer = setTimeout(() => controller.abort(new Error('OpenViking request timed out')), options.timeoutMs ?? this.requestTimeoutMs)
    try {
      const response = await this.requestFetch(`${connection.endpoint}${path}`, {
        ...init,
        headers: {
          'Content-Type': 'application/json',
          ...(connection.apiKey === undefined || connection.apiKey === '' ? {} : { Authorization: `Bearer ${connection.apiKey}` }),
          ...(keyBound || connection.account === undefined || connection.account === '' ? {} : { 'X-OpenViking-Account': String(connection.account) }),
          ...(keyBound || connection.user === undefined || connection.user === '' ? {} : { 'X-OpenViking-User': String(connection.user) }),
          ...(connection.actorPeerId === undefined || connection.actorPeerId === '' ? {} : { 'X-OpenViking-Actor-Peer': String(connection.actorPeerId) }),
          ...init.headers,
        },
        signal: controller.signal,
      })
      const envelope = await response.json().catch(() => ({})) as OpenVikingEnvelope
      if (!response.ok || envelope.status === 'error') {
        const trace = envelope.error?.trace_id ?? envelope.trace_id
        throw new Error(`${envelope.error?.message ?? `OpenViking HTTP ${response.status}`}${trace === undefined ? '' : ` (trace ${trace})`}`)
      }
      return envelope.result ?? envelope
    } catch (error) {
      if (controller.signal.aborted && options.signal?.aborted !== true) throw new Error(`OpenViking request timed out after ${options.timeoutMs ?? this.requestTimeoutMs}ms`)
      throw error
    } finally {
      clearTimeout(timer)
      options.signal?.removeEventListener('abort', relay)
    }
  }
}
