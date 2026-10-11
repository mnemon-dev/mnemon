import { useEffect, useMemo, useRef, useState, type JSX, type ReactNode } from 'react'
import {
  createMemorySourcePageClient, installMemorySourceUI, MemorySourcePageFrame, translateEn,
  type MemorySourcePageProps, type MnemonSourceManagementClient, type MnemonTranslate,
} from 'dsh-mnemon/client'
import { DocumentsPage } from './pages.tsx'
import type { DocumentsPageClient } from './api.ts'

export function documentsPageClient(management: MnemonSourceManagementClient): DocumentsPageClient {
  const client = createMemorySourcePageClient(management)
  return {
    documents: () => client.read('snapshot'),
    document: id => client.read('document', { id }),
    searchDocuments: (query, includeArchived = false, limit = 50) => client.read('search', { query, includeArchived, limit }),
    mutateDocument: input => client.canAssist('mutate') ? client.assist('mutate', { ...input }, true) : client.mutate('mutate', { ...input }, true),
    archiveDocument: id => client.canAssist('archive') ? client.assist('archive', { id }, true) : client.mutate('archive', { id }, true),
  }
}

function DocumentsSourceView(props: MemorySourcePageProps): JSX.Element | null {
  const client = useMemo(() => props.management === undefined ? undefined : documentsPageClient(props.management), [props.management])
  const [revision, setRevision] = useState(0)
  // The workspace's refresh reloads this page as well.
  const refreshKey = useRef(props.refreshKey)
  useEffect(() => {
    if (refreshKey.current === props.refreshKey) return
    refreshKey.current = props.refreshKey
    setRevision(value => value + 1)
  }, [props.refreshKey])
  if (client === undefined) return null
  // A conversation turn opens the document it read or wrote; each visit starts from that document.
  const navigation = props.navigationInput
  const focus = typeof navigation === 'object' && navigation !== null && !Array.isArray(navigation) && typeof navigation.seed === 'string' && navigation.seed !== '' ? navigation : undefined
  return <DocumentsPage key={typeof focus?.nonce === 'number' ? focus.nonce : 0} {...(focus === undefined ? {} : { documentId: focus.seed as string })} canCreate client={client} revision={revision} writeEnabled={props.writable === true} onMutate={() => { setRevision(value => value + 1); props.onRefresh?.() }} />
}

export function DocumentsSourcePage(props: MemorySourcePageProps): ReactNode {
  return <MemorySourcePageFrame locale={props.locale}><DocumentsSourceView key={props.sourceInstanceKey} {...props} /></MemorySourcePageFrame>
}

export function installDocumentsMemoryUI(ctx: Parameters<typeof installMemorySourceUI>[0], t: MnemonTranslate = translateEn): () => void {
  return installMemorySourceUI(ctx, { sourceTypeId: 'documents', pages: [{ id: 'library', order: 200, navigation: { group: 'storage', glyph: '▤' }, label: () => t('nav.documents'), component: DocumentsSourcePage }] })
}

export const inject = ['slots', 'locale']
export function apply(ctx: Parameters<typeof installMemorySourceUI>[0]): void { installDocumentsMemoryUI(ctx, ctx.locale!.bind('mnemon')) }
