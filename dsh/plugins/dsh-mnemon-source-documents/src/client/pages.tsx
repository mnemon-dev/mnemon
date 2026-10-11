import { css, sidebarCss, useT } from './presentation.ts'
import type { JSX } from 'react'
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type FormEvent } from 'react'
import Markdown from 'markdown-to-jsx'
import { type DocumentRecord, type DocumentSnapshot, type DocumentView } from '../contracts.ts'
import type { DocumentsPageClient } from './api.ts'
import { useRequestVersion, appearanceClass, useLocale, humanBytes, message, PageHeader, ProgressiveFooter, SidebarModal, EmptyState, SearchField } from 'dsh-mnemon/client'

const SAFE_LINK_PATTERN = /^(?:https?:|mailto:|#|\/)/iu

function safeLink(href: string | null | undefined): string | undefined {
  if (href == null) return undefined
  const value = href.trim()
  return SAFE_LINK_PATTERN.test(value) ? value : undefined
}

/** Render managed Markdown without raw HTML and with a deliberately small link surface. */
function DocumentMarkdown(props: { content: string }): JSX.Element {
  return (
    <div className={css.markdownBody}>
      <Markdown options={{
        disableParsingRawHTML: true,
        forceBlock: true,
        overrides: {
          a: {
            component: ({ href, children, ...rest }: { href?: string; children?: JSX.Element | string }) => {
              const target = safeLink(href)
              return target === undefined
                ? <span>{children}</span>
                : <a {...rest} href={target} target={target.startsWith('http') ? '_blank' : undefined} rel={target.startsWith('http') ? 'noreferrer noopener' : undefined}>{children}</a>
            },
          },
        },
      }}>{props.content}</Markdown>
    </div>
  )
}

type DocumentListItem = DocumentRecord & { healthy?: boolean; excerpt: string }

export function DocumentsPage(props: { client: DocumentsPageClient; revision: number; writeEnabled: boolean; sessionId?: string; canCreate?: boolean; documentId?: string; onMutate: () => void }): JSX.Element {
  const t = useT()
  const locale = useLocale()
  const documentCreateFormId = useId()
  const documentEditFormId = useId()
  const pageSize = 8
  const readerRef = useRef<HTMLElement | null>(null)
  const [snapshot, setSnapshot] = useState<DocumentSnapshot | null>(null)
  const [items, setItems] = useState<DocumentListItem[]>([])
  const [visibleLimit, setVisibleLimit] = useState(pageSize)
  const [selectedId, setSelectedId] = useState<string | null>(props.documentId ?? null)
  const [selected, setSelected] = useState<DocumentView | null>(null)
  const [status, setStatus] = useState<'active' | 'archived'>('active')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [composing, setComposing] = useState(false)
  const [editing, setEditing] = useState(false)
  const [confirmArchive, setConfirmArchive] = useState(false)
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [content, setContent] = useState('')
  const [sources, setSources] = useState('')
  const displayRequests = useRequestVersion()

  const display = useCallback(async (nextQuery: string, nextStatus: 'active' | 'archived') => {
    const request = displayRequests.begin()
    setLoading(true); setError(null); setVisibleLimit(pageSize)
    try {
      const current = await props.client.documents()
      const records = nextQuery.trim() === ''
        ? current.documents
        : (await props.client.searchDocuments(nextQuery, nextStatus === 'archived')).results
      const filtered = records.filter(record => record.status === nextStatus)
        .sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt))
      if (!displayRequests.isCurrent(request)) return
      setSnapshot(current)
      setItems(filtered)
      setSelectedId(previous => previous !== null && filtered.some(record => record.id === previous) ? previous : filtered[0]?.id ?? null)
    } catch (reason) {
      if (!displayRequests.isCurrent(request)) return
      setError(message(reason)); setSnapshot(null); setItems([]); setSelectedId(null)
    } finally {
      if (displayRequests.isCurrent(request)) setLoading(false)
    }
  }, [displayRequests, pageSize, props.client])

  useEffect(() => { void display(query, status) }, [display, props.revision, status])
  useEffect(() => {
    setSelected(null)
    if (selectedId === null) return
    let active = true
    void props.client.document(selectedId).then(value => { if (active) setSelected(value) }).catch(reason => { if (active) setError(message(reason)) })
    return () => { active = false }
  }, [props.client, selectedId, props.revision])
  useLayoutEffect(() => {
    if (readerRef.current !== null) readerRef.current.scrollTop = 0
  }, [selectedId])
  useEffect(() => {
    if (selectedId === null) return
    const index = items.findIndex(item => item.id === selectedId)
    if (index >= visibleLimit) setVisibleLimit(Math.ceil((index + 1) / pageSize) * pageSize)
  }, [items, pageSize, selectedId, visibleLimit])

  const resetComposer = () => { setTitle(''); setDescription(''); setContent(''); setSources(''); setComposing(false) }
  const startComposer = () => { setTitle(''); setDescription(''); setContent(''); setSources(''); setEditing(false); setComposing(true) }
  const sourcePaths = (value: string) => value.split(/\r?\n|,/gu).map(path => path.trim()).filter(Boolean)

  const create = async (event: FormEvent) => {
    event.preventDefault(); setSaving(true); setError(null); setNotice(null)
    try {
      const result = await props.client.mutateDocument({ action: 'create', title, description, content, sourcePaths: sourcePaths(sources) })
      setNotice(result.maintenance === undefined ? t('documents.created') : t('documents.createdAfterArchive', { count: result.maintenance.archivedDocumentIds.length }))
      setStatus('active'); setQuery(''); resetComposer(); props.onMutate(); await display('', 'active'); setSelectedId(result.document.id)
    } catch (reason) { setError(message(reason)) } finally { setSaving(false) }
  }

  const beginEdit = () => {
    if (selected === null) return
    setTitle(selected.title); setDescription(selected.description); setContent(selected.content); setSources(selected.sourcePaths.join('\n')); setEditing(true); setComposing(false); setConfirmArchive(false)
  }

  const update = async (event: FormEvent) => {
    event.preventDefault()
    if (selected === null) return
    setSaving(true); setError(null); setNotice(null)
    try {
      const result = await props.client.mutateDocument({ action: 'update', id: selected.id, title, description, content, sourcePaths: sourcePaths(sources) })
      setNotice(result.maintenance === undefined ? t('documents.updated') : t('documents.updatedAfterArchive', { count: result.maintenance.archivedDocumentIds.length }))
      setEditing(false); props.onMutate(); await display(query, status); setSelectedId(result.document.id)
    } catch (reason) { setError(message(reason)) } finally { setSaving(false) }
  }

  const archive = async () => {
    if (selected === null) return
    setSaving(true); setError(null); setNotice(null)
    try {
      const result = await props.client.archiveDocument(selected.id)
      setNotice(t('documents.archived', { spaces: result.maintenance?.memoryBodyIds.join(', ') || '—' }))
      setConfirmArchive(false); setStatus('archived'); setQuery(''); props.onMutate(); await display('', 'archived'); setSelectedId(result.document.id)
    } catch (reason) { setError(message(reason)) } finally { setSaving(false) }
  }

  const usage = snapshot === null ? 0 : Math.min(100, snapshot.activeBytes / snapshot.limitBytes * 100)
  const activeCount = snapshot?.activeCount ?? 0
  const archivedCount = snapshot?.archivedCount ?? 0
  const composer = <form id={documentCreateFormId} className={css.documentEditor} onSubmit={event => void create(event)}>
    <header><div><h3>{t('documents.newTitle')}</h3><p>{t('documents.editorHint')}</p></div><span>{t('documents.managedCopy')}</span></header>
    <div className={css.documentEditorMeta}><label>{t('documents.name')}<input value={title} onChange={event => setTitle(event.target.value)} required /></label><label>{t('documents.routing')}<input value={description} onChange={event => setDescription(event.target.value)} /></label></div>
    <label>{t('documents.sources')}<input value={sources} onChange={event => setSources(event.target.value)} placeholder={t('documents.sourcesPlaceholder')} /></label>
    <label>{t('documents.markdown')}<textarea value={content} onChange={event => setContent(event.target.value)} rows={10} required /></label>
  </form>
  const editComposer = selected === null ? null : <form id={documentEditFormId} className={css.documentEditor} onSubmit={event => void update(event)}>
    <header><div><h3>{t('documents.editTitle')}</h3><p>{t('documents.editorHint')}</p></div><code>{selected.id}</code></header>
    <div className={css.documentEditorMeta}><label>{t('documents.name')}<input value={title} onChange={event => setTitle(event.target.value)} required /></label><label>{t('documents.routing')}<input value={description} onChange={event => setDescription(event.target.value)} /></label></div>
    <label>{t('documents.sources')}<input value={sources} onChange={event => setSources(event.target.value)} /></label><label>{t('documents.markdown')}<textarea value={content} onChange={event => setContent(event.target.value)} rows={18} required /></label>
  </form>
  const documentEditActionClass = appearanceClass(css.ghostButton, appearanceClass(sidebarCss.itemActionButton, sidebarCss.itemEditAction))
  const documentArchiveActionClass = appearanceClass(css.dangerButton, appearanceClass(sidebarCss.itemActionButton, sidebarCss.itemDangerAction))
  const visibleItems = items.slice(0, visibleLimit)
  const selectDocument = (documentId: string) => {
    if (selectedId === documentId) return
    setSelected(null)
    setSelectedId(documentId)
    setEditing(false)
    setConfirmArchive(false)
  }

  return (
    <div className={css.page}>
      <PageHeader title={t('documents.title')} description={t('documents.description')} meta={snapshot === null ? t('common.loading') : t('documents.capacity', { used: humanBytes(snapshot.activeBytes), limit: humanBytes(snapshot.limitBytes) })} {...(props.writeEnabled && (props.canCreate ?? props.sessionId !== undefined) ? { action: <button type="button" className={css.primaryButton} onClick={startComposer}>{t('documents.new')}</button> } : {})} />
      {error !== null && <div className={css.inlineError} role="alert">{error}</div>}
      {notice !== null && <div className={css.runtimeNotice} role="status">{notice}</div>}

      <section className={css.documentSummary} aria-label={t('documents.summary')}>
        <article><span>{t('documents.active')}</span><strong>{activeCount}</strong><small>{t('documents.activeHint')}</small></article>
        <article><span>{t('documents.archivedCount')}</span><strong>{archivedCount}</strong><small>{t('documents.archivedHint')}</small></article>
        <article className={css.documentCapacity}><span>{t('documents.activeCapacity')}</span><strong>{snapshot === null ? '—' : `${usage.toFixed(1)}%`}</strong><div><i style={{ width: `${usage}%` }} /></div><small>{t('documents.capacityHint')}</small></article>
      </section>

      <section className={css.documentToolbar}>
        <form onSubmit={event => { event.preventDefault(); void display(query, status) }}><SearchField className={css.documentSearch} label={t('documents.searchAria')} value={query} onChange={event => setQuery(event.target.value)} placeholder={t('documents.searchPlaceholder')} /><button type="submit" className={css.secondaryButton}>{t('documents.search')}</button></form>
        <div role="group" aria-label={t('documents.scope')}><button type="button" data-active={status === 'active' || undefined} onClick={() => setStatus('active')}>{t('documents.active')} <b>{activeCount}</b></button><button type="button" data-active={status === 'archived' || undefined} onClick={() => setStatus('archived')}>{t('documents.archivedCount')} <b>{archivedCount}</b></button></div>

      </section>



      <div className={css.documentWorkspace}>
        <aside className={css.documentList} aria-label={t('documents.list')}>
          <header><span>{status === 'active' ? t('documents.activeList') : t('documents.archiveList')}</span><code>{items.length}</code></header>
          {visibleItems.map(document => <button type="button" key={document.id} aria-pressed={selectedId === document.id} data-selected={selectedId === document.id || undefined} onClick={() => selectDocument(document.id)}><div><strong>{document.title}</strong><time dateTime={document.createdAt}>{new Date(document.createdAt).toLocaleDateString(locale)}</time></div><p>{document.description || document.excerpt || t('documents.noDescription')}</p><footer><span>{humanBytes(document.sizeBytes)}</span>{document.healthy === false && <em>{t('documents.missing')}</em>}</footer></button>)}
          {!loading && <ProgressiveFooter compact visible={visibleItems.length} total={items.length} pageSize={pageSize} onMore={() => setVisibleLimit(value => value + pageSize)} />}
          {!loading && items.length === 0 && <div className={css.documentListEmpty}><span>▤</span><strong>{status === 'active' ? t('documents.emptyActive') : t('documents.emptyArchived')}</strong><p>{status === 'active' ? t('documents.emptyActiveText') : t('documents.emptyArchivedText')}</p></div>}
          {loading && <div className={css.loading}>{t('common.loading')}</div>}
        </aside>

        <section ref={readerRef} className={css.documentReader} aria-label={t('documents.reader')} data-scroll-region={''}>
          {selected === null ? <EmptyState glyph="▤" title={t('documents.selectTitle')}>{t('documents.selectText')}</EmptyState> : (<article className={css.documentDetail}>
            <header><div><span>{selected.status === 'active' ? t('documents.active') : t('documents.coldArchive')}</span><h3>{selected.title}</h3><p>{selected.description || t('documents.noDescription')}</p></div><div>{props.writeEnabled && selected.status === 'active' && <button type="button" className={documentEditActionClass} onClick={beginEdit}>{t('documents.edit')}</button>}</div></header>
            <dl><div><dt>{t('documents.path')}</dt><dd><code>{selected.relativePath}</code></dd></div><div><dt>{t('documents.revision')}</dt><dd>{selected.revision}</dd></div><div><dt>{t('documents.hash')}</dt><dd><code>{selected.contentHash.slice(0, 16)}</code></dd></div><div><dt>{t('documents.size')}</dt><dd>{humanBytes(selected.sizeBytes)}</dd></div></dl>
            {selected.sourcePaths.length > 0 && <div className={css.documentSources}><span>{t('documents.sources')}</span>{selected.sourcePaths.map(path => <code key={path}>{path}</code>)}</div>}
            {selected.status === 'archived' && <div className={css.documentArchiveReceipt}><strong>{t('documents.archiveReceipt')}</strong><p>{selected.archiveSummary}</p><div>{selected.memoryBodyIds.map(id => <code key={id}>{id}</code>)}</div></div>}
            <DocumentMarkdown content={selected.content} />
            {props.writeEnabled && selected.status === 'active' && <footer className={css.documentDanger}>{<><div><strong>{t('documents.archiveTitle')}</strong><p>{t('documents.archiveDescription')}</p></div><button type="button" className={documentArchiveActionClass} onClick={() => setConfirmArchive(true)}>{t('documents.archive')}</button></>}</footer>}
          </article>)}
        </section>
      </div>
      <p className={css.runtimeFootnote}>{t('documents.footnote')}</p>
      {composing && <SidebarModal title={t('documents.newTitle')} description={t('documents.editorHint')} busy={saving} onClose={resetComposer} footer={<div className={css.modalFooterActions}><button type="button" data-dialog-close className={css.secondaryButton} disabled={saving} onClick={resetComposer}>{t('common.cancel')}</button><button type="submit" form={documentCreateFormId} className={css.primaryButton} disabled={saving || title.trim() === '' || content.trim() === ''}>{saving ? t('documents.saving') : t('documents.create')}</button></div>}>{composer}</SidebarModal>}
      {editing && selected !== null && <SidebarModal title={t('documents.editTitle')} description={selected.title} busy={saving} onClose={() => setEditing(false)} footer={<div className={css.modalFooterActions}><button type="button" data-dialog-close className={css.secondaryButton} disabled={saving} onClick={() => setEditing(false)}>{t('common.cancel')}</button><button type="submit" form={documentEditFormId} className={css.primaryButton} disabled={saving}>{saving ? t('documents.saving') : t('documents.save')}</button></div>}>{editComposer}</SidebarModal>}
      {confirmArchive && selected !== null && <SidebarModal title={t('documents.archiveConfirm')} description={selected.title} busy={saving} onClose={() => setConfirmArchive(false)} footer={<div className={css.modalFooterActions}><button type="button" data-dialog-close data-autofocus className={css.secondaryButton} disabled={saving} onClick={() => setConfirmArchive(false)}>{t('common.cancel')}</button><button type="button" className={css.dangerSolidButton} disabled={saving} onClick={() => void archive()}>{saving ? t('documents.archiving') : t('documents.archiveNow')}</button></div>}><div className={css.bodyDeleteConfirm}><p>{t('documents.archiveDescription')}</p><div className={css.bodyDeleteSummary}><strong>{selected.title}</strong><span>{selected.relativePath} · {humanBytes(selected.sizeBytes)}</span></div></div></SidebarModal>}
    </div>
  )
}
