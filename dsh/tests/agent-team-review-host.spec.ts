import { readFileSync, realpathSync } from 'node:fs'
import { createRequire } from 'node:module'
import { dirname, join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { expect, it } from 'vitest'
import * as scoped from 'dsh-mnemon-strategy-scoped'
import * as light from 'dsh-mnemon-strategy-light-context'
import * as capture from 'dsh-mnemon-strategy-auto-capture'
import type { HostAgent, HostContextShape } from '../src/host/dsh.ts'
import { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { compositionFixture } from './fixtures/composition.ts'

// Published-package matrix: DSH loads through its package exports only, with no
// aliases, source overlay or DSH edits. MNEMON_TEAM_TEST_PROFILE points it at
// another installation instead of the pinned development host.
const pinned = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8')).devDependencies['@deepseek-ai/dsh'] as string
const profile = process.env.MNEMON_TEAM_TEST_PROFILE
const hostRoot = profile === undefined
  ? dirname(realpathSync(new URL('../node_modules/@deepseek-ai/dsh/package.json', import.meta.url)))
  : resolve(profile)

it('runs guarded Team review against the published DSH cohort (issue 275)', async () => {
  const require = createRequire(join(hostRoot, 'package.json'))
  const load = async (name: string) => import(pathToFileURL(require.resolve(`@deepseek-ai/${name}`)).href)
  const [cordis, agent, loop, llm, session, persistence, projection, query, subagent, fork, spawn, prompt, tools, team, teamTools, ptc] = await Promise.all([
    'cordis', 'dsh-agent', 'dsh-agent-loop', 'dsh-llm', 'dsh-session', 'dsh-session-persistence-jsonl',
    'dsh-session-projection', 'dsh-session-query-sqlite', 'dsh-subagent', 'dsh-subagent-fork-in-process',
    'dsh-subagent-spawn-in-process', 'dsh-system-prompt', 'dsh-tools', 'dsh-experimental-agent-team',
    'dsh-experimental-tool-agent-team', 'dsh-ptc-runtime-node',
  ].map(load))
  const versions = Object.fromEntries(['dsh', 'dsh-agent', 'dsh-tools', 'dsh-subagent', 'dsh-experimental-agent-team', 'dsh-experimental-tool-agent-team'].map(name => {
    const version = JSON.parse(readFileSync(require.resolve(`@deepseek-ai/${name}/package.json`), 'utf8')).version
    expect(version).toBe(pinned)
    return [name, version]
  }))
  const ptcServices = await Promise.all(['dsh-fs-local', 'dsh-subprocess-local', 'dsh-sandbox-local', 'dsh-sandbox-policy'].map(load))
  type Reply = string | { name: string; args: Record<string, unknown> }
  class Adapter extends llm.LlmAdapter {
    constructor(private readonly respond: (options: Record<string, any>) => Reply) { super() }
    async *stream(options: Record<string, any>) {
      const reply = this.respond(options)
      if (typeof reply === 'string') {
        yield { type: 'block-start', index: 0, blockType: 'text' }
        yield { type: 'text-delta', index: 0, text: reply }
        yield { type: 'block-end', index: 0, block: { type: 'text', text: reply } }
      } else {
        const args = JSON.stringify(reply.args)
        yield { type: 'block-start', index: 0, blockType: 'tool-call' }
        yield { type: 'tool-call-delta', index: 0, id: `call-${++this.calls}`, name: reply.name, argumentsDelta: args }
        yield { type: 'block-end', index: 0, block: { type: 'tool-call', id: `call-${this.calls}`, name: reply.name, arguments: args } }
      }
      yield { type: 'usage', usage: { inputTokens: 10, outputTokens: 5 } }
      yield { type: 'finish', reason: { kind: typeof reply === 'string' ? 'stop' : 'tool-calls' } }
    }
    private calls = 0
  }
  const reports: unknown[] = []
  for (const provider of ['spawn', 'fork'] as const) for (const teams of ['off', 'service', 'tools'] as const) {
    for (const mode of ['native', 'ptc'] as const) for (const policy of teams === 'tools' ? ['pause', 'scoped'] as const : ['scoped'] as const) {
      const f = await compositionFixture({ idleReview: { provider, agentTeams: policy } })
      const ctx = new cordis.Context()
      const children: any[] = []
      const receipts: Array<{ name: string; isError: boolean }> = []
      let childCalls = 0
      let cancelNext = false
      const cancellation = new AbortController()
      let cancelled = false
      let stop: (() => void) | undefined
      const memory = `The synthetic ${provider} ${mode} Team review project uses SQLite.`
      try {
        await f.mount(scoped, { instanceId: 'scoped' })
        await f.mount(light, { instanceId: 'light' })
        await f.mount(capture, { instanceId: 'capture' })
        for (const plugin of [llm.default, session.default]) await ctx.plugin(plugin)
        await ctx.plugin(persistence.default, { root: join(f.root, 'sessions'), compression: 'none' })
        await ctx.plugin(projection.default)
        await ctx.plugin(query.default, { path: ':memory:', openAt: 'never' })
        await ctx.plugin(prompt.default)
        if (mode === 'ptc') for (const [index, service] of ptcServices.entries()) {
          await ctx.plugin(service.default, index === 0 ? { cwd: f.workspace } : index === 3 ? { mode: 'read-only', workspaceRoot: f.workspace } : {})
        }
        if (mode === 'ptc') await ctx.plugin(ptc.default)
        await ctx.plugin(tools.default, { mode: mode === 'ptc' ? 'both' : 'native' })
        await ctx.plugin(agent.default)
        await ctx.plugin(loop.default, { agents: [] })
        await ctx.plugin(subagent.default)
        await ctx.plugin(fork, { providerName: 'fork' })
        await ctx.plugin(spawn, { providerName: 'spawn' })
        if (teams !== 'off') await ctx.plugin(team.default)
        if (teams === 'tools') await ctx.plugin(teamTools)
        ctx.llm.registerAdapter(['fixture'], new Adapter(options => {
          const turn = f.graph.composableTurns.activeTurn(options.sessionId)
          expect(turn?.view.strategyExtensions?.map(extension => extension.slot).sort()).toEqual(['capture', 'projection', 'selection'])
          if (options.sessionId === 'parent') return 'The explicit project decision is ready for a bounded maintenance review.'
          if (cancelNext) {
            cancelNext = false
            childCalls++
            cancellation.abort(new Error('Synthetic review cancellation before mutation'))
            return 'This cancelled child must not commit a mutation.'
          }
          const inherited = JSON.stringify(options.messages)
          expect(inherited).toContain(memory)
          const system = options.system ?? JSON.stringify(options.messages.filter((message: { role: string }) => message.role === 'system'))
          const terminal = system.match(/Completion protocol: call `([^`]+)`/u)?.[1]
          const requestId = system.match(/requestId `([^`]+)`/u)?.[1]
          expect(terminal).toBeTruthy()
          const call = (name: string, args: Record<string, unknown>): Reply => mode === 'native' ? { name, args } : {
            name: 'run_code', args: { description: 'Exercise bounded Team review.', code: `return await tools[${JSON.stringify(name)}](${JSON.stringify(args)})` },
          }
          const step = childCalls++
          if (step === 0) return call('spawn_teammate', { name: 'forbidden-review-teammate', description: 'Must never run.', prompt: 'Must never run.' })
          if (step === 1) return call('mnemon_runtime_memory', { action: 'add', target: 'memory', content: memory, importance: 'normal' })
          if (step === 2) return call('mnemon_document_search', { query: 'synthetic Team review project', limit: 1 })
          const result = { action: 'added', summary: 'Committed the explicit synthetic project decision.', memoryBodyIds: [], documentIds: [] }
          return call(terminal, requestId === undefined ? result : { requestId, result })
        }))
        ctx.on('agent/created', ({ agent: child }: { agent: any }) => {
          if (child.session.header.origin !== 'subagent') return
          children.push(child)
          if (mode === 'ptc') child.ctx.tools.presentAs('ptc')
        })
        ctx.on('tools/result', (execution: any, result: any) => {
          if (execution.agent?.session.header.origin === 'subagent') receipts.push({ name: execution.name, isError: result.isError })
        })
        const host = ctx as HostContextShape
        const coordinator = new MnemonSubagentCoordinator(host.subagents, f.live, host)
        const lifecycle = new MnemonLifecycle(host, coordinator, f.config, f.live)
        registerTools(host, f.live, coordinator)
        stop = lifecycle.start()
        const parent = await ctx.agentLoop.create(session.SessionId('parent'), { provider: 'fixture', model: 'fixture' }, { cwd: f.workspace })
        parent.followup(llm.createUserMessage({ content: [{ type: 'text', text: memory }], source: { kind: 'user' } }))
        await parent.whenIdle()
        const review = coordinator.review(parent as HostAgent, AbortSignal.timeout(10_000))
        if (policy === 'pause') {
          await expect(review).resolves.toMatchObject({ delegated: false })
          expect(children).toHaveLength(0)
          expect(childCalls).toBe(0)
        } else {
          await expect(review).resolves.toMatchObject({ delegated: true, provider, action: 'added' })
          expect(children).toHaveLength(1)
          expect(childCalls).toBe(4)
          expect(receipts).toContainEqual({ name: 'mnemon_runtime_memory', isError: false })
          expect(receipts).toContainEqual({ name: 'mnemon_document_search', isError: false })
          expect(coordinator.snapshot()).toMatchObject({ reviews: 1, failures: 0 })
          expect(JSON.stringify(await f.graph.source('runtime').read('snapshot'))).toContain(memory)
          if (teams === 'tools' && mode === 'native') {
            const committed = receipts.length
            cancelNext = true
            await expect(coordinator.review(parent as HostAgent, cancellation.signal)).rejects.toMatchObject({ review: { status: 'failed', receipts: [] } })
            cancelled = true
            expect(receipts).toHaveLength(committed)
            expect(coordinator.snapshot()).toMatchObject({ reviews: 1, failures: 1 })
          }
        }
        if (policy === 'scoped') {
          expect(receipts.filter(receipt => receipt.name === 'spawn_teammate').every(receipt => receipt.isError)).toBe(true)
          if (teams === 'tools') expect(receipts).toContainEqual({ name: 'spawn_teammate', isError: true })
          expect(children).toHaveLength(cancelled ? 2 : 1)
          for (const child of children) expect(ctx.agents.get(child.id)).toBeUndefined()
        }
        if (teams === 'tools') {
          const result = await parent.ctx.tools.execute({ name: 'team_task_create', arguments: { subject: 'Parent Teams remains available', description: 'Synthetic official Team preservation probe.' },
            agent: parent, callId: 'team-preservation', signal: new AbortController().signal })
          expect(result.isError).toBe(false)
          expect(ctx.agentTeams.listTasks(parent)).toHaveLength(1)
        }
        reports.push({ provider, teams, mode, policy, strategyExtensions: ['scoped', 'light-context', 'auto-capture'], childCalls, children: children.length, cancelled, receipts })
      } finally { stop?.(); await ctx.fiber.dispose(); await f.dispose() }
    }
  }
  if (profile !== undefined) console.log(JSON.stringify({ versions, reports }))
}, 90_000)
