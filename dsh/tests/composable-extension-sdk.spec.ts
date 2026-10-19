import { Context } from '@deepseek-ai/cordis'
import { describe, expect, it, vi } from 'vitest'
import { COMPOSABLE_MEMORY_API_VERSION, type MemorySourceDefinition, type MemoryStrategyDefinition } from "../src/core/contracts/index.ts"
import {
  defineMemorySource,
  defineMemoryStrategy,
  installMemory,
} from "../src/sdk/index.ts"
import { MemoryRuntime } from '../src/core/runtime.ts'

function source(typeId = 'example'): MemorySourceDefinition {
  return defineMemorySource({
    manifest: {
      apiVersion: COMPOSABLE_MEMORY_API_VERSION,
      kind: 'source',
      typeId,
      packageName: `dsh-mnemon-source-${typeId}`,
      role: 'external-memory',
      capabilities: ['project'],
      consistency: 'exact-snapshot',
    },
    create: context => ({
      facts: () => ({
        sourceInstanceKey: context.sourceInstanceKey,
        sourceTypeId: typeId,
        role: 'external-memory',
        availability: 'ready',
        revision: 'r1',
        capabilities: ['project'],
        routeIds: [],
        actionIds: [],
      }),
      project: () => ({ fragments: [] }),
    }),
  })
}

function strategy(typeId = 'example'): MemoryStrategyDefinition {
  return defineMemoryStrategy({
    manifest: {
      apiVersion: COMPOSABLE_MEMORY_API_VERSION,
      kind: 'strategy',
      typeId,
      packageName: `dsh-mnemon-strategy-${typeId}`,
      deterministic: true,
      supportedSourceRoles: ['external-memory'],
      maxSources: 4,
      maxRoutes: 4,
      maxActions: 4,
    },
    compose: () => ({ strategyTypeId: typeId, sources: [], explanation: 'Fixture.' }),
  })
}

async function host(): Promise<{ ctx: Context; runtime: MemoryRuntime }> {
  const ctx = new Context()
  const runtime = new MemoryRuntime()
  ctx.provide('mnemonMemory', runtime.service)
  return { ctx, runtime }
}

