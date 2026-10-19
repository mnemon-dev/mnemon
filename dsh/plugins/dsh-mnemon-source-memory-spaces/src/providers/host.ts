import type { Context } from '@deepseek-ai/cordis'
import type { MemoryProviderDescriptor } from '../contracts.ts'
import type { MemoryProviderAdapter } from './adapter.ts'
import { MemoryProviderAdapterRegistry } from './registry.ts'
import { defineMemorySpaceProviderDefinition, digest, requiredId, type MemorySpaceProviderDefinition, type MemorySpaceProviderDisposer, type MemorySpaceProviderHost, type MemorySpaceProviderManifest } from './definitions.ts'

const SOURCE_INSTANCE_ID = /^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,299}$/u

export interface MemorySpaceProviderSnapshotEntry {
  childKey: string
  instanceId: string
  configDigest: string
  definition: MemorySpaceProviderDefinition
}

function requiredSourceInstanceId(value: string): string {
  const normalized = value.trim()
  if (!SOURCE_INSTANCE_ID.test(normalized)) {
    throw new Error('Memory Spaces Source instanceId must start with a letter or digit and contain only letters, digits, ., _, :, / or -')
  }
  return normalized
}

/** Presentation labels/icons do not change the semantic Generation digest. */
function semanticManifest(manifest: MemorySpaceProviderManifest): unknown {
  return {
    apiVersion: manifest.apiVersion,
    kind: manifest.kind,
    typeId: manifest.typeId,
    packageName: manifest.packageName,
    version: manifest.version,
    origin: manifest.origin,
    locality: manifest.locality,
    workspaceBinding: manifest.workspaceBinding,
    capabilities: manifest.capabilities,
    fields: manifest.fields.map(field => ({
      key: field.key, scope: field.scope, role: field.role, input: field.input, required: field.required,
      defaultValue: field.defaultValue, min: field.min, max: field.max, maxLength: field.maxLength,
      pattern: field.pattern, normalize: field.normalize,
      discoveryDefaultFrom: field.discoveryDefaultFrom,
      options: field.options?.map(option => option.value),
    })),
    secrets: manifest.secrets,
    scoreSemantics: manifest.scoreSemantics,
  }
}

function assertRuntime(definition: MemorySpaceProviderDefinition, adapter: MemoryProviderAdapter): void {
  const { manifest } = definition
  if (adapter.id !== manifest.typeId) {
    throw new Error(`Memory Space Provider ${manifest.typeId} factory returned ${String(adapter.id)}`)
  }
  const requiredMethods: Array<[boolean, keyof MemoryProviderAdapter]> = [
    [manifest.capabilities.search, 'search'],
    [manifest.capabilities.browse, 'list'],
    [manifest.capabilities.graph, 'graph'],
    [manifest.capabilities.related, 'related'],
    [manifest.capabilities.remember, 'remember'],
    [manifest.capabilities.link, 'link'],
    [manifest.capabilities.forget, 'forget'],
  ]
  for (const [required, method] of requiredMethods) {
    if (required && typeof adapter[method] !== 'function') {
      throw new Error(`Memory Space Provider ${manifest.typeId} declares ${String(method)} but its runtime does not implement it`)
    }
  }
  if (manifest.scoreSemantics === 'normalized-relevance' && adapter.scoreSemantics?.kind !== 'normalized-relevance') {
    throw new Error(`Memory Space Provider ${manifest.typeId} declares normalized scores but its runtime does not`)
  }
}

/** Expose the configured child identity while preserving the driver methods. */
function instanceAdapter(adapter: MemoryProviderAdapter, instanceId: string): MemoryProviderAdapter {
  return new Proxy(adapter, {
    get(target, property) {
      if (property === 'id') return instanceId
      const value = Reflect.get(target, property, target) as unknown
      return typeof value === 'function' ? value.bind(target) : value
    },
  })
}

export class MemorySpaceProviderSnapshot {
  readonly digest: string
  readonly entries: readonly MemorySpaceProviderSnapshotEntry[]

