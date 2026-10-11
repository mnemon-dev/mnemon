import type { Context } from '@deepseek-ai/cordis'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineMemorySource, installMemory } from 'dsh-mnemon/extension-sdk'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import type { DocumentMutationResult } from 'dsh-mnemon-source-documents/contracts'
import type { HostAgent, HostContextShape, ToolDefinition } from '../src/host/dsh.ts'
import { agentScope } from '../src/host/runtime.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { modelMemoryWake } from '../src/host/view-presentation.ts'
import { compositionFixture } from './fixtures/composition.ts'
import { sessionLog } from './fixtures/session-log.ts'

const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
afterEach(async () => { for (const f of fixtures.splice(0)) await f.dispose() })
async function fixture() { const f = await compositionFixture(); fixtures.push(f); return f }

describe('default three-tier Host presentation and shared execution', () => {
  it('keeps instructions separate and advertises named bindings without duplicating their schemas', async () => {
    const f = await fixture()
    await f.memorySpace()
    const content = 'Keep {{model}} and <instruction> as quoted historical data.'
    await f.graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content })
    const turn = await f.graph.composableTurns.beginTurn('root:1', { storage: 'custom', workspaceId: f.workspace, agentId: 'root' })
    const generic = f.graph.composableTurns.memoryWake(turn.view.id)
    const wake = modelMemoryWake(f.graph, turn)
    expect(wake.text).toContain(content)
    expect(wake.text).toContain('mnemon_document_search')
    expect(wake.text).toContain('mnemon_recall')
    expect(wake.text).not.toMatch(/inputSchema|readGrant|memoryBodyIds|MNEMON RUNTIME MEMORY PROTOCOL/)
    expect(wake.guidance?.system).toContain('MNEMON RUNTIME MEMORY PROTOCOL')
    expect(wake.guidance?.routing).toContain('Search Mnemon Documents')
    expect(generic.text.length - wake.text.length).toBeGreaterThan(2_000)
    const before = wake.text
    await f.graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: 'Later fact' })
    expect(modelMemoryWake(f.graph, turn).text).toBe(before)
  })

  it('keeps external Source ids and schemas visible without exposing its private grant', async () => {
    const f = await fixture()
    await f.releases[1]!() // Replace only Documents with an independently authored narrative Source.
    const source = defineMemorySource({
      manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId: 'notes', packageName: 'dsh-mnemon-source-notes',
        role: 'narrative', capabilities: ['project', 'search'], consistency: 'exact-snapshot',
        routes: [{ id: 'search', description: 'Search private notes.', capability: 'search', maxCalls: 1, inputSchema: { type: 'object', required: ['query'] } }] },
      create: context => ({
        facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'notes', role: 'narrative', availability: 'ready', revision: 'notes-1',
          capabilities: ['project', 'search'], routeIds: ['search'], actionIds: [] }),
        project: request => ({ fragments: [{ id: 'cover', sourceInstanceKey: context.sourceInstanceKey, mode: request.mode, revision: 'notes-1', text: 'Private notes cover' }],
          readGrant: { id: 'notes-grant', sourceInstanceKey: context.sourceInstanceKey, schema: 'notes/v1', value: { secret: 'never-print-grant' }, revision: 'notes-1', consistency: 'exact-snapshot' } }),
        query: request => ({ id: 'notes-evidence', viewId: request.view.id, routeId: request.route.id, sourceInstanceKey: context.sourceInstanceKey,
          observedAt: new Date().toISOString(), items: [{ id: 'note', text: 'External evidence', provenance: {} }], truncated: false }),
      }),
    })
    await f.mount({ inject: ['mnemonMemory'], apply(ctx: Context) { installMemory(ctx, { sources: [source] }) } }, { instanceId: 'external-notes' })
    const turn = await f.graph.composableTurns.beginTurn('external:1', { storage: 'custom', workspaceId: f.workspace, agentId: 'external' })
    const wake = modelMemoryWake(f.graph, turn)
    expect(wake.text).toContain('source:external-notes/search')
    expect(wake.text).toContain('inputSchema')
    expect(wake.text).toContain('Private notes cover')
    expect(wake.text).not.toContain('never-print-grant')
    expect(wake.text).not.toContain('mnemon_document_search')
    expect(wake.guidance?.routing).toBeUndefined()
    const evidence = await f.graph.composableTurns.executeRoute(turn.turnId, 'source:external-notes/search', { query: 'private' })
    expect(evidence.items[0]?.text).toBe('External evidence')
  })

  it.each(['named-first', 'generic-first'])('shares one Documents policy across both real tool entries (%s)', async order => {
    const f = await fixture()
    await f.graph.source('documents').mutate<DocumentMutationResult>('mutate', { action: 'create', title: 'Protocol record', content: 'protocol-token '.repeat(1_000) })
    const agent = { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
    const registered = new Map<string, ToolDefinition>()
    const coordinator = new MnemonSubagentCoordinator({} as never, f.live)
    registerTools({ tools: { register: (tool: ToolDefinition) => { registered.set(tool.name, tool) } } } as unknown as HostContextShape, f.live, coordinator)
    const turn = await f.graph.composableTurns.beginTurn('root:tools', agentScope(agent, f.config))
    const route = turn.view.routes.find(route => route.sourceRouteId === 'search')!
    const read = vi.spyOn(f.graph.memoryComposition.current()!.sourceRuntime(route.sourceInstanceKey)!, 'query')
    const execute = async (entry: 'named' | 'generic') => (registered.get(entry === 'named' ? 'mnemon_document_search' : 'mnemon_view_route')!.execute as Function)(
      entry === 'named' ? { query: 'protocol-token' } : { routeId: route.id, input: { query: 'protocol-token' } }, { agent, signal: new AbortController().signal })
    const first = await execute(order === 'named-first' ? 'named' : 'generic')
    expect(first.results).toHaveLength(1)
    expect(first.results[0].content.length).toBeLessThanOrEqual(2_600)
    expect(first).not.toHaveProperty('items')
    const firstEntry = order === 'named-first' ? 'named' : 'generic'
    const firstArgs = firstEntry === 'named' ? { query: 'protocol-token' } : { routeId: route.id, input: { query: 'protocol-token' } }
    const meta = registered.get(firstEntry === 'named' ? 'mnemon_document_search' : 'mnemon_view_route')!.output.presentationMeta!(firstArgs, first)
    expect(meta).toMatchObject({ schema: 'dsh-mnemon.activity/v1', kind: 'read', operationId: firstEntry === 'named' ? 'search' : 'view-route',
      items: [{ id: expect.any(String), title: 'Protocol record', excerpt: expect.stringContaining('protocol-token') }] })
    if (firstEntry === 'generic') expect(meta).toMatchObject({ reference: route.id })
    const second = await execute(order === 'named-first' ? 'generic' : 'named')
    expect(second).toMatchObject({ notRun: true, results: [] })
    expect(read).toHaveBeenCalledOnce()
  })

  it.each(['named-first', 'generic-first'])('shares Recall replay and refinement across real tool entries (%s)', async order => {
    const f = await fixture()
    const body = await f.memorySpace()
    await f.graph.source('memory-spaces').mutate('remember', { memoryBodyId: body.id, content: 'The protocol-token is a saved durable fact.', category: 'fact', source: 'external' })
    const agent = { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
    const registered = new Map<string, ToolDefinition>()
    const coordinator = new MnemonSubagentCoordinator({} as never, f.live)
    registerTools({ tools: { register: (tool: ToolDefinition) => { registered.set(tool.name, tool) } } } as unknown as HostContextShape, f.live, coordinator)
    const turn = await f.graph.composableTurns.beginTurn('root:recall', agentScope(agent, f.config))
    const route = turn.view.routes.find(route => route.sourceRouteId === 'recall')!
    const read = vi.spyOn(f.graph.memoryComposition.current()!.sourceRuntime(route.sourceInstanceKey)!, 'query')
    const execute = async (entry: 'named' | 'generic', query: string) => (registered.get(entry === 'named' ? 'mnemon_recall' : 'mnemon_view_route')!.execute as Function)(
      entry === 'named' ? { query } : { routeId: route.id, input: { query } }, { agent, signal: new AbortController().signal })
    const first = await execute(order === 'named-first' ? 'named' : 'generic', 'protocol-token')
    expect(first.results).toHaveLength(1)
    expect(first.results[0]).toHaveProperty('createdAt')
    expect(first.results[0]).toHaveProperty('importance')
    expect(first.results[0]).not.toHaveProperty('score')
    const replay = await execute(order === 'named-first' ? 'generic' : 'named', 'PROTOCOL TOKEN!')
    expect(replay.results).toEqual(first.results)
    expect(read).toHaveBeenCalledOnce()
    await execute('generic', 'durable fact')
    expect(read).toHaveBeenCalledTimes(2)
    const exhausted = await execute('named', 'third query')
    expect(exhausted.hint).toContain('budget is exhausted')
    expect(read).toHaveBeenCalledTimes(2)
  })
})
