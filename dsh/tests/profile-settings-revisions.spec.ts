import { Context, type Fiber } from '@deepseek-ai/cordis'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Config, InteractionConfig } from '../src/host/config.ts'
import type { HostContextShape, HostSettingsService } from '../src/host/dsh.ts'
import type { SettingsOperation } from '../src/host/protocol.ts'
import { ProfileMnemonSettings } from '../src/host/settings-service.ts'
import { schema as ViewSchema } from '../src/host/view-preferences.ts'

type Data = Record<string, unknown>
type Layer = 'value' | 'base' | 'user'
type Descriptor = ReturnType<HostSettingsService['describe']>[number]
type Entry = { id: string; options: { id: string; config: Data } }
const viewNamespace = 'mnemon-view-0123456789abcdef'
const secret = 'synthetic-revision-secret-before'
const replacementSecret = 'synthetic-revision-secret-after'
const roots: Context[] = []

afterEach(async () => {
  for (const root of roots.splice(0).reverse()) await root.fiber.dispose()
})

function applyOperations(input: Data, operations: SettingsOperation[]): Data {
  const result = structuredClone(input)
  for (const operation of operations) {
    let parent = result
    for (const part of operation.path.slice(0, -1)) {
      const value = parent[part]
      if (value === null || typeof value !== 'object' || Array.isArray(value)) parent[part] = {}
      parent = parent[part] as Data
    }
    const field = operation.path.at(-1)!
    if (operation.op === 'set') parent[field] = structuredClone(operation.value)
    else delete parent[field]
  }
  return result
}

function redact(input: Data): Data {
  const result = structuredClone(input)
  const embedding = result.embedding as Data | undefined
  if (embedding !== undefined && 'apiKey' in embedding) embedding.apiKey = '[redacted]'
  return result
}

/** The real facade uses a Forms mock with one serialized native revision lock. */
async function fixture() {
  const root = new Context()
  roots.push(root)
  const initial = {
    timeoutMs: 12000,
    embedding: { apiKey: secret },
    conversationInteraction: { toolviews: false, turnBar: true, saveAction: true },
    memoryView: { strategyTypeId: 'light-context', entries: {} },
  }
  const state = {
    revision: 0,
    value: { ...Config(initial) } as Data,
    base: { ...Config({ ...initial, timeoutMs: 10000 }) } as Data,
    user: structuredClone(initial) as Data,
    entries: [] as Entry[],
    visible: true,
    failure: undefined as unknown,
    beforeAttempt: undefined as (() => void | Promise<void>) | undefined,
  }
  const entry: Entry = { id: 'include:revision-mnemon', options: { id: 'revision-mnemon', config: state.user } }
  state.entries.push(entry)
  let fiber!: Fiber
  let settings!: ProfileMnemonSettings
  let queue = Promise.resolve()

  function sync(): void {
    fiber.config = state.value
    entry.options.config = state.user
  }

  const forms = {
    writable: true,
    configure: vi.fn((_presentation: { auto: boolean }, _owner: unknown) => vi.fn()),
    describe: vi.fn((options?: { redactSecrets?: boolean }): Descriptor[] => state.visible ? [{
      ns: entry.options.id, revision: state.revision, applies: 'live',
      // Native descriptors may reference live data; the facade must retain its
      // own snapshot and must request unredacted data for conflict checks.
      value: options?.redactSecrets ? redact(state.value) : state.value,
      base: options?.redactSecrets ? redact(state.base) : state.base,
      user: options?.redactSecrets ? redact(state.user) : state.user,
    }] : []),
    mutate: vi.fn((namespace: string, operations: SettingsOperation[], expectedRevision?: number): Promise<void> => {
      const result = queue.then(async () => {
        expect(namespace).toBe(entry.options.id)
        await state.beforeAttempt?.()
        if (state.failure !== undefined) throw state.failure
        if (expectedRevision !== undefined && expectedRevision !== state.revision) {
          throw Object.assign(new Error('Native settings revision changed'), {
            code: 'SETTINGS_CONFLICT', expected: expectedRevision, actual: state.revision,
          })
        }
        const next = applyOperations(state.value, operations)
        root.events.waterfall(fiber, 'internal/config', next, () => next)
        state.value = { ...Config(next) }
        state.user = applyOperations(state.user, operations)
        state.revision += 1
        sync()
      })
      queue = result.catch(() => {})
      return result
    }),
  }
  root.provide('settings', forms)
  root.provide('configEditor', { entries: () => state.entries })
  await root.plugin({ inject: ['settings'], apply(ctx: Context) {
    fiber = ctx.fiber
    Object.assign(fiber, { entry })
    sync()
    settings = new ProfileMnemonSettings(ctx as unknown as HostContextShape, state.value)
    settings.register('mnemon', Config, { applies: 'live' })
    settings.register('mnemon-ui', InteractionConfig, { applies: 'live' })
    settings.register(viewNamespace, ViewSchema, { applies: 'live' })
  } })

  function external(operations: SettingsOperation[], layers: readonly Layer[] = ['value', 'user']): void {
    for (const layer of layers) state[layer] = applyOperations(state[layer], operations)
    state.revision += 1
    sync()
  }

  function read(namespace = 'mnemon', redactSecrets = false): Descriptor {
    return settings.describe({ redactSecrets }).find(item => item.ns === namespace)!
  }

  return { settings, forms, state, external, read, entry, fiber }
}

