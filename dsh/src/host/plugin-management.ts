import { createHash } from 'node:crypto'
import type {
  MemoryJsonValue,
  MemoryOperationScope,
  MemoryPluginDescriptor,
  MemoryPluginRole,
} from '../core/contracts/index.ts'
import { DEFAULT_MEMORY_VIEW_BUDGET } from '../core/contracts/index.ts'
import {
  MemoryCompositionGeneration,
  captureMemoryContributionSnapshot,
  validateMemoryPluginGraph,
} from '../core/composition.ts'
import {
  prepareMemoryContributions,
  type InstalledMemoryPlugin,
  type MemoryContributionSnapshot,
} from '../core/contributions.ts'
import { canonicalMemoryJson, defineMemoryPlugin } from '../core/definitions.ts'
import type { MemoryRuntime } from '../core/runtime.ts'
import { readMemoryStrategyConfiguration, memoryStrategyConfigurationValues as configuration, type MemoryStrategyConfiguration } from '../sdk/strategy-configuration.ts'
import type { ResolvedConfig } from './config.ts'
import type { HostContextShape, HostSettingsScope, HostSettingsService } from './dsh.ts'
import { memoryGenerationOptions } from './runtime.ts'
import {
  MNEMON_VIEW_SETTINGS_NAMESPACE,
  type MemoryPluginEntryView,
  type MemoryPluginPreference,
  type MemoryPluginSavedPreference,
  type MemoryViewConfigurationRequest,
  type MemoryViewPreferences,
} from './view-protocol.ts'
import { inspectMemoryView } from './view-presentation.ts'
import { schema, legacySchema, preferences, legacyPreferences, type LegacySourcePreferences } from './view-preferences.ts'

/** Deliberately excludes Loader.write(): generated or package YAML is never edited. */
export interface MemoryPluginLoaderEntry {
  id: string
  disabled: boolean
  options: { id: string; name: string; disabled?: unknown; config?: unknown; group?: unknown }
  fiber?: { runtime?: { callback?: unknown }; await?(): Promise<unknown> }
  parent: { tree: { import(name: string): Promise<unknown> | unknown }; ctx?: { fiber?: { entry?: { disabled: boolean } } } }
  update(options: { disabled?: boolean | null; config?: unknown }): Promise<void>
}

export interface MemoryPluginLoader {
  entries(): Iterable<MemoryPluginLoaderEntry>
  config?: { baseUrl?: string }
  ctx?: { baseUrl?: string }
  context?: { baseUrl?: string }
}

interface MemoryPluginModule {
  memoryPlugin?: MemoryPluginDescriptor
  memoryStrategyConfiguration?: MemoryStrategyConfiguration
}

/** DSH's official plugin manager; it owns enablement in the profile patch. */
interface HostPluginManager {
  setPluginEnabled(entryId: string, enabled: boolean): Promise<{ application: string; error?: { message?: string } }>
}

/** Saved choices keep only configuration once the profile patch owns enablement. */
function configurationOnly(entries: Record<string, MemoryPluginSavedPreference>): Record<string, MemoryPluginSavedPreference> {
  return Object.fromEntries(Object.entries(entries).map(([entryId, value]) => [entryId, { config: value.config }]))
}

interface ManagedPlugin {
  entry: MemoryPluginLoaderEntry
  descriptor: MemoryPluginDescriptor
  editor?: MemoryStrategyConfiguration
  value: MemoryPluginEntryView
}

const PACKAGE = /^(?:@[a-z0-9._-]+\/)?dsh-mnemon-(source|strategy)-[a-z0-9._-]+$/u
const hash = (value: unknown) => createHash('sha256').update(canonicalMemoryJson(value)).digest('hex')
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T


function contributionRoles(snapshot: MemoryContributionSnapshot, entryId: string): MemoryPluginRole[] {
  const roles = new Set<MemoryPluginRole>()
  if (snapshot.sources.some(value => value.provenance.entryId === entryId)) roles.add('source')
  if (snapshot.strategies.some(value => value.provenance.entryId === entryId)) roles.add('strategy')
  if ((snapshot.strategyExtensions ?? []).some(value => value.provenance.entryId === entryId)) roles.add('strategy-extension')
  return [...roles]
}

