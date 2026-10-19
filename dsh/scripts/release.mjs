import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { execFile } from 'node:child_process'
import { mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { isDeepStrictEqual, promisify } from 'node:util'
import semver from 'semver'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const execute = promisify(execFile)
const defaultRegistry = 'https://registry.npmjs.org'
const manifestName = 'release-manifest.json'
const starterName = 'dsh-mnemon'
// The workspace is the dsh/ directory of the mnemon repository. Published
// manifests name that repository and their path inside it.
const repositoryUrl = 'git+https://github.com/mnemon-dev/mnemon.git'
const workspaceDirectory = 'dsh'
const internalName = /^dsh-mnemon-(?:source|strategy|provider)-[a-z0-9-]+$/u
const releaseVersion = /^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-(alpha|beta|rc)\.(?:0|[1-9]\d*))?$/u

function distTagForVersion(version) {
  const match = releaseVersion.exec(version)
  assert(match, `${version}: expected a stable version or an alpha.N / beta.N / rc.N prerelease`)
  return match[1] ?? 'latest'
}

function normalizedRange(range, version) {
  if (!range.startsWith('workspace:')) return range
  const workspaceRange = range.slice('workspace:'.length)
  if (workspaceRange === '*') return version
  if (workspaceRange === '^' || workspaceRange === '~') return `${workspaceRange}${version}`
  return workspaceRange
}

function buildPluginLayers(plugins, packagesByName) {
  const selected = new Map(plugins.map(item => [item.manifest.name, item]))
  const dependencies = new Map(plugins.map(item => [item.manifest.name, new Set()]))
  for (const item of plugins) {
    for (const field of ['dependencies', 'optionalDependencies', 'peerDependencies']) {
      for (const name of Object.keys(item.manifest[field] ?? {})) {
        if (name !== starterName && selected.has(name)) dependencies.get(item.manifest.name).add(name)
      }
    }
  }
  const layers = []
  while (dependencies.size > 0) {
    const ready = [...dependencies].filter(([, values]) => values.size === 0).map(([name]) => name).sort()
    assert(ready.length > 0, `Release dependency cycle: ${[...dependencies.keys()].join(', ')}`)
    layers.push(ready.map(name => packagesByName.get(name)))
    for (const name of ready) dependencies.delete(name)
    for (const values of dependencies.values()) for (const name of ready) values.delete(name)
  }
  return layers
}

