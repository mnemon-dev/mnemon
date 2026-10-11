import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { strToU8, unzipSync, zipSync } from 'fflate'
import { resolveConfig } from "../src/host/config.ts"
import { MnemonPackManager, MNEMON_PACK_FORMAT, MNEMON_PACK_MIME } from "../src/host/pack.ts"
import { createStorageRoot } from '../src/host/storage-root.ts'
import { sourceFixture } from './fixtures/sources.ts'
import { withMemoryStorageLock } from 'dsh-mnemon/extension-sdk'

const directories: string[] = []
const releases: Array<() => Promise<void>> = []
const now = () => new Date('2026-08-14T12:00:00.000Z')

function temporary(label: string): string {
  const directory = mkdtempSync(join(tmpdir(), `dsh-mnemon-${label}-`))
  directories.push(directory)
  return directory
}

function sqlite(seed: number): Buffer {
  const bytes = Buffer.alloc(4096)
  bytes.write('SQLite format 3\0', 0, 'binary')
  bytes[4095] = seed
  return bytes
}

function runner(root: string) {
  const config = resolveConfig({ storageScope: 'custom', dataDir: root, cliPath: '/fake/mnemon' })
  return { config, runner: createStorageRoot(config) }
}

async function fixture(label: string, seed: number, bodyId = 'project') {
  const root = temporary(label)
  const workspace = temporary(`${label}-workspace`)
  const created = runner(root)
  const sources = await sourceFixture({ dataDir: root, workspace })
  releases.push(sources.dispose)
  await sources.runtime.mutate('mutate', { action: 'add', target: 'user', content: 'Prefer concise answers', importance: 'normal' }, { confirmed: true })
  await sources.documents.mutate('mutate', { action: 'create', title: `Design ${seed}`, content: `# Design\n\nSeed ${seed}` }, { confirmed: true })
  const data = join(root, 'data')
  mkdirSync(join(data, bodyId), { recursive: true })
  writeFileSync(join(data, bodyId, 'mnemon.db'), sqlite(seed))
  writeFileSync(join(data, '.dsh-memory-bodies.json'), `${JSON.stringify({
    version: 1,
    bodies: [{ id: bodyId, name: `Space ${seed}`, description: `Seed ${seed}`, active: true, createdAt: now().toISOString(), updatedAt: now().toISOString() }],
  }, null, 2)}\n`)
  writeFileSync(join(root, 'active'), `${bodyId}\n`)
  return { root, workspace, ...created, manager: new MnemonPackManager(created.runner, created.config, undefined, now) }
}

