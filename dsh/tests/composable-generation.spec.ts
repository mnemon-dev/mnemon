import { describe, expect, it, vi } from 'vitest'
import { COMPOSABLE_MEMORY_API_VERSION } from '../src/core/contracts/index.ts'
import type { InstalledMemorySource, InstalledMemoryStrategy, MemoryContributionSnapshot } from '../src/core/contributions.ts'
import { MemoryGenerationHost, defineMemorySource, defineMemoryStrategy } from "../src/core/index.ts"
import { MemoryRuntime } from '../src/core/runtime.ts'

function source(instance: string, dispose: () => void = () => {}, fail = false): InstalledMemorySource {
  const definition = defineMemorySource({
    manifest: {
      apiVersion: COMPOSABLE_MEMORY_API_VERSION,
      kind: 'source',
      typeId: instance,
      packageName: `dsh-mnemon-source-${instance}`,
      role: 'fixture',
      capabilities: ['project'],
      consistency: 'exact-snapshot',
    },
    create: context => {
      if (fail) throw new Error(`factory failed: ${context.sourceInstanceKey}`)
      return {
        facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: instance, role: 'fixture', availability: 'ready' as const, revision: 'r1', capabilities: ['project' as const], routeIds: [], actionIds: [] }),
        project: () => ({ fragments: [] }),
        dispose,
      }
    },
  })
  return {
    kind: 'source',
    instanceKey: `source:${instance}`,
    provenance: { packageName: definition.manifest.packageName, entryId: instance },
    definition,
  }
}

function strategy(instance = 'default'): InstalledMemoryStrategy {
  const definition = defineMemoryStrategy({
    manifest: {
      apiVersion: COMPOSABLE_MEMORY_API_VERSION,
      kind: 'strategy',
      typeId: instance,
      packageName: `dsh-mnemon-strategy-${instance}`,
      deterministic: true,
      supportedSourceRoles: ['fixture'],
      maxSources: 10,
      maxRoutes: 10,
      maxActions: 10,
    },
    compose: () => ({ strategyTypeId: instance, sources: [], explanation: 'Fixture.' }),
  })
  return {
    kind: 'strategy',
    instanceKey: `strategy:${instance}`,
    provenance: { packageName: definition.manifest.packageName, entryId: instance },
    definition,
  }
}

function snapshot(revision: number, sources: InstalledMemorySource[], strategies: InstalledMemoryStrategy[] = [strategy()]): MemoryContributionSnapshot {
  return { revision, sources, strategies }
}

describe('Memory generation lifecycle', () => {
  it('reports cleanup failures even after a Host attachment has detached', async () => {
    const runtime = new MemoryRuntime()
    runtime.installContributions({ sources: [source('failure', () => { throw new Error('provider close failed') })], strategies: [strategy()] })
    const attachment = runtime.attachGeneration()
    await expect(attachment.dispose()).rejects.toThrow('disposal failed')
    await expect(runtime.dispose()).rejects.toThrow('Memory Runtime cleanup failed')
  })
  it('publishes a complete candidate and drains the previous generation after its lease', async () => {
    const firstDisposed = vi.fn()
    const secondDisposed = vi.fn()
    const host = new MemoryGenerationHost()
    expect(host.reconcile(snapshot(1, [{ ...source('stable', firstDisposed), effectiveDigest: 'v1' }])).state).toBe('ready')
    const lease = host.acquire()
    const firstId = lease.id

    expect(host.reconcile(snapshot(2, [{ ...source('stable', secondDisposed), effectiveDigest: 'v2' }])).state).toBe('ready')
    expect(host.inspect()).toMatchObject({
      servingGenerationId: expect.not.stringMatching(firstId),
      drainingGenerationIds: [firstId],
    })
    expect(firstDisposed).not.toHaveBeenCalled()
    lease.release()
    await Promise.resolve()
    expect(firstDisposed).toHaveBeenCalledOnce()
    expect(host.inspect().drainingGenerationIds).toEqual([])
    await host.dispose()
    expect(secondDisposed).toHaveBeenCalledOnce()
  })

  it('preserves Serving when an additive candidate fails', async () => {
    const disposed = vi.fn()
    const host = new MemoryGenerationHost()
    host.reconcile(snapshot(1, [source('stable', disposed)]))
    const stable = host.current()
    const report = host.reconcile(snapshot(2, [source('stable'), source('broken', undefined, true)]))
    expect(report).toMatchObject({ state: 'rejected', diagnostics: [{ message: expect.stringContaining('factory failed') }] })
    expect(host.current()).toBe(stable)
    const lease = host.acquire()
    expect(lease.id).toBe(stable?.id)
    lease.release()
    await host.dispose()
  })

  it('fails closed for new turns after an explicitly required contribution is removed', async () => {
    const disposed = vi.fn()
    const host = new MemoryGenerationHost()
    host.reconcile(snapshot(1, [source('required', disposed)]))
    const lease = host.acquire()

    expect(host.reconcile(snapshot(2, [], [strategy()])).state).toBe('incomplete')
    expect(() => host.acquire()).toThrow('No Memory Source')
    expect(lease.generation.id).toBe(lease.id)
    expect(disposed).not.toHaveBeenCalled()
    lease.release()
    await Promise.resolve()
    expect(disposed).toHaveBeenCalledOnce()
    await host.dispose()
  })

  it('publishes a valid replacement when one of several optional Sources is removed', async () => {
    const firstDisposed = vi.fn()
    const secondDisposed = vi.fn()
    const host = new MemoryGenerationHost()
    expect(host.reconcile(snapshot(1, [source('first', firstDisposed), source('second', secondDisposed)])).state).toBe('ready')
    const previous = host.current()!.id
    expect(host.reconcile(snapshot(2, [source('first')])).state).toBe('ready')
    expect(host.current()?.id).not.toBe(previous)
    expect(host.inspect().servingGenerationId).toBe(host.current()?.id)
    await Promise.resolve()
    expect(firstDisposed).toHaveBeenCalledOnce()
    expect(secondDisposed).toHaveBeenCalledOnce()
    await host.dispose()
  })

  it('does not publish an incomplete initial assembly or infer a Strategy by priority', () => {
    const host = new MemoryGenerationHost()
    expect(host.reconcile(snapshot(1, [], [])).state).toBe('incomplete')
    expect(() => host.acquire()).toThrow('no Serving')
    expect(host.reconcile(snapshot(2, [source('one')], [strategy('one'), strategy('two')]))).toMatchObject({
      state: 'rejected',
      diagnostics: [{ message: expect.stringContaining('exactly one') }],
    })
  })

  it('waits for in-flight leases on shutdown and refuses new acquisitions immediately', async () => {
    const disposed = vi.fn()
    const host = new MemoryGenerationHost()
    host.reconcile(snapshot(1, [source('one', disposed)]))
    const lease = host.acquire()
    const shutdown = host.dispose()
    expect(() => host.acquire()).toThrow('disposed')
    expect(disposed).not.toHaveBeenCalled()
    lease.release()
    await shutdown
    expect(disposed).toHaveBeenCalledOnce()
  })

  it('makes lease release idempotent', async () => {
    const disposed = vi.fn()
    const host = new MemoryGenerationHost()
    host.reconcile(snapshot(1, [source('one', disposed)]))
    const lease = host.acquire()
    host.reconcile(snapshot(2, [source('two')]))
    lease.release()
    lease.release()
    await Promise.resolve()
    expect(disposed).toHaveBeenCalledOnce()
    await host.dispose()
  })
})
