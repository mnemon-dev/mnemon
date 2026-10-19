import { isDefaultSourceInstance, isWorkspaceStorageScope } from './protocol.ts'
import type { HostConnectionHandle, HostRpcHandler, RpcResult } from './dsh.ts'
import type { MnemonLifecycle } from './lifecycle.ts'
import type { LiveMnemonRuntime } from './runtime.ts'
import { assertParticipation } from './access.ts'
import { sourceFailure } from './source-session.ts'
import { isVersionComponentId, VersionUpdateManager } from './version-updates.ts'
import type { MemoryCapability, MemoryJsonValue, MemoryOperationScope, MemorySourceManagementInstance, MemorySourceManagementRequest } from '../core/contracts/index.ts'
import type { CreateMemoryBodyRequest as CreateMemorySpaceRequest, Insight, MemoryBodyCatalog as MemorySpaceCatalog, PreparedMemoryPlacement, RememberRequest } from 'dsh-mnemon-source-memory-spaces/contracts'
import type { RuntimeMemoryMutation } from 'dsh-mnemon-source-runtime/contracts'
import type { DocumentMutation } from 'dsh-mnemon-source-documents/contracts'
import { MNEMON_ACTIVATION_CHANNEL, MNEMON_PACK_CHANNEL, MNEMON_READ_CHANNEL, MNEMON_WRITE_CHANNEL, type MemoryCompositionStatus } from './protocol.ts'
export { MNEMON_ACTIVATION_CHANNEL, MNEMON_PACK_CHANNEL, MNEMON_READ_CHANNEL, MNEMON_WRITE_CHANNEL } from './protocol.ts'

function object(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('payload must be an object')
  return value as Record<string, unknown>
}
function requestedScope(payload: Record<string, unknown>): { workspaceId?: string; sessionId?: string } {
  return Object.fromEntries(['workspaceId', 'sessionId'].flatMap(key => {
    const value = payload[key]
    if (value === undefined) return []
    if (typeof value !== 'string') throw new Error(key + ' must be a string')
    return value.trim() === '' ? [] : [[key, value.trim()]]
  }))
}
function scoped(runtime: LiveMnemonRuntime, payload: Record<string, unknown>, lifecycle?: MnemonLifecycle) {
  const requested = requestedScope(payload)
  const route = runtime.route(requested)
  // Browser workspace ids, and sessions whose Agents are not loaded, resolve through the
  // authenticated DSH registry.
  const workspaceId = route.selectedWorkspace?.path ?? lifecycle?.workspaceRoot(requested.sessionId)
  const scope: MemoryOperationScope = {
    storage: route.graph.config.storageScope,
    ...(workspaceId === undefined ? {} : { workspaceId }),
    ...(requested.sessionId === undefined ? {} : { sessionId: requested.sessionId }),
  }
  return { ...route, scope, source: (typeId: string) => route.graph.source(typeId, scope) }
}
type ScopedRuntime = ReturnType<typeof scoped>
function requireAligned(runtime: ScopedRuntime): void {
  if (!runtime.aligned) throw new Error('the selected memory workspace differs from the current session; align the workbench before running an Agent-backed operation')
}
function requireWritable(runtime: ScopedRuntime): void {
  if (!runtime.graph.config.writeEnabled) throw new Error('dsh-mnemon is configured read-only (writeEnabled: false)')
}
function requireCapability(runtime: ScopedRuntime, typeId: string, capability: MemoryCapability): void {
  assertParticipation(runtime.graph.config, typeId, capability, 'manual')
}
/** A memory layer that is off takes no part in reads or writes, management and assistance included. */
function requireLayerOn(runtime: ScopedRuntime, sourceTypeId: string | undefined): void {
  if (sourceTypeId !== undefined && runtime.graph.config.memoryTopology.layers[sourceTypeId]?.enabled === false) {
    throw new Error(`Memory layer ${sourceTypeId} is off; turn it on to read or change it`)
  }
}
function sourceTypeOf(generation: { sourceInstances(): ReadonlyArray<{ sourceInstanceKey: string; sourceTypeId: string }> } | undefined, sourceInstanceKey: string): string | undefined {
  return generation?.sourceInstances().find(source => source.sourceInstanceKey === sourceInstanceKey)?.sourceTypeId
}
function success(value: unknown): RpcResult<unknown> { return { ok: true, value } }
function failure(error: unknown): RpcResult<unknown> {
  return { ok: false, error: { code: 'internal', message: error instanceof Error ? error.message : String(error), details: {} } }
}
function badRequest(message: string): RpcResult<unknown> {
  return { ok: false, error: { code: 'bad-request', message, details: { issues: [] } } }
}

