import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { NORMALIZED_RELEVANCE_SCORE, type MemorySpaceNativeRunner, type MemoryProviderAdapter, type JsonValue, type Insight, type MemoryBody as MemorySpace, type MemoryBodyStats as MemorySpaceStats, type MemoryGraphEdge, type MemoryGraphNode, type MemoryGraphSnapshot, type MemoryListRequest, type EdgeType, type RememberRequest, type SearchRequest, type ProviderBodyStatus as ProviderSpaceStatus, type ProviderSearchResult } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

/**
 * Output cap for the two whole-Store reads: the full readonly recall and the
 * HTML graph. Their size grows with the Store, not with a misbehaving command,
 * and a Store of about 1,000 insights already produces 3–9 MB (issue #320).
 * Every other command keeps the Source's 2 MiB default.
 */
export const STORE_DUMP_MAX_OUTPUT_BYTES = 128 * 1024 * 1024

function record(value: JsonValue | undefined): Record<string, JsonValue> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? value as Record<string, JsonValue>
    : undefined
}

function text(value: JsonValue | undefined): string | undefined {
  return typeof value === 'string' ? value : undefined
}

function number(value: JsonValue | undefined): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function stringArray(value: JsonValue | undefined): string[] | undefined {
  if (!Array.isArray(value)) return undefined
  return value.filter((entry): entry is string => typeof entry === 'string')
}

function normalizeInsight(value: JsonValue): Insight | undefined {
  const item = record(value)
  if (item === undefined) return undefined
  // Mnemon <=0.1.2 returns full recall rows as
  // `{ insight: { id, content, ... }, score, intent, via, signals }`; newer
  // builds default to a compact flat row. Accept both without version gates.
  const nested = record(item.insight)
  const core = nested ?? item
  const id = text(core.id)
  const content = text(core.content)
  if (id === undefined || content === undefined) return undefined
  const insight: Insight = { id, content }
  const optionalText = {
    category: text(core.category),
    source: text(core.source),
    confidence: text(item.confidence),
    intent: text(item.intent),
    matchedVia: text(item.matched_via ?? item.via ?? item.via_edge_type),
    createdAt: text(core.created_at),
    edgeType: text(item.via_edge_type),
  }
  for (const [key, value] of Object.entries(optionalText)) if (value !== undefined) Object.assign(insight, { [key]: value })
  const optionalNumbers = {
    importance: number(core.importance),
    score: number(item.score),
    depth: number(item.depth),
  }
  for (const [key, value] of Object.entries(optionalNumbers)) if (value !== undefined) Object.assign(insight, { [key]: value })
  const tags = stringArray(core.tags)
  const entities = stringArray(core.entities)
  if (tags !== undefined) insight.tags = tags
  if (entities !== undefined) insight.entities = entities
  return insight
}

const JS_STRING = '"(?:\\\\.|[^"\\\\])*"'
const VIZ_NODE_PATTERN = new RegExp(`\\{id:(${JS_STRING}),label:(${JS_STRING}),title:(${JS_STRING}),color:(${JS_STRING}),font:\\{color:"white"\\}\\}`, 'g')
const VIZ_EDGE_PATTERN = new RegExp(`\\{from:(${JS_STRING}),to:(${JS_STRING}),label:(${JS_STRING}),color:\\{color:(${JS_STRING})\\},arrows:"to"`, 'g')
const EDGE_COLORS: Record<string, EdgeType> = {
  '#aaaaaa': 'temporal',
  '#3498db': 'semantic',
  '#e74c3c': 'causal',
  '#2ecc71': 'entity',
}

function decodeJsString(value: string): string {
  const decoded = JSON.parse(value) as unknown
  if (typeof decoded !== 'string') throw new Error('Mnemon viz contained an invalid string')
  return decoded
}