function fallbackDescriptor(entry: MemoryPluginLoaderEntry, snapshot: MemoryContributionSnapshot, editor?: MemoryStrategyConfiguration): MemoryPluginDescriptor {
  const sources = snapshot.sources.filter(value => value.provenance.entryId === entry.id)
  const strategies = snapshot.strategies.filter(value => value.provenance.entryId === entry.id)
  const extensions = (snapshot.strategyExtensions ?? []).filter(value => value.provenance.entryId === entry.id)
  const roles = contributionRoles(snapshot, entry.id)
  if (roles.length === 0) roles.push(editor?.kind ?? (PACKAGE.exec(entry.options.name)?.[1] === 'source' ? 'source' : 'strategy'))
  const typeId = editor?.typeId ?? strategies[0]?.definition.manifest.typeId ?? extensions[0]?.definition.manifest.typeId ?? sources[0]?.definition.manifest.typeId
  const sourceLabel = sources[0]?.definition.manifest.management?.label
  const label = editor?.label ?? { en: sourceLabel ?? typeId ?? entry.options.name, 'zh-CN': sourceLabel ?? typeId ?? entry.options.name }
  const description = editor?.description ?? { en: '', 'zh-CN': '' }
  const provides = roles.includes('source')
    ? [{ id: 'source' }, ...sources.map(source => ({ id: `source.${source.definition.manifest.role}` }))]
    : [{ id: roles.includes('strategy') ? 'strategy' : `strategy.${typeId ?? hash(entry.options.name).slice(0, 12)}` }]
  return defineMemoryPlugin({ packageName: entry.options.name, label, description, roles, provides })
}

function prepared(item: ManagedPlugin, config: Record<string, MemoryJsonValue>) {
  if (item.editor === undefined) throw new Error(`Plugin does not expose a pure configuration factory: ${item.entry.id}`)
  const contribution = item.editor.create(config)
  const values = [...(contribution.strategies ?? []), ...(contribution.strategyExtensions ?? [])]
  if (values[0]!.manifest.packageName !== item.entry.options.name) throw new Error('Plugin factory does not match its declared Entry')
  if (contribution.plugin !== undefined && hash(contribution.plugin) !== hash(item.descriptor)) throw new Error('Plugin factory descriptor does not match its module export')
  return prepareMemoryContributions({ ...contribution, plugin: contribution.plugin ?? item.descriptor }, { instanceId: item.entry.id })
}

function installedPlugin(descriptor: MemoryPluginDescriptor, entryId: string): InstalledMemoryPlugin {
  return {
    kind: 'plugin',
    instanceKey: `plugin:${entryId}`,
    provenance: { packageName: descriptor.packageName, entryId },
    descriptor,
  }
}

function orderPlugins(values: MemoryPluginEntryView[]): MemoryPluginEntryView[] {
  const byId = new Map(values.map(value => [value.entryId, value]))
  const edges = new Map(values.map(value => [value.entryId, new Set<string>()]))
  const indegree = new Map(values.map(value => [value.entryId, 0]))
  for (const dependent of values) for (const requirement of dependent.requires) {
    for (const provider of values) {
      if (provider.entryId === dependent.entryId || !provider.provides.some(capability => capability.id === requirement)) continue
      if (edges.get(provider.entryId)!.has(dependent.entryId)) continue
      edges.get(provider.entryId)!.add(dependent.entryId)
      indegree.set(dependent.entryId, indegree.get(dependent.entryId)! + 1)
    }
  }
  const stable = (left: string, right: string) => byId.get(left)!.packageName.localeCompare(byId.get(right)!.packageName) || left.localeCompare(right)
  const ready = [...indegree].filter(([, count]) => count === 0).map(([entryId]) => entryId).sort(stable)
  const ordered: MemoryPluginEntryView[] = []
  while (ready.length > 0) {
    const entryId = ready.shift()!
    ordered.push(byId.get(entryId)!)
    for (const dependent of [...edges.get(entryId)!].sort(stable)) {
      const next = indegree.get(dependent)! - 1
      indegree.set(dependent, next)
      if (next === 0) { ready.push(dependent); ready.sort(stable) }
    }
  }
  if (ordered.length < values.length) ordered.push(...values.filter(value => !ordered.includes(value)).sort((left, right) => stable(left.entryId, right.entryId)))
  return ordered
}

/**
 * Profile-local plugin graph overlay. Every dsh-mnemon Source or Strategy Entry
 * is one peer node; activation, configuration, dependency checks and rollback
 * use one transaction. Contribution roles do not create separate managers.
 */
export class MemoryPluginManagement {
  readonly settingsNamespace: string
  private readonly settings: HostSettingsScope<MemoryViewPreferences>
  private readonly legacySettings: HostSettingsScope<LegacySourcePreferences>
  private queue: Promise<unknown> = Promise.resolve()
  private closed = false
  private timer: ReturnType<typeof setTimeout> | undefined
  private restoreError: string | undefined
  private discoveryWarnings: string[] = []
  private changingEntries = false
  private restorePending = false
  /** A failed move keeps saved choices in effect instead of retrying on every profile reload. */
  private enablementMoveError: string | undefined

