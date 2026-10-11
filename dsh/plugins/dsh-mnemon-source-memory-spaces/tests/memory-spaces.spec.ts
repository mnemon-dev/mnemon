import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { resolveMemorySpacesConfig } from "../src/config.ts"
import { createRegistry, installedCliStub } from './providers.ts'
import type { ProcessRunner } from '../src/providers/process.ts'
import { MemoryProviderCatalog } from '../src/providers/catalog.ts'
import { createRunner } from '../src/runner.ts'

// The runner sees an installed Mnemon CLI; each test fakes the process it starts.
const FAKE_CLI = installedCliStub()

const temporaryDirectories: string[] = []

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

function temporaryDirectory(): string {
  const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-registry-'))
  temporaryDirectories.push(directory)
  return directory
}

describe('MemorySpaceRegistry', () => {
  it('preserves user-authored legacy names and database bytes when loading the renamed model', () => {
    const dataDir = temporaryDirectory()
    mkdirSync(join(dataDir, 'data', 'research'), { recursive: true })
    const database = join(dataDir, 'data', 'research', 'mnemon.db')
    writeFileSync(database, 'unchanged existing database')
    const original = { id: 'research', name: '研究记忆体', description: 'User-authored title, not product copy.', active: false,
      createdAt: '2026-08-13T00:00:00.000Z', updatedAt: '2026-08-13T00:00:00.000Z' }
    writeFileSync(join(dataDir, 'data', '.dsh-memory-bodies.json'), JSON.stringify({ version: 1, bodies: [original] }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)
    expect(registry.get('research')).toMatchObject(original)
    registry.update('research', { active: true })
    const stored = JSON.parse(readFileSync(registry.registryPath, 'utf8'))
    expect(stored.bodies[0]).toMatchObject({ id: 'research', name: original.name, active: true })
    expect(stored.spaces).toBeUndefined()
    expect(readFileSync(database, 'utf8')).toBe('unchanged existing database')
  })

  it('refreshes once per public metadata operation, not once per memory space or Provider descriptor', () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)
    registry.syncProviderService('holographic', { dataPath: join(dataDir, 'facts.json') }, Array.from({ length: 128 }, (_, index) => ({
      externalId: `namespace-${index}`, name: `Namespace ${index}`, description: 'Real registry metadata fixture.', connection: {},
    })))
    const revision = vi.spyOn(registry as unknown as { diskRevision(): string }, 'diskRevision')
    const bodies = registry.list()
    expect(bodies).toHaveLength(128)
    expect(revision).toHaveBeenCalledOnce()
    revision.mockClear()
    expect(registry.active()).toHaveLength(128)
    expect(revision).toHaveBeenCalledOnce()
    revision.mockClear()
    expect(registry.providerServices().items).toHaveLength(8)
    expect(revision).toHaveBeenCalledOnce()
    revision.mockClear()
    expect(registry.providerConnection(bodies[0]!.id)).toMatchObject({ dataPath: join(dataDir, 'facts.json') })
    expect(revision).toHaveBeenCalledOnce()
  })

  it('keeps an empty data directory at zero memory spaces instead of creating a phantom default', () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)

    expect(registry.list()).toEqual([])
    expect(existsSync(join(dataDir, 'data', '.dsh-memory-bodies.json'))).toBe(false)
  })

  it('migrates native stores into a global memory-space catalog without moving their databases', () => {
    const dataDir = temporaryDirectory()
    mkdirSync(join(dataDir, 'data', 'project'), { recursive: true })
    writeFileSync(join(dataDir, 'data', 'project', 'mnemon.db'), 'existing database')
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir, store: 'project' }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true, () => new Date('2026-08-13T00:00:00.000Z'))

    expect(registry.list()).toEqual([
      expect.objectContaining({
        id: 'project',
        name: 'project',
        active: true,
        dbPath: join(dataDir, 'data', 'project', 'mnemon.db'),
        provider: expect.objectContaining({ id: 'mnemon-native', label: 'Mnemon Native', kind: 'local' }),
      }),
    ])
    expect(readFileSync(join(dataDir, 'data', 'project', 'mnemon.db'), 'utf8')).toBe('existing database')
    expect(existsSync(join(dataDir, 'data', '.dsh-memory-bodies.json'))).toBe(true)
  })

  it('keeps a legacy default Store without presenting it as an auto-created default Memory Space', () => {
    const dataDir = temporaryDirectory()
    mkdirSync(join(dataDir, 'data', 'default'), { recursive: true })
    writeFileSync(join(dataDir, 'data', 'default', 'mnemon.db'), 'existing database')
    writeFileSync(join(dataDir, 'data', '.dsh-memory-bodies.json'), JSON.stringify({
      version: 1,
      bodies: [{
        id: 'default',
        name: '默认记忆体',
        description: '从现有 Mnemon Store 自动接入。',
        active: true,
        createdAt: '2026-05-29T00:00:00.000Z',
        updatedAt: '2026-08-13T00:00:00.000Z',
      }],
    }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())

    const registry = createRegistry(runner, true)

    expect(registry.list()).toEqual([
      expect.objectContaining({ id: 'default', name: 'default', description: 'Existing Mnemon Store discovered on disk.', active: true }),
    ])
    expect(readFileSync(join(dataDir, 'data', 'default', 'mnemon.db'), 'utf8')).toBe('existing database')
  })

  it('uses default for the first native Store and persists DSH metadata independently', async () => {
    const dataDir = temporaryDirectory()
    const process = vi.fn<ProcessRunner>(async () => ({ stdout: 'Created store', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir, store: 'default' }), process)
    const registry = createRegistry(runner, true, () => new Date('2026-08-13T00:00:00.000Z'))

    const created = await registry.create({ name: '产品决策', description: '产品范围、取舍与稳定决策；规划或复盘产品方向时召回。' })
    expect(created.id).toBe('default')
    expect(created.active).toBe(false)
    registry.update(created.id, { active: true, description: '稳定产品上下文；规划或复盘产品方向时召回。' })

    const reloaded = createRegistry(runner, true)
    expect(reloaded.get(created.id)).toMatchObject({ name: '产品决策', description: '稳定产品上下文；规划或复盘产品方向时召回。', active: true })
    expect(process).toHaveBeenCalledWith(FAKE_CLI, expect.arrayContaining(['--store', 'default', 'store', 'create', 'default']), expect.anything())
  })

  it('removes the native store before deleting its catalog entry', async () => {
    const dataDir = temporaryDirectory()
    const storeDirectory = join(dataDir, 'data', 'project')
    for (const store of ['default', 'project']) {
      mkdirSync(join(dataDir, 'data', store), { recursive: true })
      writeFileSync(join(dataDir, 'data', store, 'mnemon.db'), 'existing database')
    }
    const process = vi.fn<ProcessRunner>(async (_command, args) => {
      if (args.includes('remove')) rmSync(storeDirectory, { recursive: true, force: true })
      return { stdout: 'Removed store', stderr: '', exitCode: 0 }
    })
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir, store: 'project' }), process)
    const registry = createRegistry(runner, true)

    await expect(registry.remove('project')).resolves.toMatchObject({ id: 'project', name: 'project' })
    expect(registry.list()).toEqual([expect.objectContaining({ id: 'default' })])
    expect(process).toHaveBeenCalledWith(FAKE_CLI, ['--data-dir', dataDir, '--store', 'default', 'store', 'remove', 'project'], expect.anything())
  })

  it('switches Mnemon away from a deactivated default Store before deleting it', async () => {
    const dataDir = temporaryDirectory()
    for (const id of ['default', 'research']) {
      mkdirSync(join(dataDir, 'data', id), { recursive: true })
      writeFileSync(join(dataDir, 'data', id, 'mnemon.db'), `${id} database`)
    }
    writeFileSync(join(dataDir, 'active'), 'default\n')
    const process = vi.fn<ProcessRunner>(async (_command, args) => {
      const operation = args.indexOf('store')
      if (args[operation + 1] === 'set') writeFileSync(join(dataDir, 'active'), `${args[operation + 2]}\n`)
      if (args[operation + 1] === 'remove') rmSync(join(dataDir, 'data', String(args[operation + 2])), { recursive: true, force: true })
      return { stdout: '', stderr: '', exitCode: 0 }
    })
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), process)
    const registry = createRegistry(runner, true)
    registry.setActive('default', false)
    registry.setActive('research', true)

    await expect(registry.remove('default')).resolves.toMatchObject({ id: 'default', active: false })

    expect(readFileSync(join(dataDir, 'active'), 'utf8')).toBe('research\n')
    expect(registry.list()).toEqual([expect.objectContaining({ id: 'research', active: true })])
    expect(process.mock.calls.map(([, args]) => args)).toEqual([
      ['--data-dir', dataDir, '--store', 'research', 'store', 'set', 'research'],
      ['--data-dir', dataDir, '--store', 'research', 'store', 'remove', 'default'],
    ])
  })

  it('preserves the last native Store even when it is disabled for DSH', async () => {
    const dataDir = temporaryDirectory()
    mkdirSync(join(dataDir, 'data', 'default'), { recursive: true })
    writeFileSync(join(dataDir, 'data', 'default', 'mnemon.db'), 'default database')
    const process = vi.fn<ProcessRunner>()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), process)
    const registry = createRegistry(runner, true)
    registry.setActive('default', false)

    await expect(registry.remove('default')).rejects.toThrow('disable it for DSH or create another Memory Space first')
    expect(registry.list()).toEqual([expect.objectContaining({ id: 'default', active: false })])
    expect(process).not.toHaveBeenCalled()
  })

  it('requires a routing description and never derives a new id from model-authored text', async () => {
    const dataDir = temporaryDirectory()
    const process = vi.fn<ProcessRunner>(async () => ({ stdout: 'Created store', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir, store: 'default' }), process)
    const registry = createRegistry(runner, true)

    await expect(registry.create({ name: '含义不足', description: '' })).rejects.toThrow('description is required')
    await registry.create({ name: '基础空间', description: '首次初始化使用 Mnemon 原生 default Store。' })
    const created = await registry.create({ name: '发布与交付', description: '发布门禁、部署约束与回滚经验；准备发布时召回。' })
    expect(created.id).not.toContain('发布')
    expect(created.id).toMatch(/^[0-9a-f-]{36}$/)
  })

  it('registers an OpenViking memory space without creating or deleting a native Store', async () => {
    const dataDir = temporaryDirectory()
    const process = vi.fn<ProcessRunner>()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), process)
    const registry = createRegistry(runner, true, () => new Date('2026-08-16T00:00:00.000Z'))

    const created = await registry.create({
      name: '团队 OpenViking',
      description: '团队共享的远程长期记忆。',
      active: true,
      providerId: 'openviking',
      openViking: {
        endpoint: 'https://memory.example.com/',
        targetUri: 'viking://user/team/memories/',
        apiKey: 'secret-token',
        account: 'acme',
        user: 'grivn',
      },
    })

    expect(created).toMatchObject({
      id: expect.stringMatching(/^openviking-/),
      active: true,
      dbPath: '',
      provider: {
        id: 'openviking',
        label: 'OpenViking',
        kind: 'remote',
        location: 'https://memory.example.com',
        targetUri: 'viking://user/team/memories',
        account: 'acme',
        user: 'grivn',
        apiKeyConfigured: true,
        capabilities: expect.objectContaining({ graph: false, remember: true, writeMode: 'exact' }),
      },
    })
    expect(registry.providerConnection(created.id, 'openviking')).toMatchObject({ apiKey: 'secret-token' })
    expect(JSON.parse(readFileSync(registry.registryPath, 'utf8'))).toEqual({ version: 1, bodies: [] })
    expect(JSON.parse(readFileSync(registry.providerRegistryPath, 'utf8'))).toMatchObject({
      version: 4,
      services: { openviking: expect.objectContaining({ endpoint: 'https://memory.example.com', apiKey: 'secret-token', account: 'acme' }) },
      enabled: { openviking: true },
      bodies: [expect.objectContaining({ id: created.id, providerId: 'openviking', connection: expect.objectContaining({ targetUri: 'viking://user/team/memories', user: 'grivn' }) })],
    })
    expect(statSync(registry.providerRegistryPath).mode & 0o777).toBe(0o600)
    expect(createRegistry(runner, true).get(created.id)).toMatchObject({ provider: { id: 'openviking', apiKeyConfigured: true } })
    expect(process).not.toHaveBeenCalled()

    await expect(registry.remove(created.id)).resolves.toMatchObject({ id: created.id })
    expect(registry.list()).toEqual([])
    expect(existsSync(registry.providerRegistryPath)).toBe(true)
    expect(JSON.parse(readFileSync(registry.providerRegistryPath, 'utf8'))).toMatchObject({ services: { openviking: expect.any(Object) }, bodies: [] })
    expect(process).not.toHaveBeenCalled()
  })

  it('keeps provider service settings separate from Memory Space scope settings', async () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)

    await expect(registry.create({
      name: '团队记忆', description: '团队共享内容。', providerId: 'openviking',
      connection: { targetUri: 'viking://user/team/memories', user: 'alice' },
    })).rejects.toThrow('enable it on the dsh-mnemon page under Plugins first')

    const service = registry.updateProviderService('openviking', { endpoint: 'http://127.0.0.1:1933', apiKey: 'service-secret', account: 'team' })
    expect(service).toEqual({ providerId: 'openviking', enabled: true, configured: true, settings: { endpoint: 'http://127.0.0.1:1933', account: 'team' }, configuredSecrets: ['apiKey'] })
    expect(registry.providerServices({ includeSecrets: true }).items.find(item => item.providerId === 'openviking')).toMatchObject({ secretValues: { apiKey: 'service-secret' } })

    const created = await registry.create({
      name: '团队记忆', description: '团队共享内容。', providerId: 'openviking',
      connection: { targetUri: 'viking://user/team/memories', user: 'alice' },
    })
    expect(created.provider.settings).toEqual({ targetUri: 'viking://user/team/memories', user: 'alice', actorPeerId: 'dsh' })
    expect(registry.providerConnection(created.id)).toMatchObject({ endpoint: 'http://127.0.0.1:1933', apiKey: 'service-secret', account: 'team', targetUri: 'viking://user/team/memories', user: 'alice' })

    const stored = JSON.parse(readFileSync(registry.providerRegistryPath, 'utf8'))
    expect(stored.services.openviking).toMatchObject({ endpoint: 'http://127.0.0.1:1933', apiKey: 'service-secret' })
    expect(stored.enabled.openviking).toBe(true)
    expect(stored.bodies[0].connection).toEqual({ targetUri: 'viking://user/team/memories', user: 'alice', actorPeerId: 'dsh' })
  })

  it('derives third-party card location from its descriptor instead of built-in field names', async () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const catalog = new MemoryProviderCatalog([{
      id: 'vector-store', label: 'Vector Store', kind: 'remote', workspaceBinding: 'provider-global',
      summary: 'Fixture vector store.', origin: 'third-party',
      capabilities: {
        search: true, browse: true, graph: false, entities: false, related: false,
        remember: true, link: false, forget: false, writeMode: 'exact', deletionMode: 'unsupported',
      },
      fields: [
        { key: 'serverUrl', label: 'Server', scope: 'service', input: 'url', required: true },
        { key: 'collection', label: 'Collection', scope: 'memory', input: 'text', required: true },
      ],
    }])
    const registry = createRegistry(runner, true, () => new Date('2026-08-30T00:00:00.000Z'), catalog)
    registry.updateProviderService('vector-store', { serverUrl: 'https://vectors.example' })

    await expect(registry.create({
      name: 'Vectors', description: 'Independent plugin namespace.', providerId: 'vector-store', connection: { collection: 'alpha' },
    })).resolves.toMatchObject({ provider: { id: 'vector-store', location: 'https://vectors.example' } })
  })

  it('keeps third-party providers off by default and removes local Memory Space projections when disabled', async () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)

    expect(registry.providerServices().items.every(service => !service.enabled && !service.configured)).toBe(true)

    registry.updateProviderService('openviking', { endpoint: 'http://127.0.0.1:1933', apiKey: 'secret' })
    const body = await registry.create({
      name: '团队记忆', description: '团队共享内容。', active: true, providerId: 'openviking',
      connection: { targetUri: 'viking://user/team/memories' },
    })
    expect(registry.active().map(item => item.id)).toContain(body.id)

    const disabled = registry.updateProviderService('openviking', {}, [], false)
    expect(disabled).toMatchObject({ enabled: false, configured: true, configuredSecrets: ['apiKey'] })
    expect(registry.active().map(item => item.id)).not.toContain(body.id)
    expect(registry.list()).toEqual([])
    expect(registry.placementCandidates({}).find(candidate => candidate.id === 'openviking')).toMatchObject({ configured: false })

    const reloaded = createRegistry(runner, true)
    expect(reloaded.providerServices().items.find(service => service.providerId === 'openviking')).toMatchObject({ enabled: false, configured: true, configuredSecrets: ['apiKey'] })
    expect(reloaded.list()).toEqual([])
    expect(JSON.parse(readFileSync(registry.providerRegistryPath, 'utf8'))).toMatchObject({ enabled: { openviking: false }, bodies: [] })
  })

  it('repairs legacy disabled providers that still contain stale Memory Space metadata', () => {
    const dataDir = temporaryDirectory()
    mkdirSync(join(dataDir, 'state'), { recursive: true })
    writeFileSync(join(dataDir, 'state', 'memory-providers.json'), JSON.stringify({
      version: 3,
      services: { openviking: { endpoint: 'http://127.0.0.1:1933', apiKey: 'secret', account: '' } },
      enabled: { openviking: false },
      bodies: [{
        id: 'openviking-stale', name: 'Stale body', description: 'Must be removed.', active: true, providerId: 'openviking',
        connection: { targetUri: 'viking://user/memories', user: 'alice', actorPeerId: 'dsh' },
        createdAt: '2026-08-16T00:00:00.000Z', updatedAt: '2026-08-16T00:00:00.000Z',
      }],
    }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)

    expect(registry.list()).toEqual([])
    expect(JSON.parse(readFileSync(registry.providerRegistryPath, 'utf8'))).toMatchObject({ version: 4, enabled: { openviking: false }, bodies: [] })
  })

  it('atomically maps provider discovery metadata and removes namespaces missing from the next sync', () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true, () => new Date('2026-08-17T00:00:00.000Z'))
    const service = registry.resolveProviderService('hindsight', { endpoint: 'http://127.0.0.1:18889', apiKey: 'secret' })

    registry.syncProviderService('hindsight', service, [
      { externalId: 'bank-a', name: 'Alice', description: 'Original upstream profile.', connection: { bankId: 'bank-a', budget: 'mid' } },
      { externalId: 'bank-b', name: 'Team', description: 'Shared upstream bank.', connection: { bankId: 'bank-b', budget: 'high' } },
    ])
    const first = registry.list()
    expect(first).toEqual([
      expect.objectContaining({ name: 'Alice', description: 'Original upstream profile.', active: true, provider: expect.objectContaining({ id: 'hindsight', settings: { bankId: 'bank-a', budget: 'mid' } }) }),
      expect.objectContaining({ name: 'Team', description: 'Shared upstream bank.', active: true }),
    ])

    registry.syncProviderService('hindsight', service, [
      { externalId: 'bank-a', name: 'Alice profile', description: 'Updated directly in Hindsight.', connection: { bankId: 'bank-a', budget: 'low' } },
    ])
    const refreshed = registry.list()
    expect(refreshed).toEqual([
      expect.objectContaining({ id: first[0]!.id, name: 'Alice profile', description: 'Updated directly in Hindsight.', active: true, provider: expect.objectContaining({ settings: { bankId: 'bank-a', budget: 'low' } }) }),
    ])

    registry.updateMetadata([{
      memoryBodyId: first[0]!.id,
      title: '产品与用户洞察',
      description: '汇总产品范围、用户反馈与关键取舍，在规划和复盘产品方向时召回。',
    }])
    registry.syncProviderService('hindsight', service, [
      { externalId: 'bank-a', name: 'Alice renamed upstream', description: 'Changed again directly in Hindsight.', connection: { bankId: 'bank-a', budget: 'high' } },
    ])
    expect(registry.list()).toEqual([
      expect.objectContaining({
        id: first[0]!.id,
        name: '产品与用户洞察',
        description: '汇总产品范围、用户反馈与关键取舍，在规划和复盘产品方向时召回。',
        provider: expect.objectContaining({ settings: { bankId: 'bank-a', budget: 'high' } }),
      }),
    ])
    expect(JSON.parse(readFileSync(registry.providerRegistryPath, 'utf8'))).toMatchObject({
      version: 4,
      bodies: [{ externalId: 'bank-a', name: '产品与用户洞察', metadataSource: 'ai' }],
    })
  })

  it('fills missing provider metadata from the nearest namespace fields and bounded defaults', () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)
    const longTitle = 'T'.repeat(120)
    const longDescription = 'D'.repeat(1_200)

    registry.syncProviderService('hindsight', { endpoint: 'https://hindsight.example' }, [
      { externalId: 'blank-title', name: '   ', description: '   ', connection: { bankId: 'blank-title', budget: 'mid' } },
      { externalId: 'long-title', name: longTitle, description: longDescription, connection: { bankId: 'long-title', budget: 'mid' } },
    ])

    expect(registry.list().map(body => ({ name: body.name, description: body.description }))).toEqual([
      { name: 'blank-title', description: 'Hindsight memory namespace mapped from blank-title.' },
      { name: longTitle.slice(0, 100), description: longDescription.slice(0, 1000) },
    ])
  })

  it('promotes a discovered ByteRover directory into reusable service configuration', () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)
    const service = registry.resolveProviderService('byterover', { cliPath: 'brv' })

    registry.syncProviderService('byterover', service, [{
      externalId: '/srv/knowledge', name: 'knowledge', description: 'ByteRover directory', connection: { workingDirectory: '/srv/knowledge' },
    }])
    registry.updateProviderService('byterover', {}, [], false)

    expect(registry.list()).toEqual([])
    expect(registry.providerServices().items.find(item => item.providerId === 'byterover')).toMatchObject({
      enabled: false,
      configured: true,
      settings: { cliPath: 'brv', defaultDirectory: '/srv/knowledge' },
    })
  })

  it('commits validated AI metadata as one batch without partially applying invalid output', async () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>(async () => ({ stdout: '', stderr: '', exitCode: 0 })))
    const registry = createRegistry(runner, true)
    const first = await registry.create({ name: 'First', description: 'First durable scope.' })
    const second = await registry.create({ name: 'Second', description: 'Second durable scope.' })

    expect(() => registry.updateMetadata([
      { memoryBodyId: first.id, title: '产品决策', description: '记录稳定的产品范围与取舍，在规划和复盘产品方向时召回。' },
      { memoryBodyId: second.id, title: 'x'.repeat(49), description: '记录稳定的发布规则和经验，在上线或故障处理时召回。' },
    ])).toThrow('title is too long')
    expect(registry.get(first.id).name).toBe('First')

    expect(registry.updateMetadata([
      { memoryBodyId: first.id, title: '产品决策', description: '记录稳定的产品范围与取舍，在规划和复盘产品方向时召回。' },
      { memoryBodyId: second.id, title: '发布运行手册', description: '记录稳定的发布规则和经验，在上线或故障处理时召回。' },
    ])).toEqual([
      expect.objectContaining({ id: first.id, name: '产品决策' }),
      expect.objectContaining({ id: second.id, name: '发布运行手册' }),
    ])
  })

  it('persists an audited automatic placement and refuses to create before placement resolves', async () => {
    const dataDir = temporaryDirectory()
    const process = vi.fn<ProcessRunner>(async () => ({ stdout: 'Created store', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), process)
    const registry = createRegistry(runner, true, () => new Date('2026-08-16T00:00:00.000Z'))
    const request = {
      name: '本地产品决策',
      description: '需要精确写入和关系图的长期产品决策。',
      placement: {
        mode: 'automatic' as const,
        rules: { requiredCapabilities: ['graph' as const] },
      },
    }

    await expect(registry.create(request)).rejects.toThrow('must be resolved')
    const created = await registry.create(request, undefined, {
      mode: 'automatic',
      providerId: 'mnemon-native',
      decidedBy: 'rules',
      reason: 'Only Mnemon Native satisfies the configured placement rules.',
      confidence: 'high',
      candidateProviderIds: ['mnemon-native'],
      appliedRules: ['requires:graph', 'preference:balanced'],
      decidedAt: '2026-08-16T00:00:00.000Z',
    })

    expect(created).toMatchObject({
      provider: { id: 'mnemon-native' },
      placement: { decidedBy: 'rules', providerId: 'mnemon-native', confidence: 'high' },
    })
    expect(createRegistry(runner, true).get(created.id)).toMatchObject({
      placement: { reason: expect.stringContaining('Mnemon Native'), appliedRules: ['requires:graph', 'preference:balanced'] },
    })
  })

  it('keeps candidate credentials out of placement metadata and persists only the selected provider connection', async () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true, () => new Date('2026-08-16T00:00:00.000Z'))
    const request = {
      name: '用户偏好',
      description: '由外部服务提炼和去重的跨会话用户偏好。',
      providerConnections: {
        mem0: { endpoint: 'https://api.mem0.ai', apiKey: 'mem0-secret', mode: 'platform', userId: 'alice', agentId: 'dsh' },
        supermemory: { endpoint: 'https://api.supermemory.ai', apiKey: 'sm-secret', containerTag: 'alice', searchMode: 'hybrid' },
      },
      placement: { mode: 'automatic' as const },
    }

    const candidates = registry.placementCandidates(request)
    expect(candidates.find(candidate => candidate.id === 'mem0')).toMatchObject({ configured: true })
    expect(candidates.find(candidate => candidate.id === 'supermemory')).toMatchObject({ configured: true })
    expect(JSON.stringify(candidates)).not.toContain('secret')

    const created = await registry.create(request, undefined, {
      mode: 'automatic',
      providerId: 'mem0',
      decidedBy: 'llm',
      reason: 'Mem0 best matches automatic extraction and deduplication.',
      confidence: 'high',
      candidateProviderIds: ['mem0', 'supermemory'],
      appliedRules: ['preference:balanced'],
      decidedAt: '2026-08-16T00:00:00.000Z',
      runId: 'placement-1',
      subagentProvider: 'spawn',
    })

    expect(created).toMatchObject({
      provider: {
        id: 'mem0',
        settings: { userId: 'alice', agentId: 'dsh', rerank: false },
        configuredSecrets: [],
        apiKeyConfigured: true,
      },
    })
    expect(registry.providerConnection(created.id, 'mem0')).toMatchObject({ apiKey: 'mem0-secret' })
    expect(readFileSync(registry.providerRegistryPath, 'utf8')).not.toContain('sm-secret')
  })

  it('ignores malformed placement metadata without losing the Memory Space', () => {
    const dataDir = temporaryDirectory()
    mkdirSync(join(dataDir, 'data', 'default'), { recursive: true })
    writeFileSync(join(dataDir, 'data', 'default', 'mnemon.db'), 'existing database')
    writeFileSync(join(dataDir, 'data', '.dsh-memory-bodies.json'), JSON.stringify({
      version: 1,
      bodies: [{
        id: 'default', name: 'Local', description: 'Local decisions.', active: true,
        placement: { mode: 'automatic', providerId: 'openviking', decidedBy: 'llm', confidence: 'certain', reason: 'wrong provider' },
        createdAt: '2026-08-16T00:00:00.000Z', updatedAt: '2026-08-16T00:00:00.000Z',
      }],
    }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())

    const body = createRegistry(runner, true).get('default')
    expect(body).toMatchObject({ id: 'default', name: 'Local', provider: { id: 'mnemon-native' } })
    expect(body.placement).toBeUndefined()
  })

  it('migrates a version 2 mixed registry into native data and provider state without losing credentials', () => {
    const dataDir = temporaryDirectory()
    mkdirSync(join(dataDir, 'data', 'default'), { recursive: true })
    writeFileSync(join(dataDir, 'data', 'default', 'mnemon.db'), 'existing database')
    writeFileSync(join(dataDir, 'data', '.dsh-memory-bodies.json'), JSON.stringify({
      version: 2,
      bodies: [
        { id: 'default', name: 'Local', description: 'Local data.', active: true, providerId: 'mnemon-native', createdAt: '2026-08-16T00:00:00.000Z', updatedAt: '2026-08-16T00:00:00.000Z' },
        { id: 'openviking-legacy', name: 'Remote', description: 'Remote data.', active: true, providerId: 'openviking', openViking: { endpoint: 'https://memory.example.com', targetUri: 'viking://user/team/memories', apiKey: 'legacy-secret', account: '', user: '', actorPeerId: '' }, createdAt: '2026-08-16T00:00:00.000Z', updatedAt: '2026-08-16T00:00:00.000Z' },
      ],
    }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())

    const registry = createRegistry(runner, true)

    expect(registry.list()).toHaveLength(2)
    expect(JSON.parse(readFileSync(registry.registryPath, 'utf8'))).toMatchObject({ version: 1, bodies: [expect.objectContaining({ id: 'default' })] })
    expect(readFileSync(registry.providerRegistryPath, 'utf8')).toContain('legacy-secret')
  })

  it('rejects OpenViking targets outside the user memory namespace', async () => {
    const dataDir = temporaryDirectory()
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: FAKE_CLI, dataDir }), vi.fn<ProcessRunner>())
    const registry = createRegistry(runner, true)

    await expect(registry.create({
      name: '错误范围',
      description: '不能把资源目录当作长期记忆空间。',
      providerId: 'openviking',
      openViking: { endpoint: 'http://127.0.0.1:1933', targetUri: 'viking://resources' },
    })).rejects.toThrow('viking://user/.../memories root')
    await expect(registry.create({
      name: '过窄范围',
      description: '异步提炼无法保证写入自定义子目录。',
      providerId: 'openviking',
      openViking: { endpoint: 'http://127.0.0.1:1933', targetUri: 'viking://user/memories/team' },
    })).rejects.toThrow('memories root')
  })
})
