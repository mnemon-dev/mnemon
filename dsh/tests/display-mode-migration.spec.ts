import { describe, expect, it, vi } from 'vitest'
import { Config } from '../src/host/config.ts'
import type { HostSettingsService, SettingsOperation } from '../src/host/dsh.ts'
import { createSettingsHandler, migrateLegacyDisplayMode } from '../src/host/settings.ts'

function fixture(options: { base?: Record<string, unknown>; user?: Record<string, unknown>; writable?: boolean } = {}) {
  const base = options.base ?? {}
  let user = structuredClone(options.user ?? {})
  let revision = 3
  const conflict = () => Object.assign(new Error('settings changed concurrently'), { code: 'SETTINGS_CONFLICT' })
  const mutate = vi.fn(async (namespace: string, ops: SettingsOperation[], expected?: number) => {
    expect(namespace).toBe('mnemon')
    if (expected !== undefined && expected !== revision) throw conflict()
    for (const op of ops) {
      expect(op.path).toHaveLength(1)
      if (op.op === 'set') user[op.path[0]!] = op.value
      else delete user[op.path[0]!]
    }
    revision += 1
  })
  const settings = {
    writable: options.writable ?? true,
    register: vi.fn(),
    describe: () => [{ ns: 'mnemon', base, user: structuredClone(user), value: Config({ ...base, ...user }), revision, applies: 'live' as const }],
    mutate,
    onUpdated: () => () => {},
  } satisfies HostSettingsService
  return {
    settings, mutate, conflict,
    stored: () => structuredClone(user),
    externalEdit: (patch: Record<string, unknown>) => { user = { ...user, ...patch }; revision += 1 },
  }
}

const canonicalOp = { op: 'set', path: ['displayMode'], value: 'builtin' }

describe('canonical builtin configuration', () => {
  it.each(['user', 'base'] as const)('migrates legacy %s configuration with one fenced leaf edit', async source => {
    const value = fixture({ [source]: { displayMode: 'buildin' }, user: { timeoutMs: 25000, ...(source === 'user' ? { displayMode: 'buildin' } : {}) } })
    await migrateLegacyDisplayMode(value.settings)
    expect(value.mutate).toHaveBeenCalledExactlyOnceWith('mnemon', [canonicalOp], 3)
    expect(value.stored()).toEqual({ timeoutMs: 25000, displayMode: 'builtin' })
    await migrateLegacyDisplayMode(value.settings)
    expect(value.mutate).toHaveBeenCalledTimes(1)
  })

  it.each(['sidebar', 'builtin'] as const)('preserves an explicit %s override above a legacy profile base', async displayMode => {
    const value = fixture({ base: { displayMode: 'buildin' }, user: { displayMode } })
    await migrateLegacyDisplayMode(value.settings)
    expect(value.mutate).not.toHaveBeenCalled()
    expect(value.stored()).toEqual({ displayMode })
  })

  it.each([undefined, 'sidebar', 'builtin'] as const)('does not pin already canonical or absent configuration: %s', async displayMode => {
    const value = fixture({ base: displayMode === undefined ? {} : { displayMode } })
    await migrateLegacyDisplayMode(value.settings)
    expect(value.mutate).not.toHaveBeenCalled()
    expect(value.stored()).toEqual({})
  })

  it('recognizes legacy read-only settings without attempting a write or misreporting the raw document', async () => {
    const value = fixture({ user: { displayMode: 'buildin' }, writable: false })
    await migrateLegacyDisplayMode(value.settings)
    expect(value.mutate).not.toHaveBeenCalled()
    await expect(createSettingsHandler(value.settings)('get', {})).resolves.toMatchObject({
      ok: true, value: { value: { displayMode: 'builtin' }, user: { displayMode: 'buildin' }, writable: false },
    })
  })

  it('never overwrites a newer explicit Sidebar choice', async () => {
    const value = fixture({ user: { displayMode: 'buildin' } })
    value.mutate.mockImplementationOnce(async () => {
      value.externalEdit({ displayMode: 'sidebar' })
      throw value.conflict()
    })
    await migrateLegacyDisplayMode(value.settings)
    expect(value.mutate).toHaveBeenCalledTimes(1)
    expect(value.stored()).toEqual({ displayMode: 'sidebar' })
  })

  it('retries an unrelated concurrent edit with its new revision and preserves it', async () => {
    const value = fixture({ user: { displayMode: 'buildin', timeoutMs: 10000 } })
    value.mutate.mockImplementationOnce(async () => {
      value.externalEdit({ timeoutMs: 20000 })
      throw value.conflict()
    })
    await migrateLegacyDisplayMode(value.settings)
    expect(value.mutate).toHaveBeenNthCalledWith(2, 'mnemon', [canonicalOp], 4)
    expect(value.stored()).toEqual({ displayMode: 'builtin', timeoutMs: 20000 })
  })

  it('surfaces persistence failures without pretending the stored alias was changed', async () => {
    const value = fixture({ user: { displayMode: 'buildin' } })
    value.mutate.mockRejectedValue(new Error('test persistence unavailable'))
    await expect(migrateLegacyDisplayMode(value.settings)).rejects.toThrow('persistence unavailable')
    expect(value.stored()).toEqual({ displayMode: 'buildin' })
    await expect(createSettingsHandler(value.settings)('get', {})).resolves.toMatchObject({ ok: true, value: { value: { displayMode: 'builtin' } } })
  })

  it('canonicalizes an external legacy edit again without repeated writes after convergence', async () => {
    const value = fixture({ user: { displayMode: 'buildin' } })
    await migrateLegacyDisplayMode(value.settings)
    value.externalEdit({ displayMode: 'buildin', tabEnabled: false })
    await migrateLegacyDisplayMode(value.settings)
    await migrateLegacyDisplayMode(value.settings)
    expect(value.mutate).toHaveBeenCalledTimes(2)
    expect(value.stored()).toEqual({ displayMode: 'builtin', tabEnabled: false })
  })

  it('accepts an old client write but persists and returns only builtin', async () => {
    const value = fixture()
    const op = { op: 'set', path: ['displayMode'], value: 'buildin' }
    await expect(createSettingsHandler(value.settings)('mutate', { ops: [op], expectedRevision: 3 })).resolves.toMatchObject({
      ok: true, value: { value: { displayMode: 'builtin' }, user: { displayMode: 'builtin' }, revision: 4 },
    })
    expect(value.mutate).toHaveBeenCalledExactlyOnceWith('mnemon', [canonicalOp], 3)
    expect(op.value).toBe('buildin')
  })

  it.each(['built-in', 'Builtin', '', true])('rejects unsupported placement %s before persisting', async displayMode => {
    const value = fixture()
    await expect(createSettingsHandler(value.settings)('mutate', { ops: [{ op: 'set', path: ['displayMode'], value: displayMode }] })).resolves.toMatchObject({ ok: false })
    expect(value.mutate).not.toHaveBeenCalled()
  })
})
