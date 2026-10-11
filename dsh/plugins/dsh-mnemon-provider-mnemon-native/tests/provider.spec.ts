import { existsSync, readFileSync } from 'node:fs'
import { describe, expect, it, vi } from 'vitest'
import type { MemorySpaceNativeRunner } from 'dsh-mnemon-source-memory-spaces/provider-sdk'
import { createMemorySpaceProviderFixture, mountMemorySpaceProvider } from 'dsh-mnemon-source-memory-spaces/testing'
import module, { definition, descriptor, MnemonNativeProvider } from '../src/index.ts'
import { STORE_DUMP_MAX_OUTPUT_BYTES } from '../src/driver.ts'

describe('independent Native Provider', () => {
  it('mounts through the public module fixture and honors an aliased child identity', async () => {
    const mounted = await mountMemorySpaceProvider(module, { instanceId: 'local', config: undefined })
    const { authority } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused', instanceId: 'local' })
    try {
      expect(mounted.descriptor).toMatchObject({ id: 'local', typeId: 'mnemon-native' })
      expect(mounted.createAdapter({ memoryBodies: authority, config: { timeoutMs: 100 }, nativeRunner: {
        runJson: vi.fn(), runText: vi.fn(),
      } }).id).toBe('local')
    } finally { await mounted.dispose() }
    expect(mounted.registered).toBe(false)
  })

  it('requires a command capability and creates its own adapter', () => {
    const { authority } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused' })
    expect(() => definition.create({ memoryBodies: authority, config: { timeoutMs: 100 }, providerInstanceId: 'native', manifest: definition.manifest })).toThrow('nativeRunner')
  })

  it('decodes both CLI response generations and keeps every command scoped', async () => {
    const { body } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused', memoryBodyId: 'work' })
    const runJson = vi.fn<MemorySpaceNativeRunner['runJson']>()
      .mockResolvedValueOnce([{ insight: { id: 'old', content: 'Nested recall', entities: ['A'] }, score: 0.9 }])
      .mockResolvedValueOnce({ results: [{ id: 'new', content: 'Flat recall', score: 0.8 }] })
    const provider = new MnemonNativeProvider({ runJson, runText: vi.fn() })
    await expect(provider.search(body, { query: 'recall' })).resolves.toMatchObject({ results: [{ id: 'old', entities: ['A'], score: 0.9 }] })
    await expect(provider.metadataSample(body, 6)).resolves.toMatchObject([{ id: 'new', score: 0.8 }])
    expect(runJson.mock.calls.map(call => call[1]?.store)).toEqual(['work', 'work'])
    expect(runJson.mock.calls[1]?.[0]).toEqual(['--readonly', 'recall', '', '--basic', '--limit', '6'])
  })

  it('validates batch receipts and removes its private draft after failure', async () => {
    const { body } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused' })
    let draftPath = ''
    const runJson = vi.fn<MemorySpaceNativeRunner['runJson']>(async args => {
      if (args[0] === '--readonly') return []
      draftPath = args[1]!
      expect(args[2]).toBe('--no-diff')
      expect(JSON.parse(readFileSync(draftPath, 'utf8')).insights[0].content).toBe('Keep exact content')
      return { imported: 0, updated: 0, skipped: 0, errors: 1, results: [] }
    })
    const provider = new MnemonNativeProvider({ runJson, runText: vi.fn() })
    await expect(provider.rememberMany(body, [{ content: 'Keep exact content' }])).rejects.toThrow('invalid or partial result')
    expect(existsSync(draftPath)).toBe(false)
  })

  it('archives similar facts exactly while reusing identical persisted and in-batch content', async () => {
    const { body } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused', memoryBodyId: 'work' })
    const old = 'Backend order retry limit is 3; preserve this independent decision.'
    const next = 'Backend order retry limit is 4; preserve this independent decision.'
    const another = 'Frontend order retry limit is 3; preserve this independent decision.'
    let persisted = [{ id: 'old', content: old }]
    const runJson = vi.fn<MemorySpaceNativeRunner['runJson']>(async (args, options) => {
      expect(options?.store).toBe('work')
      if (args[0] === '--readonly') return persisted
      expect(args).toEqual(['import', expect.any(String), '--no-diff'])
      const draft = JSON.parse(readFileSync(args[1]!, 'utf8')) as { insights: Array<{ content: string }> }
      expect(draft.insights.map(entry => entry.content)).toEqual([next, another])
      const results = draft.insights.map((entry, index) => ({ index, id: 'new-' + index, action: 'added', content: entry.content }))
      persisted = [...persisted, ...results]
      return { imported: 2, updated: 0, skipped: 0, errors: 0, results: results.toReversed() }
    })
    const provider = new MnemonNativeProvider({ runJson, runText: vi.fn() })
    const requests = [old, next, another, next].map(content => ({ content }))
    await expect(provider.rememberMany(body, requests)).resolves.toMatchObject([
      { action: 'skipped', id: 'old', content: old }, { action: 'added', id: 'new-0', content: next },
      { action: 'added', id: 'new-1', content: another }, { action: 'skipped', id: 'new-0', content: next },
    ])
    await expect(provider.rememberMany(body, requests)).resolves.toMatchObject(requests.map(({ content }) => ({ action: 'skipped', content })))
    expect(runJson.mock.calls.filter(([args]) => args[0] === 'import')).toHaveLength(1)
    expect(persisted.find(entry => entry.id === 'old')?.content).toBe(old)
  })

  it('does not import when the readonly exact-content snapshot fails', async () => {
    const { body } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused', memoryBodyId: 'work' })
    const runJson = vi.fn<MemorySpaceNativeRunner['runJson']>().mockRejectedValue(new Error('snapshot unavailable'))
    const provider = new MnemonNativeProvider({ runJson, runText: vi.fn() })
    await expect(provider.rememberMany(body, [{ content: 'Preserve the existing store' }])).rejects.toThrow('snapshot unavailable')
    expect(runJson).toHaveBeenCalledOnce()
    expect(runJson.mock.calls[0]?.[0][0]).toBe('--readonly')
  })

  it.each(['updated', 'skipped'])('rejects a %s receipt for a forced exact import', async action => {
    const { body } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused' })
    const content = 'A distinct fact must be inserted exactly'
    const runJson = vi.fn<MemorySpaceNativeRunner['runJson']>()
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce({ imported: 0, updated: action === 'updated' ? 1 : 0, skipped: action === 'skipped' ? 1 : 0,
        errors: 0, results: [{ index: 0, id: 'other', action, content }] })
    const provider = new MnemonNativeProvider({ runJson, runText: vi.fn() })
    await expect(provider.rememberMany(body, [{ content }])).rejects.toThrow('invalid or partial result')
  })

  it('raises the output cap for whole-Store reads only', async () => {
    const { body } = createMemorySpaceProviderFixture(descriptor, {}, { dataDir: '/unused', memoryBodyId: 'work' })
    const html = 'var nodes = new vis.DataSet([{id:"a",label:"a: [fact] One",title:"One",color:"#3498db",font:{color:"white"}}]);\nvar edges = new vis.DataSet([]);'
    const runJson = vi.fn<MemorySpaceNativeRunner['runJson']>(async args => {
      if (args[0] === 'import') {
        const draft = JSON.parse(readFileSync(args[1]!, 'utf8')) as { insights: Array<{ content: string }> }
        return { imported: 1, updated: 0, skipped: 0, errors: 0, results: [{ index: 0, id: 'b', action: 'added', content: draft.insights[0]!.content }] }
      }
      if (args[0] === 'status') return { total_insights: 1 }
      return [{ id: 'a', content: 'One' }]
    })
    const runText = vi.fn<MemorySpaceNativeRunner['runText']>(async () => html)
    const provider = new MnemonNativeProvider({ runJson, runText })
    await provider.list(body, {})
    await provider.graph(body)
    await provider.rememberMany(body, [{ content: 'Two' }])
    await provider.search(body, { query: 'one' })
    await provider.status(body)
    const cap = (args: readonly string[], options?: { maxOutputBytes?: number }) => [args.find(arg => !arg.startsWith('-')), options?.maxOutputBytes]
    expect(runText.mock.calls.map(([args, options]) => cap(args, options))).toEqual([['viz', STORE_DUMP_MAX_OUTPUT_BYTES]])
    expect(runJson.mock.calls.map(([args, options]) => cap(args, options))).toEqual([
      ['recall', STORE_DUMP_MAX_OUTPUT_BYTES], ['recall', STORE_DUMP_MAX_OUTPUT_BYTES], ['recall', STORE_DUMP_MAX_OUTPUT_BYTES],
      ['import', undefined], ['recall', undefined], ['status', undefined],
    ])
    expect(STORE_DUMP_MAX_OUTPUT_BYTES).toBeGreaterThanOrEqual(64 * 1024 * 1024)
  })
})
