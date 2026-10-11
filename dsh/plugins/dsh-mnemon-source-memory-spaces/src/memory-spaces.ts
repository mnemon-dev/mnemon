import { createHash, randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, readdirSync, renameSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { basename, dirname, join } from 'node:path'
import type { MnemonRunner, MnemonTextCommand } from './runner.ts'
import {
  EMPTY_MEMORY_PROVIDER_CATALOG,
  type MemoryProviderCatalog,
} from './providers/catalog.ts'
import type {
  CreateMemorySpaceRequest,
  MemorySpace,
  MemorySpaceProvider,
  MemoryPlacementCandidate,
  MemoryPlacementDecision,
  MemoryProviderServiceCatalog,
  MemoryProviderServiceView,
  MemoryProviderConnection,
  MemoryProviderId,
  MemorySpaceMetadataUpdate,
  OpenVikingSpaceConnection,
  UpdateMemorySpaceRequest,
} from './contracts.ts'
import type { ProviderMemorySpace } from './providers/adapter.ts'

const NATIVE_REGISTRY_VERSION = 1
const PROVIDER_REGISTRY_VERSION = 4
const ID_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_-]*$/
const PROVIDER_ID_PATTERN = /^[a-z][a-z0-9-]{0,127}$/u

function storedProviderId(value: unknown): value is MemoryProviderId {
  return typeof value === 'string' && PROVIDER_ID_PATTERN.test(value)
}

function storedProviderConnection(value: unknown): MemoryProviderConnection {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return {}
  return Object.fromEntries(Object.entries(value as Record<string, unknown>)
    .filter((entry): entry is [string, string | number | boolean] => {
      const [key, item] = entry
      return /^[a-z][a-zA-Z0-9_-]{0,127}$/u.test(key)
        && (typeof item === 'boolean' || typeof item === 'number' && Number.isFinite(item) || typeof item === 'string' && item.length <= 8_000)
    }))
}

interface StoredOpenVikingConnection {
  endpoint: string
  targetUri: string
  apiKey: string
  account: string
  user: string
  actorPeerId: string
}

interface StoredMemorySpace extends Omit<MemorySpace, 'dbPath' | 'provider'> {
  providerId: MemoryProviderId
  /** Stable provider-side namespace used to refresh this local projection. */
  externalId?: string
  /** Controls whether discovery may refresh presentation metadata. */
  metadataSource?: 'provider' | 'manual' | 'ai'
  connection?: MemoryProviderConnection
  /** Provider-registry v1 compatibility; migrated to connection on load. */
  openViking?: StoredOpenVikingConnection
}

interface StoredNativeMemorySpace extends Omit<StoredMemorySpace, 'providerId' | 'connection' | 'openViking'> {}

interface NativeRegistryFile {
  version: 1
  bodies: StoredNativeMemorySpace[]
}

interface LegacyProviderRegistryFile {
  version: 2
  bodies: StoredMemorySpace[]
}

interface LegacyProviderRegistryFileOnDisk {
  version: 1 | 2
  bodies: StoredMemorySpace[]
}

interface ProviderRegistryFile {
  version: 4
  services: Partial<Record<MemoryProviderId, MemoryProviderConnection>>
  enabled?: Partial<Record<MemoryProviderId, boolean>>
  bodies: StoredMemorySpace[]
}

interface LegacyProviderRegistryFileV3 {
  version: 3
  services: Partial<Record<MemoryProviderId, MemoryProviderConnection>>
  enabled?: Partial<Record<MemoryProviderId, boolean>>
  bodies: StoredMemorySpace[]
}

function requiredText(value: string, label: string, max: number): string {
  const normalized = value.trim()
  if (normalized === '') throw new Error(`${label} is required`)
  if (normalized.length > max) throw new Error(`${label} is too long (max ${max} characters)`)
  return normalized
}

function optionalText(value: string | undefined, label: string, max: number): string {
  const normalized = value?.trim() ?? ''
  if (normalized.length > max) throw new Error(`${label} is too long (max ${max} characters)`)
  return normalized
}

const PROVIDER_METADATA_KEYS = [
  'name', 'title', 'displayName', 'workspace', 'bankId', 'project', 'containerTag',
  'userId', 'user', 'workingDirectory', 'targetUri',
] as const

function compactProviderMetadataValue(value: unknown): string | undefined {
  if (typeof value !== 'string') return undefined
  const normalized = value.trim()
  if (normalized === '' || normalized === '*') return undefined
  const pathTail = normalized.split(/[/:\\]+/u).filter(Boolean).at(-1)
  return (pathTail ?? normalized).trim() || undefined
}

/**
 * Normalize uneven provider discovery metadata at the projection boundary.
 * Adapters map the richest native fields they know; the registry then tries
 * the nearest namespace setting before falling back to a stable provider
 * identity. This keeps every discovered namespace usable without teaching the
 * Web UI each provider's response shape.
 */
function providerProjectionMetadata(providerCatalog: MemoryProviderCatalog, providerId: MemoryProviderId, candidate: ProviderMemorySpace): { name: string; description: string } {
  const descriptor = providerCatalog.descriptor(providerId)
  const externalId = requiredText(candidate.externalId, 'provider externalId', 2000)
  const mappedName = String(candidate.name ?? '').trim()
  const nearestName = PROVIDER_METADATA_KEYS
    .map(key => compactProviderMetadataValue(candidate.connection[key]))
    .find((value): value is string => value !== undefined)
  const fallbackId = compactProviderMetadataValue(externalId) ?? externalId
  const name = (mappedName || nearestName || `${descriptor.label} ${fallbackId}`).slice(0, 100)
  const mappedDescription = String(candidate.description ?? '').trim()
  const description = (mappedDescription || `${descriptor.label} memory namespace mapped from ${externalId}.`).slice(0, 1000)
  return {
    name: requiredText(name, 'name', 100),
    description: optionalText(description, 'description', 1000),
  }
}

