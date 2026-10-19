import { afterEach, describe, expect, it, vi } from 'vitest'
import { resolveConfig, type Config } from '../src/host/config.ts'
import type { HostConnectionHandle, HostRpcHandler } from '../src/host/dsh.ts'
import type { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { createActivationHandler, createPackHandler, createReadHandler, createWriteHandler, MNEMON_ACTIVATION_CHANNEL, MNEMON_PACK_CHANNEL, MNEMON_READ_CHANNEL, MNEMON_WRITE_CHANNEL, registerRpc } from '../src/host/rpc.ts'
import type { LiveMnemonRuntime, MnemonRuntimeGraph } from '../src/host/runtime.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import type { VersionUpdateManager } from '../src/host/version-updates.ts'
import { compositionFixture } from './fixtures/composition.ts'
import openviking from 'dsh-mnemon-provider-openviking'

const cleanup: Array<() => Promise<void>> = []
afterEach(async () => { for (const release of cleanup.splice(0)) await release() })

async function fixture(config: Config = {}) {
  const value = await compositionFixture(config)
  cleanup.push(value.dispose)
  return value
}

/** Host unit tests use Source JSON boundaries, not fake Source implementations. */
function protocolFixture(options: Config = {}) {
  const sources = Object.fromEntries(['runtime', 'documents', 'memory-spaces'].map(type => [type, {
    read: vi.fn(async (_operation: string, _input?: unknown, _signal?: AbortSignal): Promise<unknown> => ({})),
    mutate: vi.fn(async (_operation: string, _input?: unknown, _signal?: AbortSignal): Promise<unknown> => ({})),
  }]))
  sources['runtime']!.read.mockResolvedValue({ entries: [], targets: {} })
  sources['documents']!.read.mockResolvedValue({ documents: [], activeCount: 0 })
  sources['memory-spaces']!.read.mockImplementation(async operation => {
    if (operation === 'body-directory') return { items: [{ id: 'project', active: true, providerEnabled: true }], total: 1, providers: [] }
    if (operation === 'search') return { query: 'SQLite', mode: 'smart', results: [] }
    if (operation === 'prepare-body-placement') return { candidates: [{ id: 'mnemon-native' }], selectorBrief: 'eligible providers' }
    return { healthy: true, memoryBodies: [] }
  })
  const generation = {
    managementCatalog: vi.fn(async () => ({ generationId: 'g1', sources: ['runtime', 'documents', 'memory-spaces'].map(type => ({
      sourceInstanceKey: 'source:mnemon-source-' + type, sourceTypeId: type, packageName: 'dsh-mnemon-source-' + type,
      role: type, availability: 'ready', revision: 'r1', capabilities: ['status'], management: { label: type, description: type },
    })) })),
    executeManagement: vi.fn(async (_request: unknown) => ({ revision: 'r2', value: {} })),
    sourceInstances: () => ['runtime', 'documents', 'memory-spaces'].map(type => ({ sourceInstanceKey: 'source:mnemon-source-' + type, sourceTypeId: type })),
    strategy: { definition: { manifest: { typeId: 'default-three-tier' } } },
  }
  const release = vi.fn()
  const graph = {
    config: resolveConfig(options), directory: '/fixture/data',
    source: vi.fn((type: string) => sources[type]!),
    memoryComposition: { current: () => generation, acquire: vi.fn(() => ({ generation, release })), inspect: () => ({ evaluation: { state: 'ready' } }) },
    storage: { catalog: vi.fn(() => ({ activeRoot: '/fixture/data' })) },
    packs: {
      target: vi.fn(() => ({ root: '/fixture/data', scope: 'custom' })),
      exportPack: vi.fn(async () => ({ fileName: 'backup.zip', base64: 'eA==' })),
      inspectPack: vi.fn(() => ({ archiveBytes: 1 })),
      importPack: vi.fn(async () => ({ imported: true })),
    },
  }
  const route = {
    graph, selectedWorkspace: { id: 'workspace', title: 'Fixture', path: '/fixture/workspace' },
    selectedRoot: '/fixture/data', effectiveRoot: '/fixture/data', aligned: true, liveSession: true,
  }
  const runtime = { config: graph.config, route: vi.fn(() => route) } as unknown as LiveMnemonRuntime
  return { runtime, graph, route, sources, generation, release }
}

function lifecycle(methods: Record<string, unknown> = {}): MnemonLifecycle {
  return { workspaceRoot: () => '/fixture/workspace', snapshot: () => ({ enabled: true, taskAgentAvailable: true }), ...methods } as unknown as MnemonLifecycle
}

describe('Mnemon RPC Source boundaries', () => {
  it('provides default assistance for include-qualified Sources without rewriting their identity', async () => {
    const f = await compositionFixture({}, { entryPrefix: 'include' })
    cleanup.push(f.dispose)
    const catalog = await createReadHandler(f.live, lifecycle())('source-management-catalog', { workspaceId: 'workspace' })
    expect(catalog).toMatchObject({ ok: true, value: { sources: expect.arrayContaining([expect.objectContaining({
      sourceInstanceKey: 'source:include:mnemon-source-runtime', assistance: expect.arrayContaining(['mutate']),
    })]) } })
  })
  it('uses scoped Serving leases, Source identity, confirmation and exact revisions', async () => {
    const f = await fixture()
    const generation = f.graph.memoryComposition.current()!
    const execute = vi.spyOn(generation, 'executeManagement')
    const read = createReadHandler(f.live)
    const write = createWriteHandler(f.live)
    const catalog = await read('source-management-catalog', { workspaceId: 'workspace', sessionId: 's1' })
    expect(catalog).toMatchObject({ ok: true, value: { sources: expect.arrayContaining([expect.objectContaining({
      sourceInstanceKey: 'source:mnemon-source-runtime', packageName: 'dsh-mnemon-source-runtime',
    })]) } })
    const snapshot = await read('source-management-read', { workspaceId: 'workspace', sourceInstanceKey: 'source:mnemon-source-runtime', operation: 'snapshot' })
    expect(snapshot.ok).toBe(true)
    const revision = (snapshot as { ok: true; value: { revision: string } }).value.revision
    const request = { workspaceId: 'workspace', sourceInstanceKey: 'source:mnemon-source-runtime', operation: 'mutate',
      input: { action: 'add', target: 'memory', content: 'through Source protocol' }, expectedRevision: revision }
    expect(await write('source-management-mutate', { ...request, confirmed: false })).toMatchObject({ ok: false })
    expect(await write('source-management-mutate', { ...request, confirmed: true })).toMatchObject({ ok: true, value: { value: { added: 'through Source protocol' } } })
    expect(await write('source-management-mutate', { ...request, confirmed: true })).toMatchObject({ ok: false, error: { message: expect.stringContaining('revision conflict') } })
    expect(execute).toHaveBeenCalledWith(expect.objectContaining({ scope: { storage: 'custom', workspaceId: f.workspace }, sourceInstanceKey: request.sourceInstanceKey }))
    expect(await read('source-management-read', { sourceInstanceKey: 'source:missing', operation: 'snapshot' })).toMatchObject({ ok: false })
  })

  it('refuses management and assistance on a memory layer that is off', async () => {
    const f = protocolFixture({ memoryTopology: { layers: { documents: { enabled: false } } } } as Config)
    const read = createReadHandler(f.runtime)
    const write = createWriteHandler(f.runtime, lifecycle({ manageSource: vi.fn() }))
    const off = { ok: false, error: { message: 'Memory layer documents is off; turn it on to read or change it' } }
    expect(await read('source-management-read', { sourceInstanceKey: 'source:mnemon-source-documents', operation: 'snapshot' })).toMatchObject(off)
    expect(await write('source-management-mutate', { sourceInstanceKey: 'source:mnemon-source-documents', operation: 'mutate', input: {}, expectedRevision: 'r1', confirmed: true })).toMatchObject(off)
    expect(await write('source-assistance', { sourceInstanceKey: 'source:mnemon-source-documents', operation: 'mutate', input: {}, expectedRevision: 'r1', confirmed: true })).toMatchObject(off)
    expect(f.generation.executeManagement).not.toHaveBeenCalled()
    // Another layer stays usable.
    expect(await read('source-management-read', { sourceInstanceKey: 'source:mnemon-source-runtime', operation: 'snapshot' })).toMatchObject({ ok: true })
  })

  it('keeps the Runtime UX and validates branch input in the owning Source', async () => {
    const f = await fixture()
    const write = createWriteHandler(f.live)
    expect(await write('runtime-memory', { action: 'add', target: 'memory', content: 'hello', branches: ['main'] })).toMatchObject({ ok: true, value: { added: 'hello' } })
    expect(await createReadHandler(f.live)('runtime-memory', {})).toMatchObject({ ok: true, value: { entries: [expect.objectContaining({ content: 'hello', branches: ['main'] })] } })
    expect(await write('runtime-memory', { action: 'add', target: 'memory', content: 'bad', branches: ['main', 42] })).toMatchObject({ ok: false })
    expect(await write('runtime-memory', { action: 'replace', target: 'memory', old_text: 'hello', content: 'updated' })).toMatchObject({ ok: true, value: { replaced: { to: 'updated' } } })
  })

  it('preserves explicit empty branch scope through Host assistance without a session', async () => {
    const f = await fixture()
    const coordinator = new MnemonSubagentCoordinator({} as never, f.live)
    const write = createWriteHandler(f.live, lifecycle({ manageSource: coordinator.manageSource.bind(coordinator) }))
    const sourceInstanceKey = 'source:mnemon-source-runtime'
    const assist = async (input: unknown) => {
      const sources = await f.graph.memoryComposition.current()!.managementCatalog({ storage: 'custom' })
      const expectedRevision = sources.sources.find(source => source.sourceInstanceKey === sourceInstanceKey)!.revision
      return write('source-assistance', { sourceInstanceKey, operation: 'mutate', confirmed: true, expectedRevision, input })
    }
    expect(await assist({ action: 'add', target: 'memory', content: 'scoped', branches: ['main'] })).toMatchObject({ ok: true })
    expect(await assist({ action: 'replace', target: 'memory', old_text: 'scoped', content: 'unscoped', branches: [] })).toMatchObject({ ok: true })
    const snapshot = await f.graph.source('runtime').read<{ entries: Array<{ content: string; branches?: string[] }> }>('snapshot')
    expect(snapshot.entries).toHaveLength(1)
    expect(snapshot.entries[0]).toMatchObject({ content: 'unscoped' })
    expect(snapshot.entries[0]).not.toHaveProperty('branches')
  })

  it('round-trips Documents through their selected workspace Source', async () => {
    const f = await fixture()
    const write = createWriteHandler(f.live)
    const read = createReadHandler(f.live)
    const created = await write('document', { workspaceId: 'workspace', action: 'create', title: 'Design', content: '# composable views', sourcePaths: ['src/index.ts'] })
    expect(created).toMatchObject({ ok: true, value: { action: 'created' } })
    const id = (created as { ok: true; value: { document: { id: string } } }).value.document.id
    expect(await read('documents', { workspaceId: 'workspace' })).toMatchObject({ ok: true, value: { activeCount: 1 } })
    expect(await read('document', { workspaceId: 'workspace', id })).toMatchObject({ ok: true, value: { id, content: '# composable views' } })
    expect(await read('document-search', { workspaceId: 'workspace', query: 'composable' })).toMatchObject({ ok: true, value: { total: 1 } })
    expect(await read('documents', { workspaceId: '/arbitrary/path' })).toMatchObject({ ok: false, error: { message: expect.stringContaining('workspace is unavailable') } })
  })

  it('keeps Provider secrets Host-only and Provider metadata on the read channel', async () => {
    const f = await compositionFixture({}, { providers: [{ instanceId: openviking.id, module: openviking, config: undefined }] })
    cleanup.push(f.dispose)
    const write = createWriteHandler(f.live)
    const read = createReadHandler(f.live)
    const updated = await write('provider-service-update', { providerId: 'openviking', settings: { endpoint: 'https://memory.example', apiKey: 'host-only-secret' }, enabled: false })
    expect(updated).toMatchObject({ ok: true })
    const providers = await read('provider-services', {})
    expect(providers).toMatchObject({ ok: true, value: { items: expect.arrayContaining([expect.objectContaining({ providerId: 'openviking', configuredSecrets: ['apiKey'] })]) } })
    expect(JSON.stringify([updated, providers])).not.toContain('host-only-secret')
    expect(await write('provider-services', {})).toMatchObject({ ok: false, error: { code: 'bad-request' } })
  })

  it('validates enum values before Provider I/O', async () => {
    const f = await fixture()
    expect(await createReadHandler(f.live)('search', { query: 'x', mode: 'anything' })).toMatchObject({ ok: false, error: { message: expect.stringContaining('mode') } })
  })

  it('preserves human write provenance through the Source protocol', async () => {
    const f = await fixture()
    const body = await f.memorySpace()
    const execute = vi.spyOn(f.graph.memoryComposition.current()!, 'executeManagement')
    const result = await createWriteHandler(f.live)('remember', { content: 'SQLite is local', source: 'inferred', memoryBodyId: body.id })
    expect(result).toMatchObject({ ok: true })
    expect(execute).toHaveBeenCalledWith(expect.objectContaining({ operation: 'remember', input: expect.objectContaining({ source: 'user' }) }))
    expect(await createReadHandler(f.live)('search', { query: 'SQLite', memoryBodyIds: [body.id] })).toMatchObject({ ok: true, value: { results: expect.arrayContaining([expect.objectContaining({ content: 'SQLite is local' })]) } })
  })

  it('exposes configuration while disabling model/manual data-plane participation, not inspection', async () => {
    const f = await fixture({ memoryTopology: { layers: { 'memory-spaces': { enabled: false } } } })
    const read = createReadHandler(f.live)
    expect(await read('memory-system', {})).toMatchObject({ ok: true, value: { configuration: { layers: { 'memory-spaces': { enabled: false } } } } })
    expect(await read('search', { query: 'blocked' })).toMatchObject({ ok: false, error: { message: expect.stringContaining('does not allow manual') } })
    expect(await read('body-directory', {})).toMatchObject({ ok: true })
  })

  it('returns clear errors for malformed payloads and unknown endpoints', async () => {
    const f = protocolFixture()
    const read = createReadHandler(f.runtime)
    expect(await read('nope', {})).toEqual({ ok: false, error: { code: 'bad-request', message: 'unknown read endpoint: nope', details: { issues: [] } } })
    expect(await read('status', [])).toMatchObject({ ok: false })
    expect(await read('status', { workspaceId: 42 })).toMatchObject({ ok: false })
    expect(await createWriteHandler(f.runtime)('nope', {})).toMatchObject({ ok: false, error: { code: 'bad-request' } })
  })

  it('leases generic management independently of product-specific Source sessions', async () => {
    const f = protocolFixture()
    await createReadHandler(f.runtime)('source-management-catalog', { workspaceId: 'workspace', sessionId: 's1' })
    await createReadHandler(f.runtime)('source-management-read', { sourceInstanceKey: 'source:git/work', operation: 'repository', input: { branch: 'main' } })
    await createWriteHandler(f.runtime)('source-management-mutate', { sourceInstanceKey: 'source:git/work', operation: 'refresh', input: {}, expectedRevision: 'r1', confirmed: true })
    expect(f.generation.managementCatalog).toHaveBeenCalledWith({ storage: 'global', workspaceId: '/fixture/workspace', sessionId: 's1' })
    expect(f.generation.executeManagement).toHaveBeenLastCalledWith(expect.objectContaining({ sourceInstanceKey: 'source:git/work', mode: 'mutate', operation: 'refresh', expectedRevision: 'r1', confirmed: true }))
    expect(f.graph.source).not.toHaveBeenCalled()
    expect(f.release).toHaveBeenCalledTimes(3)
  })
})

describe('Host assistance and channels', () => {
  it('advertises only available, instance-bound assistance', async () => {
    const f = protocolFixture()
    const read = createReadHandler(f.runtime, lifecycle({ snapshot: () => ({ taskAgentAvailable: false }) }))
    const catalog = await read('source-management-catalog', {})
    expect(catalog).toMatchObject({ ok: true, value: { sources: [
      expect.objectContaining({ assistance: ['mutate'] }),
      expect.objectContaining({ assistance: ['mutate'] }),
      expect.objectContaining({ assistance: ['activation'] }),
    ] } })
    expect(await createWriteHandler(f.runtime, lifecycle())('source-assistance', { sourceInstanceKey: 'source:other-runtime', operation: 'mutate', input: {}, expectedRevision: 'r1', confirmed: true })).toMatchObject({ ok: false })
    expect(await createWriteHandler(f.runtime, lifecycle())('source-assistance', { sourceInstanceKey: 'source:mnemon-source-runtime', operation: 'mutate', input: {}, expectedRevision: 'old', confirmed: true })).toMatchObject({ ok: false })
  })

  it('routes all Runtime writes through the selected scope, independently of session alignment', async () => {
    const f = protocolFixture()
    Object.assign(f.sources.runtime!, { identity: async () => ({ sourceInstanceKey: 'source:mnemon-source-runtime' }) })
    Object.assign(f.generation, { managementRevision: async () => 'r1' })
    const mutate = vi.fn(async () => ({ revision: 'r2', value: { success: true } }))
    const write = createWriteHandler(f.runtime, lifecycle({ manageSource: mutate }))
    await write('runtime-memory', { sessionId: 's1', action: 'replace', target: 'memory', old_text: 'before', content: 'after' })
    expect(mutate).toHaveBeenCalledWith(f.graph, expect.objectContaining({
      scope: expect.objectContaining({ workspaceId: '/fixture/workspace', sessionId: 's1' }),
      input: expect.objectContaining({ oldText: 'before', content: 'after' }), expectedRevision: 'r1', confirmed: true,
    }))
    f.route.aligned = false
    await write('runtime-memory', { sessionId: 's1', action: 'add', target: 'memory', content: 'inspect' })
    expect(mutate).toHaveBeenLastCalledWith(f.graph, expect.objectContaining({ input: expect.objectContaining({ content: 'inspect' }) }))
    expect(f.sources.runtime!.mutate).not.toHaveBeenCalled()
    expect(mutate).toHaveBeenCalledTimes(2)
  })

  it('keeps Tab reads deterministic while delegating semantic writes', async () => {
    const f = protocolFixture()
    const recall = vi.fn()
    const remember = vi.fn(async () => ({ delegated: true, runId: 'write-1' }))
    const life = lifecycle({ recall, remember })
    const read = createReadHandler(f.runtime, life)
    await read('search', { sessionId: 's1', query: 'SQLite' })
    await read('entities', { sessionId: 's1', entity: 'SQLite' })
    await read('related', { sessionId: 's1', id: 'm1' })
    expect(recall).not.toHaveBeenCalled()
    expect(await createWriteHandler(f.runtime, life)('remember', { sessionId: 's1', content: 'SQLite' })).toMatchObject({ ok: true, value: { runId: 'write-1' } })
    expect(remember).toHaveBeenCalledWith('s1', expect.objectContaining({ source: 'user' }), undefined)
    f.route.aligned = false
    await createWriteHandler(f.runtime, life)('remember', { sessionId: 's1', content: 'inspected' })
    expect(f.sources['memory-spaces']!.mutate).toHaveBeenCalledWith('remember', expect.objectContaining({ content: 'inspected', source: 'user' }), undefined)
  })

  it('writes to the Sources directly for a session whose Agent is not loaded', async () => {
    // A conversation opened from the list, or just switched to, before DSH loads its Agent.
    const f = protocolFixture()
    Object.assign(f.route, { liveSession: false })
    const remember = vi.fn()
    const mutateDocument = vi.fn()
    const write = createWriteHandler(f.runtime, lifecycle({ remember, mutateDocument }))
    expect(await write('remember', { sessionId: 's1', content: 'Prefer pnpm.' })).toMatchObject({ ok: true })
    expect(f.sources['memory-spaces']!.mutate).toHaveBeenCalledWith('remember', expect.objectContaining({ content: 'Prefer pnpm.', source: 'user' }), undefined)
    expect(await write('document', { sessionId: 's1', action: 'create', title: 'Boot', content: '# Boot' })).toMatchObject({ ok: true })
    expect(f.sources.documents!.mutate).toHaveBeenCalledWith('mutate', expect.objectContaining({ action: 'create', title: 'Boot' }), undefined)
    expect(remember).not.toHaveBeenCalled()
    expect(mutateDocument).not.toHaveBeenCalled()
  })

  it('makes room for a Document through a task Agent when the session\'s Agent is not loaded', async () => {
    const f = protocolFixture()
    Object.assign(f.route, { liveSession: false })
    const capacity = Object.assign(new Error('Would exceed active document capacity: 11 bytes (limit 10). Archive the least-recently-used active document before retrying.'), { code: 'document-capacity', candidates: [{ id: 'old' }] })
    const mutateDocumentTask = vi.fn(async () => ({ action: 'created', document: { id: 'new' } }))
    const write = createWriteHandler(f.runtime, lifecycle({ mutateDocumentTask }))
    f.sources.documents!.mutate.mockRejectedValueOnce(capacity)
    expect(await write('document', { sessionId: 's1', action: 'create', title: 'Boot', content: '# Boot' })).toMatchObject({ ok: true })
    // The task Agent archives the least recently used Document, as a loaded Agent does.
    expect(mutateDocumentTask).toHaveBeenCalledWith('s1', expect.objectContaining({ action: 'create', title: 'Boot' }), '/fixture/workspace', undefined)
    // Other failures, and a page without a conversation, keep the Source's answer.
    f.sources.documents!.mutate.mockRejectedValueOnce(new Error('document not found: x'))
    expect(await write('document', { sessionId: 's1', action: 'update', id: 'x', content: '# X' })).toMatchObject({ ok: false, error: { message: 'document not found: x' } })
    f.sources.documents!.mutate.mockRejectedValueOnce(capacity)
    expect(await write('document', { action: 'create', title: 'Boot', content: '# Boot' })).toMatchObject({ ok: false, error: { message: expect.stringContaining('Would exceed active document capacity') } })
    expect(mutateDocumentTask).toHaveBeenCalledOnce()
  })

  it('chooses a new Memory Space\'s Provider through a task Agent when the session\'s Agent is not loaded', async () => {
    const f = protocolFixture()
    Object.assign(f.route, { liveSession: false })
    const decision = { providerId: 'mnemon-native', reason: 'local', confidence: 'high' }
    const placeProvider = vi.fn()
    const placeProviderTask = vi.fn(async () => decision)
    const write = createWriteHandler(f.runtime, lifecycle({ placeProvider, placeProviderTask }))
    const request = { sessionId: 's1', name: 'Product', description: 'Decisions', placement: { mode: 'automatic', prompt: 'local first' } }
    expect(await write('body-create', request)).toMatchObject({ ok: true })
    expect(placeProviderTask).toHaveBeenCalledWith('s1', { name: 'Product', description: 'Decisions' }, expect.objectContaining({ selectorBrief: 'eligible providers' }), '/fixture/workspace', undefined)
    expect(placeProvider).not.toHaveBeenCalled()
    expect(f.sources['memory-spaces']!.mutate).toHaveBeenCalledWith('body-create', { request, placementDecision: decision }, undefined)
  })

  it('synthesizes answers only after deterministic Source search and honors cancellation', async () => {
    const f = protocolFixture()
    const answerTask = vi.fn(async () => ({ answer: 'SQLite.', citations: [] }))
    const controller = new AbortController()
    expect(await createReadHandler(f.runtime, lifecycle({ answerTask }))('agent-search', { sessionId: 's1', query: 'SQLite' }, controller.signal)).toMatchObject({ ok: true, value: { query: 'SQLite', answer: 'SQLite.' } })
    expect(f.sources['memory-spaces']!.read).toHaveBeenCalledWith('search', expect.objectContaining({ query: 'SQLite' }), controller.signal)
    expect(answerTask).toHaveBeenCalledWith('s1', 'SQLite', [], '/fixture/workspace', controller.signal)
    expect(f.sources['memory-spaces']!.read.mock.invocationCallOrder[0]).toBeLessThan(answerTask.mock.invocationCallOrder[0]!)
  })

  it('resolves Provider placement before asking its Source to create a namespace', async () => {
    const f = protocolFixture()
    const decision = { providerId: 'mnemon-native', reason: 'local', confidence: 'high' }
    const placeProvider = vi.fn(async () => decision)
    const write = createWriteHandler(f.runtime, lifecycle({ placeProvider }))
    const request = { sessionId: 's1', name: 'Product', description: 'Decisions', placement: { mode: 'automatic', prompt: 'local first' } }
    expect(await write('body-create', request)).toMatchObject({ ok: true })
    expect(placeProvider).toHaveBeenCalledWith('s1', { name: 'Product', description: 'Decisions' }, expect.objectContaining({ selectorBrief: 'eligible providers' }), undefined)
    expect(f.sources['memory-spaces']!.mutate).toHaveBeenCalledWith('body-create', { request, placementDecision: decision }, undefined)
    f.route.aligned = false
    expect(await write('body-create', request)).toMatchObject({ ok: false, error: { message: expect.stringContaining('align the workbench') } })
    expect(placeProvider).toHaveBeenCalledOnce()
  })

  it('validates exactly selected active Spaces before metadata curation and commits only valid results', async () => {
    const f = protocolFixture()
    const updates = [{ memoryBodyId: 'project', title: '产品决策', description: '记录产品决策并供规划时召回。' }]
    const maintainMetadata = vi.fn(async () => ({ runId: 'metadata', updates }))
    const write = createWriteHandler(f.runtime, lifecycle({ maintainMetadata }))
    expect(await write('body-metadata-maintain', { memoryBodyIds: ['project', 'project'] })).toMatchObject({ ok: true })
    expect(maintainMetadata).toHaveBeenCalledWith('', ['project'], '/fixture/workspace', undefined)
    expect(f.sources['memory-spaces']!.mutate).toHaveBeenCalledWith('body-metadata-update', { updates }, undefined)
    expect(await write('body-metadata-maintain', { memoryBodyIds: ['unknown'] })).toMatchObject({ ok: false })
    expect(await write('body-metadata-maintain', { memoryBodyIds: [] })).toMatchObject({ ok: false })
    expect(maintainMetadata).toHaveBeenCalledOnce()
    maintainMetadata.mockResolvedValue({ runId: 'empty', updates: [] })
    f.sources['memory-spaces']!.mutate.mockClear()
    expect(await write('body-metadata-maintain', { memoryBodyIds: ['project'] })).toMatchObject({ ok: true })
    expect(f.sources['memory-spaces']!.mutate).not.toHaveBeenCalled()
  })

  it.each(['s1', undefined])('routes supervised work through workspace task context (%s)', async sessionId => {
    const f = protocolFixture()
    const superviseTask = vi.fn(async () => ({ runId: 'task', delegated: true }))
    expect(await createWriteHandler(f.runtime, lifecycle({ superviseTask }))('supervise', { ...(sessionId ? { sessionId } : {}), content: 'candidate', idempotencyKey: 'message' })).toMatchObject({ ok: true })
    expect(superviseTask).toHaveBeenCalledWith(sessionId ?? '', 'candidate', 'message', '/fixture/workspace', undefined)
    expect(f.sources['memory-spaces']!.mutate).not.toHaveBeenCalled()
  })

  it('archives Documents through the selected workspace even without a session', async () => {
    const f = protocolFixture()
    f.route.aligned = false
    const archiveDocument = vi.fn(async () => ({ action: 'archived' }))
    expect(await createWriteHandler(f.runtime, lifecycle({ archiveDocument }))('document', { action: 'archive', id: 'doc-1' })).toMatchObject({ ok: true })
    expect(archiveDocument).toHaveBeenCalledWith('', 'doc-1', '/fixture/workspace', undefined)
  })

  it('adds lifecycle and storage diagnostics without replacing Source status', async () => {
    const f = protocolFixture()
    f.route.aligned = false
    f.route.effectiveRoot = '/fixture/other'
    expect(await createReadHandler(f.runtime, lifecycle())('status-summary', { sessionId: 's1' })).toMatchObject({ ok: true, value: {
      healthy: true, lifecycle: { enabled: true }, memorySystem: { strategyTypeId: 'default-three-tier', evaluation: { state: 'ready' } },
      workspaceContext: { aligned: false, selectedRoot: '/fixture/data', effectiveRoot: '/fixture/other' },
    } })
  })

  it('names the running version and an installed update in the status every page reads', async () => {
    const f = protocolFixture()
    const restartStatus = vi.fn(() => ({ running: '0.5.22', installed: '0.5.23' }))
    const versions = { runningVersion: '0.5.22', currentDshMnemonVersion: '0.5.23', restartStatus } as unknown as VersionUpdateManager
    expect(await createReadHandler(f.runtime, lifecycle(), versions)('status-summary', { sessionId: 's1' })).toMatchObject({ ok: true, value: {
      dshMnemonVersion: '0.5.22', restartPending: { running: '0.5.22', installed: '0.5.23' },
    } })
    restartStatus.mockReturnValue(undefined as never)
    const current = await createReadHandler(f.runtime, lifecycle(), versions)('status', {})
    expect(current).toMatchObject({ ok: true, value: { dshMnemonVersion: '0.5.22' } })
    expect((current as { value: Record<string, unknown> }).value).not.toHaveProperty('restartPending')
    // The reminder is optional; a failure to read it never fails the status.
    restartStatus.mockImplementation(() => { throw new Error('unreadable profile') })
    expect(await createReadHandler(f.runtime, lifecycle(), versions)('status', {})).toMatchObject({ ok: true, value: { healthy: true } })
  })

  it('keeps activation strictly narrower than the configuration/mutation channel', async () => {
    const f = protocolFixture()
    const activate = createActivationHandler(f.runtime)
    expect(await activate('body', { memoryBodyId: 'project', active: true })).toMatchObject({ ok: true })
    expect(f.sources['memory-spaces']!.mutate).toHaveBeenCalledWith('body-update', { memoryBodyId: 'project', active: true }, undefined)
    for (const payload of [{ memoryBodyId: 'project', active: true, name: 'rename' }, { memoryBodyId: 'project', active: 'yes' }, { memoryBodyId: '', active: false }]) {
      expect(await activate('body', payload)).toMatchObject({ ok: false, error: { code: 'bad-request' } })
    }
    expect(await activate('body-delete', {})).toMatchObject({ ok: false })
    expect(f.sources['memory-spaces']!.mutate).toHaveBeenCalledOnce()
  })

  it('allows Source-bound activation with the exact revision, not other Source operations', async () => {
    const f = protocolFixture()
    const activate = createActivationHandler(f.runtime)
    const request = { sourceInstanceKey: 'source:mnemon-source-memory-spaces', operation: 'activation', expectedRevision: 'r1', confirmed: true, input: { memoryBodyId: 'project', active: false } }
    expect(await activate('source-assistance', request)).toMatchObject({ ok: true })
    expect(f.generation.executeManagement).toHaveBeenCalledWith(expect.objectContaining({ sourceInstanceKey: request.sourceInstanceKey, expectedRevision: 'r1', operation: 'body-update' }))
    expect(await activate('source-assistance', { ...request, input: { ...request.input, name: 'rename' } })).toMatchObject({ ok: false })
    expect(await activate('source-assistance', { ...request, operation: 'body-delete' })).toMatchObject({ ok: false })
  })

  it('keeps inspection and activation RPC registrations stable while enforcing read-only mutations', async () => {
    const f = protocolFixture({ writeEnabled: false })
    const handle = vi.fn()
    registerRpc({ rpc: { handle } } as unknown as HostConnectionHandle, f.runtime)
    expect(handle).toHaveBeenCalledTimes(4)
    for (const channel of [MNEMON_READ_CHANNEL, MNEMON_ACTIVATION_CHANNEL, MNEMON_WRITE_CHANNEL, MNEMON_PACK_CHANNEL]) {
      expect(handle).toHaveBeenCalledWith(channel, expect.any(Function))
    }
    for (const channel of [MNEMON_WRITE_CHANNEL, MNEMON_ACTIVATION_CHANNEL]) {
      const handler = handle.mock.calls.find(([id]) => id === channel)![1] as HostRpcHandler
      expect(await handler(channel === MNEMON_WRITE_CHANNEL ? 'remember' : 'body', { content: 'blocked', memoryBodyId: 'project', active: false })).toMatchObject({ ok: false })
    }
    expect(f.sources['memory-spaces']!.mutate).not.toHaveBeenCalled()
  })

  it('keeps Pack transport authenticated, selected-root scoped, and merge-only from the page', async () => {
    const f = protocolFixture()
    const handler = createPackHandler(f.runtime)
    expect(await handler('target', {})).toMatchObject({ ok: true, value: { root: '/fixture/data' } })
    expect(await handler('export', { scope: 'runtime' })).toMatchObject({ ok: true })
    expect(f.graph.packs.exportPack).toHaveBeenCalledWith('full')
    expect(await handler('inspect', { base64: 'eA==', fileName: 'backup.zip' })).toMatchObject({ ok: true })
    expect(await handler('import', { base64: 'eA==', mode: 'replace', components: ['runtime'] })).toMatchObject({ ok: true })
    expect(f.graph.packs.importPack).toHaveBeenCalledWith('eA==', { mode: 'merge' })
    expect(f.sources['memory-spaces']!.mutate).toHaveBeenCalledWith('reload', {})
    const readonly = protocolFixture({ writeEnabled: false })
    expect(await createPackHandler(readonly.runtime)('import', { base64: 'eA==' })).toMatchObject({ ok: false })
    expect(readonly.graph.packs.importPack).not.toHaveBeenCalled()
  })

  it('checks versions on read and performs explicit updates only on management', async () => {
    const f = protocolFixture()
    const versions = { check: vi.fn(async () => ({ components: [] })), update: vi.fn(async () => ({ updated: true })) } as unknown as VersionUpdateManager
    expect(await createReadHandler(f.runtime, undefined, versions)('versions', {})).toMatchObject({ ok: true })
    expect(await createWriteHandler(f.runtime, undefined, versions)('version-update', { component: 'mnemon' })).toMatchObject({ ok: true })
    expect(await createWriteHandler(f.runtime, undefined, versions)('version-update', { component: 'other' })).toMatchObject({ ok: false })
    expect(versions.update).toHaveBeenCalledOnce()
    expect(await createWriteHandler(f.runtime, undefined, versions)('version-update', { component: 'dsh-mnemon-source-runtime' })).toMatchObject({ ok: true })
    expect(versions.update).toHaveBeenLastCalledWith('dsh-mnemon-source-runtime')
    expect(await createWriteHandler(f.runtime, undefined, versions)('version-update', { component: 'dsh-mnemon-source-runtime@latest' })).toMatchObject({ ok: false })
    const readonly = protocolFixture({ writeEnabled: false })
    expect(await createWriteHandler(readonly.runtime, undefined, versions)('version-update', { component: 'mnemon' })).toMatchObject({ ok: false })
    expect(versions.update).toHaveBeenCalledTimes(2)
  })

  it('exposes task model choices and cached turn activities without Source work', async () => {
    const f = protocolFixture()
    const taskAgentModels = vi.fn(async () => ({ groups: [], failures: [] }))
    const turnActivities = vi.fn(() => ({ cursor: 12, activities: [{ turn: 2, recalls: 1 }] }))
    const read = createReadHandler(f.runtime, lifecycle({ taskAgentModels, turnActivities }))
    expect(await read('task-agent-models', { includeCatalog: false })).toMatchObject({ ok: true })
    expect(taskAgentModels).toHaveBeenCalledWith(false)
    expect(await read('turn-activities', { sessionId: 's1' })).toMatchObject({ ok: true, value: { cursor: 12 } })
    expect(await read('turn-activity', { sessionId: 's1', turn: 2 })).toMatchObject({ ok: true, value: { recalls: 1 } })
    expect(f.graph.source).not.toHaveBeenCalled()
  })
})
