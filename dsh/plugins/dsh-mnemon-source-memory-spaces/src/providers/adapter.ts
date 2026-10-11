import type { JsonValue } from '../contracts.ts'
import type {
  EdgeType,
  Insight,
  MemorySpace,
  MemorySpaceStats,
  MemoryGraphSnapshot,
  MemoryListRequest,
  MemoryProviderConnection,
  RememberRequest,
  SearchRequest,
} from '../contracts.ts'

/** Minimum parent authority a Provider needs; no private controller class. */
export interface MemorySpaceAuthority {
  readonly runner: { effectiveDataDir(): string }
  list(): MemorySpace[]
  providerConnection(id: string, expectedProviderId?: string): MemoryProviderConnection
}

/** Per-call options of the Native command transport. */
export interface MemorySpaceNativeRunOptions {
  signal?: AbortSignal
  store?: string
  /**
   * Output cap for this call, in bytes (default 2 MiB). Raise it only for reads
   * whose size the Store itself bounds, such as a whole-Store dump; Source
   * hosts older than 0.5.14 ignore it and keep the default.
   */
  maxOutputBytes?: number
}

/** Scoped command transport consumed by the Native Provider. */
export interface MemorySpaceNativeRunner {
  runJson(args: readonly string[], options?: MemorySpaceNativeRunOptions): Promise<JsonValue>
  runText(args: readonly string[], options?: MemorySpaceNativeRunOptions): Promise<string>
}

export interface MemoryProviderAdapterFactoryContext {
  /** Canonical name; optional while older Source hosts remain supported. */
  memorySpaces?: MemorySpaceAuthority
  /** @deprecated Use memorySpaces when present; retained for existing Provider factories. */
  memoryBodies: MemorySpaceAuthority
  config: { timeoutMs: number; defaultRecallLimit?: number }
  nativeRunner?: MemorySpaceNativeRunner
}

export interface ProviderSpaceStatus {
  healthy: boolean
  error?: string
  stats?: MemorySpaceStats
}

export interface ProviderSearchResult {
  results: Insight[]
  hint?: string
}

export interface ProviderScoreSemantics {
  /** Provider promises a finite relevance score in 0..1 where larger is better. */
  kind: 'normalized-relevance'
}

export type MemoryProviderScoreSemantics = 'normalized-relevance' | 'provider-native' | 'none'

export const NORMALIZED_RELEVANCE_SCORE: ProviderScoreSemantics = Object.freeze({ kind: 'normalized-relevance' })

/** The memories of one Memory Space that carry entities, as the Provider's own index has them. */
export interface ProviderEntityIndex {
  /** Each memory with at least one entity, listing its entities. */
  memories: Insight[]
  /** False when the Provider could not include every memory of the space. */
  complete: boolean
}

/** One provider-owned namespace projected into DSH as a Memory Space. */
export interface ProviderMemorySpace {
  /** Stable identifier owned by the provider, never a DSH-generated title. */
  externalId: string
  name: string
  description: string
  connection: MemoryProviderConnection
}

/**
 * Third-layer memory data plane. DSH owns routing and lifecycle; adapters own
 * only one memory space's persistence and retrieval semantics.
 */
export interface MemoryProviderAdapter {
  readonly id: MemorySpace['provider']['id']
  readonly scoreSemantics?: ProviderScoreSemantics
  /** Enumerate the complete set of namespaces visible to this service connection. */
  discover?(connection: MemoryProviderConnection, signal?: AbortSignal): Promise<ProviderMemorySpace[]>
  /** Drop a short-lived health result before an explicit user reconnect. */
  invalidateStatus?(memoryBodyId?: string): void
  status(body: MemorySpace, signal?: AbortSignal): Promise<ProviderSpaceStatus>
  search(body: MemorySpace, request: SearchRequest, signal?: AbortSignal): Promise<ProviderSearchResult>
  graph(body: MemorySpace, signal?: AbortSignal): Promise<MemoryGraphSnapshot>
  list(body: MemorySpace, request: MemoryListRequest, signal?: AbortSignal): Promise<Insight[]>
  remember(body: MemorySpace, request: RememberRequest, signal?: AbortSignal): Promise<JsonValue>
  /** Optional cheap bounded metadata sampling, without a graph projection. */
  metadataSample?(body: MemorySpace, limit: number, signal?: AbortSignal): Promise<Insight[]>
  /**
   * Optional entity index for the Entities page. Without it the Source builds
   * the index from list(), so a Provider whose list() stops short of every
   * memory, or that cannot list, implements this to keep entity counts whole.
   */
  entityIndex?(body: MemorySpace, signal?: AbortSignal): Promise<ProviderEntityIndex>
  /** Persist an ordered host-authorized batch and return one receipt per request. */
  rememberMany?(body: MemorySpace, requests: readonly RememberRequest[], signal?: AbortSignal): Promise<JsonValue[]>
  related?(body: MemorySpace, id: string, depth: number, edge?: EdgeType, signal?: AbortSignal): Promise<Insight[]>
  link?(body: MemorySpace, sourceId: string, targetId: string, type: EdgeType, weight: number, reason?: string, signal?: AbortSignal): Promise<JsonValue>
  forget?(body: MemorySpace, id: string, signal?: AbortSignal): Promise<JsonValue>
  /** Release generation-owned clients, timers, pools, or subprocess handles. */
  dispose?(): void | Promise<void>
}

/** @deprecated Use ProviderSpaceStatus. */
export type ProviderBodyStatus = ProviderSpaceStatus
