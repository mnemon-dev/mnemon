/** Starter Entry ids remain reserved when DSH nests them under an include. */
export function isDefaultSourceInstance(instanceKey: string, sourceTypeId: string): boolean {
  return instanceKey.startsWith('source:') && instanceKey.endsWith(':mnemon-source-' + sourceTypeId)
}
export type MemoryParticipationMode = 'off' | 'manual' | 'automatic'
export type MemoryParticipationChannel = 'recall' | 'write' | 'projection' | 'maintenance'
export type MemoryLayerParticipation = Record<MemoryParticipationChannel, MemoryParticipationMode>
export interface MemoryTopologyDefinition { id: string; strategyId: string; layers: Array<ResolvedMemoryLayerConfig & { id: string }> }
export interface MemoryCompositionStatus {
  /** Whether a composition serves new turns. A rejected change can leave the previous one serving. */
  serving: boolean
  /** The main Strategy composing the serving composition, which a fallback can make differ from the selected one. */
  strategyTypeId?: string
  evaluation: import('../core/contracts/index.ts').MemoryCompositionEvaluationReport
  sources: MemorySourceManagementInstance[]
  configuration: ResolvedMemoryTopologyConfig
}

import type { RecallQualityConfig, ResolvedRecallQualityConfig, MnemonEmbeddingConfig, ResolvedMnemonEmbeddingConfig } from 'dsh-mnemon-source-memory-spaces/contracts'
import type {
  MemoryPersistenceStrategy,
  ResolvedMemoryPersistenceStrategy,
  MemoryProviderRuntimeStatus,
  Source,
  MemoryBodyStats as MemorySpaceStats,
  MemoryBodyView as MemorySpaceView,
} from 'dsh-mnemon-source-memory-spaces/contracts'
import type { ConnectionHandle as DshClientConnectionHandle } from '@deepseek-ai/dsh-client-connection/client'
import type { DocumentSnapshot } from 'dsh-mnemon-source-documents/contracts'

export const MNEMON_READ_CHANNEL = '/dsh-mnemon-read'
export const MNEMON_ACTIVATION_CHANNEL = '/dsh-mnemon-activation'
export const MNEMON_WRITE_CHANNEL = '/dsh-mnemon-write'
export const MNEMON_PACK_CHANNEL = '/dsh-mnemon-pack'
export const MNEMON_SETTINGS_CHANNEL = '/dsh-mnemon-settings'
/** DSH API Gateway endpoints used by paired remote Web clients. */
export const MNEMON_REMOTE_CHANNEL = '/api'
export const MNEMON_REMOTE_NAMESPACE = 'dshMnemon'
export const MNEMON_REMOTE_READ_ENDPOINT = `${MNEMON_REMOTE_NAMESPACE}/read`
export const MNEMON_REMOTE_ACTIVATION_ENDPOINT = `${MNEMON_REMOTE_NAMESPACE}/activation`
export const MNEMON_REMOTE_WRITE_ENDPOINT = `${MNEMON_REMOTE_NAMESPACE}/write`
export const MNEMON_REMOTE_PACK_ENDPOINT = `${MNEMON_REMOTE_NAMESPACE}/pack`
export const MNEMON_REMOTE_SETTINGS_ENDPOINT = `${MNEMON_REMOTE_NAMESPACE}/settings`
export const MNEMON_REMOTE_VIEW_ENDPOINT = `${MNEMON_REMOTE_NAMESPACE}/view`
export const MNEMON_REMOTE_VIEW_WRITE_ENDPOINT = `${MNEMON_REMOTE_NAMESPACE}/viewWrite`
export const MNEMON_SETTINGS_NAMESPACE = 'mnemon'
export const MNEMON_UI_SETTINGS_NAMESPACE = 'mnemon-ui'
export * from './view-protocol.ts'
export type JsonPrimitive = string | number | boolean | null
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue }

export interface MemorySourceManagementField {
  key: string
  label: string
  description?: string
  input: 'text' | 'number' | 'boolean' | 'url' | 'secret' | 'select'
  required: boolean
  secret?: boolean
  options?: Array<{ value: string; label: string }>
}

/** Browser-safe descriptor of one Source instance visible in the current scope. */
export interface MemorySourceManagementInstance {
  sourceInstanceKey: string
  sourceTypeId: string
  packageName: string
  role: string
  availability: 'ready' | 'degraded' | 'unavailable'
  revision: string
  capabilities: string[]
  assistance?: readonly string[]
  management: {
    label: string
    description: string
    fields?: MemorySourceManagementField[]
    diagnostics?: string[]
  }
  hints?: JsonValue
}