function providerDisplayLocation(
  descriptor: ReturnType<MemoryProviderCatalog['descriptor']>,
  connection: MemoryProviderConnection,
): string {
  const populated = (field: (typeof descriptor.fields)[number]): boolean =>
    field.input !== 'secret' && String(connection[field.key] ?? '').trim() !== ''
  const field = descriptor.fields.find(candidate => candidate.role === 'global-location' && populated(candidate))
    ?? descriptor.fields.find(candidate => (candidate.input === 'url' || candidate.input === 'path') && populated(candidate))
    ?? descriptor.fields.find(populated)
  return field === undefined ? '' : String(connection[field.key])
}

function legacyOpenVikingConnection(connection: StoredOpenVikingConnection | OpenVikingSpaceConnection): MemoryProviderConnection {
  return Object.fromEntries(Object.entries(connection).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
}

function normalizePlacementDecision(value: unknown, providerId: MemoryProviderId, providerCatalog: MemoryProviderCatalog = EMPTY_MEMORY_PROVIDER_CATALOG): MemoryPlacementDecision | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined
  const placement = value as Partial<MemoryPlacementDecision>
  if (placement.mode !== 'automatic' || placement.providerId !== providerId) return undefined
  if (placement.decidedBy !== 'rules' && placement.decidedBy !== 'llm') return undefined
  if (placement.confidence !== 'high' && placement.confidence !== 'medium' && placement.confidence !== 'low') return undefined
  if (typeof placement.reason !== 'string' || placement.reason.trim() === '' || placement.reason.length > 1000) return undefined
  if (!Array.isArray(placement.candidateProviderIds) || !placement.candidateProviderIds.every(id => providerCatalog.has(id)) || !placement.candidateProviderIds.includes(providerId)) return undefined
  if (!Array.isArray(placement.appliedRules) || !placement.appliedRules.every(rule => typeof rule === 'string' && rule.length <= 500)) return undefined
  if (typeof placement.decidedAt !== 'string' || placement.decidedAt.trim() === '') return undefined
  if (placement.runId !== undefined && typeof placement.runId !== 'string') return undefined
  if (placement.subagentProvider !== undefined && typeof placement.subagentProvider !== 'string') return undefined
  return {
    mode: 'automatic',
    providerId,
    decidedBy: placement.decidedBy,
    reason: placement.reason.trim(),
    confidence: placement.confidence,
    candidateProviderIds: [...new Set(placement.candidateProviderIds)],
    appliedRules: [...placement.appliedRules],
    decidedAt: placement.decidedAt,
    ...(placement.runId === undefined ? {} : { runId: placement.runId }),
    ...(placement.subagentProvider === undefined ? {} : { subagentProvider: placement.subagentProvider }),
  }
}

export function validateMemorySpaceId(value: string): string {
  const normalized = value.trim()
  if (!ID_PATTERN.test(normalized)) throw new Error('memoryBodyId must match [a-zA-Z0-9][a-zA-Z0-9_-]*')
  return normalized
}

/**
 * Persistent metadata layered over Mnemon's native named stores.
 *
 * Native metadata lives beside Store directories so existing Mnemon Packs stay
 * compatible. External connection metadata lives under state and is never
 * included in Memory Space Packs.
 */
export class MemorySpaceRegistry {
  readonly directory: string
  readonly registryPath: string
  readonly providerRegistryPath: string
  private refreshing = false
  private readonly state: {
    spaces: StoredMemorySpace[]
    services: Partial<Record<MemoryProviderId, MemoryProviderConnection>>
    serviceEnabled: Partial<Record<MemoryProviderId, boolean>>
    diskRevision?: string
  }

  constructor(
    readonly runner: MnemonRunner,
    // Provider service configuration and third-party Memory Space projections
    // are DSH-owned state. Their durability must not depend on whether the
    // optional native Mnemon CLI happens to be installed.
    private readonly persistent = true,
    private readonly now: () => Date = () => new Date(),
    private readonly providerCatalog: MemoryProviderCatalog = EMPTY_MEMORY_PROVIDER_CATALOG,
    sharedState?: MemorySpaceRegistry['state'],
  ) {
    this.state = sharedState ?? { spaces: [], services: {}, serviceEnabled: {} }
    this.directory = join(runner.effectiveDataDir(), 'data')
    // Keep the published v1 filename and serialized bodies key for existing stores and Packs.
    this.registryPath = join(this.directory, '.dsh-memory-bodies.json')
    this.providerRegistryPath = join(runner.effectiveDataDir(), 'state', 'memory-providers.json')
    if (sharedState === undefined) this.reload()
  }

  private get spaces(): StoredMemorySpace[] { return this.state.spaces }
  private set spaces(value: StoredMemorySpace[]) { this.state.spaces = value }
  private get services(): Partial<Record<MemoryProviderId, MemoryProviderConnection>> { return this.state.services }
  private set services(value: Partial<Record<MemoryProviderId, MemoryProviderConnection>>) { this.state.services = value }
  private get serviceEnabled(): Partial<Record<MemoryProviderId, boolean>> { return this.state.serviceEnabled }
  private set serviceEnabled(value: Partial<Record<MemoryProviderId, boolean>>) { this.state.serviceEnabled = value }

  /** One generation-local descriptor view over the same persistent authority. */
  withProviderCatalog(providerCatalog: MemoryProviderCatalog): MemorySpaceRegistry {
    return new MemorySpaceRegistry(this.runner, this.persistent, this.now, providerCatalog, this.state)
  }

