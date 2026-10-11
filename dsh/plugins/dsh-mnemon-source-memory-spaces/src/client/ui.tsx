import { css, sidebarCss, useT } from './presentation.ts'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react'
import type { MemoryJsonValue } from 'dsh-mnemon/contracts'
import {
  createMemorySourcePageClient, installMemorySourceUI, MemorySourcePageFrame, translateEn, message, PageHeader,
  appearanceClass, type MemorySourcePageProps, type MnemonSourceManagementClient, type MnemonTranslate,
} from 'dsh-mnemon/client'
import type { Insight, MemorySpacesStatus } from '../contracts.ts'
import { OverviewPage, ExplorePage, EntitiesPage, RememberPage, ListPage, PersistenceStrategyDialog, type MemoryPlacementPageConfig } from './pages.tsx'
import type { MemorySpacesPageClient } from './api.ts'

/** The adapter never gets a transport or chooses another Source instance. */
export function memorySpacesPageClient(management: MnemonSourceManagementClient): MemorySpacesPageClient & { status(): Promise<MemorySpacesStatus>; canAssist(operation: string): boolean } {
  const client = createMemorySourcePageClient(management)
  const input = (value: unknown): MemoryJsonValue => JSON.parse(JSON.stringify(value)) as MemoryJsonValue
  return {
    canAssist: client.canAssist,
    status: () => client.read('status-summary'),
    bodies: () => client.read('bodies'),
    bodyDirectory: () => client.read('body-directory'),
    graph: memoryBodyIds => client.read('graph', input({ memoryBodyIds })),
    list: request => client.read('list', input(request ?? {})),
    entities: (entity, limit) => client.read('entities', input({ entity, limit })),
    entityMemories: (entity, offset, limit) => client.read('entity-memories', input({ entity, offset, limit })),
    entityRelated: (entity, limit, view) => client.read('entity-related', input({ entity, limit, view })),
    search: request => client.read('search', input(request)),
    related: (id, memoryBodyId) => client.read('related', input({ id, memoryBodyId })),
    reconnectBody: memoryBodyId => client.read('body-reconnect', { memoryBodyId }),
    remember: request => client.mutate('remember', input(request), true),
    forget: (id, memoryBodyId) => client.mutate('forget', input({ id, memoryBodyId }), true),
    createBody: request => request.placement !== undefined && client.canAssist('body-create') ? client.assist('body-create', input(request), true) : client.mutate('body-create', input(request), true),
    updateBody: (memoryBodyId, request) => typeof request.active === 'boolean' && Object.keys(request).every(key => key === 'active') && client.canAssist('activation')
      ? client.assist('activation', { memoryBodyId, active: request.active }, true) : client.mutate('body-update', input({ memoryBodyId, ...request }), true),
    deleteBody: memoryBodyId => client.mutate('body-delete', { memoryBodyId }, true),
    agentSearch: request => client.assist('agent-search', input(request), false),
    supervise: (content, idempotencyKey) => client.assist('supervise', input({ content, idempotencyKey }), true),
    maintainBodyMetadata: memoryBodyIds => client.assist('body-metadata-maintain', { memoryBodyIds }, true),
  }
}

const rememberedPages = new WeakMap<MnemonSourceManagementClient, Page>()

type Page = 'spaces' | 'explore' | 'entities' | 'remember' | 'content'
const TABS = [{ id: 'spaces', key: 'nav.overview' }, { id: 'explore', key: 'nav.search' }, { id: 'content', key: 'nav.content' }, { id: 'entities', key: 'nav.entities' }] as const

