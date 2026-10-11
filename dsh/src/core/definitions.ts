import type { MemoryPackageProvenance, MemoryPluginDescriptor, MemoryPluginLocalizedText, MemorySourceActionManifest, MemorySourceDefinition, MemorySourceManifest, MemorySourceRouteManifest, MemoryStrategyDefinition, MemoryStrategyExtensionDefinition } from './contracts/index.ts'
import { ANY_MEMORY_SOURCE_ROLE, ANY_MEMORY_STRATEGY, COMPOSABLE_MEMORY_API_VERSION, MEMORY_CAPABILITIES, MEMORY_PLUGIN_API_VERSION, MEMORY_VIEW_EXTENSION_SLOTS } from './contracts/index.ts'

const ID = /^[a-z][a-z0-9-]{0,127}$/u
const PACKAGE = /^(?:@[a-z0-9][a-z0-9._-]*\/)?[a-z0-9][a-z0-9._-]*$/u
const CAPABILITIES = new Set<string>(MEMORY_CAPABILITIES)
const PLUGIN_CAPABILITY = /^[a-z][a-z0-9.-]{0,127}$/u

export function requiredText(value: unknown, label: string, maximum = 500): string {
  if (typeof value !== 'string') throw new Error(`${label} must be a string`)
  const normalized = value.trim()
  if (normalized === '') throw new Error(`${label} is required`)
  if (normalized.length > maximum) throw new Error(`${label} is too long (max ${maximum} characters)`)
  return normalized
}

export function id(value: unknown, label: string): string {
  const normalized = requiredText(value, label, 128)
  if (!ID.test(normalized)) throw new Error(`${label} must match [a-z][a-z0-9-]{0,127}`)
  return normalized
}

/** A role-agnostic Strategy names the wildcard alone, never beside specific roles. */
function sourceRoles(value: readonly string[]): string[] {
  if (value.includes(ANY_MEMORY_SOURCE_ROLE)) {
    if (value.length !== 1) throw new Error('memory Strategy supported Source roles must list the any-role wildcard alone')
    return [ANY_MEMORY_SOURCE_ROLE]
  }
  return uniqueIds(value, 'memory Strategy supported Source role')
}

/** Only standard slots have a value contract every Strategy can share. */
function anyStrategyTarget(slot: unknown): typeof ANY_MEMORY_STRATEGY {
  if (!(MEMORY_VIEW_EXTENSION_SLOTS as readonly unknown[]).includes(slot)) throw new Error(`memory Strategy extension for any Strategy must use a standard slot: ${String(slot)}`)
  return ANY_MEMORY_STRATEGY
}

export function positiveInteger(value: unknown, label: string, maximum = 1_000_000): number {
  if (!Number.isInteger(value) || (value as number) < 1 || (value as number) > maximum) {
    throw new Error(`${label} must be an integer within 1..${maximum}`)
  }
  return value as number
}

/** Canonical JSON representation shared by validation, digest, and replay. */
export function canonicalMemoryJson(value: unknown, label = 'memory value', ancestors = new Set<object>(), depth = 0): string {
  if (depth > 48) throw new Error(`${label} is nested too deeply`)
  if (value === null || typeof value === 'boolean' || typeof value === 'string') return JSON.stringify(value)
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new Error(`${label} contains a non-finite number`)
    return JSON.stringify(value)
  }
  if (typeof value !== 'object') throw new Error(`${label} contains a non-JSON value`)
  if (ancestors.has(value)) throw new Error(`${label} contains a cycle`)
  ancestors.add(value)
  try {
    if (Array.isArray(value)) return `[${value.map(item => canonicalMemoryJson(item, label, ancestors, depth + 1)).join(',')}]`
    const prototype = Object.getPrototypeOf(value)
    if (prototype !== Object.prototype && prototype !== null) throw new Error(`${label} contains a non-JSON object`)
    const entries = Object.entries(value as Record<string, unknown>).sort(([left], [right]) => left.localeCompare(right))
    return `{${entries.map(([key, item]) => `${JSON.stringify(key)}:${canonicalMemoryJson(item, label, ancestors, depth + 1)}`).join(',')}}`
  } finally {
    ancestors.delete(value)
  }
}

