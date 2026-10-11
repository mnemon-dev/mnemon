/**
 * Public narrow waist for Composable View Memory plugins.
 *
 * Definitions contain executable factories and remain inside the Host realm.
 * Every manifest, fact, spec, View, Evidence, and Receipt crossing that
 * boundary is JSON-safe and can therefore be validated, digested, and replayed.
 */
import type {
  MemoryCapability,
  MemoryJsonValue,
  MemoryOperationScope,
  MemoryReceiptStatus,
  MemorySourceMode,
} from './index.ts'

export const COMPOSABLE_MEMORY_API_VERSION = 'dsh-mnemon/v1' as const

/** Persistence truth, not a taxonomy of memory operations. */
export type MemoryMutationCompletion = 'accepted' | 'candidate' | 'committed' | 'partial' | 'failed' | 'unknown'

export type MemoryContributionKind = 'source' | 'strategy' | 'strategy-extension'
export type MemorySourceAvailability = 'ready' | 'degraded' | 'unavailable'
export type MemoryViewConsistencyMode = 'exact-snapshot' | 'namespace-pinned-live-read'

export interface MemoryPackageProvenance {
  /** npm package name or another stable artifact namespace. */
  packageName: string
  /** Stable Loader Entry id (or an explicit id for direct ctx.plugin mounts). */
  entryId: string
  /** Optional immutable package artifact digest supplied by an installer. */
  artifactDigest?: string
}

export interface MemoryManagementField {
  key: string
  label: string
  description?: string
  input: 'text' | 'number' | 'boolean' | 'url' | 'secret' | 'select'
  required: boolean
  secret?: boolean
  options?: Array<{ value: string; label: string }>
}

/** JSON-safe presentation metadata. It never contains a React component. */
export interface MemoryManagementDescriptor {
  label: string
  description: string
  fields?: MemoryManagementField[]
  diagnostics?: string[]
}

export type MemorySourceManagementMode = 'read' | 'mutate'

/**
 * Authenticated human-management request delivered to one Serving Source.
 * It is deliberately separate from model Routes and ActionOffers: no View
 * grant crosses into the browser management plane.
 */
export interface MemorySourceManagementRequest {
  scope: MemoryOperationScope
  sourceInstanceKey: string
  mode: MemorySourceManagementMode
  operation: string
  input: MemoryJsonValue
  /** Mutations are revision-fenced by the caller and rechecked by the Source. */
  expectedRevision?: string
  /** The Host only forwards mutations after an explicit confirmation. */
  confirmed: boolean
  signal?: AbortSignal
}

/** JSON-safe management projection or mutation result from one Source. */
export interface MemorySourceManagementResult {
  revision: string
  value: MemoryJsonValue
}

/** Sanitized Source instance descriptor returned to authenticated clients. */
export interface MemorySourceManagementInstance {
  sourceInstanceKey: string
  sourceTypeId: string
  packageName: string
  role: string
  availability: MemorySourceAvailability
  revision: string
  capabilities: MemoryCapability[]
  management: MemoryManagementDescriptor
  hints?: MemoryJsonValue
}

export interface MemorySourceManagementCatalog {
  generationId: string
  sources: MemorySourceManagementInstance[]
  diagnostics?: MemoryCompositionDiagnostic[]
}

export interface MemorySourceRouteManifest {
  /** Stable within one Source type. Runtime prefixes it with the instance key. */
  id: string
  description: string
  capability: MemoryCapability
  inputSchema: MemoryJsonValue
  maxCalls: number
  maxResults?: number
  maxCharacters?: number
}

export interface MemorySourceActionManifest {
  /** Stable within one Source type. Runtime prefixes it with the instance key. */
  id: string
  description: string
  capability: MemoryCapability
  inputSchema: MemoryJsonValue
  /** High-risk non-memory actions must name an external authority. */
  authority?: string
}

export interface MemorySourceManifest {
  apiVersion: typeof COMPOSABLE_MEMORY_API_VERSION
  kind: 'source'
  typeId: string
  packageName: string
  role: string
  capabilities: MemoryCapability[]
  consistency: MemoryViewConsistencyMode
  routes?: MemorySourceRouteManifest[]
  actions?: MemorySourceActionManifest[]
  management?: MemoryManagementDescriptor
}

