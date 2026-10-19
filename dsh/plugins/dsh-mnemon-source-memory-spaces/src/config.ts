import z from '@deepseek-ai/schemastery'
import { homedir } from 'node:os'
import { isAbsolute, join } from 'node:path'
import type { MemoryPersistenceStrategy, ResolvedMemoryPersistenceStrategy, MemoryPlacementCapability, MemoryPlacementPreference, MemoryProviderId, MemoryProviderConnection, MnemonEmbeddingConfig, ResolvedMnemonEmbeddingConfig, RecallQualityConfig, ResolvedRecallQualityConfig } from './contracts.ts'
import { DEFAULT_EMBEDDING_ENDPOINT, DEFAULT_EMBEDDING_MODEL, DEFAULT_EMBEDDING_PROTOCOL, MNEMON_EMBEDDING_PROTOCOLS } from './contracts.ts'
import { DEFAULT_TIMEOUT_MS, DEFAULT_RECALL_LIMIT, DEFAULT_RECALL_QUALITY_POLICY, DEFAULT_RECALL_LOW_SCORE_THRESHOLD, DEFAULT_RECALL_HIGH_SCORE_THRESHOLD, DEFAULT_RECALL_CANDIDATE_MULTIPLIER, DEFAULT_RECALL_MAX_MEDIUM_RESULTS, DEFAULT_RECALL_MAX_UNKNOWN_RESULTS } from './defaults.ts'

export interface MemorySpacesConfig {
  dataDir?: string
  cliPath?: string
  store?: string
  timeoutMs?: number
  defaultRecallLimit?: number
  writeEnabled?: boolean
  embedding?: MnemonEmbeddingConfig
  recallQuality?: RecallQualityConfig
  persistenceStrategy?: MemoryPersistenceStrategy
}

export interface ResolvedMemorySpacesConfig {
  storageScope: 'global' | 'workspace' | 'custom'
  dataDir?: string
  cliPath?: string
  store?: string
  timeoutMs: number
  defaultRecallLimit: number
  writeEnabled: boolean
  embedding: ResolvedMnemonEmbeddingConfig
  recallQuality: ResolvedRecallQualityConfig
  persistenceStrategy: ResolvedMemoryPersistenceStrategy
}

export const MemorySpacesConfig = z.object({
  dataDir: z.string(), cliPath: z.string(), store: z.string(),
  timeoutMs: z.number().step(1).min(1).max(600_000),
  defaultRecallLimit: z.number().step(1).min(1).max(50),
  writeEnabled: z.boolean(), embedding: z.any(), recallQuality: z.any(), persistenceStrategy: z.any(),
}) as unknown as z<MemorySpacesConfig>

