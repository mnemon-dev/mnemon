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
import { createHash } from 'node:crypto'
import { basename, join } from 'node:path'
import {
  DEFAULT_RUNTIME_MEMORY_LIMIT_BYTES,
  DEFAULT_RUNTIME_USER_LIMIT_BYTES,
  MAX_RUNTIME_MEMORY_LIMIT_BYTES,
} from './defaults.ts'
import type { RuntimeMemoryStorage } from './config.ts'
import type {
  RuntimeMemoryAction,
  RuntimeMemoryCompactedEntry,
  RuntimeMemoryEntry,
  RuntimeMemoryImportance,
  RuntimeMemoryMaintenancePlan,
  RuntimeMemoryMutation,
  RuntimeMemoryMutationResult,
  RuntimeMemorySnapshot,
  RuntimeMemoryTarget,
  RuntimeMemoryTargetView,
  RuntimeMemoryUsage,
} from './contracts.ts'
import { RUNTIME_MEMORY_VERSION, RUNTIME_ENTRY_DELIMITER, RUNTIME_MEMORY_LIMITS, type RuntimeMemoryLimits } from './contracts.ts'

export interface RuntimeMemoryContextProjection {
  revision: string
  text: string
  /** Exact entries represented by this branch-aware prompt projection. */
  entries: RuntimeMemoryEntry[]
  /** Complete stored entry count before branch filtering. */
  totalEntries: number
}

const LOCK_TIMEOUT_MS = 5_000
const LOCK_STALE_MS = 30_000
const LOCK_RETRY_MS = 20
const MAX_ENTRY_BYTES = 8 * 1024

interface RuntimeMemoryFile {
  version: typeof RUNTIME_MEMORY_VERSION
  entries: RuntimeMemoryEntry[]
}

type RuntimeMemoryResultFields = Pick<RuntimeMemoryMutationResult, 'message' | 'added' | 'replaced' | 'removed'>

interface PreparedRuntimeMemoryMutation {
  changed: boolean
  projectedEntries: RuntimeMemoryEntry[]
  compactableEntries: RuntimeMemoryEntry[]
  pendingEntry?: RuntimeMemoryEntry
  excludedEntry?: RuntimeMemoryEntry
  fields: RuntimeMemoryResultFields
}

export class RuntimeMemoryCapacityError extends Error {
  readonly code = 'runtime-capacity' as const
  constructor(
    readonly target: RuntimeMemoryTarget,
    readonly used: number,
    readonly projected: number,
    readonly limit: number,
  ) {
    super(`Would exceed ${target} runtime memory capacity: ${projected} bytes (current ${used}, limit ${limit}). Archive and compact runtime memory before retrying.`)
    this.name = 'RuntimeMemoryCapacityError'
  }
}

