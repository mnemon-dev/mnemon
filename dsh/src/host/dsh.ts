import type { JsonValue, RpcResult, SettingsOperation } from "./protocol.ts"

export type {
  ClientConnectionHandle,
  ClientSettingsScope,
  ClientSettingsSnapshot,
  JsonPrimitive,
  JsonValue,
  RpcError,
  RpcResult,
  SettingsOperation,
} from "./protocol.ts"

export type HostRpcHandler = (endpoint: string, payload: unknown, signal?: AbortSignal) => Promise<RpcResult<unknown>>

export interface HostConnectionHandle {
  rpc: {
    handle(channel: string, handler: HostRpcHandler): unknown
  }
}

export interface HostSettingsScope<T> {
  get(): T
}

/** Mnemon's settings namespaces over DSH profile Config, with revision-fenced writes. */
export interface HostSettingsService {
  readonly writable: boolean
  register<T>(
    namespace: string,
    schema: unknown,
    options: { base?: Partial<T>; applies: 'live' | 'restart'; validate?: (value: T) => void },
  ): HostSettingsScope<T>
  describe(options?: { redactSecrets?: boolean }): Array<{
    ns: string
    value: unknown
    base?: unknown
    user?: unknown
    revision: number
    applies: 'live' | 'restart'
  }>
  mutate(namespace: string, ops: SettingsOperation[], expectedRevision?: number): Promise<void>
  /** Observe committed namespace values; returns the unsubscribe function. */
  onUpdated(listener: (namespace: string, value: unknown) => void): () => void
}

export interface ToolExecution {
  signal: AbortSignal
  agent?: HostAgent
  name?: string
  /** Parsed tool arguments, available on the authoritative tools/result event. */
  arguments?: unknown
  parent?: symbol
  token?: symbol
  /** End the current model turn after an authoritative terminal tool call. */
  concludeTurn?: () => void
}

export type CommandResult = { kind: 'success'; text?: string } | { kind: 'error'; text: string }

export interface CommandInvocation {
  agent: HostAgent
  rawInput: string
  signal: AbortSignal
}

export interface CommandDefinition {
  name: string
  description: string
  input?: { hint: string }
  handler(invocation: CommandInvocation): CommandResult | Promise<CommandResult>
}

export interface CommandService {
  register(definition: CommandDefinition): unknown
}

export interface ToolDefinition {
  name: string
  description: string
  parameters: Record<string, unknown>
  output: {
    schema: Record<string, unknown>
    render: (args: Record<string, unknown>, value: never) => Array<{ type: 'text'; text: string }>
    /** Durable, JSON-safe, tool-private projection available to Host UI replay. */
    presentationMeta?: (args: unknown, value: unknown) => JsonValue
  }
  execute: (args: never, execution: ToolExecution) => Promise<unknown>
  presentCall?: (args: never) => Record<string, unknown>
  presentResult?: () => Record<string, unknown>
}

export interface HostMessageSource {
  kind: string
  plugin?: string
  form?: string
  summary?: string
}

/** Minimum shape shared by DSH content blocks, including merge-added blocks. */
export interface HostOpaqueContentBlock {
  type: string
  /** Present on text-like blocks; omitted by images and other merge-added blocks. */
  text?: string
}

export interface HostTextContentBlock extends HostOpaqueContentBlock {
  type: 'text'
  text: string
}

/** Durable image metadata, matching DSH's attachment reference. */
export interface HostImageAttachmentRef {
  attachmentId: string
  mediaType: 'image/png' | 'image/jpeg' | 'image/webp' | 'image/gif'
  bytes: number
  width: number
  height: number
  name?: string
}

export interface HostImageContentBlock extends HostOpaqueContentBlock {
  type: 'image'
  attachment: HostImageAttachmentRef
}

/** User content is open to DSH/plugin blocks while documenting first-party text and image shapes. */
export type HostUserContentBlock = HostTextContentBlock | HostImageContentBlock | HostOpaqueContentBlock

export interface HostUserMessage {
  id: string
  role: 'user'
  content: HostUserContentBlock[]
  source: HostMessageSource
}

export interface HostSessionEvent {
  type: string
  seq?: number
  time?: number
  data: Record<string, unknown>
}

/** Minimum DSH Session surface: immutable header, range snapshots and indexed reads. */
export interface HostSession {
  header?: { origin?: 'subagent'; parentSession?: string; delegationDepth?: number; cwd?: string; agentPreset?: string }
  snapshotEvents(fromSeq?: number, toSeqExclusive?: number): readonly HostSessionEvent[]
  eventAt(seq: number): HostSessionEvent | undefined
  /** Model-visible event sequences, in order; rewinds and compaction replace them. */
  surface: { readonly nodes: readonly number[] }
  /** DSH's append, as its own request-error recoveries use it before retrying a step. */
  append?(type: string, data: unknown, options?: { surfaceOp?: 'append' }): unknown
}

