import { randomUUID } from 'node:crypto'
import { resolve } from 'node:path'
import type { ResolvedConfig } from './config.ts'
import type {
  CreateHostAgentOptions,
  HostAgent,
  HostAgentHandle,
  HostContextShape,
  HostLlmService,
  HostPreStepDecision,
  HostSessionEvent,
  HostUserMessage,
} from "./dsh.ts"
import type { Insight, RememberRequest, SearchRequest } from 'dsh-mnemon-source-memory-spaces/contracts'
import type { RuntimeMemoryMutation } from 'dsh-mnemon-source-runtime/contracts'
import type { DocumentMutation } from 'dsh-mnemon-source-documents/contracts'
import { IdleReviewError, MnemonSubagentCoordinator, type DelegatedWriteResult } from './subagent.ts'
import { scoreReviewActivity } from './review-activity.ts'
import { idleReviewBlockReason } from './review-tools.ts'
import { TurnActivityProjection, type TurnMemoryActivity, type TurnMemoryActivitySnapshot } from './activity.ts'
import { applyMemoryViewGuidance } from './guidance.ts'
import { modelMemoryWake } from './view-presentation.ts'
import { AgentMemoryTurn, openAgentTurn, type DelegatedMemoryView } from './agent-memory-turn.ts'
import type { AssistantMessageText, LifecycleAgentSnapshot, LifecycleCounters, LifecyclePhase, LifecycleSnapshot, ReviewActivityScore, TaskAgentModelCatalog } from "./protocol.ts"
import type { PreparedMemoryPlacement } from 'dsh-mnemon-source-memory-spaces/contracts'
import type { MemoryWake } from "../core/contracts/index.ts"
import { agentScope, type MnemonAgentRuntimeSource } from './runtime.ts'
import { createPluginMessage, MNEMON_PLUGIN_SOURCE } from './plugin-message.ts'

type AgentRuntimeSource = Pick<MnemonAgentRuntimeSource, 'forAgent' | 'executions'>

interface HostDefaultModelService {
  currentSelection(): { provider: string; model: string }
}

interface HostAgentPresetService {
  resolve(id?: string): Promise<{ id: string }>
  mount(agentCtx: HostAgent['ctx'], id?: string): Promise<unknown>
}

function modelService(value: unknown): HostDefaultModelService | undefined {
  if (typeof value !== 'object' || value === null || !('currentSelection' in value) || typeof value.currentSelection !== 'function') return undefined
  return value as HostDefaultModelService
}

function presetService(value: unknown): HostAgentPresetService | undefined {
  if (typeof value !== 'object' || value === null || !('resolve' in value) || typeof value.resolve !== 'function' || !('mount' in value) || typeof value.mount !== 'function') return undefined
  return value as HostAgentPresetService
}

function llmService(value: unknown): HostLlmService | undefined {
  if (typeof value !== 'object' || value === null || !('listProviders' in value) || typeof value.listProviders !== 'function' || !('listModels' in value) || typeof value.listModels !== 'function') return undefined
  return value as HostLlmService
}

export type { TurnMemoryActivity, TurnMemoryActivitySnapshot } from './activity.ts'
export type { AssistantMessageText, LifecycleAgentSnapshot, LifecycleCounters, LifecyclePhase, LifecycleSnapshot } from "./protocol.ts"

export { MNEMON_PLUGIN_SOURCE }

export interface SupervisedWritebackResult extends DelegatedWriteResult { sessionId: string }

interface AgentEventPayload {
  agent: HostAgent
}

interface SessionStartPayload extends AgentEventPayload {
  source: 'startup' | 'resume' | 'clear' | 'compact'
}

interface PreStepPayload extends AgentEventPayload {
  messages: HostUserMessage[]
  turn: number
  step: number
  signal: AbortSignal
}

interface TurnStoppingPayload extends AgentEventPayload {
  turn: number
  signal: AbortSignal
}

interface PromptAssembly {
  sections: Array<{ name: string; text: string }>
  contexts: Array<{ name: string; text: string }>
  [key: string]: unknown
}

interface PromptAssemblyContext {
  agent?: HostAgent
  signal?: AbortSignal
}

function isMnemonMessageSource(source: unknown): boolean {
  if (typeof source !== 'object' || source === null) return false
  const { kind, plugin } = source as { kind?: unknown; plugin?: unknown }
  // V3-to-V4 migrates the legacy plugin wrapper to `plugin:dsh-mnemon`.
  return kind === MNEMON_PLUGIN_SOURCE
    || kind === `plugin:${MNEMON_PLUGIN_SOURCE}`
    || (kind === 'plugin' && plugin === MNEMON_PLUGIN_SOURCE)
}

/**
 * Whether an event is a durable user message this plugin produced.
 */
function isOwnUserMessageEvent(event: HostSessionEvent | undefined): boolean {
  if (event?.type !== 'user/message') return false
  return isMnemonMessageSource(event.data.source)
}

function eventTurn(event: HostSessionEvent): number | undefined {
  return typeof event.data.turn === 'number' ? event.data.turn : undefined
}

function memoryToolCalls(events: readonly HostSessionEvent[], turn?: number): number {
  return events.filter(event => event.type === 'tool/call'
    && (turn === undefined || eventTurn(event) === turn)
    && typeof event.data.name === 'string'
    && event.data.name.startsWith('mnemon_')).length
}

