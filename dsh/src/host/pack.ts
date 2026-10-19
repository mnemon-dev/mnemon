import { createHash, randomUUID } from 'node:crypto'
import {
  closeSync,
  copyFileSync,
  existsSync,
  fstatSync,
  lstatSync,
  mkdirSync,
  openSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs'
import { basename, dirname, join, resolve, sep } from 'node:path'
import { homedir } from 'node:os'
import { strFromU8, strToU8, unzipSync, zipSync, type Unzipped, type Zippable } from 'fflate'
import type { ResolvedConfig } from './config.ts'
import { DOCUMENTS_ACTIVE_LIMIT_BYTES, DOCUMENTS_VERSION, type DocumentRecord } from 'dsh-mnemon-source-documents/contracts'
import { RUNTIME_ENTRY_DELIMITER, RUNTIME_MEMORY_LIMITS, RUNTIME_MEMORY_VERSION, type RuntimeMemoryEntry, type RuntimeMemoryLimits, type RuntimeMemoryTarget } from 'dsh-mnemon-source-runtime/contracts'
import { createStorageRoot, type StorageRoot } from './storage-root.ts'
import { MNEMON_PACK_COMPONENTS, type MnemonPackComponent, type MnemonPackComponentSummary, type MnemonPackExport, type MnemonPackImportMode, type MnemonPackImportResult, type MnemonPackManifest, type MnemonPackPreview, type MnemonPackScope, type MnemonPackTarget } from "./protocol.ts"

export type { MnemonPackComponent, MnemonPackComponentSummary, MnemonPackExport, MnemonPackImportMode, MnemonPackImportResult, MnemonPackManifest, MnemonPackPreview, MnemonPackScope } from "./protocol.ts"

export const MNEMON_PACK_FORMAT = 'mnemonpack'
export const MNEMON_PACK_VERSION = 1
export const MNEMON_PACK_MIME = 'application/zip'
export const MNEMON_PACK_MAX_ARCHIVE_BYTES = 48 * 1024 * 1024
export const MNEMON_PACK_MAX_EXPANDED_BYTES = 256 * 1024 * 1024

const MAX_FILE_BYTES = 128 * 1024 * 1024
const MAX_FILES = 4096
const LOCK_TIMEOUT_MS = 5_000
const LOCK_STALE_MS = 30_000
const LOCK_RETRY_MS = 20
const COMPONENT_DIRECTORIES = { runtime: 'runtime', documents: 'documents', 'memory-spaces': 'data' } as const
const COMPONENT_ORDER = MNEMON_PACK_COMPONENTS
const BODY_ID = /^[a-zA-Z0-9][a-zA-Z0-9_-]*$/
const SQLITE_HEADER = Buffer.from('SQLite format 3\0', 'binary')

interface ChecksumFile {
  algorithm: 'sha256'
  files: Record<string, string>
}

interface ParsedPack {
  archiveBytes: number
  expandedBytes: number
  files: Unzipped
  manifest: MnemonPackManifest
}

interface RuntimeFile {
  version: 1
  entries: RuntimeMemoryEntry[]
}

interface DocumentIndex {
  version: 1
  documents: DocumentRecord[]
}

interface StoredBody {
  id: string
  name: string
  description: string
  active: boolean
  createdAt: string
  updatedAt: string
}

interface BodyRegistry {
  version: 1
  bodies: StoredBody[]
}

function record(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

function utf8(bytes: Uint8Array, label: string): string {
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    throw new Error(`${label} is not valid UTF-8`)
  }
}

function json(bytes: Uint8Array, label: string): unknown {
  try {
    return JSON.parse(utf8(bytes, label)) as unknown
  } catch (error) {
    if (error instanceof SyntaxError) throw new Error(`${label} is not valid JSON`)
    throw error
  }
}

function sha256(value: Uint8Array | string): string {
  return createHash('sha256').update(value).digest('hex')
}

function safeArchivePath(path: string): void {
  if (path === '' || path.length > 512 || path.includes('\0') || path.includes('\\') || path.startsWith('/') || /^[a-zA-Z]:/.test(path)) {
    throw new Error(`unsafe Pack entry path: ${JSON.stringify(path)}`)
  }
  const parts = path.split('/')
  if (parts.some(part => part === '' || part === '.' || part === '..')) throw new Error(`unsafe Pack entry path: ${JSON.stringify(path)}`)
}

function payloadComponent(path: string): MnemonPackComponent | undefined {
  if (path.startsWith('payload/runtime/')) return 'runtime'
  if (path.startsWith('payload/documents/')) return 'documents'
  if (path.startsWith('payload/data/')) return 'memory-spaces'
  return undefined
}

function allowedPayloadPath(path: string): boolean {
  if (path === 'payload/runtime/memories.json' || path === 'payload/runtime/USER.md' || path === 'payload/runtime/MEMORY.md') return true
  if (path === 'payload/documents/index.json') return true
  if (/^payload\/documents\/(active|archived)\/[a-zA-Z0-9._-]+\.md$/u.test(path)) return true
  if (path === 'payload/data/.dsh-memory-bodies.json') return true
  return /^payload\/data\/[a-zA-Z0-9][a-zA-Z0-9_-]*\/mnemon\.db$/u.test(path)
}

function componentsForScope(scope: MnemonPackScope): MnemonPackComponent[] {
  if (scope === 'full') return [...COMPONENT_ORDER]
  if (!COMPONENT_ORDER.includes(scope)) throw new Error('Mnemon Pack scope must be full, runtime, documents, or memory-spaces')
  return [scope]
}

function parseManifest(value: unknown): MnemonPackManifest {
  const manifest = record(value)
  if (manifest?.format !== MNEMON_PACK_FORMAT || manifest.version !== MNEMON_PACK_VERSION) throw new Error('unsupported Mnemon Pack format or version')
  if (manifest.scope !== 'full' && !COMPONENT_ORDER.includes(manifest.scope as MnemonPackComponent)) throw new Error('Mnemon Pack scope is invalid')
  if (typeof manifest.exportedAt !== 'string') throw new Error('Mnemon Pack exportedAt is invalid')
  if (!Array.isArray(manifest.components)) throw new Error('Mnemon Pack components are invalid')
  const components = manifest.components.map(String) as MnemonPackComponent[]
  if (components.length === 0 || new Set(components).size !== components.length || components.some(component => !COMPONENT_ORDER.includes(component))) {
    throw new Error('Mnemon Pack components are invalid')
  }
  const expected = componentsForScope(manifest.scope as MnemonPackScope)
  if (components.length !== expected.length || expected.some(component => !components.includes(component))) throw new Error('Mnemon Pack scope does not match its components')
  const source = record(manifest.source)
  if (source?.plugin !== 'dsh-mnemon' || typeof source.pluginVersion !== 'string') throw new Error('Mnemon Pack source is invalid')
  if (!Array.isArray(manifest.summary)) throw new Error('Mnemon Pack summary is invalid')
  const summary = manifest.summary.map((entry): MnemonPackComponentSummary => {
    const item = record(entry)
    const component = item?.component as MnemonPackComponent
    if (!COMPONENT_ORDER.includes(component) || !Number.isSafeInteger(item?.files) || !Number.isSafeInteger(item?.bytes) || !Number.isSafeInteger(item?.items)
      || Number(item?.files) < 0 || Number(item?.bytes) < 0 || Number(item?.items) < 0) {
      throw new Error('Mnemon Pack summary entry is invalid')
    }
    return { component, files: Number(item!.files), bytes: Number(item!.bytes), items: Number(item!.items) }
  })
  return {
    format: MNEMON_PACK_FORMAT,
    version: MNEMON_PACK_VERSION,
    scope: manifest.scope as MnemonPackScope,
    exportedAt: manifest.exportedAt,
    source: { plugin: 'dsh-mnemon', pluginVersion: source.pluginVersion },
    components,
    summary,
  }
}

function decodeArchive(base64: string): Buffer {
  if (typeof base64 !== 'string' || base64 === '' || base64.length > Math.ceil(MNEMON_PACK_MAX_ARCHIVE_BYTES / 3) * 4 + 4) throw new Error('Mnemon Pack archive is empty or too large')
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/u.test(base64)) throw new Error('Mnemon Pack payload is not valid base64')
  const bytes = Buffer.from(base64, 'base64')
  if (bytes.length === 0 || bytes.length > MNEMON_PACK_MAX_ARCHIVE_BYTES) throw new Error('Mnemon Pack archive is empty or too large')
  return bytes
}

