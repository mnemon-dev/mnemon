import { afterEach, describe, expect, it } from 'vitest'
import type { DocumentMutationResult, DocumentSearchResult } from 'dsh-mnemon-source-documents/contracts'
import * as runtimePlugin from 'dsh-mnemon-source-runtime'
import { DEFAULT_MEMORY_VIEW_BUDGET } from '../src/core/index.ts'
import { compositionFixture } from './fixtures/composition.ts'

const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
async function fixture() {
  const current = await compositionFixture()
  fixtures.push(current)
  return { ...current, generation: current.graph.memoryComposition.current()! }
}
afterEach(async () => { for (const value of fixtures.splice(0)) await value.dispose() })
const request = (workspace: string) => ({
  scope: { storage: 'custom' as const, workspaceId: workspace, sessionId: 'session-1', agentId: 'agent-1' },
  scenario: 'test.root-turn', budget: { ...DEFAULT_MEMORY_VIEW_BUDGET },
})

describe('public Source plugin composition', () => {
  it('composes three independently owned Sources through the default Strategy', async () => {
    const { workspace, graph, generation, memorySpace } = await fixture()
    await memorySpace()
    await graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: 'Pinned runtime context', importance: 'critical' })
    await graph.source('documents').mutate('mutate', { action: 'create', title: 'Architecture', content: 'Composable View Memory.' })
    const view = await generation.compose(request(workspace))
    expect(view.strategyTypeId).toBe('default-three-tier')
    expect(view.projection.map(item => item.sourceInstanceKey)).toEqual([
      'source:mnemon-source-runtime', 'source:mnemon-source-documents', 'source:mnemon-source-memory-spaces',
    ])
    expect(view.projection[0]?.text).toContain('Pinned runtime context')
    expect(view.routes.map(route => route.sourceRouteId)).toEqual(expect.arrayContaining(['search', 'recall', 'inspect']))
    expect(view.readGrants.map(grant => grant.schema)).toEqual(['dsh-mnemon.documents/v1', 'dsh-mnemon.memory-spaces/v1'])
    expect(JSON.stringify(view)).not.toContain('apiKey')
  })

  it('pins the Documents namespace while management sees newly created documents', async () => {
    const { workspace, graph, generation } = await fixture()
    const documents = graph.source('documents')
    const first = await documents.mutate<DocumentMutationResult>('mutate', { action: 'create', title: 'First', content: 'grant-token first' })
    const view = await generation.compose(request(workspace))
    const second = await documents.mutate<DocumentMutationResult>('mutate', { action: 'create', title: 'Second', content: 'grant-token second' })
    const route = view.routes.find(route => route.sourceRouteId === 'search')!
    const evidence = await generation.executeRoute(view, route.id, { query: 'grant-token', limit: 20 })
    expect(evidence.items.map(item => item.id)).toContain(first.document.id)
    expect(evidence.items.map(item => item.id)).not.toContain(second.document.id)
    const managed = await documents.read<DocumentSearchResult>('search', { query: 'grant-token' })
    expect(managed.results.map(item => item.id)).toEqual(expect.arrayContaining([first.document.id, second.document.id]))
  })

  it('binds durable write Receipts to the View and reads through pinned Provider namespaces', async () => {
    const { workspace, generation, memorySpace } = await fixture()
    const body = await memorySpace()
    const first = await generation.compose(request(workspace))
    const offer = first.actionOffers.find(item => item.sourceActionId === 'remember')!
    const receipt = await generation.executeAction(first, offer.id, { content: 'provider-marker durable evidence', memoryBodyId: body.id }, () => true)
    expect(receipt).toMatchObject({ viewId: first.id, offerId: offer.id, status: 'succeeded' })
    const next = await generation.compose(request(workspace))
    const route = next.routes.find(item => item.sourceRouteId === 'recall')!
    const evidence = await generation.executeRoute(next, route.id, { query: 'provider-marker', mode: 'keyword', limit: 10 })
    expect(evidence.items.some(item => item.text.includes('provider-marker'))).toBe(true)
    expect(evidence.items.every(item => (item.provenance as { memoryBodyId: string }).memoryBodyId === body.id)).toBe(true)
    await expect(generation.executeRoute(next, route.id, { query: 'provider-marker', memoryBodyIds: ['not-pinned'] })).rejects.toThrow('outside pinned Source')
  })

  it('rejects ambiguous Source roles without inventing precedence', async () => {
    const { workspace, graph, mount, root } = await fixture()
    await mount(runtimePlugin, { instanceId: 'runtime-second', config: { dataDir: root + '/second' } })
    await expect(graph.memoryComposition.current()!.compose(request(workspace))).rejects.toThrow('ambiguous working-context Sources')
  })
})
