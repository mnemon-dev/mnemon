import { descriptor } from './descriptor.ts'
import type { JsonValue } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemorySpaceAuthority } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { Insight, MemoryBody as MemorySpace, MemoryListRequest, RememberRequest, SearchRequest } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { HttpMemoryProvider, firstArray, jsonNumber, jsonObject, jsonString, type HttpProviderOptions } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { NORMALIZED_RELEVANCE_SCORE, type MemoryProviderAdapter, type ProviderBodyStatus as ProviderSpaceStatus, type ProviderMemorySpace, type ProviderSearchResult } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

function insight(value: unknown): Insight | undefined {
  const item = jsonObject(value)
  const id = jsonString(item?.id)
  const content = jsonString(item?.memory) ?? jsonString(item?.chunk) ?? jsonString(item?.content)
  if (id === undefined || content === undefined) return undefined
  const metadata = jsonObject(item?.metadata)
  const score = jsonNumber(item?.similarity) ?? jsonNumber(item?.score)
  const createdAt = jsonString(item?.updatedAt) ?? jsonString(item?.createdAt)
  return {
    id,
    content,
    category: jsonString(metadata?.category) ?? 'general',
    source: 'external',
    ...(score === undefined ? {} : { score }),
    ...(createdAt === undefined ? {} : { createdAt }),
  }
}

export class SupermemoryProvider extends HttpMemoryProvider implements MemoryProviderAdapter {
  readonly id = 'supermemory' as const
  readonly scoreSemantics = NORMALIZED_RELEVANCE_SCORE

  constructor(memorySpaces: MemorySpaceAuthority, options: HttpProviderOptions = {}) {
    super(memorySpaces, { label: descriptor.label, ...options })
  }

  async discover(connection: Record<string, string | number | boolean>, signal?: AbortSignal): Promise<ProviderMemorySpace[]> {
    const payload = await this.requestConnection(connection, '/v3/container-tags/list', { headers: this.headers(connection), signal })
    return firstArray(payload, 'containerTags', 'items').flatMap(value => {
      const item = jsonObject(value)
      const tag = jsonString(item?.containerTag) ?? jsonString(item?.container_tag)
      if (tag === undefined) return []
      return [{
        externalId: jsonString(item?.id) ?? tag,
        name: jsonString(item?.name) ?? tag,
        description: jsonString(item?.description) ?? `Supermemory space ${tag}`,
        connection: { containerTag: tag, searchMode: 'hybrid' },
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
    const payload = await this.request(body, '/v4/search', {
      headers: this.headers(connection),
      json: {
        q: request.query,
        containerTag: String(connection.containerTag),
        searchMode: String(connection.searchMode ?? 'hybrid'),
        limit: request.limit ?? 10,
      },
      signal,
    })
    return { results: firstArray(payload, 'results').map(insight).filter((item): item is Insight => item !== undefined) }
  }

  async list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]> {
    const connection = this.connection(body)
    const limit = Math.min(Math.max(request.limit ?? 200, 1), 200)
    const payload = await this.request(body, '/v4/memories/list', {
      headers: this.headers(connection),
      json: {
        containerTags: [String(connection.containerTag)],
        limit,
        page: 1,
        sort: 'createdAt',
        order: 'desc',
      },
      signal,
    })
    const memories = firstArray(payload, 'memoryEntries', 'results').map(insight).filter((item): item is Insight => item !== undefined)

    // Supermemory exposes extracted memories and their source documents as
    // separate read surfaces. Lite may extract only some documents, so merge
    // both surfaces instead of hiding documents as soon as one memory exists.
    const documents = await this.request(body, '/v3/documents/documents', {
      headers: this.headers(connection),
      json: {
        containerTags: [String(connection.containerTag)],
        limit,
        page: 1,
        sort: 'createdAt',
        order: 'desc',
      },
      signal,
    })
    const projectedDocuments = firstArray(documents, 'documents', 'memories', 'results').map(insight).filter((item): item is Insight => item !== undefined)
    return [...new Map([...memories, ...projectedDocuments].map(item => [item.id, item])).values()]
      .filter(item => request.category === undefined || item.category === request.category)
      .slice(0, limit)
  }

  async remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    const payload = await this.request(body, '/v3/documents', {
      headers: this.headers(connection),
      json: {
        content: request.content,
        containerTag: String(connection.containerTag),
        taskType: 'memory',
        metadata: {
          sm_source: 'dsh-mnemon',
          ...(request.category === undefined ? {} : { category: request.category }),
          ...(request.importance === undefined ? {} : { importance: request.importance }),
        },
      },
      signal,
    })
    const result = jsonObject(payload) ?? {}
    return {
      action: 'queued',
      provider: this.id,
      summary: 'Supermemory accepted the memory document for extraction.',
      ...(jsonString(result.id) === undefined ? {} : { id: jsonString(result.id)! }),
      ...(jsonString(result.status) === undefined ? {} : { status: jsonString(result.status)! }),
    }
  }

  async forget(body: MemorySpace, id: string, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    try {
      const payload = await this.request(body, '/v4/memories', {
        method: 'DELETE',
        headers: this.headers(connection),
        json: { id, containerTag: String(connection.containerTag), reason: 'Deleted from dsh-mnemon' },
        signal,
      })
      return {
        action: 'deleted',
        provider: this.id,
        id,
        ...(jsonObject(payload)?.forgotten === undefined ? {} : { forgotten: jsonObject(payload)!.forgotten as JsonValue }),
      }
    } catch (error) {
      if (!(error instanceof Error) || !/HTTP 404\b/u.test(error.message)) throw error
      await this.request(body, `/v3/documents/${encodeURIComponent(id)}`, {
        method: 'DELETE',
        headers: this.headers(connection),
        signal,
      })
      return { action: 'deleted', provider: this.id, id, document: true }
    }
  }

  private headers(connection: Record<string, string | number | boolean>): HeadersInit {
    const token = String(connection.apiKey ?? '').replace(/^Bearer\s+/iu, '')
    return { Authorization: `Bearer ${token}`, 'x-sm-source': 'dsh-mnemon' }
  }
}