export interface MemorySourceManagementCatalog {
  generationId: string
  sources: MemorySourceManagementInstance[]
}

export interface MemorySourceManagementResult {
  revision: string
  value: JsonValue
}

export type RpcError =
  | { code: 'bad-request'; message: string; details: { issues: JsonValue[] } }
  | { code: 'settings-rejected'; message: string; details: { ns: string } }
  | { code: 'internal'; message: string; details: Record<string, never> }

export type RpcResult<T = JsonValue> =
  | { ok: true; value: T }
  | { ok: false; error: RpcError }

/** Public DSH browser RPC face plus the transport boundary needed to gate local-only writes. */
export type ClientConnectionHandle = Pick<DshClientConnectionHandle, 'rpc' | 'isLoopback'>

export interface ClientSettingsSnapshot<T> {
  status: 'loading' | 'ready' | 'unavailable'
  value?: T
  base?: unknown
  user?: unknown
  revision?: number
  writable: boolean
  mode: 'host' | 'memory'
}

export interface ClientSettingsScope<T> {
  getSnapshot(): ClientSettingsSnapshot<T>
  subscribe(listener: () => void): () => void
  mutate(ops: SettingsOperation[]): Promise<void>
}

export type SettingsOperation = { op: 'set'; path: string[]; value: unknown } | { op: 'unset'; path: string[] }

export type StorageScopeKind = 'global' | 'workspace' | 'custom' | 'workspaces'

export function isWorkspaceStorageScope(scope: string | undefined): boolean {
  return scope === 'workspace' || scope === 'workspaces'
}

export interface MemoryLayerConfig {
  enabled?: boolean
  participation?: Partial<MemoryLayerParticipation>
  adapterIds?: string[]
}

export interface MemoryTopologyConfig {
  id?: string
  strategyId?: string
  layers?: Record<string, MemoryLayerConfig>
}

export interface ResolvedMemoryLayerConfig {
  enabled: boolean
  participation: MemoryLayerParticipation
  adapterIds: string[]
}

export interface ResolvedMemoryTopologyConfig {
  id: string
  strategyId: string
  layers: Record<string, ResolvedMemoryLayerConfig>
}

export interface CustomPackConfig {
  id: string
  name: string
  dataDir: string
}

export interface RuntimeMemoryConfig {
  /** Maximum UTF-8 bytes projected into MEMORY.md. */
  memoryLimitBytes?: number
  /** Maximum UTF-8 bytes projected into USER.md. */
  userLimitBytes?: number
  /** Completion-token budget for Runtime migration and compaction workers. */
  maintenanceMaxTokens?: number
}

export interface ResolvedRuntimeMemoryConfig {
  memoryLimitBytes: number
  userLimitBytes: number
  maintenanceMaxTokens: number
}

export { normalizeDisplayMode } from './display-mode.ts'
export type MnemonDisplayMode = 'sidebar' | 'builtin'

export const DEFAULT_IDLE_REVIEW = {
  enabled: true,
  runtimeMemory: true,
  provider: 'spawn',
  fallback: 'spawn',
  agentTeams: 'pause',
  minIntervalMs: 300_000,
  maxPerSession: 20,
  maxContextChars: 24_000,
  maxTokens: 4_096,
} satisfies ResolvedIdleReviewConfig

export interface ResolvedIdleReviewConfig {
  enabled: boolean
  /** Whether review may change USER.md and MEMORY.md; it can create Documents either way. */
  runtimeMemory: boolean
  provider: 'spawn' | 'fork'
  /** Applies only before a child starts; a failed run is never replayed. */
  fallback: 'spawn' | 'skip'
  /** Explicit opt-in for compatible Team policies; tool isolation is mandatory. */
  agentTeams: 'pause' | 'scoped'
  minIntervalMs: number
  maxPerSession: number
  maxContextChars: number
  maxTokens: number
}

