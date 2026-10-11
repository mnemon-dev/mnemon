import { describe, expect, it, vi } from 'vitest'
import type { HostAgent, HostPreStepDecision, HostSubagentRun, HostUserMessage } from '../src/host/dsh.ts'
import { CONTINUATION_TEXT, startWithContinuationTurns } from '../src/host/continuation-turn.ts'

type PreStep = (payload: { messages: HostUserMessage[]; step: number; signal: AbortSignal }, next: () => Promise<HostPreStepDecision>) => Promise<HostPreStepDecision>
type RequestError = (payload: { turn: number; step: number; failure: { message: string; code: string }; signal: AbortSignal }, next: () => Promise<unknown>) => Promise<unknown>

function host() {
  const listeners = new Map<string, Set<(...args: unknown[]) => unknown>>()
  const owners = new Map<string, HostAgent>()
  const value = {
    agents: { isOwnedBy: (id: string, parent: HostAgent) => owners.get(id) === parent },
    on: vi.fn((name: string, listener: (...args: unknown[]) => unknown) => {
      const set = listeners.get(name) ?? new Set()
      set.add(listener)
      listeners.set(name, set)
      return () => { set.delete(listener) }
    }),
  }
  const created = (agent: HostAgent, parent: HostAgent) => {
    owners.set(agent.id, parent)
    for (const listener of listeners.get('agent/created') ?? []) listener({ agent })
  }
  return { value, created, observers: () => listeners.get('agent/created')?.size ?? 0 }
}

function agent(id: string) {
  const handlers = new Map<string, { handler: unknown; options: unknown }>()
  const stops: Array<ReturnType<typeof vi.fn>> = []
  const append = vi.fn()
  const value = { id, session: { append }, ctx: { on: vi.fn((name: string, handler: unknown, options?: unknown) => {
    handlers.set(name, { handler, options })
    const stop = vi.fn()
    stops.push(stop)
    return stop
  }) } } as unknown as HostAgent
  return {
    value, handlers, stops, append,
    preStep: () => handlers.get('agent/pre-step')!.handler as PreStep,
    requestError: () => handlers.get('agent/request-error')!.handler as RequestError,
  }
}

function run(id: string, child?: HostAgent): HostSubagentRun & { disposed: ReturnType<typeof vi.fn> } {
  const disposed = vi.fn(async () => {})
  return { id, ...(child === undefined ? {} : { localAgent: child }), result: Promise.resolve({ output: [], stopReason: 'completed' }), dispose: disposed, disposed }
}

const user = (text: string, kind = 'user'): HostUserMessage => ({ id: 'message-' + text, role: 'user', content: [{ type: 'text', text }], source: { kind } })
const live = new AbortController().signal
const refusal = { message: 'SERVER: no user query found in messages', code: 'SERVER_ERROR' }

async function started() {
  const h = host()
  const parent = agent('parent').value
  const child = agent('child')
  const value = await startWithContinuationTurns(h.value, parent, async () => {
    h.created(child.value, parent)
    return run('child', child.value)
  })
  return { h, parent, child, run: value }
}

