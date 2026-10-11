import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { homedir } from 'node:os'
import { isAbsolute, join, resolve } from 'node:path'
import type { ResolvedConfig } from './config.ts'
import type { StorageRoot } from './storage-root.ts'
import { createStorageRoot } from './storage-root.ts'
import type { StorageAreaInventory, StorageAreaKind, StorageAreaStatus, StorageScopeCatalog, StorageScopeInventory, StorageScopeKind } from "./protocol.ts"

export type { StorageAreaInventory, StorageAreaKind, StorageAreaStatus, StorageScopeCatalog, StorageScopeInventory, StorageScopeKind } from "./protocol.ts"

function expandHome(path: string): string {
  if (path === '~') return homedir()
  return path.startsWith('~/') ? join(homedir(), path.slice(2)) : path
}

function canonical(path: string): string {
  return resolve(expandHome(path))
}

function globalRoot(): string {
  const fromEnvironment = process.env.MNEMON_DATA_DIR?.trim()
  return canonical(fromEnvironment === undefined || fromEnvironment === '' ? '~/.mnemon' : fromEnvironment)
}

function safeBytes(path: string): number {
  if (!existsSync(path)) return 0
  try {
    const stats = statSync(path)
    if (stats.isFile()) return stats.size
    if (!stats.isDirectory()) return 0
    return readdirSync(path, { withFileTypes: true }).reduce((total, entry) => total + safeBytes(join(path, entry.name)), 0)
  } catch {
    return 0
  }
}

function readJson(path: string): unknown {
  return JSON.parse(readFileSync(path, 'utf8')) as unknown
}

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

function missing(kind: StorageAreaKind, path: string): StorageAreaInventory {
  return { kind, path, status: 'missing', bytes: 0, itemCount: 0, details: {} }
}

function runtimeArea(root: string): StorageAreaInventory {
  const path = join(root, 'runtime')
  const source = join(path, 'memories.json')
  if (!existsSync(source)) return missing('runtime', path)
  try {
    const file = record(readJson(source))
    if (file === undefined || !Array.isArray(file.entries)) throw new Error('memories.json is not a valid runtime-memory source')
    const entries = file.entries.map(record).filter((entry): entry is Record<string, unknown> => entry !== undefined)
    const userEntries = entries.filter(entry => entry.target === 'user').length
    const memoryEntries = entries.filter(entry => entry.target === 'memory').length
    const projectionsHealthy = existsSync(join(path, 'USER.md')) && existsSync(join(path, 'MEMORY.md'))
    return {
      kind: 'runtime', path,
      status: entries.length === 0 ? 'empty' : projectionsHealthy ? 'ready' : 'invalid',
      bytes: safeBytes(path), itemCount: entries.length,
      details: { userEntries, memoryEntries, projectionsHealthy, source: 'memories.json' },
      ...(projectionsHealthy ? {} : { issue: 'USER.md or MEMORY.md projection is missing' }),
    }
  } catch (error) {
    return { kind: 'runtime', path, status: 'invalid', bytes: safeBytes(path), itemCount: 0, details: {}, issue: error instanceof Error ? error.message : String(error) }
  }
}

function memorySpacesArea(root: string): StorageAreaInventory {
  const path = join(root, 'data')
  if (!existsSync(path)) return missing('memory-bodies', path)
  try {
    const registryPath = join(path, '.dsh-memory-bodies.json')
    const registry = existsSync(registryPath) ? record(readJson(registryPath)) : undefined
    const bodies = Array.isArray(registry?.bodies) ? registry.bodies.map(record).filter((body): body is Record<string, unknown> => body !== undefined) : []
    const databaseCount = readdirSync(path, { withFileTypes: true }).filter(entry => entry.isDirectory() && existsSync(join(path, entry.name, 'mnemon.db'))).length
    const activeCount = bodies.filter(body => body.active === true).length
    const invalidRegistry = existsSync(registryPath) && (registry?.version !== 1 || !Array.isArray(registry.bodies))
    return {
      kind: 'memory-bodies', path,
      status: invalidRegistry ? 'invalid' : databaseCount === 0 && bodies.length === 0 ? 'empty' : 'ready',
      bytes: safeBytes(path), itemCount: Math.max(bodies.length, databaseCount),
      details: { registeredBodies: bodies.length, activeBodies: activeCount, databases: databaseCount, registry: existsSync(registryPath) },
      ...(invalidRegistry ? { issue: 'memory-space registry is invalid' } : {}),
    }
  } catch (error) {
    return { kind: 'memory-bodies', path, status: 'invalid', bytes: safeBytes(path), itemCount: 0, details: {}, issue: error instanceof Error ? error.message : String(error) }
  }
}