export interface MemoryStrategyManifest {
  apiVersion: typeof COMPOSABLE_MEMORY_API_VERSION
  kind: 'strategy'
  typeId: string
  packageName: string
  deterministic: true
  /** Source roles this Strategy may select; [ANY_MEMORY_SOURCE_ROLE] accepts every role. */
  supportedSourceRoles: string[]
  maxSources: number
  maxRoutes: number
  maxActions: number
  /** Exclusive contribution slots this Strategy accepts; standard slots follow Core's value contracts. */
  extensionSlots?: string[]
}

/** Extension target for every selected Strategy that declares the extension's standard slot. */
export const ANY_MEMORY_STRATEGY = '*' as const

/** Strategy role list entry for a role-agnostic Strategy that may select any Source. */
export const ANY_MEMORY_SOURCE_ROLE = '*' as const

/** Standard View extension slots, each with one public value contract any Strategy may accept. */
export const MEMORY_VIEW_EXTENSION_SLOTS = ['selection', 'projection', 'capture'] as const
export type MemoryViewExtensionSlot = typeof MEMORY_VIEW_EXTENSION_SLOTS[number]

export interface MemoryViewExtensionValues {
  /** Admitted Source instances in priority order; writableSourceKeys keeps actions on a subset. */
  selection: { sourceKeys: string[]; writableSourceKeys?: string[] }
  /** Ceiling for resident projection characters within the Host budget. */
  projection: { maxProjectionCharacters: number }
  /** Asks the current model to record qualified durable facts through Source-local actions. */
  capture: { instruction: string; actionIds: string[]; sourceKeys?: string[] }
}

/** An additive plugin targets one Strategy contract, or any Strategy through a standard slot; it never replaces the Strategy. */
export interface MemoryStrategyExtensionManifest {
  apiVersion: typeof COMPOSABLE_MEMORY_API_VERSION
  kind: 'strategy-extension'
  typeId: string
  packageName: string
  /** One Strategy typeId, or ANY_MEMORY_STRATEGY with a standard slot. */
  strategyTypeId: string
  slot: string
  deterministic: true
}

export interface MemoryStrategyExtensionDefinition {
  manifest: MemoryStrategyExtensionManifest
  /** Pure, bounded JSON proposal; no Source handles, write callbacks or credentials. */
  contribute(request: MemoryViewRequest, sources: readonly MemoryAvailableSource[]): MemoryJsonValue
}

/** The owning Strategy interprets value using its public, slot-specific contract. */
export interface MemoryStrategyContribution {
  instanceKey: string
  typeId: string
  slot: string
  value: MemoryJsonValue
}

export interface MemorySourceFacts {
  sourceInstanceKey: string
  sourceTypeId: string
  role: string
  availability: MemorySourceAvailability
  revision: string
  capabilities: MemoryCapability[]
  routeIds: string[]
  actionIds: string[]
  /** Coarse, non-sensitive hints only; never raw Authority data or credentials. */
  hints?: MemoryJsonValue
}

export interface MemoryViewBudget {
  maxProjectionCharacters: number
  maxRoutes: number
  maxActions: number
  maxEvidenceResults: number
  maxEvidenceCharacters: number
}

/** Manifest operations filtered by live availability and Host permissions. */
export interface MemoryAvailableSource extends MemorySourceFacts {
  routes: MemorySourceRouteManifest[]
  actions: MemorySourceActionManifest[]
}

export const DEFAULT_MEMORY_VIEW_BUDGET: Readonly<MemoryViewBudget> = Object.freeze({
  maxProjectionCharacters: 64 * 1024,
  maxRoutes: 16,
  maxActions: 16,
  maxEvidenceResults: 16,
  maxEvidenceCharacters: 16 * 1024,
})

export interface MemoryViewRequest {
  scope: MemoryOperationScope
  scenario: string
  budget: MemoryViewBudget
}

