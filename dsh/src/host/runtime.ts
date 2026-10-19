import { isDefaultSourceInstance, isWorkspaceStorageScope } from './protocol.ts'
import { resolve } from 'node:path'
import type { ResolvedConfig } from './config.ts'
import type { HostAgent, HostAgentsService, HostWorkspace, HostWorkspaceRegistry } from './dsh.ts'
import { MnemonPackManager } from './pack.ts'
import { StorageScopeInspector } from './storage-scope.ts'
import { createStorageRoot } from './storage-root.ts'
import { canonicalWorkspacePath } from './workspace-storage.ts'
import { SourceSession } from './source-session.ts'
import { MemoryRuntime } from '../core/runtime.ts'
import type { MemoryGenerationHost } from '../core/generation.ts'
import { ComposableMemoryTurnManager } from '../core/turns.ts'
import { MEMORY_CAPABILITIES, type MemoryOperationScope } from '../core/contracts/index.ts'
import { allowsParticipation } from './access.ts'
import type { CompileMemoryGenerationOptions } from '../core/composition.ts'
import { MemoryExecutions } from './memory-executions.ts'

export interface MnemonRuntimeGraph {
  readonly config: ResolvedConfig
  readonly directory: string
  readonly storage: StorageScopeInspector
  readonly packs: MnemonPackManager
  readonly memoryComposition: MemoryGenerationHost
  readonly composableTurns: ComposableMemoryTurnManager
  source(typeId: string, scope?: MemoryOperationScope): SourceSession
  retire(): void
  dispose(): void
}

export interface MnemonAgentRuntimeSource {
  readonly config: ResolvedConfig
  readonly executions: MemoryExecutions
  forAgent(agent: HostAgent): MnemonRuntimeGraph
  bindAgentRuntime(agentId: string, graph: MnemonRuntimeGraph): () => void
}

export function agentScope(agent: HostAgent, config: ResolvedConfig): MemoryOperationScope {
  const workspaceId = agent.session.header?.cwd?.trim()
  return { storage: config.storageScope, ...(workspaceId ? { workspaceId: resolve(workspaceId) } : {}), sessionId: agent.id, agentId: agent.id }
}

/** Shared by production graphs and read-only View previews. */
export function memoryGenerationOptions(config: ResolvedConfig, workspaceRoot: string | undefined): CompileMemoryGenerationOptions {
  const directory = createStorageRoot(config, workspaceRoot).effectiveDataDir()
  const userDirectory = config.runtimeUserScope === 'global' ? createStorageRoot({ storageScope: 'global' }).effectiveDataDir() : directory
  return {
    strategyTypeId: config.memoryTopology.strategyId,
    // A main Strategy switched off in the DSH Plugins page must not leave the
    // Host without a View while exactly one other Strategy remains.
    strategyFallback: 'sole-strategy',
    sourceTimeoutMs: config.timeoutMs,
    sourceCapabilities: installed => MEMORY_CAPABILITIES.filter(capability =>
      (config.writeEnabled || !['write', 'archive', 'link', 'forget', 'maintain', 'import'].includes(capability))
      && allowsParticipation(config, installed.definition.manifest.typeId, capability, 'automatic')),
    sourceConfiguration: installed => {
      const type = installed.definition.manifest.typeId
      if (!isDefaultSourceInstance(installed.instanceKey, type)) return {}
      if (type === 'runtime') return { dataDir: directory, userDataDir: userDirectory, memoryLimitBytes: config.runtimeMemory.memoryLimitBytes, userLimitBytes: config.runtimeMemory.userLimitBytes }
      if (type === 'documents') return { dataDir: directory }
      if (type === 'memory-spaces') return JSON.parse(JSON.stringify({
        dataDir: directory, cliPath: config.cliPath, store: config.store, timeoutMs: config.timeoutMs,
        defaultRecallLimit: config.defaultRecallLimit, writeEnabled: config.writeEnabled,
        embedding: config.embedding, recallQuality: config.recallQuality, persistenceStrategy: config.persistenceStrategy,
      }))
      return {}
    },
  }
}

/** One default-product scope over the single Composable Runtime. */
export function createRuntimeGraph(config: ResolvedConfig, workspaceRoot: string | undefined, extensions: MemoryRuntime): MnemonRuntimeGraph {
  const root = createStorageRoot(config, workspaceRoot)
  const directory = root.effectiveDataDir()
  const attachment = extensions.attachGeneration(memoryGenerationOptions(config, workspaceRoot))
  const evaluation = attachment.host.inspect().evaluation
  if (evaluation.state === 'rejected') {
    void attachment.dispose()
    throw new Error(evaluation.diagnostics.map(value => value.message).join('; '))
  }
  const composableTurns = new ComposableMemoryTurnManager(attachment.host)
  let disposed = false
  return {
    config, directory, storage: new StorageScopeInspector(root, config), packs: new MnemonPackManager(root, config),
    memoryComposition: attachment.host, composableTurns,
    source: (type, scope = { storage: config.storageScope, ...(workspaceRoot === undefined ? {} : { workspaceId: workspaceRoot }) }) => new SourceSession(attachment.host, composableTurns, type, scope),
    retire: () => attachment.release(),
    dispose: () => {
      if (disposed) return
      disposed = true
      composableTurns.dispose()
      void attachment.dispose()
    },
  }
}

