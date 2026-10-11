import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type JSX, type ReactNode } from 'react'
import { Button, IconChevronDownOutlineRegular, Switch } from '@deepseek-ai/dsh-client-ui-primitives'
import type {
  ClientConnectionHandle,
  MemoryProviderConfigField,
  MemoryProviderConnection,
  MemoryProviderDescriptor,
  MemoryProviderServiceCatalog,
  MemoryProviderServiceView,
} from "../host/protocol.ts"
import { MnemonClient } from './api.ts'
import { GlobalLocationSetting } from './GlobalLocationSetting.tsx'
import css from './MnemonSettingsCard.module.css'
import { useRequestVersion } from './use-request-version.ts'
import type { MnemonKey, MnemonTranslate } from './locales.ts'
import { message } from './page-kit.tsx'
import { Reveal } from './feedback.tsx'
import { ProviderIcon } from './ProviderIcon.tsx'
import { SelectField } from './page-controls.tsx'
import {
  providerFieldLabel,
  providerOptionLabel,
  providerSummary,
} from './provider-presentation.ts'

interface ProviderSettingsSectionProps {
  connection?: ClientConnectionHandle
  sessionId?: string
  workspaceId?: string
  workspaceLabel?: string
  activeScope: 'global' | 'workspace'
  refreshKey: number
  disabled: boolean
  scopeChanging: boolean
  t: MnemonTranslate
  /** Rendered first in the list; Settings puts Mnemon Native here as a peer of the others. */
  leading?: ReactNode
  /** Why the other Providers cannot be listed now; they are not read while it is set. */
  blocked?: ReactNode
  /** Whether it is not yet known if the Providers can be listed; they are read once it is. */
  pending?: boolean
  /** Why the listed Providers cannot be switched; rendered above them. */
  notice?: ReactNode
}

interface ServiceDraft {
  settings: MemoryProviderConnection
}

const SAVED_SECRET_MASK = '••••••••••••'

const EMPTY_PROVIDER_CATALOG: MemoryProviderServiceCatalog = {
  providers: [],
  items: [],
  generatedAt: '',
}

const providerCatalogCache = new WeakMap<ClientConnectionHandle, Map<string, MemoryProviderServiceCatalog>>()

function catalogRouteKey(sessionId?: string, workspaceId?: string): string {
  return `${sessionId ?? ''}\u0000${workspaceId ?? ''}`
}

function cachedCatalog(connection: ClientConnectionHandle | undefined, key: string): MemoryProviderServiceCatalog | undefined {
  return connection === undefined ? undefined : providerCatalogCache.get(connection)?.get(key)
}

function cacheCatalog(connection: ClientConnectionHandle | undefined, key: string, catalog: MemoryProviderServiceCatalog): void {
  if (connection === undefined) return
  let routes = providerCatalogCache.get(connection)
  if (routes === undefined) {
    routes = new Map()
    providerCatalogCache.set(connection, routes)
  }
  routes.set(key, catalog)
}

function stabilizeProviderCard(element: HTMLElement): void {
  const view = element.ownerDocument.defaultView
  let scrollContainer: HTMLElement | undefined

  for (let ancestor = element.parentElement; ancestor !== null; ancestor = ancestor.parentElement) {
    const overflowY = view?.getComputedStyle(ancestor).overflowY ?? ancestor.style.overflowY
    if (scrollContainer !== undefined && overflowY === 'hidden' && ancestor.scrollTop !== 0) ancestor.scrollTop = 0
    if (scrollContainer === undefined && (overflowY === 'auto' || overflowY === 'scroll') && ancestor.scrollHeight > ancestor.clientHeight) {
      scrollContainer = ancestor
    }
    if (ancestor.getAttribute('role') === 'dialog') break
  }

  if (scrollContainer === undefined) return
  const header = element.firstElementChild instanceof HTMLElement ? element.firstElementChild : element
  const headerRect = header.getBoundingClientRect()
  const containerRect = scrollContainer.getBoundingClientRect()
  if (headerRect.top < containerRect.top) scrollContainer.scrollTop -= containerRect.top - headerRect.top
  else if (headerRect.bottom > containerRect.bottom) scrollContainer.scrollTop += headerRect.bottom - containerRect.bottom
}