export type HostPreStepDecision = { kind: 'reject' } | { kind: 'enter'; messages: HostUserMessage[] }

export interface HostAgentContext {
  /** DSH's monotonic guard also covers tools registered in this Agent's scope. */
  tools?: {
    guard?(guard: (execution: ToolExecution) => string | undefined): unknown
    /** Public presentation-agnostic lookup in the exact Agent scope. */
    get?(name: string, scope?: HostAgent): unknown
  }
  /**
   * `options` is optional and forwarded verbatim to the host. `prepend`
   * places the listener at the head of the chain, which for a waterfall
   * means it observes the fully assembled result. Omitting it keeps the
   * previous two-argument behaviour exactly.
   */
  on(name: string, listener: (...args: never[]) => unknown, options?: { prepend?: boolean }): () => unknown
  effect(callback: () => (() => unknown) | void, label?: string): () => unknown
  get?(name: string): unknown
}

export interface HostAgent {
  id: string
  status: 'idle' | 'running'
  options?: { provider?: string; model?: string; maxTokens?: number }
  session: HostSession
  ctx: HostAgentContext
  followup(message: HostUserMessage): void
  steer(message: HostUserMessage): void
  inject(message: HostUserMessage): void
}

export interface HostAgentHandle {
  agent: HostAgent
  dispose(): Promise<void>
}

export interface CreateHostAgentOptions {
  sessionId: string
  meta?: { cwd?: string; agentPreset?: string }
  agentOptions?: { provider?: string; model?: string; maxTokens?: number }
  signal?: AbortSignal
  setup?: (agentCtx: HostAgentContext) => unknown | Promise<unknown>
}

export interface HostAgentsService {
  get(id: string): HostAgent | undefined
  /** Public DSH runtime ownership, independent of persisted session lineage. */
  isOwnedBy?(id: string, parent: HostAgent): boolean
  roots(): HostAgent[]
  /** Factory for an owned, clean top-level Agent. */
  create(options: CreateHostAgentOptions): Promise<HostAgentHandle>
}

export interface HostWorkspace {
  readonly id: string
  readonly path: string
  readonly title: string
  /** Sessions the workspace lists, whether or not their Agents are loaded. */
  readonly sessionIds?: readonly string[]
}

export interface HostWorkspaceRegistry {
  get(id: string): HostWorkspace | undefined
  list(): HostWorkspace[]
}

export interface HostSubagentResult {
  output: Array<{ type: string; text?: string; [key: string]: unknown }>
  structured?: unknown
  /** Provider-authored failure detail; in-process children leave it unset. */
  diagnostic?: string
  stopReason: string
}

export interface HostSubagentRun {
  id: string
  localAgent?: HostAgent
  result: Promise<HostSubagentResult>
  dispose(): Promise<void>
}

export interface HostSubagentProvider {
  inheritsParentContext?: boolean
  capabilities: { outputSchema: boolean; depthLimit: boolean; toolFilter: boolean; persona: boolean }
}

export interface HostSubagentsService {
  list(): string[]
  getProvider(name: string): HostSubagentProvider | undefined
  start(name: string, request: {
    label?: string
    prompt: Array<{ type: 'text'; text: string }>
    parent: HostAgent
    signal: AbortSignal
    agentOptions?: { provider?: string; model?: string; maxTokens?: number }
    outputSchema?: Record<string, unknown>
    maxDepth?: number
    toolFilter?: { allow?: string[]; deny?: string[] }
    persona?: string
  }): Promise<HostSubagentRun>
}

export interface HostLlmService {
  listProviders(): Array<{ id: string; name: string }>
  listModels(provider: string): Promise<Array<{
    id: string
    name: string
    description?: string
    /** Absent means unknown; an explicit list declares the accepted request modalities. */
    inputModalities?: readonly string[]
  }>>
}

export interface HostContextShape {
  tools: { register(definition: ToolDefinition): unknown }
  commands: CommandService
  /** DSH's profile settings forms; only the Mnemon settings facade reads them. */
  settings: unknown
  /** Web-only transport; absent from non-Web profiles such as Headless. */
  connection?: HostConnectionHandle
  agents: HostAgentsService
  subagents: HostSubagentsService
  /** Web workbench catalog; Agent execution routes by session cwd without it. */
  workspaceRegistry?: HostWorkspaceRegistry
  get(name: string): unknown
  /** Cordis owns service publication in every profile, including Headless. */
  provide(name: string, value?: unknown, check?: () => boolean): unknown
  inject(services: string[], callback: (ctx: HostContextShape) => void): unknown
  /**
   * `options` is optional and forwarded verbatim to the host. `prepend`
   * places the listener at the head of the chain, which for a waterfall
   * means it observes the fully assembled result. Omitting it keeps the
   * previous two-argument behaviour exactly.
   */
  on(name: string, listener: (...args: never[]) => unknown, options?: { prepend?: boolean }): () => unknown
  effect(callback: () => (() => unknown) | void, label?: string): () => unknown
}
