import type { MemoryProviderConfigField, MemoryProviderConnection, MemoryProviderDescriptor, MemoryProviderId } from '../contracts.ts'

export function memoryProviderDescriptor(id: MemoryProviderId, catalog: readonly MemoryProviderDescriptor[] = []): MemoryProviderDescriptor {
  const descriptor = catalog.find(candidate => candidate.id === id)
  if (descriptor === undefined) throw new Error(`unsupported memory provider: ${String(id)}`)
  return descriptor
}

export function isMemoryProviderId(value: unknown): value is MemoryProviderId {
  return typeof value === 'string' && /^[a-z][a-z0-9-]{0,127}$/u.test(value)
}

function normalizeUrl(value: string, label: string): string {
  const normalized = value.trim().replace(/\/+$/u, '')
  let url: URL
  try { url = new URL(normalized) } catch { throw new Error(`${label} must be a valid http(s) URL`) }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') throw new Error(`${label} must use http or https`)
  if (url.username !== '' || url.password !== '') throw new Error(`${label} must not contain credentials`)
  return normalized
}

function normalizeString(value: unknown, field: MemoryProviderConfigField): string {
  let normalized = typeof value === 'string' ? value.trim() : value === undefined || value === null ? '' : String(value).trim()
  if (field.normalize === 'trim-trailing-slash') normalized = normalized.replace(/\/+$/u, '')
  const maximum = field.maxLength ?? (field.input === 'secret' ? 8000 : 2000)
  if (normalized.length > maximum) throw new Error(`${field.label} is too long (max ${maximum} characters)`)
  if (field.required && normalized === '') throw new Error(`${field.label} is required`)
  if (field.input === 'url' && normalized !== '') return normalizeUrl(normalized, field.label)
  if (field.options !== undefined && normalized !== '' && !field.options.some(option => option.value === normalized)) {
    throw new Error(`${field.label} has an unsupported value`)
  }
  if (field.pattern !== undefined && normalized !== '' && !new RegExp(field.pattern, 'u').test(normalized)) {
    throw new Error(field.validationMessage ?? `${field.label.charAt(0).toLowerCase()}${field.label.slice(1)} has an invalid format`)
  }
  return normalized
}

function validateFieldBounds(field: MemoryProviderConfigField, value: number): void {
  if (field.min !== undefined && field.max !== undefined && (value < field.min || value > field.max)) {
    throw new Error(`${field.label.charAt(0).toLowerCase()}${field.label.slice(1)} must be within ${field.min}..${field.max}`)
  }
  if (field.min !== undefined && value < field.min) throw new Error(`${field.label} must be at least ${field.min}`)
  if (field.max !== undefined && value > field.max) throw new Error(`${field.label} must be at most ${field.max}`)
}

function normalizeScopedProviderConnection(
  providerId: MemoryProviderId,
  scope: MemoryProviderConfigField['scope'],
  input: MemoryProviderConnection | undefined,
  previous: MemoryProviderConnection = {},
  clearSecrets: readonly string[] = [],
  catalog: readonly MemoryProviderDescriptor[] = [],
): MemoryProviderConnection {
  const descriptor = memoryProviderDescriptor(providerId, catalog)
  if ((descriptor.typeId ?? descriptor.id) === 'mnemon-native') return {}
  const fields = descriptor.fields.filter(item => item.scope === scope)
  const allowed = new Set(fields.map(item => item.key))
  for (const key of Object.keys(input ?? {})) if (!allowed.has(key)) throw new Error(`unsupported ${descriptor.label} ${scope} setting: ${key}`)
  for (const key of clearSecrets) {
    const configField = fields.find(item => item.key === key)
    if (configField?.input !== 'secret') throw new Error(`cannot clear non-secret ${descriptor.label} ${scope} setting: ${key}`)
  }
  const output: MemoryProviderConnection = {}
  for (const configField of fields) {
    if (clearSecrets.includes(configField.key)) {
      output[configField.key] = ''
      continue
    }
    const supplied = input?.[configField.key]
    const value = supplied ?? previous[configField.key] ?? configField.defaultValue
    if (configField.input === 'boolean') {
      if (value === undefined) continue
      if (typeof value === 'boolean') output[configField.key] = value
      else if (value === 'true' || value === 'false') output[configField.key] = value === 'true'
      else throw new Error(`${configField.label} must be true or false`)
      continue
    }
    if (configField.input === 'number') {
      if (value === undefined || value === '') continue
      const parsed = typeof value === 'number' ? value : Number(value)
      if (!Number.isFinite(parsed)) throw new Error(`${configField.label} must be a finite number`)
      validateFieldBounds(configField, parsed)
      output[configField.key] = parsed
      continue
    }
    const normalized = normalizeString(value, configField)
    if (normalized !== '' || configField.required || configField.input === 'secret') output[configField.key] = normalized
  }
  return output
}

