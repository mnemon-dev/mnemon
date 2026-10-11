import { describe, expect, it, vi } from 'vitest'
import type { HostConnectionHandle, HostSettingsService } from "../src/host/dsh.ts"
import { createSettingsHandler, registerSettingsRpc } from "../src/host/settings.ts"
import { MNEMON_SETTINGS_CHANNEL } from "../src/host/protocol.ts"

describe('Mnemon settings bridge', () => {
  it('exposes and mutates only the Mnemon namespace through a revision fence', async () => {
    let revision = 2
    let value = { store: 'base', timeoutMs: 10000 }
    let user: Record<string, unknown> = {}
    const mutate = vi.fn(async (_namespace: string, ops: Array<{ op: string; path: string[]; value?: unknown }>, expected?: number) => {
      expect(expected).toBe(revision)
      for (const op of ops) {
        if (op.op === 'set') user[op.path[0]!] = op.value
        else delete user[op.path[0]!]
      }
      value = { ...value, ...user }
      revision += 1
    })
    const settings = {
      writable: true,
      register: vi.fn(),
      mutate,
      describe: () => [{ ns: 'mnemon', value, base: { store: 'base', timeoutMs: 10000 }, user, revision, applies: 'restart' as const }],
    } as unknown as HostSettingsService
    const handler = createSettingsHandler(settings)

    const read = await handler('get', {})
    expect(read).toEqual(expect.objectContaining({ ok: true, value: expect.objectContaining({ revision: 2, writable: true }) }))

    const ops = [
      { op: 'set', path: ['store'], value: 'settings-store' },
      { op: 'set', path: ['idleReviewMs'], value: 45000 },
      { op: 'set', path: ['tabEnabled'], value: false },
      { op: 'set', path: ['runtimeUserScope'], value: 'global' },
      { op: 'set', path: ['embedding'], value: { enabled: true, endpoint: 'http://127.0.0.1:11434', model: 'qwen3-embedding:0.6b' } },
      { op: 'set', path: ['runtimeMemory'], value: { memoryLimitBytes: 20480, userLimitBytes: 10240, maintenanceMaxTokens: 32768 } },
      { op: 'set', path: ['taskAgentModel'], value: { mode: 'fixed', provider: 'deepseek', model: 'deepseek-chat' } },
    ]
    const written = await handler('mutate', { expectedRevision: 2, ops })
    expect(mutate).toHaveBeenCalledWith('mnemon', ops, 2)
    expect(written).toEqual(expect.objectContaining({ ok: true, value: expect.objectContaining({ revision: 3, user: { store: 'settings-store', idleReviewMs: 45000, tabEnabled: false, runtimeUserScope: 'global', embedding: { enabled: true, endpoint: 'http://127.0.0.1:11434', model: 'qwen3-embedding:0.6b' }, runtimeMemory: { memoryLimitBytes: 20480, userLimitBytes: 10240, maintenanceMaxTokens: 32768 }, taskAgentModel: { mode: 'fixed', provider: 'deepseek', model: 'deepseek-chat' } } }) }))
  })

  it.each(['sidebar', 'builtin'] as const)('accepts live displayMode=%s and resetting the preference', async displayMode => {
    const mutate = vi.fn(async () => {})
    const settings = {
      writable: true,
      register: vi.fn(),
      mutate,
      describe: () => [{ ns: 'mnemon', value: {}, revision: 0, applies: 'live' as const }],
    } as unknown as HostSettingsService
    const handler = createSettingsHandler(settings)
    const op = { op: 'set', path: ['displayMode'], value: displayMode }
    await expect(handler('mutate', { ops: [op], expectedRevision: 0 })).resolves.toMatchObject({ ok: true })
    expect(mutate).toHaveBeenCalledWith('mnemon', [op], 0)
    await expect(handler('mutate', { ops: [{ op: 'unset', path: ['displayMode'] }] })).resolves.toMatchObject({ ok: true })
  })

  it('rejects unsupported settings fields', async () => {
    const settings = {
      writable: true,
      register: vi.fn(),
      mutate: vi.fn(),
      describe: () => [{ ns: 'mnemon', value: {}, revision: 0, applies: 'restart' as const }],
    } as unknown as HostSettingsService
    const response = await createSettingsHandler(settings)('mutate', { ops: [{ op: 'set', path: ['other'], value: true }] })
    expect(response).toEqual(expect.objectContaining({
      ok: false,
      error: expect.objectContaining({ code: 'settings-rejected', details: { ns: 'mnemon' } }),
    }))
    expect(settings.mutate).not.toHaveBeenCalled()
  })

  it('accepts only Boolean Layer switches from the Web settings surface', async () => {
    const mutate = vi.fn(async () => {})
    const settings = {
      writable: true,
      register: vi.fn(),
      mutate,
      describe: () => [{ ns: 'mnemon', value: {}, revision: 0, applies: 'live' as const }],
    } as unknown as HostSettingsService
    const handler = createSettingsHandler(settings)
    const switchOp = { op: 'set', path: ['memoryTopology', 'layers', 'documents', 'enabled'], value: false }

    await expect(handler('mutate', { ops: [switchOp] })).resolves.toMatchObject({ ok: true })
    expect(mutate).toHaveBeenCalledWith('mnemon', [switchOp], undefined)

    await expect(handler('mutate', { ops: [{ ...switchOp, value: 'off' }] })).resolves.toMatchObject({ ok: false })
    await expect(handler('mutate', { ops: [{ op: 'set', path: ['memoryTopology'], value: {} }] })).resolves.toMatchObject({ ok: false })
    await expect(handler('mutate', { ops: [{ op: 'set', path: ['memoryTopology', 'layers', 'documents', 'participation'], value: {} }] })).resolves.toMatchObject({ ok: false })
  })

  it('accepts the whole persistence strategy through the Mnemon namespace', async () => {
    const persistenceStrategy = {
      mode: 'automatic',
      providerId: 'mnemon-native',
      prompt: 'Prefer shared project memory.',
      rules: {
        allowedProviderIds: ['mnemon-native', 'openviking'],
        dataBoundary: 'allow-remote',
        requiredCapabilities: ['graph'],
        preference: 'shared-first',
      },
      providerConnections: { openviking: { targetUri: 'viking://resources/team' } },
    }
    const mutate = vi.fn(async () => {})
    const settings = {
      writable: true,
      register: vi.fn(),
      mutate,
      describe: () => [{ ns: 'mnemon', value: { persistenceStrategy }, revision: 1, applies: 'restart' as const }],
    } as unknown as HostSettingsService

    await expect(createSettingsHandler(settings)('mutate', {
      ops: [{ op: 'set', path: ['persistenceStrategy'], value: persistenceStrategy }],
    })).resolves.toMatchObject({ ok: true })
    expect(mutate).toHaveBeenCalledWith('mnemon', [
      { op: 'set', path: ['persistenceStrategy'], value: persistenceStrategy },
    ], undefined)
  })

  it('keeps the startup-only remote access setting immutable from the Web bridge', async () => {
    const settings = {
      writable: true,
      register: vi.fn(),
      mutate: vi.fn(),
      describe: () => [{ ns: 'mnemon', value: { remoteAccess: 'read-only' }, revision: 0, applies: 'live' as const }],
    } as unknown as HostSettingsService
    await expect(createSettingsHandler(settings)('mutate', {
      ops: [{ op: 'set', path: ['remoteAccess'], value: 'trusted-host' }],
    })).resolves.toMatchObject({ ok: false, error: { code: 'settings-rejected' } })
    expect(settings.mutate).not.toHaveBeenCalled()
  })

  it('exposes live browser interaction settings through a separate namespace', async () => {
    let revision = 4
    let value = { turnBar: false, saveAction: false }
    const mutate = vi.fn(async (_namespace: string, ops: Array<{ op: string; path: string[]; value?: unknown }>) => {
      value = { ...value, ...Object.fromEntries(ops.filter(op => op.op === 'set').map(op => [op.path[0], op.value])) }
      revision += 1
    })
    const settings = {
      writable: true,
      register: vi.fn(),
      mutate,
      describe: () => [
        { ns: 'mnemon', value: {}, revision: 1, applies: 'restart' as const },
        { ns: 'mnemon-ui', value, base: { turnBar: false, saveAction: false }, user: {}, revision, applies: 'live' as const },
      ],
    } as unknown as HostSettingsService
    const handler = createSettingsHandler(settings)

    await expect(handler('get', { namespace: 'mnemon-ui' })).resolves.toMatchObject({ ok: true, value: { applies: 'live', revision: 4 } })
    const ops = [
      { op: 'set', path: ['turnBar'], value: true },
      { op: 'set', path: ['saveAction'], value: true },
    ]
    await expect(handler('mutate', { namespace: 'mnemon-ui', expectedRevision: 4, ops })).resolves.toMatchObject({ ok: true, value: { revision: 5 } })
    expect(mutate).toHaveBeenCalledWith('mnemon-ui', ops, 4)

    await expect(handler('mutate', { namespace: 'mnemon-ui', ops: [{ op: 'set', path: ['toolviews'], value: true }] })).resolves.toMatchObject({ ok: false })
  })
})