function parseArchive(base64: string, runtimeLimits: RuntimeMemoryLimits = RUNTIME_MEMORY_LIMITS): ParsedPack {
  const archive = decodeArchive(base64)
  let count = 0
  let expandedBytes = 0
  const files = unzipSync(archive, {
    filter(info) {
      safeArchivePath(info.name)
      if (info.name.endsWith('/')) return false
      count += 1
      expandedBytes += info.originalSize
      if (count > MAX_FILES) throw new Error(`Mnemon Pack contains more than ${MAX_FILES} files`)
      if (info.originalSize > MAX_FILE_BYTES || expandedBytes > MNEMON_PACK_MAX_EXPANDED_BYTES) throw new Error('Mnemon Pack expanded data exceeds the safety limit')
      return true
    },
  })
  const names = Object.keys(files)
  if (!names.includes('manifest.json') || !names.includes('checksums.json')) throw new Error('Mnemon Pack is missing manifest.json or checksums.json')
  for (const path of names) {
    safeArchivePath(path)
    if (path !== 'manifest.json' && path !== 'checksums.json' && !allowedPayloadPath(path)) throw new Error(`unsupported Mnemon Pack entry: ${path}`)
  }
  const manifest = parseManifest(json(files['manifest.json']!, 'manifest.json'))
  const checksumValue = record(json(files['checksums.json']!, 'checksums.json'))
  const checksumFiles = record(checksumValue?.files)
  if (checksumValue?.algorithm !== 'sha256' || checksumFiles === undefined) throw new Error('Mnemon Pack checksums are invalid')
  const payloadNames = names.filter(path => path.startsWith('payload/')).sort()
  if (Object.keys(checksumFiles).length !== payloadNames.length) throw new Error('Mnemon Pack checksum inventory does not match the payload')
  for (const path of payloadNames) {
    const component = payloadComponent(path)
    if (component === undefined || !manifest.components.includes(component)) throw new Error(`Mnemon Pack payload is outside its declared components: ${path}`)
    const expected = checksumFiles[path]
    if (typeof expected !== 'string' || expected !== sha256(files[path]!)) throw new Error(`Mnemon Pack checksum mismatch: ${path}`)
  }
  validatePackPayload(files, manifest.components, runtimeLimits)
  const actualSummary = summaryFor(manifest.components, files, runtimeLimits)
  return { archiveBytes: archive.length, expandedBytes, files, manifest: { ...manifest, summary: actualSummary } }
}

