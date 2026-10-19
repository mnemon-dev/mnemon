import { descriptor } from './descriptor.ts'
import type { JsonValue } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemorySpaceAuthority } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { Insight, MemoryBody as MemorySpace, MemoryListRequest, RememberRequest, SearchRequest } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { HttpMemoryProvider, firstArray, jsonObject, jsonString, type HttpProviderOptions } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemoryProviderAdapter, ProviderBodyStatus as ProviderSpaceStatus, ProviderMemorySpace, ProviderSearchResult } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

function insight(value: unknown): Insight | undefined {
  const item = jsonObject(value)
  const id = jsonString(item?.id)
  const content = jsonString(item?.content)
  if (id === undefined || content === undefined) return undefined
  const observer = jsonString(item?.observer_id) ?? jsonString(item?.observer)
  const observed = jsonString(item?.observed_id) ?? jsonString(item?.observed)
  const createdAt = jsonString(item?.created_at) ?? jsonString(item?.createdAt)
  const entities = [observer, observed].filter((entry): entry is string => entry !== undefined)
  return {
    id,
    content,
    category: jsonString(item?.level) ?? 'insight',
    source: 'external',
    ...(createdAt === undefined ? {} : { createdAt }),
    ...(entities.length === 0 ? {} : { entities }),
  }
}

export class HonchoProvider extends HttpMemoryProvider implements MemoryProviderAdapter {
  readonly id = 'honcho' as const

  constructor(memorySpaces: MemorySpaceAuthority, options: HttpProviderOptions = {}) {
    super(memorySpaces, { label: descriptor.label, ...options })
  }

  async discover(connection: Record<string, string | number | boolean>, signal?: AbortSignal): Promise<ProviderMemorySpace[]> {
    const payload = await this.requestConnection(connection, '/v3/workspaces/list?page=1&size=100', {
      headers: this.headers(connection),
      json: {},
      signal,
    })
    return firstArray(payload, 'items', 'results').flatMap(value => {
      const item = jsonObject(value)
      const id = jsonString(item?.id)
      if (id === undefined) return []
      const metadata = jsonObject(item?.metadata)
      return [{
        externalId: id,
        name: jsonString(metadata?.name) ?? jsonString(metadata?.title) ?? id,
        description: jsonString(metadata?.description) ?? `Honcho workspace ${id}`,
        connection: { workspace: id, userId: '*', agentId: '*' },
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
    const payload = await this.request(body, `${this.basePath(connection)}/conclusions/query`, {
      headers: this.headers(connection),
      json: {
        query: request.query,
        top_k: Math.min(request.limit ?? 10, 100),
        // Honcho semantic query requires both peers even when discovery mapped
        // the workspace-wide body with wildcard scope. Use the same stable
        // DSH peer defaults as explicit writes so native recall remains valid.
        filters: this.scope(connection, true),
      },
      signal,
    })
    return { results: firstArray(payload, 'items', 'results').map(insight).filter((item): item is Insight => item !== undefined) }
  }

  async list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]> {
    const connection = this.connection(body)
    const limit = Math.min(Math.max(request.limit ?? 200, 1), 100)
    const payload = await this.request(body, `${this.basePath(connection)}/conclusions/list?page=1&size=${limit}`, {
      headers: this.headers(connection),
      json: {
        filters: {
          ...this.scope(connection),
          ...(request.category === undefined ? {} : { level: request.category }),
        },
      },
      signal,
    })
    return firstArray(payload, 'items', 'results').map(insight).filter((item): item is Insight => item !== undefined)
  }

  async remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    const payload = await this.request(body, `${this.basePath(connection)}/conclusions`, {
      headers: this.headers(connection),
      json: {
        conclusions: [{
          content: request.content,
          observer_id: String(connection.agentId) === '*' ? 'dsh' : String(connection.agentId),
          observed_id: String(connection.userId) === '*' ? 'dsh-user' : String(connection.userId),
          session_id: null,
        }],
      },
      signal,
    })
    const created = firstArray(payload, 'items', 'results').map(jsonObject).find(item => item !== undefined)
    return {
      action: 'stored',
      provider: this.id,
      summary: 'Honcho stored an explicit peer conclusion.',
      ...(jsonString(created?.id) === undefined ? {} : { id: jsonString(created?.id)! }),
    }
  }

  async forget(body: MemorySpace, id: string, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    await this.request(body, `${this.basePath(connection)}/conclusions/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      headers: this.headers(connection),
      signal,
    })
    return { action: 'deleted', provider: this.id, id }
  }

  private basePath(connection: Record<string, string | number | boolean>): string {
    return `/v3/workspaces/${encodeURIComponent(String(connection.workspace))}`
  }

  private scope(connection: Record<string, string | number | boolean>, requirePeers = false): Record<string, JsonValue> {
    const agentId = String(connection.agentId)
    const userId = String(connection.userId)
    return {
      ...(agentId === '*' ? requirePeers ? { observer_id: 'dsh' } : {} : { observer_id: agentId }),
      ...(userId === '*' ? requirePeers ? { observed_id: 'dsh-user' } : {} : { observed_id: userId }),
    }
  }

  private headers(connection: Record<string, string | number | boolean>): HeadersInit {
    const token = String(connection.apiKey ?? '').replace(/^Bearer\s+/iu, '')
    return token === '' ? {} : { Authorization: `Bearer ${token}` }
  }
}
