// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ClientConnectionHandle } from "../src/host/dsh.ts"
import type { TurnMemoryActivitySnapshot } from '../src/host/protocol.ts'
import type { Config } from "../src/host/config.ts"
import { MnemonSaveAction } from '../src/client/MnemonSaveAction.tsx'
import { MnemonTurnTail, memoryPageForTool, turnItems, turnTools } from '../src/client/MnemonTurnTail.tsx'
import { consumeMnemonAnchor, dispatchMnemonAnchor, subscribeMnemonAnchor } from '../src/client/anchor.ts'
import { settingsScope } from './helpers/settings-scope.ts'

// DSH's Modal and Tooltip measure their surfaces before showing them; jsdom
// has no layout engine, so report an empty box as soon as one is observed.
globalThis.ResizeObserver ??= class {
  constructor(private readonly callback: ResizeObserverCallback) {}
  observe(target: Element): void {
    this.callback([{ target, borderBoxSize: [{ inlineSize: 0, blockSize: 0 }] } as unknown as ResizeObserverEntry], this as unknown as ResizeObserver)
  }
  unobserve(): void {}
  disconnect(): void {}
} as unknown as typeof ResizeObserver

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const translate = (key: string): string => key
function createLocaleRuntime() {
  let snapshot = { active: 'zh' as 'zh' | 'en', locales: [], revision: 0 }
  const listeners = new Set<() => void>()
  return {
    getSnapshot: () => snapshot,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } },
    select: (active: 'zh' | 'en') => {
      snapshot = { ...snapshot, active, revision: snapshot.revision + 1 }
      listeners.forEach(listener => listener())
    },
  }
}
const localeRuntime = createLocaleRuntime()
const writableSettingsScope = settingsScope<Config>({ status: 'ready', value: {}, writable: true, mode: 'host' })
const readOnlySettingsScope = settingsScope<Config>({ status: 'unavailable', writable: false, mode: 'host' })

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((onResolve, onReject) => {
    resolve = onResolve
    reject = onReject
  })
  return { promise, resolve, reject }
}

function remoteRpc(domain: (endpoint: string, payload: Record<string, unknown>) => unknown | Promise<unknown>) {
  return vi.fn(async (channel: string, remoteEndpoint: string, rawArgs: unknown) => {
    if (channel !== '/api' || !remoteEndpoint.startsWith('dshMnemon/')) throw new Error(`unexpected remote endpoint: ${channel} ${remoteEndpoint}`)
    const args = (rawArgs as { args: { endpoint: string; payload: Record<string, unknown> } }).args
    return { ok: true as const, value: await domain(args.endpoint, args.payload) }
  })
}

function calledRemote(call: ReturnType<typeof remoteRpc>, endpoint: string): boolean {
  return call.mock.calls.some(([, , rawArgs]) => (rawArgs as { args?: { endpoint?: string } }).args?.endpoint === endpoint)
}