function parseRuntime(value: unknown, limits: RuntimeMemoryLimits = RUNTIME_MEMORY_LIMITS): RuntimeFile {
  const source = record(value)
  if (source?.version !== RUNTIME_MEMORY_VERSION || !Array.isArray(source.entries)) throw new Error('runtime memories.json is invalid')
  const entries = source.entries.map((raw): RuntimeMemoryEntry => {
    const entry = record(raw)
    if (typeof entry?.content !== 'string' || (entry.target !== 'memory' && entry.target !== 'user') || !['critical', 'normal', 'low'].includes(String(entry.importance))) {
      throw new Error('runtime memories.json contains an invalid entry')
    }
    if (typeof entry.created_at !== 'string' || typeof entry.updated_at !== 'string') throw new Error('runtime memories.json contains invalid timestamps')
    const content = entry.content.trim().replace(/\s+/gu, ' ')
    if (content === '' || content.includes('§') || Buffer.byteLength(content, 'utf8') > 8 * 1024) throw new Error('runtime memories.json contains invalid content')
    return { content, target: entry.target, importance: entry.importance as RuntimeMemoryEntry['importance'], created_at: entry.created_at, updated_at: entry.updated_at }
  })
  for (const target of ['user', 'memory'] as const) {
    const used = runtimeBytes(entries, target)
    if (used > limits[target]) throw new Error(`runtime ${target} memory exceeds its ${limits[target]} byte limit`)
  }
  return { version: 1, entries }
}

function runtimeBytes(entries: RuntimeMemoryEntry[], target: RuntimeMemoryTarget): number {
  return Buffer.byteLength(entries.filter(entry => entry.target === target).map(entry => entry.content).join(RUNTIME_ENTRY_DELIMITER), 'utf8')
}

function runtimeProjection(entries: RuntimeMemoryEntry[], target: RuntimeMemoryTarget): string {
  const content = entries.filter(entry => entry.target === target).map(entry => entry.content).join(RUNTIME_ENTRY_DELIMITER)
  return content === '' ? '' : `${content}\n`
}

function parseDocumentIndex(value: unknown): DocumentIndex {
  const index = record(value)
  if (index?.version !== DOCUMENTS_VERSION || !Array.isArray(index.documents)) throw new Error('Documents index.json is invalid')
  const ids = new Set<string>()
  const documents = index.documents.map((raw): DocumentRecord => {
    const item = record(raw)
    if (typeof item?.id !== 'string' || item.id.trim() === '' || ids.has(item.id)) throw new Error('Documents index contains an invalid or duplicate id')
    if (typeof item.title !== 'string' || typeof item.description !== 'string' || (item.status !== 'active' && item.status !== 'archived')) throw new Error('Documents index contains an invalid record')
    if (typeof item.filename !== 'string' || basename(item.filename) !== item.filename || !/^[a-zA-Z0-9._-]+\.md$/u.test(item.filename)) throw new Error('Documents index contains an unsafe filename')
    if (typeof item.createdAt !== 'string' || typeof item.updatedAt !== 'string' || typeof item.lastAccessedAt !== 'string' || !Number.isSafeInteger(item.revision) || typeof item.contentHash !== 'string' || !Number.isSafeInteger(item.sizeBytes)) throw new Error('Documents index contains invalid metadata')
    if (!Array.isArray(item.sourcePaths) || !Array.isArray(item.sessionIds) || !Array.isArray(item.memoryBodyIds)) throw new Error('Documents index contains invalid lists')
    ids.add(item.id)
    return {
      id: item.id, title: item.title, description: item.description, status: item.status, filename: item.filename,
      relativePath: `documents/${item.status}/${item.filename}`,
      sourcePaths: item.sourcePaths.filter((entry): entry is string => typeof entry === 'string'),
      sessionIds: item.sessionIds.filter((entry): entry is string => typeof entry === 'string'),
      createdAt: item.createdAt, updatedAt: item.updatedAt, lastAccessedAt: item.lastAccessedAt,
      revision: Number(item.revision), contentHash: item.contentHash, sizeBytes: Number(item.sizeBytes),
      ...(typeof item.archivedAt === 'string' ? { archivedAt: item.archivedAt } : {}),
      ...(typeof item.archiveSummary === 'string' ? { archiveSummary: item.archiveSummary } : {}),
      memoryBodyIds: item.memoryBodyIds.filter((entry): entry is string => typeof entry === 'string'),
    }
  })
  return { version: 1, documents }
}

