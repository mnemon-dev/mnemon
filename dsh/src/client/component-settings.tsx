import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react'
import { Button, IconChevronDownOutlineRegular } from '@deepseek-ai/dsh-client-ui-primitives'
import {
  DEFAULT_EMBEDDING_ENDPOINT,
  DEFAULT_EMBEDDING_MODEL,
  DEFAULT_EMBEDDING_PROTOCOL,
  DEFAULT_IDLE_REVIEW,
  MNEMON_EMBEDDING_PROTOCOLS,
  isWorkspaceStorageScope,
  type ClientConnectionHandle,
  type ClientSettingsScope,
  type Config,
  type MnemonEmbeddingStatus,
  type ResolvedIdleReviewConfig,
  type SettingsOperation,
  type TaskAgentModelCatalog,
} from '../host/protocol.ts'
import { MnemonClient } from './api.ts'
import { Callout } from './feedback.tsx'
import { installMemoryComponentUI, type MemoryComponentSettingsProps, type MemoryComponentUIContext } from './component-ui.tsx'
import type { MnemonTranslate } from './locales.ts'
import css from './MnemonSettingsCard.module.css'
import { message } from './page-kit.tsx'
import { ProviderIcon } from './ProviderIcon.tsx'
import { ProviderSettingsSection } from './ProviderSettingsSection.tsx'
import { SelectRow, ToggleRow, type SettingOption } from './settings-controls.tsx'
import { SelectField } from './page-controls.tsx'
import { PanelActions, useLive, useScope, useStaged, WriteFailure } from './settings-panel.tsx'

/** The packages whose own settings dsh-mnemon supplies, through the same region an installed component uses. */
export const RUNTIME_PACKAGE = 'dsh-mnemon-source-runtime'
export const MEMORY_SPACES_PACKAGE = 'dsh-mnemon-source-memory-spaces'
export const THREE_TIER_PACKAGE = 'dsh-mnemon-strategy-default-three-tier'

/** What a shipped component's settings read and write: dsh-mnemon's own settings and Host connection. */
export interface ShippedSettingsServices {
  scope: ClientSettingsScope<Config>
  connection?: ClientConnectionHandle
  t: MnemonTranslate
}

/**
 * Register the settings dsh-mnemon keeps for its shipped components, the way
 * an installed component registers its own: Runtime Memory carries where its
 * user profile lives, Memory Spaces its Providers and embedding, the Layered
 * strategy the background tasks it drives.
 */
export function installShippedComponentSettings(ctx: MemoryComponentUIContext, services: ShippedSettingsServices): () => void {
  const disposers = [
    installMemoryComponentUI(ctx, { packageName: RUNTIME_PACKAGE, settings: page => <RuntimeSettings {...services} page={page} /> }),
    installMemoryComponentUI(ctx, { packageName: MEMORY_SPACES_PACKAGE, settings: page => <MemorySpacesSettings {...services} page={page} /> }),
    installMemoryComponentUI(ctx, { packageName: THREE_TIER_PACKAGE, settings: page => <ThreeTierSettings {...services} page={page} /> }),
  ]
  return () => { for (const dispose of disposers.reverse()) dispose() }
}

// ---- Runtime Memory: where its user profile lives ----

/** Runtime Memory's own setting: whether USER.md follows the storage scope or is shared by every workspace. */
export function RuntimeSettings(props: ShippedSettingsServices & { page: MemoryComponentSettingsProps }): JSX.Element {
  const { scope, page, t } = props
  const snapshot = useScope(scope)
  const saved: 'storage' | 'global' = snapshot.value?.runtimeUserScope === 'global' ? 'global' : 'storage'
  const profile = useLive(saved, async value => { await scope.mutate([{ op: 'set', path: ['runtimeUserScope'], value }]) })
  const disabled = !page.writable || snapshot.status === 'loading' || !snapshot.writable
  return <section className={css.panelSection} aria-label={t('config.runtimeUserScopeTitle')}>
    <div className={css.rows}>
      <SelectRow id="mnemon-runtime-user-scope" label={t('config.runtimeUserScopeTitle')} value={profile.value} disabled={disabled} onChange={profile.set} options={[
        { value: 'storage', label: t('config.runtimeUserScopeStorage'), detail: t('config.runtimeUserScopeStorageHint') },
        { value: 'global', label: t('config.runtimeUserScopeGlobal'), detail: t('config.runtimeUserScopeGlobalHint') },
      ]} />
    </div>
    <WriteFailure error={profile.failed} t={t} />
  </section>
}