function serviceFields(provider: MemoryProviderDescriptor): MemoryProviderConfigField[] {
  return provider.fields.filter(field => field.scope === 'service')
}

function globalLocationFields(provider: MemoryProviderDescriptor): MemoryProviderConfigField[] {
  return serviceFields(provider).filter(field => field.role === 'global-location')
}

function serviceDefaults(provider: MemoryProviderDescriptor): MemoryProviderConnection {
  return Object.fromEntries(serviceFields(provider).flatMap(field => field.defaultValue === undefined ? [] : [[field.key, field.defaultValue]]))
}

function draftFor(provider: MemoryProviderDescriptor, service: MemoryProviderServiceView): ServiceDraft {
  return { settings: { ...serviceDefaults(provider), ...service.settings, ...service.secretValues } }
}

function configurationComplete(provider: MemoryProviderDescriptor, draft: ServiceDraft, service: MemoryProviderServiceView): boolean {
  return serviceFields(provider).every(field => {
    if (!field.required) return true
    if (field.input === 'secret' && service.configuredSecrets.includes(field.key)) return true
    const value = draft.settings[field.key]
    return field.input === 'boolean' ? typeof value === 'boolean' : String(value ?? '').trim() !== ''
  })
}

function SecretVisibilityIcon({ visible }: { visible: boolean }): JSX.Element {
  return <svg viewBox="0 0 20 20" fill="none" aria-hidden="true">
    <path d="M2.3 10s2.8-4.5 7.7-4.5 7.7 4.5 7.7 4.5-2.8 4.5-7.7 4.5S2.3 10 2.3 10Z" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
    <circle cx="10" cy="10" r="2.1" stroke="currentColor" strokeWidth="1.4" />
    {visible && <path d="m3.5 3.5 13 13" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />}
  </svg>
}

function ServiceField(props: {
  field: MemoryProviderConfigField
  value: string | number | boolean | undefined
  configuredSecrets: string[]
  disabled: boolean
  t: MnemonTranslate
  onChange: (value: string | number | boolean) => void
}): JSX.Element {
  const [secretVisible, setSecretVisible] = useState(false)
  const label = providerFieldLabel(props.t, props.field)
  const savedSecret = props.configuredSecrets.includes(props.field.key)
  const required = props.field.required && !savedSecret
  const secret = props.field.input === 'secret'
  const fieldValue = String(props.value ?? '')
  const showingSavedMask = secret && savedSecret && fieldValue === ''
  const displayValue = showingSavedMask
    ? secretVisible ? props.t('config.providerSecretStoredValue') : SAVED_SECRET_MASK
    : fieldValue
  const input = props.field.input === 'boolean'
    ? <label className={css.providerBoolean}><input aria-label={label} type="checkbox" checked={Boolean(props.value)} disabled={props.disabled} onChange={event => props.onChange(event.target.checked)} /><span>{label}</span></label>
    : props.field.input === 'select'
      ? <SelectField label={label} value={String(props.value ?? '')} disabled={props.disabled} options={(props.field.options ?? []).map(option => ({ value: option.value, label: providerOptionLabel(props.t, option) }))} onChange={next => props.onChange(next)} />
      : <label>{label}<div className={secret ? css.providerSecretInput : undefined}><input aria-label={label} type={secret ? secretVisible ? 'text' : 'password' : props.field.input === 'number' ? 'number' : props.field.input === 'url' ? 'url' : 'text'} value={displayValue} required={required} disabled={props.disabled} autoComplete={secret ? 'new-password' : undefined} placeholder={props.field.placeholder ?? (secret ? props.t('overview.providerApiKeyOptional') : undefined)} maxLength={props.field.maxLength ?? (secret ? 8000 : 2000)} min={props.field.min} max={props.field.max} pattern={props.field.pattern} step={props.field.input === 'number' ? 'any' : undefined} onFocus={event => {
        if (showingSavedMask) event.currentTarget.select()
      }} onClick={event => {
        if (showingSavedMask) event.currentTarget.select()
      }} onChange={event => {
        const value = showingSavedMask ? event.target.value.replace(SAVED_SECRET_MASK, '').replace(props.t('config.providerSecretStoredValue'), '') : event.target.value
        props.onChange(value)
      }} />{secret && <button type="button" className={css.providerSecretVisibility} aria-label={props.t(secretVisible ? 'config.providerSecretHide' : 'config.providerSecretShow')} title={props.t(secretVisible ? 'config.providerSecretHide' : 'config.providerSecretShow')} aria-pressed={secretVisible} disabled={props.disabled} onClick={() => setSecretVisible(value => !value)}><SecretVisibilityIcon visible={secretVisible} /></button>}</div></label>
  return <div className={css.providerFieldControl} data-input={props.field.input}>
    {input}
  </div>
}

