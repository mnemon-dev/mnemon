import type { DocumentMutation, DocumentMutationResult, DocumentSearchResult, DocumentSnapshot, DocumentView } from '../contracts.ts'

/** Source-owned structural page API; the default bundle may supply agent-assisted callbacks. */
export interface DocumentsPageClient {
  documents(): Promise<DocumentSnapshot>
  document(id: string): Promise<DocumentView>
  searchDocuments(query: string, includeArchived?: boolean, limit?: number): Promise<DocumentSearchResult>
  mutateDocument(request: DocumentMutation): Promise<DocumentMutationResult>
  archiveDocument(id: string): Promise<DocumentMutationResult>
}
