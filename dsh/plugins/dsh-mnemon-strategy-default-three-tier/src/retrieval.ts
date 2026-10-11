import { createHash, randomUUID } from 'node:crypto'
import { memoryInputRecord as object, truncateMemoryText as clip, type ComposableMemoryView, type MemoryEvidence, type MemoryStrategyRead, type MemoryStrategyReadRequest, type MemoryStrategyTurn } from 'dsh-mnemon/extension-sdk'
import type { MemoryJsonValue } from 'dsh-mnemon/contracts'

/** The three-tier public read shape. Other strategies are free to use other Source protocols. */
interface RecallInsight {
  id: string; content: string; score?: number; revision?: string
  category?: string; importance?: number; tags?: string[]; entities?: string[]; source?: string; createdAt?: string
  depth?: number; edgeType?: string; memoryBodyId?: string; memoryBodyName?: string
  memoryProviderId?: string; memoryCapabilities?: MemoryJsonValue; externalUri?: string
  relevanceTier?: 'high' | 'medium' | 'low' | 'unknown'
}
type Insight = RecallInsight
interface SearchRequest { query: string; mode?: string; limit?: number; memoryBodyIds?: string[] }
interface RecallResult {
  query: string; mode: string; results: RecallInsight[]; hint?: string
  memoryEvidence?: Omit<MemoryEvidence, 'items'>
}
interface RecallAuthority { viewId: string; sourceInstanceKey: string; memoryBodyIds: string[] }
interface RecallAttempt { queryDigest: string; result?: RecallResult; pending?: Promise<RecallResult> }
interface TurnRetrievalState {
  documentSearchClaimed?: boolean; recallAttempts: RecallAttempt[]
  relatedDigest?: string; relatedResult?: RecallResult; relatedPending?: Promise<RecallResult>
  evidenceDigests: Set<string>; evidenceReferences: Set<string>
}
function optionalObject(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}
const sha256 = (text: string) => createHash('sha256').update(text).digest('hex')
const json = (value: unknown): MemoryJsonValue => JSON.parse(JSON.stringify(value)) as MemoryJsonValue
function scopeRecall(request: SearchRequest, authority: RecallAuthority): SearchRequest {
  const requested = [...new Set((request.memoryBodyIds ?? []).map(id => id.trim()).filter(Boolean))]
  if (requested.some(id => !authority.memoryBodyIds.includes(id))) throw new Error('Recall requested a Memory Space outside pinned Source ' + authority.viewId)
  return { ...request, memoryBodyIds: requested.length === 0 ? [...authority.memoryBodyIds] : requested }
}
function scopeRelated(id: string | undefined, authority: RecallAuthority): string {
  const requested = id?.trim()
  if (!requested) {
    if (authority.memoryBodyIds.length === 1) return authority.memoryBodyIds[0]!
    throw new Error('related memory requires one Memory Space from pinned Source ' + authority.viewId)
  }
  if (!authority.memoryBodyIds.includes(requested)) throw new Error('related memory requested a Memory Space outside pinned Source ' + authority.viewId)
  return requested
}

function evidenceInsights(evidence: MemoryEvidence): RecallInsight[] {
  return evidence.items.map(item => {
    const metadata = optionalObject(item.provenance) ?? {}
    return { ...metadata, id: item.id, content: item.text, score: item.score, revision: item.revision } as RecallInsight
  })
}

/** Preserve the original read identity and truncation when the Strategy admits or replays evidence. */
function recallEvidence(evidence: MemoryEvidence, results: readonly RecallInsight[]): NonNullable<RecallResult['memoryEvidence']> {
  const { items, ...metadata } = evidence
  return {
    ...metadata,
    truncated: metadata.truncated || results.length < items.length || results.some(result => (
      !items.some(item => item.id === result.id && item.text === result.content)
    )),
  }
}

