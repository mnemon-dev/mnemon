import assert from 'node:assert/strict'
import { execFile } from 'node:child_process'
import { createRequire } from 'node:module'
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import {
  assertVersionedPublicationChanges,
  createReleasePlan,
  devOnlyManifestChanges,
  publicationInputsChanged,
  readChangedPaths,
  readReleasePackages,
  readReleaseVersionsAtRevision,
} from './release.mjs'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const execute = promisify(execFile)
const require = createRequire(import.meta.url)
const changesetBin = require.resolve('@changesets/cli/bin.js')

async function git(args) {
  const { stdout } = await execute('git', args, { cwd: root, timeout: 60_000, maxBuffer: 16 * 1024 * 1024 })
  return stdout.trim()
}

export function assertReleaseIntentCoverage(changedPackages, releases) {
  const covered = new Set(releases.map(release => release.name))
  const missing = [...changedPackages].filter(name => !covered.has(name)).sort()
  assert.equal(missing.length, 0, `Published inputs changed without a changeset: ${missing.join(', ')}`)
  return covered
}

/**
 * A package absent at the base revision is introduced by this change. It needs
 * a release intent like any changed package, but it first publishes with the
 * next release pull request, so it does not make this change a release.
 */
export function introducedPackageBaseVersions(packages, baseVersions) {
  const versions = new Map(baseVersions)
  for (const item of packages) if (!versions.has(item.manifest.name)) versions.set(item.manifest.name, item.manifest.version)
  return versions
}

export function assertVersionedReleaseIntent(plan, paths, options, pendingChangesets) {
  assert(plan.selectionComputed && plan.releasePackages.length > 0, 'Expected a versioned release plan')
  assert.equal(pendingChangesets.length, 0, `Release pull request still contains pending changesets: ${pendingChangesets.join(', ')}`)
  assertVersionedPublicationChanges(plan, paths, options)
  return new Set(plan.releasePackages.map(item => item.manifest.name))
}

async function readChangesetStatusSince(baseRevision) {
  const directory = await mkdtemp(join(tmpdir(), 'mnemon-changeset-status-'))
  const output = join(directory, 'status.json')
  try {
    await execute(process.execPath, [changesetBin, 'status', '--since', baseRevision, '--output', output], {
      cwd: root,
      timeout: 60_000,
      maxBuffer: 16 * 1024 * 1024,
    })
    return JSON.parse(await readFile(output, 'utf8'))
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
}

async function readPendingChangesets() {
  return (await readdir(join(root, '.changeset')))
    .filter(name => name.endsWith('.md') && name !== 'README.md')
    .sort()
}

async function main() {
  const revision = process.env.CHANGESET_REVISION?.trim() || await git(['rev-parse', 'HEAD'])
  const baseRevision = process.env.CHANGESET_BASE_REVISION?.trim() || await git(['merge-base', revision, 'origin/master'])
  for (const value of [baseRevision, revision]) assert.match(value, /^[0-9a-f]{40}$/u, 'Changeset validation requires full Git revisions')

  const packages = await readReleasePackages()
  const versionsAtBase = await readReleaseVersionsAtRevision(packages, baseRevision)
  // The change that brings the whole workspace into a repository relocates
  // published packages; it does not publish anything new. Release intent starts
  // with the first change made inside the workspace.
  if (!versionsAtBase.has('dsh-mnemon')) {
    console.log('The workspace is introduced by this change; release intent applies from the next change.')
    return
  }
  const baseVersions = introducedPackageBaseVersions(packages, versionsAtBase)
  const plan = createReleasePlan(packages, { baseVersions })
  const paths = await readChangedPaths(baseRevision, revision)
  const ignoredPackageJson = await devOnlyManifestChanges(plan, paths, baseRevision, revision)
  const changedPackages = publicationInputsChanged(plan, paths, { ignoredPackageJson })
  if (plan.releasePackages.length > 0) {
    const covered = assertVersionedReleaseIntent(plan, paths, { ignoredPackageJson }, await readPendingChangesets())
    console.log(`Verified consumed changesets and version coverage for release packages: ${[...covered].sort().join(', ')}.`)
    return
  }
  if (changedPackages.size === 0) {
    console.log('No published package inputs changed; no release intent is required.')
    return
  }
  const status = await readChangesetStatusSince(baseRevision)
  assertReleaseIntentCoverage(changedPackages, status.releases)

  console.log(`Verified changeset coverage for ${[...changedPackages].sort().join(', ')}.`)
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main()