/** Official distribution only. External plugin repositories own their releases. */
export function createReleasePlan(packages, { tag, prerelease, baseVersions } = {}) {
  const starter = packages.find(item => item.manifest.name === starterName)
  assert(starter, 'Release must include the default Starter')
  const { version } = starter.manifest
  const distTag = distTagForVersion(version)
  assert.equal(tag ?? `v${version}`, `v${version}`, 'Release tag must match the Starter version')
  assert.equal(prerelease ?? (distTag !== 'latest'), distTag !== 'latest', 'GitHub prerelease flag must match the Starter version')

  const packagesByName = new Map(packages.map(item => [item.manifest.name, item]))
  assert.equal(packagesByName.size, packages.length, 'Duplicate package in release composition')
  const plugins = packages.filter(item => item !== starter).sort((a, b) => a.manifest.name.localeCompare(b.manifest.name, 'en'))
  assert(plugins.length > 0, 'Release must include independently packed plugins')
  const composition = [...plugins, starter]

  for (const item of composition) {
    const { manifest } = item
    const packageTag = distTagForVersion(manifest.version)
    assert.notEqual(manifest.private, true, `${manifest.name}: private package cannot be published`)
    assert.equal(manifest.publishConfig?.access, 'public', `${manifest.name}: public access must be explicit`)
    assert.equal(manifest.publishConfig?.tag, packageTag, `${manifest.name}: publishConfig.tag must match its release channel`)
    if (item !== starter) {
      assert.match(manifest.name, internalName)
      assert.equal(starter.manifest.dependencies?.[manifest.name] ?? starter.manifest.devDependencies?.[manifest.name], manifest.version,
        `${manifest.name}: Starter must pin the tested plugin version (runtime or development)`)
      if (item.directory.startsWith(root + '/')) {
        assert.equal(manifest.repository?.url, repositoryUrl, `${manifest.name}: repository URL must identify the official source`)
        assert.equal(manifest.repository?.directory, `${workspaceDirectory}/${relative(root, item.directory)}`, `${manifest.name}: repository directory must identify the package source`)
      }
    }
    for (const field of ['dependencies', 'optionalDependencies', 'peerDependencies', 'devDependencies']) {
      for (const [name, range] of Object.entries(manifest[field] ?? {})) {
        if (name !== starterName && !name.startsWith('dsh-mnemon-')) continue
        const dependency = packagesByName.get(name)
        assert(dependency, `${manifest.name}: missing release composition package for ${name}`)
        if (item === starter && name !== starterName) {
          assert.equal(range, dependency.manifest.version, `${manifest.name}: ${field}.${name} must exactly pin the tested plugin version`)
          continue
        }
        const checkRange = normalizedRange(range, dependency.manifest.version)
        assert(semver.validRange(checkRange), `${manifest.name}: ${field}.${name} is not a valid semver range`)
        assert(semver.satisfies(dependency.manifest.version, checkRange, { includePrerelease: true }),
          `${manifest.name}: ${field}.${name} does not accept ${dependency.manifest.version}`)
        if (field === 'peerDependencies') {
          assert.notEqual(range, dependency.manifest.version,
            `${manifest.name}: peerDependencies.${name} must declare a compatible range for independent releases`)
        }
      }
    }
  }

  const selectionComputed = baseVersions !== undefined
  const previous = baseVersions instanceof Map ? baseVersions : new Map(Object.entries(baseVersions ?? {}))
  const selected = selectionComputed ? composition.filter(item => {
    const oldVersion = previous.get(item.manifest.name)
    if (oldVersion === undefined) return true
    if (oldVersion === item.manifest.version) return false
    assert(semver.gt(item.manifest.version, oldVersion), `${item.manifest.name}: version must advance from ${oldVersion}`)
    return true
  }) : composition
  const releaseNames = new Set(selected.map(item => item.manifest.name))
  const releasePlugins = plugins.filter(item => releaseNames.has(item.manifest.name))
  if (selectionComputed && releasePlugins.length > 0) {
    assert(releaseNames.has(starterName), 'Starter version must advance whenever the plugin composition changes')
  }
  const pluginLayers = buildPluginLayers(releasePlugins, packagesByName)
  const releasePackages = [...pluginLayers.flat(), ...(releaseNames.has(starterName) ? [starter] : [])]

  return {
    version,
    distTag,
    starter,
    plugins,
    composition,
    releasePlugins,
    releasePackages,
    pluginLayers,
    selectionComputed,
  }
}

export async function readReleasePackages(directory = root) {
  const names = (await readdir(join(directory, 'plugins'), { withFileTypes: true }))
    .filter(entry => entry.isDirectory() && entry.name.startsWith('dsh-mnemon-')).map(entry => entry.name)
  return Promise.all([directory, ...names.map(name => join(directory, 'plugins', name))].map(async packageDirectory => ({
    directory: packageDirectory,
    manifest: JSON.parse(await readFile(join(packageDirectory, 'package.json'), 'utf8')),
  })))
}

async function git(args, cwd = root) {
  const { stdout } = await execute('git', args, { cwd, timeout: 60_000, maxBuffer: 16 * 1024 * 1024 })
  return stdout
}

function missingAtRevision(error) {
  const output = `${error?.stdout ?? ''}\n${error?.stderr ?? ''}`
  return error?.code === 128 && /does not exist|exists on disk, but not in|path .* not in/iu.test(output)
}