export function deepFreeze<T>(value: T): T {
  if (typeof value !== 'object' || value === null || Object.isFrozen(value)) return value
  for (const child of Object.values(value as Record<string, unknown>)) deepFreeze(child)
  return Object.freeze(value)
}

/** Validate the JSON boundary without allocating a discarded canonical string. */
function assertMemoryJson(value: unknown, label: string, ancestors = new Set<object>(), depth = 0): void {
  if (depth > 48) throw new Error(`${label} is nested too deeply`)
  if (value === null || typeof value === 'boolean' || typeof value === 'string') return
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new Error(`${label} contains a non-finite number`)
    return
  }
  if (typeof value !== 'object') throw new Error(`${label} contains a non-JSON value`)
  if (ancestors.has(value)) throw new Error(`${label} contains a cycle`)
  ancestors.add(value)
  try {
    if (Array.isArray(value)) {
      // Do not dispatch through caller-owned forEach/map properties: a hidden
      // override can skip invalid elements and then disappear in structuredClone.
      const length = value.length
      for (let index = 0; index < length; index++) {
        if (index in value) assertMemoryJson(value[index], label, ancestors, depth + 1)
      }
      return
    }
    const prototype = Object.getPrototypeOf(value)
    if (prototype !== Object.prototype && prototype !== null) throw new Error(`${label} contains a non-JSON object`)
    // Accessors can affect later values. Retain canonical visitation order so
    // removing serialization does not change which boundary values are checked.
    const entries = Object.entries(value).sort(([left], [right]) => left.localeCompare(right))
    for (const [, item] of entries) assertMemoryJson(item, label, ancestors, depth + 1)
  } finally {
    ancestors.delete(value)
  }
}

export function jsonClone<T>(value: T, label: string): T {
  assertMemoryJson(value, label)
  return deepFreeze(structuredClone(value))
}

export function uniqueIds(values: readonly string[], label: string): string[] {
  const output: string[] = []
  const seen = new Set<string>()
  for (const value of values) {
    const normalized = id(value, label)
    if (seen.has(normalized)) throw new Error(`${label} is duplicated: ${normalized}`)
    seen.add(normalized)
    output.push(normalized)
  }
  return output
}

export function validateProvenance(value: MemoryPackageProvenance, expectedPackage: string): MemoryPackageProvenance {
  const packageName = requiredText(value.packageName, 'memory package name', 214)
  if (!PACKAGE.test(packageName)) throw new Error(`invalid memory package name: ${packageName}`)
  if (packageName !== expectedPackage) throw new Error(`memory package provenance does not match manifest: ${packageName} != ${expectedPackage}`)
  const entryId = requiredText(value.entryId, 'memory Entry id', 300)
  const artifactDigest = value.artifactDigest === undefined ? undefined : requiredText(value.artifactDigest, 'memory artifact digest', 500)
  return deepFreeze({ packageName, entryId, ...(artifactDigest === undefined ? {} : { artifactDigest }) })
}

export function validateCapabilities(values: readonly string[], label: string): MemorySourceManifest['capabilities'] {
  const normalized = uniqueIds(values, label)
  for (const capability of normalized) {
    if (!CAPABILITIES.has(capability)) throw new Error(`${label} contains unsupported capability: ${capability}`)
  }
  return normalized as MemorySourceManifest['capabilities']
}

function localizedText(value: MemoryPluginLocalizedText, label: string): MemoryPluginLocalizedText {
  return deepFreeze({
    en: requiredText(value.en, `${label} (English)`, 4_000),
    'zh-CN': requiredText(value['zh-CN'], `${label} (Simplified Chinese)`, 4_000),
  })
}

function pluginCapabilities(values: readonly string[], label: string): string[] {
  const output: string[] = []
  const seen = new Set<string>()
  for (const value of values) {
    const normalized = requiredText(value, label, 128)
    if (!PLUGIN_CAPABILITY.test(normalized)) throw new Error(`${label} must match [a-z][a-z0-9.-]{0,127}`)
    if (seen.has(normalized)) throw new Error(`${label} is duplicated: ${normalized}`)
    seen.add(normalized)
    output.push(normalized)
  }
  return output
}

