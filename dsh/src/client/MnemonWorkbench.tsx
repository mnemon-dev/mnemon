import { isWorkspaceStorageScope } from '../host/protocol.ts'
import { isDefaultSourceInstance } from '../host/protocol.ts'
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type FormEvent, type ReactNode } from 'react'
import { IconChevronLeftOutline14, IconRefreshOutlineRegular, IconSettingsOutlineRegular } from './ui-icons.ts'
import type { PropsRenderSlots } from '@deepseek-ai/dsh-client-ui-slots'
import { consumeMnemonAnchor, subscribeMnemonAnchor, type MnemonAnchor } from "./anchor.ts"

import { type ClientConnectionHandle, type ClientSettingsScope, type Config, type JsonValue, type MemoryProviderRuntimeStatus, type MemorySourceManagementCatalog, type MemorySourceManagementInstance, type StatusView, type StorageAreaInventory, type StorageScopeInventory, type StorageScopeKind, type VersionRestartStatus } from "../host/protocol.ts"
import type { MemoryPluginEntryView, MemoryViewDashboard } from '../host/view-protocol.ts'
import { MnemonClient } from "./api.ts"
import { isRemoteConnection } from "./remote-rpc.ts"
import { VersionDialog } from "./VersionDialog.tsx"
import { clearStarterUpdate, pendingStarterUpdate } from './starter-update.ts'
import { translateZh, type MnemonKey, type MnemonTranslate } from "./locales.ts"

import { ProviderIcon } from "./ProviderIcon.tsx"

import { MNEMON_SOURCE_CONFIGURATION_MUTATE, MNEMON_SOURCE_CONFIGURATION_READ, MNEMON_SOURCE_PAGE_SLOT, type MemorySourcePageDirectory, type MemorySourcePageEntry } from "./source-pages.tsx"
import type { MnemonSourceManagementClient } from "./dsh-context.ts"
import type { MnemonActionSeat } from './action-seat.ts'
import type { MnemonChangeSignal } from './change-signal.ts'
import type { MnemonDisplayMode } from '../host/protocol.ts'
import { appearanceClass } from './view-styles.ts'
import { compositionNotice } from './composition-notice.ts'
import { layerOf, sourceOf } from './component-model.ts'
import { componentCopy } from './component-copy.ts'
import { WorkbenchStatusContext } from './component-status.tsx'
import { MNEMON_COMPONENT_STATUS_SLOT } from './component-ui.tsx'
import { Callout, Reveal, useToast } from './feedback.tsx'
import feedbackCss from './MnemonFeedback.module.css'
import { Button, StateDot, type StateDotState } from '@deepseek-ai/dsh-client-ui-primitives'
import { SelectField } from './page-controls.tsx'
import { isRecord } from './is-record.ts'
import sidebarCss from './MnemonSidebarView.module.css'
import css from "./MnemonView.module.css"
import { I18nContext, LocaleContext, useT, useLocale, humanBytes, message, short, PageHeader, EmptyState } from "./page-kit.tsx"

interface MnemonWorkbenchProps {
  connection: ClientConnectionHandle
  settingsScope: ClientSettingsScope<Config>
  sessionId?: string
  workspaceId?: string
  workspaceSelection?: MnemonWorkspaceSelection
  /** Placement changes scope controls, not Source pages or their visual skin. */
  surface?: MnemonDisplayMode
  /** Sidebar retains its subtree while hidden; refresh View when reopened. */
  active?: boolean
  t?: MnemonTranslate
  locale?: string
  onClose?: () => void
  /** Opens the dsh-mnemon page under DSH Plugins, where the configuration lives, while that page offers navigation. */
  configuration?: MnemonActionSeat
  /** Moves when a component is switched through DSH's plugin manager. */
  componentChanges?: MnemonChangeSignal
  sourcePageDirectory?: MemorySourcePageDirectory
  renderSlot?: PropsRenderSlots<typeof MNEMON_SOURCE_PAGE_SLOT | typeof MNEMON_COMPONENT_STATUS_SLOT>['renderSlot']
}

const NO_ACTION_SEAT: Pick<MnemonActionSeat, 'subscribe' | 'getSnapshot'> = { subscribe: () => () => {}, getSnapshot: () => undefined }
const NO_CHANGE_SIGNAL: Pick<MnemonChangeSignal, 'subscribe' | 'getSnapshot'> = { subscribe: () => () => {}, getSnapshot: () => 0 }

export interface MnemonWorkspaceSelection {
  options: Array<{ id: string; title: string; path: string }>
  selectedWorkspaceId?: string
  effectiveWorkspaceId?: string
  onSelect(workspaceId: string): void
  onAlign(): void
}

type SourcePage = `source:${string}`

type ManagedSourcePage = `source-management:${string}`

/** A configured memory layer whose Source is not running, so it has no page of its own to show. */
type StoppedSourcePage = `source-stopped:${string}`

type Page = 'status' | SourcePage | ManagedSourcePage | StoppedSourcePage

const EMPTY_SOURCE_PAGE_SNAPSHOT: readonly MemorySourcePageEntry[] = Object.freeze([])

const EMPTY_SOURCE_MANAGEMENT_FIELDS: NonNullable<MemorySourceManagementInstance['management']['fields']> = []

const EMPTY_SOURCE_PAGE_DIRECTORY: MemorySourcePageDirectory = {
  getSnapshot: () => EMPTY_SOURCE_PAGE_SNAPSHOT,
  subscribe: () => () => {},
}

function sourcePage(entryId: string): SourcePage {
  return `source:${entryId}`
}

function sourcePageEntryId(page: Page): string | undefined {
  return page.startsWith('source:') ? page.slice('source:'.length) : undefined
}

function managedSourcePage(sourceTypeId: string): ManagedSourcePage {
  return `source-management:${sourceTypeId}`
}

function managedSourceTypeId(page: Page): string | undefined {
  return page.startsWith('source-management:') ? page.slice('source-management:'.length) : undefined
}

function stoppedSourcePage(sourceTypeId: string): StoppedSourcePage {
  return `source-stopped:${sourceTypeId}`
}

function stoppedSourceTypeId(page: Page): string | undefined {
  return page.startsWith('source-stopped:') ? page.slice('source-stopped:'.length) : undefined
}

/** Which way a memory layer is not serving now, or that it is. */
type LayerState = 'on' | 'off' | 'stopped' | 'memory-off'

/** A Source component's card on the Status page. */
interface ComponentCard {
  key: string
  packageName: string
  label: string
  enabled: boolean
  state: LayerState
}

/**
 * How memory is composed now, beside the connection in the header: the main
 * Strategy composing it, and on hover each layer's state. Selecting it opens
 * the configuration where that composition is changed.
 */
function CompositionStatus(props: { tone: StateDotState; label: string; strategy?: string | undefined; layers: ReadonlyArray<{ id: string; label: string; state: 'on' | 'off' | 'stopped' | 'memory-off' }>; onOpen: (() => void) | undefined }): JSX.Element {
  const t = useT()
  const summaryId = useId()
  const layerDot = (state: 'on' | 'off' | 'stopped' | 'memory-off'): StateDotState => state === 'on' ? 'done' : state === 'stopped' ? 'warning' : 'idle'
  const layerNote = (state: 'on' | 'off' | 'stopped' | 'memory-off'): string | undefined => state === 'on' ? undefined
    : t(state === 'off' ? 'layers.disabledBadge' : state === 'stopped' ? 'layers.stoppedBadge' : 'layers.memoryOffBadge')
  const anchor = <button type="button" className={css.compositionStatus} onClick={props.onOpen} disabled={props.onOpen === undefined}
    aria-label={props.strategy === undefined ? props.label : `${props.label} · ${props.strategy}`} {...(props.layers.length === 0 ? {} : { 'aria-describedby': summaryId })}>
    <StateDot state={props.tone} />
    <span>{props.label}</span>
    {props.strategy !== undefined && <span className={css.compositionStrategy}>{props.strategy}</span>}
  </button>
  if (props.layers.length === 0) return anchor
  // A popover under the status, aligned to its right edge, shown on hover and on keyboard focus.
  return <span className={css.compositionStatusWrap}>
    {anchor}
    <span id={summaryId} className={css.compositionSummary} role="tooltip">
      <strong>{t('board.title')}</strong>
      {props.strategy !== undefined && <span className={feedbackCss.chip} data-state={props.tone === 'done' ? 'done' : 'warning'}><StateDot state={props.tone === 'done' ? 'done' : 'warning'} /><span className={feedbackCss.chipLabel}>{props.strategy}</span></span>}
      <span className={css.compositionLayers}>{props.layers.map(layer => <span key={layer.id} className={feedbackCss.chip} data-state={layerDot(layer.state)}>
        <StateDot state={layerDot(layer.state)} />
        <span className={feedbackCss.chipLabel}>{layer.label}</span>
        {layerNote(layer.state) !== undefined && <span className={feedbackCss.chipNote}>{layerNote(layer.state)}</span>}
      </span>)}</span>
      {props.onOpen !== undefined && <span className={css.compositionHint}>{t('header.compositionHint')}</span>}
    </span>
  </span>
}