  constructor(private readonly ctx: HostContextShape, private readonly engine: MemoryRuntime, private readonly settingsService: HostSettingsService) {
    const loader = this.loader()
    const anchor = loader?.config?.baseUrl ?? loader?.ctx?.baseUrl ?? loader?.context?.baseUrl
    const suffix = anchor ? `-${hash(anchor).slice(0, 16)}` : ''
    this.settingsNamespace = `${MNEMON_VIEW_SETTINGS_NAMESPACE}${suffix}`
    this.settings = settingsService.register<MemoryViewPreferences>(this.settingsNamespace, schema, {
      base: { entries: {} }, applies: 'live', validate: value => { preferences(value) },
    })
    // Read the previously published Source overlay only as an input migration.
    // New writes always use the unified mnemon-view document above.
    this.legacySettings = settingsService.register<LegacySourcePreferences>(`mnemon-plugins${suffix}`, legacySchema, {
      base: { sources: {} }, applies: 'live', validate: value => { legacyPreferences(value) },
    })
  }

  resolveConfig(config: ResolvedConfig, view = this.settings.get()): ResolvedConfig {
    const selected = view.strategyTypeId
    return selected === undefined ? config : { ...config, memoryTopology: { ...config.memoryTopology, strategyId: selected } }
  }

  start(): () => void {
    const schedule = () => this.scheduleRestore()
    const stops = [this.ctx.on('loader/entry-init', schedule), this.ctx.on('loader/partial-dispose', schedule),
      // ConfigEditor reconciles the whole profile, including unrelated writes.
      // Its public settled event can arrive while an overlay is being restored.
      this.ctx.on('app-boot/config-reload', () => this.scheduleRestore(true)),
      this.settingsService.onUpdated(namespace => {
        if (namespace === this.settingsNamespace || namespace === `mnemon-plugins${this.settingsNamespace.slice(MNEMON_VIEW_SETTINGS_NAMESPACE.length)}`) schedule()
      })]
    // Saved enablement moves into the profile patch once the plugin manager runs.
    this.ctx.inject(['pluginManager'], () => { schedule() })
    schedule()
    return () => { this.closed = true; this.restorePending = false; clearTimeout(this.timer); for (const stop of stops.reverse()) stop() }
  }

  private scheduleRestore(profileReload = false): void {
    const hasSavedState = Object.keys(this.settings.get().entries ?? {}).length > 0 || Object.keys(this.legacySettings.get().sources ?? {}).length > 0
    if (this.closed || (!profileReload && this.changingEntries) || !hasSavedState) return
    this.restorePending = true
    if (this.changingEntries || this.timer) return
    this.timer = setTimeout(() => { this.timer = undefined; this.enqueueRestore() }, 0)
  }

  private enqueueRestore(): void {
    if (this.closed || this.changingEntries || !this.restorePending) return
    clearTimeout(this.timer)
    this.timer = undefined
    this.restorePending = false
    void this.exclusive(async () => {
      try { await this.restore() }
      catch (error) { this.restoreError = error instanceof Error ? error.message : String(error) }
    }).catch(() => {})
  }

  /** A response must not race the deferred restore after profile reconciliation. */
  private async settled(): Promise<void> {
    do {
      this.enqueueRestore()
      const pending = this.queue
      await pending
      if (pending === this.queue && !this.restorePending) return
    } while (!this.closed)
  }

  private loader(): MemoryPluginLoader | undefined {
    const candidate = this.ctx.get('loader') as Partial<MemoryPluginLoader> | undefined
    return candidate && typeof candidate.entries === 'function' ? candidate as MemoryPluginLoader : undefined
  }

  /**
   * With the DSH plugin manager, the profile patch owns enablement, so its
   * Plugins page and Mnemon agree. Profiles without it keep Mnemon's overlay.
   */
  private pluginManager(): HostPluginManager | undefined {
    const candidate = this.ctx.get('pluginManager') as Partial<HostPluginManager> | undefined
    return typeof candidate?.setPluginEnabled === 'function' ? candidate as HostPluginManager : undefined
  }

  private async setEnabled(manager: HostPluginManager, entryId: string, enabled: boolean): Promise<void> {
    const result = await manager.setPluginEnabled(entryId, enabled)
    if (result.application !== 'applied') throw new Error(`DSH could not ${enabled ? 'enable' : 'disable'} ${entryId}: ${result.error?.message ?? result.application}`)
  }

  private settingsRevision(namespace = this.settingsNamespace): number {
    return this.settingsService.describe({ redactSecrets: true }).find(value => value.ns === namespace)?.revision ?? 0
  }

  /** The plugin manager owns enablement unless moving the saved choices failed. */
  private enablementOwner(): HostPluginManager | undefined {
    return this.enablementMoveError === undefined ? this.pluginManager() : undefined
  }

