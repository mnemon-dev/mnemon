import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it, vi } from 'vitest'
import {
  assertVersionedPublicationChanges,
  createReleasePlan,
  packRelease,
  publishRelease,
  readChangedPaths,
  readReleaseArtifacts,
  readReleasePackages,
  publicationInputsChanged,
  verifyRegistryRelease,
  waitForRegistryArtifact,
  writeReleaseManifest,
} from '../scripts/release.mjs'
import { syncReleaseMetadata } from '../scripts/sync-release-metadata.mjs'
import { assertReleaseIntentCoverage } from '../scripts/check-release-intent.mjs'

const root = fileURLToPath(new URL('../', import.meta.url))
const revision = '1'.repeat(40)
const baseRevision = '2'.repeat(40)

function tag(version) {
  return version.match(/-(alpha|beta|rc)\./)?.[1] ?? 'latest'
}

function item(directory, name, version, extra = {}) {
  return { directory, manifest: { name, version, publishConfig: { access: 'public', tag: tag(version) }, ...extra } }
}

function fixture({
  starterVersion = '0.5.2',
  sourceVersion = '0.5.0',
  providerVersion = '0.5.1',
  secondProviderVersion,
} = {}) {
  const source = item('/source', 'dsh-mnemon-source-memory-spaces', sourceVersion, {
    peerDependencies: { 'dsh-mnemon': '^0.5.0' },
  })
  const providers = [item('/provider', 'dsh-mnemon-provider-example', providerVersion, {
    peerDependencies: { 'dsh-mnemon-source-memory-spaces': '^0.5.0' },
  })]
  if (secondProviderVersion !== undefined) providers.push(item('/provider-two', 'dsh-mnemon-provider-second', secondProviderVersion, {
    peerDependencies: { 'dsh-mnemon-source-memory-spaces': '^0.5.0' },
  }))
  const dependencies = Object.fromEntries([source, ...providers].map(packageItem => [packageItem.manifest.name, packageItem.manifest.version]))
  const starter = item('/starter', 'dsh-mnemon', starterVersion, { dependencies })
  return [starter, source, ...providers]
}

function previousVersions(packages, overrides = {}) {
  return new Map(packages.map(packageItem => [packageItem.manifest.name,
    overrides[packageItem.manifest.name] ?? packageItem.manifest.version]))
}

function artifactFor(packageItem) {
  return {
    name: packageItem.manifest.name,
    version: packageItem.manifest.version,
    filename: join('/artifacts', `${packageItem.manifest.name}-${packageItem.manifest.version}.tgz`),
    integrity: `sha512-${packageItem.manifest.name}-${packageItem.manifest.version}`,
  }
}