function parseRegistry(value: unknown): BodyRegistry {
  const registry = record(value)
  if (registry?.version !== 1 || !Array.isArray(registry.bodies)) throw new Error('Memory Space registry is invalid')
  const ids = new Set<string>()
  const bodies = registry.bodies.map((raw): StoredBody => {
    const body = record(raw)
    if (typeof body?.id !== 'string' || !BODY_ID.test(body.id) || ids.has(body.id)) throw new Error('Memory Space registry contains an invalid or duplicate id')
    if (typeof body.name !== 'string' || body.name.trim() === '' || body.name.length > 100 || typeof body.description !== 'string' || body.description.length > 1000) throw new Error('Memory Space registry contains invalid metadata')
    if (typeof body.createdAt !== 'string' || typeof body.updatedAt !== 'string') throw new Error('Memory Space registry contains invalid timestamps')
    ids.add(body.id)
    return { id: body.id, name: body.name, description: body.description, active: body.active === true, createdAt: body.createdAt, updatedAt: body.updatedAt }
  })
  return { version: 1, bodies }
}

function validDatabase(bytes: Uint8Array, path: string): void {
  if (bytes.length < 100 || !Buffer.from(bytes.subarray(0, SQLITE_HEADER.length)).equals(SQLITE_HEADER)) throw new Error(`${path} is not a SQLite mnemon.db`)
}

function validatePackPayload(files: Unzipped, components: MnemonPackComponent[], runtimeLimits: RuntimeMemoryLimits): void {
  if (components.includes('runtime')) parseRuntime(json(files['payload/runtime/memories.json'] ?? new Uint8Array(), 'payload/runtime/memories.json'), runtimeLimits)
  if (components.includes('documents')) {
    const index = parseDocumentIndex(json(files['payload/documents/index.json'] ?? new Uint8Array(), 'payload/documents/index.json'))
    for (const document of index.documents) {
      const path = `payload/${document.relativePath}`
      const bytes = files[path]
      if (bytes === undefined) throw new Error(`Documents payload is missing ${document.relativePath}`)
      if (bytes.length !== document.sizeBytes) throw new Error(`Documents payload size does not match ${document.relativePath}`)
      const markdown = utf8(bytes, path)
      const frontmatterEnd = markdown.startsWith('---\n') ? markdown.indexOf('\n---\n', 4) : -1
      if (frontmatterEnd < 0 || !markdown.slice(4, frontmatterEnd).split('\n').includes(`id: ${JSON.stringify(document.id)}`)) throw new Error(`Documents payload identity does not match ${document.relativePath}`)
      const body = markdown.slice(frontmatterEnd + 5).trim()
      if (sha256(body) !== document.contentHash) throw new Error(`Documents payload content hash does not match ${document.relativePath}`)
    }
  }
  if (components.includes('memory-spaces')) {
    const registry = parseRegistry(json(files['payload/data/.dsh-memory-bodies.json'] ?? new Uint8Array(), 'payload/data/.dsh-memory-bodies.json'))
    for (const body of registry.bodies) {
      const path = `payload/data/${body.id}/mnemon.db`
      const database = files[path]
      if (database === undefined) throw new Error(`Memory Space payload is missing ${body.id}/mnemon.db`)
      validDatabase(database, path)
    }
    const databases = Object.keys(files).filter(path => /^payload\/data\/[^/]+\/mnemon\.db$/u.test(path))
    if (databases.length !== registry.bodies.length) throw new Error('Memory Space registry does not match the database payload')
  }
}

function sleepSync(milliseconds: number): void {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, milliseconds)
}

function acquireLock(path: string): () => void {
  mkdirSync(dirname(path), { recursive: true, mode: 0o700 })
  const deadline = Date.now() + LOCK_TIMEOUT_MS
  let descriptor: number | undefined
  while (descriptor === undefined) {
    try {
      descriptor = openSync(path, 'wx', 0o600)
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
      try { if (Date.now() - statSync(path).mtimeMs > LOCK_STALE_MS) rmSync(path, { force: true }) } catch {}
      if (Date.now() >= deadline) throw new Error(`timed out waiting for Pack component lock: ${path}`)
      sleepSync(LOCK_RETRY_MS)
    }
  }
  const identity = fstatSync(descriptor)
  return () => {
    closeSync(descriptor!)
    try {
      const current = lstatSync(path)
      if (current.dev === identity.dev && current.ino === identity.ino) rmSync(path, { force: true })
    } catch {}
  }
}

function withLocks<T>(root: string, components: MnemonPackComponent[], operation: () => T): T {
  const paths = [join(root, '.dsh-pack.lock')]
  if (components.includes('runtime')) paths.push(join(root, 'runtime', '.memories.lock'))
  if (components.includes('documents')) paths.push(join(root, 'documents', '.index.lock'))
  const releases: Array<() => void> = []
  try {
    for (const path of paths) releases.push(acquireLock(path))
    return operation()
  } finally {
    for (const release of releases.reverse()) release()
  }
}

function assertRegularFile(path: string): void {
  const stat = lstatSync(path)
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`Pack source is not a regular file: ${path}`)
  if (stat.size > MAX_FILE_BYTES) throw new Error(`Pack source file exceeds the safety limit: ${path}`)
}

function sourceBytes(path: string): Uint8Array {
  assertRegularFile(path)
  return readFileSync(path)
}

function emptyRuntime(): RuntimeFile {
  return { version: 1, entries: [] }
}

