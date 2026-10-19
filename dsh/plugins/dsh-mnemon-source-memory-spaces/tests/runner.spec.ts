import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, win32 } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { resolveMemorySpacesConfig as resolveConfig } from '../src/config.ts'
import { createRunner } from '../src/runner.ts'
import { ProcessError, type ProcessRunner } from '../src/providers/process.ts'
import { findMnemonCommand, nodeLauncherEnvironment } from '../src/native-cli.ts'

const temporaryDirectories: string[] = []

afterEach(() => {
  vi.unstubAllEnvs()
  for (const directory of temporaryDirectories.splice(0)) rmSync(directory, { recursive: true, force: true })
})

function temporaryDirectory(): string {
  const directory = mkdtempSync(join(tmpdir(), 'dsh-mnemon-runner-'))
  temporaryDirectories.push(directory)
  return directory
}

describe('Mnemon CLI discovery', () => {
  it('sets Node mode once for case-insensitive Windows environments without mutating the input', () => {
    const inherited = { Path: 'C:\\npm global', electron_run_as_node: '0', ELECTRON_RUN_AS_NODE: '0' }
    expect(nodeLauncherEnvironment(inherited)).toEqual({ Path: 'C:\\npm global', ELECTRON_RUN_AS_NODE: '1' })
    expect(inherited).toEqual({ Path: 'C:\\npm global', electron_run_as_node: '0', ELECTRON_RUN_AS_NODE: '0' })
  })

  it.each([
    { platform: 'darwin' as const, command: 'mnemon', env: { PATH: '/tools/bin' }, expected: '/tools/bin/mnemon' },
    { platform: 'linux' as const, command: 'mnemon-custom', env: { PATH: '/tools/bin' }, expected: '/tools/bin/mnemon-custom' },
    { platform: 'win32' as const, command: 'mnemon', env: { Path: 'C:\\tools' }, expected: 'C:\\tools\\mnemon.exe' },
    { platform: 'win32' as const, command: 'mnemon.exe', env: { Path: 'C:\\tools' }, expected: 'C:\\tools\\mnemon.exe' },
  ])('resolves the configured command name through PATH on $platform ($command)', ({ platform, command, env, expected }) => {
    expect(findMnemonCommand({ cliPath: command }, {
      platform, env, home: '/unused', isExecutable: path => path === expected,
    })).toBe(expected)
  })

  it('does not substitute a different binary for an unavailable configured command', () => {
    expect(findMnemonCommand({ cliPath: 'missing-mnemon' }, {
      platform: 'linux', env: { PATH: '/tools', MNEMON_CLI_PATH: '/other/mnemon' }, home: '/unused',
      isExecutable: path => path === '/tools/mnemon' || path === '/other/mnemon',
    })).toBeUndefined()
  })

  it('uses the same executable for command-name health checks and execution', async () => {
    const root = temporaryDirectory()
    const binary = join(root, process.platform === 'win32' ? 'mnemon.exe' : 'mnemon')
    writeFileSync(binary, 'fixture')
    chmodSync(binary, 0o755)
    vi.stubEnv('PATH', root)
    vi.stubEnv('MNEMON_CLI_PATH', '')
    const config = resolveConfig({ cliPath: 'mnemon', dataDir: root })
    const run = vi.fn(async () => ({ stdout: 'mnemon version 0.2.5\n', stderr: '', exitCode: 0 }))
    const runner = createRunner(config, run)

    expect(runner.command).toBe(binary)
    expect(runner.commandFound).toBe(true)
    await runner.runText(['--version'], { globalFlags: false })
    expect(run).toHaveBeenCalledWith(binary, ['--version'], expect.any(Object))
  })

  it('refreshes CLI discovery after installation and removal without recreating the runner', async () => {
    const root = temporaryDirectory()
    const binary = join(root, process.platform === 'win32' ? 'mnemon.exe' : 'mnemon')
    vi.stubEnv('PATH', root)
    vi.stubEnv('MNEMON_CLI_PATH', '')
    const run = vi.fn(async () => ({ stdout: 'mnemon version 0.2.5\n', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveConfig({ cliPath: 'mnemon', dataDir: root }), run)
    expect(runner.commandFound).toBe(false)

    writeFileSync(binary, 'fixture')
    chmodSync(binary, 0o755)
    expect(runner.commandFound).toBe(true)
    expect(runner.command).toBe(binary)
    await runner.runText(['--version'], { globalFlags: false })
    expect(run).toHaveBeenCalledWith(binary, ['--version'], expect.any(Object))

    rmSync(binary)
    expect(runner.commandFound).toBe(false)
  })

  it('keeps an explicit cliPath first and expands Windows home syntax', () => {
    expect(findMnemonCommand(
      { cliPath: '~\\go\\bin\\mnemon.exe' },
      { platform: 'win32', env: {}, home: 'C:\\Users\\alice', isExecutable: () => false },
    )).toBe('C:\\Users\\alice\\go\\bin\\mnemon.exe')
  })

  it('reads Windows environment names case-insensitively and accepts only mnemon.exe from PATH', () => {
    const probes: string[] = []
    const command = findMnemonCommand({}, {
      platform: 'win32',
      env: { Path: 'C:\\tools' },
      home: 'C:\\Users\\alice',
      isExecutable: (path) => {
        probes.push(path)
        return path.endsWith('mnemon.cmd')
      },
    })

    expect(command).toBeUndefined()
    expect(probes).toContain('C:\\tools\\mnemon.exe')
    expect(probes.every(path => !path.endsWith('.cmd'))).toBe(true)
  })

  it.each([
    {
      name: 'GOBIN',
      env: { GOBIN: 'D:\\go-bin', GOPATH: 'E:\\go-work' },
      expected: 'D:\\go-bin\\mnemon.exe',
    },
    {
      name: 'the first GOPATH entry',
      env: { GOPATH: 'D:\\go-work;E:\\other-work' },
      expected: 'D:\\go-work\\bin\\mnemon.exe',
    },
    {
      name: 'the default user Go bin',
      env: {},
      expected: 'C:\\Users\\alice\\go\\bin\\mnemon.exe',
    },
    {
      name: 'LOCALAPPDATA programs',
      env: { LOCALAPPDATA: 'C:\\Users\\alice\\AppData\\Local' },
      expected: 'C:\\Users\\alice\\AppData\\Local\\Programs\\mnemon\\mnemon.exe',
    },
    {
      name: 'Program Files',
      env: { ProgramFiles: 'C:\\Program Files' },
      expected: 'C:\\Program Files\\mnemon\\mnemon.exe',
    },
  ])('discovers a Windows binary from $name', ({ env, expected }) => {
    expect(findMnemonCommand({}, {
      platform: 'win32',
      env,
      home: 'C:\\Users\\alice',
      isExecutable: path => path === expected,
    })).toBe(expected)
  })

  it('rejects a directory and accepts a regular .exe file on Windows', () => {
    const root = temporaryDirectory()
    const directory = join(root, 'directory.exe')
    const command = join(root, 'mnemon.exe')
    mkdirSync(directory)
    writeFileSync(command, 'fixture', 'utf8')

    expect(findMnemonCommand({}, {
      platform: 'win32', env: { MNEMON_CLI_PATH: directory }, home: root,
    })).toBeUndefined()
    expect(findMnemonCommand({}, {
      platform: 'win32', env: { MNEMON_CLI_PATH: command }, home: root,
    })).toBe(command)
  })

  it('uses native Windows path joining for discovery candidates', () => {
    const expected = win32.join('C:\\Users\\alice', 'go', 'bin', 'mnemon.exe')
    expect(findMnemonCommand({}, {
      platform: 'win32', env: {}, home: 'C:\\Users\\alice', isExecutable: path => path === expected,
    })).toBe(expected)
  })

  it('discovers an official npm shim and calls its launcher with Node while preserving CLI arguments', async () => {
    const root = temporaryDirectory()
    const packageRoot = join(root, 'node_modules', '@mnemon-dev', 'mnemon')
    const launcher = join(packageRoot, 'bin', 'mnemon.js')
    mkdirSync(join(packageRoot, 'bin'), { recursive: true })
    writeFileSync(join(packageRoot, 'package.json'), JSON.stringify({ name: '@mnemon-dev/mnemon' }))
    writeFileSync(launcher, '#!/usr/bin/env node\n')
    const command = join(root, 'mnemon.cmd')
    writeFileSync(command, '@"%dp0%\\node_modules\\@mnemon-dev\\mnemon\\bin\\mnemon.js" %*')
    chmodSync(command, 0o755)
    expect(findMnemonCommand({}, { platform: 'win32', env: { MNEMON_CLI_PATH: command }, home: root })).toBe(command)
    const run = vi.fn(async () => ({ stdout: '{}', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveConfig({ cliPath: command, dataDir: root }), run)
    expect(runner.commandFound).toBe(true)
    await runner.runText(['status'], { store: 'fixture' })
    expect(run).toHaveBeenCalledWith(process.execPath, [launcher, '--data-dir', root, '--store', 'fixture', 'status'], expect.any(Object))
    writeFileSync(command, '@some-other-command %*')
    expect(findMnemonCommand({}, { platform: 'win32', env: { MNEMON_CLI_PATH: command }, home: root })).toBeUndefined()
  })

  it.each([
    { shim: true, embedding: false, protocol: 'auto' as const },
    { shim: true, embedding: true, protocol: 'auto' as const },
    { shim: false, embedding: true, protocol: 'openai' as const },
  ])('runs npm launchers as Node and preserves child configuration ($shim, $embedding, $protocol)', async ({ shim, embedding, protocol }) => {
    const root = join(temporaryDirectory(), 'npm global with spaces')
    const packageRoot = join(root, 'node_modules', '@mnemon-dev', 'mnemon')
    const launcher = join(packageRoot, 'bin', 'mnemon.js')
    mkdirSync(join(packageRoot, 'bin'), { recursive: true })
    writeFileSync(join(packageRoot, 'package.json'), JSON.stringify({ name: '@mnemon-dev/mnemon' }))
    // A real child process reports only test-owned environment values.
    writeFileSync(launcher, `console.log(JSON.stringify({
      args: process.argv.slice(2), runAsNode: process.env.ELECTRON_RUN_AS_NODE,
      endpoint: process.env.MNEMON_EMBED_ENDPOINT, model: process.env.MNEMON_EMBED_MODEL,
      key: process.env.MNEMON_EMBED_API_KEY, protocol: process.env.MNEMON_EMBED_PROTOCOL,
      dimensions: process.env.MNEMON_EMBED_DIMENSIONS,
    }))`)
    const command = shim ? join(root, 'mnemon.cmd') : launcher
    if (shim) writeFileSync(command, '@"%dp0%\\node_modules\\@mnemon-dev\\mnemon\\bin\\mnemon.js" %*')
    vi.stubEnv('ELECTRON_RUN_AS_NODE', '0')
    vi.stubEnv('MNEMON_EMBED_ENDPOINT', 'http://inherited.invalid')
    vi.stubEnv('MNEMON_EMBED_MODEL', 'inherited-model')
    vi.stubEnv('MNEMON_EMBED_API_KEY', 'inherited-test-key')
    vi.stubEnv('MNEMON_EMBED_PROTOCOL', 'ollama')
    vi.stubEnv('MNEMON_EMBED_DIMENSIONS', '256')
    const runner = createRunner(resolveConfig({ cliPath: command, dataDir: root,
      embedding: { enabled: embedding, endpoint: 'http://configured.invalid', model: 'configured-model', apiKey: 'test-key', protocol },
    }))
    await expect(runner.runJson(['status', 'literal & $argument'], { store: 'fixture' })).resolves.toEqual({
      args: ['--data-dir', root, '--store', 'fixture', 'status', 'literal & $argument'], runAsNode: '1',
      endpoint: embedding ? 'http://configured.invalid' : 'http://inherited.invalid',
      model: embedding ? 'configured-model' : 'inherited-model', key: embedding ? 'test-key' : 'inherited-test-key',
      ...(embedding && protocol === 'auto' ? {} : { protocol: embedding ? protocol : 'ollama' }), dimensions: '256',
    })
    expect(process.env.ELECTRON_RUN_AS_NODE).toBe('0')
    expect(process.env.MNEMON_EMBED_PROTOCOL).toBe('ollama')
  })

  it('keeps native executables on the direct path without adding a Node environment', async () => {
    vi.stubEnv('ELECTRON_RUN_AS_NODE', undefined)
    const root = temporaryDirectory()
    const command = join(root, 'mnemon.exe')
    const run = vi.fn<ProcessRunner>(async () => ({ stdout: '{}', stderr: '', exitCode: 0 }))
    const signal = new AbortController().signal
    const runner = createRunner(resolveConfig({ cliPath: command, dataDir: root, timeoutMs: 4321 }), run)
    await runner.runJson(['status'], { signal })
    expect(run).toHaveBeenCalledWith(command, ['--data-dir', root, 'status'], { timeoutMs: 4321, signal })
    expect(process.env.ELECTRON_RUN_AS_NODE).toBeUndefined()
  })
})

describe('Mnemon CLI output caps and failure messages', () => {
  function fixture(run: ProcessRunner) {
    vi.stubEnv('ELECTRON_RUN_AS_NODE', undefined)
    const root = temporaryDirectory()
    const command = join(root, 'mnemon.exe')
    return { root, command, runner: createRunner(resolveConfig({ cliPath: command, dataDir: root, timeoutMs: 4321 }), run) }
  }

  it('forwards a per-call output cap to the process and leaves the default alone otherwise', async () => {
    const run = vi.fn<ProcessRunner>(async () => ({ stdout: '[]', stderr: '', exitCode: 0 }))
    const { root, command, runner } = fixture(run)
    await runner.runJson(['--readonly', 'recall', '', '--basic', '--limit', '100000'], { store: 'work', maxOutputBytes: 128 * 1024 * 1024 })
    await runner.runText(['viz', '--format', 'html', '--output', '-'], { store: 'work', maxOutputBytes: 64 })
    await runner.runJson(['status'], { store: 'work' })
    await runner.runTextBatch([{ args: ['store', 'set', 'work'], options: { store: 'work', maxOutputBytes: 32 } }])
    expect(run.mock.calls.map(call => call[2])).toEqual([
      { timeoutMs: 4321, maxOutputBytes: 128 * 1024 * 1024 },
      { timeoutMs: 4321, maxOutputBytes: 64 },
      { timeoutMs: 4321 },
      { timeoutMs: 4321, maxOutputBytes: 32 },
    ])
    expect(run.mock.calls[0]?.slice(0, 2)).toEqual([command, ['--data-dir', root, '--store', 'work', '--readonly', 'recall', '', '--basic', '--limit', '100000']])
  })

  it('reports an oversized output as such instead of suggesting a reinstall', async () => {
    const run = vi.fn<ProcessRunner>(async () => { throw new ProcessError('mnemon output exceeded 2097152 bytes', 'output-limit') })
    const { runner } = fixture(run)
    const failure = runner.runJson(['--readonly', 'recall', '', '--basic', '--limit', '100000'], { store: 'work' })
    await expect(failure).rejects.toMatchObject({ name: 'MnemonCliError', message: 'mnemon recall stopped: output exceeded 2097152 bytes' })
    await expect(failure).rejects.not.toThrow(/Install|PATH|cliPath/u)
  })

  it('keeps the install hint for launch failures only', async () => {
    const reasons = ['launch', 'timeout', 'aborted'] as const
    const messages: string[] = []
    for (const reason of reasons) {
      const run = vi.fn<ProcessRunner>(async () => { throw new ProcessError(`mnemon ${reason} detail`, reason) })
      const { runner } = fixture(run)
      messages.push(await runner.runJson(['status']).then(() => '', (error: Error) => error.message))
    }
    expect(messages[0]).toMatch(/^mnemon launch detail\. Install/u)
    expect(messages[1]).toBe('mnemon timeout detail')
    expect(messages[2]).toBe('mnemon aborted detail')
    // A custom process runner without failure reasons still gets the hint, as before.
    const plain = vi.fn<ProcessRunner>(async () => { throw new Error('spawn mnemon ENOENT') })
    await expect(fixture(plain).runner.runJson(['status'])).rejects.toThrow(/^spawn mnemon ENOENT\. Install/u)
  })
})
