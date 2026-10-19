import { mkdtempSync, mkdirSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import * as runtimePlugin from 'dsh-mnemon-source-runtime'
import * as documentsPlugin from 'dsh-mnemon-source-documents'
import * as spacesPlugin from 'dsh-mnemon-source-memory-spaces'
import * as strategyPlugin from 'dsh-mnemon-strategy-default-three-tier'
import native from 'dsh-mnemon-provider-mnemon-native'
import holographic from 'dsh-mnemon-provider-holographic'
import { Context, type Plugin } from '@deepseek-ai/cordis'
import type { MemoryBodyView as MemorySpaceView, MemoryBodyCatalog as MemorySpaceCatalog } from 'dsh-mnemon-source-memory-spaces/contracts'
import type { MemorySpaceProviderEntry } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { resolveConfig, type Config } from '../../src/host/config.ts'
import { createRuntimeGraph, LiveMnemonRuntime } from '../../src/host/runtime.ts'
import type { HostWorkspaceRegistry, HostAgentsService } from '../../src/host/dsh.ts'
import { provideMemoryRuntime } from '../../src/core/runtime.ts'

/** Compose real, public Cordis modules. No Source controller or Host business binding. */
export async function compositionFixture(options: Config = {}, host: {
  workspaceRegistry?: HostWorkspaceRegistry; agents?: Pick<HostAgentsService, 'get'>; providers?: MemorySpaceProviderEntry[]
  entryPrefix?: string
} = {}) {
  const root = mkdtempSync(join(tmpdir(), 'mnemon-composition-'))
  const workspace = join(root, 'workspace')
  mkdirSync(workspace)
  const dataDir = join(root, 'data')
  const config = resolveConfig({ storageScope: 'custom', runtimeUserScope: 'storage', dataDir, cliPath: '/fake/mnemon', ...options })
  // Host-internal tests own a Core engine; independent plugins use the public
  // MemoryCompositionRunner instead. Neither needs an SDK escape hatch.
  const context = new Context()
  const extensions = provideMemoryRuntime(context)
  const entries = new WeakMap<object, string>()
  context.provide('loader', { locate: (fiber: object) => entries.get(fiber) })
  async function mount<C>(plugin: Plugin.Object<C>, entry: { instanceId: string; config?: C }) {
    const bound: Plugin.Object<C | undefined> = { ...plugin, apply(ctx: Context, config: C | undefined) {
      entries.set(ctx.fiber, entry.instanceId)
      return plugin.apply(ctx, config as C)
    } }
    const fiber = context.plugin(bound, entry.config)
    try { await fiber.await() }
    catch (error) { await fiber.dispose(); throw error }
    return () => fiber.dispose()
  }
  const entryId = (id: string) => (host.entryPrefix ? host.entryPrefix + ':' : '') + id
  const releases = [
    await mount(runtimePlugin, { instanceId: entryId('mnemon-source-runtime') }),
    await mount(documentsPlugin, { instanceId: entryId('mnemon-source-documents') }),
    await mount({ inject: ['mnemonMemory'], async apply(ctx: Context) {
      await spacesPlugin.installMemorySpaces(ctx, [
        { instanceId: native.id, module: native, config: undefined },
        { instanceId: holographic.id, module: holographic, config: undefined },
        ...(host.providers ?? []),
      ])
    } }, { instanceId: entryId('mnemon-source-memory-spaces') }),
    await mount(strategyPlugin, { instanceId: entryId('mnemon-strategy-default-three-tier') }),
  ]
  const graph = createRuntimeGraph(config, workspace, extensions)
  const workspaceRegistry = host.workspaceRegistry ?? {
    list: () => [{ id: 'workspace', title: 'Fixture', path: workspace }],
    get: (id: string) => id === 'workspace' ? { id, title: 'Fixture', path: workspace } : undefined,
  }
  const live = new LiveMnemonRuntime(graph, workspaceRegistry, host.agents, extensions)
  async function memorySpace() {
    const source = graph.source('memory-spaces')
    await source.mutate('provider-service-update', {
      providerId: 'holographic', settings: { dataPath: join(config.dataDir ?? dataDir, 'fixture-facts.json') }, enabled: true,
    })
    const catalog = await source.read<MemorySpaceCatalog>('body-directory')
    const body = catalog.items.find(item => item.provider.id === 'holographic')
    if (body === undefined) throw new Error('Fixture Provider did not discover its namespace')
    return body
  }
  async function dispose() {
    live.dispose()
    await context.fiber.dispose()
    rmSync(root, { recursive: true, force: true })
  }
  return { root, workspace, config, mount, extensions, graph, live, releases, memorySpace, dispose }
}