// ---- Memory Spaces: Providers and the Native embedding runtime ----

/** The embedding connection DSH manages for Mnemon Native. */
interface EmbeddingDraft {
  endpoint: string
  model: string
  apiKey: string
  protocol: string
}

function embeddingDraft(value: Config | undefined): EmbeddingDraft {
  const embedding = value?.embedding
  return {
    endpoint: embedding?.endpoint?.trim() || DEFAULT_EMBEDDING_ENDPOINT,
    model: embedding?.model?.trim() || DEFAULT_EMBEDDING_MODEL,
    apiKey: embedding?.apiKey?.trim() ?? '',
    protocol: embedding?.protocol ?? DEFAULT_EMBEDDING_PROTOCOL,
  }
}

export function validEmbeddingEndpoint(value: string): boolean {
  const endpoint = value.trim()
  if (endpoint === '' || endpoint.length > 2048) return false
  try {
    const parsed = new URL(endpoint)
    return ['http:', 'https:'].includes(parsed.protocol)
      && parsed.username === '' && parsed.password === ''
      && !endpoint.includes('?') && !endpoint.includes('#')
  } catch {
    return false
  }
}

function validEmbeddingModel(value: string): boolean {
  const model = value.trim()
  return model.length > 0 && model.length <= 200 && !/[\u0000-\u001f\u007f]/u.test(model)
}

function validEmbeddingApiKey(value: string): boolean {
  const key = value.trim()
  return key.length <= 2048 && !/[\u0000-\u001f\u007f]/u.test(key)
}

const validProtocol = (protocol: string): boolean => MNEMON_EMBEDDING_PROTOCOLS.includes(protocol as typeof MNEMON_EMBEDDING_PROTOCOLS[number])

function embeddingProblem(draft: EmbeddingDraft, t: MnemonTranslate): string | null {
  if (!validEmbeddingEndpoint(draft.endpoint)) return t('config.embeddingEndpointInvalid')
  if (!validEmbeddingModel(draft.model)) return t('config.embeddingModelInvalid')
  if (!validEmbeddingApiKey(draft.apiKey)) return t('config.embeddingApiKeyInvalid')
  if (!validProtocol(draft.protocol)) return t('config.embeddingProtocolInvalid')
  return null
}

/** The stored value: a switched-off override keeps only the fields that are valid. */
function embeddingValue(enabled: boolean, draft: EmbeddingDraft): Record<string, unknown> {
  const endpoint = draft.endpoint.trim().replace(/\/+$/u, '')
  if (enabled) return { enabled: true, endpoint, model: draft.model.trim(), protocol: draft.protocol, apiKey: draft.apiKey.trim() }
  return {
    enabled: false,
    ...(validEmbeddingEndpoint(draft.endpoint) ? { endpoint } : {}),
    ...(validEmbeddingModel(draft.model) ? { model: draft.model.trim() } : {}),
    ...(validProtocol(draft.protocol) ? { protocol: draft.protocol } : {}),
    ...(validEmbeddingApiKey(draft.apiKey) ? { apiKey: draft.apiKey.trim() } : {}),
  }
}

/**
 * Memory Spaces' own settings: the Providers that store its spaces, Mnemon
 * Native first with the embedding runtime only Native reads.
 */