  private async managed(): Promise<ManagedPlugin[]> {
    const loader = this.loader()
    if (!loader) return []
    const snapshot = this.engine.contributionSnapshot()
    const result: ManagedPlugin[] = []
    const warnings: string[] = []
    for (const entry of loader.entries()) {
      if (entry.options.group || !PACKAGE.test(entry.options.name)) continue
      try {
        let module = entry.fiber?.runtime?.callback as MemoryPluginModule | undefined
        if (!module?.memoryPlugin && !module?.memoryStrategyConfiguration) module = await entry.parent.tree.import(entry.options.name) as MemoryPluginModule
        const editor = module?.memoryStrategyConfiguration === undefined ? undefined : readMemoryStrategyConfiguration(module.memoryStrategyConfiguration)
        const registeredDescriptor = snapshot.plugins?.find(plugin => plugin.provenance.entryId === entry.id)?.descriptor
        const descriptor = defineMemoryPlugin(module?.memoryPlugin ?? registeredDescriptor ?? fallbackDescriptor(entry, snapshot, editor))
        if (descriptor.packageName !== entry.options.name) throw new Error('Plugin descriptor package does not match its Loader Entry')
        if (editor !== undefined && !descriptor.roles.includes(editor.kind)) throw new Error('Plugin descriptor does not declare its configurable contribution role')
        const source = snapshot.sources.find(value => value.provenance.entryId === entry.id)
        const strategy = snapshot.strategies.find(value => value.provenance.entryId === entry.id)
        const extension = (snapshot.strategyExtensions ?? []).find(value => value.provenance.entryId === entry.id)
        const typeId = editor?.typeId ?? strategy?.definition.manifest.typeId ?? extension?.definition.manifest.typeId ?? source?.definition.manifest.typeId
        const ancestorDisabled = entry.parent.ctx?.fiber?.entry?.disabled === true
        const active = contributionRoles(snapshot, entry.id).length > 0
        const value: MemoryPluginEntryView = {
          entryId: entry.id,
          packageName: entry.options.name,
          roles: descriptor.roles,
          ...(typeId === undefined ? {} : { typeId }),
          ...(extension === undefined ? {} : { strategyTypeId: extension.definition.manifest.strategyTypeId, slot: extension.definition.manifest.slot }),
          label: descriptor.label,
          description: descriptor.description,
          provides: descriptor.provides.map(capability => ({ id: capability.id, exclusive: capability.exclusive === true })),
          requires: [...descriptor.requires ?? []],
          requiredBy: [],
          fields: editor?.fields ?? [],
          config: editor === undefined ? {} : configuration(editor, entry.options.config),
          enabled: !entry.disabled,
          active,
          writable: this.settingsService.writable && !ancestorDisabled && (entry.options.disabled === undefined || typeof entry.options.disabled === 'boolean'),
          ...(ancestorDisabled ? { diagnostic: 'This Entry is disabled by its parent.' }
            : !entry.disabled && !active ? { diagnostic: 'This Entry is enabled but has not registered a memory contribution.' } : {}),
        }
        const item: ManagedPlugin = { entry, descriptor, ...(editor === undefined ? {} : { editor }), value }
        if (editor?.kind === 'strategy-extension' && value.strategyTypeId === undefined) {
          const candidate = prepared(item, value.config).strategyExtensions?.[0]?.definition.manifest
          if (candidate) { value.strategyTypeId = candidate.strategyTypeId; value.slot = candidate.slot }
        }
        result.push(item)
      } catch (error) {
        warnings.push(`${entry.id}: ${error instanceof Error ? error.message : String(error)}`)
      }
    }
    this.discoveryWarnings = warnings
    return result.sort((left, right) => left.entry.options.name.localeCompare(right.entry.options.name) || left.entry.id.localeCompare(right.entry.id))
  }

  private revision(items: ManagedPlugin[]): string {
    const namespaces = new Set(['mnemon', this.settingsNamespace, `mnemon-plugins${this.settingsNamespace.slice(MNEMON_VIEW_SETTINGS_NAMESPACE.length)}`])
    return hash({
      settings: this.settingsService.describe({ redactSecrets: true }).filter(value => namespaces.has(value.ns)).map(value => [value.ns, value.revision]),
      contribution: this.engine.contributionSnapshot().revision,
      entries: items.map(({ entry, value, descriptor }) => ({ id: entry.id, name: entry.options.name, enabled: !entry.disabled, config: value.config, descriptor })),
    })
  }

