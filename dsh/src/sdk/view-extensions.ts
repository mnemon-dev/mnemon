import { defineMemoryStrategyExtension } from '../core/definitions.ts'
import {
  ANY_MEMORY_STRATEGY, COMPOSABLE_MEMORY_API_VERSION, MEMORY_VIEW_EXTENSION_SLOTS,
  type MemoryAvailableSource, type MemoryJsonValue, type MemoryStrategyContribution, type MemoryStrategyExtensionDefinition,
  type MemoryViewExtensionSlot, type MemoryViewExtensionValues, type MemoryViewRequest,
} from '../core/contracts/index.ts'
import { integer, record, text } from './input.ts'

const SOURCE_KEY = /^source:[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,292}$/u
const ACTION_ID = /^[a-z][a-z0-9-]{0,127}$/u
const FIELDS: Record<MemoryViewExtensionSlot, readonly string[]> = {
  selection: ['sourceKeys', 'writableSourceKeys'],
  projection: ['maxProjectionCharacters'],
  capture: ['instruction', 'actionIds', 'sourceKeys'],
}

function sourceKeys(value: MemoryJsonValue | undefined, label: string): string[] {
  if (!Array.isArray(value) || value.length > 32 || value.some(key => typeof key !== 'string' || !SOURCE_KEY.test(key))) {
    throw new Error(`${label} must contain at most 32 exact Source instance keys`)
  }
  if (new Set(value).size !== value.length) throw new Error(`${label} contains duplicate Source keys`)
  return [...value] as string[]
}

/** Validate one standard View extension value; a Strategy validates again before interpreting it. */
export function validateMemoryViewExtension<K extends MemoryViewExtensionSlot>(slot: K, value: MemoryJsonValue): MemoryViewExtensionValues[K] {
  if (!(MEMORY_VIEW_EXTENSION_SLOTS as readonly string[]).includes(slot)) throw new Error(`unsupported View extension slot: ${String(slot)}`)
  const input = record(value, `View ${slot} contribution`)
  for (const key of Object.keys(input)) if (!FIELDS[slot].includes(key)) throw new Error(`unsupported View ${slot} field: ${key}`)
  let result: MemoryViewExtensionValues[MemoryViewExtensionSlot]
  if (slot === 'selection') {
    const selected = sourceKeys(input.sourceKeys, 'sourceKeys')
    const writable = input.writableSourceKeys === undefined ? undefined : sourceKeys(input.writableSourceKeys, 'writableSourceKeys')
    if (writable?.some(key => !selected.includes(key))) throw new Error('writableSourceKeys must be within selected sourceKeys')
    result = { sourceKeys: selected, ...(writable === undefined ? {} : { writableSourceKeys: writable }) }
  } else if (slot === 'projection') {
    if (input.maxProjectionCharacters === undefined) throw new Error('maxProjectionCharacters is required')
    result = { maxProjectionCharacters: integer(input.maxProjectionCharacters, 4_096, 1, 10_000_000) }
  } else {
    const actionIds = input.actionIds
    if (!Array.isArray(actionIds) || actionIds.length === 0 || actionIds.length > 32
      || actionIds.some(id => typeof id !== 'string' || !ACTION_ID.test(id))
      || new Set(actionIds).size !== actionIds.length) throw new Error('capture actionIds must name 1..32 distinct Source-local recording actions')
    result = { instruction: text(input.instruction, 'capture instruction', 4_000)!, actionIds: [...actionIds] as string[],
      ...(input.sourceKeys === undefined ? {} : { sourceKeys: sourceKeys(input.sourceKeys, 'capture sourceKeys') }) }
  }
  return result as MemoryViewExtensionValues[K]
}

/** Standard values contributed to one composition; each slot has at most one owner. */
export function memoryViewExtensionValues(contributions: readonly MemoryStrategyContribution[]): Partial<MemoryViewExtensionValues> {
  const output: Partial<MemoryViewExtensionValues> = {}
  for (const contribution of contributions) {
    const slot = contribution.slot as MemoryViewExtensionSlot
    if (Object.hasOwn(output, slot)) throw new Error(`duplicate View extension slot: ${slot}`)
    Object.assign(output, { [slot]: validateMemoryViewExtension(slot, contribution.value) })
  }
  return output
}

/** An extension for whichever selected Strategy declares its standard slot. */
export function defineMemoryViewExtension<K extends MemoryViewExtensionSlot>(definition: {
  typeId: string
  packageName: string
  slot: K
  contribute(request: MemoryViewRequest, sources: readonly MemoryAvailableSource[]): MemoryViewExtensionValues[K]
}): MemoryStrategyExtensionDefinition {
  return defineMemoryStrategyExtension({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy-extension', typeId: definition.typeId,
      packageName: definition.packageName, strategyTypeId: ANY_MEMORY_STRATEGY, slot: definition.slot, deterministic: true },
    contribute: (request, sources) => validateMemoryViewExtension(definition.slot, definition.contribute(request, sources)),
  })
}