function readCurrentRuntime(root: string, limits: RuntimeMemoryLimits = RUNTIME_MEMORY_LIMITS): RuntimeFile {
  const path = join(root, 'runtime', 'memories.json')
  return existsSync(path) ? parseRuntime(JSON.parse(readFileSync(path, 'utf8')) as unknown, limits) : emptyRuntime()
}

function writeRuntime(directory: string, file: RuntimeFile): void {
  mkdirSync(directory, { recursive: true, mode: 0o700 })
  writeFileSync(join(directory, 'memories.json'), `${JSON.stringify(file, null, 2)}\n`, { mode: 0o600 })
  writeFileSync(join(directory, 'USER.md'), runtimeProjection(file.entries, 'user'), { mode: 0o600 })
  writeFileSync(join(directory, 'MEMORY.md'), runtimeProjection(file.entries, 'memory'), { mode: 0o600 })
}

function readCurrentDocuments(root: string): { index: DocumentIndex; files: Map<string, Uint8Array> } {
  const indexPath = join(root, 'documents', 'index.json')
  const index = existsSync(indexPath) ? parseDocumentIndex(JSON.parse(readFileSync(indexPath, 'utf8')) as unknown) : { version: 1 as const, documents: [] }
  const files = new Map<string, Uint8Array>()
  for (const document of index.documents) {
    const path = join(root, document.relativePath)
    if (!existsSync(path)) throw new Error(`current Documents index is missing ${document.relativePath}`)
    files.set(document.id, sourceBytes(path))
  }
  return { index, files }
}

function writeDocuments(directory: string, index: DocumentIndex, files: Map<string, Uint8Array>): void {
  mkdirSync(join(directory, 'active'), { recursive: true, mode: 0o700 })
  mkdirSync(join(directory, 'archived'), { recursive: true, mode: 0o700 })
  for (const document of index.documents) {
    const bytes = files.get(document.id)
    if (bytes === undefined) throw new Error(`Documents staging is missing ${document.id}`)
    writeFileSync(join(directory, document.status, document.filename), bytes, { mode: 0o600 })
  }
  writeFileSync(join(directory, 'index.json'), `${JSON.stringify(index, null, 2)}\n`, { mode: 0o600 })
}

function archiveDocuments(pack: ParsedPack): { index: DocumentIndex; files: Map<string, Uint8Array> } {
  const index = parseDocumentIndex(json(pack.files['payload/documents/index.json']!, 'payload/documents/index.json'))
  return { index, files: new Map(index.documents.map(document => [document.id, pack.files[`payload/${document.relativePath}`]!])) }
}

function remapDocument(document: DocumentRecord, bytes: Uint8Array, id: string): { document: DocumentRecord; bytes: Uint8Array } {
  const base = document.filename.replace(/\.md$/u, '').slice(0, 80) || 'document'
  const filename = `${base}-${id.slice(0, 8)}.md`
  const markdown = utf8(bytes, document.filename).replace(/^id:\s*.*$/mu, `id: ${JSON.stringify(id)}`)
  const output = strToU8(markdown)
  return {
    document: { ...document, id, filename, relativePath: `documents/${document.status}/${filename}`, sizeBytes: output.length },
    bytes: output,
  }
}

function readCurrentRegistry(root: string): { registry: BodyRegistry; databases: Map<string, Uint8Array> } {
  const data = join(root, 'data')
  if (!existsSync(data)) return { registry: { version: 1, bodies: [] }, databases: new Map() }
  const discovered = readdirSync(data, { withFileTypes: true }).filter(entry => entry.isDirectory() && BODY_ID.test(entry.name) && existsSync(join(data, entry.name, 'mnemon.db'))).map(entry => entry.name)
  const registryPath = join(data, '.dsh-memory-bodies.json')
  const existing = existsSync(registryPath) ? parseRegistry(JSON.parse(readFileSync(registryPath, 'utf8')) as unknown) : { version: 1 as const, bodies: [] }
  const byId = new Map(existing.bodies.map(body => [body.id, body]))
  const timestamp = new Date().toISOString()
  const bodies = discovered.map(id => byId.get(id) ?? { id, name: id, description: 'Existing Mnemon Store discovered on disk.', active: false, createdAt: timestamp, updatedAt: timestamp })
  const databases = new Map<string, Uint8Array>()
  for (const body of bodies) {
    const path = join(data, body.id, 'mnemon.db')
    const wal = `${path}-wal`
    if (existsSync(wal) && statSync(wal).size > 0) throw new Error(`Memory Space ${body.id} is busy (mnemon.db-wal is not checkpointed); retry after writes settle`)
    const bytes = sourceBytes(path)
    validDatabase(bytes, path)
    databases.set(body.id, bytes)
  }
  return { registry: { version: 1, bodies }, databases }
}

function archiveRegistry(pack: ParsedPack): { registry: BodyRegistry; databases: Map<string, Uint8Array> } {
  const registry = parseRegistry(json(pack.files['payload/data/.dsh-memory-bodies.json']!, 'payload/data/.dsh-memory-bodies.json'))
  return { registry, databases: new Map(registry.bodies.map(body => [body.id, pack.files[`payload/data/${body.id}/mnemon.db`]!])) }
}

