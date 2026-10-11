import type { MemorySpace, MemoryProviderConnection, MemoryProviderDescriptor } from './contracts.ts'
import { MemoryProviderCatalog } from './providers/catalog.ts'
import { Context, type Plugin } from '@deepseek-ai/cordis'
import type { MemorySpaceAuthority, MemoryProviderAdapter, MemoryProviderAdapterFactoryContext } from './providers/adapter.ts'
import { deepFreeze, type MemorySpaceProviderManifest, type MemorySpaceProviderModule } from './providers/definitions.ts'
import { PrivateMemorySpaceProviderHost } from './providers/host.ts'
import { createMemorySpaceProviderPlugin } from './providers/plugin.ts'

export interface MemorySpaceProviderTest {
  readonly registered: boolean
  readonly descriptor: MemoryProviderDescriptor
  readonly manifest: MemorySpaceProviderManifest
  createAdapter(context: MemoryProviderAdapterFactoryContext): MemoryProviderAdapter
  dispose(): Promise<void>
}

/** Real private-child registration and factory validation, without exposing its Host/Registry. */
export async function mountMemorySpaceProvider<C>(
  module: MemorySpaceProviderModule<C>,
  options: { instanceId?: string; sourceInstanceId?: string; config: C },
): Promise<MemorySpaceProviderTest> {
  const context = new Context()
  const host = new PrivateMemorySpaceProviderHost(options.sourceInstanceId ?? 'test-memory-spaces')
  const instanceId = options.instanceId ?? module.id
  const plugin = createMemorySpaceProviderPlugin({ instanceId, module, config: options.config }, host)
  const bound: Plugin.Object<C | undefined> = { ...plugin, apply: (ctx, config) => plugin.apply(ctx, config as C) }
  const child = context.plugin(bound, options.config)
  try {
    await child.await()
    if (!host.has(instanceId)) throw new Error(`Memory Space Provider child did not install a definition: ${instanceId}`)
    const snapshot = host.snapshot()
    const adapters = new Set<MemoryProviderAdapter>()
    let closing: Promise<void> | undefined
    return Object.freeze({
      get registered() { return host.has(instanceId) },
      descriptor: deepFreeze(snapshot.descriptors()[0]!),
      manifest: snapshot.entries[0]!.definition.manifest,
      createAdapter: (input: MemoryProviderAdapterFactoryContext) => {
        if (closing !== undefined) throw new Error('Memory Space Provider test is disposed')
        const adapter = snapshot.adapterRegistry().create(input).get(instanceId)!
        adapters.add(adapter)
        return adapter
      },
      dispose: () => {
        closing ??= Promise.resolve().then(async () => {
          const failures: unknown[] = []
          for (const adapter of adapters) {
            try { await adapter.dispose?.() } catch (error) { failures.push(error) }
          }
          adapters.clear()
          try { await context.fiber.dispose() } catch (error) { failures.push(error) }
          if (failures.length > 0) throw new AggregateError(failures, 'Memory Space Provider test cleanup failed')
        })
        return closing
      },
    })
  } catch (error) {
    await context.fiber.dispose()
    throw error
  }
}

/** Driver fixture only: real descriptor validation, no Core or service singleton. */
export function createMemorySpaceProviderFixture(
  descriptor: MemoryProviderDescriptor,
  input: MemoryProviderConnection = {},
  options: { dataDir: string; instanceId?: string; memoryBodyId?: string },
): { body: MemorySpace; authority: MemorySpaceAuthority; connection: MemoryProviderConnection } {
  const instanceId = options.instanceId ?? descriptor.id
  const catalog = new MemoryProviderCatalog([{
    ...descriptor, id: instanceId,
    ...(instanceId === descriptor.id ? {} : { typeId: descriptor.id }),
  }])
  const connection = catalog.normalize(instanceId, input)
  const publicConnection = catalog.public(instanceId, connection)
  const body: MemorySpace = {
    id: options.memoryBodyId ?? 'test-memory', name: 'Test memory', description: 'Provider-owned test fixture',
    active: true, dbPath: '', createdAt: '2026-08-30T00:00:00.000Z', updatedAt: '2026-08-30T00:00:00.000Z',
    provider: {
      id: instanceId,
      ...(instanceId === descriptor.id ? {} : { typeId: descriptor.id }),
      label: descriptor.label, kind: descriptor.kind, origin: descriptor.origin,
      location: String(connection.endpoint ?? options.dataDir),
      apiKeyConfigured: publicConnection.configuredSecrets.includes('apiKey'),
      ...publicConnection, capabilities: structuredClone(descriptor.capabilities),
    },
  }
  const authority: MemorySpaceAuthority = {
    runner: { effectiveDataDir: () => options.dataDir },
    list: () => [body],
    providerConnection(id, expectedProviderId) {
      if (id !== body.id || expectedProviderId !== undefined && expectedProviderId !== instanceId) {
        throw new Error('Provider attempted to cross its test authority boundary')
      }
      return structuredClone(connection)
    },
  }
  return { body, authority, connection }
}