/** Validate a plugin node independently of whether its Fiber is active. */
export function defineMemoryPlugin(value: Omit<MemoryPluginDescriptor, 'apiVersion'> | MemoryPluginDescriptor): MemoryPluginDescriptor {
  if ('apiVersion' in value && value.apiVersion !== MEMORY_PLUGIN_API_VERSION) throw new Error(`unsupported memory plugin API: ${String(value.apiVersion)}`)
  const packageName = requiredText(value.packageName, 'memory plugin packageName', 214)
  if (!PACKAGE.test(packageName)) throw new Error(`invalid memory plugin packageName: ${packageName}`)
  const roles = uniqueIds(value.roles, 'memory plugin role') as MemoryPluginDescriptor['roles']
  if (roles.length === 0 || roles.some(role => !['source', 'strategy', 'strategy-extension'].includes(role))) throw new Error('memory plugin must declare at least one supported contribution role')
  const providedIds = pluginCapabilities(value.provides.map(capability => capability.id), 'memory plugin provided capability')
  const provides = value.provides.map((capability, index) => deepFreeze({ id: providedIds[index]!, ...(capability.exclusive === true ? { exclusive: true as const } : {}) }))
  if (provides.length === 0) throw new Error('memory plugin must provide at least one capability')
  const requires = pluginCapabilities(value.requires ?? [], 'memory plugin required capability')
  const selfProvided = new Set(providedIds)
  for (const requirement of requires) if (selfProvided.has(requirement)) throw new Error(`memory plugin cannot require its own capability: ${requirement}`)
  return deepFreeze({
    apiVersion: MEMORY_PLUGIN_API_VERSION,
    packageName,
    label: localizedText(value.label, 'memory plugin label'),
    description: localizedText(value.description, 'memory plugin description'),
    roles,
    provides,
    ...(requires.length === 0 ? {} : { requires }),
  })
}

function validateRoute(route: MemorySourceRouteManifest): MemorySourceRouteManifest {
  const normalized = {
    id: id(route.id, 'memory Source route id'),
    description: requiredText(route.description, 'memory Source route description', 2_000),
    capability: id(route.capability, 'memory Source route capability') as MemorySourceRouteManifest['capability'],
    inputSchema: jsonClone(route.inputSchema, 'memory Source route input schema'),
    maxCalls: positiveInteger(route.maxCalls, 'memory Source route maxCalls', 100),
    ...(route.maxResults === undefined ? {} : { maxResults: positiveInteger(route.maxResults, 'memory Source route maxResults', 10_000) }),
    ...(route.maxCharacters === undefined ? {} : { maxCharacters: positiveInteger(route.maxCharacters, 'memory Source route maxCharacters', 10_000_000) }),
  }
  if (!CAPABILITIES.has(normalized.capability)) throw new Error(`unsupported memory Source route capability: ${normalized.capability}`)
  return deepFreeze(normalized)
}

function validateAction(action: MemorySourceActionManifest): MemorySourceActionManifest {
  const normalized = {
    id: id(action.id, 'memory Source action id'),
    description: requiredText(action.description, 'memory Source action description', 2_000),
    capability: id(action.capability, 'memory Source action capability') as MemorySourceActionManifest['capability'],
    inputSchema: jsonClone(action.inputSchema, 'memory Source action input schema'),
    ...(action.authority === undefined ? {} : { authority: requiredText(action.authority, 'memory Source action authority', 300) }),
  }
  if (!CAPABILITIES.has(normalized.capability)) throw new Error(`unsupported memory Source action capability: ${normalized.capability}`)
  return deepFreeze(normalized)
}