export async function readReleaseVersionsAtRevision(packages, revision, run = git) {
  assert.match(revision ?? '', /^[0-9a-f]{40}$/u, 'A full base release revision is required')
  await run(['cat-file', '-e', `${revision}^{commit}`], root)
  const versions = new Map()
  for (const item of packages) {
    const directory = relative(root, item.directory)
    const path = directory === '' ? 'package.json' : `${directory}/package.json`
    try {
      // ./ resolves the path from the workspace, not from the repository root.
      const manifest = JSON.parse(await run(['show', `${revision}:./${path}`], root))
      versions.set(item.manifest.name, manifest.version)
    } catch (error) {
      if (!missingAtRevision(error)) throw error
    }
  }
  return versions
}

export async function readChangedPaths(baseRevision, revision, run = git) {
  for (const value of [baseRevision, revision]) {
    assert.match(value ?? '', /^[0-9a-f]{40}$/u, 'Changed-package validation requires full revisions')
    await run(['cat-file', '-e', `${value}^{commit}`], root)
  }
  // --relative keeps the workspace's own paths (plugins/..., src/...) and
  // leaves out changes elsewhere in the repository.
  const output = await run(['diff', '--name-only', '--relative', '--diff-filter=ACMRD', baseRevision, revision, '--'], root)
  return output.split(/\r?\n/u).filter(Boolean)
}

export async function devOnlyManifestChanges(plan, paths, baseRevision, revision, run = git) {
  const changedPaths = new Set(paths)
  const ignored = new Set()
  for (const item of plan.composition) {
    const directory = item === plan.starter ? '' : `plugins/${item.manifest.name}/`
    const path = `${directory}package.json`
    if (!changedPaths.has(path)) continue
    try {
      const [before, after] = await Promise.all([
        run(['show', `${baseRevision}:./${path}`], root),
        run(['show', `${revision}:./${path}`], root),
      ])
      const beforeManifest = JSON.parse(before)
      const afterManifest = JSON.parse(after)
      delete beforeManifest.devDependencies
      delete afterManifest.devDependencies
      if (isDeepStrictEqual(beforeManifest, afterManifest)) ignored.add(item.manifest.name)
    } catch (error) {
      if (!missingAtRevision(error)) throw error
    }
  }
  return ignored
}

export function publicationInputsChanged(plan, paths, { ignoredPackageJson = new Set() } = {}) {
  const packageNames = new Set(plan.composition.map(item => item.manifest.name))
  const changed = new Set()
  const rootInputs = /^(?:package\.json|cordis\.patch\.yml|README(?:\.zh-CN)?\.md|LICENSE|SECURITY\.md|THIRD_PARTY_NOTICES\.md|tsconfig(?:\.types)?\.json|tsdown\.config\.ts|scripts\/link-bundle-declarations\.mjs|bin\/|src\/|lib\/|locale\/)/u
  for (const path of paths) {
    const plugin = /^plugins\/(dsh-mnemon-(?:source|strategy|provider)-[a-z0-9-]+)\/(.+)$/u.exec(path)
    if (plugin) {
      const [, name, packagePath] = plugin
      if (packageNames.has(name) && !(packagePath === 'package.json' && ignoredPackageJson.has(name))
        && !packagePath.startsWith('tests/') && !/^vitest\.config\./u.test(packagePath)) changed.add(name)
      continue
    }
    if (rootInputs.test(path) && !(path === 'package.json' && ignoredPackageJson.has(starterName))) changed.add(starterName)
  }
  return changed
}

export function assertVersionedPublicationChanges(plan, paths, options) {
  assert(plan.selectionComputed, 'Changed-package validation requires a selected release plan')
  const releaseNames = new Set(plan.releasePackages.map(item => item.manifest.name))
  const missing = [...publicationInputsChanged(plan, paths, options)].filter(name => !releaseNames.has(name)).sort()
  assert.equal(missing.length, 0, `Published inputs changed without a version bump: ${missing.join(', ')}`)
}

async function npm(args, cwd) {
  // Release publication runs on Linux. No shell expansion or implicit scripts.
  const { stdout } = await execute('npm', args, { cwd, timeout: 300_000, maxBuffer: 16 * 1024 * 1024 })
  return stdout
}