/** Resolve every property access against one generation, binding methods to it. */
function liveProxy<T extends object>(resolve: () => T): T {
  return new Proxy({} as T, {
    get(_placeholder, property) {
      const target = resolve()
      const value = Reflect.get(target, property, target) as unknown
      return typeof value === 'function' ? value.bind(target) : value
    },
    has(_placeholder, property) {
      return property in resolve()
    },
    ownKeys() {
      return Reflect.ownKeys(resolve())
    },
    getOwnPropertyDescriptor(_placeholder, property) {
      const descriptor = Reflect.getOwnPropertyDescriptor(resolve(), property)
      return descriptor === undefined ? undefined : { ...descriptor, configurable: true }
    },
  })
}

/**
 * Stable faces handed to DSH registrations. `swap` is synchronous and contains
 * no user code, so all faces move to the same prevalidated generation in one
 * JavaScript turn. A method obtained before the swap stays bound to its old
 * generation until that invocation settles.
 */
export class LiveMnemonRuntime implements MnemonAgentRuntimeSource {
  readonly executions = new MemoryExecutions(this)
  private current: MnemonRuntimeGraph
  private readonly workspaceGraphs = new Map<string, MnemonRuntimeGraph>()
  private readonly agentGraphs = new Map<string, { token: symbol; graph: MnemonRuntimeGraph }>()
  private readonly retiredGraphs = new Set<MnemonRuntimeGraph>()
  private closed = false

  readonly config: ResolvedConfig
  readonly storage: StorageScopeInspector
  readonly packs: MnemonPackManager

  constructor(initial: MnemonRuntimeGraph, private readonly workspaceRegistry: HostWorkspaceRegistry | undefined, private readonly agents: Pick<HostAgentsService, 'get'> | undefined, private readonly extensions: MemoryRuntime) {
    this.current = initial
    this.config = liveProxy(() => this.current.config)
    this.storage = liveProxy(() => this.current.storage)
    this.packs = liveProxy(() => this.current.packs)
  }

  swap(next: MnemonRuntimeGraph): void {
    if (this.closed) {
      next.dispose()
      throw new Error('Mnemon runtime is disposed')
    }
    const previous = this.current
    this.current = next
    this.retireGraph(previous)
    for (const graph of this.workspaceGraphs.values()) this.retireGraph(graph)
    this.workspaceGraphs.clear()
  }

  snapshot(): MnemonRuntimeGraph {
    this.assertOpen()
    return this.current
  }

  bindAgentRuntime(agentId: string, graph: MnemonRuntimeGraph): () => void {
    this.assertOpen()
    const id = agentId.trim()
    if (id === '') throw new Error('Mnemon runtime binding requires an Agent id')
    if (this.agentGraphs.has(id)) throw new Error(`Mnemon runtime is already pinned for Agent ${id}`)
    const token = Symbol(id)
    this.agentGraphs.set(id, { token, graph })
    return () => {
      if (this.agentGraphs.get(id)?.token !== token) return
      this.agentGraphs.delete(id)
      this.collectRetired(graph)
    }
  }

  dispose(): void {
    if (this.closed) return
    this.executions.dispose()
    this.closed = true
    const graphs = new Set<MnemonRuntimeGraph>([
      this.current,
      ...this.workspaceGraphs.values(),
      ...[...this.agentGraphs.values()].map(binding => binding.graph),
      ...this.retiredGraphs,
    ])
    this.agentGraphs.clear()
    this.workspaceGraphs.clear()
    this.retiredGraphs.clear()
    for (const graph of graphs) graph.dispose()
  }

  /** Resolve the runtime that must serve one Agent execution. */
  forAgent(agent: HostAgent): MnemonRuntimeGraph {
    this.assertOpen()
    const pinned = this.agentGraphs.get(agent.id)
    if (pinned !== undefined) return pinned.graph
    const parentSession = agent.session.header?.origin === 'subagent' ? agent.session.header.parentSession?.trim() : undefined
    const inherited = parentSession === undefined || parentSession === '' ? undefined : this.agentGraphs.get(parentSession)
    if (inherited !== undefined) return inherited.graph
    if (!isWorkspaceStorageScope(this.current.config.storageScope)) return this.current
    const cwd = agent.session.header?.cwd?.trim()
    if (cwd === undefined || cwd === '') throw new Error('the current DSH session has no workspace for Mnemon')
    return this.forWorkspacePath(cwd)
  }