  private isNative(providerId: MemoryProviderId): boolean {
    return this.providerTypeId(providerId) === 'mnemon-native'
  }

  private providerTypeId(providerId: MemoryProviderId): string {
    const descriptor = this.providerCatalog.descriptor(providerId)
    return descriptor.typeId ?? descriptor.id
  }

  list(): MemorySpace[] {
    this.refreshIfChanged()
    this.reconcileDiscoveredStores()
    return this.spaces.filter(body => this.providerCatalog.has(body.providerId)).map(body => this.view(body))
  }

  active(): MemorySpace[] {
    // list() has just refreshed this synchronous authority snapshot. Avoid
    // restatting both registry files once more for every projected body.
    return this.list().filter(body => body.active && (this.isNative(body.provider.id)
      || Object.hasOwn(this.services, body.provider.id) && this.serviceEnabled[body.provider.id] === true))
  }

  get(id: string): MemorySpace {
    const normalized = validateMemorySpaceId(id)
    const body = this.list().find(entry => entry.id === normalized)
    if (body === undefined) throw new Error(`unknown memory space: ${normalized}`)
    return body
  }

  providerConnection(id: string, expectedProviderId?: MemoryProviderId): MemoryProviderConnection {
    this.refreshIfChanged()
    const normalized = validateMemorySpaceId(id)
    const body = this.spaces.find(entry => entry.id === normalized)
    if (body === undefined || this.isNative(body.providerId)) throw new Error(`memory space has no external provider connection: ${normalized}`)
    if (expectedProviderId !== undefined && body.providerId !== expectedProviderId) {
      throw new Error(`memory space ${normalized} uses ${body.providerId}, not ${expectedProviderId}`)
    }
    return this.connectionFor(body)
  }

  /** Used only after a public operation has refreshed the registry authority. */
  private connectionFor(body: StoredMemorySpace): MemoryProviderConnection {
    const legacy = this.providerTypeId(body.providerId) === 'openviking' && body.openViking !== undefined
      ? legacyOpenVikingConnection(body.openViking)
      : undefined
    return this.providerCatalog.normalize(body.providerId, {
      ...(this.services[body.providerId] ?? {}),
      ...(legacy ?? {}),
      ...(body.connection ?? {}),
    })
  }

  providerServiceConfigured(providerId: MemoryProviderId): boolean {
    this.refreshIfChanged()
    return this.isNative(providerId) ? this.runner.commandFound : Object.hasOwn(this.services, providerId)
  }

  providerServiceEnabled(providerId: MemoryProviderId): boolean {
    return this.isNative(providerId)
      ? this.runner.commandFound
      : this.providerServiceConfigured(providerId) && this.serviceEnabled[providerId] === true
  }

  /**
   * The provider used when none is named: Mnemon Native while its CLI is
   * available, which keeps the long-standing default, otherwise the first
   * other provider, in catalog order, that can take writes now.
   */
  defaultProviderId(): MemoryProviderId | undefined {
    const ready = this.providerCatalog.providers.filter(provider => this.providerServiceEnabled(provider.id))
    return (ready.find(provider => this.isNative(provider.id)) ?? ready[0])?.id
  }

  providerServices(options: { includeSecrets?: boolean } = {}): MemoryProviderServiceCatalog {
    this.refreshIfChanged()
    const providers = this.providerCatalog.providers.filter(provider => (provider.typeId ?? provider.id) !== 'mnemon-native')
    const items: MemoryProviderServiceView[] = providers.map(provider => {
      const connection = this.services[provider.id]
      const publicConnection = this.providerCatalog.publicScoped(provider.id, 'service', connection ?? {})
      return {
        providerId: provider.id,
        enabled: Object.hasOwn(this.services, provider.id) && this.serviceEnabled[provider.id] === true,
        configured: connection !== undefined,
        ...publicConnection,
        ...(options.includeSecrets === true && connection !== undefined
          ? { secretValues: Object.fromEntries(publicConnection.configuredSecrets.map(key => [key, connection[key]!])) }
          : {}),
      }
    })
    return { providers: [...providers], items, generatedAt: this.now().toISOString() }
  }

  updateProviderService(providerId: MemoryProviderId, settings: MemoryProviderConnection, clearSecrets: readonly string[] = [], enabled = true): MemoryProviderServiceView {
    this.refreshIfChanged()
    if (this.isNative(providerId)) throw new Error('Mnemon Native service settings are managed by the native configuration')
    const previous = this.services[providerId] ?? {}
    this.services[providerId] = this.providerCatalog.normalizeService(providerId, settings, previous, clearSecrets)
    this.serviceEnabled[providerId] = enabled
    // Third-party Memory Spaces are local projections of provider-owned
    // namespaces. Once the provider is disconnected those projections are no
    // longer addressable and must not linger as unhealthy, uneditable cards.
    // Keep only the reusable service configuration so reconnecting can
    // discover and rebuild the projections from the source of truth.
    if (!enabled) this.spaces = this.spaces.filter(body => body.providerId !== providerId)
    this.save()
    return this.providerServices().items.find(item => item.providerId === providerId)!
  }

  resolveProviderService(providerId: MemoryProviderId, settings: MemoryProviderConnection, clearSecrets: readonly string[] = []): MemoryProviderConnection {
    this.refreshIfChanged()
    if (this.isNative(providerId)) throw new Error('Mnemon Native service settings are managed by the native configuration')
    return this.providerCatalog.normalizeService(providerId, settings, this.services[providerId] ?? {}, clearSecrets)
  }