function optionalText(value: string | undefined): string | undefined { return value?.trim() || undefined }
const MEMORY_PROVIDER_ID = /^[a-z][a-z0-9-]{0,127}$/u
const MEMORY_PLACEMENT_CAPABILITY_SET = new Set(['graph', 'entities', 'related', 'exact-write', 'link', 'forget'])
const MEMORY_PLACEMENT_PREFERENCE_SET = new Set(['balanced', 'local-first', 'shared-first'])
export function resolvePersistenceStrategy(value: MemoryPersistenceStrategy | undefined): ResolvedMemorySpacesConfig['persistenceStrategy'] {
  const mode = value?.mode ?? 'manual'
  if (mode !== 'manual' && mode !== 'automatic') throw new Error(`dsh-mnemon: unsupported persistence strategy mode: ${String(mode)}`)
  const providerId = value?.providerId ?? 'mnemon-native'
  if (!MEMORY_PROVIDER_ID.test(providerId)) throw new Error(`dsh-mnemon: invalid persistence strategy provider: ${String(providerId)}`)
  const prompt = value?.prompt?.trim() ?? ''
  if (prompt.length > 4000) throw new Error('dsh-mnemon: persistence strategy prompt is too long (max 4000 characters)')
  const configuredProviderIds = value?.rules?.allowedProviderIds
  const allowedProviderIds = [...new Set(configuredProviderIds === undefined || (configuredProviderIds.length === 0 && mode === 'manual') ? ['mnemon-native'] : configuredProviderIds)]
  if (allowedProviderIds.length === 0) throw new Error('dsh-mnemon: persistence strategy requires at least one allowed provider')
  for (const id of allowedProviderIds) if (!MEMORY_PROVIDER_ID.test(id)) throw new Error(`dsh-mnemon: invalid persistence strategy provider: ${String(id)}`)
  const dataBoundary = value?.rules?.dataBoundary ?? 'allow-remote'
  if (dataBoundary !== 'allow-remote' && dataBoundary !== 'local-only') throw new Error(`dsh-mnemon: unsupported persistence data boundary: ${String(dataBoundary)}`)
  const requiredCapabilities = [...new Set(value?.rules?.requiredCapabilities ?? [])]
  for (const capability of requiredCapabilities) if (!MEMORY_PLACEMENT_CAPABILITY_SET.has(capability)) throw new Error(`dsh-mnemon: unsupported persistence capability: ${String(capability)}`)
  const preference = value?.rules?.preference ?? 'balanced'
  if (!MEMORY_PLACEMENT_PREFERENCE_SET.has(preference)) throw new Error(`dsh-mnemon: unsupported persistence preference: ${String(preference)}`)
  const providerConnections = Object.fromEntries(Object.entries(value?.providerConnections ?? {}).flatMap(([id, connection]) => {
    if (!MEMORY_PROVIDER_ID.test(id) || connection === undefined) return []
    const normalized = Object.fromEntries(Object.entries(connection).filter((entry): entry is [string, string | number | boolean] => ['string', 'number', 'boolean'].includes(typeof entry[1])))
    return [[id, normalized]]
  })) as Partial<Record<MemoryProviderId, MemoryProviderConnection>>
  return {
    mode,
    providerId,
    ...(value?.providerId === undefined ? { providerDefaulted: true as const } : {}),
    prompt,
    rules: {
      allowedProviderIds: allowedProviderIds as MemoryProviderId[],
      dataBoundary,
      requiredCapabilities: requiredCapabilities as MemoryPlacementCapability[],
      preference: preference as MemoryPlacementPreference,
    },
    providerConnections,
  }
}

export function resolveEmbedding(value: MemorySpacesConfig['embedding']): ResolvedMemorySpacesConfig['embedding'] {
  const endpoint = optionalText(value?.endpoint) ?? DEFAULT_EMBEDDING_ENDPOINT
  if (endpoint.length > 2048) throw new Error('dsh-mnemon: embedding endpoint is too long')
  let parsed: URL
  try {
    parsed = new URL(endpoint)
  } catch {
    throw new Error('dsh-mnemon: embedding endpoint must be an absolute HTTP or HTTPS URL')
  }
  if (!['http:', 'https:'].includes(parsed.protocol)
    || parsed.username !== '' || parsed.password !== ''
    || endpoint.includes('?') || endpoint.includes('#')) {
    throw new Error('dsh-mnemon: embedding endpoint must be an absolute HTTP or HTTPS URL without credentials, query, or fragment')
  }
  const normalizedEndpoint = endpoint.replace(/\/+$/u, '')
  const model = optionalText(value?.model) ?? DEFAULT_EMBEDDING_MODEL
  if (model.length > 200 || /[\u0000-\u001f\u007f]/u.test(model)) throw new Error('dsh-mnemon: embedding model must contain 1..200 characters without control characters')
  const apiKey = optionalText(value?.apiKey) ?? ''
  if (apiKey.length > 2048 || /[\u0000-\u001f\u007f]/u.test(apiKey)) throw new Error('dsh-mnemon: embedding API key must contain 0..2048 characters without control characters')
  const protocol = value?.protocol ?? DEFAULT_EMBEDDING_PROTOCOL
  if (!MNEMON_EMBEDDING_PROTOCOLS.includes(protocol)) throw new Error(`dsh-mnemon: unsupported embedding protocol: ${String(protocol)}`)
  return { enabled: value?.enabled === true, endpoint: normalizedEndpoint, model, apiKey, protocol }
}

