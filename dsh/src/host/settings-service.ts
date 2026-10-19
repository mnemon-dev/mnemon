import { readFile, stat } from 'node:fs/promises'
import { existsSync, readFileSync } from 'node:fs'
import { canonicalMemoryJson } from '../core/definitions.ts'
import { join } from 'node:path'
import { parseDocument } from 'yaml'
import type { HostContextShape, HostSettingsScope, HostSettingsService } from './dsh.ts'
import type { SettingsOperation } from './protocol.ts'
import { plainHostConfig } from './live-config.ts'
import { planLegacySettingsImport } from './legacy-settings-import.ts'

type NativeDescriptor = ReturnType<HostSettingsService['describe']>[number]
type SettingsListener = (namespace: string, value: unknown) => void
type Registration = {
  namespace: string
  field?: 'conversationInteraction' | 'memoryView'
  legacy: boolean
  schema: (value: unknown) => unknown
  base: unknown
  validate?: (value: never) => void
}

interface ProfileEntry {
  id?: string
  options: { id: string; config?: unknown }
}

interface ProfileFiber {
  entry?: ProfileEntry
  config: unknown
}

interface ProfileEditor {
  documentPath: string
  entries(): ProfileEntry[]
  configuration(): Array<{ entry: ProfileEntry; inherited: Record<string, unknown>; override: Record<string, unknown> }>
  edit(entry: ProfileEntry, change: (current: Record<string, unknown>, inherited: Record<string, unknown>) => Record<string, unknown>): Promise<void>
}

interface ProfileForms {
  readonly writable: boolean
  configure(presentation: { auto: boolean }, owner: ProfileFiber): () => void
  describe(options?: { redactSecrets?: boolean }): ReturnType<HostSettingsService['describe']>
  mutate(namespace: string, operations: SettingsOperation[], expectedRevision?: number): Promise<void>
}

// A native import can remount this plugin; only a new host may clear the gate.
const pendingNativeImports = new WeakSet<object>()
const clone = <T>(value: T): T => structuredClone(value)
const object = (value: unknown): Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}

// Preserve DSH expressions as data; never evaluate or flatten them into strings.
function profileYaml(source: string): unknown {
  const document = parseDocument(source, { customTags: [{
    tag: 'tag:yaml.org,2002:js', resolve: (value: string) => ({ __jsExpr: value }),
  }] })
  if (document.errors.length > 0 || document.warnings.length > 0) throw new Error('Invalid or unsupported legacy/profile YAML; retained backup was not changed')
  return document.toJS({ maxAliasCount: 100 })
}

/** A local facade preserves Mnemon's wire namespaces; DSH owns all persistence. */
export class ProfileMnemonSettings implements HostSettingsService {
  private readonly registrations = new Map<string, Registration>()
  private readonly listeners = new Set<SettingsListener>()
  private readonly revisions = new Map<number, NativeDescriptor>()
  private readonly owner: ProfileFiber | undefined
  private readonly forms: ProfileForms
  private readonly editor: ProfileEditor | undefined
  private closed = false
  private importing: Promise<void> | undefined
  private readonly nativeImportPending: boolean

  constructor(private readonly ctx: HostContextShape, private readonly input: unknown) {
    this.forms = ctx.settings as unknown as ProfileForms
    this.owner = (ctx as unknown as { fiber?: ProfileFiber }).fiber
    this.editor = ctx.get('configEditor') as ProfileEditor | undefined
    const profile = ctx.get('profileContext') as { home?: string } | undefined
    const root = (ctx as unknown as { root?: object }).root ?? ctx
    if (profile?.home !== undefined && existsSync(join(profile.home, 'settings.yaml'))) pendingNativeImports.add(root)
    this.nativeImportPending = pendingNativeImports.has(root)
    const owner = this.owner
    const validate = (candidate: unknown): void => {
      const config = plainHostConfig(candidate)
      for (const registration of this.registrations.values()) {
        if (registration.legacy) continue
        const value = this.resolve(registration, config)
        registration.validate?.(value as never)
      }
    }
    // The public Cordis waterfall is also run by ConfigEditor before it writes.
    // Scope by fiber: another plugin's config must never initialize our graph.
    ctx.on('internal/config', function (this: ProfileFiber, _raw: unknown, next: () => unknown) {
      const candidate = next()
      if (this === owner) validate(candidate)
      return candidate
    } as never, { prepend: true })
    ctx.on('loader/volatile-update', ((paths: readonly (readonly string[])[]) => {
      if (this.closed) return
      for (const registration of this.registrations.values()) {
        if (registration.legacy) continue
        const changed = paths.some(path => registration.field === undefined
          ? path.length === 0 || !['memoryView', 'legacySettingsImported'].includes(path[0]!)
          : path.length === 0 || path[0] === registration.field)
        if (!changed) continue
        const value = this.resolve(registration)
        for (const listener of this.listeners) {
          try { listener(registration.namespace, value) }
          catch (error) { console.warn('dsh-mnemon: committed settings listener failed', error) }
        }
      }
    }) as never)
    ctx.effect(() => {
      const disposePresentation = owner === undefined ? undefined : this.forms.configure({ auto: false }, owner)
      return () => {
        this.closed = true
        this.listeners.clear()
        this.revisions.clear()
        disposePresentation?.()
      }
    }, 'dsh-mnemon: profile settings')
  }