export function MemorySpacesSettings(props: ShippedSettingsServices & { page: MemoryComponentSettingsProps }): JSX.Element {
  const { scope, connection, page, t } = props
  const snapshot = useScope(scope)
  const sessionId = page.sessionId
  const workspaceId = page.workspace?.id
  const client = useMemo(() => connection === undefined ? undefined : new MnemonClient(connection, sessionId, workspaceId), [connection, sessionId, workspaceId])
  const saved = embeddingDraft(snapshot.value)
  const managedSaved = snapshot.value?.embedding?.enabled === true
  // Whether DSH manages the embedding is a switch: it applies at once, with the saved connection.
  const managed = useLive(managedSaved, async enabled => { await scope.mutate([{ op: 'set', path: ['embedding'], value: embeddingValue(enabled, saved) } as SettingsOperation]) })
  // The connection is typed, so it waits for its Apply.
  const form = useStaged(saved, async draft => { await scope.mutate([{ op: 'set', path: ['embedding'], value: embeddingValue(managedSaved, draft) } as SettingsOperation]) })
  const [nativeCliFound, setNativeCliFound] = useState<boolean | undefined>(undefined)
  const [status, setStatus] = useState<{ state: 'idle' | 'loading' | 'ready' | 'error'; value?: MnemonEmbeddingStatus; error?: string }>({ state: 'idle' })
  const statusRequest = useRef(0)
  const on = page.component.enabled

  useEffect(() => {
    if (client === undefined || !on) { setNativeCliFound(undefined); return }
    let current = true
    void client.statusSummary().then(summary => { if (current) setNativeCliFound(summary.commandFound) }, () => { if (current) setNativeCliFound(undefined) })
    return () => { current = false }
  }, [client, on, snapshot.revision])
  useEffect(() => {
    statusRequest.current += 1
    setStatus({ state: 'idle' })
  }, [client, snapshot.revision])

  // Providers are read only while their Source runs; the page's switch turns it on.
  if (!on) return <p className={css.panelNote}>{t('spaces.offNote')}</p>

  const test = (): void => {
    if (client === undefined) return
    const request = statusRequest.current + 1
    statusRequest.current = request
    setStatus({ state: 'loading' })
    void client.embeddingStatus().then(
      value => { if (statusRequest.current === request) setStatus({ state: 'ready', value }) },
      reason => { if (statusRequest.current === request) setStatus({ state: 'error', error: message(reason) }) },
    )
  }
  const activeScope = isWorkspaceStorageScope(snapshot.value?.storageScope ?? 'global') ? 'workspace' : 'global'
  const readOnly = snapshot.value?.writeEnabled === false
  const disabled = !page.writable || snapshot.status === 'loading' || !snapshot.writable
  const cliMissing = nativeCliFound === false
  return <section className={css.panelSection} aria-labelledby="mnemon-spaces-providers">
    <div className={css.panelHeading}><h3 id="mnemon-spaces-providers">{t('config.providersTitle')}</h3><p>{t('config.providersDescription')}</p></div>
    <ProviderSettingsSection
      {...(connection === undefined ? {} : { connection })}
      {...(sessionId === undefined ? {} : { sessionId })}
      {...(workspaceId === undefined ? {} : { workspaceId })}
      {...(activeScope !== 'workspace' || page.workspace?.label === undefined ? {} : { workspaceLabel: page.workspace.label })}
      activeScope={activeScope}
      refreshKey={snapshot.revision ?? 0}
      disabled={disabled || readOnly}
      scopeChanging={false}
      {...(readOnly ? { notice: <Callout tone="warning" className={css.groupCallout} title={t('config.providersReadOnlyTitle')}>{t('config.providersReadOnlyDetail')}</Callout> } : {})}
      t={t}
      leading={<NativeProviderCard activeScope={activeScope} cliMissing={cliMissing} t={t}>
        <EmbeddingEditor managed={managed.value} draft={form.draft} disabled={disabled} connectionAvailable={client !== undefined} cliMissing={cliMissing} changing={form.dirty}
          status={status} t={t} onEdit={form.edit} onTest={test}
          onManaged={on => { if (!on) form.discard(); managed.set(on) }}
          actions={<>
            <WriteFailure error={managed.failed} t={t} />
            <PanelActions dirty={form.dirty} saving={form.saving} invalid={embeddingProblem(form.draft, t)} failed={form.failed} applied={form.applied}
              disabled={disabled} t={t} onDiscard={form.discard} onApply={() => { void form.apply() }} />
          </>} />
      </NativeProviderCard>}
    />
  </section>
}