  constructor(entries: readonly MemorySpaceProviderSnapshotEntry[]) {
    const sorted = [...entries].sort((left, right) => left.childKey.localeCompare(right.childKey))
    this.entries = Object.freeze(sorted.map(entry => Object.freeze({ ...entry })))
    this.digest = digest(sorted.map(entry => ({
      childKey: entry.childKey,
      instanceId: entry.instanceId,
      configDigest: entry.configDigest,
      manifest: semanticManifest(entry.definition.manifest),
    })), 'Memory Space Provider snapshot')
    Object.freeze(this)
  }

  descriptors(): MemoryProviderDescriptor[] {
    return this.entries.map(({ instanceId, definition }) => ({
      id: instanceId,
      ...(instanceId === definition.manifest.typeId ? {} : { typeId: definition.manifest.typeId }),
      label: definition.manifest.label,
      ...(definition.manifest.icon === undefined ? {} : { icon: structuredClone(definition.manifest.icon) }),
      kind: definition.manifest.locality,
      workspaceBinding: definition.manifest.workspaceBinding,
      summary: definition.manifest.summary,
      ...(definition.manifest.summaryI18nKey === undefined ? {} : { summaryI18nKey: definition.manifest.summaryI18nKey }),
      origin: definition.manifest.origin,
      capabilities: structuredClone(definition.manifest.capabilities),
      fields: structuredClone(definition.manifest.fields),
    }))
  }

  adapterRegistry(): MemoryProviderAdapterRegistry {
    return new MemoryProviderAdapterRegistry(this.entries.map(({ instanceId, definition }) => ({
      id: instanceId,
      create: context => {
        const adapter = definition.create({
          ...context,
          providerInstanceId: instanceId,
          manifest: definition.manifest,
        })
        assertRuntime(definition, adapter)
        return instanceAdapter(adapter, instanceId)
      },
    })))
  }
}

interface ProviderRegistration {
  childKey: string
  instanceId: string
  configDigest: string
  definition: MemorySpaceProviderDefinition
}

/**
 * Source-private definition host. It is a plain closure-owned object, never a
 * Cordis Context service and never a Mnemon contribution registry.
 */
export class PrivateMemorySpaceProviderHost {
  private readonly registrations = new Map<string, ProviderRegistration>()

  constructor(private readonly sourceInstanceId: string) {
    this.sourceInstanceId = requiredSourceInstanceId(sourceInstanceId)
  }

  bind(instanceIdValue: string, moduleTypeIdValue: string, config: unknown): MemorySpaceProviderHost {
    const instanceId = requiredId(instanceIdValue, 'Memory Space Provider instanceId')
    const moduleTypeId = requiredId(moduleTypeIdValue, 'Memory Space Provider module id')
    const childKey = `${this.sourceInstanceId}/provider:${instanceId}`
    const configDigest = digest(config ?? null, `Memory Space Provider ${instanceId} config`)
    let installed = false
    return Object.freeze({
      install: (owner: Context, definitionValue: MemorySpaceProviderDefinition): MemorySpaceProviderDisposer => {
        if (installed) throw new Error(`Memory Space Provider child already installed a definition: ${childKey}`)
        const definition = defineMemorySpaceProviderDefinition(definitionValue)
        if (definition.manifest.typeId !== moduleTypeId) {
          throw new Error(`Memory Space Provider module ${moduleTypeId} installed definition ${definition.manifest.typeId}`)
        }
        installed = true
        return owner.effect(() => {
          if (this.registrations.has(childKey)) throw new Error(`Memory Space Provider child is already installed: ${childKey}`)
          const registration = Object.freeze({ childKey, instanceId, configDigest, definition })
          this.registrations.set(childKey, registration)
          let active = true
          return () => {
            if (!active) return
            active = false
            if (this.registrations.get(childKey) === registration) this.registrations.delete(childKey)
          }
        }, `dsh-mnemon: install private Provider ${childKey}`)
      },
    })
  }

  has(instanceId: string): boolean {
    return this.registrations.has(`${this.sourceInstanceId}/provider:${instanceId}`)
  }

  snapshot(): MemorySpaceProviderSnapshot {
    return new MemorySpaceProviderSnapshot([...this.registrations.values()])
  }
}
