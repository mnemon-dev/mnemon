/**
 * Contracts owned by the Memory Spaces Source, not the Mnemon Core.
 * The canonical model is MemorySpace. Existing memoryBodyId(s), memoryBodies,
 * and related wire fields keep their v0.5.x spelling for installed consumers
 * and persisted Document/Pack lineage; they all refer to memory spaces.
 */
export type { MemoryJsonValue as JsonValue } from 'dsh-mnemon/contracts'
import type { MemoryReadGrant } from 'dsh-mnemon/contracts'

export type MemoryProviderId = string

export type MemoryProviderConnectionValue = string | number | boolean
export type MemoryProviderConnection = Record<string, MemoryProviderConnectionValue>

export interface MemoryProviderIcon {
  /** Bundled brand token, self-contained image data, or a short text glyph. */
  kind: 'brand' | 'data-url' | 'glyph'
  value: string
}

export interface MemoryProviderConfigOption {
  value: string
  label: string
  /** Optional Host-provided translation key; clients fall back to label. */
  i18nKey?: string
}

export interface MemoryProviderConfigField {
  key: string
  label: string
  /** Optional Host-provided translation key; clients fall back to label. */
  i18nKey?: string
  /** Service fields are configured once on the dsh-mnemon page under Plugins; memory fields belong to each Memory Space. */
  scope: 'service' | 'memory'
  /** A reusable local data location presented with the same default/custom scope UI as Mnemon Native. */
  role?: 'global-location'
  input: 'text' | 'url' | 'secret' | 'number' | 'boolean' | 'select' | 'path'
  required: boolean
  defaultValue?: MemoryProviderConnectionValue
  placeholder?: string
  help?: string
  min?: number
  max?: number
  maxLength?: number
  pattern?: string
  normalize?: 'trim-trailing-slash'
  validationMessage?: string
  /** Seed an empty service default from this field on the first discovered Memory Space. */
  discoveryDefaultFrom?: string
  options?: MemoryProviderConfigOption[]
}

export type MemoryPlacementCapability = 'graph' | 'entities' | 'related' | 'exact-write' | 'link' | 'forget'
export type MemoryPlacementPreference = 'balanced' | 'local-first' | 'shared-first'

export interface MemoryPlacementRules {
  allowedProviderIds?: MemoryProviderId[]
  dataBoundary?: 'allow-remote' | 'local-only'
  requiredCapabilities?: MemoryPlacementCapability[]
  preference?: MemoryPlacementPreference
}

/** Persistent policy for provider selection during Agent-supervised memory distillation. */
export interface MemoryPersistenceStrategy {
  mode?: 'manual' | 'automatic'
  /** Fixed provider in manual mode. */
  providerId?: MemoryProviderId
  /** User-authored guidance used only after hard rules have filtered automatic candidates. */
  prompt?: string
  rules?: MemoryPlacementRules
  /** Memory-level connection values for providers that may be selected by the policy. */
  providerConnections?: Partial<Record<MemoryProviderId, MemoryProviderConnection>>
}

export interface ResolvedMemoryPersistenceStrategy {
  mode: 'manual' | 'automatic'
  providerId: MemoryProviderId
  /** Set when no provider was chosen; manual mode then uses whichever provider is ready. */
  providerDefaulted?: true
  prompt: string
  rules: {
    allowedProviderIds: MemoryProviderId[]
    dataBoundary: 'allow-remote' | 'local-only'
    requiredCapabilities: MemoryPlacementCapability[]
    preference: MemoryPlacementPreference
  }
  providerConnections: Partial<Record<MemoryProviderId, MemoryProviderConnection>>
}

export interface AutomaticMemoryPlacementRequest {
  mode: 'automatic'
  /** User-authored routing guidance. Hard rules above always take precedence. */
  prompt?: string
  rules?: MemoryPlacementRules
}

export interface MemoryPlacementDecision {
  mode: 'automatic'
  providerId: MemoryProviderId
  decidedBy: 'rules' | 'llm'
  reason: string
  confidence: 'high' | 'medium' | 'low'
  candidateProviderIds: MemoryProviderId[]
  appliedRules: string[]
  decidedAt: string
  runId?: string
  subagentProvider?: string
}

export interface MemoryProviderCapabilities {
  search: boolean
  browse: boolean
  graph: boolean
  entities: boolean
  related: boolean
  remember: boolean
  link: boolean
  forget: boolean
  writeMode: 'exact' | 'async-extracting'
  deletionMode: 'soft' | 'hard' | 'unsupported'
}

