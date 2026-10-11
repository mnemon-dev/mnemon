import { realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { Context } from '@deepseek-ai/cordis'
import AgentRegistry, { type Agent } from '@deepseek-ai/dsh-agent'
import AgentLoop from '@deepseek-ai/dsh-agent-loop'
import LlmRuntime, { LlmAdapter, createUserMessage, type GenerateOptions, type StreamChunk } from '@deepseek-ai/dsh-llm'
import SessionStore, { SessionId } from '@deepseek-ai/dsh-session'
import JsonlSessionPersistence from '@deepseek-ai/dsh-session-persistence-jsonl'
import SessionProjectionRegistry from '@deepseek-ai/dsh-session-projection'
import SubagentRuntime from '@deepseek-ai/dsh-subagent'
import SystemPrompt from '@deepseek-ai/dsh-system-prompt'
import ToolRuntime, { type ToolExecutionInput } from '@deepseek-ai/dsh-tools'
import { expect, it } from 'vitest'
import type { HostAgent, HostContextShape } from '../src/host/dsh.ts'
import { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { compositionFixture } from './fixtures/composition.ts'

const requireDsh = createRequire(realpathSync(new URL('../node_modules/@deepseek-ai/dsh/package.json', import.meta.url)))
const fork = await import(requireDsh.resolve('@deepseek-ai/dsh-subagent-fork-in-process'))
const spawn = await import(requireDsh.resolve('@deepseek-ai/dsh-subagent-spawn-in-process'))
const ptc = await import(requireDsh.resolve('@deepseek-ai/dsh-ptc-runtime-node'))
const ptcServices = await Promise.all(['dsh-fs-local', 'dsh-subprocess-local', 'dsh-sandbox-local', 'dsh-sandbox-policy']
  .map(name => import(requireDsh.resolve(`@deepseek-ai/${name}`))))
const foreignTool = 'mcp__aoci__aoci_overview'
const chunks = Array.from({ length: 5 }, (_, chunk) => `COMPLETE_OVERVIEW_CHUNK_${chunk + 1}\n` + Array.from({ length: 67 }, (_, index) => `Synthetic module ${chunk * 67 + index + 1}: complete inherited index evidence.`).join('\n'))
type Reply = string | { name: string; args: Record<string, unknown> }

/** Only the model is fixed: scopes, fork history, dispatch and tools are real DSH. */
class ReviewAdapter extends LlmAdapter {
  private calls = 0
  constructor(private readonly respond: (options: GenerateOptions) => Reply) { super() }
  override async *stream(options: GenerateOptions): AsyncIterable<StreamChunk> {
    const reply = this.respond(options)
    const id = `review-${++this.calls}` as Extract<StreamChunk, { type: 'tool-call-delta' }>['id']
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

it.each(['native', 'ptc'].flatMap(mode => ['fork', 'spawn'].map(provider => ({ mode, provider }))))('reuses checkpoint evidence and denies own-scope foreign execution with $provider in $mode mode', async ({ mode, provider }) => {
  const f = await compositionFixture()
  f.config.idleReview.provider = provider as 'fork' | 'spawn'
  const ctx = new Context()
  let stop: (() => void) | undefined
  const executions: string[] = []
  const childReceipts: Array<{ name: string | undefined; isError: boolean }> = []
  const children: Agent[] = []
  let parentCalls = 0
  let childCalls = 0
  let inheritedChunks = 0
  const attempted = Promise.withResolvers<void>()
  let providerReturned = false
  try {
    await ctx.plugin(LlmRuntime)
    await ctx.plugin(SessionStore)
    await ctx.plugin(JsonlSessionPersistence, { root: join(f.root, 'sessions'), compression: 'none' })
    await ctx.plugin(SessionProjectionRegistry)
    await ctx.plugin(SystemPrompt)
    if (mode === 'ptc') {
      for (const [index, service] of ptcServices.entries()) {
        await ctx.plugin(service.default, index === 0 ? { cwd: f.workspace } : index === 3 ? { mode: 'read-only', workspaceRoot: f.workspace } : {})
      }
      await ctx.plugin(ptc.default)
    }
    await ctx.plugin(ToolRuntime, { mode: mode === 'ptc' ? 'both' : 'native' })
    await ctx.plugin(AgentRegistry)
    await ctx.plugin(AgentLoop, { agents: [] })
    await ctx.plugin(SubagentRuntime)
    await ctx.plugin(fork, { providerName: 'fork' })
    await ctx.plugin(spawn, { providerName: 'spawn' })
    ctx.llm.registerAdapter(['mock'], new ReviewAdapter(options => {
      if (options.sessionId === 'parent') return parentCalls < 5
        ? { name: foreignTool, args: { chunk: parentCalls++ } }
        : 'The complete overview is already in this checkpoint. No new durable user preference.'
      const system = options.system ?? JSON.stringify(options.messages.filter(message => message.role === 'system'))
      const terminal = system.match(/Completion protocol: call `([^`]+)`/u)?.[1]
      const requestId = system.match(/requestId `([^`]+)`/u)?.[1]
      if (!terminal) throw new Error('review completion capability is missing')
      const call = (name: string, args: Record<string, unknown>): Reply => mode === 'native' ? { name, args } : {
        name: 'run_code', args: { description: 'Exercise the review boundary through Code Mode.', code: `return await tools[${JSON.stringify(name)}](${JSON.stringify(args)})` },
      }
      const step = childCalls++
      if (step === 0) {
        inheritedChunks = chunks.filter(chunk => JSON.stringify(options.messages).includes(JSON.stringify(chunk).slice(1, -1))).length
        // The same attempt on baseline and fixed code exercises the dispatcher.
        return call(foreignTool, { chunk: 0 })
      }
      if (step === 1) return call('mnemon_document_search', { query: 'synthetic module overview', limit: 1 })
      const result = { summary: 'Complete inherited overview reviewed; no durable new assertion.', action: 'skipped', memoryBodyIds: [], documentIds: [] }
      return call(terminal, requestId === undefined ? result : { requestId, result })
    }))
    // An independently composed plugin attaches its capability to EVERY Agent.
    // Such own-scope tools intentionally survive DSH's inherited-tool restrict().
    ctx.on('agent/created', ({ agent }): undefined => {
      if (agent.session.header.origin === 'subagent') {
        children.push(agent)
        if (mode === 'ptc') agent.ctx.tools.presentAs('ptc')
      }
      agent.ctx.tools.register({
        name: foreignTool, description: 'Read one complete synthetic overview chunk.',
        parameters: { type: 'object', properties: { chunk: { type: 'integer', minimum: 0, maximum: 4 } }, required: ['chunk'], additionalProperties: false },
        output: { schema: { type: 'string' }, render: (_args, value) => [{ type: 'text', text: String(value) }] },
        async execute(args) { executions.push(agent.id); return chunks[(args as { chunk: number }).chunk]! },
      })
    })
    ctx.on('tools/result', (execution, result) => {
      if (execution.agent?.session.header.origin === 'subagent') {
        childReceipts.push({ name: execution.name, isError: result.isError })
        if (execution.name === foreignTool) {
          expect(providerReturned).toBe(false)
          attempted.resolve()
        }
      }
    })
    const host = ctx as unknown as HostContextShape
    const service = {
      list: () => host.subagents.list(), getProvider: (name: string) => host.subagents.getProvider(name),
      async start(...args: Parameters<typeof host.subagents.start>) {
        const run = await host.subagents.start(...args)
        // A fast child can call tools before the caller receives its run handle.
        try {
          await Promise.race([attempted.promise, run.result.then(() => { throw new Error('child completed before its foreign tool attempt') })])
        } catch (error) {
          await run.dispose()
          throw error
        }
        providerReturned = true
        return run
      },
    }
    const coordinator = new MnemonSubagentCoordinator(service, f.live, host)
    const lifecycle = new MnemonLifecycle(host, coordinator, f.config, f.live)
    registerTools(host, f.live, coordinator)
    stop = lifecycle.start()
    const parent = await ctx.agentLoop.create(SessionId('parent'), { provider: 'mock', model: 'mock' }, { cwd: f.workspace })
    parent.followup(createUserMessage({ content: [{ type: 'text', text: 'Read the full synthetic overview once.' }], source: { kind: 'user' } }))
    await parent.whenIdle()
    await coordinator.review(parent as unknown as HostAgent, new AbortController().signal)
    expect(inheritedChunks).toBe(5)
    expect(executions.filter(id => id === 'parent')).toHaveLength(5)
    expect(children).toHaveLength(1)
    expect(executions.filter(id => id !== 'parent')).toEqual([])
    expect(childReceipts.filter(receipt => receipt.name !== 'run_code')).toEqual([
      { name: foreignTool, isError: true },
      { name: 'mnemon_document_search', isError: false },
      { name: expect.stringMatching(/^mnemon_subagent_result(?:_|$)/u), isError: false },
    ])
    if (mode === 'ptc') expect(childReceipts.filter(receipt => receipt.name === 'run_code').map(receipt => receipt.isError)).toEqual([true, false, false])
    expect(coordinator.snapshot().reviews).toBe(1)
    const parentResult = await ctx.tools.execute({ name: foreignTool, arguments: { chunk: 0 }, agent: parent,
      callId: 'after-review' as ToolExecutionInput['callId'], signal: new AbortController().signal })
    expect(parentResult.isError).toBe(false)
    expect(executions.filter(id => id === 'parent')).toHaveLength(6)
  } finally {
    stop?.()
    await ctx.fiber.dispose()
    await f.dispose()
  }
}, 20_000)
