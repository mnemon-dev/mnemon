import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { RUNTIME_ENTRY_DELIMITER } from '../src/contracts.ts'
import {
  RuntimeMemoryCapacityError,
  RuntimeMemoryController,
} from "../src/controller.ts"

const directories: string[] = []

afterEach(() => {
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

function fixture(now = new Date('2026-08-13T08:00:00.000Z')): { directory: string; controller: RuntimeMemoryController } {
  const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runtime-'))
  directories.push(directory)
  return {
    directory,
    controller: new RuntimeMemoryController({ effectiveDataDir: () => directory }, () => now),
  }
}

describe('RuntimeMemoryController', () => {
  it('creates the JSON source of truth and deterministic Markdown projections', async () => {
    const { directory, controller } = fixture()
    await controller.mutate({ action: 'add', target: 'user', content: '用户偏好简洁回答', importance: 'critical' })
    await controller.mutate({ action: 'add', target: 'memory', content: '项目使用 TypeScript', importance: 'normal' })

    const root = join(directory, 'runtime')
    const source = JSON.parse(readFileSync(join(root, 'memories.json'), 'utf8')) as { version: number; entries: unknown[] }
    expect(source).toEqual({
      version: 1,
      entries: [
        {
          content: '用户偏好简洁回答',
          created_at: '2026-08-13T08:00:00.000Z',
          updated_at: '2026-08-13T08:00:00.000Z',
          target: 'user',
          importance: 'critical',
        },
        {
          content: '项目使用 TypeScript',
          created_at: '2026-08-13T08:00:00.000Z',
          updated_at: '2026-08-13T08:00:00.000Z',
          target: 'memory',
          importance: 'normal',
        },
      ],
    })
    expect(readFileSync(join(root, 'USER.md'), 'utf8')).toBe('用户偏好简洁回答\n')
    expect(readFileSync(join(root, 'MEMORY.md'), 'utf8')).toBe('项目使用 TypeScript\n')
    expect(existsSync(join(root, '.memories.lock'))).toBe(false)
  })

  it('projects one-line entries separated by a standalone section sign', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: '第一条\n  测试' })
    await controller.mutate({ action: 'add', target: 'memory', content: '第二条\t测试' })

    expect(controller.snapshot().entries.map(entry => entry.content)).toEqual(['第一条 测试', '第二条 测试'])
    expect(readFileSync(controller.memoryPath, 'utf8')).toBe('第一条 测试\n§\n第二条 测试\n')
    expect(RUNTIME_ENTRY_DELIMITER).toBe('\n§\n')
    await expect(controller.mutate({ action: 'add', target: 'memory', content: '不能包含 § 分隔符' })).rejects.toThrow('reserved § entry delimiter')
  })

  it('supports unique-substring replace and remove while preserving creation time', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'Use TypeScript for plugin code' })
    const replaced = await controller.mutate({ action: 'replace', target: 'memory', oldText: 'TypeScript', content: 'Use Rust for plugin code', importance: 'low' })
    expect(replaced).toMatchObject({ message: 'Entry replaced.', replaced: { from: 'Use TypeScript for plugin code', to: 'Use Rust for plugin code' } })
    expect(controller.snapshot().entries).toEqual([
      expect.objectContaining({ content: 'Use Rust for plugin code', importance: 'low', created_at: '2026-08-13T08:00:00.000Z' }),
    ])

    await expect(controller.mutate({ action: 'remove', target: 'memory', oldText: 'Rust' })).resolves.toMatchObject({ removed: 'Use Rust for plugin code', entryCount: 0 })
    expect(controller.snapshot().entries).toEqual([])
  })

  it.each([
    ['memory', 'remove'], ['memory', 'replace'], ['user', 'remove'], ['user', 'replace'],
  ] as const)('prefers an exact %s entry for %s over other substring matches', async (target, action) => {
    const now = new Date('2026-08-13T08:00:00.000Z')
    const { directory, controller } = fixture(now)
    const otherTarget = target === 'memory' ? 'user' : 'memory'
    await controller.mutate({ action: 'add', target, content: 'EGO_LINUX_CHROME' })
    await controller.mutate({ action: 'add', target, content: 'X', importance: 'critical', ...(target === 'memory' ? { branches: ['main'] } : {}) })
    await controller.mutate({ action: 'add', target: otherTarget, content: 'X' })
    const before = controller.snapshot()
    now.setTime(Date.parse('2026-08-14T08:00:00.000Z'))

    const result = await controller.mutate({ action, target, oldText: ' \nX\t ', ...(action === 'replace' ? { content: 'Y' } : {}) })
    expect(result).toMatchObject(action === 'remove'
      ? { removed: 'X', entryCount: 1 }
      : { replaced: { from: 'X', to: 'Y' }, entryCount: 2 })
    const entries = controller.snapshot().entries
    expect(entries.filter(entry => entry.content !== 'Y')).toEqual([before.entries[0], before.entries[2]])
    if (action === 'replace') {
      expect(entries[1]).toEqual({ ...before.entries[1], content: 'Y', updated_at: '2026-08-14T08:00:00.000Z' })
    }
    const projection = action === 'remove' ? 'EGO_LINUX_CHROME\n' : `EGO_LINUX_CHROME${RUNTIME_ENTRY_DELIMITER}Y\n`
    expect(readFileSync(target === 'memory' ? controller.memoryPath : controller.userPath, 'utf8')).toBe(projection)
    expect(new RuntimeMemoryController({ effectiveDataDir: () => directory }).snapshot().entries).toEqual(entries)
    expect(controller.contextProjection('main').entries.filter(entry => entry.target === target).map(entry => entry.content))
      .toEqual(action === 'remove' ? ['EGO_LINUX_CHROME'] : ['EGO_LINUX_CHROME', 'Y'])
    if (target === 'memory') {
      expect(controller.contextProjection('dev').entries.filter(entry => entry.target === target).map(entry => entry.content)).toEqual(['EGO_LINUX_CHROME'])
    }
  })

  it.each(['memory', 'user'] as const)('keeps substring fallback within %s when another target has an exact match', async target => {
    const { controller } = fixture()
    const otherTarget = target === 'memory' ? 'user' : 'memory'
    await controller.mutate({ action: 'add', target, content: 'EGO_LINUX_CHROME' })
    await controller.mutate({ action: 'add', target: otherTarget, content: 'X' })
    const other = controller.snapshot().entries[1]

    await expect(controller.mutate({ action: 'replace', target, oldText: 'X', content: 'Updated environment' }))
      .resolves.toMatchObject({ replaced: { from: 'EGO_LINUX_CHROME', to: 'Updated environment' } })
    expect(controller.snapshot().entries[1]).toEqual(other)
  })

  it.each(['remove', 'replace'] as const)('rejects %s of duplicate exact entries without choosing a branch or changing files', async action => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'X', branches: ['main'] })
    await controller.mutate({ action: 'add', target: 'memory', content: 'temporary value', branches: ['dev'] })
    await controller.mutate({ action: 'replace', target: 'memory', oldText: 'temporary value', content: 'X' })
    await controller.mutate({ action: 'add', target: 'memory', content: 'EGO_LINUX_CHROME' })
    const paths = [controller.sourcePath, controller.memoryPath, controller.userPath]
    const before = paths.map(path => readFileSync(path, 'utf8'))

    await expect(controller.mutate({ action, target: 'memory', oldText: 'X', content: 'Y', branches: ['main'] }))
      .rejects.toThrow('Multiple memory entries')
    expect(paths.map(path => readFileSync(path, 'utf8'))).toEqual(before)
  })

  it.each(['remove', 'replace'] as const)('rejects ambiguous substring %s without changing the source', async action => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'SQLite is local-first' })
    await controller.mutate({ action: 'add', target: 'memory', content: 'SQLite uses one file' })
    const paths = [controller.sourcePath, controller.memoryPath, controller.userPath]
    const before = paths.map(path => readFileSync(path, 'utf8'))
    await expect(controller.mutate({ action, target: 'memory', oldText: 'SQLite', content: 'Updated database' })).rejects.toThrow('Multiple memory entries')
    await expect(controller.mutate({ action, target: 'memory', oldText: 'missing', content: 'Updated database' })).rejects.toThrow('No memory entry')
    expect(paths.map(path => readFileSync(path, 'utf8'))).toEqual(before)
  })

  it('serializes concurrent callers, including independent controller instances', async () => {
    const { directory, controller } = fixture()
    const other = new RuntimeMemoryController({ effectiveDataDir: () => directory })
    await Promise.all(Array.from({ length: 20 }, (_, index) => (index % 2 === 0 ? controller : other).mutate({
      action: 'add',
      target: index % 3 === 0 ? 'user' : 'memory',
      content: `concurrent-entry-${index}`,
    })))
    const snapshot = controller.snapshot()
    expect(snapshot.entries).toHaveLength(20)
    expect(new Set(snapshot.entries.map(entry => entry.content)).size).toBe(20)
    const memoryProjection = snapshot.entries.filter(entry => entry.target === 'memory').map(entry => entry.content).join(RUNTIME_ENTRY_DELIMITER)
    expect(readFileSync(controller.memoryPath, 'utf8')).toBe(`${memoryProjection}\n`)
  })

  it('uses UTF-8 bytes for capacity and leaves every file unchanged on overflow', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'user', content: 'x'.repeat(4_090) })
    const beforeJson = readFileSync(controller.sourcePath, 'utf8')
    const beforeMarkdown = readFileSync(controller.userPath, 'utf8')
    await expect(controller.mutate({ action: 'add', target: 'user', content: '超出' })).rejects.toBeInstanceOf(RuntimeMemoryCapacityError)
    expect(readFileSync(controller.sourcePath, 'utf8')).toBe(beforeJson)
    expect(readFileSync(controller.userPath, 'utf8')).toBe(beforeMarkdown)
  })

  it('applies configured USER.md and MEMORY.md byte limits to every Runtime view and mutation', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runtime-configured-'))
    directories.push(directory)
    const controller = new RuntimeMemoryController(
      { effectiveDataDir: () => directory },
      undefined,
      { memory: 20_480, user: 10_240 },
    )

    await expect(controller.mutate({ action: 'add', target: 'user', content: 'u'.repeat(6_000) }))
      .resolves.toMatchObject({ usage: { used: 6_000, limit: 10_240 } })
    await controller.mutate({ action: 'add', target: 'memory', content: 'a'.repeat(8_000) })
    await expect(controller.mutate({ action: 'add', target: 'memory', content: 'b'.repeat(8_000) }))
      .resolves.toMatchObject({ usage: { used: 16_004, limit: 20_480 } })

    expect(controller.snapshot().targets).toMatchObject({
      user: { used: 6_000, limit: 10_240 },
      memory: { used: 16_004, limit: 20_480 },
    })
    expect((await controller.planMaintenance({ action: 'add', target: 'memory', content: 'c'.repeat(5_000) }))).toMatchObject({
      projected: 21_008,
      limit: 20_480,
      requiresMaintenance: true,
    })
  })

  it('combines a global USER.md with workspace MEMORY.md without moving or widening hidden entries', async () => {
    const globalRoot = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runtime-global-user-'))
    const workspaceRoot = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runtime-workspace-memory-'))
    directories.push(globalRoot, workspaceRoot)
    const globalRunner = { effectiveDataDir: () => globalRoot }
    const workspaceRunner = { effectiveDataDir: () => workspaceRoot }
    const global = new RuntimeMemoryController(globalRunner)
    const workspace = new RuntimeMemoryController(workspaceRunner)
    await global.mutate({ action: 'add', target: 'user', content: 'Always stash before pulling.' })
    await global.mutate({ action: 'add', target: 'memory', content: 'Hidden global project fact.' })
    await workspace.mutate({ action: 'add', target: 'user', content: 'Hidden workspace profile.' })
    await workspace.mutate({ action: 'add', target: 'memory', content: 'Exclude environment YAML from commits.' })

    const combined = new RuntimeMemoryController(workspaceRunner, undefined, undefined, globalRunner)
    expect(combined.userPath).toBe(join(globalRoot, 'runtime', 'USER.md'))
    expect(combined.userSourcePath).toBe(join(globalRoot, 'runtime', 'memories.json'))
    expect(combined.memoryPath).toBe(join(workspaceRoot, 'runtime', 'MEMORY.md'))
    expect(combined.snapshot().entries.map(entry => entry.content)).toEqual([
      'Always stash before pulling.',
      'Exclude environment YAML from commits.',
    ])
    expect(combined.contextProjection().text).toContain('Always stash before pulling.')
    expect(combined.contextProjection().text).toContain('Exclude environment YAML from commits.')
    expect(combined.contextProjection().text).not.toContain('Hidden global project fact.')
    expect(combined.contextProjection().text).not.toContain('Hidden workspace profile.')

    await combined.mutate({ action: 'add', target: 'user', content: 'Prefer concise answers.' })
    await combined.mutate({ action: 'add', target: 'memory', content: 'Run tests before project commits.' })
    expect(global.snapshot().entries.map(entry => entry.content)).toEqual([
      'Always stash before pulling.',
      'Hidden global project fact.',
      'Prefer concise answers.',
    ])
    expect(workspace.snapshot().entries.map(entry => entry.content)).toEqual([
      'Hidden workspace profile.',
      'Exclude environment YAML from commits.',
      'Run tests before project commits.',
    ])

    const userPlan = await combined.planMaintenance({ action: 'add', target: 'user', content: 'Prefer Chinese replies.' })
    await combined.compactAndMutate(
      userPlan.revision,
      { action: 'add', target: 'user', content: 'Prefer Chinese replies.' },
      [{ content: 'Keep local changes safe before Git synchronization.', importance: 'critical' }],
    )
    expect(global.snapshot().entries.map(entry => entry.content)).toEqual([
      'Hidden global project fact.',
      'Keep local changes safe before Git synchronization.',
      'Prefer Chinese replies.',
    ])
  })

  it('keeps the global profile visible while branch-filtering only workspace memory', async () => {
    const globalRoot = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runtime-global-branch-user-'))
    const workspaceRoot = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runtime-workspace-branch-memory-'))
    directories.push(globalRoot, workspaceRoot)
    const globalRunner = { effectiveDataDir: () => globalRoot }
    const workspaceRunner = { effectiveDataDir: () => workspaceRoot }
    const global = new RuntimeMemoryController(globalRunner)
    const workspace = new RuntimeMemoryController(workspaceRunner)
    await global.mutate({ action: 'add', target: 'user', content: 'Use the global user profile.' })
    await global.mutate({ action: 'add', target: 'memory', content: 'Hidden global project memory.' })
    await workspace.mutate({ action: 'add', target: 'user', content: 'Hidden workspace user profile.' })
    await workspace.mutate({ action: 'add', target: 'memory', content: 'Shared workspace fact.' })
    await workspace.mutate({ action: 'add', target: 'memory', content: 'Main-only workspace fact.', branches: ['main'] })
    await workspace.mutate({ action: 'add', target: 'memory', content: 'Dev-only workspace fact.', branches: ['dev'] })

    const combined = new RuntimeMemoryController(workspaceRunner, undefined, undefined, globalRunner)
    const complete = combined.snapshot()
    const onMain = combined.contextProjection('main')
    const onDev = combined.contextProjection('dev')

    expect(complete.entries.map(entry => entry.content)).toEqual([
      'Use the global user profile.',
      'Shared workspace fact.',
      'Main-only workspace fact.',
      'Dev-only workspace fact.',
    ])
    expect(onMain.revision).toBe(complete.revision)
    expect(onDev.revision).toBe(complete.revision)
    expect(onMain.text).toContain('Use the global user profile.')
    expect(onMain.text).toContain('Shared workspace fact.')
    expect(onMain.text).toContain('Main-only workspace fact.')
    expect(onMain.text).not.toContain('Dev-only workspace fact.')
    expect(onMain.text).not.toContain('Hidden global project memory.')
    expect(onMain.text).not.toContain('Hidden workspace user profile.')
    expect(onMain.text).toContain('Git branch: main (1 branch-scoped entry hidden)')
    expect(onMain.text).toContain(`Contents of MEMORY.md (working reference; entries: 2; UTF-8 bytes: ${complete.targets.memory.used}/${complete.targets.memory.limit})`)
    expect(onDev.text).toContain('Use the global user profile.')
    expect(onDev.text).toContain('Dev-only workspace fact.')
    expect(onDev.text).not.toContain('Main-only workspace fact.')
    expect(readFileSync(combined.memoryPath, 'utf8')).toContain('Main-only workspace fact.')
    expect(readFileSync(combined.memoryPath, 'utf8')).toContain('Dev-only workspace fact.')
  })

  it('repairs derived files from memories.json when a controller starts', async () => {
    const { directory, controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'source-owned value' })
    writeFileSync(controller.memoryPath, 'manual edit\n')
    new RuntimeMemoryController({ effectiveDataDir: () => directory })
    expect(readFileSync(controller.memoryPath, 'utf8')).toBe('source-owned value\n')
  })

  it('renders a bounded QoderWork-style runtime context from the committed source', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'user', content: 'User prefers concise Chinese replies', importance: 'critical' })
    const context = controller.contextProjection().text
    expect(context).toContain('MNEMON RUNTIME MEMORY SNAPSHOT')
    expect(context).toMatch(/Revision: [a-f0-9]{64}/u)
    expect(context).toContain('Contents of USER.md (user profile; entries: 1; UTF-8 bytes:')
    expect(context).toContain('<runtime-memory-file name="USER.md">\n[importance=critical; created=0d; updated=0d]\nUser prefers concise Chinese replies\n</runtime-memory-file>')
    expect(context).toContain('Contents of MEMORY.md (working reference; entries: 0; UTF-8 bytes: 0/10240)')
    expect(context).toContain('<runtime-memory-file name="MEMORY.md">\n(empty)\n</runtime-memory-file>')
    expect(context).not.toContain('MNEMON RUNTIME MEMORY PROTOCOL')
    expect(context).not.toContain('WRITE PROTOCOL')
    expect(context).not.toContain(controller.sourcePath)
  })

  it('projects recorded importance and elapsed creation/update days without rewriting stored content', async () => {
    const now = new Date('2026-09-01T08:00:00.000Z')
    const { controller } = fixture(now)
    await controller.mutate({ action: 'add', target: 'user', content: 'Prefer concise replies.', importance: 'critical' })
    await controller.mutate({ action: 'add', target: 'memory', content: 'Use pnpm.', importance: 'normal' })
    await controller.mutate({ action: 'add', target: 'memory', content: 'Temporary compatibility note.', importance: 'low' })
    now.setTime(Date.parse('2026-09-13T08:00:00.000Z'))
    await controller.mutate({ action: 'replace', target: 'memory', oldText: 'Use pnpm.', content: 'Use pnpm 10.' })
    const stored = controller.snapshot()
    const paths = [controller.sourcePath, controller.userPath, controller.memoryPath]
    const files = paths.map(path => readFileSync(path, 'utf8'))

    now.setTime(Date.parse('2026-09-15T07:59:59.999Z'))
    const beforeDay = controller.contextProjection()
    expect(beforeDay.text).toContain('[importance=critical; created=13d; updated=13d]\nPrefer concise replies.')
    expect(beforeDay.text).toContain('[importance=normal; created=13d; updated=1d]\nUse pnpm 10.')
    expect(beforeDay.text).toContain('[importance=low; created=13d; updated=13d]\nTemporary compatibility note.')
    expect(beforeDay.text).toContain('Metadata lines are annotations')
    expect(beforeDay.text).toContain('For old_text/oldText, use entry content only.')
    expect(beforeDay.text).toContain('Current instructions win.')

    now.setTime(Date.parse('2026-09-15T08:00:00.000Z'))
    const afterDay = controller.contextProjection()
    expect(afterDay.text).toContain('[importance=normal; created=14d; updated=2d]\nUse pnpm 10.')
    expect(afterDay.revision).toBe(beforeDay.revision)
    expect(afterDay.entries).toEqual(stored.entries)
    expect(controller.snapshot().targets).toEqual(stored.targets)
    expect(paths.map(path => readFileSync(path, 'utf8'))).toEqual(files)
    expect(beforeDay.text).toContain('created=13d; updated=1d')

    // Matching still uses the content, even when other entries have identical metadata.
    await controller.mutate({ action: 'remove', target: 'memory', oldText: 'Use pnpm 10.' })
    expect(controller.snapshot().entries.map(entry => entry.content)).toEqual(['Prefer concise replies.', 'Temporary compatibility note.'])
  })

  it('projects unknown or future ages safely and honors timestamp offsets', () => {
    const { controller } = fixture()
    const entries = [
      { content: 'Invalid timestamp remains readable.', created_at: 'invalid\n<instruction>', updated_at: '', target: 'user', importance: 'critical' },
      { content: 'Future timestamp remains readable.', created_at: '2026-08-14T08:00:00.000Z', updated_at: '2026-08-13T08:00:00.001Z', target: 'memory', importance: 'normal' },
      { content: 'A line\nwith whitespace.', created_at: '2026-08-12T10:00:00+02:00', updated_at: '2026-08-13T07:00:00Z', target: 'memory', importance: 'low' },
    ]
    writeFileSync(controller.sourcePath, JSON.stringify({ version: 1, entries }))
    const source = readFileSync(controller.sourcePath, 'utf8')
    const projection = controller.contextProjection()
    expect(projection.text).toContain('[importance=critical; created=unknown; updated=unknown]\nInvalid timestamp remains readable.')
    expect(projection.text).toContain('[importance=normal; created=future; updated=future]\nFuture timestamp remains readable.')
    expect(projection.text).toContain('[importance=low; created=1d; updated=0d]\nA line with whitespace.')
    expect(projection.text).not.toMatch(/NaN|Invalid Date|<instruction>/u)
    expect(readFileSync(controller.sourcePath, 'utf8')).toBe(source)
  })

  it('uses one age clock for global USER and branch-filtered workspace MEMORY', async () => {
    const { directory: globalRoot, controller: global } = fixture()
    const { directory: workspaceRoot, controller: workspace } = fixture()
    await global.mutate({ action: 'add', target: 'user', content: 'Global profile.' })
    await workspace.mutate({ action: 'add', target: 'memory', content: 'Visible main fact.', branches: ['main'] })
    await workspace.mutate({ action: 'add', target: 'memory', content: 'Hidden dev fact.', branches: ['dev'] })
    let samples = 0
    const combined = new RuntimeMemoryController(
      { effectiveDataDir: () => workspaceRoot },
      () => new Date(Date.parse('2026-08-14T07:59:59.999Z') + samples++),
      undefined, { effectiveDataDir: () => globalRoot },
    )
    const projection = combined.contextProjection('main')
    expect(projection.text).toContain('[importance=normal; created=0d; updated=0d]\nGlobal profile.')
    expect(projection.text).toContain('[importance=normal; created=0d; updated=0d]\nVisible main fact.')
    expect(projection.text).not.toContain('Hidden dev fact.')
    expect(samples).toBe(1)
  })

  it('assembles every prompt from the latest committed USER and MEMORY records', async () => {
    const { controller } = fixture()
    const empty = controller.contextProjection().text
    expect(empty).not.toContain('User prefers compact release notes')

    await controller.mutate({ action: 'add', target: 'user', content: 'User prefers compact release notes', importance: 'critical' })
    await controller.mutate({ action: 'add', target: 'memory', content: 'Release checks run with pnpm verify' })
    const populated = controller.contextProjection().text

    expect(populated).toContain('User prefers compact release notes')
    expect(populated).toContain('Release checks run with pnpm verify')
    expect(readFileSync(controller.userPath, 'utf8')).toBe('User prefers compact release notes\n')
    expect(readFileSync(controller.memoryPath, 'utf8')).toBe('Release checks run with pnpm verify\n')
  })

  it('keeps every changed snapshot complete across add, replace, and remove operations', async () => {
    const { controller } = fixture()
    const revisions = new Set([controller.contextProjection().revision])

    await controller.mutate({ action: 'add', target: 'user', content: 'User prefers compact release notes' })
    await controller.mutate({ action: 'add', target: 'memory', content: 'Release checks run with pnpm verify' })
    const added = controller.contextProjection()
    revisions.add(added.revision)
    expect(added.text).toContain('User prefers compact release notes')
    expect(added.text).toContain('Release checks run with pnpm verify')

    await controller.mutate({ action: 'replace', target: 'memory', oldText: 'pnpm verify', content: 'Release checks run with pnpm run verify' })
    const replaced = controller.contextProjection()
    revisions.add(replaced.revision)
    expect(replaced.text).toContain('User prefers compact release notes')
    expect(replaced.text).toContain('Release checks run with pnpm run verify')
    expect(replaced.text).not.toContain('Release checks run with pnpm verify\n')

    await controller.mutate({ action: 'remove', target: 'user', oldText: 'compact release notes' })
    const removed = controller.contextProjection()
    revisions.add(removed.revision)
    expect(removed.text).not.toContain('User prefers compact release notes')
    expect(removed.text).toContain('Release checks run with pnpm run verify')
    expect(removed.text).toContain('Contents of USER.md (user profile; entries: 0; UTF-8 bytes: 0/4096)')
    expect(revisions.size).toBe(4)
  })

  it('packs semantic compaction candidates into an exact host-owned byte budget', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'user', content: 'Original verbose preference.' })
    const request = { action: 'add', target: 'user', content: 'Newest preference.' } as const
    const plan = await controller.planMaintenance(request)

    await controller.compactAndMutate(plan.revision, request, [
      { content: 'normal candidate that cannot join the critical one', importance: 'normal' },
      { content: 'critical rule', importance: 'critical' },
      { content: 'low detail', importance: 'low' },
    ], 28)

    expect(controller.snapshot().entries.map(entry => ({ content: entry.content, importance: entry.importance }))).toEqual([
      { content: 'critical rule', importance: 'critical' },
      { content: 'low detail', importance: 'low' },
      { content: 'Newest preference.', importance: 'normal' },
    ])
  })

  it('atomically compacts surviving entries and applies a capacity-blocked exact replacement', async () => {
    const { controller } = fixture()
    const oldContent = 'X'
    const containingEntry = `EGO_LINUX_CHROME ${'a'.repeat(4_984)}`
    const replacement = `new-${'n'.repeat(496)}`
    await controller.mutate({ action: 'add', target: 'memory', content: oldContent })
    await controller.mutate({ action: 'add', target: 'memory', content: containingEntry })
    await controller.mutate({ action: 'add', target: 'memory', content: 'b'.repeat(5_000) })
    const request = { action: 'replace', target: 'memory', oldText: oldContent, content: replacement } as const

    await expect(controller.mutate(request)).rejects.toBeInstanceOf(RuntimeMemoryCapacityError)
    const plan = await controller.planMaintenance(request)
    expect(plan).toMatchObject({
      action: 'replace',
      requiresMaintenance: true,
      pending: { content: replacement },
      excluded: { content: oldContent },
    })
    expect(plan.entries.map(entry => entry.content)).toEqual([containingEntry, 'b'.repeat(5_000)])

    const result = await controller.compactAndMutate(
      plan.revision,
      request,
      [{ content: 'Archived details remain available in project memory.', importance: 'normal' }],
      6_000,
    )
    expect(result).toMatchObject({ replaced: { from: oldContent, to: replacement } })
    expect(controller.snapshot().entries.map(entry => entry.content)).toEqual([
      'Archived details remain available in project memory.',
      replacement,
    ])
    expect(readFileSync(controller.memoryPath, 'utf8')).not.toContain(oldContent)
  })

  it('atomically commits a capacity-blocked add with the reviewed compaction', async () => {
    const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runtime-atomic-lineage-'))
    directories.push(directory)
    const controller = new RuntimeMemoryController(
      { effectiveDataDir: () => directory },
      undefined,
    )
    await controller.mutate({ action: 'add', target: 'memory', content: 'a'.repeat(5_000) })
    await controller.mutate({ action: 'add', target: 'memory', content: 'b'.repeat(5_000) })
    const request = { action: 'add', target: 'memory', content: 'new durable fact '.repeat(30) } as const
    await expect(controller.mutate(request)).rejects.toBeInstanceOf(RuntimeMemoryCapacityError)
    const plan = await controller.planMaintenance(request)
    const result = await controller.compactAndMutate(
      plan.revision,
      request,
      [{ content: 'Archived workspace history.', importance: 'normal' }],
      6_000,
    )

    expect(result).toMatchObject({ added: request.content.trim() })
    expect(controller.snapshot().entries.map(entry => entry.content)).toEqual([
      'Archived workspace history.',
      request.content.trim(),
    ])
  })

  it('never archives or preserves a removed entry while recovering an already-over-capacity file', async () => {
    const { controller } = fixture()
    const now = '2026-08-13T08:00:00.000Z'
    const removed = `delete-${'x'.repeat(93)}`
    const entries = [removed, 'a'.repeat(5_150), 'b'.repeat(5_150)].map(content => ({
      content,
      created_at: now,
      updated_at: now,
      target: 'memory' as const,
      importance: 'normal' as const,
    }))
    writeFileSync(controller.sourcePath, `${JSON.stringify({ version: 1, entries }, null, 2)}\n`)
    writeFileSync(controller.memoryPath, `${entries.map(entry => entry.content).join(RUNTIME_ENTRY_DELIMITER)}\n`)
    const request = { action: 'remove', target: 'memory', oldText: 'delete-' } as const

    await expect(controller.mutate(request)).rejects.toBeInstanceOf(RuntimeMemoryCapacityError)
    const plan = await controller.planMaintenance(request)
    expect(plan).toMatchObject({ action: 'remove', requiresMaintenance: true, excluded: { content: removed } })
    expect(plan.pending).toBeUndefined()
    expect(plan.entries.map(entry => entry.content)).not.toContain(removed)

    const result = await controller.compactAndMutate(
      plan.revision,
      request,
      [{ content: 'Remaining history is archived.', importance: 'normal' }],
      6_000,
    )
    expect(result).toMatchObject({ removed })
    expect(controller.snapshot().entries.map(entry => entry.content)).toEqual(['Remaining history is archived.'])
  })

  it('leaves every local file unchanged when a compacted mutation conflicts or still exceeds capacity', async () => {
    const { directory, controller } = fixture()
    const oldContent = `old-${'o'.repeat(96)}`
    await controller.mutate({ action: 'add', target: 'memory', content: oldContent })
    await controller.mutate({ action: 'add', target: 'memory', content: 'a'.repeat(5_000) })
    await controller.mutate({ action: 'add', target: 'memory', content: 'b'.repeat(5_000) })
    const request = { action: 'replace', target: 'memory', oldText: 'old-', content: 'n'.repeat(3_000) } as const
    await expect(controller.mutate(request)).rejects.toBeInstanceOf(RuntimeMemoryCapacityError)
    const obsolete = await controller.planMaintenance(request)

    const other = new RuntimeMemoryController({ effectiveDataDir: () => directory })
    await other.mutate({ action: 'replace', target: 'memory', oldText: 'old-', content: 'concurrent replacement' })
    const paths = [controller.sourcePath, controller.memoryPath, controller.userPath]
    const afterConcurrent = paths.map(path => readFileSync(path, 'utf8'))
    await expect(controller.compactAndMutate(
      obsolete.revision,
      request,
      [{ content: 'summary', importance: 'normal' }],
      6_000,
    )).rejects.toThrow('changed while archival')
    expect(paths.map(path => readFileSync(path, 'utf8'))).toEqual(afterConcurrent)

    const currentRequest = {
      action: 'replace',
      target: 'memory',
      oldText: 'concurrent replacement',
      content: 'n'.repeat(3_000),
    } as const
    await expect(controller.mutate(currentRequest)).rejects.toBeInstanceOf(RuntimeMemoryCapacityError)
    const current = await controller.planMaintenance(currentRequest)
    const beforeOverflow = paths.map(path => readFileSync(path, 'utf8'))
    await expect(controller.compactAndMutate(
      current.revision,
      currentRequest,
      [{ content: 'c'.repeat(8_000), importance: 'normal' }],
      10_240,
    )).rejects.toBeInstanceOf(RuntimeMemoryCapacityError)
    expect(paths.map(path => readFileSync(path, 'utf8'))).toEqual(beforeOverflow)
  })

  it('rejects compaction output that duplicates the pending mutation or reintroduces its excluded entry', async () => {
    const { controller } = fixture()
    const oldContent = `obsolete-${'o'.repeat(91)}`
    const replacement = `corrected-${'n'.repeat(490)}`
    await controller.mutate({ action: 'add', target: 'memory', content: oldContent })
    await controller.mutate({ action: 'add', target: 'memory', content: 'a'.repeat(5_000) })
    await controller.mutate({ action: 'add', target: 'memory', content: 'b'.repeat(5_000) })
    const request = { action: 'replace', target: 'memory', oldText: 'obsolete-', content: replacement } as const
    await expect(controller.mutate(request)).rejects.toBeInstanceOf(RuntimeMemoryCapacityError)
    const plan = await controller.planMaintenance(request)
    const paths = [controller.sourcePath, controller.memoryPath, controller.userPath]
    const before = paths.map(path => readFileSync(path, 'utf8'))

    await expect(controller.compactAndMutate(
      plan.revision,
      request,
      [{ content: replacement, importance: 'normal' }],
      6_000,
    )).rejects.toThrow('duplicates the pending mutation')
    expect(paths.map(path => readFileSync(path, 'utf8'))).toEqual(before)

    await expect(controller.compactAndMutate(
      plan.revision,
      request,
      [{ content: oldContent, importance: 'normal' }],
      6_000,
    )).rejects.toThrow('reintroduces the replaced or removed entry')
    expect(paths.map(path => readFileSync(path, 'utf8'))).toEqual(before)
  })
})