const SPACE_READ_CAPABILITIES: Record<string, MemoryCapability> = {
  graph: 'graph', list: 'browse', entities: 'search', search: 'recall', related: 'related',
  bodies: 'status', 'body-directory': 'status', 'body-reconnect': 'status', 'provider-services': 'status', 'embedding-status': 'status',
}
const SPACE_WRITE_CAPABILITIES: Record<string, MemoryCapability> = {
  remember: 'write', link: 'link', forget: 'forget', 'body-create': 'write', 'body-update': 'write',
  'body-delete': 'forget', 'body-merge': 'write', 'provider-service-update': 'maintain',
}

// Optional product workflows. They are not Source or Core operations and are
// advertised only for the exact default instances that this Host coordinates.
const ASSISTANCE: Record<string, readonly string[]> = {
  'runtime': ['mutate'],
  'documents': ['mutate', 'archive'],
  'memory-spaces': ['agent-search', 'supervise', 'body-create', 'body-metadata-maintain'],
}
function assistance(source: MemorySourceManagementInstance, lifecycle?: MnemonLifecycle, runtime?: ScopedRuntime): readonly string[] {
  const controls = source.sourceTypeId === 'memory-spaces' ? ['activation'] : []
  if (lifecycle === undefined || !isDefaultSourceInstance(source.sourceInstanceKey, source.sourceTypeId)) return controls
  const taskAvailable = lifecycle.snapshot(runtime?.scope.sessionId, runtime?.scope.workspaceId).taskAgentAvailable
  return [...controls, ...(ASSISTANCE[source.sourceTypeId] ?? []).filter(operation => operation === 'mutate' || taskAvailable)]
}
async function catalog(runtime: ScopedRuntime, lifecycle?: MnemonLifecycle) {
  if (runtime.graph.memoryComposition.current() === undefined) return { generationId: 'unavailable', sources: [] }
  const lease = runtime.graph.memoryComposition.acquire()
  try {
    const value = await lease.generation.managementCatalog(runtime.scope)
    return { ...value, sources: value.sources.map(source => ({ ...source, assistance: assistance(source, lifecycle, runtime) })) }
  } finally { lease.release() }
}
async function compositionStatus(runtime: ScopedRuntime): Promise<MemoryCompositionStatus> {
  const composition = runtime.graph.memoryComposition.inspect()
  const strategyTypeId = runtime.graph.memoryComposition.current()?.strategy.definition.manifest.typeId
  return {
    serving: composition.servingGenerationId !== undefined,
    ...(strategyTypeId === undefined ? {} : { strategyTypeId }),
    evaluation: composition.evaluation, sources: (await catalog(runtime)).sources, configuration: runtime.graph.config.memoryTopology,
  }
}