const MODEL_RECALL_RESULT_LIMIT = 6
const MODEL_RECALL_CONTENT_LIMIT = 1_200
const MODEL_RECALL_TOTAL_CONTENT_LIMIT = 4_800
const MODEL_RECALL_ATTEMPT_LIMIT = 2
const MODEL_RECALL_INITIAL_RESULT_LIMIT = 4
const MODEL_RECALL_INITIAL_TOTAL_CONTENT_LIMIT = 3_600
const MODEL_RECALL_MEDIUM_LIMIT_PER_ATTEMPT = 1
const MODEL_RECALL_UNKNOWN_LIMIT_PER_ATTEMPT = 1
const MODEL_RECALL_LIST_LIMIT = 8
const MODEL_RECALL_METADATA_LIMIT = 300

interface ModelInsightAdmission {
  resultLimit?: number
  contentLimit?: number
  totalContentLimit?: number
  mediumLimit?: number
  unknownLimit?: number
  excludeDigests?: ReadonlySet<string>
}

interface ModelInsightEnvelope {
  results: RecallInsight[]
}

function insightDigest(result: Pick<Insight, 'content'>): string {
  return sha256(result.content.trim().replace(/\s+/gu, ' '))
}

function recallQueryDigest(request: SearchRequest, sourceInstanceKey: string): string {
  const lexical = (request.query.match(/[\p{L}\p{N}]+/gu) ?? []).join(' ').toLocaleLowerCase()
  return sha256(JSON.stringify({ sourceInstanceKey, query: lexical, memoryBodyIds: [...request.memoryBodyIds ?? []].sort() }))
}

/** A replay must still respect the current call's selected Source subset. */
function replayEvidence(previous: RecallResult | undefined, memoryBodyIds: readonly string[] | undefined, sourceInstanceKey: string): Pick<RecallResult, 'results' | 'memoryEvidence'> {
  if (previous?.memoryEvidence?.sourceInstanceKey !== sourceInstanceKey) return { results: [] }
  const original = previous?.results ?? []
  const results = structuredClone(memoryBodyIds === undefined ? original : original.filter(result => (
    result.memoryBodyId !== undefined && memoryBodyIds.includes(result.memoryBodyId)
  ))) as RecallInsight[]
  return { results, ...(previous?.memoryEvidence === undefined ? {} : { memoryEvidence: {
    ...structuredClone(previous.memoryEvidence),
    truncated: previous.memoryEvidence.truncated || results.length < original.length,
  } }) }
}

/** Admit a small, deduplicated evidence envelope after Provider quality policy. */
function boundedModelInsights(results: readonly RecallInsight[], admission: ModelInsightAdmission = {}): ModelInsightEnvelope {
  const resultLimit = admission.resultLimit ?? MODEL_RECALL_RESULT_LIMIT
  const contentLimit = admission.contentLimit ?? MODEL_RECALL_CONTENT_LIMIT
  const totalContentLimit = admission.totalContentLimit ?? MODEL_RECALL_TOTAL_CONTENT_LIMIT
  const mediumLimit = admission.mediumLimit ?? MODEL_RECALL_MEDIUM_LIMIT_PER_ATTEMPT
  const unknownLimit = admission.unknownLimit ?? MODEL_RECALL_UNKNOWN_LIMIT_PER_ATTEMPT
  const seenReferences = new Set<string>()
  const seenDigests = new Set<string>(admission.excludeDigests)
  let mediumCount = 0
  let unknownCount = 0
  let contentCharacters = 0
  const admitted: RecallInsight[] = []
  for (const result of results) {
    if (admitted.length >= resultLimit || result.id.length === 0 || result.id.length > 1_000) continue
    const tier = result.relevanceTier ?? 'unknown'
    if (tier === 'low') continue
    if (tier === 'medium' && mediumCount >= mediumLimit) continue
    if (tier === 'unknown' && unknownCount >= unknownLimit) continue
    const reference = `${result.memoryBodyId ?? ''}/${result.id}`
    const digest = insightDigest(result)
    if (seenReferences.has(reference) || seenDigests.has(digest)) continue
    const remainingContent = totalContentLimit - contentCharacters
    if (remainingContent <= 0) break
    const content = clip(result.content, Math.min(contentLimit, remainingContent))
    if (content === '') continue
    seenReferences.add(reference)
    seenDigests.add(digest)
    contentCharacters += content.length
    if (tier === 'medium') mediumCount += 1
    if (tier === 'unknown') unknownCount += 1
    admitted.push({
      id: result.id,
      content,
      ...(result.category === undefined ? {} : { category: clip(result.category, MODEL_RECALL_METADATA_LIMIT) }),
      ...(typeof result.importance !== 'number' || !Number.isFinite(result.importance) ? {} : { importance: result.importance }),
      ...(result.tags === undefined ? {} : { tags: result.tags.slice(0, MODEL_RECALL_LIST_LIMIT).map(tag => clip(tag, MODEL_RECALL_METADATA_LIMIT)) }),
      ...(result.entities === undefined ? {} : { entities: result.entities.slice(0, MODEL_RECALL_LIST_LIMIT).map(entity => clip(entity, MODEL_RECALL_METADATA_LIMIT)) }),
      ...(result.source === undefined ? {} : { source: clip(result.source, MODEL_RECALL_METADATA_LIMIT) }),
      ...(result.createdAt === undefined ? {} : { createdAt: clip(result.createdAt, MODEL_RECALL_METADATA_LIMIT) }),
      ...(typeof result.depth !== 'number' || !Number.isFinite(result.depth) ? {} : { depth: result.depth }),
      ...(result.edgeType === undefined ? {} : { edgeType: clip(result.edgeType, MODEL_RECALL_METADATA_LIMIT) }),
      ...(result.memoryBodyId === undefined ? {} : { memoryBodyId: clip(result.memoryBodyId, 1_000) }),
      ...(result.memoryBodyName === undefined ? {} : { memoryBodyName: clip(result.memoryBodyName, MODEL_RECALL_METADATA_LIMIT) }),
      ...(result.memoryProviderId === undefined ? {} : { memoryProviderId: result.memoryProviderId }),
      ...(result.memoryCapabilities === undefined ? {} : { memoryCapabilities: structuredClone(result.memoryCapabilities) }),
      ...(result.externalUri === undefined ? {} : { externalUri: clip(result.externalUri, 2_000) }),
    })
  }
  return { results: admitted }
}