  /** Atomically replace one provider's local projections after authoritative discovery. */
  syncProviderService(providerId: MemoryProviderId, service: MemoryProviderConnection, discovered: readonly ProviderMemorySpace[]): MemoryProviderServiceView {
    this.refreshIfChanged()
    if (this.isNative(providerId)) throw new Error('Mnemon Native Stores are discovered from disk')
    let normalizedService = this.providerCatalog.normalizeService(providerId, service)
    const seen = new Set<string>()
    const existing = this.spaces.filter(body => body.providerId === providerId)
    const existingByExternalId = new Map<string | undefined, StoredMemorySpace>()
    for (const body of existing) if (!existingByExternalId.has(body.externalId)) existingByExternalId.set(body.externalId, body)
    const reservedIds = new Set(this.spaces.filter(body => body.providerId !== providerId).map(body => body.id))
    const timestamp = this.now().toISOString()
    const projections = discovered.map(candidate => {
      const externalId = requiredText(candidate.externalId, 'provider externalId', 2000)
      if (seen.has(externalId)) throw new Error(`${this.providerCatalog.descriptor(providerId).label} returned a duplicate memory namespace: ${externalId}`)
      seen.add(externalId)
      const connection = this.providerCatalog.normalizeMemory(providerId, candidate.connection)
      this.providerCatalog.normalize(providerId, { ...normalizedService, ...connection })
      const previous = existingByExternalId.get(externalId)
      let id = previous?.id ?? validateMemorySpaceId(`${providerId}-${createHash('sha256').update(externalId).digest('hex').slice(0, 24)}`)
      let suffix = 1
      while (reservedIds.has(id)) {
        id = validateMemorySpaceId(`${providerId}-${createHash('sha256').update(`${externalId}:${suffix}`).digest('hex').slice(0, 24)}`)
        suffix += 1
      }
      reservedIds.add(id)
      const metadata = providerProjectionMetadata(this.providerCatalog, providerId, candidate)
      const metadataSource = previous?.metadataSource ?? (previous === undefined ? 'provider' : 'manual')
      const preserveLocalMetadata = previous !== undefined && metadataSource !== 'provider'
      return {
        id,
        externalId,
        // Discovery owns initial presentation metadata. Once a user or AI has
        // curated it, reconnect only refreshes provider identity and settings.
        name: preserveLocalMetadata ? previous.name : metadata.name,
        description: preserveLocalMetadata ? previous.description : metadata.description,
        metadataSource,
        active: previous?.active ?? true,
        providerId,
        connection,
        createdAt: previous?.createdAt ?? timestamp,
        updatedAt: this.nextTimestamp(previous?.updatedAt, timestamp),
      } satisfies StoredMemorySpace
    })
    // Some non-enumerating Providers declare how their first discovered
    // namespace seeds a reusable service default. The generic host never
    // branches on Provider identity.
    for (const field of this.providerCatalog.serviceFields(providerId)) {
      if (field.discoveryDefaultFrom === undefined || String(normalizedService[field.key] ?? '').trim() !== '') continue
      const discoveredValue = projections[0]?.connection?.[field.discoveryDefaultFrom]
      if (discoveredValue !== undefined && String(discoveredValue).trim() !== '') {
        normalizedService = this.providerCatalog.normalizeService(providerId, { ...normalizedService, [field.key]: discoveredValue })
      }
    }
    this.services[providerId] = normalizedService
    this.serviceEnabled[providerId] = true
    this.spaces = [...this.spaces.filter(body => body.providerId !== providerId), ...projections]
    this.save()
    return this.providerServices().items.find(item => item.providerId === providerId)!
  }

  placementCandidates(request: Pick<CreateMemorySpaceRequest, 'connection' | 'providerConnections' | 'openViking'>): MemoryPlacementCandidate[] {
    return this.providerCatalog.providers.map(descriptor => {
      const providerTypeId = descriptor.typeId ?? descriptor.id
      const requestConnection = request.providerConnections?.[descriptor.id]
        ?? (providerTypeId === 'openviking' && request.connection === undefined && request.openViking !== undefined
        ? request.openViking as unknown as MemoryProviderConnection
        : request.connection)
      let configured = providerTypeId === 'mnemon-native' ? this.runner.commandFound : false
      if (providerTypeId !== 'mnemon-native' && (requestConnection !== undefined || this.providerServiceConfigured(descriptor.id))) {
        try {
          const split = this.providerCatalog.split(descriptor.id, requestConnection)
          this.providerCatalog.normalize(descriptor.id, { ...(this.services[descriptor.id] ?? {}), ...split.service, ...split.memory })
          configured = Object.keys(split.service).length > 0 || this.providerServiceEnabled(descriptor.id)
        } catch { configured = false }
      }
      return {
        id: descriptor.id,
        label: descriptor.label,
        kind: descriptor.kind,
        configured,
        summary: descriptor.summary,
        capabilities: descriptor.capabilities,
      }
    })
  }

