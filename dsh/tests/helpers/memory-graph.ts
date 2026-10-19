import { afterEach, vi } from 'vitest'
import { resolveConfig } from '../../src/host/config.ts'
import { createRuntimeGraph, type MnemonRuntimeGraph } from '../../src/host/runtime.ts'
import { MemoryRuntime } from '../../src/core/runtime.ts'
import { defineMemorySource, defineMemoryStrategy } from 'dsh-mnemon/extension-sdk'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import type { SearchRequest } from 'dsh-mnemon-source-memory-spaces/contracts'
import { DEFAULT_THREE_TIER_VIEW_STRATEGY } from 'dsh-mnemon-strategy-default-three-tier'

const fixtures: Array<{ graph: MnemonRuntimeGraph; extensions: MemoryRuntime }> = []
afterEach(async () => {
  for (const { graph, extensions } of fixtures.splice(0).reverse()) {
    graph.dispose()
    await extensions.dispose()
  }
})

/** Real Composable Runtime and Source grants; only the external data is stubbed. */
export function memoryGraphFixture(initialIds = ['project']) {
  let activeIds = [...initialIds]
  const config = resolveConfig({ cliPath: '/fake/mnemon', storageScope: 'global', writeEnabled: false })
  const extensions = new MemoryRuntime()
  const snapshot = vi.fn(async () => ({
    revision: activeIds.join(','), wake: 'Active Memory Spaces.', state: { memoryBodyIds: [...activeIds] },
  }))
  const search = vi.fn(async (request: SearchRequest) => ({
    query: request.query,
    mode: 'smart',
    results: (request.memoryBodyIds ?? []).map(memoryBodyId => ({
      id: memoryBodyId + ':' + request.query, content: 'Evidence for ' + request.query + ' in ' + memoryBodyId + '.',
      relevanceTier: 'high' as const, memoryBodyId,
    })),
  }))
  extensions.service.installContributions({ sources: [defineMemorySource({
    manifest: {
      apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'memory-spaces',
      packageName: 'dsh-mnemon-source-test-spaces', role: 'fixture', capabilities: ['project', 'search'],
      consistency: 'namespace-pinned-live-read',
      routes: [{ id: 'recall', description: 'Query the dispatch-time namespaces.', capability: 'search',
        inputSchema: { type: 'object', required: ['query'], properties: { query: { type: 'string' }, memoryBodyIds: { type: 'array' }, limit: { type: 'number' }, mode: { type: 'string' } } },
        maxCalls: 2, maxResults: 8, maxCharacters: 8000 }],
      management: { label: 'Test spaces', description: 'In-memory external data fixture.' },
    },
    create(context) {
      return {
        facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'memory-spaces', role: 'fixture',
          availability: 'ready', revision: activeIds.join(','), capabilities: ['project', 'search'], routeIds: ['recall'], actionIds: [] }),
        project: async request => {
          const state = await snapshot()
          return {
            fragments: [{ id: context.sourceInstanceKey + '/cover', sourceInstanceKey: context.sourceInstanceKey,
              mode: request.mode, text: state.wake, revision: state.revision }],
            readGrant: { id: context.sourceInstanceKey + '/grant', sourceInstanceKey: context.sourceInstanceKey,
              schema: 'dsh-mnemon.memory-spaces/v1', value: state.state, revision: state.revision, consistency: 'namespace-pinned-live-read' },
          }
        },
        query: async request => {
          const input = request.input as unknown as SearchRequest
          const grant = request.grant.value as { memoryBodyIds: string[] }
          if (input.memoryBodyIds?.some(id => !grant.memoryBodyIds.includes(id))) throw new Error('outside dispatch grant')
          const result = await search(input)
          return { id: 'evidence:' + input.query, viewId: request.view.id, routeId: request.route.id,
            sourceInstanceKey: context.sourceInstanceKey, observedAt: new Date().toISOString(), truncated: false,
            items: result.results.map(row => ({ id: row.id, text: row.content, provenance: { memoryBodyId: row.memoryBodyId, relevanceTier: row.relevanceTier } })) }
        },
      }
    },
  })] }, { instanceId: 'mnemon-source-memory-spaces' })
  extensions.service.installContributions({ sources: [defineMemorySource({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'documents', packageName: 'test-documents',
      role: 'fixture', capabilities: ['project', 'search'], consistency: 'exact-snapshot',
      management: { label: 'Documents fixture', description: 'Empty public Documents protocol.' },
      routes: [{ id: 'search', description: 'Search fixture documents.', capability: 'search', inputSchema: { type: 'object', required: ['query'] }, maxCalls: 4 }],
    },
    create: context => ({
      facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'documents', role: 'fixture', availability: 'ready', revision: 'empty',
        capabilities: ['project', 'search'], routeIds: ['search'], actionIds: [] }),
      project: () => ({ fragments: [], readGrant: { id: 'documents-grant', sourceInstanceKey: context.sourceInstanceKey,
        schema: 'dsh-mnemon.documents/v1', value: {}, revision: 'empty', consistency: 'exact-snapshot' } }),
      query: request => ({ id: 'documents-evidence', viewId: request.view.id, routeId: request.route.id, sourceInstanceKey: context.sourceInstanceKey,
        observedAt: '2026-08-31T00:00:00.000Z', items: [], truncated: false, metadata: { total: 0 } }),
    }),
  })] }, { instanceId: 'mnemon-source-documents' })
  extensions.service.installContributions({ strategies: [defineMemoryStrategy({
    manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'default-three-tier',
      packageName: 'dsh-mnemon-strategy-test', deterministic: true, supportedSourceRoles: ['fixture'], maxSources: 2, maxRoutes: 2, maxActions: 1 },
    createTurn: DEFAULT_THREE_TIER_VIEW_STRATEGY.createTurn!,
    compose: (_request, sources) => ({ strategyTypeId: 'default-three-tier', explanation: 'Select the test Source.',
      sources: sources.map(source => ({ sourceInstanceKey: source.sourceInstanceKey,
        projection: { mode: 'routed', maxCharacters: 100 }, routeIds: source.routeIds, actionIds: [] })) }),
  })] }, { instanceId: 'test-strategy' })
  const graph = createRuntimeGraph(config, undefined, extensions)
  fixtures.push({ graph, extensions })
  return { graph, extensions, views: graph.composableTurns, search, snapshot, setIds: (ids: string[]) => { activeIds = [...ids] } }
}
