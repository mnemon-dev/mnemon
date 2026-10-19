import { afterEach, describe, expect, it, vi } from 'vitest'
import { join } from 'node:path'
import { AgentMemoryTurn } from '../src/host/agent-memory-turn.ts'
import type { HostAgent } from '../src/host/dsh.ts'
import { createViewHandler } from '../src/host/view-rpc.ts'
import { createRuntimeGraph, LiveMnemonRuntime } from '../src/host/runtime.ts'
import type { MemoryViewConfigurationRequest, MemoryViewPreferences } from '../src/host/view-protocol.ts'
import { viewManagementFixture } from './fixtures/view-management.ts'
import type { RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
import type { Context } from '@deepseek-ai/cordis'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { defineMemoryStrategy, defineMemoryStrategyConfiguration, installMemory } from 'dsh-mnemon/extension-sdk'
import { sessionLog } from './fixtures/session-log.ts'

const fixtures: Awaited<ReturnType<typeof viewManagementFixture>>[] = []
afterEach(async () => { for (const value of fixtures.splice(0)) await value.dispose() })
async function fixture(saved?: MemoryViewPreferences, anchor?: string, stored?: Record<string, object>) {
  const value = await viewManagementFixture(saved, anchor, stored)
  fixtures.push(value)
  return value
}
const runtimeKey = 'source:mnemon-source-runtime'
const documentsKey = 'source:mnemon-source-documents'
const spacesKey = 'source:mnemon-source-memory-spaces'
async function request(f: Awaited<ReturnType<typeof fixture>>, entries: MemoryViewConfigurationRequest['entries'] = {}): Promise<MemoryViewConfigurationRequest> {
  return { expectedRevision: (await f.management.catalog()).revision, strategyTypeId: 'default-three-tier', entries }
}
const scope = (f: Awaited<ReturnType<typeof fixture>>) => ({ storage: 'custom' as const, workspaceId: f.workspace, sessionId: 'root', agentId: 'root' })

describe('View configuration with the real pinned DSH Cordis Loader', () => {
  it('discovers disabled entries through public descriptors without activating or persisting them', async () => {
    const f = await fixture()
    const before = f.engine.contributionSnapshot().revision
    const catalog = await f.management.catalog()
    expect(catalog.entries.map(item => [item.typeId, item.enabled, item.active])).toEqual([
      ['documents', true, true], ['memory-spaces', true, true], ['runtime', true, true], ['default-three-tier', true, true],
      ['auto-capture', false, false], ['light-context', false, false], ['scoped', false, false],
    ])
    expect(catalog.entries.find(entry => entry.typeId === 'light-context')).toMatchObject({
      label: { en: 'Light context', 'zh-CN': '轻量上下文' }, slot: 'projection', writable: true,
      roles: ['strategy-extension'], requires: ['strategy'],
    })
    expect(catalog.entries.find(entry => entry.typeId === 'memory-spaces')).toMatchObject({ roles: ['source'], provides: expect.arrayContaining([{ id: 'source.durable-evidence', exclusive: false }]) })
    expect(f.engine.contributionSnapshot().revision).toBe(before)
    expect(f.settings.mutate).not.toHaveBeenCalled()
    expect(f.treeWrite).not.toHaveBeenCalled()
  })

  it('applies Source and Strategy activation through one graph transaction', async () => {
    const f = await fixture()
    await f.management.apply(f.config, scope(f), await request(f, {
      capture: { enabled: true, config: {} },
    }))
    expect(f.engine.contributionSnapshot().strategyExtensions?.map(value => value.definition.manifest.typeId)).toContain('auto-capture')

    await expect(f.management.apply(f.config, scope(f), await request(f, {
      'mnemon-source-memory-spaces': { enabled: false, config: {} },
    }))).rejects.toThrow('source.durable-evidence')
    expect(f.loader.resolve('mnemon-source-memory-spaces').disabled).toBe(false)

    await f.management.apply(f.config, scope(f), await request(f, {
      'mnemon-source-memory-spaces': { enabled: false, config: {} },
      capture: { enabled: false, config: {} },
    }))
    expect(f.loader.resolve('mnemon-source-memory-spaces').disabled).toBe(true)
    expect(f.engine.contributionSnapshot().sources.some(value => value.provenance.entryId === 'mnemon-source-memory-spaces')).toBe(false)
    expect(f.graph.memoryComposition.inspect().evaluation.state).toBe('ready')
    const turn = await f.graph.composableTurns.beginTurn('without-durable-source', scope(f))
    expect(turn.view.sourcePresentations?.map(value => value.sourceInstanceKey)).not.toContain(spacesKey)
    f.graph.composableTurns.endTurn(turn.turnId)
  })

  it('previews the actual composition read-only, saves through Cordis, and keeps the old turn immutable', async () => {
    const f = await fixture()
    await f.graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: 'Keep this real context. '.repeat(100) })
    const parent = { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
    const owner = new AgentMemoryTurn(parent, f.live)
    await owner.begin(1)
    const current = owner.inspect()!
    const before = await f.graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')
    const revision = f.engine.contributionSnapshot().revision
    const next = await request(f, {
      light: { enabled: true, config: { maxProjectionCharacters: 512 } },
      scoped: { enabled: true, config: { sourceKeys: [runtimeKey, documentsKey, spacesKey], writableSourceKeys: [spacesKey] } },
      capture: { enabled: true, config: {} },
    })
    const preview = await f.management.preview(f.config, scope(f), next)
    expect(preview.state).toBe('preview')
    expect(preview.extensions.map(item => item.slot).sort()).toEqual(['capture', 'projection', 'selection'])
    expect(preview.projection.reduce((sum, item) => sum + item.text.length, 0)).toBeLessThanOrEqual(512)
    expect(preview.sourcePresentations?.map(item => item.sourceInstanceKey)).toEqual([runtimeKey, documentsKey, spacesKey])
    expect(preview.actions.every(action => action.sourceInstanceKey === spacesKey)).toBe(true)
    expect(preview).not.toHaveProperty('readGrants')
    expect(preview.routes.every(route => !('readGrantId' in route))).toBe(true)
    expect(f.engine.contributionSnapshot().revision).toBe(revision)
    expect(await f.graph.source('runtime').read('snapshot')).toMatchObject({ revision: before.revision, entries: before.entries, targets: before.targets })
    expect(owner.inspect()).toEqual(current)
    await f.management.apply(f.config, scope(f), next)
    expect(f.settings.mutate).toHaveBeenCalledOnce()
    expect(f.treeWrite).not.toHaveBeenCalled()
    expect(owner.inspect()).toEqual(current)
    owner.end()
    expect(owner.inspect()?.state).toBe('recent')
    expect(owner.inspect(join(f.root, 'different-workspace'))).toBeUndefined()
    await owner.begin(2)
    const actual = owner.inspect()!
    expect(actual.extensions.map(item => item.typeId)).toEqual(preview.extensions.map(item => item.typeId))
    expect(actual.projection).toEqual(preview.projection)
    expect(actual.memoryText).toBe(preview.memoryText)
    owner.dispose()
    expect(owner.inspect()).toBeUndefined()
  })

  it('changes one extension without disabling other enabled plugins or losing their config', async () => {
    const f = await fixture()
    await f.management.apply(f.config, scope(f), await request(f, {
      light: { enabled: true, config: { maxProjectionCharacters: 700 } },
      capture: { enabled: true, config: { instruction: 'Remember only user-approved preferences.' } },
    }))
    await f.management.apply(f.config, scope(f), await request(f, { light: { enabled: false, config: { maxProjectionCharacters: 700 } } }))
    const catalog = await f.management.catalog()
    expect(catalog.entries.find(item => item.entryId === 'light')).toMatchObject({ enabled: false, config: { maxProjectionCharacters: 700 } })
    expect(catalog.entries.find(item => item.entryId === 'capture')).toMatchObject({ enabled: true, active: true, config: { instruction: 'Remember only user-approved preferences.' } })
    expect(f.treeWrite).not.toHaveBeenCalled()
  })

  it('selects another complete Strategy and can return without uninstalling the previous composition', async () => {
    const f = await fixture()
    const name = 'dsh-mnemon-strategy-empty-view'
    const descriptor = defineMemoryStrategyConfiguration({ kind: 'strategy', typeId: 'empty-view', fields: [],
      label: { en: 'Empty view', 'zh-CN': '空视图' }, description: { en: 'Test-only complete Strategy.', 'zh-CN': '测试策略。' },
      create: () => ({ strategies: [defineMemoryStrategy({ manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'strategy', typeId: 'empty-view', packageName: name,
        deterministic: true, supportedSourceRoles: ['working-context', 'narrative', 'durable-evidence'], maxSources: 3, maxRoutes: 1, maxActions: 1 },
        compose: () => ({ strategyTypeId: 'empty-view', explanation: 'Explicit empty test view.', sources: [] }) })] }),
    })
    f.modules[name] = { name, inject: ['mnemonMemory'], memoryStrategyConfiguration: descriptor, apply: (ctx: Context) => installMemory(ctx, descriptor.create({})) }
    await f.loader.root.update([...f.loader.entries()].map(entry => entry.options).concat({ id: 'empty', name, disabled: true }))
    const discovered = await f.management.catalog()
    expect(discovered.diagnostics).toEqual([])
    expect(discovered.entries.find(entry => entry.entryId === 'empty')).toMatchObject({ typeId: 'empty-view', enabled: false, writable: true })
    await f.management.apply(f.config, scope(f), await request(f, { light: { enabled: true, config: { maxProjectionCharacters: 512 } } }))
    const alternate = { ...await request(f, { empty: { enabled: true, config: {} } }), strategyTypeId: 'empty-view' }
    expect(await f.management.preview(f.config, scope(f), alternate)).toMatchObject({ strategyTypeId: 'empty-view', projection: [], extensions: [] })
    await f.management.apply(f.config, scope(f), alternate)
    const graph = createRuntimeGraph(f.management.resolveConfig(f.config), f.workspace, f.engine)
    try {
      const turn = await graph.composableTurns.beginTurn('alternate', scope(f))
      expect(turn.view.strategyTypeId).toBe('empty-view')
      expect(turn.view.strategyExtensions ?? []).toEqual([])
      graph.composableTurns.endTurn(turn.turnId)
    } finally { graph.dispose() }
    expect(f.loader.resolve('light').disabled).toBe(false)
    expect(f.loader.resolve('mnemon-strategy-default-three-tier').disabled).toBe(false)
    await f.management.apply(f.config, scope(f), await request(f))
    const restored = await f.management.preview(f.config, scope(f), await request(f))
    expect(restored.extensions.map(value => value.typeId)).toEqual(['light-context'])
    expect(f.loader.resolve('empty').disabled).toBe(false)
    expect(f.treeWrite).not.toHaveBeenCalled()
  })

  it('restores saved user choices on fresh Loader entries without editing the bundled tree', async () => {
    const f = await fixture({ strategyTypeId: 'default-three-tier', entries: {
      light: { enabled: true, config: { maxProjectionCharacters: 900 } },
      capture: { enabled: true, config: {} },
    } })
    await vi.waitFor(async () => expect((await f.management.catalog()).entries.filter(entry => entry.roles.includes('strategy-extension') && entry.active)).toHaveLength(2))
    expect(f.loader.resolve('light').options.config).toEqual({ maxProjectionCharacters: 900 })
    expect(f.settings.mutate).not.toHaveBeenCalled()
    expect(f.treeWrite).not.toHaveBeenCalled()
  })

  it('isolates Profiles sharing one settings document, and restores only the matching Loader anchor', async () => {
    const webAnchor = 'file:///isolated/profiles/web/cordis.yml'
    const first = await fixture(undefined, webAnchor)
    await first.management.apply(first.config, scope(first), await request(first, { light: { enabled: true, config: { maxProjectionCharacters: 700 } } }))
    const namespace = first.management.settingsNamespace
    const stored = { [namespace]: first.settingsDocuments.get(namespace)!.value as MemoryViewPreferences }
    const headless = await fixture(undefined, 'file:///isolated/profiles/headless/cordis.yml', stored)
    expect(headless.management.settingsNamespace).not.toBe(namespace)
    expect(headless.loader.resolve('light').disabled).toBe(true)
    const restarted = await fixture(undefined, webAnchor, stored)
    expect(restarted.management.settingsNamespace).toBe(namespace)
    await vi.waitFor(async () => expect((await restarted.management.catalog()).entries.find(entry => entry.entryId === 'light')).toMatchObject({ active: true, config: { maxProjectionCharacters: 700 } }))
  })

  it('reads the previous Source overlay once as migration input without writing a second settings model', async () => {
    const anchor = 'file:///isolated/profiles/legacy/cordis.yml'
    const probe = await fixture(undefined, anchor)
    const suffix = probe.management.settingsNamespace.slice('mnemon-view'.length)
    const restarted = await fixture(undefined, anchor, {
      [`mnemon-plugins${suffix}`]: { sources: { 'mnemon-source-documents': { enabled: false } } },
    })
    await vi.waitFor(async () => expect((await restarted.management.catalog()).entries.find(entry => entry.entryId === 'mnemon-source-documents')).toMatchObject({ enabled: false, active: false }))
    expect(restarted.settings.mutate).not.toHaveBeenCalled()
    expect(restarted.settingsDocuments.get(restarted.management.settingsNamespace)?.value).toEqual({ entries: {} })
    expect(restarted.treeWrite).not.toHaveBeenCalled()
  })

  it('rolls back live registrations if durable settings fail, including pinned and future views', async () => {
    const f = await fixture()
    const id = f.graph.memoryComposition.current()!.id
    vi.mocked(f.settings.mutate).mockImplementationOnce(async () => {
      // Registration updates are buffered; no half-applied Serving generation.
      expect(f.graph.memoryComposition.current()!.id).toBe(id)
      throw new Error('Disk full')
    })
    await expect(f.management.apply(f.config, scope(f), await request(f, {
      light: { enabled: true, config: {} }, capture: { enabled: true, config: {} },
    }))).rejects.toThrow('Disk full')
    expect(f.loader.resolve('light').disabled).toBe(true)
    expect(f.loader.resolve('capture').disabled).toBe(true)
    expect(f.engine.contributionSnapshot().strategyExtensions ?? []).toEqual([])
    const next = await f.graph.composableTurns.beginTurn('after-failure', scope(f))
    expect(next.view.strategyExtensions ?? []).toEqual([])
    expect(next.view.strategyTypeId).toBe('default-three-tier')
    f.graph.composableTurns.endTurn('after-failure')
    expect(f.treeWrite).not.toHaveBeenCalled()
  })

  it('keeps a newly opened workspace on the committed baseline during an in-flight save', async () => {
    const f = await fixture()
    const mutate = f.settings.mutate.bind(f.settings)
    let opened: ReturnType<typeof createRuntimeGraph> | undefined
    vi.mocked(f.settings.mutate).mockImplementationOnce(async (...args) => {
      opened = createRuntimeGraph(f.config, join(f.root, 'new-workspace'), f.engine)
      const before = await opened.composableTurns.beginTurn('during-commit', scope(f))
      expect(before.view.strategyExtensions ?? []).toEqual([])
      opened.composableTurns.endTurn(before.turnId)
      await mutate(...args)
    })
    await f.management.apply(f.config, scope(f), await request(f, { light: { enabled: true, config: {} } }))
    try {
      const after = await opened!.composableTurns.beginTurn('after-commit', scope(f))
      expect(after.view.strategyExtensions?.map(entry => entry.typeId)).toEqual(['light-context'])
      opened!.composableTurns.endTurn(after.turnId)
    } finally { opened?.dispose() }
  })

  it('does not endlessly retry a failing startup overlay through its own rollback events', async () => {
    const f = await fixture({ entries: { light: { enabled: true, config: {} }, capture: { enabled: true, config: {} } } })
    const update = vi.spyOn(f.loader.resolve('capture'), 'update').mockRejectedValue(new Error('Startup plugin failure'))
    await vi.waitFor(async () => expect((await f.management.catalog()).diagnostics.join(' ')).toContain('rollback was incomplete'))
    // One attempt plus the explicit rollback. Its Loader events must not
    // schedule another activation loop against unchanged persisted settings.
    await new Promise(resolve => setTimeout(resolve, 30))
    expect(update).toHaveBeenCalledTimes(2)
    expect(f.loader.resolve('light').disabled).toBe(true)
  })

  it('rolls back earlier entries after a later Cordis activation fails', async () => {
    const f = await fixture()
    vi.spyOn(f.loader.resolve('capture'), 'update').mockRejectedValueOnce(new Error('Plugin activation failed'))
    await expect(f.management.apply(f.config, scope(f), await request(f, {
      light: { enabled: true, config: {} }, capture: { enabled: true, config: {} },
    }))).rejects.toThrow('Plugin activation failed')
    expect(f.loader.resolve('light').disabled).toBe(true)
    expect(f.engine.contributionSnapshot().strategyExtensions ?? []).toEqual([])
    expect(f.settings.mutate).not.toHaveBeenCalled()
  })

  it('rejects stale and simultaneous saves before they can replace a newer choice', async () => {
    const f = await fixture()
    const next = await request(f, { light: { enabled: true, config: {} } })
    const results = await Promise.allSettled([f.management.apply(f.config, scope(f), next), f.management.apply(f.config, scope(f), next)])
    expect(results.map(result => result.status)).toEqual(['fulfilled', 'rejected'])
    expect(f.settings.mutate).toHaveBeenCalledOnce()
    await expect(f.management.preview(f.config, scope(f), next)).rejects.toThrow('changed')
  })

  it('fences read-only and aborted requests without activating an Entry', async () => {
    const f = await fixture()
    const next = await request(f, { light: { enabled: true, config: {} } })
    const abort = new AbortController()
    abort.abort(new Error('Preview cancelled'))
    await expect(f.management.preview(f.config, scope(f), next, abort.signal)).rejects.toThrow('cancelled')
    Object.defineProperty(f.settings, 'writable', { value: false })
    await expect(f.management.apply(f.config, scope(f), next)).rejects.toThrow('read-only')
    expect((await f.management.catalog()).writable).toBe(false)
    expect(f.loader.resolve('light').disabled).toBe(true)
    expect(f.settings.mutate).not.toHaveBeenCalled()
  })

  it('does not partially restore an invalid persisted combination', async () => {
    const f = await fixture({ entries: {
      capture: { enabled: true, config: {} }, light: { enabled: true, config: { maxProjectionCharacters: -1 } },
    } })
    await vi.waitFor(async () => expect((await f.management.catalog()).diagnostics.join(' ')).toContain('Invalid numeric'))
    expect(f.engine.contributionSnapshot().strategyExtensions ?? []).toEqual([])
    expect(f.settings.mutate).not.toHaveBeenCalled()
    expect(f.treeWrite).not.toHaveBeenCalled()
  })

  it('keeps other editors and the real snapshot available when an optional module is broken', async () => {
    const f = await fixture()
    f.modules['dsh-mnemon-strategy-light-context'] = { memoryStrategyConfiguration: { apiVersion: 'dsh-mnemon/strategy-configuration/v1', create: () => ({}), fields: null } }
    const catalog = await f.management.catalog()
    expect(catalog.diagnostics.join(' ')).toContain('light: Invalid plugin configuration descriptor')
    expect(catalog.entries.filter(entry => entry.roles.includes('strategy') || entry.roles.includes('strategy-extension')).map(entry => entry.entryId)).toEqual(['mnemon-strategy-default-three-tier', 'capture', 'scoped'])
    const preview = await f.management.preview(f.config, scope(f), await request(f))
    expect(preview.strategyTypeId).toBe('default-three-tier')
  })

  it.each([
    { light: { enabled: true, config: { maxProjectionCharacters: 0 } } },
    { missing: { enabled: true, config: {} } },
  ])('rejects invalid configuration without changing state (#%#)', async entries => {
    const f = await fixture()
    const revision = f.engine.contributionSnapshot().revision
    await expect(f.management.apply(f.config, scope(f), await request(f, entries as MemoryViewConfigurationRequest['entries']))).rejects.toThrow()
    expect(f.engine.contributionSnapshot().revision).toBe(revision)
    expect(f.settings.mutate).not.toHaveBeenCalled()
  })

  it('rejects an unconfirmed RPC write and never invents a current turn when there is no session', async () => {
    const f = await fixture()
    const handler = createViewHandler(f.live, f.engine, f.management, 'read')
    const writer = createViewHandler(f.live, f.engine, f.management, 'write')
    const config = await request(f, { light: { enabled: true, config: {} } })
    await expect(writer('apply', { workspaceId: 'workspace', configuration: config })).resolves.toMatchObject({ ok: false, error: { message: expect.stringContaining('confirmation') } })
    await expect(handler('apply', { workspaceId: 'workspace', configuration: config, confirmed: true })).resolves.toMatchObject({ ok: false, error: { message: expect.stringContaining('channel') } })
    await expect(handler('dashboard', { workspaceId: 'workspace' })).resolves.toMatchObject({ ok: true, value: { currentUnavailable: 'no-session' } })
    await expect(handler('preview', { workspaceId: 'workspace', configuration: config })).resolves.toMatchObject({ ok: true, value: { state: 'preview' } })
    expect(f.settings.mutate).not.toHaveBeenCalled()
  })

  it('keeps plugin inspection read-only and requires confirmation before DSH installation', async () => {
    const f = await fixture()
    const installation = {
      environment: () => ({ supported: true, profileName: 'web', suggestions: ['dsh-mnemon-strategy-focus'] }),
      registered: () => [],
      inspect: vi.fn(async () => ({ packageName: 'dsh-mnemon-strategy-focus', version: '0.5.0-beta.4', kind: 'strategy', mnemonPeerRange: '^0.5.0', installed: false })),
      install: vi.fn(async () => ({ packageName: 'dsh-mnemon-strategy-focus', version: '0.5.0-beta.4', profileName: 'web', installed: true, restartRequired: true })),
    }
    const reader = createViewHandler(f.live, f.engine, f.management, 'read', undefined, installation as never)
    const writer = createViewHandler(f.live, f.engine, f.management, 'write', undefined, installation as never)
    await expect(reader('inspect-plugin', { packageName: 'dsh-mnemon-strategy-focus' })).resolves.toMatchObject({ ok: true, value: { kind: 'strategy' } })
    await expect(writer('install-plugin', { packageName: 'dsh-mnemon-strategy-focus', version: '0.5.0-beta.4' })).resolves.toMatchObject({ ok: false, error: { message: expect.stringContaining('confirmation') } })
    await expect(writer('install-plugin', { packageName: 'dsh-mnemon-strategy-focus', version: '0.5.0-beta.4', confirmed: true })).resolves.toMatchObject({ ok: true, value: { restartRequired: true } })
    expect(installation.install).toHaveBeenCalledOnce()
    await expect(reader('dashboard', {})).resolves.toMatchObject({ ok: true, value: { pluginInstallation: { supported: true, profileName: 'web' } } })
  })

  it('aligns a listed session whose Agent is not loaded with the workspace DSH lists it under', async () => {
    const f = await fixture()
    const listed = { id: 'workspace', path: f.workspace, title: 'Fixture', sessionIds: ['listed'] }
    const live = new LiveMnemonRuntime(f.graph, { list: () => [listed], get: id => id === 'workspace' ? listed : undefined }, undefined, f.engine)
    try {
      // No loaded Agent, so the lifecycle knows no root for the session.
      const memoryView = vi.fn(() => undefined)
      const lifecycle = { workspaceRoot: () => undefined, memoryView, turnActivities: () => ({ cursor: 0, activities: [] }) }
      const handler = createViewHandler(live, f.engine, f.management, 'read', lifecycle as never)
      await expect(handler('dashboard', { sessionId: 'listed', workspaceId: 'workspace' })).resolves.toMatchObject({ ok: true, value: { currentUnavailable: 'not-generated' } })
      await expect(handler('dashboard', { sessionId: 'listed' })).resolves.toMatchObject({ ok: true, value: { currentUnavailable: 'not-generated' } })
      expect(memoryView.mock.calls).toEqual([['listed', f.workspace], ['listed', f.workspace]])
      // A session no workspace lists stays unaligned with a selected workspace.
      await expect(handler('dashboard', { sessionId: 'elsewhere', workspaceId: 'workspace' })).resolves.toMatchObject({ ok: true, value: { currentUnavailable: 'unaligned' } })
    } finally {
      live.dispose()
    }
  })

  it('keeps durable turn activity beside, rather than inside, the frozen View inspection', async () => {
    const f = await fixture()
    const preview = await f.management.preview(f.config, scope(f), await request(f))
    const current = { ...preview, state: 'active' as const, turn: 7 }
    const activity = { turn: 7, count: 1, names: ['mnemon_document_search'], recalls: 0, writes: 0,
      documentSearches: 1, inspections: 0, failures: 0,
      retrieved: [{ callId: 'read-1', toolName: 'mnemon_document_search', sourceTypeId: 'documents', operationId: 'search',
        items: [{ id: 'doc-1', title: 'Release plan' }] }], writebacks: [] }
    const lifecycle = {
      workspaceRoot: () => f.workspace,
      memoryView: () => current,
      turnActivities: () => ({ cursor: 9, activities: [activity] }),
    }
    const handler = createViewHandler(f.live, f.engine, f.management, 'read', lifecycle as never)
    const response = await handler('dashboard', { sessionId: 'root', workspaceId: 'workspace' })
    expect(response).toMatchObject({ ok: true, value: { current: { id: current.id, turn: 7 }, activity } })
    expect(current).not.toHaveProperty('activity')
  })
})
