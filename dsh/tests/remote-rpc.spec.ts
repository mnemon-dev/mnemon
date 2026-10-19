import { Context } from '@deepseek-ai/cordis'
import { describe, expect, it, vi } from 'vitest'
import type { HostRpcHandler } from '../src/host/dsh.ts'
import { MnemonRemoteService } from '../src/host/remote-rpc.ts'

function handler(value: string): HostRpcHandler {
  return vi.fn(async endpoint => ({ ok: true as const, value: `${value}:${endpoint}` }))
}

describe('Mnemon API Gateway service', () => {
  function fixture(management: boolean) {
    const handlers = {
      read: handler('read'), activation: handler('activation'), write: handler('write'), pack: handler('pack'),
      settings: handler('settings'), view: handler('view'), viewWrite: handler('view-write'), management,
    }
    const service = new MnemonRemoteService(new Context(), handlers)
    return { service, handlers, signal: new AbortController().signal }
  }

  it('delegates reads and narrow activation without widening management authority', async () => {
    const { service, handlers, signal } = fixture(false)
    await expect(service.read('status-summary', { workspaceId: 'workspace' }, signal)).resolves.toEqual({ ok: true, value: 'read:status-summary' })
    await expect(service.view('dashboard', {}, signal)).resolves.toEqual({ ok: true, value: 'view:dashboard' })
    await expect(service.activation('body', { active: true }, signal)).resolves.toEqual({ ok: true, value: 'activation:body' })
    vi.mocked(handlers.settings).mockResolvedValueOnce({ ok: true, value: { status: 'ready', writable: true } })
    await expect(service.settings('get', { namespace: 'mnemon' }, signal)).resolves.toEqual({ ok: true, value: { status: 'ready', writable: false } })
    await expect(service.settings('mutate', {}, signal)).resolves.toMatchObject({ ok: false, error: { code: 'bad-request' } })
    await expect(service.write('remember', {}, signal)).resolves.toMatchObject({ ok: false, error: { code: 'bad-request' } })
    await expect(service.pack('export', {}, signal)).resolves.toMatchObject({ ok: false, error: { code: 'bad-request' } })
    await expect(service.viewWrite('apply', {}, signal)).resolves.toMatchObject({ ok: false, error: { code: 'bad-request' } })
    expect(handlers.read).toHaveBeenCalledWith('status-summary', { workspaceId: 'workspace' }, signal)
    expect(handlers.view).toHaveBeenCalledWith('dashboard', {}, signal)
    expect(handlers.activation).toHaveBeenCalledWith('body', { active: true }, signal)
    expect(handlers.settings).toHaveBeenCalledWith('get', { namespace: 'mnemon' }, signal)
    expect(handlers.write).not.toHaveBeenCalled()
  })

  it('delegates management only after the explicit trusted-host grant', async () => {
    const { service, handlers, signal } = fixture(true)
    await expect(service.write('remember', {}, signal)).resolves.toEqual({ ok: true, value: 'write:remember' })
    await expect(service.pack('export', {}, signal)).resolves.toEqual({ ok: true, value: 'pack:export' })
    await expect(service.viewWrite('apply', {}, signal)).resolves.toEqual({ ok: true, value: 'view-write:apply' })
    await expect(service.settings('mutate', {}, signal)).resolves.toEqual({ ok: true, value: 'settings:mutate' })
    expect(handlers.write).toHaveBeenCalledWith('remember', {}, signal)
    expect(handlers.pack).toHaveBeenCalledWith('export', {}, signal)
    expect(handlers.viewWrite).toHaveBeenCalledWith('apply', {}, signal)
    expect(handlers.settings).toHaveBeenCalledWith('mutate', {}, signal)
  })
})