export interface Config {
  storageScope?: StorageScopeKind
  /** Whether USER.md follows the selected storage root or stays in the global root. */
  runtimeUserScope?: 'storage' | 'global'
  cliPath?: string
  dataDir?: string
  customPackId?: string
  customPacks?: CustomPackConfig[]
  store?: string
  timeoutMs?: number
  defaultRecallLimit?: number
  runtimeMemory?: RuntimeMemoryConfig
  /** Optional DSH-owned overrides injected into every Mnemon CLI process. */
  embedding?: MnemonEmbeddingConfig
  memoryTopology?: MemoryTopologyConfig
  /** Profile-owned plugin choices on DSH 0.1.7 and newer. */
  memoryView?: import('./view-protocol.ts').MemoryViewPreferences
  /** Successful import of this profile's removed settings namespaces. */
  legacySettingsImported?: boolean
  recallQuality?: RecallQualityConfig
  routingGuidance?: boolean
  /** Entry placement only. Legacy `buildin` input is migrated to `builtin`. */
  displayMode?: MnemonDisplayMode | 'buildin'
  tabEnabled?: boolean
  writeEnabled?: boolean
  /** Remote management grant for paired pages; loopback pages keep full access. */
  remoteAccess?: 'read-only' | 'trusted-host'
  lifecycleEnabled?: boolean
  recallMode?: 'guided' | 'off'
  writebackMode?: 'guided' | 'off'
  idleReviewMs?: number
  idleReview?: Partial<ResolvedIdleReviewConfig>
  conversationInteraction?: {
    toolviews?: boolean
    turnBar?: boolean
    saveAction?: boolean
  }
  /** Provider policy used when an Agent must create a new Memory Space while distilling memory. */
  persistenceStrategy?: MemoryPersistenceStrategy
  /** Model route used by clean, session-independent maintenance Agents. */
  taskAgentModel?: TaskAgentModelConfig
}

export interface TaskAgentModelConfig {
  mode?: 'inherit' | 'fixed'
  provider?: string
  model?: string
}

export interface ResolvedTaskAgentModelConfig {
  mode: 'inherit' | 'fixed'
  provider?: string
  model?: string
}

export interface TaskAgentModelCatalogModel {
  id: string
  name: string
  description?: string
  /** DSH's merge-extensible model modality ids; absent means capability is unknown. */
  inputModalities?: string[]
}

export interface TaskAgentModelCatalogGroup {
  id: string
  name: string
  models: TaskAgentModelCatalogModel[]
}

export interface TaskAgentModelCatalogFailure {
  id: string
  name: string
  message: string
}

export interface TaskAgentModelCatalog {
  effective?: { provider: string; model: string; source: 'fixed' | 'dsh-default' | 'active-agent' }
  defaultSelection?: { provider: string; model: string }
  groups: TaskAgentModelCatalogGroup[]
  failures: TaskAgentModelCatalogFailure[]
}

export interface InteractionConfig {
  turnBar?: boolean
  saveAction?: boolean
}

export interface ResolvedConfig {
  storageScope: StorageScopeKind
  runtimeUserScope: 'storage' | 'global'
  cliPath?: string
  dataDir?: string
  store?: string
  timeoutMs: number
  defaultRecallLimit: number
  runtimeMemory: ResolvedRuntimeMemoryConfig
  embedding: ResolvedMnemonEmbeddingConfig
  memoryTopology: ResolvedMemoryTopologyConfig
  recallQuality: ResolvedRecallQualityConfig
  routingGuidance: boolean
  displayMode: MnemonDisplayMode
  tabEnabled: boolean
  writeEnabled: boolean
  /** Remote management grant for paired pages; loopback pages keep full access. */
  remoteAccess: 'read-only' | 'trusted-host'
  lifecycleEnabled: boolean
  recallMode: 'guided' | 'off'
  writebackMode: 'guided' | 'off'
  idleReviewMs: number
  idleReview: ResolvedIdleReviewConfig
  conversationInteraction: {
    toolviews: boolean
    turnBar: boolean
    saveAction: boolean
  }
  persistenceStrategy: ResolvedMemoryPersistenceStrategy
  taskAgentModel: ResolvedTaskAgentModelConfig
}

export interface ResolvedInteractionConfig {
  turnBar: boolean
  saveAction: boolean
}

/**
 * Provider instance identity inside one Memory Spaces Source. Built-in ids
 * remain stable; third-party child modules may contribute additional ids.
 */
export type * from 'dsh-mnemon-source-memory-spaces/contracts'

export type * from 'dsh-mnemon-source-documents/contracts'
export type * from 'dsh-mnemon-source-runtime/contracts'

export interface TurnMemoryActivity {
  turn: number
  count: number
  names: string[]
  recalls: number
  writes: number
  documentSearches: number
  inspections: number
  failures: number
  /** Successful evidence-bearing reads with tool-authored safe previews. */
  retrieved: MemoryActivityRead[]
  /** Only writes whose tool-authored result confirms durable commitment. */
  writebacks: MemoryActivityWriteback[]
}

export interface MemoryActivityItem {
  id: string
  title: string
  excerpt?: string
}

export interface MemoryActivityRead {
  callId: string
  toolName: string
  operationId: string
  reference?: string
  sourceTypeId?: string
  items: MemoryActivityItem[]
}

