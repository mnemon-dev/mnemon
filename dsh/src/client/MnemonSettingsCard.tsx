import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type JSX, type ReactNode } from 'react'
import {
  normalizeDisplayMode,
  type ClientConnectionHandle,
  type ClientSettingsScope,
  type Config,
  type InteractionConfig,
  type MemoryCompositionStatus,
} from "../host/protocol.ts"
import type { MemoryPluginEntryView } from '../host/view-protocol.ts'
import { MnemonClient } from './api.ts'
import { isRemoteConnection } from './remote-rpc.ts'
import { CompositionBoard, usePageTrail, type ComponentSettingsRenderer, type LayerSettings } from './CompositionBoard.tsx'
import { componentCopy } from './component-copy.ts'
import { useViewFeedback } from './view-feedback.tsx'
import { SelectRow, ToggleRow } from './settings-controls.tsx'
import { useLive, useScope, WriteFailure } from './settings-panel.tsx'
import { useViewStore } from './view-store.ts'
import css from './MnemonSettingsCard.module.css'
import { isRecord } from './is-record.ts'
import { translateZh, type MnemonTranslate } from './locales.ts'
import { usePackTarget } from './MnemonPackSection.tsx'
import { MnemonStorageSection } from './MnemonStorageSection.tsx'
import type { MnemonChangeSignal } from './change-signal.ts'

/** How the page reaches the settings components contribute to their own pages. */
export interface ComponentSettingsSource {
  /** Whether a component registered settings, by package name. */
  has(packageName: string): boolean
  /** Render them for one component's page. */
  render(entry: MemoryPluginEntryView, page: { enabled: boolean; label: string; writable: boolean; language: string; sessionId?: string; workspace?: { id: string; label?: string } }): ReactNode
}

export interface MnemonSettingsCardProps {
  scope: ClientSettingsScope<Config>
  /** Separate live namespace; without it the interaction toggles use `scope`. */
  interactionScope?: ClientSettingsScope<InteractionConfig>
  /** Loopback RPC used for whole-directory ZIP backup and restore. */
  connection?: ClientConnectionHandle
  sessionId?: string
  workspaceId?: string
  workspaceLabel?: string
  t?: MnemonTranslate
  /** Active DSH locale id for installed Strategies that bring their own text. */
  language?: string
  /** Moves when a component is switched through DSH's plugin manager, for example on the Plugins page below. */
  componentChanges?: Pick<MnemonChangeSignal, 'subscribe' | 'getSnapshot'>
  /** The settings components contribute to their pages; without it a page shows what their declarations give. */
  componentSettings?: ComponentSettingsSource
  /** Show one component's page alone, by its package name, as DSH's row page for that component. */
  component?: string
}

const NO_CHANGE_SIGNAL: Pick<MnemonChangeSignal, 'subscribe' | 'getSnapshot'> = { subscribe: () => () => {}, getSnapshot: () => 0 }

/**
 * The dsh-mnemon configuration, shown on its bundle page under DSH Plugins:
 * the memory composition, then only what belongs to no component — where
 * memory lives and how the interface shows it. Each component's own settings
 * live on its page, which a component's name opens anywhere on this page.
 *
 * Switches and selectors apply when they change, as the composition's do.
 * Typed values wait for their group's Apply; the storage location waits too,
 * because it moves where every component reads and writes.
 */
