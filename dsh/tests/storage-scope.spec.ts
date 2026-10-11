import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { StorageScopeInspector } from "../src/host/storage-scope.ts"

const directories: string[] = []

function temporaryDirectory(): string {
  const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-storage-'))
  directories.push(directory)
  return directory
}

afterEach(() => {
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

describe('Mnemon storage-domain inventory', () => {
  it('inspects every persisted area under the configured active root without mutating it', () => {
    const root = temporaryDirectory()
    mkdirSync(join(root, 'runtime'), { recursive: true })
    writeFileSync(join(root, 'runtime', 'memories.json'), JSON.stringify({ version: 1, entries: [
      { content: 'Concise replies', target: 'user', importance: 'normal', created_at: 'now', updated_at: 'now' },
      { content: 'Use pnpm', target: 'memory', importance: 'critical', created_at: 'now', updated_at: 'now' },
    ] }))
    writeFileSync(join(root, 'runtime', 'USER.md'), 'Concise replies\n')
    writeFileSync(join(root, 'runtime', 'MEMORY.md'), 'Use pnpm\n')
    mkdirSync(join(root, 'data', 'space-a'), { recursive: true })
    writeFileSync(join(root, 'data', 'space-a', 'mnemon.db'), 'db')
    writeFileSync(join(root, 'data', '.dsh-memory-bodies.json'), JSON.stringify({ version: 1, bodies: [{ id: 'space-a', active: true }] }))
    mkdirSync(join(root, 'documents', 'active'), { recursive: true })
    mkdirSync(join(root, 'documents', 'archived'), { recursive: true })
    writeFileSync(join(root, 'documents', 'index.json'), JSON.stringify({ version: 1, documents: [{ status: 'active' }, { status: 'archived' }] }))

    const catalog = new StorageScopeInspector({ effectiveDataDir: () => root }, { storageScope: 'custom', dataDir: root }).catalog()

    expect(catalog).toMatchObject({ activeKind: 'custom', activeRoot: root })
    const active = catalog.scopes.find(scope => scope.active)!
    expect(active.kind).toBe('custom')
    expect(active.areas).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'runtime', status: 'ready', itemCount: 2, details: expect.objectContaining({ userEntries: 1, memoryEntries: 1 }) }),
      expect.objectContaining({ kind: 'memory-bodies', status: 'ready', itemCount: 1, details: expect.objectContaining({ activeBodies: 1, databases: 1 }) }),
      expect.objectContaining({ kind: 'documents', status: 'ready', itemCount: 2, details: expect.objectContaining({ activeDocuments: 1, archivedDocuments: 1 }) }),
      expect.objectContaining({ kind: 'state', status: 'missing' }),
    ]))
  })

  it('shows all four storage modes as distinct scopes while marking only the configured one active', () => {
    const workspace = temporaryDirectory()
    const root = temporaryDirectory()
    const catalog = new StorageScopeInspector({ effectiveDataDir: () => root }, { storageScope: 'custom', dataDir: root }).catalog(workspace)

    expect(catalog.scopes.map(scope => scope.kind)).toEqual(['global', 'workspace', 'custom', 'workspaces'])
    expect(catalog.scopes.filter(scope => scope.active)).toEqual([expect.objectContaining({ kind: 'custom', root })])
    expect(catalog.scopes.find(scope => scope.kind === 'workspace')?.root).toBe(join(workspace, '.mnemon'))
  })
})

describe('centralized storage roots', () => {
  it('uses the configured central root and inventories only the selected workspace subtree', async () => {
    const { createStorageRoot } = await import('../src/host/storage-root.ts')
    const root = temporaryDirectory(), one = temporaryDirectory(), two = temporaryDirectory()
    const config = { storageScope: 'workspaces' as const, dataDir: root }
    const first = createStorageRoot(config, one), second = createStorageRoot(config, two)
    expect(first.effectiveDataDir()).not.toBe(second.effectiveDataDir())
    const catalog = new StorageScopeInspector(first, config).catalog(one)
    expect(catalog.activeRoot).toBe(first.effectiveDataDir())
    expect(catalog.scopes.filter(scope => scope.active)).toEqual([expect.objectContaining({ kind: 'workspaces', root: first.effectiveDataDir() })])
    const active = catalog.scopes.find(scope => scope.active)!
    expect(active.areas.map(area => area.path)).toEqual(['runtime', 'data', 'documents', 'state'].map(area => join(first.effectiveDataDir(), area)))
    expect(active.areas.every(area => area.status === 'missing')).toBe(true)
  })
  it('honors dataDir, then MNEMON_DATA_DIR, then the default home root', async () => {
    const { createStorageRoot } = await import('../src/host/storage-root.ts')
    const { homedir } = await import('node:os')
    const { vi } = await import('vitest')
    const workspace = temporaryDirectory(), root = temporaryDirectory()
    try {
      vi.stubEnv('MNEMON_DATA_DIR', root)
      expect(createStorageRoot({ storageScope: 'workspaces' }, workspace).effectiveDataDir()).toMatch(join(root, 'workspaces'))
      expect(createStorageRoot({ storageScope: 'workspaces', dataDir: '~/central' }, workspace).effectiveDataDir()).toMatch(join(homedir(), 'central', 'workspaces'))
      vi.stubEnv('MNEMON_DATA_DIR', '')
      expect(createStorageRoot({ storageScope: 'workspaces' }, workspace).effectiveDataDir()).toMatch(join(homedir(), '.mnemon', 'workspaces'))
    } finally { vi.unstubAllEnvs() }
  })
})