async function assisted(runtime: ScopedRuntime, lifecycle: MnemonLifecycle, typeId: string, operation: string, input: Record<string, unknown>, signal?: AbortSignal, target?: { sourceInstanceKey: string; expectedRevision: string }): Promise<unknown> {
  const sessionId = runtime.scope.sessionId ?? ''
  const workspaceRoot = runtime.scope.workspaceId
  if (typeId === 'runtime' && operation === 'mutate') {
    requireCapability(runtime, typeId, 'write')
    const request = { ...input, ...(input.oldText ?? input.old_text) === undefined ? {} : { oldText: input.oldText ?? input.old_text } } as unknown as RuntimeMemoryMutation
    const lease = runtime.graph.memoryComposition.acquire()
    try {
      const sourceInstanceKey = target?.sourceInstanceKey ?? (await runtime.source(typeId).identity()).sourceInstanceKey
      const result = await lifecycle.manageSource(runtime.graph, {
        scope: runtime.scope, sourceInstanceKey, mode: 'mutate', operation: 'mutate', input: request as unknown as MemoryJsonValue,
        confirmed: true, expectedRevision: target?.expectedRevision ?? await lease.generation.managementRevision(sourceInstanceKey, runtime.scope, signal),
        ...(signal === undefined ? {} : { signal }),
      })
      return { ...object(result.value), revision: result.revision }
    } finally { lease.release() }
  }
  if (typeId === 'documents') {
    if (operation === 'archive') {
      requireCapability(runtime, typeId, 'archive')
      requireCapability(runtime, 'memory-spaces', 'write')
      return lifecycle.archiveDocument(sessionId, String(input.id ?? ''), workspaceRoot, signal)
    }
    if (operation === 'mutate') {
      requireCapability(runtime, typeId, 'write')
      const request = input as unknown as DocumentMutation
      if (runtime.aligned && runtime.liveSession && sessionId !== '') return lifecycle.mutateDocument(sessionId, request, signal)
      try {
        return await runtime.source(typeId).mutate('mutate', request, signal)
      } catch (error) {
        // A conversation whose Agent is not loaded makes room as its Agent would:
        // a task Agent archives the least recently used Document, then writes.
        if (!runtime.aligned || sessionId === '' || !sourceFailure(error, 'document-capacity')) throw error
        return lifecycle.mutateDocumentTask(sessionId, request, workspaceRoot, signal)
      }
    }
  }
  if (typeId !== 'memory-spaces') throw new Error('unsupported Source assistance operation')
  const source = runtime.source(typeId)
  switch (operation) {
    case 'agent-search': {
      requireCapability(runtime, typeId, 'recall')
      const recalled = await source.read<{ results: Insight[] }>('search', input, signal)
      const answer = await lifecycle.answerTask(sessionId, String(input.query ?? ''), recalled.results, workspaceRoot, signal)
      return { ...recalled, ...answer }
    }
    case 'supervise':
      requireCapability(runtime, typeId, 'write')
      return lifecycle.superviseTask(sessionId, String(input.content ?? ''), input.idempotencyKey === undefined ? undefined : String(input.idempotencyKey), workspaceRoot, signal)
    case 'body-create': {
      requireCapability(runtime, typeId, 'write')
      const request = input as unknown as CreateMemorySpaceRequest
      if (request.placement === undefined) return source.mutate('body-create', request, signal)
      requireAligned(runtime)
      const prepared = await source.read<PreparedMemoryPlacement>('prepare-body-placement', request, signal)
      const body = { name: request.name, description: request.description }
      // Without a loaded conversation Agent, a task Agent in the same workspace decides.
      const placementDecision = runtime.liveSession
        ? await lifecycle.placeProvider(sessionId, body, prepared, signal)
        : await lifecycle.placeProviderTask(sessionId, body, prepared, workspaceRoot, signal)
      return source.mutate('body-create', { request, placementDecision }, signal)
    }
    case 'body-metadata-maintain': {
      requireCapability(runtime, typeId, 'maintain')
      if (!Array.isArray(input.memoryBodyIds) || input.memoryBodyIds.some(id => typeof id !== 'string')) throw new Error('memoryBodyIds must be a string array')
      const ids = [...new Set((input.memoryBodyIds as string[]).map(id => id.trim()).filter(Boolean))]
      if (ids.length === 0 || ids.length > 20) throw new Error('metadata maintenance requires 1 through 20 Memory Spaces')
      const directory = await source.read<MemorySpaceCatalog>('body-directory', null, signal)
      for (const id of ids) {
        const body = directory.items.find(item => item.id === id)
        if (body === undefined || !body.active || body.providerEnabled === false) throw new Error('metadata maintenance requires an active Memory Space: ' + id)
      }
      const maintained = await lifecycle.maintainMetadata(sessionId, ids, workspaceRoot, signal)
      if (maintained.updates.length > 0) await source.mutate('body-metadata-update', { updates: maintained.updates }, signal)
      return maintained
    }
    default: throw new Error('unsupported Source assistance operation: ' + operation)
  }
}