/** Mnemon Native, listed first among the Providers; its body holds the settings only Native reads. */
function NativeProviderCard(props: { activeScope: 'global' | 'workspace'; cliMissing: boolean; t: MnemonTranslate; children: ReactNode }): JSX.Element {
  return <details className={css.providerRow} data-provider="mnemon-native" data-native="">
    <summary className={css.providerRowHeader}>
      <span className={css.providerIdentity}><ProviderIcon providerId="mnemon-native" icon={{ kind: 'brand', value: 'mnemon' }} className={css.providerMark} /><span><strong>{props.t('config.nativeName')}</strong><small>{props.t('config.nativeSummary')}</small></span></span>
      <span className={css.providerEnableControl}>
        <span className={css.providerScopeTag} data-scope={props.activeScope}>{props.t(`config.${props.activeScope}`)}</span>
        <span className={css.providerState} data-enabled={props.cliMissing ? undefined : ''}>{props.t(props.cliMissing ? 'config.nativeCliMissing' : 'config.officialNative')}</span>
        <IconChevronDownOutlineRegular className={css.providerChevron} size={14} />
      </span>
    </summary>
    <div className={css.providerInlineBody}>{props.children}</div>
  </details>
}

/** Reachability and coverage line; the protocol appears only when the Host reports one. */
function embeddingStatusText(t: MnemonTranslate, status: MnemonEmbeddingStatus): string {
  const coverage = { embedded: status.embedded, total: status.totalInsights, coverage: status.coverage }
  return status.protocol === undefined
    ? t(status.available ? 'config.embeddingStatusAvailable' : 'config.embeddingStatusUnavailable', { model: status.model, ...coverage })
    : t(status.available ? 'config.embeddingStatusAvailableWithProtocol' : 'config.embeddingStatusUnavailableWithProtocol', { model: status.model, protocol: status.protocol, ...coverage })
}

/** The embedding runtime: the switch applies at once; the connection fields, shown while DSH manages it, wait for Apply. */
function EmbeddingEditor(props: {
  managed: boolean
  draft: EmbeddingDraft
  disabled: boolean
  connectionAvailable: boolean
  /** The test runs the Mnemon CLI, which only Mnemon Native needs. */
  cliMissing: boolean
  changing: boolean
  status: { state: 'idle' | 'loading' | 'ready' | 'error'; value?: MnemonEmbeddingStatus; error?: string }
  t: MnemonTranslate
  actions: ReactNode
  onManaged: (managed: boolean) => void
  onEdit: (values: Partial<EmbeddingDraft>) => void
  onTest: () => void
}): JSX.Element {
  const { draft, t, status } = props
  const feedback = props.cliMissing ? t('config.embeddingCliMissing')
    : props.changing ? t('config.embeddingSaveBeforeTest')
      : status.state === 'loading' ? t('config.embeddingTesting')
        : status.state === 'error' ? t('config.embeddingStatusFailed', { error: status.error ?? '' })
          : status.state === 'ready' && status.value !== undefined ? embeddingStatusText(t, status.value)
            : !props.connectionAvailable ? t('config.embeddingTestUnavailable') : t('config.embeddingNotTested')
  const text = (field: 'endpoint' | 'model' | 'apiKey', type: string, label: string, valid: boolean, placeholder: string): JSX.Element => <label>
    {label}
    <input type={type} aria-label={label} aria-invalid={!valid} value={draft[field]} disabled={props.disabled}
      autoComplete="off" spellCheck={false} autoCapitalize="none" autoCorrect="off" placeholder={placeholder}
      onChange={event => props.onEdit({ [field]: event.target.value })} />
  </label>
  return <section className={css.editor} aria-labelledby="mnemon-embedding-heading">
    <div className={css.editorHeading}>
      <h3 id="mnemon-embedding-heading">{t('config.embeddingTitle')}</h3>
      <p>{t('config.embeddingDescription')}</p>
    </div>
    <ToggleRow id="mnemon-embedding-managed" label={t('config.embeddingManaged')} hint={t('config.embeddingManagedHint')} checked={props.managed} disabled={props.disabled}
      onChange={props.onManaged} />
    {props.managed && <>
      <div className={css.fieldGrid}>
        {text('endpoint', 'url', t('config.embeddingEndpoint'), validEmbeddingEndpoint(draft.endpoint), DEFAULT_EMBEDDING_ENDPOINT)}
        {text('model', 'text', t('config.embeddingModel'), validEmbeddingModel(draft.model), DEFAULT_EMBEDDING_MODEL)}
        <SelectField label={t('config.embeddingProtocol')} value={draft.protocol} disabled={props.disabled} options={[
          { value: 'auto', label: t('config.embeddingProtocolAuto') },
          { value: 'ollama', label: t('config.embeddingProtocolOllama') },
          { value: 'openai', label: t('config.embeddingProtocolOpenai') },
        ]} onChange={protocol => props.onEdit({ protocol })} />
        {text('apiKey', 'password', t('config.embeddingApiKey'), validEmbeddingApiKey(draft.apiKey), 'sk-…')}
      </div>
      <p className={css.editorNote}>{t('config.embeddingSecurity')}</p>
    </>}
    {props.actions}
    <div className={css.embeddingTest} aria-live="polite">
      <span className={status.state === 'error' ? css.error : undefined} role={status.state === 'error' ? 'alert' : undefined}>{feedback}</span>
      <Button variant="outline" size="sm" disabled={props.disabled || !props.connectionAvailable || props.cliMissing || props.changing || status.state === 'loading'} onClick={props.onTest}>
        {t('config.embeddingTest')}
      </Button>
    </div>
  </section>
}

