import { createHash } from 'node:crypto'
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, statSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import { DocumentController } from '../src/controller.ts'
import type { DocumentMutationResult, DocumentRecord, DocumentSnapshot } from '../src/contracts.ts'
import * as plugin from '../src/index.ts'
import { strategy } from './fixture.ts'

const CACHE_LIMIT = 2_048
const contentFor = (index: number) => `Original record ${String(index).padStart(4, '0')}.`

/** Seed valid real-disk records from one public write, avoiding a quadratic
 * fixture import. Reads, mutations and snapshots use actual Cordis management. */
async function fixture(size: number) {
  const root = mkdtempSync(join(tmpdir(), 'mnemon-excerpt-boundary-'))
  const workspace = join(root, 'workspace'), dataDir = join(root, 'data')
  const runner = new MemoryCompositionRunner()
  mkdirSync(workspace)
  try {
    await runner.mount(strategy, { instanceId: 'strategy' })
    await runner.mount(plugin, { instanceId: 'work', config: { dataDir } })
    const client = await runner.managementClient('source:work', { storage: 'custom', workspaceId: workspace })
    const seed = (await client.mutate('mutate', { action: 'create', title: 'Seed record', content: 'Seed content.' }, { confirmed: true })).value as unknown as DocumentMutationResult
    const { content: seedContent, ...template } = seed.document
    const stored = readFileSync(join(dataDir, template.relativePath), 'utf8')
    const records = Array.from({ length: size }, (_, index): DocumentRecord => {
      const content = contentFor(index), filename = `record-${index}.md`
      const record = { ...template, id: `cache-record-${index}`, title: `Record ${index}`, filename,
        relativePath: `documents/active/${filename}`, contentHash: createHash('sha256').update(content).digest('hex') }
      const markdown = stored.replaceAll(template.id, record.id).replaceAll(template.title, record.title)
        .replaceAll(seedContent, content).replaceAll(template.contentHash, record.contentHash)
      record.sizeBytes = Buffer.byteLength(markdown)
      writeFileSync(join(dataDir, record.relativePath), markdown, { mode: 0o600 })
      return record
    })
    const saveIndex = (documents: DocumentRecord[]) => writeFileSync(seed.snapshot.indexPath, JSON.stringify({ version: 1, documents }) + '\n')
    saveIndex(records)
    rmSync(join(dataDir, template.relativePath))
    const readBody = vi.spyOn(DocumentController.prototype as unknown as { readBody(record: DocumentRecord): string }, 'readBody')
    return { root, dataDir, client, records, readBody, saveIndex,
      indexRecords: () => (JSON.parse(readFileSync(seed.snapshot.indexPath, 'utf8')) as { documents: DocumentRecord[] }).documents,
      snapshot: async () => (await client.read('snapshot')).value as unknown as DocumentSnapshot,
      async dispose() { readBody.mockRestore(); await runner.dispose(); rmSync(root, { recursive: true, force: true }) },
    }
  } catch (error) { await runner.dispose(); rmSync(root, { recursive: true, force: true }); throw error }
}

