import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import * as scoped from 'dsh-mnemon-strategy-scoped'
import * as light from 'dsh-mnemon-strategy-light-context'
import * as capture from 'dsh-mnemon-strategy-auto-capture'
import * as documents from 'dsh-mnemon-source-documents'
import type { DocumentMutationResult } from 'dsh-mnemon-source-documents/contracts'
import type { RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
import type { HostAgent, HostContextShape, HostSubagentsService, ToolDefinition } from '../src/host/dsh.ts'
import { agentScope } from '../src/host/runtime.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { modelMemoryWake } from '../src/host/view-presentation.ts'
import { compositionFixture } from './fixtures/composition.ts'
import { sessionLog } from './fixtures/session-log.ts'

const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
afterEach(async () => { for (const fixture of fixtures.splice(0)) await fixture.dispose() })
async function fixture(options: Parameters<typeof compositionFixture>[0] = {}) {
  const value = await compositionFixture(options)
  fixtures.push(value)
  return value
}
function agent(f: Awaited<ReturnType<typeof fixture>>) {
  return { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
}
const runtimeKey = 'source:mnemon-source-runtime'
const documentsKey = 'source:mnemon-source-documents'
const spacesKey = 'source:mnemon-source-memory-spaces'
const selectedKeys = [runtimeKey, documentsKey, spacesKey]

describe('additive Strategy plugins through the real Host', () => {
  it('combines selection, a smaller projection and capture without replacing the default Strategy', async () => {
    const f = await fixture()
    const body = await f.memorySpace()
    await f.graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: 'Runtime sentinel. '.repeat(100) })
    await f.mount(scoped, { instanceId: 'scoped', config: { sourceKeys: selectedKeys, writableSourceKeys: [spacesKey] } })
    const removeLight = await f.mount(light, { instanceId: 'light', config: { maxProjectionCharacters: 500 } })
    await f.mount(capture, { instanceId: 'capture' })
    const parent = agent(f)
    const turn = await f.graph.composableTurns.beginTurn('additive:1', agentScope(parent, f.config))
    expect(turn.view.strategyTypeId).toBe('default-three-tier')
    expect(turn.view.strategyExtensions?.map(extension => extension.slot).sort()).toEqual(['capture', 'projection', 'selection'])
    expect(turn.view.projection.reduce((sum, fragment) => sum + fragment.text.length, 0)).toBeLessThanOrEqual(500)
    expect(turn.view.actionOffers.every(offer => offer.sourceInstanceKey === spacesKey)).toBe(true)
    expect(modelMemoryWake(f.graph, turn).guidance?.system).toContain('MNEMON OPTIONAL AUTO CAPTURE')

    const tools = new Map<string, ToolDefinition>()
    const coordinator = new MnemonSubagentCoordinator({} as HostSubagentsService, f.live)
    registerTools({ tools: { register: (tool: ToolDefinition) => { tools.set(tool.name, tool) } } } as unknown as HostContextShape, f.live, coordinator)
    const execute = (name: string, args: object) => (tools.get(name)!.execute as Function)(args, { agent: parent, signal: new AbortController().signal })
    await expect(execute('mnemon_runtime_memory', { action: 'add', target: 'memory', content: 'must not write' })).rejects.toThrow('not offered')
    const remember = turn.view.actionOffers.find(offer => offer.sourceActionId === 'remember')!
    await expect(execute('mnemon_view_action', { offerId: remember.id, input: { memoryBodyId: body.id, content: 'Additive plugin durable sentinel.' } })).resolves.toMatchObject({ completion: 'committed' })

    await removeLight()
    expect(turn.view.strategyExtensions).toHaveLength(3) // The existing turn remains pinned.
    f.graph.composableTurns.endTurn(turn.turnId)
    const next = await f.graph.composableTurns.beginTurn('additive:2', agentScope(parent, f.config))
    expect(next.view.strategyExtensions?.map(extension => extension.slot).sort()).toEqual(['capture', 'selection'])
    expect(next.view.projection.reduce((sum, fragment) => sum + fragment.text.length, 0)).toBeGreaterThan(500)
    expect(next.view.actionOffers.every(offer => offer.sourceInstanceKey === spacesKey)).toBe(true)
    const recall = next.view.routes.find(route => route.sourceRouteId === 'recall')!
    const evidence = await f.graph.composableTurns.executeRoute(next.turnId, recall.id, { query: 'Additive plugin durable sentinel' })
    expect(evidence.items[0]?.text).toContain('Additive plugin durable sentinel')
  })

  it('does not archive Runtime into a View-read-only Source on capacity overflow', async () => {
    const f = await fixture({ runtimeMemory: { memoryLimitBytes: 512 } })
    await f.memorySpace()
    const runtime = f.graph.source('runtime')
    await runtime.mutate('mutate', { action: 'add', target: 'memory', content: 'Saved durable fact. '.repeat(15) })
    await f.mount(scoped, { instanceId: 'scoped', config: { sourceKeys: selectedKeys, writableSourceKeys: [runtimeKey] } })
    const parent = agent(f)
    await f.graph.composableTurns.beginTurn('readonly:1', agentScope(parent, f.config))
    const before = await runtime.read<RuntimeMemorySnapshot>('snapshot')
    const space = f.graph.memoryComposition.current()!.sourceRuntime(spacesKey)!
    const management = vi.spyOn(space as { manage: NonNullable<typeof space.manage> }, 'manage')
    const start = vi.fn()
    const coordinator = new MnemonSubagentCoordinator({ start } as unknown as HostSubagentsService, f.live)
    await expect(coordinator.runtime(parent, { action: 'add', target: 'memory', content: 'Pending durable fact. '.repeat(15) }, new AbortController().signal))
      .rejects.toThrow('Source Action is not offered by the current View: memory-spaces/remember')
    expect(management.mock.calls.filter(([request]) => request.mode === 'mutate')).toEqual([])
    expect(start).not.toHaveBeenCalled()
    expect(await runtime.read('snapshot')).toMatchObject({ revision: before.revision, entries: before.entries, targets: before.targets })
  })

  it('rejects a read-only Documents mutation before its archive preflight can start a worker', async () => {
    const f = await fixture()
    await f.releases[1]!()
    await f.mount(documents, { instanceId: 'small-documents', config: { dataDir: join(f.root, 'small-documents'), limitBytes: 700 } })
    const controller = f.graph.source('documents')
    const initial = await controller.mutate<DocumentMutationResult>('mutate', { action: 'create', title: 'Saved', content: 'Old record. '.repeat(12) })
    await f.mount(scoped, { instanceId: 'scoped', config: {
      sourceKeys: [runtimeKey, 'source:small-documents', spacesKey], writableSourceKeys: [runtimeKey, spacesKey],
    } })
    const parent = agent(f)
    await f.graph.composableTurns.beginTurn('readonly:documents', agentScope(parent, f.config))
    const start = vi.fn()
    const coordinator = new MnemonSubagentCoordinator({ start } as unknown as HostSubagentsService, f.live)
    await expect(coordinator.document(parent, { action: 'create', title: 'New', content: 'New record. '.repeat(12) }, new AbortController().signal))
      .rejects.toThrow('Source Action is not offered by the current View: documents/manage')
    expect(start).not.toHaveBeenCalled()
    expect(await controller.read('document', { id: initial.document.id })).toMatchObject({ status: 'active', content: initial.document.content })
  })

  it('retains automatic capacity archival when both Sources are offered as writable', async () => {
    const f = await fixture({ runtimeMemory: { memoryLimitBytes: 512 } })
    await f.memorySpace()
    const saved = 'Saved durable fact. '.repeat(15).trim()
    const pending = 'Pending durable fact. '.repeat(15).trim()
    await f.graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: saved })
    await f.mount(scoped, { instanceId: 'scoped' })
    const parent = agent(f)
    const turn = await f.graph.composableTurns.beginTurn('writable:1', agentScope(parent, f.config))
    const start = vi.fn()
    const coordinator = new MnemonSubagentCoordinator({ start } as unknown as HostSubagentsService, f.live)
    await expect(coordinator.runtime(parent, { action: 'add', target: 'memory', content: pending }, new AbortController().signal))
      .resolves.toMatchObject({ added: pending, maintenance: { kind: 'mnemon-archive', provider: 'host' } })
    expect(start).not.toHaveBeenCalled()
    expect((await f.graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')).entries.map(entry => entry.content)).toEqual([pending])
    const recall = turn.view.routes.find(route => route.sourceRouteId === 'recall')!
    const evidence = await f.graph.composableTurns.executeRoute(turn.turnId, recall.id, { query: 'Saved durable fact' })
    expect(evidence.items[0]?.text).toBe(saved)
  })
})