export class RuntimeMemoryConflictError extends Error {
  readonly code = 'revision-conflict' as const
  constructor() {
    super('runtime memory changed while archival was running; no compacted data was applied')
    this.name = 'RuntimeMemoryConflictError'
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isTarget(value: unknown): value is RuntimeMemoryTarget {
  return value === 'memory' || value === 'user'
}

function isImportance(value: unknown): value is RuntimeMemoryImportance {
  return value === 'critical' || value === 'normal' || value === 'low'
}

export const RUNTIME_BRANCH_NAME_MAX = 128
export const RUNTIME_BRANCHES_PER_ENTRY_MAX = 16
const RUNTIME_BRANCH_NAME_RE = /^[A-Za-z0-9._/-]+$/u

/** Validate a model-supplied branch scope. Returns the normalized list; an empty list means "no scope". */
export function normalizeRuntimeBranches(value: unknown, field = 'branches'): string[] {
  if (!Array.isArray(value)) throw new Error(`${field} must be an array of git branch names`)
  const branches: string[] = []
  const seen = new Set<string>()
  for (const item of value) {
    if (typeof item !== 'string') throw new Error(`${field} must contain only git branch names`)
    const branch = item.trim()
    if (branch === ''
      || branch.length > RUNTIME_BRANCH_NAME_MAX
      || !RUNTIME_BRANCH_NAME_RE.test(branch)
      || branch === '-'
      || branch.startsWith('/') || branch.startsWith('-')
      || branch.endsWith('/') || branch.endsWith('.')
      || branch.includes('..') || branch.includes('//') || branch.includes('@{')) {
      throw new Error(`${field} must contain git branch names (letters, numbers, dot, underscore, slash, dash)`)
    }
    if (seen.has(branch)) throw new Error(`${field} must not repeat a branch name`)
    seen.add(branch)
    branches.push(branch)
  }
  if (branches.length > RUNTIME_BRANCHES_PER_ENTRY_MAX) {
    throw new Error(`${field} supports at most ${RUNTIME_BRANCHES_PER_ENTRY_MAX} branch names`)
  }
  return branches
}

/** Parse a stored branch scope. Returns undefined for absent or invalid data. */
function parseRuntimeBranches(value: unknown): string[] | undefined {
  if (value === undefined) return undefined
  if (!Array.isArray(value)) return undefined
  const branches: string[] = []
  for (const item of value) {
    if (typeof item !== 'string' || item === '' || item.length > RUNTIME_BRANCH_NAME_MAX || !RUNTIME_BRANCH_NAME_RE.test(item)) return undefined
    branches.push(item)
  }
  return branches
}

function entryMatchesBranch(entry: RuntimeMemoryEntry, branch: string): boolean {
  return entry.branches === undefined || entry.branches.length === 0 || entry.branches.includes(branch)
}

function scopeBranch(branch: string | undefined): string | undefined {
  const value = branch?.trim()
  return value === undefined || value === '' ? undefined : value
}

function normalizeContent(value: string | undefined, field: string): string {
  const content = value?.trim().replace(/\s+/gu, ' ') ?? ''
  if (content === '') throw new Error(`${field} is required`)
  if (content.includes('§')) throw new Error(`${field} must not contain the reserved § entry delimiter`)
  const bytes = Buffer.byteLength(content, 'utf8')
  if (bytes > MAX_ENTRY_BYTES) throw new Error(`${field} is too large (${bytes} bytes; max ${MAX_ENTRY_BYTES})`)
  return content
}

function parseEntry(value: unknown): RuntimeMemoryEntry | undefined {
  if (!isRecord(value) || typeof value.content !== 'string' || !isTarget(value.target) || !isImportance(value.importance)) return undefined
  if (typeof value.created_at !== 'string' || typeof value.updated_at !== 'string') return undefined
  const content = value.content.trim().replace(/\s+/gu, ' ')
  if (content === '' || content.includes('§')) return undefined
  if (value.target === 'user' && value.branches !== undefined) return undefined
  const branches = parseRuntimeBranches(value.branches)
  return {
    content,
    created_at: value.created_at,
    updated_at: value.updated_at,
    target: value.target,
    importance: value.importance,
    ...(branches === undefined ? {} : { branches }),
  }
}

function byteCount(entries: readonly RuntimeMemoryEntry[], target: RuntimeMemoryTarget): number {
  const content = entries.filter(entry => entry.target === target).map(entry => entry.content).join(RUNTIME_ENTRY_DELIMITER)
  return Buffer.byteLength(content, 'utf8')
}

function markdown(entries: readonly RuntimeMemoryEntry[], target: RuntimeMemoryTarget): string {
  const content = entries.filter(entry => entry.target === target).map(entry => entry.content).join(RUNTIME_ENTRY_DELIMITER)
  return content === '' ? '' : `${content}\n`
}

function age(timestamp: string, projectedAt: number): string {
  const time = Date.parse(timestamp)
  if (!Number.isFinite(time)) return 'unknown'
  if (time > projectedAt) return 'future'
  return `${Math.floor((projectedAt - time) / 86_400_000)}d`
}

/** Prompt-only annotations; stored content, matching and capacity stay unchanged. */
function contextMarkdown(entries: readonly RuntimeMemoryEntry[], target: RuntimeMemoryTarget, projectedAt: number): string {
  return entries.filter(entry => entry.target === target).map(entry =>
    `[importance=${entry.importance}; created=${age(entry.created_at, projectedAt)}; updated=${age(entry.updated_at, projectedAt)}]\n${entry.content}`,
  ).join(RUNTIME_ENTRY_DELIMITER)
}

function revision(file: RuntimeMemoryFile): string {
  return createHash('sha256').update(JSON.stringify(file)).digest('hex')
}

function prepareMutation(
  before: readonly RuntimeMemoryEntry[],
  request: RuntimeMemoryMutation,
  now: string,
): PreparedRuntimeMemoryMutation {
  if (!isTarget(request.target)) throw new Error('target must be memory or user')
  if (!['add', 'replace', 'remove'].includes(request.action)) throw new Error('action must be add, replace, or remove')
  if (request.importance !== undefined && !isImportance(request.importance)) throw new Error('importance must be critical, normal, or low')
  if (request.target === 'user' && request.branches !== undefined) throw new Error('branches applies to target=memory only')
  const branchScope = request.branches === undefined ? undefined : normalizeRuntimeBranches(request.branches)
  const entries = before.map(entry => ({ ...entry }))

  if (request.action === 'add') {
    const content = normalizeContent(request.content, 'content')
    const duplicate = entries.find(entry => entry.target === request.target && entry.content === content)
    if (duplicate !== undefined) {
      return {
        changed: false,
        projectedEntries: entries,
        compactableEntries: entries.filter(entry => entry.target === request.target),
        fields: { message: 'Entry already exists (no duplicate added).', added: duplicate.content },
      }
    }
    const pendingEntry: RuntimeMemoryEntry = {
      content,
      created_at: now,
      updated_at: now,
      target: request.target,
      importance: request.importance ?? 'normal',
      ...(branchScope !== undefined && branchScope.length > 0 ? { branches: branchScope } : {}),
    }
    return {
      changed: true,
      projectedEntries: [...entries, pendingEntry],
      compactableEntries: entries.filter(entry => entry.target === request.target),
      pendingEntry,
      fields: { message: 'Entry added.', added: content },
    }
  }

  const oldText = normalizeContent(request.oldText, 'oldText')
  const substringMatches = entries
    .map((entry, index) => entry.target === request.target && entry.content.includes(oldText) ? index : -1)
    .filter(index => index >= 0)
  const exactMatches = substringMatches.filter(index => entries[index]!.content === oldText)
  // Full entry content takes precedence, but duplicate exact entries remain ambiguous.
  const matches = exactMatches.length > 0 ? exactMatches : substringMatches
  if (matches.length === 0) throw new Error(`No ${request.target} entry contains ${JSON.stringify(oldText)}.`)
  if (matches.length > 1) throw new Error(`Multiple ${request.target} entries contain ${JSON.stringify(oldText)}; use a unique substring.`)
  const index = matches[0]!
  const previous = entries[index]!
  const compactableEntries = entries.filter((entry, entryIndex) => entry.target === request.target && entryIndex !== index)

  if (request.action === 'replace') {
    const content = normalizeContent(request.content, 'content')
    const pendingEntry: RuntimeMemoryEntry = {
      ...previous,
      content,
      updated_at: now,
      importance: request.importance ?? previous.importance,
    }
    if (branchScope !== undefined) {
      if (branchScope.length === 0) delete pendingEntry.branches
      else pendingEntry.branches = branchScope
    }
    entries[index] = pendingEntry
    return {
      changed: true,
      projectedEntries: entries,
      compactableEntries,
      pendingEntry,
      excludedEntry: previous,
      fields: { message: 'Entry replaced.', replaced: { from: previous.content, to: content } },
    }
  }

  return {
    changed: true,
    projectedEntries: entries.filter((_, entryIndex) => entryIndex !== index),
    compactableEntries,
    excludedEntry: previous,
    fields: { message: 'Entry removed.', removed: previous.content },
  }
}

function compactionCandidates(
  compacted: readonly RuntimeMemoryCompactedEntry[],
  existing: readonly RuntimeMemoryEntry[],
  target: RuntimeMemoryTarget,
  now: string,
): RuntimeMemoryEntry[] {
  const seen = new Set<string>()
  const existingByContent = new Map<string, RuntimeMemoryEntry>()
  // Preserve find()'s first match, including legacy duplicates with different scopes.
  for (const entry of existing) if (!existingByContent.has(entry.content)) existingByContent.set(entry.content, entry)
  return compacted.map((entry): RuntimeMemoryEntry => {
    const content = normalizeContent(entry.content, 'compacted content')
    if (!isImportance(entry.importance)) throw new Error('compacted importance must be critical, normal, or low')
    if (seen.has(content)) throw new Error('compacted runtime memory contains duplicate entries')
    seen.add(content)
    const unchanged = existingByContent.get(content)
    // A compactor that drops the scope inherits it from the identical committed entry, so branch
    // visibility can never be silently widened by maintenance.
    const inheritedBranches = entry.branches ?? unchanged?.branches
    return {
      content,
      created_at: unchanged?.created_at ?? now,
      updated_at: unchanged?.updated_at ?? now,
      target,
      importance: entry.importance,
      ...(inheritedBranches === undefined ? {} : { branches: inheritedBranches }),
    }
  })
}

function packCompactionCandidates(
  replacements: readonly RuntimeMemoryEntry[],
  target: RuntimeMemoryTarget,
  maxBytes: number,
): RuntimeMemoryEntry[] {
  const ranked: Record<RuntimeMemoryImportance, number[]> = { critical: [], normal: [], low: [] }
  replacements.forEach((entry, index) => ranked[entry.importance].push(index))
  const selected = new Set<number>()
  const delimiterBytes = Buffer.byteLength(RUNTIME_ENTRY_DELIMITER, 'utf8')
  let used = 0
  let count = 0
  // Three stable buckets preserve priority and original order without sorting.
  for (const importance of ['critical', 'normal', 'low'] as const) for (const index of ranked[importance]) {
    const entry = replacements[index]!
    const matchesTarget = entry.target === target
    const added = matchesTarget ? Buffer.byteLength(entry.content, 'utf8') + (count === 0 ? 0 : delimiterBytes) : 0
    if (used + added > maxBytes) continue
    used += added
    if (matchesTarget) count += 1
    selected.add(index)
  }
  return replacements.filter((_, index) => selected.has(index))
}

function sleepSync(milliseconds: number): void {
  const buffer = new Int32Array(new SharedArrayBuffer(4))
  Atomics.wait(buffer, 0, 0, milliseconds)
}

/**
 * Single authority for hot memory. JSON is the durable source of truth;
 * Markdown files retain plain content; prompt projections add recorded metadata.
 */
export class RuntimeMemoryController {
  readonly directory: string
  readonly sourcePath: string
  readonly userSourcePath: string
  readonly memoryPath: string
  readonly userPath: string
  readonly lockPath: string
  readonly limits: RuntimeMemoryLimits

  private queue: Promise<unknown> = Promise.resolve()
  private readonly localUserPath: string
  private readonly userController: RuntimeMemoryController | undefined

  constructor(
    runner: RuntimeMemoryStorage,
    private readonly now: () => Date = () => new Date(),
    limits: RuntimeMemoryLimits = RUNTIME_MEMORY_LIMITS,
    userRunner?: RuntimeMemoryStorage,
  ) {
    for (const [target, limit] of Object.entries(limits)) {
      if (!Number.isInteger(limit) || limit < 1 || limit > MAX_RUNTIME_MEMORY_LIMIT_BYTES) {
        throw new Error(`runtime ${target} memory limit must be an integer within 1..${MAX_RUNTIME_MEMORY_LIMIT_BYTES} bytes`)
      }
    }
    this.limits = { memory: limits.memory, user: limits.user }
    this.directory = join(runner.effectiveDataDir(), 'runtime')
    this.sourcePath = join(this.directory, 'memories.json')
    this.memoryPath = join(this.directory, 'MEMORY.md')
    this.localUserPath = join(this.directory, 'USER.md')
    this.lockPath = join(this.directory, '.memories.lock')
    const userDirectory = userRunner === undefined ? this.directory : join(userRunner.effectiveDataDir(), 'runtime')
    this.userController = userDirectory === this.directory || userRunner === undefined
      ? undefined
      : new RuntimeMemoryController(userRunner, now, this.limits)
    this.userPath = this.userController?.userPath ?? this.localUserPath
    this.userSourcePath = this.userController?.sourcePath ?? this.sourcePath
    this.initialize()
  }

  snapshot(): RuntimeMemorySnapshot {
    const local = this.readSource()
    if (this.userController === undefined) return this.snapshotUnlocked(local)
    const global = this.userController.readSource()
    return this.snapshotUnlocked({
      version: RUNTIME_MEMORY_VERSION,
      entries: [
        ...global.entries.filter(entry => entry.target === 'user'),
        ...local.entries.filter(entry => entry.target === 'memory'),
      ],
    })
  }

  /**
   * Read the exact Runtime revision and its prompt projection from each
   * configured authority root.
   * When a git branch is supplied, target=memory entries scoped to other
   * branches are hidden from the projection; the on-disk Markdown files and
   * the source JSON always remain complete.
   */
  contextProjection(branch?: string): RuntimeMemoryContextProjection {
    const branchScope = scopeBranch(branch)
    const projectedAt = this.now().getTime()
    const local = this.localContextProjection(projectedAt, branchScope)
    const global = this.userController?.localContextProjection(projectedAt)
    const user = global?.user ?? local.user
    const memory = local.memory
    const entries = global === undefined
      ? local.snapshot.entries
      : [
          ...global.snapshot.entries.filter(entry => entry.target === 'user'),
          ...local.snapshot.entries.filter(entry => entry.target === 'memory'),
        ]
    const visibleMemory = local.snapshot.targets.memory
    const visibleUser = global?.snapshot.targets.user ?? local.snapshot.targets.user
    const snapshot = global === undefined
      ? local.snapshot
      : {
          directory: this.directory,
          sourcePath: this.sourcePath,
          revision: revision({ version: RUNTIME_MEMORY_VERSION, entries }),
          generatedAt: new Date(projectedAt).toISOString(),
          entries,
          targets: {
            memory: { ...visibleMemory, markdownPath: this.memoryPath },
            user: { ...visibleUser, markdownPath: this.userPath },
          },
        } satisfies RuntimeMemorySnapshot
    const storeMemory = this.targetView(entries, 'memory')
    const storeUser = this.targetView(entries, 'user')
    const visibleEntries = branchScope === undefined
      ? entries
      : entries.filter(entry => entry.target === 'user' || entryMatchesBranch(entry, branchScope))
    const branchLine = branchScope === undefined
      ? ''
      : `\nGit branch: ${branchScope}${local.hidden > 0 ? ` (${local.hidden} branch-scoped entr${local.hidden === 1 ? 'y' : 'ies'} hidden)` : ''}`
    return {
      revision: snapshot.revision,
      entries: visibleEntries.map(entry => ({ ...entry, ...(entry.branches === undefined ? {} : { branches: [...entry.branches] }) })),
      totalEntries: entries.length,
      text: `MNEMON RUNTIME MEMORY SNAPSHOT
Revision: ${snapshot.revision}${branchLine}
Metadata lines are annotations; created/updated are ages at projection in whole days (future/unknown for future/invalid timestamps). Current instructions win. For old_text/oldText, use entry content only.

Contents of USER.md (user profile; entries: ${visibleUser.entryCount}; UTF-8 bytes: ${storeUser.used}/${storeUser.limit})
<runtime-memory-file name="USER.md">
${user || '(empty)'}
</runtime-memory-file>

Contents of MEMORY.md (working reference; entries: ${visibleMemory.entryCount}; UTF-8 bytes: ${storeMemory.used}/${storeMemory.limit})
<runtime-memory-file name="MEMORY.md">
${memory || '(empty)'}
</runtime-memory-file>`,
    }
  }

  private localContextProjection(projectedAt: number, branch?: string): { snapshot: RuntimeMemorySnapshot; user: string; memory: string; hidden: number } {
    const branchScope = scopeBranch(branch)
    return this.withLock(() => {
      const file = this.readSource()
      this.repairProjections(file)
      const entries = file.entries.map(entry => ({ ...entry }))
      const visible = branchScope === undefined
        ? entries
        : entries.filter(entry => entry.target === 'user' || entryMatchesBranch(entry, branchScope))
      return {
        snapshot: {
          directory: this.directory,
          sourcePath: this.sourcePath,
          revision: revision(file),
          generatedAt: new Date(projectedAt).toISOString(),
          entries,
          targets: {
            memory: this.targetView(visible, 'memory'),
            user: this.targetView(visible, 'user'),
          },
        } satisfies RuntimeMemorySnapshot,
        user: contextMarkdown(visible, 'user', projectedAt),
        memory: contextMarkdown(visible, 'memory', projectedAt),
        hidden: branchScope === undefined ? 0 : entries.length - visible.length,
      }
    })
  }

  mutate(request: RuntimeMemoryMutation): Promise<RuntimeMemoryMutationResult> {
    if (request.target === 'user' && this.userController !== undefined) return this.userController.mutate(request)
    const operation = this.queue.then(() => this.withLock(() => this.mutateLocked(request)))
    this.queue = operation.catch(() => undefined)
    return operation
  }

  /** Resolve exactly which committed entries survive a blocked mutation and are safe to compact. */
  planMaintenance(request: RuntimeMemoryMutation): Promise<RuntimeMemoryMaintenancePlan> {
    if (request.target === 'user' && this.userController !== undefined) return this.userController.planMaintenance(request)
    const operation = this.queue.then(() => this.withLock(() => {
      const file = this.readSource()
      const prepared = prepareMutation(file.entries, request, this.now().toISOString())
      const used = byteCount(file.entries, request.target)
      const projected = byteCount(prepared.projectedEntries, request.target)
      const limit = this.limits[request.target]
      return {
        revision: revision(file),
        action: request.action,
        target: request.target,
        entries: prepared.compactableEntries.map(entry => ({ ...entry })),
        ...(prepared.pendingEntry === undefined ? {} : {
          pending: { content: prepared.pendingEntry.content, importance: prepared.pendingEntry.importance },
        }),
        ...(prepared.excludedEntry === undefined ? {} : { excluded: { ...prepared.excludedEntry } }),
        used,
        projected,
        limit,
        requiresMaintenance: prepared.changed && projected > limit,
      }
    }))
    this.queue = operation.catch(() => undefined)
    return operation
  }

  /** Commit semantic compaction and the original mutation together, or leave every local file unchanged. */
  compactAndMutate(
    expectedRevision: string,
    request: RuntimeMemoryMutation,
    compacted: RuntimeMemoryCompactedEntry[],
    maxCompactedBytes?: number,
  ): Promise<RuntimeMemoryMutationResult> {
    if (request.target === 'user' && this.userController !== undefined) {
      return this.userController.compactAndMutate(expectedRevision, request, compacted, maxCompactedBytes)
    }
    const operation = this.queue.then(() => this.withLock(() => {
      const file = this.readSource()
      const beforeRevision = revision(file)
      if (beforeRevision !== expectedRevision) throw new RuntimeMemoryConflictError()
      const now = this.now().toISOString()
      const prepared = prepareMutation(file.entries, request, now)
      const compactedByteBudget = maxCompactedBytes ?? this.limits[request.target]
      if (!Number.isInteger(compactedByteBudget) || compactedByteBudget < 0 || compactedByteBudget > this.limits[request.target]) {
        throw new Error('compaction byte budget is invalid')
      }
      if (!prepared.changed) {
        return this.result(request.target, prepared.projectedEntries, prepared.fields)
      }
      const replacements = compactionCandidates(compacted, prepared.compactableEntries, request.target, now)
      if (prepared.pendingEntry !== undefined && replacements.some(entry => entry.content === prepared.pendingEntry!.content)) {
        throw new Error('compacted runtime memory duplicates the pending mutation')
      }
      if (prepared.excludedEntry !== undefined && replacements.some(entry => entry.content === prepared.excludedEntry!.content)) {
        throw new Error('compacted runtime memory reintroduces the replaced or removed entry')
      }
      // The worker supplies semantic candidates; deterministic packing owns exact
      // UTF-8 accounting so the LLM never has to count bytes or delimiters.
      const fitted = packCompactionCandidates(replacements, request.target, compactedByteBudget)
      const targetEntries = [...fitted, ...(prepared.pendingEntry === undefined ? [] : [prepared.pendingEntry])]
      const entries = [...file.entries.filter(entry => entry.target !== request.target), ...targetEntries]
      const used = byteCount(entries, request.target)
      const limit = this.limits[request.target]
      if (used > limit) throw new RuntimeMemoryCapacityError(request.target, byteCount(file.entries, request.target), used, limit)
      const next: RuntimeMemoryFile = { version: RUNTIME_MEMORY_VERSION, entries }
      this.persist(next)
      return this.result(request.target, entries, prepared.fields)
    }))
    this.queue = operation.catch(() => undefined)
    return operation
  }

  private initialize(): void {
    mkdirSync(this.directory, { recursive: true, mode: 0o700 })
    this.withLock(() => {
      const file = this.readSource()
      this.persist(file)
    })
  }

  private mutateLocked(request: RuntimeMemoryMutation): RuntimeMemoryMutationResult {
    const file = this.readSource()
    const prepared = prepareMutation(file.entries, request, this.now().toISOString())
    if (!prepared.changed) return this.result(request.target, prepared.projectedEntries, prepared.fields)
    const used = byteCount(prepared.projectedEntries, request.target)
    const limit = this.limits[request.target]
    if (used > limit) throw new RuntimeMemoryCapacityError(request.target, byteCount(file.entries, request.target), used, limit)
    this.persist({ version: RUNTIME_MEMORY_VERSION, entries: prepared.projectedEntries })
    return this.result(request.target, prepared.projectedEntries, prepared.fields)
  }

  private result(
    target: RuntimeMemoryTarget,
    entries: readonly RuntimeMemoryEntry[],
    fields: RuntimeMemoryResultFields,
  ): RuntimeMemoryMutationResult {
    return {
      success: true,
      message: fields.message,
      target,
      entryCount: entries.filter(entry => entry.target === target).length,
      usage: { used: byteCount(entries, target), limit: this.limits[target] },
      ...(fields.added === undefined ? {} : { added: fields.added }),
      ...(fields.replaced === undefined ? {} : { replaced: fields.replaced }),
      ...(fields.removed === undefined ? {} : { removed: fields.removed }),
    }
  }

  private targetView(entries: readonly RuntimeMemoryEntry[], target: RuntimeMemoryTarget): RuntimeMemoryTargetView {
    return {
      target,
      entryCount: entries.filter(entry => entry.target === target).length,
      used: byteCount(entries, target),
      limit: this.limits[target],
      markdownPath: target === 'memory' ? this.memoryPath : this.userPath,
    }
  }

  private snapshotUnlocked(file: RuntimeMemoryFile): RuntimeMemorySnapshot {
    const entries = file.entries.map(entry => ({ ...entry }))
    return {
      directory: this.directory,
      sourcePath: this.sourcePath,
      revision: revision(file),
      generatedAt: this.now().toISOString(),
      entries,
      targets: {
        memory: this.targetView(entries, 'memory'),
        user: this.targetView(entries, 'user'),
      },
    }
  }

  private readSource(): RuntimeMemoryFile {
    if (!existsSync(this.sourcePath)) return { version: RUNTIME_MEMORY_VERSION, entries: [] }
    let parsed: unknown
    try {
      parsed = JSON.parse(readFileSync(this.sourcePath, 'utf8'))
    } catch (error) {
      throw new Error(`runtime memories.json is unreadable: ${error instanceof Error ? error.message : String(error)}`)
    }
    if (!isRecord(parsed) || parsed.version !== RUNTIME_MEMORY_VERSION || !Array.isArray(parsed.entries)) {
      throw new Error(`runtime memories.json must use version ${RUNTIME_MEMORY_VERSION}`)
    }
    const entries = parsed.entries.map(parseEntry)
    if (entries.some(entry => entry === undefined)) throw new Error('runtime memories.json contains an invalid entry')
    return { version: RUNTIME_MEMORY_VERSION, entries: entries as RuntimeMemoryEntry[] }
  }

  private persist(file: RuntimeMemoryFile): void {
    mkdirSync(this.directory, { recursive: true, mode: 0o700 })
    const nonce = `${process.pid}.${Date.now()}.${Math.random().toString(16).slice(2)}`
    const writes: Array<[string, string]> = [
      [this.localUserPath, markdown(file.entries, 'user')],
      [this.memoryPath, markdown(file.entries, 'memory')],
      [this.sourcePath, `${JSON.stringify(file, null, 2)}\n`],
    ]
    const temporaries = writes.map(([path]) => join(this.directory, `.${basename(path)}.${nonce}.tmp`))
    try {
      writes.forEach(([, content], index) => writeFileSync(temporaries[index]!, content, { encoding: 'utf8', mode: 0o600 }))
      // Projections move first; memories.json is the final commit marker and source of truth.
      writes.forEach(([path], index) => renameSync(temporaries[index]!, path))
    } finally {
      for (const temporary of temporaries) rmSync(temporary, { force: true })
    }
  }

  private repairProjections(file: RuntimeMemoryFile): void {
    for (const [path, target] of [[this.localUserPath, 'user'], [this.memoryPath, 'memory']] as const) {
      const expected = markdown(file.entries, target)
      let current: string | undefined
      try {
        current = readFileSync(path, 'utf8')
      } catch {
        current = undefined
      }
      if (current === expected) continue
      const temporary = join(this.directory, `.${basename(path)}.${process.pid}.${Date.now()}.tmp`)
      try {
        writeFileSync(temporary, expected, { encoding: 'utf8', mode: 0o600 })
        renameSync(temporary, path)
      } finally {
        rmSync(temporary, { force: true })
      }
    }
  }

  private withLock<T>(callback: () => T): T {
    const started = Date.now()
    let descriptor: number | undefined
    while (descriptor === undefined) {
      try {
        descriptor = openSync(this.lockPath, 'wx', 0o600)
      } catch (error) {
        const code = isRecord(error) && typeof error.code === 'string' ? error.code : undefined
        if (code !== 'EEXIST') throw error
        try {
          if (Date.now() - statSync(this.lockPath).mtimeMs > LOCK_STALE_MS) {
            rmSync(this.lockPath, { force: true })
            continue
          }
        } catch {
          continue
        }
        if (Date.now() - started >= LOCK_TIMEOUT_MS) throw new Error('timed out waiting for the runtime memory controller lock')
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
