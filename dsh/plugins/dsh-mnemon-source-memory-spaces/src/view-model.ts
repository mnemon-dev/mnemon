import type { MemorySpaceCatalog, MemorySpacesStatus } from './contracts.ts'
const MODEL_MEMORY_BODY_LIMIT = 16
function boundedToolText(value: string, maximum: number): string {
  return value.length <= maximum ? value : `${value.slice(0, maximum - 1)}…`
}

/** Strip control-plane paths, provider settings, and statistics from model output. */
export function modelSpaceCatalog(catalog: MemorySpaceCatalog) {
  const items = catalog.items.slice(0, MODEL_MEMORY_BODY_LIMIT)
  return {
    items: items.map(body => ({
      id: body.id,
      name: boundedToolText(body.name, 120),
      description: boundedToolText(body.description, 500),
      active: body.active,
      providerEnabled: body.providerEnabled !== false,
      status: body.providerEnabled === false
        ? 'provider-disabled'
        : body.statusLoading === true ? 'not-probed' : body.healthy ? 'healthy' : 'unhealthy',
      ...(body.error === undefined ? {} : { error: boundedToolText(body.error, 500) }),
      provider: {
        id: body.provider.id,
        label: boundedToolText(body.provider.label, 120),
        capabilities: structuredClone(body.provider.capabilities),
      },
    })),
    persistenceStrategy: catalog.persistenceStrategy,
    total: catalog.total,
    activeCount: catalog.activeCount,
    omittedCount: Math.max(0, catalog.items.length - items.length),
  }
}


/** Keep health diagnostics useful without exposing complete control-plane state. */
export function modelStatus(status: MemorySpacesStatus) {
  const active = status.memoryBodies.filter(body => body.active && body.providerEnabled !== false)
  const unhealthy = active.filter(body => !body.healthy)
  const relevantProviders = status.providerServices?.filter(provider => (
    provider.enabled || provider.configured || provider.memoryBodyCount > 0 || provider.status === 'unhealthy'
  )) ?? []
  const providers = relevantProviders.slice(0, 16)
  return {
    healthy: status.healthy && unhealthy.length === 0,
    ...(status.error === undefined ? {} : { error: boundedToolText(status.error, 1_000) }),
    ...(status.version === undefined ? {} : { version: boundedToolText(status.version, 120) }),
    commandFound: status.commandFound,
    writeEnabled: status.writeEnabled,
    memorySpaces: {
      total: status.memoryBodies.length,
      active: active.length,
      healthy: active.length - unhealthy.length,
      unhealthy: unhealthy.length,
      providerDisabled: status.memoryBodies.filter(body => body.providerEnabled === false).length,
    },
    providers: providers.map(provider => ({
      providerId: provider.providerId,
      label: boundedToolText(provider.label, 120),
      enabled: provider.enabled,
      configured: provider.configured,
      status: provider.status,
      memoryBodyCount: provider.memoryBodyCount,
      activeMemoryBodyCount: provider.activeMemoryBodyCount,
      ...(provider.error === undefined ? {} : { error: boundedToolText(provider.error, 500) }),
    })),
    omittedProviderCount: Math.max(0, relevantProviders.length - providers.length),
    ...(status.stats === undefined ? {} : {
      aggregate: {
        totalInsights: status.stats.totalInsights,
        deletedInsights: status.stats.deletedInsights,
        edgeCount: status.stats.edgeCount,
        oplogCount: status.stats.oplogCount,
        dbSizeBytes: status.stats.dbSizeBytes,
      },
    }),
  }
}


/** Preserve valid JSON when the Route's evidence budget is smaller than a directory. */
export function modelJson(value: unknown, maximum: number): string {
  const bounded = structuredClone(value) as { items?: unknown[]; providers?: unknown[]; omittedCount?: number; omittedProviderCount?: number }
  let result = JSON.stringify(bounded)
  while (result.length > maximum) {
    if (bounded.items !== undefined && bounded.items.length > 0) { bounded.items.pop(); bounded.omittedCount = (bounded.omittedCount ?? 0) + 1 }
    else if (bounded.providers !== undefined && bounded.providers.length > 0) { bounded.providers.pop(); bounded.omittedProviderCount = (bounded.omittedProviderCount ?? 0) + 1 }
    else return JSON.stringify({ unavailable: 'Inspection summary exceeds the Route budget; narrow the query.' })
    result = JSON.stringify(bounded)
  }
  return result
}