  async create(request: CreateMemorySpaceRequest, signal?: AbortSignal, placement?: MemoryPlacementDecision): Promise<MemorySpace> {
    const name = requiredText(request.name, 'name', 100)
    const description = requiredText(request.description, 'description', 1000)
    if (request.placement !== undefined && placement === undefined) throw new Error('automatic provider placement must be resolved before creating a Memory Space')
    if (placement !== undefined && request.providerId !== undefined && request.providerId !== placement.providerId) throw new Error('resolved provider placement conflicts with providerId')
    const providerId = placement?.providerId ?? request.providerId ?? this.defaultProviderId()
    if (providerId === undefined) throw new Error('No memory provider is ready: install the Mnemon CLI to use Mnemon Native, or connect another provider on the dsh-mnemon page under Plugins')
    if (!this.providerCatalog.has(providerId)) throw new Error(`unsupported memory provider: ${String(providerId)}`)
    const normalizedPlacement = placement === undefined ? undefined : normalizePlacementDecision(placement, providerId, this.providerCatalog)
    if (placement !== undefined && normalizedPlacement === undefined) throw new Error('resolved provider placement is invalid')
    const reservedIds = new Set(this.list().map(body => body.id))
    const nativeStoreIds = this.nativeStoreIds()
    const nativeProvider = this.isNative(providerId)
    let id = nativeProvider && nativeStoreIds.length === 0 && !reservedIds.has('default')
      ? 'default'
      : validateMemorySpaceId(nativeProvider ? randomUUID() : `${providerId}-${randomUUID()}`)
    while (reservedIds.has(id) || nativeStoreIds.includes(id)) id = validateMemorySpaceId(randomUUID())
    const connectionInput = request.providerConnections?.[providerId]
      ?? (this.providerTypeId(providerId) === 'openviking' && request.connection === undefined && request.openViking !== undefined
      ? request.openViking as unknown as MemoryProviderConnection
      : request.connection)
    let connection: MemoryProviderConnection | undefined
    if (!nativeProvider) {
      const split = this.providerCatalog.split(providerId, connectionInput)
      if (Object.keys(split.service).length > 0) {
        this.services[providerId] = this.providerCatalog.normalizeService(providerId, split.service, this.services[providerId] ?? {})
        this.serviceEnabled[providerId] = true
      }
      if (!this.providerServiceEnabled(providerId)) throw new Error(`${this.providerCatalog.descriptor(providerId).label} service is not enabled; enable it on the dsh-mnemon page under Plugins first`)
      connection = this.providerCatalog.normalizeMemory(providerId, split.memory)
      this.providerCatalog.normalize(providerId, { ...this.services[providerId], ...connection })
    }
    if (nativeProvider) await this.runner.runText(['store', 'create', id], { ...(signal === undefined ? {} : { signal }), store: id })
    const timestamp = this.now().toISOString()
    const body: StoredMemorySpace = {
      id,
      name,
      description,
      active: request.active ?? false,
      providerId,
      ...(nativeProvider ? {} : { metadataSource: 'manual' as const }),
      ...(normalizedPlacement === undefined ? {} : { placement: normalizedPlacement }),
      ...(connection === undefined ? {} : { connection }),
      createdAt: timestamp,
      updatedAt: timestamp,
    }
    this.spaces.push(body)
    this.save()
    return this.view(body)
  }

  update(id: string, request: UpdateMemorySpaceRequest): MemorySpace {
    this.refreshIfChanged()
    const normalized = validateMemorySpaceId(id)
    const index = this.spaces.findIndex(body => body.id === normalized)
    if (index < 0) throw new Error(`unknown memory space: ${normalized}`)
    const current = this.spaces[index]!
    if (request.openViking !== undefined && this.providerTypeId(current.providerId) !== 'openviking') throw new Error('OpenViking connection settings only apply to OpenViking memory spaces')
    const nativeProvider = this.isNative(current.providerId)
    if ((request.connection !== undefined || request.clearSecrets !== undefined) && nativeProvider) {
      throw new Error('Mnemon Native memory spaces do not have provider connection settings')
    }
    const legacyPatch = request.openViking === undefined ? undefined : {
      ...request.openViking,
      ...(request.openViking.clearApiKey === true ? { apiKey: '' } : {}),
    } as unknown as MemoryProviderConnection
    const previousConnection = nativeProvider ? {} : current.connection ?? {}
    const connectionPatch = request.connection ?? legacyPatch
    let connection: MemoryProviderConnection | undefined
    if (!nativeProvider) {
      const split = this.providerCatalog.split(current.providerId, connectionPatch)
      const clearSecrets = [...(request.clearSecrets ?? []), ...(request.openViking?.clearApiKey === true ? ['apiKey'] : [])]
      if (Object.keys(split.service).length > 0 || clearSecrets.length > 0) {
        this.services[current.providerId] = this.providerCatalog.normalizeService(current.providerId, split.service, this.services[current.providerId] ?? {}, clearSecrets)
        this.serviceEnabled[current.providerId] = true
      }
      if (!this.providerServiceEnabled(current.providerId)) throw new Error(`${this.providerCatalog.descriptor(current.providerId).label} service is not enabled; enable it on the dsh-mnemon page under Plugins first`)
      connection = this.providerCatalog.normalizeMemory(current.providerId, split.memory, previousConnection)
      this.providerCatalog.normalize(current.providerId, { ...this.services[current.providerId], ...connection })
    }
    const { openViking: _legacyOpenViking, ...currentBody } = current
    const body: StoredMemorySpace = {
      ...currentBody,
      ...(request.name === undefined ? {} : { name: requiredText(request.name, 'name', 100) }),
      ...(request.description === undefined ? {} : { description: optionalText(request.description, 'description', 1000) }),
      ...(request.active === undefined ? {} : { active: request.active }),
      ...(nativeProvider || (request.name === undefined && request.description === undefined) ? {} : { metadataSource: 'manual' as const }),
      ...(connection === undefined ? {} : { connection }),
      updatedAt: this.nextTimestamp(current.updatedAt),
    }
    this.spaces[index] = body
    this.save()
    return this.view(body)
  }

