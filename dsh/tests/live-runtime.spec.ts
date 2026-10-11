import { existsSync, readFileSync, mkdtempSync, rmSync, symlinkSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
import type { HostAgent, HostWorkspaceRegistry, HostAgentsService, HostContextShape, HostSubagentsService } from '../src/host/dsh.ts'
import { createRuntimeGraph, LiveMnemonRuntime } from '../src/host/runtime.ts'
import { createStorageRoot } from '../src/host/storage-root.ts'
import { resolveConfig } from '../src/host/config.ts'
import { compositionFixture } from './fixtures/composition.ts'
import { createReadHandler, createWriteHandler } from '../src/host/rpc.ts'
import { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { sessionLog } from './fixtures/session-log.ts'

const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
const directories: string[] = []
function directory() { const value = mkdtempSync(join(tmpdir(), 'mnemon-routing-')); directories.push(value); return value }
async function fixture(options: Parameters<typeof compositionFixture>[0] = {}) {
  const value = await compositionFixture(options); fixtures.push(value); return value
}
function agent(id: string, cwd: string): HostAgent {
  return { id, status: 'idle', session: { header: { cwd }, ...sessionLog() }, ctx: { on: vi.fn(), effect: vi.fn() }, followup: vi.fn(), steer: vi.fn(), inject: vi.fn() }
}
afterEach(async () => {
  for (const value of fixtures.splice(0)) await value.dispose()
  for (const value of directories.splice(0)) rmSync(value, { recursive: true, force: true })
  vi.unstubAllEnvs()
})

describe('default Host scope over the Composable Runtime', () => {
  it.each(['global', 'workspace', 'workspaces', 'custom', 'legacy-custom'] as const)('keeps Builtin session-only reads and writes in %s storage through real Sources', async storageScope => {
    const globalRoot = directory(), customRoot = directory()
    vi.stubEnv('MNEMON_DATA_DIR', globalRoot)
    const sessions = [agent('session-1', directory()), agent('session-2', directory())]
    const workspaces = sessions.map((session, index) => ({ id: `workspace-${index + 1}`, title: `Workspace ${index + 1}`, path: session.session.header!.cwd! }))
    const agents = { get: (id: string) => sessions.find(session => session.id === id), roots: () => sessions }
    const config = resolveConfig({
      displayMode: 'builtin', cliPath: '/fake/mnemon', runtimeUserScope: 'storage',
      ...(storageScope === 'legacy-custom' ? {} : { storageScope }),
      ...(storageScope === 'custom' || storageScope === 'legacy-custom' ? { dataDir: customRoot } : {}),
    })
    const value = await compositionFixture(config, {
      agents, workspaceRegistry: { get: id => workspaces.find(workspace => workspace.id === id), list: () => workspaces },
    })
    fixtures.push(value)
    const subagents = { run: vi.fn(), start: vi.fn() }
    const lifecycle = new MnemonLifecycle({ agents } as HostContextShape,
      new MnemonSubagentCoordinator(subagents as unknown as HostSubagentsService, value.live), value.live.config, value.live)
    const read = createReadHandler(value.live, lifecycle)
    const write = createWriteHandler(value.live, lifecycle)
    for (const session of sessions) {
      const expected = createStorageRoot(value.config, session.session.header!.cwd!).effectiveDataDir()
      expect(value.live.route({ sessionId: session.id })).toMatchObject({ selectedRoot: expected, effectiveRoot: expected, aligned: true })
      await expect(write('runtime-memory', { sessionId: session.id, action: 'add', target: 'memory', content: `Memory from ${session.id}` })).resolves.toMatchObject({ ok: true })
      await expect(write('document', { sessionId: session.id, action: 'create', title: `Document from ${session.id}`, content: `# ${session.id}` })).resolves.toMatchObject({ ok: true })
    }
    for (const session of sessions) {
      const expectedSessions = ['workspace', 'workspaces'].includes(storageScope) ? [session] : sessions
      const memory = await read('runtime-memory', { sessionId: session.id })
      expect(memory).toMatchObject({ ok: true, value: { entries: expect.arrayContaining(expectedSessions.map(item => expect.objectContaining({ content: `Memory from ${item.id}` }))) } })
      if (memory.ok) expect((memory.value as { entries: unknown[] }).entries).toHaveLength(expectedSessions.length)
      await expect(read('documents', { sessionId: session.id })).resolves.toMatchObject({ ok: true, value: { activeCount: expectedSessions.length } })
    }
    expect(subagents.run).not.toHaveBeenCalled()
    expect(subagents.start).not.toHaveBeenCalled()
  })

  it.each(['global', 'workspace', 'workspaces'] as const)('routes listed sessions whose Agents are not loaded through their workspaces in %s storage', async storageScope => {
    vi.stubEnv('MNEMON_DATA_DIR', directory())
    const roots = [directory(), directory()]
    // DSH's registry lists each session under its workspace; neither session's Agent is loaded,
    // as after opening a conversation from the list or switching to one.
    const workspaces = roots.map((path, index) => ({ id: `workspace-${index + 1}`, title: `Workspace ${index + 1}`, path, sessionIds: [`session-${index + 1}`] }))
    const agents = { get: (_id: string): HostAgent | undefined => undefined, roots: () => [] }
    const value = await compositionFixture(resolveConfig({ displayMode: 'builtin', cliPath: '/fake/mnemon', storageScope }), {
      agents, workspaceRegistry: { get: id => workspaces.find(workspace => workspace.id === id), list: () => workspaces },
    })
    fixtures.push(value)
    const lifecycle = new MnemonLifecycle({ agents } as unknown as HostContextShape,
      new MnemonSubagentCoordinator({ run: vi.fn(), start: vi.fn() } as unknown as HostSubagentsService, value.live), value.live.config, value.live)
    const read = createReadHandler(value.live, lifecycle)
    const write = createWriteHandler(value.live, lifecycle)
    for (const [index, workspace] of workspaces.entries()) {
      const sessionId = `session-${index + 1}`
      const expected = createStorageRoot(value.config, workspace.path).effectiveDataDir()
      expect(value.live.route({ sessionId })).toMatchObject({
        selectedWorkspace: { id: workspace.id }, effectiveWorkspace: { id: workspace.id }, selectedRoot: expected, effectiveRoot: expected, aligned: true,
      })
      // A manual write needs no conversation Agent: it goes to the Source in the session's workspace.
      await expect(write('document', { sessionId, action: 'create', title: `Document from ${sessionId}`, content: `# ${sessionId}` })).resolves.toMatchObject({ ok: true })
      const catalog = await read('source-management-catalog', { sessionId })
      const documents = catalog.ok ? (catalog.value as { sources: Array<{ sourceTypeId: string; sourceInstanceKey: string; availability: string }> }).sources.find(source => source.sourceTypeId === 'documents') : undefined
      expect(documents).toMatchObject({ availability: 'ready' })
      // The conversation tab's Documents page reads this; it used to fail with no workspace.
      await expect(read('source-management-read', { sessionId, sourceInstanceKey: documents!.sourceInstanceKey, operation: 'snapshot', input: null }))
        .resolves.toMatchObject({ ok: true, value: { value: { workspaceRoot: workspace.path } } })
      await expect(read('status-summary', { sessionId })).resolves.toMatchObject({ ok: true, value: { documents: { workspaceRoot: workspace.path } } })
    }
    // As with loaded Agents: a workspace storage keeps each workspace's Documents apart.
    await expect(read('documents', { sessionId: 'session-2' })).resolves.toMatchObject({ ok: true, value: { activeCount: storageScope === 'global' ? 2 : 1 } })
    expect(value.live.route({ sessionId: 'session-1' }).liveSession).toBe(false)
    // A session no workspace lists still has no workspace.
    expect(value.live.route({ sessionId: 'session-elsewhere' })).not.toHaveProperty('selectedWorkspace')
  })

  it('has one composition and no duplicate controllers, catalog or kernel', async () => {
    const { graph } = await fixture()
    expect(graph.memoryComposition.inspect().evaluation.state).toBe('ready')
    for (const retired of ['service', 'runtimeMemory', 'documents', 'memoryKernel', 'memoryViews', 'memoryTopology', 'runner']) expect(graph).not.toHaveProperty(retired)
    expect((await graph.memoryComposition.current()!.managementCatalog({ storage: 'custom' })).sources.map(source => source.sourceTypeId)).toEqual(['runtime', 'documents', 'memory-spaces'])
  })
  it('supplies existing Runtime capacity settings through Source configuration', async () => {
    const { graph } = await fixture({ runtimeMemory: { memoryLimitBytes: 20_480, userLimitBytes: 10_240, maintenanceMaxTokens: 32_768 } })
    const snapshot = await graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')
    expect(snapshot.targets).toMatchObject({ memory: { limit: 20_480 }, user: { limit: 10_240 } })
    expect(graph.config.runtimeMemory.maintenanceMaxTokens).toBe(32_768)
  })
  it('preserves default settings and storage under DSH include-qualified Entry identities', async () => {
    const value = await compositionFixture({ runtimeMemory: { memoryLimitBytes: 20_480 } }, { entryPrefix: 'profile:include' })
    fixtures.push(value)
    const snapshot = await value.graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')
    expect(snapshot.targets.memory).toMatchObject({ limit: 20_480, markdownPath: join(value.graph.directory, 'runtime', 'MEMORY.md') })
    const catalog = await value.graph.memoryComposition.current()!.managementCatalog({ storage: 'custom' })
    expect(catalog.sources.map(source => source.sourceInstanceKey)).toEqual([
      'source:profile:include:mnemon-source-runtime',
      'source:profile:include:mnemon-source-documents',
      'source:profile:include:mnemon-source-memory-spaces',
    ])
  })
  it('keeps an opt-in global user profile separate from project memory', async () => {
    const global = directory()
    vi.stubEnv('MNEMON_DATA_DIR', global)
    const { graph } = await fixture({ runtimeUserScope: 'global', runtimeMemory: { memoryLimitBytes: 20_480, userLimitBytes: 10_240 } })
    const source = graph.source('runtime')
    await source.mutate('mutate', { action: 'add', target: 'user', content: 'Prefer concise answers.' })
    await source.mutate('mutate', { action: 'add', target: 'memory', content: 'Project uses pnpm.' })
    const snapshot = await source.read<RuntimeMemorySnapshot>('snapshot')
    expect(snapshot.targets.user.markdownPath).toBe(join(global, 'runtime', 'USER.md'))
    expect(snapshot.targets.memory.markdownPath).toBe(join(graph.directory, 'runtime', 'MEMORY.md'))
    expect(snapshot.entries).toHaveLength(2)
  })
  it('keeps root and child Agents on their pinned graph across a live settings swap', async () => {
    const { graph, live, extensions, workspace, config } = await fixture()
    const parent = agent('session', workspace)
    const turn = await graph.composableTurns.beginTurn('session:1', { storage: 'custom', workspaceId: workspace, agentId: parent.id })
    const release = live.bindAgentRuntime(parent.id, graph)
    const next = createRuntimeGraph(resolveConfig({ ...config, defaultRecallLimit: 3 }), workspace, extensions)
    live.swap(next)
    expect(live.snapshot()).toBe(next)
    expect(live.forAgent(parent)).toBe(graph)
    const child = agent('child', workspace)
    child.session.header = { cwd: workspace, origin: 'subagent', parentSession: parent.id }
    expect(live.forAgent(child)).toBe(graph)
    graph.composableTurns.endTurn(turn.turnId)
    release()
    expect(live.forAgent(parent)).toBe(next)
    live.dispose()
    expect(() => live.forAgent(parent)).toThrow('disposed')
  })
  it.each(['workspace', 'workspaces'] as const)('resolves inspection and execution workspaces through the Host registry in %s mode', async storageScope => {
    const one = directory(), two = directory()
    const { graph, extensions } = await fixture({ storageScope })
    const workspaces = [{ id: 'one', title: 'One', path: one }, { id: 'two', title: 'Two', path: two }]
    const registry = { get: (id: string) => workspaces.find(value => value.id === id), list: () => workspaces } satisfies HostWorkspaceRegistry
    const session = agent('session', one)
    const agents = { get: (id: string) => id === session.id ? session : undefined, roots: () => [session] } satisfies Pick<HostAgentsService, 'get' | 'roots'>
    const live = new LiveMnemonRuntime(graph, registry, agents, extensions)
    try {
      expect(live.forAgent(session).directory).toBe(createStorageRoot(graph.config, one).effectiveDataDir())
      expect(live.forWorkspaceId('two').directory).toBe(createStorageRoot(graph.config, two).effectiveDataDir())
      expect(live.route({ workspaceId: 'two', sessionId: session.id })).toMatchObject({
        selectedRoot: createStorageRoot(graph.config, two).effectiveDataDir(), effectiveRoot: createStorageRoot(graph.config, one).effectiveDataDir(), aligned: false,
      })
      expect(live.route({ workspaceId: 'one', sessionId: session.id }).aligned).toBe(true)
      expect(() => live.forWorkspaceId('../../private')).toThrow('selected DSH workspace is unavailable')
    } finally { live.dispose() }
  })
  it.each(['workspace', 'workspaces'] as const)('uses Agent cwd in Headless without a Web workspace registry in %s mode', async storageScope => {
    const workspace = directory()
    const { graph, extensions } = await fixture({ storageScope })
    const session = agent('headless', workspace)
    const live = new LiveMnemonRuntime(graph, undefined, { get: () => session }, extensions)
    try {
      expect(live.forAgent(session).directory).toBe(createStorageRoot(graph.config, workspace).effectiveDataDir())
      expect(live.route({ sessionId: session.id })).toMatchObject({ selectedRoot: createStorageRoot(graph.config, workspace).effectiveDataDir(), effectiveRoot: createStorageRoot(graph.config, workspace).effectiveDataDir(), aligned: true })
    } finally { live.dispose() }
  })
  it('preserves one singleton root for global and custom storage', async () => {
    const global = directory()
    vi.stubEnv('MNEMON_DATA_DIR', global)
    for (const storageScope of ['global', 'custom'] as const) {
      const value = await fixture({ storageScope })
      const session = agent('other-workspace', directory())
      expect(value.live.forAgent(session)).toBe(value.graph)
      if (storageScope === 'global') expect(value.graph.directory).toBe(global)
    }
  })
  it('keeps all eight existing Source enable/disable combinations without adding another topology engine', async () => {
    const ids = ['runtime', 'documents', 'memory-spaces'] as const
    for (let mask = 0; mask < 8; mask++) {
      const { graph, workspace } = await fixture({ memoryTopology: { layers: Object.fromEntries(ids.map((id, i) => [id, { enabled: (mask & (1 << i)) !== 0 }])) } })
      const turn = await graph.composableTurns.beginTurn('fixture:1', { storage: 'custom', workspaceId: workspace })
      for (let i = 0; i < ids.length; i++) {
        const source = 'source:mnemon-source-' + ids[i]
        const exposed = turn.view.projection.some(item => item.sourceInstanceKey === source)
        expect(exposed).toBe((mask & (1 << i)) !== 0)
      }
      graph.composableTurns.endTurn(turn.turnId)
    }
  })
})

describe('centralized workspace persistence', () => {
  it('shares one Source generation for registered aliases of the same workspace', async () => {
    const workspace = directory(), alias = join(directory(), 'alias')
    symlinkSync(workspace, alias, process.platform === 'win32' ? 'junction' : 'dir')
    const workspaces = [{ id: 'real', path: workspace, title: 'Real' }, { id: 'alias', path: alias, title: 'Alias' }]
    const f = await compositionFixture({ storageScope: 'workspaces', dataDir: directory() }, {
      workspaceRegistry: { get: id => workspaces.find(value => value.id === id), list: () => workspaces },
    })
    fixtures.push(f)
    const real = f.live.forWorkspaceId('real'), linked = f.live.forWorkspaceId('alias')
    expect(linked).toBe(real)
    expect(f.live.forAgent(agent('session', alias))).toBe(real)
    await linked.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: 'Shared canonical workspace' })
    expect((await real.source('runtime').read<RuntimeMemorySnapshot>('snapshot')).entries.map(entry => entry.content)).toEqual(['Shared canonical workspace'])
  })

  it.each(['storage', 'global'] as const)('isolates every Source area while USER.md follows %s scope, and preserves roots across switches', async runtimeUserScope => {
    const central = directory(), globalRoot = directory(), one = directory(), two = directory()
    vi.stubEnv('MNEMON_DATA_DIR', globalRoot)
    const workspaces = [{ id: 'one', path: one, title: 'One' }, { id: 'two', path: two, title: 'Two' }]
    const f = await compositionFixture({ storageScope: 'workspaces', dataDir: central, runtimeUserScope }, {
      workspaceRegistry: { get: id => workspaces.find(workspace => workspace.id === id), list: () => workspaces },
    })
    fixtures.push(f)
    const first = f.live.forWorkspaceId('one'), second = f.live.forWorkspaceId('two')
    const firstRuntime = first.source('runtime'), secondRuntime = second.source('runtime')
    await firstRuntime.mutate('mutate', { action: 'add', target: 'memory', content: 'Project One only' })
    await firstRuntime.mutate('mutate', { action: 'add', target: 'user', content: 'Prefer compact answers' })
    await first.source('documents').mutate('mutate', { action: 'create', title: 'One design', content: '# One only' })
    const spaces = first.source('memory-spaces')
    await spaces.mutate('provider-service-update', { providerId: 'holographic', settings: {}, enabled: true })
    const catalog = await spaces.read<{ items: Array<{ id: string; provider: { id: string } }> }>('body-directory')
    const body = catalog.items.find(item => item.provider.id === 'holographic')!
    expect(body).toBeDefined()
    await spaces.mutate('remember', { memoryBodyId: body.id, content: 'Project One durable sentinel' })
    for (const area of ['runtime', 'data', 'documents', 'state']) expect(existsSync(join(first.directory, area)), area).toBe(true)
    const snapshot = await secondRuntime.read<RuntimeMemorySnapshot>('snapshot')
    expect(snapshot.entries.map(entry => entry.content)).toEqual(runtimeUserScope === 'global' ? ['Prefer compact answers'] : [])
    expect(snapshot.targets.user.markdownPath).toBe(join(runtimeUserScope === 'global' ? globalRoot : second.directory, 'runtime', 'USER.md'))
    expect((await second.source('documents').read<{ activeCount: number }>('snapshot')).activeCount).toBe(0)
    expect((await second.source('memory-spaces').read<{ items: unknown[] }>('body-directory')).items).toHaveLength(0)
    expect(existsSync(join(one, '.mnemon'))).toBe(false)
    expect(existsSync(join(two, '.mnemon'))).toBe(false)
    const originalMemory = readFileSync(join(first.directory, 'runtime', 'memories.json'), 'utf8')
    const originalState = readFileSync(join(first.directory, 'state', 'memory-providers.json'), 'utf8')
    const legacy = createRuntimeGraph(resolveConfig({ ...f.config, storageScope: 'custom' }), one, f.extensions)
    f.live.swap(legacy)
    expect((await f.live.forWorkspaceId('one').source('runtime').read<RuntimeMemorySnapshot>('snapshot')).entries.some(entry => entry.content === 'Project One only')).toBe(false)
    f.live.swap(createRuntimeGraph(f.config, one, f.extensions))
    expect((await f.live.forWorkspaceId('one').source('runtime').read<RuntimeMemorySnapshot>('snapshot')).entries.some(entry => entry.content === 'Project One only')).toBe(true)
    expect(readFileSync(join(first.directory, 'runtime', 'memories.json'), 'utf8')).toBe(originalMemory)
    expect(readFileSync(join(first.directory, 'state', 'memory-providers.json'), 'utf8')).toBe(originalState)
    expect(() => f.live.forAgent(agent('missing-cwd', ''))).toThrow('no workspace')
    expect(() => f.live.forWorkspaceId('../../other')).toThrow('unavailable')
  })
})