  async catalog(): Promise<{ revision: string; writable: boolean; entries: MemoryPluginEntryView[]; diagnostics: string[] }> {
    await this.settled()
    const pending = this.queue
    const items = await this.managed()
    // Discovery imports can yield while another plugin's profile write queues
    // a restore. Discard that mixed read and inspect the settled tree again.
    if (pending !== this.queue || this.restorePending) return this.catalog()
    const values = items.map(item => clone(item.value))
    const snapshot = this.engine.contributionSnapshot()
    const managedIds = new Set(items.map(item => item.entry.id))
    const entryIds = new Set([...snapshot.sources, ...snapshot.strategies, ...(snapshot.strategyExtensions ?? [])].map(value => value.provenance.entryId))
    for (const entryId of entryIds) {
      if (managedIds.has(entryId)) continue
      const source = snapshot.sources.find(value => value.provenance.entryId === entryId)
      const strategy = snapshot.strategies.find(value => value.provenance.entryId === entryId)
      const extension = (snapshot.strategyExtensions ?? []).find(value => value.provenance.entryId === entryId)
      const contribution = source ?? strategy ?? extension!
      const descriptor = snapshot.plugins?.find(plugin => plugin.provenance.entryId === entryId)?.descriptor
        ?? defineMemoryPlugin({
          packageName: contribution.provenance.packageName,
          label: { en: contribution.definition.manifest.typeId, 'zh-CN': contribution.definition.manifest.typeId },
          description: { en: 'Mounted outside the managed DSH Loader.', 'zh-CN': '由受管 DSH Loader 之外的入口挂载。' },
          roles: contributionRoles(snapshot, entryId),
          provides: [{ id: source ? 'source' : strategy ? 'strategy' : `strategy.${extension!.definition.manifest.typeId}` }],
        })
      values.push({
        entryId,
        packageName: descriptor.packageName,
        roles: descriptor.roles,
        typeId: contribution.definition.manifest.typeId,
        ...(extension === undefined ? {} : { strategyTypeId: extension.definition.manifest.strategyTypeId, slot: extension.definition.manifest.slot }),
        label: descriptor.label,
        description: descriptor.description,
        provides: descriptor.provides.map(capability => ({ id: capability.id, exclusive: capability.exclusive === true })),
        requires: [...descriptor.requires ?? []],
        requiredBy: [],
        fields: [], config: {}, enabled: true, active: true, writable: false,
        diagnostic: 'This plugin is not owned by a managed DSH Loader Entry.',
      })
    }
    for (const value of values) {
      const capabilities = new Set(value.provides.map(capability => capability.id))
      value.requiredBy = values.filter(candidate => candidate.entryId !== value.entryId && candidate.requires.some(requirement => capabilities.has(requirement)))
        .map(candidate => candidate.entryId)
    }
    return {
      revision: this.revision(items),
      writable: this.settingsService.writable && this.loader() !== undefined,
      entries: orderPlugins(values),
      diagnostics: [...this.discoveryWarnings, ...[this.restoreError, this.enablementMoveError].filter((value): value is string => value !== undefined)],
    }
  }

  private choices(items: ManagedPlugin[], entries: Record<string, MemoryPluginPreference>): Map<string, MemoryPluginPreference> {
    return new Map(items.map(item => [item.entry.id, entries[item.entry.id] ?? { enabled: item.value.enabled, config: item.value.config }]))
  }

  private validateGraph(items: ManagedPlugin[], choices: Map<string, MemoryPluginPreference>): void {
    const managedIds = new Set(items.map(item => item.entry.id))
    const nodes = items.filter(item => choices.get(item.entry.id)?.enabled).map(item => ({ instanceKey: `plugin:${item.entry.id}`, descriptor: item.descriptor }))
    const snapshot = this.engine.contributionSnapshot()
    for (const plugin of snapshot.plugins ?? []) if (!managedIds.has(plugin.provenance.entryId)) nodes.push(plugin)
    const unmanagedSources = snapshot.sources.filter(source => !managedIds.has(source.provenance.entryId))
    validateMemoryPluginGraph(nodes, [
      ...(unmanagedSources.length === 0 ? [] : ['source']),
      ...unmanagedSources.map(source => `source.${source.definition.manifest.role}`),
      ...(snapshot.strategies.some(strategy => !managedIds.has(strategy.provenance.entryId)) ? ['strategy'] : []),
    ])
  }

  private validateRequest(items: ManagedPlugin[], request: MemoryViewConfigurationRequest, restoring = false): Map<string, MemoryPluginPreference> {
    if (request.expectedRevision !== this.revision(items)) throw new Error('Memory plugin configuration changed; refresh before saving or previewing.')
    const incoming = preferences({ strategyTypeId: request.strategyTypeId, entries: request.entries })
    const managedIds = new Set(items.map(item => item.entry.id))
    for (const [entryId, value] of Object.entries(incoming.entries)) {
      if (!managedIds.has(entryId)) throw new Error('Memory plugin Entry is not managed by this Host: ' + entryId)
      if (typeof value.enabled !== 'boolean') throw new Error('Plugin enabled must be boolean')
    }
    const choices = this.choices(items, incoming.entries as Record<string, MemoryPluginPreference>)
    for (const item of items) {
      const chosen = choices.get(item.entry.id)!
      if (!restoring && !item.value.writable && hash(chosen) !== hash({ enabled: item.value.enabled, config: item.value.config })) throw new Error('Memory plugin Entry is read-only: ' + item.entry.id)
      if (item.editor !== undefined) prepared(item, chosen.config)
      else if (Object.keys(chosen.config).length > 0) throw new Error('Plugin does not expose configurable public fields: ' + item.entry.id)
    }
    this.validateGraph(items, choices)
    return choices
  }