  /** Validate every model-authored update before committing the batch. */
  updateMetadata(updates: readonly MemorySpaceMetadataUpdate[]): MemorySpace[] {
    this.refreshIfChanged()
    if (updates.length === 0 || updates.length > 20) throw new Error('metadata maintenance requires 1 through 20 Memory Spaces')
    const seen = new Set<string>()
    const replacements = updates.map(update => {
      const id = validateMemorySpaceId(update.memoryBodyId)
      if (seen.has(id)) throw new Error(`duplicate metadata update: ${id}`)
      seen.add(id)
      const index = this.spaces.findIndex(body => body.id === id)
      if (index < 0) throw new Error(`unknown memory space: ${id}`)
      return {
        index,
        body: {
          ...this.spaces[index]!,
          name: requiredText(update.title, 'title', 48),
          description: requiredText(update.description, 'description', 200),
          metadataSource: 'ai',
          updatedAt: this.nextTimestamp(this.spaces[index]!.updatedAt),
        } satisfies StoredMemorySpace,
      }
    })
    for (const replacement of replacements) this.spaces[replacement.index] = replacement.body
    this.save()
    return replacements.map(replacement => this.view(replacement.body))
  }

  async remove(id: string, signal?: AbortSignal): Promise<MemorySpace> {
    const body = this.get(id)
    if (!this.isNative(body.provider.id)) {
      this.spaces = this.spaces.filter(entry => entry.id !== body.id)
      this.save()
      return body
    }
    const nativeStoreIds = this.nativeStoreIds()
    if (nativeStoreIds.includes(body.id) && nativeStoreIds.length === 1) {
      throw new Error(`cannot delete the last Mnemon Store "${body.id}"; disable it for DSH or create another Memory Space first`)
    }
    const persistedStore = this.runner.persistedStore()
    const commands: MnemonTextCommand[] = []
    let commandStore = persistedStore
    if (persistedStore === body.id) {
      const nativeIds = new Set(nativeStoreIds)
      const replacement = this.list()
        .filter(candidate => candidate.id !== body.id && nativeIds.has(candidate.id))
        .sort((left, right) => Number(right.active) - Number(left.active) || left.id.localeCompare(right.id))[0]?.id
        ?? nativeStoreIds.filter(candidate => candidate !== body.id).sort()[0]
      if (replacement === undefined) throw new Error(`cannot switch away from Mnemon Store "${body.id}" before deleting it`)
      commandStore = replacement
      commands.push({
        args: ['store', 'set', replacement],
        options: { ...(signal === undefined ? {} : { signal }), store: replacement },
      })
    }
    commands.push({
      args: ['store', 'remove', body.id],
      // Mnemon treats --store as the active Store even for `store remove`.
      // Keep the deletion target out of command context or every removal fails.
      options: { ...(signal === undefined ? {} : { signal }), store: commandStore },
    })
    await this.runner.runTextBatch(commands)
    this.spaces = this.spaces.filter(entry => entry.id !== body.id)
    this.save()
    return body
  }

  setActive(id: string, active: boolean): MemorySpace {
    return this.update(id, { active })
  }

  /** Advance the safe catalog checkpoint after provider-backed content changes. */
  touch(id: string): MemorySpace {
    const normalized = validateMemorySpaceId(id)
    const index = this.spaces.findIndex(body => body.id === normalized)
    if (index < 0) throw new Error(`unknown memory space: ${normalized}`)
    const body = { ...this.spaces[index]!, updatedAt: this.nextTimestamp(this.spaces[index]!.updatedAt) }
    this.spaces[index] = body
    this.save()
    return this.view(body)
  }

  /** Refresh metadata after an atomic Pack import replaced the data component. */
  reload(): void {
    this.refreshing = true
    try {
      this.spaces = []
      this.services = {}
      this.serviceEnabled = {}
      this.loadAndReconcile()
      this.state.diskRevision = this.diskRevision()
    } finally { this.refreshing = false }
  }

  private diskRevision(): string {
    if (!this.persistent) return 'ephemeral'
    return [this.registryPath, this.providerRegistryPath].map(path => {
      try {
        const stat = statSync(path, { bigint: true })
        return `${stat.ino}:${stat.mtimeNs}:${stat.size}`
      } catch (error) {
        if ((error as NodeJS.ErrnoException).code === 'ENOENT') return 'missing'
        throw error
      }
    }).join('/')
  }

  /** Separate generations and legacy facades observe the same durable authority. */
  private refreshIfChanged(): void {
    if (!this.refreshing && this.persistent && this.state.diskRevision !== this.diskRevision()) this.reload()
  }

