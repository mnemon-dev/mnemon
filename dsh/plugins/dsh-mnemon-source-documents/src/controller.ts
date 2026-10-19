import { createHash } from 'node:crypto'
import {
  closeSync,
  existsSync,
  mkdirSync,
  openSync,
  readFileSync,
  renameSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs'
import type { BigIntStats } from 'node:fs'
import { basename, isAbsolute, join, relative, resolve, sep } from 'node:path'
import type { DocumentCapacityPlan, DocumentMutation, DocumentMutationResult, DocumentRecord, DocumentSearchResult, DocumentSnapshot, DocumentStatus, DocumentView } from './contracts.ts'
import { lexicalRequiredMatchCount, lexicalSearchTokens } from './search-tokens.ts'
import { DOCUMENTS_VERSION, DOCUMENTS_ACTIVE_LIMIT_BYTES } from './contracts.ts'

const MAX_DOCUMENT_BYTES = 2 * 1024 * 1024
const LOCK_TIMEOUT_MS = 5_000
const LOCK_STALE_MS = 30_000
const LOCK_RETRY_MS = 20
const MAX_EXCERPT_CACHE_ENTRIES = 2_048

interface DocumentIndex {
  version: typeof DOCUMENTS_VERSION
  documents: DocumentRecord[]
}

export class DocumentCapacityError extends Error {
  readonly code = 'document-capacity' as const
  constructor(
    readonly projected: number,
    readonly limit: number,
    readonly candidates: DocumentRecord[],
  ) {
    super(`Would exceed active document capacity: ${projected} bytes (limit ${limit}). Archive the least-recently-used active document before retrying.`)
    this.name = 'DocumentCapacityError'
  }
}

export class DocumentConflictError extends Error {
  readonly code = 'revision-conflict' as const
  constructor() {
    super('document changed while archival was running; the active copy was preserved')
    this.name = 'DocumentConflictError'
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function normalizeLine(value: string | undefined, field: string, maximum: number, required: boolean): string {
  const normalized = value?.trim().replace(/\s+/gu, ' ') ?? ''
  if (required && normalized === '') throw new Error(`${field} is required`)
  if (normalized.length > maximum) throw new Error(`${field} is too long (max ${maximum} characters)`)
  return normalized
}

function normalizeContent(value: string | undefined, required: boolean): string | undefined {
  if (value === undefined && !required) return undefined
  const normalized = value?.replace(/\0/gu, '').trim() ?? ''
  if (normalized === '') throw new Error('document content is required')
  const size = Buffer.byteLength(normalized, 'utf8')
  if (size > MAX_DOCUMENT_BYTES) throw new Error(`document content is too large (${size} bytes; max ${MAX_DOCUMENT_BYTES})`)
  return normalized
}

function unique(values: readonly string[], maximum: number): string[] {
  return [...new Set(values.map(value => value.trim()).filter(Boolean))].slice(0, maximum)
}

function hash(value: string): string {
  return createHash('sha256').update(value).digest('hex')
}

function indexRevision(index: DocumentIndex): string {
  return hash(JSON.stringify(index))
}

function copyIndex(index: DocumentIndex): DocumentIndex {
  return { version: index.version, documents: index.documents.map(record => ({
    ...record,
    sourcePaths: [...record.sourcePaths],
    sessionIds: [...record.sessionIds],
    memoryBodyIds: [...record.memoryBodyIds],
  })) }
}

function slug(title: string): string {
  const result = title.normalize('NFKD').toLowerCase().replace(/[^a-z0-9]+/gu, '-').replace(/^-|-$/gu, '').slice(0, 48)
  return result || 'document'
}

function yamlString(value: string): string {
  return JSON.stringify(value)
}

function renderDocument(record: DocumentRecord, content: string): string {
  const sources = record.sourcePaths.length === 0 ? '  []' : record.sourcePaths.map(path => `  - ${yamlString(path)}`).join('\n')
  const sessions = record.sessionIds.length === 0 ? '  []' : record.sessionIds.map(id => `  - ${yamlString(id)}`).join('\n')
  const bodies = record.memoryBodyIds.length === 0 ? '  []' : record.memoryBodyIds.map(id => `  - ${yamlString(id)}`).join('\n')
  return `---
id: ${yamlString(record.id)}
title: ${yamlString(record.title)}
description: ${yamlString(record.description)}
status: ${yamlString(record.status)}
created_at: ${yamlString(record.createdAt)}
updated_at: ${yamlString(record.updatedAt)}
content_hash: ${yamlString(record.contentHash)}
source_paths:
${sources}
session_ids:
${sessions}
memory_body_ids:
${bodies}
---

${content.trim()}\n`
}

function documentBody(markdown: string): string {
  if (!markdown.startsWith('---\n')) return markdown.trim()
  const end = markdown.indexOf('\n---\n', 4)
  return end < 0 ? markdown.trim() : markdown.slice(end + 5).trim()
}

function excerpt(content: string, maximum = 220): string {
  const normalized = content.replace(/[#>*_`\[\]]/gu, '').replace(/\s+/gu, ' ').trim()
  return normalized.length <= maximum ? normalized : `${normalized.slice(0, maximum - 1)}…`
}

/** Include ctime and identity so restored mtimes and atomic replacements miss. */
function diskIdentity(stat: BigIntStats): string {
  return `${stat.dev}:${stat.ino}:${stat.size}:${stat.mtimeNs}:${stat.ctimeNs}:${stat.mode}`
}

function sleepSync(milliseconds: number): void {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, milliseconds)
}

function parseRecord(value: unknown): DocumentRecord | undefined {
  if (!isRecord(value) || typeof value.id !== 'string' || typeof value.title !== 'string' || typeof value.description !== 'string') return undefined
  if ((value.status !== 'active' && value.status !== 'archived') || typeof value.filename !== 'string' || typeof value.relativePath !== 'string') return undefined
  if (typeof value.createdAt !== 'string' || typeof value.updatedAt !== 'string' || typeof value.lastAccessedAt !== 'string') return undefined
  if (typeof value.revision !== 'number' || typeof value.contentHash !== 'string' || typeof value.sizeBytes !== 'number') return undefined
  if (!Array.isArray(value.sourcePaths) || !Array.isArray(value.sessionIds) || !Array.isArray(value.memoryBodyIds)) return undefined
  return {
    id: value.id,
    title: value.title,
    description: value.description,
    status: value.status,
    filename: value.filename,
    relativePath: value.relativePath,
    sourcePaths: value.sourcePaths.filter((entry): entry is string => typeof entry === 'string'),
    sessionIds: value.sessionIds.filter((entry): entry is string => typeof entry === 'string'),
    createdAt: value.createdAt,
    updatedAt: value.updatedAt,
    lastAccessedAt: value.lastAccessedAt,
    revision: value.revision,
    contentHash: value.contentHash,
    sizeBytes: value.sizeBytes,
    ...(typeof value.archivedAt === 'string' ? { archivedAt: value.archivedAt } : {}),
    ...(typeof value.archiveSummary === 'string' ? { archiveSummary: value.archiveSummary } : {}),
    memoryBodyIds: value.memoryBodyIds.filter((entry): entry is string => typeof entry === 'string'),
  }
}

/** Project-scoped control plane for managed active and cold document copies. */
export class DocumentController {
  readonly workspaceRoot: string
  readonly storageRoot: string
  readonly directory: string
  readonly activeDirectory: string
  readonly archivedDirectory: string
  readonly indexPath: string
  readonly lockPath: string
  private readonly managedRelativePrefix: string
  private queue: Promise<unknown> = Promise.resolve()
  private readonly excerpts = new Map<string, { identity: string; value: string }>()
  private indexCache: { identity: string; index: DocumentIndex; revision: string } | undefined
  private readonly indexRevisions = new WeakMap<DocumentIndex, string>()

  constructor(
    workspaceRoot: string,
    readonly limitBytes = DOCUMENTS_ACTIVE_LIMIT_BYTES,
    private readonly now: () => Date = () => new Date(),
    storageRoot?: string,
  ) {
    this.workspaceRoot = resolve(workspaceRoot)
    if (!existsSync(this.workspaceRoot) || !statSync(this.workspaceRoot).isDirectory()) throw new Error(`document workspace is unavailable: ${this.workspaceRoot}`)
    if (!Number.isSafeInteger(limitBytes) || limitBytes < 1) throw new Error('active document limit must be a positive integer')
    this.storageRoot = storageRoot === undefined ? join(this.workspaceRoot, '.mnemon') : resolve(storageRoot)
    this.managedRelativePrefix = storageRoot === undefined ? ['.mnemon', 'documents'].join('/') : 'documents'
    this.directory = join(this.storageRoot, 'documents')
    this.activeDirectory = join(this.directory, 'active')
    this.archivedDirectory = join(this.directory, 'archived')
    this.indexPath = join(this.directory, 'index.json')
    this.lockPath = join(this.directory, '.index.lock')
    this.initialize()
  }

  snapshot(): DocumentSnapshot {
    return this.withLock(() => this.snapshotUnlocked(this.readIndex()))
  }

  /** Source facts and read-grant membership need metadata, not every document body. */
  catalog(): DocumentSnapshot {
    return this.withLock(() => this.snapshotUnlocked(this.readIndex(), false))
  }

  revision(): string {
    return this.withLock(() => this.revisionOf(this.readIndex()))
  }

  get(id: string): DocumentView {
    return this.withLock(() => this.view(this.requireDocument(this.readIndex(), id)))
  }

  capacityPlan(request: DocumentMutation): DocumentCapacityPlan {
    return this.withLock(() => {
      const index = this.readIndex()
      const active = index.documents.filter(record => record.status === 'active')
      const used = active.reduce((sum, record) => sum + record.sizeBytes, 0)
      let projected: number
      let excludeId: string | undefined
      if (request.action === 'create') {
        const now = this.now().toISOString()
        const title = normalizeLine(request.title, 'document title', 160, true)
        const content = normalizeContent(request.content, true)!
        const id = crypto.randomUUID()
        const record: DocumentRecord = {
          id, title,
          description: normalizeLine(request.description, 'document description', 600, false),
          status: 'active', filename: `${slug(title)}-${id.slice(0, 8)}.md`, relativePath: '',
          sourcePaths: this.normalizeSourcePaths(request.sourcePaths ?? []), sessionIds: unique(request.sessionIds ?? [], 20),
          createdAt: now, updatedAt: now, lastAccessedAt: now, revision: 1,
          contentHash: hash(content), sizeBytes: 0, memoryBodyIds: [],
        }
        projected = used + Buffer.byteLength(renderDocument(record, content), 'utf8')
      } else {
        const current = this.requireDocument(index, request.id)
        if (current.status !== 'active') throw new Error('archived documents are immutable; create a new active revision instead')
        const content = normalizeContent(request.content, false) ?? this.readBody(current)
        const updated: DocumentRecord = {
          ...current,
          title: request.title === undefined ? current.title : normalizeLine(request.title, 'document title', 160, true),
          description: request.description === undefined ? current.description : normalizeLine(request.description, 'document description', 600, false),
          sourcePaths: request.sourcePaths === undefined ? current.sourcePaths : this.normalizeSourcePaths(request.sourcePaths),
          sessionIds: request.sessionIds === undefined ? current.sessionIds : unique([...current.sessionIds, ...request.sessionIds], 20),
          contentHash: hash(content), revision: current.revision + 1,
        }
        projected = used - current.sizeBytes + Buffer.byteLength(renderDocument(updated, content), 'utf8')
        excludeId = current.id
      }
      const candidates = active
        .filter(record => record.id !== excludeId)
        .sort((left, right) => Date.parse(left.lastAccessedAt) - Date.parse(right.lastAccessedAt) || Date.parse(left.updatedAt) - Date.parse(right.updatedAt))
      return { projected, limit: this.limitBytes, fits: projected <= this.limitBytes, candidates }
    })
  }

  search(query: string, options: { includeArchived?: boolean; limit?: number; allowedIds?: readonly string[] } = {}): Promise<DocumentSearchResult> {
    const operation = this.queue.then(() => this.withLock(() => {
      const index = this.readIndex()
      const normalized = query.trim().normalize('NFKC').toLocaleLowerCase()
      const tokens = lexicalSearchTokens(normalized)
      const requiredTokenMatches = lexicalRequiredMatchCount(tokens)
      const includeArchived = options.includeArchived === true
      const limit = Math.max(1, Math.min(50, Math.trunc(options.limit ?? 20)))
      const allowedIds = options.allowedIds === undefined ? undefined : new Set(options.allowedIds)
      const ranked = index.documents
        .filter(record => allowedIds === undefined || allowedIds.has(record.id))
        .filter(record => includeArchived || record.status === 'active')
        .map(record => {
          const view = this.view(record)
          const title = view.title.normalize('NFKC').toLocaleLowerCase()
          const description = view.description.normalize('NFKC').toLocaleLowerCase()
          const content = view.content.normalize('NFKC').toLocaleLowerCase()
          let score = normalized === '' ? 1 : title.includes(normalized) ? 12 : description.includes(normalized) ? 7 : content.includes(normalized) ? 4 : 0
          let tokenMatches = 0
          for (const token of tokens) {
            const titleMatch = title.includes(token)
            const descriptionMatch = description.includes(token)
            const contentMatch = content.includes(token)
            if (titleMatch || descriptionMatch || contentMatch) tokenMatches += 1
            score += titleMatch ? 4 : descriptionMatch ? 2 : contentMatch ? 1 : 0
          }
          return { result: { ...view, score }, tokenMatches }
        })
        .filter(candidate => normalized === '' || (candidate.result.score > 0 && candidate.tokenMatches >= requiredTokenMatches))
        .sort((left, right) => right.result.score - left.result.score || Date.parse(right.result.updatedAt) - Date.parse(left.result.updatedAt))
        .slice(0, limit)
        .map(({ result }) => ({ ...result, excerpt: excerpt(result.content) }))
      if (ranked.length > 0) {
        const accessedAt = this.now().toISOString()
        const ids = new Set(ranked.map(result => result.id))
        index.documents = index.documents.map(record => ids.has(record.id) ? { ...record, lastAccessedAt: accessedAt } : record)
        this.persistIndex(index)
      }
      const result = { query: query.trim(), includeArchived, total: ranked.length, generatedAt: this.now().toISOString(), results: ranked }
      return result
    }))
    this.queue = operation.catch(() => undefined)
    return operation
  }

  mutate(request: DocumentMutation, expectedRevision?: string): Promise<DocumentMutationResult> {
    const operation = this.queue.then(() => this.withLock(() => this.mutateLocked(request, expectedRevision)))
    this.queue = operation.catch(() => undefined)
    return operation
  }

  archive(id: string, expectedRevision: number, details: { summary: string; memoryBodyIds: string[] }): Promise<DocumentMutationResult> {
    const operation: Promise<DocumentMutationResult> = this.queue.then(() => this.withLock((): DocumentMutationResult => {
      const index = this.readIndex()
      const current = this.requireDocument(index, id)
      if (current.status !== 'active') throw new Error('only active documents can be archived')
      if (current.revision !== expectedRevision) throw new DocumentConflictError()
      const source = this.pathFor(current)
      const now = this.now().toISOString()
      const updated: DocumentRecord = {
        ...current,
        status: 'archived',
        relativePath: this.relativeManagedPath('archived', current.filename),
        updatedAt: now,
        lastAccessedAt: now,
        revision: current.revision + 1,
        archivedAt: now,
        archiveSummary: normalizeLine(details.summary, 'archive summary', 1000, true),
        memoryBodyIds: unique(details.memoryBodyIds, 20),
      }
      const content = this.readBody(current)
      const rendered = renderDocument(updated, content)
      updated.sizeBytes = Buffer.byteLength(rendered, 'utf8')
      const destination = this.pathFor(updated)
      renameSync(source, destination)
      try {
        writeFileSync(destination, rendered, 'utf8')
        index.documents = index.documents.map(record => record.id === id ? updated : record)
        this.persistIndex(index)
      } catch (error) {
        if (existsSync(destination)) renameSync(destination, source)
        throw error
      }
      return { success: true, action: 'archived', document: { ...updated, content }, snapshot: this.snapshotUnlocked(index) }
    }))
    this.queue = operation.catch(() => undefined)
    return operation
  }

  private mutateLocked(request: DocumentMutation, expectedRevision?: string): DocumentMutationResult {
    const index = this.readIndex()
    if (expectedRevision !== undefined && this.revisionOf(index) !== expectedRevision) throw new DocumentConflictError()
    const now = this.now().toISOString()
    if (request.action === 'create') {
      const title = normalizeLine(request.title, 'document title', 160, true)
      const description = normalizeLine(request.description, 'document description', 600, false)
      const content = normalizeContent(request.content, true)!
      const id = crypto.randomUUID()
      const filename = `${slug(title)}-${id.slice(0, 8)}.md`
      const record: DocumentRecord = {
        id, title, description, status: 'active', filename,
        relativePath: this.relativeManagedPath('active', filename),
        sourcePaths: this.normalizeSourcePaths(request.sourcePaths ?? []),
        sessionIds: unique(request.sessionIds ?? [], 20),
        createdAt: now, updatedAt: now, lastAccessedAt: now, revision: 1,
        contentHash: hash(content), sizeBytes: 0, memoryBodyIds: [],
      }
      const rendered = renderDocument(record, content)
      record.sizeBytes = Buffer.byteLength(rendered, 'utf8')
      this.assertCapacity(index, record.sizeBytes)
      this.persistDocument(record, content)
      index.documents.push(record)
      this.persistIndex(index)
      return { success: true, action: 'created', document: { ...record, content }, snapshot: this.snapshotUnlocked(index) }
    }

    const current = this.requireDocument(index, request.id)
    if (current.status !== 'active') throw new Error('archived documents are immutable; create a new active revision instead')
    const content = normalizeContent(request.content, false) ?? this.readBody(current)
    const updated: DocumentRecord = {
      ...current,
      title: request.title === undefined ? current.title : normalizeLine(request.title, 'document title', 160, true),
      description: request.description === undefined ? current.description : normalizeLine(request.description, 'document description', 600, false),
      sourcePaths: request.sourcePaths === undefined ? current.sourcePaths : this.normalizeSourcePaths(request.sourcePaths),
      sessionIds: request.sessionIds === undefined ? current.sessionIds : unique([...current.sessionIds, ...request.sessionIds], 20),
      updatedAt: now,
      lastAccessedAt: now,
      revision: current.revision + 1,
      contentHash: hash(content),
    }
    const rendered = renderDocument(updated, content)
    updated.sizeBytes = Buffer.byteLength(rendered, 'utf8')
    this.assertCapacity(index, updated.sizeBytes - current.sizeBytes, current.id)
    this.persistDocument(updated, content)
    index.documents = index.documents.map(record => record.id === current.id ? updated : record)
    this.persistIndex(index)
    return { success: true, action: 'updated', document: { ...updated, content }, snapshot: this.snapshotUnlocked(index) }
  }

  private initialize(): void {
    mkdirSync(this.activeDirectory, { recursive: true })
    mkdirSync(this.archivedDirectory, { recursive: true })
    if (!existsSync(this.indexPath)) this.atomicWrite(this.indexPath, `${JSON.stringify({ version: DOCUMENTS_VERSION, documents: [] }, null, 2)}\n`)
    this.readIndex()
  }

  private readIndex(): DocumentIndex {
    const identity = diskIdentity(statSync(this.indexPath, { bigint: true }))
    if (this.indexCache?.identity === identity) {
      const index = copyIndex(this.indexCache.index)
      this.indexRevisions.set(index, this.indexCache.revision)
      return index
    }
    this.indexCache = undefined
    const raw = JSON.parse(readFileSync(this.indexPath, 'utf8')) as unknown
    if (!isRecord(raw) || raw.version !== DOCUMENTS_VERSION || !Array.isArray(raw.documents)) throw new Error(`invalid document index: ${this.indexPath}`)
    const documents = raw.documents.map(parseRecord)
    if (documents.some(record => record === undefined)) throw new Error(`invalid document record in ${this.indexPath}`)
    const index: DocumentIndex = { version: DOCUMENTS_VERSION, documents: documents as DocumentRecord[] }
    const revision = indexRevision(index)
    this.indexRevisions.set(index, revision)
    try {
      if (diskIdentity(statSync(this.indexPath, { bigint: true })) === identity) {
        // Never expose cached mutable records/arrays through controller results.
        this.indexCache = { identity, index: copyIndex(index), revision }
      }
    } catch { /* Do not cache an index concurrently removed by an external writer. */ }
    return index
  }

  private revisionOf(index: DocumentIndex): string {
    return this.indexRevisions.get(index) ?? indexRevision(index)
  }

  private snapshotUnlocked(index: DocumentIndex, includeExcerpts = true): DocumentSnapshot {
    // Fix admission before scanning: FIFO insertion on every overflow miss
    // would evict the next body and make every subsequent full scan miss.
    const cachedPaths = includeExcerpts ? index.documents.slice(0, MAX_EXCERPT_CACHE_ENTRIES).map(record => this.pathFor(record)) : undefined
    const eligiblePaths = cachedPaths === undefined ? undefined : new Set(cachedPaths)
    if (eligiblePaths !== undefined) {
      for (const path of this.excerpts.keys()) if (!eligiblePaths.has(path)) this.excerpts.delete(path)
    }
    const documents = index.documents.map((record, position) => {
      const path = cachedPaths?.[position] ?? this.pathFor(record)
      let stat: BigIntStats
      try { stat = statSync(path, { bigint: true }) }
      catch {
        this.excerpts.delete(path)
        return { ...record, healthy: false, excerpt: '' }
      }
      return { ...record, healthy: true, excerpt: includeExcerpts ? this.bodyExcerpt(record, path, stat, eligiblePaths!.has(path)) : '' }
    })
    const active = documents.filter(record => record.status === 'active')
    return {
      workspaceRoot: this.workspaceRoot,
      directory: this.directory,
      indexPath: this.indexPath,
      generatedAt: this.now().toISOString(),
      revision: this.revisionOf(index),
      limitBytes: this.limitBytes,
      activeBytes: active.reduce((sum, record) => sum + record.sizeBytes, 0),
      activeCount: active.length,
      archivedCount: documents.length - active.length,
      total: documents.length,
      documents,
    }
  }

  /** Cache only presentation excerpts; reads/search and each health check stay live. */
  private bodyExcerpt(record: DocumentRecord, path: string, stat: BigIntStats, cacheable: boolean): string {
    if (!cacheable) return excerpt(this.readBody(record))
    const identity = diskIdentity(stat)
    const cached = this.excerpts.get(path)
    if (cached?.identity === identity) return cached.value
    this.excerpts.delete(path)
    const value = excerpt(this.readBody(record))
    // An out-of-band writer may replace the body between stat and read. Return
    // the observed body, but never stamp it with an unrelated file's identity.
    try {
      if (diskIdentity(statSync(path, { bigint: true })) === identity) {
        this.excerpts.set(path, { identity, value })
      }
    } catch { /* The next snapshot will observe a removed/inaccessible body. */ }
    return value
  }

  private requireDocument(index: DocumentIndex, rawId: string): DocumentRecord {
    const id = rawId.trim()
    const record = index.documents.find(document => document.id === id)
    if (record === undefined) throw new Error(`document not found: ${id}`)
    return record
  }

  private assertCapacity(index: DocumentIndex, delta: number, excludeId?: string): void {
    const active = index.documents.filter(record => record.status === 'active')
    const used = active.reduce((sum, record) => sum + record.sizeBytes, 0)
    const projected = used + delta
    if (projected <= this.limitBytes) return
    const candidates = active
      .filter(record => record.id !== excludeId)
      .sort((left, right) => Date.parse(left.lastAccessedAt) - Date.parse(right.lastAccessedAt) || Date.parse(left.updatedAt) - Date.parse(right.updatedAt))
    throw new DocumentCapacityError(projected, this.limitBytes, candidates)
  }

  private normalizeSourcePaths(paths: readonly string[]): string[] {
    return unique(paths, 50).map(value => {
      const absolute = resolve(this.workspaceRoot, value)
      const workspaceRelative = relative(this.workspaceRoot, absolute)
      if (workspaceRelative === '..' || workspaceRelative.startsWith(`..${sep}`) || isAbsolute(workspaceRelative)) throw new Error(`source path must stay inside the workspace: ${value}`)
      if (absolute === this.directory || absolute.startsWith(`${this.directory}${sep}`)) throw new Error('managed document paths cannot be used as source paths')
      return workspaceRelative.split(sep).join('/') || '.'
    })
  }

  private relativeManagedPath(status: DocumentStatus, filename: string): string {
    return [this.managedRelativePrefix, status, basename(filename)].join('/')
  }

  private pathFor(record: Pick<DocumentRecord, 'relativePath'>): string {
    const legacyPrefix = ['.mnemon', 'documents'].join('/')
    const relativePath = record.relativePath === legacyPrefix || record.relativePath.startsWith(`${legacyPrefix}/`)
      ? record.relativePath.slice('.mnemon/'.length)
      : record.relativePath
    const path = resolve(this.storageRoot, relativePath)
    const managedRoot = `${resolve(this.directory)}${sep}`
    if (!path.startsWith(managedRoot)) throw new Error('document index contains an unsafe managed path')
    return path
  }

  private readBody(record: DocumentRecord): string {
    return documentBody(readFileSync(this.pathFor(record), 'utf8'))
  }

  private view(record: DocumentRecord): DocumentView {
    return { ...record, content: this.readBody(record) }
  }

  private persistDocument(record: DocumentRecord, content: string): void {
    this.atomicWrite(this.pathFor(record), renderDocument(record, content))
  }

  private persistIndex(index: DocumentIndex): void {
    // This working copy has changed. A failed write must not publish its data
    // or revision through the cache; successful writes are observed from disk.
    this.indexCache = undefined
    this.indexRevisions.delete(index)
    this.atomicWrite(this.indexPath, `${JSON.stringify(index, null, 2)}\n`)
  }

  private atomicWrite(path: string, content: string): void {
    const temporary = `${path}.${process.pid}.${crypto.randomUUID()}.tmp`
    writeFileSync(temporary, content, { encoding: 'utf8', mode: 0o600 })
    renameSync(temporary, path)
  }

  private withLock<T>(callback: () => T): T {
    const deadline = Date.now() + LOCK_TIMEOUT_MS
    let descriptor: number | undefined
    while (descriptor === undefined) {
      try {
        descriptor = openSync(this.lockPath, 'wx', 0o600)
      } catch (error) {
        const code = (error as NodeJS.ErrnoException).code
        if (code !== 'EEXIST') throw error
        try {
          if (Date.now() - statSync(this.lockPath).mtimeMs > LOCK_STALE_MS) rmSync(this.lockPath, { force: true })
        } catch {}
        if (Date.now() >= deadline) throw new Error(`timed out waiting for document lock: ${this.lockPath}`)
        sleepSync(LOCK_RETRY_MS)
      }
    }
    try {
      return callback()
    } finally {
      closeSync(descriptor)
      rmSync(this.lockPath, { force: true })
    }
  }
}

/** Resolves one cached controller per canonical DSH workspace. */
export class DocumentManager {
  private readonly controllers = new Map<string, DocumentController>()

  constructor(
    private readonly limitBytes = DOCUMENTS_ACTIVE_LIMIT_BYTES,
    private readonly now: () => Date = () => new Date(),
    private readonly storageRoot?: () => string,
  ) {}

  forWorkspace(workspaceRoot: string): DocumentController {
    const root = resolve(workspaceRoot)
    const storageRoot = this.storageRoot?.()
    const key = storageRoot === undefined ? root : `${resolve(storageRoot)}\0${root}`
    let controller = this.controllers.get(key)
    if (controller === undefined) {
      controller = new DocumentController(root, this.limitBytes, this.now, storageRoot)
      this.controllers.set(key, controller)
    }
    return controller
  }
}
