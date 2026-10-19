import type { MemoryJsonValue } from '../core/contracts/index.ts'
import type { MemoryPluginSavedPreference, MemoryViewPreferences } from './view-protocol.ts'

/** Explicit profile rows, not the Loader's resolved defaults or effective config. */
export interface LegacySettingsEntryOverride {
  readonly id: string
  readonly disabled?: unknown
  readonly config?: unknown
}

export interface LegacySettingsImportOptions {
  readonly entryId: string
  readonly viewNamespace: string
  readonly legacyNamespace: string
  readonly sections: unknown
  /** The owning Entry's explicit config override, before applying schema defaults. */
  readonly currentOverride: unknown
  readonly entryOverrides: readonly LegacySettingsEntryOverride[]
  /** Settled Loader entries whose public descriptors actually declare a Source. */
  readonly sourceEntryIds: readonly string[]
}

export interface LegacySettingsImportPatch {
  [field: string]: unknown
  memoryView?: MemoryViewPreferences
  legacySettingsImported?: true
}

export interface LegacySettingsImportPlan {
  /** Root config fields only. The caller validates and atomically commits them. */
  patch: LegacySettingsImportPatch
  diagnostics: string[]
}

type JsonObject = Record<string, MemoryJsonValue>
const UNSAFE_KEYS = new Set(['__proto__', 'prototype', 'constructor'])
const ENTRY_ID = /^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,299}$/u
const STRATEGY_ID = /^[a-z][a-z0-9-]{0,127}$/u
const VIEW_NAMESPACE = /^mnemon-view(?:-[a-f0-9]{16})?$/u
const LEGACY_NAMESPACE = /^mnemon-plugins(?:-[a-f0-9]{16})?$/u

function object(value: unknown, label: string): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)
    || ![Object.prototype, null].includes(Object.getPrototypeOf(value))) throw new Error(`${label} must be a plain object`)
  for (const key of Reflect.ownKeys(value)) {
    if (typeof key !== 'string' || UNSAFE_KEYS.has(key)) throw new Error(`${label} contains an unsafe key`)
    const descriptor = Object.getOwnPropertyDescriptor(value, key)!
    if (!descriptor.enumerable || !Object.hasOwn(descriptor, 'value')) throw new Error(`${label} must contain only data properties`)
  }
  return value as Record<string, unknown>
}

/** Copy JSON data without invoking getters, serializers or prototype methods. */
function copy(value: unknown, label: string, ancestors = new Set<object>(), depth = 0): MemoryJsonValue {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value !== 'object' || value === null) throw new Error(`${label} must contain only JSON data`)
  if (depth > 64 || ancestors.has(value)) throw new Error(`${label} contains cyclic or excessively nested data`)
  ancestors.add(value)
  try {
    if (Array.isArray(value)) {
      if (Object.getPrototypeOf(value) !== Array.prototype) throw new Error(`${label} must contain plain arrays`)
      if (Reflect.ownKeys(value).length !== value.length + 1) throw new Error(`${label} contains a sparse or extended array`)
      return Array.from({ length: value.length }, (_, index) => {
        const descriptor = Object.getOwnPropertyDescriptor(value, String(index))
        if (!descriptor || !descriptor.enumerable || !Object.hasOwn(descriptor, 'value')) throw new Error(`${label} must contain only data properties`)
        return copy(descriptor.value, label, ancestors, depth + 1)
      })
    }
    return Object.fromEntries(Object.entries(object(value, label)).map(([key, item]) => [key, copy(item, label, ancestors, depth + 1)]))
  } finally { ancestors.delete(value) }
}

function copiedObject(value: unknown, label: string): JsonObject {
  object(value, label)
  return copy(value, label) as JsonObject
}

/** Inspect validated copies only; never evaluate expressions or invoke getters. */
function containsExpression(value: MemoryJsonValue): boolean {
  return value !== null && typeof value === 'object'
    && (Object.hasOwn(value, '__jsExpr') || Object.values(value).some(containsExpression))
}

function fields(value: JsonObject, allowed: readonly string[], label: string): void {
  if (Object.keys(value).some(key => !allowed.includes(key))) throw new Error(`${label} contains unsupported fields`)
}

function interaction(value: unknown, label: string, allowed: readonly string[]): JsonObject {
  const result = copiedObject(value, label)
  fields(result, allowed, label)
  if (Object.values(result).some(item => typeof item !== 'boolean')) throw new Error(`${label} preferences must be boolean`)
  return result
}

function entryId(value: unknown, label: string): asserts value is string {
  if (typeof value !== 'string' || !ENTRY_ID.test(value) || UNSAFE_KEYS.has(value)) throw new Error(`${label} contains an invalid Entry id`)
}

