import { afterEach, describe, expect, it, vi } from 'vitest'
import openviking from 'dsh-mnemon-provider-openviking'
import { compositionFixture } from './fixtures/composition.ts'
import { openVikingProtocolFixture } from '../scripts/fixtures/openviking-protocol.mjs'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { SourceSession } from '../src/host/source-session.ts'
import { registerTools } from '../src/host/tools.ts'
import { agentScope } from '../src/host/runtime.ts'
import { sessionLog } from './fixtures/session-log.ts'

const releases = []
afterEach(async () => {
  vi.restoreAllMocks()
  for (const release of releases.splice(0).reverse()) await release()
})

async function fixture() {
  const backend = openVikingProtocolFixture()
  await new Promise(resolve => backend.server.listen(0, '127.0.0.1', resolve))
  releases.push(() => new Promise(resolve => backend.server.close(resolve)))
  const f = await compositionFixture({}, { providers: [
    { instanceId: 'work-openviking', module: openviking, config: undefined },
    { instanceId: 'personal-openviking', module: openviking, config: undefined },
  ] })
  releases.push(f.dispose)
  const spaces = f.graph.source('memory-spaces')
  const endpoint = `http://127.0.0.1:${backend.server.address().port}`
  for (const account of ['work', 'personal']) await spaces.mutate('provider-service-update', {
    providerId: `${account}-openviking`, settings: { endpoint, account }, enabled: true,
  })
  const catalog = await spaces.read('body-directory')
  const select = providerId => catalog.items.find(item => item.provider.id === providerId && item.name.includes('Issue 233'))
  const work = select('work-openviking')
  const personal = select('personal-openviking')
  expect(work).toBeDefined()
  expect(personal).toBeDefined()
  const agent = { id: 'openviking-root', session: { header: { cwd: f.workspace }, ...sessionLog() } }
  const planning = { action: 'planned', summary: 'Synthetic cold index preserving the exact original.', memoryBodyId: work.id }
  const host = { list: () => ['spawn'], getProvider: () => ({ capabilities: { outputSchema: true, depthLimit: true, toolFilter: true, persona: true } }),
    start: vi.fn(async () => ({ id: 'archive-planner', result: Promise.resolve({ output: [], structured: planning, stopReason: 'completed' }), dispose() {} })) }
  const coordinator = new MnemonSubagentCoordinator(host, f.live, { tools: { register: () => () => {} }, on: () => () => {} })
  releases.push(async () => coordinator.dispose())
  return { ...f, backend, spaces, work, personal, agent, coordinator }
}