async function fileIntegrity(path) {
  const bytes = await readFile(path)
  return `sha512-${createHash('sha512').update(bytes).digest('base64')}`
}

async function assertNoPendingReleaseIntents(directory = root) {
  const pending = (await readdir(join(directory, '.changeset')))
    .filter(name => name.endsWith('.md') && name !== 'README.md')
    .sort()
  assert.equal(pending.length, 0, `Apply pending changesets in a release pull request first: ${pending.join(', ')}`)
}

export async function packRelease(plan, destination, run = npm) {
  assert(plan.selectionComputed, 'Packing requires a release selection computed from a base revision')
  assert(plan.releasePackages.length > 0, 'No package version advanced since the base release')
  await mkdir(destination, { recursive: true })
  const existing = await readdir(destination)
  assert.equal(existing.length, 0, `Release artifact directory must be empty: ${destination}`)
  const artifacts = []
  for (const item of plan.releasePackages) {
    const output = await run(['pack', '--ignore-scripts', '--json', '--pack-destination', destination], item.directory)
    const [artifact] = JSON.parse(output)
    assert.equal(artifact.name, item.manifest.name, 'Unexpected packed package')
    assert.equal(artifact.version, item.manifest.version, `${artifact.name}: unexpected packed version`)
    assert.equal(artifact.filename, basename(artifact.filename), 'Artifact filename must not escape the destination')
    assert(artifact.files.some(file => file.path === 'LICENSE'), `${artifact.name}: packed license is missing`)
    const filename = join(destination, artifact.filename)
    const integrity = await fileIntegrity(filename)
    if (artifact.integrity !== undefined) assert.equal(artifact.integrity, integrity, `${artifact.name}: npm pack integrity mismatch`)
    artifacts.push({ name: artifact.name, version: artifact.version, filename, integrity, size: (await stat(filename)).size })
  }
  return artifacts
}

function compositionManifest(plan) {
  return plan.composition.map(item => ({ name: item.manifest.name, version: item.manifest.version }))
}

export async function writeReleaseManifest(plan, artifacts, destination, {
  revision = process.env.RELEASE_REVISION,
  baseRevision = process.env.RELEASE_BASE_REVISION,
} = {}) {
  assert.deepEqual(artifacts.map(item => [item.name, item.version]),
    plan.releasePackages.map(item => [item.manifest.name, item.manifest.version]), 'Manifest requires the selected ordered artifact set')
  assert.match(revision ?? '', /^[0-9a-f]{40}$/u, 'A full frozen release revision is required')
  assert.match(baseRevision ?? '', /^[0-9a-f]{40}$/u, 'A full base release revision is required')
  const manifest = {
    schemaVersion: 2,
    revision,
    baseRevision,
    starter: { name: starterName, version: plan.version, distTag: plan.distTag },
    composition: compositionManifest(plan),
    packages: artifacts.map(item => ({ ...item, filename: basename(item.filename) })),
  }
  await writeFile(join(destination, manifestName), JSON.stringify(manifest, null, 2) + '\n')
  return manifest
}

export async function readReleaseArtifacts(plan, destination, {
  revision = process.env.RELEASE_REVISION,
  baseRevision = process.env.RELEASE_BASE_REVISION,
} = {}) {
  const manifest = JSON.parse(await readFile(join(destination, manifestName), 'utf8'))
  assert.equal(manifest.schemaVersion, 2, 'Unsupported release manifest')
  assert.deepEqual(manifest.starter, { name: starterName, version: plan.version, distTag: plan.distTag }, 'Artifact Starter metadata does not match source')
  if (revision !== undefined) assert.equal(manifest.revision, revision, 'Artifacts were not built from the frozen revision')
  if (baseRevision !== undefined) assert.equal(manifest.baseRevision, baseRevision, 'Artifacts were not selected from the frozen base revision')
  assert.deepEqual(manifest.composition, compositionManifest(plan), 'Artifact composition does not match source')
  assert.deepEqual(manifest.packages.map(item => [item.name, item.version]),
    plan.releasePackages.map(item => [item.manifest.name, item.manifest.version]), 'Artifact manifest does not contain the selected ordered release')
  const artifacts = []
  for (const item of manifest.packages) {
    assert.equal(item.filename, basename(item.filename), `${item.name}: artifact filename must not escape its directory`)
    const filename = join(destination, item.filename)
    assert.equal(await fileIntegrity(filename), item.integrity, `${item.name}: artifact bytes changed after packing`)
    assert.equal((await stat(filename)).size, item.size, `${item.name}: artifact size changed after packing`)
    artifacts.push({ ...item, filename })
  }
  return artifacts
}

