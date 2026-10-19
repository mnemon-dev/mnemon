import { describe, expect, it, vi } from 'vitest'
import type { InteractionConfig } from "../src/host/config.ts"
import type { ClientConnectionHandle } from "../src/host/dsh.ts"
import { MnemonSettingsScope } from '../src/client/settings.ts'

describe('MnemonSettingsScope', () => {
  it('keeps a successful Host snapshot when one subscriber throws', async () => {
    const snapshot = { status: 'ready' as const, value: { turnBar: true }, revision: 1, writable: true, mode: 'host' as const }
    const call = vi.fn(async () => ({ ok: true as const, value: snapshot }))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const scope = new MnemonSettingsScope<InteractionConfig>({ rpc: { call }, isLoopback: true } as ClientConnectionHandle, 'mnemon-ui')
    const laterListener = vi.fn()
    scope.subscribe(() => { throw new Error('broken client mount') })
    scope.subscribe(laterListener)

    await vi.waitFor(() => expect(scope.getSnapshot()).toEqual(snapshot))

    expect(laterListener).toHaveBeenCalledOnce()
    expect(call).toHaveBeenCalledOnce()
    expect(call).toHaveBeenCalledWith('/dsh-mnemon-settings', 'get', { namespace: 'mnemon-ui' }, expect.any(AbortSignal))
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('keeping the published Host snapshot'), expect.any(Error))
    warn.mockRestore()
  })

  it('uses the shared API carrier for a writable paired remote client', async () => {
    const call = vi.fn(async (_channel: string, _endpoint: string, args: unknown) => {
      const endpoint = (args as { args: { endpoint: string } }).args.endpoint
      return {
        ok: true as const,
        value: {
          ok: true as const,
          value: {
            status: 'ready' as const,
            value: { turnBar: endpoint === 'mutate' },
            revision: endpoint === 'mutate' ? 2 : 1,
            writable: true,
            mode: 'host' as const,
          },
        },
      }
    })
    const connection = { isLoopback: false, rpc: { call } } as ClientConnectionHandle
    const scope = new MnemonSettingsScope<InteractionConfig>(connection, 'mnemon-ui')
    await vi.waitFor(() => expect(scope.getSnapshot().revision).toBe(1))
    await scope.mutate([{ op: 'set', path: ['turnBar'], value: true }])

    expect(call).toHaveBeenNthCalledWith(1, '/api', 'dshMnemon/settings', {
      args: { endpoint: 'get', payload: { namespace: 'mnemon-ui' } },
    }, expect.any(AbortSignal))
    expect(call).toHaveBeenNthCalledWith(2, '/api', 'dshMnemon/settings', {
      args: {
        endpoint: 'mutate',
        payload: { namespace: 'mnemon-ui', expectedRevision: 1, ops: [{ op: 'set', path: ['turnBar'], value: true }] },
      },
    }, expect.any(AbortSignal))
  })

  it('commits a multi-field edit through one namespaced revision fence', async () => {
    const call = vi.fn(async (_channel: string, endpoint: string, payload: unknown) => {
      if (endpoint === 'get') return { ok: true as const, value: { status: 'ready', value: { turnBar: false, saveAction: false }, revision: 3, writable: true, mode: 'host' } }
      return { ok: true as const, value: { status: 'ready', value: { turnBar: true, saveAction: true }, revision: 4, writable: true, mode: 'host' } }
    })
    const scope = new MnemonSettingsScope<InteractionConfig>({ rpc: { call }, isLoopback: true } as ClientConnectionHandle, 'mnemon-ui')
    await vi.waitFor(() => expect(scope.getSnapshot().revision).toBe(3))

    const ops = [
      { op: 'set' as const, path: ['turnBar'], value: true },
      { op: 'set' as const, path: ['saveAction'], value: true },
    ]
    await scope.mutate(ops)

    expect(call).toHaveBeenLastCalledWith('/dsh-mnemon-settings', 'mutate', { namespace: 'mnemon-ui', expectedRevision: 3, ops }, expect.any(AbortSignal))
    expect(scope.getSnapshot()).toMatchObject({ revision: 4, value: { turnBar: true, saveAction: true } })
  })

  it('reloads the authoritative snapshot after a rejected revision', async () => {
    let reads = 0
    const call = vi.fn(async (_channel: string, endpoint: string) => {
      if (endpoint === 'get') {
        reads += 1
        return { ok: true as const, value: { status: 'ready', value: { turnBar: reads > 1 }, revision: reads, writable: true, mode: 'host' } }
      }
      return { ok: false as const, error: { code: 'settings-rejected' as const, message: 'settings changed concurrently', details: { ns: 'mnemon-ui' } } }
    })
    const scope = new MnemonSettingsScope<InteractionConfig>({ rpc: { call }, isLoopback: true } as ClientConnectionHandle, 'mnemon-ui')
    await vi.waitFor(() => expect(scope.getSnapshot().revision).toBe(1))

    await expect(scope.mutate([{ op: 'set', path: ['turnBar'], value: true }])).rejects.toThrow('concurrently')
    expect(scope.getSnapshot()).toMatchObject({ revision: 2, value: { turnBar: true } })
  })

  it('bounds a stalled settings write instead of leaving the UI saving forever', async () => {
    const call = vi.fn((_channel: string, endpoint: string, _payload: unknown, signal?: AbortSignal) => {
      if (endpoint === 'get') {
        return Promise.resolve({ ok: true as const, value: { status: 'ready', value: { turnBar: false }, revision: 1, writable: true, mode: 'host' } })
      }
      return new Promise<never>((_resolve, reject) => {
        if (signal?.aborted) reject(signal.reason)
        else signal?.addEventListener('abort', () => reject(signal.reason), { once: true })
      })
    })
    const scope = new MnemonSettingsScope<InteractionConfig>({ rpc: { call }, isLoopback: true } as ClientConnectionHandle, 'mnemon-ui', 20)
    await vi.waitFor(() => expect(scope.getSnapshot().revision).toBe(1))

    await expect(scope.mutate([{ op: 'set', path: ['turnBar'], value: true }])).rejects.toThrow('timed out')

    expect(call).toHaveBeenLastCalledWith('/dsh-mnemon-settings', 'mutate', expect.any(Object), expect.any(AbortSignal))
    expect(scope.getSnapshot()).toMatchObject({ status: 'ready', revision: 1, value: { turnBar: false } })
  })
})