describe('continuation turns for delegated children', () => {
  it('changes nothing until a server refuses a request without a user query', async () => {
    const { child, run: active } = await started()
    expect(active.localAgent).toBe(child.value)
    // Both handlers run first, so they see every other plugin's decision.
    expect([...child.handlers.values()].map(entry => entry.options)).toEqual([{ prepend: true }, { prepend: true }])
    await expect(child.preStep()({ messages: [], step: 2, signal: live }, async () => ({ kind: 'enter', messages: [] }))).resolves.toEqual({ kind: 'enter', messages: [] })
    const next = vi.fn(async () => ({ kind: 'retry' }))
    // Other failures, a first step, and an aborted step go to the next handler.
    await child.requestError()({ turn: 1, step: 2, failure: { message: '429 Too Many Requests', code: 'RATE_LIMITED' }, signal: live }, next)
    await child.requestError()({ turn: 1, step: 1, failure: refusal, signal: live }, next)
    await child.requestError()({ turn: 1, step: 2, failure: refusal, signal: AbortSignal.abort() }, next)
    expect(next).toHaveBeenCalledTimes(3)
    expect(child.append).not.toHaveBeenCalled()
    await expect(child.preStep()({ messages: [], step: 3, signal: live }, async () => ({ kind: 'enter', messages: [] }))).resolves.toEqual({ kind: 'enter', messages: [] })
  })

  it('ends a refused step with a user turn, retries it once, then ends each later continuation the same way', async () => {
    const { child } = await started()
    const next = vi.fn(async () => undefined)
    await expect(child.requestError()({ turn: 1, step: 4, failure: refusal, signal: live }, next)).resolves.toEqual({ kind: 'retry' })
    expect(next).not.toHaveBeenCalled()
    expect(child.append).toHaveBeenCalledExactlyOnceWith('user/message', expect.objectContaining({
      role: 'user', content: [{ type: 'text', text: CONTINUATION_TEXT }], source: { kind: 'dsh-mnemon', form: 'instructions' },
    }), { surfaceOp: 'append' })
    // A second refusal of the same step is someone else's to handle.
    await child.requestError()({ turn: 1, step: 4, failure: refusal, signal: live }, next)
    expect(next).toHaveBeenCalledOnce()
    expect(child.append).toHaveBeenCalledOnce()
    const step = child.preStep()
    const continued = await step({ messages: [], step: 5, signal: live }, async () => ({ kind: 'enter', messages: [], startsRequestSeries: true } as HostPreStepDecision))
    expect(continued).toMatchObject({ kind: 'enter', startsRequestSeries: true, messages: [{ role: 'user', content: [{ type: 'text', text: CONTINUATION_TEXT }] }] })
    // A step that already brings user text, such as a steer, gets nothing more.
    const steer = user('Also check the archive.')
    await expect(step({ messages: [steer], step: 6, signal: live }, async () => ({ kind: 'enter', messages: [steer] }))).resolves.toEqual({ kind: 'enter', messages: [steer] })
    // Blank text is no user query.
    const blank = user('  ', 'runtime-context')
    const padded = await step({ messages: [blank], step: 6, signal: live }, async () => ({ kind: 'enter', messages: [blank] }))
    expect(padded.kind === 'enter' && padded.messages.map(message => message.content)).toEqual([[{ type: 'text', text: '  ' }], [{ type: 'text', text: CONTINUATION_TEXT }]])
    // A handler that emptied the claimed messages ends the turn, and that stays so.
    await expect(step({ messages: [steer], step: 7, signal: live }, async () => ({ kind: 'enter', messages: [] }))).resolves.toEqual({ kind: 'enter', messages: [] })
    await expect(step({ messages: [], step: 1, signal: live }, async () => ({ kind: 'enter', messages: [] }))).resolves.toEqual({ kind: 'enter', messages: [] })
    await expect(step({ messages: [], step: 5, signal: live }, async () => ({ kind: 'reject' }))).resolves.toEqual({ kind: 'reject' })
    await expect(step({ messages: [], step: 5, signal: AbortSignal.abort() }, async () => ({ kind: 'enter', messages: [] }))).resolves.toEqual({ kind: 'enter', messages: [] })
  })

  it('leaves a refused step to the next handler where the session cannot take a message', async () => {
    const h = host()
    const parent = agent('parent').value
    const child = agent('child')
    Object.assign(child.value, { session: {} })
    await startWithContinuationTurns(h.value, parent, async () => { h.created(child.value, parent); return run('child', child.value) })
    const next = vi.fn(async () => undefined)
    await child.requestError()({ turn: 1, step: 4, failure: refusal, signal: live }, next)
    expect(next).toHaveBeenCalledOnce()
  })

  it('attaches only to the child its own start creates under the parent', async () => {
    const h = host()
    const parent = agent('parent').value
    const child = agent('child')
    const other = agent('other')
    const late = agent('late')
    const active = await startWithContinuationTurns(h.value, parent, async () => {
      // Another parent's child published during this start is not ours.
      h.created(other.value, agent('someone-else').value)
      h.created(child.value, parent)
      return run('child', child.value)
    })
    expect(child.handlers.size).toBe(2)
    expect(other.handlers.size).toBe(0)
    // Observation ends with the start.
    expect(h.observers()).toBe(0)
    h.created(late.value, parent)
    expect(late.handlers.size).toBe(0)
    await active.dispose()
    expect(child.stops.map(stop => stop.mock.calls.length)).toEqual([1, 1])
  })

  it('separates overlapping starts by their async context', async () => {
    const h = host()
    const parent = agent('parent').value
    const first = agent('first')
    const second = agent('second')
    let publishSecond!: () => void
    const secondPublished = new Promise<void>(resolve => { publishSecond = resolve })
    const one = startWithContinuationTurns(h.value, parent, async () => {
      await secondPublished
      h.created(first.value, parent)
      return run('first', first.value)
    })
    const two = startWithContinuationTurns(h.value, parent, async () => {
      h.created(second.value, parent)
      publishSecond()
      return run('second', second.value)
    })
    await Promise.all([one, two])
    expect(first.handlers.size).toBe(2)
    expect(second.handlers.size).toBe(2)
  })

  it('releases what it attached when the start fails', async () => {
    const h = host()
    const parent = agent('parent').value
    const child = agent('child')
    await expect(startWithContinuationTurns(h.value, parent, async () => {
      h.created(child.value, parent)
      throw new Error('provider rolled back the child')
    })).rejects.toThrow('provider rolled back the child')
    expect(child.stops.map(stop => stop.mock.calls.length)).toEqual([1, 1])
    expect(h.observers()).toBe(0)
  })

  it('leaves a child without these hooks, or a host without creation events, as it was', async () => {
    const h = host()
    const parent = agent('parent').value
    const bare = { id: 'bare', session: {}, ctx: {} } as unknown as HostAgent
    const active = await startWithContinuationTurns(h.value, parent, async () => {
      h.created(bare, parent)
      return run('bare', bare)
    })
    expect(active.id).toBe('bare')
    await active.dispose()
    const plain = run('plain')
    const result = await startWithContinuationTurns({ on: () => undefined }, parent, async () => plain)
    expect(result.id).toBe('plain')
    await result.dispose()
    expect(plain.disposed).toHaveBeenCalledOnce()
  })
})
