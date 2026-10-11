import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, realpathSync, renameSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { resolveMemorySpacesConfig } from '../src/config.ts'
import { createRunner } from '../src/runner.ts'
import type { ProcessRunner } from '../src/providers/process.ts'

const temporary: string[] = []
afterEach(() => {
  vi.unstubAllEnvs()
  for (const path of temporary.splice(0)) rmSync(path, { recursive: true, force: true })
})

function npmFixture(nested = false) {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'mnemon npm console ')))
  temporary.push(root)
  const name = '@mnemon-dev/mnemon'
  const target = `${process.platform}-${process.arch}`
  const alias = `${name}-${target}`
  const packageRoot = join(root, 'node_modules', name)
  const nativeRoot = join(nested ? join(packageRoot, 'node_modules') : join(root, 'node_modules'), alias)
  const binary = join(nativeRoot, 'bin', process.platform === 'win32' ? 'mnemon.exe' : 'mnemon')
  const launcher = join(packageRoot, 'bin', 'mnemon.js')
  const shim = join(root, 'mnemon.cmd')
  const metadata = { name, version: '0.2.9', type: 'module', optionalDependencies: { [alias]: `npm:${name}@0.2.9-${target}` } }
  mkdirSync(join(packageRoot, 'bin'), { recursive: true })
  mkdirSync(join(nativeRoot, 'bin'), { recursive: true })
  writeFileSync(join(packageRoot, 'package.json'), JSON.stringify(metadata))
  writeFileSync(join(nativeRoot, 'package.json'), JSON.stringify({ name, version: `0.2.9-${target}` }))
  // Match the official npm launcher's second-generation spawn. The copied Node
  // executable lets the probe run on real Windows as well as Unix without a shell.
  copyFileSync(process.execPath, binary)
  chmodSync(binary, 0o755)
  writeFileSync(launcher, `import { spawn } from 'node:child_process';
const child = spawn(${JSON.stringify(binary)}, process.argv.slice(2), { stdio: 'inherit' });
child.on('exit', code => { process.exitCode = code ?? 1 });\n`)
  chmodSync(launcher, 0o755)
  writeFileSync(shim, '@"%dp0%\\node_modules\\@mnemon-dev\\mnemon\\bin\\mnemon.js" %*')
  chmodSync(shim, 0o755)
  const probe = join(root, 'probe.cjs')
  writeFileSync(probe, 'console.log(JSON.stringify({ parent: process.ppid, args: process.argv.slice(2), endpoint: process.env.MNEMON_EMBED_ENDPOINT, protocol: process.env.MNEMON_EMBED_PROTOCOL }))')
  return { root, packageRoot, nativeRoot, binary, launcher, shim, metadata, probe }
}