// ---- Layered strategy: the background tasks it drives ----

/** Where the background task Agents run: DSH's new-session route, or a fixed Provider and model. */
interface Route {
  mode: 'inherit' | 'fixed'
  provider: string
  model: string
}
type ReviewChoice = Pick<ResolvedIdleReviewConfig, 'enabled' | 'runtimeMemory' | 'provider' | 'fallback' | 'agentTeams'>
type ReviewLimits = Pick<ResolvedIdleReviewConfig, 'minIntervalMs' | 'maxPerSession' | 'maxContextChars' | 'maxTokens'>

/** Each limit as the page shows it: the interval in seconds, the others as stored. */
const LIMITS: ReadonlyArray<{ key: keyof ReviewLimits; min: number; max: number; scale: number }> = [
  { key: 'minIntervalMs', min: 5_000, max: 86_400_000, scale: 1_000 },
  { key: 'maxPerSession', min: 0, max: 200, scale: 1 },
  { key: 'maxContextChars', min: 1_000, max: 1_000_000, scale: 1 },
  { key: 'maxTokens', min: 128, max: 131_072, scale: 1 },
]

function routeOf(value: Config | undefined): Route {
  return {
    mode: value?.taskAgentModel?.mode === 'fixed' ? 'fixed' : 'inherit',
    provider: value?.taskAgentModel?.provider?.trim() ?? '',
    model: value?.taskAgentModel?.model?.trim() ?? '',
  }
}

function reviewOf(value: Config | undefined): ResolvedIdleReviewConfig {
  return { ...DEFAULT_IDLE_REVIEW, ...value?.idleReview }
}

const choiceOf = (review: ResolvedIdleReviewConfig): ReviewChoice => ({ enabled: review.enabled, runtimeMemory: review.runtimeMemory, provider: review.provider, fallback: review.fallback, agentTeams: review.agentTeams })
const limitsOf = (review: ResolvedIdleReviewConfig): Record<keyof ReviewLimits, string> =>
  Object.fromEntries(LIMITS.map(limit => [limit.key, String(review[limit.key] / limit.scale)])) as Record<keyof ReviewLimits, string>

/** The value a limit stores, or undefined when the Host would refuse it. */
function limitValue(limit: typeof LIMITS[number], text: string): number | undefined {
  if (text.trim() === '') return undefined
  const value = Math.round(Number(text) * limit.scale)
  return Number.isInteger(value) && value >= limit.min && value <= limit.max ? value : undefined
}

/**
 * The idle review is written whole, as the Host stores it: what is configured
 * now plus what changed, so a setting never set keeps inheriting its default.
 */
function reviewWrite<T extends Partial<ResolvedIdleReviewConfig>>(configured: Config['idleReview'], next: T, before: T): SettingsOperation[] {
  const changed = (Object.keys(next) as Array<keyof T & string>).filter(key => next[key] !== before[key])
  if (changed.length === 0) return []
  return [{ op: 'set', path: ['idleReview'], value: { ...configured, ...Object.fromEntries(changed.map(key => [key, next[key]])) } }]
}