export function providerServiceFields(providerId: MemoryProviderId, catalog: readonly MemoryProviderDescriptor[] = []): MemoryProviderConfigField[] {
  return memoryProviderDescriptor(providerId, catalog).fields.filter(field => field.scope === 'service')
}

export function providerMemoryFields(providerId: MemoryProviderId, catalog: readonly MemoryProviderDescriptor[] = []): MemoryProviderConfigField[] {
  return memoryProviderDescriptor(providerId, catalog).fields.filter(field => field.scope === 'memory')
}

export function splitProviderConnection(providerId: MemoryProviderId, connection: MemoryProviderConnection | undefined, catalog: readonly MemoryProviderDescriptor[] = []): {
  service: MemoryProviderConnection
  memory: MemoryProviderConnection
} {
  const serviceKeys = new Set(providerServiceFields(providerId, catalog).map(field => field.key))
  return {
    service: Object.fromEntries(Object.entries(connection ?? {}).filter(([key]) => serviceKeys.has(key))),
    memory: Object.fromEntries(Object.entries(connection ?? {}).filter(([key]) => !serviceKeys.has(key))),
  }
}

export function normalizeProviderServiceConnection(providerId: MemoryProviderId, input: MemoryProviderConnection | undefined, previous: MemoryProviderConnection = {}, clearSecrets: readonly string[] = [], catalog: readonly MemoryProviderDescriptor[] = []): MemoryProviderConnection {
  return normalizeScopedProviderConnection(providerId, 'service', input, previous, clearSecrets, catalog)
}

export function normalizeProviderMemoryConnection(providerId: MemoryProviderId, input: MemoryProviderConnection | undefined, previous: MemoryProviderConnection = {}, catalog: readonly MemoryProviderDescriptor[] = []): MemoryProviderConnection {
  return normalizeScopedProviderConnection(providerId, 'memory', input, previous, [], catalog)
}

export function normalizeProviderConnection(
  providerId: MemoryProviderId,
  input: MemoryProviderConnection | undefined,
  previous: MemoryProviderConnection = {},
  clearSecrets: readonly string[] = [],
  catalog: readonly MemoryProviderDescriptor[] = [],
): MemoryProviderConnection {
  const descriptor = memoryProviderDescriptor(providerId, catalog)
  if ((descriptor.typeId ?? descriptor.id) === 'mnemon-native') return {}
  const allowed = new Set(descriptor.fields.map(item => item.key))
  for (const key of Object.keys(input ?? {})) if (!allowed.has(key)) throw new Error(`unsupported ${descriptor.label} setting: ${key}`)
  for (const key of clearSecrets) {
    const configField = descriptor.fields.find(item => item.key === key)
    if (configField?.input !== 'secret') throw new Error(`cannot clear non-secret ${descriptor.label} setting: ${key}`)
  }

  const output: MemoryProviderConnection = {}
  for (const configField of descriptor.fields) {
    if (clearSecrets.includes(configField.key)) {
      output[configField.key] = ''
      continue
    }
    const supplied = input?.[configField.key]
    const fallback = previous[configField.key] ?? configField.defaultValue
    const value = supplied ?? fallback
    if (configField.input === 'boolean') {
      if (value === undefined) continue
      if (typeof value === 'boolean') output[configField.key] = value
      else if (value === 'true' || value === 'false') output[configField.key] = value === 'true'
      else throw new Error(`${configField.label} must be true or false`)
      continue
    }
    if (configField.input === 'number') {
      if (value === undefined || value === '') continue
      const parsed = typeof value === 'number' ? value : Number(value)
      if (!Number.isFinite(parsed)) throw new Error(`${configField.label} must be a finite number`)
      validateFieldBounds(configField, parsed)
      output[configField.key] = parsed
      continue
    }
    const normalized = normalizeString(value, configField)
    if (normalized !== '' || configField.required || configField.input === 'secret') output[configField.key] = normalized
  }
  return output
}

