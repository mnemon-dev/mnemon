import { descriptor } from './descriptor.ts'
import type { JsonValue } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemorySpaceAuthority } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { Insight, MemoryBody as MemorySpace, MemoryListRequest, RememberRequest, SearchRequest } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { HttpMemoryProvider, firstArray, jsonNumber, jsonObject, jsonString, type HttpProviderOptions } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { NORMALIZED_RELEVANCE_SCORE, type MemoryProviderAdapter, type ProviderBodyStatus as ProviderSpaceStatus, type ProviderMemorySpace, type ProviderSearchResult } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

function insight(value: unknown): Insight | undefined {
  const item = jsonObject(value)
  const id = jsonString(item?.id) ?? jsonString(item?.memory_id)
  const content = jsonString(item?.content) ?? jsonString(item?.memory) ?? jsonString(item?.text)
  if (id === undefined || content === undefined) return undefined
  const score = jsonNumber(item?.score) ?? jsonNumber(item?.similarity)
  const createdAt = jsonString(item?.created_at) ?? jsonString(item?.createdAt) ?? jsonString(item?.updated_at)
  return {
    id,
    content,
    category: jsonString(item?.memory_type) ?? jsonString(item?.category) ?? 'general',
    source: 'external',
    ...(score === undefined ? {} : { score }),
    ...(createdAt === undefined ? {} : { createdAt }),
  }
}

export class RetainDbProvider extends HttpMemoryProvider implements MemoryProviderAdapter {
  readonly id = 'retaindb' as const
  readonly scoreSemantics = NORMALIZED_RELEVANCE_SCORE

  constructor(memorySpaces: MemorySpaceAuthority, options: HttpProviderOptions = {}) {
    super(memorySpaces, { label: descriptor.label, ...options })
  }

  async discover(connection: Record<string, string | number | boolean>, signal?: AbortSignal): Promise<ProviderMemorySpace[]> {
    const payload = await this.requestConnection(connection, '/v1/projects', { headers: this.headers(connection, '/v1/projects'), signal })
    return firstArray(payload, 'projects', 'items').flatMap(value => {
      const item = jsonObject(value)
      const project = jsonString(item?.slug) ?? jsonString(item?.name) ?? jsonString(item?.id)
      if (project === undefined) return []
      return [{
        externalId: jsonString(item?.id) ?? project,
        name: jsonString(item?.name) ?? project,
        description: jsonString(item?.description) ?? `RetainDB project ${project}`,
        connection: { project, userId: '*' },
      }]
    })
  }

  async status(body: MemorySpace, signal?: AbortSignal): Promise<ProviderSpaceStatus> {
    try {
      await this.list(body, { limit: 1 }, signal)
      return { healthy: true }
    } catch (error) {
      return { healthy: false, error: error instanceof Error ? error.message : String(error) }
    }
  }

  async search(body: MemorySpace, request: SearchRequest, signal?: AbortSignal): Promise<ProviderSearchResult> {
    const connection = this.connection(body)
    const payload = await this.request(body, '/v1/memory/search', {
      headers: this.headers(connection, '/v1/memory/search'),
      json: {
        project: String(connection.project),
        query: request.query,
        ...(String(connection.userId) === '*' ? {} : { user_id: String(connection.userId) }),
        session_id: `dsh-${body.id}`,
        top_k: request.limit ?? 10,
        include_pending: true,
      },
      signal,
    })
    return { results: firstArray(payload, 'results', 'memories').map(insight).filter((item): item is Insight => item !== undefined) }
  }

  async list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]> {
    const connection = this.connection(body)
    const params = new URLSearchParams({ project: String(connection.project), include_pending: 'true' })
    let payload: unknown
    try {
      if (String(connection.userId) === '*') throw new Error('project-wide scope uses the collection endpoint')
      payload = await this.request(body, `/v1/memory/profile/${encodeURIComponent(String(connection.userId))}?${params}`, {
        headers: this.headers(connection, '/v1/memory/profile'),
        signal,
      })
    } catch {
      if (String(connection.userId) !== '*') params.set('user_id', String(connection.userId))
      params.set('limit', String(Math.min(Math.max(request.limit ?? 200, 1), 200)))
      payload = await this.request(body, `/v1/memories?${params}`, { headers: this.headers(connection, '/v1/memories'), signal })
    }
    return firstArray(payload, 'memories', 'results').map(insight).filter((item): item is Insight => item !== undefined)
      .filter(item => request.category === undefined || item.category === request.category)
      .slice(0, Math.min(Math.max(request.limit ?? 200, 1), 200))
  }

  async remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    const json = {
      project: String(connection.project),
      content: request.content,
      memory_type: request.category ?? 'factual',
      user_id: String(connection.userId) === '*' ? 'dsh-user' : String(connection.userId),
      session_id: `dsh-${body.id}`,
      importance: request.importance ?? 0.7,
      write_mode: 'sync',
    } as const
    let payload: unknown
    try {
      payload = await this.request(body, '/v1/memory', { headers: this.headers(connection, '/v1/memory'), json, signal })
    } catch {
      const { write_mode: _writeMode, ...legacy } = json
      payload = await this.request(body, '/v1/memories', { headers: this.headers(connection, '/v1/memories'), json: legacy, signal })
    }
    const result = jsonObject(payload) ?? {}
    return {
      action: 'stored',
      provider: this.id,
      summary: 'RetainDB stored the memory synchronously.',
      ...(jsonString(result.id) === undefined ? {} : { id: jsonString(result.id)! }),
    }
  }

  async forget(body: MemorySpace, id: string, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    try {
      await this.request(body, `/v1/memory/${encodeURIComponent(id)}`, { method: 'DELETE', headers: this.headers(connection, '/v1/memory'), signal })
    } catch {
      await this.request(body, `/v1/memories/${encodeURIComponent(id)}`, { method: 'DELETE', headers: this.headers(connection, '/v1/memories'), signal })
    }
    return { action: 'deleted', provider: this.id, id }
  }

  private headers(connection: Record<string, string | number | boolean>, path: string): HeadersInit {
    const token = String(connection.apiKey ?? '').replace(/^Bearer\s+/iu, '')
    return {
      Authorization: `Bearer ${token}`,
      'x-sdk-runtime': 'dsh-mnemon',
      ...(path.startsWith('/v1/memory') || path.startsWith('/v1/context') ? { 'X-API-Key': token } : {}),
    }
  }
}