/** Parse the official Mnemon vis.js export without executing its HTML or loading its CDN script. */
export function parseMemoryGraph(html: string, now = new Date()): MemoryGraphSnapshot {
  const nodes: MemoryGraphNode[] = []
  const edges: MemoryGraphEdge[] = []
  for (const match of html.matchAll(VIZ_NODE_PATTERN)) {
    const id = decodeJsString(match[1]!)
    const label = decodeJsString(match[2]!)
    const content = decodeJsString(match[3]!).replaceAll('\\n', '\n')
    const color = decodeJsString(match[4]!)
    const category = /\[([a-z_]+)\]/i.exec(label)?.[1] ?? 'general'
    nodes.push({ id, content, category, color })
  }
  for (const match of html.matchAll(VIZ_EDGE_PATTERN)) {
    const color = decodeJsString(match[4]!)
    const type = EDGE_COLORS[color.toLowerCase()]
    edges.push({
      sourceId: decodeJsString(match[1]!),
      targetId: decodeJsString(match[2]!),
      label: decodeJsString(match[3]!),
      color,
      ...(type === undefined ? {} : { type }),
    })
  }
  if (!html.includes('var nodes = new vis.DataSet([')) throw new Error('Mnemon viz returned an unexpected HTML payload')
  return { nodes, edges, generatedAt: now.toISOString() }
}

function commaList(values: string[] | undefined, label: string, limit: number): string | undefined {
  if (values === undefined) return undefined
  const normalized = values.map(value => value.trim()).filter(value => value !== '')
  if (normalized.length > limit) throw new Error(`${label} accepts at most ${limit} values`)
  if (normalized.some(value => value.includes(','))) throw new Error(`${label} values cannot contain commas`)
  return normalized.length === 0 ? undefined : normalized.join(',')
}


/** Native CLI data plane owned and independently testable by this Provider. */
export class MnemonNativeProvider implements MemoryProviderAdapter {
  readonly id = 'mnemon-native'
  readonly scoreSemantics = NORMALIZED_RELEVANCE_SCORE
  constructor(private readonly runner: MemorySpaceNativeRunner, private readonly config: { defaultRecallLimit: number } = { defaultRecallLimit: 10 }) {}

  list(body: MemorySpace, _request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]> {
    return this.allNativeInsights(body, signal, true)
  }

  async status(body: MemorySpace, signal?: AbortSignal): Promise<ProviderSpaceStatus> {
    try {
      const raw = await this.runner.runJson(['status'], { ...(signal === undefined ? {} : { signal }), store: body.id })
      const status = record(raw)
      if (status === undefined) throw new Error('mnemon status returned an unexpected payload')
      return { healthy: true, stats: this.parseStats(status) }
    } catch (error) {
      return { healthy: false, error: error instanceof Error ? error.message : String(error) }
    }
  }

  private parseStats(status: Record<string, JsonValue>): MemorySpaceStats {
    const byCategoryRecord = record(status.by_category) ?? {}
    const byCategory: Record<string, number> = {}
    for (const [category, count] of Object.entries(byCategoryRecord)) if (typeof count === 'number') byCategory[category] = count
    const topEntities = Array.isArray(status.top_entities)
      ? status.top_entities.flatMap((entry) => {
          const entity = record(entry)
          const name = text(entity?.entity)
          const count = number(entity?.count)
          return name === undefined || count === undefined ? [] : [{ entity: name, count }]
        })
      : []
    return {
      totalInsights: number(status.total_insights) ?? 0,
      deletedInsights: number(status.deleted_insights) ?? 0,
      edgeCount: number(status.edge_count) ?? 0,
      oplogCount: number(status.oplog_count) ?? 0,
      dbSizeBytes: number(status.db_size_bytes) ?? 0,
      byCategory,
      topEntities,
    }
  }