/** The route choosing a fixed model starts from: the saved one, else DSH's default, else the first listed. */
function startingRoute(catalog: TaskAgentModelCatalog, current: Route): { provider: string; model: string } | undefined {
  const listed = (provider: string): boolean => catalog.groups.some(group => group.id === provider)
  const provider = (listed(current.provider) ? current.provider : '') || catalog.defaultSelection?.provider || catalog.groups[0]?.id || ''
  const models = catalog.groups.find(group => group.id === provider)?.models ?? []
  const model = (current.provider === provider ? current.model : '')
    || (catalog.defaultSelection?.provider === provider ? catalog.defaultSelection.model : '') || models[0]?.id || ''
  return provider === '' || model === '' ? undefined : { provider, model }
}

/**
 * The Layered strategy's own settings: the model its background task Agents use,
 * and the idle review that keeps its layers in shape. Choices apply at once;
 * the review limits are typed and wait for their Apply.
 */
export function ThreeTierSettings(props: ShippedSettingsServices & { page: MemoryComponentSettingsProps }): JSX.Element {
  const { scope, connection, page, t } = props
  const snapshot = useScope(scope)
  const savedReview = reviewOf(snapshot.value)
  const route = useLive(routeOf(snapshot.value), async next => {
    await scope.mutate([{ op: 'set', path: ['taskAgentModel'], value: next.mode === 'inherit' ? { mode: 'inherit' } : { mode: 'fixed', provider: next.provider, model: next.model } }])
  })
  const configured = snapshot.value?.idleReview
  const choice = useLive(choiceOf(savedReview), async next => {
    const operations = reviewWrite(configured, next, choiceOf(savedReview))
    if (operations.length > 0) await scope.mutate(operations)
  })
  const limits = useStaged(limitsOf(savedReview), async draft => {
    const next = Object.fromEntries(LIMITS.map(limit => [limit.key, limitValue(limit, draft[limit.key])!])) as ReviewLimits
    const before = Object.fromEntries(LIMITS.map(limit => [limit.key, savedReview[limit.key]])) as ReviewLimits
    const operations = reviewWrite(configured, next, before)
    if (operations.length > 0) await scope.mutate(operations)
  })
  const [catalog, setCatalog] = useState<{ state: 'unavailable' | 'loading' | 'ready' | 'error'; value: TaskAgentModelCatalog | null; error: string | null; full: boolean }>(
    { state: connection === undefined ? 'unavailable' : 'loading', value: null, error: null, full: false })
  const request = useRef(0)
  // What the catalog read so far: DSH's route alone, or every Provider's models too.
  const loaded = useRef<'none' | 'route' | 'full'>('none')
  const load = useCallback((full: boolean): void => {
    if (connection === undefined) return
    const current = ++request.current
    setCatalog(value => ({ ...value, state: 'loading', error: null }))
    void new MnemonClient(connection).taskAgentModels(full).then(value => {
      if (request.current !== current) return
      loaded.current = full ? 'full' : 'route'
      setCatalog({ state: 'ready', value, error: null, full })
    }, reason => {
      if (request.current === current) setCatalog(value => ({ ...value, state: 'error', error: message(reason) }))
    })
  }, [connection])
  useEffect(() => () => { request.current += 1 }, [])
  // A fixed route shows every Provider's models; what was read already is not read again.
  const configuredMode = snapshot.value?.taskAgentModel?.mode === 'fixed' ? 'fixed' : 'inherit'
  useEffect(() => {
    if (configuredMode === 'fixed' ? loaded.current === 'full' : loaded.current !== 'none') return
    load(configuredMode === 'fixed')
  }, [configuredMode, load])
  // Choosing a fixed model before the full catalog arrived takes its first choices once it does.
  const [choosing, setChoosing] = useState(false)
  useEffect(() => {
    if (!choosing) return
    if (catalog.state === 'error') { setChoosing(false); return }
    if (!catalog.full || catalog.value === null) return
    setChoosing(false)
    const start = startingRoute(catalog.value, route.value)
    if (start !== undefined) route.set({ mode: 'fixed', ...start })
  }, [choosing, catalog])
  const chooseMode = (mode: Route['mode']): void => {
    if (mode === 'inherit') { setChoosing(false); route.set({ mode: 'inherit', provider: '', model: '' }); return }
    const start = catalog.full && catalog.value !== null ? startingRoute(catalog.value, route.value) : undefined
    if (start !== undefined) { route.set({ mode: 'fixed', ...start }); return }
    setChoosing(true)
    load(true)
  }
  const disabled = !page.writable || snapshot.status === 'loading' || !snapshot.writable
  return <section className={css.panelSection} aria-labelledby="mnemon-three-tier-background">
    <div className={css.panelHeading}>
      <h3 id="mnemon-three-tier-background">{t('config.backgroundTitle')}</h3>
      <p>{t('config.backgroundDescription')}</p>
      {catalog.state === 'loading' && <span className={css.miniSpinner} aria-hidden="true" />}
    </div>
    {!page.component.enabled && <p className={css.panelNote}>{t('threeTier.offNote')}</p>}
    <div className={css.rows}>
      <TaskAgentModelRows mode={choosing ? 'fixed' : route.value.mode} route={route.value} choosing={choosing} catalog={catalog.value} state={catalog.state} error={catalog.error}
        disabled={disabled} onMode={chooseMode} onRoute={next => route.set({ mode: 'fixed', ...next })} t={t} />
      <IdleReviewRows choice={choice.value} limits={limits} disabled={disabled} onChoice={next => choice.set({ ...choice.value, ...next })} t={t} />
    </div>
    <WriteFailure error={route.failed ?? choice.failed} t={t} />
  </section>
}