export interface MemoryProviderDescriptor {
  /** Provider implementation type; omitted when it is identical to id. */
  typeId?: string
  id: MemoryProviderId
  label: string
  icon?: MemoryProviderIcon
  kind: 'local' | 'remote'
  /** How the provider data scope reacts when DSH switches workspaces. */
  workspaceBinding: 'automatic' | 'optional-override' | 'provider-global'
  summary: string
  /** Optional Host-provided translation key; clients fall back to summary. */
  summaryI18nKey?: string
  origin: 'native' | 'third-party'
  capabilities: MemoryProviderCapabilities
  fields: MemoryProviderConfigField[]
  /** Runtime projection: whether this scope has a usable saved service configuration. */
  serviceConfigured?: boolean
}

export interface MemoryProviderServiceView {
  providerId: MemoryProviderId
  enabled: boolean
  configured: boolean
  settings: MemoryProviderConnection
  configuredSecrets: string[]
  /** Present only on the settings RPC so its password inputs can use native reveal/hide behavior. */
  secretValues?: MemoryProviderConnection
}

export interface MemoryProviderServiceCatalog {
  providers: MemoryProviderDescriptor[]
  items: MemoryProviderServiceView[]
  generatedAt: string
}

export interface UpdateMemoryProviderServiceRequest {
  providerId: MemoryProviderId
  settings: MemoryProviderConnection
  enabled?: boolean
  clearSecrets?: string[]
}

export interface MemoryProviderRuntimeStatus {
  providerId: MemoryProviderId
  label: string
  icon?: MemoryProviderIcon
  enabled: boolean
  configured: boolean
  status: 'disabled' | 'idle' | 'healthy' | 'unhealthy'
  memoryBodyCount: number
  activeMemoryBodyCount: number
  error?: string
}

export interface MemorySpaceProvider {
  id: MemoryProviderId
  /** Implementation type; differs from id when one module has several child instances. */
  typeId?: string
  label: string
  icon?: MemoryProviderIcon
  origin?: 'native' | 'third-party'
  kind: 'local' | 'remote'
  location: string
  targetUri?: string
  account?: string
  user?: string
  actorPeerId?: string
  apiKeyConfigured: boolean
  settings: MemoryProviderConnection
  configuredSecrets: string[]
  capabilities: MemoryProviderCapabilities
}

export interface OpenVikingSpaceConnection {
  endpoint: string
  targetUri: string
  apiKey?: string
  account?: string
  user?: string
  actorPeerId?: string
}

export interface MemorySpace {
  id: string
  name: string
  description: string
  active: boolean
  dbPath: string
  provider: MemorySpaceProvider
  placement?: MemoryPlacementDecision
  createdAt: string
  updatedAt: string
}

export interface CreateMemorySpaceRequest {
  name: string
  description: string
  active?: boolean
  providerId?: MemoryProviderId
  connection?: MemoryProviderConnection
  /** Candidate-specific settings used only while resolving automatic placement. */
  providerConnections?: Partial<Record<MemoryProviderId, MemoryProviderConnection>>
  openViking?: OpenVikingSpaceConnection
  placement?: AutomaticMemoryPlacementRequest
}

export interface UpdateMemorySpaceRequest {
  name?: string
  description?: string
  active?: boolean
  connection?: MemoryProviderConnection
  clearSecrets?: string[]
  openViking?: Partial<OpenVikingSpaceConnection> & { clearApiKey?: boolean }
}

export interface MemorySpaceMetadataUpdate {
  memoryBodyId: string
  title: string
  description: string
}

export interface MemorySpaceMetadataMaintenanceResult {
  delegated: true
  runId: string
  provider: string
  summary: string
  updates: MemorySpaceMetadataUpdate[]
}

export type Category = 'preference' | 'decision' | 'fact' | 'insight' | 'context' | 'general'
export const CATEGORIES = ['preference', 'decision', 'fact', 'insight', 'context', 'general'] as const satisfies readonly Category[]
export type Source = 'user' | 'agent' | 'external'
export const SOURCES = ['user', 'agent', 'external'] as const satisfies readonly Source[]
export type EdgeType = 'temporal' | 'semantic' | 'causal' | 'entity'
export const EDGE_TYPES = ['temporal', 'semantic', 'causal', 'entity'] as const satisfies readonly EdgeType[]
export type Intent = 'WHY' | 'WHEN' | 'ENTITY' | 'GENERAL'
export const INTENTS = ['WHY', 'WHEN', 'ENTITY', 'GENERAL'] as const satisfies readonly Intent[]