export function resolveRecallQuality(value: RecallQualityConfig | undefined): ResolvedMemorySpacesConfig['recallQuality'] {
  const policy = optionalText(value?.policy) ?? DEFAULT_RECALL_QUALITY_POLICY
  if (!/^[a-z][a-z0-9-]{0,63}$/u.test(policy)) throw new Error('dsh-mnemon: recall quality policy id must match [a-z][a-z0-9-]{0,63}')
  const lowScoreThreshold = value?.lowScoreThreshold ?? DEFAULT_RECALL_LOW_SCORE_THRESHOLD
  const highScoreThreshold = value?.highScoreThreshold ?? DEFAULT_RECALL_HIGH_SCORE_THRESHOLD
  const candidateMultiplier = value?.candidateMultiplier ?? DEFAULT_RECALL_CANDIDATE_MULTIPLIER
  const maxMediumResults = value?.maxMediumResults ?? DEFAULT_RECALL_MAX_MEDIUM_RESULTS
  const maxUnknownResults = value?.maxUnknownResults ?? DEFAULT_RECALL_MAX_UNKNOWN_RESULTS
  if (!Number.isFinite(lowScoreThreshold) || lowScoreThreshold < 0 || lowScoreThreshold > 1) throw new Error('dsh-mnemon: recall low score threshold must be within 0..1')
  if (!Number.isFinite(highScoreThreshold) || highScoreThreshold < 0 || highScoreThreshold > 1) throw new Error('dsh-mnemon: recall high score threshold must be within 0..1')
  if (lowScoreThreshold >= highScoreThreshold) throw new Error('dsh-mnemon: recall low score threshold must be less than the high score threshold')
  if (!Number.isInteger(candidateMultiplier) || candidateMultiplier < 1 || candidateMultiplier > 5) throw new Error('dsh-mnemon: recall candidate multiplier must be an integer within 1..5')
  if (!Number.isInteger(maxMediumResults) || maxMediumResults < 0 || maxMediumResults > 50) throw new Error('dsh-mnemon: recall max medium results must be an integer within 0..50')
  if (!Number.isInteger(maxUnknownResults) || maxUnknownResults < 0 || maxUnknownResults > 50) throw new Error('dsh-mnemon: recall max unknown results must be an integer within 0..50')
  return { policy, lowScoreThreshold, highScoreThreshold, candidateMultiplier, maxMediumResults, maxUnknownResults }
}


/** Capture only this Source's settings, with an instance-local default authority. */
export function resolveMemorySpacesConfig(value: MemorySpacesConfig = {}, sourceInstanceKey = 'standalone'): ResolvedMemorySpacesConfig {
  const config = MemorySpacesConfig(value)
  const requested = optionalText(config.dataDir)
  const dataDir = requested === undefined ? join(homedir(), '.mnemon', 'sources', encodeURIComponent(sourceInstanceKey))
    : requested === '~' ? homedir() : requested.startsWith('~/') ? join(homedir(), requested.slice(2)) : requested
  if (!isAbsolute(dataDir)) throw new Error('Memory Spaces dataDir must be absolute or start with ~/')
  const store = optionalText(config.store)
  if (store !== undefined && !/^[a-zA-Z0-9][a-zA-Z0-9_-]*$/u.test(store)) throw new Error('Memory Spaces store has an invalid identifier')
  const cliPath = optionalText(config.cliPath)
  return {
    storageScope: 'custom', dataDir,
    ...(cliPath === undefined ? {} : { cliPath }), ...(store === undefined ? {} : { store }),
    timeoutMs: config.timeoutMs ?? DEFAULT_TIMEOUT_MS, defaultRecallLimit: config.defaultRecallLimit ?? DEFAULT_RECALL_LIMIT,
    writeEnabled: config.writeEnabled ?? true,
    embedding: resolveEmbedding(config.embedding), recallQuality: resolveRecallQuality(config.recallQuality),
    persistenceStrategy: resolvePersistenceStrategy(config.persistenceStrategy),
  }
}