function view(value: unknown, label: string): Partial<MemoryViewPreferences> {
  const result = copiedObject(value, label)
  fields(result, ['strategyTypeId', 'entries'], label)
  if (Object.hasOwn(result, 'strategyTypeId') && (typeof result.strategyTypeId !== 'string' || !STRATEGY_ID.test(result.strategyTypeId))) {
    throw new Error(`${label} contains an invalid Strategy type id`)
  }
  const entries: Record<string, MemoryPluginSavedPreference> = {}
  if (Object.hasOwn(result, 'entries')) {
    const values = object(result.entries, `${label}.entries`)
    if (Object.keys(values).length > 64) throw new Error(`${label} contains too many Entries`)
    for (const [id, raw] of Object.entries(values)) {
      entryId(id, label)
      const item = copiedObject(raw, `${label}.entries`)
      fields(item, ['enabled', 'config'], `${label}.entries`)
      // Enablement moves into the profile patch once DSH's plugin manager runs.
      if (Object.hasOwn(item, 'enabled') && typeof item.enabled !== 'boolean') throw new Error(`${label} Entry enabled must be boolean`)
      const config = Object.hasOwn(item, 'config') ? copiedObject(item.config, `${label} Entry config`) : {}
      if (JSON.stringify(config).length > 64 * 1024) throw new Error(`${label} Entry config exceeds 64 KiB`)
      entries[id] = { ...(typeof item.enabled === 'boolean' ? { enabled: item.enabled } : {}), config }
    }
  }
  return {
    ...(typeof result.strategyTypeId === 'string' ? { strategyTypeId: result.strategyTypeId } : {}),
    ...(Object.hasOwn(result, 'entries') ? { entries } : {}),
  }
}

function sources(value: unknown, label: string, sourceEntryIds: ReadonlySet<string>, diagnostics: string[]): Record<string, MemoryPluginSavedPreference> {
  const result = copiedObject(value, label)
  fields(result, ['sources'], label)
  const values = Object.hasOwn(result, 'sources') ? object(result.sources, `${label}.sources`) : {}
  if (Object.keys(values).length > 64) throw new Error(`${label} contains too many Source Entries`)
  const entries: Record<string, MemoryPluginSavedPreference> = {}
  for (const [id, raw] of Object.entries(values)) {
    entryId(id, label)
    const item = copiedObject(raw, `${label}.sources`)
    fields(item, ['enabled'], `${label}.sources`)
    if (typeof item.enabled !== 'boolean') throw new Error(`${label} Source enabled must be boolean`)
    if (!sourceEntryIds.has(id)) {
      diagnostics.push(`Skipped legacy Source preference for ${id}: the current profile does not declare it as a Source.`)
      continue
    }
    entries[id] = { enabled: item.enabled, config: {} }
  }
  return entries
}

function applyEntryOverrides(entries: Record<string, MemoryPluginSavedPreference>, overrides: readonly LegacySettingsEntryOverride[], sourceEntryIds: ReadonlySet<string>, diagnostics: string[]): void {
  if (!Array.isArray(overrides)) throw new Error('Explicit Entry overrides must be an array')
  const matching = new Map<string, { disabled?: unknown; config?: unknown }>()
  for (const raw of overrides) {
    const row = object(raw, 'Explicit Entry override')
    entryId(row.id, 'Explicit Entry override')
    if (!Object.hasOwn(entries, row.id)) continue
    const previous = matching.get(row.id) ?? {}
    matching.set(row.id, {
      ...previous,
      ...(row.disabled === undefined ? {} : { disabled: row.disabled }),
      ...(row.config === undefined ? {} : { config: row.config }),
    })
  }
  for (const [id, row] of matching) {
    if (row.disabled !== undefined && typeof row.disabled !== 'boolean') {
      delete entries[id]
      diagnostics.push(`Skipped legacy View preference for ${id}: its explicit disabled value is not boolean.`)
      continue
    }
    // Source configuration stays on its native Entry. The View manager only
    // owns Source activation and must not overwrite those independent fields.
    if (row.config !== undefined && !sourceEntryIds.has(id)) {
      const config = copiedObject(row.config, 'Explicit Entry config')
      if (containsExpression(config)) {
        delete entries[id]
        diagnostics.push(`Skipped legacy View preference for ${id}: its explicit config contains an expression.`)
        continue
      }
      entries[id]!.config = config
    }
    if (typeof row.disabled === 'boolean') entries[id]!.enabled = !row.disabled
  }
}

/**
 * Recover only the canonical root and the exact owning profile's old namespaces.
 * This does not read files, edit profile rows, validate plugin domains or commit.
 * Malformed relevant input rejects the complete plan without an import marker.
 */
