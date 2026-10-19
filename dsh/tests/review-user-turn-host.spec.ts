import { realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { Context } from '@deepseek-ai/cordis'
import AgentRegistry from '@deepseek-ai/dsh-agent'
import AgentLoop from '@deepseek-ai/dsh-agent-loop'
import LlmRuntime, { LlmAdapter, createUserMessage, type GenerateOptions, type StreamChunk } from '@deepseek-ai/dsh-llm'
import SessionStore, { SessionId } from '@deepseek-ai/dsh-session'
import JsonlSessionPersistence from '@deepseek-ai/dsh-session-persistence-jsonl'
import SessionProjectionRegistry from '@deepseek-ai/dsh-session-projection'
import SubagentRuntime from '@deepseek-ai/dsh-subagent'
import SystemPrompt from '@deepseek-ai/dsh-system-prompt'
import ToolRuntime from '@deepseek-ai/dsh-tools'
import { expect, it } from 'vitest'
import type { HostAgent, HostContextShape } from '../src/host/dsh.ts'
import { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { CONTINUATION_TEXT } from '../src/host/continuation-turn.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { compositionFixture } from './fixtures/composition.ts'

const requireDsh = createRequire(realpathSync(new URL('../node_modules/@deepseek-ai/dsh/package.json', import.meta.url)))
const fork = await import(requireDsh.resolve('@deepseek-ai/dsh-subagent-fork-in-process'))
const spawn = await import(requireDsh.resolve('@deepseek-ai/dsh-subagent-spawn-in-process'))

type Message = GenerateOptions['messages'][number]
type Reply = string | { name: string; args: Record<string, unknown> }
const text = (message: Message) => message.content.flatMap(block => block.type === 'text' ? [block.text] : []).join('')
const tokens = (message: Message) => Math.ceil(JSON.stringify(message).length / 4)
const sourceKind = (message: Message) => (message as { source?: { kind?: string } }).source?.kind
/** The delegated prompt: the last user message a person, not a plugin, sent. */
const promptIndex = (messages: readonly Message[]) => messages.findLastIndex(message => message.role === 'user' && sourceKind(message) === 'user')

interface OllamaRequest {
  session: string
  /** The last message, named by where it came from. */
  last: string
  /** Continuation turns in the request. */
  continuations: number
  /** A user text turn survived truncation, so the template renders. */
  userQuery: boolean
  promptKept: boolean
}

/**
 * Ollama 0.33 behind DSH's pi-ai route, serving a model whose chat template
 * requires a user query (issue #327). pi-ai sends the leading system message as
 * the system prompt and folds a later one into a user message. Ollama keeps the
 * system prompt and the longest suffix that fits num_ctx, always the last
 * message, and refuses a prompt without a user text turn.
 */
class StrictTemplateOllama extends LlmAdapter {
  readonly requests: OllamaRequest[] = []
  numCtx = Number.POSITIVE_INFINITY
  private calls = 0
  constructor(private readonly respond: (options: GenerateOptions) => Reply) { super() }

  override async *stream(options: GenerateOptions): AsyncIterable<StreamChunk> {
    const messages = options.messages
    const leading = messages[0]?.role === 'system' ? 1 : 0
    let start = messages.length - 1
    let size = messages.slice(0, leading).reduce((sum, message) => sum + tokens(message), 0) + tokens(messages[start]!)
    while (start > leading && size + tokens(messages[start - 1]!) <= this.numCtx) size += tokens(messages[--start]!)
    const kept = messages.slice(start)
    const userQuery = kept.some(message => (message.role === 'user' || message.role === 'system') && text(message).trim() !== '')
    const last = messages.at(-1)!
    this.requests.push({
      session: String(options.sessionId),
      last: last.role === 'user' ? sourceKind(last) ?? 'user' : last.role,
      continuations: messages.filter(message => message.role === 'user' && text(message) === CONTINUATION_TEXT).length,
      userQuery,
      promptKept: promptIndex(messages) >= start,
    })
    if (!userQuery) {
      yield { type: 'finish', reason: { kind: 'error', failure: { message: 'no user query found in messages', code: 'SERVER_ERROR', status: 500 } } }
      return
    }
    const reply = this.respond(options)
    const id = `ollama-${++this.calls}` as Extract<StreamChunk, { type: 'tool-call-delta' }>['id']
    if (typeof reply === 'string') {
      yield { type: 'block-start', index: 0, blockType: 'text' }
      yield { type: 'text-delta', index: 0, text: reply }
      yield { type: 'block-end', index: 0, block: { type: 'text', text: reply } }
    } else {
      const args = JSON.stringify(reply.args)
      yield { type: 'block-start', index: 0, blockType: 'tool-call' }
      yield { type: 'tool-call-delta', index: 0, id, name: reply.name, argumentsDelta: args }
      yield { type: 'block-end', index: 0, block: { type: 'tool-call', id, name: reply.name, arguments: args } }
    }
    yield { type: 'usage', usage: { inputTokens: 10, outputTokens: 5 } }
    yield { type: 'finish', reason: { kind: typeof reply === 'string' ? 'stop' : 'tool-calls' } }
  }
}

const decision = Array.from({ length: 24 }, (_, index) => `- Checkpoint rule ${index + 1}: the fixture project writes review checkpoints to SQLite before compaction.`).join('\n')

it.each([
  { provider: 'fork', truncates: true },
  { provider: 'spawn', truncates: true },
  { provider: 'fork', truncates: false },
  { provider: 'spawn', truncates: false },
])('completes a $provider review whose server refuses a request without a user query (truncates: $truncates)', async ({ provider, truncates }) => {
  const f = await compositionFixture()
  f.config.idleReview.provider = provider as 'fork' | 'spawn'
  const ctx = new Context()
  let stop: (() => void) | undefined
  let childCalls = 0
  let documentId: string | undefined
  const ollama = new StrictTemplateOllama(options => {
    if (options.sessionId === 'parent') return 'Settled: the fixture project keeps its review checkpoints in SQLite.'
    const system = JSON.stringify(options.messages.filter(message => message.role === 'system'))
    const terminal = system.match(/Completion protocol: call `([^`]+)`/u)?.[1]
    const requestId = system.match(/requestId `([^`]+)`/u)?.[1]
    if (terminal === undefined || requestId === undefined) throw new Error('review completion capability is missing')
    const step = childCalls++
    if (step === 0) return { name: 'mnemon_document_search', args: { query: 'SQLite review checkpoints', limit: 1 } }
    if (step === 1) return { name: 'mnemon_document_create', args: {
      title: 'Review checkpoint storage', description: 'Synthetic project knowledge for issue #327.', content: `# Review checkpoint storage\n\n${decision}`,
    } }
    if (step === 2) {
      for (const message of options.messages) {
        if (message.role !== 'tool') continue
        try {
          const value = JSON.parse(text(message)) as { action?: string; document?: { id?: string } }
          if (value.action === 'created') documentId = value.document?.id
        } catch { /* Search results are not receipts. */ }
      }
      // The window holds this request from the delegated prompt on; the next tool round overflows it.
      const messages = options.messages
      const prompt = promptIndex(messages)
      if (truncates) ollama.numCtx = tokens(messages[0]!) + messages.slice(prompt).reduce((sum, message) => sum + tokens(message), 0)
      return { name: 'mnemon_document_search', args: { query: 'Review checkpoint storage', limit: 1 } }
    }
    return { name: terminal, args: { requestId, result: {
      summary: 'Recorded where review checkpoints are stored.', action: 'created', memoryBodyIds: [], documentIds: documentId === undefined ? [] : [documentId],
    } } }
  })
  try {
    await ctx.plugin(LlmRuntime)
    await ctx.plugin(SessionStore)
    await ctx.plugin(JsonlSessionPersistence, { root: join(f.root, 'sessions'), compression: 'none' })
    await ctx.plugin(SessionProjectionRegistry)
    await ctx.plugin(SystemPrompt)
    await ctx.plugin(ToolRuntime, { mode: 'native' })
    await ctx.plugin(AgentRegistry)
    await ctx.plugin(AgentLoop, { agents: [] })
    await ctx.plugin(SubagentRuntime)
    await ctx.plugin(fork, { providerName: 'fork' })
    await ctx.plugin(spawn, { providerName: 'spawn' })
    ctx.llm.registerAdapter(['ollama'], ollama)
    const host = ctx as unknown as HostContextShape
    const coordinator = new MnemonSubagentCoordinator(host.subagents, f.live, host)
    const lifecycle = new MnemonLifecycle(host, coordinator, f.config, f.live)
    registerTools(host, f.live, coordinator)
    stop = lifecycle.start()
    const parent = await ctx.agentLoop.create(SessionId('parent'), { provider: 'ollama', model: 'qwen3.8' }, { cwd: f.workspace })
    parent.followup(createUserMessage({ content: [{ type: 'text', text: 'Decide where the fixture project keeps its review checkpoints.' }], source: { kind: 'user' } }))
    await parent.whenIdle()

    const result = await coordinator.review(parent as unknown as HostAgent, new AbortController().signal)

    expect(result).toMatchObject({ delegated: true, action: 'created' })
    expect(documentId).toBeDefined()
    const child = ollama.requests.filter(request => request.session !== 'parent')
    if (truncates) {
      // As reported, the fourth request lost the delegated prompt and was refused. It ran again at once,
      // ending with the continuation turn; nothing before the refusal changed.
      expect(child.map(request => request.promptKept)).toEqual([true, true, true, false, false])
      expect(child.map(request => request.userQuery)).toEqual([true, true, true, false, true])
      expect(child.map(request => request.continuations)).toEqual([0, 0, 0, 0, 1])
      expect(child.at(-1)!.last).toBe('dsh-mnemon')
    } else {
      // A server that keeps the prompt never refuses, so every request stays as it was.
      expect(child.map(request => [request.userQuery, request.promptKept, request.continuations])).toEqual([[true, true, 0], [true, true, 0], [true, true, 0], [true, true, 0]])
    }
    expect(child.slice(0, 4).map(request => request.last)).not.toContain('dsh-mnemon')
    expect(ollama.requests.filter(request => request.session === 'parent').map(request => request.continuations)).toEqual([0])
  } finally {
    stop?.()
    await ctx.fiber.dispose()
    await f.dispose()
  }
}, 20_000)
