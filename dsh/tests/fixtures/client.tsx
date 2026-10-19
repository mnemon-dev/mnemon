import { SlotCore } from '@deepseek-ai/dsh-client-ui-slots'
import { createElement, useEffect, useMemo, type ComponentProps, type ComponentType } from 'react'
import { installRuntimeMemoryUI } from 'dsh-mnemon-source-runtime/client'
import { installDocumentsMemoryUI } from 'dsh-mnemon-source-documents/client'
import { installMemorySpacesUI } from 'dsh-mnemon-source-memory-spaces/client'
import { MnemonWorkbench } from '../../src/client/MnemonWorkbench.tsx'
import { translateZh, type MnemonTranslate } from '../../src/client/locales.ts'
import { createMemorySourcePageDirectory, MNEMON_SOURCE_PAGE_SLOT, type MemorySourcePageProps } from '../../src/client/source-pages.tsx'
import type { MemorySourceManagementCatalog } from '../../src/host/protocol.ts'

/** Test assembly uses the same DSH Slot ledger and independently exported Clients. */
function sourcePages(t: MnemonTranslate) {
  const slots = new SlotCore()
  const releases = [slots.register({ name: 'root', children: { [MNEMON_SOURCE_PAGE_SLOT]: { kind: 'list', scope: 'root' } } } as never, (() => null) as never)]
  const ctx = { slots: {
    inject: (_name: string, setup: () => () => void) => setup(),
    register: slots.register.bind(slots),
    getVersion: slots.getVersion.bind(slots),
    entriesOfSlot: slots.entriesOfSlot.bind(slots),
    subscribe: slots.subscribe.bind(slots),
  }, locale: { getSnapshot: () => 'zh', subscribe: () => () => {} } }
  for (const install of [installRuntimeMemoryUI, installDocumentsMemoryUI, installMemorySpacesUI]) releases.push(install(ctx as never, t))
  return {
    sourcePageDirectory: createMemorySourcePageDirectory(ctx as never),
    renderSlot: ((_name: string, props: MemorySourcePageProps, options: { only?: string }) => {
      const entry = slots.entriesOfSlot(MNEMON_SOURCE_PAGE_SLOT).find(entry => entry.options.id === options.only)
      return entry === undefined ? null : <div data-slot={MNEMON_SOURCE_PAGE_SLOT} style={{ display: 'contents' }}>{createElement(entry.component as ComponentType<MemorySourcePageProps>, props)}</div>
    }) as NonNullable<ComponentProps<typeof MnemonWorkbench>['renderSlot']>,
    dispose: () => { for (const release of releases.reverse()) release() },
  }
}

/** `runningSources` limits the Source catalog to the Sources whose components run. */
export function ComposedMnemonWorkbench({ runningSources, ...props }: ComponentProps<typeof MnemonWorkbench> & { runningSources?: readonly string[] }) {
  const pages = useMemo(() => sourcePages(props.t ?? translateZh), [props.t])
  useEffect(() => () => pages.dispose(), [pages])
  const running = runningSources?.join('\u0000')
  const connection = useMemo(() => {
    const catalog = running === undefined ? sourceCatalog : { ...sourceCatalog, sources: sourceCatalog.sources.filter(source => running.split('\u0000').includes(source.sourceTypeId)) }
    return { ...props.connection, rpc: { ...props.connection.rpc, call: sourceTransport(props.connection.rpc.call.bind(props.connection.rpc), catalog) } }
  }, [props.connection, running])
  return <MnemonWorkbench {...pages} {...props} connection={connection as typeof props.connection} />
}

export const sourceCatalog: MemorySourceManagementCatalog = {
  generationId: 'test-generation',
  sources: [
    { sourceTypeId: 'runtime', role: 'working-context', label: '运行时记忆', assistance: ['mutate'] },
    { sourceTypeId: 'documents', role: 'narrative', label: '项目档案', assistance: ['mutate', 'archive'] },
    { sourceTypeId: 'memory-spaces', role: 'durable-evidence', label: '记忆空间', assistance: ['activation', 'agent-search', 'supervise', 'body-metadata-maintain', 'body-create'] },
  ].map(({ sourceTypeId, role, label, assistance }) => ({
    sourceInstanceKey: 'source:mnemon-source-' + sourceTypeId,
    sourceTypeId, role, assistance, packageName: 'dsh-mnemon-source-' + sourceTypeId,
    availability: 'ready', revision: 'r1', capabilities: ['read', 'write'], management: { label, description: '' },
  })),
}

/** Domain fixtures remain independent of the Source transport envelope. */
export function sourceTransport(domain: (channel: string, endpoint: string, payload?: Record<string, unknown>) => Promise<any>, catalog: MemorySourceManagementCatalog = sourceCatalog) {
  return async (channel: string, transportEndpoint: string, transportPayload?: Record<string, unknown>) => {
    const gateway = channel === '/api' && transportEndpoint.startsWith('dshMnemon/')
    const gatewayArgs = gateway ? transportPayload?.args as Record<string, unknown> | undefined : undefined
    const endpoint = gateway ? String(gatewayArgs?.endpoint) : transportEndpoint
    const payload = gateway ? gatewayArgs?.payload as Record<string, unknown> | undefined : transportPayload
    const wrap = (response: unknown): unknown => gateway ? { ok: true, value: response } : response
    if (endpoint === 'source-management-catalog') return wrap({ ok: true, value: catalog })
    if (!['source-management-read', 'source-management-mutate', 'source-assistance'].includes(endpoint)) return wrap(await domain(channel, endpoint, payload))
    const key = String(payload?.sourceInstanceKey)
    if (!catalog.sources.some(source => source.sourceInstanceKey === key)) throw new Error('Unregistered test Source: ' + key)
    const type = key.slice('source:mnemon-source-'.length)
    const operation = String(payload?.operation)
    const input: Record<string, unknown> = { ...(payload?.input as Record<string, unknown> ?? {}),
      ...(payload?.sessionId === undefined ? {} : { sessionId: payload.sessionId }),
      ...(payload?.workspaceId === undefined ? {} : { workspaceId: payload.workspaceId }),
    }
    let logical = operation
    if (type === 'runtime') logical = 'runtime-memory'
    if (type === 'documents') {
      logical = operation === 'snapshot' ? 'documents' : operation === 'search' ? 'document-search' : 'document'
      if (operation === 'archive') Object.assign(input, { action: 'archive' })
    }
    if (operation === 'activation') logical = 'body'
    if (input.oldText !== undefined) { input.old_text = input.oldText; delete input.oldText }
    const response = await domain(channel, logical, input)
    return wrap(!response.ok ? response : { ok: true, value: { revision: 'r1', value: response.value } })
  }
}