describe('RuntimeMemoryController branch scoping', () => {
  it('projects branch-scoped memory entries only when the current branch matches', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'Shared cross-branch fact' })
    await controller.mutate({ action: 'add', target: 'memory', content: 'Main-branch architecture decision', branches: ['main'] })

    const onMain = controller.contextProjection('main')
    expect(onMain.text).toContain('Shared cross-branch fact')
    expect(onMain.text).toContain('Main-branch architecture decision')
    expect(onMain.text).toContain('Git branch: main')

    const onDev = controller.contextProjection('dev')
    expect(onDev.text).toContain('Shared cross-branch fact')
    expect(onDev.text).not.toContain('Main-branch architecture decision')
    expect(onDev.text).toContain('Git branch: dev')
    expect(onDev.text).toContain('1 branch-scoped entry hidden')

    const unscoped = controller.contextProjection()
    expect(unscoped.text).toContain('Main-branch architecture decision')
    expect(unscoped.text).toContain('Shared cross-branch fact')
    expect(unscoped.text).not.toContain('Git branch:')
  })

  it('never filters the user profile and keeps disk projections complete', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'user', content: 'User prefers concise answers' })
    await controller.mutate({ action: 'add', target: 'memory', content: 'Feature-flag decision for release branch', branches: ['release'] })

    const onMain = controller.contextProjection('main')
    expect(onMain.text).toContain('User prefers concise answers')
    expect(onMain.text).not.toContain('Feature-flag decision for release branch')
    expect(readFileSync(controller.memoryPath, 'utf8')).toBe('Feature-flag decision for release branch\n')
  })

  it('validates and stores branch tags on add', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'Multi-branch fact', branches: ['main', 'feature/deep'] })
    expect(controller.snapshot().entries[0]).toMatchObject({ branches: ['main', 'feature/deep'] })
  })

  it('rejects invalid branch names without changing the store', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'baseline' })
    const before = readFileSync(controller.sourcePath, 'utf8')
    const invalid = ['has space', 'dot..dot', '/leading-slash', 'trailing/', 'trailing.', 'x'.repeat(129), 'bad*name', 'a@{b', 'a^b', 'a:b', 'a~b']
    for (const name of invalid) {
      await expect(controller.mutate({ action: 'add', target: 'memory', content: `t-${encodeURIComponent(name)}`, branches: [name] })).rejects.toThrow(/branch name/)
    }
    await expect(controller.mutate({ action: 'add', target: 'memory', content: 'too many', branches: ['a', 'b', 'a'] })).rejects.toThrow(/repeat/)
    await expect(controller.mutate({
      action: 'add',
      target: 'memory',
      content: 'too many branches',
      branches: Array.from({ length: 17 }, (_, index) => `b${index}`),
    })).rejects.toThrow(/at most/)
    expect(readFileSync(controller.sourcePath, 'utf8')).toBe(before)
  })

  it('rejects branch scoping for the user target', async () => {
    const { controller } = fixture()
    await expect(controller.mutate({ action: 'add', target: 'user', content: 'scoped user fact', branches: ['main'] }))
      .rejects.toThrow('branches applies to target=memory only')
  })

  it('replace inherits, updates, and clears branch scope', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'v1 decision', branches: ['main'] })
    await controller.mutate({ action: 'replace', target: 'memory', oldText: 'v1 decision', content: 'v2 decision' })
    expect(controller.snapshot().entries[0]).toMatchObject({ branches: ['main'] })

    await controller.mutate({ action: 'replace', target: 'memory', oldText: 'v2 decision', content: 'v3 decision', branches: ['dev'] })
    expect(controller.snapshot().entries[0]).toMatchObject({ branches: ['dev'] })

    await controller.mutate({ action: 'replace', target: 'memory', oldText: 'v3 decision', content: 'v4 global', branches: [] })
    const cleared = controller.snapshot().entries[0]!
    expect(cleared).toMatchObject({ content: 'v4 global' })
    expect('branches' in cleared).toBe(false)
    expect(controller.contextProjection('nowhere').text).toContain('v4 global')
  })

  it('keeps legacy memories.json files (without branches) loadable and fully visible', async () => {
    const { directory, controller } = fixture()
    writeFileSync(controller.sourcePath, JSON.stringify({
      version: 1,
      entries: [{
        content: 'Legacy unscoped fact',
        created_at: '2026-08-01T00:00:00.000Z',
        updated_at: '2026-08-01T00:00:00.000Z',
        target: 'memory',
        importance: 'normal',
      }],
    }, null, 2), 'utf8')
    const reloaded = new RuntimeMemoryController({ effectiveDataDir: () => directory })
    expect(reloaded.snapshot().entries).toEqual([
      { content: 'Legacy unscoped fact', created_at: '2026-08-01T00:00:00.000Z', updated_at: '2026-08-01T00:00:00.000Z', target: 'memory', importance: 'normal' },
    ])
    expect(reloaded.contextProjection('any-branch').text).toContain('Legacy unscoped fact')
  })

  it('carries branch scope through semantic compaction', async () => {
    const { controller } = fixture()
    await controller.mutate({ action: 'add', target: 'memory', content: 'v1 branch-scoped', branches: ['main', 'dev'] })
    await controller.mutate({ action: 'add', target: 'memory', content: 'unscoped' })
    const request = { action: 'add', target: 'memory', content: 'new unscoped fact' } as const
    const plan = await controller.planMaintenance(request)

    await controller.compactAndMutate(plan.revision, request, [
      { content: 'branch-scoped merged', importance: 'normal', branches: ['main', 'dev'] },
      { content: 'unscoped merged', importance: 'normal' },
    ])
    expect(controller.snapshot().entries).toEqual(expect.arrayContaining([
      expect.objectContaining({ content: 'branch-scoped merged', branches: ['main', 'dev'] }),
      expect.objectContaining({ content: 'unscoped merged' }),
    ]))
    const projection = controller.contextProjection('dev')
    expect(projection.text).toContain('branch-scoped merged')
  })
})