describe('conversation interaction surfaces', () => {
  it('consumes a delivered anchor instead of replaying it after remount', () => {
    const received: string[] = []
    const unsubscribe = subscribeMnemonAnchor('session-a', anchor => received.push(anchor.page))

    dispatchMnemonAnchor({ page: 'documents/library', sessionId: 'session-a' })

    expect(received).toEqual(['documents/library'])
    expect(consumeMnemonAnchor('session-a')).toBeNull()
    unsubscribe()
  })

  it('keeps an anchor pending when no matching view is mounted', () => {
    dispatchMnemonAnchor({ page: 'memory-spaces/explore', seed: 'sqlite', sessionId: 'session-b' })

    expect(consumeMnemonAnchor('session-b')).toEqual({ page: 'memory-spaces/explore', seed: 'sqlite', sessionId: 'session-b' })
    expect(consumeMnemonAnchor('session-b')).toBeNull()
  })

  it.each([
    null,
    undefined,
    {},
    { turn: 2 },
    { turn: 2, status: 'open' },
    { status: 'closed' },
    { turn: '2', status: 'closed' },
  ])('does not request or display activity for an open or invalid turn: %j', async turn => {
    const rpcCall = vi.fn(async () => ({ ok: true as const, value: { cursor: 12, activities: [] } }))
    render(<MnemonTurnTail turn={turn} seq={12} openFile={vi.fn()} sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} localeRuntime={localeRuntime} t={translate} />)

    await act(async () => {})
    expect(rpcCall).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: /turnTail\.label/ })).toBeNull()
  })

  it('loads activity when the same turn number and sequence close without final assistant text', async () => {
    const rpcCall = vi.fn(async () => ({ ok: true as const, value: {
      cursor: 12, activities: [{ turn: 2, count: 1, names: ['mnemon_runtime_memory'], recalls: 0, writes: 1, documentSearches: 0, inspections: 0, failures: 0 }],
    } }))
    const props = { seq: 12, openFile: vi.fn(), sessionId: 'session-a', connection: { rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle, localeRuntime, t: translate }
    const view = render(<MnemonTurnTail {...props} turn={{ turn: 2, status: 'open', closing: null }} />)
    await act(async () => {})
    expect(rpcCall).not.toHaveBeenCalled()

    view.rerender(<MnemonTurnTail {...props} turn={{ turn: 2, status: 'closed', closing: null }} />)

    expect(await screen.findByRole('button', { name: /turnTail\.label/ })).toBeTruthy()
    expect(rpcCall).toHaveBeenCalledTimes(1)
    expect(rpcCall).toHaveBeenCalledWith('/dsh-mnemon-read', 'turn-activities', { sessionId: 'session-a' })
  })

  it('hides a completed turn with no memory activity', async () => {
    const rpcCall = vi.fn(async () => ({ ok: true as const, value: { cursor: 12, activities: [] } }))
    render(<MnemonTurnTail turn={{ turn: 2, status: 'closed' }} seq={12} openFile={vi.fn()} sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} localeRuntime={localeRuntime} t={translate} />)

    await act(async () => {})
    expect(rpcCall).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('button', { name: /turnTail\.label/ })).toBeNull()
  })

  it('ignores an in-flight activity result after its turn becomes open', async () => {
    const request = deferred<{ ok: true; value: TurnMemoryActivitySnapshot }>()
    const rpcCall = vi.fn(() => request.promise)
    const props = { seq: 12, openFile: vi.fn(), sessionId: 'session-a', connection: { rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle, localeRuntime, t: translate }
    const view = render(<MnemonTurnTail {...props} turn={{ turn: 2, status: 'closed' }} />)
    expect(rpcCall).toHaveBeenCalledTimes(1)
    view.rerender(<MnemonTurnTail {...props} turn={{ turn: 2, status: 'open' }} />)

    await act(async () => request.resolve({ ok: true, value: {
      cursor: 12, activities: [{ turn: 2, count: 1, names: ['mnemon_runtime_memory'], recalls: 0, writes: 1, documentSearches: 0, inspections: 0, failures: 0, retrieved: [], writebacks: [] }],
    } }))

    expect(rpcCall).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('button', { name: /turnTail\.label/ })).toBeNull()
  })

  it('hides already loaded activity when its turn becomes open', async () => {
    const rpcCall = vi.fn(async () => ({ ok: true as const, value: {
      cursor: 12, activities: [{ turn: 2, count: 1, names: ['mnemon_runtime_memory'], recalls: 0, writes: 1, documentSearches: 0, inspections: 0, failures: 0 }],
    } }))
    const props = { seq: 12, openFile: vi.fn(), sessionId: 'session-a', connection: { rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle, localeRuntime, t: translate }
    const view = render(<MnemonTurnTail {...props} turn={{ turn: 2, status: 'closed' }} />)
    await screen.findByRole('button', { name: /turnTail\.label/ })

    view.rerender(<MnemonTurnTail {...props} turn={{ turn: 2, status: 'open' }} />)

    expect(rpcCall).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('button', { name: /turnTail\.label/ })).toBeNull()
  })

  it.each(['mnemon_document_search', 'mnemon_document_create'])('opens %s from turn activity on the Documents page', async toolName => {
    const rpcCall = vi.fn(async (_channel: string, endpoint: string) => {
      if (endpoint !== 'turn-activities') throw new Error(`unexpected endpoint: ${endpoint}`)
      return {
        ok: true as const,
        value: {
          cursor: 12,
          activities: [{ turn: 2, count: 2, names: [toolName, 'mnemon_runtime_memory'], recalls: 0, writes: 1, documentSearches: 1, inspections: 0, failures: 0 }],
        },
      }
    })
    const connection = { rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle
    const received: string[] = []
    const unsubscribe = subscribeMnemonAnchor('session-a', anchor => received.push(anchor.page))
    render(<MnemonTurnTail turn={{ turn: 2, status: 'closed' }} seq={12} openFile={vi.fn()} sessionId="session-a" connection={connection} localeRuntime={localeRuntime} t={translate as never} />)

    fireEvent.click(await screen.findByRole('button', { name: /turnTail\.label/ }))
    fireEvent.click(screen.getAllByRole('button', { name: 'turnTail.openTool' })[0]!)

    expect(received).toEqual(['documents/library'])
    expect(memoryPageForTool('mnemon_runtime_memory')).toBe('runtime/entries')
    unsubscribe()
  })

  it('names each memory tool once with how often the turn used it, and keeps the tool name on hover', async () => {
    const rpcCall = vi.fn(async () => ({ ok: true as const, value: {
      cursor: 12,
      activities: [{ turn: 2, count: 3, names: ['mnemon_document_search', 'mnemon_recall', 'mnemon_recall'], recalls: 2, writes: 0, documentSearches: 1, inspections: 0, failures: 0 }],
    } }))
    render(<MnemonTurnTail turn={{ turn: 2, status: 'closed' }} seq={12} openFile={vi.fn()} sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} localeRuntime={localeRuntime} t={translate as never} />)
    const bar = await screen.findByRole('button', { name: /turnTail\.label/ })
    expect(bar.querySelector('svg path')).not.toBeNull()
    fireEvent.click(bar)
    const chips = screen.getAllByRole('button', { name: 'turnTail.openTool' })
    expect(chips.map(chip => chip.textContent)).toEqual(['turnTail.tool.documentSearch', 'turnTail.tool.recall×2'])
    fireEvent.mouseEnter(chips[1]!)
    expect(screen.getByRole('tooltip').textContent).toBe('mnemon_recall')
    expect(turnTools(['a', 'b', 'a', 'c'])).toEqual([{ name: 'a', count: 2 }, { name: 'b', count: 1 }, { name: 'c', count: 1 }])
  })

  it('lists what each tool read or wrote and opens each item where it lives', async () => {
    const activity = {
      turn: 2, count: 3, names: ['mnemon_document_search', 'mnemon_recall', 'mnemon_runtime_memory', 'mnemon_forget'],
      recalls: 1, writes: 2, documentSearches: 1, inspections: 0, failures: 0,
      retrieved: [
        { callId: 'read-1', toolName: 'mnemon_document_search', operationId: 'search', sourceTypeId: 'documents',
          items: [{ id: 'doc-1', title: 'Decision: queue event ingestion', excerpt: 'Events go through Kafka first.' }] },
        { callId: 'read-2', toolName: 'mnemon_recall', operationId: 'recall', sourceTypeId: 'memory-spaces',
          items: [{ id: 'memory-1', title: 'Lumen', excerpt: 'Consumers dedupe by event_id.' }, { id: 'memory-2', title: 'Lumen', excerpt: 'Consumers dedupe by event_id.' }] },
      ],
      writebacks: [
        { callId: 'write-1', toolName: 'mnemon_runtime_memory', operationId: 'mutate', sourceTypeId: 'runtime', item: { id: 'mutate', title: 'Checkout p75 LCP is 2.4 s.' } },
        { callId: 'write-2', toolName: 'mnemon_forget', operationId: 'forget', sourceTypeId: 'memory-spaces', item: { id: 'memory-9', title: 'memory-9' } },
      ],
    }
    const rpcCall = vi.fn(async () => ({ ok: true as const, value: { cursor: 12, activities: [activity] } }))
    const received: Array<{ page: string; seed?: string }> = []
    const unsubscribe = subscribeMnemonAnchor('session-a', anchor => received.push({ page: anchor.page, ...(anchor.seed === undefined ? {} : { seed: anchor.seed }) }))
    render(<MnemonTurnTail turn={{ turn: 2, status: 'closed' }} seq={12} openFile={vi.fn()} sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} localeRuntime={localeRuntime} t={translate as never} />)
    fireEvent.click(await screen.findByRole('button', { name: /turnTail\.label/ }))

    const list = screen.getByRole('list', { name: 'turnTail.toolList' })
    expect([...list.querySelectorAll('li')].map(row => row.textContent)).toEqual([
      'turnTail.tool.documentSearchDecision: queue event ingestion',
      'turnTail.tool.recallConsumers dedupe by event_id.Lumen',
      'turnTail.tool.runtimeCheckout p75 LCP is 2.4 s.',
      'turnTail.tool.forget',
    ])
    fireEvent.click(screen.getByRole('button', { name: 'Decision: queue event ingestion' }))
    fireEvent.click(screen.getByRole('button', { name: 'Consumers dedupe by event_id. Lumen' }))
    fireEvent.click(screen.getByRole('button', { name: 'Checkout p75 LCP is 2.4 s.' }))
    expect(received).toEqual([
      { page: 'documents/library', seed: 'doc-1' },
      { page: 'memory-spaces/explore', seed: 'Consumers dedupe by event_id.' },
      { page: 'runtime/entries', seed: 'Checkout p75 LCP is 2.4 s.' },
    ])
    unsubscribe()
  })

  it('shows at most three items per tool and counts the rest', async () => {
    const items = Array.from({ length: 5 }, (_, index) => ({ id: `doc-${index}`, title: `Document ${index}` }))
    const retrieved = [{ callId: 'read', toolName: 'mnemon_document_search', operationId: 'search', sourceTypeId: 'documents', items }]
    expect(turnItems({ retrieved }).get('mnemon_document_search')).toHaveLength(5)
    expect(turnItems({})).toEqual(new Map())
    const rpcCall = vi.fn(async () => ({ ok: true as const, value: { cursor: 12, activities: [{
      turn: 2, count: 1, names: ['mnemon_document_search'], recalls: 0, writes: 0, documentSearches: 1, inspections: 0, failures: 0, retrieved, writebacks: [],
    }] } }))
    const t = (key: string, params?: Record<string, unknown>) => params?.count === undefined ? key : `${key}:${String(params.count)}`
    render(<MnemonTurnTail turn={{ turn: 2, status: 'closed' }} seq={12} openFile={vi.fn()} sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} localeRuntime={localeRuntime} t={t as never} />)
    fireEvent.click(await screen.findByRole('button', { name: /turnTail\.label/ }))
    const row = screen.getByRole('list', { name: 'turnTail.toolList' }).querySelector('li')!
    expect([...row.querySelectorAll('button')].map(button => button.textContent)).toEqual(['turnTail.tool.documentSearch', 'Document 0', 'Document 1', 'Document 2'])
    expect(row.textContent).toContain('turnTail.more:2')
  })

  it('shows the task Agent receipt, leads to what was written, and waits for an edit before sending again', async () => {
    const rpcCall = vi.fn(async (_channel: string, endpoint: string) => {
      if (endpoint === 'status') return { ok: true as const, value: { writeEnabled: true, lifecycle: { taskAgentAvailable: true } } }
      if (endpoint === 'assistant-message') return { ok: true as const, value: { messageId: 'message-1', text: 'Checkout loads the payment SDK after interaction.' } }
      if (endpoint === 'supervise') return { ok: true as const, value: { summary: 'Saved to Lumen project.', action: 'stored', memoryBodyIds: ['lumen'] } }
      throw new Error(`unexpected endpoint: ${endpoint}`)
    })
    const received: string[] = []
    const unsubscribe = subscribeMnemonAnchor('session-a', anchor => received.push(anchor.page))
    render(<MnemonSaveAction messageId="message-1" sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} settingsScope={writableSettingsScope} localeRuntime={localeRuntime} t={translate as never} />)
    const action = screen.getByRole('button', { name: 'saveAction.button' })
    expect(action.querySelector('svg path')).not.toBeNull()
    fireEvent.click(action)
    await screen.findByText('taskAgent.ready')
    const submit = screen.getByRole('button', { name: 'saveAction.submit' }) as HTMLButtonElement
    await waitFor(() => expect(submit.disabled).toBe(false))
    fireEvent.click(submit)

    await screen.findByText('receipt.written')
    expect(screen.getByText('Saved to Lumen project.')).toBeTruthy()
    const footerClose = screen.getAllByRole('button', { name: 'saveAction.close' })
    expect(footerClose.length).toBeGreaterThan(1)
    expect((screen.getByRole('button', { name: 'saveAction.submit' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(screen.getByRole('textbox', { name: 'saveAction.candidate' }), { target: { value: 'Checkout loads the payment SDK after interaction; p75 LCP is 2.4 s.' } })
    expect((screen.getByRole('button', { name: 'saveAction.submit' }) as HTMLButtonElement).disabled).toBe(false)

    fireEvent.click(screen.getByRole('button', { name: 'receipt.view' }))
    expect(received).toEqual(['memory-spaces/content'])
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'saveAction.title' })).toBeNull())
    expect(rpcCall.mock.calls.filter(call => call[1] === 'supervise')).toHaveLength(1)
    unsubscribe()
  })

  it('keeps sending closed while no task Agent can take the write', async () => {
    const rpcCall = vi.fn(async (_channel: string, endpoint: string) => {
      if (endpoint === 'status') return { ok: true as const, value: { writeEnabled: true, lifecycle: { taskAgentAvailable: false } } }
      if (endpoint === 'assistant-message') return { ok: true as const, value: { messageId: 'message-1', text: 'A durable project decision.' } }
      throw new Error(`unexpected endpoint: ${endpoint}`)
    })
    render(<MnemonSaveAction messageId="message-1" sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} settingsScope={writableSettingsScope} localeRuntime={localeRuntime} t={translate as never} />)
    fireEvent.click(screen.getByRole('button', { name: 'saveAction.button' }))
    await screen.findByText('taskAgent.unavailable')
    expect((screen.getByRole('button', { name: 'saveAction.submit' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('opens a centered modal and prevents a second supervised write while it is closed', async () => {
    const status = deferred<{ ok: true; value: { writeEnabled: boolean } }>()
    const supervision = deferred<{ ok: true; value: { summary: string; action: string } }>()
    let statusCalls = 0
    const rpcCall = vi.fn(async (_channel: string, endpoint: string) => {
      if (endpoint === 'status') {
        statusCalls += 1
        return statusCalls === 1 ? status.promise : { ok: true as const, value: { writeEnabled: true } }
      }
      if (endpoint === 'assistant-message') return { ok: true as const, value: { messageId: 'message-1', text: 'A durable project decision.' } }
      if (endpoint === 'supervise') return supervision.promise
      throw new Error(`unexpected endpoint: ${endpoint}`)
    })
    const connection = { rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle

    render(<MnemonSaveAction messageId="message-1" sessionId="session-a" connection={connection} settingsScope={writableSettingsScope} localeRuntime={localeRuntime} t={translate as never} />)
    const action = screen.getByRole('button', { name: 'saveAction.button' })
    expect(action.textContent).toBe('')
    expect(action.getAttribute('aria-haspopup')).toBe('dialog')
    expect(action.getAttribute('title')).toBeNull()
    fireEvent.mouseEnter(action)
    expect(screen.getByRole('tooltip').textContent).toBe('saveAction.tooltip')
    fireEvent.click(action)
    const dialog = screen.getByRole('dialog', { name: 'saveAction.title' })
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(action.parentElement?.contains(dialog)).toBe(false)
    expect(screen.getByRole('button', { name: 'common.cancel' })).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('tooltip')).toBeNull())
    expect(rpcCall.mock.calls.filter(call => call[1] === 'supervise')).toHaveLength(0)

    const submit = await screen.findByRole('button', { name: 'saveAction.submit' }) as HTMLButtonElement
    expect(submit.disabled).toBe(true)
    status.resolve({ ok: true, value: { writeEnabled: true } })
    await waitFor(() => expect(submit.disabled).toBe(false))
    fireEvent.click(submit)
    expect(rpcCall.mock.calls.filter(call => call[1] === 'supervise')).toHaveLength(1)
    expect(rpcCall).toHaveBeenCalledWith(expect.anything(), 'supervise', {
      sessionId: 'session-a',
      content: 'A durable project decision.',
      idempotencyKey: 'message-1',
    })

    fireEvent.click(screen.getByRole('button', { name: 'saveAction.close' }))
    fireEvent.click(screen.getByRole('button', { name: 'saveAction.button' }))
    const reopenedSubmit = await screen.findByRole('button', { name: 'saveAction.submitting' }) as HTMLButtonElement
    expect(reopenedSubmit.disabled).toBe(true)
    fireEvent.click(reopenedSubmit)
    expect(rpcCall.mock.calls.filter(call => call[1] === 'supervise')).toHaveLength(1)

    supervision.resolve({ ok: true, value: { summary: 'stored', action: 'remember' } })
    await waitFor(() => expect((screen.getByRole('button', { name: 'saveAction.submit' }) as HTMLButtonElement).disabled).toBe(false))
    expect(screen.queryByText('saveAction.result')).toBeNull()
  })

  it('keeps supervised message writes read-only when Host settings are not writable', async () => {
    const rpcCall = remoteRpc(async (endpoint: string) => {
      if (endpoint === 'status') return { ok: true as const, value: { writeEnabled: true } }
      if (endpoint === 'assistant-message') return { ok: true as const, value: { messageId: 'message-1', text: 'A durable project decision.' } }
      throw new Error(`unexpected endpoint: ${endpoint}`)
    })
    const connection = { rpc: { call: rpcCall }, isLoopback: false } as ClientConnectionHandle

    render(<MnemonSaveAction messageId="message-1" sessionId="session-a" connection={connection} settingsScope={readOnlySettingsScope} localeRuntime={localeRuntime} t={translate as never} />)
    fireEvent.click(screen.getByRole('button', { name: 'saveAction.button' }))

    const submit = await screen.findByRole('button', { name: 'saveAction.submit' }) as HTMLButtonElement
    await waitFor(() => expect(screen.getByRole('status').textContent).toBe('saveAction.readOnly'))
    expect(submit.disabled).toBe(true)
    fireEvent.click(submit)
    expect(calledRemote(rpcCall, 'supervise')).toBe(false)
  })

  it('allows supervised message writes when authenticated Host settings are writable', async () => {
    const rpcCall = remoteRpc(async (endpoint: string) => {
      if (endpoint === 'status') return { ok: true as const, value: { writeEnabled: true } }
      if (endpoint === 'assistant-message') return { ok: true as const, value: { messageId: 'message-1', text: 'A durable project decision.' } }
      if (endpoint === 'supervise') return { ok: true as const, value: { summary: 'stored', action: 'remember' } }
      throw new Error(`unexpected endpoint: ${endpoint}`)
    })
    const connection = { rpc: { call: rpcCall }, isLoopback: false } as ClientConnectionHandle

    render(<MnemonSaveAction messageId="message-1" sessionId="session-a" connection={connection} settingsScope={writableSettingsScope} localeRuntime={localeRuntime} t={translate as never} />)
    fireEvent.click(screen.getByRole('button', { name: 'saveAction.button' }))

    const submit = await screen.findByRole('button', { name: 'saveAction.submit' }) as HTMLButtonElement
    await waitFor(() => expect(submit.disabled).toBe(false))
    fireEvent.click(submit)
    await waitFor(() => expect(calledRemote(rpcCall, 'supervise')).toBe(true))
  })

  it('updates the save action on locale changes without remounting an edited candidate', async () => {
    const locale = createLocaleRuntime()
    const t = (key: string) => `${locale.getSnapshot().active}:${key}`
    const rpcCall = vi.fn(async (_channel: string, endpoint: string) => {
      if (endpoint === 'status') return { ok: true as const, value: { writeEnabled: true } }
      if (endpoint === 'assistant-message') return { ok: true as const, value: { messageId: 'message-1', text: 'Original candidate.' } }
      throw new Error(`unexpected endpoint: ${endpoint}`)
    })
    render(<MnemonSaveAction messageId="message-1" sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} settingsScope={writableSettingsScope} localeRuntime={locale} t={t} />)
    expect(screen.getByRole('button', { name: 'zh:saveAction.button' })).toBeTruthy()
    act(() => locale.select('en'))
    fireEvent.click(screen.getByRole('button', { name: 'en:saveAction.button' }))
    const candidate = await screen.findByRole('textbox', { name: 'en:saveAction.candidate' })
    fireEvent.change(candidate, { target: { value: 'Edited candidate.' } })
    const calls = rpcCall.mock.calls.length
    act(() => locale.select('zh'))
    expect(screen.getByRole('dialog', { name: 'zh:saveAction.title' })).toBeTruthy()
    expect((screen.getByRole('textbox', { name: 'zh:saveAction.candidate' }) as HTMLTextAreaElement).value).toBe('Edited candidate.')
    expect(rpcCall).toHaveBeenCalledTimes(calls)
  })

  it('updates an expanded turn activity bar on locale changes without refetching', async () => {
    const locale = createLocaleRuntime()
    const t = (key: string) => `${locale.getSnapshot().active}:${key}`
    const rpcCall = vi.fn(async () => ({ ok: true as const, value: {
      cursor: 12, activities: [{ turn: 2, count: 1, names: ['mnemon_runtime_memory'], recalls: 0, writes: 1, documentSearches: 0, inspections: 0, failures: 0 }],
    } }))
    render(<MnemonTurnTail turn={{ turn: 2, status: 'closed' }} seq={12} openFile={vi.fn()} sessionId="session-a" connection={{ rpc: { call: rpcCall }, isLoopback: true } as ClientConnectionHandle} localeRuntime={locale} t={t} />)
    fireEvent.click(await screen.findByRole('button', { name: /zh:turnTail\.label/ }))
    act(() => locale.select('en'))
    expect(screen.getByRole('button', { name: /en:turnTail\.label/ }).getAttribute('aria-expanded')).toBe('true')
    expect(screen.getByRole('button', { name: 'en:turnTail.openTool' })).toBeTruthy()
    expect(rpcCall).toHaveBeenCalledTimes(1)
  })
})
