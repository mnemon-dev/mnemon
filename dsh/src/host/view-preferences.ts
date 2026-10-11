import z from '@deepseek-ai/schemastery'
import type { MemoryJsonValue } from '../core/contracts/index.ts'
import type { MemoryViewPreferences } from './view-protocol.ts'

export interface LegacySourcePreferences {
  sources: Record<string, { enabled: boolean }>
}

export const schema: z<MemoryViewPreferences> = z.object({
  strategyTypeId: z.string(),
  entries: z.dict(z.object({ enabled: z.boolean(), config: z.dict(z.any()).default({}) })).default({}),
})
export const legacySchema: z<LegacySourcePreferences> = z.object({
  sources: z.dict(z.object({ enabled: z.boolean() })).default({}),
})
const ENTRY_ID = /^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,299}$/u
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T

function record(value: unknown): Record<string, MemoryJsonValue> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('Plugin configuration must be an object')
  const json = JSON.stringify(value)
  if (json.length > 64 * 1024) throw new Error('Plugin configuration exceeds 64 KiB')
  const parsed = JSON.parse(json) as Record<string, MemoryJsonValue>
  for (const key of Object.keys(parsed)) if (['__proto__', 'prototype', 'constructor'].includes(key)) throw new Error('Unsafe plugin configuration key')
  return parsed
}

export function preferences(value: MemoryViewPreferences): MemoryViewPreferences {
  if (value.strategyTypeId !== undefined && !/^[a-z][a-z0-9-]{0,127}$/u.test(value.strategyTypeId)) throw new Error('Invalid Strategy type id')
  const entries = record(value.entries ?? {})
  if (Object.keys(entries).length > 64) throw new Error('At most 64 memory plugin Entries can be configured')
  for (const [entryId, item] of Object.entries(entries)) {
    if (!ENTRY_ID.test(entryId)) throw new Error('Invalid memory plugin Entry id')
    const candidate = record(item)
    if (candidate.enabled !== undefined && typeof candidate.enabled !== 'boolean') throw new Error('Plugin enabled must be boolean')
    record(candidate.config)
  }
  return clone(value)
}

export function legacyPreferences(value: LegacySourcePreferences): LegacySourcePreferences {
  const sources = value.sources ?? {}
  if (Object.keys(sources).length > 64) throw new Error('At most 64 legacy Source Entries can be configured')
  for (const [entryId, item] of Object.entries(sources)) {
    if (!ENTRY_ID.test(entryId) || typeof item?.enabled !== 'boolean') throw new Error('Invalid legacy Source Entry preference')
  }
  return clone({ sources })
}