function bindSourceManagementClient(client: MnemonClient, instance: MemorySourceManagementInstance, taskClient: MnemonClient): MnemonSourceManagementClient {
  return {
    sourceInstanceKey: instance.sourceInstanceKey,
    revision: instance.revision,
    ...(instance.assistance === undefined || instance.assistance.length === 0 ? {} : { assistance: {
      operations: instance.assistance,
      execute: (operation: string, input: JsonValue, options: { expectedRevision: string; confirmed: boolean }) => {
        // These clean task-Agent workflows follow the inspected Sidebar root;
        // normal Source edits keep their existing session-aware write path.
        const task = ['agent-search', 'supervise', 'body-create', 'body-metadata-maintain'].includes(operation)
        return (task ? taskClient : client).assistSource(instance.sourceInstanceKey, operation, input, options.expectedRevision, options.confirmed)
      },
    } }),
    read: (operation, input = null) => client.readSourceManagement(instance.sourceInstanceKey, operation, input),
    mutate: (operation, input, options) => client.mutateSourceManagement(
      instance.sourceInstanceKey,
      operation,
      input,
      options.expectedRevision ?? instance.revision,
      options.confirmed,
    ),
  }
}

function jsonRecord(value: JsonValue): Record<string, JsonValue> | undefined {
  return isRecord(value) ? value : undefined
}

function SourceDisabledPage(props: { title: string; onOpenConfiguration: (() => void) | undefined }): JSX.Element {
  const t = useT()
  return <div className={css.page}>
    <PageHeader title={props.title} description={t('layers.disabledDescription')} meta={t('layers.disabledBadge')} {...(props.onOpenConfiguration === undefined ? {} : { action: <button type="button" className={css.secondaryButton} onClick={props.onOpenConfiguration}>{t('common.openConfiguration')}</button> })} />
    <EmptyState glyph="⊘" title={t('layers.disabledTitle', { layer: props.title })}>{t('layers.disabledText')}</EmptyState>
  </div>
}

/** A layer that is on but whose Source is not running: its component did not start, or no memory is composed at all. */
function SourceStoppedPage(props: { title: string; memoryOff: boolean; onOpenConfiguration: (() => void) | undefined }): JSX.Element {
  const t = useT()
  return <div className={css.page}>
    <PageHeader title={props.title} description={t(props.memoryOff ? 'layers.memoryOffDescription' : 'layers.stoppedDescription')} meta={t(props.memoryOff ? 'layers.memoryOffBadge' : 'layers.stoppedBadge')} {...(props.onOpenConfiguration === undefined ? {} : { action: <button type="button" className={css.secondaryButton} onClick={props.onOpenConfiguration}>{t('common.openConfiguration')}</button> })} />
    <EmptyState glyph="⊘" title={t(props.memoryOff ? 'layers.memoryOffTitle' : 'layers.stoppedTitle', { layer: props.title })}>{t(props.memoryOff ? 'layers.memoryOffText' : 'layers.stoppedText')}</EmptyState>
  </div>
}

interface SourceNavigationEntry {
  id: string
  page: Page
  sourceTypeId?: string
  label: string
  detail: string
  group: 'system' | 'storage' | 'tools' | 'sources'
  glyph: string
  primary: boolean
  /** The layer is on, but its Source is not running. */
  stopped?: boolean
}

function WorkspaceNavigation(props: { page: Page; onSelect(page: Page): void; sourcePages: readonly SourceNavigationEntry[]; disabledTypes: ReadonlySet<string>; memoryOff: boolean }): JSX.Element {
  const t = useT()
  const entries: readonly SourceNavigationEntry[] = [
    { id: 'status', page: 'status', label: t('nav.status'), detail: '', group: 'system', glyph: '⌘', primary: true },
    ...props.sourcePages,
  ]
  const selectedType = sourcePageEntryId(props.page)?.split('/')[0]
  const button = (item: SourceNavigationEntry) => {
    const active = props.page === item.page || selectedType !== undefined && selectedType === item.sourceTypeId
    const disabled = item.sourceTypeId !== undefined && props.disabledTypes.has(item.sourceTypeId)
    // A switched-off layer says so first; a stopped Source matters only while the layer is on.
    const badge = disabled ? t('layers.disabledBadge') : item.stopped === true ? t(props.memoryOff ? 'layers.memoryOffBadge' : 'layers.stoppedBadge') : undefined
    return <button key={item.id} type="button" role="tab" aria-selected={active} data-active={active ? '' : undefined} aria-label={badge === undefined ? undefined : item.label + ' · ' + badge} data-layer-disabled={badge === undefined ? undefined : ''} onClick={() => props.onSelect(item.page)}><span>{item.label}</span>{badge !== undefined && <em className={css.layerDisabledBadge}>{badge}</em>}</button>
  }
  return <div className={appearanceClass(css.topNavigation, sidebarCss.topNavigation)}>
    <div className={appearanceClass(css.nav, sidebarCss.nav)} role="tablist" aria-label={t('nav.aria')}>{entries.filter(entry => entry.primary).map(button)}</div>
  </div>
}

