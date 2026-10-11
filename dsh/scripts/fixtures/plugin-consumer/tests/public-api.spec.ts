import type { Context } from '@deepseek-ai/cordis'
import { describe, expect, expectTypeOf, it } from 'vitest'
import * as core from 'dsh-mnemon/core'
import * as sdk from 'dsh-mnemon/extension-sdk'
import type { MnemonMemoryService, MemorySourceRuntime } from 'dsh-mnemon/extension-sdk'
import { MemoryCompositionRunner, type MemoryTestTurn } from 'dsh-mnemon/testing'
import * as providerSdk from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { resolveEmbedding, resolvePersistenceStrategy, resolveRecallQuality } from 'dsh-mnemon-source-memory-spaces'
import { createMemorySpaceProviderFixture, mountMemorySpaceProvider } from 'dsh-mnemon-source-memory-spaces/testing'
import provider, { canonicalMemorySpace, LegacyHttpProvider } from '../lib/external-provider.js'
import { memoryPlugin, memoryStrategyConfiguration } from '../lib/external-strategy-extension.js'
// @ts-expect-error The installed SDK must not expose Core's engine.
import type { MemoryRuntime } from 'dsh-mnemon/extension-sdk'
// @ts-expect-error Installed records are not public author contracts.
import type { InstalledMemorySource } from 'dsh-mnemon/contracts'
// @ts-expect-error The Provider SDK must not expose its Source's private host.
import type { PrivateMemorySpaceProviderHost } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

describe('published author API', () => {
  it('resolves Source configuration through the packed package without a Host', () => {
    expect(resolveRecallQuality(undefined)).toMatchObject({ policy: 'strict-v1', candidateMultiplier: 3 })
    expect(resolveEmbedding({ endpoint: ' https://embedding.example.test/// ' }).endpoint).toBe('https://embedding.example.test')
    expect(resolvePersistenceStrategy({ mode: 'manual', rules: { allowedProviderIds: [] } }).rules.allowedProviderIds).toEqual(['mnemon-native'])
  })
  it('compiles and consumes an optional Strategy editor against packed public SDKs', () => {
    expect(memoryPlugin).toMatchObject({ apiVersion: 'dsh-mnemon/plugin/v1', roles: ['strategy-extension'], requires: ['strategy.default-three-tier'] })
    expect(memoryStrategyConfiguration.apiVersion).toBe('dsh-mnemon/strategy-configuration/v1')
    expect(memoryStrategyConfiguration.label.en).toBe('External budget')
    expect(memoryStrategyConfiguration.create({})).toMatchObject({
      plugin: memoryPlugin,
      strategyExtensions: [{ manifest: { typeId: 'external-budget', slot: 'projection' } }],
    })
    expect(() => memoryStrategyConfiguration.create({ unknown: true })).toThrow('not declared')
    expect(() => sdk.defineMemoryStrategyConfiguration({
      ...memoryStrategyConfiguration,
      fields: [{ key: 'budget', input: 'number', label: { en: 'Budget', 'zh-CN': '预算' }, defaultValue: 1.5 }],
    })).toThrow('Invalid numeric')
  })
  it('keeps Context and testing types independent of private engine types', async () => {
    expectTypeOf<Context['mnemonMemory']>().toEqualTypeOf<MnemonMemoryService>()
    expectTypeOf<keyof MnemonMemoryService>().toEqualTypeOf<'installContributions'>()
    type Query = Parameters<NonNullable<MemorySourceRuntime['query']>>[0]
    type Mutation = Parameters<NonNullable<MemorySourceRuntime['mutate']>>[0]
    expectTypeOf<keyof Query['view']>().toEqualTypeOf<'id' | 'scope'>()
    expectTypeOf<keyof Mutation['view']>().toEqualTypeOf<'id' | 'scope'>()
    expectTypeOf<keyof MemoryTestTurn>().toEqualTypeOf<'view' | 'executeRoute' | 'executeAction' | 'release'>()
    expect(Object.keys(core).sort()).toEqual(['apply', 'inject', 'name', 'provide'])
    for (const key of ['MemoryRuntime', 'MemoryContributionRegistry', 'MemoryGenerationHost']) expect(key in sdk).toBe(false)
    const runner = new MemoryCompositionRunner()
    try {
      expect(Object.keys(runner.context.mnemonMemory)).toEqual(['installContributions'])
      expect('runtime' in runner).toBe(false)
      expect('generations' in runner).toBe(false)
      expect(runner.inspect().evaluation.sourceInstanceKeys).toEqual([])
    } finally { await runner.dispose() }
  })

  it('tests a new Provider through the published module fixture, with no parent internals', async () => {
    for (const key of ['PrivateMemorySpaceProviderHost', 'MemorySpaceProviderSnapshot', 'MemoryProviderAdapterRegistry', 'MemoryProviderCatalog']) {
      expect(key in providerSdk).toBe(false)
    }
    const mounted = await mountMemorySpaceProvider(provider, { instanceId: 'account', config: undefined })
    try {
      const { authority, body } = createMemorySpaceProviderFixture(mounted.descriptor, {}, { dataDir: '/unused', instanceId: 'account' })
      expect(canonicalMemorySpace(body, { healthy: true })).toEqual({ space: body, status: { healthy: true } })
      expect(new LegacyHttpProvider(authority).sharesAuthority()).toBe(true)
      const adapter = mounted.createAdapter({ memoryBodies: authority, config: { timeoutMs: 100 } })
      expect(adapter.id).toBe('account')
      expect(mounted.registered).toBe(true)
      await adapter.remember!(body, { content: 'public fixture sentinel' })
      expect((await adapter.search(body, { query: 'sentinel' })).results).toMatchObject([{ content: 'public fixture sentinel' }])
    } finally { await mounted.dispose() }
    expect(mounted.registered).toBe(false)
  })
})
