import { createServer } from 'node:http'
import { createReadStream } from 'node:fs'
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { cp, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const args = new Set(process.argv.slice(2))
for (const arg of args) if (!['--skip-build', '--keep'].includes(arg)) throw new Error(`Unknown argument: ${arg}`)
const names = (await readdir(join(root, 'plugins'))).filter(name => name.startsWith('dsh-mnemon-')).sort()
const configuredConcurrency = process.env.MNEMON_PLUGIN_VERIFY_CONCURRENCY ?? '4'
assert.match(configuredConcurrency, /^[1-9]\d*$/, 'MNEMON_PLUGIN_VERIFY_CONCURRENCY must be a positive integer')
const concurrency = Math.min(Number(configuredConcurrency), names.length)
const manifest = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'))
// The newest earlier release that runs on the supported DSH is the realistic
// upgrade base; upgrading to the same version would test nothing.
const upgradeBase = '0.5.15'
assert.notEqual(upgradeBase, manifest.version, 'The upgrade base must be an earlier release than the packed version')
const temporary = await mkdtemp(join(tmpdir(), 'mnemon-plugin-artifacts-'))
assert(!inside(root, temporary), 'The consumer must be outside the development workspace')
const artifacts = new Map()
const artifactManifests = new Map()
let registryUrl = ''
const publishedDocuments = new Map()
function publishedDocument(name) {
  if (!publishedDocuments.has(name)) {
    publishedDocuments.set(name, fetch(`https://registry.npmjs.org/${name}`).then(async response => {
      if (response.status === 404) return undefined
      assert(response.ok, `Unable to read the published ${name} document (${response.status})`)
      return response.json()
    }))
  }
  return publishedDocuments.get(name)
}
// The local registry answers as npm will once this build is published: earlier
// releases stay resolvable (the Headless upgrade re-resolves its installed base
// here), and each packed artifact replaces its own version and takes its dist-tag.
const registry = createServer(async (request, response) => {
  const path = decodeURIComponent(new URL(request.url, 'http://localhost').pathname).slice(1)
  if (path.startsWith('tarballs/')) {
    const artifact = artifacts.get(path.slice('tarballs/'.length))
    if (artifact === undefined) { response.writeHead(404); response.end(); return }
    response.writeHead(200, { 'content-type': 'application/octet-stream' })
    createReadStream(artifact).pipe(response)
  } else if (artifactManifests.has(path)) {
    const value = artifactManifests.get(path)
    let published
    try { published = await publishedDocument(path) } catch (error) { response.writeHead(502); response.end(String(error)); return }
    const versions = { ...published?.versions, [value.version]: { ...value, dist: { ...value.dist, tarball: registryUrl + '/tarballs/' + path } } }
    response.writeHead(200, { 'content-type': 'application/json' })
    response.end(JSON.stringify({ name: path, 'dist-tags': { ...published?.['dist-tags'], [value.publishConfig.tag]: value.version }, versions }))
  } else {
    response.writeHead(307, { location: 'https://registry.npmjs.org' + request.url })
    response.end()
  }
})

function inside(directory, path) {
  const child = relative(directory, path)
  return child !== '..' && !child.startsWith(`..${sep}`) && !isAbsolute(child)
}

async function run(command, arguments_, cwd, label) {
  const started = performance.now()
  const output = await new Promise((fulfill, reject) => {
    const child = spawn(command, arguments_, {
      cwd, stdio: ['ignore', 'pipe', 'pipe'],
      // NODE_PATH must never rescue an undeclared dependency from the workspace.
      env: { ...process.env, NODE_PATH: '', CI: '1', INIT_CWD: cwd },
    })
    let stdout = ''
    let stderr = ''
    const deadline = setTimeout(() => child.kill('SIGTERM'), 180_000)
    child.stdout.on('data', chunk => { stdout += chunk })
    child.stderr.on('data', chunk => { stderr += chunk })
    child.on('error', error => { clearTimeout(deadline); reject(error) })
    child.on('close', (code, signal) => {
      clearTimeout(deadline)
      if (code === 0) fulfill(stdout)
      else reject(new Error(`${label} failed (${code ?? signal}):\n${stdout.slice(-16_000)}\n${stderr.slice(-8_000)}`))
    })
  })
  console.log(`✓ ${label} (${((performance.now() - started) / 1000).toFixed(1)}s)`)
  return output
}

async function parallel(values, concurrency, action) {
  const pending = [...values]
  // Wait for every in-flight check before cleaning or reporting the directory.
  const results = await Promise.allSettled(Array.from({ length: concurrency }, async () => {
    while (pending.length > 0) await action(pending.shift())
  }))
  const failures = results.flatMap(result => result.status === 'rejected' ? [result.reason] : [])
  if (failures.length > 0) throw new AggregateError(failures, failures.map(error => error.message).join('\n'))
}

async function pack(directory, expectedName) {
  const output = await run('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', join(temporary, 'tarballs')], directory, `pack ${expectedName}`)
  const [artifact] = JSON.parse(output)
  const ownManifest = JSON.parse(await readFile(join(directory, 'package.json'), 'utf8'))
  assert.equal(artifact.name, expectedName)
  assert.equal(artifact.version, ownManifest.version)
  assert.match(artifact.integrity, /^sha512-\S+$/u)
  assert(artifact.files.some(file => file.path === 'LICENSE'), `${expectedName} must carry its own license`)
  for (const file of artifact.files) assert(!/^(?:src|tests|node_modules)\//u.test(file.path), `${expectedName} shipped development sources: ${file.path}`)
  return {
    path: join(temporary, 'tarballs', artifact.filename),
    // Unreleased artifacts can share a version with npm. Supplying integrity
    // prevents pnpm from reusing different bytes from its package store.
    manifest: { ...ownManifest, dist: { integrity: artifact.integrity } },
  }
}

async function install(directory, input) {
  // Serve the exact artifacts with their original semver manifests. This
  // exercises peer resolution and Starter dependencies without file overrides.
  await run('npm', ['install', '--registry', registryUrl, '--ignore-scripts', '--no-audit', '--no-fund', '--package-lock=false', '--workspaces=false'], directory, `${input.name}: install tarballs`)
  const code = `
    const fs = require('node:fs'); const path = require('node:path');
    const direct = new Set(${JSON.stringify(Object.keys({ ...input.dependencies, ...input.devDependencies, ...input.peerDependencies }))});
    for (const name of ${JSON.stringify([...artifacts.keys()].filter(name => name !== input.name))}) {
      let resolved;
      try { resolved = require.resolve(name + '/package.json'); }
      catch (error) {
        if (error.code === 'MODULE_NOT_FOUND' && !direct.has(name)) continue;
        throw error;
      }
      const installed = fs.realpathSync(resolved);
      if (!installed.startsWith(process.cwd() + path.sep + 'node_modules' + path.sep)) throw new Error('Workspace dependency leaked: ' + installed);
    }
  `
  await run(process.execPath, ['-e', code], directory, `${input.name}: no workspace links`)
}

async function verifyIndependent(name) {
  const source = join(root, 'plugins', name)
  const directory = join(temporary, 'independent', name)
  await mkdir(directory, { recursive: true })
  for (const file of await readdir(source)) {
    if (['lib', 'node_modules', '.DS_Store'].includes(file)) continue
    await cp(join(source, file), join(directory, file), { recursive: true, dereference: false })
  }
  const ownManifest = JSON.parse(await readFile(join(directory, 'package.json'), 'utf8'))
  await install(directory, ownManifest)
  // npm runs the package's own commands; no workspace runner, shared config,
  // prebuilt lib/, repository aliases, or sibling source packages are present.
  for (const command of ['typecheck', 'test', 'build']) {
    await run('npm', ['run', command], directory, `${name}: standalone ${command}`)
  }
  const installed = await realpath(directory)
  assert(!inside(root, installed))
}

async function verifyConsumer() {
  const consumer = join(temporary, 'consumer')
  await cp(join(root, 'scripts/fixtures/plugin-consumer'), consumer, { recursive: true })
  const consumerManifest = JSON.parse(await readFile(join(consumer, 'package.json'), 'utf8'))
  await install(consumer, consumerManifest)
  await writeFile(join(consumer, 'artifacts.json'), JSON.stringify([...artifacts.keys()]) + '\n')
  for (const command of ['build', 'typecheck', 'test']) await run('npm', ['run', command], consumer, `external consumer: ${command}`)
}

let succeeded = false
try {
  if (!args.has('--skip-build')) {
    await run('pnpm', ['build'], root, 'build default distribution and public SDK')
    await Promise.all([
      parallel(names.filter(name => name.startsWith('dsh-mnemon-source-')), concurrency,
        name => run('pnpm', ['build'], join(root, 'plugins', name), `build ${name}`)),
      run('pnpm', ['build'], join(root, 'plugins/dsh-mnemon-strategy-default-three-tier'), 'build Strategy-owned extension SDK'),
    ])
    await parallel(names.filter(name => !name.startsWith('dsh-mnemon-source-') && name !== 'dsh-mnemon-strategy-default-three-tier'), concurrency,
      name => run('pnpm', ['build'], join(root, 'plugins', name), `build ${name}`))
  }
  await mkdir(join(temporary, 'tarballs'))
  const packages = [
    { directory: root, name: manifest.name },
    ...names.map(name => ({ directory: join(root, 'plugins', name), name })),
  ]
  const packed = new Map()
  await parallel(packages, concurrency, async input => packed.set(input.name, await pack(input.directory, input.name)))
  // Parallel packing completes out of order. Populate the public registry maps
  // in package-name order so leak checks and the consumer fixture stay stable.
  for (const input of packages) {
    const result = packed.get(input.name)
    assert(result, `Missing packed artifact for ${input.name}`)
    artifacts.set(input.name, result.path)
    artifactManifests.set(input.name, result.manifest)
  }
  await new Promise(resolve => registry.listen(0, '127.0.0.1', resolve))
  registryUrl = 'http://127.0.0.1:' + registry.address().port
  const priority = name => name.startsWith('dsh-mnemon-source-') ? 40
    : name.startsWith('dsh-mnemon-provider-') ? 30 : 20
  const checks = [
    ...names.map(name => ({ label: name, priority: priority(name), action: () => verifyIndependent(name) })),
    { label: 'external consumer', priority: 25, action: verifyConsumer },
    { label: 'real DSH Starter upgrade', priority: 10, action: () => run(process.execPath, [
      join(root, 'scripts/verify-headless-profile.mjs'),
      '--package', 'file:' + artifacts.get(manifest.name), '--registry', registryUrl,
      '--upgrade-from', `dsh-mnemon@${upgradeBase}`, '--upgrade-registry', 'https://registry.npmjs.org',
    ], root, 'real DSH: install only the packed Starter and activate its plugins') },
    { label: 'real DSH Strategy composition', priority: 10, action: () => run(process.execPath, [
      join(root, 'scripts/verify-headless-profile.mjs'),
      '--package', 'file:' + artifacts.get(manifest.name), '--registry', registryUrl, '--strategy-extensions', 'true',
    ], root, 'real DSH: activate three optional packed Strategy plugins together') },
  ].sort((left, right) => right.priority - left.priority || left.label.localeCompare(right.label))
  // Longest checks enter the bounded worker pool first. This keeps all npm
  // installs, real DSH checks and the external consumer concurrent without
  // oversubscribing the runner with an unbounded Promise.all.
  await parallel(checks, concurrency, check => check.action())
  console.log(`Verified ${names.length} independent plugin repositories and ${artifacts.size} packed artifacts with ${concurrency} workers, public SDK-only composition and Client tests.`)
  succeeded = true
} finally {
  await new Promise(resolve => registry.close(resolve))
  if (succeeded && !args.has('--keep')) await rm(temporary, { recursive: true, force: true })
  else console.log(`Artifact test directory retained for inspection: ${temporary}`)
}