export interface MemoryActivityWriteback {
  callId: string
  toolName: string
  operationId: string
  reference?: string
  sourceTypeId?: string
  item: MemoryActivityItem
}

export interface TurnMemoryActivitySnapshot {
  cursor: number
  activities: TurnMemoryActivity[]
}

export interface AssistantMessageText {
  messageId: string
  text: string
}

export type StorageAreaKind = 'runtime' | 'memory-bodies' | 'documents' | 'state'
export type StorageAreaStatus = 'ready' | 'empty' | 'missing' | 'invalid'

export interface StorageAreaInventory {
  kind: StorageAreaKind
  path: string
  status: StorageAreaStatus
  bytes: number
  itemCount: number
  details: Record<string, number | string | boolean>
  issue?: string
}

export interface StorageScopeInventory {
  kind: StorageScopeKind
  root?: string
  configured: boolean
  active: boolean
  available: boolean
  totalBytes: number
  areas: StorageAreaInventory[]
  issue?: string
}

export interface StorageScopeCatalog {
  activeKind: StorageScopeKind
  activeRoot: string
  scopes: StorageScopeInventory[]
  generatedAt: string
}

export interface ReviewActivity {
  totalUserTextLength: number
  turnCount: number
  toolCallCount: number
  uniqueToolCount: number
}

export interface ReviewActivityScore extends ReviewActivity {
  textLengthScore: number
  turnScore: number
  toolCallScore: number
  toolDiversityScore: number
  score: number
  threshold: number
  eligible: boolean
}

export interface SubagentCounters {
  recalls: number
  writes: number
  answers: number
  reviews: number
  placements: number
  migrations: number
  compactions: number
  documentArchives: number
  metadataMaintenances: number
  failures: number
  lastRunId?: string
  lastOperation?: 'recall' | 'write' | 'review' | 'placement' | 'migration' | 'compaction' | 'document-archive' | 'metadata-maintenance'
  lastAt?: string
}

export type LifecyclePhase = 'idle' | 'prime' | 'recall' | 'writeback' | 'review' | 'supervised' | 'error'

export interface LifecycleCounters {
  primes: number
  recallCues: number
  writebackCues: number
  supervisedRequests: number
  failures: number
}

export interface LifecycleAgentSnapshot {
  sessionId: string
  status: 'idle' | 'running'
  startSource: 'startup' | 'resume' | 'clear' | 'compact' | 'adopted'
  primePending: boolean
  guidedTurns: number
  memoryToolCalls: number
  idleReviewPending: boolean
  reviewRunning: boolean
  reviewActivity: ReviewActivityScore
  lastPhase: LifecyclePhase
  lastReviewAt?: string
  lastReviewAction?: string
  lastReviewScore?: number
  lastReviewDocumentIds?: string[]
  idleReviewAttempts?: number
  idleReviewBlocked?: 'agent-team'
  nextReviewAt?: string
  lastReviewFailure?: IdleReviewFailure
  lastAt?: string
  lastError?: string
}

/** Reconciliation metadata only; raw tool arguments and memory content stay private. */
export interface IdleReviewFailure {
  status: 'failed' | 'partial'
  runId?: string
  provider: string
  receipts: Array<{ tool: string; action: string; documentId?: string; target?: string; revision?: string }>
}

export interface LifecycleSnapshot {
  enabled: boolean
  recallMode: 'guided' | 'off'
  writebackMode: 'guided' | 'off'
  idleReviewMs: number
  activeAgents: number
  sessionAvailable: boolean
  /** A session-independent task Agent can be created for WebUI maintenance. */
  taskAgentAvailable: boolean
  counters: LifecycleCounters
  subagents: SubagentCounters
  current?: LifecycleAgentSnapshot
}

export interface StatusView {
  healthy: boolean
  error?: string
  version?: string
  /** The dsh-mnemon version this Host runs, which an installed update replaces only when DSH restarts. */
  dshMnemonVersion?: string
  /** Updates installed while this Host runs; they load when DSH restarts. */
  restartPending?: VersionRestartStatus
  cliPath: string
  commandFound: boolean
  dataDir: string
  /** Legacy comma-separated DSH-enabled Store list. */
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
  memorySystem?: MemoryCompositionStatus
  lifecycle?: LifecycleSnapshot
  documents?: DocumentSnapshot
  storage?: StorageScopeCatalog
  workspaceContext?: {
    mode: StorageScopeKind
    selectedRoot: string
    effectiveRoot: string
    aligned: boolean
    selectedWorkspace?: { id: string; title: string; path: string }
    effectiveWorkspace?: { id: string; title: string; path: string }
  }
  stats?: MemorySpaceStats & { dbPath?: string }
}