export function planLegacySettingsImport(options: LegacySettingsImportOptions): LegacySettingsImportPlan {
  const diagnostics: string[] = []
  try {
    const rawCurrent = object(options.currentOverride, 'Current config override')
    if (rawCurrent.legacySettingsImported === true) return { patch: {}, diagnostics }
    if (options.entryId !== 'mnemon') throw new Error('Legacy settings belong to the canonical mnemon Entry; another root Entry is ambiguous.')
    if (!VIEW_NAMESPACE.test(options.viewNamespace) || !LEGACY_NAMESPACE.test(options.legacyNamespace)
      || options.legacyNamespace !== options.viewNamespace.replace(/^mnemon-view/u, 'mnemon-plugins')) {
      throw new Error('Legacy settings namespaces must identify the same exact profile.')
    }
    const sections = object(options.sections, 'Legacy settings document')
    for (const key of Object.keys(sections)) {
      if (key.startsWith('mnemon-view-') && !VIEW_NAMESPACE.test(key)
        || key.startsWith('mnemon-plugins-') && !LEGACY_NAMESPACE.test(key)) throw new Error('Legacy settings document contains an invalid Mnemon namespace.')
    }
    const relevant = ['mnemon', 'mnemon-ui', options.viewNamespace, options.legacyNamespace]
    if (!relevant.some(key => Object.hasOwn(sections, key))) return { patch: {}, diagnostics }
    for (const key of relevant) {
      if (Object.hasOwn(sections, key) && containsExpression(copiedObject(sections[key], key))) {
        throw new Error(`${key} contains an expression; legacy settings must contain only literal data.`)
      }
    }
    const current = copiedObject(rawCurrent, 'Current config override')
    if (Object.hasOwn(current, 'legacySettingsImported') && typeof current.legacySettingsImported !== 'boolean') throw new Error('Current import marker must be boolean')
    const root = Object.hasOwn(sections, 'mnemon') ? copiedObject(sections.mnemon, 'mnemon') : {}
    if (Object.hasOwn(root, 'memoryView') || Object.hasOwn(root, 'legacySettingsImported')) throw new Error('Legacy mnemon contains reserved migration fields')
    const oldInteraction = Object.hasOwn(root, 'conversationInteraction')
      ? interaction(root.conversationInteraction, 'mnemon.conversationInteraction', ['toolviews', 'turnBar', 'saveAction']) : {}
    const oldUi = Object.hasOwn(sections, 'mnemon-ui') ? interaction(sections['mnemon-ui'], 'mnemon-ui', ['turnBar', 'saveAction']) : {}
    const currentInteraction = Object.hasOwn(current, 'conversationInteraction')
      ? interaction(current.conversationInteraction, 'Current conversationInteraction', ['toolviews', 'turnBar', 'saveAction']) : {}
    const patch: LegacySettingsImportPatch = Object.fromEntries(Object.entries(root).filter(([key]) => key !== 'conversationInteraction' && !Object.hasOwn(current, key)))
    const inheritedInteraction = { ...oldInteraction, ...oldUi }
    if (Object.keys(inheritedInteraction).some(key => !Object.hasOwn(currentInteraction, key))) {
      patch.conversationInteraction = { ...inheritedInteraction, ...currentInteraction }
    }
    const currentView = Object.hasOwn(current, 'memoryView') ? view(current.memoryView, 'Current memoryView') : {}
    if (Object.hasOwn(sections, options.viewNamespace) || Object.hasOwn(sections, options.legacyNamespace)) {
      if (!Array.isArray(options.sourceEntryIds)) throw new Error('Source Entry ids must be an array')
      for (const id of options.sourceEntryIds) entryId(id, 'Source Entry ids')
      const sourceEntryIds = new Set(options.sourceEntryIds)
      const oldSources = Object.hasOwn(sections, options.legacyNamespace)
        ? sources(sections[options.legacyNamespace], options.legacyNamespace, sourceEntryIds, diagnostics) : {}
      const oldView = Object.hasOwn(sections, options.viewNamespace) ? view(sections[options.viewNamespace], options.viewNamespace) : {}
      const entries = { ...oldSources, ...oldView.entries }
      applyEntryOverrides(entries, options.entryOverrides, sourceEntryIds, diagnostics)
      const migratedView: MemoryViewPreferences = { ...oldView, entries, ...currentView }
      if (!Object.hasOwn(currentView, 'entries') || !Object.hasOwn(currentView, 'strategyTypeId') && oldView.strategyTypeId !== undefined) {
        patch.memoryView = migratedView
      }
    }
    patch.legacySettingsImported = true
    return { patch, diagnostics }
  } catch (error) {
    diagnostics.push(error instanceof Error ? error.message : 'Legacy settings import could not be planned')
    return { patch: {}, diagnostics }
  }
}