/** Fixed descriptor-driven baseline; custom Source pages can only add to it. */
function SourceManagementPage(props: {
  instance: MemorySourceManagementInstance
  instances: readonly MemorySourceManagementInstance[]
  management?: MnemonSourceManagementClient
  onSelect(sourceInstanceKey: string): void
  onMutate(): void
}): JSX.Element {
  const t = useT()
  const fields = props.instance.management.fields ?? EMPTY_SOURCE_MANAGEMENT_FIELDS
  const [draft, setDraft] = useState<Record<string, JsonValue>>({})
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    let active = true
    setDraft({})
    setError(null)
    setSaved(false)
    if (fields.length === 0 || props.management === undefined) {
      setLoading(false)
      return () => { active = false }
    }
    setLoading(true)
    void props.management.read(MNEMON_SOURCE_CONFIGURATION_READ).then(result => {
      if (!active) return
      const root = jsonRecord(result.value)
      const values = jsonRecord(root?.values ?? result.value) ?? {}
      setDraft(Object.fromEntries(fields.flatMap(field => {
        if (field.secret === true || field.input === 'secret') return []
        const value = values[field.key]
        return value === undefined ? [] : [[field.key, value]]
      })))
    }).catch(reason => {
      if (active) setError(message(reason))
    }).finally(() => {
      if (active) setLoading(false)
    })
    return () => { active = false }
  }, [fields, props.instance.revision, props.instance.sourceInstanceKey, props.management])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (props.management === undefined) return
    setSaving(true)
    setSaved(false)
    setError(null)
    try {
      const input = Object.fromEntries(fields.flatMap(field => {
        const value = draft[field.key]
        if ((field.secret === true || field.input === 'secret') && (value === undefined || value === '')) return []
        if (field.input === 'number' && value !== undefined && value !== '') return [[field.key, Number(value)]]
        if (field.input === 'boolean') return [[field.key, value === true]]
        return value === undefined || value === '' ? [] : [[field.key, value]]
      }))
      await props.management.mutate(MNEMON_SOURCE_CONFIGURATION_MUTATE, input, { confirmed: true })
      setSaved(true)
      props.onMutate()
    } catch (reason) {
      setError(message(reason))
    } finally {
      setSaving(false)
    }
  }

  const availability = t(`sourcePage.availability.${props.instance.availability}` as MnemonKey)
  return <div className={css.page} data-source-management={props.instance.sourceTypeId}>
    <PageHeader
      title={props.instance.management.label}
      description={props.instance.management.description}
      meta={props.instance.sourceTypeId}
      {...(loading ? { loadingLabel: t('sourcePage.configLoading') } : {})}
    />
    {props.instances.length > 1 && <SelectField inline size="sm" className={css.workspacePicker} label={t('sourcePage.instance')} ariaLabel={t('sourcePage.instanceAria')} value={props.instance.sourceInstanceKey} options={props.instances.map(instance => ({ value: instance.sourceInstanceKey, label: instance.management.label, detail: instance.sourceInstanceKey }))} onChange={props.onSelect} />}
    <section className={css.sourceManagementSummary} data-availability={props.instance.availability} aria-label={t('sourcePage.summaryAria')}>
      <div className={css.sourceManagementIdentity}><span aria-hidden="true" /><div><small>{t('sourcePage.package')}</small><strong>{props.instance.packageName}</strong><code>{props.instance.sourceInstanceKey}</code></div></div>
      <dl>
        <div><dt>{t('sourcePage.availability')}</dt><dd>{availability}</dd></div>
        <div><dt>{t('sourcePage.role')}</dt><dd>{props.instance.role}</dd></div>
        <div><dt>{t('sourcePage.revision')}</dt><dd><code>{short(props.instance.revision, 32)}</code></dd></div>
      </dl>
      <div className={css.sourceManagementCapabilities}><small>{t('sourcePage.permissions')}</small><div>{props.instance.capabilities.map(capability => <span key={capability}>{capability}</span>)}</div></div>
    </section>
    {props.instance.management.diagnostics !== undefined && props.instance.management.diagnostics.length > 0 && <section className={css.sourceManagementDiagnostics} aria-label={t('sourcePage.diagnostics')}><h3>{t('sourcePage.diagnostics')}</h3><ul>{props.instance.management.diagnostics.map((diagnostic, index) => <li key={`${index}:${diagnostic}`}>{diagnostic}</li>)}</ul></section>}
    {fields.length > 0 && <form className={css.sourceManagementForm} onSubmit={event => void submit(event)}>
      <div><h3>{t('sourcePage.configuration')}</h3><p>{t('sourcePage.configurationDescription')}</p></div>
      <div className={css.formGrid}>{fields.map(field => {
        const value = draft[field.key]
        const update = (next: JsonValue): void => setDraft(current => ({ ...current, [field.key]: next }))
        return <label key={field.key}>{field.label}
          {field.input === 'boolean'
            ? <input type="checkbox" checked={value === true} onChange={event => update(event.target.checked)} />
            : field.input === 'select'
              ? <SelectField hideLabel label={field.label} value={typeof value === 'string' ? value : ''} options={[{ value: '', label: '—' }, ...(field.options ?? []).map(option => ({ value: option.value, label: option.label }))]} onChange={update} />
              : <input type={field.input === 'secret' ? 'password' : field.input === 'number' ? 'number' : field.input === 'url' ? 'url' : 'text'} value={typeof value === 'string' || typeof value === 'number' ? value : ''} required={field.required && field.input !== 'secret'} autoComplete={field.input === 'secret' ? 'new-password' : undefined} placeholder={field.input === 'secret' ? t('sourcePage.secretPlaceholder') : undefined} onChange={event => update(event.target.value)} />}
          {field.description !== undefined && <small>{field.description}</small>}
        </label>
      })}</div>
      {error !== null && <div className={css.alert} role="alert">{error}</div>}
      {saved && <div className={css.runtimeNotice} role="status">{t('sourcePage.configSaved')}</div>}
      <div><button type="submit" className={css.primaryButton} disabled={saving || loading || props.management === undefined || props.instance.availability === 'unavailable'}>{saving ? t('sourcePage.configSaving') : t('sourcePage.configSave')}</button>{props.management === undefined && <span>{t('sourcePage.unavailable')}</span>}</div>
    </form>}
    {fields.length === 0 && props.instance.availability === 'unavailable' && <div className={css.emptyState}><span className={css.emptyGlyph}>!</span><div><h3>{t('sourcePage.unavailable')}</h3><p>{t('sourcePage.unavailableDescription')}</p></div></div>}
  </div>
}

function StatusPage(props: {
  client: MnemonClient
  status: StatusView | null
  loading: boolean
  writeEnabled: boolean
  attention: boolean
  /** One card per Source component, in the order the configuration lists its layers. */
  components: readonly ComponentCard[]
  /** What a running component's card says: what it contributed, or that it runs. */
  renderCard: (card: ComponentCard) => ReactNode
  /** A storage area's name, from the component that keeps its data there. */
  areaName: (kind: StorageAreaInventory['kind']) => string
  onRefresh: () => void
  /** Where Providers that are off can be turned on. */
  onOpenConfiguration?: (() => void) | undefined
}): JSX.Element {
  const t = useT()
  // DSH swapped this client in during a Starter update started here: show how it ended.
  const [resumedUpdate, setResumedUpdate] = useState(() => pendingStarterUpdate())
  const [versionsOpen, setVersionsOpen] = useState(false)
  const status = props.status
  const answered = status !== null || !props.loading
  useEffect(() => {
    if (resumedUpdate === undefined || !answered) return
    // A DSH that already restarted onto the update has nothing left to report.
    if (status !== null && status.restartPending === undefined && resumedUpdate.to !== undefined && status.dshMnemonVersion === resumedUpdate.to) {
      clearStarterUpdate()
      setResumedUpdate(undefined)
      return
    }
    setVersionsOpen(true)
  }, [answered])
  const reviewError = status?.lifecycle?.current?.lastError
  const storage = status?.storage
  const selectedScopeKind = storage?.activeKind ?? 'global'
  const selectedScope = storage?.scopes.find(scope => scope.kind === selectedScopeKind)
  // A component that is off, or whose Source is not running, has no reading to wait for.
  const idle = (state: LayerState): { title: string; detail: string } | undefined => state === 'on' ? undefined
    : state === 'off' ? { title: t('layers.disabledBadge'), detail: t('status.layerOffDetail') }
      : state === 'memory-off' ? { title: t('layers.memoryOffBadge'), detail: t('status.layerMemoryOffDetail') }
        : { title: t('layers.stoppedBadge'), detail: t('status.layerStoppedDetail') }
  return (
    <div className={css.page}>
      <PageHeader title={t('status.title')} description={t('status.description')} meta={status === null && props.loading ? t('common.loading') : status === null || reviewError !== undefined || props.attention ? t('status.checkRequired') : t('status.nominal')} {...(props.loading ? { loadingLabel: t('status.rechecking') } : {})} action={<div className={css.statusHeaderActions}><button type="button" className={css.secondaryButton} onClick={() => setVersionsOpen(true)}>{t('versions.checkAction')}</button></div>} />

      <section className={css.healthStrip} aria-label={t('status.aria')}>
        <article><span className={`${css.healthIndicator} ${status === null ? css.healthMuted : css.healthGood}`} /><div><small>{t('status.engine')}</small><strong>{status?.dshMnemonVersion === undefined ? 'dsh-mnemon' : `dsh-mnemon ${status.dshMnemonVersion}`}</strong><p>{status === null ? t('status.pluginChecking') : t('status.pluginReady')}</p></div></article>
        {props.components.map(card => {
          const note = idle(card.state)
          return <article key={card.key} data-component={card.packageName}>
            <span className={`${css.healthIndicator} ${note === undefined && status !== null ? css.healthGood : css.healthMuted}`} />
            <div><small>{card.label}</small>{note === undefined ? props.renderCard(card) : <><strong>{note.title}</strong><p>{note.detail}</p></>}</div>
          </article>
        })}
      </section>

      <div className={css.asyncStatusBlock}>{status !== null && (status.providerServices !== undefined || (status.memoryBodies !== undefined && nativeInUse(status))) && <ProviderHealth status={status} services={status.providerServices ?? []} onOpenConfiguration={props.onOpenConfiguration} />}</div>
      <div className={css.asyncStatusBlock}><StorageDomains catalog={storage} selected={selectedScope} selectedKind={selectedScopeKind} areaName={props.areaName} /></div>
      {versionsOpen && <VersionDialog client={props.client} writeEnabled={props.writeEnabled} resumedUpdate={resumedUpdate} onClose={() => { setResumedUpdate(undefined); setVersionsOpen(false) }} onRefreshStatus={props.onRefresh} />}
    </div>
  )
}

/**
 * An installed update waits for DSH to restart: the Host keeps the code it loaded, while DSH has
 * already swapped in this page. Every Memory System page says so until the restart.
 */
