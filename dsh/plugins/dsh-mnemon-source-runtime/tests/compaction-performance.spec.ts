import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RuntimeMemoryController } from '../src/controller.ts'
import { RUNTIME_ENTRY_DELIMITER, type RuntimeMemoryEntry, type RuntimeMemoryTarget } from '../src/contracts.ts'

const directories: string[] = []
afterEach(() => {
  vi.restoreAllMocks()
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})
const timestamp = '2026-10-02T00:00:00.000Z'
function fixture(entries: RuntimeMemoryEntry[], target: RuntimeMemoryTarget = 'memory') {
  const directory = mkdtempSync(join(tmpdir(), 'mnemon-compaction-algorithm-'))
  directories.push(directory)
  const controller = new RuntimeMemoryController({ effectiveDataDir: () => directory }, () => new Date('2026-10-03T00:00:00.000Z'), { memory: 1_048_576, user: 1_048_576 })
  writeFileSync(controller.sourcePath, JSON.stringify({ version: 1, entries: [...entries, { content: 'remove sentinel', target, importance: 'normal', created_at: timestamp, updated_at: timestamp }] }))
  return controller
}
function entry(content: string, i: number, target: RuntimeMemoryTarget = 'memory'): RuntimeMemoryEntry {
  return { content, target, importance: (['low', 'critical', 'normal'] as const)[i % 3]!, created_at: timestamp, updated_at: timestamp, ...(target === 'memory' ? { branches: ['main'] } : {}) }
}

describe('compaction algorithm equivalence', () => {
  it.each(['memory', 'user'] as const)('preserves greedy packing, UTF-8 limits, metadata and order for %s', async target => {
    const entries = Array.from({ length: 40 }, (_, i) => entry(`${i}:${['a', '中文', '𠀀', '\ud800'][i % 4]!.repeat(i % 7 + 1)}`, i, target))
    const priority = { critical: 0, normal: 1, low: 2 }
    for (const budget of [0, 1, 4, 10, 23, 60, 137, 512, 5000]) {
      // Frozen full-rescan reference: preserves the original priority/packing contract.
      const selected = new Set<RuntimeMemoryEntry>()
      const packed: RuntimeMemoryEntry[] = []
      for (const candidate of [...entries].sort((a, b) => priority[a.importance] - priority[b.importance])) {
        if (Buffer.byteLength([...packed, candidate].map(e => e.content).join(RUNTIME_ENTRY_DELIMITER)) > budget) continue
        packed.push(candidate)
        selected.add(candidate)
      }
      const controller = fixture(entries, target)
      await controller.compactAndMutate(controller.snapshot().revision, { action: 'remove', target, oldText: 'remove sentinel' }, entries.map(({ content, importance }) => ({ content, importance })), budget)
      const expected = entries.filter(e => selected.has(e))
      expect(controller.snapshot().entries).toEqual(expected)
      const content = expected.map(e => e.content).join(RUNTIME_ENTRY_DELIMITER)
      // Disk encoding replaces an unpaired surrogate, as before.
      expect(readFileSync(target === 'memory' ? controller.memoryPath : controller.userPath, 'utf8')).toBe(Buffer.from(content === '' ? '' : `${content}\n`).toString())
    }
  })

  it('inherits the first identical committed entry when legacy duplicates have different branches', async () => {
    const first = entry('same original content', 1)
    const controller = fixture([first, { ...first, branches: ['feature'], created_at: '2026-10-01T00:00:00.000Z' }])
    await controller.compactAndMutate(controller.snapshot().revision, { action: 'remove', target: 'memory', oldText: 'remove sentinel' }, [{ content: first.content, importance: first.importance }])
    expect(controller.snapshot().entries).toEqual([first])
  })

  it.each([1, 10, 100])('counts UTF-8 characters only linearly at N=%i', async n => {
    const entries = Array.from({ length: n }, (_, i) => entry(`Synthetic 中文 content ${i}`, i))
    const controller = fixture(entries)
    const revision = controller.snapshot().revision
    const byteLength = Buffer.byteLength
    let characters = 0
    const counter = vi.spyOn(Buffer, 'byteLength').mockImplementation((value, encoding) => {
      if (typeof value === 'string') characters += value.length
      return byteLength(value, encoding)
    })
    await controller.compactAndMutate(revision, { action: 'remove', target: 'memory', oldText: 'remove sentinel' }, entries)
    counter.mockRestore()
    expect(controller.snapshot().entries).toEqual(entries)
    expect(characters).toBeLessThan(12 * entries.map(e => e.content).join(RUNTIME_ENTRY_DELIMITER).length + 100)
  })
})