export function createReadHandler(input: LiveMnemonRuntime, lifecycle?: MnemonLifecycle, versions?: VersionUpdateManager): HostRpcHandler {
  return async (endpoint, rawPayload, signal) => {
    try {
      const payload = object(rawPayload)
      if (endpoint === 'versions') {
        if (versions === undefined) throw new Error('version checks are unavailable')
        return success(await versions.check())
      }
      if (endpoint === 'task-agent-models') {
        if (lifecycle === undefined) throw new Error('Mnemon task Agent model directory is unavailable')
        return success(await lifecycle.taskAgentModels(payload.includeCatalog !== false))
      }
      const runtime = scoped(input, payload, lifecycle)
      if (Object.hasOwn(SPACE_READ_CAPABILITIES, endpoint)) {
        // Inspection remains available when model participation is disabled.
        if (SPACE_READ_CAPABILITIES[endpoint] !== 'status') requireCapability(runtime, 'memory-spaces', SPACE_READ_CAPABILITIES[endpoint]!)
        return success(await runtime.source('memory-spaces').read(endpoint, payload, signal))
      }
      switch (endpoint) {
        case 'source-management-catalog': return success(await catalog(runtime, lifecycle))
        case 'source-assistance': {
          if (lifecycle === undefined || payload.operation !== 'agent-search') throw new Error('Read-only Host assistance is unavailable')
          const source = (await catalog(runtime, lifecycle)).sources.find(item => item.sourceInstanceKey === payload.sourceInstanceKey)
          if (source === undefined || !source.assistance.includes('agent-search')) throw new Error('Host assistance is not available for this Source instance')
          const value = await assisted(runtime, lifecycle, source.sourceTypeId, 'agent-search', object(payload.input), signal)
          const current = (await catalog(runtime)).sources.find(item => item.sourceInstanceKey === source.sourceInstanceKey)
          return success({ revision: current?.revision ?? source.revision, value })
        }
        case 'source-management-read': {
          const lease = runtime.graph.memoryComposition.acquire()
          try {
            requireLayerOn(runtime, sourceTypeOf(lease.generation, String(payload.sourceInstanceKey ?? '')))
            return success(await lease.generation.executeManagement({
              scope: runtime.scope, sourceInstanceKey: String(payload.sourceInstanceKey ?? ''), mode: 'read',
              operation: String(payload.operation ?? ''), input: (payload.input ?? null) as MemoryJsonValue, confirmed: false,
              ...(signal === undefined ? {} : { signal }),
            }))
          } finally { lease.release() }
        }
        case 'memory-system': return success(await compositionStatus(runtime))
        case 'runtime-memory':
          requireCapability(runtime, 'runtime', 'read')
          return success(await runtime.source('runtime').read('snapshot', null, signal))
        case 'documents':
        case 'document':
        case 'document-search':
          requireCapability(runtime, 'documents', endpoint === 'document-search' ? 'search' : 'read')
          return success(await runtime.source('documents').read(endpoint === 'documents' ? 'snapshot' : endpoint === 'document' ? 'document' : 'search', payload, signal))
        case 'status':
        case 'status-summary': {
          let documents
          try { documents = await runtime.source('documents').read('snapshot', null, signal) } catch { /* Optional Source may be unavailable in this scope. */ }
          const composition = await compositionStatus(runtime)
          // Every Memory System page reads this status, so each one can say what a restart would load.
          let restartPending
          try { restartPending = versions?.restartStatus() } catch { /* The reminder is optional; the status is not. */ }
          const hasSpaces = composition.sources.some(source => source.sourceTypeId === 'memory-spaces')
          const status = hasSpaces ? await runtime.source('memory-spaces').read<Record<string, unknown>>(endpoint, payload, signal) : {
            healthy: composition.evaluation.state === 'ready', commandFound: false, cliPath: runtime.graph.config.cliPath ?? '',
            dataDir: runtime.graph.directory, mnemonDefaultStore: '', dshActiveStores: [],
            writeEnabled: runtime.graph.config.writeEnabled, defaultRecallLimit: runtime.graph.config.defaultRecallLimit,
          }
          return success({
            ...status,
            ...(versions === undefined ? {} : { dshMnemonVersion: versions.runningVersion }),
            ...(restartPending === undefined ? {} : { restartPending }),
            ...(lifecycle === undefined ? {} : { lifecycle: lifecycle.snapshot(runtime.scope.sessionId, isWorkspaceStorageScope(runtime.graph.config.storageScope) ? runtime.scope.workspaceId : undefined) }),
            ...(documents === undefined ? {} : { documents }),
            memorySystem: composition,
            storage: runtime.graph.storage.catalog(runtime.scope.workspaceId),
            workspaceContext: {
              mode: runtime.graph.config.storageScope, selectedRoot: runtime.selectedRoot, effectiveRoot: runtime.effectiveRoot, aligned: runtime.aligned,
              ...(runtime.selectedWorkspace === undefined ? {} : { selectedWorkspace: runtime.selectedWorkspace }),
              ...(runtime.effectiveWorkspace === undefined ? {} : { effectiveWorkspace: runtime.effectiveWorkspace }),
            },
          })
        }
        case 'agent-search':
          if (lifecycle === undefined) throw new Error('Mnemon Agent query is unavailable')
          return success(await assisted(runtime, lifecycle, 'memory-spaces', endpoint, payload, signal))
        case 'turn-activities':
        case 'turn-activity': {
          if (lifecycle === undefined) throw new Error('Mnemon turn activity is unavailable')
          const snapshot = lifecycle.turnActivities(String(payload.sessionId ?? ''))
          return success(endpoint === 'turn-activities' ? snapshot : snapshot.activities.find(activity => activity.turn === Number(payload.turn)) ?? null)
        }
        case 'assistant-message':
          if (lifecycle === undefined) throw new Error('Mnemon assistant message is unavailable')
          return success(lifecycle.assistantMessage(String(payload.sessionId ?? ''), String(payload.messageId ?? '')))
        default: return badRequest('unknown read endpoint: ' + endpoint)
      }
    } catch (error) { return failure(error) }
  }
}