export function defineMemorySource<T extends MemorySourceDefinition>(definition: T): T {
  const manifest = definition.manifest
  if (manifest.apiVersion !== COMPOSABLE_MEMORY_API_VERSION) throw new Error(`unsupported memory Source API: ${String(manifest.apiVersion)}`)
  if (manifest.kind !== 'source') throw new Error('memory Source manifest kind must be source')
  const typeId = id(manifest.typeId, 'memory Source typeId')
  const packageName = requiredText(manifest.packageName, 'memory Source packageName', 214)
  if (!PACKAGE.test(packageName)) throw new Error(`invalid memory Source packageName: ${packageName}`)
  const role = id(manifest.role, 'memory Source role')
  if (manifest.consistency !== 'exact-snapshot' && manifest.consistency !== 'namespace-pinned-live-read') {
    throw new Error(`unsupported memory Source consistency: ${String(manifest.consistency)}`)
  }
  if (typeof definition.create !== 'function') throw new Error(`memory Source create() is required: ${typeId}`)
  const routes = (manifest.routes ?? []).map(validateRoute)
  uniqueIds(routes.map(route => route.id), 'memory Source route id')
  const actions = (manifest.actions ?? []).map(validateAction)
  uniqueIds(actions.map(action => action.id), 'memory Source action id')
  const normalizedManifest = jsonClone({
    ...manifest,
    typeId,
    packageName,
    role,
    capabilities: validateCapabilities(manifest.capabilities, 'memory Source capability'),
    routes,
    actions,
  }, 'memory Source manifest')
  return Object.freeze({ manifest: normalizedManifest, create: definition.create }) as T
}

export function defineMemoryStrategy<T extends MemoryStrategyDefinition>(definition: T): T {
  const manifest = definition.manifest
  if (manifest.apiVersion !== COMPOSABLE_MEMORY_API_VERSION) throw new Error(`unsupported memory Strategy API: ${String(manifest.apiVersion)}`)
  if (manifest.kind !== 'strategy') throw new Error('memory Strategy manifest kind must be strategy')
  const typeId = id(manifest.typeId, 'memory Strategy typeId')
  const packageName = requiredText(manifest.packageName, 'memory Strategy packageName', 214)
  if (!PACKAGE.test(packageName)) throw new Error(`invalid memory Strategy packageName: ${packageName}`)
  if (manifest.deterministic !== true) throw new Error('memory Strategy must declare deterministic: true')
  if (typeof definition.compose !== 'function') throw new Error(`memory Strategy compose() is required: ${typeId}`)
  if (definition.createTurn !== undefined && typeof definition.createTurn !== 'function') throw new Error('memory Strategy createTurn must be a function')
  const normalizedManifest = jsonClone({
    ...manifest,
    typeId,
    packageName,
    supportedSourceRoles: sourceRoles(manifest.supportedSourceRoles),
    maxSources: positiveInteger(manifest.maxSources, 'memory Strategy maxSources', 1_000),
    maxRoutes: positiveInteger(manifest.maxRoutes, 'memory Strategy maxRoutes', 1_000),
    maxActions: positiveInteger(manifest.maxActions, 'memory Strategy maxActions', 1_000),
    ...(manifest.extensionSlots === undefined ? {} : { extensionSlots: uniqueIds(manifest.extensionSlots, 'memory Strategy extension slot') }),
  }, 'memory Strategy manifest')
  return Object.freeze({ manifest: normalizedManifest, compose: definition.compose,
    ...(definition.createTurn === undefined ? {} : { createTurn: definition.createTurn }),
  }) as T
}

export function defineMemoryStrategyExtension<T extends MemoryStrategyExtensionDefinition>(definition: T): T {
  const manifest = definition.manifest
  if (manifest.apiVersion !== COMPOSABLE_MEMORY_API_VERSION) throw new Error(`unsupported memory Strategy extension API: ${String(manifest.apiVersion)}`)
  if (manifest.kind !== 'strategy-extension') throw new Error('memory Strategy extension kind must be strategy-extension')
  const packageName = requiredText(manifest.packageName, 'memory Strategy extension packageName', 214)
  if (!PACKAGE.test(packageName)) throw new Error(`invalid memory Strategy extension packageName: ${packageName}`)
  if (manifest.deterministic !== true) throw new Error('memory Strategy extension must declare deterministic: true')
  if (typeof definition.contribute !== 'function') throw new Error('memory Strategy extension contribute() is required')
  return Object.freeze({
    manifest: jsonClone({
      ...manifest, packageName,
      typeId: id(manifest.typeId, 'memory Strategy extension typeId'),
      strategyTypeId: manifest.strategyTypeId === ANY_MEMORY_STRATEGY ? anyStrategyTarget(manifest.slot) : id(manifest.strategyTypeId, 'memory Strategy extension target'),
      slot: id(manifest.slot, 'memory Strategy extension slot'),
    }, 'memory Strategy extension manifest'),
    contribute: definition.contribute,
  }) as T
}