describe('selective, channel-safe official release', () => {
  it('validates every real package without requiring one shared version', async () => {
    const packages = await readReleasePackages(root)
    const plan = createReleasePlan(packages)
    expect(plan.composition).toHaveLength(18)
    expect(plan.distTag).toBe('latest')
    expect(plan.composition.at(-1).manifest.name).toBe('dsh-mnemon')
    for (const { directory, manifest } of packages.filter(packageItem => packageItem.manifest.name.startsWith('dsh-mnemon-provider-'))) {
      const source = await readFile(join(directory, 'src/index.ts'), 'utf8')
      expect(source).toContain(`version: '${manifest.version}'`)
    }
  })

  it('selects one changed plugin plus the aggregate Starter and reuses unchanged versions', () => {
    const packages = fixture()
    const baseVersions = previousVersions(packages, {
      'dsh-mnemon': '0.5.1',
      'dsh-mnemon-provider-example': '0.5.0',
    })
    const plan = createReleasePlan(packages, { baseVersions })
    expect(plan.composition.map(packageItem => `${packageItem.manifest.name}@${packageItem.manifest.version}`)).toEqual([
      'dsh-mnemon-provider-example@0.5.1',
      'dsh-mnemon-source-memory-spaces@0.5.0',
      'dsh-mnemon@0.5.2',
    ])
    expect(plan.releasePackages.map(packageItem => packageItem.manifest.name)).toEqual([
      'dsh-mnemon-provider-example',
      'dsh-mnemon',
    ])
  })

  it('requires the Starter snapshot to advance with a plugin and rejects incompatible internal ranges', () => {
    const packages = fixture({ starterVersion: '0.5.1' })
    const baseVersions = previousVersions(packages, { 'dsh-mnemon-provider-example': '0.5.0' })
    expect(() => createReleasePlan(packages, { baseVersions })).toThrow('Starter version must advance')

    const exactPeer = fixture()
    exactPeer[1].manifest.peerDependencies['dsh-mnemon'] = '0.5.2'
    expect(() => createReleasePlan(exactPeer)).toThrow('must declare a compatible range')

    const stalePeer = fixture()
    stalePeer[2].manifest.peerDependencies['dsh-mnemon-source-memory-spaces'] = '^0.4.0'
    expect(() => createReleasePlan(stalePeer)).toThrow('does not accept')

    const stalePin = fixture()
    stalePin[0].manifest.dependencies['dsh-mnemon-provider-example'] = '0.5.0'
    expect(() => createReleasePlan(stalePin)).toThrow('pin the tested plugin version')
  })

  it.each([
    ['0.5.2', 'latest', false],
    ['0.5.2-beta.1', 'beta', true],
    ['0.5.2-rc.1', 'rc', true],
  ])('selects the explicit Starter channel for %s', (version, channel, prerelease) => {
    const packages = fixture({ starterVersion: version, providerVersion: version })
    expect(createReleasePlan(packages, { tag: `v${version}`, prerelease }).distTag).toBe(channel)
  })

  it('rejects tag, prerelease, package channel and version regressions before packing', () => {
    expect(() => createReleasePlan(fixture(), { tag: 'v0.4.0' })).toThrow('Release tag')
    expect(() => createReleasePlan(fixture({ starterVersion: '0.5.2-rc.1', providerVersion: '0.5.2-rc.1' }), { prerelease: false })).toThrow('prerelease flag')
    const wrongChannel = fixture()
    wrongChannel[2].manifest.publishConfig.tag = 'beta'
    expect(() => createReleasePlan(wrongChannel)).toThrow('publishConfig.tag')
    const packages = fixture()
    expect(() => createReleasePlan(packages, { baseVersions: previousVersions(packages, { 'dsh-mnemon': '0.5.3' }) })).toThrow('version must advance')
  })

  it('orders changed dependencies before dependents while leaving unrelated plugins in one layer', () => {
    const packages = fixture({ sourceVersion: '0.5.1', secondProviderVersion: '0.5.1' })
    const plan = createReleasePlan(packages, { baseVersions: previousVersions(packages, {
      'dsh-mnemon': '0.5.1',
      'dsh-mnemon-source-memory-spaces': '0.5.0',
      'dsh-mnemon-provider-example': '0.5.0',
      'dsh-mnemon-provider-second': '0.5.0',
    }) })
    expect(plan.pluginLayers.map(layer => layer.map(packageItem => packageItem.manifest.name))).toEqual([
      ['dsh-mnemon-source-memory-spaces'],
      ['dsh-mnemon-provider-example', 'dsh-mnemon-provider-second'],
    ])
    expect(plan.releasePackages.at(-1).manifest.name).toBe('dsh-mnemon')
  })

  it('requires every changed publication input to have an advanced package version', () => {
    const packages = fixture()
    const plan = createReleasePlan(packages, { baseVersions: previousVersions(packages, {
      'dsh-mnemon': '0.5.1',
      'dsh-mnemon-provider-example': '0.5.0',
    }) })
    expect(publicationInputsChanged(plan, [
      'plugins/dsh-mnemon-provider-example/src/index.ts',
      'plugins/dsh-mnemon-source-memory-spaces/tests/source.spec.ts',
      '.github/workflows/ci.yml',
      'src/index.ts',
    ])).toEqual(new Set(['dsh-mnemon-provider-example', 'dsh-mnemon']))
    expect(publicationInputsChanged(plan, ['bin/repair-legacy-session.mjs'])).toEqual(new Set(['dsh-mnemon']))
    expect(() => assertVersionedPublicationChanges(plan, ['plugins/dsh-mnemon-source-memory-spaces/src/source.ts']))
      .toThrow('dsh-mnemon-source-memory-spaces')
    expect(() => assertVersionedPublicationChanges(plan, ['plugins/dsh-mnemon-source-memory-spaces/tests/source.spec.ts']))
      .not.toThrow()
    expect(() => assertVersionedPublicationChanges(plan, ['plugins/dsh-mnemon-source-memory-spaces/package.json'], {
      ignoredPackageJson: new Set(['dsh-mnemon-source-memory-spaces']),
    })).not.toThrow()
  })

  it('includes deleted files when discovering changed publication inputs', async () => {
    const run = vi.fn(async args => args[0] === 'diff' ? 'src/removed.ts\n' : '')
    await expect(readChangedPaths(baseRevision, revision, run)).resolves.toEqual(['src/removed.ts'])
    expect(run.mock.calls.at(-1)[0]).toEqual([
      'diff', '--name-only', '--relative', '--diff-filter=ACMRD', baseRevision, revision, '--',
    ])
  })

  it('requires release intent and a Starter version bump for locale-only changes', () => {
    const packages = fixture()
    const plan = createReleasePlan(packages, { baseVersions: previousVersions(packages) })
    const paths = ['locale/zh.json']
    const changed = publicationInputsChanged(plan, paths)
    expect(changed).toEqual(new Set(['dsh-mnemon']))
    expect(() => assertReleaseIntentCoverage(changed, [])).toThrow('dsh-mnemon')
    expect(() => assertVersionedPublicationChanges(plan, paths)).toThrow('dsh-mnemon')
    const release = createReleasePlan(packages, { baseVersions: previousVersions(packages, { 'dsh-mnemon': '0.5.1' }) })
    expect(() => assertVersionedPublicationChanges(release, paths)).not.toThrow()
  })

  it('requires a changeset for every package with changed publication inputs', () => {
    const changed = new Set(['dsh-mnemon', 'dsh-mnemon-provider-example'])
    expect(() => assertReleaseIntentCoverage(changed, [{ name: 'dsh-mnemon', type: 'patch' }]))
      .toThrow('dsh-mnemon-provider-example')
    expect(() => assertReleaseIntentCoverage(changed, [
      { name: 'dsh-mnemon', type: 'patch' },
      { name: 'dsh-mnemon-provider-example', type: 'patch' },
    ])).not.toThrow()
  })

  it('packs only selected versions and freezes the full mixed-version composition', async () => {
    const packages = fixture()
    const plan = createReleasePlan(packages, { baseVersions: previousVersions(packages, {
      'dsh-mnemon': '0.5.1',
      'dsh-mnemon-provider-example': '0.5.0',
    }) })
    const destination = await mkdtemp(join(tmpdir(), 'mnemon-release-test-'))
    const calls = []
    const run = vi.fn(async (args, cwd) => {
      calls.push([args, cwd])
      const packageItem = plan.releasePackages.find(candidate => candidate.directory === cwd)
      const filename = `${packageItem.manifest.name}-${packageItem.manifest.version}.tgz`
      await writeFile(join(destination, filename), `${packageItem.manifest.name}@${packageItem.manifest.version}`)
      return JSON.stringify([{
        name: packageItem.manifest.name,
        version: packageItem.manifest.version,
        filename,
        files: [{ path: 'LICENSE' }],
      }])
    })
    try {
      const artifacts = await packRelease(plan, destination, run)
      expect(calls.map(([, cwd]) => cwd)).toEqual(['/provider', '/starter'])
      expect(artifacts.map(artifact => `${artifact.name}@${artifact.version}`)).toEqual([
        'dsh-mnemon-provider-example@0.5.1',
        'dsh-mnemon@0.5.2',
      ])
      const manifest = await writeReleaseManifest(plan, artifacts, destination, { revision, baseRevision })
      expect(manifest.schemaVersion).toBe(2)
      expect(manifest.composition).toEqual([
        { name: 'dsh-mnemon-provider-example', version: '0.5.1' },
        { name: 'dsh-mnemon-source-memory-spaces', version: '0.5.0' },
        { name: 'dsh-mnemon', version: '0.5.2' },
      ])
      await expect(readReleaseArtifacts(plan, destination, { revision, baseRevision })).resolves.toHaveLength(2)
    } finally {
      await rm(destination, { recursive: true, force: true })
    }
  })

  it('submits independent plugins concurrently, waits for their cohort and never includes the Starter', async () => {
    const packages = fixture({ secondProviderVersion: '0.5.1' })
    const plan = createReleasePlan(packages, { baseVersions: previousVersions(packages, {
      'dsh-mnemon': '0.5.1',
      'dsh-mnemon-provider-example': '0.5.0',
      'dsh-mnemon-provider-second': '0.5.0',
    }) })
    const artifacts = plan.releasePackages.map(artifactFor)
    const published = new Set()
    let active = 0
    let maximumActive = 0
    const run = vi.fn(async args => {
      if (args[0] !== 'publish') return ''
      active += 1
      maximumActive = Math.max(maximumActive, active)
      await new Promise(resolve => setTimeout(resolve, 5))
      const artifact = artifacts.find(candidate => candidate.filename === args[1])
      published.add(artifact.name)
      active -= 1
      return ''
    })
    const inspect = vi.fn(async (name, version) => published.has(name)
      ? { version, integrity: artifacts.find(artifact => artifact.name === name).integrity }
      : null)
    const log = vi.spyOn(console, 'log').mockImplementation(() => {})
    try {
      await publishRelease(plan, artifacts, run, { scope: 'plugins', inspect, provenance: false, concurrency: 2 })
    } finally {
      log.mockRestore()
    }
    expect(maximumActive).toBe(2)
    expect(published).toEqual(new Set(['dsh-mnemon-provider-example', 'dsh-mnemon-provider-second']))
    expect(run.mock.calls.flatMap(([args]) => args).join(' ')).not.toContain('/artifacts/dsh-mnemon-0.5.2.tgz')
  })

  it('resumes only when an existing selected artifact matches the frozen bytes', async () => {
    const packages = fixture()
    const plan = createReleasePlan(packages, { baseVersions: previousVersions(packages, {
      'dsh-mnemon': '0.5.1',
      'dsh-mnemon-provider-example': '0.5.0',
    }) })
    const artifacts = plan.releasePackages.map(artifactFor)
    const run = vi.fn(async () => '')
    const inspect = vi.fn(async (name, version) => ({ version, integrity: artifacts.find(artifact => artifact.name === name).integrity }))
    const log = vi.spyOn(console, 'log').mockImplementation(() => {})
    try { await publishRelease(plan, artifacts, run, { inspect, provenance: false }) } finally { log.mockRestore() }
    expect(run).not.toHaveBeenCalled()
    await expect(publishRelease(plan, artifacts, run, {
      inspect: async (name, version) => ({ name, version, integrity: 'sha512-different' }),
      provenance: false,
    })).rejects.toThrow('different Registry artifact')
  })

  it('allows Registry metadata to become readable after the old twelve-attempt window', async () => {
    const expected = { name: 'dsh-mnemon-provider-example', version: '0.5.1', integrity: 'sha512-example' }
    const inspect = vi.fn(async () => inspect.mock.calls.length < 14 ? null : {
      version: expected.version,
      integrity: expected.integrity,
    })
    await expect(waitForRegistryArtifact(expected, 'https://registry.example.test', vi.fn(), inspect, 30, 0)).resolves.toEqual({
      version: expected.version,
      integrity: expected.integrity,
    })
    expect(inspect).toHaveBeenCalledTimes(14)
  })

  it('installs the frozen Starter and verifies every mixed-version Registry package', async () => {
    const packages = fixture()
    const plan = createReleasePlan(packages, { baseVersions: previousVersions(packages, {
      'dsh-mnemon': '0.5.1',
      'dsh-mnemon-provider-example': '0.5.0',
    }) })
    const artifacts = plan.releasePackages.map(artifactFor)
    const run = vi.fn(async (args, directory) => {
      expect(args[0]).toBe('install')
      for (const packageItem of plan.composition) {
        const packageDirectory = join(directory, 'node_modules', packageItem.manifest.name)
        await mkdir(packageDirectory, { recursive: true })
        await writeFile(join(packageDirectory, 'package.json'), JSON.stringify({
          name: packageItem.manifest.name,
          version: packageItem.manifest.version,
        }))
      }
      return ''
    })
    const log = vi.spyOn(console, 'log').mockImplementation(() => {})
    try {
      await verifyRegistryRelease(plan, artifacts, run, {
        scope: 'plugins',
        inspect: async (name, version) => ({
          version,
          integrity: artifacts.find(artifact => artifact.name === name)?.integrity ?? `registry-${name}`,
        }),
      })
    } finally {
      log.mockRestore()
    }
    expect(run).toHaveBeenCalledOnce()
    expect(run.mock.calls[0][0]).toContain(`file:${artifacts.find(artifact => artifact.name === 'dsh-mnemon').filename}`)
  })

  it('keeps the workflow frozen, selective, dependency-ordered and Starter-last', async () => {
    const workflow = await readFile(join(root, '.github/workflows/publish.yml'), 'utf8')
    expect(workflow).toContain('RELEASE_BASE_REVISION')
    expect(workflow).toContain('node scripts/release.mjs --publish-plugins')
    expect(workflow).toContain('node scripts/release.mjs --verify-plugins')
    expect(workflow).toContain('node scripts/release.mjs --publish-starter')
    expect(workflow.indexOf('--publish-plugins')).toBeLessThan(workflow.indexOf('--publish-starter'))
    expect(workflow.indexOf('--publish-starter')).toBeLessThan(workflow.indexOf('gh release create'))
    expect(workflow).not.toContain('seventeen-artifact')
    expect(workflow).not.toContain('sixteen plugins serially')
  })
})

