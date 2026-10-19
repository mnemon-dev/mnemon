import {
  MNEMON_ACTIVATION_CHANNEL,
  MNEMON_PACK_CHANNEL,
  MNEMON_READ_CHANNEL,
  MNEMON_WRITE_CHANNEL,
  MNEMON_VIEW_CHANNEL,
  MNEMON_VIEW_WRITE_CHANNEL,
  type MemoryViewDashboard,
  type MemoryViewConfigurationRequest,
  type AssistantMessageText,
  type ClientConnectionHandle,
  type JsonValue,
  type MemoryProviderServiceCatalog,
  type MemoryProviderServiceView,
  type MemoryCompositionStatus,
  type MemorySourceManagementCatalog,
  type MemorySourceManagementResult,
  type MnemonPackExport,
  type MnemonPackImportResult,
  type MnemonPackPreview,
  type MnemonPackTarget,
  type MnemonEmbeddingStatus,
  type StatusView,
  type TaskAgentModelCatalog,
  type TurnMemoryActivity,
  type TurnMemoryActivitySnapshot,
  type UpdateMemoryProviderServiceRequest,
  type VersionComponentId,
  type VersionStatus,
  type VersionUpdateResult,
} from "../host/protocol.ts"
import { callMnemonRpc } from './remote-rpc.ts'

interface TurnActivityCacheEntry {
  cursor: number
  activities: Map<number, TurnMemoryActivity>
  inFlight?: Promise<TurnMemoryActivitySnapshot>
}

const turnActivityCache = new WeakMap<ClientConnectionHandle, Map<string, TurnActivityCacheEntry>>()

async function loadTurnActivities(connection: ClientConnectionHandle, sessionId: string | undefined, requiredCursor: number): Promise<TurnMemoryActivitySnapshot> {
  let sessions = turnActivityCache.get(connection)
  if (sessions === undefined) {
    sessions = new Map()
    turnActivityCache.set(connection, sessions)
  }
  const key = sessionId ?? ''
  let entry = sessions.get(key)
  if (entry === undefined) {
    entry = { cursor: -1, activities: new Map() }
    sessions.set(key, entry)
  }
  if (entry.cursor >= requiredCursor) return { cursor: entry.cursor, activities: [...entry.activities.values()] }
  if (entry.inFlight !== undefined) {
    const snapshot = await entry.inFlight
    return snapshot.cursor >= requiredCursor ? snapshot : loadTurnActivities(connection, sessionId, requiredCursor)
  }

  const request = callMnemonRpc(connection, MNEMON_READ_CHANNEL, 'turn-activities', sessionId === undefined ? {} : { sessionId })
    .then(response => {
      if (!response.ok) throw new Error(response.error.message)
      const snapshot = response.value as TurnMemoryActivitySnapshot
      entry!.cursor = snapshot.cursor
      entry!.activities = new Map(snapshot.activities.map(activity => [activity.turn, activity]))
      return snapshot
    })
    .finally(() => { delete entry!.inFlight })
  entry.inFlight = request
  return request
}

export class MnemonClient {
  constructor(private readonly connection: ClientConnectionHandle, private readonly sessionId?: string, private readonly workspaceId?: string) {}

  private async call<T>(channel: string, endpoint: string, payload: unknown): Promise<T> {
    const response = await callMnemonRpc(this.connection, channel, endpoint, payload)
    if (!response.ok) throw new Error(response.error.message)
    return response.value as T
  }

  private scoped<T extends object = Record<string, never>>(payload: T = {} as T): T & { sessionId?: string; workspaceId?: string } {
    return {
      ...payload,
      ...(this.sessionId === undefined ? {} : { sessionId: this.sessionId }),
      ...(this.workspaceId === undefined ? {} : { workspaceId: this.workspaceId }),
    }
  }

  status(): Promise<StatusView> {
    return this.call(MNEMON_READ_CHANNEL, 'status', this.scoped())
  }

  statusSummary(): Promise<StatusView> {
    return this.call(MNEMON_READ_CHANNEL, 'status-summary', this.scoped())
  }

  embeddingStatus(): Promise<MnemonEmbeddingStatus> {
    return this.call(MNEMON_READ_CHANNEL, 'embedding-status', this.scoped())
  }

  memorySystem(): Promise<MemoryCompositionStatus> {
    return this.call(MNEMON_READ_CHANNEL, 'memory-system', this.scoped())
  }

