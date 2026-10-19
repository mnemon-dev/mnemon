import { execFile } from 'node:child_process'
import { createRequire } from 'node:module'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import { describe, expect, it } from 'vitest'
import { assertReleaseIntentCoverage, assertVersionedReleaseIntent, introducedPackageBaseVersions } from '../scripts/check-release-intent.mjs'
import { createReleasePlan, publicationInputsChanged } from '../scripts/release.mjs'

const execute = promisify(execFile)
const require = createRequire(import.meta.url)
const changesetBin = require.resolve('@changesets/cli/bin.js')
const repositoryRoot = dirname(dirname(fileURLToPath(import.meta.url)))

async function json(path, value) {
  await writeFile(path, JSON.stringify(value, null, 2) + '\n')
}

function published(name, version, extra = {}) {
  return { name, version, publishConfig: { access: 'public', tag: 'latest' }, ...extra }
}

describe('independent package versioning', () => {
  it('bumps one changed plugin and the exact aggregate without republishing compatible peers', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'mnemon-changesets-'))
    const sourceName = 'dsh-mnemon-source-memory-spaces'
    const changedName = 'dsh-mnemon-provider-changed'
    const unchangedName = 'dsh-mnemon-provider-unchanged'
    try {
      await mkdir(join(directory, '.changeset'), { recursive: true })
      await mkdir(join(directory, 'plugins', sourceName), { recursive: true })
      await mkdir(join(directory, 'plugins', changedName), { recursive: true })
      await mkdir(join(directory, 'plugins', unchangedName), { recursive: true })
      await writeFile(join(directory, 'pnpm-workspace.yaml'), 'packages:\n  - .\n  - plugins/*\n')
      await writeFile(join(directory, '.changeset/config.json'), await readFile(join(repositoryRoot, '.changeset/config.json'), 'utf8'))
      await writeFile(join(directory, '.changeset/one-plugin.md'), `---\n"${changedName}": patch\n---\n\nFix one Provider.\n`)
      await json(join(directory, 'package.json'), published('dsh-mnemon', '0.5.1', {
        dependencies: { [sourceName]: '0.5.1', [changedName]: '0.5.1', [unchangedName]: '0.5.1' },
      }))
      await json(join(directory, 'plugins', sourceName, 'package.json'), published(sourceName, '0.5.1', {
        peerDependencies: { 'dsh-mnemon': '^0.5.0' },
        devDependencies: { 'dsh-mnemon': '^0.5.0', [changedName]: '^0.5.0', [unchangedName]: '^0.5.0' },
      }))
      await json(join(directory, 'plugins', changedName, 'package.json'), published(changedName, '0.5.1', {
        peerDependencies: { [sourceName]: '^0.5.0' },
        devDependencies: { [sourceName]: '^0.5.0' },
      }))
      await json(join(directory, 'plugins', unchangedName, 'package.json'), published(unchangedName, '0.5.1', {
        peerDependencies: { [sourceName]: '^0.5.0' },
        devDependencies: { [sourceName]: '^0.5.0' },
      }))

      await execute(process.execPath, [changesetBin, 'version'], { cwd: directory })

      const starter = JSON.parse(await readFile(join(directory, 'package.json'), 'utf8'))
      const source = JSON.parse(await readFile(join(directory, 'plugins', sourceName, 'package.json'), 'utf8'))
      const changed = JSON.parse(await readFile(join(directory, 'plugins', changedName, 'package.json'), 'utf8'))
      const unchanged = JSON.parse(await readFile(join(directory, 'plugins', unchangedName, 'package.json'), 'utf8'))
      expect(starter.version).toBe('0.5.2')
      expect(starter.dependencies).toEqual({ [sourceName]: '0.5.1', [changedName]: '0.5.2', [unchangedName]: '0.5.1' })
      expect(changed.version).toBe('0.5.2')
      expect(source).toMatchObject({
        version: '0.5.1',
        peerDependencies: { 'dsh-mnemon': '^0.5.0' },
        devDependencies: { 'dsh-mnemon': '^0.5.0', [changedName]: '^0.5.0', [unchangedName]: '^0.5.0' },
      })
      expect(unchanged).toMatchObject({
        version: '0.5.1',
        peerDependencies: { [sourceName]: '^0.5.0' },
        devDependencies: { [sourceName]: '^0.5.0' },
      })
    } finally {
      await rm(directory, { recursive: true, force: true })
    }
  })

  it('accepts a release PR that consumed its changesets and rejects incomplete version coverage', () => {
    const pluginName = 'dsh-mnemon-source-example'
    const packages = [
      {
        directory: `/fixture/plugins/${pluginName}`,
        manifest: published(pluginName, '0.5.2', { peerDependencies: { 'dsh-mnemon': '^0.5.1' } }),
      },
      {
        directory: '/fixture',
        manifest: published('dsh-mnemon', '0.5.2', { dependencies: { [pluginName]: '0.5.2' } }),
      },
    ]
    const plan = createReleasePlan(packages, { baseVersions: new Map([
      ['dsh-mnemon', '0.5.1'],
      [pluginName, '0.5.1'],
    ]) })
    const paths = ['package.json', `plugins/${pluginName}/package.json`]

    expect(() => assertVersionedReleaseIntent(plan, paths, { ignoredPackageJson: new Set() }, [])).not.toThrow()
    expect(() => assertVersionedReleaseIntent(plan, paths, { ignoredPackageJson: new Set() }, ['pending.md']))
      .toThrow(/still contains pending changesets/u)

    const incompletePackages = packages.map(item => item.manifest.name === pluginName
      ? { ...item, manifest: { ...item.manifest, version: '0.5.1' } }
      : { ...item, manifest: { ...item.manifest, dependencies: { [pluginName]: '0.5.1' } } })
    const incompletePlan = createReleasePlan(incompletePackages, { baseVersions: new Map([
      ['dsh-mnemon', '0.5.1'],
      [pluginName, '0.5.1'],
    ]) })
    expect(() => assertVersionedReleaseIntent(incompletePlan, paths, { ignoredPackageJson: new Set() }, []))
      .toThrow(/without a version bump: dsh-mnemon-source-example/u)
  })

  it('asks an introduced package for a release intent without turning the change into a release', () => {
    const introduced = 'dsh-mnemon-strategy-example'
    const packages = [
      {
        directory: `/fixture/plugins/${introduced}`,
        manifest: published(introduced, '0.5.0', { peerDependencies: { 'dsh-mnemon': '^0.5.1' } }),
      },
      {
        directory: '/fixture',
        manifest: published('dsh-mnemon', '0.5.1', { dependencies: { [introduced]: '0.5.0' } }),
      },
    ]
    const baseVersions = new Map([['dsh-mnemon', '0.5.1']])
    expect(() => createReleasePlan(packages, { baseVersions })).toThrow(/Starter version must advance/u)

    const plan = createReleasePlan(packages, { baseVersions: introducedPackageBaseVersions(packages, baseVersions) })
    expect(plan.releasePackages).toEqual([])
    const changed = publicationInputsChanged(plan, ['package.json', `plugins/${introduced}/package.json`, `plugins/${introduced}/src/index.ts`])
    expect([...changed].sort()).toEqual(['dsh-mnemon', introduced])
    expect(() => assertReleaseIntentCoverage(changed, [{ name: 'dsh-mnemon', type: 'patch' }])).toThrow(introduced)
    expect(() => assertReleaseIntentCoverage(changed, [{ name: 'dsh-mnemon', type: 'patch' }, { name: introduced, type: 'patch' }])).not.toThrow()
  })
})
