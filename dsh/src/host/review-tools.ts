import { AsyncLocalStorage } from 'node:async_hooks'
import type { HostAgent, HostSubagentRun, ToolExecution } from './dsh.ts'

export interface ReviewToolHost {
  agents?: { isOwnedBy?(id: string, parent: HostAgent): boolean }
  on(name: string, listener: (...args: never[]) => unknown): unknown
}

// One store distinguishes nested starts as well as independent overlapping starts.
const startingReview = new AsyncLocalStorage<symbol>()

/**
 * Pausing stays the conservative default while Team tools are installed; the
 * operator can select scoped review instead (verified with DSH/Teams
 * 0.1.7-rc.2). That opt-in still requires startGuardedReview; it never removes
 * another plugin's policy.
 */
export function idleReviewBlockReason(parent: HostAgent, agentTeams: 'pause' | 'scoped' = 'pause'): 'agent-team' | undefined {
  return agentTeams !== 'scoped' && parent.ctx?.get?.('agentTeams') !== undefined
    && parent.ctx.tools?.get?.('spawn_teammate', parent) !== undefined ? 'agent-team' : undefined
}

export type ReviewToolPolicy = (execution: Readonly<ToolExecution>) => string | undefined

type ReviewLayer = 'working-memory' | 'documents'

function reviewLayer(execution: Readonly<ToolExecution>): ReviewLayer | undefined {
  if (execution.name === 'mnemon_document_create') return 'documents'
  if (execution.name !== 'mnemon_runtime_memory') return undefined
  const input = execution.arguments
  // Profile changes (target=user) never belong in a Document, so they stay independent.
  return typeof input === 'object' && input !== null && (input as { target?: unknown }).target === 'memory' ? 'working-memory' : undefined
}

/**
 * One idle review pass records project knowledge in one layer (#319). The first
 * Document creation or working-memory change it admits claims the pass; the
 * other layer is refused from then on, even when that first call fails, so a
 * refused or full layer is never evaded by writing the same knowledge elsewhere.
 */
export function reviewLayerPolicy(): ReviewToolPolicy {
  let claimed: ReviewLayer | undefined
  return execution => {
    const layer = reviewLayer(execution)
    if (layer === undefined) return undefined
    claimed ??= layer
    if (claimed === layer) return undefined
    return claimed === 'documents'
      ? 'This idle review already created a Document, so it cannot also change working memory (target=memory). Finish with the result tool.'
      : 'This idle review already changed working memory, so it cannot also create a Document. Finish with the result tool.'
  }
}

/**
 * DSH restrict() filters inherited capabilities, leaving own-scope plugin tools
 * visible. Attach its monotonic execution guard during publication, before a
 * review child can run. Async context attributes concurrent provider starts;
 * public registry ownership verifies the exact causal parent. An optional
 * policy then judges each allowed call, PTC sub-dispatches included.
 */
export async function startGuardedReview(
  host: ReviewToolHost,
  parent: HostAgent,
  toolNames: readonly string[],
  start: () => Promise<HostSubagentRun>,
  published?: (agent: HostAgent) => void,
  policy?: ReviewToolPolicy,
): Promise<HostSubagentRun> {
  const agents = host.agents
  if (typeof agents?.isOwnedBy !== 'function') throw new Error('Mnemon review requires DSH Agent ownership and scoped tool guard support')
  const pending = Symbol('Mnemon review publication')
  const allowed = new Set(toolNames)
  const guards = new Map<HostAgent, () => unknown>()
  let attachmentError: unknown
  let run: HostSubagentRun | undefined
  const listener = host.on('agent/created', (({ agent }: { agent: HostAgent }) => {
    if (startingReview.getStore() !== pending || !agents.isOwnedBy!(agent.id, parent)) return
    try {
      const tools = agent.ctx.tools
      if (typeof tools?.guard !== 'function') throw new Error('Mnemon review requires DSH scoped tool guard support')
      const dispose = tools.guard((execution: ToolExecution) => {
        // PTC is a transport: DSH also guards each end-capability sub-dispatch.
        if (execution.name === 'run_code' || (execution.name !== undefined && allowed.has(execution.name))) return policy?.(execution)
        return `Mnemon review cannot execute ${JSON.stringify(execution.name)}; reuse the inherited checkpoint and bounded Document search.`
      })
      if (typeof dispose !== 'function') throw new Error('Mnemon review tool guard did not return a disposer')
      guards.set(agent, dispose as () => unknown)
      published?.(agent)
    } catch (error) {
      attachmentError = error
      // Synchronous publication failure lets the provider roll back the child.
      throw error
    }
  }) as never)
  if (typeof listener !== 'function') throw new Error('Mnemon review creation observer did not return a disposer')
  let disposeObserver: (() => unknown) | undefined = listener as () => unknown
  const closeObserver = async () => {
    const dispose = disposeObserver
    disposeObserver = undefined
    await dispose?.()
  }
  const release = async () => {
    const disposers = [...guards.values()]
    guards.clear()
    const outcomes = await Promise.allSettled(disposers.map(async dispose => { await dispose() }))
    const failed = outcomes.filter(outcome => outcome.status === 'rejected')
    if (failed.length) throw new AggregateError(failed.map(outcome => outcome.reason), 'Mnemon review guard cleanup failed')
  }
  try {
    run = await startingReview.run(pending, start)
    if (attachmentError !== undefined) throw attachmentError
    const child = run.localAgent
    if (child === undefined || !guards.has(child)) {
      throw new Error('Mnemon review provider did not publish a local child with its scoped tool guard')
    }
    // Close publication observation before transferring the owned child; a
    // disposer failure must take the same rollback path as a failed start.
    await closeObserver()
    const active = run
    return { id: active.id, localAgent: child, result: active.result, async dispose() {
      try { await active.dispose() } finally { await release() }
    } }
  } catch (error) {
    try { await closeObserver() } catch { /* Preserve the original start failure. */ }
    try { await run?.dispose() } catch { /* Guards remain until child cleanup settles. */ }
    try { await release() } catch { /* Every guard disposer was attempted. */ }
    throw error
  }
}
