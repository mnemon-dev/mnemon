/** Metadata, operation inputs and protocol results use JSON-safe values. */
export type MemoryJsonPrimitive = string | number | boolean | null
export type MemoryJsonValue = MemoryJsonPrimitive | MemoryJsonValue[] | { [key: string]: MemoryJsonValue }

export const MEMORY_CAPABILITIES = [
  'status',
  'project',
  'recall',
  'search',
  'read',
  'browse',
  'write',
  'archive',
  'graph',
  'related',
  'link',
  'forget',
  'maintain',
  'export',
  'import',
] as const
export type MemoryCapability = typeof MEMORY_CAPABILITIES[number]

export interface MemoryOperationScope {
  storage: 'global' | 'workspace' | 'custom' | 'workspaces'
  workspaceId?: string
  sessionId?: string
  agentId?: string
}

export type MemoryReceiptStatus = 'succeeded' | 'partial' | 'failed' | 'cancelled'

export interface MemoryMigrationLineageEndpoint {
  layerId: string
  reference: string
  digest: string
}

/** Auditable proof that one exact source item reached one committed destination. */
export interface MemoryMigrationLineage {
  source: MemoryMigrationLineageEndpoint
  destination: MemoryMigrationLineageEndpoint
}

export const MEMORY_SOURCE_MODES = ['eager', 'routed'] as const
export type MemorySourceMode = typeof MEMORY_SOURCE_MODES[number]

export interface MemoryWakeSection {
  layerId: string
  mode: MemorySourceMode
  text: string
}

export interface MemoryWake {
  viewId: string
  viewDigest: string
  text: string
  sections: MemoryWakeSection[]
  guidance?: import('./view.ts').MemoryViewGuidance
}

export * from './plugin.ts'
export * from './view.ts'