  private loadAndReconcile(): void {
    let migratedSyntheticDefault = false
    let migratedProviderRegistry = false
    if (this.persistent && existsSync(this.registryPath)) {
      try {
        const parsed = JSON.parse(readFileSync(this.registryPath, 'utf8')) as NativeRegistryFile | LegacyProviderRegistryFile
        if ((parsed.version === NATIVE_REGISTRY_VERSION || parsed.version === 2) && Array.isArray(parsed.bodies)) {
          migratedProviderRegistry = parsed.version === 2
          this.spaces = parsed.bodies.filter(body => ID_PATTERN.test(body.id)).map(body => {
            // Earlier dsh-mnemon builds gave an already-existing upstream
            // `default` Store a synthetic Chinese product name. That made a
            // compatibility import look like a newly-created default Memory
            // Space. Preserve the Store and its activation state, but restore
            // its neutral on-disk identity.
            const syntheticDefault = body.id === 'default'
              && body.name === '默认记忆体'
              && body.description === '从现有 Mnemon Store 自动接入。'
            migratedSyntheticDefault ||= syntheticDefault
            const providerId: MemoryProviderId = 'providerId' in body && storedProviderId(body.providerId) ? body.providerId : 'mnemon-native'
            const providerKnown = this.providerCatalog.has(providerId)
            if (!providerKnown) {
              return {
                id: body.id,
                name: requiredText(body.name || body.id, 'name', 100),
                description: optionalText(body.description, 'description', 1000),
                active: body.active === true,
                providerId,
                connection: storedProviderConnection('connection' in body ? body.connection : undefined),
                createdAt: body.createdAt,
                updatedAt: body.updatedAt,
              }
            }
            const placement = 'placement' in body ? normalizePlacementDecision(body.placement, providerId, this.providerCatalog) : undefined
            const rawConnection = 'connection' in body && body.connection != null
              ? body.connection as MemoryProviderConnection
              : this.providerTypeId(providerId) === 'openviking' && 'openViking' in body && body.openViking != null
                ? body.openViking as unknown as MemoryProviderConnection
                : undefined
            const split = this.isNative(providerId) ? undefined : this.providerCatalog.split(providerId, rawConnection)
            if (split !== undefined && this.services[providerId] === undefined) {
              this.services[providerId] = this.providerCatalog.normalizeService(providerId, split.service)
              this.serviceEnabled[providerId] = true
            }
            const connection = split === undefined ? undefined : this.providerCatalog.normalizeMemory(providerId, split.memory)
            if (connection !== undefined) this.providerCatalog.normalize(providerId, { ...this.services[providerId], ...connection })
            return {
              id: body.id,
              name: requiredText(syntheticDefault ? body.id : body.name || body.id, 'name', 100),
              description: optionalText(syntheticDefault ? 'Existing Mnemon Store discovered on disk.' : body.description, 'description', 1000),
              active: body.active === true,
              providerId,
              ...(placement === undefined ? {} : { placement }),
              ...(connection === undefined ? {} : { connection }),
              createdAt: body.createdAt,
              updatedAt: body.updatedAt,
            }
          })
        }
      } catch {
        // Rebuild a valid catalog from native stores without touching their DBs.
        this.spaces = []
      }
    }
    if (this.persistent && existsSync(this.providerRegistryPath)) {
      try {
        const parsed = JSON.parse(readFileSync(this.providerRegistryPath, 'utf8')) as ProviderRegistryFile | LegacyProviderRegistryFileV3 | LegacyProviderRegistryFileOnDisk
        if ((parsed.version === 3 || parsed.version === PROVIDER_REGISTRY_VERSION) && typeof parsed.services === 'object' && parsed.services !== null) {
          for (const [providerId, settings] of Object.entries(parsed.services)) {
            if (!storedProviderId(providerId) || typeof settings !== 'object' || settings === null) continue
            if (this.providerCatalog.has(providerId) && this.isNative(providerId)) continue
            this.services[providerId] = this.providerCatalog.has(providerId)
              ? this.providerCatalog.normalizeService(providerId, settings)
              : storedProviderConnection(settings)
            this.serviceEnabled[providerId] = parsed.enabled === undefined ? true : parsed.enabled[providerId] === true
          }
        }
        if ((parsed.version === 1 || parsed.version === 2 || parsed.version === 3 || parsed.version === PROVIDER_REGISTRY_VERSION) && Array.isArray(parsed.bodies)) {
          migratedProviderRegistry ||= parsed.version !== PROVIDER_REGISTRY_VERSION
          const existingIds = new Set(this.spaces.map(body => body.id))
          this.spaces.push(...parsed.bodies
            .filter(body => storedProviderId(body.providerId)
              && (!this.providerCatalog.has(body.providerId) || !this.isNative(body.providerId))
              && ID_PATTERN.test(body.id) && !existingIds.has(body.id))
            .map(body => {
              const providerId = body.providerId
              if (!this.providerCatalog.has(providerId)) {
                return {
                  id: body.id,
                  name: requiredText(body.name || body.id, 'name', 100),
                  description: optionalText(body.description, 'description', 1000),
                  active: body.active === true,
                  providerId,
                  ...(typeof body.externalId !== 'string' || body.externalId.trim() === '' ? {} : { externalId: body.externalId.trim() }),
                  ...(body.metadataSource === 'provider' || body.metadataSource === 'manual' || body.metadataSource === 'ai' ? { metadataSource: body.metadataSource } : {}),
                  connection: storedProviderConnection(body.connection ?? body.openViking),
                  createdAt: body.createdAt,
                  updatedAt: body.updatedAt,
                } satisfies StoredMemorySpace
              }
              const placement = normalizePlacementDecision(body.placement, providerId, this.providerCatalog)
              const rawConnection = body.connection ?? (this.providerTypeId(providerId) === 'openviking' && body.openViking !== undefined
                ? body.openViking as unknown as MemoryProviderConnection
                : undefined)
              const split = parsed.version === 3 || parsed.version === PROVIDER_REGISTRY_VERSION
                ? { service: {}, memory: rawConnection ?? {} }
                : this.providerCatalog.split(providerId, rawConnection)
              if (parsed.version !== 3 && parsed.version !== PROVIDER_REGISTRY_VERSION && this.services[providerId] === undefined) {
                this.services[providerId] = this.providerCatalog.normalizeService(providerId, split.service)
                this.serviceEnabled[providerId] = true
              }
              const connection = this.providerCatalog.normalizeMemory(providerId, split.memory)
              this.providerCatalog.normalize(providerId, { ...this.services[providerId], ...connection })
              return {
                id: body.id,
                name: requiredText(body.name || body.id, 'name', 100),
                description: optionalText(body.description, 'description', 1000),
                active: body.active === true,
                providerId,
                ...(typeof body.externalId !== 'string' || body.externalId.trim() === '' ? {} : { externalId: body.externalId.trim() }),
                ...(body.metadataSource === 'provider' || body.metadataSource === 'manual' || body.metadataSource === 'ai' ? { metadataSource: body.metadataSource } : {}),
                ...(placement === undefined ? {} : { placement }),
                connection,
                createdAt: body.createdAt,
                updatedAt: body.updatedAt,
              }
            }))
        }
      } catch {
        // Ignore an invalid optional provider registry; native Stores remain usable.
      }
    }
    const retainedBodies = this.spaces.filter(body => !this.providerCatalog.has(body.providerId)
      || this.isNative(body.providerId)
      || this.providerServiceEnabled(body.providerId))
    if (retainedBodies.length !== this.spaces.length) {
      this.spaces = retainedBodies
      migratedProviderRegistry = true
    }
    this.reconcileDiscoveredStores()
    if (migratedSyntheticDefault || migratedProviderRegistry) this.save()
  }