function writeRegistry(directory: string, registry: BodyRegistry, databases: Map<string, Uint8Array>): void {
  mkdirSync(directory, { recursive: true, mode: 0o700 })
  for (const body of registry.bodies) {
    const bytes = databases.get(body.id)
    if (bytes === undefined) throw new Error(`Memory Space staging is missing ${body.id}/mnemon.db`)
    const bodyDirectory = join(directory, body.id)
    mkdirSync(bodyDirectory, { recursive: true, mode: 0o700 })
    writeFileSync(join(bodyDirectory, 'mnemon.db'), bytes, { mode: 0o600 })
  }
  writeFileSync(join(directory, '.dsh-memory-bodies.json'), `${JSON.stringify(registry, null, 2)}\n`, { mode: 0o600 })
}

function componentItems(component: MnemonPackComponent, files: Record<string, Uint8Array>, runtimeLimits: RuntimeMemoryLimits = RUNTIME_MEMORY_LIMITS): number {
  if (component === 'runtime') return parseRuntime(json(files['payload/runtime/memories.json']!, 'payload/runtime/memories.json'), runtimeLimits).entries.length
  if (component === 'documents') return parseDocumentIndex(json(files['payload/documents/index.json']!, 'payload/documents/index.json')).documents.length
  return parseRegistry(json(files['payload/data/.dsh-memory-bodies.json']!, 'payload/data/.dsh-memory-bodies.json')).bodies.length
}

function summaryFor(components: MnemonPackComponent[], files: Record<string, Uint8Array>, runtimeLimits: RuntimeMemoryLimits = RUNTIME_MEMORY_LIMITS): MnemonPackComponentSummary[] {
  return components.map(component => {
    const prefix = `payload/${COMPONENT_DIRECTORIES[component]}/`
    const entries = Object.entries(files).filter(([path]) => path.startsWith(prefix))
    return { component, files: entries.length, bytes: entries.reduce((sum, [, value]) => sum + value.length, 0), items: componentItems(component, files, runtimeLimits) }
  })
}

function collectExport(root: string, components: MnemonPackComponent[], runtimeLimits: RuntimeMemoryLimits = RUNTIME_MEMORY_LIMITS): Record<string, Uint8Array> {
  const files: Record<string, Uint8Array> = {}
  if (components.includes('runtime')) {
    const runtime = readCurrentRuntime(root, runtimeLimits)
    files['payload/runtime/memories.json'] = strToU8(`${JSON.stringify(runtime, null, 2)}\n`)
    files['payload/runtime/USER.md'] = strToU8(runtimeProjection(runtime.entries, 'user'))
    files['payload/runtime/MEMORY.md'] = strToU8(runtimeProjection(runtime.entries, 'memory'))
  }
  if (components.includes('documents')) {
    const current = readCurrentDocuments(root)
    files['payload/documents/index.json'] = strToU8(`${JSON.stringify(current.index, null, 2)}\n`)
    for (const document of current.index.documents) files[`payload/${document.relativePath}`] = current.files.get(document.id)!
  }
  if (components.includes('memory-spaces')) {
    const current = readCurrentRegistry(root)
    files['payload/data/.dsh-memory-bodies.json'] = strToU8(`${JSON.stringify(current.registry, null, 2)}\n`)
    for (const body of current.registry.bodies) files[`payload/data/${body.id}/mnemon.db`] = current.databases.get(body.id)!
  }
  return files
}

function mergeRuntime(root: string, pack: ParsedPack, runtimeLimits: RuntimeMemoryLimits): RuntimeFile {
  const current = readCurrentRuntime(root, runtimeLimits)
  const incoming = parseRuntime(json(pack.files['payload/runtime/memories.json']!, 'payload/runtime/memories.json'), runtimeLimits)
  const keys = new Set(current.entries.map(entry => `${entry.target}\0${entry.content}`))
  const entries = [...current.entries]
  for (const entry of incoming.entries) {
    const key = `${entry.target}\0${entry.content}`
    if (!keys.has(key)) { keys.add(key); entries.push(entry) }
  }
  return parseRuntime({ version: 1, entries }, runtimeLimits)
}

function mergeDocuments(root: string, pack: ParsedPack): { index: DocumentIndex; files: Map<string, Uint8Array> } {
  const current = readCurrentDocuments(root)
  const incoming = archiveDocuments(pack)
  const ids = new Map(current.index.documents.map(document => [document.id, document]))
  for (const source of incoming.index.documents) {
    const existing = ids.get(source.id)
    if (existing !== undefined && existing.contentHash === source.contentHash) continue
    let document = source
    let bytes = incoming.files.get(source.id)!
    if (existing !== undefined) {
      const remapped = remapDocument(source, bytes, randomUUID())
      document = remapped.document
      bytes = remapped.bytes
    }
    current.index.documents.push(document)
    current.files.set(document.id, bytes)
    ids.set(document.id, document)
  }
  const activeBytes = current.index.documents.filter(document => document.status === 'active').reduce((sum, document) => sum + document.sizeBytes, 0)
  if (activeBytes > DOCUMENTS_ACTIVE_LIMIT_BYTES) throw new Error(`merged Documents exceed the ${DOCUMENTS_ACTIVE_LIMIT_BYTES} byte active limit`)
  return current
}

