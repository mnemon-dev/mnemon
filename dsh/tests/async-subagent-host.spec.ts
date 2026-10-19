import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { Context } from '@deepseek-ai/cordis'
import AgentRegistry, { type Agent } from '@deepseek-ai/dsh-agent'
import AgentLoop from '@deepseek-ai/dsh-agent-loop'
import LlmRuntime, { LlmAdapter, createUserMessage, type ContentBlock, type GenerateOptions, type StreamChunk } from '@deepseek-ai/dsh-llm'
import SessionStore, { SessionId } from '@deepseek-ai/dsh-session'
import JsonlSessionPersistence from '@deepseek-ai/dsh-session-persistence-jsonl'
import SessionProjectionRegistry from '@deepseek-ai/dsh-session-projection'
import SessionQueryEngine from '@deepseek-ai/dsh-session-query'
import SubagentRuntime from '@deepseek-ai/dsh-subagent'
import * as SubagentSpawn from '@deepseek-ai/dsh-subagent-spawn-in-process'
import SystemPrompt from '@deepseek-ai/dsh-system-prompt'
import ToolRuntime, { type ToolExecutionResult } from '@deepseek-ai/dsh-tools'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { HostAgent, HostContextShape } from '../src/host/dsh.ts'
import { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { LiveMnemonRuntime } from '../src/host/runtime.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { memoryGraphFixture } from './helpers/memory-graph.ts'

type Response = string | { name: string; args: Record<string, unknown> }
const cleanups: Array<() => Promise<void>> = []

afterEach(async () => {
  for (const cleanup of cleanups.splice(0).reverse()) await cleanup()
})

/** Use DSH's exact session reads; these scenarios do not need a full-text index. */
class SessionReads extends SessionQueryEngine {
  override async searchSessions(): Promise<never> { throw new Error('unexpected full-text session search') }
  override async searchEvents(): Promise<never> { throw new Error('unexpected full-text event search') }
}

/** A model stub; all Agent, tool, scope, persistence and subagent code is DSH. */
class ScriptedAdapter extends LlmAdapter {
  private calls = 0

  constructor(private readonly respond: (options: GenerateOptions) => Response | Promise<Response>) { super() }

  override async *stream(options: GenerateOptions): AsyncIterable<StreamChunk> {
    const response = await this.respond(options)
    options.signal?.throwIfAborted()
    const id = `test-call-${++this.calls}` as Extract<StreamChunk, { type: 'tool-call-delta' }>['id']
    if (typeof response === 'string') {
      yield { type: 'block-start', index: 0, blockType: 'text' }
      yield { type: 'text-delta', index: 0, text: response }
      yield { type: 'block-end', index: 0, block: { type: 'text', text: response } }
    } else {
      const args = JSON.stringify(response.args)
      yield { type: 'block-start', index: 0, blockType: 'tool-call' }
      yield { type: 'tool-call-delta', index: 0, id, name: response.name, argumentsDelta: args }
      yield { type: 'block-end', index: 0, block: { type: 'tool-call', id, name: response.name, arguments: args } }
    }
    yield { type: 'usage', usage: { inputTokens: 10, outputTokens: 5 } }
    yield { type: 'finish', reason: { kind: typeof response === 'string' ? 'stop' : 'tool-calls' } }
  }
}

async function gate(promise: Promise<void>, signal?: AbortSignal): Promise<void> {
  const aborted = Promise.withResolvers<void>()
  const abort = () => aborted.reject(signal?.reason)
  signal?.addEventListener('abort', abort, { once: true })
  try {
    signal?.throwIfAborted()
    await Promise.race([promise, aborted.promise])
  } finally {
    signal?.removeEventListener('abort', abort)
  }
}

interface CompatibleSubagentContinuation {
  followup?(parent: Agent, childId: SessionId, content: ContentBlock[], options: {
    source: { kind: 'user' }
    signal: AbortSignal
  }): Promise<unknown>
  sendMessage?(sender: Agent, targetId: SessionId, content: ContentBlock[], options: {
    signal: AbortSignal
  }): Promise<unknown>
}

async function continueChild(runtime: SubagentRuntime, parent: Agent, childId: SessionId, content: ContentBlock[]): Promise<void> {
  const compatible = runtime as unknown as CompatibleSubagentContinuation
  const signal = new AbortController().signal
  if (typeof compatible.sendMessage === 'function') {
    await compatible.sendMessage(parent, childId, content, { signal })
    return
  }
  if (typeof compatible.followup === 'function') {
    await compatible.followup(parent, childId, content, { source: { kind: 'user' }, signal })
    return
  }
  throw new TypeError('Unsupported DSH subagent continuation API')
}

async function harness(respond: (options: GenerateOptions) => Response | Promise<Response>) {
  const root = await mkdtemp(join(tmpdir(), 'mnemon-async-host-'))
  const ctx = new Context()
  const memory = memoryGraphFixture()
  const runtime = new LiveMnemonRuntime(memory.graph, undefined, undefined, memory.extensions)
  let stop: (() => void) | undefined
  cleanups.push(async () => {
    try { await ctx.fiber.dispose() } finally {
      stop?.()
      runtime.dispose()
      await rm(root, { recursive: true, force: true })
    }
  })
  await ctx.plugin(LlmRuntime)
  await ctx.plugin(SessionStore)
  await ctx.plugin(JsonlSessionPersistence, { root, compression: 'none' })
  await ctx.plugin(SessionProjectionRegistry)
  await ctx.plugin(SessionReads)
  await ctx.plugin(SystemPrompt)
  await ctx.plugin(ToolRuntime)
  await ctx.plugin(AgentRegistry)
  await ctx.plugin(AgentLoop, { agents: [] })
  await ctx.plugin(SubagentRuntime)
  await ctx.plugin(SubagentSpawn, { providerName: 'spawn' })
  ctx.llm.registerAdapter(['mock'], new ScriptedAdapter(respond))
  const host = ctx as unknown as HostContextShape
  const coordinator = new MnemonSubagentCoordinator(host.subagents, runtime)
  const lifecycle = new MnemonLifecycle(host, coordinator, memory.graph.config, runtime)
  registerTools(host, runtime, coordinator)
  stop = lifecycle.start()
  const children: Agent[] = []
  const recalls: ToolExecutionResult[] = []
  ctx.on('agent/created', ({ agent }): undefined => {
    if (agent.session.header.origin === 'subagent') children.push(agent)
  })
  ctx.on('tools/result', (execution, result) => {
    if (execution.name === 'mnemon_recall') recalls.push(result)
  })
  ctx.tools.register({
    name: 'test_start_child',
    description: 'Start one asynchronous child.',
    parameters: { type: 'object', properties: {} },
    output: { schema: { type: 'object', additionalProperties: true }, render: (_args, value) => [{ type: 'text', text: JSON.stringify(value) }] },
    async execute(_args, execution) {
      if (execution.agent === undefined) throw new Error('test tool requires an Agent')
      return ctx.subagents.startContinuable({
        provider: 'spawn', label: 'Recall task',
        request: { parent: execution.agent, prompt: [{ type: 'text', text: 'Recall release history.' }] },
        signal: execution.signal,
      })
    },
  })
  const parent = await ctx.agentLoop.create(SessionId('parent'), { provider: 'mock', model: 'mock' }, { cwd: root })
  const runParent = async () => {
    parent.followup(createUserMessage({ content: [{ type: 'text', text: 'Continue.' }], source: { kind: 'user' } }))
    await parent.whenIdle()
  }
  return { ...memory, ctx, runtime, lifecycle, parent, children, recalls, runParent }
}

describe('asynchronous Recall with the real DSH host', () => {
  it('keeps the dispatch authority after parent completion, a later turn and a runtime swap', async () => {
    const release = Promise.withResolvers<void>()
    let parentCalls = 0
    let childCalls = 0
    const value = await harness(async options => {
      if (options.sessionId === 'parent') return parentCalls++ === 0 ? { name: 'test_start_child', args: {} } : 'Parent done.'
      if (childCalls++ === 0) {
        await gate(release.promise, options.signal)
        return { name: 'mnemon_recall', args: { query: 'release history' } }
      }
      return 'Child done.'
    })
    await value.runParent()
    expect(value.children).toHaveLength(1)
    const child = value.children[0]!
    await vi.waitFor(() => expect(childCalls).toBe(1), { timeout: 5_000 })
    const dispatchView = value.views.activeTurn(child.id)!.view.id
    expect(value.views.activeTurn(value.parent.id)).toBeUndefined()
    const replacement = memoryGraphFixture(['replacement'])
    value.runtime.swap(replacement.graph)
    await value.runParent()
    expect(value.runtime.forAgent(child as unknown as HostAgent)).toBe(value.graph)
    expect(value.views.activeTurn(child.id)!.view.id).toBe(dispatchView)
    expect(value.lifecycle.snapshot().activeAgents).toBe(1)
    release.resolve()
    await vi.waitFor(() => expect(value.ctx.agents.get(child.id)).toBeUndefined(), { timeout: 5_000 })
    expect(value.recalls).toEqual([expect.objectContaining({ isError: false, value: expect.objectContaining({ results: [expect.objectContaining({ memoryBodyId: 'project' })] }) })])
    expect(value.search).toHaveBeenCalledOnce()
    expect(replacement.search).not.toHaveBeenCalled()
    expect(value.views.activeTurn(child.id)).toBeUndefined()
    expect(value.runtime.forAgent(child as unknown as HostAgent)).toBe(replacement.graph)
  }, 15_000)

  it('gives a cold-resumed child a new pin and retrieval budget instead of replaying its previous activation', async () => {
    let parentCalls = 0
    let childCalls = 0
    const value = await harness(options => {
      if (options.sessionId === 'parent') return parentCalls++ === 0 ? { name: 'test_start_child', args: {} } : 'Parent done.'
      const step = childCalls++ % 4
      return step === 3 ? 'Child done.' : { name: 'mnemon_recall', args: { query: step === 0 ? 'release history' : step === 1 ? 'release details' : 'one query too many' } }
    })
    await value.runParent()
    expect(value.children).toHaveLength(1)
    const child = value.children[0]!
    await vi.waitFor(() => expect(value.ctx.agents.get(child.id)).toBeUndefined(), { timeout: 5_000 })
    expect(value.search).toHaveBeenCalledTimes(2)
    const replacement = memoryGraphFixture(['replacement'])
    value.runtime.swap(replacement.graph)
    await continueChild(value.ctx.subagents, value.parent, child.id, [{ type: 'text', text: 'Recall release history again.' }])
    await vi.waitFor(() => expect(value.ctx.agents.get(child.id)).toBeUndefined(), { timeout: 5_000 })
    expect(value.children).toHaveLength(2)
    expect(value.children[1]!.id).toBe(child.id)
    expect(value.children[1]).not.toBe(child)
    expect(replacement.search).toHaveBeenCalledTimes(2)
    expect(value.recalls).toHaveLength(6)
    expect(value.recalls.every(result => !result.isError)).toBe(true)
    expect(value.recalls[3]).toMatchObject({ value: { query: 'release history', results: [expect.objectContaining({ memoryBodyId: 'replacement' })] } })
    expect(value.views.activeTurn(child.id)).toBeUndefined()
    expect(replacement.views.activeTurn(child.id)).toBeUndefined()
  }, 15_000)
})