  viewDashboard(): Promise<MemoryViewDashboard> { return this.call(MNEMON_VIEW_CHANNEL, 'dashboard', this.scoped()) }
  applyView(configuration: MemoryViewConfigurationRequest): Promise<{ saved: true }> {
    return this.call(MNEMON_VIEW_WRITE_CHANNEL, 'apply', this.scoped({ configuration, confirmed: true }))
  }

  sourceManagementCatalog(): Promise<MemorySourceManagementCatalog> {
    return this.call(MNEMON_READ_CHANNEL, 'source-management-catalog', this.scoped())
  }

  readSourceManagement(sourceInstanceKey: string, operation: string, input: JsonValue = null): Promise<MemorySourceManagementResult> {
    return this.call(MNEMON_READ_CHANNEL, 'source-management-read', this.scoped({ sourceInstanceKey, operation, input }))
  }

  mutateSourceManagement(sourceInstanceKey: string, operation: string, input: JsonValue, expectedRevision: string, confirmed: boolean): Promise<MemorySourceManagementResult> {
    return this.call(MNEMON_WRITE_CHANNEL, 'source-management-mutate', this.scoped({ sourceInstanceKey, operation, input, expectedRevision, confirmed }))
  }

  assistSource(sourceInstanceKey: string, operation: string, input: JsonValue, expectedRevision: string, confirmed: boolean): Promise<MemorySourceManagementResult> {
    return this.call(operation === 'activation' ? MNEMON_ACTIVATION_CHANNEL : confirmed ? MNEMON_WRITE_CHANNEL : MNEMON_READ_CHANNEL, 'source-assistance', this.scoped({ sourceInstanceKey, operation, input, expectedRevision, confirmed }))
  }

  taskAgentModels(includeCatalog?: boolean): Promise<TaskAgentModelCatalog> {
    return this.call(MNEMON_READ_CHANNEL, 'task-agent-models', includeCatalog === undefined ? {} : { includeCatalog })
  }

  versions(): Promise<VersionStatus> {
    return this.call(MNEMON_READ_CHANNEL, 'versions', {})
  }

  updateVersion(component: VersionComponentId): Promise<VersionUpdateResult> {
    return this.call(MNEMON_WRITE_CHANNEL, 'version-update', { component })
  }

  providerServices(): Promise<MemoryProviderServiceCatalog> {
    return this.call(MNEMON_READ_CHANNEL, 'provider-services', this.scoped())
  }

  updateProviderService(request: UpdateMemoryProviderServiceRequest): Promise<MemoryProviderServiceView> {
    return this.call(MNEMON_WRITE_CHANNEL, 'provider-service-update', this.scoped(request))
  }

  /** Settled memory-tool activity of one turn, shared across all mounted tails. */
  async turnActivity(turn: number, cursor = 0): Promise<TurnMemoryActivity | null> {
    const snapshot = await loadTurnActivities(this.connection, this.sessionId, cursor)
    return snapshot.activities.find(activity => activity.turn === turn) ?? null
  }

  /** Plain text of one finalized assistant message; null when absent or empty. */
  assistantMessageText(messageId: string): Promise<AssistantMessageText | null> {
    return this.call(MNEMON_READ_CHANNEL, 'assistant-message', { sessionId: this.sessionId, messageId })
  }

  supervise(content: string, idempotencyKey?: string): Promise<{ delegated: true; sessionId: string; runId: string; provider: string; summary: string; action: string; memoryBodyIds: string[] }> {
    return this.call(MNEMON_WRITE_CHANNEL, 'supervise', this.scoped({ content, ...(idempotencyKey === undefined ? {} : { idempotencyKey }) }))
  }

  packTarget(): Promise<MnemonPackTarget> {
    return this.call(MNEMON_PACK_CHANNEL, 'target', this.scoped())
  }

  exportPack(): Promise<MnemonPackExport> {
    return this.call(MNEMON_PACK_CHANNEL, 'export', this.scoped())
  }

  inspectPack(base64: string, fileName?: string): Promise<MnemonPackPreview> {
    return this.call(MNEMON_PACK_CHANNEL, 'inspect', this.scoped({ base64, ...(fileName === undefined ? {} : { fileName }) }))
  }

  importPack(base64: string): Promise<MnemonPackImportResult> {
    return this.call(MNEMON_PACK_CHANNEL, 'import', this.scoped({ base64 }))
  }
}