export interface MemoryViewSourceSpec {
  sourceInstanceKey: string
  /** Defaults to true. An explicitly optional Source may be omitted on read failure. */
  required?: boolean
  projection?: {
    mode: MemorySourceMode
    maxCharacters: number
  }
  /** Source-local route ids declared by its manifest/facts. */
  routeIds?: string[]
  /** Source-local action ids declared by its manifest/facts. */
  actionIds?: string[]
}

/** Pure Strategy output. Runtime treats it as a proposal, never as authority. */
export interface MemoryViewSpec {
  strategyTypeId: string
  sources: MemoryViewSourceSpec[]
  explanation: string
  /** Trusted Strategy instructions, separate from quoted Source data. */
  guidance?: MemoryViewGuidance
}

export interface MemoryViewGuidance {
  system?: string
  routing?: string
  reminders?: { read?: string; write?: string; both?: string }
}

export interface MemoryProjectionRequest {
  scope: MemoryOperationScope
  sourceInstanceKey: string
  /** Facts revision selected by the Strategy; the Source owns its snapshot consistency. */
  expectedRevision: string
  includeProjection: boolean
  mode: MemorySourceMode
  maxCharacters: number
}

export interface MemoryViewFragment {
  id: string
  sourceInstanceKey: string
  mode: MemorySourceMode
  text: string
  revision: string
  provenance?: MemoryJsonValue
}

/**
 * Bounded, Source-authored human presentation for one item in a View.
 * It is descriptive only: Core never treats it as Evidence, authority, or
 * model instructions, and the Host never renders executable UI from it.
 */
export interface MemorySourcePresentationItem {
  id: string
  title: string
  excerpt?: string
}

/**
 * Optional browser-safe description of the items represented by a Source.
 * `visibleItems` is the number in this View scope; `items` may be a bounded
 * preview subset. `totalItems` may include inactive or out-of-scope items.
 */
export interface MemorySourcePresentation {
  visibleItems: number
  totalItems?: number
  items?: MemorySourcePresentationItem[]
}

/** Source presentation after Core binds it to the selected mode and instance. */
export interface MemoryViewSourcePresentation extends MemorySourcePresentation {
  sourceInstanceKey: string
  mode: MemorySourceMode
}

export interface MemoryReadGrant {
  id: string
  sourceInstanceKey: string
  schema: string
  value: MemoryJsonValue
  revision: string
  consistency: MemoryViewConsistencyMode
}

export interface MemoryViewContribution {
  fragments: MemoryViewFragment[]
  readGrant?: MemoryReadGrant
  /** Optional non-authoritative presentation; old Sources may omit it. */
  presentation?: MemorySourcePresentation
}

export interface MemoryViewRoute {
  id: string
  sourceInstanceKey: string
  sourceRouteId: string
  description: string
  capability: MemoryCapability
  inputSchema: MemoryJsonValue
  readGrantId: string
  maxCalls: number
  maxResults?: number
  maxCharacters?: number
}

export interface MemoryActionOffer {
  id: string
  sourceInstanceKey: string
  sourceActionId: string
  description: string
  capability: MemoryCapability
  inputSchema: MemoryJsonValue
  authority?: string
}

export interface MemoryViewConsistency {
  mode: 'mixed' | MemoryViewConsistencyMode
  sourceRevisions: Record<string, string>
}

/** Complete Host-side safety envelope. Read grants are never model-rendered. */
export interface ComposableMemoryView {
  id: string
  digest: string
  runtimeGeneration: string
  strategyInstanceKey: string
  strategyTypeId: string
  /** Auditable active contributions, without publishing their configuration to the LLM. */
  strategyExtensions?: Array<{ instanceKey: string; typeId: string; slot: string; digest: string }>
  createdAt: string
  scope: MemoryOperationScope
  projection: MemoryViewFragment[]
  /** Optional Source-authored browser presentation; never model-rendered. */
  sourcePresentations?: MemoryViewSourcePresentation[]
  routes: MemoryViewRoute[]
  readGrants: MemoryReadGrant[]
  actionOffers: MemoryActionOffer[]
  consistency: MemoryViewConsistency
  explanation: string
  guidance?: MemoryViewGuidance
  /** Sanitized, turn-local availability failures; never raw provider errors. */
  diagnostics?: MemoryCompositionDiagnostic[]
}