function mergeRegistry(root: string, pack: ParsedPack): { registry: BodyRegistry; databases: Map<string, Uint8Array> } {
  const current = readCurrentRegistry(root)
  const incoming = archiveRegistry(pack)
  const ids = new Set(current.registry.bodies.map(body => body.id))
  for (const source of incoming.registry.bodies) {
    let body = source
    const sourceDb = incoming.databases.get(source.id)!
    if (ids.has(source.id)) {
      const currentDb = current.databases.get(source.id)!
      if (sha256(currentDb) === sha256(sourceDb)) continue
      const id = randomUUID()
      body = { ...source, id, updatedAt: new Date().toISOString() }
    }
    ids.add(body.id)
    current.registry.bodies.push(body)
    current.databases.set(body.id, sourceDb)
  }
  return current
}

function persistedStore(root: string): string {
  try {
    const value = readFileSync(join(root, 'active'), 'utf8').trim()
    if (BODY_ID.test(value)) return value
  } catch {}
  return 'default'
}

function reconcilePersistedStore(root: string): void {
  const current = readCurrentRegistry(root).registry.bodies
  const ids = new Set(current.map(body => body.id))
  const selected = persistedStore(root)
  if (ids.has(selected)) return
  const replacement = ids.has('default')
    ? 'default'
    : current.filter(body => body.active).map(body => body.id).sort()[0] ?? [...ids].sort()[0] ?? 'default'
  const temporary = join(root, `.active-${process.pid}-${randomUUID()}.tmp`)
  try {
    writeFileSync(temporary, `${replacement}\n`, { mode: 0o600 })
    renameSync(temporary, join(root, 'active'))
  } finally {
    rmSync(temporary, { force: true })
  }
}

function stageImport(root: string, pack: ParsedPack, components: MnemonPackComponent[], mode: MnemonPackImportMode, runtimeLimits: RuntimeMemoryLimits): string {
  const staging = join(root, `.dsh-pack-stage-${randomUUID()}`)
  mkdirSync(staging, { recursive: true, mode: 0o700 })
  try {
    if (components.includes('runtime')) {
      const runtime = mode === 'merge' ? mergeRuntime(root, pack, runtimeLimits) : parseRuntime(json(pack.files['payload/runtime/memories.json']!, 'payload/runtime/memories.json'), runtimeLimits)
      writeRuntime(join(staging, 'runtime'), runtime)
    }
    if (components.includes('documents')) {
      const documents = mode === 'merge' ? mergeDocuments(root, pack) : archiveDocuments(pack)
      writeDocuments(join(staging, 'documents'), documents.index, documents.files)
    }
    if (components.includes('memory-spaces')) {
      const memory = mode === 'merge' ? mergeRegistry(root, pack) : archiveRegistry(pack)
      if (mode === 'replace' && readCurrentRegistry(root).registry.bodies.length > 0 && memory.registry.bodies.length === 0) {
        throw new Error('cannot replace the last Mnemon Store with an empty Memory Space set')
      }
      writeRegistry(join(staging, 'data'), memory.registry, memory.databases)
    }
    return staging
  } catch (error) {
    rmSync(staging, { recursive: true, force: true })
    throw error
  }
}

function commitStaging(root: string, staging: string, components: MnemonPackComponent[]): void {
  const backup = join(root, `.dsh-pack-backup-${randomUUID()}`)
  mkdirSync(backup, { recursive: true, mode: 0o700 })
  const committed: Array<{ directory: string; hadPrevious: boolean }> = []
  const replacementLocks = components.flatMap(component => component === 'runtime'
    ? [join(staging, 'runtime', '.memories.lock')]
    : component === 'documents' ? [join(staging, 'documents', '.index.lock')] : [])
  const activePath = join(root, 'active')
  const previousActive = existsSync(activePath) ? readFileSync(activePath) : undefined
  try {
    for (const lock of replacementLocks) writeFileSync(lock, 'pack-import\n', { mode: 0o600 })
    for (const component of components) {
      const directory = COMPONENT_DIRECTORIES[component]
      const target = join(root, directory)
      const previous = join(backup, directory)
      const hadPrevious = existsSync(target)
      if (hadPrevious) renameSync(target, previous)
      try {
        renameSync(join(staging, directory), target)
      } catch (error) {
        if (hadPrevious) renameSync(previous, target)
        throw error
      }
      committed.push({ directory, hadPrevious })
    }
    if (components.includes('memory-spaces')) reconcilePersistedStore(root)
    if (components.includes('runtime')) rmSync(join(root, 'runtime', '.memories.lock'), { force: true })
    if (components.includes('documents')) rmSync(join(root, 'documents', '.index.lock'), { force: true })
  } catch (error) {
    for (const entry of committed.reverse()) {
      const target = join(root, entry.directory)
      rmSync(target, { recursive: true, force: true })
      if (entry.hadPrevious) renameSync(join(backup, entry.directory), target)
    }
    if (components.includes('memory-spaces')) {
      if (previousActive === undefined) rmSync(activePath, { force: true })
      else writeFileSync(activePath, previousActive, { mode: 0o600 })
    }
    throw error
  } finally {
    rmSync(staging, { recursive: true, force: true })
    rmSync(backup, { recursive: true, force: true })
  }
}

