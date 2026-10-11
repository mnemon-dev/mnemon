import type { MemoryInstallContribution } from './service.ts'
import type { MemoryJsonValue, MemoryPluginLocalizedText } from '../core/contracts/index.ts'
import { canonicalMemoryJson, deepFreeze } from '../core/definitions.ts'

/** Human-facing metadata only. It is never added to a model View. */
export type MemoryLocalizedText = MemoryPluginLocalizedText

export interface MemoryStrategyConfigurationField {
  key: string
  label: MemoryLocalizedText
  description?: MemoryLocalizedText
  /** Numbers are finite integers; lists have at most 32 unique, nonempty strings. */
  input: 'number' | 'text' | 'textarea' | 'string-list' | 'source-list'
  defaultValue?: MemoryJsonValue
  minimum?: number
  maximum?: number
  sourceRoles?: string[]
}

/**
 * Optional module export named `memoryStrategyConfiguration`.
 * An installed, trusted Cordis Entry owns its fields and its pure factory.
 * The Host may preview that factory, but mounting/disposal still belongs to
 * Cordis. Configuration here must be public; credentials belong to Sources.
 */
export interface MemoryStrategyConfiguration {
  apiVersion: 'dsh-mnemon/strategy-configuration/v1'
  kind: 'strategy' | 'strategy-extension'
  typeId: string
  label: MemoryLocalizedText
  description: MemoryLocalizedText
  fields: readonly MemoryStrategyConfigurationField[]
  /** Same factory used by apply(); must not perform I/O or register a Fiber. */
  create(config: Record<string, MemoryJsonValue>): MemoryInstallContribution
}

export function defineMemoryStrategyConfiguration(value: Omit<MemoryStrategyConfiguration, 'apiVersion'>): MemoryStrategyConfiguration {
  return readMemoryStrategyConfiguration({ ...value, apiVersion: 'dsh-mnemon/strategy-configuration/v1' })
}

/** Host discovery also validates modules that do not use the author helper. */
export function readMemoryStrategyConfiguration(value: MemoryStrategyConfiguration): MemoryStrategyConfiguration {
  const text = (input: unknown) => typeof input === 'object' && input !== null && ['en', 'zh-CN'].every(key => {
    const item = (input as Record<string, unknown>)[key]
    return typeof item === 'string' && item.trim() !== '' && item.length <= 4_000
  })
  if (!value || value.apiVersion !== 'dsh-mnemon/strategy-configuration/v1' || typeof value.create !== 'function'
    || !['strategy', 'strategy-extension'].includes(value.kind) || !/^[a-z][a-z0-9-]{0,127}$/u.test(value.typeId)
    || !Array.isArray(value.fields) || value.fields.length > 16
    || value.fields.some(field => !field || !/^[a-zA-Z][a-zA-Z0-9]{0,99}$/u.test(field.key)
      || !['number', 'text', 'textarea', 'string-list', 'source-list'].includes(field.input)
      || !text(field.label) || field.description !== undefined && !text(field.description)
      || [field.minimum, field.maximum].some(bound => bound !== undefined && (typeof bound !== 'number' || !Number.isFinite(bound)))
      || field.sourceRoles !== undefined && (!Array.isArray(field.sourceRoles) || field.sourceRoles.length > 32
        || field.sourceRoles.some((role: unknown) => typeof role !== 'string' || !role || role.length > 128)))
    || new Set(value.fields.map(field => field.key)).size !== value.fields.length
    || !text(value.label) || !text(value.description)) throw new Error('Invalid plugin configuration descriptor')
  const json = canonicalMemoryJson({ label: value.label, description: value.description, fields: value.fields }, 'Plugin editor metadata')
  if (json.length > 64 * 1024) throw new Error('Plugin editor metadata exceeds 64 KiB')
  const metadata = deepFreeze(JSON.parse(json)) as Pick<MemoryStrategyConfiguration, 'label' | 'description' | 'fields'>
  const factory = value.create
  const definition: MemoryStrategyConfiguration = Object.freeze({
    apiVersion: value.apiVersion, kind: value.kind, typeId: value.typeId, ...metadata,
    create(input: Record<string, MemoryJsonValue>): MemoryInstallContribution {
      const contribution = factory(memoryStrategyConfigurationValues(definition, input))
      if (!contribution || !Array.isArray(contribution.strategies ?? []) || !Array.isArray(contribution.strategyExtensions ?? [])) {
        throw new Error('Plugin factory must return a Strategy contribution')
      }
      const values = [...contribution.strategies ?? [], ...contribution.strategyExtensions ?? []]
      if ((contribution.sources?.length ?? 0) !== 0 || values.length !== 1
        || values[0]?.manifest?.typeId !== definition.typeId || values[0]?.manifest?.kind !== definition.kind
        || contribution.plugin !== undefined && contribution.plugin.packageName !== values[0].manifest.packageName) {
        throw new Error('Plugin factory does not match its declared contribution')
      }
      return contribution
    },
  })
  for (const field of definition.fields) if (field.defaultValue !== undefined) memoryStrategyConfigurationValues(definition, { [field.key]: field.defaultValue })
  return definition
}

/** Validate and copy supplied values without applying defaults or executing code. */
export function memoryStrategyConfigurationValues(definition: MemoryStrategyConfiguration, input: unknown): Record<string, MemoryJsonValue> {
  const value = input ?? {}
  if (typeof value !== 'object' || Array.isArray(value)) throw new Error('Plugin configuration must be an object')
  const json = JSON.stringify(value)
  if (json.length > 64 * 1024) throw new Error('Plugin configuration exceeds 64 KiB')
  const config = JSON.parse(json) as Record<string, MemoryJsonValue>
  const fields = new Map(definition.fields.map(field => [field.key, field]))
  for (const [key, value] of Object.entries(config)) {
    if (['__proto__', 'prototype', 'constructor'].includes(key)) throw new Error('Unsafe plugin configuration key')
    const field = fields.get(key)
    if (!field) throw new Error(`Plugin field is not declared for management: ${key}`)
    if (field.input === 'number') {
      if (typeof value !== 'number' || !Number.isFinite(value) || !Number.isInteger(value)
        || field.minimum !== undefined && value < field.minimum || field.maximum !== undefined && value > field.maximum) throw new Error(`Invalid numeric plugin field: ${key}`)
    } else if (field.input === 'source-list' || field.input === 'string-list') {
      if (!Array.isArray(value) || value.length > 32 || value.some(item => typeof item !== 'string' || !item.trim() || item.length > 500) || new Set(value).size !== value.length) throw new Error(`Invalid plugin list: ${key}`)
    } else if (typeof value !== 'string' || value.length > (field.maximum ?? 4_000)) throw new Error(`Invalid plugin text: ${key}`)
  }
  return config
}
