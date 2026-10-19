import { strategy } from './fixture.ts'
import { mkdtempSync, mkdirSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as plugin from '../src/index.ts'

describe('standalone documents Source', () => {
  it('manages create/read/update/search/local-archive within its selected Source store', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-documents-management-'))
    const workspace = join(directory, 'workspace')
    mkdirSync(workspace)
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(strategy, { instanceId: 'strategy' })
      await runner.mount(plugin, { instanceId: 'work', config: { dataDir: join(directory, 'data') } })
      const base = { sourceInstanceKey: 'source:work', scope: { storage: 'custom' as const, workspaceId: workspace }, confirmed: false }
      const initial = await runner.executeManagement({ ...base, mode: 'read', operation: 'snapshot', input: null })
      const added = await runner.executeManagement({ ...base, mode: 'mutate', operation: 'mutate', confirmed: true, expectedRevision: initial.revision,
        input: { action: 'create', title: 'Managed doc', content: 'alpha sentinel' } })
      const { document } = added.value as unknown as plugin.DocumentMutationResult
      await expect(runner.executeManagement({ ...base, mode: 'mutate', operation: 'mutate', confirmed: true, expectedRevision: initial.revision,
        input: { action: 'update', id: document.id, content: 'stale' } })).rejects.toThrow('revision conflict')
      const updated = await runner.executeManagement({ ...base, mode: 'mutate', operation: 'mutate', confirmed: true, expectedRevision: added.revision,
        input: { action: 'update', id: document.id, content: 'beta sentinel' } })
      const loaded = await runner.executeManagement({ ...base, mode: 'read', operation: 'document', input: { id: document.id } })
      expect(loaded.value).toMatchObject({ content: 'beta sentinel' })
      const capacity = await runner.executeManagement({ ...base, mode: 'read', operation: 'capacity-plan', input: { action: 'create', title: 'Plan', content: 'new narrative' } })
      expect(capacity.value).toMatchObject({ fits: true })
      const search = await runner.executeManagement({ ...base, mode: 'read', operation: 'search', input: { query: 'beta' } })
      expect(search.value).toMatchObject({ total: 1 })
      await expect(runner.executeManagement({ ...base, mode: 'mutate', operation: 'archive', confirmed: true, expectedRevision: search.revision,
        input: { id: document.id, documentRevision: 1 } })).rejects.toMatchObject({ code: 'revision-conflict' })
      const archived = await runner.executeManagement({ ...base, mode: 'mutate', operation: 'archive', confirmed: true, expectedRevision: search.revision, input: { id: document.id } })
      expect(archived.value).toMatchObject({ action: 'archived', document: { status: 'archived', memoryBodyIds: [] } })
      const archivedTurn = await runner.beginTurn({ scope: base.scope })
      const archivedEvidence = await archivedTurn.executeRoute(archivedTurn.view.routes[0]!.id, { query: 'beta', includeArchived: true })
      const otherWorkspace = await runner.executeManagement({ ...base, scope: { ...base.scope, workspaceId: directory }, mode: 'read', operation: 'snapshot', input: null })
      // A configured dataDir is one explicit authority, even if a caller's cwd changes.
      expect(otherWorkspace.value).toMatchObject({ total: 1, workspaceRoot: directory })
      await expect(runner.executeManagement({ ...base, scope: { storage: 'custom' }, mode: 'read', operation: 'snapshot', input: null })).rejects.toThrow('unavailable')
    } finally { await runner.dispose(); rmSync(directory, { recursive: true, force: true }) }
  })

  it('owns storage and composes two independent configured instances with no private Host binding', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'mnemon-documents-plugin-'))
    const workspace = join(directory, 'workspace')
    mkdirSync(workspace)
    const runner = new MemoryCompositionRunner()
    try {
      await runner.mount(strategy, { instanceId: 'strategy' })
      await runner.mount(plugin, { instanceId: 'work', config: { dataDir: join(directory, 'work') } })
      await runner.mount(plugin, { instanceId: 'personal', config: { dataDir: join(directory, 'personal') } })
      const first = await runner.beginTurn({ scope: { storage: 'custom', workspaceId: workspace } })
      const offer = first.view.actionOffers.find(value => value.sourceInstanceKey === 'source:work')!
      const receipt = await first.executeAction(offer.id,
        { action: 'create', title: 'Work', content: 'work-only sentinel' }, () => true)
      expect(receipt.status).toBe('succeeded')
      expect(receipt.completion).toBe('committed')
      first.release()
      const next = await runner.beginTurn({ scope: { storage: 'custom', workspaceId: workspace } })
      expect(next.view.sourcePresentations?.find(value => value.sourceInstanceKey === 'source:work')).toMatchObject({
        mode: 'routed', visibleItems: 1, totalItems: 1, items: [{ title: 'Work' }],
      })
      const route = next.view.routes.find(value => value.sourceInstanceKey === 'source:work')!
      const evidence = await next.executeRoute(route.id, { query: 'sentinel' })
      expect(evidence.items).toHaveLength(1)
      const suggestions = await next.executeRoute(route.id, { query: 'unmatched-secret' })
      const personal = next.view.routes.find(value => value.sourceInstanceKey === 'source:personal')!
      expect((await next.executeRoute(personal.id, { query: 'sentinel' })).items).toHaveLength(0)
      next.release()
    } finally {
      await runner.dispose()
      rmSync(directory, { recursive: true, force: true })
    }
  })

  it('fails invalid configuration before publishing a Source', async () => {
    const runner = new MemoryCompositionRunner()
    try {
      await expect(runner.mount(plugin, { instanceId: 'invalid', config: { limitBytes: -1 } })).rejects.toThrow()
      expect(runner.inspect().evaluation.sourceInstanceKeys).toHaveLength(0)
    } finally { await runner.dispose() }
  })
})