function missingRegistryVersion(error) {
  const output = `${error?.stdout ?? ''}\n${error?.stderr ?? ''}`
  return error?.code === 'E404' || /\bE404\b|404 Not Found|is not in this registry/iu.test(output)
}

export async function inspectRegistryArtifact(name, version, registry = defaultRegistry, run = npm) {
  try {
    const output = await run(['view', `${name}@${version}`, 'version', 'dist.integrity', '--json', '--prefer-online', '--registry', registry], root)
    const value = JSON.parse(output)
    return { version: value.version, integrity: value['dist.integrity'] }
  } catch (error) {
    if (missingRegistryVersion(error)) return null
    throw error
  }
}

export async function waitForRegistryArtifacts(expectedArtifacts, registry, run, inspect, attempts = 90, retryDelayMs = 5_000) {
  assert(expectedArtifacts.length > 0, 'Expected at least one Registry artifact')
  const pending = new Map(expectedArtifacts.map(expected => [expected.name, expected]))
  assert.equal(pending.size, expectedArtifacts.length, 'Registry artifact names must be unique')
  const published = new Map()
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    const inspections = await Promise.all([...pending.values()].map(async expected => ({
      expected,
      actual: await inspect(expected.name, expected.version, registry, run),
    })))
    for (const { expected, actual } of inspections) {
      if (actual === null) continue
      assert.equal(actual.version, expected.version, `${expected.name}: unexpected Registry version`)
      if (expected.integrity !== undefined) assert.equal(actual.integrity, expected.integrity, `${expected.name}: Registry bytes differ from the frozen tarball`)
      pending.delete(expected.name)
      published.set(expected.name, actual)
    }
    if (pending.size === 0) return expectedArtifacts.map(expected => published.get(expected.name))
    if (attempt < attempts) await new Promise(resolveDelay => setTimeout(resolveDelay, retryDelayMs))
  }
  const missing = [...pending.values()].map(expected => `${expected.name}@${expected.version}`).join(', ')
  throw new Error(`${missing} did not become readable from ${registry}`)
}

export async function waitForRegistryArtifact(expected, registry, run, inspect, attempts = 90, retryDelayMs = 5_000) {
  const [published] = await waitForRegistryArtifacts([expected], registry, run, inspect, attempts, retryDelayMs)
  return published
}

async function parallelMap(values, concurrency, action) {
  assert(Number.isSafeInteger(concurrency) && concurrency > 0, 'Publish concurrency must be a positive integer')
  const results = new Array(values.length)
  let next = 0
  await Promise.all(Array.from({ length: Math.min(concurrency, values.length) }, async () => {
    while (next < values.length) {
      const index = next
      next += 1
      results[index] = await action(values[index])
    }
  }))
  return results
}

