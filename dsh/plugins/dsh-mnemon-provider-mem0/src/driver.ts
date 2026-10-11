import { descriptor } from './descriptor.ts'
import type { JsonValue } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemorySpaceAuthority } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { Insight, MemoryBody as MemorySpace, MemoryListRequest, RememberRequest, SearchRequest } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import {
  HttpMemoryProvider,
  firstArray,
  jsonArray,
  jsonNumber,
  jsonObject,
  jsonString,
  type HttpProviderOptions,
} from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { NORMALIZED_RELEVANCE_SCORE, type MemoryProviderAdapter, type ProviderBodyStatus as ProviderSpaceStatus, type ProviderMemorySpace, type ProviderSearchResult } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

function category(item: Record<string, unknown>): string {
  const categories = jsonArray(item.categories).filter((value): value is string => typeof value === 'string')
  return jsonString(item.category) ?? categories[0] ?? 'general'
}

function insight(value: unknown): Insight | undefined {
  const item = jsonObject(value)
  const id = jsonString(item?.id)
  const content = jsonString(item?.memory) ?? jsonString(item?.text) ?? jsonString(item?.content)
  if (id === undefined || content === undefined) return undefined
  const score = jsonNumber(item?.score)
  const createdAt = jsonString(item?.created_at) ?? jsonString(item?.createdAt) ?? jsonString(item?.updated_at)
  const tags = jsonArray(item?.categories).filter((entry): entry is string => typeof entry === 'string')
  return {
    id,
    content,
    category: category(item!),
    source: 'external',
    ...(score === undefined ? {} : { score }),
    ...(createdAt === undefined ? {} : { createdAt }),
    ...(tags.length === 0 ? {} : { tags }),
  }
}

export class Mem0Provider extends HttpMemoryProvider implements MemoryProviderAdapter {
  readonly id = 'mem0' as const
  readonly scoreSemantics = NORMALIZED_RELEVANCE_SCORE

  constructor(memorySpaces: MemorySpaceAuthority, options: HttpProviderOptions = {}) {
    super(memorySpaces, { label: descriptor.label, ...options })
  }

  async discover(connection: Record<string, string | number | boolean>, signal?: AbortSignal): Promise<ProviderMemorySpace[]> {
    const mode = String(connection.mode ?? 'platform')
    const payload = await this.requestConnection(connection, mode === 'self-hosted' ? '/entities' : '/v1/entities', {
      headers: this.headers(connection, mode),
      signal,
    })
    return firstArray(payload, 'entities', 'results').flatMap(value => {
      const item = jsonObject(value)
      const id = jsonString(item?.id)
      const type = jsonString(item?.type)
      if (id === undefined || (type !== 'user' && type !== 'agent')) return []
      const metadata = jsonObject(item?.metadata)
      const count = jsonNumber(item?.total_memories)
      return [{
        externalId: `${type}:${id}`,
        name: jsonString(item?.name) ?? jsonString(metadata?.name) ?? id,
        description: jsonString(metadata?.description) ?? `${type === 'user' ? 'User' : 'Agent'} memory${count === undefined ? '' : ` · ${count} memories`}`,
        connection: type === 'user'
          ? { userId: id, agentId: '*', rerank: false }
          : { userId: '*', agentId: id, rerank: false },
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
    const mode = String(connection.mode ?? 'platform')
    const filters = this.filters(connection)
    const payload = await this.request(body, mode === 'self-hosted' ? '/search' : '/v3/memories/search/', {
      headers: this.headers(connection, mode),
      json: {
        query: request.query,
        filters,
        top_k: request.limit ?? 10,
        ...(mode === 'platform' && connection.rerank === true ? { rerank: true } : {}),
      },
      signal,
    })
    return { results: firstArray(payload, 'results', 'memories').map(insight).filter((item): item is Insight => item !== undefined) }
  }

  async list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]> {
    const connection = this.connection(body)
    const mode = String(connection.mode ?? 'platform')
    const limit = Math.min(Math.max(request.limit ?? 200, 1), 200)
    const payload = mode === 'self-hosted'
      ? await this.request(body, `/memories?${new URLSearchParams({ ...this.filters(connection) as Record<string, string>, limit: String(limit) })}`, {
          headers: this.headers(connection, mode),
          signal,
        })
      : await this.request(body, `/v3/memories/?page=1&page_size=${limit}`, {
          headers: this.headers(connection, mode),
          json: { filters: this.filters(connection), ...(request.category === undefined ? {} : { categories: [request.category] }) },
          signal,
        })
    return firstArray(payload, 'results', 'memories').map(insight).filter((item): item is Insight => item !== undefined)
  }

  async remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    const mode = String(connection.mode ?? 'platform')
    const payload = await this.request(body, mode === 'self-hosted' ? '/memories' : '/v3/memories/add/', {
      headers: this.headers(connection, mode),
      json: {
        messages: [{ role: 'user', content: request.content }],
        user_id: String(connection.userId) === '*' ? 'dsh-user' : String(connection.userId),
        agent_id: String(connection.agentId) === '*' ? 'dsh' : String(connection.agentId),
        ...(mode === 'self-hosted' ? { infer: false } : {}),
        metadata: {
          source: 'dsh-mnemon',
          ...(request.category === undefined ? {} : { category: request.category }),
          ...(request.importance === undefined ? {} : { importance: request.importance }),
          ...(request.tags === undefined ? {} : { tags: request.tags }),
          ...(request.entities === undefined ? {} : { entities: request.entities }),
        },
      },
      signal,
    })
    const result = jsonObject(payload) ?? {}
    return {
      action: mode === 'platform' ? 'queued' : 'stored',
      provider: this.id,
      summary: mode === 'platform' ? 'Mem0 queued the memory for extraction.' : 'Mem0 stored the explicit memory.',
      ...(jsonString(result.event_id) === undefined ? {} : { eventId: jsonString(result.event_id)! }),
      ...(jsonString(result.status) === undefined ? {} : { status: jsonString(result.status)! }),
    }
  }

  async forget(body: MemorySpace, id: string, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    const mode = String(connection.mode ?? 'platform')
    const path = mode === 'self-hosted' ? `/memories/${encodeURIComponent(id)}` : `/v1/memories/${encodeURIComponent(id)}`
    await this.request(body, path, { method: 'DELETE', headers: this.headers(connection, mode), signal })
    return { action: 'deleted', provider: this.id, id }
  }

  private filters(connection: Record<string, string | number | boolean>): Record<string, JsonValue> {
    const userId = String(connection.userId)
    const agentId = String(connection.agentId ?? '')
    return {
      ...(userId === '*' ? {} : { user_id: userId }),
      ...(agentId === '' || agentId === '*' ? {} : { agent_id: agentId }),
    }
  }

  private headers(connection: Record<string, string | number | boolean>, mode: string): HeadersInit {
    const apiKey = String(connection.apiKey ?? '').replace(/^(?:Token|Bearer)\s+/iu, '')
    if (apiKey === '') return {}
    return mode === 'self-hosted' ? { 'X-API-Key': apiKey } : { Authorization: `Token ${apiKey}` }
  }
}
