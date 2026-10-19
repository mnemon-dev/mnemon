import { afterEach, describe, expect, it, vi } from 'vitest'
import * as general from 'dsh-mnemon-strategy-general'
import type { HostAgent, HostContextShape, ToolDefinition } from '../src/host/dsh.ts'
import { agentScope } from '../src/host/runtime.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { compositionFixture } from './fixtures/composition.ts'
import { sessionLog } from './fixtures/session-log.ts'

const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
afterEach(async () => { for (const fixture of fixtures.splice(0)) await fixture.dispose() })

describe('General strategy named reads at the Host JSON boundary', () => {
  it.each([{}, { score: 0 }, { revision: 'r1' }, { score: 0.5, revision: 'r2' }])(
    'preserves optional Evidence metadata without introducing undefined (%j)', async metadata => {
      const f = await compositionFixture({ memoryTopology: { strategyId: 'general' } })
      fixtures.push(f)
      await f.mount(general, { instanceId: 'mnemon-strategy-general' })
      await f.releases[3]!()
      const body = await f.memorySpace()
      await f.graph.source('memory-spaces').mutate('remember', {
        memoryBodyId: body.id, content: 'General recall transport sentinel.', category: 'fact', source: 'external', entities: ['shared-entity'],
      })
      await f.graph.source('memory-spaces').mutate('remember', {
        memoryBodyId: body.id, content: 'Rollback trace.', category: 'fact', source: 'external', entities: ['shared-entity'],
      })
      const agent = { id: 'root', session: { header: { cwd: f.workspace }, ...sessionLog() } } as unknown as HostAgent
      const turn = await f.graph.composableTurns.beginTurn('general:reads', agentScope(agent, f.config))
      expect(turn.view.strategyTypeId).toBe('general')
      const route = turn.view.routes.find(item => item.sourceRouteId === 'recall')!
      const source = f.graph.memoryComposition.current()!.sourceRuntime(route.sourceInstanceKey)!
      const query = source.query!.bind(source)
      // Sources own these optional fields. General forwards Evidence directly,
      // without the Layered strategy's pre-rendered product output.
      source.query = vi.fn(async (request: Parameters<typeof query>[0]) => {
        const evidence = await query(request)
        return { ...evidence, items: evidence.items.map(({ score: _score, revision: _revision, ...item }) => ({ ...item, ...metadata })) }
      })
      const registered = new Map<string, ToolDefinition>()
      const coordinator = new MnemonSubagentCoordinator({} as never, f.live)
      registerTools({ tools: { register: (tool: ToolDefinition) => { registered.set(tool.name, tool) } } } as unknown as HostContextShape, f.live, coordinator)
      const execute = (name: string, args: object) => (registered.get(name)!.execute as Function)(args, { agent, signal: new AbortController().signal })
      const recall = await execute('mnemon_recall', { query: 'General recall transport sentinel', mode: 'keyword' })
      expect(recall.results).toHaveLength(1)
      expect(JSON.parse(JSON.stringify(recall))).toStrictEqual(recall)
      expect(recall.results[0]).toMatchObject({ content: 'General recall transport sentinel.', ...metadata })
      for (const field of ['score', 'revision']) {
        if (!Object.hasOwn(metadata, field)) expect(recall.results[0]).not.toHaveProperty(field)
      }
      const related = await execute('mnemon_related', { id: recall.results[0].id, memoryBodyId: body.id })
      expect(related.results.length).toBeGreaterThan(0)
      expect(JSON.parse(JSON.stringify(related))).toStrictEqual(related)
    },
  )
})
