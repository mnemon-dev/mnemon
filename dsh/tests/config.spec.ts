import { afterEach, describe, expect, it, vi } from 'vitest'
import { Config, resolveConfig } from "../src/host/config.ts"

afterEach(() => vi.unstubAllEnvs())

describe('Mnemon config and resolution', () => {
  it.each([{ maxPerSession: -1 }, { maxPerSession: 201 }, { minIntervalMs: 0 }, { maxContextChars: 1 }, { maxTokens: 0 }, { provider: 'unisolated' }, { agentTeams: 'unrestricted' }, { runtimeMemory: 'documents' }])('rejects invalid idle review policy %j', idleReview => {
    expect(() => resolveConfig({ idleReview } as never)).toThrow()
  })
  it('keeps idle review to Documents only when runtime memory is off', () => {
    expect(resolveConfig({ idleReview: { runtimeMemory: false } }).idleReview).toMatchObject({ enabled: true, runtimeMemory: false, provider: 'spawn' })
  })
  it('materializes conservative defaults', () => {
    expect(resolveConfig({})).toMatchObject({
      storageScope: 'global',
      runtimeUserScope: 'storage',
      timeoutMs: 10_000,
      defaultRecallLimit: 10,
      runtimeMemory: {
        memoryLimitBytes: 10_240,
        userLimitBytes: 4_096,
        maintenanceMaxTokens: 8_192,
      },
      embedding: {
        enabled: false,
        endpoint: 'http://localhost:11434',
        model: 'nomic-embed-text',
      },
      memoryTopology: {
        id: 'default-three-tier',
        strategyId: 'default-three-tier',
        layers: {
          runtime: { enabled: true, participation: { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' }, adapterIds: [] },
          documents: { enabled: true, participation: { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' }, adapterIds: [] },
          'memory-spaces': { enabled: true, participation: { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' }, adapterIds: [] },
        },
      },
      recallQuality: {
        policy: 'strict-v1',
        lowScoreThreshold: 0.25,
        highScoreThreshold: 0.6,
        candidateMultiplier: 3,
        maxMediumResults: 4,
        maxUnknownResults: 2,
      },
      routingGuidance: true,
      lifecycleEnabled: true,
      recallMode: 'guided',
      writebackMode: 'guided',
      idleReviewMs: 30_000,
      idleReview: { enabled: true, runtimeMemory: true, provider: 'spawn', fallback: 'spawn', agentTeams: 'pause', minIntervalMs: 300_000, maxPerSession: 20, maxContextChars: 24_000, maxTokens: 4_096 },
      displayMode: 'sidebar',
      tabEnabled: true,
      writeEnabled: true,
      remoteAccess: 'read-only',
      conversationInteraction: { turnBar: true, saveAction: true },
      persistenceStrategy: {
        mode: 'manual',
        providerId: 'mnemon-native',
        prompt: '',
        rules: {
          allowedProviderIds: ['mnemon-native'],
          dataBoundary: 'allow-remote',
          requiredCapabilities: [],
          preference: 'balanced',
        },
        providerConnections: {},
      },
      taskAgentModel: { mode: 'inherit' },
    })
  })

  it('resolves independently switchable memory layer participation without deleting bindings', () => {
    expect(resolveConfig({
      memoryTopology: {
        layers: {
          documents: {
            enabled: false,
            participation: { recall: 'manual', write: 'off' },
            adapterIds: ['openviking', 'openviking'],
          },
        },
      },
    }).memoryTopology.layers.documents).toEqual({
      enabled: false,
      participation: { recall: 'manual', write: 'off', projection: 'automatic', maintenance: 'automatic' },
      adapterIds: ['openviking'],
    })
    expect(() => resolveConfig({ memoryTopology: { strategyId: '../unsafe' } })).toThrow('memory strategy id')
  })

  it('resolves all eight built-in Layer switch combinations without changing the fixed default policy', () => {
    const ids = ['runtime', 'documents', 'memory-spaces'] as const
    for (let mask = 0; mask < 8; mask += 1) {
      const layers = Object.fromEntries(ids.map((id, index) => [id, { enabled: (mask & (1 << index)) !== 0 }]))
      const resolved = resolveConfig({ memoryTopology: { layers } }).memoryTopology.layers
      for (const id of ids) {
        expect(resolved[id]).toEqual({
          enabled: layers[id]!.enabled,
          participation: { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' },
          adapterIds: [],
        })
      }
    }
  })

  it('resolves bounded Runtime Memory capacity and maintenance budgets', () => {
    expect(resolveConfig({
      runtimeMemory: {
        memoryLimitBytes: 20_480,
        userLimitBytes: 10_240,
        maintenanceMaxTokens: 32_768,
      },
    }).runtimeMemory).toEqual({
      memoryLimitBytes: 20_480,
      userLimitBytes: 10_240,
      maintenanceMaxTokens: 32_768,
    })
    expect(() => resolveConfig({ runtimeMemory: { memoryLimitBytes: 0 } })).toThrow('MEMORY.md limit')
    expect(() => resolveConfig({ runtimeMemory: { userLimitBytes: 1.5 } })).toThrow('USER.md limit')
    expect(() => resolveConfig({ runtimeMemory: { maintenanceMaxTokens: 1_000_001 } })).toThrow('maintenance maxTokens')
  })

  it('inherits the DSH new-session model by default and validates fixed task routes', () => {
    expect(resolveConfig({}).taskAgentModel).toEqual({ mode: 'inherit' })
    expect(resolveConfig({
      taskAgentModel: { mode: 'fixed', provider: ' deepseek ', model: ' deepseek-chat ' },
    }).taskAgentModel).toEqual({ mode: 'fixed', provider: 'deepseek', model: 'deepseek-chat' })
    expect(() => resolveConfig({ taskAgentModel: { mode: 'fixed', provider: 'deepseek' } }))
      .toThrow('provider and model')
  })

  it('migrates the settings schema empty candidate list to the conservative manual default', () => {
    expect(resolveConfig({ persistenceStrategy: { mode: 'manual', rules: { allowedProviderIds: [] } } }).persistenceStrategy.rules.allowedProviderIds)
      .toEqual(['mnemon-native'])
    expect(() => resolveConfig({ persistenceStrategy: { mode: 'automatic', rules: { allowedProviderIds: [] } } }))
      .toThrow('at least one allowed provider')
  })

  it('keeps explicit conversation-surface opt-outs', () => {
    expect(resolveConfig({ conversationInteraction: { turnBar: false, saveAction: false } }).conversationInteraction)
      .toMatchObject({ turnBar: false, saveAction: false })
  })

  it.each(['builtin', 'sidebar'] as const)('preserves displayMode=%s without changing storage or visibility', displayMode => {
    const legacy = { displayMode, storageScope: 'workspace' as const, tabEnabled: false, writeEnabled: false }
    const parsed = Config(legacy)
    expect(resolveConfig(parsed)).toMatchObject({ displayMode, storageScope: 'workspace', tabEnabled: false, writeEnabled: false })
    expect(resolveConfig({}).displayMode).toBe('sidebar')
    expect(Config({}).displayMode).toBe('sidebar')
    expect(legacy).toEqual({ displayMode, storageScope: 'workspace', tabEnabled: false, writeEnabled: false })
  })

  it('normalizes legacy buildin input to builtin without changing storage, visibility, or caller data', () => {
    const legacy = { displayMode: 'buildin' as const, storageScope: 'workspace' as const, tabEnabled: false }
    expect(resolveConfig(Config(legacy))).toMatchObject({ displayMode: 'builtin', storageScope: 'workspace', tabEnabled: false })
    expect(resolveConfig(legacy).displayMode).toBe('builtin')
    expect(legacy.displayMode).toBe('buildin')
  })

  it.each(['built-in', 'Builtin', 'unknown', '', true])('rejects invalid displayMode=%s', displayMode => {
    expect(() => Config({ displayMode } as never)).toThrow()
    expect(() => resolveConfig({ displayMode } as never)).toThrow('displayMode')
  })

  it('resolves the one storage-scope setting and preserves legacy dataDir as custom', () => {
    expect(resolveConfig({ storageScope: 'workspace' })).toMatchObject({ storageScope: 'workspace' })
    expect(resolveConfig({ dataDir: '/memory/custom' })).toMatchObject({
      storageScope: 'custom', dataDir: '/memory/custom',
    })
    expect(() => resolveConfig({ storageScope: 'custom' })).toThrow('custom dataDir')
    expect(() => resolveConfig({ storageScope: 'custom', dataDir: 'relative/memory' })).toThrow('absolute')
  })

  it('keeps USER.md on the selected root by default and accepts an explicit global profile', () => {
    expect(resolveConfig({ storageScope: 'workspace' })).toMatchObject({ storageScope: 'workspace', runtimeUserScope: 'storage' })
    expect(resolveConfig({ storageScope: 'workspace', runtimeUserScope: 'global' })).toMatchObject({ storageScope: 'workspace', runtimeUserScope: 'global' })
    expect(() => resolveConfig({ runtimeUserScope: 'workspace' as never })).toThrow('USER.md scope')
  })

  it('migrates the selected root from the former named-Pack settings', () => {
    expect(resolveConfig({
      storageScope: 'custom',
      customPackId: 'research',
      customPacks: [
        { id: 'project', name: 'Project', dataDir: '/memory/project' },
        { id: 'research', name: 'Research', dataDir: '~/memory/research' },
      ],
    })).toMatchObject({
      storageScope: 'custom',
      dataDir: '~/memory/research',
    })
  })

  it('rejects duplicate, missing, and unsafe custom Pack definitions', () => {
    expect(() => resolveConfig({ customPacks: [{ id: 'same', name: 'One', dataDir: '/one' }, { id: 'same', name: 'Two', dataDir: '/two' }] })).toThrow('duplicate')
    expect(() => resolveConfig({ storageScope: 'custom', customPackId: 'missing', customPacks: [{ id: 'other', name: 'Other', dataDir: '/other' }] })).toThrow('unknown custom Pack')
    expect(() => resolveConfig({ customPacks: [{ id: '../bad', name: 'Bad', dataDir: '/bad' }] })).toThrow('id')
  })

  it('rejects unsafe store names', () => {
    expect(() => resolveConfig({ store: '../other' })).toThrow('store')
  })

})

describe('centralized workspace configuration', () => {
  it('accepts default, home-relative and custom central roots with either USER.md scope', () => {
    for (const runtimeUserScope of ['storage', 'global'] as const) {
      for (const dataDir of [undefined, '', '~/central-memory', '/central-memory']) {
        const input = { storageScope: 'workspaces' as const, runtimeUserScope, ...(dataDir === undefined ? {} : { dataDir }) }
        expect(resolveConfig(Config(input))).toMatchObject({ storageScope: 'workspaces', runtimeUserScope })
      }
    }
    expect(() => resolveConfig({ storageScope: 'workspaces', dataDir: 'relative' })).toThrow('absolute')
    expect(() => resolveConfig({ storageScope: 'workspaces', dataDir: '/root\0' })).toThrow('null byte')
    expect(() => resolveConfig({ storageScope: 'unknown' as never })).toThrow('storageScope')
  })
})
