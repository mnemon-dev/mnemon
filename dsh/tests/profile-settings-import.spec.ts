import { Context, type Fiber } from '@deepseek-ai/cordis'
import { mkdtempSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { parse, parseDocument, stringify } from 'yaml'
import { Config, InteractionConfig, resolveConfig } from '../src/host/config.ts'
import type { HostContextShape, HostSettingsService } from '../src/host/dsh.ts'
import { plainHostConfig } from '../src/host/live-config.ts'
import { ProfileMnemonSettings } from '../src/host/settings-service.ts'
import { legacyPreferences, legacySchema, preferences, schema as ViewSchema } from '../src/host/view-preferences.ts'

const viewNamespace = 'mnemon-view-0123456789abcdef'
const legacyNamespace = 'mnemon-plugins-0123456789abcdef'
const light = 'mnemon-strategy-light-context'
const documents = 'mnemon-source-documents'
const expressionTag = { tag: 'tag:yaml.org,2002:js', resolve: (value: string) => ({ __jsExpr: value }) }
type Row = { id: string; config?: Record<string, unknown>; [key: string]: unknown }
type Entry = { id: string; options: { id: string; config: Record<string, unknown> }; fiber?: Fiber }
type Mounted = { ctx: Context; fiber: Fiber; entry: Entry; settings: ProfileMnemonSettings }
type Files = { home: string; source: string; backup: string; patch: string }
const directories: string[] = []
const roots: Context[] = []

afterEach(async () => {
  for (const root of roots.splice(0).reverse()) await root.fiber.dispose()
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true })
  vi.restoreAllMocks()
})

function files(options: { source?: string; backup?: string; patch?: string } = {}): Files {
  const home = mkdtempSync(join(tmpdir(), 'mnemon-profile-settings-import-'))
  directories.push(home)
  const profile = join(home, 'profiles', 'web')
  mkdirSync(profile, { recursive: true })
  const result = { home, source: join(home, 'settings.yaml'), backup: join(home, 'settings.yaml.imported'), patch: join(profile, 'cordis.patch.yml') }
  writeFileSync(result.patch, options.patch ?? '# Existing profile patch\n[]\n')
  if (options.source !== undefined) writeFileSync(result.source, options.source)
  if (options.backup !== undefined) writeFileSync(result.backup, options.backup)
  return result
}

function rows(disk: Files): Row[] {
  return parse(readFileSync(disk.patch, 'utf8'), { customTags: [expressionTag] }) as Row[]
}

function override(disk: Files): Record<string, unknown> {
  return rows(disk).findLast(row => row.id === 'mnemon' && row.config !== undefined)?.config ?? {}
}

function writeOverride(disk: Files, config: Record<string, unknown>): void {
  const values = rows(disk)
  // The public editor retains existing expression tags as scalar AST nodes.
  const document = parseDocument(readFileSync(disk.patch, 'utf8'), {
    customTags: [{ tag: expressionTag.tag, resolve: (value: string) => value }],
  })
  const index = values.findLastIndex(row => row.id === 'mnemon')
  if (index === -1) document.add({ id: 'mnemon', config })
  else document.setIn([index, 'config'], document.createNode(config))
  writeFileSync(disk.patch, String(document))
}

