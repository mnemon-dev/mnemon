// Opt-in measurement: pnpm bench:algorithms. No wall-clock assertions in CI.
import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { arch, cpus, platform, tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { performance } from 'node:perf_hooks'
import { expect, it, vi } from 'vitest'
import { RuntimeMemoryController } from '../plugins/dsh-mnemon-source-runtime/src/controller.ts'
import type { RuntimeMemoryEntry } from '../plugins/dsh-mnemon-source-runtime/src/contracts.ts'
import { DocumentController } from '../plugins/dsh-mnemon-source-documents/src/controller.ts'
import { HolographicProvider } from '../plugins/dsh-mnemon-provider-holographic/src/driver.ts'
import { descriptor } from '../plugins/dsh-mnemon-provider-holographic/src/descriptor.ts'
import { createMemorySpaceProviderFixture } from '../plugins/dsh-mnemon-source-memory-spaces/src/testing.ts'
import { createRunner } from '../plugins/dsh-mnemon-source-memory-spaces/src/runner.ts'
import { resolveMemorySpacesConfig } from '../plugins/dsh-mnemon-source-memory-spaces/src/config.ts'
import { createRegistry, createService } from '../plugins/dsh-mnemon-source-memory-spaces/tests/providers.ts'
import { MemoryProviderAdapterRegistry } from '../plugins/dsh-mnemon-source-memory-spaces/src/providers/registry.ts'
import { NORMALIZED_RELEVANCE_SCORE } from '../plugins/dsh-mnemon-source-memory-spaces/src/providers/adapter.ts'
import { compositionFixture } from './fixtures/composition.ts'

const now = () => new Date('2026-10-03T00:00:00.000Z')
interface Measurement { name: string; n: number; iterations: number; medianMs: number; p95Ms: number; samplesMs: number[] }

it('measures local algorithms with synthetic, isolated data', async () => {
  const root = mkdtempSync(join(tmpdir(), 'mnemon-algorithms-'))
  const measurements: Measurement[] = []
  const workCounts: Array<{ name: string; n: number; byteLengthCalls: number; byteLengthCharacters: number }> = []
  const sizes = (process.env.MNEMON_BENCH_SIZES ?? '1,10,100,1000').split(',').map(Number)
  if (sizes.some(n => !Number.isSafeInteger(n) || n < 1 || n > 10_000)) throw new Error('invalid benchmark size')
  async function measure(name: string, n: number, run: () => unknown | Promise<unknown>, prepare?: () => void) {
    const iterations = n >= 1000 ? (name === 'recall.pinned' || name === 'storage.batch-prepare' ? 1 : 3) : 10
    for (let i = 0; i < 3; i++) { prepare?.(); await run() }
    await new Promise<void>(done => setImmediate(done))
    const samplesMs: number[] = []
    for (let sample = 0; sample < 9; sample++) {
      let elapsed = 0
      for (let i = 0; i < iterations; i++) {
        prepare?.()
        const start = performance.now()
        await run()
        elapsed += performance.now() - start
      }
      samplesMs.push(elapsed / iterations)
      // Let the worker flush RPC/logs between samples; exclude this yield from timing.
      await new Promise<void>(done => setImmediate(done))
    }
    const sorted = [...samplesMs].sort((a, b) => a - b)
    measurements.push({ name, n, iterations, medianMs: sorted[4]!, p95Ms: sorted[8]!, samplesMs })
    console.log(`${name} N=${n}: ${sorted[4]!.toFixed(3)} ms`)
  }
  try {
    for (const n of sizes) {
      const directory = join(root, String(n))
      mkdirSync(directory)
      const runtime = new RuntimeMemoryController({ effectiveDataDir: () => directory }, now, { memory: 1_048_576, user: 1_048_576 })
      const entries: RuntimeMemoryEntry[] = Array.from({ length: n }, (_, i) => ({
        content: `Synthetic record ${i}: storage decisions and branch scope 测试.`,
        target: 'memory', importance: i % 3 === 0 ? 'critical' : i % 3 === 1 ? 'normal' : 'low',
        created_at: now().toISOString(), updated_at: now().toISOString(), branches: ['main'],
      }))
      const seeded = { version: 1, entries: [...entries, { ...entries[0]!, content: 'remove this sentinel' }] }
      const seed = JSON.stringify(seeded)
      const resetRuntime = () => writeFileSync(runtime.sourcePath, seed)
      resetRuntime()
      const revision = runtime.snapshot().revision
      await measure('runtime.snapshot', n, () => runtime.snapshot())
      runtime.contextProjection('main')
      await measure('runtime.projection', n, () => runtime.contextProjection('main'))
      await measure('runtime.compact', n, () => runtime.compactAndMutate(revision, { action: 'remove', target: 'memory', oldText: 'remove this sentinel' }, entries), resetRuntime)
      resetRuntime()
      const byteLength = Buffer.byteLength
      let byteLengthCalls = 0, byteLengthCharacters = 0
      const counter = vi.spyOn(Buffer, 'byteLength').mockImplementation((value, encoding) => {
        byteLengthCalls += 1
        if (typeof value === 'string') byteLengthCharacters += value.length
        return byteLength(value, encoding)
      })
      try { await runtime.compactAndMutate(revision, { action: 'remove', target: 'memory', oldText: 'remove this sentinel' }, entries) }
      finally { counter.mockRestore() }
      workCounts.push({ name: 'runtime.compact', n, byteLengthCalls, byteLengthCharacters })
      await measure('runtime.write', n, () => runtime.mutate({ action: 'add', target: 'memory', content: 'new synthetic record' }), resetRuntime)

      const documents = new DocumentController(directory, 16 * 1024 * 1024, now)
      for (let i = 0; i < n; i++) await documents.mutate({ action: 'create', title: `Storage ${i}`, description: 'Synthetic archive', content: `${'## Storage schema durable query. '.repeat(128)} Record ${i}` })
      await measure('documents.catalog', n, () => documents.catalog())
      await measure('documents.search', n, () => documents.search('storage schema', { limit: 10 }))
      const document = documents.catalog().documents[0]!
      await measure('documents.write', n, () => documents.mutate({ action: 'update', id: document.id, description: 'Synthetic updated description' }))

      const dataPath = join(directory, 'holographic.json')
      const { authority, body } = createMemorySpaceProviderFixture(descriptor, { dataPath }, { dataDir: directory })
      const provider = new HolographicProvider(authority)
      const facts = entries.map((entry, i) => ({ id: `fact-${i}`, content: `Storage schema ${i}: ${entry.content}`, category: 'fact', tags: ['storage'], entities: ['Storage'], trustScore: 0.3 + (i * 37 % 70) / 100, createdAt: entry.created_at, updatedAt: entry.updated_at }))
      const holoSeed = JSON.stringify({ version: 1, facts })
      const resetHolo = () => writeFileSync(dataPath, holoSeed)
      resetHolo()
      await measure('holographic.search', n, () => provider.search(body, { query: 'storage query schema', limit: 10 }))
      await measure('holographic.related', n, () => provider.related(body, 'fact-0', 2))
      await measure('holographic.write', n, () => provider.remember(body, { content: 'New synthetic memory' }), resetHolo)

      const config = resolveMemorySpacesConfig({ dataDir: join(directory, 'spaces'), cliPath: join(root, 'missing-cli'), writeEnabled: true })
      const runner = createRunner(config, async () => { throw new Error('benchmark must not start external processes') })
      const registry = createRegistry(runner, false, now)
      const discovered = Array.from({ length: n }, (_, i) => ({ externalId: `namespace-${i}`, name: `Space ${i}`, description: 'Synthetic namespace', connection: {} }))
      registry.syncProviderService('holographic', {}, discovered)
      await measure('registry.discovery', n, () => registry.syncProviderService('holographic', {}, discovered))
      const nativeConfig = resolveMemorySpacesConfig({ dataDir: join(directory, 'native'), cliPath: join(root, 'missing-cli') })
      for (let i = 0; i < n; i++) {
        const path = join(nativeConfig.dataDir!, 'data', `store-${i}`)
        mkdirSync(path, { recursive: true })
        writeFileSync(join(path, 'mnemon.db'), 'Synthetic discovery marker, not a database')
      }
      const native = createRegistry(createRunner(nativeConfig, async () => { throw new Error('unexpected CLI') }), true, now)
      await measure('registry.native-catalog', n, () => native.list())

      // Local orchestration only: fixed deterministic provider responses, no network/LLM/CLI.
      const rows = Array.from({ length: 30 }, (_, i) => ({ id: `row-${i}`, content: `Synthetic evidence ${i}`, score: 0.9 - i / 100 }))
      const adapters = new MemoryProviderAdapterRegistry([{ id: 'holographic', create: () => ({
        id: 'holographic', scoreSemantics: NORMALIZED_RELEVANCE_SCORE,
        status: async () => ({ healthy: true }), search: async () => ({ results: rows }),
        list: async () => rows, graph: async () => ({ nodes: [], edges: [], generatedAt: now().toISOString() }),
        remember: async () => ({ action: 'skipped' }), rememberMany: async (_body, requests) => requests.map(() => ({ action: 'skipped' })),
      }) }])
      const service = createService(runner, config, registry, undefined, adapters)
      const bodies = registry.active()
      expect(bodies).toHaveLength(n)
      await measure('recall.federated', n, () => service.search({ query: 'evidence', mode: 'basic', limit: 10 }))
      await measure('recall.pinned', n, () => service.search({ query: 'evidence', mode: 'basic', limit: 10, memoryBodyIds: bodies.map(body => body.id) }))
      const requests = Array.from({ length: n }, (_, i) => ({ content: `Batch item ${i}`, memoryBodyId: bodies[0]!.id }))
      await measure('storage.batch-prepare', n, () => service.rememberMany(requests))
      // Isolate batch grouping from registry projection and provider/storage latency.
      const get = vi.spyOn(service.memorySpaces, 'get').mockReturnValue(bodies[0]!)
      await measure('storage.batch-grouping', n, () => service.rememberMany(requests))
      get.mockRestore()
      await service.dispose()

      const composition = await compositionFixture()
      try {
        for (let i = 0; i < n; i++) await composition.graph.source('runtime').mutate('mutate', { action: 'add', target: 'memory', content: `r${i}` })
        const generation = composition.graph.memoryComposition!.current()!
        const request = { scope: { storage: 'custom' as const, workspaceId: composition.workspace, sessionId: 'benchmark' }, scenario: 'benchmark', budget: { maxProjectionCharacters: 65_536, maxRoutes: 16, maxActions: 16, maxEvidenceResults: 16, maxEvidenceCharacters: 16_384 } }
        await measure('core.compose', n, () => generation.compose(request))
      } finally { await composition.dispose() }
    }
    const result = { schemaVersion: 1, date: '2026-10-03', revision: execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim(), node: process.version, platform: platform(), arch: arch(), cpu: cpus()[0]?.model, sizes, samples: 9, warmups: 3, workCounts, measurements }
    const output = resolve(process.env.MNEMON_BENCH_OUTPUT ?? '.cache/algorithm-benchmark.json')
    mkdirSync(dirname(output), { recursive: true })
    writeFileSync(output, `${JSON.stringify(result, null, 2)}\n`)
    console.log(`Wrote ${measurements.length} measurements to ${output}`)
  } finally { vi.restoreAllMocks(); rmSync(root, { recursive: true, force: true }) }
})
