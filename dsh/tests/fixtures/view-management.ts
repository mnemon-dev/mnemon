import { mkdtempSync, mkdirSync, realpathSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createRequire } from 'node:module'
import { Context, type Plugin } from '@deepseek-ai/cordis'
import { vi } from 'vitest'
import * as runtime from 'dsh-mnemon-source-runtime'
import * as documents from 'dsh-mnemon-source-documents'
import * as spaces from 'dsh-mnemon-source-memory-spaces'
import holographic from 'dsh-mnemon-provider-holographic'
import * as base from 'dsh-mnemon-strategy-default-three-tier'
import * as scoped from 'dsh-mnemon-strategy-scoped'
import * as light from 'dsh-mnemon-strategy-light-context'
import * as capture from 'dsh-mnemon-strategy-auto-capture'
import * as general from 'dsh-mnemon-strategy-general'
import { provideMemoryRuntime } from '../../src/core/runtime.ts'
import { resolveConfig } from '../../src/host/config.ts'
import { createRuntimeGraph, LiveMnemonRuntime } from '../../src/host/runtime.ts'
import { MemoryPluginManagement, type MemoryPluginLoader, type MemoryPluginLoaderEntry } from '../../src/host/plugin-management.ts'
import type { HostContextShape, HostSettingsService } from '../../src/host/dsh.ts'
import type { MemoryViewPreferences } from '../../src/host/view-protocol.ts'

// Use the Loader shipped with the pinned DSH, not a lifecycle simulator.
const requireDsh = createRequire(realpathSync(new URL('../../node_modules/@deepseek-ai/dsh/package.json', import.meta.url)))
const { Loader } = await import(requireDsh.resolve('@deepseek-ai/cordis-plugin-loader')) as { Loader: Plugin }
interface TestLoader extends MemoryPluginLoader {
  import(name: string): Promise<unknown>
  root: { update(entries: object[]): Promise<void>; stop(): void }
  resolve(id: string): MemoryPluginLoaderEntry
  write(): void
}

/** pluginManager simulates DSH's official manager: it writes the profile row, then reconciles. */
export interface ViewManagementFixtureOptions { pluginManager?: boolean; generalStrategy?: boolean }

export async function viewManagementFixture(saved?: MemoryViewPreferences, anchor?: string, stored: Record<string, object> = {}, options: ViewManagementFixtureOptions = {}) {
  const root = mkdtempSync(join(tmpdir(), 'mnemon-view-management-'))
  const workspace = join(root, 'workspace')
  mkdirSync(workspace)
  const ctx = new Context()
  const engine = provideMemoryRuntime(ctx)
  const loaderFiber = ctx.plugin(Loader, anchor === undefined ? undefined : { baseUrl: anchor })
  await loaderFiber.await()
  const loader = ctx.get('loader') as TestLoader
  const modules: Record<string, unknown> = {
    [runtime.name]: runtime, [documents.name]: documents,
    [spaces.name]: { name: spaces.name, inject: ['mnemonMemory'], apply: (ctx: Context) => spaces.installMemorySpaces(ctx, [{ instanceId: holographic.id, module: holographic, config: undefined }]) },
    [base.name]: base, [scoped.name]: scoped, [light.name]: light, [capture.name]: capture, [general.name]: general,
  }
  loader.import = vi.fn(async name => {
    if (!modules[name]) throw new Error('Unknown fixture module: ' + name)
    return modules[name]
  })
  const settingsDocuments = new Map<string, { value: object; revision: number; validate?: (value: never) => void }>()
  const updateListeners = new Set<(namespace: string, value: unknown) => void>()
  const settings: HostSettingsService = {
    writable: true,
    register: (namespace, _schema, options) => {
      settingsDocuments.set(namespace, { value: structuredClone(stored[namespace] ?? (namespace.startsWith('mnemon-view') && saved ? saved : options.base ?? {})), revision: 0,
        ...(options.validate === undefined ? {} : { validate: options.validate as (value: never) => void }) })
      return { get: () => settingsDocuments.get(namespace)!.value as never }
    },
    describe: () => [...settingsDocuments].map(([ns, document]) => ({ ns, ...document, applies: 'live' as const })),
    mutate: vi.fn(async (namespace, ops, expected) => {
      const current = settingsDocuments.get(namespace)!
      if (expected !== current.revision) throw new Error('Settings changed concurrently')
      const next = structuredClone(current.value) as Record<string, unknown>
      for (const op of ops) {
        if (op.path.length !== 1) throw new Error('Unexpected fixture mutation')
        if (op.op === 'unset') delete next[op.path[0]!]
        else next[op.path[0]!] = structuredClone(op.value)
      }
      current.validate?.(next as never)
      current.value = next
      current.revision += 1
      for (const listener of updateListeners) listener(namespace, next)
    }),
    onUpdated: listener => { updateListeners.add(listener); return () => { updateListeners.delete(listener) } },
  }
  ctx.provide('settings', settings)
  const config = resolveConfig({ storageScope: 'custom', dataDir: join(root, 'data'), cliPath: '/fake/mnemon', runtimeUserScope: 'storage' })
  settings.register('mnemon', {}, { base: config, applies: 'live' })
  const management = new MemoryPluginManagement(ctx as unknown as HostContextShape, engine, settings)
  const stop = management.start()
  const profileEntries: Array<{ id: string; name: string; disabled?: boolean }> = [
    { id: 'mnemon-source-runtime', name: runtime.name },
    { id: 'mnemon-source-documents', name: documents.name },
    { id: 'mnemon-source-memory-spaces', name: spaces.name },
    { id: 'mnemon-strategy-default-three-tier', name: base.name },
    ...(options.generalStrategy ? [{ id: 'general', name: general.name, disabled: true }] : []),
    { id: 'scoped', name: scoped.name, disabled: true },
    { id: 'light', name: light.name, disabled: true },
    { id: 'capture', name: capture.name, disabled: true },
  ]
  const reconcileProfile = async () => {
    await loader.root.update(structuredClone(profileEntries))
    // The public event fires after every entry in a profile replay has settled.
    ;(ctx.emit as (event: string) => void)('app-boot/config-reload')
  }
  const pluginManager = {
    setPluginEnabled: vi.fn(async (entryId: string, enabled: boolean) => {
      const row = profileEntries.find(entry => entry.id === entryId)
      if (row === undefined) return { application: 'failed', error: { message: 'unknown-plugin' } }
      row.disabled = !enabled
      await reconcileProfile()
      return { application: 'applied' }
    }),
  }
  await loader.root.update(structuredClone(profileEntries))
  if (options.pluginManager) ctx.provide('pluginManager', pluginManager)
  const treeWrite = vi.spyOn(loader, 'write')
  const graph = createRuntimeGraph(management.resolveConfig(config), workspace, engine)
  const live = new LiveMnemonRuntime(graph, {
    list: () => [{ id: 'workspace', path: workspace, title: 'Fixture' }],
    get: id => id === 'workspace' ? { id, path: workspace, title: 'Fixture' } : undefined,
  }, undefined, engine)
  const dispose = async () => {
    stop()
    live.dispose()
    await loader.root.stop()
    await ctx.fiber.dispose()
    rmSync(root, { recursive: true, force: true })
  }
  return { ctx, root, workspace, engine, loader, modules, settings, settingsDocuments, management, treeWrite, config, graph, live, profileEntries, reconcileProfile, pluginManager, dispose }
}