/** Real facade and files; only the published ConfigEditor/Forms boundary is mocked. */
function host(disk: Files) {
  const root = new Context()
  roots.push(root)
  const inherited = { timeoutMs: 10000, defaultRecallLimit: 10 }
  const state = {
    writable: true,
    revision: 0,
    entries: [] as Entry[],
    beforeEdit: undefined as (() => void | Promise<void>) | undefined,
    failure: undefined as Error | undefined,
  }
  const forms = {
    get writable() { return state.writable },
    configure: vi.fn((_presentation: { auto: boolean }, _owner: unknown) => vi.fn()),
    describe: vi.fn((): ReturnType<HostSettingsService['describe']> => state.entries.map(entry => ({
      ns: entry.options.id,
      value: plainHostConfig(entry.fiber?.config), base: Config(inherited), user: override(disk),
      revision: state.revision, applies: 'live',
    }))),
    mutate: vi.fn(async () => { throw new Error('Import must use ConfigEditor, not form mutation') }),
  }
  const editor = {
    documentPath: disk.patch,
    entries: () => state.entries,
    configuration: () => state.entries.map(entry => ({ entry, inherited: structuredClone(inherited), override: override(disk) })),
    edit: vi.fn(async (entry: Entry, change: (current: Record<string, unknown>, base: Record<string, unknown>) => Record<string, unknown>) => {
      await state.beforeEdit?.()
      if (!state.entries.includes(entry) || entry.fiber === undefined) throw new Error('Configuration Entry is no longer available')
      // The real editor reconciles the current profile inside its lock before
      // invoking the caller's callback, then validates before touching disk.
      const current = rows(disk).some(row => row.id === entry.options.id && row.config !== undefined) ? override(disk) : inherited
      entry.options.config = structuredClone(current)
      entry.fiber.config = Config(current)
      const next = change(structuredClone(current), structuredClone(inherited))
      const candidate = root.events.waterfall(entry.fiber, 'internal/config', next, () => next)
      const config = Config(plainHostConfig(candidate))
      resolveConfig(config)
      if (state.failure !== undefined) throw state.failure
      writeOverride(disk, next)
      entry.options.config = structuredClone(next)
      entry.fiber.config = config
      state.revision += 1
    }),
  }
  root.provide('settings', forms)
  root.provide('configEditor', editor)
  root.provide('profileContext', { home: disk.home })

  async function mount(): Promise<Mounted> {
    const raw = rows(disk).some(row => row.id === 'mnemon' && row.config !== undefined) ? override(disk) : inherited
    const entry: Entry = { id: 'include:mnemon', options: { id: 'mnemon', config: structuredClone(raw) } }
    state.entries.push(entry)
    let ctx!: Context
    let settings!: ProfileMnemonSettings
    const fiber = await root.plugin({ inject: ['settings'], apply(child: Context) {
      ctx = child
      Object.assign(child.fiber, { entry })
      child.fiber.config = Config(raw)
      entry.fiber = child.fiber
      settings = new ProfileMnemonSettings(child as unknown as HostContextShape, child.fiber.config)
      settings.register('mnemon', Config, { applies: 'live', validate: resolveConfig })
      settings.register('mnemon-ui', InteractionConfig, { applies: 'live' })
      settings.register(viewNamespace, ViewSchema, { applies: 'live', validate: preferences })
      settings.register(legacyNamespace, legacySchema, { applies: 'live', validate: legacyPreferences })
    } })
    return { ctx, fiber, entry, settings }
  }

  async function unmount(target: Mounted): Promise<void> {
    await target.fiber.dispose()
    state.entries = state.entries.filter(entry => entry !== target.entry)
  }

  return { root, state, forms, editor, mount, unmount }
}

