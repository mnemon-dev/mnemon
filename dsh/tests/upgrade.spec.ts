import { createHash } from 'node:crypto'
import { cpSync, mkdtempSync, readFileSync, readdirSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import type { RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
import type { DocumentSnapshot } from 'dsh-mnemon-source-documents/contracts'
import type { MemoryBodyCatalog as MemorySpaceCatalog } from 'dsh-mnemon-source-memory-spaces/contracts'
import { compositionFixture } from './fixtures/composition.ts'

const original = fileURLToPath(new URL('./fixtures/upgrade/v0.3.6/', import.meta.url))
function inventory(directory: string): Record<string, string> {
  return Object.fromEntries(readdirSync(directory, { recursive: true, withFileTypes: true })
    .filter(entry => entry.isFile()).map(entry => {
      const path = join(entry.parentPath, entry.name)
      return [relative(directory, path), createHash('sha256').update(readFileSync(path)).digest('hex')]
    }))
}

describe('copied pre-extraction storage and user configuration', () => {
  it('reads existing three-tier data, preserves assembly bytes, writes through grants and reopens without a format migration', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-upgrade-'))
    const dataDir = join(directory, 'data-copy')
    cpSync(original, dataDir, { recursive: true })
    const before = inventory(original)
    const options = {
      dataDir, storageScope: 'custom' as const, runtimeUserScope: 'storage' as const,
      displayMode: 'buildin' as const, recallMode: 'guided' as const, writebackMode: 'guided' as const,
      runtimeMemory: { memoryLimitBytes: 20_480, userLimitBytes: 8_192 },
      persistenceStrategy: { mode: 'manual' as const, providerId: 'holographic', rules: { allowedProviderIds: ['holographic'] } },
    }
    let fixture: Awaited<ReturnType<typeof compositionFixture>> | undefined
    try {
      fixture = await compositionFixture(options)
      const { displayMode, ...preservedOptions } = options
      expect(displayMode).toBe('buildin')
      expect(fixture.config).toMatchObject(preservedOptions)
      expect(fixture.config).toHaveProperty('displayMode', 'builtin')
      const { graph, workspace } = fixture
      const turn = await graph.composableTurns.beginTurn('upgrade:1', { storage: 'custom', workspaceId: workspace, agentId: 'upgrade' })
      const wake = graph.composableTurns.memoryWake(turn.view.id)
      expect(wake.text).toContain('Upgrade fixture user prefers concise Chinese answers.')
      expect(wake.text).toContain('Upgrade fixture preserves source-owned data.')
      expect(wake.sections.map(section => section.layerId)).toEqual(['runtime', 'documents', 'memory-spaces'])
      expect(turn.view.diagnostics).toBeUndefined()
      expect(inventory(dataDir)).toEqual(before)

      const snapshot = await graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')
      expect(snapshot.entries).toHaveLength(2)
      expect(snapshot.entries.find(entry => entry.target === 'memory')?.branches).toEqual(['main'])
      expect(snapshot.targets.memory.limit).toBe(20_480)
      expect(snapshot.targets.user.limit).toBe(8_192)
      const documents = await graph.source('documents').read<DocumentSnapshot>('snapshot')
      expect(documents.documents[0]).toMatchObject({ id: '7aad2578-c1ac-404d-9a4d-2c6c090cbdd8', healthy: true, revision: 1 })
      const catalog = await graph.source('memory-spaces').read<MemorySpaceCatalog>('body-directory')
      const body = catalog.items.find(item => item.provider.id === 'holographic')!
      expect(body).toMatchObject({ id: 'holographic-b391de2a35579f6ee0be3c92', active: true, name: 'Legacy facts' })

      const search = turn.view.routes.find(route => route.sourceRouteId === 'search')!
      expect((await graph.composableTurns.executeRoute(turn.turnId, search.id, { query: 'upgradesentinel' })).items[0]?.text)
        .toContain('preserves narrative evidence')
      const recall = turn.view.routes.find(route => route.sourceRouteId === 'recall')!
      expect((await graph.composableTurns.executeRoute(turn.turnId, recall.id, { query: 'upgradesentinel', memoryBodyIds: [body.id] })).items[0]?.text)
        .toContain('keeps existing provider evidence')

      const runtime = turn.view.actionOffers.find(offer => offer.sourceInstanceKey.endsWith('mnemon-source-runtime'))!
      await expect(graph.composableTurns.executeAction(turn.turnId, runtime.id, {
        action: 'add', target: 'memory', content: 'Beta appended without replacing the old records.',
      }, () => true)).resolves.toMatchObject({ status: 'succeeded' })
      const document = turn.view.actionOffers.find(offer => offer.sourceInstanceKey.endsWith('mnemon-source-documents'))!
      await expect(graph.composableTurns.executeAction(turn.turnId, document.id, {
        action: 'update', id: documents.documents[0]!.id, content: '# Upgrade sentinel\n\nupgradesentinel updated through the beta View.',
      }, () => true)).resolves.toMatchObject({ status: 'succeeded' })
      const remember = turn.view.actionOffers.find(offer => offer.sourceActionId === 'remember')!
      await expect(graph.composableTurns.executeAction(turn.turnId, remember.id, {
        content: 'betasentinel appends a second provider fact.', category: 'fact', source: 'user', importance: 5, memoryBodyId: body.id,
      }, () => true)).resolves.toMatchObject({ status: 'succeeded' })
      graph.composableTurns.endTurn(turn.turnId)
      await fixture.dispose()
      fixture = undefined

      fixture = await compositionFixture(options)
      expect((await fixture.graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')).entries).toHaveLength(3)
      expect((await fixture.graph.source('documents').read<DocumentSnapshot>('snapshot')).documents[0])
        .toMatchObject({ revision: 2, healthy: true, id: documents.documents[0]!.id })
      const next = await fixture.graph.composableTurns.beginTurn('upgrade:2', { storage: 'custom', workspaceId: fixture.workspace })
      const nextRecall = next.view.routes.find(route => route.sourceRouteId === 'recall')!
      expect((await fixture.graph.composableTurns.executeRoute(next.turnId, nextRecall.id, { query: 'betasentinel', memoryBodyIds: [body.id] })).items[0]?.text)
        .toContain('appends a second provider fact')
      fixture.graph.composableTurns.endTurn(next.turnId)
      for (const [path, version] of [['runtime/memories.json', 1], ['documents/index.json', 1], ['state/memory-providers.json', 4], ['state/holographic/store.json', 1]] as const) {
        expect(JSON.parse(readFileSync(join(dataDir, path), 'utf8')).version).toBe(version)
      }
      expect(inventory(original)).toEqual(before)
    } finally {
      await fixture?.dispose()
      rmSync(directory, { recursive: true, force: true })
    }
  })
})
