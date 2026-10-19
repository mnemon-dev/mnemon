import { afterEach, describe, expect, it, vi } from 'vitest'
import { writeFileSync } from 'node:fs'
import type { RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
import * as scoped from 'dsh-mnemon-strategy-scoped'
import * as light from 'dsh-mnemon-strategy-light-context'
import * as capture from 'dsh-mnemon-strategy-auto-capture'
import { modelMemoryWake } from '../src/host/view-presentation.ts'
import { compositionFixture } from './fixtures/composition.ts'
const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
async function fixture() { const value = await compositionFixture(); fixtures.push(value); return value }
afterEach(async () => { for (const value of fixtures.splice(0)) await value.dispose() })

describe('LLM View publication boundary', () => {
  it('describes default projections as bounded when metadata clips a store within both byte limits', async () => {
    const { graph, workspace } = await fixture()
    const source = graph.source('runtime')
    const empty = await source.read<RuntimeMemorySnapshot>('snapshot')
    const entries = (['user', 'memory'] as const).flatMap(target =>
      Array.from({ length: target === 'user' ? 500 : 1200 }, (_, index) => ({
        content: `${target === 'user' ? 'u' : 'm'}${index}`, target, importance: 'normal',
        created_at: '2026-09-01T00:00:00.000Z', updated_at: '2026-09-01T00:00:00.000Z',
      })),
    )
    writeFileSync(empty.sourcePath, JSON.stringify({ version: 1, entries }))
    const stored = await source.read<RuntimeMemorySnapshot>('snapshot')
    expect(stored.targets.user.used).toBeLessThanOrEqual(stored.targets.user.limit)
    expect(stored.targets.memory.used).toBeLessThanOrEqual(stored.targets.memory.limit)
    const turn = await graph.composableTurns.beginTurn('metadata:dense', { storage: 'custom', workspaceId: workspace })
    const wake = modelMemoryWake(graph, turn)
    expect(wake.text).toContain('[importance=normal;')
    expect(wake.text).not.toContain('\nm1199\n')
    expect(wake.guidance?.system).toContain('budget-limited projection')
    expect(wake.guidance?.system).toContain('absence is not evidence that an entry was deleted')
    expect(wake.guidance?.system).not.toContain('complete projection')
    expect((await source.read<RuntimeMemorySnapshot>('snapshot')).entries).toEqual(entries)
    graph.composableTurns.endTurn(turn.turnId)
  })

  it.each([false, true])('preserves Runtime metadata in model context and immutable turns (extensions=%s)', async extensions => {
    vi.useFakeTimers({ toFake: ['Date'] })
    try {
      vi.setSystemTime(new Date('2026-09-01T08:00:00.000Z'))
      const f = await fixture()
      const source = f.graph.source('runtime')
      await source.mutate('mutate', { action: 'add', target: 'user', content: 'Prefer concise replies.', importance: 'critical' })
      await source.mutate('mutate', { action: 'add', target: 'memory', content: 'Use pnpm.', importance: 'low' })
      if (extensions) {
        await f.mount(scoped, { instanceId: 'scoped' })
        await f.mount(light, { instanceId: 'light', config: { maxProjectionCharacters: 1500 } })
        await f.mount(capture, { instanceId: 'capture' })
      }
      vi.setSystemTime(new Date('2026-09-15T08:00:00.000Z'))
      const scope = { storage: 'custom' as const, workspaceId: f.workspace, agentId: 'agent' }
      const first = await f.graph.composableTurns.beginTurn('metadata:1', scope)
      const wake = modelMemoryWake(f.graph, first)
      expect(wake.text).toContain('[importance=critical; created=14d; updated=14d]\nPrefer concise replies.')
      expect(wake.text).toContain('[importance=low; created=14d; updated=14d]\nUse pnpm.')
      expect(wake.guidance?.system).toMatch(/current turn wins|Current user instructions win/u)
      if (extensions) {
        expect(first.view.strategyExtensions).toHaveLength(3)
        expect(first.view.projection.reduce((sum, fragment) => sum + fragment.text.length, 0)).toBeLessThanOrEqual(1500)
      }
      vi.setSystemTime(new Date('2026-09-16T08:00:00.000Z'))
      expect(await f.graph.composableTurns.beginTurn('metadata:1', scope)).toBe(first)
      expect(modelMemoryWake(f.graph, first)).toEqual(wake)
      const next = await f.graph.composableTurns.beginTurn('metadata:2', scope)
      expect(modelMemoryWake(f.graph, next).text).toContain('[importance=low; created=15d; updated=15d]\nUse pnpm.')
      expect(next.view.projection.find(fragment => fragment.sourceInstanceKey === 'source:mnemon-source-runtime')?.revision)
        .toBe(first.view.projection.find(fragment => fragment.sourceInstanceKey === 'source:mnemon-source-runtime')?.revision)
      f.graph.composableTurns.endTurn(first.turnId)
      f.graph.composableTurns.endTurn(next.turnId)
    } finally { vi.useRealTimers() }
  })

  it('projects exact hot memory and compact Source covers without disclosing namespaces or control-plane paths', async () => {
    const { graph, workspace, memorySpace } = await fixture()
    const body = await memorySpace()
    await graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: 'Use immutable per-turn context.' })
    await graph.source('documents').mutate('mutate', { action: 'create', title: 'Private title', content: 'Detailed source document.' })
    const turn = await graph.composableTurns.beginTurn('agent:1', { storage: 'custom', workspaceId: workspace, agentId: 'agent' })
    const wake = graph.composableTurns.memoryWake(turn.view.id)
    expect(wake.text).toContain('Use immutable per-turn context.')
    expect(wake.text).toContain('1 active project Document')
    expect(wake.text).not.toContain(body.id)
    expect(wake.text).not.toContain('Private title')
    expect(wake.text).not.toContain('Detailed source document.')
    expect(wake.text).not.toContain(workspace)
    // Opaque read grants remain on the Host, never in the Wake delivered to the model.
    expect(turn.view.readGrants).toHaveLength(2)
    graph.composableTurns.endTurn(turn.turnId)
  })
  it('holds a turn immutable and observes committed Source changes only at the next boundary', async () => {
    const { graph, workspace } = await fixture()
    const scope = { storage: 'custom' as const, workspaceId: workspace, agentId: 'agent' }
    const turns = graph.composableTurns
    const source = graph.source('runtime')
    await source.mutate('mutate', { action: 'add', target: 'memory', content: 'First revision.' })
    const first = await turns.beginTurn('agent:1', scope)
    const wake = turns.memoryWake(first.view.id)
    await source.mutate('mutate', { action: 'add', target: 'memory', content: 'Second revision.' })
    await graph.source('documents').mutate('mutate', { action: 'create', title: 'Next View', content: 'Not eager content.' })
    expect(await turns.beginTurn('agent:1', scope)).toBe(first)
    expect(turns.memoryWake(first.view.id)).toEqual(wake)
    turns.endTurn(first.turnId)
    const next = await turns.beginTurn('agent:2', scope)
    expect(next.view.id).not.toBe(first.view.id)
    expect(turns.memoryWake(next.view.id).text).toContain('Second revision.')
    expect(turns.memoryWake(next.view.id).text).not.toContain('Not eager content.')
    turns.endTurn(next.turnId)
  })
})