  async graph(body: MemorySpace, signal?: AbortSignal): Promise<MemoryGraphSnapshot> {
    const [html, insights] = await Promise.all([
      this.runner.runText(['viz', '--format', 'html', '--output', '-'], {
        ...(signal === undefined ? {} : { signal }), store: body.id, maxOutputBytes: STORE_DUMP_MAX_OUTPUT_BYTES,
      }),
      // Mnemon's HTML visualization omits tags and entities. A readonly recall
      // supplies that metadata without incrementing access counters.
      this.allNativeInsights(body, signal, true),
    ])
    const snapshot = parseMemoryGraph(html)
    const metadata = new Map(insights.map(insight => [insight.id, insight]))
    return {
      ...snapshot,
      nodes: snapshot.nodes.map(node => {
        const insight = metadata.get(node.id)
        return insight === undefined
          ? node
          : { ...node, ...insight, id: node.id, content: node.content, color: node.color }
      }),
    }
  }

  private async allNativeInsights(body: MemorySpace, signal?: AbortSignal, readonly = false): Promise<Insight[]> {
    const payload = await this.runner.runJson([
      ...(readonly ? ['--readonly'] : []),
      'recall', '', '--basic', '--limit', '100000',
    ], { ...(signal === undefined ? {} : { signal }), store: body.id, maxOutputBytes: STORE_DUMP_MAX_OUTPUT_BYTES })
    const values = Array.isArray(payload) ? payload : Array.isArray(record(payload)?.results) ? record(payload)!.results as JsonValue[] : []
    return values.map(normalizeInsight).filter((entry): entry is Insight => entry !== undefined)
  }

  async metadataSample(body: MemorySpace, limit: number, signal?: AbortSignal): Promise<Insight[]> {
    const payload = await this.runner.runJson([
      '--readonly',
      'recall', '', '--basic', '--limit', String(limit),
    ], { ...(signal === undefined ? {} : { signal }), store: body.id })
    const wrapper = record(payload)
    const values = Array.isArray(payload) ? payload : Array.isArray(wrapper?.results) ? wrapper.results : []
    return values.map(normalizeInsight).filter((entry): entry is Insight => entry !== undefined)
  }

  async search(body: MemorySpace, request: SearchRequest, signal?: AbortSignal): Promise<ProviderSearchResult> {
    const mode = request.mode ?? 'smart'
    const args = mode === 'keyword'
      ? ['search', request.query, '--limit', String(request.limit ?? this.config.defaultRecallLimit)]
      : ['recall', request.query, '--limit', String(request.limit ?? this.config.defaultRecallLimit)]
    if (mode === 'basic') args.push('--basic')
    if (mode !== 'keyword') {
      if (request.category !== undefined) args.push('--cat', request.category)
      if (request.source !== undefined) args.push('--source', request.source)
      if (request.intent !== undefined) args.push('--intent', request.intent)
    }
    const payload = await this.runner.runJson(args, { ...(signal === undefined ? {} : { signal }), store: body.id })
    const wrapper = record(payload)
    const values = Array.isArray(payload) ? payload : Array.isArray(wrapper?.results) ? wrapper.results : []
    const hint = text(wrapper?.hint)
    return {
      results: values.map(normalizeInsight).filter((entry): entry is Insight => entry !== undefined),
      ...(hint === undefined ? {} : { hint }),
    }
  }