function backup(): string {
  return '# Retained synthetic backup; preserve these bytes.\n' + stringify({
    mnemon: { timeoutMs: 25000, conversationInteraction: { turnBar: true, saveAction: true } },
    'mnemon-ui': { turnBar: false, saveAction: false },
    [viewNamespace]: { strategyTypeId: 'default-three-tier', entries: { [light]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
    [legacyNamespace]: { sources: { [documents]: { enabled: false } } },
    'mnemon-view-fedcba9876543210': { entries: { untouched: { enabled: true, config: {} } } },
  })
}

describe('profile settings recovery from retained legacy YAML', () => {
  it('imports through the real facade and leaves every backup byte unchanged', async () => {
    const disk = files({ backup: backup() })
    const original = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    await target.settings.importLegacy([documents])
    expect(f.editor.edit).toHaveBeenCalledTimes(1)
    expect(f.forms.mutate).not.toHaveBeenCalled()
    expect(override(disk)).toEqual({
      timeoutMs: 25000, defaultRecallLimit: 10,
      conversationInteraction: { turnBar: false, saveAction: false },
      memoryView: { strategyTypeId: 'default-three-tier', entries: {
        [documents]: { enabled: false, config: {} }, [light]: { enabled: true, config: { maxProjectionCharacters: 700 } },
      } },
      legacySettingsImported: true,
    })
    expect(target.settings.describe().find(item => item.ns === 'mnemon-ui')).toMatchObject({
      value: { turnBar: false, saveAction: false }, revision: 1,
    })
    expect(readFileSync(disk.backup)).toEqual(original)
    expect(override(disk).memoryView).not.toHaveProperty('entries.untouched')
  })

  it('preserves current root choices, legitimate strategy patch rows and independent Source config', async () => {
    const existingRows: Row[] = [
      { id: 'mnemon', config: { timeoutMs: 12000, conversationInteraction: { turnBar: true } } },
      { id: light, disabled: false, config: { maxProjectionCharacters: 1200 } },
      { id: documents, disabled: true, config: { dataDir: '/synthetic/independent-documents' } },
      { id: 'unrelated-plugin', disabled: { __jsExpr: 'profile.enabled' }, config: { unrelated: true } },
    ]
    const disk = files({ backup: backup(), patch: '# Preserve existing profile choices\n' + stringify(existingRows) })
    const original = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    await target.settings.importLegacy([documents])
    expect(override(disk)).toMatchObject({
      timeoutMs: 12000,
      conversationInteraction: { turnBar: true, saveAction: false },
      memoryView: { entries: {
        [light]: { enabled: true, config: { maxProjectionCharacters: 1200 } },
        [documents]: { enabled: false, config: {} },
      } },
      legacySettingsImported: true,
    })
    expect(rows(disk).filter(row => row.id !== 'mnemon')).toEqual(existingRows.slice(1))
    expect(readFileSync(disk.patch, 'utf8')).toContain('# Preserve existing profile choices')
    expect(readFileSync(disk.backup)).toEqual(original)
  })

  it('maps profile row ids to the unique full Loader ids before applying explicit plugin choices', async () => {
    const fullLight = `include:${light}`
    const fullDocuments = `include:${documents}`
    const existingRows: Row[] = [
      { id: light, disabled: true, config: { maxProjectionCharacters: 1200 } },
      { id: documents, disabled: false, config: { dataDir: '/synthetic/independent-documents' } },
      { id: 'unrelated-plugin', disabled: false, config: { unrelated: true } },
    ]
    const disk = files({
      patch: '# Existing id-directed plugin choices\n' + stringify(existingRows),
      backup: stringify({
        [viewNamespace]: { entries: { [fullLight]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
        [legacyNamespace]: { sources: { [fullDocuments]: { enabled: false } } },
      }),
    })
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    f.state.entries.push(
      { id: fullLight, options: { id: light, config: { maxProjectionCharacters: 1200 } } },
      { id: fullDocuments, options: { id: documents, config: { dataDir: '/synthetic/independent-documents' } } },
    )
    await target.settings.importLegacy([fullDocuments])
    expect(override(disk)).toMatchObject({
      memoryView: { entries: {
        [fullLight]: { enabled: false, config: { maxProjectionCharacters: 1200 } },
        [fullDocuments]: { enabled: true, config: {} },
      } },
      legacySettingsImported: true,
    })
    expect((override(disk).memoryView as { entries: object }).entries).not.toHaveProperty(light)
    expect((override(disk).memoryView as { entries: object }).entries).not.toHaveProperty(documents)
    expect(rows(disk).filter(row => row.id !== 'mnemon')).toEqual(existingRows)
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
  })

  it.each([
    '  config: !!js profile.strategyConfig\n',
    '  config:\n    maxProjectionCharacters: !!js profile.lightLimit\n',
    '  config:\n    rules:\n      - options: !!js profile.strategyOptions\n',
  ])('does not freeze tagged strategy expressions into a recovered View (%#)', async configYaml => {
    const fullLight = `include:${light}`
    const disk = files({
      patch: `# Keep the native strategy expression\n- id: ${light}\n  disabled: false\n${configYaml}`,
      backup: stringify({
        'mnemon-ui': { turnBar: false },
        [viewNamespace]: { entries: {
          [fullLight]: { enabled: false, config: { maxProjectionCharacters: 700 } },
          'include:other-strategy': { enabled: true, config: { limit: 12 } },
        } },
      }),
    })
    const originalRows = rows(disk)
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    f.state.entries.push({ id: fullLight, options: { id: light, config: { maxProjectionCharacters: 900 } } })
    await target.settings.importLegacy([])
    expect(f.editor.edit).toHaveBeenCalledTimes(1)
    expect(override(disk)).toMatchObject({
      conversationInteraction: { turnBar: false },
      memoryView: { entries: { 'include:other-strategy': { enabled: true, config: { limit: 12 } } } },
      legacySettingsImported: true,
    })
    expect((override(disk).memoryView as { entries: object }).entries).not.toHaveProperty(fullLight)
    expect(rows(disk).filter(row => row.id !== 'mnemon')).toEqual(originalRows)
    expect(readFileSync(disk.patch, 'utf8')).toContain(configYaml.trimEnd())
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
  })

  it('preserves Source and unrelated tagged expressions while migrating literal preferences', async () => {
    const fullDocuments = `include:${documents}`
    const disk = files({
      patch: `- id: ${documents}\n  disabled: false\n  config:\n    dataDir: !!js profile.documentsPath\n- id: unrelated-plugin\n  config: !!js profile.unrelatedConfig\n`,
      backup: stringify({
        [legacyNamespace]: { sources: { [fullDocuments]: { enabled: false } } },
        [viewNamespace]: { entries: { [light]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
      }) + 'mnemon-view-fedcba9876543210:\n  entries:\n    foreign-strategy:\n      enabled: true\n      config: !!js profile.foreignConfig\n',
    })
    const originalRows = rows(disk)
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    f.state.entries.push({ id: fullDocuments, options: { id: documents, config: { dataDir: '/synthetic/evaluated-source' } } })
    await target.settings.importLegacy([fullDocuments])
    expect(f.editor.edit).toHaveBeenCalledTimes(1)
    expect((override(disk).memoryView as { entries: object }).entries).toEqual({
      [fullDocuments]: { enabled: true, config: {} },
      [light]: { enabled: true, config: { maxProjectionCharacters: 700 } },
    })
    expect(override(disk).legacySettingsImported).toBe(true)
    expect(rows(disk).filter(row => row.id !== 'mnemon')).toEqual(originalRows)
    expect(readFileSync(disk.patch, 'utf8')).toContain('dataDir: !!js profile.documentsPath')
    expect(readFileSync(disk.patch, 'utf8')).toContain('config: !!js profile.unrelatedConfig')
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
  })

  it.each([
    'mnemon:\n  dataDir: !!js profile.memoryPath\n',
    'mnemon-ui:\n  turnBar: !!js profile.turnBar\n',
    `${viewNamespace}:\n  entries:\n    ${light}:\n      enabled: true\n      config:\n        maxProjectionCharacters: !!js profile.lightLimit\n`,
    `${legacyNamespace}:\n  sources:\n    ${documents}:\n      enabled: !!js profile.documentsEnabled\n`,
  ])('rejects a tagged expression anywhere in relevant retained settings without marking import complete (%#)', async legacyYaml => {
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const disk = files({ backup: legacyYaml })
    const originalPatch = readFileSync(disk.patch)
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    await target.settings.importLegacy([documents])
    expect(f.editor.edit).not.toHaveBeenCalled()
    expect(warning).toHaveBeenCalledWith(expect.stringContaining('retained backup requires review'))
    expect(readFileSync(disk.patch)).toEqual(originalPatch)
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
    expect(override(disk)).not.toHaveProperty('legacySettingsImported')
  })

  it('rejects ambiguous profile-to-Loader id mappings before writing any recovered settings', async () => {
    const fullLight = `include:${light}`
    const disk = files({
      patch: stringify([{ id: light, disabled: true, config: { maxProjectionCharacters: 1200 } }]),
      backup: stringify({
        mnemon: { timeoutMs: 25000 },
        [viewNamespace]: { entries: { [fullLight]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
      }),
    })
    const originalPatch = readFileSync(disk.patch)
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    f.state.entries.push(
      { id: fullLight, options: { id: light, config: {} } },
      { id: `another-include:${light}`, options: { id: light, config: {} } },
    )
    await expect(target.settings.importLegacy([])).rejects.toThrow(/ambiguous/iu)
    expect(f.editor.edit).not.toHaveBeenCalled()
    expect(readFileSync(disk.patch)).toEqual(originalPatch)
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
    expect(override(disk)).not.toHaveProperty('legacySettingsImported')
  })

  it('does not reactivate old choices after an explicit empty memoryView override', async () => {
    const current = { memoryView: { strategyTypeId: 'default-three-tier', entries: {} }, conversationInteraction: { turnBar: true, saveAction: true } }
    const disk = files({ backup: backup(), patch: stringify([{ id: 'mnemon', config: current }]) })
    const f = host(disk)
    const target = await f.mount()
    await target.settings.importLegacy([documents])
    expect(override(disk)).toMatchObject({ ...current, legacySettingsImported: true })
  })

  it('replans inside the editor callback and preserves an intervening explicit update', async () => {
    const disk = files({ backup: backup() })
    const f = host(disk)
    const target = await f.mount()
    const newer = { timeoutMs: 30000, conversationInteraction: { turnBar: true, saveAction: true }, memoryView: { entries: {} } }
    f.state.beforeEdit = () => { writeOverride(disk, newer) }
    await target.settings.importLegacy([documents])
    expect(override(disk)).toMatchObject({ ...newer, legacySettingsImported: true })
  })

  it.each([
    { mnemon: { timeoutMs: 25000 }, [viewNamespace]: { entries: { [light]: { enabled: true, config: [] } } } },
    { mnemon: { timeoutMs: 25000 }, 'mnemon-ui': { turnBar: 'false' } },
    JSON.parse('{"mnemon":{"embedding":{"__proto__":{"unsafe":true}}}}'),
  ])('rejects a damaged relevant section as a whole without changing the patch or backup (%#)', async sections => {
    const warning = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const disk = files({ backup: stringify(sections) })
    const originalPatch = readFileSync(disk.patch)
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    await target.settings.importLegacy([documents])
    expect(f.editor.edit).not.toHaveBeenCalled()
    expect(warning).toHaveBeenCalledWith(expect.stringContaining('retained backup requires review'))
    expect(readFileSync(disk.patch)).toEqual(originalPatch)
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
    expect(override(disk)).not.toHaveProperty('legacySettingsImported')
  })

  it('rejects syntactically broken YAML without marking or deleting the retained backup', async () => {
    const disk = files({ backup: 'mnemon: [unterminated\n' })
    const originalPatch = readFileSync(disk.patch)
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    await expect(target.settings.importLegacy([documents])).rejects.toThrow()
    expect(f.editor.edit).not.toHaveBeenCalled()
    expect(readFileSync(disk.patch)).toEqual(originalPatch)
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
  })

  it('retains both files when schema validation rejects otherwise well-shaped legacy data', async () => {
    const disk = files({ backup: stringify({ mnemon: { timeoutMs: 1 } }) })
    const originalPatch = readFileSync(disk.patch)
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    await expect(target.settings.importLegacy([documents])).rejects.toThrow()
    expect(readFileSync(disk.patch)).toEqual(originalPatch)
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
    expect(plainHostConfig(target.fiber.config).legacySettingsImported).toBeUndefined()
  })

  it('coalesces concurrent recovery and remains idempotent after a cold restart', async () => {
    const disk = files({ backup: backup() })
    const f = host(disk)
    const target = await f.mount()
    await Promise.all([target.settings.importLegacy([documents]), target.settings.importLegacy([documents])])
    await target.settings.importLegacy([documents])
    expect(f.editor.edit).toHaveBeenCalledTimes(1)
    const committed = readFileSync(disk.patch)
    await f.unmount(target)
    const restarted = host(disk)
    const next = await restarted.mount()
    await next.settings.importLegacy([documents])
    expect(restarted.editor.edit).not.toHaveBeenCalled()
    expect(readFileSync(disk.patch)).toEqual(committed)
  })

  it('keeps a failed write retryable without persisting an import marker', async () => {
    const disk = files({ backup: backup() })
    const originalPatch = readFileSync(disk.patch)
    const originalBackup = readFileSync(disk.backup)
    const f = host(disk)
    const target = await f.mount()
    f.state.failure = new Error('Synthetic disk write failed')
    await expect(target.settings.importLegacy([documents])).rejects.toThrow('Synthetic disk write failed')
    expect(readFileSync(disk.patch)).toEqual(originalPatch)
    expect(plainHostConfig(target.fiber.config).legacySettingsImported).toBeUndefined()
    f.state.failure = undefined
    await target.settings.importLegacy([documents])
    expect(f.editor.edit).toHaveBeenCalledTimes(2)
    expect(override(disk).legacySettingsImported).toBe(true)
    expect(readFileSync(disk.backup)).toEqual(originalBackup)
  })

  it('keeps the native-import gate across remounts and recovers only in a new root', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const source = '# Original source, renamed only by the simulated native importer\n' + stringify({
      mnemon: { timeoutMs: 25000 }, 'mnemon-ui': { turnBar: false, saveAction: false },
      [viewNamespace]: { entries: { [light]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
    })
    const disk = files({ source })
    const originalPatch = readFileSync(disk.patch)
    const f = host(disk)
    const first = await f.mount()
    await first.settings.importLegacy([documents])
    expect(f.editor.edit).not.toHaveBeenCalled()
    expect(readFileSync(disk.source, 'utf8')).toBe(source)
    expect(readFileSync(disk.patch)).toEqual(originalPatch)

    // DSH renames before finishing all sections. Even a fresh adapter in this
    // root must not misidentify that in-flight import as a previous startup.
    renameSync(disk.source, disk.backup)
    await f.unmount(first)
    const remounted = await f.mount()
    await remounted.settings.importLegacy([documents])
    expect(f.editor.edit).not.toHaveBeenCalled()
    expect(readFileSync(disk.patch)).toEqual(originalPatch)
    expect(override(disk)).not.toHaveProperty('legacySettingsImported')

    // Finish the native root-section import, then simulate a cold Host start.
    writeOverride(disk, { timeoutMs: 25000 })
    await f.unmount(remounted)
    const restarted = host(disk)
    const recovered = await restarted.mount()
    await recovered.settings.importLegacy([documents])
    expect(restarted.editor.edit).toHaveBeenCalledTimes(1)
    expect(override(disk)).toMatchObject({
      timeoutMs: 25000, conversationInteraction: { turnBar: false, saveAction: false },
      memoryView: { entries: { [light]: { enabled: true, config: { maxProjectionCharacters: 700 } } } },
      legacySettingsImported: true,
    })
    expect(readFileSync(disk.backup, 'utf8')).toBe(source)
  })

  it.each(['missing-backup', 'read-only'] as const)('does not write when recovery is %s', async reason => {
    const disk = files(reason === 'read-only' ? { backup: backup() } : {})
    const originalPatch = readFileSync(disk.patch)
    const f = host(disk)
    const target = await f.mount()
    if (reason === 'read-only') f.state.writable = false
    await target.settings.importLegacy([documents])
    expect(f.editor.edit).not.toHaveBeenCalled()
    expect(readFileSync(disk.patch)).toEqual(originalPatch)
  })
})