describe('Bounded snapshot excerpt admission through public Cordis management', () => {
  it.each([2_048, 2_049, 4_096])('does not cycle the whole cache at %i documents', async size => {
    const f = await fixture(size)
    try {
      await f.snapshot()
      expect(f.readBody).toHaveBeenCalledTimes(size)
      const controller = f.readBody.mock.contexts[0] as unknown as { excerpts: Map<string, unknown> }
      for (let repeat = 0; repeat < 3; repeat++) {
        f.readBody.mockClear()
        const snapshot = await f.snapshot()
        expect(snapshot.total).toBe(size)
        expect(snapshot.activeCount).toBe(size)
        expect(snapshot.documents.map(record => record.excerpt)).toEqual(f.records.map((_, index) => contentFor(index)))
        expect(snapshot.documents.every(record => record.healthy)).toBe(true)
        expect(f.readBody).toHaveBeenCalledTimes(Math.max(0, size - CACHE_LIMIT))
        expect(f.readBody.mock.calls.map(([record]) => record.id)).toEqual(f.records.slice(CACHE_LIMIT).map(record => record.id))
        expect(controller.excerpts.size).toBe(Math.min(size, CACHE_LIMIT))
      }
    } finally { await f.dispose() }
  })

  it('changes admission on archive, renamed paths and removed index members without keeping orphan cache entries', async () => {
    const f = await fixture(CACHE_LIMIT + 1)
    try {
      await f.snapshot()
      const controller = f.readBody.mock.contexts[0] as unknown as { excerpts: Map<string, unknown> }
      const oldPath = join(f.dataDir, f.records[0]!.relativePath)
      const archived = (await f.client.mutate('archive', { id: f.records[0]!.id, documentRevision: 1, summary: 'Kept as a cold copy.' }, { confirmed: true })).value as unknown as DocumentMutationResult
      expect(archived.snapshot).toMatchObject({ total: CACHE_LIMIT + 1, activeCount: CACHE_LIMIT, archivedCount: 1 })
      expect(archived.snapshot.documents[0]).toMatchObject({ healthy: true, excerpt: contentFor(0), status: 'archived' })
      expect(controller.excerpts.has(oldPath)).toBe(false)
      expect(controller.excerpts.has(join(f.dataDir, archived.document.relativePath))).toBe(true)
      expect(controller.excerpts.size).toBe(CACHE_LIMIT)

      const records = f.indexRecords(), renamed = records[1]!, previous = join(f.dataDir, renamed.relativePath)
      renamed.filename = 'renamed-record.md'; renamed.relativePath = `documents/active/${renamed.filename}`
      renameSync(previous, join(f.dataDir, renamed.relativePath))
      f.saveIndex(records)
      f.readBody.mockClear()
      expect((await f.snapshot()).documents[1]).toMatchObject({ healthy: true, excerpt: contentFor(1), relativePath: renamed.relativePath })
      expect(f.readBody.mock.calls.map(([record]) => record.id)).toEqual([renamed.id, records[CACHE_LIMIT]!.id])
      expect(controller.excerpts.has(previous)).toBe(false)
      expect(controller.excerpts.size).toBe(CACHE_LIMIT)

      rmSync(join(f.dataDir, archived.document.relativePath))
      f.saveIndex(records.slice(1))
      f.readBody.mockClear()
      expect((await f.snapshot()).total).toBe(CACHE_LIMIT)
      expect(f.readBody.mock.calls.map(([record]) => record.id)).toEqual([records[CACHE_LIMIT]!.id])
      expect(controller.excerpts.has(join(f.dataDir, archived.document.relativePath))).toBe(false)
      expect(controller.excerpts.size).toBe(CACHE_LIMIT)
      f.readBody.mockClear()
      await f.snapshot()
      expect(f.readBody).not.toHaveBeenCalled()

      const added = (await f.client.mutate('mutate', { action: 'create', title: 'New overflow', content: 'Still in the full UI snapshot.' }, { confirmed: true })).value as unknown as DocumentMutationResult
      expect(added.snapshot.total).toBe(CACHE_LIMIT + 1)
      expect(added.snapshot.documents.at(-1)?.excerpt).toBe('Still in the full UI snapshot.')
      f.readBody.mockClear()
      await f.snapshot()
      expect(f.readBody.mock.calls.map(([record]) => record.id)).toEqual([added.document.id])
      expect(controller.excerpts.size).toBe(CACHE_LIMIT)
    } finally { await f.dispose() }
  })

  it.each([0, CACHE_LIMIT])('keeps external rewrite, replacement, deletion and permissions live for record %i', async index => {
    const f = await fixture(CACHE_LIMIT + 1)
    try {
      const target = f.records[index]!, path = join(f.dataDir, target.relativePath)
      const original = readFileSync(path, 'utf8'), fixed = new Date('2025-01-01T00:00:00.000Z')
      utimesSync(path, fixed, fixed)
      const initial = await f.snapshot()
      const select = (snapshot: DocumentSnapshot) => snapshot.documents.find(record => record.id === target.id)!
      const edited = original.replace('Original record', 'Modified record')
      expect(Buffer.byteLength(edited)).toBe(Buffer.byteLength(original))
      writeFileSync(path, edited); utimesSync(path, fixed, fixed)
      expect(statSync(path, { bigint: true }).mtimeNs).toBe(BigInt(fixed.getTime()) * 1_000_000n)
      f.readBody.mockClear()
      const changed = await f.snapshot()
      expect(changed.revision).toBe(initial.revision)
      expect(select(changed)).toMatchObject({ healthy: true, excerpt: contentFor(index).replace('Original', 'Modified') })
      expect(f.readBody).toHaveBeenCalledTimes(index < CACHE_LIMIT ? 2 : 1)

      const replacement = join(f.root, 'replacement.md')
      writeFileSync(replacement, original.replace('Original record', 'Replaced record'))
      utimesSync(replacement, fixed, fixed); renameSync(replacement, path)
      expect(select(await f.snapshot()).excerpt).toBe(contentFor(index).replace('Original', 'Replaced'))
      rmSync(path)
      const missing = await f.snapshot()
      expect(missing.total).toBe(CACHE_LIMIT + 1)
      expect(select(missing)).toMatchObject({ healthy: false, excerpt: '' })
      writeFileSync(path, original, { mode: 0o600 })
      expect(select(await f.snapshot())).toMatchObject({ healthy: true, excerpt: contentFor(index) })
      chmodSync(path, 0)
      try {
        if (process.getuid?.() !== 0 && process.platform !== 'win32') await expect(f.snapshot()).rejects.toThrow()
      } finally { chmodSync(path, 0o600) }
      expect(select(await f.snapshot())).toMatchObject({ healthy: true, excerpt: contentFor(index) })
      expect((await f.client.read('document', { id: target.id })).value).toMatchObject({ content: contentFor(index) })
    } finally { await f.dispose() }
  })
})