  /** Resolve an authorized DSH workspace selected by the Web workbench. */
  forWorkspaceId(workspaceId: string): MnemonRuntimeGraph {
    this.assertOpen()
    const workspace = this.requireWorkspace(workspaceId)
    return isWorkspaceStorageScope(this.current.config.storageScope) ? this.forWorkspacePath(workspace.path) : this.current
  }

  /** Resolve a Web request, preferring its explicit inspection workspace. */
  route(request: { workspaceId?: string; sessionId?: string }): {
    graph: MnemonRuntimeGraph
    selectedWorkspace?: HostWorkspace
    effectiveWorkspace?: HostWorkspace
    selectedRoot: string
    effectiveRoot: string
    aligned: boolean
    /** The request names a session whose Agent is loaded, so work can run in that conversation. */
    liveSession: boolean
  } {
    this.assertOpen()
    const effectiveAgent = this.agent(request.sessionId)
    // DSH loads a listed session's Agent only when the session runs, so a session opened from the
    // list, or just switched to, may have none yet; DSH's workspace registry still lists it.
    const effectiveWorkspace = effectiveAgent === undefined
      ? this.workspaceForSession(request.sessionId)
      : this.workspaceForPath(effectiveAgent.session.header?.cwd)
    const selectedWorkspace = request.workspaceId === undefined || request.workspaceId.trim() === ''
      ? effectiveWorkspace
      : this.requireWorkspace(request.workspaceId)
    const effectiveGraph = effectiveAgent !== undefined ? this.forAgent(effectiveAgent)
      : effectiveWorkspace !== undefined && isWorkspaceStorageScope(this.current.config.storageScope) ? this.forWorkspacePath(effectiveWorkspace.path) : this.current
    const graph = selectedWorkspace === undefined
      ? effectiveGraph
      : isWorkspaceStorageScope(this.current.config.storageScope) ? this.forWorkspacePath(selectedWorkspace.path) : this.current
    const selectedRoot = resolve(graph.directory)
    const effectiveRoot = resolve(effectiveGraph.directory)
    return {
      graph,
      ...(selectedWorkspace === undefined ? {} : { selectedWorkspace }),
      ...(effectiveWorkspace === undefined ? {} : { effectiveWorkspace }),
      selectedRoot,
      effectiveRoot,
      aligned: selectedRoot === effectiveRoot,
      liveSession: effectiveAgent !== undefined,
    }
  }

  private forWorkspacePath(workspaceRoot: string): MnemonRuntimeGraph {
    const key = this.current.config.storageScope === 'workspaces' ? canonicalWorkspacePath(resolve(workspaceRoot)) : resolve(workspaceRoot)
    let graph = this.workspaceGraphs.get(key)
    if (graph === undefined) {
      graph = createRuntimeGraph(this.current.config, key, this.extensions)
      this.workspaceGraphs.set(key, graph)
    }
    return graph
  }

  private retireGraph(graph: MnemonRuntimeGraph): void {
    graph.retire()
    if ([...this.agentGraphs.values()].some(binding => binding.graph === graph)) this.retiredGraphs.add(graph)
    else graph.dispose()
  }

  private collectRetired(graph: MnemonRuntimeGraph): void {
    if (!this.retiredGraphs.has(graph)) return
    if ([...this.agentGraphs.values()].some(binding => binding.graph === graph)) return
    this.retiredGraphs.delete(graph)
    graph.dispose()
  }

  private assertOpen(): void {
    if (this.closed) throw new Error('Mnemon runtime is disposed')
  }

  private agent(sessionId?: string): HostAgent | undefined {
    const normalized = sessionId?.trim()
    return normalized === undefined || normalized === '' ? undefined : this.agents?.get(normalized)
  }

  private requireWorkspace(workspaceId: string): HostWorkspace {
    const normalized = workspaceId.trim()
    const workspace = normalized === '' ? undefined : this.workspaceRegistry?.get(normalized)
    if (workspace === undefined) throw new Error('selected DSH workspace is unavailable')
    return workspace
  }

  private workspaceForSession(sessionId?: string): HostWorkspace | undefined {
    const normalized = sessionId?.trim()
    if (normalized === undefined || normalized === '') return undefined
    return this.workspaceRegistry?.list().find(workspace => workspace.sessionIds?.includes(normalized) === true)
  }

  private workspaceForPath(path?: string): HostWorkspace | undefined {
    const normalized = path?.trim()
    if (normalized === undefined || normalized === '') return undefined
    const canonical = resolve(normalized)
    return this.workspaceRegistry?.list().find(workspace => resolve(workspace.path) === canonical)
  }
}