function RestartReminder({ pending }: { pending: VersionRestartStatus }): JSX.Element {
  const t = useT()
  return pending.installed !== undefined
    ? <Callout tone="warning" title={t('versions.pendingInstalledTitle', { version: pending.installed })}>{t('versions.pendingInstalledDetail', { running: pending.running })}</Callout>
    : <Callout tone="warning" title={t('versions.pendingPackagesTitle', { names: (pending.packages ?? []).join(t('config.listSeparator')) })}>{t('versions.pendingPackagesDetail')}</Callout>
}

/** Mnemon Native is one Provider among peers: it has a health card once its CLI is installed or a space uses it. */
function nativeInUse(status: StatusView): boolean {
  return status.commandFound || (status.memoryBodies ?? []).some(body => body.provider.origin === 'native')
}

/** Mnemon Native as the first row of the Provider list; its health comes from the CLI and its own spaces. */
function NativeProviderRow({ status }: { status: StatusView }): JSX.Element {
  const t = useT()
  const bodies = (status.memoryBodies ?? []).filter(body => body.provider.origin === 'native')
  const active = bodies.filter(body => body.active)
  const pending = active.filter(body => body.statusLoading === true)
  const failed = active.filter(body => body.statusLoading !== true && !body.healthy)
  const state: MemoryProviderRuntimeStatus['status'] = !status.commandFound || failed.length > 0 ? 'unhealthy' : active.length === 0 || pending.length > 0 ? 'idle' : 'healthy'
  const error = !status.commandFound
    ? t('status.nativeCliMissing')
    : failed.map(body => `${body.name}: ${body.error ?? t('status.engineUnavailable')}`).join('; ')
  const version = status.version === undefined ? t('status.versionWaiting') : `Mnemon ${status.version}`
  return <article aria-label={t('status.nativeAria')} data-status={state} data-native="">
    <ProviderIcon providerId="mnemon-native" icon={{ kind: 'brand', value: 'mnemon' }} className={css.providerHealthMark} />
    <div className={css.providerHealthCopy}>
      <strong>{t('config.nativeName')}</strong>
      <small>{t(`status.providerState.${state}` as MnemonKey)} · <span>{version}</span></small>
      {error !== '' && <p title={error}>{error}</p>}
    </div>
    <div className={css.providerHealthMeta}>
      <span className={css.providerHealthSignal} aria-hidden="true" />
      <small>{t('status.providerSpaces', { active: active.length, total: bodies.length })}</small>
    </div>
  </article>
}

/** The Providers that run, one row each; the ones that are off share one line saying where to turn them on. */
function ProviderHealth({ status, services, onOpenConfiguration }: { status: StatusView; services: MemoryProviderRuntimeStatus[]; onOpenConfiguration?: (() => void) | undefined }): JSX.Element {
  const t = useT()
  const native = status.memoryBodies !== undefined && nativeInUse(status)
  const running = services.filter(service => service.enabled)
  const off = services.length - running.length
  const enabled = running.length + (native && status.commandFound ? 1 : 0)
  const total = services.length + (native ? 1 : 0)
  return <section className={css.providerHealth} aria-label={t('status.providersAria')}>
    <div className={css.statusSectionHeader}>
      <div><h3>{t('status.providersTitle')}</h3><p>{t('status.providersDescription')}</p></div>
      <span className={css.phaseBadge}>{t('status.providersEnabled', { enabled, total })}</span>
    </div>
    <div className={css.providerHealthList}>
      {native && <NativeProviderRow status={status} />}
      {running.map(service => <article key={service.providerId} data-status={service.status}>
        <ProviderIcon providerId={service.providerId} icon={service.icon} className={css.providerHealthMark} />
        <div className={css.providerHealthCopy}>
          <strong>{service.label}</strong>
          <small>{t(`status.providerState.${service.status}` as MnemonKey)}</small>
          {service.error !== undefined && <p title={service.error}>{service.error}</p>}
        </div>
        <div className={css.providerHealthMeta}>
          <span className={css.providerHealthSignal} aria-hidden="true" />
          <small>{t('status.providerSpaces', { active: service.activeMemoryBodyCount, total: service.memoryBodyCount })}</small>
        </div>
      </article>)}
    </div>
    {off > 0 && <div className={css.providerHealthOff}>
      <span>{t('status.providersOff', { count: off })}</span>
      {onOpenConfiguration !== undefined && <Button variant="ghost" size="sm" onClick={onOpenConfiguration}>{t('common.openConfiguration')}</Button>}
    </div>}
  </section>
}

function storageScopeLabel(t: MnemonTranslate, kind: StorageScopeKind): string {
  return t(kind === 'global' ? 'status.storageGlobal' : kind === 'workspace' ? 'status.storageWorkspace' : kind === 'workspaces' ? 'status.storageWorkspaces' : 'status.storageCustom')
}

/** Resolve the configured scope before the first status round-trip to keep the Sidebar header stable. */
function configuredStorageScope(config: Config | undefined): StorageScopeKind {
  return config?.storageScope ?? (config?.dataDir?.trim() ? 'custom' : 'global')
}

/** The Source whose data a storage area holds. */
const AREA_SOURCES: Readonly<Partial<Record<StorageAreaInventory['kind'], string>>> = { runtime: 'runtime', documents: 'documents', 'memory-bodies': 'memory-spaces' }

function storageAreaLabel(t: MnemonTranslate, kind: StorageAreaInventory['kind']): string {
  return t(kind === 'runtime' ? 'status.storageRuntime' : kind === 'memory-bodies' ? 'status.storageSpaces' : kind === 'documents' ? 'status.storageDocuments' : 'status.storageState')
}

function storageAreaDetails(t: MnemonTranslate, area: StorageAreaInventory): string {
  if (area.kind === 'runtime') return t('status.storageRuntimeDetail', { user: area.details.userEntries ?? 0, memory: area.details.memoryEntries ?? 0 })
  if (area.kind === 'memory-bodies') return t('status.storageSpacesDetail', { active: area.details.activeBodies ?? 0, databases: area.details.databases ?? 0 })
  if (area.kind === 'documents') return t('status.storageDocumentsDetail', { active: area.details.activeDocuments ?? 0, archived: area.details.archivedDocuments ?? 0 })
  return area.details.reviewLedger === true ? t('status.storageStateReady') : t('status.storageStateVolatile')
}

function StorageDomains(props: {
  catalog: StatusView['storage']
  selected: StorageScopeInventory | undefined
  selectedKind: StorageScopeKind
  areaName: (kind: StorageAreaInventory['kind']) => string
}): JSX.Element {
  const t = useT()
  const areaStatus = (status: StorageAreaInventory['status']) => t(status === 'ready' ? 'status.storageReady' : status === 'empty' ? 'status.storageEmpty' : status === 'missing' ? 'status.storageMissing' : 'status.storageInvalid')
  return (
    <section className={css.storageDomains} aria-label={t('status.storageDomains')}>
      <div className={css.statusSectionHeader}>
        <div><h3>{t('status.storageDomains')}</h3><p>{t('status.storageDomainsText')}</p></div>
        <span className={css.phaseBadge}>{storageScopeLabel(t, props.selectedKind)}</span>
      </div>
      {props.catalog === undefined ? <div className={css.storageUnavailable}>{t('status.storageWaiting')}</div> : props.selected?.root === undefined ? <div className={css.storageUnavailable}><strong>{storageScopeLabel(t, props.selectedKind)}</strong><p>{props.selectedKind === 'custom' ? t('status.storageCustomUnset') : t('status.storageWorkspaceUnavailable')}</p></div> : <>
        <div className={css.storageRoot}>
          <div><span>{storageScopeLabel(t, props.selectedKind)} · {t('status.storageActiveRoot')}</span><code>{props.selected.root}</code></div>
          <div><strong>{humanBytes(props.selected.totalBytes)}</strong><small>{props.selected.available ? t('status.storageAvailable') : t('status.storageNotCreated')}</small></div>
        </div>
        <div className={css.storageAreaGrid}>
          {props.selected.areas.filter(area => area.kind !== 'state').map(area => <article key={area.kind} data-status={area.status}>
            <header><div><span /> <strong>{props.areaName(area.kind)}</strong></div><em>{areaStatus(area.status)}</em></header>
            <div className={css.storageAreaMetric}><strong>{area.itemCount}</strong><span>{t('status.storageItems')}</span><code>{humanBytes(area.bytes)}</code></div>
            <p>{storageAreaDetails(t, area)}</p>
            <code className={css.storagePath}>{area.path}</code>
            {area.issue !== undefined && <small>{area.issue}</small>}
          </article>)}
        </div>
      </>}
      {props.catalog !== undefined && <p className={css.storageFootnote}>{t('status.storageFootnote', { root: props.catalog.activeRoot })}</p>}
    </section>
  )
}

