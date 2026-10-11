import type { Context } from '@deepseek-ai/cordis'
import { describe, expect, it } from 'vitest'
import { createMemoryMutationReceipt, defineMemorySource, defineMemoryViewExtension, installMemory } from 'dsh-mnemon/extension-sdk'
import { COMPOSABLE_MEMORY_API_VERSION, type MemoryCapability } from 'dsh-mnemon/contracts'
import { DEFAULT_MEMORY_VIEW_BUDGET, MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as general from '../src/index.ts'

interface SourceShape {
  typeId: string
  role: string
  routes?: string[]
  actions?: string[]
  available?: boolean
}

function source({ typeId, role, routes = [], actions = [], available = true }: SourceShape) {
  const capabilities: MemoryCapability[] = ['project', ...(routes.length === 0 ? [] : ['search' as const]), ...(actions.length === 0 ? [] : ['write' as const])]
  return {
    inject: ['mnemonMemory'],
    apply(ctx: Context) {
      installMemory(ctx, { sources: [defineMemorySource({
        manifest: { apiVersion: COMPOSABLE_MEMORY_API_VERSION, kind: 'source', typeId, packageName: `external-test-${typeId}`,
          role, capabilities, consistency: 'exact-snapshot',
          routes: routes.map(id => ({ id, description: `Search ${typeId}.`, capability: 'search', inputSchema: { type: 'object' }, maxCalls: 2 })),
          actions: actions.map(id => ({ id, description: `Write ${typeId}.`, capability: 'write', inputSchema: { type: 'object' } })) },
        create: context => ({
          facts: () => ({ sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: typeId, role, availability: available ? 'ready' : 'unavailable',
            revision: 'r1', capabilities, routeIds: routes, actionIds: actions }),
          project: request => ({
            fragments: request.includeProjection ? [{ id: typeId, sourceInstanceKey: context.sourceInstanceKey,
              mode: request.mode, revision: 'r1', text: typeId.repeat(20_000).slice(0, request.maxCharacters) }] : [],
            ...(routes.length === 0 ? {} : { readGrant: { id: `${context.sourceInstanceKey}/grant`, sourceInstanceKey: context.sourceInstanceKey,
              schema: 'external-test/grant-v1', value: {}, revision: 'r1', consistency: 'exact-snapshot' as const } }),
          }),
          query: async request => ({ id: 'evidence', viewId: request.view.id, routeId: request.route.id, sourceInstanceKey: context.sourceInstanceKey,
            observedAt: new Date(0).toISOString(), items: [], truncated: false }),
          mutate: async request => createMemoryMutationReceipt(request.view.id, request.offer.id, context.sourceInstanceKey, 'r2', request.input, 'committed'),
        }),
      })] })
    },
  }
}

const catalog: SourceShape[] = [
  { typeId: 'runtime', role: 'working-context', actions: ['mutate'] },
  { typeId: 'documents', role: 'narrative', routes: ['search', 'inspect'], actions: ['create'] },
  { typeId: 'tasks', role: 'task-log', routes: ['list'], actions: ['append', 'close'] },
  { typeId: 'offline', role: 'durable-evidence', routes: ['recall'], available: false },
]

async function fixture(options: { config?: general.Config } = {}, shapes = catalog) {
  const runner = new MemoryCompositionRunner({ strategyTypeId: 'general' })
  for (const shape of shapes) await runner.mount(source(shape), { instanceId: shape.typeId })
  await runner.mount(general, { instanceId: 'general', ...(options.config === undefined ? {} : { config: options.config }) })
  return runner
}

// The routing guidance lists admitted Sources in View order.
const admitted = (routing: string | undefined) => (routing ?? '').split('\n').slice(1).map(line => line.split(' ')[1])

const extension = <K extends 'selection' | 'projection' | 'capture'>(slot: K, value: Parameters<typeof defineMemoryViewExtension<K>>[0]['contribute'] extends (...args: never[]) => infer R ? R : never) => ({
  inject: ['mnemonMemory'],
  apply(ctx: Context) {
    installMemory(ctx, { strategyExtensions: [defineMemoryViewExtension({ typeId: `test-${slot}`, packageName: `external-test-${slot}`, slot, contribute: () => value })] })
  },
})

describe('general main Strategy', () => {
  it('admits every available Source of any role and keeps working-context Sources resident', async () => {
    const runner = await fixture()
    try {
      const turn = await runner.beginTurn()
      const view = turn.view
      expect(view.strategyTypeId).toBe('general')
      expect(admitted(view.guidance?.routing)).toEqual(['source:runtime', 'source:documents', 'source:tasks'])
      const resident = view.projection.filter(fragment => fragment.mode === 'eager')
      const routed = view.projection.filter(fragment => fragment.mode === 'routed')
      expect(resident.map(fragment => fragment.sourceInstanceKey)).toEqual(['source:runtime'])
      expect(routed.map(fragment => fragment.sourceInstanceKey).sort()).toEqual(['source:documents', 'source:tasks'])
      expect(resident[0]!.text.length).toBeGreaterThan(routed[0]!.text.length * 8)
      expect(view.routes.map(route => route.sourceRouteId).sort()).toEqual(['inspect', 'list', 'search'])
      expect(view.actionOffers.map(offer => offer.sourceActionId).sort()).toEqual(['append', 'close', 'create', 'mutate'])
      expect(view.guidance?.system).toContain('MNEMON GENERAL MEMORY PROTOCOL')
      // Named Source tools are listed apart from the generic Route envelope; the protocol names both.
      expect(view.guidance?.system).toMatch(/MNEMON VIEW TOOLS[\s\S]*MNEMON VIEW ROUTES/u)
      expect(view.guidance?.routing).toContain('- source:tasks (tasks, role task-log): on demand, 1 route(s), 2 action(s)')
      expect(view.guidance?.routing).toContain('- source:runtime (runtime, role working-context): resident, 0 route(s), 1 action(s)')
      turn.release()
    } finally { await runner.dispose() }
  })

  it('deals Routes and Actions round-robin within the request budget', async () => {
    const runner = await fixture()
    try {
      const turn = await runner.beginTurn({ budget: { ...DEFAULT_MEMORY_VIEW_BUDGET, maxRoutes: 2, maxActions: 3 } })
      // Every Source receives its first operation before any receives a second.
      expect(turn.view.routes.map(route => `${route.sourceInstanceKey}/${route.sourceRouteId}`)).toEqual(['source:documents/search', 'source:tasks/list'])
      expect(turn.view.actionOffers.map(offer => `${offer.sourceInstanceKey}/${offer.sourceActionId}`).sort())
        .toEqual(['source:documents/create', 'source:runtime/mutate', 'source:tasks/append'])
      turn.release()
    } finally { await runner.dispose() }
  })

  it('follows configured resident Sources and appends operator guidance', async () => {
    const runner = await fixture({ config: { residentSourceKeys: ['source:tasks'], instruction: 'Keep task notes short.' } })
    try {
      const turn = await runner.beginTurn()
      expect(turn.view.projection.filter(fragment => fragment.mode === 'eager').map(fragment => fragment.sourceInstanceKey)).toEqual(['source:tasks'])
      expect(turn.view.guidance?.system).toMatch(/PROTOCOL[\s\S]*Keep task notes short\./u)
      turn.release()
    } finally { await runner.dispose() }
    expect(() => general.createGeneralStrategy({ residentSourceKeys: ['tasks'] })).toThrow('exact Source instance keys')
  })

  it('accepts the standard selection, projection and capture slots', async () => {
    const runner = await fixture()
    try {
      await runner.mount(extension('selection', { sourceKeys: ['source:tasks', 'source:runtime'], writableSourceKeys: ['source:tasks'] }), { instanceId: 'scope' })
      await runner.mount(extension('projection', { maxProjectionCharacters: 100 }), { instanceId: 'light' })
      await runner.mount(extension('capture', { instruction: 'Record one durable task fact.', actionIds: ['append'] }), { instanceId: 'capture' })
      const turn = await runner.beginTurn()
      expect(turn.view.strategyExtensions?.map(item => item.slot).sort()).toEqual(['capture', 'projection', 'selection'])
      expect(admitted(turn.view.guidance?.routing)).toEqual(['source:tasks', 'source:runtime'])
      expect(turn.view.projection.reduce((sum, fragment) => sum + fragment.text.length, 0)).toBeLessThanOrEqual(100)
      expect(turn.view.actionOffers.map(offer => offer.sourceInstanceKey)).toEqual(['source:tasks', 'source:tasks'])
      expect(turn.view.guidance?.system).toContain('MNEMON OPTIONAL AUTO CAPTURE\nRecord one durable task fact.\nEligible Source instances: source:tasks')
      turn.release()
    } finally { await runner.dispose() }
  })

  it('describes an empty composition instead of failing', async () => {
    const runner = await fixture({}, [{ typeId: 'offline', role: 'durable-evidence', routes: ['recall'], available: false }])
    try {
      const turn = await runner.beginTurn()
      expect(turn.view.routes).toEqual([])
      expect(turn.view.guidance?.routing).toBe('No memory Source is currently admitted; continue without memory.')
      turn.release()
    } finally { await runner.dispose() }
  })

  it('publishes one main Strategy plugin with configurable fields', () => {
    expect(general.memoryPlugin).toMatchObject({ roles: ['strategy'], provides: [{ id: 'strategy' }, { id: 'strategy.general' }] })
    expect(general.memoryStrategyConfiguration.fields.map(field => field.key)).toEqual(['residentSourceKeys', 'instruction'])
    expect(general.GENERAL_VIEW_STRATEGY.manifest).toMatchObject({ typeId: 'general', supportedSourceRoles: ['*'], extensionSlots: ['selection', 'projection', 'capture'] })
  })
})
