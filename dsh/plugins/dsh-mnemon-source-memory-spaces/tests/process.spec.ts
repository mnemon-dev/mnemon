import { EventEmitter } from 'node:events'
import { spawn } from 'node:child_process'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { DEFAULT_MAX_OUTPUT_BYTES, ProcessError, runProcess } from '../src/providers/process.ts'

vi.mock('node:child_process', () => ({ spawn: vi.fn() }))

function subprocess() {
  const child = Object.assign(new EventEmitter(), {
    stdout: new EventEmitter(),
    stderr: new EventEmitter(),
    exitCode: null as number | null,
    signalCode: null as string | null,
    kill: vi.fn((signal: string) => { child.signalCode = signal; return true }),
  })
  vi.mocked(spawn).mockReturnValue(child as unknown as ReturnType<typeof spawn>)
  return child
}

describe('bounded subprocess UTF-8 output', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('preserves interleaved stdout and stderr even when every multibyte character is split', async () => {
    const child = subprocess()
    const stdout = '{"content":"配置键为 backend.rule.4，验证端口为 12004。𠮷"}\n'
    const stderr = '错误信息：保留原文 𠀀\n'
    const out = Buffer.from(stdout)
    const err = Buffer.from(stderr)
    const result = runProcess('test-cli', [], { timeoutMs: 1000, maxOutputBytes: out.length + err.length })
    expect(spawn).toHaveBeenCalledWith('test-cli', [], expect.objectContaining({ windowsHide: true, shell: false, stdio: ['ignore', 'pipe', 'pipe'] }))
    for (let index = 0; index < Math.max(out.length, err.length); index++) {
      if (index < out.length) child.stdout.emit('data', out.subarray(index, index + 1))
      if (index < err.length) child.stderr.emit('data', err.subarray(index, index + 1))
    }
    child.exitCode = 0
    child.emit('close', 0)
    expect(await result).toEqual({ stdout, stderr, exitCode: 0 })
    expect(child.kill).not.toHaveBeenCalled()
  })

  it('still bounds combined raw bytes, including incomplete UTF-8 sequences', async () => {
    const child = subprocess()
    const result = runProcess('test-cli', [], { timeoutMs: 1000, maxOutputBytes: 2 })
    const rejected = expect(result).rejects.toThrow('mnemon output exceeded 2 bytes')
    const character = Buffer.from('中')
    child.stdout.emit('data', character.subarray(0, 1))
    child.stderr.emit('data', character.subarray(0, 1))
    child.stdout.emit('data', character.subarray(1, 2))
    await rejected
    expect(child.kill).toHaveBeenCalledWith('SIGTERM')
    child.emit('close', null)
  })

  it('flushes a truncated final character independently on each stream at close', async () => {
    const child = subprocess()
    const result = runProcess('test-cli', [], { timeoutMs: 1000 })
    child.stdout.emit('data', Buffer.from('中').subarray(0, 2))
    child.stderr.emit('data', Buffer.from('𠀀').subarray(0, 3))
    child.exitCode = 1
    child.emit('close', 1)
    expect(await result).toEqual({ stdout: '�', stderr: '�', exitCode: 1 })
  })
})

describe('subprocess failure reasons', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('keeps the 2 MiB default and marks an oversized output as an output limit, not a launch failure', async () => {
    const child = subprocess()
    const result = runProcess('test-cli', [], { timeoutMs: 1000 })
    const rejected = expect(result).rejects.toMatchObject({ name: 'ProcessError', reason: 'output-limit', message: `mnemon output exceeded ${DEFAULT_MAX_OUTPUT_BYTES} bytes` })
    child.stdout.emit('data', Buffer.alloc(DEFAULT_MAX_OUTPUT_BYTES + 1, 0x61))
    await rejected
    expect(DEFAULT_MAX_OUTPUT_BYTES).toBe(2 * 1024 * 1024)
    child.emit('close', null)
  })

  it('honors a per-call cap above the default', async () => {
    const child = subprocess()
    const big = Buffer.alloc(DEFAULT_MAX_OUTPUT_BYTES + 1024, 0x61)
    const result = runProcess('test-cli', [], { timeoutMs: 1000, maxOutputBytes: big.length })
    child.stdout.emit('data', big)
    child.exitCode = 0
    child.emit('close', 0)
    expect((await result).stdout).toHaveLength(big.length)
  })

  it('names launch failures, timeouts and cancellations', async () => {
    const launch = subprocess()
    const launched = runProcess('missing-cli', [], { timeoutMs: 1000 })
    launch.emit('error', Object.assign(new Error('spawn missing-cli ENOENT'), { code: 'ENOENT' }))
    await expect(launched).rejects.toMatchObject({ reason: 'launch' })

    vi.useFakeTimers()
    try {
      const slow = subprocess()
      const timed = runProcess('slow-cli', [], { timeoutMs: 50 })
      const timedOut = expect(timed).rejects.toMatchObject({ reason: 'timeout' })
      await vi.advanceTimersByTimeAsync(60)
      await timedOut
      slow.emit('close', null)
    } finally {
      vi.useRealTimers()
    }

    subprocess()
    const controller = new AbortController()
    const aborted = runProcess('test-cli', [], { timeoutMs: 1000, signal: controller.signal })
    controller.abort('user closed the page')
    await expect(aborted).rejects.toBeInstanceOf(ProcessError)
    await expect(aborted).rejects.toMatchObject({ reason: 'aborted' })
  })
})