export function MnemonWorkbench(props: MnemonWorkbenchProps): JSX.Element {
  const t = props.t ?? translateZh
  return <I18nContext.Provider value={t}><LocaleContext.Provider value={props.locale ?? 'zh'}><MnemonWorkspace {...props} /></LocaleContext.Provider></I18nContext.Provider>
}

function MnemonWorkspace({ connection, settingsScope, sessionId, workspaceId, workspaceSelection, surface = 'sidebar', active = true, onClose, configuration, componentChanges, sourcePageDirectory = EMPTY_SOURCE_PAGE_DIRECTORY, renderSlot }: MnemonWorkbenchProps): JSX.Element {
  const t = useT()
  const locale = useLocale()
  const configurationSeat = configuration ?? NO_ACTION_SEAT
  const openConfiguration = useSyncExternalStore(configurationSeat.subscribe, configurationSeat.getSnapshot, configurationSeat.getSnapshot)
  const componentSignal = componentChanges ?? NO_CHANGE_SIGNAL
  const componentRevision = useSyncExternalStore(componentSignal.subscribe, componentSignal.getSnapshot, componentSignal.getSnapshot)
  const subscribeSettings = useCallback((listener: () => void) => settingsScope.subscribe(listener), [settingsScope])
  const getSettingsSnapshot = useCallback(() => settingsScope.getSnapshot(), [settingsScope])
  const settingsSnapshot = useSyncExternalStore(subscribeSettings, getSettingsSnapshot, getSettingsSnapshot)
  const subscribeSourcePages = useCallback((listener: () => void) => sourcePageDirectory.subscribe(listener), [sourcePageDirectory])
  const getSourcePages = useCallback(() => sourcePageDirectory.getSnapshot(), [sourcePageDirectory])
  const sourcePageEntries = useSyncExternalStore(subscribeSourcePages, getSourcePages, getSourcePages)
  const client = useMemo(() => new MnemonClient(connection, sessionId, workspaceId), [connection, sessionId, workspaceId])
  const taskClient = useMemo(() => surface === 'builtin' ? client : new MnemonClient(connection, undefined, workspaceId), [client, connection, surface, workspaceId])
  const clientContextKey = `${sessionId ?? ''}\u0000${workspaceId ?? ''}`
  // Pages remount only when they would show other data: another storage scope
  // or directory. Any other save re-reads in place and keeps page drafts.
  const storageContext = settingsSnapshot.revision === undefined ? 'loading'
    : JSON.stringify([settingsSnapshot.value?.storageScope ?? null, settingsSnapshot.value?.dataDir ?? null, settingsSnapshot.value?.runtimeUserScope ?? null])
  const viewContextKey = `${clientContextKey}\u0000${storageContext}`
  const [page, setPage] = useState<Page>('status')
  const workspaceToast = useToast()
  const showWorkspaceToast = workspaceToast.show
  const canvasRef = useRef<HTMLElement | null>(null)

  const selectPage = useCallback((next: Page) => setPage(next), [])

  /** Pages share one plugin-owned scroll container; never mutate DSH ancestor scrollports. */
  const resetViewportScroll = useCallback(() => {
    const canvas = canvasRef.current
    if (canvas !== null) canvas.scrollTop = 0
  }, [])
  const revealElement = useCallback((element: HTMLElement, topInset?: number) => {
    const canvas = canvasRef.current
    if (canvas === null || !canvas.contains(element)) return
    // A locked page header covers the canvas top; a Source that pins its own header measures it.
    const header = topInset === undefined && canvas.hasAttribute('data-lock-page-header') ? canvas.querySelector<HTMLElement>(`.${css.pageHeader}`) : null
    const inset = topInset ?? (header === null ? 0 : header.getBoundingClientRect().height + 12)
    canvas.scrollTop = Math.max(0, canvas.scrollTop + element.getBoundingClientRect().top - canvas.getBoundingClientRect().top - inset)
  }, [])

  // Reset before paint so a newly selected page never flashes at the previous
  // page's scroll offset for one frame. The host still owns every ancestor.
  useLayoutEffect(() => { resetViewportScroll() }, [viewContextKey, page, resetViewportScroll])
  const [statusState, setStatusState] = useState<{ contextKey: string; value: StatusView | null; loading: boolean; error: string | null }>(() => ({ contextKey: viewContextKey, value: null, loading: true, error: null }))
  const currentStatusState = statusState.contextKey === viewContextKey
    ? statusState
    : { contextKey: viewContextKey, value: null, loading: true, error: null }
  const status = currentStatusState.value
  const statusLoading = currentStatusState.loading
  const statusError = currentStatusState.error
  const statusRequest = useRef(0)
  const [revision, setRevision] = useState(0)
  const [sourceCatalogState, setSourceCatalogState] = useState<{ contextKey: string; value: MemorySourceManagementCatalog | null; error: string | null }>(() => ({ contextKey: viewContextKey, value: null, error: null }))
  const [selectedSourceInstances, setSelectedSourceInstances] = useState<Record<string, string>>({})
  const [navigationInput, setNavigationInput] = useState<{ page: string; value: JsonValue } | undefined>()
  /** A tab opens its page fresh; only an anchor carries navigation into a page. */
  const openTab = useCallback((next: Page) => { setNavigationInput(undefined); setPage(next) }, [])

  useEffect(() => {
    let active = true
    void client.sourceManagementCatalog().then(value => {
      if (active) setSourceCatalogState({ contextKey: viewContextKey, value, error: null })
    }).catch(reason => {
      if (active) setSourceCatalogState({ contextKey: viewContextKey, value: null, error: message(reason) })
    })
    return () => { active = false }
  }, [client, revision, viewContextKey])

  const sourceCatalog = sourceCatalogState.contextKey === viewContextKey ? sourceCatalogState.value : null
  const sourceInstances = sourceCatalog?.sources ?? []
  const sourceManagementClients = useMemo(() => new Map(sourceInstances
    .map(source => [source.sourceInstanceKey, bindSourceManagementClient(client, source, taskClient)])), [client, sourceInstances, taskClient])
  const visibleSourcePages = useMemo(() => {
    const visibleTypes = new Set(sourceInstances.map(source => source.sourceTypeId))
    return sourcePageEntries.filter(entry => visibleTypes.has(entry.sourceTypeId))
  }, [sourceInstances, sourcePageEntries])
  const managedSourceTypes = useMemo(() => {
    const byType = new Map<string, MemorySourceManagementInstance[]>()
    for (const source of sourceInstances) {
      if (sourcePageEntries.some(entry => entry.sourceTypeId === source.sourceTypeId) && (source.management.fields?.length ?? 0) === 0) continue
      const current = byType.get(source.sourceTypeId)
      if (current === undefined) byType.set(source.sourceTypeId, [source])
      else current.push(source)
    }
    return [...byType.entries()].sort(([left], [right]) => left.localeCompare(right))
  }, [sourceInstances, sourcePageEntries])
  // Configured layers whose Source is not running: its component is off, or
  // nothing is composed. They keep a tab that says so instead of vanishing.
  const stoppedLayers = status?.memorySystem?.configuration.layers
  const stoppedTypes = useMemo(() => {
    if (sourceCatalog === null || stoppedLayers === undefined) return new Set<string>()
    const running = new Set(sourceInstances.map(source => source.sourceTypeId))
    return new Set(Object.keys(stoppedLayers).filter(id => !running.has(id)))
  }, [sourceCatalog, sourceInstances, stoppedLayers])
  // The installed components name themselves: every name here comes from a
  // component's own declaration, the way the configuration shows it.
  const [dashboard, setDashboard] = useState<MemoryViewDashboard | null>(null)
  useEffect(() => {
    let current = true
    // Names are a courtesy: an unreadable dashboard leaves the pages' own labels.
    client.viewDashboard().then(value => { if (current) setDashboard(Array.isArray(value?.entries) ? value : null) }, () => { if (current) setDashboard(null) })
    return () => { current = false }
  }, [client, revision, componentRevision])
  const sourceName = useCallback((sourceTypeId: string, fallback?: string): string => {
    const component = dashboard === null ? undefined : sourceOf(dashboard, sourceTypeId)
    return component === undefined ? fallback ?? sourceTypeId : componentCopy(component, locale).label
  }, [dashboard, locale])
  const strategyName = (typeId: string): string => {
    const component = dashboard?.entries.find(entry => entry.roles.includes('strategy') && entry.typeId === typeId)
    return component === undefined ? typeId : componentCopy(component, locale).label
  }
  // A stopped layer whose Source component is switched off is off: the
  // configuration shows that component as the layer's own switch.
  const switchedOff = useMemo(() => new Set(dashboard === null ? [] : [...stoppedTypes].filter(id => sourceOf(dashboard, id)?.enabled === false)), [dashboard, stoppedTypes])
  const sourceNavigationEntries = useMemo<SourceNavigationEntry[]>(() => {
    const entries: SourceNavigationEntry[] = []
    const stopped = (sourceTypeId: string, label: string): SourceNavigationEntry => ({
      id: 'stopped:' + sourceTypeId, page: stoppedSourcePage(sourceTypeId), sourceTypeId, label, detail: sourceTypeId, group: 'sources', glyph: '◇', primary: true, stopped: true,
    })
    const stoppedShown = new Set<string>()
    // A Source's first primary page carries the component's name; its other pages keep their own.
    const named = new Set<string>()
    const tabName = (sourceTypeId: string, label: string): string => {
      if (named.has(sourceTypeId)) return label
      named.add(sourceTypeId)
      return sourceName(sourceTypeId, label)
    }
    // Registration order, so a layer keeps its place while its Source is stopped.
    for (const entry of sourcePageEntries) {
      const primary = entry.navigation?.primary ?? true
      if (visibleSourcePages.includes(entry)) {
        entries.push({
          id: entry.id, page: sourcePage(entry.id), sourceTypeId: entry.sourceTypeId, label: primary ? tabName(entry.sourceTypeId, entry.label) : entry.label,
          detail: entry.navigation?.detail ?? entry.sourceTypeId, group: entry.navigation?.group ?? 'sources',
          glyph: entry.navigation?.glyph ?? '◇', primary,
        })
      } else if (stoppedTypes.has(entry.sourceTypeId) && !stoppedShown.has(entry.sourceTypeId) && primary) {
        stoppedShown.add(entry.sourceTypeId)
        entries.push(stopped(entry.sourceTypeId, tabName(entry.sourceTypeId, entry.label)))
      }
    }
    // A layer whose client pages left with its component keeps its tab, named by that component.
    for (const sourceTypeId of stoppedTypes) {
      if (!stoppedShown.has(sourceTypeId) && dashboard !== null && sourceOf(dashboard, sourceTypeId) !== undefined) entries.push(stopped(sourceTypeId, sourceName(sourceTypeId)))
    }
    entries.push(...managedSourceTypes.map(([sourceTypeId, instances]) => ({
      id: 'management:' + sourceTypeId, page: managedSourcePage(sourceTypeId), sourceTypeId,
      label: sourceName(sourceTypeId, instances[0]!.management.label), detail: sourceTypeId, group: 'sources' as const, glyph: '◇', primary: true,
    })))
    return entries
  }, [dashboard, managedSourceTypes, sourceName, sourcePageEntries, stoppedTypes, visibleSourcePages])

  // A page follows its layer: to the stopped page when its Source stops, and
  // back to the layer's own page when it runs again.
  useEffect(() => {
    if (sourceCatalog === null) return
    const entryId = sourcePageEntryId(page)
    const managedTypeId = managedSourceTypeId(page)
    const stoppedTypeId = stoppedSourceTypeId(page)
    const whileStopped = (sourceTypeId: string | undefined): Page => sourceTypeId !== undefined && stoppedTypes.has(sourceTypeId) ? stoppedSourcePage(sourceTypeId) : 'status'
    if (entryId !== undefined && !visibleSourcePages.some(entry => entry.id === entryId)) {
      setPage(whileStopped(sourcePageEntries.find(entry => entry.id === entryId)?.sourceTypeId))
    } else if (managedTypeId !== undefined && !managedSourceTypes.some(([sourceTypeId]) => sourceTypeId === managedTypeId)) {
      setPage(whileStopped(managedTypeId))
    } else if (stoppedTypeId !== undefined && !stoppedTypes.has(stoppedTypeId)) {
      const resumed = visibleSourcePages.find(entry => entry.sourceTypeId === stoppedTypeId && (entry.navigation?.primary ?? true))
      setPage(resumed !== undefined ? sourcePage(resumed.id)
        : managedSourceTypes.some(([sourceTypeId]) => sourceTypeId === stoppedTypeId) ? managedSourcePage(stoppedTypeId) : 'status')
      showWorkspaceToast({ text: t('feedback.layerResumed', { layer: sourceName(stoppedTypeId, resumed?.label) }), tone: 'success' })
    }
  }, [managedSourceTypes, page, sourcePageEntries, stoppedTypes, visibleSourcePages, sourceCatalog, showWorkspaceToast, sourceName, t])

  useLayoutEffect(() => { setNavigationInput(undefined) }, [viewContextKey])

  /** Anchors address Source page ids; no Source component is imported here. */
  const applyAnchor = useCallback((anchor: MnemonAnchor) => {
    setNavigationInput({ page: anchor.page, value: { seed: anchor.seed ?? '', nonce: Date.now() } })
    selectPage(anchor.page === 'status' ? 'status' : sourcePage(anchor.page))
  }, [selectPage])
  useEffect(() => {
    const held = consumeMnemonAnchor(sessionId)
    if (held !== null) applyAnchor(held)
    return subscribeMnemonAnchor(sessionId, applyAnchor)
  }, [sessionId, applyAnchor])

  const loadStatus = useCallback(async () => {
    const request = ++statusRequest.current
    setStatusState(current => ({ contextKey: viewContextKey, value: current.contextKey === viewContextKey ? current.value : null, loading: true, error: null }))
    try {
      const summary = await client.statusSummary()
      if (request !== statusRequest.current) return
      // A full status also reads the CLI version, which the summary has only after one did.
      const needsDeepStatus = summary.memoryBodies?.some(body => body.statusLoading === true) === true
        || summary.commandFound === true && summary.version === undefined
      setStatusState({ contextKey: viewContextKey, value: summary, loading: needsDeepStatus, error: null })
      if (!needsDeepStatus) return
      try {
        const next = await client.status()
        if (request === statusRequest.current) setStatusState({ contextKey: viewContextKey, value: next, loading: false, error: null })
      } catch (reason) {
        if (request === statusRequest.current) setStatusState({ contextKey: viewContextKey, value: summary, loading: false, error: message(reason) })
      }
    } catch (reason) {
      if (request === statusRequest.current) setStatusState({ contextKey: viewContextKey, value: null, loading: false, error: message(reason) })
    }
  }, [client, viewContextKey])
  useEffect(() => { void loadStatus() }, [loadStatus])

  const mutate = useCallback(() => { setRevision(value => value + 1); void loadStatus() }, [loadStatus])
  const refreshAll = mutate
  // The header's refresh also asks the open Source page to reload its data.
  const [refreshKey, setRefreshKey] = useState(0)
  const refreshPage = useCallback(() => { setRefreshKey(value => value + 1); refreshAll() }, [refreshAll])
  // The configuration lives on another page: re-read when the Sidebar
  // reopens the workspace and when a component is switched elsewhere.
  const shown = useRef(active)
  useEffect(() => {
    if (active && !shown.current) refreshAll()
    shown.current = active
  }, [active, refreshAll])
  const seenComponents = useRef(componentRevision)
  useEffect(() => {
    if (componentRevision === seenComponents.current) return
    seenComponents.current = componentRevision
    refreshAll()
  }, [componentRevision, refreshAll])
  // A save that keeps the storage re-reads in place; one that changes it
  // moves the context key, which reloads everything by itself.
  const seenSettings = useRef({ revision: settingsSnapshot.revision, context: viewContextKey })
  useEffect(() => {
    const seen = seenSettings.current
    seenSettings.current = { revision: settingsSnapshot.revision, context: viewContextKey }
    if (seen.revision !== settingsSnapshot.revision && seen.context === viewContextKey) refreshAll()
  }, [settingsSnapshot.revision, viewContextKey, refreshAll])
  const activationEnabled = status?.writeEnabled === true
  const writeEnabled = activationEnabled && settingsSnapshot.status === 'ready' && settingsSnapshot.writable
  // A remote page without the management grant says why it cannot write.
  const remoteReadOnly = activationEnabled && settingsSnapshot.status === 'ready' && !settingsSnapshot.writable && isRemoteConnection(connection)
  const workspaceContext = status?.workspaceContext
  const storageMode = workspaceContext?.mode ?? status?.storage?.activeKind ?? configuredStorageScope(settingsSnapshot.value)
  const storageModeText = storageScopeLabel(t, storageMode)
  const showWorkspacePicker = isWorkspaceStorageScope(storageMode) && workspaceSelection !== undefined && workspaceSelection.options.length > 0
  const workspaceDiverged = workspaceContext !== undefined && isWorkspaceStorageScope(workspaceContext.mode) && !workspaceContext.aligned
  const canAlignWorkspace = workspaceDiverged && workspaceSelection?.effectiveWorkspaceId !== undefined
  const workspaceDifference = workspaceContext === undefined
    ? ''
    : `${t('workspace.selectedRoot', { root: workspaceContext.selectedRoot })}; ${t('workspace.effectiveRoot', { root: workspaceContext.effectiveRoot })}`
  const workspacePicker = showWorkspacePicker && <SelectField inline size="sm" className={appearanceClass(css.workspacePicker, sidebarCss.workspacePicker)} label={t('workspace.viewing')} ariaLabel={t('workspace.selectorAria')} value={workspaceSelection.selectedWorkspaceId ?? ''} options={workspaceSelection.options.map(workspace => ({ value: workspace.id, label: workspace.title }))} onChange={workspaceSelection.onSelect} />
  // The saved settings answer at once; the status catches up after it reloads.
  const savedLayers = settingsSnapshot.value?.memoryTopology?.layers
  const configuredLayers = status?.memorySystem?.configuration.layers
  const disabledTypes = new Set([...new Set([...Object.keys(configuredLayers ?? {}), ...Object.keys(savedLayers ?? {})])]
    .filter(id => (savedLayers?.[id]?.enabled ?? configuredLayers?.[id]?.enabled) === false || switchedOff.has(id)))
  const notice = compositionNotice(status?.memorySystem, t)
  const memoryOff = status?.memorySystem !== undefined && !status.memorySystem.serving
  const composingType = status?.memorySystem?.serving === true ? status.memorySystem.strategyTypeId : undefined
  const composingLabel = composingType === undefined ? undefined : strategyName(composingType)
  const statusTone: StateDotState = status === null && statusLoading ? 'ongoing'
    : statusError !== null || notice?.tone === 'error' || status?.healthy === false && notice === undefined ? 'error'
      : notice !== undefined ? 'warning' : 'done'
  const connectionLabel = status === null && statusLoading
    ? t('header.checking')
    : notice?.tone === 'error'
      ? t('status.memoryOff')
      : status?.healthy !== true
        ? t('header.unavailable')
        : (t('header.connected'))
  const allInstancesFor = (sourceTypeId: string): MemorySourceManagementInstance[] => sourceInstances.filter(source => source.sourceTypeId === sourceTypeId)
  const instancesFor = (sourceTypeId: string): MemorySourceManagementInstance[] => allInstancesFor(sourceTypeId)
  const renderSourceContribution = (entryId: string): ReactNode => {
    const sourceTypeId = entryId.split('/')[0]!
    const instances = instancesFor(sourceTypeId)
    const selectedKey = selectedSourceInstances[sourceTypeId]
    const selected = instances.find(instance => instance.sourceInstanceKey === selectedKey) ?? instances.find(instance => isDefaultSourceInstance(instance.sourceInstanceKey, sourceTypeId)) ?? instances[0]
    if (selected === undefined || renderSlot === undefined) return null
    if (disabledTypes.has(sourceTypeId)) return <SourceDisabledPage title={sourceName(sourceTypeId, selected.management.label)} onOpenConfiguration={openConfiguration} />
    const management = sourceManagementClients.get(selected.sourceInstanceKey)
    const preferences = !isDefaultSourceInstance(selected.sourceInstanceKey, 'memory-spaces') ? undefined : {
      value: JSON.parse(JSON.stringify({ persistenceStrategy: { ...settingsSnapshot.value?.persistenceStrategy, providerConnections: {} } })) as JsonValue,
      writable: settingsSnapshot.writable,
      replace: async (value: JsonValue) => {
        const requested = jsonRecord(value)?.persistenceStrategy
        const strategy = requested === undefined ? undefined : jsonRecord(requested)
        if (strategy === undefined) throw new Error('Persistence strategy preferences must be an object')
        const connections = jsonRecord(strategy.providerConnections ?? {}) ?? {}
        const merged = { ...settingsSnapshot.value?.persistenceStrategy?.providerConnections }
        for (const [id, fields] of Object.entries(connections)) merged[id] = { ...merged[id], ...jsonRecord(fields) } as NonNullable<typeof merged[string]>
        await settingsScope.mutate([{ op: 'set', path: ['persistenceStrategy'], value: { ...strategy, providerConnections: merged } }])
      },
    }
    return renderSlot(MNEMON_SOURCE_PAGE_SLOT, {
      sourceTypeId, sourceInstanceKey: selected.sourceInstanceKey, sourceInstances: instances, writable: writeEnabled, locale,
      ...(management === undefined ? {} : { management }),
      ...(sessionId === undefined ? {} : { sessionId }), ...(workspaceId === undefined ? {} : { workspaceId }),
      ...(navigationInput?.page === entryId ? { navigationInput: navigationInput.value } : {}),
      ...(preferences === undefined ? {} : { preferences }), onRefresh: mutate, refreshKey, onResetScroll: resetViewportScroll, onRevealElement: revealElement,
    }, { only: entryId })
  }
  const activeStoppedType = stoppedSourceTypeId(page)
  const stoppedTitle = (sourceTypeId: string): string => sourceName(sourceTypeId, sourceNavigationEntries.find(entry => entry.page === page)?.label)
  const layerState = (sourceTypeId: string): LayerState => disabledTypes.has(sourceTypeId) ? 'off'
    : !stoppedTypes.has(sourceTypeId) ? 'on' : memoryOff ? 'memory-off' : 'stopped'
  const layerIds = Object.keys(status?.memorySystem?.configuration.layers ?? {})
  const headerLayers = layerIds.map(id => ({ id, label: sourceName(id), state: layerState(id) }))
  // Every Source component has a card, in the order the configuration lists its layer; a
  // component that is off and registered nothing still has one. Without the components,
  // the configured layers stand in.
  const sourceComponents = (dashboard?.entries ?? []).filter(entry => entry.roles.includes('source'))
  const rank = (entry: MemoryPluginEntryView): number => { const index = layerIds.indexOf(layerOf(entry) ?? ''); return index < 0 ? layerIds.length : index }
  const componentCards: ComponentCard[] = dashboard === null
    ? layerIds.map(id => ({ key: id, packageName: 'dsh-mnemon-source-' + id, label: sourceName(id), enabled: layerState(id) !== 'off', state: layerState(id) }))
    : [...sourceComponents].sort((left, right) => rank(left) - rank(right)).map(entry => {
      const layerId = layerOf(entry)
      return { key: entry.entryId, packageName: entry.packageName, label: componentCopy(entry, locale).label, enabled: entry.enabled,
        state: !entry.enabled ? 'off' : layerId === undefined ? (memoryOff ? 'memory-off' : 'stopped') : layerState(layerId) }
    })
  const renderCard = (card: ComponentCard): ReactNode => renderSlot === undefined ? <strong>{t('board.running')}</strong> : renderSlot(MNEMON_COMPONENT_STATUS_SLOT, {
    component: { packageName: card.packageName, label: card.label, enabled: card.enabled }, language: locale,
    ...(sessionId === undefined ? {} : { sessionId }), ...(workspaceId === undefined ? {} : { workspace: { id: workspaceId } }),
  }, { entryKey: card.packageName, fallback: <strong>{t('board.running')}</strong> })
  const areaName = (kind: StorageAreaInventory['kind']): string => {
    const sourceTypeId = AREA_SOURCES[kind]
    return sourceTypeId === undefined ? storageAreaLabel(t, kind) : sourceName(sourceTypeId, storageAreaLabel(t, kind))
  }
  const activeSourcePageId = sourcePageEntryId(page)
  const activeSourcePage = activeSourcePageId === undefined ? undefined : visibleSourcePages.find(entry => entry.id === activeSourcePageId)
  const activeSourceInstances = activeSourcePage === undefined ? [] : instancesFor(activeSourcePage.sourceTypeId)
  const activeSelectedKey = activeSourcePage === undefined ? undefined : selectedSourceInstances[activeSourcePage.sourceTypeId]
  const activeSelectedInstance = activeSourceInstances.find(instance => instance.sourceInstanceKey === activeSelectedKey) ?? activeSourceInstances.find(instance => isDefaultSourceInstance(instance.sourceInstanceKey, activeSourcePage?.sourceTypeId ?? '')) ?? activeSourceInstances[0]
  const customSourcePage = activeSourcePage === undefined || activeSelectedInstance === undefined ? null : <div className={css.page} data-source-page={activeSourcePage.id}>
    {activeSourceInstances.length > 1 && <SelectField inline size="sm" className={css.workspacePicker} label={t('sourcePage.instance')} ariaLabel={t('sourcePage.instanceAria')} value={activeSelectedInstance.sourceInstanceKey} options={activeSourceInstances.map(instance => ({ value: instance.sourceInstanceKey, label: instance.management.label, detail: instance.sourceInstanceKey }))} onChange={value => setSelectedSourceInstances(current => ({ ...current, [activeSourcePage.sourceTypeId]: value }))} />}
    {renderSourceContribution(activeSourcePage.id)}
  </div>
  const activeManagedSourceTypeId = managedSourceTypeId(page)
  const activeManagedSourceInstances = activeManagedSourceTypeId === undefined ? [] : allInstancesFor(activeManagedSourceTypeId)
  const activeManagedSelectedKey = activeManagedSourceTypeId === undefined ? undefined : selectedSourceInstances[activeManagedSourceTypeId]
  const activeManagedSourceInstance = activeManagedSourceInstances.find(instance => instance.sourceInstanceKey === activeManagedSelectedKey) ?? activeManagedSourceInstances[0]
  const managedSourcePageContent = activeManagedSourceTypeId === undefined || activeManagedSourceInstance === undefined ? null : <SourceManagementPage
    instance={activeManagedSourceInstance}
    instances={activeManagedSourceInstances}
    {...(sourceManagementClients.get(activeManagedSourceInstance.sourceInstanceKey) === undefined ? {} : { management: sourceManagementClients.get(activeManagedSourceInstance.sourceInstanceKey)! })}
    onSelect={sourceInstanceKey => setSelectedSourceInstances(current => ({ ...current, [activeManagedSourceTypeId]: sourceInstanceKey }))}
    onMutate={mutate}
  />

  return (
    <main className={appearanceClass(css.shell, sidebarCss.shell)} data-dsh-plugin="dsh-mnemon" data-dsh-part="mnemon-view" data-mnemon-surface={surface}>
      <header className={appearanceClass(css.masthead, sidebarCss.masthead)}>
        {onClose !== undefined && <button type="button" className={appearanceClass(css.ghostButton, css.backButton)} onClick={onClose} aria-label={t('header.backToConversation')}><IconChevronLeftOutline14 size={14} /><span>{t('header.backToConversation')}</span></button>}
        <div className={appearanceClass(css.brand, sidebarCss.brand)}>
          <h1>{t('tab.label')}</h1>
          {surface === 'sidebar' && <>
            <span className={css.storageMode} aria-label={t('workspace.storageModeAria', { mode: storageModeText })}><span>{t('workspace.storageMode')}</span><strong>{storageModeText}</strong></span>
            {workspacePicker}
            {canAlignWorkspace && <div className={appearanceClass(css.workspaceMismatch, sidebarCss.workspaceMismatch)} role="status" aria-label={`${t('workspace.mismatchTitle')}. ${workspaceDifference}`} title={workspaceDifference}><span>{t('workspace.mismatchShort')}</span><button type="button" onClick={workspaceSelection.onAlign}>{t('workspace.align')}</button></div>}
          </>}
        </div>
        <div className={appearanceClass(css.headerActions, sidebarCss.headerActions)}><div className={appearanceClass(css.statusCluster, sidebarCss.statusCluster)}><CompositionStatus tone={statusTone} label={connectionLabel} strategy={composingLabel} layers={headerLayers} onOpen={openConfiguration} /><button type="button" className={css.iconButton} disabled={statusLoading} onClick={refreshPage} aria-label={t('common.refresh')} title={t('common.refresh')}><IconRefreshOutlineRegular size={16} /></button></div>{openConfiguration !== undefined && <button type="button" className={css.iconButton} onClick={openConfiguration} aria-label={t('header.configure')} title={t('header.configure')}><IconSettingsOutlineRegular size={16} /></button>}</div>
      </header>
      {sourceCatalogState.contextKey === viewContextKey && sourceCatalogState.error !== null && <div className={css.alert} role="alert">{sourceCatalogState.error}</div>}
      {(statusError !== null || status?.healthy === false && notice === undefined) && <div className={css.alert} role="alert"><strong>{t('header.notReady')}</strong><span>{statusError ?? status?.error}</span></div>}
      <Reveal className={css.notice}>{statusError === null && notice !== undefined && <Callout tone={notice.tone === 'error' ? 'error' : 'warning'} title={notice.title}
        actions={openConfiguration === undefined ? undefined : <Button variant="outline" size="sm" onClick={openConfiguration}>{t('common.openConfiguration')}</Button>}>
        {notice.detail}
      </Callout>}</Reveal>
      <Reveal className={css.notice}>{status?.restartPending !== undefined && <RestartReminder pending={status.restartPending} />}</Reveal>
      {workspaceToast.element}
      {remoteReadOnly && <div className={css.alert} role="status">{t('workspace.remoteReadOnly')}</div>}
      {status?.lifecycle?.current?.idleReviewBlocked === 'agent-team' && <div className={css.alert} role="status">{t('status.reviewTeamPaused')}</div>}
      {status?.lifecycle?.current?.lastError !== undefined && <div className={css.alert} role="alert" aria-label={t('status.reviewFailed')}>
        <strong>{t('status.reviewFailed')}</strong>
        <span>{status.lifecycle.current.lastError}</span>
        <span>{t('status.reviewFailedDetail')}</span>
        {status.lifecycle.current.lastReviewFailure?.status === 'partial' && <>
          <strong>{t('status.reviewPartial')}</strong>
          <span>{t('status.reviewRun', { id: status.lifecycle.current.lastReviewFailure.runId ?? '' })}</span>
          <ul>{status.lifecycle.current.lastReviewFailure.receipts.map((receipt, index) => <li key={index}>{[receipt.tool, receipt.action, receipt.documentId, receipt.target, receipt.revision].filter(Boolean).join(' · ')}</li>)}</ul>
        </>}
        {/CONTEXT_WINDOW_EXCEEDED|exceed(?:s|ed)? (?:the )?(?:available )?context (?:size|window)/iu.test(status.lifecycle.current.lastError) && <span>{t('status.reviewContextWindow')}</span>}
      </div>}
      <div className={css.workspace}>
        <WorkspaceNavigation page={page} onSelect={openTab} sourcePages={sourceNavigationEntries} disabledTypes={disabledTypes} memoryOff={memoryOff} />
        <section key={viewContextKey} className={appearanceClass(css.canvas, sidebarCss.canvas)} ref={canvasRef} data-testid="mnemon-canvas" data-lock-page-header={(activeSourcePage?.navigation?.stickyHeader !== false) ? '' : undefined}>
          {page === 'status' && <WorkbenchStatusContext.Provider value={status}>
            <StatusPage client={client} status={status} loading={statusLoading} writeEnabled={writeEnabled} attention={notice !== undefined}
              components={componentCards} renderCard={renderCard} areaName={areaName} onRefresh={() => void loadStatus()} onOpenConfiguration={openConfiguration} />
          </WorkbenchStatusContext.Provider>}
          {activeManagedSourceInstance !== undefined && managedSourcePageContent}
          {activeSourcePage !== undefined && customSourcePage}
          {activeStoppedType !== undefined && (disabledTypes.has(activeStoppedType)
            ? <SourceDisabledPage title={stoppedTitle(activeStoppedType)} onOpenConfiguration={openConfiguration} />
            : <SourceStoppedPage title={stoppedTitle(activeStoppedType)} memoryOff={memoryOff} onOpenConfiguration={openConfiguration} />)}
        </section>

      </div>
    </main>
  )
}