const ACTIVATION_FIELDS = new Set(['memoryBodyId', 'active', 'sessionId', 'workspaceId'])
export function createActivationHandler(input: LiveMnemonRuntime): HostRpcHandler {
  return async (endpoint, rawPayload, signal) => {
    try {
      if (endpoint === 'source-assistance') {
        const payload = object(rawPayload)
        if (payload.operation !== 'activation' || payload.confirmed !== true) throw new Error('Unsupported activation operation')
        const fields = object(payload.input)
        if (Object.keys(fields).some(key => key !== 'memoryBodyId' && key !== 'active') || typeof fields.active !== 'boolean' || typeof fields.memoryBodyId !== 'string') throw new Error('Activation accepts only a Memory Space id and boolean state')
        const runtime = scoped(input, payload)
        requireWritable(runtime)
        const lease = runtime.graph.memoryComposition.acquire()
        try {
          const source = (await lease.generation.managementCatalog(runtime.scope)).sources.find(item => item.sourceInstanceKey === payload.sourceInstanceKey && item.sourceTypeId === 'memory-spaces')
          if (source === undefined) throw new Error('Memory Spaces Source instance is unavailable')
          requireLayerOn(runtime, source.sourceTypeId)
          return success(await lease.generation.executeManagement({
            scope: runtime.scope, sourceInstanceKey: source.sourceInstanceKey, mode: 'mutate', operation: 'body-update',
            input: fields as MemoryJsonValue, confirmed: true, expectedRevision: String(payload.expectedRevision ?? ''),
            ...(signal === undefined ? {} : { signal }),
          }))
        } finally { lease.release() }
      }
      if (endpoint !== 'body') return badRequest('unknown activation endpoint: ' + endpoint)
      const payload = object(rawPayload)
      const unexpected = Object.keys(payload).filter(field => !ACTIVATION_FIELDS.has(field))
      if (unexpected.length > 0) return badRequest('unsupported activation fields: ' + unexpected.join(', '))
      if (typeof payload.memoryBodyId !== 'string' || payload.memoryBodyId.trim() === '') return badRequest('memoryBodyId must be a non-empty string')
      if (typeof payload.active !== 'boolean') return badRequest('active must be a boolean')
      const runtime = scoped(input, payload)
      requireWritable(runtime)
      return success(await runtime.source('memory-spaces').mutate('body-update', { memoryBodyId: payload.memoryBodyId.trim(), active: payload.active }, signal))
    } catch (error) { return failure(error) }
  }
}