function ProviderServiceForm(props: {
  provider: MemoryProviderDescriptor
  service: MemoryProviderServiceView
  activeScope: 'global' | 'workspace'
  disabled: boolean
  t: MnemonTranslate
  onSave: (provider: MemoryProviderDescriptor, draft: ServiceDraft) => Promise<void>
}): JSX.Element {
  const [draft, setDraft] = useState<ServiceDraft>(() => draftFor(props.provider, props.service))
  const [customLocations, setCustomLocations] = useState<Set<string>>(() => new Set(globalLocationFields(props.provider).filter(field => String(props.service.settings[field.key] ?? '').trim() !== '').map(field => field.key)))
  const [saving, setSaving] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const formRef = useRef<HTMLFormElement>(null)
  const stabilizeAfterLocationLayout = useRef(false)

  useEffect(() => {
    setDraft(draftFor(props.provider, props.service))
    setCustomLocations(new Set(globalLocationFields(props.provider).filter(field => String(props.service.settings[field.key] ?? '').trim() !== '').map(field => field.key)))
  }, [props.provider, props.service])

  useLayoutEffect(() => {
    if (!stabilizeAfterLocationLayout.current || formRef.current === null) return
    stabilizeAfterLocationLayout.current = false
    stabilizeProviderCard(formRef.current.closest<HTMLElement>('[data-provider]') ?? formRef.current)
  }, [customLocations])

  const submit = async (event: FormEvent): Promise<void> => {
    event.preventDefault()
    const locationsComplete = props.activeScope === 'workspace' || globalLocationFields(props.provider).every(field => !customLocations.has(field.key) || String(draft.settings[field.key] ?? '').trim() !== '')
    if (!configurationComplete(props.provider, draft, props.service) || !locationsComplete || saving || props.disabled) return
    setSaving(true); setFailed(null); setSaved(false)
    const settings = { ...draft.settings }
    for (const field of globalLocationFields(props.provider)) {
      if (props.activeScope === 'workspace' || !customLocations.has(field.key)) settings[field.key] = ''
    }
    try { await props.onSave(props.provider, { settings }); setSaved(true) } catch (reason) { setFailed(message(reason)) } finally { setSaving(false) }
  }

  const update = (key: string, value: string | number | boolean): void => {
    setDraft(current => ({ ...current, settings: { ...current.settings, [key]: value } }))
    setFailed(null); setSaved(false)
  }

  const useCustomLocation = (field: MemoryProviderConfigField, custom: boolean): void => {
    stabilizeAfterLocationLayout.current = true
    setCustomLocations(current => {
      const next = new Set(current)
      if (custom) next.add(field.key)
      else next.delete(field.key)
      return next
    })
    setFailed(null); setSaved(false)
  }

  const stabilizeLocationCard = (): void => {
    if (formRef.current === null) return
    stabilizeProviderCard(formRef.current.closest<HTMLElement>('[data-provider]') ?? formRef.current)
  }

  const locations = globalLocationFields(props.provider)
  const regularFields = serviceFields(props.provider).filter(field => field.role !== 'global-location')
  const locationsComplete = props.activeScope === 'workspace' || locations.every(field => !customLocations.has(field.key) || String(draft.settings[field.key] ?? '').trim() !== '')
  const formComplete = configurationComplete(props.provider, draft, props.service) && locationsComplete

  return <form ref={formRef} className={css.providerServiceForm} onSubmit={event => void submit(event)} data-provider={props.provider.id}>
    <p className={css.providerServicePrompt}>{props.t(props.service.configured ? 'config.providerServiceHint' : 'config.providerEnableHint')}</p>
    {locations.map(field => <GlobalLocationSetting
      key={field.key}
      className={css.providerServiceLocation}
      name={`${props.provider.id}-${field.key}-location`}
      ariaLabel={`${props.provider.label} ${props.t('config.providerGlobalLocation')}`}
      label={props.t('config.providerGlobalLocation')}
      hint={props.t(props.activeScope === 'workspace' ? 'config.providerGlobalLocationWorkspaceHint' : 'config.providerGlobalLocationHint', { provider: props.provider.label })}
      defaultLabel={props.t('config.providerDefaultLocation')}
      customLabel={props.t('config.custom')}
      custom={customLocations.has(field.key)}
      workspace={props.activeScope === 'workspace'}
      disabled={props.disabled || saving}
      onInteract={stabilizeLocationCard}
      onChange={custom => useCustomLocation(field, custom)}
    >
      <div className={css.providerLocationField}><ServiceField field={field} value={draft.settings[field.key]} configuredSecrets={props.service.configuredSecrets} disabled={props.disabled || saving} t={props.t} onChange={value => update(field.key, value)} /></div>
    </GlobalLocationSetting>)}
    <div className={css.providerSettingsGrid}>
      {regularFields.map(field => <ServiceField key={field.key} field={field} value={draft.settings[field.key]} configuredSecrets={props.service.configuredSecrets} disabled={props.disabled || saving} t={props.t} onChange={value => update(field.key, value)} />)}
    </div>
    <div className={`${css.memoryConfigFooter} ${css.providerServiceFooter}`}>
      <div className={css.configFeedback} aria-live="polite">{failed !== null && <span className={css.error}>{props.t('config.providerSaveFailed', { error: failed })}</span>}{saved && <span className={css.packSuccess}>{props.t('config.providerServiceSaved')}</span>}</div>
      <Button type="submit" variant="primary" size="sm" disabled={props.disabled || saving || !formComplete}>{saving ? props.t('config.saving') : props.t(props.service.configured ? 'config.saveProviderService' : 'config.enableProvider')}</Button>
    </div>
  </form>
}