describe('npm native CLI in desktop Hosts', () => {
  it.each([false, true])('runs the native child directly instead of a wrapper that can open a console (nested: %s)', async nested => {
    const f = npmFixture(nested)
    vi.stubEnv('MNEMON_EMBED_PROTOCOL', 'ollama')
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: f.shim, dataDir: f.root,
      embedding: { enabled: true, endpoint: 'http://127.0.0.1:1234/v1', model: 'fixture', apiKey: '', protocol: 'auto' },
    }))
    const result = await runner.runJson([f.probe, 'literal & $argument'], { globalFlags: false })
    expect(result).toEqual({ parent: process.pid, args: ['literal & $argument'], endpoint: 'http://127.0.0.1:1234/v1' })
    expect(process.env.MNEMON_EMBED_PROTOCOL).toBe('ollama')
    expect(runner.command).toBe(f.shim)
    expect(runner.commandFound).toBe(true)
  })

  it('preserves global flags, cancellation, timeouts and saved embedding settings on the direct route', async () => {
    const f = npmFixture()
    const run = vi.fn<ProcessRunner>(async () => ({ stdout: '{}', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: f.launcher, dataDir: f.root, timeoutMs: 9876,
      embedding: { enabled: true, endpoint: 'http://127.0.0.1/v1', model: 'fixture', apiKey: 'synthetic-fixture-key', protocol: 'openai' },
    }), run)
    const signal = new AbortController().signal
    await runner.runJson(['status'], { store: 'fixture-store', signal })
    expect(run).toHaveBeenCalledWith(f.binary, ['--data-dir', f.root, '--store', 'fixture-store', 'status'], expect.objectContaining({
      timeoutMs: 9876, signal, env: expect.objectContaining({ MNEMON_EMBED_ENDPOINT: 'http://127.0.0.1/v1', MNEMON_EMBED_MODEL: 'fixture', MNEMON_EMBED_API_KEY: 'synthetic-fixture-key', MNEMON_EMBED_PROTOCOL: 'openai' }),
    }))
  })

  it('retains npm update semantics instead of invoking the native self-updater', async () => {
    const f = npmFixture()
    const run = vi.fn<ProcessRunner>(async () => ({ stdout: 'updated', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: f.shim, dataDir: f.root }), run)
    await runner.runText(['update'], { globalFlags: false })
    expect(run).toHaveBeenCalledWith(process.execPath, [f.launcher, 'update'], expect.objectContaining({ env: expect.objectContaining({ ELECTRON_RUN_AS_NODE: '1' }) }))
  })

  it('resolves a replaced native package symlink without restarting the Host', async () => {
    const f = npmFixture(true)
    const previous = `${f.nativeRoot}-previous`
    const replacement = `${f.nativeRoot}-replacement`
    renameSync(f.nativeRoot, previous)
    symlinkSync(previous, f.nativeRoot, 'junction')
    const run = vi.fn<ProcessRunner>(async command => ({ stdout: realpathSync(command), stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: f.shim, dataDir: f.root }), run)
    const filename = process.platform === 'win32' ? 'mnemon.exe' : 'mnemon'
    expect(await runner.runText(['--version'], { globalFlags: false })).toBe(join(previous, 'bin', filename))
    mkdirSync(join(replacement, 'bin'), { recursive: true })
    writeFileSync(join(replacement, 'package.json'), JSON.stringify({ name: '@mnemon-dev/mnemon', version: `0.2.10-${process.platform}-${process.arch}` }))
    copyFileSync(join(previous, 'bin', process.platform === 'win32' ? 'mnemon.exe' : 'mnemon'), join(replacement, 'bin', process.platform === 'win32' ? 'mnemon.exe' : 'mnemon'))
    writeFileSync(join(f.packageRoot, 'package.json'), JSON.stringify({ ...f.metadata, version: '0.2.10', optionalDependencies: { [`@mnemon-dev/mnemon-${process.platform}-${process.arch}`]: `npm:@mnemon-dev/mnemon@0.2.10-${process.platform}-${process.arch}` } }))
    rmSync(f.nativeRoot)
    symlinkSync(replacement, f.nativeRoot, 'junction')
    expect(await runner.runText(['--version'], { globalFlags: false })).toBe(join(replacement, 'bin', filename))
  })

  it.each(['missing', 'mismatched-version', 'unrelated-package', 'unrecognized-dependency'])(
    'keeps the verified launcher fallback for %s native installations', async reason => {
      const f = npmFixture()
      if (reason === 'missing') rmSync(f.binary)
      else if (reason === 'unrecognized-dependency') writeFileSync(join(f.packageRoot, 'package.json'), JSON.stringify({ ...f.metadata, optionalDependencies: {} }))
      else writeFileSync(join(f.nativeRoot, 'package.json'), JSON.stringify({ name: reason === 'unrelated-package' ? 'unrelated' : '@mnemon-dev/mnemon', version: reason === 'mismatched-version' ? '0.2.8' : `0.2.9-${process.platform}-${process.arch}` }))
      const run = vi.fn<ProcessRunner>(async () => ({ stdout: 'version', stderr: '', exitCode: 0 }))
      const runner = createRunner(resolveMemorySpacesConfig({ cliPath: f.shim, dataDir: f.root }), run)
      await runner.runText(['--version'], { globalFlags: false })
      expect(run).toHaveBeenCalledWith(process.execPath, [f.launcher, '--version'], expect.objectContaining({ env: expect.objectContaining({ ELECTRON_RUN_AS_NODE: '1' }) }))
    },
  )
})