export interface MemoryEvidenceItem {
  id: string
  text: string
  provenance: MemoryJsonValue
  score?: number
  revision?: string
}

export interface MemoryEvidence {
  id: string
  viewId: string
  routeId: string
  sourceInstanceKey: string
  observedAt: string
  items: MemoryEvidenceItem[]
  truncated: boolean
  unavailable?: string
  /** Source-owned read metadata; never authority or credentials. */
  metadata?: MemoryJsonValue
  /** Strategy-owned model presentation. Host audit retains the evidence separately. */
  output?: MemoryJsonValue
}

export interface MemoryMutationReceipt {
  id: string
  viewId: string
  offerId: string
  sourceInstanceKey: string
  status: MemoryReceiptStatus
  completion: MemoryMutationCompletion
  /** Only an explicitly confirmed commit may carry this timestamp. */
  committedAt?: string
  revision?: string
  details?: MemoryJsonValue
}

export interface MemorySourceRuntimeContext {
  sourceInstanceKey: string
  provenance: MemoryPackageProvenance
  /** Immutable, JSON-only Host/scope defaults; never business objects or clients. */
  configuration?: Readonly<Record<string, MemoryJsonValue>>
}

/** Operation identity only; never the complete View or another Source's data. */
export interface MemorySourceViewContext {
  id: string
  scope: MemoryOperationScope
}

export interface MemorySourceRuntime {
  /** Lightweight metadata. Honor cancellation; do not perform writes here. */
  facts(request: MemoryViewRequest, signal?: AbortSignal): MemorySourceFacts | Promise<MemorySourceFacts>
  project(request: MemoryProjectionRequest, signal?: AbortSignal): MemoryViewContribution | Promise<MemoryViewContribution>
  manage?(request: MemorySourceManagementRequest): MemorySourceManagementResult | Promise<MemorySourceManagementResult>
  query?(request: {
    view: MemorySourceViewContext
    route: MemoryViewRoute
    grant: MemoryReadGrant
    input: MemoryJsonValue
    signal?: AbortSignal
  }): MemoryEvidence | Promise<MemoryEvidence>
  mutate?(request: {
    view: MemorySourceViewContext
    offer: MemoryActionOffer
    /** This Source's own pinned read scope, when it contributed one. Not write authorization. */
    grant?: MemoryReadGrant
    input: MemoryJsonValue
    signal?: AbortSignal
  }): MemoryMutationReceipt | Promise<MemoryMutationReceipt>
  dispose?(): void | Promise<void>
}

export interface MemorySourceDefinition {
  manifest: MemorySourceManifest
  create(context: MemorySourceRuntimeContext): MemorySourceRuntime
}

export interface MemoryStrategyDefinition {
  manifest: MemoryStrategyManifest
  compose(request: MemoryViewRequest, sources: readonly MemoryAvailableSource[], contributions?: readonly MemoryStrategyContribution[]): MemoryViewSpec
  /** Optional execution policy, instantiated once for each executing turn, never during compose. */
  createTurn?(view: ComposableMemoryView): MemoryStrategyTurn
}

export interface MemoryStrategyReadRequest {
  route: MemoryViewRoute
  input: MemoryJsonValue
  signal?: AbortSignal
}

export interface MemoryStrategyTurn {
  /** The continuation can read only this offered Route, with the same grant and ceilings. */
  query(request: MemoryStrategyReadRequest, read: MemoryStrategyRead): Promise<MemoryEvidence>
}

export type MemoryStrategyRead = (input: MemoryJsonValue, limits?: Partial<Pick<MemoryViewRoute, 'maxResults' | 'maxCharacters'>>) => Promise<MemoryEvidence>

export interface MemoryCompositionDiagnostic {
  code: string
  message: string
  contributionInstanceKey?: string
}

export interface MemoryCompositionEvaluationReport {
  state: 'ready' | 'incomplete' | 'rejected'
  contributionRevision: number
  generationId?: string
  strategyInstanceKey?: string
  strategyExtensionInstanceKeys?: string[]
  sourceInstanceKeys: string[]
  diagnostics: MemoryCompositionDiagnostic[]
}