class ThreeTierTurn implements MemoryStrategyTurn {
  private readonly state: TurnRetrievalState = { recallAttempts: [], evidenceDigests: new Set(), evidenceReferences: new Set() }
  constructor(private readonly view: ComposableMemoryView) {}
  async query(request: MemoryStrategyReadRequest, read: MemoryStrategyRead): Promise<MemoryEvidence> {
    const grant = this.view.readGrants.find(grant => grant.id === request.route.readGrantId && grant.sourceInstanceKey === request.route.sourceInstanceKey)!
    const input = object(request.input, 'three-tier read')
    if (grant.schema === 'dsh-mnemon.documents/v1' && request.route.sourceRouteId === 'search') return this.documents(request, read)
    if (grant.schema !== 'dsh-mnemon.memory-spaces/v1' || !['recall', 'related', 'inspect'].includes(request.route.sourceRouteId)) return read(request.input)
    if (request.route.sourceRouteId === 'inspect') {
      const evidence = await read(request.input)
      // Catalog is already bounded and sanitized by its Source. Never split JSON.
      return evidence.items.length === 1 ? { ...evidence, output: JSON.parse(evidence.items[0]!.text) as MemoryJsonValue } : evidence
    }
    const value = object(grant.value, 'Memory Spaces grant')
    if (!Array.isArray(value.memoryBodyIds) || value.memoryBodyIds.some(id => typeof id !== 'string')) throw new Error('Invalid Memory Spaces read scope')
    const authority = { viewId: this.view.id, sourceInstanceKey: request.route.sourceInstanceKey, memoryBodyIds: [...new Set(value.memoryBodyIds as string[])] }
    const signal = request.signal ?? new AbortController().signal
    const result = request.route.sourceRouteId === 'recall'
      ? await this.recall(input as unknown as SearchRequest, read, signal, authority)
      : await this.related(input.id as string, input.memoryBodyId as string | undefined, read, signal, authority, input)
    const { memoryEvidence, ...output } = result
    const items = result.results.map(({ id, content, score, ...provenance }) => ({
      id, text: content, provenance: json(provenance), ...(score === undefined ? {} : { score }),
    }))
    return { ...this.empty(request), ...memoryEvidence, items,
      output: json({ ...output, ...(memoryEvidence?.unavailable === undefined ? {} : { unavailable: memoryEvidence.unavailable }) }),
    }
  }
  private empty(request: MemoryStrategyReadRequest): MemoryEvidence {
    return { id: 'evidence:' + randomUUID(), viewId: this.view.id, routeId: request.route.id,
      sourceInstanceKey: request.route.sourceInstanceKey, observedAt: new Date().toISOString(), items: [], truncated: false }
  }
  private async documents(request: MemoryStrategyReadRequest, read: MemoryStrategyRead): Promise<MemoryEvidence> {
    const input = object(request.input, 'Documents search')
    const query = String(input.query).trim()
    const includeArchived = input.includeArchived === true
    if (this.state.documentSearchClaimed) return { ...this.empty(request), output: {
      query, includeArchived, notRun: true, results: [],
      hint: 'This Agent turn already used its Documents search slot, so no second disk query ran. Use the admitted evidence, make one focused mnemon_recall only if exact durable history is still missing, or answer with appropriate uncertainty.',
    } }
    this.state.documentSearchClaimed = true
    const evidence = await read({ query, includeArchived, limit: Math.min(4, typeof input.limit === 'number' ? Math.max(1, input.limit) : 4) },
      { maxResults: 4, maxCharacters: 6_000 })
    const metadata = optionalObject(evidence.metadata) ?? {}
    const results = evidence.items.filter(item => optionalObject(item.provenance)?.kind !== 'suggestion').map(item => {
      const record = optionalObject(item.provenance) ?? {}
      return { id: item.id, title: clip(String(record.title ?? ''), 200), description: clip(String(record.description ?? ''), 500),
        status: record.status, relativePath: clip(String(record.relativePath ?? ''), 500),
        sourcePaths: Array.isArray(record.sourcePaths) ? record.sourcePaths.slice(0, 8).map(path => clip(String(path), 500)) : [],
        score: item.score, content: item.text }
    })
    const suggestions = evidence.items.filter(item => optionalObject(item.provenance)?.kind === 'suggestion').map(item => {
      const record = optionalObject(item.provenance) ?? {}
      return { id: item.id, title: record.title, description: record.description, status: record.status, excerpt: item.text }
    })
    return { ...evidence, output: json({ query, includeArchived, total: metadata.total ?? results.length, results,
      hint: 'Document evidence is bounded. Use it, then one focused mnemon_recall only if exact durable history is still missing; do not repeat Document search this turn.',
      ...(suggestions.length === 0 ? {} : { suggestions, suggestionHint: 'No exact match. Use one focused mnemon_recall rather than repeating Document search.' }),
      ...(evidence.unavailable === undefined ? {} : { unavailable: evidence.unavailable }),
    }) }
  }
  async recall(request: SearchRequest, read: MemoryStrategyRead, signal: AbortSignal, authority: RecallAuthority): Promise<RecallResult> {
    signal.throwIfAborted()
    // Model-selected semantic filters are too brittle to be authoritative:
    // one wrong category can hide the exact evidence. The query and pinned
    // Source remain the complete model-facing routing contract.
    const limited: SearchRequest = {
      query: request.query,
      ...(request.mode === undefined ? {} : { mode: request.mode }),
      limit: Math.min(request.limit ?? MODEL_RECALL_RESULT_LIMIT, MODEL_RECALL_RESULT_LIMIT),
      ...(request.memoryBodyIds === undefined ? {} : { memoryBodyIds: request.memoryBodyIds }),
    }
    const scoped = scopeRecall(limited, authority)
    const digest = recallQueryDigest(scoped, authority.sourceInstanceKey)
    const retrieval = this.state
    const repeated = retrieval.recallAttempts.find(attempt => attempt.queryDigest === digest)
    if (repeated !== undefined) {
      const previous = repeated.result ?? await repeated.pending
      return {
        query: previous?.query ?? scoped.query,
        mode: previous?.mode ?? scoped.mode ?? 'smart',
        ...replayEvidence(previous, scoped.memoryBodyIds, authority.sourceInstanceKey),
        hint: retrieval.recallAttempts.length === MODEL_RECALL_ATTEMPT_LIMIT
          ? 'This Recall query already ran. The Host replayed its admitted evidence without another Provider query. The turn Recall budget is closed; stop retrieval and answer from the evidence or state what remains unknown.'
          : 'This Recall query already ran. The Host replayed its admitted evidence without another Provider query. If this evidence is insufficient, use at most one materially different focused query; otherwise stop retrieval.',
      }
    }
    if (retrieval.recallAttempts.length >= MODEL_RECALL_ATTEMPT_LIMIT) {
      const latest = retrieval.recallAttempts[retrieval.recallAttempts.length - 1]
      const last = latest?.result ?? await latest?.pending
      const previous = last?.memoryEvidence?.sourceInstanceKey === authority.sourceInstanceKey ? last : undefined
      return {
        query: previous?.query ?? scoped.query,
        mode: previous?.mode ?? scoped.mode ?? 'smart',
        ...replayEvidence(previous, scoped.memoryBodyIds, authority.sourceInstanceKey),
        hint: 'The two-query turn Recall budget is exhausted. The Host replayed the latest admitted evidence without another Provider query; stop retrieval and answer from the evidence or state what remains unknown.',
      }
    }
    const attemptIndex = retrieval.recallAttempts.length
    const predecessor = attemptIndex === 0 ? undefined : retrieval.recallAttempts[attemptIndex - 1]?.pending
    const attempt: RecallAttempt = { queryDigest: digest }
    retrieval.recallAttempts.push(attempt)
    const operation = (async (): Promise<RecallResult> => {
      // Distinct concurrent tool calls are serialized so the refinement can
      // exclude evidence admitted by the initial query and share one envelope.
      if (predecessor !== undefined) await predecessor
      signal.throwIfAborted()
      const evidence = await read(json(scoped))
      const result = { query: scoped.query, mode: scoped.mode ?? 'smart', results: evidenceInsights(evidence) }
      const priorAttempts = retrieval.recallAttempts.slice(0, attemptIndex)
      const priorResults = priorAttempts.flatMap(entry => entry.result?.results ?? [])
      const priorContentCharacters = priorResults.reduce((total, insight) => total + insight.content.length, 0)
      const requestedLimit = Math.min(limited.limit ?? MODEL_RECALL_RESULT_LIMIT, MODEL_RECALL_RESULT_LIMIT)
      const envelope = boundedModelInsights(result.results, {
        resultLimit: Math.min(requestedLimit, attemptIndex === 0
          ? MODEL_RECALL_INITIAL_RESULT_LIMIT
          : Math.max(0, MODEL_RECALL_RESULT_LIMIT - priorResults.length)),
        totalContentLimit: attemptIndex === 0
          ? MODEL_RECALL_INITIAL_TOTAL_CONTENT_LIMIT
          : Math.max(0, MODEL_RECALL_TOTAL_CONTENT_LIMIT - priorContentCharacters),
        // A materially different query must be able to contribute one
        // bounded medium/unknown clue even when the first attempt used
        // its own slot. The shared result and character limits still cap
        // the complete turn envelope.
        mediumLimit: MODEL_RECALL_MEDIUM_LIMIT_PER_ATTEMPT,
        unknownLimit: MODEL_RECALL_UNKNOWN_LIMIT_PER_ATTEMPT,
        excludeDigests: retrieval.evidenceDigests,
      })
      const results = envelope.results
      const response: RecallResult = {
        query: result.query,
        mode: result.mode,
        results,
        memoryEvidence: recallEvidence(evidence, results),
        hint: attemptIndex === 0
          ? results.length === 0
            ? 'No durable evidence was admitted. If exact history is still required, you may make one materially different focused Recall query; otherwise stop and answer with appropriate uncertainty.'
            : 'Answer from this admitted evidence. Only if it is insufficient for the current question may you make one materially different focused Recall query; otherwise stop retrieval. Use Related only when graph context is materially required.'
          : results.length === 0
            ? 'Recall refinement admitted no new durable evidence. The turn Recall budget is closed; stop retrieval and answer with appropriate uncertainty.'
            : 'Recall refinement is complete. The turn Recall budget is closed; stop retrieval and answer from the admitted evidence. Use Related only when graph context is materially required.',
      }
      attempt.result = structuredClone(response)
      for (const insight of results) {
        retrieval.evidenceDigests.add(insightDigest(insight))
        retrieval.evidenceReferences.add(`${authority.sourceInstanceKey}/${insight.memoryBodyId ?? ''}/${insight.id}`)
      }
      return response
    })()
    attempt.pending = operation
    try {
      return await operation
    } catch (error) {
      if (attempt.result === undefined) {
        const index = retrieval.recallAttempts.indexOf(attempt)
        if (index >= 0) retrieval.recallAttempts.splice(index, 1)
      }
      throw error
    } finally {
      if (attempt.pending === operation) delete attempt.pending
    }
  }

