import { describe, expect, it } from 'vitest'
import { COMPOSABLE_MEMORY_API_VERSION } from '../src/core/contracts/index.ts'
import type { InstalledMemoryPlugin, InstalledMemorySource, InstalledMemoryStrategy, MemoryContributionSnapshot } from '../src/core/contributions.ts'
import { MemoryCompositionRunner, defineMemoryPlugin, defineMemorySource, defineMemoryStrategy } from '../src/core/index.ts'

function source(entryId: string, role: string): InstalledMemorySource {
  const definition = defineMemorySource({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: entryId,
      packageName: `dsh-mnemon-source-${entryId}`, role, capabilities: ['project'], consistency: 'exact-snapshot' },
    create: context => ({
      facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: entryId, role, availability: 'ready', revision: 'r1', capabilities: ['project'], routeIds: [], actionIds: [] }),
      project: () => ({ fragments: [] }),
    }),
  })
  return { kind: 'source', instanceKey: `source:${entryId}`, provenance: { packageName: definition.manifest.packageName, entryId }, definition }
}

function strategy(entryId = 'base'): InstalledMemoryStrategy {
  const definition = defineMemoryStrategy({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: entryId,
      packageName: `dsh-mnemon-strategy-${entryId}`, deterministic: true, supportedSourceRoles: ['working-context', 'durable-evidence'], maxSources: 4, maxRoutes: 4, maxActions: 4 },
    compose: () => ({ strategyTypeId: entryId, sources: [], explanation: 'Fixture.' }),
  })
  return { kind: 'strategy', instanceKey: `strategy:${entryId}`, provenance: { packageName: definition.manifest.packageName, entryId }, definition }
}

function plugin(entryId: string, value: Parameters<typeof defineMemoryPlugin>[0]): InstalledMemoryPlugin {
  const descriptor = defineMemoryPlugin(value)
  return { kind: 'plugin', instanceKey: `plugin:${entryId}`, provenance: { packageName: descriptor.packageName, entryId }, descriptor }
}

function snapshot(sources: InstalledMemorySource[], plugins: InstalledMemoryPlugin[]): MemoryContributionSnapshot {
  return { revision: 1, sources, strategies: [strategy()], plugins }
}

describe('memory plugin graph contract', () => {
  it('rejects a missing hard dependency before creating a generation', async () => {
    const work = source('work', 'working-context')
    const result = await new MemoryCompositionRunner().run({ contributions: snapshot([work], [
      plugin('work', { packageName: work.provenance.packageName, label: { en: 'Work', 'zh-CN': '工作' }, description: { en: 'Work.', 'zh-CN': '工作。' }, roles: ['source'], provides: [{ id: 'source.working-context' }] }),
      plugin('base', { packageName: 'dsh-mnemon-strategy-base', label: { en: 'Base', 'zh-CN': '基础' }, description: { en: 'Base.', 'zh-CN': '基础。' }, roles: ['strategy'], provides: [{ id: 'strategy.base' }], requires: ['source.durable-evidence'] }),
    ]) })
    expect(result.report).toMatchObject({ state: 'rejected', diagnostics: [{ message: expect.stringContaining('source.durable-evidence') }] })
  })

  it('rejects two active providers of one exclusive capability', async () => {
    const left = source('left', 'working-context'), right = source('right', 'working-context')
    const result = await new MemoryCompositionRunner().run({ contributions: snapshot([left, right], [
      plugin('left', { packageName: left.provenance.packageName, label: { en: 'Left', 'zh-CN': '左' }, description: { en: 'Left.', 'zh-CN': '左。' }, roles: ['source'], provides: [{ id: 'source.primary', exclusive: true }] }),
      plugin('right', { packageName: right.provenance.packageName, label: { en: 'Right', 'zh-CN': '右' }, description: { en: 'Right.', 'zh-CN': '右。' }, roles: ['source'], provides: [{ id: 'source.primary', exclusive: true }] }),
    ]) })
    expect(result.report).toMatchObject({ state: 'rejected', diagnostics: [{ message: expect.stringContaining('source.primary') }] })
  })

  it('lets a legacy Source satisfy a semantic dependency from its runtime role', async () => {
    const durable = source('legacy', 'durable-evidence')
    const result = await new MemoryCompositionRunner().run({ contributions: snapshot([durable], [
      plugin('base', { packageName: 'dsh-mnemon-strategy-base', label: { en: 'Base', 'zh-CN': '基础' }, description: { en: 'Base.', 'zh-CN': '基础。' }, roles: ['strategy'], provides: [{ id: 'strategy.base' }], requires: ['source.durable-evidence'] }),
    ]) })
    expect(result.report.state).toBe('ready')
    await result.generation?.dispose()
  })
})
