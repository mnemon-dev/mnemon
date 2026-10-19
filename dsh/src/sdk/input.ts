import { createHash, randomUUID } from 'node:crypto'
import { canonicalMemoryJson } from '../core/definitions.ts'
import type { MemoryJsonValue, MemoryMutationReceipt, MemoryMigrationLineage, MemoryMutationCompletion } from "../core/contracts/index.ts"

/** Stable digest of validated configuration; raw values never enter diagnostics. */
export function memoryConfigurationDigest(value: unknown): string {
  return `config:${createHash('sha256').update(canonicalMemoryJson(value, 'memory configuration')).digest('hex')}`
}

export function record(value: MemoryJsonValue, label: string): Record<string, MemoryJsonValue> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error(`${label} must be an object`)
  return value
}

export function text(value: MemoryJsonValue | undefined, label: string, maximum: number, required = true): string | undefined {
  if (value === undefined && !required) return undefined
  if (typeof value !== 'string') throw new Error(`${label} must be a string`)
  const normalized = value.trim()
  if (required && normalized === '') throw new Error(`${label} is required`)
  if (normalized.length > maximum) throw new Error(`${label} is too long (max ${maximum} characters)`)
  return normalized === '' ? undefined : normalized
}

export function integer(value: MemoryJsonValue | undefined, fallback: number, minimum: number, maximum: number): number {
  if (value === undefined) return fallback
  if (typeof value !== 'number' || !Number.isInteger(value) || value < minimum || value > maximum) {
    throw new Error(`value must be an integer within ${minimum}..${maximum}`)
  }
  return value
}

export function stringArray(value: MemoryJsonValue | undefined, label: string, maximum = 50): string[] | undefined {
  if (value === undefined) return undefined
  if (!Array.isArray(value) || value.length > maximum || value.some(item => typeof item !== 'string')) throw new Error(`${label} must be a string array`)
  return value.map(item => (item as string).trim()).filter(Boolean)
}

export function truncate(value: string, maximum: number): string {
  if (maximum <= 0) return ''
  if (value.length <= maximum) return value
  let end = maximum - 1
  // Keep a supplementary character whole when reserving one unit for the ellipsis.
  if (end > 0 && /[\uD800-\uDBFF]/u.test(value[end - 1]!)) end--
  return `${value.slice(0, end)}…`
}

export function receipt(
  viewId: string,
  offerId: string,
  sourceInstanceKey: string,
  revision: string | undefined,
  details: MemoryJsonValue,
  completion: MemoryMutationCompletion = 'unknown',
): MemoryMutationReceipt {
  return {
    id: `receipt:${randomUUID()}`,
    viewId,
    offerId,
    sourceInstanceKey,
    status: completion === 'failed' ? 'failed' : completion === 'partial' || completion === 'unknown' ? 'partial' : 'succeeded',
    completion,
    ...(completion === 'committed' ? { committedAt: new Date().toISOString() } : {}),
    ...(revision === undefined ? {} : { revision }),
    details,
  }
}

/** Validate migration proof supplied by an authorized Source management caller. */
export function migrationLineage(value: MemoryJsonValue | undefined): MemoryMigrationLineage[] {
  if (value === undefined) return []
  if (!Array.isArray(value) || value.length > 1_000) throw new Error('lineage must be an array of at most 1000 entries')
  return value.map(item => {
    const entry = record(item, 'lineage')
    const endpoint = (value: MemoryJsonValue | undefined) => {
      const fields = record(value!, 'lineage endpoint')
      return {
        layerId: text(fields.layerId, 'lineage layerId', 300)!,
        reference: text(fields.reference, 'lineage reference', 2_000)!,
        digest: text(fields.digest, 'lineage digest', 300)!,
      }
    }
    return { source: endpoint(entry.source), destination: endpoint(entry.destination) }
  })
}
