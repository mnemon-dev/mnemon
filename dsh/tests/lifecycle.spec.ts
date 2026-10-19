import { afterEach, describe, expect, it, vi } from 'vitest'
import { realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { assertReleasedPayloadSemantics } from '@deepseek-ai/dsh-session-format-v0-to-v1'
import { MemoryExecutions } from '../src/host/memory-executions.ts'
import { resolveConfig } from "../src/host/config.ts"
import type {
  CreateHostAgentOptions,
  HostAgent,
  HostAgentContext,
  HostContextShape,
  HostPreStepDecision,
  HostSession,
  HostSessionEvent,
  HostUserMessage,
} from "../src/host/dsh.ts"
import { MnemonLifecycle } from "../src/host/lifecycle.ts"
import { IdleReviewError, type MnemonSubagentCoordinator } from "../src/host/subagent.ts"
import { sessionLog } from './fixtures/session-log.ts'

type Listener = (...args: unknown[]) => unknown

function userMessage(text = 'Continue the project'): HostUserMessage {
  return {
    id: crypto.randomUUID(),
    role: 'user',
    content: [{ type: 'text', text }],
    source: { kind: 'user' },
  }
}

function durableCandidate(filler = 97): HostUserMessage {
  return userMessage(`Please remember this durable architecture rationale: ${'x'.repeat(filler)}`)
}

function fixture(config = resolveConfig({ cliPath: '/fake/mnemon' }), options: { taskModelRoute?: boolean } = {}) {
  const agentListeners = new Map<string, Listener>()
  const rootListeners = new Map<string, Listener>()
  const agentSections: Array<{ name: string; order: number; text: () => string }> = []
  const agentContexts: Array<{ name: string; order: number; text: () => string }> = []
  const promptAssemblies: Array<{ sections: Array<{ name: string; text: string }>; contexts: Array<{ name: string; text: string }> }> = []
  const events: HostSessionEvent[] = []
  const followup = vi.fn()
  const steer = vi.fn()
  const taskAgents: HostAgent[] = []
  const disposedTaskAgents: string[] = []
  const taskModelRoute = options.taskModelRoute !== false
  const defaultModel = { currentSelection: vi.fn(() => ({ provider: 'deepseek', model: 'deepseek-chat' })) }
  const llm = {
    listProviders: vi.fn(() => [
      { id: 'deepseek', name: 'DeepSeek' },
      { id: 'openai', name: 'OpenAI' },
    ]),
    listModels: vi.fn(async (provider: string): Promise<Array<{ id: string; name: string; description?: string; inputModalities?: readonly string[] }>> => provider === 'openai'
      ? [{ id: 'gpt-5', name: 'GPT-5', description: 'General reasoning' }]
      : [{ id: 'deepseek-chat', name: 'DeepSeek Chat' }]),
  }
  const agentPresets = {
    resolve: vi.fn(async () => ({ id: 'default' })),
    mount: vi.fn(async () => ({ id: 'default' })),
  }
  // DSH appends an admitted step batch to the Session log and its surface.
  const admit = (listener: Listener): Listener => async (...args) => {
    const decision = await listener(...args) as HostPreStepDecision
    if (decision.kind === 'enter') {
      for (const message of decision.messages) events.push({ type: 'user/message', seq: events.length, data: { ...message } })
    }
    return decision
  }
  const agentCtx = {
    on: vi.fn((name: string, listener: Listener) => {
      agentListeners.set(name, name === 'agent/pre-step' ? admit(listener) : listener)
      return () => agentListeners.delete(name)
    }),
    effect: vi.fn((callback: () => (() => unknown) | void) => {
      const cleanup = callback()
      return () => cleanup?.()
    }),
    get: vi.fn((name: string) => name === 'systemPrompt'
      ? {
          section: (value: { name: string; order: number; text: () => string }) => {
            agentSections.push(value)
            return () => undefined
          },
          context: (value: { name: string; order: number; text: () => string }) => {
            agentContexts.push(value)
            return () => undefined
          },
        }
      : undefined),
  } as unknown as HostAgentContext
  const agent = {
    id: 'session-1',
    status: 'idle',
    ...(taskModelRoute ? { options: { provider: 'deepseek', model: 'deepseek-chat' } } : {}),
    session: { ...sessionLog(events) },
    ctx: agentCtx,
    followup,
    steer,
    inject: vi.fn(),
  } satisfies HostAgent
  const coordinator = {
    recall: vi.fn(async (_agent, request) => ({ query: request.query, mode: 'smart', results: [] })),
    write: vi.fn(async () => ({ delegated: true, runId: 'write-child', provider: 'spawn', summary: 'No durable memory', action: 'skipped', memoryBodyIds: [] })),
    answer: vi.fn(async () => ({ answer: 'SQLite.', citations: [], delegation: { runId: 'answer-child', provider: 'spawn' } })),
    review: vi.fn(async () => ({ delegated: true, runId: 'review-child', provider: 'fork', summary: 'No durable change', action: 'skipped', memoryBodyIds: [] })),
    maintainMetadata: vi.fn(async () => ({ delegated: true, runId: 'metadata-child', provider: 'spawn', summary: 'updated', updates: [] })),
    archiveDocument: vi.fn(async () => ({ success: true, action: 'archived', document: { id: 'doc-1' } })),
    snapshot: vi.fn(() => ({ recalls: 0, writes: 0, answers: 0, reviews: 0, failures: 0 })),
  } as unknown as MnemonSubagentCoordinator
  const createTaskAgent = vi.fn(async (options: CreateHostAgentOptions) => {
    const taskCtx = {
      on: vi.fn(() => () => undefined),
      effect: vi.fn((callback: () => (() => unknown) | void) => {
        const cleanup = callback()
        return () => cleanup?.()
      }),
    } as unknown as HostAgentContext
    await options.setup?.(taskCtx)
    const taskAgent = {
      id: options.sessionId,
      status: 'idle' as const,
      ...(options.agentOptions === undefined ? {} : { options: options.agentOptions }),
      session: { header: options.meta ?? {}, ...sessionLog() },
      ctx: taskCtx,
      followup: vi.fn(),
      steer: vi.fn(),
      inject: vi.fn(),
    } satisfies HostAgent
    taskAgents.push(taskAgent)
    rootListeners.get('agent/created')?.({ agent: taskAgent })
    return {
      agent: taskAgent,
      dispose: vi.fn(async () => {
        disposedTaskAgents.push(taskAgent.id)
        const index = taskAgents.indexOf(taskAgent)
        if (index >= 0) taskAgents.splice(index, 1)
      }),
    }
  })
  const ctx = {
    agents: { get: (id: string) => id === agent.id ? agent : taskAgents.find(candidate => candidate.id === id), roots: () => [agent, ...taskAgents], create: createTaskAgent },
    get: vi.fn((name: string) => name === 'agentDefaultModel' && taskModelRoute
      ? defaultModel
      : name === 'agentPresets'
        ? agentPresets
        : name === 'llm'
          ? llm
          : undefined),
    on: vi.fn((name: string, listener: Listener) => {
      rootListeners.set(name, listener)
      return () => rootListeners.delete(name)
    }),
  } as unknown as HostContextShape
  const pinnedTurns = new Map<string, object>()
  const composableTurns = {
    activeTurn: vi.fn(() => [...pinnedTurns.values()].at(-1)),
    beginTurn: vi.fn(async (turnId: string, scope: object) => {
      const turn = turnId.slice(turnId.lastIndexOf(':') + 1)
      const context = { turnId, view: { id: `view-${turn}`, digest: `digest-${turn}`, runtimeGeneration: 'fixture-generation',
        strategyTypeId: config.memoryTopology.strategyId, strategyInstanceKey: 'strategy:fixture', createdAt: '2026-08-23T00:00:00.000Z',
        projection: [], readGrants: [], routes: [], actionOffers: [] }, scope, startedAt: '2026-08-23T00:00:00.000Z' }
      pinnedTurns.set(turnId, context)
      return context
    }),
    turn: vi.fn((turnId: string) => pinnedTurns.get(turnId)),
    memoryWake: vi.fn((viewId: string) => ({
      viewId,
      viewDigest: viewId.replace('view-', 'digest-'),
      text: `Pinned Wake ${viewId}`,
      sections: [{ layerId: 'runtime', mode: 'eager', text: `Pinned Wake ${viewId}` }],
    })),
    endTurn: vi.fn((turnId: string) => pinnedTurns.delete(turnId)),
  }
  const runtimeBinding = {
    forAgent: vi.fn((_agent: HostAgent) => ({ config, composableTurns, memoryComposition: { generation: () => ({ sourceInstances: () => [] }) } })),
    bindAgentRuntime: vi.fn((_agentId: string, _graph: unknown): (() => void) => () => undefined),
  }
  const runtimeSource = { ...runtimeBinding, executions: new MemoryExecutions(runtimeBinding as never) }
  const lifecycle = new MnemonLifecycle(ctx, coordinator, config, runtimeSource as never)
  const stop = lifecycle.start()

  const assemblePrompt = async (turn: number) => {
    const hasOpenTurn = events.some(event => event.type === 'turn/start' && event.data.turn === turn)
      && !events.some(event => event.type === 'turn/end' && event.data.turn === turn)
    if (!hasOpenTurn) events.push({ type: 'turn/start', data: { turn } })
    const listener = agentListeners.get('system-prompt/assemble')
    if (listener === undefined) throw new Error('system-prompt/assemble listener missing')
    const assembly = {
      sections: agentSections.map(section => ({ name: section.name, text: section.text() })),
      contexts: agentContexts.map(context => ({ name: context.name, text: context.text() })),
      tools: [],
      variables: {},
    }
    const assembled = await listener(assembly, { agent, signal: new AbortController().signal }, async () => assembly) as typeof assembly
    promptAssemblies.push(assembled)
    return assembled
  }
  const preStep = async (messages: HostUserMessage[], turn: number, step = 1): Promise<HostPreStepDecision> => {
    // Match the real DSH order: turn/start -> systemPrompt.assemble -> agent/pre-step.
    await assemblePrompt(turn)
    const listener = agentListeners.get('agent/pre-step')
    if (listener === undefined) throw new Error('pre-step listener missing')
    return await listener({ agent, messages, turn, step, signal: new AbortController().signal }, async () => ({ kind: 'enter', messages })) as HostPreStepDecision
  }
  const turnStopping = async (turn: number) => {
    const listener = agentListeners.get('agent/turn-stopping')
    if (listener === undefined) throw new Error('turn-stopping listener missing')
    await listener({ agent, turn, signal: new AbortController().signal })
    if (!events.some(event => event.type === 'turn/end' && event.data.turn === turn)) {
      const event = { type: 'turn/end', data: { turn } }
      events.push(event)
      agentListeners.get('session/event')?.(agent.session, event)
    }
  }
  return { agent, agentListeners, agentSections, agentContexts, promptAssemblies, events, followup, steer, lifecycle, coordinator, composableTurns, runtimeSource, createTaskAgent, disposedTaskAgents, defaultModel, llm, agentPresets, assemblePrompt, preStep, turnStopping, stop }
}

// The DSH-owned Session codec, resolved exactly as the installed host resolves it.
const requireDsh = createRequire(realpathSync(new URL('../node_modules/@deepseek-ai/dsh/package.json', import.meta.url)))

afterEach(() => vi.useRealTimers())

describe('Mnemon DSH lifecycle integration', () => {

  it('emits producer-owned guided context accepted by the published legacy Session migration', async () => {
    const value = fixture(resolveConfig({ recallMode: 'guided', writebackMode: 'guided' }))
    const decision = await value.preStep([userMessage()], 1)
    if (decision.kind !== 'enter') throw new Error('unexpected rejection')
    const messages = decision.messages.filter(message => message.source.kind === 'dsh-mnemon')
    expect(messages.map(message => message.source.form)).toEqual(['instructions', 'recall'])
    for (const message of messages) {
      expect(message.source).not.toHaveProperty('plugin')
      expect(() => assertReleasedPayloadSemantics({
        type: 'user/message', seq: 0, time: 0, surfaceOp: 'append',
        data: JSON.parse(JSON.stringify(message)),
      }, 0)).not.toThrow()
    }
    value.stop()
  })

  it('persists guided context through the published V4 codec that rejects plugin wrappers', async () => {
    const sessionApi = await import(requireDsh.resolve('@deepseek-ai/dsh-session'))
    const format = await import(requireDsh.resolve('@deepseek-ai/dsh-session-format-v3-to-v4'))
    const sessionId = sessionApi.SessionId('mnemon-source-contract-fixture')
    const session = sessionApi.Session.create(sessionId, undefined, {
      version: 4, id: sessionId, createdAt: 0, isSeeded: false, delegationDepth: 0,
    })
    const value = fixture()
    const decision = await value.preStep([userMessage()], 1)
    if (decision.kind !== 'enter') throw new Error('unexpected rejection')
    const messages = decision.messages.slice(1)
    expect(messages.map(message => message.source.form)).toEqual(['instructions', 'recall'])
    for (const message of messages) {
      const event = session.append('user/message', message, { surfaceOp: 'append' })
      expect(format.releasedV4SessionFormatCodec.encodeEvent(event).data.source).toEqual(message.source)
    }
    const artifact = { header: session.header, inheritedEventCount: 0, events: session.snapshotEvents() }
    expect(() => format.restoreReleasedV4Artifact(artifact, sessionApi.KNOWN_SESSION_EVENT_TYPES)).not.toThrow()
    const oldWrapper = { ...artifact.events[0], data: { ...messages[0], source: { kind: 'plugin', plugin: 'dsh-mnemon', form: 'instructions' } } }
    expect(() => format.restoreReleasedV4Artifact({ ...artifact, events: [oldWrapper] }, sessionApi.KNOWN_SESSION_EVENT_TYPES))
      .toThrow('format v4 message requires a producer-owned source kind')
    value.stop()
  })

  const ownSources = [
    { kind: 'dsh-mnemon', form: 'instructions' },
    { kind: 'plugin:dsh-mnemon', form: 'instructions' },
    { kind: 'plugin', plugin: 'dsh-mnemon', form: 'instructions' },
  ]

  it.each(ownSources)('does not reinject context into a request already owned by Mnemon: %j', async source => {
    const value = fixture()
    const own = { ...userMessage('Previously queued Mnemon context'), source }
    expect(await value.preStep([own], 1)).toEqual({ kind: 'enter', messages: [own] })
    value.stop()
  })

  it.each(ownSources)('recognizes current, migrated and legacy attribution on the durable surface: %j', async source => {
    const value = fixture()
    value.events.push({ type: 'user/message', seq: 0, data: { ...userMessage('Retained Mnemon reminder'), source } })
    ;(value.agent.session as { surface?: { nodes: readonly number[] } }).surface = { nodes: [0] }
    const decision = await value.preStep([userMessage()], 1)
    if (decision.kind !== 'enter') throw new Error('unexpected rejection')
    expect(decision.messages).toHaveLength(2)
    expect(decision.messages.some(message => message.source.form === 'instructions')).toBe(false)
    expect(decision.messages.at(-1)?.source.form).toBe('recall')
    value.stop()
  })

  it.each([
    { kind: 'plugin', plugin: 'other-plugin' },
    { kind: 'plugin:dsh-mnemon-other' },
    { kind: 'other-plugin', plugin: 'dsh-mnemon' },
  ])('does not let another producer suppress Mnemon context: %j', async source => {
    const value = fixture()
    const other = { ...userMessage('Unrelated producer context'), source }
    value.events.push({ type: 'user/message', seq: 0, data: other })
    ;(value.agent.session as { surface?: { nodes: readonly number[] } }).surface = { nodes: [0] }
    const decision = await value.preStep([other], 1)
    if (decision.kind !== 'enter') throw new Error('unexpected rejection')
    expect(decision.messages[0]).toEqual(other)
    expect(decision.messages.slice(1).map(message => message.source)).toEqual([
      { kind: 'dsh-mnemon', form: 'instructions' }, { kind: 'dsh-mnemon', form: 'recall' },
    ])
    value.stop()
  })

  it('appends a literal View snapshot after the complete shared-context batch without repeating it', async () => {
    const value = fixture()
    const text = 'Keep {{model}}, $HOME and <runtime-context> as quoted memory text.'
    value.composableTurns.memoryWake.mockImplementation(viewId => ({ viewId, viewDigest: 'stable', text, sections: [] }))
    await value.assemblePrompt(1)
    const user = userMessage('Continue')
    const shared = { ...userMessage('Other plugin context'), source: { kind: 'plugin' as const, plugin: 'other-plugin' } }
    const decision = await value.agentListeners.get('agent/pre-step')!({
      agent: value.agent, messages: [user], turn: 1, step: 1, signal: new AbortController().signal,
    }, async () => ({ kind: 'enter', messages: [user, shared] })) as HostPreStepDecision
    expect(value.agent.ctx.on).toHaveBeenCalledWith('agent/pre-step', expect.any(Function), { prepend: true })
    expect(decision.kind === 'enter' && decision.messages.slice(0, 2)).toEqual([user, shared])
    expect(decision.kind === 'enter' && decision.messages.at(-1)).toMatchObject({
      content: [{ type: 'text', text }],
      source: { kind: 'dsh-mnemon', form: 'recall' },
    })
    if (decision.kind === 'enter') expect(decision.messages.at(-1)?.source).not.toHaveProperty('summary')
    await value.turnStopping(1)
    const next = await value.preStep([user], 2)
    expect(next.kind === 'enter' && next.messages).toEqual([user])
    value.stop()
  })

  it('re-composes the guided reminder after a rewind drops it from the surface', async () => {
    const value = fixture()
    const forms = (decision: HostPreStepDecision) => decision.kind === 'enter' ? decision.messages.map(message => message.source.form) : []
    expect(forms(await value.preStep([userMessage()], 1))).toEqual([undefined, 'instructions', 'recall'])

    // Still visible: a later first step must not duplicate it. The snapshot
    // rides along because this fixture's Wake text is turn-derived.
    expect(forms(await value.preStep([userMessage()], 2))).toEqual([undefined, 'recall'])

    // Rewind replaces the surface and drops the reminder. The append-only event
    // log still contains it, which is why presence cannot be read from there.
    value.agent.session.surface = { nodes: [] }
    expect(forms(await value.preStep([userMessage()], 3))).toEqual([undefined, 'instructions', 'recall'])
  })

  it('pins one immutable Wake across every model step and releases it at the turn boundary', async () => {
    const value = fixture()
    // The snapshot is no longer a shared runtime-context contribution.
    expect(value.agentContexts).toHaveLength(0)

    const first = await value.preStep([userMessage('First step')], 7, 1)
    expect(value.promptAssemblies[0]?.contexts).toEqual([])
    expect(first.kind === 'enter' && first.messages.at(-1)?.content).toEqual([{ type: 'text', text: 'Pinned Wake view-7' }])
    expect(value.composableTurns.beginTurn).toHaveBeenCalledOnce()
    expect(value.runtimeSource.bindAgentRuntime).toHaveBeenCalledOnce()
    expect(value.composableTurns.beginTurn).toHaveBeenCalledWith('session-1:7', {
      storage: 'global',
      sessionId: 'session-1',
      agentId: 'session-1',
    }, 'agent.root-turn', expect.any(AbortSignal))

    const continuation = await value.preStep([userMessage('Tool continuation')], 7, 2)
    expect(continuation.kind === 'enter' && continuation.messages).toHaveLength(1)
    expect(value.composableTurns.beginTurn).toHaveBeenCalledOnce()

    await value.turnStopping(7)
    expect(value.composableTurns.endTurn).toHaveBeenCalledWith('session-1:7')
    expect(value.composableTurns.turn('session-1:7')).toBeUndefined()

    const next = await value.preStep([userMessage('Next turn')], 8, 1)
    expect(next.kind === 'enter' && next.messages.at(-1)?.content).toEqual([{ type: 'text', text: 'Pinned Wake view-8' }])
    expect(value.composableTurns.beginTurn).toHaveBeenCalledTimes(2)
  })

  it('offers an active root Agent to standalone WebUI maintenance when no session is selected', () => {
    const value = fixture()

    expect(value.lifecycle.snapshot()).toMatchObject({
      activeAgents: 1,
      sessionAvailable: true,
      taskAgentAvailable: true,
      current: { sessionId: 'session-1' },
    })
    expect(value.lifecycle.snapshot('missing-session')).toMatchObject({
      sessionAvailable: true,
      current: { sessionId: 'session-1' },
    })
  })

  it('does not advertise task Agent actions when creation has no usable model route', async () => {
    const value = fixture(resolveConfig({ cliPath: '/fake/mnemon' }), { taskModelRoute: false })

    expect(value.lifecycle.snapshot()).toMatchObject({ sessionAvailable: true, taskAgentAvailable: false })
    await expect(value.lifecycle.maintainMetadata('', ['project'], '/tmp/workspace-two')).rejects.toThrow('no default provider/model')
    expect(value.createTaskAgent).not.toHaveBeenCalled()
  })

  it('runs standalone maintenance under a disposable clean root Agent scoped to the selected workspace', async () => {
    const value = fixture()

    await value.lifecycle.maintainMetadata('', ['project'], '/tmp/workspace-two')
    await value.lifecycle.archiveDocument('', 'doc-1', '/tmp/workspace-two')

    expect(value.createTaskAgent).toHaveBeenCalledTimes(2)
    expect(value.createTaskAgent).toHaveBeenNthCalledWith(1, expect.objectContaining({
      sessionId: expect.any(String),
      meta: { cwd: '/tmp/workspace-two', agentPreset: 'default' },
      agentOptions: { provider: 'deepseek', model: 'deepseek-chat' },
      setup: expect.any(Function),
      signal: expect.any(AbortSignal),
    }))
    const metadataAgent = vi.mocked(value.coordinator.maintainMetadata).mock.calls[0]?.[0] as HostAgent
    expect(metadataAgent).not.toBe(value.agent)
    expect(metadataAgent.session.header?.cwd).toBe('/tmp/workspace-two')
    expect(value.coordinator.archiveDocument).toHaveBeenCalledWith(expect.objectContaining({ session: expect.objectContaining({ header: { cwd: '/tmp/workspace-two', agentPreset: 'default' } }) }), 'doc-1', expect.any(AbortSignal))
    expect(value.defaultModel.currentSelection).toHaveBeenCalledTimes(2)
    expect(value.agentPresets.resolve).toHaveBeenCalledTimes(2)
    expect(value.agentPresets.mount).toHaveBeenCalledTimes(2)
    expect(value.disposedTaskAgents).toHaveLength(2)
    expect(value.lifecycle.snapshot()).toMatchObject({ activeAgents: 1, taskAgentAvailable: true })
  })

  it.each([false, true])('runs Runtime maintenance with clean workspace ownership and disposes it on failure=%s', async failed => {
    const value = fixture()
    const operation = vi.fn(async (agent: HostAgent) => {
      expect(agent).not.toBe(value.agent)
      expect(agent.session.header?.cwd).toBe('/tmp/workspace-two')
      expect(agent.session.snapshotEvents()).toEqual([])
      if (failed) throw new Error('model unavailable')
      return 'maintained'
    })
    const result = value.lifecycle.runRuntimeMaintenanceTask({ storage: 'workspace', workspaceId: '/tmp/workspace-two', sessionId: 'unrelated-session' }, new AbortController().signal, operation)
    if (failed) await expect(result).rejects.toThrow('model unavailable')
    else await expect(result).resolves.toBe('maintained')
    expect(value.createTaskAgent).toHaveBeenCalledOnce()
    expect(value.disposedTaskAgents).toHaveLength(1)
  })

  it('uses a fixed Provider and model for independent task Agents when configured', async () => {
    const value = fixture(resolveConfig({
      cliPath: '/fake/mnemon',
      taskAgentModel: { mode: 'fixed', provider: 'openai', model: 'gpt-5' },
    }))

    await value.lifecycle.maintainMetadata('', ['project'], '/tmp/workspace-two')

    expect(value.createTaskAgent).toHaveBeenCalledWith(expect.objectContaining({
      agentOptions: { provider: 'openai', model: 'gpt-5', maxTokens: undefined },
    }))
    expect(value.defaultModel.currentSelection).not.toHaveBeenCalled()
  })

  it('lists model Providers concurrently and reports the effective independent task route', async () => {
    const value = fixture()

    await expect(value.lifecycle.taskAgentModels()).resolves.toEqual({
      effective: { provider: 'deepseek', model: 'deepseek-chat', source: 'dsh-default' },
      defaultSelection: { provider: 'deepseek', model: 'deepseek-chat' },
      groups: [
        { id: 'deepseek', name: 'DeepSeek', models: [{ id: 'deepseek-chat', name: 'DeepSeek Chat' }] },
        { id: 'openai', name: 'OpenAI', models: [{ id: 'gpt-5', name: 'GPT-5', description: 'General reasoning' }] },
      ],
      failures: [],
    })
    expect(value.llm.listModels).toHaveBeenCalledTimes(2)
  })

  it('preserves first-party model modality metadata in the task Agent catalog', async () => {
    const value = fixture()
    value.llm.listProviders.mockReturnValue([{ id: 'deepseek-official', name: 'DeepSeek' }])
    value.llm.listModels.mockResolvedValue([{
      id: 'deepseek-v4-flash-vision-exp',
      name: 'DeepSeek-V4-Flash-Vision-Exp',
      inputModalities: ['text', 'image'],
    }])

    await expect(value.lifecycle.taskAgentModels()).resolves.toMatchObject({
      groups: [{
        id: 'deepseek-official',
        models: [{
          id: 'deepseek-v4-flash-vision-exp',
          inputModalities: ['text', 'image'],
        }],
      }],
    })
  })

  it('resolves the inherited task route without loading the full model directory', async () => {
    const value = fixture()

    await expect(value.lifecycle.taskAgentModels(false)).resolves.toEqual({
      effective: { provider: 'deepseek', model: 'deepseek-chat', source: 'dsh-default' },
      defaultSelection: { provider: 'deepseek', model: 'deepseek-chat' },
      groups: [],
      failures: [],
    })
    expect(value.llm.listProviders).not.toHaveBeenCalled()
    expect(value.llm.listModels).not.toHaveBeenCalled()
  })

  it('isolates a stalled Provider while keeping the rest of the model catalog usable', async () => {
    vi.useFakeTimers()
    const value = fixture()
    value.llm.listModels.mockImplementation(async provider => provider === 'openai'
      ? await new Promise<never>(() => {})
      : [{ id: 'deepseek-chat', name: 'DeepSeek Chat' }])

    const pending = value.lifecycle.taskAgentModels()
    await vi.advanceTimersByTimeAsync(3_000)

    await expect(pending).resolves.toMatchObject({
      groups: [{ id: 'deepseek', models: [{ id: 'deepseek-chat' }] }],
      failures: [{ id: 'openai', message: 'model directory timed out after 3 seconds' }],
    })
  })

  it('runs Agent Query synthesis under a disposable clean root Agent', async () => {
    const value = fixture()

    await value.lifecycle.answerTask('', 'Which database?', [], '/tmp/workspace-two')

    expect(value.createTaskAgent).toHaveBeenCalledWith(expect.objectContaining({
      meta: { cwd: '/tmp/workspace-two', agentPreset: 'default' },
      agentOptions: { provider: 'deepseek', model: 'deepseek-chat' },
    }))
    expect(value.coordinator.answer).toHaveBeenCalledWith(expect.objectContaining({
      options: { provider: 'deepseek', model: 'deepseek-chat' },
      session: expect.objectContaining({ header: expect.objectContaining({ cwd: '/tmp/workspace-two' }) }),
    }), 'Which database?', [], expect.any(AbortSignal))
    expect(value.disposedTaskAgents).toHaveLength(1)
  })

  it('adds one durable optional reminder without repeating it into later turn history', async () => {
    const value = fixture()
    const prompt = userMessage('Aster 发布前需要检查哪些事项？')
    const decision = await value.preStep([prompt], 1)

    expect(decision).toMatchObject({ kind: 'enter' })
    if (decision.kind !== 'enter') throw new Error('unexpected rejection')
    expect(decision.messages).toHaveLength(3)
    expect(decision.messages[1]?.source).toMatchObject({ kind: 'dsh-mnemon', form: 'instructions' })
    expect(decision.messages[1]?.content[0]?.text).toBe('[MNEMON] Use mnemon_view_route only when relevant evidence is missing. Use mnemon_view_action only for an intended memory change; require a write receipt. Use only ids offered by the current View and follow each Source\'s semantics. Otherwise use none.')
    expect(value.coordinator.recall).not.toHaveBeenCalled()

    const second = await value.preStep([userMessage('Second turn')], 2)
    if (second.kind !== 'enter') throw new Error('unexpected rejection')
    // The reminder is not repeated. The snapshot is present because this
    // fixture derives Wake text from the turn, so turn 2 is a new revision.
    expect(second.messages).toHaveLength(2)
    expect(second.messages.some(message => (message.source as { form?: string }).form === 'instructions')).toBe(false)
    expect(second.messages.at(-1)?.source).toMatchObject({ kind: 'dsh-mnemon', form: 'recall' })
    expect(value.coordinator.recall).not.toHaveBeenCalled()
    expect(value.lifecycle.snapshot('session-1').counters).toMatchObject({ primes: 1, recallCues: 1, writebackCues: 1 })
  })

  it('keeps durable image references intact while scoring only text from a multimodal prompt', async () => {
    const value = fixture()
    const prompt: HostUserMessage = {
      id: crypto.randomUUID(),
      role: 'user',
      content: [
        {
          type: 'image',
          attachment: {
            attachmentId: 'image:sha256:fixture',
            mediaType: 'image/png',
            bytes: 128,
            width: 16,
            height: 8,
            name: 'architecture.png',
          },
        },
        { type: 'text', text: 'Inspect this diagram.' },
      ],
      source: { kind: 'user' },
    }

    const decision = await value.preStep([prompt], 1)
    await value.turnStopping(1)

    if (decision.kind !== 'enter') throw new Error('unexpected rejection')
    expect(decision.messages[0]).toBe(prompt)
    expect(decision.messages[0]?.content[0]).toEqual(prompt.content[0])
    expect(value.lifecycle.snapshot('session-1').current?.reviewActivity).toMatchObject({
      totalUserTextLength: 'Inspect this diagram.'.length,
      turnCount: 1,
    })
  })

  it('waits for the QoderWork score threshold, then debounces a full-checkpoint review', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    await value.preStep([durableCandidate()], 1)
    value.events.push({ type: 'turn/end', data: { turn: 1 } })
    await value.turnStopping(1)

    expect(value.coordinator.review).not.toHaveBeenCalled()
    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
      idleReviewPending: false,
      reviewActivity: { score: 4, threshold: 5, eligible: false, totalUserTextLength: 150, turnCount: 1 },
    })

    await value.preStep([userMessage('one more turn')], 2)
    value.events.push({ type: 'turn/end', data: { turn: 2 } })
    await value.turnStopping(2)

    expect(value.coordinator.review).not.toHaveBeenCalled()
    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
      idleReviewPending: true,
      reviewRunning: false,
      reviewActivity: { score: 5, threshold: 5, eligible: true, turnCount: 2 },
    })
    await vi.advanceTimersByTimeAsync(4_999)
    expect(value.coordinator.review).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(value.coordinator.review).toHaveBeenCalledWith(value.agent, expect.any(AbortSignal))
    expect(value.coordinator.write).not.toHaveBeenCalled()
    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
      idleReviewPending: false,
      lastReviewAction: 'skipped',
      lastReviewScore: 5,
      reviewActivity: { score: 0, eligible: false, turnCount: 0 },
    })
  })

  it('bounds idle review attempts across a long session, including failed runs (issues 100/255)', async () => {
    vi.useFakeTimers()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const value = fixture({ ...resolveConfig({ idleReviewMs: 5_000 }), idleReview: { enabled: true, provider: 'spawn', minIntervalMs: 300_000, maxPerSession: 2 } } as never)
    vi.mocked(value.coordinator.review).mockRejectedValue(new Error('TeamError: reviewer failed'))
    try {
      for (let turn = 1; turn <= 30; turn += 1) {
        await value.preStep([durableCandidate(300)], turn)
        await value.turnStopping(turn)
        await vi.advanceTimersByTimeAsync(5_000)
      }
      expect(value.coordinator.review).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(300_000)
      expect(value.coordinator.review).toHaveBeenCalledTimes(2)
      await value.preStep([durableCandidate(300)], 31)
      await value.turnStopping(31)
      await vi.advanceTimersByTimeAsync(600_000)
      expect(value.coordinator.review).toHaveBeenCalledTimes(2)
    } finally { value.stop(); warn.mockRestore() }
  })

  it('caps successful review children even when every later checkpoint is eligible (issue 100)', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000, idleReview: { minIntervalMs: 5_000, maxPerSession: 2 } }))
    try {
      for (let turn = 1; turn <= 30; turn += 1) {
        await value.preStep([durableCandidate(300)], turn); await value.turnStopping(turn)
        await vi.advanceTimersByTimeAsync(5_000)
      }
      expect(value.coordinator.review).toHaveBeenCalledTimes(2)
      expect(value.lifecycle.snapshot('session-1').current).toMatchObject({ idleReviewAttempts: 2, idleReviewPending: false })
      await value.preStep([durableCandidate(300)], 31); await value.turnStopping(31)
      await vi.advanceTimersByTimeAsync(300_000)
      expect(value.coordinator.review).toHaveBeenCalledTimes(2)
    } finally { value.stop() }
  })

  it('pauses review before creating a child while Team tools are installed and resumes after removal', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    let teamTools = true
    const get = value.agent.ctx.get!
    value.agent.ctx.get = name => name === 'agentTeams' ? {} : get(name)
    value.agent.ctx.tools = { get: name => teamTools && name === 'spawn_teammate' ? {} : undefined }
    try {
      for (let turn = 1; turn <= 3; turn += 1) { await value.preStep([durableCandidate(300)], turn); await value.turnStopping(turn) }
      await vi.advanceTimersByTimeAsync(600_000)
      expect(value.coordinator.review).not.toHaveBeenCalled()
      expect(value.lifecycle.snapshot('session-1').current).toMatchObject({ idleReviewBlocked: 'agent-team', idleReviewAttempts: 0, idleReviewPending: false })
      teamTools = false
      await value.preStep([durableCandidate(300)], 4)
      await value.turnStopping(4)
      await vi.advanceTimersByTimeAsync(5_000)
      expect(value.coordinator.review).toHaveBeenCalledOnce()
    } finally { value.stop() }
  })

  it('allows explicitly scoped Team review and honors switching back to pause (issue 275)', async () => {
    vi.useFakeTimers()
    const config = resolveConfig({ idleReviewMs: 5_000, idleReview: { agentTeams: 'scoped', minIntervalMs: 5_000 } } as never)
    const value = fixture(config)
    const get = value.agent.ctx.get!
    value.agent.ctx.get = name => name === 'agentTeams' ? {} : get(name)
    value.agent.ctx.tools = { get: name => name === 'spawn_teammate' ? {} : undefined }
    try {
      for (let turn = 1; turn <= 2; turn += 1) { await value.preStep([durableCandidate(300)], turn); await value.turnStopping(turn) }
      await vi.advanceTimersByTimeAsync(5_000)
      expect(value.coordinator.review).toHaveBeenCalledOnce()
      expect(value.lifecycle.snapshot('session-1').current?.idleReviewBlocked).toBeUndefined()
      for (let turn = 3; turn <= 4; turn += 1) { await value.preStep([durableCandidate(300)], turn); await value.turnStopping(turn) }
      Object.assign(config.idleReview, { agentTeams: 'pause' })
      await vi.advanceTimersByTimeAsync(5_000)
      expect(value.coordinator.review).toHaveBeenCalledOnce()
      expect(value.lifecycle.snapshot('session-1').current).toMatchObject({ idleReviewBlocked: 'agent-team', idleReviewAttempts: 1 })
    } finally { value.stop() }
  })

  it('disables only idle review and cancels an already scheduled review when toggled off', async () => {
    vi.useFakeTimers()
    const config = resolveConfig({ idleReviewMs: 5_000 })
    const value = fixture(config)
    try {
      await value.preStep([durableCandidate(300)], 1); await value.turnStopping(1)
      await value.preStep([durableCandidate(300)], 2); await value.turnStopping(2)
      config.idleReview.enabled = false
      await vi.advanceTimersByTimeAsync(5_000)
      expect(value.coordinator.review).not.toHaveBeenCalled()
      expect(value.lifecycle.snapshot('session-1').counters).toMatchObject({ recallCues: 1, writebackCues: 1 })
      await value.lifecycle.supervise('session-1', 'Explicit durable fact')
      expect(value.coordinator.write).toHaveBeenCalledOnce()
    } finally { value.stop() }
  })

  it('surfaces committed receipts without counting an incomplete review as success', async () => {
    vi.useFakeTimers()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    vi.mocked(value.coordinator.review).mockRejectedValueOnce(new IdleReviewError('TeamError: after commit', {
      status: 'partial', runId: 'review-run', provider: 'fork', receipts: [{ tool: 'mnemon_document_create', action: 'created', documentId: 'committed-document' }],
    }))
    try {
      await value.preStep([durableCandidate(300)], 1); await value.turnStopping(1)
      await value.preStep([durableCandidate(300)], 2); await value.turnStopping(2)
      await vi.advanceTimersByTimeAsync(5_000)
      expect(value.lifecycle.snapshot('session-1').current).toMatchObject({ lastPhase: 'error', idleReviewAttempts: 1,
        lastReviewFailure: { status: 'partial', runId: 'review-run', receipts: [{ documentId: 'committed-document' }] } })
      expect(value.lifecycle.snapshot('session-1').counters.failures).toBe(1)
      expect(value.lifecycle.snapshot('session-1').current?.lastReviewAction).toBeUndefined()
    } finally { value.stop(); warn.mockRestore() }
  })

  it('reports idle-review failures and retains the warning until a successful review', async () => {
    vi.useFakeTimers()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    vi.mocked(value.coordinator.review).mockRejectedValueOnce(new Error('CONTEXT_WINDOW_EXCEEDED: request (145508 tokens) exceeds the available context size (98304 tokens), sk-secret123456'))
    try {
      await value.preStep([durableCandidate()], 1)
      await value.turnStopping(1)
      await value.preStep([userMessage('threshold turn')], 2)
      await value.turnStopping(2)
      await vi.advanceTimersByTimeAsync(5_000)

      expect(warn).toHaveBeenCalledOnce()
      expect(warn).toHaveBeenCalledWith(expect.stringContaining('idle review failed'))
      expect(warn).toHaveBeenCalledWith(expect.stringContaining('[redacted]'))
      expect(value.lifecycle.snapshot('session-1').current).toMatchObject({ lastPhase: 'error', reviewRunning: false, lastError: expect.stringContaining('CONTEXT_WINDOW_EXCEEDED') })
      expect(value.lifecycle.snapshot('session-1').current?.reviewActivity.turnCount).toBe(2)
      expect(value.followup).not.toHaveBeenCalled()
      expect(value.steer).not.toHaveBeenCalled()

      await value.preStep([userMessage('Continue the task')], 3)
      expect(value.lifecycle.snapshot('session-1').current?.lastError).toContain('CONTEXT_WINDOW_EXCEEDED')
      await value.turnStopping(3)
      await vi.advanceTimersByTimeAsync(300_000)
      expect(value.lifecycle.snapshot('session-1').current?.lastError).toBeUndefined()
      expect(warn).toHaveBeenCalledOnce()
    } finally {
      value.stop()
      warn.mockRestore()
    }
  })

  it('keeps an aborted review quiet when a new parent turn cancels it', async () => {
    vi.useFakeTimers()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    vi.mocked(value.coordinator.review).mockImplementationOnce(async (_parent, signal) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new Error('cancelled review')), { once: true })
    }))
    try {
      await value.preStep([durableCandidate()], 1)
      await value.turnStopping(1)
      await value.preStep([userMessage('threshold turn')], 2)
      await value.turnStopping(2)
      await vi.advanceTimersByTimeAsync(5_000)
      await value.preStep([userMessage('Resume the parent task')], 3)
      expect(warn).not.toHaveBeenCalled()
      expect(value.lifecycle.snapshot('session-1').counters.failures).toBe(0)
      expect(value.lifecycle.snapshot('session-1').current?.lastError).toBeUndefined()
    } finally {
      value.stop()
      warn.mockRestore()
    }
  })

  it('does not start background work when activity has no deterministic dirty candidate', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    await value.preStep([userMessage('x'.repeat(150))], 1)
    value.events.push({ type: 'turn/end', data: { turn: 1 } })
    await value.turnStopping(1)
    await value.preStep([userMessage('one more ordinary turn')], 2)
    value.events.push({ type: 'turn/end', data: { turn: 2 } })
    await value.turnStopping(2)

    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
      idleReviewPending: false,
      reviewActivity: { score: 5, eligible: true },
    })
    await vi.advanceTimersByTimeAsync(5_000)
    expect(value.coordinator.review).not.toHaveBeenCalled()
  })

  it('admits a substantial completed assistant artifact without another classifier call', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    await value.preStep([userMessage('x'.repeat(150))], 1)
    value.events.push({
      type: 'assistant/message',
      data: {
        turn: 1,
        message: { content: [{ type: 'text', text: 'Reusable architecture artifact. '.repeat(24) }] },
      },
    })
    value.events.push({ type: 'turn/end', data: { turn: 1 } })
    await value.turnStopping(1)
    await value.preStep([userMessage('one more ordinary turn')], 2)
    value.events.push({ type: 'turn/end', data: { turn: 2 } })
    await value.turnStopping(2)

    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
      idleReviewPending: true,
      reviewActivity: { score: 5, eligible: true },
    })
  })

  it('honors explicit no-memory maintenance intent before starting a review child', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    await value.preStep([userMessage(`${'x'.repeat(150)}，不要主动写记忆。`)], 1)
    value.events.push({ type: 'turn/end', data: { turn: 1 } })
    await value.turnStopping(1)
    await value.preStep([userMessage('请简短确认，但仍不要主动写记忆。')], 2)
    value.events.push({ type: 'turn/end', data: { turn: 2 } })
    await value.turnStopping(2)

    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
      idleReviewPending: false,
      reviewActivity: { score: 5, eligible: true },
    })
    await vi.advanceTimersByTimeAsync(5_000)
    expect(value.coordinator.review).not.toHaveBeenCalled()
  })

  it('cancels a pending idle review when a new turn begins', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    await value.preStep([durableCandidate()], 1)
    value.events.push({ type: 'turn/end', data: { turn: 1 } })
    await value.turnStopping(1)
    await value.preStep([userMessage('threshold turn')], 2)
    value.events.push({ type: 'turn/end', data: { turn: 2 } })
    await value.turnStopping(2)
    await vi.advanceTimersByTimeAsync(4_000)
    await value.preStep([userMessage('A new turn arrived')], 3)
    await vi.advanceTimersByTimeAsync(2_000)

    expect(value.coordinator.review).not.toHaveBeenCalled()
    expect(value.lifecycle.snapshot('session-1').current?.idleReviewPending).toBe(false)
    expect(value.lifecycle.snapshot('session-1').current?.reviewActivity).toMatchObject({ score: 6, turnCount: 3 })
  })

  it('cancels delayed review work when a one-shot Host disposes the plugin', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    await value.preStep([durableCandidate()], 1)
    value.events.push({ type: 'turn/end', data: { turn: 1 } })
    await value.turnStopping(1)
    await value.preStep([userMessage('threshold turn')], 2)
    value.events.push({ type: 'turn/end', data: { turn: 2 } })
    await value.turnStopping(2)
    expect(value.lifecycle.snapshot('session-1').current?.idleReviewPending).toBe(true)

    value.stop()
    await vi.advanceTimersByTimeAsync(5_000)

    expect(value.coordinator.review).not.toHaveBeenCalled()
    expect(value.lifecycle.snapshot('session-1').activeAgents).toBe(0)
  })

  it('counts completed tool results and unique tool names with QoderWork weights', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    await value.preStep([userMessage('x'.repeat(100))], 1)
    const names = ['read', 'read', 'write', 'search', 'read']
    names.forEach((name, index) => {
      value.events.push({ type: 'tool/call', data: { turn: 1, step: 1, callId: `call-${index}`, name } })
      value.events.push({ type: 'tool/result', data: { turn: 1, step: 1, message: { source: { callId: `call-${index}` } } } })
    })
    value.events.push({ type: 'turn/end', data: { turn: 1 } })
    await value.turnStopping(1)

    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
      idleReviewPending: true,
      reviewActivity: {
        totalUserTextLength: 100,
        turnCount: 1,
        toolCallCount: 5,
        uniqueToolCount: 3,
        textLengthScore: 2,
        turnScore: 1,
        toolCallScore: 1,
        toolDiversityScore: 1,
        score: 5,
      },
    })
  })

  it('does not double-score repeated stopping notifications for the same turn', async () => {
    const value = fixture()
    await value.preStep([userMessage('short')], 1)
    await value.turnStopping(1)
    await value.turnStopping(1)

    expect(value.lifecycle.snapshot('session-1').current?.reviewActivity).toMatchObject({
      totalUserTextLength: 5,
      turnCount: 1,
      score: 1,
    })
  })

  it('deduplicates the same entered user message across multiple steps', async () => {
    const value = fixture()
    const prompt = userMessage('x'.repeat(50))
    await value.preStep([prompt], 1, 1)
    await value.preStep([prompt], 1, 2)
    await value.turnStopping(1)

    expect(value.lifecycle.snapshot('session-1').current?.reviewActivity).toMatchObject({
      totalUserTextLength: 50,
      turnCount: 1,
      textLengthScore: 1,
      turnScore: 1,
      score: 2,
    })
  })

  it('keeps the activity watermark when a new turn aborts an in-flight review', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    vi.mocked(value.coordinator.review).mockImplementationOnce(async (_parent, signal) => await new Promise(resolve => {
      const finish = () => resolve({
        delegated: true,
        runId: 'review-child',
        provider: 'fork',
        summary: 'Stale completion',
        action: 'skipped',
        memoryBodyIds: [],
      })
      if (signal.aborted) finish()
      else signal.addEventListener('abort', finish, { once: true })
    }))

    await value.preStep([durableCandidate()], 1)
    value.events.push({ type: 'turn/end', data: { turn: 1 } })
    await value.turnStopping(1)
    await value.preStep([userMessage('threshold turn')], 2)
    value.events.push({ type: 'turn/end', data: { turn: 2 } })
    await value.turnStopping(2)
    await vi.advanceTimersByTimeAsync(5_000)
    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({ reviewRunning: true })

    await value.preStep([userMessage('new evidence')], 3)
    await vi.advanceTimersByTimeAsync(0)

    expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
      reviewRunning: false,
      idleReviewPending: false,
      reviewActivity: { score: 6, turnCount: 3, eligible: true },
    })
    expect(value.lifecycle.snapshot('session-1').current?.lastReviewAt).toBeUndefined()
  })

  it('settles an aborted idle review before a new turn pins the same Agent runtime', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    vi.mocked(value.coordinator.review).mockImplementationOnce(async (parent, signal) => {
      const execution = await value.runtimeSource.executions.workflow(parent, 'review', signal)
      try {
        await new Promise<void>(resolve => {
          if (signal.aborted) resolve()
          else signal.addEventListener('abort', () => resolve(), { once: true })
        })
        return {
          delegated: true,
          runId: 'review-child',
          provider: 'fork',
          summary: 'Aborted stale review',
          action: 'skipped',
          memoryBodyIds: [],
          documentIds: [],
        }
      } finally {
        execution.release()
      }
    })

    try {
      await value.preStep([durableCandidate()], 1)
      await value.turnStopping(1)
      await value.preStep([userMessage('threshold turn')], 2)
      await value.turnStopping(2)
      await vi.advanceTimersByTimeAsync(5_000)
      expect(value.lifecycle.snapshot('session-1').current).toMatchObject({ reviewRunning: true })

      await expect(value.preStep([userMessage('new evidence')], 3)).resolves.toMatchObject({ kind: 'enter' })
      expect(value.lifecycle.snapshot('session-1').current).toMatchObject({
        reviewRunning: false,
        idleReviewPending: false,
      })
      expect(value.composableTurns.activeTurn()).toMatchObject({ turnId: 'session-1:3' })
    } finally {
      value.stop()
      await vi.advanceTimersByTimeAsync(0)
    }
  })

  it('can cue recall and remember independently', async () => {
    const recallOnly = fixture(resolveConfig({ recallMode: 'guided', writebackMode: 'off' }))
    const recallDecision = await recallOnly.preStep([userMessage()], 1)
    if (recallDecision.kind !== 'enter') throw new Error('unexpected rejection')
    expect(recallDecision.messages[1]?.content[0]?.text).toContain('mnemon_view_route')
    expect(recallDecision.messages[1]?.content[0]?.text).not.toContain('mnemon_view_action')

    const rememberOnly = fixture(resolveConfig({ recallMode: 'off', writebackMode: 'guided' }))
    const rememberDecision = await rememberOnly.preStep([userMessage()], 1)
    if (rememberDecision.kind !== 'enter') throw new Error('unexpected rejection')
    expect(rememberDecision.messages[1]?.content[0]?.text).toContain('mnemon_view_action')
    expect(rememberDecision.messages[1]?.content[0]?.text).not.toContain('mnemon_view_route')
  })

  it('does not send default Source guidance or run three-tier review under a custom Strategy', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000, memoryTopology: { strategyId: 'personal-notes' } }))
    try {
      const decision = await value.preStep([durableCandidate()], 1)
      if (decision.kind !== 'enter') throw new Error('unexpected rejection')
      const reminder = decision.messages[1]?.content[0]?.text
      expect(reminder).toContain('current View')
      expect(reminder).not.toMatch(/Documents|Memory Spaces|mnemon_recall|mnemon_runtime_memory/)
      await value.turnStopping(1)
      await value.preStep([userMessage('one more substantive turn')], 2)
      await value.turnStopping(2)
      expect(value.lifecycle.snapshot('session-1').current?.reviewActivity.eligible).toBe(true)
      expect(value.lifecycle.snapshot('session-1').current?.idleReviewPending).toBe(false)
      await vi.advanceTimersByTimeAsync(10_000)
      expect(value.coordinator.review).not.toHaveBeenCalled()
      expect(value.coordinator.write).not.toHaveBeenCalled()
    } finally { value.stop() }
  })

  it('does not suggest View actions when the existing writeEnabled preference is false', async () => {
    const value = fixture(resolveConfig({ writeEnabled: false }))
    try {
      const decision = await value.preStep([userMessage()], 1)
      if (decision.kind !== 'enter') throw new Error('unexpected rejection')
      expect(decision.messages[1]?.content[0]?.text).toContain('mnemon_view_route')
      expect(decision.messages[1]?.content[0]?.text).not.toContain('mnemon_view_action')
    } finally { value.stop() }
  })

  it('rechecks the selected Strategy before running an already-scheduled default review', async () => {
    vi.useFakeTimers()
    const config = resolveConfig({ idleReviewMs: 5_000 })
    const value = fixture(config)
    try {
      await value.preStep([durableCandidate()], 1)
      await value.turnStopping(1)
      await value.preStep([userMessage('one more turn')], 2)
      await value.turnStopping(2)
      expect(value.lifecycle.snapshot('session-1').current?.idleReviewPending).toBe(true)
      config.memoryTopology.strategyId = 'personal-notes'
      await vi.advanceTimersByTimeAsync(5_000)
      expect(value.coordinator.review).not.toHaveBeenCalled()
    } finally { value.stop() }
  })

  it('runs three-tier review only over turns that three-tier composed', async () => {
    vi.useFakeTimers()
    const value = fixture(resolveConfig({ idleReviewMs: 5_000 }))
    const pin = value.composableTurns.beginTurn.getMockImplementation()!
    // The selected Strategy is off, so the only installed one composes instead.
    value.composableTurns.beginTurn.mockImplementation(async (turnId: string, scope: object) => {
      const context = await pin(turnId, scope)
      context.view.strategyTypeId = 'general'
      return context
    })
    try {
      await value.preStep([durableCandidate()], 1)
      await value.turnStopping(1)
      await value.preStep([userMessage('one more substantive turn')], 2)
      await value.turnStopping(2)
      expect(value.lifecycle.snapshot('session-1').current?.reviewActivity.eligible).toBe(true)
      expect(value.lifecycle.snapshot('session-1').current?.idleReviewPending).toBe(false)
      await vi.advanceTimersByTimeAsync(10_000)
      expect(value.coordinator.review).not.toHaveBeenCalled()
    } finally { value.stop() }
  })

  it('delegates memory-tab candidates directly to an isolated memory subagent', async () => {
    const value = fixture()
    const result = await value.lifecycle.supervise('session-1', 'Use SQLite because deployment must remain single-file.')

    expect(result).toMatchObject({ delegated: true, sessionId: 'session-1', runId: 'write-child' })
    expect(value.followup).not.toHaveBeenCalled()
    expect(value.coordinator.write).toHaveBeenCalledWith(value.agent, 'supervised-writeback', {
      content: 'Use SQLite because deployment must remain single-file.',
      source: 'explicit Mnemon tab submission',
    }, expect.any(AbortSignal))
    expect(value.lifecycle.snapshot('session-1').counters.supervisedRequests).toBe(1)
  })

  it('runs workspace-only memory-tab candidates in a disposable top-level task Agent', async () => {
    const value = fixture()

    const result = await value.lifecycle.superviseTask('', 'Keep workspace release decisions durable.', undefined, '/tmp/workspace-two')

    expect(result).toMatchObject({ delegated: true, runId: 'write-child' })
    expect(value.createTaskAgent).toHaveBeenCalledWith(expect.objectContaining({ meta: { cwd: '/tmp/workspace-two', agentPreset: 'default' } }))
    expect(value.coordinator.write).toHaveBeenCalledWith(expect.objectContaining({ session: expect.objectContaining({ header: expect.objectContaining({ cwd: '/tmp/workspace-two' }) }) }), 'supervised-writeback', {
      content: 'Keep workspace release decisions durable.',
      source: 'explicit Mnemon tab submission',
    }, expect.anything())
    expect(value.disposedTaskAgents).toContain(result.sessionId)
  })

  it('deduplicates replayed assistant-message supervision by session and message id', async () => {
    const value = fixture()

    const [first, replay] = await Promise.all([
      value.lifecycle.supervise('session-1', 'Keep the release checklist durable.', 'message-1'),
      value.lifecycle.supervise('session-1', 'Keep the release checklist durable.', 'message-1'),
    ])

    expect(replay).toEqual(first)
    expect(value.coordinator.write).toHaveBeenCalledTimes(1)
    expect(value.coordinator.write).toHaveBeenCalledWith(value.agent, 'supervised-writeback', {
      content: 'Keep the release checklist durable.',
      source: 'explicit assistant memory action',
    }, expect.any(AbortSignal))
    expect(value.lifecycle.snapshot('session-1').counters.supervisedRequests).toBe(1)
    await expect(value.lifecycle.supervise('session-1', 'Different content.', 'message-1')).rejects.toThrow('different content')
  })

  it('extracts assistant text from the durable DSH message content', () => {
    const value = fixture()
    value.events.push({
      type: 'assistant/message',
      data: {
        turn: 1,
        step: 1,
        message: {
          id: 'message-1',
          role: 'assistant',
          content: [
            { type: 'text', text: 'First paragraph.' },
            { type: 'tool-call', id: 'call-1', name: 'read', arguments: '{}' },
            { type: 'text', text: 'Second paragraph.' },
          ],
          source: { kind: 'model', provider: 'mock', model: 'mock' },
        },
      },
    })

    expect(value.lifecycle.assistantMessage('session-1', 'message-1')).toEqual({
      messageId: 'message-1',
      text: 'First paragraph.\n\nSecond paragraph.',
    })
    expect(value.lifecycle.assistantMessage('session-1', 'missing')).toBeNull()
  })

  it('keeps disabled lifecycle hooks out of model input while retaining manual supervision', async () => {
    const value = fixture(resolveConfig({ lifecycleEnabled: false, recallMode: 'off', writebackMode: 'off' }))
    const prompt = userMessage()
    const decision = await value.preStep([prompt], 1)
    expect(decision).toEqual({ kind: 'enter', messages: [prompt] })
    expect(value.composableTurns.beginTurn).toHaveBeenCalledOnce()
    expect(value.steer).not.toHaveBeenCalled()
    await expect(value.lifecycle.supervise('session-1', 'Durable preference')).resolves.toMatchObject({ delegated: true })
  })
})