function ProviderPanel(props: {
  provider: MemoryProviderDescriptor
  service: MemoryProviderServiceView
  disabled: boolean
  t: MnemonTranslate
  onSave: (provider: MemoryProviderDescriptor, draft: ServiceDraft) => Promise<void>
  onToggle: (provider: MemoryProviderDescriptor, enabled: boolean) => Promise<MemoryProviderServiceView>
  activeScope: 'global' | 'workspace'
}): JSX.Element {
  const [enabled, setEnabled] = useState(props.service.enabled)
  const [expanded, setExpanded] = useState(props.service.enabled && !props.service.configured)
  const [toggling, setToggling] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)
  const rowRef = useRef<HTMLDivElement>(null)
  const stabilizeAfterLayout = useRef(false)

  useLayoutEffect(() => {
    if (!stabilizeAfterLayout.current || rowRef.current === null) return
    stabilizeAfterLayout.current = false
    stabilizeProviderCard(rowRef.current)
  }, [enabled, expanded])

  useEffect(() => {
    if (toggling) return
    setEnabled(props.service.enabled)
    if (!props.service.enabled) setExpanded(false)
  }, [props.service.enabled, toggling])

  const toggle = async (next: boolean): Promise<void> => {
    setFailed(null)
    stabilizeAfterLayout.current = true
    if (next && !props.service.configured) {
      setEnabled(true)
      setExpanded(true)
      return
    }
    if (!next && !props.service.enabled) {
      setEnabled(false)
      setExpanded(false)
      return
    }
    const restoreEnabled = enabled
    const restoreExpanded = expanded
    setEnabled(next)
    if (!next && expanded) setExpanded(false)
    setToggling(true)
    try {
      const updated = await props.onToggle(props.provider, next)
      setEnabled(updated.enabled)
      if (!next) setExpanded(false)
    } catch (reason) {
      setEnabled(restoreEnabled)
      if (restoreExpanded) setExpanded(true)
      setFailed(message(reason))
    } finally {
      setToggling(false)
    }
  }

  const stateKey: MnemonKey = enabled
    ? props.service.configured ? 'config.providerEnabled' : 'config.providerNeedsConfiguration'
    : props.service.configured ? 'config.providerDisabledConfigured' : 'config.providerDisabled'
  const providerScope = props.provider.workspaceBinding === 'provider-global' ? 'global' : props.activeScope
  const controlDisabled = props.disabled || toggling
  const toggleExpanded = (): void => {
    stabilizeAfterLayout.current = true
    setExpanded(value => !value)
  }
  return <div
    ref={rowRef}
    className={css.providerRow}
    data-provider={props.provider.id}
    data-enabled={enabled || undefined}
    data-expanded={expanded || undefined}
    role="group"
    aria-label={`${props.provider.label} ${props.t('config.providerServiceTitle')}`}
  >
    <div className={css.providerRowHeader}>
      <button type="button" className={css.providerDisclosure} aria-expanded={expanded} disabled={!enabled || controlDisabled} onClick={toggleExpanded}>
        <span className={css.providerIdentity}><ProviderIcon providerId={props.provider.id} icon={props.provider.icon} className={css.providerMark} /><span><strong>{props.provider.label}</strong><small>{providerSummary(props.t, props.provider)}</small></span></span>
        {enabled && <IconChevronDownOutlineRegular className={css.providerChevron} size={14} />}
      </button>
      <div className={css.providerEnableControl}>
        <span className={css.providerScopeTag} data-scope={providerScope}>{props.t(`config.${providerScope}`)}</span>
        <span className={css.providerState} data-enabled={enabled || undefined}>{props.t(stateKey)}</span>
        <Switch className={css.providerToggle} checked={enabled} label={props.t('config.providerToggleAria', { provider: props.provider.label })} disabled={controlDisabled} onChange={next => void toggle(next)} />
      </div>
    </div>
    {failed !== null && <p className={css.providerToggleError} role="alert">{props.t('config.providerToggleFailed', { error: failed })}</p>}
    {enabled && expanded && <div className={css.providerInlineBody}><ProviderServiceForm provider={props.provider} service={props.service} activeScope={props.activeScope} disabled={controlDisabled} t={props.t} onSave={props.onSave} /></div>}
  </div>
}