export type MnemonPackComponent = 'runtime' | 'documents' | 'memory-spaces'
/** The Sources that keep their data in Mnemon's data directory, in the order a backup lists them. */
export const MNEMON_PACK_COMPONENTS = ['runtime', 'documents', 'memory-spaces'] as const satisfies readonly MnemonPackComponent[]
export type MnemonPackScope = 'full' | MnemonPackComponent
export type MnemonPackImportMode = 'merge' | 'replace'

export interface MnemonPackComponentSummary {
  component: MnemonPackComponent
  files: number
  bytes: number
  items: number
}

export interface MnemonPackManifest {
  format: 'mnemonpack'
  version: 1
  scope: MnemonPackScope
  exportedAt: string
  source: { plugin: 'dsh-mnemon'; pluginVersion: string }
  components: MnemonPackComponent[]
  summary: MnemonPackComponentSummary[]
}

/** Where memory lives now, and where the global scope keeps it when no directory is chosen. */
export interface MnemonPackTarget {
  root: string
  scope: StorageScopeKind
  defaultRoot: string
}

export interface MnemonPackExport {
  fileName: string
  mimeType: 'application/zip'
  bytes: number
  base64: string
  targetRoot: string
  manifest: MnemonPackManifest
}

export interface MnemonPackPreview {
  fileName?: string
  archiveBytes: number
  expandedBytes: number
  targetRoot: string
  targetScope: StorageScopeKind
  manifest: MnemonPackManifest
  occupied: Record<MnemonPackComponent, boolean>
}

export interface MnemonPackImportResult {
  imported: true
  mode: MnemonPackImportMode
  targetRoot: string
  components: MnemonPackComponent[]
  summary: MnemonPackComponentSummary[]
}

export type VersionPackageId = `dsh-mnemon-${'source' | 'strategy' | 'provider'}-${string}`
export type VersionComponentId = 'mnemon' | 'dsh-mnemon' | VersionPackageId
export type VersionInstallMode = 'homebrew' | 'go' | 'npm' | 'link' | 'manual' | 'missing'

export interface VersionComponentStatus {
  id: VersionComponentId
  name: string
  executablePath?: string
  installPath?: string
  installProfile?: string
  current?: string
  latest?: string
  outdated: boolean
  installMode: VersionInstallMode
  updateSupported: boolean
  updateHint: string
  checkError?: string
  restartRequired?: boolean
  packages?: VersionPackageStatus[]
}

export interface VersionPackageStatus extends VersionComponentStatus {
  id: VersionPackageId
  kind: 'source' | 'strategy' | 'provider'
  managedBy: 'starter' | 'profile'
  expectedVersion?: string
}

export interface VersionStatus {
  checkedAt: string
  components: VersionComponentStatus[]
  /** How the last update this Host ran from Check versions ended, for a page DSH swapped in meanwhile. */
  lastUpdate?: VersionUpdateOutcome
}

export interface VersionUpdateOutcome {
  at: string
  component: VersionComponentId
  /** The update's reply, when it ended without an error. */
  result?: VersionUpdateResult
  /** Why it failed, when it did. */
  error?: string
}

export interface VersionRestartStatus {
  /** The dsh-mnemon version this Host runs. */
  running: string
  /** The dsh-mnemon version now installed, when it differs, however it was installed: Check versions or `dsh plugin`, for example. */
  installed?: string
  /** Packages Check versions updated on their own. */
  packages?: VersionPackageId[]
}

export interface VersionUpdateResult {
  component: VersionComponentId
  previousVersion?: string
  currentVersion?: string
  updated: boolean
  restartRequired: boolean
  output?: string
}

// Default-product form values; Source configuration is validated again by its owner.
export const CATEGORIES = ['preference', 'decision', 'fact', 'insight', 'context', 'general'] as const
export const SOURCES = ['user', 'agent', 'external'] as const
export const EDGE_TYPES = ['temporal', 'semantic', 'causal', 'entity'] as const
export const INTENTS = ['WHY', 'WHEN', 'ENTITY', 'GENERAL'] as const
export const DEFAULT_EMBEDDING_ENDPOINT = 'http://localhost:11434'
export const DEFAULT_EMBEDDING_MODEL = 'nomic-embed-text'
export const DEFAULT_EMBEDDING_PROTOCOL = 'auto'
export const MNEMON_EMBEDDING_PROTOCOLS = ['auto', 'ollama', 'openai'] as const