function documentsArea(root: string): StorageAreaInventory {
  const path = join(root, 'documents')
  const indexPath = join(path, 'index.json')
  if (!existsSync(indexPath)) return missing('documents', path)
  try {
    const index = record(readJson(indexPath))
    if (index === undefined || !Array.isArray(index.documents)) throw new Error('index.json is not a valid Documents index')
    const documents = index.documents.map(record).filter((document): document is Record<string, unknown> => document !== undefined)
    const active = documents.filter(document => document.status === 'active').length
    const archived = documents.filter(document => document.status === 'archived').length
    return {
      kind: 'documents', path, status: documents.length === 0 ? 'empty' : 'ready',
      bytes: safeBytes(path), itemCount: documents.length,
      details: { activeDocuments: active, archivedDocuments: archived, index: 'index.json' },
    }
  } catch (error) {
    return { kind: 'documents', path, status: 'invalid', bytes: safeBytes(path), itemCount: 0, details: {}, issue: error instanceof Error ? error.message : String(error) }
  }
}

function stateArea(root: string): StorageAreaInventory {
  const path = join(root, 'state')
  if (!existsSync(path)) return missing('state', path)
  try {
    const files = readdirSync(path, { withFileTypes: true }).filter(entry => entry.isFile())
    const providerRegistry = join(path, 'memory-providers.json')
    let providerConnections = 0
    let providerServices = 0
    if (existsSync(providerRegistry)) {
      try {
        const registry = record(readJson(providerRegistry))
        providerConnections = Array.isArray(registry?.bodies) ? registry.bodies.length : 0
        providerServices = record(registry?.services) === undefined ? 0 : Object.keys(record(registry?.services)!).length
      } catch {}
    }
    return {
      kind: 'state', path, status: files.length === 0 ? 'empty' : 'ready', bytes: safeBytes(path), itemCount: files.length,
      details: { reviewLedger: existsSync(join(path, 'review-ledger.json')), providerServices, providerConnections, files: files.length },
    }
  } catch (error) {
    return { kind: 'state', path, status: 'invalid', bytes: safeBytes(path), itemCount: 0, details: {}, issue: error instanceof Error ? error.message : String(error) }
  }
}

function inspect(kind: StorageScopeKind, rawRoot: string | undefined, activeRoot: string): StorageScopeInventory {
  if (rawRoot === undefined) return { kind, configured: false, active: false, available: false, totalBytes: 0, areas: [], issue: 'scope is not configured' }
  const root = canonical(rawRoot)
  const areas = [runtimeArea(root), memorySpacesArea(root), documentsArea(root), stateArea(root)]
  const exists = existsSync(root)
  const available = exists && (() => { try { return statSync(root).isDirectory() } catch { return false } })()
  return {
    kind, root, configured: true, active: root === activeRoot, available,
    totalBytes: areas.reduce((total, area) => total + area.bytes, 0), areas,
    ...(exists && !available ? { issue: 'storage root is not a directory' } : {}),
  }
}

/** Read-only storage catalog. It never creates, moves, or repairs files. */
export class StorageScopeInspector {
  constructor(private readonly runner: Pick<StorageRoot, 'effectiveDataDir'>, private readonly config: Pick<ResolvedConfig, 'dataDir' | 'storageScope'>) {}

  catalog(workspaceRoot?: string): StorageScopeCatalog {
    const activeRoot = canonical(this.runner.effectiveDataDir())
    const global = globalRoot()
    const workspace = workspaceRoot === undefined || workspaceRoot.trim() === '' ? undefined : join(canonical(workspaceRoot), '.mnemon')
    const configuredDataDir = this.config.dataDir === undefined ? undefined : canonical(this.config.dataDir)
    const activeKind: StorageScopeKind = this.config.storageScope
    const custom = configuredDataDir !== undefined && configuredDataDir !== global && configuredDataDir !== workspace ? configuredDataDir : undefined
    const workspaces = workspace === undefined ? undefined : createStorageRoot({ ...this.config, storageScope: 'workspaces' }, workspaceRoot).effectiveDataDir()
    return {
      activeKind,
      activeRoot,
      scopes: [
        inspect('global', activeKind === 'global' ? activeRoot : global, activeRoot),
        inspect('workspace', activeKind === 'workspace' ? activeRoot : workspace, activeRoot),
        inspect('custom', activeKind === 'custom' ? activeRoot : custom, activeRoot),
        inspect('workspaces', activeKind === 'workspaces' ? activeRoot : workspaces, activeRoot),
      ].map(scope => ({ ...scope, active: scope.kind === activeKind })),
      generatedAt: new Date().toISOString(),
    }
  }
}

export function validateCustomStorageRoot(value: string): string {
  const path = value.trim()
  if (path === '') throw new Error('custom storage directory is required')
  if (path.includes('\0')) throw new Error('custom storage directory must not contain a null byte')
  const expanded = expandHome(path)
  if (!isAbsolute(expanded)) throw new Error('custom storage directory must be an absolute path or start with ~/')
  return canonical(path)
}
