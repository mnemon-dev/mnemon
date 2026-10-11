import { spawnSync } from 'node:child_process'
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const root = fileURLToPath(new URL('../', import.meta.url))
const manifest = JSON.parse(readFileSync(join(root, 'package.json'), 'utf8'))

/** Replay Desktop's real package layout, without substituting any module exports. */
function generation() {
  const directory = mkdtempSync(join(tmpdir(), 'mnemon-generation-import-'))
  const profile = join(directory, 'profiles/web')
  const installed = join(directory, 'profiles/.generations/live/mnemon/node_modules')
  const packageRoot = join(installed, 'dsh-mnemon')
  mkdirSync(packageRoot, { recursive: true })
  cpSync(join(root, 'lib'), join(packageRoot, 'lib'), { recursive: true })
  writeFileSync(join(packageRoot, 'package.json'), JSON.stringify(manifest))

  // The published installer removes @deepseek-ai singletons from generations,
  // so they resolve through the host's packages as they do in Desktop.
  const dependencies = { ...manifest.dependencies, ...manifest.peerDependencies }
  const hostModules = join(directory, 'profiles/node_modules')
  for (const name of Object.keys(dependencies)) {
    const target = join(name.startsWith('@deepseek-ai/') ? hostModules : installed, name)
    mkdirSync(dirname(target), { recursive: true })
    symlinkSync(join(root, 'node_modules', name), target, 'junction')
  }
  mkdirSync(join(profile, 'node_modules'), { recursive: true })
  symlinkSync(packageRoot, join(profile, 'node_modules/dsh-mnemon'), 'junction')
  return { directory, profile }
}

describe('profile generation imports against host-owned framework packages', () => {
  it('loads the complete built Starter with the host Cosmokit and live configuration', () => {
    const fixture = generation()
    try {
      const entry = join(fixture.profile, 'check.mjs')
      writeFileSync(entry, `
        import assert from 'node:assert/strict'
        import * as mnemon from 'dsh-mnemon'
        const config = mnemon.Config({ displayMode: 'builtin', remoteAccess: 'read-only' })
        assert.equal(typeof config.displayMode?.get, 'function')
        assert.equal(config.displayMode.get(), 'builtin')
        assert.equal(config.remoteAccess, 'read-only')
        assert.equal(typeof mnemon.apply, 'function')
        assert.equal(mnemon.name, 'dsh-mnemon')
        console.log(JSON.stringify({ loaded: true }))
      `)
      const result = spawnSync(process.execPath, [entry], { encoding: 'utf8', timeout: 20_000 })
      expect(result.error).toBeUndefined()
      expect(result.status, result.stderr).toBe(0)
      expect(JSON.parse(result.stdout)).toEqual({ loaded: true })
    } finally { rmSync(fixture.directory, { recursive: true, force: true }) }
  })
})
