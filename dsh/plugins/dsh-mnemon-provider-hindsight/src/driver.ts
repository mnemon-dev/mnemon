import { descriptor } from './descriptor.ts'
import { randomUUID } from 'node:crypto'
import type { JsonValue } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemorySpaceAuthority } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type {
  EdgeType,
  Insight,
  MemoryBody as MemorySpace,
  MemoryGraphEdge,
  MemoryGraphNode,
  MemoryGraphSnapshot,
  MemoryListRequest,
  RememberRequest,
  SearchRequest,
} from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { HttpMemoryProvider, firstArray, jsonArray, jsonNumber, jsonObject, jsonString, type HttpProviderOptions } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { NORMALIZED_RELEVANCE_SCORE, type MemoryProviderAdapter, type ProviderBodyStatus as ProviderSpaceStatus, type ProviderMemorySpace, type ProviderSearchResult } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

function insight(value: unknown): Insight | undefined {
  const item = jsonObject(value)
  const id = jsonString(item?.id)
  const content = jsonString(item?.text) ?? jsonString(item?.content) ?? jsonString(item?.label)
  if (id === undefined || content === undefined) return undefined
  const scores = jsonObject(item?.scores)
  const score = jsonNumber(scores?.final) ?? jsonNumber(item?.score)
  const createdAt = jsonString(item?.mentioned_at) ?? jsonString(item?.date) ?? jsonString(item?.occurred_start)
  const rawEntities = item?.entities
  const entities = Array.isArray(rawEntities)
    ? rawEntities.filter((entry): entry is string => typeof entry === 'string')
    : typeof rawEntities === 'string'
      ? rawEntities.split(',').map(entry => entry.replace(/\s*\([^)]*\)\s*$/u, '').trim()).filter(Boolean)
      : []
  const tags = jsonArray(item?.tags).filter((entry): entry is string => typeof entry === 'string')
  return {
    id,
    content,
    category: jsonString(item?.type) ?? jsonString(item?.fact_type) ?? 'general',
    source: 'external',
    ...(score === undefined ? {} : { score }),
    ...(createdAt === undefined ? {} : { createdAt }),
    ...(entities.length === 0 ? {} : { entities }),
    ...(tags.length === 0 ? {} : { tags }),
  }
}

function edgeType(value: unknown): EdgeType | undefined {
  return value === 'temporal' || value === 'semantic' || value === 'causal' || value === 'entity' ? value : undefined
}

export class HindsightProvider extends HttpMemoryProvider implements MemoryProviderAdapter {
  readonly id = 'hindsight' as const
  readonly scoreSemantics = NORMALIZED_RELEVANCE_SCORE

  constructor(memorySpaces: MemorySpaceAuthority, options: HttpProviderOptions = {}) {
    super(memorySpaces, { label: descriptor.label, ...options })
  }

  async discover(connection: Record<string, string | number | boolean>, signal?: AbortSignal): Promise<ProviderMemorySpace[]> {
    const payload = await this.requestConnection(connection, '/v1/default/banks', { headers: this.headers(connection), signal })
    return firstArray(payload, 'banks', 'items').flatMap(value => {
      const item = jsonObject(value)
      const id = jsonString(item?.bank_id) ?? jsonString(item?.id)
      if (id === undefined) return []
      const description = jsonString(item?.mission)?.trim()
        || jsonString(item?.description)?.trim()
        || `Hindsight memory bank ${id}`
      return [{
        externalId: id,
        name: jsonString(item?.name) ?? id,
        description,
        connection: { bankId: id, budget: 'mid' },
      }]
    })
  }

  async status(body: MemorySpace, signal?: AbortSignal): Promise<ProviderSpaceStatus> {
    try {
      const connection = this.connection(body)
      await this.request(body, '/health/live', { headers: this.headers(connection), signal })
      try {
        const [statsPayload, entitiesPayload] = await Promise.all([
          this.request(body, `${this.bankPath(connection)}/stats`, { headers: this.headers(connection), signal }),
          this.request(body, `${this.bankPath(connection)}/entities?limit=100&offset=0`, { headers: this.headers(connection), signal }),
        ])
        const stats = jsonObject(statsPayload) ?? {}
        const byFactType = jsonObject(stats.nodes_by_fact_type) ?? {}
        const byCategory = Object.fromEntries(Object.entries(byFactType).flatMap(([category, count]) => {
          const value = jsonNumber(count)
          return value === undefined ? [] : [[category, value]]
        }))
        const operations = jsonObject(stats.operations_by_status) ?? {}
        const topEntities = firstArray(entitiesPayload, 'items').flatMap(value => {
          const item = jsonObject(value)
          const entity = jsonString(item?.canonical_name)
          const count = jsonNumber(item?.mention_count)
          return entity === undefined || count === undefined ? [] : [{ entity, count }]
        })
        return {
          healthy: true,
          stats: {
            totalInsights: jsonNumber(stats.total_nodes) ?? 0,
            deletedInsights: 0,
            edgeCount: jsonNumber(stats.total_links) ?? 0,
            oplogCount: Object.values(operations).reduce<number>((total, value) => total + (jsonNumber(value) ?? 0), 0),
            dbSizeBytes: 0,
            byCategory,
            topEntities,
          },
        }
      } catch {
        // Older Hindsight deployments may expose recall and graph APIs without
        // the newer statistics surfaces. Liveness remains sufficient there.
        return { healthy: true }
      }
    } catch (error) {
      return { healthy: false, error: error instanceof Error ? error.message : String(error) }
    }
  }