function TaskAgentModelRows(props: {
  mode: Route['mode']
  route: Route
  /** A fixed model was chosen and waits for the catalog to pick its first route. */
  choosing: boolean
  catalog: TaskAgentModelCatalog | null
  state: 'unavailable' | 'loading' | 'ready' | 'error'
  error: string | null
  disabled: boolean
  onMode: (mode: Route['mode']) => void
  onRoute: (route: { provider: string; model: string }) => void
  t: MnemonTranslate
}): JSX.Element {
  const { route, t } = props
  const groups = props.catalog?.groups ?? []
  const group = groups.find(candidate => candidate.id === route.provider)
  const inherited = props.catalog?.defaultSelection
    ?? (props.catalog?.effective?.source === 'fixed' ? undefined : props.catalog?.effective)
  const fixed = props.mode === 'fixed' && !props.choosing
  const effective = props.mode === 'fixed'
    ? (!fixed || route.provider === '' || route.model === '' ? undefined : { provider: route.provider, model: route.model })
    : inherited
  // A saved route the catalog no longer lists stays selectable, so it can be read and changed.
  // Two Providers by the same name are told apart by their ids.
  const shared = (name: string): boolean => groups.filter(candidate => candidate.name === name).length > 1
  const providers: SettingOption<string>[] = [
    ...(route.provider !== '' && group === undefined ? [{ value: route.provider, label: route.provider }] : []),
    ...groups.map(candidate => ({ value: candidate.id, label: candidate.name, ...(shared(candidate.name) ? { detail: candidate.id } : {}) })),
  ]
  const models: SettingOption<string>[] = [
    ...(route.model !== '' && group?.models.some(model => model.id === route.model) !== true ? [{ value: route.model, label: route.model }] : []),
    ...(group?.models ?? []).map(model => ({ value: model.id, label: model.name, ...(model.inputModalities?.includes('image') === true ? { detail: t('config.taskAgentImageInput') } : {}) })),
  ]
  const chooseProvider = (provider: string): void => {
    const model = groups.find(candidate => candidate.id === provider)?.models[0]?.id
    if (model !== undefined) props.onRoute({ provider, model })
  }
  return <>
    <SelectRow id="mnemon-task-agent" label={t('config.taskAgentTitle')} value={props.mode} disabled={props.disabled} onChange={props.onMode}
      options={[
        { value: 'inherit', label: t('config.taskAgentInherit'), detail: t('config.taskAgentInheritHint') },
        { value: 'fixed', label: t('config.taskAgentFixed'), detail: t('config.taskAgentFixedHint'), ...(props.state === 'unavailable' ? { disabled: true } : {}) },
      ]} />
    {fixed && <>
      <SelectRow id="mnemon-task-agent-provider" label={t('config.taskAgentProvider')} value={route.provider} disabled={props.disabled || props.state !== 'ready'} options={providers} onChange={chooseProvider} />
      <SelectRow id="mnemon-task-agent-model" label={t('config.taskAgentModel')} value={route.model} disabled={props.disabled || props.state !== 'ready' || group === undefined} options={models}
        onChange={model => props.onRoute({ provider: route.provider, model })} />
    </>}
    <div className={css.rowDetail}>
      <span className={css.effectiveRoute}>
        <span>{t('config.taskAgentEffective')}</span>
        {effective === undefined
          ? <small>{props.state === 'loading' || props.choosing ? t('config.taskAgentLoading') : t('config.taskAgentUnavailable')}</small>
          : <code>{effective.provider} / {effective.model}</code>}
      </span>
      {props.state === 'error' && <p className={css.warning}>{t('config.taskAgentLoadFailed', { error: props.error ?? '' })}</p>}
      {(props.catalog?.failures.length ?? 0) > 0 && groups.length > 0 && <p className={css.warning}>{t('config.taskAgentPartial', { count: props.catalog!.failures.length })}</p>}
    </div>
  </>
}

