import { afterEach, describe, expect, it, vi } from 'vitest'
import { resolveMemorySpacesConfig } from '../src/config.ts'
import { createRunner } from '../src/runner.ts'
import type { ProcessOptions, ProcessRunner } from '../src/providers/process.ts'
afterEach(() => vi.unstubAllEnvs())
describe('Source-owned CLI configuration and serialization', () => {
  it('preserves Mnemon environment and active-store semantics when config is omitted', () => {
    vi.stubEnv('MNEMON_DATA_DIR', '/memory-root')
    vi.stubEnv('MNEMON_STORE', 'shared')
    const process: ProcessRunner = async () => ({ stdout: '{}', stderr: '', exitCode: 0 })
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: '/fake/mnemon', dataDir: '/memory-root' }), process)
    expect(runner.effectiveDataDir()).toBe('/memory-root')
    expect(runner.effectiveStore()).toBe('shared')
  })

  it('injects saved embedding overrides into every Mnemon process without changing the Host environment', async () => {
    vi.stubEnv('MNEMON_EMBED_ENDPOINT', 'http://launchctl.example:11434')
    vi.stubEnv('MNEMON_EMBED_MODEL', 'launchctl-model')
    vi.stubEnv('MNEMON_EMBED_API_KEY', 'host-secret')
    vi.stubEnv('MNEMON_EMBED_PROTOCOL', 'openai')
    vi.stubEnv('MNEMON_EMBED_DIMENSIONS', '256')
    const processRunner = vi.fn<ProcessRunner>(async () => ({ stdout: '{}', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({
      cliPath: '/fake/mnemon',
      embedding: { enabled: true, endpoint: 'http://127.0.0.1:11434', model: 'qwen3-embedding:0.6b' },
    }), processRunner)

    await runner.runText(['status'])

    expect(processRunner).toHaveBeenCalledWith('/fake/mnemon', expect.arrayContaining(['status']), expect.objectContaining({
      env: expect.objectContaining({
        MNEMON_EMBED_ENDPOINT: 'http://127.0.0.1:11434',
        MNEMON_EMBED_MODEL: 'qwen3-embedding:0.6b',
        MNEMON_EMBED_API_KEY: '',
        MNEMON_EMBED_DIMENSIONS: '256',
      }),
    }))
    const options = processRunner.mock.calls[0]![2] as ProcessOptions
    expect(options.env?.MNEMON_EMBED_PROTOCOL).toBeUndefined()
    expect(process.env.MNEMON_EMBED_ENDPOINT).toBe('http://launchctl.example:11434')
    expect(process.env.MNEMON_EMBED_MODEL).toBe('launchctl-model')
    expect(process.env.MNEMON_EMBED_API_KEY).toBe('host-secret')
  })

  it('forwards a saved embedding API key and protocol over the inherited Host environment', async () => {
    vi.stubEnv('MNEMON_EMBED_API_KEY', 'host-secret')
    const processRunner = vi.fn<ProcessRunner>(async () => ({ stdout: '{}', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({
      cliPath: '/fake/mnemon',
      embedding: { enabled: true, endpoint: 'http://127.0.0.1:8080/api', model: 'bge-m3-mlx-8bit', apiKey: ' sk-managed ', protocol: 'openai' },
    }), processRunner)

    await runner.runText(['status'])

    expect(processRunner).toHaveBeenCalledWith('/fake/mnemon', expect.arrayContaining(['status']), expect.objectContaining({
      env: expect.objectContaining({
        MNEMON_EMBED_API_KEY: 'sk-managed',
        MNEMON_EMBED_PROTOCOL: 'openai',
      }),
    }))
  })

  it('leaves child environment inheritance untouched when the DSH override is disabled', async () => {
    vi.stubEnv('MNEMON_EMBED_ENDPOINT', 'http://launchctl.example:11434')
    const processRunner = vi.fn<ProcessRunner>(async () => ({ stdout: '{}', stderr: '', exitCode: 0 }))
    const runner = createRunner(resolveMemorySpacesConfig({
      cliPath: '/fake/mnemon',
      embedding: { enabled: false, endpoint: 'http://ignored.example:11434', model: 'ignored' },
    }), processRunner)

    await runner.runText(['status'])

    expect(processRunner.mock.calls[0]![2]).not.toHaveProperty('env')
  })

  it('serializes CLI processes so concurrent WebUI reads cannot race Mnemon migrations', async () => {
    let active = 0
    let maximumActive = 0
    const process = vi.fn<ProcessRunner>(async () => {
      active += 1
      maximumActive = Math.max(maximumActive, active)
      await Promise.resolve()
      active -= 1
      return { stdout: '{}', stderr: '', exitCode: 0 }
    })
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: '/fake/mnemon' }), process)

    await Promise.all([
      runner.runText(['status']),
      runner.runText(['viz', '--format', 'html', '--output', '-']),
      runner.runText(['--version'], { globalFlags: false }),
    ])

    expect(process).toHaveBeenCalledTimes(3)
    expect(maximumActive).toBe(1)
  })

  it('continues the CLI queue after one command fails', async () => {
    const process = vi.fn<ProcessRunner>()
      .mockResolvedValueOnce({ stdout: '', stderr: 'locked', exitCode: 1 })
      .mockResolvedValueOnce({ stdout: 'recovered', stderr: '', exitCode: 0 })
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: '/fake/mnemon' }), process)

    await expect(runner.runText(['status'])).rejects.toThrow('locked')
    await expect(runner.runText(['--version'], { globalFlags: false })).resolves.toBe('recovered')
  })

  it('keeps a related CLI batch contiguous in the shared process queue', async () => {
    const events: string[] = []
    const process = vi.fn<ProcessRunner>(async (_command, args) => {
      events.push(String(args.at(-1)))
      await Promise.resolve()
      return { stdout: '', stderr: '', exitCode: 0 }
    })
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: '/fake/mnemon' }), process)

    const batch = runner.runTextBatch([{ args: ['first'] }, { args: ['second'] }])
    const queued = runner.runText(['third'])
    await Promise.all([batch, queued])

    expect(events).toEqual(['first', 'second', 'third'])
  })

  it('points launch failures at the environment variable and actual settings namespace', async () => {
    const process = vi.fn<ProcessRunner>().mockRejectedValue(new Error('spawn mnemon ENOENT'))
    const runner = createRunner(resolveMemorySpacesConfig({ cliPath: '/missing/mnemon' }), process)

    await expect(runner.runText(['status'])).rejects.toThrow('MNEMON_CLI_PATH or mnemon.cliPath')
  })
})
