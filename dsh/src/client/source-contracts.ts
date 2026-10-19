import type { MemoryJsonValue, MemorySourceManagementInstance, MemorySourceManagementResult } from 'dsh-mnemon/contracts'

/** Instance- and scope-bound capability; never the Host's raw transport. */
export interface MnemonSourceManagementClient {
  readonly sourceInstanceKey: string
  readonly revision: string
  /** Optional workflows advertised and authorized by the Host for this exact instance. */
  readonly assistance?: {
    readonly operations: readonly string[]
    execute(operation: string, input: MemoryJsonValue, options: { expectedRevision: string; confirmed: boolean }): Promise<MemorySourceManagementResult>
  }
  read(operation: string, input?: MemoryJsonValue): Promise<MemorySourceManagementResult>
  mutate(operation: string, input: MemoryJsonValue, options: { expectedRevision?: string; confirmed: true }): Promise<MemorySourceManagementResult>
}

/** Browser metadata is display-only; the Host validates capability identifiers. */
export type MemorySourcePageInstance = Omit<MemorySourceManagementInstance, 'capabilities'> & { capabilities: readonly string[] }

export interface MemorySourcePageProps {
  /** Type-level presentation identity; never a Client or Host Fiber uid. */
  sourceTypeId: string
  /** Selected Host instance in the current authenticated scope. */
  sourceInstanceKey?: string
  sourceInstances: readonly MemorySourcePageInstance[]
  /** Present only while the selected Host instance remains visible. */
  management?: MnemonSourceManagementClient
  /** Presentation hint only; the Host still authenticates every mutation. */
  writable?: boolean
  sessionId?: string
  workspaceId?: string
  locale: string
  /** Opaque navigation data owned by this Source page. */
  navigationInput?: MemoryJsonValue
  onRefresh?(): void
  /** Changes each time the user refreshes the workspace; reload this page's data when it does. */
  refreshKey?: number
  /** Reset only this workbench's page scroll when Source-owned navigation changes. */
  onResetScroll?(): void
  /**
   * Reveal a descendant in this canvas only, below the locked page header, or
   * `topInset` below the top when the Source pins and measures its own header.
   */
  onRevealElement?(element: HTMLElement, topInset?: number): void
  /** Optional, Source-bound product preferences; no raw settings service or secrets. */
  preferences?: { value: MemoryJsonValue; writable: boolean; replace(value: MemoryJsonValue): Promise<void> }
}