export function ProviderSettingsSection(props: ProviderSettingsSectionProps): JSX.Element {
  const client = useMemo(() => props.connection === undefined ? null : new MnemonClient(props.connection, props.sessionId, props.workspaceId), [props.connection, props.sessionId, props.workspaceId])
  const routeKey = catalogRouteKey(props.sessionId, props.workspaceId)
  const initialCatalog = cachedCatalog(props.connection, routeKey)
  const [catalog, setCatalog] = useState<MemoryProviderServiceCatalog>(() => initialCatalog ?? EMPTY_PROVIDER_CATALOG)
  const [loading, setLoading] = useState(client !== null && initialCatalog === undefined)
  const [failed, setFailed] = useState<string | null>(null)
  const loadRequests = useRequestVersion()

  const load = useCallback(async (quiet = false) => {
    if (client === null) return
    const request = loadRequests.begin()
    if (!quiet) setLoading(true)
    setFailed(null)
    try {
      const next = await client.providerServices()
      if (!loadRequests.isCurrent(request)) return
      cacheCatalog(props.connection, routeKey, next)
      setCatalog(next)
    } catch (reason) {
      if (!loadRequests.isCurrent(request)) return
      setFailed(message(reason))
    } finally {
      if (!quiet && loadRequests.isCurrent(request)) setLoading(false)
    }
  }, [client, loadRequests, props.connection, routeKey])

  const blocked = props.blocked !== undefined
  const pending = props.pending === true
  useEffect(() => {
    if (blocked || pending) {
      loadRequests.begin()
      setLoading(pending)
      setFailed(null)
      return
    }
    const cached = cachedCatalog(props.connection, routeKey)
    setCatalog(cached ?? EMPTY_PROVIDER_CATALOG)
    setLoading(client !== null && cached === undefined)
    void load(cached !== undefined)
  }, [blocked, client, load, loadRequests, pending, props.connection, props.refreshKey, routeKey])

  const acceptService = useCallback((service: MemoryProviderServiceView): void => {
    setCatalog(current => {
      const items = current.items.some(item => item.providerId === service.providerId)
        ? current.items.map(item => item.providerId === service.providerId ? service : item)
        : [...current.items, service]
      const next = { ...current, items, generatedAt: new Date().toISOString() }
      cacheCatalog(props.connection, routeKey, next)
      return next
    })
  }, [props.connection, routeKey])

  const save = async (provider: MemoryProviderDescriptor, draft: ServiceDraft): Promise<void> => {
    if (client === null) throw new Error(props.t('config.providerUnavailable'))
    const settings = Object.fromEntries(Object.entries(draft.settings).filter(([key, value]) => serviceFields(provider).find(field => field.key === key)?.input !== 'secret' || String(value).trim() !== ''))
    acceptService(await client.updateProviderService({ providerId: provider.id, settings, enabled: true }))
  }

  const toggle = async (provider: MemoryProviderDescriptor, enabled: boolean): Promise<MemoryProviderServiceView> => {
    if (client === null) throw new Error(props.t('config.providerUnavailable'))
    const updated = await client.updateProviderService({ providerId: provider.id, settings: {}, enabled })
    acceptService(updated)
    return updated
  }

  const disabled = props.disabled || props.scopeChanging || client === null || loading || catalog.generatedAt === ''
  // Explanations slide in and out, so the list does not jump when a dependency changes.
  if (blocked) return <><Reveal>{props.blocked}</Reveal><div className={css.providerList}>{props.leading}</div></>
  return <>
    <Reveal>{props.notice ?? null}</Reveal>
    {props.scopeChanging && <p className={css.scopeChanging} role="status">{props.t('config.saveScopeBeforeProviders')}</p>}
    {props.workspaceLabel !== undefined && <p className={css.providerTarget}>{props.t('config.providerTargetWorkspace', { workspace: props.workspaceLabel })}</p>}
    {loading && <span className={css.visuallyHidden} role="status">{props.t('config.loadingProviders')}</span>}
    {failed !== null && <div className={css.providerLoadError}><span className={css.error}>{props.t('config.providerLoadFailed', { error: failed })}</span><Button variant="ghost" size="sm" onClick={() => void load()}>{props.t('config.retryProviders')}</Button></div>}
    <div className={css.providerList} aria-busy={loading}>{props.leading}{catalog.providers.map(provider => {
      const service = catalog.items.find(item => item.providerId === provider.id) ?? { providerId: provider.id, enabled: false, configured: false, settings: {}, configuredSecrets: [] }
      return <ProviderPanel key={provider.id} provider={provider} service={service} disabled={disabled} activeScope={props.activeScope} t={props.t} onSave={save} onToggle={toggle} />
    })}</div>
  </>
}