function IdleReviewRows(props: {
  choice: ReviewChoice
  limits: ReturnType<typeof useStaged<Record<keyof ReviewLimits, string>>>
  disabled: boolean
  onChoice: (next: Partial<ReviewChoice>) => void
  t: MnemonTranslate
}): JSX.Element {
  const { choice, limits, t } = props
  const invalid = LIMITS.find(limit => limitValue(limit, limits.draft[limit.key]) === undefined)
  return <>
    <ToggleRow id="mnemon-idle-review" label={t('config.reviewTitle')} ariaLabel={t('config.reviewEnabled')} hint={t('config.reviewDescription')} checked={choice.enabled} disabled={props.disabled} onChange={enabled => props.onChoice({ enabled })} />
    {choice.enabled && <>
      <ToggleRow id="mnemon-review-runtime-memory" label={t('config.reviewRuntimeMemory')} hint={t('config.reviewRuntimeMemoryHint')} checked={choice.runtimeMemory} disabled={props.disabled} onChange={runtimeMemory => props.onChoice({ runtimeMemory })} />
      <SelectRow id="mnemon-review-provider" label={t('config.reviewProvider')} value={choice.provider} disabled={props.disabled} onChange={provider => props.onChoice({ provider })} options={[
        { value: 'spawn', label: t('config.reviewSpawn'), detail: t('config.reviewSpawnHint') },
        { value: 'fork', label: t('config.reviewFork'), detail: t('config.reviewForkHint') },
      ]} />
      {/* Only a forked review can find its context unavailable and need another way to run. */}
      {choice.provider === 'fork' && <SelectRow id="mnemon-review-fallback" label={t('config.reviewFallback')} value={choice.fallback} disabled={props.disabled} onChange={fallback => props.onChoice({ fallback })} options={[
        { value: 'spawn', label: t('config.reviewSpawn') },
        { value: 'skip', label: t('config.reviewSkip') },
      ]} />}
      <SelectRow id="mnemon-review-teams" label={t('config.reviewAgentTeams')} value={choice.agentTeams} disabled={props.disabled} onChange={agentTeams => props.onChoice({ agentTeams })} options={[
        { value: 'pause', label: t('config.reviewTeamPause'), detail: t('config.reviewTeamPauseHint') },
        { value: 'scoped', label: t('config.reviewTeamScoped'), detail: t('config.reviewTeamScopedHint') },
      ]} />
      <details className={css.advanced}>
        <summary>{t('config.reviewLimits')}<IconChevronDownOutlineRegular size={12} /></summary>
        <div className={css.fieldGrid}>
          {LIMITS.map(limit => <label key={limit.key}>{t(`config.review.${limit.key}`)}
            <input type="number" step="1" inputMode="numeric" value={limits.draft[limit.key]} disabled={props.disabled}
              aria-invalid={limitValue(limit, limits.draft[limit.key]) === undefined}
              onChange={event => limits.edit({ [limit.key]: event.target.value })} />
          </label>)}
        </div>
        <PanelActions dirty={limits.dirty} saving={limits.saving} applied={limits.applied} failed={limits.failed} disabled={props.disabled} t={t}
          invalid={invalid === undefined ? null : t('config.reviewLimitInvalid', { field: t(`config.review.${invalid.key}`), min: invalid.min / invalid.scale, max: invalid.max / invalid.scale })}
          onDiscard={limits.discard} onApply={() => { void limits.apply() }} />
      </details>
    </>}
  </>
}
