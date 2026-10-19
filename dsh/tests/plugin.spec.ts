import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { Context } from '@deepseek-ai/cordis'
import { HostConnectionService } from '@deepseek-ai/dsh-client-connection'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ToolDefinition, HostAgent } from "../src/host/dsh.ts"
import { apply, inject } from '../src/index.ts'
import { MnemonSubagentCoordinator } from "../src/host/subagent.ts"
import { registerTools } from "../src/host/tools.ts"
import { MemoryRuntime } from '../src/core/runtime.ts'
import type { MnemonMemoryService } from 'dsh-mnemon/extension-sdk'
import { compositionFixture } from './fixtures/composition.ts'
import { agentScope } from '../src/host/runtime.ts'
import { sessionLog } from './fixtures/session-log.ts'

const manifest = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8')) as {
  dsh: { client: { inject: string[]; platform: string } }
  devDependencies: Record<string, string>
  engines: { node: string }
  peerDependencies: Record<string, string>
}
const lockfile = readFileSync(new URL('../pnpm-lock.yaml', import.meta.url), 'utf8')
const workspaceConfig = readFileSync(new URL('../pnpm-workspace.yaml', import.meta.url), 'utf8')
const bundlePatch = readFileSync(new URL('../cordis.patch.yml', import.meta.url), 'utf8')

const directories: string[] = []