  get writable(): boolean {
    const entry = this.owner?.entry
    return !this.closed && this.forms.writable && entry !== undefined && this.editor?.entries().includes(entry) === true
  }

  onUpdated(listener: SettingsListener): () => void {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  private resolve(registration: Registration, config = plainHostConfig(this.owner?.config ?? this.input)): unknown {
    const value = registration.legacy ? registration.base : registration.field === undefined ? config : config[registration.field]
    return registration.schema(object(value))
  }

  register<T>(namespace: string, schema: unknown, options: { base?: Partial<T>; applies: 'live' | 'restart'; validate?: (value: T) => void }): HostSettingsScope<T> {
    if (this.registrations.has(namespace)) throw new Error(`Mnemon settings already registered: ${namespace}`)
    if (typeof schema !== 'function') throw new Error('Mnemon settings require a schema')
    const field = namespace === 'mnemon-ui' ? 'conversationInteraction'
      : /^mnemon-view(?:-[a-f0-9]{16})?$/u.test(namespace) ? 'memoryView' : undefined
    const legacy = /^mnemon-plugins(?:-[a-f0-9]{16})?$/u.test(namespace)
    if (namespace !== 'mnemon' && field === undefined && !legacy) throw new Error('Unsupported Mnemon settings namespace')
    const registration: Registration = { namespace, ...(field === undefined ? {} : { field }), legacy, schema: schema as Registration['schema'], base: clone(options.base ?? {}), ...(options.validate === undefined ? {} : { validate: options.validate }) }
    const value = this.resolve(registration)
    options.validate?.(value as T)
    this.registrations.set(namespace, registration)
    return { get: () => this.resolve(registration) as T }
  }

  private nativeDescriptor(): NativeDescriptor | undefined {
    if (this.closed || this.owner?.entry === undefined || !this.editor?.entries().includes(this.owner.entry)) return undefined
    const descriptor = this.forms.describe().find(item => item.ns === this.owner?.entry?.options.id)
    if (descriptor !== undefined) {
      this.revisions.set(descriptor.revision, clone({
        ns: descriptor.ns, revision: descriptor.revision, applies: descriptor.applies,
        value: descriptor.value, base: descriptor.base, user: descriptor.user,
      }))
      if (this.revisions.size > 64) this.revisions.delete(this.revisions.keys().next().value!)
    }
    return descriptor
  }

  private unchanged(previous: NativeDescriptor, current: NativeDescriptor, registration: Registration, operations: SettingsOperation[]): boolean {
    const project = (value: unknown): unknown => {
      if (registration.field !== undefined) return object(value)[registration.field]
      const { memoryView: _view, legacySettingsImported: _imported, conversationInteraction, ...core } = object(value)
      return operations.some(operation => operation.path[0] === 'conversationInteraction')
        ? { ...core, conversationInteraction } : core
    }
    const signature = (descriptor: NativeDescriptor) => canonicalMemoryJson(JSON.parse(JSON.stringify([
      project(descriptor.value) ?? null, project(descriptor.base) ?? null, project(descriptor.user) ?? null,
    ])))
    return signature(previous) === signature(current)
  }

  describe(options?: { redactSecrets?: boolean }): ReturnType<HostSettingsService['describe']> {
    const raw = this.nativeDescriptor()
    if (raw === undefined) return []
    const descriptor = options?.redactSecrets
      ? this.forms.describe(options).find(item => item.ns === this.owner?.entry?.options.id) : raw
    if (descriptor === undefined) return []
    return [...this.registrations.values()].map(registration => {
      const project = (value: unknown): unknown => registration.legacy ? {}
        : registration.field === undefined ? value : object(value)[registration.field]
      return {
        ns: registration.namespace,
        value: project(descriptor.value) ?? {},
        base: project(descriptor.base) ?? {},
        user: project(descriptor.user) ?? {},
        revision: descriptor.revision,
        applies: 'live' as const,
      }
    })
  }

  async mutate(namespace: string, operations: SettingsOperation[], expectedRevision?: number): Promise<void> {
    const registration = this.registrations.get(namespace)
    if (registration === undefined || registration.legacy) throw new Error('Unsupported Mnemon settings namespace')
    if (!this.writable) throw new Error('DSH settings are read-only')
    const translated = operations.map(operation => ({
      ...operation,
      path: [...(registration.field === undefined ? [] : [registration.field]), ...operation.path],
    }))
    let revision = expectedRevision
    const previous = revision === undefined ? undefined : this.revisions.get(revision)
    for (let attempt = 0; ; attempt += 1) {
      if (!this.writable) throw new Error('DSH settings are read-only')
      try {
        await this.forms.mutate(this.owner!.entry!.options.id, translated, revision)
        return
      } catch (error) {
        // The virtual namespaces were independent on older DSH. The new host
        // gives them one entry revision: rebase only when this namespace's
        // values and overrides still match the snapshot the caller observed.
        if (attempt >= 2 || previous === undefined || typeof error !== 'object' || error === null
          || !('code' in error) || error.code !== 'SETTINGS_CONFLICT') throw error
        if (!this.writable) throw new Error('DSH settings are read-only')
        const current = this.nativeDescriptor()
        if (current === undefined || !this.unchanged(previous, current, registration, operations)) throw error
        revision = current.revision
      }
    }
  }

  /** Read only the retained backup; ConfigEditor preserves all other patch rows. */
  async importLegacy(sourceEntryIds: readonly string[]): Promise<void> {
    if (this.importing !== undefined) return this.importing
    this.importing = this.performImport(sourceEntryIds).finally(() => { this.importing = undefined })
    return this.importing
  }

  private async performImport(sourceEntryIds: readonly string[]): Promise<void> {
    const profile = this.ctx.get('profileContext') as { home?: string } | undefined
    const entry = this.owner?.entry
    if (!this.writable || profile?.home === undefined || entry === undefined) return
    // DSH 0.1.7 exposes no completion event for its asynchronous legacy import.
    // Never race that writer: recover its retained backup on the next startup.
    if (this.nativeImportPending) {
      console.warn('dsh-mnemon: DSH is importing settings.yaml; restart once to recover retained Mnemon UI and plugin preferences')
      return
    }
    if (plainHostConfig(this.owner?.config ?? this.input).legacySettingsImported) return
    const backup = join(profile.home, 'settings.yaml.imported')
    let text: string
    try {
      if ((await stat(backup)).size > 1024 * 1024) throw new Error('Legacy settings backup exceeds the import limit')
      text = await readFile(backup, 'utf8')
    } catch (error) {
      if (typeof error === 'object' && error !== null && 'code' in error && error.code === 'ENOENT') return
      throw error
    }
    const sections: unknown = profileYaml(text)
    const viewNamespace = [...this.registrations.keys()].find(namespace => namespace.startsWith('mnemon-view'))!
    const legacyNamespace = [...this.registrations.keys()].find(namespace => namespace.startsWith('mnemon-plugins'))!
    const plan = () => {
      const configuration = this.editor!.configuration().find(item => item.entry === entry)
      if (configuration === undefined) throw new Error('Mnemon profile entry is no longer editable')
      const document: unknown = profileYaml(readFileSync(this.editor!.documentPath, 'utf8'))
      const entryOverrides = Array.isArray(document) ? document.flatMap(value => {
        const row = object(value)
        if (typeof row.id !== 'string') return []
        const entries = this.editor!.entries().filter(candidate => candidate.options.id === row.id)
        if (entries.length > 1) throw new Error('Legacy settings import requires unambiguous profile Entry ids')
        // Profile patches address local ids, whereas saved Views address the
        // complete Loader id (for example include:mnemon-strategy-scoped).
        return [{ id: entries[0]?.id ?? row.id, ...('disabled' in row ? { disabled: row.disabled } : {}), ...('config' in row ? { config: row.config } : {}) }]
      }) : []
      return planLegacySettingsImport({ entryId: entry.options.id, viewNamespace, legacyNamespace, sections, currentOverride: configuration.override, entryOverrides, sourceEntryIds })
    }
    const preview = plan()
    if (Object.keys(preview.patch).length === 0) {
      if (preview.diagnostics.length > 0) console.warn('dsh-mnemon: legacy settings were not imported; retained backup requires review')
      return
    }
    if (this.closed) return
    await this.editor!.edit(entry, current => {
      if (this.closed) throw new Error('Mnemon settings are disposed')
      // Re-read the explicit overlay inside DSH's revision/file lock.
      const next = plan()
      return { ...current, ...next.patch }
    })
  }
}
