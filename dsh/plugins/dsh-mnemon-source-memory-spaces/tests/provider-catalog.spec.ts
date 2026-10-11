import { describe, expect, it } from 'vitest'
import { catalog, descriptors } from './providers.ts'
const MEMORY_PROVIDER_CATALOG = descriptors
const MEMORY_PROVIDER_IDS = descriptors.map(provider => provider.id)
const memoryProviderDescriptor = catalog.descriptor.bind(catalog)
const normalizeProviderMemoryConnection = catalog.normalizeMemory.bind(catalog)
const normalizeProviderServiceConnection = catalog.normalizeService.bind(catalog)
const normalizeProviderConnection = catalog.normalize.bind(catalog)
const publicProviderConnection = catalog.public.bind(catalog)

describe('memory provider catalog', () => {
  it('exposes Mnemon Native plus every supported third-party provider', () => {
    expect(MEMORY_PROVIDER_IDS).toEqual([
      'mnemon-native',
      'openviking',
      'honcho',
      'mem0',
      'hindsight',
      'holographic',
      'retaindb',
      'byterover',
      'supermemory',
    ])
    expect(MEMORY_PROVIDER_CATALOG).toHaveLength(9)
    expect(MEMORY_PROVIDER_CATALOG.filter(provider => provider.origin === 'third-party')).toHaveLength(8)
    expect(MEMORY_PROVIDER_CATALOG.filter(provider => provider.capabilities.entities).map(provider => provider.id)).toEqual([
      'mnemon-native',
      'hindsight',
      'holographic',
    ])
    expect(MEMORY_PROVIDER_CATALOG.map(provider => [provider.id, provider.workspaceBinding])).toEqual([
      ['mnemon-native', 'automatic'],
      ['openviking', 'provider-global'],
      ['honcho', 'provider-global'],
      ['mem0', 'provider-global'],
      ['hindsight', 'provider-global'],
      ['holographic', 'optional-override'],
      ['retaindb', 'provider-global'],
      ['byterover', 'optional-override'],
      ['supermemory', 'provider-global'],
    ])
    expect(memoryProviderDescriptor('byterover').capabilities).toMatchObject({ search: true, browse: false, graph: false, entities: false })
    expect(memoryProviderDescriptor('holographic').fields.find(field => field.key === 'dataPath')).toMatchObject({ scope: 'service', role: 'global-location' })
    expect(memoryProviderDescriptor('byterover').fields.find(field => field.key === 'defaultDirectory')).toMatchObject({ scope: 'service', role: 'global-location' })
  })

  it('normalizes provider defaults without exposing secrets to clients', () => {
    const connection = normalizeProviderConnection('supermemory', {
      endpoint: 'https://api.supermemory.ai/',
      apiKey: 'secret',
      containerTag: 'team',
      searchMode: 'hybrid',
    })
    expect(connection.endpoint).toBe('https://api.supermemory.ai')
    expect(publicProviderConnection('supermemory', connection)).toEqual({
      settings: {
        endpoint: 'https://api.supermemory.ai',
        containerTag: 'team',
        searchMode: 'hybrid',
      },
      configuredSecrets: ['apiKey'],
    })
  })

  it('separates reusable service fields from per-Memory-Space fields', () => {
    const mem0 = memoryProviderDescriptor('mem0')
    expect(mem0.fields.filter(field => field.scope === 'service').map(field => field.key)).toEqual(['endpoint', 'apiKey', 'mode'])
    expect(mem0.fields.filter(field => field.scope === 'memory').map(field => field.key)).toEqual(['userId', 'agentId', 'rerank'])
    expect(normalizeProviderServiceConnection('mem0', { endpoint: 'http://127.0.0.1:8888', mode: 'self-hosted' })).toEqual({ endpoint: 'http://127.0.0.1:8888', apiKey: '', mode: 'self-hosted' })
    expect(normalizeProviderMemoryConnection('mem0', { userId: 'alice' })).toEqual({ userId: 'alice', agentId: 'dsh', rerank: false })
    expect(() => normalizeProviderServiceConnection('mem0', { userId: 'alice' })).toThrow(/service setting/u)
  })

  it('preserves an existing secret when editing non-secret settings and clears it explicitly', () => {
    const previous = normalizeProviderConnection('mem0', {
      endpoint: 'https://api.mem0.ai',
      apiKey: 'secret',
      mode: 'platform',
      userId: 'alice',
      agentId: 'dsh',
    })
    const edited = normalizeProviderConnection('mem0', { userId: 'bob' }, previous)
    expect(edited.apiKey).toBe('secret')
    expect(edited.userId).toBe('bob')
    expect(normalizeProviderConnection('mem0', {}, edited, ['apiKey']).apiKey).toBe('')

    const supermemory = normalizeProviderConnection('supermemory', { apiKey: 'required-secret' })
    expect(normalizeProviderConnection('supermemory', {}, supermemory, ['apiKey']).apiKey).toBe('')
  })

  it('rejects unsupported provider settings and invalid select values', () => {
    expect(() => normalizeProviderConnection('retaindb', { unexpected: 'value' })).toThrow(/unsupported RetainDB setting/u)
    expect(() => normalizeProviderConnection('mem0', { mode: 'mystery' })).toThrow(/unsupported value/u)
    expect(() => normalizeProviderConnection('holographic', { defaultTrust: 2 })).toThrow(/within 0\.\.1/u)
    expect(() => normalizeProviderConnection('supermemory', { apiKey: 'secret', containerTag: 'invalid tag' })).toThrow(/container tag/u)
    expect(memoryProviderDescriptor('holographic').kind).toBe('local')
  })
})