describe('Composable Memory extension SDK', () => {
  it('publishes an actual narrow, immutable service object instead of the engine', async () => {
    const { ctx, runtime } = await host()
    expect(ctx.mnemonMemory).toBe(runtime.service)
    expect(ctx.mnemonMemory).not.toBe(runtime)
    expect(Object.keys(ctx.mnemonMemory)).toEqual(['installContributions'])
    expect(Object.isFrozen(ctx.mnemonMemory)).toBe(true)
    for (const name of ['dispose', 'attachGeneration', 'contributionSnapshot', 'onContributionsChanged', 'contributions']) {
      expect(name in ctx.mnemonMemory).toBe(false)
    }
    await runtime.dispose()
    expect(() => ctx.mnemonMemory.installContributions({ sources: [source()] }, { instanceId: 'closed' })).toThrow('disposed')
  })

  it('accepts only definitions at the service boundary and validates before changing the registry', async () => {
    const { ctx, runtime } = await host()
    const release = ctx.mnemonMemory.installContributions({ sources: [source('work'), source('personal')] }, {
      instanceId: 'include:notes', effectiveDigest: 'backend:r1', artifactDigest: 'artifact:r1',
    })
    expect(runtime.contributionSnapshot().sources).toMatchObject([
      { instanceKey: 'source:include:notes/work', effectiveDigest: 'backend:r1', provenance: { entryId: 'include:notes', artifactDigest: 'artifact:r1' } },
      { instanceKey: 'source:include:notes/personal' },
    ])
    const snapshot = runtime.contributionSnapshot()
    expect(() => ctx.mnemonMemory.installContributions({ sources: [source()], strategies: [strategy(), strategy()] }, { instanceId: 'invalid-batch' })).toThrow('duplicated')
    expect(() => ctx.mnemonMemory.installContributions({}, { instanceId: 'empty' })).toThrow('one Source, Strategy or Strategy extension')
    expect(() => ctx.mnemonMemory.installContributions({ sources: [source()] }, { instanceId: ' ' })).toThrow('stable Loader Entry')
    expect(runtime.contributionSnapshot()).toEqual(snapshot)
    release()
    release()
    expect(runtime.contributionSnapshot()).toMatchObject({ revision: 2, sources: [], strategies: [] })
    await runtime.dispose()
  })

  it('binds installMemory registration and disposer to the calling Cordis Fiber', async () => {
    const { ctx, runtime } = await host()
    const definition = source()
    const fiber = ctx.plugin({
      name: 'dsh-mnemon-source-example',
      inject: ['mnemonMemory'],
      apply(pluginContext) {
        installMemory(pluginContext, { sources: [definition] }, { instanceId: 'example-work', artifactDigest: 'sha256:fixture' })
      },
    })
    await fiber.await()

    expect(runtime.contributionSnapshot()).toMatchObject({
      revision: 1,
      sources: [{
        instanceKey: 'source:example-work',
        provenance: { packageName: 'dsh-mnemon-source-example', entryId: 'example-work', artifactDigest: 'sha256:fixture' },
      }],
      strategies: [],
    })
    await fiber.dispose()
    expect(runtime.contributionSnapshot()).toMatchObject({ revision: 2, sources: [], strategies: [] })
  })

  it('supports two configured instances of one Source definition without mixing identity', async () => {
    const { ctx, runtime } = await host()
    const definition = source('notion')
    const work = ctx.plugin({ inject: ['mnemonMemory'], apply: child => installMemory(child, { sources: [definition] }, { instanceId: 'notion-work' }) })
    const personal = ctx.plugin({ inject: ['mnemonMemory'], apply: child => installMemory(child, { sources: [definition] }, { instanceId: 'notion-personal' }) })
    await Promise.all([work.await(), personal.await()])
    expect(runtime.contributionSnapshot().sources.map(item => item.instanceKey)).toEqual(['source:notion-work', 'source:notion-personal'])
    await work.dispose()
    expect(runtime.contributionSnapshot().sources.map(item => item.instanceKey)).toEqual(['source:notion-personal'])
    await personal.dispose()
  })

  it('fails loud on duplicate instance identity and preserves the first owner', async () => {
    const { ctx, runtime } = await host()
    const definition = source()
    const first = ctx.plugin({ inject: ['mnemonMemory'], apply: child => installMemory(child, { sources: [definition] }, { instanceId: 'same' }) })
    await first.await()
    const duplicate = ctx.plugin({ inject: ['mnemonMemory'], apply: child => installMemory(child, { sources: [definition] }, { instanceId: 'same' }) })
    await expect(duplicate.await()).rejects.toThrow('already installed')
    expect(runtime.contributionSnapshot().sources).toHaveLength(1)
    await first.dispose()
  })

  it('requires direct child mounts to supply stable identity and supports both roles in one Fiber', async () => {
    const { ctx, runtime } = await host()
    const listener = vi.fn()
    runtime.onContributionsChanged(listener)
    const unidentified = ctx.plugin({ inject: ['mnemonMemory'], apply: child => installMemory(child, { sources: [source()] }) })
    await expect(unidentified.await()).rejects.toThrow('stable Loader Entry id')

    const mixed = ctx.plugin({
      inject: ['mnemonMemory'],
      apply: child => installMemory(child, { sources: [source()], strategies: [strategy()] }, { instanceId: 'mixed' }),
    })
    await mixed.await()
    expect(listener).toHaveBeenCalledOnce()
    expect(runtime.contributionSnapshot()).toMatchObject({ revision: 1,
      sources: [{ instanceKey: 'source:mixed' }], strategies: [{ instanceKey: 'strategy:mixed' }],
    })
    await mixed.dispose()
    expect(listener).toHaveBeenCalledTimes(2)
    expect(runtime.contributionSnapshot()).toMatchObject({ revision: 2, sources: [], strategies: [] })
  })

  it('notifies generation hosts with complete immutable snapshots', async () => {
    const { ctx, runtime } = await host()
    const listener = vi.fn()
    const unsubscribe = runtime.onContributionsChanged(listener)
    const fiber = ctx.plugin({ inject: ['mnemonMemory'], apply: child => installMemory(child, { strategies: [strategy()] }, { instanceId: 'strategy' }) })
    await fiber.await()
    const snapshot = listener.mock.calls[0]![0]
    expect(snapshot).toMatchObject({ revision: 1, strategies: [{ instanceKey: 'strategy:strategy' }] })
    expect(Object.isFrozen(snapshot)).toBe(true)
    await fiber.dispose()
    expect(listener).toHaveBeenCalledTimes(2)
    unsubscribe()
  })

})
