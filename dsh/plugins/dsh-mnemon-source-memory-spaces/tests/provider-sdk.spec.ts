import { describe, expect, expectTypeOf, it, vi } from 'vitest'
import * as sdk from '../src/provider-sdk.ts'
import type { MemorySpaceProviderDefinition, MemorySpaceProviderModule, MemorySpaceProviderHost } from '../src/provider-sdk.ts'
import * as source from '../src/index.ts'
import { createMemorySpaceProviderFixture, mountMemorySpaceProvider } from '../src/testing.ts'
import { descriptor, provider } from './fixture.ts'
// @ts-expect-error Private parent implementations are not Provider author APIs.
import type { PrivateMemorySpaceProviderHost } from '../src/provider-sdk.ts'
// @ts-expect-error Snapshots carry private factories and remain inside the Source.
import type { MemorySpaceProviderSnapshot } from '../src/provider-sdk.ts'

function adapted(change: (definition: MemorySpaceProviderDefinition) => MemorySpaceProviderDefinition): MemorySpaceProviderModule<undefined> {
  return sdk.defineMemorySpaceProvider<undefined>({
    id: provider.id,
    apply(ctx, host) {
      return provider.apply(ctx, { install: (owner, definition) => host.install(owner, change(definition)) }, undefined)
    },
  })
}

describe('Provider SDK and independent module fixture', () => {
  it('exports driver contracts and author tools without parent implementation objects', () => {
    expectTypeOf<keyof MemorySpaceProviderHost>().toEqualTypeOf<'install'>()
    expectTypeOf<ReturnType<typeof source.installMemorySpaces>>().toEqualTypeOf<Promise<void>>()
    for (const key of ['PrivateMemorySpaceProviderHost', 'MemorySpaceProviderSnapshot', 'MemoryAdapterFactoryRegistry', 'MemoryProviderAdapterRegistry', 'MemoryProviderCatalog', 'digest', 'deepFreeze']) {
      expect(key in sdk).toBe(false)
    }
    expect('createMemorySpacesSource' in source).toBe(false)
    expect(sdk.defineMemorySpaceProvider).toBeTypeOf('function')
    expect(sdk.HttpMemoryProvider).toBeTypeOf('function')
    expect(sdk.runProcess).toBeTypeOf('function')
  })

  it('mounts the actual private-child protocol without a global Mnemon service', async () => {
    const released = vi.fn()
    const module = sdk.defineMemorySpaceProvider<undefined>({
      id: provider.id,
      apply(ctx, host) {
        expect(Object.keys(host)).toEqual(['install'])
        for (const service of ['mnemonMemory', 'mnemonMemorySpace', 'mnemonProvider']) expect(ctx.get(service, false)).toBeUndefined()
        ctx.effect(() => released)
        return provider.apply(ctx, host, undefined)
      },
    })
    const mounted = await mountMemorySpaceProvider(module, { instanceId: 'account', config: undefined })
    try {
      expect(mounted.registered).toBe(true)
      expect(Object.keys(mounted).sort()).toEqual(['createAdapter', 'descriptor', 'dispose', 'manifest', 'registered'])
      expect(mounted.descriptor).toMatchObject({ id: 'account', typeId: 'fixture' })
      expect(Object.isFrozen(mounted.manifest.capabilities)).toBe(true)
      expect(Object.isFrozen(mounted.descriptor)).toBe(true)
    } finally { await mounted.dispose() }
    expect(mounted.registered).toBe(false)
    expect(released).toHaveBeenCalledOnce()
  })

  it('tests two identical child ids under independent parents with separate driver state', async () => {
    const first = await mountMemorySpaceProvider(provider, { sourceInstanceId: 'work', instanceId: 'account', config: undefined })
    const second = await mountMemorySpaceProvider(provider, { sourceInstanceId: 'personal', instanceId: 'account', config: undefined })
    const { body, authority } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused', instanceId: 'account' })
    try {
      const work = first.createAdapter({ memoryBodies: authority, config: { timeoutMs: 100 } })
      const personal = second.createAdapter({ memoryBodies: authority, config: { timeoutMs: 100 } })
      expect(work.id).toBe('account')
      await work.remember(body, { content: 'work-only note' })
      expect((await work.search(body, { query: 'note' })).results).toHaveLength(1)
      expect((await personal.search(body, { query: 'note' })).results).toHaveLength(0)
      await first.dispose()
      expect(second.registered).toBe(true)
    } finally { await Promise.all([first.dispose(), second.dispose()]) }
  })

  it('validates registered factory capabilities when creating an adapter', async () => {
    const module = adapted(definition => ({
      ...definition,
      create: context => ({ ...definition.create(context), search: undefined } as unknown as sdk.MemoryProviderAdapter),
    }))
    const mounted = await mountMemorySpaceProvider(module, { config: undefined })
    const { authority } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused' })
    try {
      expect(() => mounted.createAdapter({ memoryBodies: authority, config: { timeoutMs: 100 } })).toThrow('declares search')
    } finally { await mounted.dispose() }
  })

  it('cleans up failed or missing registrations through the real child Fiber', async () => {
    const released = vi.fn()
    const missing = sdk.defineMemorySpaceProvider<undefined>({ id: 'missing', apply(ctx) { ctx.effect(() => released) } })
    await expect(mountMemorySpaceProvider(missing, { config: undefined })).rejects.toThrow('did not install')
    expect(released).toHaveBeenCalledOnce()
    const failed = sdk.defineMemorySpaceProvider<undefined>({ id: provider.id, apply(ctx, host) {
      provider.apply(ctx, host, undefined)
      ctx.effect(() => released)
      throw new Error('child failed after registration')
    } })
    await expect(mountMemorySpaceProvider(failed, { config: undefined })).rejects.toThrow('child failed after registration')
    expect(released).toHaveBeenCalledTimes(2)
  })

  it('owns created adapter cleanup and prevents creation after disposal begins', async () => {
    const disposeAdapter = vi.fn(async () => undefined)
    const module = adapted(definition => ({ ...definition, create: context => ({ ...definition.create(context), dispose: disposeAdapter }) }))
    const mounted = await mountMemorySpaceProvider(module, { config: undefined })
    const { authority } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused' })
    mounted.createAdapter({ memoryBodies: authority, config: { timeoutMs: 100 } })
    const closing = mounted.dispose()
    expect(mounted.dispose()).toBe(closing)
    expect(() => mounted.createAdapter({ memoryBodies: authority, config: { timeoutMs: 100 } })).toThrow('disposed')
    await closing
    expect(disposeAdapter).toHaveBeenCalledOnce()
    expect(mounted.registered).toBe(false)
  })

  it('reports adapter cleanup failures without leaving a child registered', async () => {
    const module = adapted(definition => ({ ...definition, create: context => ({ ...definition.create(context), dispose() { throw new Error('cleanup failed') } }) }))
    const mounted = await mountMemorySpaceProvider(module, { config: undefined })
    const { authority } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused' })
    mounted.createAdapter({ memoryBodies: authority, config: { timeoutMs: 100 } })
    await expect(mounted.dispose()).rejects.toThrow('cleanup failed')
    expect(mounted.registered).toBe(false)
  })
})