export function MnemonSettingsCard({ scope, interactionScope: suppliedInteractionScope, connection, sessionId, workspaceId, workspaceLabel, t = translateZh, language = 'zh', componentChanges = NO_CHANGE_SIGNAL, componentSettings, component }: MnemonSettingsCardProps): JSX.Element | null {
  const interactionScope = suppliedInteractionScope ?? scope as unknown as ClientSettingsScope<InteractionConfig>
  const coreSnapshot = useScope(scope)
  const interactionSnapshot = useScope(interactionScope)
  const [targetRevision, setTargetRevision] = useState(0)
  // A component switched below this configuration, on DSH's own list, changes the composition.
  const componentRevision = useSyncExternalStore(componentChanges.subscribe, componentChanges.getSnapshot, componentChanges.getSnapshot)
  // Every term only grows, so the sum changes whenever any one does. The
  // settings revision covers saves to the same Host entry from another page
  // or window, which the View revision includes.
  const dataRevision = targetRevision + componentRevision + (coreSnapshot.revision ?? 0)
  const viewClient = useMemo(() => connection === undefined ? undefined : new MnemonClient(connection, sessionId, workspaceId), [connection, sessionId, workspaceId])
  const view = useViewStore(viewClient, dataRevision)
  const pages = usePageTrail()
  // A layer an earlier configuration turned off comes back on through its saved switch.
  const layerSettings = useMemo<LayerSettings>(() => ({
    set: async (layerId, enabled) => {
      await scope.mutate([{ op: 'set', path: ['memoryTopology', 'layers', layerId, 'enabled'], value: enabled }])
      setTargetRevision(revision => revision + 1)
    },
  }), [scope])
  // Every change says what it did where the user is looking, and points at the rows it touched.
  const root = useRef<HTMLElement | null>(null)
  const feedbackToast = useViewFeedback(view, root, t, language)
  const [memorySystem, setMemorySystem] = useState<MemoryCompositionStatus | null>(null)
  const [memorySystemState, setMemorySystemState] = useState<'unavailable' | 'loading' | 'ready' | 'error'>(connection === undefined ? 'unavailable' : 'loading')
  const topologyRequest = useRef(0)
  const { target } = usePackTarget(connection, sessionId, workspaceId, dataRevision)

  useEffect(() => {
    if (connection === undefined) {
      topologyRequest.current += 1
      setMemorySystem(null)
      setMemorySystemState('unavailable')
      return
    }
    const request = topologyRequest.current + 1
    topologyRequest.current = request
    setMemorySystemState('loading')
    void new MnemonClient(connection, sessionId, workspaceId).memorySystem().then(descriptor => {
      if (topologyRequest.current !== request) return
      setMemorySystem(descriptor)
      setMemorySystemState('ready')
    }, () => {
      if (topologyRequest.current !== request) return
      setMemorySystem(null)
      setMemorySystemState('error')
    })
    return () => { topologyRequest.current += 1 }
  }, [connection, sessionId, workspaceId, dataRevision])

  const coreUser = useMemo(() => isRecord(coreSnapshot.user) ? coreSnapshot.user : {}, [coreSnapshot.user])
  const loading = coreSnapshot.status === 'loading' || interactionSnapshot.status === 'loading'
  // A successful writable settings snapshot is the Host's authoritative
  // capability grant. DSH authenticates the complete Host API, so transport
  // locality is not a capability signal.
  const writable = coreSnapshot.writable && interactionSnapshot.writable
  const display = useLive(normalizeDisplayMode(coreSnapshot.value?.displayMode), async value => { await scope.mutate([{ op: 'set', path: ['displayMode'], value }]) })
  const turnBar = useLive(interactionSnapshot.value?.turnBar !== false, async value => { await interactionScope.mutate([{ op: 'set', path: ['turnBar'], value }]) })
  const saveAction = useLive(interactionSnapshot.value?.saveAction !== false, async value => { await interactionScope.mutate([{ op: 'set', path: ['saveAction'], value }]) })

  // The page's composition opens each component's page; what the component contributed renders there.
  const settingsRenderer = useMemo<ComponentSettingsRenderer | undefined>(() => componentSettings === undefined ? undefined : {
    has: packageName => componentSettings.has(packageName),
    render: (entry, writableNow) => componentSettings.render(entry, {
      enabled: entry.enabled, label: componentCopy(entry, language).label, writable: writableNow, language,
      ...(sessionId === undefined ? {} : { sessionId }),
      ...(workspaceId === undefined ? {} : { workspace: { id: workspaceId, ...(workspaceLabel === undefined ? {} : { label: workspaceLabel }) } }),
    }),
  }, [componentSettings, language, sessionId, workspaceId, workspaceLabel])

  if (coreSnapshot.status === 'unavailable' && interactionSnapshot.status === 'unavailable') {
    return <section className={css.page} aria-label={t('config.aria')}><p className={css.error} role="alert">{t('config.unavailable')}</p></section>
  }

  const coreDisabled = loading || !coreSnapshot.writable
  const readOnlyNotice = connection !== undefined && isRemoteConnection(connection) ? t('config.remoteReadOnly') : t('config.readOnly')
  const interactionDisabled = loading || !interactionSnapshot.writable
  // DSH's row page for one component shows that component's page alone.
  if (component !== undefined) {
    return <section ref={root} className={css.page} aria-label={t('config.aria')} aria-busy={loading}>
      {!writable && !loading && <p className={css.readOnlyNotice}>{readOnlyNotice}</p>}
      <CompositionBoard view={view} system={memorySystem} systemPending={memorySystemState === 'loading'} layers={layerSettings} readOnly={!coreSnapshot.writable} language={language} t={t}
        pages={pages} page={component} {...(settingsRenderer === undefined ? {} : { componentSettings: settingsRenderer })} />
      {feedbackToast}
    </section>
  }
  // The DSH Plugins page draws the plugin's title and description above this
  // section. Composition comes first, then where memory lives, then how the
  // in-conversation surfaces behave.
  return (
    <section ref={root} className={css.page} aria-label={t('config.aria')} aria-busy={loading}>
      {loading ? <p className={css.loading} role="status">{t('common.loading')}</p> : <>
        {/* Like DSH's own settings forms, a read-only document says so above its controls. */}
        {!writable && <p className={css.readOnlyNotice}>{readOnlyNotice}</p>}
        <CompositionBoard view={view} system={memorySystem} systemPending={memorySystemState === 'loading'} layers={layerSettings} readOnly={!coreSnapshot.writable} language={language} t={t}
          pages={pages} {...(settingsRenderer === undefined ? {} : { componentSettings: settingsRenderer })} />

        <MnemonStorageSection scope={scope} value={coreSnapshot.value} user={coreUser} disabled={coreDisabled} dashboard={view.state.dashboard} target={target}
          {...(connection === undefined ? {} : { connection })} {...(sessionId === undefined ? {} : { sessionId })} {...(workspaceId === undefined ? {} : { workspaceId })}
          language={language} t={t} onOpen={entry => pages.open(entry.entryId, 'configuration')} onSaved={() => setTargetRevision(revision => revision + 1)} />

        <section className={css.section} aria-labelledby="mnemon-interface-heading">
          <div className={css.sectionHeading}><h2 id="mnemon-interface-heading">{t('config.interfaceTitle')}</h2></div>
          <div className={css.rows}>
            <SelectRow id="mnemon-display" label={t('config.displayTitle')} value={display.value} disabled={coreDisabled} onChange={display.set} options={[
              { value: 'sidebar', label: t('config.displaySidebar'), detail: t('config.displaySidebarHint') },
              { value: 'builtin', label: t('config.displayBuiltin'), detail: t('config.displayBuiltinHint') },
            ]} />
            <ToggleRow id="mnemon-interaction-turn-bar" label={t('config.interactionTurnBar')} hint={t('config.interactionTurnBarHint')} checked={turnBar.value} disabled={interactionDisabled} onChange={turnBar.set} />
            <ToggleRow id="mnemon-interaction-save-action" label={t('config.interactionSaveAction')} hint={t('config.interactionSaveActionHint')} checked={saveAction.value} disabled={interactionDisabled} onChange={saveAction.set} />
          </div>
          <WriteFailure error={display.failed ?? turnBar.failed ?? saveAction.failed} t={t} />
        </section>

        {/* DSH draws the bundle's component list right below this configuration. */}
        <p className={css.componentListNote}>{t('config.componentListNote')}</p>

        {feedbackToast}
      </>}
    </section>
  )
}
