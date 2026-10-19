import {
  HttpMemoryProvider, MEMORY_SPACE_PROVIDER_API_VERSION, NORMALIZED_RELEVANCE_SCORE, defineMemorySpaceProvider,
} from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import type { MemoryBody, MemorySpace, MemorySpaceAuthority, ProviderBodyStatus, ProviderSpaceStatus } from 'dsh-mnemon-source-memory-spaces/provider-sdk'

/** Compile both published spellings in a consumer outside the workspace. */
export function canonicalMemorySpace(value: MemoryBody, status: ProviderBodyStatus): { space: MemorySpace; status: ProviderSpaceStatus } {
  return { space: value, status }
}

/** Existing Providers may override the legacy protected field as a parameter property. */
export class LegacyHttpProvider extends HttpMemoryProvider {
  readonly id = 'legacy-http-fixture'
  constructor(protected override readonly memoryBodies: MemorySpaceAuthority) { super(memoryBodies) }
  async list() { return [] }
  sharesAuthority(): boolean { return this.memoryBodies === this.memorySpaces }
}

/** Not a Core plugin: each Source-private child owns its own test data plane. */
export default defineMemorySpaceProvider<undefined>({
  id: 'external-fixture',
  apply(ctx, host) {
    const rows: Array<{ id: string; content: string; score: number }> = []
    host.install(ctx, {
      manifest: {
        apiVersion: MEMORY_SPACE_PROVIDER_API_VERSION, kind: 'provider', typeId: 'external-fixture',
        packageName: 'dsh-mnemon-provider-external-fixture', version: '1.0.0', label: 'External fixture', summary: 'Isolated in-memory test Provider.',
        origin: 'third-party', locality: 'remote', workspaceBinding: 'provider-global',
        capabilities: { search: true, browse: true, graph: false, entities: false, related: false, remember: true, link: false, forget: false,
          writeMode: 'exact', deletionMode: 'unsupported' },
        fields: [], secrets: [], scoreSemantics: 'normalized-relevance',
      },
      create: () => ({
        id: 'external-fixture', scoreSemantics: NORMALIZED_RELEVANCE_SCORE,
        discover: async () => [{ externalId: 'notes', name: 'Notes', description: 'An external namespace.', connection: {} }],
        status: async () => ({ healthy: true }), list: async () => [...rows], search: async () => ({ results: [...rows] }),
        graph: async () => ({ nodes: [], edges: [], generatedAt: new Date().toISOString() }),
        remember: async (_body, input) => { rows.push({ id: String(rows.length), content: input.content, score: 1 }); return { action: 'stored' } },
      }),
    })
  },
})