  async remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    const args = ['remember', request.content, '--cat', request.category ?? 'general', '--imp', String(request.importance ?? 3), '--source', request.source ?? 'user']
    const tags = commaList(request.tags, 'tags', 20)
    const entities = commaList(request.entities, 'entities', 50)
    if (tags !== undefined) args.push('--tags', tags)
    if (entities !== undefined) args.push('--entities', entities)
    return this.runner.runJson(args, { ...(signal === undefined ? {} : { signal }), store: body.id })
  }

  async rememberMany(body: MemorySpace, requests: readonly RememberRequest[], signal?: AbortSignal): Promise<JsonValue[]> {
    if (requests.length === 0) return []
    // Bulk archival preserves exact committed facts. Native's normal semantic
    // deduplication can skip a distinct configuration or correction that merely
    // resembles an earlier entry. Reuse only byte-identical persisted content,
    // and import the remaining originals without semantic replacement.
    const existing = new Map((await this.allNativeInsights(body, signal, true)).map(entry => [entry.content, entry]))
    const ordered = new Array<JsonValue>(requests.length)
    const pending = new Map<string, { request: RememberRequest; indexes: number[] }>()
    for (const [index, request] of requests.entries()) {
      const exact = existing.get(request.content)
      if (exact !== undefined) ordered[index] = { action: 'skipped', id: exact.id, content: exact.content }
      else {
        const group = pending.get(request.content)
        if (group === undefined) pending.set(request.content, { request, indexes: [index] })
        else group.indexes.push(index)
      }
    }
    const batch = [...pending.values()]
    if (batch.length === 0) return ordered
    const temporary = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runtime-archive-'))
    const draftPath = join(temporary, 'memory-draft.json')
    try {
      writeFileSync(draftPath, JSON.stringify({
        schema_version: '1',
        source: 'dsh-mnemon-runtime-archive',
        insights: batch.map(({ request }) => ({
          content: request.content,
          category: request.category,
          importance: request.importance,
          source: request.source,
          ...(request.tags === undefined ? {} : { tags: request.tags }),
          ...(request.entities === undefined ? {} : { entities: request.entities }),
        })),
      }), { encoding: 'utf8', mode: 0o600 })
      const payload = await this.runner.runJson(['import', draftPath, '--no-diff'], { ...(signal === undefined ? {} : { signal }), store: body.id })
      const summary = record(payload)
      const rows = Array.isArray(summary?.results) ? summary.results : undefined
      const errors = number(summary?.errors)
      const imported = number(summary?.imported)
      const updated = number(summary?.updated)
      const skipped = number(summary?.skipped)
      const invalid = () => new Error(`Mnemon runtime archive import returned an invalid or partial result for Memory Space ${body.id}`)
      if (errors !== 0 || imported === undefined || updated === undefined || skipped === undefined || rows === undefined
        || ![imported, updated, skipped].every(value => Number.isInteger(value) && value >= 0)
        || imported !== batch.length || updated !== 0 || skipped !== 0 || rows.length !== batch.length) {
        throw invalid()
      }
      const seen = new Set<number>()
      for (const candidate of rows) {
        const row = record(candidate)
        const index = number(row?.index)
        const action = text(row?.action)?.trim().toLocaleLowerCase()
        if (index === undefined || !Number.isInteger(index) || index < 0 || index >= batch.length || seen.has(index)) {
          throw invalid()
        }
        if (row?.content !== batch[index]!.request.content || action !== 'added') {
          throw invalid()
        }
        seen.add(index)
        for (const [offset, originalIndex] of batch[index]!.indexes.entries()) {
          ordered[originalIndex] = offset === 0 ? row : { ...row, action: 'skipped' }
        }
      }
      return ordered
    } finally {
      rmSync(temporary, { recursive: true, force: true })
    }
  }

  async related(body: MemorySpace, id: string, depth: number, edge?: EdgeType, signal?: AbortSignal): Promise<Insight[]> {
    const args = ['related', id, '--depth', String(depth)]
    if (edge !== undefined) args.push('--edge', edge)
    const payload = await this.runner.runJson(args, { ...(signal === undefined ? {} : { signal }), store: body.id })
    return Array.isArray(payload) ? payload.map(normalizeInsight).filter((entry): entry is Insight => entry !== undefined) : []
  }

  async link(body: MemorySpace, sourceId: string, targetId: string, type: EdgeType, weight: number, reason?: string, signal?: AbortSignal): Promise<JsonValue> {
    const args = ['link', sourceId, targetId, '--type', type, '--weight', String(weight)]
    if (reason !== undefined) args.push('--meta', JSON.stringify({ reason }))
    return this.runner.runJson(args, { ...(signal === undefined ? {} : { signal }), store: body.id })
  }

  forget(body: MemorySpace, id: string, signal?: AbortSignal): Promise<JsonValue> {
    return this.runner.runJson(['forget', id], { ...(signal === undefined ? {} : { signal }), store: body.id })
  }

}
