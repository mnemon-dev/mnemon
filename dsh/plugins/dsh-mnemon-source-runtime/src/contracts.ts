export type RuntimeMemoryTarget = 'memory' | 'user'
export type RuntimeMemoryImportance = 'critical' | 'normal' | 'low'
export type RuntimeMemoryAction = 'add' | 'replace' | 'remove'

export interface RuntimeMemoryEntry {
  content: string
  created_at: string
  updated_at: string
  target: RuntimeMemoryTarget
  importance: RuntimeMemoryImportance
  /** Optional git branch names that limit where this entry is projected. Absent means every branch. */
  branches?: string[]
}

export interface RuntimeMemoryUsage {
  used: number
  limit: number
}

export interface RuntimeMemoryTargetView extends RuntimeMemoryUsage {
  target: RuntimeMemoryTarget
  entryCount: number
  markdownPath: string
}

export interface RuntimeMemorySnapshot {
  directory: string
  sourcePath: string
  revision: string
  generatedAt: string
  entries: RuntimeMemoryEntry[]
  targets: Record<RuntimeMemoryTarget, RuntimeMemoryTargetView>
}

export interface RuntimeMemoryCompactedEntry {
  content: string
  importance: RuntimeMemoryImportance
  /** Branch scope carried through compaction; absent means the entry is visible on every branch. */
  branches?: string[]
}

export interface RuntimeMemoryMutation {
  action: RuntimeMemoryAction
  target: RuntimeMemoryTarget
  content?: string
  oldText?: string
  importance?: RuntimeMemoryImportance
  /** Git branch names limiting where a target=memory entry is projected. Absent keeps the current scope on replace; an empty list clears it. */
  branches?: string[]
}

export type RuntimeMemoryMutationResult = {
  success: true
  message: string
  target: RuntimeMemoryTarget
  entryCount: number
  usage: RuntimeMemoryUsage
  added?: string
  replaced?: { from: string; to: string }
  removed?: string
  maintenance?: {
    kind: 'local-compaction' | 'mnemon-archive'
    runId: string
    provider: string
    summary: string
    memoryBodyIds: string[]
  }
}

export interface RuntimeMemoryMaintenancePlan {
  revision: string
  action: RuntimeMemoryAction
  target: RuntimeMemoryTarget
  entries: RuntimeMemoryEntry[]
  pending?: RuntimeMemoryCompactedEntry
  excluded?: RuntimeMemoryEntry
  used: number
  projected: number
  limit: number
  requiresMaintenance: boolean
}

import { DEFAULT_RUNTIME_MEMORY_LIMIT_BYTES, DEFAULT_RUNTIME_USER_LIMIT_BYTES } from './defaults.ts'
export const RUNTIME_MEMORY_VERSION = 1
export const RUNTIME_ENTRY_DELIMITER = '\n§\n'
export interface RuntimeMemoryLimits {
  readonly memory: number
  readonly user: number
}
export const RUNTIME_MEMORY_LIMITS: RuntimeMemoryLimits = {
  memory: DEFAULT_RUNTIME_MEMORY_LIMIT_BYTES,
  user: DEFAULT_RUNTIME_USER_LIMIT_BYTES,
}