describe('OpenViking writes through composed Host and Sources', () => {
  it('writes through a Host tool and retains Provider instance, account, and Source isolation', async () => {
    const f = await fixture()
    const tools = new Map()
    registerTools({ tools: { register: tool => tools.set(tool.name, tool) } }, f.live, f.coordinator)
    const child = { id: 'openviking-child', session: { header: { cwd: f.workspace, origin: 'subagent', parentSession: f.agent.id }, ...sessionLog() } }
    const turn = await f.graph.composableTurns.beginTurn(f.agent.id + ':1', agentScope(f.agent, f.config))
    f.graph.composableTurns.pinTurn(child.id + ':1', agentScope(child, f.config), turn.view.id)
    const content = 'Synthetic work release canary.\n逐字读回。'
    const receipt = await tools.get('mnemon_remember').execute({ memoryBodyId: f.work.id, content, category: 'decision' }, { agent: child, signal: new AbortController().signal })
    expect(receipt).toMatchObject({ action: 'stored', id: expect.stringContaining('viking://user/default/memories/experiences/'), memoryBodyId: f.work.id })
    expect(await f.spaces.read('search', { query: 'work release canary', memoryBodyIds: [f.work.id] }))
      .toMatchObject({ results: [{ id: receipt.id, content, memoryBodyId: f.work.id }] })
    expect(await f.spaces.read('search', { query: 'work release canary', memoryBodyIds: [f.personal.id] })).toMatchObject({ results: [] })
    expect([...f.backend.files.values()].map(file => file.account)).toEqual(['work'])
    expect((await f.spaces.read('body-directory')).items.find(item => item.id === f.work.id).active).toBe(true)
    expect((await f.spaces.read('body-directory')).items.find(item => item.id === f.personal.id).active).toBe(f.personal.active)
    expect((await f.graph.source('runtime').read('snapshot')).entries).toEqual([])
    expect(await f.graph.source('documents').read('snapshot')).toMatchObject({ activeCount: 0, archivedCount: 0 })
    await expect(f.spaces.mutate('forget', { memoryBodyId: f.work.id, id: 'viking://user/other/memories/entities/foreign.md' })).rejects.toThrow('inside this Memory Space')
    expect(f.backend.requests.filter(request => request.method === 'DELETE')).toEqual([])
    await f.spaces.mutate('forget', { memoryBodyId: f.work.id, id: receipt.id })
    expect(f.backend.files.size).toBe(0)
  })

  it('builds archive lineage from the canonical id and compensates only its own new index', async () => {
    const f = await fixture()
    const documents = f.graph.source('documents')
    await f.spaces.mutate('body-update', { memoryBodyId: f.work.id, request: { active: true } })
    const sentinel = await f.spaces.mutate('remember', { memoryBodyId: f.personal.id, content: 'Personal synthetic sentinel.', category: 'fact' })
    const created = await documents.mutate('mutate', { action: 'create', title: 'OpenViking archive', content: 'Exact synthetic document original.' })
    const archived = await f.coordinator.archiveDocument(f.agent, created.document.id, new AbortController().signal)
    expect(archived).toMatchObject({ action: 'archived', lineage: [{ source: { digest: created.document.contentHash }, destination: { layerId: 'memory-spaces', reference: expect.stringContaining(encodeURIComponent('viking://user/default/memories/events/')) } }] })
    const durable = await f.spaces.read('search', { query: created.document.contentHash, memoryBodyIds: [f.work.id] })
    expect(durable.results).toHaveLength(1)
    expect(durable.results[0].content).toContain('Cold path:')

    const rejected = await documents.mutate('mutate', { action: 'create', title: 'Rejected local commit', content: 'Preserve this second original.' })
    const original = SourceSession.prototype.mutate
    vi.spyOn(SourceSession.prototype, 'mutate').mockImplementation(function (operation, input, signal) {
      if (this.typeId === 'documents' && operation === 'archive') return Promise.reject(new Error('Injected local archive conflict'))
      return original.call(this, operation, input, signal)
    })
    await expect(f.coordinator.archiveDocument(f.agent, rejected.document.id, new AbortController().signal)).rejects.toThrow('Injected local archive conflict')
    expect(await documents.read('document', { id: rejected.document.id })).toMatchObject({ status: 'active', content: rejected.document.content })
    expect((await f.spaces.read('search', { query: rejected.document.contentHash, memoryBodyIds: [f.work.id] })).results).toEqual([])
    expect(f.backend.files.has(`personal:${sentinel.id}`)).toBe(true)
    expect(f.backend.files.has(`work:${durable.results[0].id}`)).toBe(true)
    expect(f.backend.requests.filter(request => request.method === 'DELETE')).toHaveLength(1)
  })

  it('keeps the document active when indexing fails and exposes no committed receipt', async () => {
    const f = await fixture()
    await f.spaces.mutate('body-update', { memoryBodyId: f.work.id, request: { active: true } })
    const documents = f.graph.source('documents')
    const created = await documents.mutate('mutate', { action: 'create', title: 'Index failure', content: 'Keep the original on failure.' })
    f.backend.state.vectorStatus = 'failed'
    await expect(f.coordinator.archiveDocument(f.agent, created.document.id, new AbortController().signal)).rejects.toThrow('remote content may remain')
    expect(await documents.read('document', { id: created.document.id })).toMatchObject({ status: 'active', content: created.document.content })
    expect(f.backend.requests.filter(request => request.method === 'DELETE')).toEqual([])
  })
})
