import { EventEmitter } from 'node:events'
import { spawn } from 'node:child_process'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { runProcess } from '../src/host/process.ts'

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