export type RecallRelevanceTier = 'high' | 'medium' | 'low' | 'unknown'

/**
 * One spelling-insensitive key per entity. The Host's entity index and the Client's graph
 * both count an entity's memories by it, so their numbers agree.
 */
export function normalizeEntityKey(entity: string): string {
  return entity.normalize('NFKC').trim().toLocaleLowerCase()
}

export interface Insight {
  id: string
  content: string
  category?: string
  importance?: number
  tags?: string[]
  entities?: string[]
  source?: string
  score?: number
  /** Policy-normalized query relevance; absent for unknown Provider score scales. */
  normalizedScore?: number
  /** Query relevance assigned by the active deterministic recall quality policy. */
  relevanceTier?: RecallRelevanceTier
  /** Comparable rank score only when results were fused across providers. */
  federatedScore?: number
  confidence?: string
  intent?: string
  matchedVia?: string
  createdAt?: string
  depth?: number
  edgeType?: string
  memoryBodyId?: string
  memoryBodyName?: string
  memoryProviderId?: MemoryProviderId
  /** Provider label captured with the result so Client UI never needs an id catalog. */
  memoryProviderLabel?: string
  /** Owning Provider capabilities at read time; safe to expose to clients. */
  memoryCapabilities?: MemoryProviderCapabilities
  externalUri?: string
}

export interface SearchRequest {
  query: string
  mode?: 'smart' | 'keyword' | 'basic'
  limit?: number
  category?: Category
  source?: Source
  intent?: Intent
  memoryBodyIds?: string[]
}

export interface RememberRequest {
  content: string
  category?: Category
  importance?: number
  tags?: string[]
  entities?: string[]
  source?: Source
  memoryBodyId?: string
}

export interface MemorySpaceStats {
  totalInsights: number
  deletedInsights: number
  edgeCount: number
  oplogCount: number
  dbSizeBytes: number
  byCategory: Record<string, number>
  topEntities: Array<{ entity: string; count: number }>
}

export interface MemorySpaceView extends MemorySpace {
  /** True when Mnemon's persisted active-file selection points to this Store. */
  mnemonDefault: boolean
  /** False when an external provider is disabled while its Memory Space registration remains preserved. */
  providerEnabled?: boolean
  healthy: boolean
  /** A fast directory response is visible while provider health resolves independently. */
  statusLoading?: boolean
  error?: string
  stats?: MemorySpaceStats
}

/** Optional Host-only body-directory input; the Host binds the grant to its initiating View and exact Source. */
export interface MemorySpaceWriteScopeRequest {
  viewId: string
  grant: MemoryReadGrant
}

/** Source-owned write authority, distinct from the active namespaces pinned for recall. */
export interface MemorySpaceWriteScope {
  viewId: string
  sourceInstanceKey: string
  memoryBodyIds: string[]
}

export interface MemorySpaceCatalog {
  items: MemorySpaceView[]
  providers: MemoryProviderDescriptor[]
  /** Sanitized policy exposed to memory workers; provider connection values are never included. */
  persistenceStrategy?: Omit<ResolvedMemoryPersistenceStrategy, 'providerConnections'>
  total: number
  activeCount: number
  directory: string
  generatedAt: string
  /** Present only when body-directory receives a supported writeScope request. Older Sources omit it. */
  writeScope?: MemorySpaceWriteScope
}

export interface MemoryGraphNode extends Insight {
  color: string
  graphId?: string
  kind?: 'memory' | 'entity' | 'space'
  memoryBodyIds?: string[]
  memoryBodyNames?: string[]
  occurrenceCount?: number
}

export interface MemoryGraphEdge {
  sourceId: string
  targetId: string
  label: string
  color: string
  type?: EdgeType | 'scope'
}

export interface MemoryGraphSnapshot {
  nodes: MemoryGraphNode[]
  edges: MemoryGraphEdge[]
  generatedAt: string
  memoryBodies?: Array<Pick<MemorySpace, 'id' | 'name' | 'active'>>
  /** Per-space observation state for capability-aware overview rendering. */
  sources?: MemoryReadSource[]
}

