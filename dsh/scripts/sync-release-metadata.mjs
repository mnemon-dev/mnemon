import assert from 'node:assert/strict'
import { readFile, readdir, writeFile } from 'node:fs/promises'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repositoryRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')

function releaseTag(version) {
  const match = /^(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)(?:-(alpha|beta|rc)\.(?:0|[1-9]\d*))?$/u.exec(version)
  assert(match, `Unsupported release version: ${version}`)
  return match[1] ?? 'latest'
}

async function readJson(path) {
  return JSON.parse(await readFile(path, 'utf8'))
}

async function writeJsonIfChanged(path, value) {
  const next = JSON.stringify(value, null, 2) + '\n'
  if (await readFile(path, 'utf8') === next) return false
  await writeFile(path, next)
  return true
}

export async function syncReleaseMetadata(root = repositoryRoot) {
  const pluginNames = (await readdir(join(root, 'plugins'), { withFileTypes: true }))
    .filter(entry => entry.isDirectory() && entry.name.startsWith('dsh-mnemon-'))
    .map(entry => entry.name)
    .sort()
  const packageDirectories = [root, ...pluginNames.map(name => join(root, 'plugins', name))]
  const packages = await Promise.all(packageDirectories.map(async directory => ({
    directory,
    path: join(directory, 'package.json'),
    manifest: await readJson(join(directory, 'package.json')),
  })))
  const packagesByName = new Map(packages.map(item => [item.manifest.name, item]))
  assert.equal(packagesByName.size, packages.length, 'Package names must be unique')
  const starter = packagesByName.get('dsh-mnemon')
  assert(starter, 'Missing dsh-mnemon Starter package')

  const changed = []
  for (const item of packages) {
    const { manifest } = item
    assert.notEqual(manifest.private, true, `${manifest.name}: official release package cannot be private`)
    manifest.publishConfig ??= {}
    manifest.publishConfig.access = 'public'
    manifest.publishConfig.tag = releaseTag(manifest.version)
    if (item !== starter) {
      const field = Object.hasOwn(starter.manifest.dependencies ?? {}, manifest.name) ? 'dependencies' : 'devDependencies'
      assert(Object.hasOwn(starter.manifest[field] ?? {}, manifest.name), `${manifest.name}: Starter does not declare the official plugin`)
      starter.manifest[field][manifest.name] = manifest.version
    }
  }

  for (const item of packages) {
    if (await writeJsonIfChanged(item.path, item.manifest)) changed.push(item.path)
    if (!item.manifest.name.startsWith('dsh-mnemon-provider-')) continue
    const sourcePath = join(item.directory, 'src/index.ts')
    const source = await readFile(sourcePath, 'utf8')
    const expression = new RegExp(`(packageName: '${item.manifest.name.replaceAll('-', '\\-')}', version: ')[^']+(')`)
    assert(expression.test(source), `${item.manifest.name}: contribution version declaration is missing`)
    const next = source.replace(expression, `$1${item.manifest.version}$2`)
    if (next !== source) {
      await writeFile(sourcePath, next)
      changed.push(sourcePath)
    }
  }

  const consumerPath = join(root, 'scripts/fixtures/plugin-consumer/package.json')
  const consumer = await readJson(consumerPath)
  for (const field of ['dependencies', 'devDependencies', 'peerDependencies']) {
    for (const [name] of Object.entries(consumer[field] ?? {})) {
      const dependency = packagesByName.get(name)
      if (dependency) consumer[field][name] = dependency.manifest.version
    }
  }
  if (await writeJsonIfChanged(consumerPath, consumer)) changed.push(consumerPath)

  console.log(changed.length === 0
    ? 'Release metadata already matches package versions.'
    : `Synchronized release metadata in ${changed.length} file(s).`)
  return changed
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await syncReleaseMetadata()
