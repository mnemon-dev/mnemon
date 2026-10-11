import { chmodSync, mkdtempSync, mkdirSync, readFileSync, renameSync, rmSync, statSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as plugin from '../src/index.ts'
import type { DocumentMutationResult, DocumentSnapshot } from '../src/contracts.ts'
import { strategy } from './fixture.ts'

async function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'mnemon-documents-io-'))
  const workspace = join(root, 'workspace'), dataDir = join(root, 'data')
  mkdirSync(workspace)
  const scope = { storage: 'custom' as const, workspaceId: workspace }
  const runners: MemoryCompositionRunner[] = []
  const mount = async () => {
    const runner = new MemoryCompositionRunner()
    runners.push(runner)
    await runner.mount(strategy, { instanceId: 'strategy' })
    await runner.mount(plugin, { instanceId: 'work', config: { dataDir } })
    const client = await runner.managementClient('source:work', scope)
    return { runner, client }
  }
  return { root, workspace, dataDir, scope, mount,
    async dispose() {
      for (const runner of runners.reverse()) await runner.dispose()
      rmSync(root, { recursive: true, force: true })
    },
  }
}

describe('Documents disk-backed snapshot freshness through the public SDK', () => {
  it('observes same-size external edits with restored mtime, replacements, deletion and restoration', async () => {
    const f = await fixture()
    try {
      const { runner, client } = await f.mount()
      const created = (await client.mutate('mutate', { action: 'create', title: 'External changes', content: 'Original story one.' }, { confirmed: true })).value as unknown as DocumentMutationResult
      await client.mutate('mutate', { action: 'create', title: 'Other story', content: 'Unchanged second story.' }, { confirmed: true })
      const path = join(f.dataDir, created.document.relativePath)
      const fixedTime = new Date('2025-01-01T00:00:00.000Z')
      utimesSync(path, fixedTime, fixedTime)
      const original = readFileSync(path, 'utf8'), before = statSync(path)
      const snapshot = async () => (await client.read('snapshot')).value as unknown as DocumentSnapshot
      const selected = (value: DocumentSnapshot) => value.documents.find(document => document.id === created.document.id)!
      const initial = await snapshot()
      expect(initial.total).toBe(2)
      expect(selected(initial).excerpt).toBe('Original story one.')

      const edited = original.replace('Original story one.', 'Modified story one.')
      expect(Buffer.byteLength(edited)).toBe(Buffer.byteLength(original))
      writeFileSync(path, edited)
      utimesSync(path, before.atime, before.mtime)
      expect(statSync(path, { bigint: true }).mtimeNs).toBe(BigInt(fixedTime.getTime()) * 1_000_000n)
      const changed = await snapshot()
      expect(selected(changed)).toMatchObject({ healthy: true, excerpt: 'Modified story one.' })
      expect(changed.revision).toBe(initial.revision) // Body edits do not invent an index revision.
      expect((await client.read('document', { id: created.document.id })).value).toMatchObject({ content: 'Modified story one.' })

      const replacement = join(f.root, 'replacement.md')
      writeFileSync(replacement, original.replace('Original story one.', 'Replaced story one.'))
      utimesSync(replacement, before.atime, before.mtime)
      renameSync(replacement, path)
      expect(selected(await snapshot()).excerpt).toBe('Replaced story one.')

      rmSync(path)
      expect(selected(await snapshot())).toMatchObject({ healthy: false, excerpt: '' })
      const missing = await runner.beginTurn({ scope: f.scope })
      expect(missing.view.readGrants[0]!.value).toMatchObject({ documentIds: expect.not.arrayContaining([created.document.id]) })
      missing.release()
      await expect(client.read('document', { id: created.document.id })).rejects.toThrow()

      writeFileSync(path, original, { mode: 0o600 })
      expect(selected(await snapshot())).toMatchObject({ healthy: true, excerpt: 'Original story one.' })
      const restored = await runner.beginTurn({ scope: f.scope })
      expect(restored.view.readGrants[0]!.value).toMatchObject({ documentIds: expect.arrayContaining([created.document.id]) })
      restored.release()
    } finally { await f.dispose() }
  })

  it('does not let a warm excerpt bypass changed file permissions', async () => {
    const f = await fixture()
    try {
      const { client } = await f.mount()
      const added = (await client.mutate('mutate', { action: 'create', title: 'Private', content: 'Private body.' }, { confirmed: true })).value as unknown as DocumentMutationResult
      const path = join(f.dataDir, added.document.relativePath)
      expect((await client.read('snapshot')).value).toMatchObject({ documents: [{ excerpt: 'Private body.' }] })
      chmodSync(path, 0)
      try {
        if (process.getuid?.() !== 0 && process.platform !== 'win32') await expect(client.read('snapshot')).rejects.toThrow()
      } finally { chmodSync(path, 0o600) }
      expect((await client.read('document', { id: added.document.id })).value).toMatchObject({ content: 'Private body.' })
      expect((await client.read('snapshot')).value).toMatchObject({ documents: [{ excerpt: 'Private body.' }] })
    } finally { await f.dispose() }
  })

  it('retains cross-generation index freshness and fences stale mutations without losing full receipts', async () => {
    const f = await fixture()
    try {
      const { runner, client } = await f.mount()
      const first = await runner.beginTurn({ scope: f.scope })
      const offer = first.view.actionOffers[0]!
      const receipt = await first.executeAction(offer.id, { action: 'create', title: 'Durable', content: 'Original authority.' }, () => true)
      expect(receipt).toMatchObject({ status: 'succeeded', completion: 'committed', committedAt: expect.any(String) })
      const details = receipt.details as unknown as DocumentMutationResult
      expect(details.snapshot).toMatchObject({ total: 1, documents: [{ healthy: true, excerpt: 'Original authority.' }] })
      const indexPath = join(f.dataDir, 'documents', 'index.json')
      expect(JSON.parse(readFileSync(indexPath, 'utf8')).documents[0].id).toBe(details.document.id)
      expect(statSync(indexPath).mode & 0o777).toBe(0o600)
      expect(statSync(join(f.dataDir, details.document.relativePath)).mode & 0o777).toBe(0o600)
      first.release()
      const before = await client.read('snapshot')
      const second = await f.mount()
      const updated = await second.client.mutate('mutate', { action: 'update', id: details.document.id, content: 'Updated authority.' }, { confirmed: true })
      expect(updated.revision).not.toBe(before.revision)
      await expect(client.mutate('mutate', { action: 'update', id: details.document.id, content: 'Stale overwrite.' }, { confirmed: true, expectedRevision: before.revision })).rejects.toThrow('revision conflict')
      const current = await client.read('snapshot')
      expect(current.revision).toBe(updated.revision)
      expect(current.value).toMatchObject({ total: 1, documents: [{ revision: 2, excerpt: 'Updated authority.' }] })
      expect((await client.read('document', { id: details.document.id })).value).toMatchObject({ content: 'Updated authority.' })
    } finally { await f.dispose() }
  })

  it('observes external index edits/replacements and does not hide corruption or removal behind a cached revision', async () => {
    const f = await fixture()
    try {
      const { client } = await f.mount()
      await client.mutate('mutate', { action: 'create', title: 'Initial', content: 'Stored body.' }, { confirmed: true })
      const path = join(f.dataDir, 'documents', 'index.json')
      const fixedTime = new Date('2025-01-01T00:00:00.000Z')
      utimesSync(path, fixedTime, fixedTime)
      const before = await client.read('snapshot')
      const original = readFileSync(path, 'utf8'), stat = statSync(path)
      const edited = original.replace('Initial', 'Changed')
      expect(edited.length).toBe(original.length)
      writeFileSync(path, edited)
      utimesSync(path, stat.atime, stat.mtime)
      expect(statSync(path, { bigint: true }).mtimeNs).toBe(BigInt(fixedTime.getTime()) * 1_000_000n)
      const changed = await client.read('snapshot')
      expect(changed.revision).not.toBe(before.revision)
      expect(changed.value).toMatchObject({ documents: [{ title: 'Changed', excerpt: 'Stored body.' }] })

      const replacement = join(f.root, 'replacement-index.json')
      writeFileSync(replacement, original.replace('Initial', 'Another'))
      utimesSync(replacement, stat.atime, stat.mtime)
      renameSync(replacement, path)
      expect((await client.read('snapshot')).value).toMatchObject({ documents: [{ title: 'Another' }] })

      writeFileSync(path, original.replace('"version": 1', '"version": 9'))
      utimesSync(path, stat.atime, stat.mtime)
      await expect(client.read('snapshot')).rejects.toThrow()
      writeFileSync(path, original)
      expect((await client.read('snapshot')).revision).toBe(before.revision)
      rmSync(path)
      await expect(client.read('snapshot')).rejects.toThrow()
      writeFileSync(path, original, { mode: 0o600 })
      expect((await client.read('snapshot')).revision).toBe(before.revision)
      chmodSync(path, 0)
      try {
        if (process.getuid?.() !== 0 && process.platform !== 'win32') await expect(client.read('snapshot')).rejects.toThrow()
      } finally { chmodSync(path, 0o600) }
      expect((await client.read('snapshot')).revision).toBe(before.revision)
    } finally { await f.dispose() }
  })

  it('checks an unchanged source revision again inside the real mutation queue', async () => {
    const f = await fixture()
    try {
      const { runner, client } = await f.mount()
      const base = { scope: f.scope, sourceInstanceKey: 'source:work', mode: 'mutate' as const, operation: 'mutate', confirmed: true, expectedRevision: client.revision }
      const writes = await Promise.allSettled([
        runner.executeManagement({ ...base, input: { action: 'create', title: 'Winner', content: 'Committed once.' } }),
        runner.executeManagement({ ...base, input: { action: 'create', title: 'Stale', content: 'Must not commit.' } }),
      ])
      expect(writes[0]!.status).toBe('fulfilled')
      expect(writes[1]).toMatchObject({ status: 'rejected', reason: { code: 'revision-conflict' } })
      expect((await client.read('snapshot')).value).toMatchObject({ total: 1, documents: [{ title: 'Winner', excerpt: 'Committed once.' }] })
    } finally { await f.dispose() }
  })
})