function MemorySpacesSourceView(props: MemorySourcePageProps & { page: Page }): JSX.Element | null {
  const t = useT()
  const client = useMemo(() => props.management === undefined ? undefined : memorySpacesPageClient(props.management), [props.management])
  const [revision, setRevision] = useState(0)
  const [status, setStatus] = useState<MemorySpacesStatus | null>(null)
  const [page, setPage] = useState<Page>(() => props.management !== undefined ? rememberedPages.get(props.management) ?? (props.page === 'remember' ? 'spaces' : props.page) : props.page)
  const [seed, setSeed] = useState('')
  const [rememberOpen, setRememberOpen] = useState(props.page === 'remember')
  const [strategyOpen, setStrategyOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const headerRef = useRef<HTMLElement | null>(null)
  const revealElement = useCallback((element: HTMLElement) => {
    props.onRevealElement?.(element, (headerRef.current?.getBoundingClientRect().height ?? 0) + 14)
  }, [props.onRevealElement])
  const refresh = useCallback(() => { setRevision(value => value + 1); props.onRefresh?.() }, [props.onRefresh])
  // The workspace's refresh reloads these pages as well, the directory included;
  // a change made here reloads only what it touched.
  const refreshKey = useRef(props.refreshKey)
  const [reloads, setReloads] = useState(0)
  useEffect(() => {
    if (refreshKey.current === props.refreshKey) return
    refreshKey.current = props.refreshKey
    setRevision(value => value + 1)
    setReloads(value => value + 1)
  }, [props.refreshKey])
  useEffect(() => {
    setPage(props.management !== undefined && props.navigationInput === undefined ? rememberedPages.get(props.management) ?? (props.page === 'remember' ? 'spaces' : props.page) : props.page === 'remember' ? 'spaces' : props.page)
    setRememberOpen(props.page === 'remember')
    const value = props.navigationInput
    setSeed(typeof value === 'object' && value !== null && !Array.isArray(value) && typeof value.seed === 'string' ? value.seed : '')
  }, [props.page, props.navigationInput])
  useEffect(() => { if (props.management !== undefined) rememberedPages.set(props.management, page) }, [props.management, page])
  useLayoutEffect(() => { props.onResetScroll?.() }, [page, props.onResetScroll])
  useEffect(() => {
    let active = true
    setError(null)
    void client?.status().then(value => { if (active) setStatus(value) }).catch(reason => { if (active) setError(message(reason)) })
    return () => { active = false }
  }, [client, revision])
  if (client === undefined) return null
  const writable = props.writable === true && status?.writeEnabled === true
  const activationEnabled = status?.writeEnabled === true && (writable || client.canAssist('activation'))
  const agentAvailable = client.canAssist('supervise')
  const explore = (query: string) => { setSeed(query); setPage('explore') }
  const remember = (content = '') => { setSeed(content); setRememberOpen(true) }
  const forget = async (insight: Insight) => { await client.forget(insight.id, insight.memoryBodyId); refresh() }
  const bodies = status?.memoryBodies ?? []
  const preferences = props.preferences
  return <>
    {error !== null && <div className={css.inlineError} role="alert">{error}</div>}
    {<section className={sidebarCss.memoryWorkspace} ref={headerRef}>
      <PageHeader title={t('nav.spaces')} description={t('overview.description')} {...(writable ? {} : { meta: activationEnabled ? t('common.activationOnly') : t('common.readOnly') })} action={<div className={css.memoryHeaderActions}><button type="button" className={appearanceClass(css.primaryButton, sidebarCss.memoryWriteButton)} disabled={!writable} onClick={() => remember()}>{t('nav.rememberAction')}</button>{preferences !== undefined && <button type="button" className={css.secondaryButton} onClick={() => setStrategyOpen(true)}>{t('strategy.action')}</button>}</div>} />
      <div className={sidebarCss.memoryNavigation}><div className={sidebarCss.memoryTabs} role="tablist" aria-label={t('nav.memory.aria')}>{TABS.map(tab => <button key={tab.id} type="button" role="tab" aria-selected={page === tab.id} data-active={page === tab.id ? '' : undefined} onClick={() => setPage(tab.id)}>{t(tab.key)}</button>)}</div></div>
    </section>}
    {page === 'spaces' && <OverviewPage client={client} metadataClient={client} revision={revision} reloadKey={reloads} activationEnabled={activationEnabled} writeEnabled={writable} agentAvailable={agentAvailable} fallbackBodies={bodies} fallbackDirectory={status?.memoryBodyDirectory} catalogKnown={status?.memoryBodies !== undefined} onMutate={refresh} onAgentRefresh={refresh} onBodyReconnect={refresh} onBodyMetadata={refresh} onExplore={explore} />}
    {page === 'explore' && <ExplorePage client={client} agentClient={client} agentAvailable={client.canAssist('agent-search')} status={status} seed={seed} writeEnabled={writable} onForget={forget} onRevealElement={revealElement} />}
    {page === 'entities' && <EntitiesPage client={client} revision={revision} writeEnabled={writable} onForget={forget} onExplore={explore} />}
    {page === 'content' && <ListPage client={client} revision={revision} writeEnabled={writable} onForget={forget} onClone={insight => remember(insight.content)} onExplore={explore} />}
    {rememberOpen && <RememberPage client={client} agentAvailable={agentAvailable} memoryBodies={bodies} writeEnabled={writable} seed={seed} onMutate={refresh} onClose={() => setRememberOpen(false)} onView={() => { setRememberOpen(false); setPage('content') }} />}
    {strategyOpen && preferences !== undefined && <PersistenceStrategyDialog client={client} settingsScope={{ setPath: async (path, value) => {
      if (path.length !== 1 || path[0] !== 'persistenceStrategy') throw new Error('Preference path is outside this Source')
      await preferences.replace(JSON.parse(JSON.stringify({ persistenceStrategy: value })) as MemoryJsonValue)
    } }} config={preferences.value as unknown as MemoryPlacementPageConfig} writable={preferences.writable} agentAvailable={agentAvailable} onClose={() => setStrategyOpen(false)} />}
  </>
}

export function MemorySpacesSourcePage(props: MemorySourcePageProps & { page: Page }): ReactNode {
  return <MemorySourcePageFrame locale={props.locale}><MemorySpacesSourceView key={props.sourceInstanceKey} {...props} /></MemorySourcePageFrame>
}

export function installMemorySpacesUI(ctx: Parameters<typeof installMemorySourceUI>[0], t: MnemonTranslate = translateEn): () => void {
  const pages = [
    { id: 'spaces', key: 'nav.spaces', order: 300, glyph: '◇' },
    { id: 'remember', key: 'nav.rememberAction', order: 400, glyph: '+' },
    { id: 'explore', key: 'nav.search', order: 500, glyph: '⌕' },
    { id: 'entities', key: 'nav.entities', order: 600, glyph: '◎' },
    { id: 'content', key: 'nav.content', order: 700, glyph: '≡' },
  ] as const
  return installMemorySourceUI(ctx, { sourceTypeId: 'memory-spaces', pages: pages.map(page => ({
    id: page.id, order: page.order, label: () => t(page.key),
    // This Source pins its compound title/tab header; nested page headings scroll with their content.
    navigation: { stickyHeader: false, group: page.id === 'spaces' ? 'storage' : 'tools', primary: page.id === 'spaces', glyph: page.glyph },
    component: props => <MemorySpacesSourcePage {...props} page={page.id} />,
  })) })
}

export const inject = ['slots', 'locale']
export function apply(ctx: Parameters<typeof installMemorySourceUI>[0]): void { installMemorySpacesUI(ctx, ctx.locale!.bind('mnemon')) }