const releases: Array<() => unknown> = []
afterEach(async () => {
  for (const release of releases.splice(0).reverse()) await release()
  vi.restoreAllMocks()
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

function dataDir(): string {
  const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-plugin-'))
  directories.push(directory)
  return directory
}

function context(options: { connection?: boolean; workspaceRegistry?: boolean } = {}) {
  const tools: unknown[] = []
  const sections: unknown[] = []
  const contexts: unknown[] = []
  const variables: unknown[] = []
  const channels: unknown[][] = []
  const connection = {
    rpc: {
      handle: vi.fn((...args: unknown[]) => { channels.push(args) }),
    },
  }
  const commands: unknown[] = []
  const listeners: unknown[] = []
  const effectCleanups: Array<() => unknown> = []
  const services = new Map<string, unknown>()
  // DSH 0.1.7 profile settings: forms over the owning Entry, edited through ConfigEditor.
  const entry = { id: 'mnemon', options: { id: 'mnemon', config: {} } }
  const fiber: { entry: typeof entry; config: unknown } = { entry, config: undefined }
  const editor = { entries: () => [entry] }
  const forms = {
    writable: true,
    configure: vi.fn(() => () => {}),
    describe: vi.fn((): unknown[] => []),
    mutate: vi.fn(async (..._args: unknown[]) => {}),
  }
  const ctx = {
    fiber,
    provide: vi.fn((name: string, value: unknown) => { services.set(name, value) }),
    tools: { register: vi.fn((tool: unknown) => {
      tools.push(tool)
      return vi.fn(() => { const index = tools.indexOf(tool); if (index >= 0) tools.splice(index, 1) })
    }) },
    commands: { register: vi.fn((command: unknown) => { commands.push(command) }) },
    settings: forms,
    agents: { get: vi.fn(), roots: vi.fn(() => []) },
    subagents: {
      list: vi.fn(() => ['spawn']),
      getProvider: vi.fn(() => ({ capabilities: { outputSchema: true, depthLimit: true, toolFilter: true, persona: true } })),
      start: vi.fn(),
    },
    get: vi.fn((name: string) => {
      if (name === 'systemPrompt') {
        return {
          section: (section: unknown) => { sections.push(section) },
          context: (context: unknown) => { contexts.push(context) },
          variable: vi.fn((...args: unknown[]) => { variables.push(args) }),
        }
      }
      if (name === 'workspaceRegistry' && 'workspaceRegistry' in ctx) return ctx.workspaceRegistry
      if (name === 'configEditor') return editor
      return services.get(name)
    }),
    inject: vi.fn((services: string[], callback: (value: unknown) => void) => {
      if (services.includes('connection') && !('connection' in ctx)) return
      callback(ctx)
    }),
    on: vi.fn((...args: unknown[]) => { listeners.push(args); return () => {} }),
    effect: vi.fn((callback: () => unknown) => {
      const cleanup = callback()
      if (typeof cleanup === 'function') effectCleanups.push(cleanup as () => unknown)
      return () => {
        const index = effectCleanups.indexOf(cleanup as () => unknown)
        if (index >= 0) effectCleanups.splice(index, 1)
        if (typeof cleanup === 'function') cleanup()
      }
    }),
  }
  if (options.connection !== false) Object.assign(ctx, { connection })
  if (options.workspaceRegistry !== false) Object.assign(ctx, { workspaceRegistry: { get: vi.fn(), list: vi.fn(() => []) } })
  releases.push(async () => { for (const cleanup of effectCleanups.splice(0).reverse()) await cleanup() })
  /** DSH validates a candidate through the owner's config waterfall before committing it. */
  const preflight = (candidate: object): unknown => {
    const listener = (listeners as Array<[string, (this: unknown, raw: unknown, next: () => unknown) => unknown]>).find(([name]) => name === 'internal/config')![1]
    return listener.call(fiber, undefined, () => candidate)
  }
  /** Commit a live profile edit the way the Loader publishes volatile updates. */
  const commit = (config: object): void => {
    fiber.config = config
    ;(listeners as Array<[string, (paths: readonly (readonly string[])[]) => void]>).find(([name]) => name === 'loader/volatile-update')![1]([[]])
  }
  return { ctx, tools, sections, contexts, variables, channels, commands, listeners, effectCleanups, forms, fiber, preflight, commit }
}

async function installStarter(target: ReturnType<typeof context>) {
  const assembled = await compositionFixture()
  releases.push(assembled.dispose)
  const core = target.ctx.get('mnemonMemory') as MnemonMemoryService
  const installed = assembled.extensions.contributionSnapshot()
  for (const item of [...installed.sources, ...installed.strategies]) {
    releases.push(core.installContributions(item.kind === 'source' ? { sources: [item.definition] } : { strategies: [item.definition] }, {
      instanceId: item.provenance.entryId,
      ...(item.provenance.artifactDigest === undefined ? {} : { artifactDigest: item.provenance.artifactDigest }),
      ...(item.kind !== 'source' || item.effectiveDigest === undefined ? {} : { effectiveDigest: item.effectiveDigest }),
    }))
  }
  return core
}

describe('dsh-mnemon plugin composition', () => {
  it('registers and disposes Web RPC routes after the official Connection is already running', async () => {
    const root = new Context()
    releases.push(() => root.fiber.dispose())
    const stub = context()
    const routes = new Map<string, unknown>()
    await root.plugin({ apply: ctx => {
      ctx.provide('webServer', { register: (route: { path: string }) => {
        routes.set(route.path, route)
        return () => { routes.delete(route.path) }
      } })
    } })
    for (const name of ['tools', 'commands', 'settings', 'agents', 'subagents'] as const) root.provide(name, stub.ctx[name])
    root.provide('webRuntime', {})
    // Official Web profile scopes Connection to webRuntime; Mnemon must be
    // installable after this fiber has already started, without restarting it.
    await root.plugin({ inject: ['webRuntime'], apply: ctx => { new HostConnectionService(ctx, [], { isAuthenticated: () => true } as never) } })
    const host = await root.plugin({ inject, apply: ctx => apply(ctx, { dataDir: dataDir() }) })
    const connectionFiber = [...root.registry.values()].flatMap(runtime => [...runtime.fibers])
      .find(fiber => fiber.parent === host.ctx && Object.hasOwn(fiber.inject, 'connection'))
    expect(connectionFiber).toBeDefined()
    await connectionFiber!.await()
    expect(routes.size).toBe(7)
    expect(routes.has('/dsh-mnemon-read')).toBe(true)
    await host.dispose()
    expect(routes.size).toBe(0)
  })

  it('keeps the installed DSH release family coherent', () => {
    // Every direct DSH package and locked DSH package follows one release;
    // peer ranges start there and also admit the DSH `next` release line.
    const baseline = manifest.devDependencies['@deepseek-ai/dsh']
    const directDshDependencies = Object.entries(manifest.devDependencies)
      .filter(([name]) => name === '@deepseek-ai/dsh' || name.startsWith('@deepseek-ai/dsh-'))
    const lockedDshVersions = [...lockfile.matchAll(/(@deepseek-ai\/dsh(?:-[a-z0-9-]+)?)@(\d+\.\d+\.\d+(?:-(?:alpha|rc)\.\d+)?)(?=[:'(_\s])/g)]
      .map(match => match[2])

    expect(baseline).toMatch(/^\d+\.\d+\.\d+(?:-(?:alpha|rc)\.\d+)?$/)
    expect(new Set(directDshDependencies.map(([, version]) => version))).toEqual(new Set([baseline]))
    expect(manifest.engines.node).toBe('>=20')
    for (const name of ['@deepseek-ai/dsh-client-ui-primitives', '@deepseek-ai/dsh-typert-protocol']) {
      expect(manifest.peerDependencies[name]).toBe(`^${baseline} || ^0.2.0-rc.1`)
    }
    expect(lockedDshVersions.length).toBeGreaterThan(100)
    expect(new Set(lockedDshVersions)).toEqual(new Set([baseline]))
    expect(workspaceConfig).not.toMatch(/^  - ['"]@deepseek-ai\/\*/m)
  })

  it('keeps Web-only workspace and connection services out of its core dependencies', () => {
    expect(inject).toEqual(['tools', 'settings', 'commands', 'agents', 'subagents'])
  })

  it('owns legacy display migration at startup, on external edits and until Host disposal', async () => {
    const fixture = context({ connection: false })
    const base = { cliPath: '/fake/mnemon', dataDir: dataDir(), displayMode: 'buildin' as const }
    let user: Record<string, unknown> = {}
    let revision = 1
    const mutate = vi.fn(async (namespace: string, ops: Array<{ path: string[]; value: unknown }>, expected: number) => {
      expect(namespace).toBe('mnemon')
      expect(expected).toBe(revision)
      expect(ops).toEqual([{ op: 'set', path: ['displayMode'], value: 'builtin' }])
      user = { ...user, displayMode: ops[0]!.value }
      revision += 1
    })
    Object.assign(fixture.ctx.settings, {
      writable: true,
      describe: () => [{ ns: 'mnemon', base, user, value: { ...base, ...user }, revision, applies: 'live' }],
      mutate,
    })
    apply(fixture.ctx as never, base)
    await vi.waitFor(() => expect(mutate).toHaveBeenCalledTimes(1))
    expect(user).toEqual({ displayMode: 'builtin' })
    const updated = (fixture.listeners as Array<[string, (namespace: string) => void]>)
      .find(([name]) => name === 'settings/document-updated')![1]
    user = { displayMode: 'buildin', timeoutMs: 25000 }
    revision += 1
    updated('unrelated')
    expect(mutate).toHaveBeenCalledTimes(1)
    updated('mnemon')
    await vi.waitFor(() => expect(mutate).toHaveBeenCalledTimes(2))
    expect(user).toEqual({ displayMode: 'builtin', timeoutMs: 25000 })
    updated('mnemon')
    expect(mutate).toHaveBeenCalledTimes(2)
    for (const cleanup of fixture.effectCleanups.splice(0).reverse()) await cleanup()
    user = { displayMode: 'buildin' }
    revision += 1
    updated('mnemon')
    expect(mutate).toHaveBeenCalledTimes(2)
    expect(user).toEqual({ displayMode: 'buildin' })
  })

  it('mounts its complete Agent surface without Web-only Host services', () => {
    const fixture = context({ connection: false, workspaceRegistry: false })
    apply(fixture.ctx as never, { cliPath: '/fake/mnemon', dataDir: dataDir() })

    expect(fixture.tools).toHaveLength(17)
    expect(fixture.sections).toEqual([expect.objectContaining({ name: 'mnemon:routing' })])
    expect(fixture.contexts).toEqual([])
    expect(fixture.variables).toEqual([])
    expect(fixture.commands).toEqual([expect.objectContaining({ name: 'mnemon' })])
    expect(fixture.channels).toEqual([])
  })

  it('discovers a Web workspace registry that becomes available after core activation', async () => {
    const fixture = context({ workspaceRegistry: false })
    const workspace = dataDir()
    apply(fixture.ctx as never, { cliPath: '/fake/mnemon', storageScope: 'workspace' })
    await installStarter(fixture)
    Object.assign(fixture.ctx, {
      workspaceRegistry: {
        get: (id: string) => id === 'late-workspace' ? { id, title: 'Late Workspace', path: workspace } : undefined,
        list: () => [{ id: 'late-workspace', title: 'Late Workspace', path: workspace }],
      },
    })

    const readRegistration = fixture.channels.find(channel => (channel as unknown[])[0] === '/dsh-mnemon-read') as [
      string,
      (endpoint: string, payload: unknown) => Promise<{ ok: boolean; value?: { directory: string } }>,
    ]
    await expect(readRegistration[1]('runtime-memory', { workspaceId: 'late-workspace' })).resolves.toMatchObject({
      ok: true,
      value: { directory: join(workspace, '.mnemon', 'runtime') },
    })
  })

  it('exports a DSH Web client with its ordering dependencies', () => {
    expect(manifest.dsh.client).toEqual({
      inject: [
        '@deepseek-ai/dsh-client-connection',
        '@deepseek-ai/dsh-client-ui-conversation',
        '@deepseek-ai/dsh-client-ui-layout',
        '@deepseek-ai/dsh-client-ui-sidebar',
        '@deepseek-ai/dsh-client-ui-renderer',
        '@deepseek-ai/dsh-client-ui-settings',
        '@deepseek-ai/dsh-client-ui-session',
        '@deepseek-ai/dsh-client-locale',
      ],
      platform: 'web',
    })
  })

  it('anchors client discovery on the Host Entry while the Starter declares every plugin', () => {
    expect(bundlePatch).toMatch(/- id: mnemon\n(?:\s+#.*\n)*\s+name: dsh-mnemon\n/u)
    expect(bundlePatch).not.toContain('bundledContributions')
    expect(bundlePatch).not.toContain('name: dsh-mnemon/core')
  })

  it('registers the full tool surface, guidance, and split RPC channels', async () => {
    const fixture = context()
    apply(fixture.ctx as never, { cliPath: '/fake/mnemon', dataDir: dataDir() })
    expect(fixture.tools.map(tool => (tool as { name: string }).name)).toEqual([
      'mnemon_subagent_result',
      'mnemon_view_route',
      'mnemon_view_action',
      'mnemon_memory_bodies',
      'mnemon_recall',
      'mnemon_related',
      'mnemon_status',
      'mnemon_document_search',
      'mnemon_document_create',
      'mnemon_document_manage',
      'mnemon_runtime_memory',
      'mnemon_remember',
      'mnemon_link',
      'mnemon_forget',
      'mnemon_memory_body_create',
      'mnemon_memory_body_update',
      'mnemon_memory_body_merge',
    ])
    expect(fixture.tools).toEqual(expect.arrayContaining([
      expect.objectContaining({ output: expect.objectContaining({ schema: { type: 'object', additionalProperties: true } }) }),
    ]))
    expect(fixture.tools.every(tool => (tool as { output: { schema: { type: string } } }).output.schema.type !== 'json')).toBe(true)
    const recallTool = fixture.tools.find(tool => (tool as { name: string }).name === 'mnemon_recall') as { description: string }
    expect(recallTool.description).toContain('one initial query plus one LLM-chosen different-query refinement')
    expect(recallTool.description).toContain('only when the current question needs history')
    expect(fixture.sections).toEqual([expect.objectContaining({ name: 'mnemon:routing' })])
    expect(fixture.contexts).toEqual([])
    expect(fixture.variables).toEqual([])
    const guidance = (fixture.sections[0] as { text: () => string }).text()
    expect(guidance).toContain('mnemon_view_route')
    expect(guidance).toContain('Never infer missing historical facts')
    expect(guidance).not.toMatch(/Documents|mnemon_recall|mnemon_runtime_memory/)
    expect(guidance.length).toBeLessThan(360)
    expect(guidance).not.toContain('RECALL RESULT')
    expect(fixture.commands).toEqual([expect.objectContaining({ name: 'mnemon' })])
    expect(fixture.channels.map(([channel]) => channel)).toEqual(expect.arrayContaining(['/dsh-mnemon-activation', '/dsh-mnemon-pack']))
    expect(fixture.channels).toHaveLength(7)
    // The settings bridge serves each Mnemon namespace from the owning profile Entry.
    fixture.forms.describe.mockReturnValue([{ ns: 'mnemon', value: { conversationInteraction: { turnBar: true, saveAction: false } }, base: {}, user: {}, revision: 3, applies: 'live' }])
    const settingsChannel = fixture.channels.find(([channel]) => channel === '/dsh-mnemon-settings')![1] as (endpoint: string, payload: unknown) => Promise<unknown>
    await expect(settingsChannel('get', { namespace: 'mnemon-ui' })).resolves.toMatchObject({
      ok: true, value: { status: 'ready', revision: 3, writable: true, value: { turnBar: true, saveAction: false } },
    })
  })

  it('keeps stable live surfaces while fencing every mutation in read-only mode', async () => {
    const fixture = context()
    apply(fixture.ctx as never, { cliPath: '/fake/mnemon', dataDir: dataDir(), writeEnabled: false })
    expect(fixture.tools).toHaveLength(17)
    const runtimeTool = fixture.tools.find(tool => (tool as { name: string }).name === 'mnemon_runtime_memory') as {
      execute: (args: unknown, execution: unknown) => Promise<unknown>
    }
    expect(() => runtimeTool.execute({ action: 'add', target: 'memory', content: 'blocked' }, { signal: new AbortController().signal })).toThrow('read-only')
    expect(fixture.channels).toHaveLength(7)
    expect(fixture.channels.map(([channel]) => channel)).toEqual(expect.arrayContaining(['/dsh-mnemon-activation', '/dsh-mnemon-pack']))
    expect(fixture.contexts).toEqual([])
  })

  it('offers bounded Source-owned suggestions when a cross-language query has no exact match', async () => {
    const f = await compositionFixture()
    releases.push(f.dispose)
    await f.graph.source('documents').mutate('mutate', {
      action: 'create', title: 'Cold Archive Transaction Contract', description: 'Write-ahead archival ordering and recovery invariants.',
      content: 'Land the durable cold reference before moving the managed original.',
    })
    const root = { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
    await f.graph.composableTurns.beginTurn('root:documents', agentScope(root, f.config), 'test')
    const tools: ToolDefinition[] = []
    const coordinator = new MnemonSubagentCoordinator({ list: () => [], getProvider: () => undefined, start: vi.fn() } as never, f.live)
    registerTools({ tools: { register: (tool: ToolDefinition) => { tools.push(tool) } } } as never, f.live, coordinator)
    const result = await tools.find(tool => tool.name === 'mnemon_document_search')!.execute({ query: '冷归档不变量' } as never, { agent: root, signal: new AbortController().signal }) as { results: unknown[]; suggestions: unknown[]; suggestionHint: string }
    expect(result.results).toEqual([])
    expect(result.suggestions).toEqual([expect.objectContaining({
      excerpt: expect.stringContaining('Land the durable cold reference'),
      title: 'Cold Archive Transaction Contract',
    })])
    expect(result.suggestionHint).toContain('No exact match.')
    f.graph.composableTurns.endTurn('root:documents')
  })

  it('returns bounded query-local evidence without copying managed record internals', async () => {
    const f = await compositionFixture()
    releases.push(f.dispose)
    const needle = 'TENANT-SKEW-NEEDLE-729'
    await f.graph.source('documents').mutate('mutate', {
      action: 'create', title: 'Long incident record', description: 'A deliberately long managed record.', sourcePaths: ['reports/incident.md'],
      content: 'before '.repeat(1_500) + needle + '\n' + 'after '.repeat(1_500),
    })
    const root = { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
    await f.graph.composableTurns.beginTurn('root:documents', agentScope(root, f.config), 'test')
    const tools: ToolDefinition[] = []
    const coordinator = new MnemonSubagentCoordinator({ list: () => [], getProvider: () => undefined, start: vi.fn() } as never, f.live)
    registerTools({ tools: { register: (tool: ToolDefinition) => { tools.push(tool) } } } as never, f.live, coordinator)
    const result = await tools.find(tool => tool.name === 'mnemon_document_search')!.execute({ query: needle } as never, { agent: root, signal: new AbortController().signal }) as { results: Array<{ content: string }> }
    expect(result.results).toHaveLength(1)
    expect(result.results[0]!.content).toContain(needle)
    expect(result.results[0]!.content.length).toBeLessThanOrEqual(2_600)
    expect(JSON.stringify(result)).not.toMatch(/contentHash|generatedAt|indexPath|directory/)
    expect(JSON.stringify(result).length).toBeLessThan(8_000)
    f.graph.composableTurns.endTurn('root:documents')
  })

  it('shares one Documents route claim across the pinned root turn', async () => {
    const f = await compositionFixture()
    releases.push(f.dispose)
    const root = { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
    const turn = await f.graph.composableTurns.beginTurn('root:documents', agentScope(root, f.config), 'test')
    const route = turn.view.routes.find(route => route.sourceRouteId === 'search')!
    const execute = vi.spyOn(f.graph.memoryComposition.current()!.sourceRuntime(route.sourceInstanceKey)!, 'query')
    const tools: ToolDefinition[] = []
    const coordinator = new MnemonSubagentCoordinator({ list: () => [], getProvider: () => undefined, start: vi.fn() } as never, f.live)
    registerTools({ tools: { register: (tool: ToolDefinition) => { tools.push(tool) } } } as never, f.live, coordinator)
    const tool = tools.find(candidate => candidate.name === 'mnemon_document_search')!
    const execution = { agent: root, signal: new AbortController().signal }
    expect(await tool.execute({ query: 'ORCHID-47 root cause' } as never, execution)).not.toHaveProperty('notRun')
    expect(await tool.execute({ query: 'incident ORCHID generation key' } as never, execution)).toMatchObject({ notRun: true, results: [] })
    expect(execute).toHaveBeenCalledOnce()
    f.graph.composableTurns.endTurn('root:documents')
  })

  it('keeps guidance and Web RPC registrations stable while their live values are disabled', () => {
    const fixture = context()
    apply(fixture.ctx as never, { cliPath: '/fake/mnemon', dataDir: dataDir(), routingGuidance: false, tabEnabled: false })
    expect(fixture.sections).toEqual([
      expect.objectContaining({ name: 'mnemon:routing', text: expect.any(Function) }),
    ])
    expect(fixture.contexts).toEqual([])
    expect((fixture.sections[0] as { text: () => string }).text()).toBe('')
    expect(fixture.channels).toHaveLength(7)
  })

  it('atomically switches the same live RPC faces after a committed profile edit', async () => {
    const fixture = context()
    const initial = { cliPath: '/fake/mnemon', storageScope: 'custom' as const, dataDir: dataDir() }
    const next = { ...initial, dataDir: dataDir() }
    fixture.fiber.config = initial
    apply(fixture.ctx as never, initial)
    const packRegistration = fixture.channels.find(channel => (channel as unknown[])[0] === '/dsh-mnemon-pack') as [string, (endpoint: string, payload: unknown) => Promise<{ ok: boolean; value?: { root: string } }>]
    await expect(packRegistration[1]('target', {})).resolves.toMatchObject({ ok: true, value: { root: initial.dataDir } })

    expect(fixture.preflight(next)).toBe(next)
    fixture.commit(next)

    await expect(packRegistration[1]('target', {})).resolves.toMatchObject({ ok: true, value: { root: next.dataDir } })
  })

  it('rejects an uninitializable live root before the active graph can move', async () => {
    const fixture = context()
    const initialRoot = dataDir()
    const invalidRoot = join(dataDir(), 'not-a-directory')
    writeFileSync(invalidRoot, 'occupied')
    apply(fixture.ctx as never, { cliPath: '/fake/mnemon', storageScope: 'custom', dataDir: initialRoot })
    await installStarter(fixture)
    const packRegistration = fixture.channels.find(channel => (channel as unknown[])[0] === '/dsh-mnemon-pack') as [string, (endpoint: string, payload: unknown) => Promise<{ ok: boolean; value?: { root: string } }>]

    expect(() => fixture.preflight({ cliPath: '/fake/mnemon', storageScope: 'custom', dataDir: invalidRoot })).toThrow(/ENOTDIR|EEXIST|not a directory/iu)
    await expect(packRegistration[1]('target', {})).resolves.toMatchObject({ ok: true, value: { root: initialRoot } })
  })

  it('retires validation candidates and closes every graph with the Cordis effect', async () => {
    const fixture = context()
    const attach = vi.spyOn(MemoryRuntime.prototype, 'attachGeneration')
    apply(fixture.ctx as never, { cliPath: '/fake/mnemon', dataDir: dataDir() })
    // Registration validates the initial candidate before the active graph starts.
    expect(attach).toHaveBeenCalledTimes(2)
    const [checked, active] = attach.mock.results.map(result => result.value as ReturnType<MemoryRuntime['attachGeneration']>)
    expect(() => checked!.host.acquire()).toThrow('disposed')
    fixture.preflight({ cliPath: '/fake/mnemon', dataDir: dataDir() })
    expect(attach).toHaveBeenCalledTimes(3)
    const candidate = attach.mock.results[2]!.value as ReturnType<MemoryRuntime['attachGeneration']>
    expect(() => candidate.host.acquire()).toThrow('disposed')
    for (const cleanup of fixture.effectCleanups.splice(0).reverse()) await cleanup()
    expect(() => active!.host.acquire()).toThrow('disposed')
    expect(() => (fixture.ctx.get('mnemonMemory') as MnemonMemoryService).installContributions({}, { instanceId: 'closed' })).toThrow('disposed')
  })
})
