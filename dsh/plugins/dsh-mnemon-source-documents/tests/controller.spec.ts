import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DocumentCapacityError, DocumentConflictError, DocumentController, DocumentManager } from "../src/controller.ts"

const directories: string[] = []

function workspace(): string {
  const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-documents-'))
  directories.push(directory)
  return directory
}

afterEach(() => {
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

describe('Mnemon Documents control plane', () => {
  it('preserves tied search order, excerpts, live reads and selected-only access updates', async () => {
    let now = new Date('2026-10-01T00:00:00.000Z')
    const controller = new DocumentController(workspace(), 1024 * 1024, () => now)
    for (let i = 0; i < 100; i++) await controller.mutate({ action: 'create', title: `storage ${i}`, description: 'schema', content: `# storage **schema** ${i}` })
    const original = controller.catalog().documents
    now = new Date('2026-10-03T00:00:00.000Z')
    const result = await controller.search('storage schema', { limit: 10 })
    expect(result.results.map(row => [row.id, row.score, row.excerpt])).toEqual(original.slice(0, 10).map((row, i) => [row.id, 6, `storage schema ${i}`]))
    expect(controller.catalog().documents.map(row => row.lastAccessedAt)).toEqual(original.map((row, i) => i < 10 ? now.toISOString() : row.lastAccessedAt))
    const last = original[99]!
    writeFileSync(join(controller.workspaceRoot, last.relativePath), 'External changed content live-sentinel')
    expect((await controller.search('live-sentinel', { allowedIds: [last.id] })).results[0]?.content).toBe('External changed content live-sentinel')
    expect((await controller.search('storage schema', { allowedIds: [] })).results).toEqual([])
    rmSync(join(controller.workspaceRoot, last.relativePath))
    // A broken eligible file must still fail even if it would rank below the limit.
    await expect(controller.search('storage schema', { limit: 1 })).rejects.toThrow()
    expect((await controller.search('storage schema', { allowedIds: [original[0]!.id] })).results[0]?.id).toBe(original[0]!.id)
  })

  it('reads no bodies for revision/catalog, one for a single read, and reuses verified UI excerpts', async () => {
    const controller = new DocumentController(workspace())
    for (let index = 0; index < 24; index++) await controller.mutate({ action: 'create', title: `Record ${index}`, content: `Original ${index}` })
    const readBody = vi.spyOn(controller as unknown as { readBody(record: unknown): string }, 'readBody')
    const catalog = controller.catalog()
    expect(controller.revision()).toBe(catalog.revision)
    expect(catalog.documents).toHaveLength(24)
    expect(catalog.documents.every(record => record.healthy && record.excerpt === '')).toBe(true)
    expect(readBody).not.toHaveBeenCalled()
    controller.get(catalog.documents[0]!.id)
    expect(readBody).toHaveBeenCalledOnce()
    readBody.mockClear()
    expect(controller.snapshot().documents.every(record => record.excerpt.startsWith('Original'))).toBe(true)
    expect(readBody).not.toHaveBeenCalled()
  })

  it('reads each newly imported body once for full snapshots instead of rereading the collection', async () => {
    const controller = new DocumentController(workspace())
    const readBody = vi.spyOn(controller as unknown as { readBody(record: unknown): string }, 'readBody')
    for (let index = 0; index < 32; index++) {
      const result = await controller.mutate({ action: 'create', title: `Record ${index}`, content: `Original ${index}` })
      expect(result.snapshot.total).toBe(index + 1)
      expect(result.snapshot.documents.every(record => record.healthy && record.excerpt.startsWith('Original'))).toBe(true)
    }
    expect(readBody).toHaveBeenCalledTimes(32)
  })

  it('rechecks a management revision inside the mutation queue', async () => {
    const controller = new DocumentController(workspace())
    const revision = controller.revision()
    const first = controller.mutate({ action: 'create', title: 'First', content: 'Kept.' }, revision)
    const stale = controller.mutate({ action: 'create', title: 'Stale', content: 'Must not commit.' }, revision)
    await first
    await expect(stale).rejects.toBeInstanceOf(DocumentConflictError)
    expect(controller.snapshot().documents.map(document => document.title)).toEqual(['First'])
  })

  it('keeps cached index records isolated from mutable controller results', async () => {
    const controller = new DocumentController(workspace())
    const added = await controller.mutate({ action: 'create', title: 'Metadata', content: 'Preserved.', sourcePaths: ['notes.md'], sessionIds: ['original'] })
    const first = controller.snapshot(), revision = first.revision
    first.documents[0]!.title = 'Untrusted caller change'
    first.documents[0]!.sourcePaths.push('injected.md')
    first.documents[0]!.sessionIds.length = 0
    controller.get(added.document.id).memoryBodyIds.push('injected-space')
    const next = controller.snapshot()
    expect(next.revision).toBe(revision)
    expect(next.documents[0]).toMatchObject({ title: 'Metadata', sourcePaths: ['notes.md'], sessionIds: ['original'], memoryBodyIds: [] })
  })

  it('does not cache an index mutation when its atomic write fails', async () => {
    const controller = new DocumentController(workspace())
    const before = await controller.mutate({ action: 'create', title: 'Durable metadata', content: 'Stored.' })
    const revision = controller.revision()
    const internal = controller as unknown as { atomicWrite(path: string, content: string): void }
    const atomicWrite = internal.atomicWrite.bind(controller)
    const failure = vi.spyOn(internal, 'atomicWrite').mockImplementation((path, content) => {
      if (path === controller.indexPath) throw new Error('injected index write failure')
      atomicWrite(path, content)
    })
    await expect(controller.mutate({ action: 'update', id: before.document.id, title: 'Uncommitted metadata' }, revision)).rejects.toThrow('index write failure')
    failure.mockRestore()
    expect(controller.revision()).toBe(revision)
    expect(controller.snapshot().documents[0]).toMatchObject({ title: 'Durable metadata', revision: 1 })
  })

  it('creates an isolated active/archive directory and managed Markdown projection', async () => {
    const root = workspace()
    writeFileSync(join(root, 'ADR.md'), '# Original\nDo not mutate me.\n')
    const controller = new DocumentController(root)
    const result = await controller.mutate({
      action: 'create',
      title: 'SQLite deployment decision',
      description: 'Why this project uses an embedded database.',
      content: '# Decision\n\nUse SQLite so deployment remains single-file.',
      sourcePaths: ['ADR.md'],
      sessionIds: ['session-1'],
    })

    expect(result.document.id).toMatch(/^[0-9a-f-]{36}$/)
    expect(result.document.relativePath).toMatch(/^\.mnemon\/documents\/active\//)
    expect(result.document.sourcePaths).toEqual(['ADR.md'])
    expect(readFileSync(join(root, 'ADR.md'), 'utf8')).toContain('Do not mutate me.')
    const projection = readFileSync(join(root, result.document.relativePath), 'utf8')
    expect(projection).toContain(`id: "${result.document.id}"`)
    expect(projection).toContain('source_paths:\n  - "ADR.md"')
    expect(projection).toContain('# Decision')
    expect(controller.snapshot()).toMatchObject({ activeCount: 1, archivedCount: 0, total: 1 })
  })

  it('updates through the index authority and searches active documents deterministically', async () => {
    const controller = new DocumentController(workspace())
    const first = await controller.mutate({ action: 'create', title: 'Release checklist', content: 'Run typecheck and WebUI E2E before push.' })
    await controller.mutate({ action: 'create', title: 'Database notes', content: 'SQLite keeps deployment local.' })
    const updated = await controller.mutate({ action: 'update', id: first.document.id, content: 'Run typecheck, unit tests, and real WebUI E2E before push.' })

    expect(updated.document.revision).toBe(2)
    expect(updated.document.contentHash).not.toBe(first.document.contentHash)
    const result = await controller.search('WebUI E2E')
    expect(result.results.map(document => document.id)).toEqual([first.document.id])
    expect(result.results[0]).toMatchObject({ status: 'active', score: expect.any(Number) })
  })

  it('matches focused Chinese queries by bounded bigrams instead of one full sentence token', async () => {
    const controller = new DocumentController(workspace())
    const release = await controller.mutate({
      action: 'create',
      title: 'Project Lantern 发布运行手册',
      content: '数据库 migration 完成后，依次提升到 5%、12%、35%、65%、100%。自动回滚按错误率和延迟阈值执行。',
    })
    await controller.mutate({ action: 'create', title: '事故记录', content: '数据库故障复盘与发布历史。' })

    const result = await controller.search('数据库迁移后灰度阶段顺序 自动回滚阈值 发布手册')
    expect(result.results.map(document => document.id)).toEqual([release.document.id])
  })

  it('uses actual active bytes, excludes cold archives, and proposes LRU eviction', async () => {
    const root = workspace()
    let tick = 0
    const controller = new DocumentController(root, 1_300, () => new Date(Date.UTC(2026, 7, 13, 0, 0, tick++)))
    const oldest = await controller.mutate({ action: 'create', title: 'Old topic', content: 'a'.repeat(180) })
    const recent = await controller.mutate({ action: 'create', title: 'Recent topic', content: 'b'.repeat(180) })

    let capacity: DocumentCapacityError | undefined
    try {
      await controller.mutate({ action: 'create', title: 'Overflow topic', content: 'c'.repeat(320) })
    } catch (error) {
      if (error instanceof DocumentCapacityError) capacity = error
      else throw error
    }
    expect(capacity?.candidates.map(record => record.id)).toEqual([oldest.document.id, recent.document.id])

    const archived = await controller.archive(oldest.document.id, oldest.document.revision, { summary: 'Archived old topic with original path.', memoryBodyIds: ['space-1'] })
    expect(archived.document.relativePath).toMatch(/^\.mnemon\/documents\/archived\//)
    expect(existsSync(join(root, oldest.document.relativePath))).toBe(false)
    expect(existsSync(join(root, archived.document.relativePath))).toBe(true)
    const snapshot = controller.snapshot()
    expect(snapshot).toMatchObject({ activeCount: 1, archivedCount: 1 })
    expect(snapshot.activeBytes).toBe(recent.document.sizeBytes)
    await expect(controller.search('Old')).resolves.toMatchObject({ total: 0 })
    await expect(controller.search('Old', { includeArchived: true })).resolves.toMatchObject({ total: 1 })
  })

  it('preserves the active copy when an archive races a document revision', async () => {
    const controller = new DocumentController(workspace())
    const created = await controller.mutate({ action: 'create', title: 'Concurrency', content: 'Revision one.' })
    await controller.mutate({ action: 'update', id: created.document.id, content: 'Revision two.' })
    await expect(controller.archive(created.document.id, created.document.revision, { summary: 'stale', memoryBodyIds: [] })).rejects.toBeInstanceOf(DocumentConflictError)
    expect(controller.get(created.document.id)).toMatchObject({ status: 'active', content: 'Revision two.' })
  })

  it('rejects source traversal and resolves one controller per workspace', async () => {
    const root = workspace()
    const manager = new DocumentManager()
    expect(manager.forWorkspace(root)).toBe(manager.forWorkspace(root))
    await expect(manager.forWorkspace(root).mutate({ action: 'create', title: 'Unsafe', content: 'x', sourcePaths: ['../outside.md'] })).rejects.toThrow('inside the workspace')
  })

  it('places managed Documents under the same configured storage root as runtime memory and Memory Spaces', async () => {
    const root = workspace()
    const storageRoot = workspace()
    const manager = new DocumentManager(undefined, undefined, () => storageRoot)

    const result = await manager.forWorkspace(root).mutate({ action: 'create', title: 'Unified storage', content: 'All managed memory belongs below one selected root.' })

    expect(result.document.relativePath).toMatch(/^documents\/active\//)
    expect(result.snapshot.directory).toBe(join(storageRoot, 'documents'))
    expect(existsSync(join(storageRoot, result.document.relativePath))).toBe(true)
    expect(existsSync(join(root, '.mnemon', 'documents'))).toBe(false)
  })
})
