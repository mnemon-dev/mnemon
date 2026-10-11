import { describe, expect, it, vi } from 'vitest'
import { compositionFixture } from './fixtures/composition.ts'

describe('Source-local operation cost', () => {
  it('does not probe unrelated Sources or recompose a View for a local read/write/route', async () => {
    const f = await compositionFixture()
    try {
      const generation = f.graph.memoryComposition.current()!
      const instances = generation.sourceInstances()
      const spies = Object.fromEntries(instances.map(source => [source.sourceTypeId,
        vi.spyOn(generation.sourceRuntime(source.sourceInstanceKey)!, 'facts')]))
      const documents = f.graph.source('documents')
      const created = await documents.mutate<{ document: { id: string } }>('mutate', { action: 'create', title: 'Local operation', content: 'Only this Source.' })
      expect(spies.documents).toHaveBeenCalledTimes(2) // obtain and validate the current mutation fence
      expect(spies.runtime).not.toHaveBeenCalled()
      expect(spies['memory-spaces']).not.toHaveBeenCalled()
      spies.documents!.mockClear()
      await documents.read('document', { id: created.document.id })
      expect(spies.documents).toHaveBeenCalledOnce()
      expect(spies.runtime).not.toHaveBeenCalled()
      expect(spies['memory-spaces']).not.toHaveBeenCalled()
      const scope = { storage: 'custom' as const, workspaceId: f.workspace, agentId: 'root' }
      const turn = await f.graph.composableTurns.beginTurn('read-turn', scope)
      Object.values(spies).forEach(spy => spy.mockClear())
      const read = await f.graph.source('documents', scope).forTurn(turn).route('search', { query: 'Only' })
      expect(read.items).toHaveLength(1)
      Object.values(spies).forEach(spy => expect(spy).not.toHaveBeenCalled())
    } finally { await f.dispose() }
  })
})
