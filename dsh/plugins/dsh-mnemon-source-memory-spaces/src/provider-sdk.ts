/** Source-owned private-child SPI. Core never imports or registers Providers. */
export { CATEGORIES, SOURCES, EDGE_TYPES, INTENTS } from './contracts.ts'
export type {
  JsonValue, Category, Source, EdgeType, Intent, Insight,
  MemorySpace, MemorySpaceProvider, MemorySpaceStats, MemoryGraphNode, MemoryGraphEdge, MemoryGraphSnapshot,
  MemoryListRequest, RememberRequest, SearchRequest, OpenVikingSpaceConnection,
  MemoryProviderId, MemoryProviderConnection, MemoryProviderConnectionValue,
  MemoryProviderIcon, MemoryProviderConfigField, MemoryProviderConfigOption,
  MemoryProviderCapabilities, MemoryProviderDescriptor,
} from './contracts.ts'
export * from './providers/adapter.ts'
export { MEMORY_SPACE_PROVIDER_API_VERSION, defineMemorySpaceProvider, defineMemorySpaceProviderDefinition } from './providers/definitions.ts'
export type {
  MemorySpaceProviderManifest, MemorySpaceProviderDefinition, MemorySpaceProviderRuntimeContext,
  MemorySpaceProviderScoreSemantics, MemorySpaceProviderDisposer, MemorySpaceProviderHost,
  MemorySpaceProviderModule, MemorySpaceProviderEntry,
} from './providers/definitions.ts'
export * from './providers/http.ts'
export * from './providers/process.ts'

// Compatibility exports for existing Provider declarations.
export type { MemoryBody, MemoryBodyProvider, MemoryBodyStats, OpenVikingBodyConnection } from './contracts.ts'
