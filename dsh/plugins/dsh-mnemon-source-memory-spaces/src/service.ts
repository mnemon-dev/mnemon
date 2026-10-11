import { mkdtempSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { JsonValue } from './contracts.ts'
import type { MemoryMutationCompletion } from 'dsh-mnemon/contracts'
import { memoryInputInteger as integer } from 'dsh-mnemon/extension-sdk'
import type { ResolvedMemorySpacesConfig as ResolvedConfig } from './config.ts'
import { MemorySpaceRegistry, validateMemorySpaceId } from './memory-spaces.ts'
import type { MnemonRunner } from './runner.ts'
import { finalizeLlmPlacement, prepareMemoryPlacement, rulesOnlyPlacement } from './provider-placement.ts'
import { EMPTY_MEMORY_PROVIDER_CATALOG, MemoryProviderCatalog } from './providers/catalog.ts'
import { type MemoryProviderAdapter, type ProviderSpaceStatus, type ProviderSearchResult } from './providers/adapter.ts'
import { MemoryProviderAdapterRegistry } from './providers/registry.ts'
import { lexicalRequiredMatchCount, lexicalSearchTokens, lexicalTokenMatchCount } from './search-tokens.ts'
import { ENTITY_INDEX_LIST_LIMIT, ENTITY_RAIL_LIMIT, buildSpaceEntityIndex, memoriesWithEntity, mergeEntityCounts, type SpaceEntityIndex } from './entity-index.ts'
import {
  applyRecallQualityPolicy,
  prepareRecallQualityPolicy,
  recallQualityPolicies,
  type EvaluatedRecallQualityCandidate,
  type RecallQualityCandidate,
  type RecallQualityPolicy,
  type RecallQualityPolicyContext,
  type RecallQualityPolicyRegistry,
} from './recall-quality/index.ts'
import {
  CATEGORIES,
  EDGE_TYPES,
  INTENTS,
  SOURCES,
  normalizeEntityKey,
  type Category,
  type CreateMemorySpaceRequest,
  type EdgeType,
  type EntityMemoriesView,
  type EntityRelatedView,
  type EntityView,
  type Insight,
  type Intent,
  type LlmMemoryPlacementSelection,
  type MemorySpace,
  type MemorySpaceCatalog,
  type MemorySpaceStats,
  type MemorySpaceMetadataSample,
  type MemorySpaceMetadataUpdate,
  type MemorySpaceView,
  type MemoryGraphEdge,
  type MemoryGraphNode,
  type MemoryGraphSnapshot,
  type MemoryListRequest,
  type MemoryListView,
  type MnemonEmbeddingStatus,
  type MemoryPlacementDecision,
  type MemoryProviderDescriptor,
  type MemoryReadMode,
  type MemoryReadSource,
  type MemoryReadStatus,
  type PreparedMemoryPlacement,
  type RememberRequest,
  type RecallQualityStats,
  type SearchRequest,
  type Source,
  type UpdateMemorySpaceRequest,
  type MemorySpacesStatus as StatusView,
} from './contracts.ts'

interface PreparedRemember {
  body: MemorySpace
  request: RememberRequest
}

/**
 * Providers whose native search is a single bounded request while their browse
 * projection fans out to multiple resources or collections. Prefer search for
 * metadata sampling so AI maintenance never pays for a detailed projection.
 */
const METADATA_SEARCH_FIRST_PROVIDERS = new Set<MemorySpace['provider']['id']>([
  'openviking',
  'supermemory',
  'byterover',
])

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

/**
 * How long a selection reuses the Provider health its index was checked with.
 * The rail always checks again; a write through this Source drops both at once.
 */
const ENTITY_SELECTION_STATUS_REUSE_MS = 10_000
/** How long an index that statistics cannot check is reused; a write through this Source still drops it. */
const ENTITY_INDEX_UNCHECKED_REUSE_MS = 10_000

interface EntityIndexRead {
  byBody: Map<string, SpaceEntityIndex>
  indexes: SpaceEntityIndex[]
  /** Healthy entity spaces related recall can query, indexed or query-only. */
  readable: MemorySpace[]
  sources: MemoryReadSource[]
  complete: boolean
}

interface CachedEntityIndex {
  fingerprint: string | undefined
  /** When the read finished; undefined while it runs. */
  settledAt: number | undefined
  index: Promise<SpaceEntityIndex>
}

function entityName(entity: string): string {
  const name = entity.trim()
  if (name.length > 200) throw new Error('entity is too long (max 200 characters)')
  return name
}

function memoryKey(memoryBodyId: string, id: string): string {
  return `${memoryBodyId}\u0000${id}`
}

/**
 * What must stay the same for a space's entity index to stay valid: its
 * Provider statistics (Mnemon counts every write in its operation log) and the
 * Source's own record of the space. Without statistics it cannot be checked.
 */
function entityIndexFingerprint(body: MemorySpace, status: ProviderSpaceStatus): string | undefined {
  const stats = status.stats
  if (stats === undefined) return undefined
  return JSON.stringify([body.provider.id, body.updatedAt, stats.totalInsights, stats.deletedInsights, stats.edgeCount, stats.oplogCount, stats.dbSizeBytes])
}

function readSource(
  body: MemorySpace,
  mode: MemoryReadMode,
  status: MemoryReadStatus,
  itemCount: number,
  options: { edgeCount?: number; memoryCount?: number; complete?: boolean; hint?: string } = {},
): MemoryReadSource {
  return {
    memoryBodyId: body.id,
    memoryBodyName: body.name,
    providerId: body.provider.id,
    providerLabel: body.provider.label,
    mode,
    status,
    itemCount,
    ...options,
  }
}

const MAX_EXACT_SEARCH_ANCHORS = 8
const EXACT_SEARCH_ANCHOR = /(?<!\d)\d{4}-\d{1,2}-\d{1,2}(?!\d)|(?<![A-Za-z0-9])[A-Za-z][A-Za-z0-9]*(?:[-_][A-Za-z0-9]+)+(?![A-Za-z0-9])|(?<!\d)\d+(?:[.,]\d+)?\s*(?:[%％]|percent(?:age)?)(?![A-Za-z])|百分之\s*\d+(?:[.,]\d+)?|(?<!\d)\d{1,2}:\d{2}(?!\d)|(?<![A-Za-z0-9])v?\d+\.\d+(?:\.\d+)*(?![A-Za-z0-9])|(?<![\d.])\d+(?![\d.%％:-])/giu

interface NativeSearchRecoveryPlan {
  kind: 'exact' | 'lexical'
  terms: string[]
  query: string
  requiredMatches: number
}

function normalizedExactText(value: string): string {
  return value
    .normalize('NFKC')
    .toLocaleLowerCase()
    .replace(/百分之\s*(\d+(?:[.,]\d+)?)/gu, '$1%')
    .replace(/(\d+(?:[.,]\d+)?)\s*percent(?:age)?/giu, '$1%')
    .replace(/\s+/gu, '')
}

/**
 * Extract only high-information lexical values that a semantic paraphrase
 * should not be allowed to erase. This is a deterministic fallback inside an
 * already-authorized search, not another Recall trigger or model decision.
 */
function exactSearchAnchorPlan(query: string): NativeSearchRecoveryPlan | undefined {
  const anchors = [...new Set([...query.matchAll(EXACT_SEARCH_ANCHOR)].map(match => normalizedExactText(match[0])))]
    .slice(0, MAX_EXACT_SEARCH_ANCHORS)
  if (anchors.length < 2) return undefined
  return {
    kind: 'exact',
    terms: anchors,
    query: anchors.join(' '),
    requiredMatches: Math.min(4, anchors.length),
  }
}

function lexicalSearchRecoveryPlan(query: string): NativeSearchRecoveryPlan | undefined {
  const tokens = lexicalSearchTokens(query, 32)
  if (tokens.length < 4) return undefined
  return {
    kind: 'lexical',
    terms: tokens,
    query,
    requiredMatches: lexicalRequiredMatchCount(tokens),
  }
}

function recoveryMatchCount(content: string, plan: NativeSearchRecoveryPlan): number {
  if (plan.kind === 'lexical') return lexicalTokenMatchCount(content, plan.terms)
  const normalized = normalizedExactText(content)
  return plan.terms.filter(anchor => normalized.includes(anchor)).length
}

function mergeRecoveryResults(
  original: readonly Insight[],
  recovered: readonly Insight[],
  plan: NativeSearchRecoveryPlan,
  limit: number,
): Insight[] {
  const admitted = recovered
    .map(insight => ({ insight, matches: recoveryMatchCount(insight.content, plan) }))
    .filter(candidate => candidate.matches >= plan.requiredMatches)
    .sort((left, right) => right.matches - left.matches || (right.insight.score ?? 0) - (left.insight.score ?? 0))
    .map(candidate => candidate.insight)
  const seen = new Set<string>()
  return [...admitted, ...original].filter(insight => {
    if (seen.has(insight.id)) return false
    seen.add(insight.id)
    return true
  }).slice(0, limit)
}

/** Put query-covering evidence first before the smaller model envelope runs. */
function prioritizeRecoveryEvidence(
  selected: readonly EvaluatedRecallQualityCandidate[],
  plan: NativeSearchRecoveryPlan,
): EvaluatedRecallQualityCandidate[] {
  return selected
    .map((entry, index) => ({ entry, index, matches: recoveryMatchCount(entry.candidate.insight.content, plan) }))
    .sort((left, right) => {
      const leftAdmitted = left.matches >= plan.requiredMatches
      const rightAdmitted = right.matches >= plan.requiredMatches
      if (leftAdmitted !== rightAdmitted) return rightAdmitted ? 1 : -1
      if (leftAdmitted && left.matches !== right.matches) return right.matches - left.matches
      return left.index - right.index
    })
    .map(candidate => candidate.entry)
}

function insightColor(category: string | undefined): string {
  if (category === 'preference') return '#9b59b6'
  if (category === 'decision') return '#e74c3c'
  if (category === 'fact') return '#3498db'
  if (category === 'insight') return '#2ecc71'
  if (category === 'context') return '#f39c12'
  return '#6574d9'
}

function required(value: string, label: string, max: number): string {
  const normalized = value.trim()
  if (normalized === '') throw new Error(`${label} is required`)
  if (normalized.length > max) throw new Error(`${label} is too long (max ${max} characters)`)
  return normalized
}

function allowed<T extends string>(value: T | undefined, values: readonly T[], label: string): T | undefined {
  if (value !== undefined && !values.includes(value)) {
    throw new Error(`${label} must be one of: ${values.join(', ')}`)
  }
  return value
}

function commaList(values: string[] | undefined, label: string, limit: number): string | undefined {
  if (values === undefined) return undefined
  const normalized = values.map(value => value.trim()).filter(value => value !== '')
  if (normalized.length > limit) throw new Error(`${label} accepts at most ${limit} values`)
  if (normalized.some(value => value.includes(','))) throw new Error(`${label} values cannot contain commas`)
  return normalized.length === 0 ? undefined : normalized.join(',')
}

const COMMITTED_MUTATION_STATES = new Set([
  'added',
  'committed',
  'completed',
  'created',
  'deleted',
  'forgotten',
  'imported',
  'invalidated',
  'linked',
  'merged',
  'removed',
  'replaced',
  'stored',
  'succeeded',
  'success',
  'updated',
])
const PENDING_MUTATION_STATES = new Set([
  'accepted',
  'pending',
  'processing',
  'queued',
  'running',
])
const FAILED_MUTATION_STATES = new Set(['canceled', 'cancelled', 'error', 'failed'])
const COMMITTED_MUTATION_COUNTS = ['created', 'deleted', 'edges_inserted', 'imported', 'removed', 'stored', 'updated'] as const

/** Source-private translation of Provider receipts; Core does not interpret Provider payloads. */
export function mutationResultCompletion(result: unknown): MemoryMutationCompletion {
  if (typeof result !== 'object' || result === null || Array.isArray(result)) return 'unknown'
  const value = result as Record<string, unknown>
  const states = [value.action, value.status, value.state]
    .filter((entry): entry is string => typeof entry === 'string')
    .map(entry => entry.trim().toLocaleLowerCase())
  const changed = COMMITTED_MUTATION_COUNTS.some(key => typeof value[key] === 'number' && Number.isFinite(value[key]) && value[key] > 0)
  const errors = Array.isArray(value.errors) ? value.errors.length > 0 : typeof value.errors === 'number' && value.errors > 0
  if (states.includes('partial') || (errors && changed)) return 'partial'
  if (value.success === false || value.ok === false || errors || states.some(state => FAILED_MUTATION_STATES.has(state))) return 'failed'
  if (states.includes('candidate')) return 'candidate'
  if (states.some(state => PENDING_MUTATION_STATES.has(state))) return 'accepted'
  if (value.committed === false || value.durable === false || states.includes('skipped')) return 'unknown'
  if (value.committed === true || value.durable === true || states.some(state => COMMITTED_MUTATION_STATES.has(state)) || changed) return 'committed'
  // A successful HTTP request or an id alone does not prove memory persistence.
  return 'unknown'
}

export function mutationResultCommitted(result: unknown): boolean {
  return mutationResultCompletion(result) === 'committed'
}

export class MemorySpacesService {
  readonly memorySpaces: MemorySpaceRegistry
  private readonly providers: Map<MemorySpace['provider']['id'], MemoryProviderAdapter>
  private readonly recallQualityPolicy: RecallQualityPolicy
  private spacesInFlight: Promise<MemorySpaceCatalog> | undefined
  /** One entity index per active entity space, valid while its fingerprint holds. */
  private readonly entityIndexCache = new Map<string, CachedEntityIndex>()
  /** settledAt stays undefined while the read runs, which every caller shares. */
  private readonly entityStatusCache = new Map<string, { settledAt: number | undefined; status: Promise<ProviderSpaceStatus> }>()
  /** The related read each page view is waiting for; a newer selection cancels the older one. */
  private readonly entityRelatedViews = new Map<string, AbortController>()
  private providersDisposed = false
  /** The CLI version a full status last read, and the binary it read it from. */
  private cliVersion: { binary: string; version: string } | undefined

  /** The CLI binary as the file system has it; another value after an update replaces it. */
  private cliBinary(): string | undefined {
    try {
      const stat = statSync(this.runner.command)
      return `${this.runner.command}\0${stat.mtimeMs}\0${stat.size}`
    } catch {
      return undefined
    }
  }

  private providerTypeId(providerId: string): string {
    const catalog = this.providerCatalog
    if (!catalog.has(providerId)) return providerId
    const descriptor = catalog.descriptor(providerId)
    return descriptor.typeId ?? descriptor.id
  }

  private isNativeProvider(providerId: string): boolean {
    return this.providerTypeId(providerId) === 'mnemon-native'
  }

  private isNativeSpace(body: MemorySpace): boolean {
    return (body.provider.typeId ?? body.provider.id) === 'mnemon-native'
  }

  constructor(
    readonly runner: MnemonRunner,
    readonly config: ResolvedConfig,
    memorySpaces?: MemorySpaceRegistry,
    private readonly recallQualityPolicyRegistry: RecallQualityPolicyRegistry = recallQualityPolicies,
    providerAdapterRegistry: MemoryProviderAdapterRegistry = new MemoryProviderAdapterRegistry(),
    private readonly providerCatalog: MemoryProviderCatalog = EMPTY_MEMORY_PROVIDER_CATALOG,
  ) {
    this.memorySpaces = memorySpaces === undefined
      ? new MemorySpaceRegistry(runner, true, () => new Date(), providerCatalog)
      : providerCatalog === EMPTY_MEMORY_PROVIDER_CATALOG ? memorySpaces : memorySpaces.withProviderCatalog(providerCatalog)
    this.recallQualityPolicy = recallQualityPolicyRegistry.resolve(config.recallQuality.policy)
    this.providers = providerAdapterRegistry.create({ memorySpaces: this.memorySpaces, memoryBodies: this.memorySpaces, config: this.config, nativeRunner: this.runner })
  }

  /** Release clients owned by one composable Memory Spaces generation. */
  async dispose(): Promise<void> {
    if (this.providersDisposed) return
    this.providersDisposed = true
    const failures: unknown[] = []
    for (const provider of [...this.providers.values()].reverse()) {
      try {
        await provider.dispose?.()
      } catch (error) {
        failures.push(error)
      }
    }
    this.providers.clear()
    if (failures.length > 0) throw new AggregateError(failures, 'Memory Space Provider disposal failed')
  }

  async spaces(signal?: AbortSignal): Promise<MemorySpaceCatalog> {
    if (signal !== undefined) return this.collectSpaces(signal)
    if (this.spacesInFlight !== undefined) return this.spacesInFlight
    const pending = this.collectSpaces()
    this.spacesInFlight = pending
    try {
      return await pending
    } finally {
      if (this.spacesInFlight === pending) this.spacesInFlight = undefined
    }
  }

  /** Coalesce simultaneous Status/Memory-page probes without caching mutations. */
  private async collectSpaces(signal?: AbortSignal): Promise<MemorySpaceCatalog> {
    const directory = this.spaceDirectory()
    const items: MemorySpaceView[] = await Promise.all(directory.items.map(async body => {
      let status: ProviderSpaceStatus
      const providerEnabled = body.providerEnabled !== false
      if (!providerEnabled) status = { healthy: false, error: `${body.provider.label} is disabled on the dsh-mnemon page under Plugins` }
      else try { status = await this.providerFor(body).status(body, signal) } catch (error) {
          status = { healthy: false, error: error instanceof Error ? error.message : String(error) }
      }
      const { statusLoading: _statusLoading, ...metadata } = body
      return { ...metadata, ...status }
    }))
    return {
      ...directory,
      items,
      activeCount: items.filter(body => body.active && body.providerEnabled !== false).length,
      generatedAt: new Date().toISOString(),
    }
  }

  /** Return the control-plane directory without waiting for provider I/O. */
  spaceDirectory(): MemorySpaceCatalog {
    const mnemonDefaultStore = this.runner.persistedStore()
    const all = this.memorySpaces.list()
    const enabled = new Set(this.memorySpaces.providerServices().items.filter(service => service.enabled).map(service => service.providerId))
    const items: MemorySpaceView[] = all.map(body => {
      const nativeProvider = this.isNativeSpace(body)
      const providerEnabled = nativeProvider || enabled.has(body.provider.id)
      return { ...body, providerEnabled, mnemonDefault: nativeProvider && body.id === mnemonDefaultStore, healthy: false, statusLoading: true }
    })
    return {
      items,
      providers: this.providerCatalog.providers.map(provider => ({
        ...provider,
        serviceConfigured: (provider.typeId ?? provider.id) === 'mnemon-native' ? this.runner.commandFound : enabled.has(provider.id),
      })),
      persistenceStrategy: {
        mode: this.config.persistenceStrategy.mode,
        providerId: this.persistenceProviderId() ?? this.config.persistenceStrategy.providerId,
        prompt: this.config.persistenceStrategy.prompt,
        rules: { ...this.config.persistenceStrategy.rules },
      },
      total: items.length,
      activeCount: items.filter(body => body.active && body.providerEnabled !== false).length,
      directory: this.memorySpaces.directory,
      generatedAt: new Date().toISOString(),
    }
  }

  /** Stable, secret-free checkpoint for the projected Memory Space authority. */
  memoryRevision(): string {
    return this.memoryState().revision
  }

  /** One local metadata observation supplies membership and its revision. */
  memoryState(): { all: MemorySpace[]; active: MemorySpace[]; revision: string } {
    const all = this.memorySpaces.list()
    const serviceItems = this.memorySpaces.providerServices().items
    const enabled = new Set(serviceItems.filter(service => service.enabled).map(service => service.providerId))
    const active = all.filter(body => body.active && (this.isNativeSpace(body) || enabled.has(body.provider.id)))
      .sort((left, right) => left.id.localeCompare(right.id))
    const spaces = [...all]
      .sort((left, right) => left.id.localeCompare(right.id))
      .map(body => ({
        id: body.id,
        name: body.name,
        description: body.description,
        active: body.active,
        providerId: body.provider.id,
        updatedAt: body.updatedAt,
        capabilities: body.provider.capabilities,
      }))
    const services = serviceItems
      .map(service => ({ providerId: service.providerId, enabled: service.enabled, configured: service.configured }))
      .sort((left, right) => left.providerId.localeCompare(right.providerId))
    return { all, active, revision: createHash('sha256').update(JSON.stringify({ bodies: spaces, services })).digest('hex') }
  }

  /** Return a usable system snapshot without waiting for any Provider I/O. */
  statusSummary(): StatusView {
    const catalog = this.spaceDirectory()
    // Reuse the version a full status read while the same binary is installed.
    const version = this.cliVersion !== undefined && this.cliBinary() === this.cliVersion.binary ? this.cliVersion.version : undefined
    const active = catalog.items.filter(body => body.active && body.providerEnabled !== false)
    const dshActiveStores = active.map(body => body.id)
    const providerServices = this.memorySpaces.providerServices().items.map(service => {
      const descriptor = this.providerCatalog.descriptor(service.providerId)
      const spaces = catalog.items.filter(body => body.provider.id === service.providerId)
      const activeSpaces = spaces.filter(body => body.active && body.providerEnabled !== false)
      return {
        providerId: service.providerId,
        label: descriptor.label,
        ...(descriptor.icon === undefined ? {} : { icon: descriptor.icon }),
        enabled: service.enabled,
        configured: service.configured,
        status: !service.enabled ? 'disabled' as const : 'idle' as const,
        memoryBodyCount: spaces.length,
        activeMemoryBodyCount: activeSpaces.length,
      }
    })
    return {
      // Provider availability is projected below and must never make the
      // dsh-mnemon engine itself appear disconnected.
      healthy: true,
      cliPath: this.runner.command,
      commandFound: this.runner.commandFound,
      dataDir: this.runner.effectiveDataDir(),
      store: dshActiveStores.join(', ') || 'none',
      mnemonDefaultStore: this.runner.persistedStore(),
      dshActiveStores,
      writeEnabled: this.config.writeEnabled,
      timeoutMs: this.config.timeoutMs,
      defaultRecallLimit: this.config.defaultRecallLimit,
      recallQuality: this.config.recallQuality,
      memoryBodyDirectory: catalog.directory,
      memoryBodies: catalog.items,
      providerServices,
      ...(version === undefined ? {} : { version }),
    }
  }

  /** Probe the effective Mnemon embedding runtime and its default Store coverage. */
  async embeddingStatus(signal?: AbortSignal): Promise<MnemonEmbeddingStatus> {
    const output = record(await this.runner.runJson(
      ['embed', '--status'],
      signal === undefined ? {} : { signal },
    ))
    // Mnemon ≥ 0.3.x reports `embedding_available`; `ollama_available` is the
    // legacy alias kept for older binaries.
    const available = output?.embedding_available ?? output?.ollama_available
    const model = text(output?.model)?.trim()
    // Mnemon ≥ 0.3.x may report the endpoint protocol it resolved; older binaries
    // omit the field, so a missing or malformed value is dropped instead of
    // failing the whole status probe.
    const protocol = text(output?.protocol)?.trim()
    const totalInsights = number(output?.total_insights)
    const embedded = number(output?.embedded)
    const coverage = text(output?.coverage)?.trim()
    if (typeof available !== 'boolean'
      || model === undefined || model === '' || model.length > 200 || /[\u0000-\u001f\u007f]/u.test(model)
      || !Number.isInteger(totalInsights) || totalInsights! < 0
      || !Number.isInteger(embedded) || embedded! < 0 || embedded! > totalInsights!
      || coverage === undefined || !/^(?:100|\d{1,2})%$/u.test(coverage)) {
      throw new Error('mnemon embed --status returned an invalid response')
    }
    const validProtocol = protocol !== undefined && protocol.length <= 32 && !/[\u0000-\u001f\u007f]/u.test(protocol)
    return { available, model, totalInsights: totalInsights!, embedded: embedded!, coverage, ...(validProtocol ? { protocol } : {}) }
  }

  async status(signal?: AbortSignal): Promise<StatusView> {
    // Mnemon Native reports a version once its CLI is installed or one of its spaces exists.
    const nativeInUse = this.runner.commandFound || this.memorySpaces.list().some(body => this.isNativeSpace(body))
    let versionError: unknown
    const [catalog, rawVersion] = await Promise.all([
      this.spaces(signal),
      nativeInUse
        ? this.runner.runText(['--version'], signal === undefined ? { globalFlags: false } : { signal, globalFlags: false }).catch(error => {
            versionError = error
            return undefined
          })
        : Promise.resolve(undefined),
    ])
    const active = catalog.items.filter(body => body.active && body.providerEnabled !== false)
    const dshActiveStores = active.map(body => body.id)
    const providerServices = this.memorySpaces.providerServices().items.map(service => {
      const descriptor = this.providerCatalog.descriptor(service.providerId)
      const spaces = catalog.items.filter(body => body.provider.id === service.providerId)
      const activeSpaces = spaces.filter(body => body.active && body.providerEnabled !== false)
      const failed = activeSpaces.filter(body => !body.healthy)
      const status = !service.enabled
        ? 'disabled' as const
        : activeSpaces.length === 0
          ? 'idle' as const
          : failed.length === 0
            ? 'healthy' as const
            : 'unhealthy' as const
      return {
        providerId: service.providerId,
        label: descriptor.label,
        ...(descriptor.icon === undefined ? {} : { icon: descriptor.icon }),
        enabled: service.enabled,
        configured: service.configured,
        status,
        memoryBodyCount: spaces.length,
        activeMemoryBodyCount: activeSpaces.length,
        ...(failed.length === 0 ? {} : { error: failed.map(body => `${body.name}: ${body.error ?? 'unavailable'}`).join('; ') }),
      }
    })
    const base = {
      cliPath: this.runner.command,
      commandFound: this.runner.commandFound,
      dataDir: this.runner.effectiveDataDir(),
      store: dshActiveStores.join(', ') || 'none',
      mnemonDefaultStore: this.runner.persistedStore(),
      dshActiveStores,
      writeEnabled: this.config.writeEnabled,
      timeoutMs: this.config.timeoutMs,
      defaultRecallLimit: this.config.defaultRecallLimit,
      recallQuality: this.config.recallQuality,
      memoryBodyDirectory: catalog.directory,
      memoryBodies: catalog.items,
      providerServices,
    }
    const version = rawVersion === undefined ? undefined : rawVersion.trim().replace(/^mnemon version\s+/i, '')
    const binary = version === undefined ? undefined : this.cliBinary()
    this.cliVersion = version === undefined || binary === undefined ? undefined : { binary, version }
    try {
      const healthySpaces = active.filter(body => body.healthy && body.stats !== undefined)
      const topEntities = new Map<string, number>()
      const byCategory: Record<string, number> = {}
      for (const body of healthySpaces) {
        for (const [category, count] of Object.entries(body.stats!.byCategory)) byCategory[category] = (byCategory[category] ?? 0) + count
        for (const entity of body.stats!.topEntities) topEntities.set(entity.entity, (topEntities.get(entity.entity) ?? 0) + entity.count)
      }
      const stats: StatusView['stats'] = {
        totalInsights: healthySpaces.reduce((total, body) => total + body.stats!.totalInsights, 0),
        deletedInsights: healthySpaces.reduce((total, body) => total + body.stats!.deletedInsights, 0),
        edgeCount: healthySpaces.reduce((total, body) => total + body.stats!.edgeCount, 0),
        oplogCount: healthySpaces.reduce((total, body) => total + body.stats!.oplogCount, 0),
        dbSizeBytes: healthySpaces.reduce((total, body) => total + body.stats!.dbSizeBytes, 0),
        byCategory,
        topEntities: [...topEntities].map(([entity, count]) => ({ entity, count })).sort((left, right) => right.count - left.count),
        ...(active.length === 1 ? { dbPath: active[0]!.dbPath } : {}),
      }
      // A missing or failing Mnemon CLI affects only its own spaces; other providers keep their stats.
      const errors = [
        ...(versionError === undefined ? [] : [versionError instanceof Error ? versionError.message : String(versionError)]),
        ...active.filter(body => !body.healthy).map(body => `${body.name}: ${body.error ?? 'unavailable'}`),
      ]
      return {
        healthy: true,
        ...base,
        ...(version === undefined ? {} : { version }),
        stats,
        ...(errors.length === 0 ? {} : { error: errors.join('; ') }),
      }
    } catch (error) {
      return { healthy: true, ...base, error: error instanceof Error ? error.message : String(error) }
    }
  }

  async reconnectSpace(id: string, signal?: AbortSignal): Promise<MemorySpaceView> {
    const body = this.memorySpaces.list().find(candidate => candidate.id === id)
    if (body === undefined) throw new Error(`unknown memory space: ${id}`)
    if (!this.isNativeSpace(body)) {
      if (!this.memorySpaces.providerServiceEnabled(body.provider.id)) throw new Error(`${body.provider.label} is disabled on the dsh-mnemon page under Plugins`)
    }
    // Card-level reconnect is deliberately scoped to this projected namespace.
    // Whole-service discovery only runs when its service is enabled or saved.
    const provider = this.providerFor(body)
    provider.invalidateStatus?.(body.id)
    this.invalidateEntityIndex(body.id)
    const status = await provider.status(body, signal)
    return {
      ...body,
      providerEnabled: true,
      mnemonDefault: this.isNativeSpace(body) && body.id === this.runner.persistedStore(),
      ...status,
    }
  }

  /** exclude: memoryKey()s to leave out before the quality policy selects. */
  async search(request: SearchRequest, signal?: AbortSignal, options: { exclude?: ReadonlySet<string> } = {}): Promise<{ query: string; mode: string; results: Insight[]; hint?: string; sources: MemoryReadSource[] }> {
    const query = required(request.query, 'query', 2000)
    const limit = integer(request.limit, this.config.defaultRecallLimit, 1, 50)
    const qualityContext: RecallQualityPolicyContext = { requestedLimit: limit, config: this.config.recallQuality }
    const preparedPolicy = prepareRecallQualityPolicy(this.recallQualityPolicy, qualityContext)
    const mode = allowed(request.mode, ['smart', 'keyword', 'basic'] as const, 'mode') ?? 'smart'
    const category = allowed(request.category, CATEGORIES, 'category')
    const source = allowed(request.source, SOURCES, 'source')
    const intent = allowed(request.intent, INTENTS, 'intent')
    const spaces = this.readSpaces(request.memoryBodyIds)
    const normalizedRequest: SearchRequest = {
      query,
      mode,
      limit: preparedPolicy.candidateLimit,
      ...(category === undefined ? {} : { category }),
      ...(source === undefined ? {} : { source }),
      ...(intent === undefined ? {} : { intent }),
    }
    let batches = await Promise.all(spaces.map(async body => {
      if (!body.provider.capabilities.search) {
        return {
          body,
          result: { results: [], hint: 'search is not supported' } satisfies ProviderSearchResult,
          source: readSource(body, 'unsupported', 'unsupported', 0, { hint: 'This provider does not expose search.' }),
        }
      }
      try {
        const result = await this.providerFor(body).search(body, normalizedRequest, signal)
        return {
          body,
          result,
          source: readSource(body, 'search', result.results.length === 0 ? 'empty' : 'ready', result.results.length, result.hint === undefined ? {} : { hint: result.hint }),
        }
      } catch (error) {
        const hint = error instanceof Error ? error.message : String(error)
        return {
          body,
          result: { results: [], hint: `unavailable: ${hint}` } satisfies ProviderSearchResult,
          source: readSource(body, 'search', 'unavailable', 0, { hint }),
        }
      }
    }))
    const recoveryPlan = mode === 'smart' && category === undefined && source === undefined && intent === undefined
      ? exactSearchAnchorPlan(query) ?? lexicalSearchRecoveryPlan(query)
      : undefined
    const evaluate = (selectedBatches: typeof batches) => {
      const candidates: RecallQualityCandidate[] = []
      const hints: string[] = []
      for (const [bodyOrder, { body, result }] of selectedBatches.entries()) {
        const scoreSemantics = this.providerFor(body).scoreSemantics
        const entries = options.exclude === undefined ? result.results : result.results.filter(entry => !options.exclude!.has(memoryKey(body.id, entry.id)))
        candidates.push(...entries.map((entry, index) => ({
          insight: this.annotate(entry, body),
          memoryBodyId: body.id,
          providerId: body.provider.id,
          providerRank: index + 1,
          bodyOrder,
          ...(scoreSemantics === undefined ? {} : { scoreSemantics }),
        })))
        if (result.hint !== undefined) hints.push(`${body.name}: ${result.hint}`)
      }
      const heterogeneous = new Set(spaces.map(body => body.provider.id)).size > 1
      if (heterogeneous) for (const candidate of candidates) candidate.insight.federatedScore = 1 / (60 + candidate.providerRank)
      candidates.sort((left, right) => heterogeneous
        ? (right.insight.federatedScore ?? 0) - (left.insight.federatedScore ?? 0) || left.bodyOrder - right.bodyOrder
        : (right.insight.score ?? 0) - (left.insight.score ?? 0))
      return { hints, quality: applyRecallQualityPolicy(preparedPolicy, candidates, qualityContext) }
    }
    let evaluation = evaluate(batches)
    const hasRecoveryEvidence = recoveryPlan !== undefined && evaluation.quality.selected.some(candidate => (
      recoveryMatchCount(candidate.candidate.insight.content, recoveryPlan) >= recoveryPlan.requiredMatches
    ))
    if (recoveryPlan !== undefined && !hasRecoveryEvidence) {
      batches = await Promise.all(batches.map(async batch => {
        if (!this.isNativeSpace(batch.body) || batch.source.status === 'unsupported' || batch.source.status === 'unavailable') return batch
        try {
          const provider = this.providerFor(batch.body)
          const recovered = await provider.search(batch.body, {
            query: recoveryPlan.query,
            mode: 'keyword',
            limit: Math.min(limit, preparedPolicy.candidateLimit),
          }, signal)
          const admitted = recovered.results.some(insight => recoveryMatchCount(insight.content, recoveryPlan) >= recoveryPlan.requiredMatches)
          const results = mergeRecoveryResults(batch.result.results, recovered.results, recoveryPlan, preparedPolicy.candidateLimit)
          return {
            ...batch,
            result: {
              results,
              ...(admitted || batch.result.hint === undefined ? {} : { hint: batch.result.hint }),
            },
          }
        } catch {
          // Deterministic Native recovery is an optional local fallback. The original
          // successful result remains authoritative if it is unavailable.
          return batch
        }
      }))
      evaluation = evaluate(batches)
    }
    const { hints, quality } = evaluation
    const selected = recoveryPlan === undefined
      ? quality.selected
      : prioritizeRecoveryEvidence(quality.selected, recoveryPlan)
    const qualityStats = new Map<string, RecallQualityStats>()
    for (const { body } of batches) qualityStats.set(body.id, {
      policyId: quality.policyId,
      ...(quality.fallbackFrom === undefined ? {} : { fallbackFrom: quality.fallbackFrom }),
      fetched: 0, retained: 0, selected: 0,
      droppedLowScore: 0, droppedNonPositiveScore: 0, droppedInvalidScore: 0,
      unscored: 0, unscaled: 0,
    })
    // Each candidate contributes once, rather than rescanning all candidates per space.
    for (const { candidate, decision } of quality.evaluated) {
      const stats = qualityStats.get(candidate.memoryBodyId)
      if (stats === undefined) continue
      stats.fetched += 1
      if (decision.action === 'keep') stats.retained += 1
      if (decision.action === 'drop') {
        if (decision.reason === 'low-score') stats.droppedLowScore += 1
        if (decision.reason === 'non-positive-score') stats.droppedNonPositiveScore += 1
        if (decision.reason === 'invalid-score') stats.droppedInvalidScore += 1
      }
      if (decision.reason === 'unscored') stats.unscored += 1
      if (decision.reason === 'unscaled-score') stats.unscaled += 1
    }
    for (const { candidate } of quality.selected) {
      const stats = qualityStats.get(candidate.memoryBodyId)
      if (stats !== undefined) stats.selected += 1
    }
    return {
      query,
      mode,
      results: selected.map(({ candidate, decision }) => ({
        ...candidate.insight,
        relevanceTier: decision.tier,
        ...(decision.normalizedScore === undefined ? {} : { normalizedScore: decision.normalizedScore }),
      })),
      sources: batches.map(batch => {
        const stats = qualityStats.get(batch.body.id)!
        if (batch.source.status === 'unavailable' || batch.source.status === 'unsupported') return { ...batch.source, quality: stats }
        return { ...batch.source, status: stats.retained === 0 ? 'empty' : 'ready', itemCount: stats.retained, quality: stats }
      }),
      ...(hints.length === 0 ? {} : { hint: hints.join('\n') }),
    }
  }

  /**
   * Read a deliberately small metadata sample through the cheapest useful path
   * exposed by the owning Provider. This avoids federated ranking, graph
   * expansion, and large browse projections before an LLM metadata pass.
   */
  async metadataSample(memoryBodyId: string, signal?: AbortSignal): Promise<MemorySpaceMetadataSample> {
    const body = this.readSpaces([memoryBodyId])[0]!
    const provider = this.providerFor(body)
    const limit = 6
    let method: MemorySpaceMetadataSample['method']
    let items: Insight[]
    if (this.isNativeSpace(body)) {
      method = 'native-basic'
      items = provider.metadataSample === undefined
        ? await provider.list(body, { limit }, signal)
        : await provider.metadataSample(body, limit, signal)
    } else if (METADATA_SEARCH_FIRST_PROVIDERS.has(body.provider.typeId ?? body.provider.id) || !body.provider.capabilities.browse) {
      method = 'search'
      const query = (body.description.trim() || body.name.trim()).slice(0, 400)
      items = (await provider.search(body, { query, mode: 'basic', limit }, signal)).results
    } else {
      method = 'browse'
      items = await provider.list(body, { limit }, signal)
    }
    return {
      memoryBodyId: body.id,
      name: body.name,
      description: body.description,
      providerId: body.provider.id,
      providerLabel: body.provider.label,
      method,
      evidence: items.slice(0, limit).map(item => ({
        content: item.content.length > 720 ? `${item.content.slice(0, 719)}…` : item.content,
        ...(item.category === undefined ? {} : { category: item.category }),
        ...(item.entities === undefined ? {} : { entities: item.entities.slice(0, 8) }),
      })),
    }
  }

  async graph(signal?: AbortSignal, memoryBodyIds?: string[]): Promise<MemoryGraphSnapshot> {
    const spaces = this.readSpaces(memoryBodyIds)
    const nodes: MemoryGraphNode[] = []
    const edges: MemoryGraphEdge[] = []
    const sources: MemoryReadSource[] = []
    const snapshots = await Promise.all(spaces.map(async body => {
      const mode: MemoryReadMode = body.provider.capabilities.graph
        ? 'graph'
        : body.provider.capabilities.browse
          ? 'projection'
          : body.provider.capabilities.search
            ? 'query-only'
            : 'unsupported'
      if (mode === 'query-only') {
        return { body, source: readSource(body, mode, 'query-required', 0, { edgeCount: 0, hint: 'Use Recall to query this provider.' }) }
      }
      if (mode === 'unsupported') {
        return { body, source: readSource(body, mode, 'unsupported', 0, { edgeCount: 0, hint: 'This provider exposes neither graph nor browse projection.' }) }
      }
      try {
        const snapshot = await this.providerFor(body).graph(body, signal)
        return {
          body,
          snapshot,
          source: readSource(body, mode, snapshot.nodes.length === 0 ? 'empty' : 'ready', snapshot.nodes.length, { edgeCount: snapshot.edges.length }),
        }
      } catch (error) {
        return {
          body,
          source: readSource(body, mode, 'unavailable', 0, { edgeCount: 0, hint: error instanceof Error ? error.message : String(error) }),
        }
      }
    }))
    for (const item of snapshots) {
      sources.push(item.source)
      if (item.snapshot === undefined) continue
      const { body, snapshot } = item
      const graphId = (id: string): string => `${body.id}:${id}`
      nodes.push(...snapshot.nodes.map(node => ({ ...this.annotate(node, body), color: node.color, graphId: graphId(node.id) })))
      edges.push(...snapshot.edges.map(edge => ({ ...edge, sourceId: graphId(edge.sourceId), targetId: graphId(edge.targetId) })))
    }
    return {
      nodes,
      edges,
      generatedAt: new Date().toISOString(),
      memoryBodies: spaces.map(({ id, name, active }) => ({ id, name, active })),
      sources,
    }
  }

  async list(request: MemoryListRequest = {}, signal?: AbortSignal): Promise<MemoryListView> {
    const rawQuery = request.query?.trim() ?? ''
    const query = rawQuery.toLocaleLowerCase()
    if (rawQuery.length > 500) throw new Error('query is too long (max 500 characters)')
    const category = allowed(request.category, CATEGORIES, 'category')
    const limit = integer(request.limit, 200, 1, 1000)
    const spaces = this.readSpaces(request.memoryBodyIds)
    const batches = await Promise.all(spaces.map(async body => {
      const mode: MemoryReadMode = body.provider.capabilities.browse
        ? 'enumerable'
        : body.provider.capabilities.search
          ? 'query-only'
          : 'unsupported'
      if (mode === 'query-only' && rawQuery === '') {
        return { body, items: [] as Insight[], source: readSource(body, mode, 'query-required', 0, { hint: 'Enter a query to inspect this provider.' }) }
      }
      if (mode === 'unsupported') {
        return { body, items: [] as Insight[], source: readSource(body, mode, 'unsupported', 0, { hint: 'This provider does not expose content browsing.' }) }
      }
      try {
        const provider = this.providerFor(body)
        const rawItems = mode === 'query-only'
          ? (await provider.search(body, { query: rawQuery, limit }, signal)).results
          : await provider.list(body, { ...request, limit }, signal)
        const items = rawItems.filter(item =>
          (category === undefined || item.category === category)
          && (query === '' || item.content.toLocaleLowerCase().includes(query) || item.id.toLocaleLowerCase().includes(query)),
        )
        return {
          body,
          items,
          source: readSource(body, mode, items.length === 0 ? 'empty' : 'ready', items.length),
        }
      } catch (error) {
        return {
          body,
          items: [] as Insight[],
          source: readSource(body, mode, 'unavailable', 0, { hint: error instanceof Error ? error.message : String(error) }),
        }
      }
    }))
    const items = batches.flatMap(({ body, items: bodyItems }) => bodyItems.map(item => ({ ...this.annotate(item, body), color: insightColor(item.category) })))
    return {
      items: items.slice(0, limit),
      total: items.length,
      generatedAt: new Date().toISOString(),
      sources: batches.map(batch => batch.source),
    }
  }

  /**
   * The entities of the active spaces, each counted once per memory that
   * carries it. With an entity, also the first page of those memories.
   */
  async entities(entity?: string, limit?: number, signal?: AbortSignal): Promise<EntityView> {
    const selected = entity === undefined ? '' : entityName(entity)
    const read = await this.readEntityIndexes(signal, 0)
    const { items } = mergeEntityCounts(read.indexes)
    const view: EntityView = {
      items: items.slice(0, ENTITY_RAIL_LIMIT),
      insights: [],
      sources: read.sources,
      total: items.length,
      complete: read.complete,
    }
    if (selected === '') return view
    const page = this.entityPage(read, selected, 0, integer(limit, 20, 1, 50))
    return { ...view, selected: page.entity, insights: page.items }
  }

  /** One page of the memories that carry an entity, by importance and then recency. */
  async entityMemories(entity: string, offset?: number, limit?: number, signal?: AbortSignal): Promise<EntityMemoriesView> {
    const selected = entityName(entity)
    if (selected === '') throw new Error('entity is required')
    const read = await this.readEntityIndexes(signal, ENTITY_SELECTION_STATUS_REUSE_MS)
    return this.entityPage(read, selected, integer(offset, 0, 0, 1_000_000), integer(limit, 50, 1, 200))
  }

  /**
   * What recall relates to an entity, without the memories that carry it.
   * Those are left out before the quality policy selects, so they cannot use up
   * its places and leave the related list empty.
   */
  async entityRelated(entity: string, limit?: number, signal?: AbortSignal, view?: string): Promise<EntityRelatedView> {
    const selected = entityName(entity)
    if (selected === '') throw new Error('entity is required')
    // Recall queues behind the store lock; a view's newer selection should not wait for one it left.
    const superseding = view === undefined ? undefined : new AbortController()
    if (view !== undefined) {
      this.entityRelatedViews.get(view)?.abort(new Error('superseded by a newer entity selection'))
      this.entityRelatedViews.set(view, superseding!)
    }
    const combined = superseding === undefined ? signal : signal === undefined ? superseding.signal : AbortSignal.any([signal, superseding.signal])
    try {
      const read = await this.readEntityIndexes(combined, ENTITY_SELECTION_STATUS_REUSE_MS)
      const key = normalizeEntityKey(selected)
      const carrying = new Set<string>()
      for (const [bodyId, index] of read.byBody) for (const position of index.byKey.get(key) ?? []) carrying.add(memoryKey(bodyId, index.memories[position]!.id))
      const display = mergeEntityCounts(read.indexes).names.get(key) ?? selected
      const readableIds = read.readable.map(body => body.id)
      if (readableIds.length === 0) return { entity: display, items: [], sources: [] }
      const result = await this.search(
        { query: selected, intent: 'ENTITY', limit: integer(limit, 20, 1, 50), memoryBodyIds: readableIds },
        combined,
        { exclude: carrying },
      )
      combined?.throwIfAborted()
      return { entity: display, items: result.results, sources: result.sources }
    } finally {
      if (view !== undefined && this.entityRelatedViews.get(view) === superseding) this.entityRelatedViews.delete(view)
    }
  }

  private entityPage(read: EntityIndexRead, selected: string, offset: number, limit: number): EntityMemoriesView {
    const key = normalizeEntityKey(selected)
    const memories = memoriesWithEntity(read.indexes, key)
    return {
      entity: mergeEntityCounts(read.indexes).names.get(key) ?? selected,
      total: memories.length,
      offset,
      items: memories.slice(offset, offset + limit),
      complete: read.complete,
      sources: read.sources,
    }
  }

  /**
   * One entity index per active space that has an entity index, read
   * concurrently. A space keeps its index while its Provider statistics and
   * metadata stay the same; a write through this Source drops it.
   */
  private async readEntityIndexes(signal: AbortSignal | undefined, statusMaxAgeMs: number): Promise<EntityIndexRead> {
    signal?.throwIfAborted()
    const active = this.memorySpaces.active()
    const capable = new Set(active.filter(body => body.provider.capabilities.entities).map(body => body.id))
    for (const id of this.entityIndexCache.keys()) if (!capable.has(id)) this.entityIndexCache.delete(id)
    const reads = await Promise.all(active.map(async (body): Promise<{ body: MemorySpace; index?: SpaceEntityIndex; recall: boolean; source: MemoryReadSource }> => {
      if (!body.provider.capabilities.entities) {
        return { body, recall: false, source: readSource(body, 'unsupported', 'unsupported', 0, { hint: 'This provider does not expose an entity index.' }) }
      }
      const status = await this.entitySpaceStatus(body, statusMaxAgeMs)
      if (!status.healthy) return { body, recall: false, source: readSource(body, 'entities', 'unavailable', 0, { hint: status.error ?? 'Provider unavailable.' }) }
      const recall = body.provider.capabilities.search
      if (this.providerFor(body).entityIndex === undefined && !body.provider.capabilities.browse) {
        // Nothing to count from: its memories reach the page only through related recall.
        return { body, recall, source: readSource(body, 'query-only', 'query-required', 0, { hint: 'This provider can only be queried; its memories appear among related memories.' }) }
      }
      try {
        const index = await this.entityIndexFor(body, status)
        const options = { memoryCount: index.memoryCount, complete: index.complete, ...(index.complete ? {} : { hint: 'The Provider indexed only part of this space.' }) }
        return { body, index, recall, source: readSource(body, 'entities', index.byKey.size === 0 ? 'empty' : 'ready', index.byKey.size, options) }
      } catch (error) {
        return { body, recall: false, source: readSource(body, 'entities', 'unavailable', 0, { hint: error instanceof Error ? error.message : String(error) }) }
      }
    }))
    signal?.throwIfAborted()
    const byBody = new Map<string, SpaceEntityIndex>()
    for (const read of reads) if (read.index !== undefined) byBody.set(read.body.id, read.index)
    const indexes = [...byBody.values()]
    return {
      byBody,
      indexes,
      readable: reads.filter(read => read.recall).map(read => read.body),
      sources: reads.map(read => read.source),
      complete: indexes.every(index => index.complete),
    }
  }

  /** Provider health for the entity reads: a read in flight is shared, a finished one reused for less than maxAgeMs. */
  private entitySpaceStatus(body: MemorySpace, maxAgeMs: number): Promise<ProviderSpaceStatus> {
    const recent = this.entityStatusCache.get(body.id)
    if (recent !== undefined && (recent.settledAt === undefined || Date.now() - recent.settledAt < maxAgeMs)) return recent.status
    const entry: { settledAt: number | undefined; status: Promise<ProviderSpaceStatus> } = { settledAt: undefined, status: Promise.resolve(undefined as never) }
    entry.status = this.providerFor(body).status(body)
      .catch((error: unknown): ProviderSpaceStatus => ({ healthy: false, error: error instanceof Error ? error.message : String(error) }))
      .then(status => {
        entry.settledAt = Date.now()
        return status
      })
    this.entityStatusCache.set(body.id, entry)
    return entry.status
  }

  private entityIndexFor(body: MemorySpace, status: ProviderSpaceStatus): Promise<SpaceEntityIndex> {
    const fingerprint = entityIndexFingerprint(body, status)
    const cached = this.entityIndexCache.get(body.id)
    if (cached !== undefined && cached.fingerprint === fingerprint) {
      // Without statistics a finished index cannot be checked, so it is trusted only briefly.
      if (fingerprint !== undefined || cached.settledAt === undefined || Date.now() - cached.settledAt < ENTITY_INDEX_UNCHECKED_REUSE_MS) return cached.index
    }
    const entry: CachedEntityIndex = { fingerprint, settledAt: undefined, index: Promise.resolve(undefined as never) }
    entry.index = this.buildEntityIndex(body).then(index => {
      entry.settledAt = Date.now()
      return index
    }, (error: unknown) => {
      if (this.entityIndexCache.get(body.id) === entry) this.entityIndexCache.delete(body.id)
      throw error
    })
    this.entityIndexCache.set(body.id, entry)
    return entry.index
  }

  private async buildEntityIndex(body: MemorySpace): Promise<SpaceEntityIndex> {
    const provider = this.providerFor(body)
    if (provider.entityIndex !== undefined) {
      const owned = await provider.entityIndex(body)
      return buildSpaceEntityIndex(owned.memories.map(memory => this.entityMemory(memory, body)), owned.memories.length, owned.complete)
    }
    const listed = await provider.list(body, { limit: ENTITY_INDEX_LIST_LIMIT })
    return buildSpaceEntityIndex(listed.map(memory => this.entityMemory(memory, body)), listed.length, listed.length < ENTITY_INDEX_LIST_LIMIT)
  }

  /** A listed memory, not a query result: no relevance fields. */
  private entityMemory(memory: Insight, body: MemorySpace): Insight {
    const {
      score: _score, normalizedScore: _normalized, relevanceTier: _tier, federatedScore: _federated,
      confidence: _confidence, intent: _intent, matchedVia: _matchedVia, depth: _depth, edgeType: _edgeType, ...listed
    } = memory
    return this.annotate(listed, body)
  }

  private invalidateEntityIndex(memoryBodyId: string): void {
    this.entityIndexCache.delete(memoryBodyId)
    this.entityStatusCache.delete(memoryBodyId)
  }

  async remember(request: RememberRequest, signal?: AbortSignal): Promise<JsonValue> {
    this.assertWritable()
    const prepared = this.prepareRemember(request)
    const result = await this.providerFor(prepared.body).remember(prepared.body, prepared.request, signal)
    this.activateAfterWrite(prepared.body, mutationResultCommitted(result))
    return this.annotateResult(result, prepared.body)
  }

  /**
   * Persist a host-authorized set of exact memories without involving a model
   * in the data plane. Mnemon Native requests share one import per destination;
   * other Providers retain their adapter-defined write semantics.
   */
  async rememberMany(requests: readonly RememberRequest[], signal?: AbortSignal): Promise<JsonValue[]> {
    this.assertWritable()
    // Preparation is synchronous. Share destination resolution only within this batch.
    const destinations = new Map<string, MemorySpace>()
    const prepared = requests.map(request => this.prepareRemember(request, destinations))
    const results = new Array<JsonValue>(prepared.length)
    const groups = new Map<string, Array<PreparedRemember & { index: number }>>()
    for (const [index, entry] of prepared.entries()) {
      const group = groups.get(entry.body.id)
      if (group === undefined) groups.set(entry.body.id, [{ ...entry, index }])
      else group.push({ ...entry, index })
    }

    for (const group of groups.values()) {
      const body = group[0]!.body
      const provider = this.providerFor(body)
      let providerChanged = false
      const batchWriter = provider.rememberMany
      const batch = batchWriter === undefined
        ? []
        : group.filter(entry => !this.isNativeSpace(body) || entry.request.content.length <= 8_000)
      if (batchWriter !== undefined && batch.length > 0) {
        const written = await batchWriter.call(provider, body, batch.map(entry => entry.request), signal)
        if (written.length !== batch.length) throw new Error(`batch remember did not return one receipt per request for Memory Space ${body.id}`)
        for (const [offset, result] of written.entries()) {
          const entry = batch[offset]!
          results[entry.index] = this.annotateResult(result, body)
          providerChanged ||= mutationResultCommitted(result)
        }
      }
      const batched = new Set(batch)
      for (const entry of group) {
        if (batched.has(entry)) continue
        const result = await provider.remember(body, entry.request, signal)
        results[entry.index] = this.annotateResult(result, body)
        providerChanged ||= mutationResultCommitted(result)
      }
      this.activateAfterWrite(body, providerChanged)
    }

    return results
  }

  async related(id: string, depth = 2, edge?: EdgeType, signal?: AbortSignal, memoryBodyId?: string): Promise<Insight[]> {
    const body = this.readSpace(memoryBodyId)
    const selectedEdge = allowed(edge, EDGE_TYPES, 'edge')
    const provider = this.providerFor(body)
    if (provider.related === undefined || !body.provider.capabilities.related) throw new Error(`${body.provider.label} does not support related-memory traversal`)
    const results = await provider.related(body, required(id, 'id', 2000), integer(depth, 2, 1, 5), selectedEdge, signal)
    return results.map(entry => this.annotate(entry, body))
  }

  async link(sourceId: string, targetId: string, type: EdgeType = 'semantic', weight = 0.5, reason?: string, signal?: AbortSignal, memoryBodyId?: string): Promise<JsonValue> {
    this.assertWritable()
    const body = this.writeSpace(memoryBodyId)
    if (!Number.isFinite(weight) || weight < 0 || weight > 1) throw new Error('weight must be within 0..1')
    const selectedType = allowed(type, EDGE_TYPES, 'type') ?? 'semantic'
    const provider = this.providerFor(body)
    if (provider.link === undefined || !body.provider.capabilities.link) throw new Error(`${body.provider.label} does not support explicit memory links`)
    const result = await provider.link(
      body,
      required(sourceId, 'sourceId', 2000),
      required(targetId, 'targetId', 2000),
      selectedType,
      weight,
      reason === undefined || reason.trim() === '' ? undefined : required(reason, 'reason', 1000),
      signal,
    )
    this.activateAfterWrite(body, mutationResultCommitted(result))
    return this.annotateResult(result, body)
  }

  async forget(id: string, signal?: AbortSignal, memoryBodyId?: string): Promise<JsonValue> {
    this.assertWritable()
    const body = this.writeSpace(memoryBodyId)
    const provider = this.providerFor(body)
    if (provider.forget === undefined || !body.provider.capabilities.forget) throw new Error(`${body.provider.label} does not expose safe forget semantics in this integration`)
    const result = await provider.forget(body, required(id, 'id', 2000), signal)
    this.activateAfterWrite(body, mutationResultCommitted(result))
    return this.annotateResult(result, body)
  }

  prepareSpacePlacement(request: CreateMemorySpaceRequest): PreparedMemoryPlacement {
    if (request.placement === undefined) throw new Error('automatic provider placement request is required')
    if (request.providerId !== undefined) throw new Error('automatic provider placement cannot include a fixed providerId')
    return prepareMemoryPlacement(request.placement, this.memorySpaces.placementCandidates(request))
  }

  async createSpace(request: CreateMemorySpaceRequest, signal?: AbortSignal, placement?: MemoryPlacementDecision): Promise<MemorySpace> {
    this.assertWritable()
    return await this.memorySpaces.create(request, signal, placement)
  }

  /**
   * Create a Memory Space from the configured distillation policy. The model
   * may choose only among candidates already filtered by the host; manual mode
   * ignores model preference and always uses the configured fixed provider.
   */
  async createSpaceForPersistence(
    body: { name: string; description: string },
    selection: LlmMemoryPlacementSelection | undefined,
    signal?: AbortSignal,
    delegation: { runId: string; provider: string } = { runId: 'memory-write', provider: 'task-agent' },
  ): Promise<MemorySpace> {
    const strategy = this.config.persistenceStrategy
    if (strategy.mode === 'manual') {
      const providerId = this.persistenceProviderId()
      const connection = providerId === undefined ? undefined : strategy.providerConnections[providerId]
      return this.createSpace({
        ...body,
        ...(providerId === undefined ? {} : { providerId }),
        ...(providerId === undefined || this.isNativeProvider(providerId) || connection === undefined ? {} : { connection }),
      }, signal)
    }

    const request: CreateMemorySpaceRequest = {
      ...body,
      placement: {
        mode: 'automatic',
        ...(strategy.prompt === '' ? {} : { prompt: strategy.prompt }),
        rules: { ...strategy.rules },
      },
      ...(Object.keys(strategy.providerConnections).length === 0 ? {} : { providerConnections: strategy.providerConnections }),
    }
    const prepared = this.prepareSpacePlacement(request)
    const decision = rulesOnlyPlacement(prepared)
      ?? finalizeLlmPlacement(prepared, selection ?? { providerId: '', reason: '', confidence: '' }, delegation)
    return this.createSpace(request, signal, decision)
  }

  /** A chosen provider stays fixed; the built-in default follows whichever provider is ready. */
  private persistenceProviderId(): MemorySpace['provider']['id'] | undefined {
    const strategy = this.config.persistenceStrategy
    return strategy.providerDefaulted === true ? this.memorySpaces.defaultProviderId() : strategy.providerId
  }

  async updateProviderService(providerId: MemorySpace['provider']['id'], settings: Record<string, string | number | boolean>, clearSecrets: readonly string[] = [], enabled = true, signal?: AbortSignal) {
    this.assertWritable()
    if (this.isNativeProvider(providerId)) throw new Error('Mnemon Native service settings are managed by the native configuration')
    if (!enabled) return this.memorySpaces.updateProviderService(providerId, settings, clearSecrets, false)
    const connection = this.memorySpaces.resolveProviderService(providerId, settings, clearSecrets)
    const provider = this.providers.get(providerId)
    if (provider?.discover === undefined) throw new Error(`${this.providerCatalog.descriptor(providerId).label} does not support Memory Space discovery`)
    const discovered = await provider.discover(connection, signal)
    return this.memorySpaces.syncProviderService(providerId, connection, discovered)
  }

  updateSpace(id: string, request: UpdateMemorySpaceRequest): MemorySpace {
    this.assertWritable()
    return this.memorySpaces.update(id, request)
  }

  updateSpaceMetadata(updates: readonly MemorySpaceMetadataUpdate[]): MemorySpace[] {
    this.assertWritable()
    return this.memorySpaces.updateMetadata(updates)
  }

  async deleteSpace(id: string, signal?: AbortSignal): Promise<MemorySpace> {
    this.assertWritable()
    return await this.memorySpaces.remove(id, signal)
  }

  async mergeSpaces(targetSpaceId: string, sourceSpaceIds: string[], deactivateSources = true, signal?: AbortSignal): Promise<JsonValue> {
    this.assertWritable()
    const target = this.memorySpaces.get(targetSpaceId)
    if (!this.isNativeSpace(target)) throw new Error('memory-space merge currently requires a Mnemon Native target')
    const sourceIds = [...new Set(sourceSpaceIds.map(id => id.trim()).filter(id => id !== ''))]
    if (sourceIds.length === 0) throw new Error('sourceMemoryBodyIds requires at least one memory space')
    if (sourceIds.includes(target.id)) throw new Error('target memory space cannot also be a merge source')
    const sources = sourceIds.map(id => this.memorySpaces.get(id))
    if (sources.some(source => !this.isNativeSpace(source))) throw new Error('memory-space merge currently supports Mnemon Native sources only')
    const insights: Array<Record<string, JsonValue>> = []
    const edges: Array<Record<string, JsonValue>> = []
    for (const source of sources) {
      const offset = insights.length
      const sourceInsights = await this.providerFor(source).list(source, { limit: 100_000 }, signal)
      const indexById = new Map(sourceInsights.map((insight, index) => [insight.id, offset + index]))
      for (const insight of sourceInsights) {
        insights.push({
          content: insight.content,
          ...(insight.category === undefined ? {} : { category: insight.category }),
          ...(insight.importance === undefined ? {} : { importance: insight.importance }),
          ...(insight.tags === undefined ? {} : { tags: insight.tags }),
          ...(insight.entities === undefined ? {} : { entities: insight.entities }),
          ...(insight.source === undefined ? {} : { source: insight.source }),
          ...(insight.createdAt === undefined ? {} : { created_at: insight.createdAt }),
        })
      }
      const graph = await this.providerFor(source).graph(source, signal)
      for (const edge of graph.edges) {
        const sourceIndex = indexById.get(edge.sourceId)
        const targetIndex = indexById.get(edge.targetId)
        if (sourceIndex === undefined || targetIndex === undefined || edge.type === undefined) continue
        edges.push({ source_index: sourceIndex, target_index: targetIndex, edge_type: edge.type, weight: 0.5, reason: edge.label })
      }
    }
    if (insights.length === 0) {
      if (deactivateSources) {
        for (const source of sources) {
          if (!source.active) continue
          this.memorySpaces.setActive(source.id, false)
        }
      }

      return { action: 'merged', imported: 0, updated: 0, skipped: 0, edges_inserted: 0, targetMemoryBodyId: target.id }
    }
    const temporary = mkdtempSync(join(tmpdir(), 'dsh-mnemon-merge-'))
    const draftPath = join(temporary, 'memory-draft.json')
    try {
      writeFileSync(draftPath, JSON.stringify({ schema_version: '1', source: 'dsh-mnemon-merge', insights, edges }), { encoding: 'utf8', mode: 0o600 })
      const result = await this.runner.runJson(['import', draftPath], { ...(signal === undefined ? {} : { signal }), store: target.id })
      const summary = record(result)
      const counts = [summary?.imported, summary?.updated, summary?.skipped]
      const complete = mutationResultCommitted(result) && summary?.errors === 0
        && counts.every(value => typeof value === 'number' && Number.isInteger(value) && value >= 0)
        && counts.reduce<number>((sum, value) => sum + Number(value), 0) === insights.length
      this.activateAfterWrite(target, complete)
      if (deactivateSources && complete) {
        for (const source of sources) {
          if (!source.active) continue
          this.memorySpaces.setActive(source.id, false)
        }
      }

      const completion = mutationResultCompletion(result)
      return this.annotateResult({ ...summary, status: complete ? 'committed' : completion === 'committed' ? 'partial' : completion }, target)
    } finally {
      rmSync(temporary, { recursive: true, force: true })
    }
  }

  private providerFor(body: MemorySpace): MemoryProviderAdapter {
    const provider = this.providers.get(body.provider.id)
    if (provider === undefined) throw new Error(`unsupported memory provider: ${body.provider.id}`)
    return provider
  }

  private readSpaces(ids?: string[]): MemorySpace[] {
    if (ids === undefined || ids.length === 0) return this.memorySpaces.active()
    const requested = [...new Set(ids.map(id => id.trim()).filter(id => id !== ''))]
    const available = new Map<string, MemorySpace>()
    // One live catalog snapshot for the pinned read; preserve first-match lookup semantics.
    for (const body of this.memorySpaces.list()) if (!available.has(body.id)) available.set(body.id, body)
    return requested.map(id => {
      const normalized = validateMemorySpaceId(id)
      const body = available.get(normalized)
      if (body === undefined) throw new Error(`unknown memory space: ${normalized}`)
      if (!body.active) throw new Error(`memory space is not active for reading: ${id}`)
      if (!this.isNativeSpace(body) && !this.memorySpaces.providerServiceEnabled(body.provider.id)) throw new Error(`${body.provider.label} is disabled on the dsh-mnemon page under Plugins`)
      return body
    })
  }

  private readSpace(id?: string): MemorySpace {
    if (id !== undefined && id.trim() !== '') {
      const body = this.memorySpaces.get(id)
      if (!body.active) throw new Error(`memory space is not active for reading: ${body.id}`)
      if (!this.isNativeSpace(body) && !this.memorySpaces.providerServiceEnabled(body.provider.id)) throw new Error(`${body.provider.label} is disabled on the dsh-mnemon page under Plugins`)
      return body
    }
    const active = this.memorySpaces.active()
    if (active.length !== 1) throw new Error('memoryBodyId is required when the number of active memory spaces is not exactly one')
    return active[0]!
  }

  private writeSpace(id?: string): MemorySpace {
    if (id !== undefined && id.trim() !== '') {
      const body = this.memorySpaces.get(id)
      if (!this.isNativeSpace(body) && !this.memorySpaces.providerServiceEnabled(body.provider.id)) throw new Error(`${body.provider.label} is disabled on the dsh-mnemon page under Plugins`)
      return body
    }
    const active = this.memorySpaces.active()
    if (active.length !== 1) throw new Error('memoryBodyId is required when the number of active memory spaces is not exactly one')
    return active[0]!
  }

  private prepareRemember(request: RememberRequest, destinations?: Map<string, MemorySpace>): PreparedRemember {
    const key = request.memoryBodyId?.trim() ?? ''
    const body = destinations?.get(key) ?? this.writeSpace(request.memoryBodyId)
    destinations?.set(key, body)
    // Runtime entries are capped at 8 KiB. Keep the service boundary large
    // enough for the Host to archive any valid hot-memory entry byte-for-byte;
    // the UI remains at its existing 8,000-character limit.
    const content = required(request.content, 'content', 8 * 1024)
    const importance = integer(request.importance, 3, 1, 5)
    const category = allowed(request.category, CATEGORIES, 'category') ?? 'general'
    const source = allowed(request.source, SOURCES, 'source') ?? 'user'
    const tags = commaList(request.tags, 'tags', 20)?.split(',')
    const entities = commaList(request.entities, 'entities', 50)?.split(',')
    return {
      body,
      request: {
        content,
        importance,
        category,
        source,
        memoryBodyId: body.id,
        ...(tags === undefined ? {} : { tags }),
        ...(entities === undefined ? {} : { entities }),
      },
    }
  }

  private annotate<T extends Insight>(insight: T, body: MemorySpace): T {
    return {
      ...insight,
      memoryBodyId: body.id,
      memoryBodyName: body.name,
      memoryProviderId: body.provider.id,
      memoryProviderLabel: body.provider.label,
      memoryCapabilities: body.provider.capabilities,
    }
  }

  private annotateResult(result: JsonValue, body: MemorySpace): JsonValue {
    const value = record(result)
    return value === undefined ? result : {
      ...value,
      memoryBodyId: body.id,
      memoryBodyName: body.name,
      memoryProviderId: body.provider.id,
      memoryProviderLabel: body.provider.label,
    }
  }

  private activateAfterWrite(body: MemorySpace, providerChanged: boolean): void {
    // Even an unconfirmed write may have reached the Provider; rebuild the index on the next read.
    this.invalidateEntityIndex(body.id)
    if (!providerChanged) return
    if (!body.active) this.memorySpaces.setActive(body.id, true)
    else this.memorySpaces.touch(body.id)
  }

  private assertWritable(): void {
    if (!this.config.writeEnabled) throw new Error('dsh-mnemon is configured read-only (writeEnabled: false)')
  }
}