  private proposal(items: ManagedPlugin[], request: MemoryViewConfigurationRequest): MemoryContributionSnapshot {
    const choices = this.validateRequest(items, request)
    const snapshot = this.engine.contributionSnapshot()
    const managedIds = new Set(items.map(item => item.entry.id))
    const candidate: MemoryContributionSnapshot = {
      revision: snapshot.revision,
      sources: snapshot.sources.filter(value => !managedIds.has(value.provenance.entryId)),
      strategies: snapshot.strategies.filter(value => !managedIds.has(value.provenance.entryId)),
      strategyExtensions: (snapshot.strategyExtensions ?? []).filter(value => !managedIds.has(value.provenance.entryId)),
      plugins: (snapshot.plugins ?? []).filter(value => !managedIds.has(value.provenance.entryId)),
    }
    for (const item of items) {
      const chosen = choices.get(item.entry.id)!
      if (!chosen.enabled) continue
      if (item.editor !== undefined) {
        const values = prepared(item, chosen.config)
        candidate.sources.push(...values.sources)
        candidate.strategies.push(...values.strategies)
        candidate.strategyExtensions!.push(...values.strategyExtensions ?? [])
        candidate.plugins!.push(...values.plugins ?? [])
        continue
      }
      const sources = snapshot.sources.filter(value => value.provenance.entryId === item.entry.id)
      const strategies = snapshot.strategies.filter(value => value.provenance.entryId === item.entry.id)
      const extensions = (snapshot.strategyExtensions ?? []).filter(value => value.provenance.entryId === item.entry.id)
      if (sources.length + strategies.length + extensions.length === 0) throw new Error(`Preview cannot activate an unloaded plugin: ${item.entry.id}`)
      candidate.sources.push(...sources)
      candidate.strategies.push(...strategies)
      candidate.strategyExtensions!.push(...extensions)
      const plugin = snapshot.plugins?.find(value => value.provenance.entryId === item.entry.id) ?? installedPlugin(item.descriptor, item.entry.id)
      candidate.plugins!.push(plugin)
    }
    return captureMemoryContributionSnapshot(candidate)
  }

  async preview(config: ResolvedConfig, scope: MemoryOperationScope, request: MemoryViewConfigurationRequest, signal?: AbortSignal) {
    await this.settled()
    const items = await this.managed()
    const value = await this.evaluateSnapshot(this.proposal(items, request), config, scope, request.strategyTypeId, signal)
    if (request.expectedRevision !== this.revision(items)) throw new Error('Memory plugin configuration changed during preview; refresh and retry.')
    return value
  }

  private async evaluateSnapshot(snapshot: MemoryContributionSnapshot, config: ResolvedConfig, scope: MemoryOperationScope, strategyTypeId: string, signal?: AbortSignal) {
    signal?.throwIfAborted()
    // An explicit choice is validated strictly; the runtime fallback never hides it.
    const { strategyFallback: _fallback, ...options } = memoryGenerationOptions(config, scope.workspaceId)
    const generation = new MemoryCompositionGeneration(snapshot, { ...options, strategyTypeId })
    try {
      const view = await generation.compose({ scope, scenario: 'agent.root-turn', budget: { ...DEFAULT_MEMORY_VIEW_BUDGET } }, signal)
      signal?.throwIfAborted()
      return inspectMemoryView(generation, view, 'preview')
    } finally { await generation.dispose() }
  }