const coreWrite: SettingsOperation[] = [{ op: 'set', path: ['defaultRecallLimit'], value: 20 }]
const uiWrite: SettingsOperation[] = [{ op: 'set', path: ['turnBar'], value: false }]
const viewWrite: SettingsOperation[] = [{ op: 'set', path: ['strategyTypeId'], value: 'scoped' }]

describe('profile settings virtual namespace revision fences', () => {
  it.each(['core-first', 'ui-first'] as const)('saves concurrent Core and UI changes with one shared observed revision (%s)', async order => {
    const f = await fixture()
    const revision = f.read().revision
    const writes = order === 'core-first'
      ? [['mnemon', coreWrite], ['mnemon-ui', uiWrite]] as const
      : [['mnemon-ui', uiWrite], ['mnemon', coreWrite]] as const
    await Promise.all(writes.map(([namespace, operations]) => f.settings.mutate(namespace, operations, revision)))
    expect(f.forms.mutate.mock.calls.map(([, , expected]) => expected)).toEqual([0, 0, 1])
    expect(f.state.user).toMatchObject({ defaultRecallLimit: 20, conversationInteraction: { turnBar: false } })
    expect(f.state.revision).toBe(2)
  })

  it('saves an already-open UI form after a LightView save without losing either change', async () => {
    const f = await fixture()
    const revision = f.read('mnemon-ui').revision
    await f.settings.mutate(viewNamespace, viewWrite, revision)
    await f.settings.mutate('mnemon-ui', uiWrite, revision)
    expect(f.forms.mutate.mock.calls.map(([, , expected]) => expected)).toEqual([0, 0, 1])
    expect(f.state.user).toMatchObject({
      memoryView: { strategyTypeId: 'scoped' }, conversationInteraction: { turnBar: false },
    })
  })

  it('rebases an ordinary Core edit over View, migration marker and UI changes', async () => {
    const f = await fixture()
    const revision = f.read().revision
    f.external([
      { op: 'set', path: ['memoryView', 'strategyTypeId'], value: 'scoped' },
      { op: 'set', path: ['legacySettingsImported'], value: true },
      { op: 'set', path: ['conversationInteraction', 'saveAction'], value: false },
    ])
    await f.settings.mutate('mnemon', coreWrite, revision)
    expect(f.forms.mutate.mock.calls.map(([, , expected]) => expected)).toEqual([0, 1])
    expect(f.state.user).toMatchObject({
      defaultRecallLimit: 20, legacySettingsImported: true,
      memoryView: { strategyTypeId: 'scoped' }, conversationInteraction: { saveAction: false },
    })
  })

  const namespaceCases = [
    { namespace: 'mnemon', changedPath: ['timeoutMs'], changedValue: 35000, write: coreWrite },
    { namespace: 'mnemon-ui', changedPath: ['conversationInteraction', 'saveAction'], changedValue: false, write: uiWrite },
    { namespace: viewNamespace, changedPath: ['memoryView', 'entries'], changedValue: { 'include:strategy': { enabled: false, config: {} } }, write: viewWrite },
  ]
  it.each(namespaceCases.flatMap(test => (['value', 'base', 'user'] as const).map(layer => ({ ...test, layer }))))(
    'rejects a genuine $namespace conflict in $layer even when the edited field is unchanged', async test => {
      const f = await fixture()
      const revision = f.read(test.namespace).revision
      f.external([{ op: 'set', path: test.changedPath, value: test.changedValue }], [test.layer])
      const before = structuredClone({ value: f.state.value, base: f.state.base, user: f.state.user })
      await expect(f.settings.mutate(test.namespace, test.write, revision)).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT' })
      expect(f.forms.mutate).toHaveBeenCalledTimes(1)
      expect({ value: f.state.value, base: f.state.base, user: f.state.user }).toEqual(before)
      expect(f.state.revision).toBe(1)
    },
  )

  it.each(['nested', 'whole', 'mixed'] as const)('protects legacy Core interaction writes after a UI change (%s)', async kind => {
    const f = await fixture()
    const revision = f.read().revision
    f.external([{ op: 'set', path: ['conversationInteraction', 'saveAction'], value: false }])
    const operations: SettingsOperation[] = kind === 'whole'
      ? [{ op: 'set', path: ['conversationInteraction'], value: { turnBar: false, saveAction: true } }]
      : [{ op: 'set', path: ['conversationInteraction', 'turnBar'], value: false }, ...(kind === 'mixed' ? coreWrite : [])]
    await expect(f.settings.mutate('mnemon', operations, revision)).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT' })
    expect(f.forms.mutate).toHaveBeenCalledTimes(1)
    expect(f.state.user.conversationInteraction).toEqual({ toolviews: false, turnBar: true, saveAction: false })
    expect(f.state.user).not.toHaveProperty('defaultRecallLimit')
  })

  it('refuses to rebase a revision the facade never observed', async () => {
    const f = await fixture()
    f.external([{ op: 'set', path: ['memoryView', 'strategyTypeId'], value: 'scoped' }])
    await expect(f.settings.mutate('mnemon-ui', uiWrite, 0)).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT' })
    expect(f.forms.mutate).toHaveBeenCalledTimes(1)
    expect(f.state.user.conversationInteraction).toMatchObject({ turnBar: true })
  })

  it.each([63, 64])('keeps a bounded history after %i newer native snapshots', async newerSnapshots => {
    const f = await fixture()
    const revision = f.read('mnemon-ui').revision
    for (let index = 0; index < newerSnapshots; index += 1) {
      f.external([{ op: 'set', path: ['memoryView', 'strategyTypeId'], value: index % 2 === 0 ? 'scoped' : 'light-context' }])
      f.read(viewNamespace)
    }
    const write = f.settings.mutate('mnemon-ui', uiWrite, revision)
    if (newerSnapshots === 63) {
      await write
      expect(f.forms.mutate.mock.calls.map(([, , expected]) => expected)).toEqual([0, 63])
      expect(f.state.user.conversationInteraction).toMatchObject({ turnBar: false })
    } else {
      await expect(write).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT' })
      expect(f.forms.mutate).toHaveBeenCalledTimes(1)
      expect(f.state.user.conversationInteraction).toMatchObject({ turnBar: true })
    }
  })

  it('preserves non-conflict native errors without retrying', async () => {
    const f = await fixture()
    const revision = f.read('mnemon-ui').revision
    f.external([{ op: 'set', path: ['memoryView', 'strategyTypeId'], value: 'scoped' }])
    const failure = Object.assign(new Error('Synthetic profile write failed'), { code: 'EACCES' })
    f.state.failure = failure
    await expect(f.settings.mutate('mnemon-ui', uiWrite, revision)).rejects.toBe(failure)
    expect(f.forms.mutate).toHaveBeenCalledTimes(1)
    expect(f.state.revision).toBe(1)
  })

  it.each([2, 3])('allows at most two retries when unrelated settings keep advancing (%i conflicts)', async conflicts => {
    const f = await fixture()
    const revision = f.read('mnemon-ui').revision
    let attempts = 0
    f.state.beforeAttempt = () => {
      if (attempts++ < conflicts) f.external([
        { op: 'set', path: ['memoryView', 'strategyTypeId'], value: attempts % 2 === 0 ? 'light-context' : 'scoped' },
      ])
    }
    const write = f.settings.mutate('mnemon-ui', uiWrite, revision)
    if (conflicts === 2) {
      await write
      expect(f.state.user.conversationInteraction).toMatchObject({ turnBar: false })
    } else {
      await expect(write).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT', expected: 2, actual: 3 })
      expect(f.state.user.conversationInteraction).toMatchObject({ turnBar: true })
    }
    expect(f.forms.mutate.mock.calls.map(([, , expected]) => expected)).toEqual([0, 1, 2])
    expect(f.state.revision).toBe(3)
  })

  it('stops retrying if this namespace changes while an unrelated conflict is being retried', async () => {
    const f = await fixture()
    const revision = f.read('mnemon-ui').revision
    let attempts = 0
    f.state.beforeAttempt = () => f.external([attempts++ === 0
      ? { op: 'set', path: ['memoryView', 'strategyTypeId'], value: 'scoped' }
      : { op: 'set', path: ['conversationInteraction', 'saveAction'], value: false }])
    await expect(f.settings.mutate('mnemon-ui', uiWrite, revision)).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT' })
    expect(f.forms.mutate.mock.calls.map(([, , expected]) => expected)).toEqual([0, 1])
    expect(f.state.user.conversationInteraction).toEqual({ toolviews: false, turnBar: true, saveAction: false })
  })

  it.each(['value', 'base', 'user'] as const)('compares unredacted %s secrets while returning only redacted descriptors', async layer => {
    const f = await fixture()
    const before = f.read('mnemon', true)
    expect(JSON.stringify(before)).not.toContain(secret)
    expect(before[layer]).toMatchObject({ embedding: { apiKey: '[redacted]' } })
    f.external([{ op: 'set', path: ['embedding', 'apiKey'], value: replacementSecret }], [layer])
    const after = f.read('mnemon', true)
    expect({ ...after, revision: before.revision }).toEqual(before)
    expect(JSON.stringify(after)).not.toContain(replacementSecret)
    await expect(f.settings.mutate('mnemon', coreWrite, before.revision)).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT' })
    expect(f.forms.mutate).toHaveBeenCalledTimes(1)
    expect(f.state[layer]).toMatchObject({ embedding: { apiKey: replacementSecret } })
  })

  it('allows UI changes over an unrelated secret update without exposing that secret', async () => {
    const f = await fixture()
    const revision = f.read('mnemon-ui', true).revision
    f.external([{ op: 'set', path: ['embedding', 'apiKey'], value: replacementSecret }])
    await f.settings.mutate('mnemon-ui', uiWrite, revision)
    expect(f.forms.mutate.mock.calls.map(([, , expected]) => expected)).toEqual([0, 1])
    expect(f.state.value).toMatchObject({ embedding: { apiKey: replacementSecret }, conversationInteraction: { turnBar: false } })
    const described = f.settings.describe({ redactSecrets: true })
    expect(JSON.stringify(described)).not.toContain(secret)
    expect(JSON.stringify(described)).not.toContain(replacementSecret)
  })

  it('retains snapshots independently of mutable objects returned by native Forms', async () => {
    const f = await fixture()
    const revision = f.read().revision
    f.state.value.timeoutMs = 35000
    f.state.revision += 1
    await expect(f.settings.mutate('mnemon', coreWrite, revision)).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT' })
    expect(f.forms.mutate).toHaveBeenCalledTimes(1)
    expect(f.state.value.timeoutMs).toBe(35000)
  })

  it('fails closed when the native descriptor disappears after a conflict', async () => {
    const f = await fixture()
    const revision = f.read('mnemon-ui').revision
    f.external([{ op: 'set', path: ['memoryView', 'strategyTypeId'], value: 'scoped' }])
    f.state.visible = false
    await expect(f.settings.mutate('mnemon-ui', uiWrite, revision)).rejects.toMatchObject({ code: 'SETTINGS_CONFLICT' })
    expect(f.forms.mutate).toHaveBeenCalledTimes(1)
  })

  it.each(['disposed', 'replaced', 'same-id-owner', 'read-only'] as const)(
    'does not retry after the owner becomes %s during the native conflict', async reason => {
      const f = await fixture()
      const revision = f.read('mnemon-ui').revision
      f.forms.describe.mockClear()
      f.state.beforeAttempt = async () => {
        f.state.beforeAttempt = undefined
        f.external([{ op: 'set', path: ['memoryView', 'strategyTypeId'], value: 'scoped' }])
        if (reason === 'disposed') await f.fiber.dispose()
        else if (reason === 'read-only') f.forms.writable = false
        else {
          const id = reason === 'same-id-owner' ? f.entry.options.id : 'replacement-mnemon'
          f.state.entries = [{ id: `include:${id}`, options: { id, config: structuredClone(f.state.user) } }]
        }
      }
      await expect(f.settings.mutate('mnemon-ui', uiWrite, revision)).rejects.toThrow('read-only')
      expect(f.forms.mutate).toHaveBeenCalledTimes(1)
      expect(f.forms.describe).not.toHaveBeenCalled()
      expect(f.state.revision).toBe(1)
      expect(f.state.user.conversationInteraction).toMatchObject({ turnBar: true })
      if (reason !== 'read-only') {
        // A remounted same-id entry may still have a native descriptor. Neither
        // ordinary nor redacted reads may expose it through the retired facade.
        expect(f.settings.describe()).toEqual([])
        expect(f.settings.describe({ redactSecrets: true })).toEqual([])
        expect(f.forms.describe).not.toHaveBeenCalled()
      }
    },
  )

  it('does not invent an expected revision when the caller omitted one', async () => {
    const f = await fixture()
    f.read('mnemon-ui')
    const failure = Object.assign(new Error('Synthetic native conflict'), { code: 'SETTINGS_CONFLICT' })
    f.state.failure = failure
    await expect(f.settings.mutate('mnemon-ui', uiWrite)).rejects.toBe(failure)
    expect(f.forms.mutate.mock.calls.map(([, , expected]) => expected)).toEqual([undefined])
  })
})