export function createWriteHandler(input: LiveMnemonRuntime, lifecycle?: MnemonLifecycle, versions?: VersionUpdateManager): HostRpcHandler {
  return async (endpoint, rawPayload, signal) => {
    try {
      const payload = object(rawPayload)
      if (endpoint === 'version-update') {
        if (versions === undefined) throw new Error('version updates are unavailable')
        if (!input.config.writeEnabled) throw new Error('dsh-mnemon is configured read-only (writeEnabled: false)')
        if (!isVersionComponentId(payload.component)) return badRequest('unknown version component')
        return success(await versions.update(payload.component))
      }
      const runtime = scoped(input, payload, lifecycle)
      requireWritable(runtime)
      if (endpoint === 'source-management-mutate') {
        const request: MemorySourceManagementRequest = {
          scope: runtime.scope, sourceInstanceKey: String(payload.sourceInstanceKey ?? ''), mode: 'mutate',
          operation: String(payload.operation ?? ''), input: (payload.input ?? null) as MemoryJsonValue,
          confirmed: payload.confirmed === true,
          ...(typeof payload.expectedRevision === 'string' ? { expectedRevision: payload.expectedRevision } : {}),
          ...(signal === undefined ? {} : { signal }),
        }
        requireLayerOn(runtime, sourceTypeOf(runtime.graph.memoryComposition.current(), request.sourceInstanceKey))
        if (lifecycle !== undefined) return success(await lifecycle.manageSource(runtime.graph, request))
        const lease = runtime.graph.memoryComposition.acquire()
        try {
          return success(await lease.generation.executeManagement(request))
        } finally { lease.release() }
      }
      if (endpoint === 'source-assistance') {
        if (lifecycle === undefined) throw new Error('Host assistance is unavailable')
        const source = (await catalog(runtime, lifecycle)).sources.find(item => item.sourceInstanceKey === payload.sourceInstanceKey)
        const operation = String(payload.operation ?? '')
        if (source === undefined || !source.assistance.includes(operation)) throw new Error('Host assistance is not available for this Source instance')
        requireLayerOn(runtime, source.sourceTypeId)
        if (operation !== 'agent-search' && (payload.confirmed !== true || payload.expectedRevision !== source.revision)) throw new Error('Host assistance requires confirmation of the current Source revision')
        const value = await assisted(runtime, lifecycle, source.sourceTypeId, operation, object(payload.input), signal, { sourceInstanceKey: source.sourceInstanceKey, expectedRevision: source.revision })
        if (source.sourceTypeId === 'runtime') return success({ revision: object(value).revision, value })
        const current = (await catalog(runtime)).sources.find(item => item.sourceInstanceKey === source.sourceInstanceKey)
        return success({ revision: current?.revision ?? source.revision, value })
      }
      if (endpoint === 'runtime-memory') {
        if (lifecycle !== undefined) return success(await assisted(runtime, lifecycle, 'runtime', 'mutate', payload, signal))
        requireCapability(runtime, 'runtime', 'write')
        return success(await runtime.source('runtime').mutate('mutate', payload, signal))
      }
      if (endpoint === 'document') {
        const operation = payload.action === 'archive' ? 'archive' : 'mutate'
        if (lifecycle !== undefined) return success(await assisted(runtime, lifecycle, 'documents', operation, payload, signal))
        requireCapability(runtime, 'documents', operation === 'archive' ? 'archive' : 'write')
        return success(await runtime.source('documents').mutate(operation, payload, signal))
      }
      if (endpoint === 'supervise' || endpoint === 'body-metadata-maintain' || endpoint === 'body-create' && payload.placement !== undefined) {
        if (lifecycle === undefined) throw new Error('Host assistance is unavailable')
        return success(await assisted(runtime, lifecycle, 'memory-spaces', endpoint, payload, signal))
      }
      if (Object.hasOwn(SPACE_WRITE_CAPABILITIES, endpoint)) {
        requireCapability(runtime, 'memory-spaces', SPACE_WRITE_CAPABILITIES[endpoint]!)
        if (endpoint === 'remember' && lifecycle !== undefined && runtime.aligned && runtime.liveSession && runtime.scope.sessionId) {
          return success(await lifecycle.remember(runtime.scope.sessionId, { ...payload, source: 'user' } as unknown as RememberRequest, signal))
        }
        return success(await runtime.source('memory-spaces').mutate(endpoint, endpoint === 'remember' ? { ...payload, source: 'user' } : payload, signal))
      }
      return badRequest('unknown write endpoint: ' + endpoint)
    } catch (error) { return failure(error) }
  }
}