  async apply(config: ResolvedConfig, scope: MemoryOperationScope, request: MemoryViewConfigurationRequest, signal?: AbortSignal): Promise<void> {
    await this.settled()
    return this.exclusive(async () => {
      if (!this.settingsService.writable || !this.loader()) throw new Error('Memory plugin configuration is read-only')
      const items = await this.managed()
      const choices = this.validateRequest(items, request)
      const expectedSettingsRevision = this.settingsRevision()
      const manager = this.enablementOwner()
      const previous = preferences(this.settings.get())
      const merged = { ...previous.entries, ...request.entries }
      const next = preferences({ strategyTypeId: request.strategyTypeId, entries: manager === undefined ? merged : configurationOnly(merged) })
      // Enable before disabling, so a dependency is never left without its provider.
      const toggles = manager === undefined ? [] : items.flatMap(item => {
        const enabled = choices.get(item.entry.id)!.enabled
        return enabled === !item.entry.disabled ? [] : [{ entryId: item.entry.id, enabled }]
      }).sort((left, right) => Number(right.enabled) - Number(left.enabled))
      const written: typeof toggles = []
      let committedRevision: number | undefined
      await this.updateEntries(items, choices, {
        validate: () => this.evaluateSnapshot(this.engine.contributionSnapshot(), config, scope, request.strategyTypeId, signal),
        persist: async () => {
          await this.settingsService.mutate(this.settingsNamespace, [
            { op: 'set', path: ['strategyTypeId'], value: next.strategyTypeId },
            { op: 'set', path: ['entries'], value: next.entries },
          ], expectedSettingsRevision)
          committedRevision = this.settingsRevision()
          for (const toggle of toggles) {
            await this.setEnabled(manager!, toggle.entryId, toggle.enabled)
            written.push(toggle)
          }
        },
        compensate: async () => {
          for (const toggle of [...written].reverse()) await this.setEnabled(manager!, toggle.entryId, !toggle.enabled)
          if (hash(preferences(this.settings.get())) !== hash(next)) throw new Error('Memory plugin settings changed before rollback; refresh and inspect the saved configuration.')
          await this.settingsService.mutate(this.settingsNamespace, [
            previous.strategyTypeId === undefined ? { op: 'unset', path: ['strategyTypeId'] }
              : { op: 'set', path: ['strategyTypeId'], value: previous.strategyTypeId },
            { op: 'set', path: ['entries'], value: previous.entries },
          ], committedRevision)
        },
      }, signal)
      this.restoreError = undefined
    })
  }

  /** Move saved enablement into the profile patch once, keeping only configuration overlays. */
  private async moveEnablement(manager: HostPluginManager, items: ManagedPlugin[]): Promise<boolean> {
    const saved = this.settings.get().entries ?? {}
    const legacy = this.legacySettings.get().sources ?? {}
    const moves = items.flatMap(item => {
      const enabled = saved[item.entry.id] !== undefined ? saved[item.entry.id]!.enabled
        : item.descriptor.roles.includes('source') ? legacy[item.entry.id]?.enabled : undefined
      return enabled === undefined ? [] : [{ entryId: item.entry.id, enabled }]
    }).sort((left, right) => Number(right.enabled) - Number(left.enabled))
    if (moves.length === 0 && Object.keys(legacy).length === 0) return false
    // Write each saved choice explicitly: the live state may still be Mnemon's
    // earlier overlay rather than what the profile patch declares.
    const legacyNamespace = `mnemon-plugins${this.settingsNamespace.slice(MNEMON_VIEW_SETTINGS_NAMESPACE.length)}`
    const revisions = { view: this.settingsRevision(), legacy: this.settingsRevision(legacyNamespace) }
    for (const move of moves) await this.setEnabled(manager, move.entryId, move.enabled)
    await this.settingsService.mutate(this.settingsNamespace, [{ op: 'set', path: ['entries'], value: configurationOnly(saved) }], revisions.view)
    if (Object.keys(legacy).length > 0) await this.settingsService.mutate(legacyNamespace, [{ op: 'set', path: ['sources'], value: {} }], revisions.legacy)
    return true
  }

  private async restore(): Promise<void> {
    if (this.closed) return
    let items = await this.managed()
    const available = this.enablementOwner()
    if (available !== undefined && this.settingsService.writable) {
      try {
        if (await this.moveEnablement(available, items)) items = await this.managed()
      } catch (error) {
        this.enablementMoveError = `Saved plugin choices could not move into the DSH profile and stay managed by Mnemon: ${error instanceof Error ? error.message : String(error)}`
      }
    }
    const manager = this.enablementOwner()
    const saved = this.settings.get().entries ?? {}
    const legacy = this.legacySettings.get().sources ?? {}
    const desired = Object.fromEntries(items.flatMap(item => {
      const unified = saved[item.entry.id]
      // With the plugin manager, the profile patch already decided enablement.
      const current = !item.entry.disabled
      if (unified !== undefined) return [[item.entry.id, { enabled: manager === undefined ? unified.enabled ?? current : current, config: unified.config }]]
      const oldSource = manager === undefined && item.descriptor.roles.includes('source') ? legacy[item.entry.id] : undefined
      return oldSource === undefined ? [] : [[item.entry.id, { enabled: oldSource.enabled, config: item.value.config }]]
    }))
    const request: MemoryViewConfigurationRequest = {
      expectedRevision: this.revision(items),
      strategyTypeId: this.settings.get().strategyTypeId ?? 'default-three-tier',
      entries: desired,
    }
    const choices = this.validateRequest(items, request, true)
    await this.updateEntries(items, choices)
    this.restoreError = undefined
  }

