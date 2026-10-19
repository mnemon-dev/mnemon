export type DocumentStatus = 'active' | 'archived'

export interface DocumentRecord {
  id: string
  title: string
  description: string
  status: DocumentStatus
  filename: string
  relativePath: string
  sourcePaths: string[]
  sessionIds: string[]
  createdAt: string
  updatedAt: string
  lastAccessedAt: string
  revision: number
  contentHash: string
  sizeBytes: number
  archivedAt?: string
  archiveSummary?: string
  memoryBodyIds: string[]
}

export interface DocumentView extends DocumentRecord {
  content: string
}

export interface DocumentSnapshot {
  workspaceRoot: string
  directory: string
  indexPath: string
  generatedAt: string
  revision: string
  limitBytes: number
  activeBytes: number
  activeCount: number
  archivedCount: number
  total: number
  documents: Array<DocumentRecord & { healthy: boolean; excerpt: string }>
}

export interface DocumentSearchResult {
  query: string
  includeArchived: boolean
  total: number
  generatedAt: string
  results: Array<DocumentView & { score: number; excerpt: string }>
}

export type DocumentMutation =
  | { action: 'create'; title: string; description?: string; content: string; sourcePaths?: string[]; sessionIds?: string[] }
  | { action: 'update'; id: string; title?: string; description?: string; content?: string; sourcePaths?: string[]; sessionIds?: string[] }

export interface DocumentMutationResult {
  success: true
  action: 'created' | 'updated' | 'archived'
  document: DocumentView
  snapshot: DocumentSnapshot
  maintenance?: { runId: string; provider: string; summary: string; memoryBodyIds: string[]; archivedDocumentIds: string[] }
}

export interface DocumentCapacityPlan {
  projected: number
  limit: number
  fits: boolean
  candidates: DocumentRecord[]
}

export const DOCUMENTS_VERSION = 1
export const DOCUMENTS_ACTIVE_LIMIT_BYTES = 10 * 1024 * 1024