export function publicScopedProviderConnection(providerId: MemoryProviderId, scope: MemoryProviderConfigField['scope'], connection: MemoryProviderConnection, catalog: readonly MemoryProviderDescriptor[] = []): {
  settings: MemoryProviderConnection
  configuredSecrets: string[]
} {
  const descriptor = memoryProviderDescriptor(providerId, catalog)
  const fields = descriptor.fields.filter(item => item.scope === scope)
  const keys = new Set(fields.map(item => item.key))
  const secrets = new Set(fields.filter(item => item.input === 'secret').map(item => item.key))
  return {
    settings: Object.fromEntries(Object.entries(connection).filter(([key]) => keys.has(key) && !secrets.has(key))),
    configuredSecrets: [...secrets].filter(key => String(connection[key] ?? '') !== ''),
  }
}

export function publicProviderConnection(providerId: MemoryProviderId, connection: MemoryProviderConnection, catalog: readonly MemoryProviderDescriptor[] = []): {
  settings: MemoryProviderConnection
  configuredSecrets: string[]
} {
  const descriptor = memoryProviderDescriptor(providerId, catalog)
  const secrets = new Set(descriptor.fields.filter(item => item.input === 'secret').map(item => item.key))
  return {
    settings: Object.fromEntries(Object.entries(connection).filter(([key]) => !secrets.has(key))),
    configuredSecrets: [...secrets].filter(key => String(connection[key] ?? '') !== ''),
  }
}

/** Immutable descriptor/config normalizer scoped to one Provider snapshot. */
export class MemoryProviderCatalog {
  readonly providers: readonly MemoryProviderDescriptor[]

  constructor(providers: readonly MemoryProviderDescriptor[]) {
    const ids = new Set<string>()
    this.providers = Object.freeze(providers.map(provider => {
      if (ids.has(provider.id)) throw new Error(`memory provider descriptor is already registered: ${provider.id}`)
      ids.add(provider.id)
      return Object.freeze(structuredClone(provider))
    }))
  }

  has(id: unknown): id is MemoryProviderId {
    return typeof id === 'string' && this.providers.some(provider => provider.id === id)
  }

  descriptor(id: MemoryProviderId): MemoryProviderDescriptor { return memoryProviderDescriptor(id, this.providers) }
  serviceFields(id: MemoryProviderId): MemoryProviderConfigField[] { return providerServiceFields(id, this.providers) }
  memoryFields(id: MemoryProviderId): MemoryProviderConfigField[] { return providerMemoryFields(id, this.providers) }
  split(id: MemoryProviderId, connection: MemoryProviderConnection | undefined) { return splitProviderConnection(id, connection, this.providers) }
  normalizeService(id: MemoryProviderId, input: MemoryProviderConnection | undefined, previous: MemoryProviderConnection = {}, clearSecrets: readonly string[] = []) {
    return normalizeProviderServiceConnection(id, input, previous, clearSecrets, this.providers)
  }
  normalizeMemory(id: MemoryProviderId, input: MemoryProviderConnection | undefined, previous: MemoryProviderConnection = {}) {
    return normalizeProviderMemoryConnection(id, input, previous, this.providers)
  }
  normalize(id: MemoryProviderId, input: MemoryProviderConnection | undefined, previous: MemoryProviderConnection = {}, clearSecrets: readonly string[] = []) {
    return normalizeProviderConnection(id, input, previous, clearSecrets, this.providers)
  }
  publicScoped(id: MemoryProviderId, scope: MemoryProviderConfigField['scope'], connection: MemoryProviderConnection) {
    return publicScopedProviderConnection(id, scope, connection, this.providers)
  }
  public(id: MemoryProviderId, connection: MemoryProviderConnection) { return publicProviderConnection(id, connection, this.providers) }
}

export const EMPTY_MEMORY_PROVIDER_CATALOG = new MemoryProviderCatalog([])