export async function publishRelease(plan, artifacts, run = npm, {
  scope = 'all',
  registry = defaultRegistry,
  inspect = inspectRegistryArtifact,
  provenance = process.env.GITHUB_ACTIONS === 'true',
  concurrency = 4,
} = {}) {
  assert.deepEqual(artifacts.map(item => [item.name, item.version]),
    plan.releasePackages.map(item => [item.manifest.name, item.manifest.version]), 'Publish requires the selected frozen artifact set')
  assert(['all', 'plugins', 'starter'].includes(scope), 'Unknown publication scope')
  const artifactsByName = new Map(artifacts.map(item => [item.name, item]))
  const pluginGroups = plan.pluginLayers.map(layer => layer.map(item => artifactsByName.get(item.manifest.name)))
  const starterArtifact = artifactsByName.get(starterName)
  const groups = scope === 'plugins' ? pluginGroups : scope === 'starter' ? [[starterArtifact]] : [...pluginGroups, [starterArtifact]]
  for (const group of groups.filter(items => items.length > 0)) {
    assert(group.every(Boolean), `Missing frozen artifact for ${scope} publication`)
    const submissions = await parallelMap(group, concurrency, async artifact => {
      const published = await inspect(artifact.name, artifact.version, registry, run)
      if (published !== null) {
        assert.equal(published.integrity, artifact.integrity, `${artifact.name}: refusing to reuse a different Registry artifact at the frozen version`)
        console.log(`Verified existing ${artifact.name}@${artifact.version}`)
        return null
      }
      await run(['publish', artifact.filename, '--access', 'public', '--ignore-scripts', '--tag', distTagForVersion(artifact.version), '--registry', registry,
        ...(provenance ? ['--provenance'] : [])], root)
      console.log(`Submitted ${artifact.name}@${artifact.version}`)
      return artifact
    })
    const awaitingRegistry = submissions.filter(Boolean)
    if (awaitingRegistry.length > 0) {
      await waitForRegistryArtifacts(awaitingRegistry, registry, run, inspect)
      for (const expected of awaitingRegistry) console.log(`Published ${expected.name}@${expected.version}`)
    }
  }
}

async function verifyInstalledPackages(directory, packages) {
  for (const { manifest: { name, version } } of packages) {
    const installed = JSON.parse(await readFile(join(directory, 'node_modules', name, 'package.json'), 'utf8'))
    assert.equal(installed.version, version, `${name}: installed version differs from the frozen composition`)
  }
}

