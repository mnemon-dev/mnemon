import { performance } from 'node:perf_hooks'
import { afterEach, describe, expect, it } from 'vitest'
import { compositionFixture } from './fixtures/composition.ts'
import type { DocumentMutationResult } from 'dsh-mnemon-source-documents/contracts'
import * as scoped from 'dsh-mnemon-strategy-scoped'
import * as light from 'dsh-mnemon-strategy-light-context'
import * as capture from 'dsh-mnemon-strategy-auto-capture'
const fixtures: Awaited<ReturnType<typeof compositionFixture>>[] = []
async function fixture() { const value = await compositionFixture(); fixtures.push(value); return value }
afterEach(async () => { for (const value of fixtures.splice(0)) await value.dispose() })

describe('Composable View performance fences', () => {
  it.each(['default', 'additive'])('composes repeated three-Source Views within a bounded local control-plane budget (%s)', async mode => {
    const f = await fixture()
    const { workspace, graph } = f
    if (mode === 'additive') {
      await f.mount(scoped, { instanceId: 'scoped' })
      await f.mount(light, { instanceId: 'light' })
      await f.mount(capture, { instanceId: 'capture' })
    }
    await graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: 'Performance fixture.' })
    await graph.source('documents').mutate<DocumentMutationResult>('mutate', { action: 'create', title: 'Performance', content: 'Bounded fixture.' })
    const generation = graph.memoryComposition!.current()!
    const request = {
      scope: { storage: 'custom' as const, workspaceId: workspace, sessionId: 'performance' },
      scenario: 'performance.fixture',
      budget: {
        maxProjectionCharacters: 65_536,
        maxRoutes: 16,
        maxActions: 16,
        maxEvidenceResults: 16,
        maxEvidenceCharacters: 16_384,
      },
    }

    const warm = await generation.compose(request)
    expect(warm.strategyExtensions?.length ?? 0).toBe(mode === 'additive' ? 3 : 0)
    const startedWall = performance.now()
    const startedCpu = process.cpuUsage()
    for (let index = 0; index < 100; index += 1) await generation.compose(request)
    const elapsedWall = performance.now() - startedWall
    const elapsedCpu = process.cpuUsage(startedCpu)
    const cpuMilliseconds = (elapsedCpu.user + elapsedCpu.system) / 1_000

    // CPU time isolates the control-plane cost from other Vitest workers. The
    // wall fence only catches accidental I/O waits, so it tolerates a loaded
    // machine: parallel suites have pushed healthy runs past five seconds.
    expect(cpuMilliseconds).toBeLessThan(2_000)
    expect(elapsedWall).toBeLessThan(15_000)
    graph.dispose()
    // The test timeout must exceed the wall fence plus fixture setup, or the fence never applies.
  }, 30_000)
})