afterEach(async () => {
  vi.unstubAllEnvs()
  for (const release of releases.splice(0)) await release()
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

describe('Mnemon Pack', () => {
  it('waits for independently loaded storage work before collecting a snapshot', async () => {
    const source = await fixture('pack-concurrency', 20)
    const started = Promise.withResolvers<void>()
    const finish = Promise.withResolvers<void>()
    const write = withMemoryStorageLock(source.root, async () => {
      started.resolve()
      await finish.promise
      writeFileSync(join(source.root, 'data', 'project', 'mnemon.db'), sqlite(21))
    })
    await started.promise
    let settled = false
    const exportPromise = source.manager.exportPack('memory-spaces').then(value => { settled = true; return value })
    await new Promise<void>(resolve => setImmediate(resolve))
    expect(settled).toBe(false)
    finish.resolve()
    await write
    const exported = await exportPromise
    const files = unzipSync(Buffer.from(exported.base64, 'base64'))
    expect(Buffer.from(files['payload/data/project/mnemon.db']!)).toEqual(sqlite(21))
  })

  it('exports one native, checksummed Pack without leaking the host root', async () => {
    const source = await fixture('pack-export', 1)
    const exported = await source.manager.exportPack('full')
    const archive = Buffer.from(exported.base64, 'base64')
    const files = unzipSync(archive)
    const manifest = JSON.parse(Buffer.from(files['manifest.json']!).toString('utf8'))

    expect(exported).toMatchObject({ mimeType: MNEMON_PACK_MIME, targetRoot: source.root })
    expect(exported.fileName).toMatch(/^mnemon-backup-.*\.zip$/u)
    expect(manifest).toMatchObject({ format: MNEMON_PACK_FORMAT, version: 1, scope: 'full', components: ['runtime', 'documents', 'memory-spaces'] })
    expect(Object.keys(files)).toEqual(expect.arrayContaining([
      'payload/runtime/memories.json',
      'payload/documents/index.json',
      'payload/data/.dsh-memory-bodies.json',
      'payload/data/project/mnemon.db',
    ]))
    expect(archive.includes(Buffer.from(source.root))).toBe(false)
    expect(source.manager.inspectPack(exported.base64, '../unsafe name.mnemonpack')).toMatchObject({
      fileName: 'unsafe-name.mnemonpack', targetRoot: source.root, archiveBytes: archive.length,
      occupied: { runtime: true, documents: true, 'memory-spaces': true },
    })
  })

  it('names the root it reads and writes, and the default one the global scope would use', () => {
    const root = temporary('pack-target')
    const defaults = temporary('pack-default')
    vi.stubEnv('MNEMON_DATA_DIR', defaults)
    const created = runner(root)
    expect(new MnemonPackManager(created.runner, created.config).target()).toEqual({ root, scope: 'custom', defaultRoot: defaults })
  })

  it('exports each component independently with the same Pack envelope', async () => {
    const source = await fixture('pack-parts', 2)
    for (const scope of ['runtime', 'documents', 'memory-spaces'] as const) {
      const exported = await source.manager.exportPack(scope)
      const files = unzipSync(Buffer.from(exported.base64, 'base64'))
      expect(exported.manifest).toMatchObject({ scope, components: [scope] })
      expect(Object.keys(files).filter(path => path.startsWith('payload/')).every(path => path.startsWith(`payload/${scope === 'memory-spaces' ? 'data' : scope}/`))).toBe(true)
    }
  })

  it.each(['full', 'runtime', 'documents', 'memory-spaces'] as const)('exports byte-identical %s Packs across timezones', async (scope) => {
    const source = await fixture('pack-timezones', 23)
    vi.stubEnv('TZ', 'UTC')
    const reference = await source.manager.exportPack(scope)

    for (const [timeZone, offset] of [
      ['UTC', 0],
      ['America/Los_Angeles', 480],
      ['Etc/GMT+12', 720],
      ['Etc/GMT-14', -840],
      ['Asia/Kolkata', -330],
    ] as const) {
      vi.stubEnv('TZ', timeZone)
      expect(new Date('1980-01-01T00:00:00.000Z').getTimezoneOffset(), timeZone).toBe(offset)
      const exported = await source.manager.exportPack(scope)
      expect(exported.base64, timeZone).toBe(reference.base64)
    }
  })

  it('validates Runtime Pack capacity against the configured target limits', async () => {
    const sourceRoot = temporary('pack-configured-runtime-source')
    const sourceConfig = resolveConfig({
      storageScope: 'custom',
      dataDir: sourceRoot,
      cliPath: '/fake/mnemon',
      runtimeMemory: { memoryLimitBytes: 20_480 },
    })
    const sourceRunner = createStorageRoot(sourceConfig)
    const sources = await sourceFixture({ dataDir: sourceRoot, workspace: sourceRoot, memoryLimitBytes: 20_480 })
    releases.push(sources.dispose)
    await sources.runtime.mutate('mutate', { action: 'add', target: 'memory', content: 'a'.repeat(8_000) }, { confirmed: true })
    await sources.runtime.mutate('mutate', { action: 'add', target: 'memory', content: 'b'.repeat(8_000) }, { confirmed: true })
    const exported = await new MnemonPackManager(sourceRunner, sourceConfig, undefined, now).exportPack('runtime')

    const defaultTarget = runner(temporary('pack-default-runtime-target'))
    expect(() => new MnemonPackManager(defaultTarget.runner, defaultTarget.config).inspectPack(exported.base64))
      .toThrow('10240 byte limit')

    const configuredRoot = temporary('pack-configured-runtime-target')
    const configuredTarget = resolveConfig({
      storageScope: 'custom',
      dataDir: configuredRoot,
      cliPath: '/fake/mnemon',
      runtimeMemory: { memoryLimitBytes: 20_480 },
    })
    const configuredRunner = createStorageRoot(configuredTarget)
    expect(new MnemonPackManager(configuredRunner, configuredTarget).inspectPack(exported.base64).manifest.scope).toBe('runtime')
  })

  it('keeps an overlaid global USER.md outside a workspace Runtime Pack', async () => {
    const globalRoot = temporary('pack-global-user')
    const workspaceRoot = temporary('pack-workspace-runtime')
    const workspace = runner(workspaceRoot)
    const sources = await sourceFixture({ dataDir: workspaceRoot, workspace: workspaceRoot, userDataDir: globalRoot })
    releases.push(sources.dispose)
    await sources.runtime.mutate('mutate', { action: 'add', target: 'user', content: 'Private global profile fixture.' }, { confirmed: true })
    await sources.runtime.mutate('mutate', { action: 'add', target: 'memory', content: 'Workspace project fixture.' }, { confirmed: true })

    const exported = await new MnemonPackManager(workspace.runner, workspace.config, undefined, now).exportPack('runtime')
    const files = unzipSync(Buffer.from(exported.base64, 'base64'))
    const source = JSON.parse(Buffer.from(files['payload/runtime/memories.json']!).toString('utf8')) as { entries: Array<{ content: string }> }

    expect(source.entries.map(entry => entry.content)).toEqual(['Workspace project fixture.'])
    expect(Buffer.from(exported.base64, 'base64').includes(Buffer.from('Private global profile fixture.'))).toBe(false)
  })

  it('keeps remote provider connections and credentials outside Mnemon Packs', async () => {
    const source = await fixture('pack-provider-boundary', 22)
    const registryPath = join(source.root, 'data', '.dsh-memory-providers.json')
    writeFileSync(registryPath, JSON.stringify({
      version: 1, services: [{ providerId: 'openviking', enabled: true, settings: {
        endpoint: 'https://memory.example.com', targetUri: 'viking://user/team/memories', apiKey: 'must-not-enter-pack',
      } }],
    }))

    const exported = await source.manager.exportPack('memory-spaces')
    const archive = Buffer.from(exported.base64, 'base64')
    const files = unzipSync(archive)

    expect(archive.includes(Buffer.from('must-not-enter-pack'))).toBe(false)
    expect(Object.keys(files).some(path => path.includes('memory-providers'))).toBe(false)
    expect(existsSync(registryPath)).toBe(true)
  })

  it('rejects checksum tampering, unsafe paths, and malformed transport data', async () => {
    const source = await fixture('pack-tamper', 3)
    const exported = await source.manager.exportPack('runtime')
    const files = unzipSync(Buffer.from(exported.base64, 'base64'))
    const runtimeSource = files['payload/runtime/memories.json']!
    runtimeSource[0] = runtimeSource[0]! ^ 1
    const tampered = Buffer.from(zipSync(files)).toString('base64')
    expect(() => source.manager.inspectPack(tampered)).toThrow('checksum mismatch')

    const unsafe = Buffer.from(zipSync({ '../escape': strToU8('x') })).toString('base64')
    expect(() => source.manager.inspectPack(unsafe)).toThrow('unsafe Pack entry path')
    expect(() => source.manager.inspectPack('not-base64')).toThrow('base64')
  })

  it('replaces a complete target atomically and refreshes the Memory Space catalog callback', async () => {
    const source = await fixture('pack-replace-source', 4, 'source')
    const target = await fixture('pack-replace-target', 5, 'target')
    const exported = await source.manager.exportPack('full')
    const refreshed = vi.fn()
    const manager = new MnemonPackManager(target.runner, target.config, refreshed, now)

    await expect(manager.importPack(exported.base64, { mode: 'replace' })).resolves.toMatchObject({ imported: true, components: ['runtime', 'documents', 'memory-spaces'] })

    expect(readdirSync(join(target.root, 'data')).filter(name => !name.startsWith('.'))).toEqual(['source'])
    expect(readFileSync(join(target.root, 'data', 'source', 'mnemon.db'))).toEqual(sqlite(4))
    expect(readFileSync(join(target.root, 'active'), 'utf8')).toBe('source\n')
    expect(refreshed).toHaveBeenCalledWith(['runtime', 'documents', 'memory-spaces'])
    expect(readdirSync(target.root).some(name => name.startsWith('.dsh-pack-stage-') || name.startsWith('.dsh-pack-backup-'))).toBe(false)
  })

  it('merges components without overwriting a divergent Memory Space id', async () => {
    const source = await fixture('pack-merge-source', 6, 'shared')
    const target = await fixture('pack-merge-target', 7, 'shared')
    const exported = await source.manager.exportPack('full')

    await target.manager.importPack(exported.base64, { mode: 'merge' })

    const registry = JSON.parse(readFileSync(join(target.root, 'data', '.dsh-memory-bodies.json'), 'utf8')) as { bodies: Array<{ id: string }> }
    expect(registry.bodies).toHaveLength(2)
    expect(new Set(registry.bodies.map(body => body.id)).size).toBe(2)
    expect(readdirSync(join(target.root, 'data')).filter(name => !name.startsWith('.'))).toHaveLength(2)
    const runtime = JSON.parse(readFileSync(join(target.root, 'runtime', 'memories.json'), 'utf8')) as { entries: unknown[] }
    expect(runtime.entries).toHaveLength(1)
    const documents = JSON.parse(readFileSync(join(target.root, 'documents', 'index.json'), 'utf8')) as { documents: unknown[] }
    expect(documents.documents).toHaveLength(2)
  })

  it('imports only the requested component from a complete Pack', async () => {
    const source = await fixture('pack-partial-source', 8, 'source')
    const target = await fixture('pack-partial-target', 9, 'target')
    const beforeDatabase = readFileSync(join(target.root, 'data', 'target', 'mnemon.db'))
    const exported = await source.manager.exportPack('full')

    await target.manager.importPack(exported.base64, { mode: 'replace', components: ['runtime'] })

    expect(readFileSync(join(target.root, 'data', 'target', 'mnemon.db'))).toEqual(beforeDatabase)
    expect(readdirSync(join(target.root, 'data')).filter(name => !name.startsWith('.'))).toEqual(['target'])
  })

  it('refuses to replace an initialized root with an empty Memory Space set', async () => {
    const empty = runner(temporary('pack-empty-source'))
    const emptyManager = new MnemonPackManager(empty.runner, empty.config, undefined, now)
    const exported = await emptyManager.exportPack('memory-spaces')
    const target = await fixture('pack-empty-target', 10, 'default')

    await expect(target.manager.importPack(exported.base64, { mode: 'replace' })).rejects.toThrow('last Mnemon Store')

    expect(readFileSync(join(target.root, 'data', 'default', 'mnemon.db'))).toEqual(sqlite(10))
    expect(readFileSync(join(target.root, 'active'), 'utf8')).toBe('default\n')
  })

  it('leaves the target untouched when staging a merge fails', async () => {
    const source = await fixture('pack-rollback-source', 10)
    const target = await fixture('pack-rollback-target', 11)
    const exported = await source.manager.exportPack('full')
    const runtimeBefore = readFileSync(join(target.root, 'runtime', 'memories.json'))
    writeFileSync(join(target.root, 'documents', 'index.json'), '{ broken')

    await expect(target.manager.importPack(exported.base64, { mode: 'merge' })).rejects.toThrow()

    expect(readFileSync(join(target.root, 'runtime', 'memories.json'))).toEqual(runtimeBefore)
    expect(readdirSync(target.root).some(name => name.startsWith('.dsh-pack-stage-') || name.startsWith('.dsh-pack-backup-'))).toBe(false)
  })

  it('refuses to copy a live WAL database as an inconsistent snapshot', async () => {
    const source = await fixture('pack-wal', 12)
    writeFileSync(join(source.root, 'data', 'project', 'mnemon.db-wal'), 'pending')
    await expect(source.manager.exportPack('memory-spaces')).rejects.toThrow('not checkpointed')
  })
})
