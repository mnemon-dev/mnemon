import { createContext, useContext, type JSX } from 'react'
import type { StatusView } from '../host/protocol.ts'
import { installMemoryComponentUI, type MemoryComponentUIContext } from './component-ui.tsx'
import { MEMORY_SPACES_PACKAGE, RUNTIME_PACKAGE } from './component-settings.tsx'
import { humanBytes, useT } from './page-kit.tsx'

export const DOCUMENTS_PACKAGE = 'dsh-mnemon-source-documents'

/**
 * The status the Memory System read for the conversation and workspace it
 * shows. Only dsh-mnemon's own cards read it; a card an installed component
 * contributes reads its own Source.
 */
export const WorkbenchStatusContext = createContext<StatusView | null>(null)

/**
 * Register the Status cards of the shipped Sources, the way an installed
 * component registers its own: each says what its Source holds.
 */
export function installShippedComponentStatus(ctx: MemoryComponentUIContext): () => void {
  const disposers = [
    installMemoryComponentUI(ctx, { packageName: RUNTIME_PACKAGE, status: () => <RuntimeStatus /> }),
    installMemoryComponentUI(ctx, { packageName: DOCUMENTS_PACKAGE, status: () => <DocumentsStatus /> }),
    installMemoryComponentUI(ctx, { packageName: MEMORY_SPACES_PACKAGE, status: () => <SpacesStatus /> }),
  ]
  return () => { for (const dispose of disposers.reverse()) dispose() }
}

/** A card's headline and one line under it. */
function Figure(props: { headline: string; detail: string }): JSX.Element {
  return <><strong>{props.headline}</strong><p>{props.detail}</p></>
}

function RuntimeStatus(): JSX.Element {
  const t = useT()
  const status = useContext(WorkbenchStatusContext)
  const storage = status?.storage
  const area = storage?.scopes.find(scope => scope.kind === (storage.activeKind ?? 'global'))?.areas.find(candidate => candidate.kind === 'runtime')
  if (area === undefined) return <Figure headline={t('status.runtimeWaiting')} detail={t('status.runtimeWaitingDetail')} />
  return <Figure headline={t('status.runtimeRatio', { user: Number(area.details.userEntries ?? 0), memory: Number(area.details.memoryEntries ?? 0) })}
    detail={t('status.runtimeBytes', { bytes: humanBytes(area.bytes) })} />
}

function DocumentsStatus(): JSX.Element {
  const t = useT()
  const documents = useContext(WorkbenchStatusContext)?.documents
  if (documents === undefined) return <Figure headline={t('status.documentsWaiting')} detail={t('status.documentsSession')} />
  return <Figure headline={t('status.documentRatio', { active: documents.activeCount, archived: documents.archivedCount })}
    detail={t('status.documentUsage', { used: humanBytes(documents.activeBytes), limit: humanBytes(documents.limitBytes) })} />
}

function SpacesStatus(): JSX.Element {
  const t = useT()
  const status = useContext(WorkbenchStatusContext)
  const spaces = status?.memoryBodies
  if (spaces === undefined) return <Figure headline={t('status.directoryUnsynced')} detail={t('status.activeMemories', { count: status?.stats?.totalInsights ?? 0 })} />
  return <Figure headline={t('status.activeRatio', { active: spaces.filter(space => space.active).length, total: spaces.length })}
    detail={t('status.activeMemories', { count: status?.stats?.totalInsights ?? 0 })} />
}