  private async updateEntries(items: ManagedPlugin[], choices: Map<string, MemoryPluginPreference>, commit?: {
    validate(): Promise<unknown>
    persist(): Promise<void>
    compensate(): Promise<void>
  }, signal?: AbortSignal): Promise<void> {
    // A profile write can reset even entries unchanged by this request. Keep
    // their original state too, before staging any live updates.
    const previous = items.map(item => ({ item, disabled: item.entry.options.disabled as boolean | undefined ?? null,
      config: clone(item.entry.options.config ?? {}), enabled: item.value.enabled, active: item.value.active }))
    const touched = new Set<string>()
    const replay = async (current: ManagedPlugin[]) => {
      for (const item of items) if (!current.some(value => value.entry.id === item.entry.id)) throw new Error('Memory plugin Entry disappeared during configuration: ' + item.entry.id)
      const ordered = current.filter(item => choices.has(item.entry.id))
        .sort((left, right) => Number(choices.get(left.entry.id)!.enabled) - Number(choices.get(right.entry.id)!.enabled))
      for (const item of ordered) {
        signal?.throwIfAborted()
        if (this.closed) throw new Error('Memory plugin management service is disposed')
        const desired = choices.get(item.entry.id)!
        const actual = { enabled: !item.entry.disabled, config: item.editor === undefined ? {} : configuration(item.editor, item.entry.options.config) }
        const active = contributionRoles(this.engine.contributionSnapshot(), item.entry.id).length > 0
        if (hash(desired) === hash(actual) && active === desired.enabled) continue
        touched.add(item.entry.id)
        await item.entry.update(item.editor === undefined
          ? { disabled: !desired.enabled }
          : { disabled: !desired.enabled, config: clone(desired.config) })
        await item.entry.fiber?.await?.()
        const registered = contributionRoles(this.engine.contributionSnapshot(), item.entry.id).length > 0
        if (item.entry.disabled === desired.enabled || registered !== desired.enabled) throw new Error('Cordis did not register the requested memory plugin state: ' + item.entry.id)
      }
    }
    let persisted = false
    this.changingEntries = true
    try {
      await this.engine.batch(async () => {
        try {
          await replay(items)
          signal?.throwIfAborted()
          if (this.closed) throw new Error('Memory plugin management service is disposed')
          if (commit !== undefined) {
            await commit.validate()
            await commit.persist()
            persisted = true
            // SettingsForms -> ConfigEditor.edit replays the entire profile.
            // Re-read actual Loader entries and restore every selected overlay
            // before publishing a Serving generation or returning success.
            const entries = new Map(Array.from(this.loader()?.entries() ?? []).map(entry => [entry.id, entry]))
            await replay(items.flatMap(item => {
              const entry = entries.get(item.entry.id)
              return entry === undefined ? [] : [{ ...item, entry }]
            }))
            await commit.validate()
          }
        } catch (error) {
          const failures: unknown[] = []
          // Compensation can itself reconcile the profile, so restore the live
          // entries only after the durable document is back to its old value.
          if (persisted) try { await commit!.compensate() } catch (rollback) { failures.push(rollback) }
          const current = new Map(Array.from(this.loader()?.entries() ?? []).map(entry => [entry.id, entry]))
          for (const before of previous.reverse()) {
            const entry = current.get(before.item.entry.id)
            const differs = entry === undefined || hash({ disabled: entry.options.disabled ?? null, config: entry.options.config ?? {} }) !== hash({ disabled: before.disabled, config: before.config })
              || !entry.disabled !== before.enabled || (contributionRoles(this.engine.contributionSnapshot(), before.item.entry.id).length > 0) !== before.active
            if (!differs && !touched.has(before.item.entry.id)) continue
            try {
              if (entry === undefined) throw new Error('Memory plugin Entry is unavailable for rollback: ' + before.item.entry.id)
              await entry.update(before.item.editor === undefined
                ? { disabled: before.disabled }
                : { disabled: before.disabled, config: before.config })
              await entry.fiber?.await?.()
              if (!entry.disabled !== before.enabled || (contributionRoles(this.engine.contributionSnapshot(), entry.id).length > 0) !== before.active) throw new Error('Cordis did not restore the previous memory plugin state: ' + entry.id)
            } catch (rollback) { failures.push(rollback) }
          }
          if (failures.length) {
            this.restorePending = false
            this.restoreError = 'Plugin change failed and rollback was incomplete; inspect DSH plugin state.'
            throw new AggregateError([error, ...failures], this.restoreError)
          }
          throw error
        }
      })
    } finally {
      this.changingEntries = false
      if (this.restorePending) this.scheduleRestore(true)
    }
  }

  private exclusive<T>(operation: () => Promise<T>): Promise<T> {
    const next = this.queue.then(() => {
      if (this.closed) throw new Error('Memory plugin management service is disposed')
      return operation()
    })
    this.queue = next.then(() => {}, () => {})
    return next
  }
}
