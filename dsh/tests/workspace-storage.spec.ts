import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, realpathSync, renameSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createStorageRoot } from '../src/host/storage-root.ts'
import { canonicalWorkspacePath, workspaceStorageId } from '../src/host/workspace-storage.ts'

const roots: string[] = []
function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'mnemon-workspace-storage-'))
  roots.push(root)
  const workspace = join(root, 'workspace')
  mkdirSync(workspace)
  return { root, workspace }
}
afterEach(() => {
  vi.restoreAllMocks()
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true })
})

describe('built-in centralized workspace layout', () => {
  it('resolves a bounded stable directory without writing to either workspace or central root', () => {
    const { root, workspace } = fixture()
    const dataDir = join(root, 'central')
    const id = workspaceStorageId(workspace)
    expect(id).toMatch(/^[0-9a-f]{64}$/u)
    expect(createStorageRoot({ storageScope: 'workspaces', dataDir }, workspace).effectiveDataDir()).toBe(join(dataDir, 'workspaces', id))
    expect(workspaceStorageId(join(workspace, 'child', '..'))).toBe(id)
    expect(readdirSync(workspace)).toEqual([])
    expect(existsSync(dataDir)).toBe(false)
  })

  it('canonicalizes symlink aliases and missing descendants to the same identity', () => {
    const { root, workspace } = fixture()
    const alias = join(root, 'alias')
    symlinkSync(workspace, alias, process.platform === 'win32' ? 'junction' : 'dir')
    expect(workspaceStorageId(alias)).toBe(workspaceStorageId(workspace))
    expect(workspaceStorageId(join(alias, 'missing', 'project'))).toBe(workspaceStorageId(join(workspace, 'missing', 'project')))
    expect(canonicalWorkspacePath(join(alias, 'missing'))).toBe(join(canonicalWorkspacePath(workspace), 'missing'))
  })

  it('isolates same-named workspaces and preserves old storage when a workspace moves', () => {
    const { root, workspace } = fixture()
    const other = join(root, 'other', 'workspace')
    mkdirSync(other, { recursive: true })
    expect(workspaceStorageId(other)).not.toBe(workspaceStorageId(workspace))
    const config = { storageScope: 'workspaces' as const, dataDir: join(root, 'central') }
    const previous = createStorageRoot(config, workspace).effectiveDataDir()
    mkdirSync(previous, { recursive: true })
    writeFileSync(join(previous, 'sentinel'), 'retained')
    const moved = join(root, 'renamed')
    renameSync(workspace, moved)
    expect(createStorageRoot(config, moved).effectiveDataDir()).not.toBe(previous)
    expect(readFileSync(join(previous, 'sentinel'), 'utf8')).toBe('retained')
  })

  it('rejects malformed identities and non-directory ancestors while allowing Unicode names', () => {
    const { root } = fixture()
    for (const workspacePath of ['', '../escape', '/workspace\0']) expect(() => workspaceStorageId(workspacePath)).toThrow()
    writeFileSync(join(root, 'file'), 'file')
    expect(() => workspaceStorageId(join(root, 'file', 'child'))).toThrow()
    expect(workspaceStorageId(resolve(root, '项目 with spaces'))).toMatch(/^[0-9a-f]{64}$/u)
  })

  it('rejects a file ancestor when native realpath reports ENOENT for its child', () => {
    const { root } = fixture()
    const file = join(root, 'file')
    writeFileSync(file, 'retained')
    // Windows reports ENOENT for this child, then successfully resolves the file.
    // Keep that second call and the ancestor's type check on the real filesystem.
    const native = vi.spyOn(realpathSync, 'native').mockImplementationOnce(() => {
      throw Object.assign(new Error('Missing child'), { code: 'ENOENT' })
    })
    expect(() => workspaceStorageId(join(file, 'child'))).toThrow(expect.objectContaining({ code: 'ENOTDIR' }))
    expect(native.mock.calls.map(([path]) => path)).toEqual([join(file, 'child'), file])
    expect(readFileSync(file, 'utf8')).toBe('retained')
  })

  it('retains existing file identities and does not create missing directory descendants', () => {
    const { root } = fixture()
    const file = join(root, 'file')
    writeFileSync(file, 'file')
    expect(canonicalWorkspacePath(file)).toBe(realpathSync.native(file))
    expect(workspaceStorageId(file)).toMatch(/^[0-9a-f]{64}$/u)
    const missing = join(root, 'missing', '项目 with spaces')
    expect(canonicalWorkspacePath(missing)).toBe(join(realpathSync.native(root), 'missing', '项目 with spaces'))
    expect(workspaceStorageId(missing)).toMatch(/^[0-9a-f]{64}$/u)
    expect(existsSync(join(root, 'missing'))).toBe(false)
  })
})