function occupied(root: string, component: MnemonPackComponent): boolean {
  const directory = join(root, COMPONENT_DIRECTORIES[component])
  if (!existsSync(directory)) return false
  try { return readdirSync(directory).some(name => !name.startsWith('.')) } catch { return true }
}

function safeName(value: string | undefined): string | undefined {
  if (value === undefined) return undefined
  const name = basename(value.trim()).replace(/[^a-zA-Z0-9._-]+/gu, '-')
  return name === '' ? undefined : name.slice(0, 160)
}

/** Native, checksummed import/export for the one currently effective Mnemon root. */
export class MnemonPackManager {
  private readonly root: string
  private readonly runtimeLimits: RuntimeMemoryLimits

  constructor(
    private readonly runner: StorageRoot,
    private readonly config: Pick<ResolvedConfig, 'storageScope' | 'runtimeMemory'>,
    private readonly afterImport: (components: MnemonPackComponent[]) => void = () => {},
    private readonly now: () => Date = () => new Date(),
  ) {
    this.root = resolve(runner.effectiveDataDir())
    this.runtimeLimits = {
      memory: config.runtimeMemory.memoryLimitBytes,
      user: config.runtimeMemory.userLimitBytes,
    }
  }

  target(): MnemonPackTarget {
    return { root: this.root, scope: this.config.storageScope, defaultRoot: resolve(createStorageRoot({ storageScope: 'global' }).effectiveDataDir()) }
  }

  async exportPack(scope: MnemonPackScope): Promise<MnemonPackExport> {
    const components = componentsForScope(scope)
    return this.runner.withExclusive(async () => {
      await new Promise<void>(resolveReady => setImmediate(resolveReady))
      return withLocks(this.root, components, () => {
        const payload = collectExport(this.root, components, this.runtimeLimits)
        const exportedAt = this.now().toISOString()
        const summary = summaryFor(components, payload, this.runtimeLimits)
        const manifest: MnemonPackManifest = {
          format: MNEMON_PACK_FORMAT, version: MNEMON_PACK_VERSION, scope, exportedAt,
          source: { plugin: 'dsh-mnemon', pluginVersion: '0.1.0' }, components, summary,
        }
        const checksums: ChecksumFile = { algorithm: 'sha256', files: Object.fromEntries(Object.entries(payload).map(([path, bytes]) => [path, sha256(bytes)])) }
        const entries: Zippable = {
          'manifest.json': strToU8(`${JSON.stringify(manifest, null, 2)}\n`),
          'checksums.json': strToU8(`${JSON.stringify(checksums, null, 2)}\n`),
          ...payload,
        }
        // fflate writes ZIP timestamps using local date fields. Pin those fields
        // to keep the archive in range and byte-identical across timezones.
        const archive = zipSync(entries, { level: 6, mtime: new Date(1980, 0, 1) })
        if (archive.length > MNEMON_PACK_MAX_ARCHIVE_BYTES) throw new Error('exported Mnemon Pack exceeds the transport safety limit')
        const stamp = exportedAt.replace(/[:.]/gu, '-').replace('T', '_').replace('Z', '')
        return {
          fileName: `mnemon-backup-${stamp}.zip`, mimeType: MNEMON_PACK_MIME, bytes: archive.length,
          base64: Buffer.from(archive).toString('base64'), targetRoot: this.root, manifest,
        }
      })
    })
  }

  inspectPack(base64: string, fileName?: string): MnemonPackPreview {
    const pack = parseArchive(base64, this.runtimeLimits)
    const sanitizedName = safeName(fileName)
    return {
      ...(sanitizedName === undefined ? {} : { fileName: sanitizedName }),
      archiveBytes: pack.archiveBytes, expandedBytes: pack.expandedBytes, targetRoot: this.root, targetScope: this.config.storageScope,
      manifest: pack.manifest,
      occupied: Object.fromEntries(COMPONENT_ORDER.map(component => [component, occupied(this.root, component)])) as Record<MnemonPackComponent, boolean>,
    }
  }

  async importPack(base64: string, options: { mode: MnemonPackImportMode; components?: MnemonPackComponent[] }): Promise<MnemonPackImportResult> {
    const pack = parseArchive(base64, this.runtimeLimits)
    if (options.mode !== 'merge' && options.mode !== 'replace') throw new Error('Pack import mode must be merge or replace')
    if (options.components !== undefined && (new Set(options.components).size !== options.components.length || options.components.some(component => !COMPONENT_ORDER.includes(component)))) {
      throw new Error('requested import components are invalid')
    }
    const components = options.components === undefined ? pack.manifest.components : COMPONENT_ORDER.filter(component => options.components!.includes(component))
    if (components.length === 0 || components.some(component => !pack.manifest.components.includes(component))) throw new Error('requested import components are not present in this Pack')
    return this.runner.withExclusive(async () => {
      await new Promise<void>(resolveReady => setImmediate(resolveReady))
      mkdirSync(this.root, { recursive: true, mode: 0o700 })
      return withLocks(this.root, components, () => {
        const staging = stageImport(this.root, pack, components, options.mode, this.runtimeLimits)
        commitStaging(this.root, staging, components)
        this.afterImport(components)
        return {
          imported: true, mode: options.mode, targetRoot: this.root, components,
          summary: pack.manifest.summary.filter(summary => components.includes(summary.component)),
        }
      })
    })
  }
}