describe('release metadata synchronization', () => {
  it('updates exact aggregate/fixture pins and generated provider versions without narrowing workspace ranges', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'mnemon-release-sync-'))
    const providerName = 'dsh-mnemon-provider-example'
    const providerDirectory = join(directory, 'plugins', providerName)
    try {
      await mkdir(join(providerDirectory, 'src'), { recursive: true })
      await mkdir(join(directory, 'scripts/fixtures/plugin-consumer'), { recursive: true })
      await writeFile(join(directory, 'package.json'), JSON.stringify({
        name: 'dsh-mnemon', version: '0.5.3', dependencies: { [providerName]: '0.5.1' }, publishConfig: { access: 'public', tag: 'latest' },
      }, null, 2) + '\n')
      await writeFile(join(providerDirectory, 'package.json'), JSON.stringify({
        name: providerName,
        version: '0.5.2',
        peerDependencies: { 'dsh-mnemon': '^0.5.0' },
        devDependencies: { 'dsh-mnemon': '^0.5.0' },
        publishConfig: { access: 'public', tag: 'latest' },
      }, null, 2) + '\n')
      await writeFile(join(providerDirectory, 'src/index.ts'), `export const value = { packageName: '${providerName}', version: '0.5.1' }\n`)
      await writeFile(join(directory, 'scripts/fixtures/plugin-consumer/package.json'), JSON.stringify({
        name: 'consumer', private: true, dependencies: { 'dsh-mnemon': '0.5.1', [providerName]: '0.5.1' },
      }, null, 2) + '\n')

      await syncReleaseMetadata(directory)

      const starter = JSON.parse(await readFile(join(directory, 'package.json'), 'utf8'))
      const provider = JSON.parse(await readFile(join(providerDirectory, 'package.json'), 'utf8'))
      const consumer = JSON.parse(await readFile(join(directory, 'scripts/fixtures/plugin-consumer/package.json'), 'utf8'))
      expect(starter.dependencies[providerName]).toBe('0.5.2')
      expect(provider.peerDependencies['dsh-mnemon']).toBe('^0.5.0')
      expect(provider.devDependencies['dsh-mnemon']).toBe('^0.5.0')
      expect(await readFile(join(providerDirectory, 'src/index.ts'), 'utf8')).toContain("version: '0.5.2'")
      expect(consumer.dependencies).toEqual({ 'dsh-mnemon': '0.5.3', [providerName]: '0.5.2' })
    } finally {
      await rm(directory, { recursive: true, force: true })
    }
  })
})
