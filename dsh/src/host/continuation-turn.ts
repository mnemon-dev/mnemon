import { AsyncLocalStorage } from 'node:async_hooks'
import type { HostAgent, HostPreStepDecision, HostSubagentRun, HostUserMessage } from './dsh.ts'
import { createPluginMessage } from './plugin-message.ts'

/** The user turn that ends a refused delegated child's tool continuations. */
export const CONTINUATION_TEXT = 'Continue from the tool results above.'

/** How a server reports a chat template that found no user query (Ollama, vLLM and others). */
const NO_USER_QUERY = /no user query found in messages/iu

export interface ContinuationHost {
  agents?: { isOwnedBy?(id: string, parent: HostAgent): boolean }
  on(name: string, listener: (...args: never[]) => unknown): unknown
}

interface PreStepPayload {
  messages: HostUserMessage[]
  step: number
  signal: AbortSignal
}

interface RequestErrorPayload {
  turn: number
  step: number
  failure?: { message?: string }
  signal: AbortSignal
}

// Distinguishes overlapping starts, as review-tools.ts does for its guard.
const startingChild = new AsyncLocalStorage<symbol>()

function hasUserText(message: HostUserMessage): boolean {
  return message.role === 'user' && message.content.some(block => block.type === 'text' && typeof block.text === 'string' && block.text.trim() !== '')
}

/**
 * Some chat templates refuse a request without a user query: Ollama 0.33
 * serving Qwen3.x answers 500 "no user query found in messages" (#327). A
 * delegated child's only user turn is its prompt. Once tool results fill the
 * model's context window, Ollama truncates from the front and drops that
 * prompt while keeping the tool messages after it.
 *
 * Nothing changes until a server refuses a request that way. The refused step
 * then ends with a short user turn and runs again at once (the server ran no
 * inference), and each later tool continuation of the same child ends the
 * same way, since the server always keeps the last message. A route that never
 * refuses keeps its requests as they are, so a template that shows reasoning
 * only after the last user query keeps the earlier steps' reasoning.
 */
function continueAfterRefusal(agent: HostAgent): Array<() => unknown> {
  if (typeof agent.ctx?.on !== 'function') return []
  let refused = false
  const retried = new Set<string>()
  const turn = () => createPluginMessage(CONTINUATION_TEXT, 'instructions')
  return [
    agent.ctx.on('agent/request-error', (async (payload: RequestErrorPayload, next: () => Promise<unknown>) => {
      const step = `${payload.turn}:${payload.step}`
      const session = agent.session
      if (payload.signal.aborted || payload.step < 2 || retried.has(step) || typeof session.append !== 'function'
        || !NO_USER_QUERY.test(payload.failure?.message ?? '')) return next()
      retried.add(step)
      refused = true
      // DSH's own context-overflow recovery also changes the session here, then retries the step.
      session.append('user/message', turn(), { surfaceOp: 'append' })
      return { kind: 'retry' }
    }) as never, { prepend: true }),
    agent.ctx.on('agent/pre-step', (async (payload: PreStepPayload, next: () => Promise<HostPreStepDecision>) => {
      const decision = await next()
      if (!refused || payload.step === 1 || payload.signal.aborted || decision.kind !== 'enter') return decision
      // A handler that emptied the messages this step claimed ends the turn; keep that.
      if (payload.messages.length > 0 && decision.messages.length === 0) return decision
      if (decision.messages.some(hasUserText)) return decision
      return { ...decision, messages: [...decision.messages, turn()] }
    }) as never, { prepend: true }),
  ]
}

/**
 * Start one delegated child and attach the refusal recovery while DSH
 * publishes it, before its first step. Async context attributes concurrent
 * starts, and DSH's ownership check confirms the parent where it exists. A host
 * that does not report creation leaves the child as it was.
 */
export async function startWithContinuationTurns(host: ContinuationHost, parent: HostAgent, start: () => Promise<HostSubagentRun>): Promise<HostSubagentRun> {
  const pending = Symbol('Mnemon delegated start')
  const stops: Array<() => unknown> = []
  const listener = host.on('agent/created', (({ agent }: { agent: HostAgent }) => {
    if (startingChild.getStore() !== pending) return
    if (typeof host.agents?.isOwnedBy === 'function' && !host.agents.isOwnedBy(agent.id, parent)) return
    for (const stop of continueAfterRefusal(agent)) if (typeof stop === 'function') stops.push(stop)
  }) as never)
  const release = async () => { for (const stop of stops.splice(0)) await stop() }
  let run: HostSubagentRun
  try {
    run = await startingChild.run(pending, start)
  } catch (error) {
    await release()
    throw error
  } finally {
    if (typeof listener === 'function') await listener()
  }
  const active = run
  return { id: active.id, ...(active.localAgent === undefined ? {} : { localAgent: active.localAgent }), result: active.result, async dispose() {
    try { await active.dispose() } finally { await release() }
  } }
}