export type MemoryReadMode = 'search' | 'graph' | 'projection' | 'enumerable' | 'query-only' | 'entities' | 'unsupported'
/** 'loading' is client-only: a page shows its spaces from the directory while the read runs. */
export type MemoryReadStatus = 'ready' | 'empty' | 'query-required' | 'unsupported' | 'unavailable' | 'loading'

/**
 * One provider-backed Memory Space participating in a read surface.
 *
 * The mode describes what the provider can truthfully expose; status describes
 * the result of this particular read. Keeping those dimensions separate lets
 * the UI distinguish an empty graph from a flat projection, a query-only
 * engine, and an unavailable connection.
 */
export interface MemoryReadSource {
  memoryBodyId: string
  memoryBodyName: string
  providerId: MemoryProviderId
  providerLabel: string
  mode: MemoryReadMode
  status: MemoryReadStatus
  itemCount: number
  edgeCount?: number
  /** Entity reads: the memories the entity index covers in this space. */
  memoryCount?: number
  /** Entity reads: false when the Provider could not index every memory of this space. */
  complete?: boolean
  hint?: string
  quality?: RecallQualityStats
}

export interface RecallQualityStats {
  policyId: string
  fallbackFrom?: string
  fetched: number
  retained: number
  selected: number
  droppedLowScore: number
  droppedNonPositiveScore: number
  droppedInvalidScore: number
  unscored: number
  unscaled: number
}

export interface MemoryListRequest {
  query?: string
  category?: Category
  limit?: number
  memoryBodyIds?: string[]
}

export interface MemoryListView {
  items: MemoryGraphNode[]
  total: number
  generatedAt: string
  /** Omitted only when talking to a pre-provider-aware Host. */
  sources?: MemoryReadSource[]
}

export interface MemorySpaceMetadataSample {
  memoryBodyId: string
  name: string
  description: string
  providerId: MemorySpace['provider']['id']
  providerLabel: string
  method: 'native-basic' | 'browse' | 'search'
  evidence: Array<Pick<Insight, 'content' | 'category' | 'entities'>>
}

export interface MemoryPlacementCandidate {
  id: MemoryProviderId
  label: string
  kind: 'local' | 'remote'
  configured: boolean
  summary: string
  capabilities: MemoryProviderCapabilities
}

export interface PreparedMemoryPlacement {
  prompt: string
  candidates: MemoryPlacementCandidate[]
  appliedRules: string[]
  selectorBrief: string
}

export interface LlmMemoryPlacementSelection {
  providerId: string
  reason: string
  confidence: string
}

export const DEFAULT_EMBEDDING_ENDPOINT = 'http://localhost:11434'
export const DEFAULT_EMBEDDING_MODEL = 'nomic-embed-text'
export const EMBEDDING_PROTOCOL_AUTO = 'auto'
export const EMBEDDING_PROTOCOL_OLLAMA = 'ollama'
export const EMBEDDING_PROTOCOL_OPENAI = 'openai'
/** Default protocol defers to Mnemon's /v1 auto-detection. */
export const DEFAULT_EMBEDDING_PROTOCOL = EMBEDDING_PROTOCOL_AUTO
/** Wire protocols accepted by the embedding protocol override; single source for schema, resolver, and UI validation. */
export const MNEMON_EMBEDDING_PROTOCOLS = [EMBEDDING_PROTOCOL_AUTO, EMBEDDING_PROTOCOL_OLLAMA, EMBEDDING_PROTOCOL_OPENAI] as const


export interface RecallQualityConfig {
  /** Registered deterministic policy id. */
  policy?: string
  lowScoreThreshold?: number
  highScoreThreshold?: number
  /** Provider candidate expansion before quality filtering, from 1 through 5. */
  candidateMultiplier?: number
  /** Maximum medium-relevance rows admitted by the strict policy. */
  maxMediumResults?: number
  /** Maximum unknown-scale or unscored rows admitted by the strict policy. */
  maxUnknownResults?: number
}


export interface ResolvedRecallQualityConfig {
  policy: string
  lowScoreThreshold: number
  highScoreThreshold: number
  candidateMultiplier: number
  maxMediumResults: number
  maxUnknownResults: number
}


export type MnemonEmbeddingProtocol = (typeof MNEMON_EMBEDDING_PROTOCOLS)[number]

