import type { Context } from '@deepseek-ai/cordis'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { defineMemoryStrategy, installMemory } from 'dsh-mnemon/extension-sdk'
import { MEMORY_SPACE_PROVIDER_API_VERSION, defineMemorySpaceProvider, NORMALIZED_RELEVANCE_SCORE, type MemoryProviderDescriptor } from '../src/provider-sdk.ts'

export const descriptor: MemoryProviderDescriptor = {
  id: 'fixture', label: 'Fixture', kind: 'remote', origin: 'third-party', workspaceBinding: 'provider-global',
  summary: 'Provider test double; Source and Cordis lifecycle are real.', fields: [],
  capabilities: { search: true, browse: true, graph: false, entities: false, related: false, remember: true,
    link: false, forget: false, writeMode: 'exact', deletionMode: 'unsupported' },
}

export const provider = defineMemorySpaceProvider<undefined>({
  id: descriptor.id,
  apply(ctx, host) {
    const rows: Array<{ id: string; content: string; score: number }> = []
    host.install(ctx, {
      manifest: { apiVersion: MEMORY_SPACE_PROVIDER_API_VERSION, kind: 'provider', typeId: 'fixture',
        packageName: 'dsh-mnemon-provider-fixture', version: '1.0.0', label: descriptor.label, summary: descriptor.summary,
        origin: descriptor.origin, locality: descriptor.kind, workspaceBinding: descriptor.workspaceBinding,
        capabilities: descriptor.capabilities, fields: [], secrets: [], scoreSemantics: 'normalized-relevance' },
      create: () => ({ id: 'fixture', scoreSemantics: NORMALIZED_RELEVANCE_SCORE,
        discover: async () => [{ externalId: 'notes', name: 'Notes', description: 'Provider namespace', connection: {} }],
        status: async () => ({ healthy: true }),
        list: async () => [...rows], search: async () => ({ results: [...rows] }),
        graph: async () => ({ nodes: [], edges: [], generatedAt: new Date().toISOString() }),
        remember: async (_body, request) => { rows.push({ id: String(rows.length), content: request.content, score: 1 }); return { action: 'stored' } },
      }),
    })
  },
})

export const strategy = {
  inject: ['mnemonMemory'],
  apply(ctx: Context) {
    installMemory(ctx, { strategies: [defineMemoryStrategy({
      manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'test', packageName: 'test-strategy',
        deterministic: true, supportedSourceRoles: ['durable-evidence'], maxSources: 4, maxRoutes: 4, maxActions: 4 },
      compose: (_request, facts) => ({ strategyTypeId: 'test', explanation: 'Explicit test composition.',
        sources: facts.map(source => ({ sourceInstanceKey: source.sourceInstanceKey,
          projection: { mode: 'routed', maxCharacters: 2048 }, routeIds: source.routeIds, actionIds: source.actionIds })) }),
    })] })
  },
}