  async search(body: MemorySpace, request: SearchRequest, signal?: AbortSignal): Promise<ProviderSearchResult> {
    const connection = this.connection(body)
    const payload = await this.request(body, `${this.bankPath(connection)}/memories/recall`, {
      headers: this.headers(connection),
      json: {
        query: request.query,
        budget: String(connection.budget ?? 'mid'),
        max_tokens: Math.min(Math.max((request.limit ?? 10) * 400, 400), 8_000),
        types: ['world', 'experience', 'observation'],
        prefer_observations: true,
      },
      signal,
    })
    return { results: firstArray(payload, 'results', 'items').map(insight).filter((item): item is Insight => item !== undefined).slice(0, request.limit ?? 10) }
  }

  async list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]> {
    const connection = this.connection(body)
    const params = new URLSearchParams({ limit: String(Math.min(Math.max(request.limit ?? 200, 1), 1000)), offset: '0', state: 'valid' })
    if (request.query !== undefined && request.query.trim() !== '') params.set('q', request.query.trim())
    const payload = await this.request(body, `${this.bankPath(connection)}/memories/list?${params}`, { headers: this.headers(connection), signal })
    return firstArray(payload, 'items', 'results').map(insight).filter((item): item is Insight => item !== undefined)
      .filter(item => request.category === undefined || item.category === request.category)
  }

  async graph(body: MemorySpace, signal?: AbortSignal): Promise<MemoryGraphSnapshot> {
    const connection = this.connection(body)
    const payload = jsonObject(await this.request(body, `${this.bankPath(connection)}/graph?limit=1000`, { headers: this.headers(connection), signal })) ?? {}
    const nodes: MemoryGraphNode[] = jsonArray(payload.nodes).flatMap(value => {
      const item = jsonObject(value)
      const data = jsonObject(item?.data) ?? item
      const projected = insight(data)
      return projected === undefined ? [] : [{ ...projected, color: jsonString(data?.color) ?? '#6574d9' }]
    })
    const edges: MemoryGraphEdge[] = jsonArray(payload.edges).flatMap(value => {
      const item = jsonObject(value)
      const data = jsonObject(item?.data) ?? item
      const sourceId = jsonString(data?.from) ?? jsonString(data?.source)
      const targetId = jsonString(data?.to) ?? jsonString(data?.target)
      if (sourceId === undefined || targetId === undefined) return []
      const rawType = jsonString(data?.type) ?? jsonString(data?.linkType)
      const type = edgeType(rawType)
      return [{
        sourceId,
        targetId,
        label: rawType ?? 'related',
        color: type === 'causal' ? '#e74c3c' : type === 'entity' ? '#2ecc71' : type === 'temporal' ? '#aaaaaa' : '#3498db',
        ...(type === undefined ? {} : { type }),
      }]
    })
    return { nodes, edges, generatedAt: new Date().toISOString() }
  }

  async related(body: MemorySpace, id: string, depth: number, _edge?: EdgeType, signal?: AbortSignal): Promise<Insight[]> {
    const graph = await this.graph(body, signal)
    let frontier = new Set([id])
    const visited = new Set([id])
    for (let level = 0; level < depth; level += 1) {
      const next = new Set<string>()
      for (const edge of graph.edges) {
        if (frontier.has(edge.sourceId) && !visited.has(edge.targetId)) next.add(edge.targetId)
        if (frontier.has(edge.targetId) && !visited.has(edge.sourceId)) next.add(edge.sourceId)
      }
      for (const value of next) visited.add(value)
      frontier = next
    }
    return graph.nodes.filter(node => node.id !== id && visited.has(node.id)).map(({ color: _color, ...node }) => node)
  }

  async remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    const operationId = randomUUID()
    const payload = jsonObject(await this.request(body, `${this.bankPath(connection)}/memories`, {
      headers: this.headers(connection),
      json: {
        items: [{
          content: request.content,
          context: request.category ?? 'dsh-mnemon',
          metadata: { source: 'dsh-mnemon' },
          ...(request.tags === undefined ? {} : { tags: request.tags }),
          ...(request.entities === undefined ? {} : { entities: request.entities.map(text => ({ text })) }),
        }],
        async: true,
        operation_id: operationId,
      },
      signal,
    })) ?? {}
    return {
      action: 'queued',
      provider: this.id,
      summary: 'Hindsight queued the content for structured memory extraction.',
      operationId: jsonString(payload.operation_id) ?? operationId,
      ...(jsonNumber(payload.items_count) === undefined ? {} : { itemsCount: jsonNumber(payload.items_count)! }),
    }
  }

  async forget(body: MemorySpace, id: string, signal?: AbortSignal): Promise<JsonValue> {
    const connection = this.connection(body)
    await this.request(body, `${this.bankPath(connection)}/memories/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: this.headers(connection),
      json: { state: 'invalidated', reason: 'Forgotten from dsh-mnemon' },
      signal,
    })
    return { action: 'invalidated', provider: this.id, id }
  }

  private bankPath(connection: Record<string, string | number | boolean>): string {
    return `/v1/default/banks/${encodeURIComponent(String(connection.bankId))}`
  }

  private headers(connection: Record<string, string | number | boolean>): HeadersInit {
    const token = String(connection.apiKey ?? '').replace(/^Bearer\s+/iu, '')
    return token === '' ? {} : { Authorization: `Bearer ${token}` }
  }
}