const REVIEW_SUBSTANTIVE_USER_CHARACTERS = 320
const REVIEW_SUBSTANTIVE_ASSISTANT_CHARACTERS = 600
const EXPLICIT_MEMORY_CANDIDATE = [
  /(?:记住|记下来|保存到记忆|写入记忆|长期记录)/u,
  /\b(?:please\s+)?remember\b/iu,
  /\b(?:save|store|write|record|persist)\b.{0,32}\b(?:memory|memories)\b/iu,
]
const NO_MEMORY_MAINTENANCE = [
  /(?:不要|不必|无需|请勿|禁止|别)(?:再|主动|自动|在后台|替我)?(?:记住|记忆)/u,
  /(?:不要|不必|无需|请勿|禁止|别)(?:再|主动|自动|在后台|替我)?(?:保存|写(?:入)?|记录|更新|维护|持久化).{0,16}(?:记忆|memory)/iu,
  /\b(?:do not|don't|dont|never|no need to)\s+(?:proactively\s+|automatically\s+)?remember\b/iu,
  /\b(?:do not|don't|dont|never|no need to)\s+(?:proactively\s+|automatically\s+)?(?:save|store|write|record|persist|update|maintain)\b.{0,32}\b(?:memory|memories)\b/iu,
  /\bno\s+(?:memory|memories)\s+(?:write|writes|writing|maintenance|update|updates)\b/iu,
]

function userMessageText(message: HostUserMessage): string {
  if (message.source.kind !== 'user') return ''
  return message.content
    .filter(block => block.type === 'text' && 'text' in block && typeof block.text === 'string')
    .map(block => block.text)
    .join('\n')
    .trim()
}

function matchesAny(value: string, patterns: readonly RegExp[]): boolean {
  return patterns.some(pattern => pattern.test(value))
}

function assistantEventTextLength(event: HostSessionEvent): number {
  if (event.type !== 'assistant/message') return 0
  const message = event.data.message as { content?: Array<{ type?: unknown; text?: unknown }> } | undefined
  if (!Array.isArray(message?.content)) return 0
  return message.content
    .filter(block => block.type === 'text' && typeof block.text === 'string')
    .map(block => String(block.text).trim().length)
    .reduce((total, length) => total + length, 0)
}

function completedToolActivity(events: readonly HostSessionEvent[], turn: number): { count: number; names: Set<string> } {
  const count = events.filter(event => event.type === 'tool/result' && eventTurn(event) === turn).length
  const names = new Set(events
    .filter(event => event.type === 'tool/call' && eventTurn(event) === turn && typeof event.data.name === 'string')
    .map(event => String(event.data.name)))
  return { count, names }
}

/** Join the text content of an `assistant/message` event whose message id matches. */
function assistantMessageText(events: readonly HostSessionEvent[], messageId: string): AssistantMessageText | null {
  for (const event of events) {
    if (event.type !== 'assistant/message') continue
    const message = event.data.message as { id?: unknown; content?: Array<{ type?: unknown; text?: unknown }> } | undefined
    if (message === undefined || typeof message !== 'object' || message.id !== messageId) continue
    const content = Array.isArray(message.content) ? message.content : []
    const text = content
      .filter(block => block.type === 'text' && typeof block.text === 'string')
      .map(block => String(block.text))
      .join('\n\n')
      .trim()
    return text === '' ? null : { messageId, text }
  }
  return null
}

function guidedReminder(config: ResolvedConfig, guidance?: import('../core/contracts/view.ts').MemoryViewGuidance): string | undefined {
  const read = config.recallMode === 'guided'
  const write = config.writeEnabled && config.writebackMode === 'guided'
  const chosen = read && write ? guidance?.reminders?.both : read ? guidance?.reminders?.read : write ? guidance?.reminders?.write : undefined
  if (chosen !== undefined) return chosen
  const instructions = [
    ...(config.recallMode === 'guided' ? ['Use mnemon_view_route only when relevant evidence is missing.'] : []),
    ...(config.writeEnabled && config.writebackMode === 'guided' ? ['Use mnemon_view_action only for an intended memory change; require a write receipt.'] : []),
  ]
  if (instructions.length === 0) return undefined
  return '[MNEMON] ' + instructions.join(' ') + ' Use only ids offered by the current View and follow each Source\'s semantics. Otherwise use none.'
}

class MnemonAgentLifecycle {
  private primePending = true
  private startSource: LifecycleAgentSnapshot['startSource']
  private readonly guidedTurns = new Set<number>()
  private readonly memoryActivity = new TurnActivityProjection()
  private readonly turnActivity = new Map<number, {
    messageIds: Set<string>
    userTextLength: number
    toolCallCount: number
    toolNames: Set<string>
    explicitCandidate: boolean
    noMaintenance: boolean
  }>()
  /**
   * Text of the runtime memory snapshot most recently injected as this
   * plugin's own message. `.context()` used to get supersede-on-change for free
   * from the host's runtime-context projection; carrying the snapshot as an own
   * message means re-emitting on change is this plugin's responsibility, and
   * the rendered text (which carries the revision digest) is the key.
   */
  private injectedMemoryText: string | undefined
  private idleReviewTimer: ReturnType<typeof setTimeout> | undefined
  private reviewController: AbortController | undefined
  private reviewRunning = false
  private lastReviewAt: string | undefined
  private lastReviewAction: string | undefined
  private lastReviewScore: number | undefined
  private lastReviewDocumentIds: string[] | undefined
  private idleReviewAttempts = 0
  private lastReviewAttemptAt: number | undefined
  private lastReviewFailure: LifecycleAgentSnapshot['lastReviewFailure']
  private lastPhase: LifecyclePhase = 'idle'
  private lastAt: string | undefined
  private lastError: string | undefined
  private composedTurn: { turn: number; strategyTypeId: string } | undefined

  constructor(
    readonly agent: HostAgent,
    private readonly coordinator: MnemonSubagentCoordinator,
    private readonly config: ResolvedConfig,
    private readonly counters: LifecycleCounters,
    source: LifecycleAgentSnapshot['startSource'],
    private readonly memoryTurn?: AgentMemoryTurn,
  ) {
    this.startSource = source
  }

  start(): () => void {
    const disposers = [
      this.agent.ctx.on('session/event', ((session: HostAgent['session'], event: HostSessionEvent) => this.sessionEvent(session, event)) as never),
      this.agent.ctx.on('system-prompt/assemble', ((assembly: PromptAssembly, context: PromptAssemblyContext, next: () => Promise<PromptAssembly>) => this.assemblePrompt(assembly, context, next)) as never),
      // `prepend: true` makes this the outermost pre-step participant, so it
      // observes the fully assembled batch and appends after every other
      // contributor's context. The snapshot therefore has no byte-stable
      // context behind it, and `recordTurnMessages` sees the complete batch
      // rather than a partial one.
      this.agent.ctx.on('agent/pre-step', ((payload: PreStepPayload, next: () => Promise<HostPreStepDecision>) => this.preStep(payload, next)) as never, { prepend: true }),
      this.agent.ctx.on('agent/turn-stopping', ((payload: TurnStoppingPayload) => this.finishTurn(payload)) as never),
    ]
    return () => {
      this.releaseView()
      this.cancelIdleReview(true)
      for (const dispose of disposers.reverse()) dispose()
    }
  }

  snapshot(): LifecycleAgentSnapshot {
    return {
      sessionId: this.agent.id,
      status: this.agent.status,
      startSource: this.startSource,
      primePending: this.primePending,
      guidedTurns: this.guidedTurns.size,
      memoryToolCalls: memoryToolCalls(this.agent.session.snapshotEvents()),
      idleReviewPending: this.idleReviewTimer !== undefined,
      reviewRunning: this.reviewRunning,
      reviewActivity: this.reviewActivity(),
      idleReviewAttempts: this.idleReviewAttempts,
      ...(idleReviewBlockReason(this.agent, this.config.idleReview.agentTeams) === undefined ? {} : { idleReviewBlocked: 'agent-team' as const }),
      ...(this.lastReviewAttemptAt === undefined ? {} : { nextReviewAt: new Date(this.lastReviewAttemptAt + this.config.idleReview.minIntervalMs).toISOString() }),
      ...(this.lastReviewFailure === undefined ? {} : { lastReviewFailure: this.lastReviewFailure }),
      lastPhase: this.lastPhase,
      ...(this.lastReviewAt === undefined ? {} : { lastReviewAt: this.lastReviewAt }),
      ...(this.lastReviewAction === undefined ? {} : { lastReviewAction: this.lastReviewAction }),
      ...(this.lastReviewScore === undefined ? {} : { lastReviewScore: this.lastReviewScore }),
      ...(this.lastReviewDocumentIds === undefined ? {} : { lastReviewDocumentIds: [...this.lastReviewDocumentIds] }),
      ...(this.lastAt === undefined ? {} : { lastAt: this.lastAt }),
      ...(this.lastError === undefined ? {} : { lastError: this.lastError }),
    }
  }

  markSupervised(): void {
    this.counters.supervisedRequests += 1
    this.mark('supervised')
  }

  /** Incremental snapshot of settled Mnemon activity in this durable log. */
  turnMemoryActivities(): TurnMemoryActivitySnapshot {
    return this.memoryActivity.snapshot(this.agent.session.snapshotEvents())
  }

  /** Plain text of one finalized assistant message, from this agent's session log. */
  assistantMessageText(messageId: string): AssistantMessageText | null {
    return assistantMessageText(this.agent.session.snapshotEvents(), messageId)
  }

  memoryWake(): MemoryWake | undefined {
    const pinned = this.memoryTurn?.current
    if (pinned === undefined) return undefined
    return modelMemoryWake(pinned.graph, pinned.context)
  }

  /**
   * Whether the reminder is still visible to the model.
   *
   * Read from the surface rather than an Agent-scoped flag, because a rewind
   * is a surface replacement inside the same live Agent: DSH does not recreate
   * the Agent, so a flag would stay set and the reminder would never return.
   * The durable event log cannot answer this either, since it is append-only
   * and still contains the discarded message.
   *
   * Scanning forward is cheap: the reminder sits near the head of the surface,
   * so the loop exits after a few nodes even on a long session.
   */
  private cueAlreadyVisible(): boolean {
    for (const seq of this.agent.session.surface.nodes) {
      if (isOwnUserMessageEvent(this.agent.session.eventAt(seq))) return true
    }
    return false
  }

  /**
   * The runtime memory snapshot, as this plugin's own message, when it changed.
   *
   * `.context()` used to carry this inside the host's shared runtime-context
   * projection. Carrying it here instead attributes it to dsh-mnemon and keeps
   * a memory write from re-emitting other contributors' sections; supersede
   * stays intact because the block is still a complete state replacing its
   * predecessor, keyed on the rendered text's revision digest.
   */
  private memorySnapshotMessage(): HostUserMessage | undefined {
    const wake = this.memoryWake()
    if (wake === undefined) return undefined
    // This message bypasses SystemPrompt interpolation; Source text stays literal.
    const text = wake.text
    if (text.trim() === '' || text === this.injectedMemoryText) return undefined
    this.injectedMemoryText = text
    return createPluginMessage(text, 'recall')
  }

  private async preStep(payload: PreStepPayload, next: () => Promise<HostPreStepDecision>): Promise<HostPreStepDecision> {
    if (payload.step === 1) this.cancelIdleReview(true)
    const decision = await next()
    if (decision.kind === 'reject' || payload.signal.aborted) {
      this.releaseView(payload.turn)
      return decision
    }
    if (!this.config.lifecycleEnabled) return decision
    if (this.config.writeEnabled && this.config.writebackMode === 'guided') {
      this.recordTurnMessages(payload.turn, decision.messages)
    }
    if (payload.step !== 1) return decision

    const ownRequest = decision.messages.some(message => isMnemonMessageSource(message.source))
    if (ownRequest) {
      return decision
    }
    if (decision.messages.length === 0) return decision
    // Independent of the one-shot reminder gate below: memory can change at any
    // point in a session, and each change must supersede the previous snapshot.
    // Appended last so the snapshot sits at the bottom of this plugin's block.
    const snapshot = this.memorySnapshotMessage()
    const withSnapshot = (messages: HostUserMessage[]): HostUserMessage[] =>
      snapshot === undefined ? messages : [...messages, snapshot]

    if (this.primePending) {
      this.primePending = false
      this.counters.primes += 1
      this.mark('prime')
    }
    if (this.cueAlreadyVisible()) return { kind: 'enter', messages: withSnapshot(decision.messages) }
    const reminder = guidedReminder(this.config, this.memoryTurn?.current?.context.view.guidance)
    if (reminder === undefined) return { kind: 'enter', messages: withSnapshot(decision.messages) }
    this.guidedTurns.add(payload.turn)
    if (this.config.recallMode === 'guided') this.counters.recallCues += 1
    if (this.config.writebackMode === 'guided' && this.config.writeEnabled) this.counters.writebackCues += 1
    this.mark(this.config.recallMode === 'guided' ? 'recall' : 'writeback')
    return { kind: 'enter', messages: withSnapshot([...decision.messages, createPluginMessage(reminder, 'instructions')]) }
  }

  private async assemblePrompt(assembly: PromptAssembly, context: PromptAssemblyContext, next: () => Promise<PromptAssembly>): Promise<PromptAssembly> {
    if (context.agent !== undefined && context.agent.id !== this.agent.id) return next()
    const turn = openAgentTurn(this.agent)
    if (turn === undefined) return next()
    // Cancel idle work before prompt assembly; the execution owner drains it.
    this.cancelIdleReview(true)
    if (context.signal?.aborted === true || openAgentTurn(this.agent) !== turn) return next()
    await this.memoryTurn?.begin(turn, context.signal)
    const assembled = await next()
    return applyMemoryViewGuidance(assembled, this.memoryTurn?.current?.context.view, this.config.routingGuidance)
  }

  private releaseView(turn?: number): void {
    this.memoryTurn?.end(turn)
  }

  private async finishTurn(payload: TurnStoppingPayload): Promise<void> {
    this.scheduleIdleReview(payload.turn)
  }

  private sessionEvent(session: HostAgent['session'], event: HostSessionEvent): void {
    if (session !== this.agent.session || event.type !== 'turn/end') return
    const turn = eventTurn(event)
    const current = this.memoryTurn?.current
    const pinned = turn === undefined || current?.turn !== turn ? undefined : current
    if (pinned === undefined) return
    this.releaseView(turn)
  }

  private scheduleIdleReview(turn: number): void {
    this.cancelIdleReview(true)
    if (!this.idleReviewAllowed() || !this.composedByDefault(turn)) return
    const activity = this.ensureTurnActivity(turn)
    const tools = completedToolActivity(this.agent.session.snapshotEvents(), turn)
    activity.toolCallCount = tools.count
    activity.toolNames = tools.names
    if (!this.reviewActivity().eligible || !this.reviewAdmitted(turn)) return
    this.idleReviewTimer = setTimeout(() => {
      this.idleReviewTimer = undefined
      if (!this.idleReviewAllowed() || this.reviewRunning) return
      if (this.lastReviewAttemptAt !== undefined && Date.now() < this.lastReviewAttemptAt + this.config.idleReview.minIntervalMs) {
        this.scheduleIdleReview(turn)
        return
      }
      if (this.agent.status !== 'idle') return
      const completed = this.agent.session.snapshotEvents().some(event => event.type === 'turn/end' && eventTurn(event) === turn)
      if (!completed || !this.reviewActivity().eligible || !this.reviewAdmitted(turn)) return
      void this.runIdleReview()
    }, Math.max(this.config.idleReviewMs, (this.lastReviewAttemptAt ?? -Infinity) + this.config.idleReview.minIntervalMs - Date.now()))
  }

  /**
   * Automatic three-tier maintenance belongs to the default product, not to
   * every third-party View Strategy, and only to turns that Strategy actually
   * composed: a turn served by a fallback Strategy, or run without memory, has
   * nothing for it to maintain. Explicit management remains available.
   */
  private composedByDefault(turn: number): boolean {
    if (this.memoryTurn === undefined) return true
    const pinned = this.memoryTurn.current
    if (pinned?.turn === turn) this.composedTurn = { turn, strategyTypeId: pinned.context.view.strategyTypeId }
    else if (this.composedTurn?.turn !== turn) this.composedTurn = undefined
    return this.composedTurn?.strategyTypeId === 'default-three-tier'
  }

  private idleReviewAllowed(): boolean {
    return this.config.lifecycleEnabled && this.config.writeEnabled && this.config.writebackMode === 'guided'
      && this.config.idleReview.enabled && this.idleReviewAttempts < this.config.idleReview.maxPerSession
      && idleReviewBlockReason(this.agent, this.config.idleReview.agentTeams) === undefined
      && this.config.memoryTopology.strategyId === 'default-three-tier'
  }

  private async runIdleReview(): Promise<void> {
    const controller = new AbortController()
    const triggeredScore = this.reviewActivity().score
    this.idleReviewAttempts += 1
    this.lastReviewAttemptAt = Date.now()
    this.reviewRunning = true
    this.reviewController = controller
    this.mark('review')
    try {
      const result = await this.coordinator.review(this.agent, controller.signal)
      if (controller.signal.aborted) return
      this.lastReviewAt = new Date().toISOString()
      this.lastReviewAction = result.action
      this.lastReviewScore = triggeredScore
      this.lastReviewDocumentIds = result.documentIds
      this.lastReviewFailure = undefined
      this.turnActivity.clear()
      this.lastError = undefined
      this.mark('review')
    } catch (error) {
      // Cancellation after a commit still needs reconciliation. Never retry a
      // failed child here, even when a different provider is available.
      if (error instanceof IdleReviewError) this.lastReviewFailure = error.review
      if (!controller.signal.aborted || error instanceof IdleReviewError && error.review.status === 'partial') this.fail(error)
    } finally {
      if (this.reviewController === controller) {
        this.reviewRunning = false
        this.reviewController = undefined
      }
    }
  }

  private cancelIdleReview(abortRunning: boolean): void {
    if (this.idleReviewTimer !== undefined) clearTimeout(this.idleReviewTimer)
    this.idleReviewTimer = undefined
    if (abortRunning) this.reviewController?.abort()
  }

  private ensureTurnActivity(turn: number) {
    let activity = this.turnActivity.get(turn)
    if (activity === undefined) {
      activity = {
        messageIds: new Set(),
        userTextLength: 0,
        toolCallCount: 0,
        toolNames: new Set(),
        explicitCandidate: false,
        noMaintenance: false,
      }
      this.turnActivity.set(turn, activity)
    }
    return activity
  }

  private recordTurnMessages(turn: number, messages: readonly HostUserMessage[]): void {
    const activity = this.ensureTurnActivity(turn)
    for (const message of messages) {
      if (message.source.kind !== 'user' || activity.messageIds.has(message.id)) continue
      activity.messageIds.add(message.id)
      const text = userMessageText(message)
      activity.userTextLength += text.length
      activity.explicitCandidate ||= matchesAny(text, EXPLICIT_MEMORY_CANDIDATE)
      activity.noMaintenance ||= matchesAny(text, NO_MEMORY_MAINTENANCE)
    }
  }

  /** Cheap Host signals decide whether an eligible checkpoint is worth an LLM call. */
  private reviewAdmitted(currentTurn: number): boolean {
    if (this.turnActivity.get(currentTurn)?.noMaintenance === true) return false
    const turns = new Set([...this.turnActivity.entries()]
      .filter(([, activity]) => !activity.noMaintenance)
      .map(([turn]) => turn))
    let totalUserTextLength = 0
    let explicitCandidate = false
    let completedNonMemoryTool = false
    for (const activity of this.turnActivity.values()) {
      if (activity.noMaintenance) continue
      totalUserTextLength += activity.userTextLength
      explicitCandidate ||= activity.explicitCandidate
      completedNonMemoryTool ||= activity.toolCallCount > 0 && [...activity.toolNames].some(name => !name.startsWith('mnemon_'))
    }
    const assistantTextLength = this.agent.session.snapshotEvents()
      .filter(event => {
        const turn = eventTurn(event)
        return turn !== undefined && turns.has(turn)
      })
      .map(assistantEventTextLength)
      .reduce((total, length) => total + length, 0)
    return explicitCandidate
      || totalUserTextLength >= REVIEW_SUBSTANTIVE_USER_CHARACTERS
      || assistantTextLength >= REVIEW_SUBSTANTIVE_ASSISTANT_CHARACTERS
      || completedNonMemoryTool
  }

  private reviewActivity(): ReviewActivityScore {
    const toolNames = new Set<string>()
    let totalUserTextLength = 0
    let toolCallCount = 0
    for (const activity of this.turnActivity.values()) {
      totalUserTextLength += activity.userTextLength
      toolCallCount += activity.toolCallCount
      for (const name of activity.toolNames) toolNames.add(name)
    }
    return scoreReviewActivity({
      totalUserTextLength,
      turnCount: this.turnActivity.size,
      toolCallCount,
      uniqueToolCount: toolNames.size,
    })
  }

  private mark(phase: LifecyclePhase): void {
    this.lastPhase = phase
    this.lastAt = new Date().toISOString()
  }

  private fail(error: unknown): void {
    this.counters.failures += 1
    this.lastPhase = 'error'
    this.lastAt = new Date().toISOString()
    this.lastError = (error instanceof Error ? error.message : String(error))
      .replace(/\bsk-[A-Za-z0-9_-]{8,}\b/gu, '[redacted]')
      .replace(/\s+/gu, ' ').trim().slice(0, 500)
    console.warn(`[dsh-mnemon] idle review failed: ${this.lastError}`)
  }

}

/** DSH-native owner for per-agent Mnemon lifecycle hooks and UI-triggered LLM work. */
export class MnemonLifecycle {
  private readonly owners = new Map<HostAgent, { lifecycle: MnemonAgentLifecycle; dispose: () => unknown }>()
  private readonly children = new Map<HostAgent, () => unknown>()
  private readonly memoryTurns = new Map<HostAgent, AgentMemoryTurn>()
  private readonly counters: LifecycleCounters = { primes: 0, recallCues: 0, writebackCues: 0, supervisedRequests: 0, failures: 0 }
  /** Creation ids reserved before DSH publishes clean task-root Agents. */
  private readonly taskAgentIds = new Set<string>()
  /** Bounded process-local replay fence for finalized-message write actions. */
  private readonly supervisedWritebacks = new Map<string, { content: string; result: Promise<SupervisedWritebackResult> }>()

  constructor(
    private readonly ctx: HostContextShape,
    private readonly coordinator: MnemonSubagentCoordinator,
    private readonly config: ResolvedConfig,
    private readonly runtimeSource?: AgentRuntimeSource,
  ) {}

  start(): () => void {
    // DSH creates a new Agent for startup, resume, clear and compaction alike.
    const stopCreated = this.ctx.on('agent/created', (({ agent, source }: SessionStartPayload) => { this.install(agent, source) }) as never)
    for (const agent of this.ctx.agents.roots()) this.install(agent, 'adopted')
    return () => {
      stopCreated()
      for (const dispose of [...this.children.values()].reverse()) dispose()
      for (const owner of [...this.owners.values()].reverse()) owner.dispose()
      this.children.clear()
      this.owners.clear()
      this.memoryTurns.clear()
    }
  }

  snapshot(sessionId?: string, workspaceRoot?: string): LifecycleSnapshot {
    const requestedId = sessionId?.trim()
    const requested = requestedId === undefined || requestedId === '' ? undefined : this.ctx.agents.get(requestedId)
    const agent = requested !== undefined && this.owners.has(requested) ? requested : this.availableAgent(workspaceRoot)
    const owner = agent === undefined ? undefined : this.owners.get(agent)?.lifecycle
    return {
      enabled: this.config.lifecycleEnabled,
      recallMode: this.config.recallMode,
      writebackMode: this.config.writebackMode,
      idleReviewMs: this.config.idleReviewMs,
      activeAgents: this.owners.size,
      sessionAvailable: agent !== undefined,
      taskAgentAvailable: this.taskAgentModelOptions(requestedId ?? '', workspaceRoot) !== undefined,
      counters: { ...this.counters },
      subagents: this.coordinator.snapshot(),
      ...(owner === undefined ? {} : { current: owner.snapshot() }),
    }
  }

  /** Provider/model directory used by Settings without requiring a live session. */
  async taskAgentModels(includeCatalog = true): Promise<TaskAgentModelCatalog> {
    const route = this.taskAgentModelRoute('', undefined)
    let defaultSelection: { provider: string; model: string } | undefined
    try {
      const selected = modelService(this.ctx.get('agentDefaultModel'))?.currentSelection()
      const provider = selected?.provider.trim()
      const model = selected?.model.trim()
      if (provider !== undefined && provider !== '' && model !== undefined && model !== '') defaultSelection = { provider, model }
    } catch {}

    const base = {
      ...(route === undefined ? {} : { effective: { provider: route.options.provider!, model: route.options.model!, source: route.source } }),
      ...(defaultSelection === undefined ? {} : { defaultSelection }),
    }
    if (!includeCatalog) return { ...base, groups: [], failures: [] }
    const llm = llmService(this.ctx.get('llm'))
    if (llm === undefined) {
      return { ...base, groups: [], failures: [{ id: 'dsh', name: 'DSH', message: 'model directory service is unavailable' }] }
    }

    let providers: Array<{ id: string; name: string }>
    try {
      providers = llm.listProviders()
    } catch (error) {
      return { ...base, groups: [], failures: [{ id: 'dsh', name: 'DSH', message: error instanceof Error ? error.message : String(error) }] }
    }
    const entries = await Promise.all(providers.map(async provider => {
      try {
        let timeout: ReturnType<typeof setTimeout> | undefined
        const models = await Promise.race([
          llm.listModels(provider.id),
          new Promise<never>((_, reject) => { timeout = setTimeout(() => reject(new Error('model directory timed out after 3 seconds')), 3_000) }),
        ]).finally(() => { if (timeout !== undefined) clearTimeout(timeout) })
        return {
          kind: 'group' as const,
          value: {
            id: provider.id,
            name: provider.name,
            models: models.map(model => ({
              id: model.id,
              name: model.name,
              ...(model.description === undefined ? {} : { description: model.description }),
              ...(model.inputModalities === undefined ? {} : { inputModalities: [...model.inputModalities] }),
            })),
          },
        }
      } catch (error) {
        return { kind: 'failure' as const, value: { id: provider.id, name: provider.name, message: error instanceof Error ? error.message : String(error) } }
      }
    }))
    return {
      ...base,
      groups: entries.flatMap(entry => entry.kind === 'group' && entry.value.models.length > 0 ? [entry.value] : []),
      failures: entries.flatMap(entry => entry.kind === 'failure' ? [entry.value] : []),
    }
  }

  private availableAgent(workspaceRoot?: string): HostAgent | undefined {
    const agents = [...this.owners.keys()]
    const normalizedRoot = workspaceRoot?.trim()
    if (normalizedRoot === undefined || normalizedRoot === '') return agents.find(agent => agent.status === 'idle') ?? agents[0]
    const expected = resolve(normalizedRoot)
    const matching = agents.filter(agent => {
      const cwd = agent.session.header?.cwd?.trim()
      return cwd !== undefined && cwd !== '' && resolve(cwd) === expected
    })
    return matching.find(agent => agent.status === 'idle') ?? matching[0]
  }

  workspaceRoot(sessionId?: string): string | undefined {
    if (sessionId === undefined || sessionId.trim() === '') return undefined
    return this.ctx.agents.get(sessionId.trim())?.session.header?.cwd
  }

  memoryView(sessionId: string, workspaceRoot?: string): import('./view-protocol.ts').MemoryViewInspection | undefined {
    const agent = this.ctx.agents.get(sessionId.trim())
    return agent === undefined ? undefined : this.memoryTurns.get(agent)?.inspect(workspaceRoot)
  }

  /** Settled memory-tool activity for all turns, resolved per session. */
  turnActivities(sessionId: string): TurnMemoryActivitySnapshot {
    const agent = this.ctx.agents.get(sessionId.trim())
    const owner = agent === undefined ? undefined : this.owners.get(agent)?.lifecycle
    return owner === undefined ? { cursor: 0, activities: [] } : owner.turnMemoryActivities()
  }

  /** Plain text of one finalized assistant message, resolved per session; null while absent. */
  assistantMessage(sessionId: string, messageId: string): AssistantMessageText | null {
    const agent = this.ctx.agents.get(sessionId.trim())
    const owner = agent === undefined ? undefined : this.owners.get(agent)?.lifecycle
    return owner === undefined ? null : owner.assistantMessageText(messageId)
  }

  recall(sessionId: string, request: SearchRequest, signal = new AbortController().signal) {
    return this.coordinator.recall(this.liveAgent(sessionId), request, signal)
  }

  related(sessionId: string, id: string, memoryBodyId?: string, signal = new AbortController().signal) {
    return this.coordinator.related(this.liveAgent(sessionId), id, memoryBodyId, signal)
  }

  answer(sessionId: string, query: string, evidence: Insight[], signal = new AbortController().signal) {
    return this.coordinator.answer(this.liveAgent(sessionId), query, evidence, signal)
  }

  /** Synthesize a Web Agent Query without borrowing a conversation Agent or its history. */
  answerTask(sessionId: string, query: string, evidence: Insight[], workspaceRoot?: string, signal = new AbortController().signal) {
    const root = workspaceRoot?.trim() || this.workspaceRoot(sessionId)
    return this.runTaskAgent(sessionId, root, signal, agent => this.coordinator.answer(agent, query, evidence, signal))
  }

  remember(sessionId: string, request: RememberRequest, signal = new AbortController().signal) {
    return this.coordinator.remember(this.liveAgent(sessionId), request, signal)
  }

  runtime(sessionId: string, request: RuntimeMemoryMutation, signal = new AbortController().signal) {
    return this.coordinator.runtime(this.liveAgent(sessionId), request, signal)
  }

  manageSource(graph: import('./runtime.ts').MnemonRuntimeGraph, request: import('../core/contracts/index.ts').MemorySourceManagementRequest) {
    return this.coordinator.manageSource(graph, request)
  }

  runRuntimeMaintenanceTask<T>(scope: import('../core/contracts/index.ts').MemoryOperationScope, signal: AbortSignal, operation: (agent: HostAgent) => Promise<T>): Promise<T> {
    return this.runTaskAgent('', scope.workspaceId, signal, operation)
  }

  documents(sessionId: string) {
    return this.coordinator.documentsSnapshot(this.liveAgent(sessionId))
  }

  document(sessionId: string, id: string) {
    return this.coordinator.documentGet(this.liveAgent(sessionId), id)
  }

  searchDocuments(sessionId: string, query: string, includeArchived = false, limit?: number) {
    return this.coordinator.documentSearch(this.liveAgent(sessionId), query, includeArchived, limit)
  }

  mutateDocument(sessionId: string, request: DocumentMutation, signal = new AbortController().signal) {
    return this.coordinator.document(this.liveAgent(sessionId), request, signal)
  }

  /**
   * The same Document write under a fresh task Agent, for a conversation whose
   * Agent is not loaded: it archives the least recently used Document to make
   * room, as the conversation's Agent would.
   */
  mutateDocumentTask(sessionId: string, request: DocumentMutation, workspaceRoot?: string, signal = new AbortController().signal) {
    const root = workspaceRoot?.trim() || this.workspaceRoot(sessionId)
    if (root === undefined || root.trim() === '') throw new Error('a selected DSH workspace is required to write a Mnemon Document')
    return this.runTaskAgent(sessionId, root, signal, agent => this.coordinator.document(agent, request, signal))
  }

  archiveDocument(sessionId: string, id: string, workspaceRoot?: string, signal = new AbortController().signal) {
    const root = workspaceRoot?.trim() || this.workspaceRoot(sessionId)
    if (root === undefined || root.trim() === '') throw new Error('a selected DSH workspace is required to archive a Mnemon Document')
    return this.runTaskAgent(sessionId, root, signal, agent => this.coordinator.archiveDocument(agent, id, signal))
  }

  mutate(sessionId: string, operation: string, request: unknown, signal = new AbortController().signal) {
    return this.coordinator.write(this.liveAgent(sessionId), operation, request, signal)
  }

  placeProvider(sessionId: string, body: { name: string; description: string }, prepared: PreparedMemoryPlacement, signal = new AbortController().signal) {
    return this.coordinator.placeProvider(this.liveAgent(sessionId), body, prepared, signal)
  }

  /** Choose a new Memory Space's Provider under a fresh task Agent, as Ask Agent does without a loaded conversation Agent. */
  placeProviderTask(sessionId: string, body: { name: string; description: string }, prepared: PreparedMemoryPlacement, workspaceRoot?: string, signal = new AbortController().signal) {
    const root = workspaceRoot?.trim() || this.workspaceRoot(sessionId)
    return this.runTaskAgent(sessionId, root, signal, agent => this.coordinator.placeProvider(agent, body, prepared, signal))
  }

  maintainMetadata(sessionId: string, memoryBodyIds: readonly string[], workspaceRoot?: string, signal = new AbortController().signal) {
    const root = workspaceRoot?.trim() || this.workspaceRoot(sessionId)
    return this.runTaskAgent(sessionId, root, signal, agent => this.coordinator.maintainMetadata(agent, memoryBodyIds, signal))
  }

  async supervise(sessionId: string, content: string, idempotencyKey?: string, signal = new AbortController().signal): Promise<SupervisedWritebackResult> {
    const normalizedSessionId = sessionId.trim()
    if (normalizedSessionId === '') throw new Error('current DSH session is unavailable')
    return this.superviseResolved(
      normalizedSessionId,
      normalizedSessionId,
      content,
      idempotencyKey,
      signal,
      async operation => operation(this.liveAgent(normalizedSessionId)),
    )
  }

  /** Run a Web workbench distillation under a fresh top-level task Agent. */
  async superviseTask(sessionId: string, content: string, idempotencyKey?: string, workspaceRoot?: string, signal = new AbortController().signal): Promise<SupervisedWritebackResult> {
    const normalizedSessionId = sessionId.trim()
    const root = workspaceRoot?.trim() || this.workspaceRoot(normalizedSessionId)
    const scopeKey = root === undefined ? `task:${normalizedSessionId || 'global'}` : `task:${resolve(root)}`
    return this.superviseResolved(
      scopeKey,
      normalizedSessionId,
      content,
      idempotencyKey,
      signal,
      operation => this.runTaskAgent(normalizedSessionId, root, signal, operation),
    )
  }

  private async superviseResolved(
    replayScope: string,
    responseSessionId: string,
    content: string,
    idempotencyKey: string | undefined,
    signal: AbortSignal,
    withAgent: <T>(operation: (agent: HostAgent) => Promise<T>) => Promise<T>,
  ): Promise<SupervisedWritebackResult> {
    if (!this.config.writeEnabled) throw new Error('dsh-mnemon is configured read-only (writeEnabled: false)')
    const normalizedContent = content.trim()
    if (normalizedContent === '') throw new Error('memory candidate is required')
    if (normalizedContent.length > 8000) throw new Error('memory candidate is too long (max 8000 characters)')
    const normalizedKey = idempotencyKey?.trim()
    if (normalizedKey !== undefined && normalizedKey.length > 200) throw new Error('idempotency key is too long (max 200 characters)')

    const execute = async (): Promise<SupervisedWritebackResult> => {
      return withAgent(async agent => {
        const owner = this.owners.get(agent)?.lifecycle
        if (owner === undefined) this.counters.supervisedRequests += 1
        else owner.markSupervised()
        const result = await this.coordinator.write(agent, 'supervised-writeback', {
          content: normalizedContent,
          source: normalizedKey === undefined || normalizedKey === '' ? 'explicit Mnemon tab submission' : 'explicit assistant memory action',
        }, signal)
        return { ...result, sessionId: responseSessionId || agent.id }
      })
    }

    if (normalizedKey === undefined || normalizedKey === '') return execute()
    const replayKey = `${replayScope}\u0000${normalizedKey}`
    const existing = this.supervisedWritebacks.get(replayKey)
    if (existing !== undefined) {
      if (existing.content !== normalizedContent) throw new Error('idempotency key was already used for different content')
      return existing.result
    }
    if (this.supervisedWritebacks.size >= 256) {
      const oldest = this.supervisedWritebacks.keys().next().value as string | undefined
      if (oldest !== undefined) this.supervisedWritebacks.delete(oldest)
    }
    const result = execute()
    this.supervisedWritebacks.set(replayKey, { content: normalizedContent, result })
    void result.catch(() => {
      if (this.supervisedWritebacks.get(replayKey)?.result === result) this.supervisedWritebacks.delete(replayKey)
    })
    return result
  }

  private liveAgent(sessionId: string): HostAgent {
    const normalized = sessionId.trim()
    if (normalized === '') throw new Error('current DSH session is unavailable')
    const agent = this.ctx.agents.get(normalized)
    if (agent === undefined) throw new Error('current DSH agent is not live; reopen or resume the conversation and try again')
    return agent
  }

  /**
   * Run session-independent maintenance under a fresh top-level Agent. Its cwd
   * is the explicit Web workbench scope, so LiveMnemonRuntime resolves the same
   * workspace graph without borrowing conversation history or ownership.
   */
  private async runTaskAgent<T>(
    fallbackSessionId: string,
    workspaceRoot: string | undefined,
    signal: AbortSignal,
    operation: (agent: HostAgent) => Promise<T>,
  ): Promise<T> {
    const sessionId = randomUUID()
    this.taskAgentIds.add(sessionId)
    let handle: HostAgentHandle | undefined
    let failure: unknown
    try {
      const creation = await this.taskAgentCreation(fallbackSessionId, workspaceRoot)
      handle = await this.ctx.agents.create({
        sessionId,
        ...creation,
        signal,
      })
      return await operation(handle.agent)
    } catch (error) {
      failure = error
      throw error
    } finally {
      if (handle !== undefined) {
        try { await handle.dispose() } catch (error) { if (failure === undefined) throw error }
      }
      this.taskAgentIds.delete(sessionId)
    }
  }

  /** Resolve the same model route and preset composition as an ordinary fresh DSH Agent. */
  private async taskAgentCreation(
    fallbackSessionId: string,
    workspaceRoot: string | undefined,
  ): Promise<Pick<CreateHostAgentOptions, 'meta' | 'agentOptions' | 'setup'>> {
    const agentOptions = this.taskAgentModelOptions(fallbackSessionId, workspaceRoot)
    if (agentOptions === undefined) throw new Error('no default provider/model is available for a clean task Agent')
    const cwd = workspaceRoot?.trim()
    const presets = presetService(this.ctx.get('agentPresets'))
    if (presets === undefined) {
      return {
        ...(cwd === undefined || cwd === '' ? {} : { meta: { cwd: resolve(cwd) } }),
        agentOptions,
      }
    }

    const presetId = (await presets.resolve()).id
    return {
      meta: {
        ...(cwd === undefined || cwd === '' ? {} : { cwd: resolve(cwd) }),
        agentPreset: presetId,
      },
      agentOptions,
      setup: async agentCtx => { await presets.mount(agentCtx, presetId) },
    }
  }

  /** Resolve a complete task route for both status admission and actual creation. */
  private taskAgentModelRoute(fallbackSessionId: string, workspaceRoot: string | undefined): { options: NonNullable<CreateHostAgentOptions['agentOptions']>; source: 'fixed' | 'dsh-default' | 'active-agent' } | undefined {
    const fallback = this.ctx.agents.get(fallbackSessionId.trim()) ?? this.availableAgent(workspaceRoot) ?? this.availableAgent()
    if (this.config.taskAgentModel.mode === 'fixed') {
      const provider = this.config.taskAgentModel.provider?.trim()
      const model = this.config.taskAgentModel.model?.trim()
      if (provider === undefined || provider === '' || model === undefined || model === '') return undefined
      return {
        source: 'fixed',
        options: { provider, model, ...(fallback?.options?.maxTokens === undefined ? {} : { maxTokens: fallback.options.maxTokens }) },
      }
    }
    let selected: { provider: string; model: string } | undefined
    try { selected = modelService(this.ctx.get('agentDefaultModel'))?.currentSelection() } catch {}
    const selectedProvider = selected?.provider.trim()
    const selectedModel = selected?.model.trim()
    const provider = selectedProvider || fallback?.options?.provider?.trim()
    const model = selectedModel || fallback?.options?.model?.trim()
    if (provider === undefined || provider === '' || model === undefined || model === '') return undefined
    return { source: selectedProvider !== undefined && selectedProvider !== '' && selectedModel !== undefined && selectedModel !== '' ? 'dsh-default' : 'active-agent', options: { provider, model, ...(fallback?.options?.maxTokens === undefined ? {} : { maxTokens: fallback.options.maxTokens }) } }
  }

  private taskAgentModelOptions(fallbackSessionId: string, workspaceRoot: string | undefined): NonNullable<CreateHostAgentOptions['agentOptions']> | undefined {
    return this.taskAgentModelRoute(fallbackSessionId, workspaceRoot)?.options
  }

  private install(agent: HostAgent, source: LifecycleAgentSnapshot['startSource']): void {
    if (this.taskAgentIds.has(agent.id) || this.owners.has(agent) || this.children.has(agent)) return
    if (agent.session.header?.origin === 'subagent') {
      this.installChild(agent)
      return
    }
    if (!this.ctx.agents.roots().includes(agent)) return
    const memoryTurn = this.runtimeSource === undefined ? undefined : new AgentMemoryTurn(agent, this.runtimeSource)
    const lifecycle = new MnemonAgentLifecycle(agent, this.coordinator, this.config, this.counters, source, memoryTurn)
    let dispose: () => unknown
    dispose = agent.ctx.effect(() => {
      const stop = lifecycle.start()
      return () => {
        try {
          stop()
        } finally {
          memoryTurn?.dispose()
          if (this.owners.get(agent)?.dispose === dispose) this.owners.delete(agent)
          if (this.memoryTurns.get(agent) === memoryTurn) this.memoryTurns.delete(agent)
        }
      }
    }, 'dsh-mnemon.lifecycle()')
    this.owners.set(agent, { lifecycle, dispose })
    if (memoryTurn !== undefined) this.memoryTurns.set(agent, memoryTurn)
  }

  /** Child memory ownership is independent of root-only reminders and idle review. */
  private installChild(agent: HostAgent): void {
    const runtime = this.runtimeSource
    const parentId = agent.session.header?.parentSession?.trim()
    const parent = parentId === undefined || parentId === '' ? undefined : this.ctx.agents.get(parentId)
    // A lineage string alone is not authority. Orphans get no model Recall pin.
    if (runtime === undefined || parent === undefined || parent === agent) return
    const parentMemory = this.memoryTurns.get(parent)
    let delegation: DelegatedMemoryView
    if (parentMemory !== undefined) {
      delegation = parentMemory.delegate()
    } else {
      const graph = runtime.forAgent(parent)
      const parentPin = graph.composableTurns.activeTurn(parent.id)
      delegation = {
        graph,
        scope: parentPin?.scope ?? agentScope(parent, graph.config),
        ...(parentPin === undefined ? {} : { viewId: parentPin.view.id }),
      }
    }
    let dispose: () => unknown
    dispose = agent.ctx.effect(() => {
      const memory = new AgentMemoryTurn(agent, runtime, delegation)
      this.memoryTurns.set(agent, memory)
      const stops: Array<() => unknown> = []
      const cleanup = () => {
        try {
          for (const stop of stops.reverse()) stop()
        } finally {
          try { memory.dispose() } finally {
            if (this.memoryTurns.get(agent) === memory) this.memoryTurns.delete(agent)
            if (this.children.get(agent) === dispose) this.children.delete(agent)
          }
        }
      }
      try {
        stops.push(agent.ctx.on('system-prompt/assemble', (async (_assembly: PromptAssembly, context: PromptAssemblyContext, next: () => Promise<PromptAssembly>) => {
          if (context.agent !== undefined && context.agent !== agent) return next()
          const turn = openAgentTurn(agent)
          if (turn !== undefined) await memory.begin(turn, context.signal)
          return applyMemoryViewGuidance(await next(), memory.current?.context.view, this.config.routingGuidance)
        }) as never))
        stops.push(agent.ctx.on('agent/pre-step', (async (payload: PreStepPayload, next: () => Promise<HostPreStepDecision>) => {
          const decision = await next()
          if (decision.kind === 'reject' || payload.signal.aborted) memory.end(payload.turn)
          return decision
        }) as never))
        stops.push(agent.ctx.on('session/event', ((session: HostAgent['session'], event: HostSessionEvent) => {
          if (session === agent.session && event.type === 'turn/end') memory.end(eventTurn(event))
        }) as never))
      } catch (error) {
        cleanup()
        throw error
      }
      return cleanup
    }, 'dsh-mnemon.child-memory()')
    this.children.set(agent, dispose)
  }
}