  private reconcileDiscoveredStores(): void {
    if (!this.persistent || !existsSync(this.directory)) return
    const timestamp = this.now().toISOString()
    const legacyActive = this.runner.effectiveStore()
    let changed = false
    const knownIds = new Set(this.spaces.map(body => body.id))
    for (const entry of readdirSync(this.directory, { withFileTypes: true })) {
      if (!entry.isDirectory() || !ID_PATTERN.test(entry.name) || !existsSync(join(this.directory, entry.name, 'mnemon.db'))) continue
      if (knownIds.has(entry.name)) continue
      this.spaces.push({
        id: entry.name,
        name: entry.name,
        description: 'Existing Mnemon Store discovered on disk.',
        active: this.spaces.length === 0 || entry.name === legacyActive,
        providerId: 'mnemon-native',
        createdAt: timestamp,
        updatedAt: timestamp,
      })
      knownIds.add(entry.name)
      changed = true
    }
    if (changed) this.save()
  }

  private nativeStoreIds(): string[] {
    if (!existsSync(this.directory)) return []
    return readdirSync(this.directory, { withFileTypes: true })
      .filter(entry => entry.isDirectory() && ID_PATTERN.test(entry.name))
      .map(entry => entry.name)
      .sort()
  }

  private nextTimestamp(previous?: string, candidate = this.now().toISOString()): string {
    if (previous === undefined) return candidate
    const previousTime = Date.parse(previous)
    const candidateTime = Date.parse(candidate)
    if (!Number.isFinite(previousTime) || !Number.isFinite(candidateTime) || candidateTime > previousTime) return candidate
    return new Date(previousTime + 1).toISOString()
  }

  private view(body: StoredMemorySpace): MemorySpace {
    const descriptor = this.providerCatalog.descriptor(body.providerId)
    const nativeProvider = this.isNative(body.providerId)
    const connection = nativeProvider ? {} : this.connectionFor(body)
    const effectivePublicConnection = this.providerCatalog.public(body.providerId, connection)
    const publicConnection = nativeProvider
      ? effectivePublicConnection
      : this.providerCatalog.publicScoped(body.providerId, 'memory', body.connection ?? {})
    const location = nativeProvider
      ? join(this.directory, body.id, 'mnemon.db')
      : providerDisplayLocation(descriptor, connection)
    const provider: MemorySpaceProvider = {
      id: descriptor.id,
      ...(descriptor.typeId === undefined || descriptor.typeId === descriptor.id ? {} : { typeId: descriptor.typeId }),
      label: descriptor.label,
      ...(descriptor.icon === undefined ? {} : { icon: descriptor.icon }),
      origin: descriptor.origin,
      kind: descriptor.kind,
      location,
      ...(typeof connection.targetUri === 'string' && connection.targetUri !== '' ? { targetUri: connection.targetUri } : {}),
      ...(typeof connection.account === 'string' && connection.account !== '' ? { account: connection.account } : {}),
      ...(typeof connection.user === 'string' && connection.user !== '' ? { user: connection.user } : {}),
      ...(typeof connection.actorPeerId === 'string' && connection.actorPeerId !== '' ? { actorPeerId: connection.actorPeerId } : {}),
      apiKeyConfigured: effectivePublicConnection.configuredSecrets.includes('apiKey'),
      ...publicConnection,
      capabilities: descriptor.capabilities,
    }
    const { providerId: _providerId, externalId: _externalId, metadataSource: _metadataSource, connection: _connection, openViking: _openViking, ...metadata } = body
    return { ...metadata, dbPath: nativeProvider ? provider.location : '', provider }
  }

  private save(): void {
    if (!this.persistent) return
    mkdirSync(this.directory, { recursive: true, mode: 0o700 })
    const nativeBodies: StoredNativeMemorySpace[] = this.spaces
      .filter(body => body.providerId === 'mnemon-native' || this.providerCatalog.has(body.providerId) && this.isNative(body.providerId))
      .map(({ providerId: _providerId, connection: _connection, openViking: _openViking, ...body }) => body)
    this.writeRegistry(this.registryPath, { version: NATIVE_REGISTRY_VERSION, bodies: nativeBodies })

    const providerBodies = this.spaces.filter(body => body.providerId !== 'mnemon-native' && (!this.providerCatalog.has(body.providerId) || !this.isNative(body.providerId)))
    if (providerBodies.length === 0 && Object.keys(this.services).length === 0) {
      rmSync(this.providerRegistryPath, { force: true })
      this.state.diskRevision = this.diskRevision()
      return
    }
    mkdirSync(join(this.runner.effectiveDataDir(), 'state'), { recursive: true, mode: 0o700 })
    this.writeRegistry(this.providerRegistryPath, { version: PROVIDER_REGISTRY_VERSION, services: this.services, enabled: this.serviceEnabled, bodies: providerBodies })
    this.state.diskRevision = this.diskRevision()
  }

  private writeRegistry(path: string, file: NativeRegistryFile | ProviderRegistryFile): void {
    const temporary = join(dirname(path), `.${basename(path)}.${process.pid}.tmp`)
    writeFileSync(temporary, `${JSON.stringify(file, null, 2)}\n`, { encoding: 'utf8', mode: 0o600 })
    renameSync(temporary, path)
  }
}