export interface MnemonEmbeddingConfig {
  /** When false or omitted, Mnemon keeps its inherited environment and built-in defaults. */
  enabled?: boolean
  endpoint?: string
  model?: string
  /** Optional Bearer token forwarded as MNEMON_EMBED_API_KEY for OpenAI-compatible endpoints. */
  apiKey?: string
  /** Explicit wire-protocol override; 'auto' keeps Mnemon's /v1 auto-detection. */
  protocol?: MnemonEmbeddingProtocol
}

export interface ResolvedMnemonEmbeddingConfig {
  enabled: boolean
  endpoint: string
  model: string
  apiKey: string
  protocol: MnemonEmbeddingProtocol
}


export interface MnemonEmbeddingStatus {
  available: boolean
  model: string
  /** Protocol Mnemon resolved for the embedding endpoint; omitted when the CLI does not report one. */
  protocol?: string
  totalInsights: number
  embedded: number
  coverage: string
}


/** Domain status only; workbench/lifecycle status belongs to the default adapter. */
export interface MemorySpacesStatus {
  healthy: boolean
  error?: string
  version?: string
  cliPath: string
  commandFound: boolean
  dataDir: string
  store: string
  mnemonDefaultStore: string
  dshActiveStores: string[]
  writeEnabled: boolean
  timeoutMs: number
  defaultRecallLimit: number
  recallQuality: ResolvedRecallQualityConfig
  memoryBodyDirectory: string
  memoryBodies: MemorySpaceView[]
  providerServices?: MemoryProviderRuntimeStatus[]
  stats?: MemorySpaceStats & { dbPath?: string }
}

/**
 * The entities of the active spaces. Each count is the number of memories that
 * carry the entity, the same set entity-memories lists.
 */
export interface EntityView {
  items: Array<{ entity: string; count: number }>
  /** With a selected entity: the first page of entity-memories, kept for existing callers. */
  insights: Insight[]
  selected?: string
  /** Omitted only when talking to a pre-provider-aware Host. */
  sources?: MemoryReadSource[]
  /** Distinct entities across the active spaces; items may be capped below it. Omitted by older Hosts. */
  total?: number
  /** False when a space's Provider could not index every memory. Omitted by older Hosts. */
  complete?: boolean
}

/** One page of the memories that carry an entity, by importance and then recency. */
export interface EntityMemoriesView {
  /** The entity's most common spelling, or the requested name when no memory carries it. */
  entity: string
  total: number
  offset: number
  items: Insight[]
  complete: boolean
  sources: MemoryReadSource[]
}

/** Memories recall relates to an entity that do not carry it themselves. */
export interface EntityRelatedView {
  entity: string
  items: Insight[]
  sources: MemoryReadSource[]
}

// Published type spellings retained for independently installed v0.5.x consumers.
/** @deprecated Use CreateMemorySpaceRequest. The wire shape is unchanged. */
export type CreateMemoryBodyRequest = CreateMemorySpaceRequest
/** @deprecated Use UpdateMemorySpaceRequest. The wire shape is unchanged. */
export type UpdateMemoryBodyRequest = UpdateMemorySpaceRequest
/** @deprecated Use MemorySpace. The wire shape is unchanged. */
export type MemoryBody = MemorySpace
/** @deprecated Use MemorySpaceCatalog. The wire shape is unchanged. */
export type MemoryBodyCatalog = MemorySpaceCatalog
/** @deprecated Use MemorySpaceMetadataMaintenanceResult. The wire shape is unchanged. */
export type MemoryBodyMetadataMaintenanceResult = MemorySpaceMetadataMaintenanceResult
/** @deprecated Use MemorySpaceMetadataSample. The wire shape is unchanged. */
export type MemoryBodyMetadataSample = MemorySpaceMetadataSample
/** @deprecated Use MemorySpaceMetadataUpdate. The wire shape is unchanged. */
export type MemoryBodyMetadataUpdate = MemorySpaceMetadataUpdate
/** @deprecated Use MemorySpaceProvider. The wire shape is unchanged. */
export type MemoryBodyProvider = MemorySpaceProvider
/** @deprecated Use MemorySpaceStats. The wire shape is unchanged. */
export type MemoryBodyStats = MemorySpaceStats
/** @deprecated Use MemorySpaceView. The wire shape is unchanged. */
export type MemoryBodyView = MemorySpaceView
/** @deprecated Use OpenVikingSpaceConnection. The wire shape is unchanged. */
export type OpenVikingBodyConnection = OpenVikingSpaceConnection
