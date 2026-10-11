import type { JsonValue, MemoryActivityItem, MemoryActivityRead, MemoryActivityWriteback } from './protocol.ts'
import { mutationResultCommitted } from './receipts.ts'

export const MNEMON_ACTIVITY_SCHEMA = 'dsh-mnemon.activity/v1' as const

interface ActivityMetaBase {
  schema: typeof MNEMON_ACTIVITY_SCHEMA
  operationId: string
  reference?: string
  sourceTypeId?: string
}

interface ReadActivityMeta extends ActivityMetaBase {
  kind: 'read'
  items: MemoryActivityItem[]
}

interface WriteActivityMeta extends ActivityMetaBase {
  kind: 'write'
  committed: boolean
  item: MemoryActivityItem
}

export type MnemonActivityMeta = ReadActivityMeta | WriteActivityMeta

const MAX_ITEMS = 8
const MAX_TITLE = 160
const MAX_EXCERPT = 500

function object(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

function bounded(value: unknown, maximum: number): string | undefined {
  if (typeof value !== 'string') return undefined
  const normalized = value.replace(/\s+/gu, ' ').trim()
  if (normalized === '') return undefined
  return normalized.length <= maximum ? normalized : normalized.slice(0, maximum - 1) + '…'
}

function item(value: unknown, index: number): MemoryActivityItem | undefined {
  const entry = object(value)
  if (entry === undefined) return undefined
  const content = bounded(entry.content ?? entry.text ?? entry.excerpt, MAX_EXCERPT)
  const explicitTitle = bounded(entry.title ?? entry.label ?? entry.name ?? entry.memoryBodyName, MAX_TITLE)
  const id = bounded(entry.id ?? entry.documentId ?? entry.memoryBodyId, 300) ?? `item-${index + 1}`
  const title = explicitTitle ?? bounded(content, MAX_TITLE)
  if (title === undefined) return undefined
  return { id, title, ...(content === undefined || content === title ? {} : { excerpt: content }) }
}

function resultItems(value: unknown): MemoryActivityItem[] {
  const root = object(value)
  if (root === undefined) return []
  const candidates = [root.results, root.items, root.suggestions].flatMap(value => Array.isArray(value) ? value : [])
  const seen = new Set<string>()
  const items: MemoryActivityItem[] = []
  for (const [index, value] of candidates.entries()) {
    const candidate = item(value, index)
    if (candidate === undefined || seen.has(candidate.id)) continue
    seen.add(candidate.id)
    items.push(candidate)
    if (items.length >= MAX_ITEMS) break
  }
  return items
}

function writeItem(argsValue: unknown, operationId: string): MemoryActivityItem {
  const outer = object(argsValue) ?? {}
  const input = object(outer.input) ?? outer
  // A removal names the text it removed; an item with no text is known only by its id.
  const content = bounded(input.content ?? input.description ?? input.reason ?? input.summary ?? input.old_text ?? input.oldText, MAX_EXCERPT)
  const explicitTitle = bounded(input.title ?? input.name, MAX_TITLE)
  const identity = bounded(input.id ?? input.memoryBodyId ?? input.targetMemoryBodyId ?? input.sourceId, 300)
  const title = explicitTitle ?? bounded(content, MAX_TITLE) ?? identity ?? operationId
  return {
    id: identity ?? operationId,
    title,
    ...(content === undefined || content === title ? {} : { excerpt: content }),
  }
}

function reference(argsValue: unknown, key: 'routeId' | 'offerId'): string | undefined {
  return bounded(object(argsValue)?.[key], 600)
}

export function memoryReadPresentation(sourceTypeId: string | undefined, operationId: string, referenceKey?: 'routeId'): (args: unknown, value: unknown) => JsonValue {
  return (args, value) => ({
    schema: MNEMON_ACTIVITY_SCHEMA,
    kind: 'read',
    operationId,
    ...(sourceTypeId === undefined ? {} : { sourceTypeId }),
    ...(referenceKey === undefined || reference(args, referenceKey) === undefined ? {} : { reference: reference(args, referenceKey)! }),
    items: resultItems(value),
  }) as unknown as JsonValue
}

export function memoryWritePresentation(sourceTypeId: string | undefined, operationId: string, referenceKey?: 'offerId'): (args: unknown, value: unknown) => JsonValue {
  return (args, value) => ({
    schema: MNEMON_ACTIVITY_SCHEMA,
    kind: 'write',
    operationId,
    ...(sourceTypeId === undefined ? {} : { sourceTypeId }),
    ...(referenceKey === undefined || reference(args, referenceKey) === undefined ? {} : { reference: reference(args, referenceKey)! }),
    committed: mutationResultCommitted(value) || operationId === 'mutate' && object(value)?.success === true,
    item: writeItem(args, operationId),
  }) as unknown as JsonValue
}

function validItem(value: unknown): value is MemoryActivityItem {
  const entry = object(value)
  return entry !== undefined && bounded(entry.id, 300) === entry.id && bounded(entry.title, MAX_TITLE) === entry.title
    && (entry.excerpt === undefined || bounded(entry.excerpt, MAX_EXCERPT) === entry.excerpt)
}

function cleanItem(value: unknown): MemoryActivityItem {
  const entry = value as MemoryActivityItem
  return { id: entry.id, title: entry.title, ...(entry.excerpt === undefined ? {} : { excerpt: entry.excerpt }) }
}

/** Fail closed on arbitrary third-party/session metadata. */
export function parseMnemonActivityMeta(value: unknown, callId: string, toolName: string): { read?: MemoryActivityRead; writeback?: MemoryActivityWriteback } {
  const meta = object(value)
  if (meta?.schema !== MNEMON_ACTIVITY_SCHEMA || (meta.kind !== 'read' && meta.kind !== 'write')) return {}
  const operationId = bounded(meta.operationId, 300)
  const sourceTypeId = bounded(meta.sourceTypeId, 128)
  const activityReference = bounded(meta.reference, 600)
  if (operationId === undefined) return {}
  const base = { callId, toolName, operationId,
    ...(sourceTypeId === undefined ? {} : { sourceTypeId }), ...(activityReference === undefined ? {} : { reference: activityReference }) }
  if (meta.kind === 'read') {
    if (!Array.isArray(meta.items) || meta.items.length > MAX_ITEMS || !meta.items.every(validItem)) return {}
    return { read: { ...base, items: meta.items.map(cleanItem) } }
  }
  if (meta.committed !== true || !validItem(meta.item)) return {}
  return { writeback: { ...base, item: cleanItem(meta.item) } }
}