  async related(id: string, memoryBodyId: string | undefined, read: MemoryStrategyRead, signal: AbortSignal, authority: RecallAuthority, options: { depth?: number; edge?: string }): Promise<RecallResult> {
    signal.throwIfAborted()
    const selected = scopeRelated(memoryBodyId, authority)
    const retrieval = this.state
    const reference = `${authority.sourceInstanceKey}/${selected}/${id}`
    if (!retrieval.evidenceReferences.has(reference)) {
      return {
        query: `related:${id}`,
        mode: 'related',
        results: [],
        hint: 'Related traversal requires an insight admitted by the current turn\'s direct Recall. No Provider query was made.',
      }
    }
    const digest = sha256(JSON.stringify({
      sourceInstanceKey: authority.sourceInstanceKey,
      id,
      memoryBodyId: selected,
      depth: options.depth ?? 2,
      edge: options.edge ?? '',
    }))
    if (retrieval.relatedDigest !== undefined) {
      const last = retrieval.relatedResult ?? await retrieval.relatedPending
      const previous = last?.memoryEvidence?.sourceInstanceKey === authority.sourceInstanceKey ? last : undefined
      return {
        query: previous?.query ?? `related:${id}`,
        mode: previous?.mode ?? 'related',
        ...replayEvidence(previous, [selected], authority.sourceInstanceKey),
        hint: retrieval.relatedDigest === digest
          ? 'This exact Related traversal already ran. The Host replayed its admitted evidence without another Provider query; stop retrieval and answer from it.'
          : 'Related traversal is complete for this turn. The Host replayed the admitted evidence; stop retrieval and answer from it.',
      }
    }
    retrieval.relatedDigest = digest
    const operation = (async (): Promise<RecallResult> => {
      const request = { id, ...(options.depth === undefined ? {} : { depth: options.depth }), ...(options.edge === undefined ? {} : { edge: options.edge }), memoryBodyId: selected }
      const evidence = await read(json(request))
      const results = evidenceInsights(evidence)
      const admitted = boundedModelInsights(results, {
        resultLimit: 4,
        totalContentLimit: 4_000,
        mediumLimit: 4,
        unknownLimit: 4,
        excludeDigests: retrieval.evidenceDigests,
      })
      for (const insight of admitted.results) {
        retrieval.evidenceDigests.add(insightDigest(insight))
        retrieval.evidenceReferences.add(`${authority.sourceInstanceKey}/${insight.memoryBodyId ?? ''}/${insight.id}`)
      }
      const response: RecallResult = {
        query: `related:${id}`,
        mode: 'related',
        results: admitted.results,
        memoryEvidence: recallEvidence(evidence, admitted.results),
        hint: admitted.results.length === 0
          ? 'No new graph evidence was admitted; stop retrieval and answer from the existing evidence.'
          : 'Related traversal is complete for this turn; stop retrieval and answer from the admitted evidence.',
      }
      retrieval.relatedResult = structuredClone(response)
      return response
    })()
    retrieval.relatedPending = operation
    try {
      return await operation
    } catch (error) {
      if (retrieval.relatedDigest === digest) {
        delete retrieval.relatedDigest
        delete retrieval.relatedResult
      }
      throw error
    } finally {
      if (retrieval.relatedPending === operation) delete retrieval.relatedPending
    }
  }


}

export const createThreeTierTurn = (view: ComposableMemoryView): MemoryStrategyTurn => new ThreeTierTurn(view)
