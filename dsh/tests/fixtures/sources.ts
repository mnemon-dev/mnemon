import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryStrategy, installMemory } from 'dsh-mnemon/extension-sdk'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as runtimePlugin from 'dsh-mnemon-source-runtime'
import * as documentsPlugin from 'dsh-mnemon-source-documents'

/** Public plugins under a minimal test Strategy; no private controllers. */
export async function sourceFixture(options: {
  dataDir: string; workspace: string; userDataDir?: string
  memoryLimitBytes?: number; userLimitBytes?: number; documentLimitBytes?: number
}) {
  const runner = new MemoryCompositionRunner()
  await runner.mount({ inject: ['mnemonMemory'], apply(ctx: Context) {
    installMemory(ctx, { strategies: [defineMemoryStrategy({
      manifest: {
        apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'source-test', packageName: 'source-test',
        deterministic: true, supportedSourceRoles: ['working-context', 'narrative'], maxSources: 4, maxRoutes: 4, maxActions: 4,
      },
      compose: (_request, sources) => ({ strategyTypeId: 'source-test', explanation: 'Test management and public Source contracts.',
        sources: sources.map(source => ({ sourceInstanceKey: source.sourceInstanceKey,
          projection: { mode: 'eager', maxCharacters: 2048 }, routeIds: source.routeIds, actionIds: source.actionIds })) }),
    })] })
  } }, { instanceId: 'test-strategy' })
  await runner.mount(runtimePlugin, { instanceId: 'runtime', config: {
    dataDir: options.dataDir, userDataDir: options.userDataDir ?? options.dataDir,
    ...(options.memoryLimitBytes === undefined ? {} : { memoryLimitBytes: options.memoryLimitBytes }),
    ...(options.userLimitBytes === undefined ? {} : { userLimitBytes: options.userLimitBytes }),
  } })
  await runner.mount(documentsPlugin, { instanceId: 'documents', config: {
    dataDir: options.dataDir, ...(options.documentLimitBytes === undefined ? {} : { limitBytes: options.documentLimitBytes }),
  } })
  const scope = { storage: 'custom' as const, workspaceId: options.workspace }
  return {
    runner,
    runtime: await runner.managementClient('source:runtime', scope),
    documents: await runner.managementClient('source:documents', scope),
    dispose: () => runner.dispose(),
  }
}