export async function verifyRegistryRelease(plan, artifacts, run = npm, {
  scope = 'all', registry = defaultRegistry, inspect = inspectRegistryArtifact,
} = {}) {
  assert(['all', 'plugins'].includes(scope), 'Unknown Registry verification scope')
  const artifactsByName = new Map(artifacts.map(item => [item.name, item]))
  const registryPackages = scope === 'plugins' ? plan.plugins : plan.composition
  await waitForRegistryArtifacts(registryPackages.map(item => ({
    name: item.manifest.name,
    version: item.manifest.version,
    integrity: artifactsByName.get(item.manifest.name)?.integrity,
  })), registry, run, inspect)

  const directory = await mkdtemp(join(tmpdir(), 'mnemon-registry-release-'))
  try {
    await writeFile(join(directory, 'package.json'), JSON.stringify({ name: 'mnemon-release-verifier', private: true }, null, 2) + '\n')
    const starter = artifactsByName.get(starterName)
    const target = scope === 'plugins' ? `file:${starter.filename}` : `${starterName}@${plan.version}`
    await run(['install', '--save-exact', '--ignore-scripts', '--no-audit', '--no-fund', '--package-lock=false', '--registry', registry, target], directory)
    await verifyInstalledPackages(directory, plan.composition)
    console.log(scope === 'plugins'
      ? `Verified ${plan.plugins.length} Registry plugins through the frozen local Starter composition.`
      : `Verified the published Starter and its ${plan.plugins.length}-plugin Registry composition.`)
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
}

function readArguments(arguments_) {
  const mode = arguments_[0] ?? '--check'
  const modes = ['--check', '--pack', '--publish-plugins', '--verify-plugins', '--publish-starter', '--verify-release']
  assert(modes.includes(mode), `Use ${modes.join(', ')}`)
  const options = new Map()
  for (let index = 1; index < arguments_.length; index += 2) {
    assert(['--artifacts', '--registry'].includes(arguments_[index]) && arguments_[index + 1], 'Expected --artifacts <directory> and/or --registry <url>')
    options.set(arguments_[index], arguments_[index + 1])
  }
  return { mode, options }
}

async function main() {
  const { mode, options } = readArguments(process.argv.slice(2))
  const { RELEASE_TAG, RELEASE_PRERELEASE, RELEASE_REVISION, RELEASE_BASE_REVISION } = process.env
  const freezesRelease = mode !== '--check'
  const computesSelection = freezesRelease || RELEASE_BASE_REVISION !== undefined
  const writesRegistry = mode === '--publish-plugins' || mode === '--publish-starter'
  if (freezesRelease) {
    assert.match(RELEASE_REVISION ?? '', /^[0-9a-f]{40}$/u, 'Release operations require a full frozen revision')
  }
  if (computesSelection) assert.match(RELEASE_BASE_REVISION ?? '', /^[0-9a-f]{40}$/u, 'Release operations require a full base release revision')
  if (writesRegistry) assert(RELEASE_TAG && RELEASE_PRERELEASE, 'Publication requires an explicit tag and prerelease flag')
  if (RELEASE_PRERELEASE !== undefined) assert(['true', 'false'].includes(RELEASE_PRERELEASE), 'RELEASE_PRERELEASE must be true or false')

  const packages = await readReleasePackages()
  const baseVersions = computesSelection ? await readReleaseVersionsAtRevision(packages, RELEASE_BASE_REVISION) : undefined
  const plan = createReleasePlan(packages, {
    tag: RELEASE_TAG,
    prerelease: RELEASE_PRERELEASE === undefined ? undefined : RELEASE_PRERELEASE === 'true',
    baseVersions,
  })
  if (computesSelection) assert(plan.releasePackages.length > 0, 'No package version advanced since the base release')
  if (computesSelection) await assertNoPendingReleaseIntents()
  if (computesSelection && RELEASE_REVISION !== undefined) {
    const changedPaths = await readChangedPaths(RELEASE_BASE_REVISION, RELEASE_REVISION)
    const ignoredPackageJson = await devOnlyManifestChanges(plan, changedPaths, RELEASE_BASE_REVISION, RELEASE_REVISION)
    assertVersionedPublicationChanges(plan, changedPaths, { ignoredPackageJson })
  }
  console.log(JSON.stringify({
    starter: { version: plan.version, distTag: plan.distTag },
    composition: Object.fromEntries(plan.composition.map(item => [item.manifest.name, item.manifest.version])),
    publish: computesSelection ? plan.releasePackages.map(item => `${item.manifest.name}@${item.manifest.version}`) : 'computed from the base release during publication',
    pluginLayers: computesSelection ? plan.pluginLayers.map(layer => layer.map(item => item.manifest.name)) : undefined,
  }, null, 2))
  if (mode === '--check') return

  const destination = resolve(options.get('--artifacts') ?? await mkdtemp(join(tmpdir(), 'mnemon-release-')))
  const registry = options.get('--registry') ?? defaultRegistry
  if (mode === '--pack') {
    const artifacts = await packRelease(plan, destination)
    await writeReleaseManifest(plan, artifacts, destination)
    console.log(`Packed ${artifacts.length} selected frozen artifacts in ${destination}`)
    return
  }
  assert(options.has('--artifacts'), `${mode} requires --artifacts <directory>`)
  const artifacts = await readReleaseArtifacts(plan, destination)
  const concurrency = Number(process.env.RELEASE_PUBLISH_CONCURRENCY ?? 4)
  if (mode === '--publish-plugins') await publishRelease(plan, artifacts, npm, { scope: 'plugins', registry, concurrency })
  if (mode === '--verify-plugins') await verifyRegistryRelease(plan, artifacts, npm, { scope: 'plugins', registry })
  if (mode === '--publish-starter') await publishRelease(plan, artifacts, npm, { scope: 'starter', registry, concurrency })
  if (mode === '--verify-release') await verifyRegistryRelease(plan, artifacts, npm, { scope: 'all', registry })
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main()