/** Pack data stays inside the selected storage root and DSH authentication. */
export function createPackHandler(input: LiveMnemonRuntime): HostRpcHandler {
  return async (endpoint, rawPayload) => {
    try {
      const payload = object(rawPayload)
      const runtime = scoped(input, payload)
      const manager = runtime.graph.packs
      if (endpoint === 'target') return success(manager.target())
      if (endpoint === 'export') return success(await manager.exportPack('full'))
      if (endpoint === 'inspect') return success(manager.inspectPack(String(payload.base64 ?? ''), payload.fileName === undefined ? undefined : String(payload.fileName)))
      if (endpoint === 'import') {
        requireWritable(runtime)
        const result = await manager.importPack(String(payload.base64 ?? ''), { mode: 'merge' })
        if ((await catalog(runtime)).sources.some(source => source.sourceTypeId === 'memory-spaces')) await runtime.source('memory-spaces').mutate('reload', {})
        return success(result)
      }
      return badRequest('unknown Pack endpoint: ' + endpoint)
    } catch (error) { return failure(error) }
  }
}

export function registerRpc(connection: HostConnectionHandle, input: LiveMnemonRuntime, lifecycle?: MnemonLifecycle, versions?: VersionUpdateManager): {
  read: HostRpcHandler
  activation: HostRpcHandler
  write: HostRpcHandler
  pack: HostRpcHandler
} {
  const versionManager = versions ?? new VersionUpdateManager({ mnemonCliPath: () => input.config.cliPath })
  const readHandler = createReadHandler(input, lifecycle, versionManager)
  const activationHandler = createActivationHandler(input)
  const writeHandler = createWriteHandler(input, lifecycle, versionManager)
  const packHandler = createPackHandler(input)
  connection.rpc.handle(MNEMON_READ_CHANNEL, readHandler)
  connection.rpc.handle(MNEMON_ACTIVATION_CHANNEL, activationHandler)
  connection.rpc.handle(MNEMON_WRITE_CHANNEL, writeHandler)
  connection.rpc.handle(MNEMON_PACK_CHANNEL, packHandler)
  return { read: readHandler, activation: activationHandler, write: writeHandler, pack: packHandler }
}
