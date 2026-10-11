import { mkdtempSync, mkdirSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { resolveConfig } from "../src/host/config.ts"
import { createRuntimeGraph } from "../src/host/runtime.ts"
import { compositionFixture } from './fixtures/composition.ts'
import type { DocumentMutationResult } from 'dsh-mnemon-source-documents/contracts'
import type { RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
async function fixture() { const value = await compositionFixture(); fixtures.push(value); return value }
afterEach(async () => { for (const value of fixtures.splice(0)) await value.dispose() })

describe('Composable Memory root-turn boundary', () => {
  it('retains dispatch authority until the last delegation and child turn release it', async () => {
    const { workspace, releases, graph } = await fixture()
    const scope = { storage: 'custom' as const, workspaceId: workspace, agentId: 'parent' }
    const parent = await graph.composableTurns.beginTurn('parent:1', scope)
    const first = graph.composableTurns.retainView(parent.view.id)
    const second = graph.composableTurns.retainView(parent.view.id)
    graph.composableTurns.endTurn(parent.turnId)
    await Promise.all(releases.map(release => release()))
    expect(graph.memoryComposition.inspect().drainingGenerationIds).toContain(parent.view.runtimeGeneration)
    first()
    first()
    expect(() => graph.composableTurns.pinTurn('child:1', { ...scope, workspaceId: '/other' }, parent.view.id)).toThrow('storage scope')
    const child = graph.composableTurns.pinTurn('child:1', { ...scope, agentId: 'child' }, parent.view.id)
    second()
    expect(child.view).toBe(parent.view)
    expect(graph.composableTurns.memoryWake(child.view.id).viewId).toBe(parent.view.id)
    expect(graph.memoryComposition.generation(parent.view.runtimeGeneration)).toBeDefined()
    graph.composableTurns.endTurn(child.turnId)
    expect(graph.composableTurns.get(parent.view.id)).toBeUndefined()
    expect(graph.memoryComposition.generation(parent.view.runtimeGeneration)).toBeUndefined()
  })

  it('assigns independent Route budgets to child executions sharing a dispatch View', async () => {
    const { workspace, graph } = await fixture()
    const scope = { storage: 'custom' as const, workspaceId: workspace, agentId: 'parent' }
    const parent = await graph.composableTurns.beginTurn('parent:1', scope)
    const child = graph.composableTurns.pinTurn('child:1', { ...scope, agentId: 'child' }, parent.view.id)
    const route = parent.view.routes.find(candidate => candidate.sourceRouteId === 'search')!
    await graph.composableTurns.executeRoute(parent.turnId, route.id, { query: 'bounded' })
    await expect(graph.composableTurns.executeRoute(parent.turnId, route.id, { query: 'bounded' })).resolves.toMatchObject({ output: { notRun: true } })
    const read = await graph.composableTurns.executeRoute(child.turnId, route.id, { query: 'bounded' })
    expect(read).toMatchObject({ viewId: parent.view.id })
    expect(read.output).not.toHaveProperty('notRun')
    graph.composableTurns.endTurn(child.turnId)
    const next = graph.composableTurns.pinTurn('child:1', { ...scope, agentId: 'child' }, parent.view.id)
    expect(next).not.toBe(child)
    await expect(graph.composableTurns.executeRoute(next.turnId, route.id, { query: 'bounded' })).resolves.toMatchObject({ viewId: parent.view.id })
  })

  it('joins concurrent beginnings, rejects scope reuse, and releases a cancelled composition', async () => {
    const { workspace, graph } = await fixture()
    const generation = graph.memoryComposition.current()!
    const original = generation.compose.bind(generation)
    const gate = Promise.withResolvers<void>()
    const compose = vi.spyOn(generation, 'compose').mockImplementation(async request => { await gate.promise; return original(request) })
    const scope = { storage: 'custom' as const, workspaceId: workspace, agentId: 'root' }
    const first = graph.composableTurns.beginTurn('root:join', scope)
    const second = graph.composableTurns.beginTurn('root:join', scope)
    await expect(graph.composableTurns.beginTurn('root:join', { ...scope, agentId: 'other' })).rejects.toThrow('another scope')
    gate.resolve()
    expect(await first).toBe(await second)
    expect(compose).toHaveBeenCalledOnce()
    graph.composableTurns.endTurn('root:join')
    const cancelled = graph.composableTurns.beginTurn('root:cancel', scope)
    graph.composableTurns.endTurn('root:cancel')
    await expect(cancelled).rejects.toThrow('ended during composition')
    expect(graph.composableTurns.activeTurn('root')).toBeUndefined()
  })

  it('does not publish a View after disposal while composition is pending', async () => {
    const { graph } = await fixture()
    const pending = graph.composableTurns.beginTurn('root:closing', { storage: 'custom', agentId: 'root' })
    graph.composableTurns.dispose()
    await expect(pending).rejects.toThrow('ended during composition')
    expect(graph.composableTurns.activeTurn('root')).toBeUndefined()
  })

  it('keeps in-flight Route I/O leased after its parent turn closes', async () => {
    const { graph } = await fixture()
    const turn = await graph.composableTurns.beginTurn('root:operation', { storage: 'custom', agentId: 'root' })
    const generation = graph.memoryComposition.current()!
    const route = turn.view.routes[0]!
    const gate = Promise.withResolvers<void>()
    vi.spyOn(generation, 'executeRoute').mockImplementation(async () => { await gate.promise; return {
      id: 'evidence:test', viewId: turn.view.id, routeId: route.id, sourceInstanceKey: route.sourceInstanceKey,
      observedAt: 'now', items: [], truncated: false,
    } })
    const call = graph.composableTurns.executeRoute(turn.turnId, route.id, {})
    graph.composableTurns.endTurn(turn.turnId)
    let closed = false
    const close = graph.memoryComposition.dispose().then(() => { closed = true })
    await Promise.resolve()
    expect(closed).toBe(false)
    gate.resolve()
    await call
    await close
    expect(closed).toBe(true)
  })


  it('pins one View and renders its projection plus callable Route ids into the LLM Wake', async () => {
    const { workspace, graph } = await fixture()
    await graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: 'Turn-pinned composable context.' })
    await graph.source('documents').mutate<DocumentMutationResult>('mutate', { action: 'create', title: 'Turn route', content: 'turn-route-token' })
    const turn = await graph.composableTurns!.beginTurn('agent-1:1', {
      storage: 'custom', workspaceId: workspace, sessionId: 'agent-1', agentId: 'agent-1',
    })
    const wake = graph.composableTurns!.memoryWake(turn.view.id)

    expect(wake.text).toContain('Turn-pinned composable context.')
    expect(wake.text).toContain('source:mnemon-source-documents/search')
    expect(wake.text).toContain('"inputSchema"')
    expect(wake.text).toContain('"required":["query"]')
    expect(wake.sections.map(section => section.layerId)).toEqual(['runtime', 'documents', 'memory-spaces'])
    expect(graph.composableTurns!.activeTurn('agent-1')).toBe(turn)

    graph.composableTurns!.endTurn(turn.turnId)
    graph.dispose()
  })

  it('executes Routes and reauthorized Actions against the exact leased generation', async () => {
    const { workspace, graph } = await fixture()
    const document = await graph.source('documents').mutate<DocumentMutationResult>('mutate', { action: 'create', title: 'Evidence', content: 'leased-route-token' })
    const turn = await graph.composableTurns!.beginTurn('agent-1:1', {
      storage: 'custom', workspaceId: workspace, sessionId: 'agent-1', agentId: 'agent-1',
    })
    const route = turn.view.routes.find(candidate => candidate.sourceRouteId === 'search')!
    const evidence = await graph.composableTurns!.executeRoute(turn.turnId, route.id, { query: 'leased-route-token' })
    expect(evidence.items.map(item => item.id)).toEqual([document.document.id])

    const offer = turn.view.actionOffers.find(candidate => candidate.sourceActionId === 'mutate')!
    await expect(graph.composableTurns!.executeAction(turn.turnId, offer.id, {
      action: 'add', target: 'memory', content: 'denied',
    }, () => false)).rejects.toThrow('not currently authorized')
    const receipt = await graph.composableTurns!.executeAction(turn.turnId, offer.id, {
      action: 'add', target: 'memory', content: 'authorized composable write',
    }, () => true)
    expect(receipt).toMatchObject({ viewId: turn.view.id, offerId: offer.id, status: 'succeeded' })
    expect((await graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')).entries.some(entry => entry.content === 'authorized composable write')).toBe(true)

    graph.composableTurns!.endTurn(turn.turnId)
    graph.dispose()
  })

  it('keeps a leased turn usable while explicit plugin removal fails closed for new turns', async () => {
    const { workspace, releases, graph } = await fixture()
    const turn = await graph.composableTurns!.beginTurn('agent-1:1', {
      storage: 'custom', workspaceId: workspace, sessionId: 'agent-1', agentId: 'agent-1',
    })
    await Promise.all(releases.map(release => release()))
    await expect(graph.composableTurns!.beginTurn('agent-2:1', {
      storage: 'custom', workspaceId: workspace, sessionId: 'agent-2', agentId: 'agent-2',
    })).rejects.toThrow('No Memory Source')
    expect(graph.composableTurns!.memoryWake(turn.view.id).viewId).toBe(turn.view.id)

    graph.composableTurns!.endTurn(turn.turnId)
    graph.dispose()
  })
})
