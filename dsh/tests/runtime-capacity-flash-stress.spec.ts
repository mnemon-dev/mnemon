import { realpathSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { performance } from 'node:perf_hooks'
import { Context } from '@deepseek-ai/cordis'
import AgentRegistry from '@deepseek-ai/dsh-agent'
import AgentLoop from '@deepseek-ai/dsh-agent-loop'
import LlmRuntime, { LlmAdapter, createUserMessage, type GenerateOptions, type StreamChunk } from '@deepseek-ai/dsh-llm'
import SessionStore, { SessionId } from '@deepseek-ai/dsh-session'
import JsonlSessionPersistence from '@deepseek-ai/dsh-session-persistence-jsonl'
import SessionProjectionRegistry from '@deepseek-ai/dsh-session-projection'
import SessionQueryEngine from '@deepseek-ai/dsh-session-query'
import SubagentRuntime from '@deepseek-ai/dsh-subagent'
import * as SubagentSpawn from '@deepseek-ai/dsh-subagent-spawn-in-process'
import SystemPrompt from '@deepseek-ai/dsh-system-prompt'
import ToolRuntime from '@deepseek-ai/dsh-tools'
import type { MemorySpaceCatalog, MemorySpaceView, Insight } from 'dsh-mnemon-source-memory-spaces/contracts'
import type { RuntimeMemoryMutationResult, RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
import { afterAll, afterEach, describe, expect, it } from 'vitest'
import type { HostContextShape } from '../src/host/dsh.ts'
import { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { createWriteHandler } from '../src/host/rpc.ts'
import { compositionFixture } from './fixtures/composition.ts'

// Explicit opt-in only. No discovery of credentials, personal memory or a live DSH profile.
const enabled = process.env.MNEMON_RUN_FLASH_STRESS === '1'
const MODEL = 'deepseek-v4-flash'
const PROVIDER = 'flash-stress'
const ENDPOINT = 'https://api.deepseek.com/chat/completions'
const DONE = 'mnemon_flash_stress_done'
const rounds = Number(process.env.MNEMON_FLASH_STRESS_ROUNDS ?? 8)
const jsonPrompt = process.env.MNEMON_FLASH_STRESS_JSON_PROMPT === '1'
const cleanups: Array<() => Promise<void>> = []
const reports: StressReport[] = []

type Domain = 'backend' | 'frontend' | 'android' | 'operations'
const domains: Domain[] = ['backend', 'frontend', 'android', 'operations']
const subjects: Record<Domain, string> = {
  backend: '后端订单服务采用事务发件箱处理支付回调，幂等键由租户、订单和请求序列组成。所有状态更新先校验版本号，数据库提交后才发布消息。失败重试必须保留审计记录，禁止绕过租户隔离。接口以服务端生成的追踪编号关联日志，测试环境只使用合成订单。',
  frontend: '前端管理台采用统一的查询缓存与表单校验规则，订单列表使用游标分页。保存操作必须等待服务端确认再刷新缓存，并保留当前筛选条件。权限不足时隐藏编辑入口，错误提示提供可重试操作，禁止在浏览器日志中记录访问令牌。无障碍导航必须覆盖弹窗和键盘焦点。',
  android: '安卓客户端采用离线队列同步订单草稿，网络恢复后按本地序列号提交。数据库迁移必须保留未同步草稿，重复响应依据业务幂等键合并。后台任务遵守系统节电约束，凭据存储在系统安全设施，界面旋转不能重复提交。发布验证覆盖断网、重连和进程重启。',
  operations: '运维发布采用分批灰度和可回退配置，每次部署记录构建指纹和数据库版本。健康检查同时观察错误率、延迟和队列积压，超过阈值停止扩容。备份恢复演练使用脱敏样本，监控告警必须关联明确责任范围，禁止把生产密钥放进部署日志。回滚后确认消息消费水位。',
}

interface Fact { id: string; domain: Domain; content: string }
interface ModelCall { session: string; purpose: string; inputTokens: number; outputTokens: number; elapsedMs: number }
interface StressReport {
  scenario: string; model: string; thinking: string; promptFormat: string; rounds: number; writers: number; memoryLimitBytes: number
  plannedFacts: number; submittedBytes: number; successfulWrites: number; archiveCycles: number; routerFallbacks: number
  maxHotBytes: number; finalHotFacts: number; finalColdFacts: number; missingFacts: string[]; changedFacts: string[]
  modelInputDrift: Array<{ id: string; kind: string }>; misroutedFacts: string[]; duplicateColdFacts: number
  unexpectedActions: string[]; changedEvidence: Array<{ id: string; submitted: string; observed: string[] }>
  toolErrors: string[]; modelsReturned: string[]; modelCalls: ModelCall[]; maxConcurrentModels: number
  maxConcurrentWrites: number; writeLatenciesMs: number[]; waves: Array<{ round: number; hotBytes: number; coldFacts: number }>
  taskAgentsCreated: number; taskAgentsRemaining: number; elapsedMs: number; status: 'running' | 'passed' | 'failed'
}

function fact(domain: Domain, index: number): Fact {
  const id = `flashstress-${domain}-${String(index).padStart(3, '0')}`
  const content = `项目 Orion，模块 ${domain}，记录 ${id}。${subjects[domain]} 本记录对应独立验收项 ${index}，配置键为 ${domain}.rule.${index}，验证端口为 ${12000 + index}，重试上限为 ${index % 5 + 2} 次。验收必须保留该记录标识、模块归属、配置键与数字约束，以便跨会话精确召回；本段全部为压力测试合成数据。`
  return { id, domain, content }
}

function prompt(facts: Fact[]): string {
  if (jsonPrompt) return `这是已获用户授权的隔离记忆压力测试。请逐条调用 mnemon_runtime_memory，将下列每一段完整原文写入 target=memory，action=add，importance=normal。不得改写、总结、合并、截断或遗漏记录标识；不要写入 USER，不要调用其他记忆工具。等待每条写入成功后，调用 ${DONE} 结束本轮，不要额外解释。若任一调用失败，立即调用 ${DONE} 交由测试报告失败，禁止反复重试或删除旧记忆。\n${facts.map(item => JSON.stringify(item.content)).join('\n')}`
  return `这是已获用户授权的隔离记忆压力测试。请逐条调用 mnemon_runtime_memory，将下列每个 memory-record 标签内的完整原文写入 target=memory，action=add，importance=normal，不要添加标签或包围引号。不得改写、总结、合并、截断或遗漏记录标识；不要写入 USER，不要调用其他记忆工具。等待每条写入成功后，调用 ${DONE} 结束本轮，不要额外解释。若任一调用失败，立即调用 ${DONE} 交由测试报告失败，禁止反复重试或删除旧记忆。\n${facts.map(item => `<memory-record>\n${item.content}\n</memory-record>`).join('\n')}`
}

function sanitized(error: unknown): string {
  const text = error instanceof Error ? error.message : typeof error === 'string' ? error : JSON.stringify(error)
  const key = process.env.DEEPSEEK_API_KEY
  return (key ? text.replaceAll(key, '[credential]') : text).replace(/\/(?:Users|private|tmp|var)\/[^\s"']+/gu, '[temporary-path]').slice(0, 600)
}

class SessionReads extends SessionQueryEngine {
  override async searchSessions(): Promise<never> { throw new Error('Stress test does not use a session index') }
  override async searchEvents(): Promise<never> { throw new Error('Stress test does not use a session index') }
}

/** The published DSH DeepSeek adapter handles the wire; this wrapper only observes it. */
class FlashAdapter extends LlmAdapter {
  private active = 0
  private readonly sessionLabels = new Map<string, string>()
  constructor(private readonly inner: LlmAdapter, private readonly report: StressReport) { super() }
  override providerInfo(provider: string) { return this.inner.providerInfo(provider) }
  override listModels(provider: string) { return this.inner.listModels(provider) }
  override resolveModel(provider: string, model: string, signal?: AbortSignal) {
    if (model !== MODEL) throw new Error('Stress test permits DeepSeek V4 Flash only')
    return this.inner.resolveModel(provider, model, signal)
  }
  override async *stream(options: GenerateOptions): AsyncIterable<StreamChunk> {
    if (options.model !== MODEL) throw new Error('Stress test permits DeepSeek V4 Flash only')
    if (this.report.modelCalls.length >= 240) throw new Error('Stress test exceeded its bounded model-call budget')
    const sessionId = String(options.sessionId ?? 'unknown')
    if (!this.sessionLabels.has(sessionId)) this.sessionLabels.set(sessionId, `session-${this.sessionLabels.size + 1}`)
    const call: ModelCall = { session: this.sessionLabels.get(sessionId)!, purpose: String(options.purpose ?? 'conversation'), inputTokens: 0, outputTokens: 0, elapsedMs: 0 }
    this.report.modelCalls.push(call)
    this.report.maxConcurrentModels = Math.max(this.report.maxConcurrentModels, ++this.active)
    const start = performance.now()
    try {
      for await (const chunk of this.inner.stream(options)) {
        if (chunk.type === 'usage') { call.inputTokens = chunk.usage.inputTokens; call.outputTokens = chunk.usage.outputTokens }
        yield chunk
      }
    } catch (error) {
      this.report.toolErrors.push(sanitized(error))
      console.log(JSON.stringify({ scenario: this.report.scenario, modelError: sanitized(error) }))
      throw error
    } finally { call.elapsedMs = Math.round(performance.now() - start); this.active-- }
  }
}

async function harness(scenario: string, multipleDestinations: boolean) {
  if (!Number.isInteger(rounds) || rounds < 2 || rounds > 24) throw new Error('Use 2 to 24 stress rounds')
  const key = process.env.DEEPSEEK_API_KEY
  const cliPath = process.env.MNEMON_NATIVE_TEST_CLI
  if (!key || !cliPath) throw new Error('Explicit DEEPSEEK_API_KEY and MNEMON_NATIVE_TEST_CLI are required')
  const report: StressReport = {
    scenario, model: MODEL, thinking: 'disabled', promptFormat: jsonPrompt ? 'json-strings' : 'record-elements', rounds, writers: 4, memoryLimitBytes: 10240, plannedFacts: 0,
    submittedBytes: 0, successfulWrites: 0, archiveCycles: 0, routerFallbacks: 0, maxHotBytes: 0,
    finalHotFacts: 0, finalColdFacts: 0, missingFacts: [], changedFacts: [], toolErrors: [], modelsReturned: [],
    modelInputDrift: [], misroutedFacts: [], duplicateColdFacts: 0,
    unexpectedActions: [], changedEvidence: [],
    modelCalls: [], maxConcurrentModels: 0, maxConcurrentWrites: 0, writeLatenciesMs: [], waves: [],
    taskAgentsCreated: 0, taskAgentsRemaining: 0, elapsedMs: 0, status: 'running',
  }
  reports.push(report)
  const started = performance.now()
  const f = await compositionFixture({ cliPath, recallMode: 'off', writebackMode: 'off',
    taskAgentModel: { mode: 'fixed', provider: PROVIDER, model: MODEL } })
  const ctx = new Context()
  let stop: (() => void) | undefined
  const audits: Promise<void>[] = []
  const wireErrors: string[] = []
  const originalFetch = globalThis.fetch
  const expected: Fact[] = []
  const submitted = new Map<string, string>()
  const progress = setInterval(() => console.log(JSON.stringify({ scenario, progress: 'running', successfulWrites: report.successfulWrites,
    archives: report.archiveCycles, modelCalls: report.modelCalls.length, tasks: report.taskAgentsCreated,
    lastModelCallMs: report.modelCalls.at(-1)?.elapsedMs, errors: report.toolErrors.length })), 15000)
  globalThis.fetch = async (input, init) => {
    if (String(input) !== ENDPOINT || typeof init?.body !== 'string') throw new Error('Unexpected network request in isolated Flash stress test')
    const body = JSON.parse(init.body) as { model: string }
    if (body.model !== MODEL) throw new Error('Blocked a non-Flash request before network dispatch')
    const response = await originalFetch(input, init)
    audits.push((async () => {
      const reader = response.clone().body!.getReader()
      const decoder = new TextDecoder()
      let pending = ''
      let sawModel = false
      try {
        while (true) {
          const { value: bytes, done } = await reader.read()
          if (done) break
          pending += decoder.decode(bytes, { stream: true })
          const lines = pending.split('\n')
          pending = lines.pop()!
          for (const line of lines) {
            if (!line.startsWith('data: ') || line.trim() === 'data: [DONE]') continue
            const value = JSON.parse(line.slice(6)) as { model?: string }
            if (value.model === undefined) continue
            sawModel = true
            if (!report.modelsReturned.includes(value.model)) report.modelsReturned.push(value.model)
            if (value.model !== MODEL) throw new Error('Provider returned a non-Flash model')
          }
        }
      } catch (error) {
        // DSH closes a completed tool-call stream without waiting for socket EOF.
        // Validate the already-read model; actual request failures are observed by FlashAdapter.
        if (!sawModel || sanitized(error) !== 'DeepSeek stream consumer stopped') wireErrors.push(sanitized(error))
      } finally { reader.releaseLock() }
    })())
    return response
  }
  cleanups.push(async () => {
    try {
      await ctx.fiber.dispose()
      stop?.()
      await Promise.all(audits)
      report.toolErrors.push(...wireErrors)
      if (report.status === 'running') report.status = 'failed'
    } finally {
      clearInterval(progress)
      globalThis.fetch = originalFetch
      await f.dispose()
      report.elapsedMs = Math.round(performance.now() - started)
    }
  })
  const spaces = f.graph.source('memory-spaces')
  const initial = await spaces.read<MemorySpaceCatalog>('body-directory')
  if (multipleDestinations) {
    for (const body of initial.items) if (body.active) await spaces.mutate('body-update', { memoryBodyId: body.id, active: false })
    for (const domain of domains) await spaces.mutate<MemorySpaceView>('body-create', {
      name: `Orion ${domain}`, description: `Only Orion ${domain} facts, configuration and durable decisions belong here.`,
      providerId: 'mnemon-native', active: true,
    })
  } else if (!initial.items.some(body => body.active)) {
    await spaces.mutate('body-create', { name: 'Orion project', description: 'Synthetic Orion project facts for all four workstreams.', providerId: 'mnemon-native', active: true })
  }
  const directory = await spaces.read<MemorySpaceCatalog>('body-directory')
  expect(directory.items.filter(body => body.active && body.provider.capabilities.remember)).toHaveLength(multipleDestinations ? 4 : 1)
  await ctx.plugin(LlmRuntime)
  await ctx.plugin(SessionStore)
  await ctx.plugin(JsonlSessionPersistence, { root: join(f.root, 'sessions'), compression: 'none' })
  await ctx.plugin(SessionProjectionRegistry)
  await ctx.plugin(SessionReads)
  await ctx.plugin(SystemPrompt)
  await ctx.plugin(ToolRuntime)
  await ctx.plugin(AgentRegistry)
  await ctx.plugin(AgentLoop, { agents: [] })
  await ctx.plugin(SubagentRuntime)
  await ctx.plugin(SubagentSpawn, { providerName: 'spawn' })
  const requireDsh = createRequire(realpathSync(new URL('../node_modules/@deepseek-ai/dsh/package.json', import.meta.url)))
  const deepseek = await import(requireDsh.resolve('@deepseek-ai/dsh-llm-deepseek'))
  const options = deepseek.resolveAdapterOptions({ baseURL: 'https://api.deepseek.com', thinking: 'disabled', reasoningEffort: 'off', maxTokens: 8192, streamIdleTimeoutMs: 60000 })
  ctx.llm.registerAdapter([PROVIDER], new FlashAdapter(new deepseek.DeepSeekAdapter({ options: () => options,
    resolveApiKey: async () => key, resolveUserId: () => 'mnemon-synthetic-capacity-stress',
    prepareExtensions: async () => ({ fields: {}, accept: async () => {} }) }), report))
  const host = ctx as unknown as HostContextShape
  const coordinator: MnemonSubagentCoordinator = new MnemonSubagentCoordinator(host.subagents, f.live, host,
    () => ({ provider: PROVIDER, model: MODEL }), () => 8192,
    (scope, signal, operation) => lifecycle.runRuntimeMaintenanceTask(scope, signal, operation))
  const lifecycle = new MnemonLifecycle(host, coordinator, f.config, f.live)
  registerTools(host, f.live, coordinator)
  ctx.tools.register({ name: DONE, description: 'Finish this synthetic test batch; the harness verifies every requested write and reports any failure.',
    parameters: { type: 'object', properties: {} },
    output: { schema: { type: 'object', properties: { done: { type: 'boolean' } } }, render: () => [{ type: 'text', text: 'Batch complete.' }] },
    async execute(_args, execution) { execution.concludeTurn?.(); return { done: true } },
  })
  let activeWrites = 0
  ctx.on('tools/execute', async (execution, next) => {
    if (execution.name !== 'mnemon_runtime_memory') return next()
    report.maxConcurrentWrites = Math.max(report.maxConcurrentWrites, ++activeWrites)
    const start = performance.now()
    try { return await next() } finally { report.writeLatenciesMs.push(Math.round(performance.now() - start)); activeWrites-- }
  })
  const recordWrite = (value: RuntimeMemoryMutationResult, content: string) => {
    const normalized = content.trim().replace(/\s+/gu, ' ')
    const id = normalized.match(/flashstress-(?:backend|frontend|android|operations)-\d{3}/u)?.[0]
    if (id === undefined) throw new Error('Model submitted memory without a test record id')
    const planned = expected.find(item => item.id === id)
    if (planned === undefined) throw new Error('Model invented a test record id')
    if (submitted.has(id)) throw new Error('Model submitted the same test record twice')
    submitted.set(id, normalized)
    expect(value.added).toBe(normalized)
    if (normalized !== planned.content) report.modelInputDrift.push({ id,
      kind: normalized.replace(/^["“]|["”]$/gu, '') === planned.content ? 'surrounding-quotes' : 'text-change' })
    report.successfulWrites++
    report.maxHotBytes = Math.max(report.maxHotBytes, value.usage.used)
    if (value.maintenance?.kind === 'mnemon-archive') {
      report.archiveCycles++
      if (value.maintenance.summary.includes('routing model failed')) report.routerFallbacks++
      console.log(JSON.stringify({ scenario, archive: report.archiveCycles, retainedBytes: value.usage.used,
        destinations: value.maintenance.memoryBodyIds.length, routerFallbacks: report.routerFallbacks }))
    }
  }
  ctx.on('tools/result', (execution, result) => {
    if (execution.name !== 'mnemon_runtime_memory') {
      if (execution.name !== DONE && execution.name !== 'mnemon_subagent_result') report.unexpectedActions.push(execution.name)
      return
    }
    const args = execution.arguments as { action?: string; target?: string; content: string }
    if (args.action !== 'add' || args.target !== 'memory') report.unexpectedActions.push(`${execution.name}:${args.action}:${args.target}`)
    if (result.isError) {
      report.toolErrors.push(sanitized(result.content))
      console.log(JSON.stringify({ scenario, writeError: sanitized(result.content) }))
    } else recordWrite(result.value as unknown as RuntimeMemoryMutationResult, args.content)
  })
  ctx.on('agent/error', ({ error }) => {
    report.toolErrors.push(sanitized(error))
    console.log(JSON.stringify({ scenario, agentError: sanitized(error) }))
  })
  ctx.on('agent/created', ({ agent }): undefined => { if (agent.session.header.origin !== 'subagent' && !domains.includes(agent.id as Domain)) report.taskAgentsCreated++ })
  stop = lifecycle.start()
  const parentHandles = await Promise.all(domains.map(domain => ctx.agents.create({ sessionId: SessionId(domain),
    agentOptions: { provider: PROVIDER, model: MODEL, maxTokens: 8192 }, meta: { cwd: f.workspace } })))
  const parents = parentHandles.map(handle => handle.agent)
  const checkpoint = async (round: number) => {
    const hot = await f.graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')
    const cold = await spaces.read<{ items: Insight[] }>('list', { limit: 1000 })
    const retained = [...hot.entries, ...cold.items]
    report.missingFacts = expected.filter(item => !retained.some(row => row.content.includes(item.id))).map(item => item.id)
    report.changedFacts = expected.filter(item => !retained.some(row => row.content === submitted.get(item.id))).map(item => item.id)
    report.changedEvidence = report.changedFacts.map(id => ({ id, submitted: submitted.get(id) ?? '', observed: retained.filter(row => row.content.includes(id)).map(row => row.content) }))
    report.duplicateColdFacts = cold.items.length - new Set(cold.items.map(item => item.content)).size
    if (multipleDestinations) report.misroutedFacts = cold.items.flatMap(item => {
      const planned = expected.find(candidate => item.content.includes(candidate.id))
      const destination = directory.items.find(body => body.id === item.memoryBodyId)
      return planned !== undefined && destination?.name !== `Orion ${planned.domain}` ? [planned.id] : []
    })
    report.finalHotFacts = hot.entries.length
    report.finalColdFacts = cold.items.length
    report.maxHotBytes = Math.max(report.maxHotBytes, hot.targets.memory.used)
    report.waves.push({ round, hotBytes: hot.targets.memory.used, coldFacts: cold.items.length })
    console.log(JSON.stringify({ scenario, round, expected: expected.length, successfulWrites: report.successfulWrites,
      archives: report.archiveCycles, hotBytes: hot.targets.memory.used, coldFacts: cold.items.length,
      missing: report.missingFacts.length, changedAfterSubmission: report.changedFacts.length,
      modelInputDrift: report.modelInputDrift.length, misrouted: report.misroutedFacts.length, modelCalls: report.modelCalls.length }))
    expect(hot.targets.memory.limit).toBe(10240)
    expect(hot.targets.memory.used).toBeLessThanOrEqual(10240)
    expect(report.missingFacts).toEqual([])
    expect(report.changedFacts).toEqual([])
    expect(cold.items.every(item => directory.items.some(body => body.active && body.id === item.memoryBodyId))).toBe(true)
    expect(report.unexpectedActions).toEqual([])
    expect(hot.entries.every(entry => entry.target === 'memory')).toBe(true)
  }
  const batch = (domain: Domain, round: number) => {
    const values = Array.from({ length: 3 }, (_, index) => fact(domain, round * 3 + index))
    expected.push(...values)
    report.plannedFacts = expected.length
    report.submittedBytes += values.reduce((sum, item) => sum + Buffer.byteLength(item.content), 0)
    return values
  }
  const finish = async () => {
    await Promise.all(audits)
    report.taskAgentsRemaining = ctx.agents.roots().filter(agent => !parents.includes(agent)).length
    expect(wireErrors).toEqual([])
    expect(report.modelsReturned).toEqual([MODEL])
    expect(report.toolErrors).toEqual([])
    expect(report.successfulWrites).toBe(report.plannedFacts)
    expect(report.archiveCycles).toBeGreaterThan(0)
    expect(report.taskAgentsRemaining).toBe(0)
    if (multipleDestinations) { expect(report.taskAgentsCreated).toBeGreaterThan(0); expect(report.routerFallbacks).toBe(0) }
    report.status = 'passed'
  }
  return { ...f, ctx, report, parents, parentHandles, batch, checkpoint, finish, lifecycle, coordinator, recordWrite }
}

afterEach(async () => { for (const cleanup of cleanups.splice(0).reverse()) await cleanup() })
afterAll(() => {
  if (enabled && process.env.MNEMON_FLASH_STRESS_REPORT) writeFileSync(process.env.MNEMON_FLASH_STRESS_REPORT,
    JSON.stringify({ model: MODEL, reports }, null, 2) + '\n')
})

describe.skipIf(!enabled)('real DeepSeek V4 Flash Runtime pressure', () => {
  it('retains all exact facts from four concurrent root sessions through repeated 10 KiB overflows', async () => {
    const f = await harness('four-roots-single-space', false)
    for (let round = 0; round < rounds; round++) {
      await Promise.all(f.parents.map(async (agent, index) => {
        agent.followup(createUserMessage({ content: [{ type: 'text', text: prompt(f.batch(domains[index]!, round)) }], source: { kind: 'user' } }))
        await agent.whenIdle()
      }))
      await f.checkpoint(round)
    }
    expect(f.report.maxConcurrentModels).toBeGreaterThanOrEqual(4)
    await f.finish()
  }, 1200000)

  it('routes exact facts from four child writers into four Native spaces using independent Flash tasks', async () => {
    const f = await harness('four-children-four-spaces', true)
    for (let round = 0; round < rounds; round++) {
      await Promise.all(f.parents.map(async (parent, index) => {
        const run = await f.ctx.subagents.start('spawn', { parent, label: `Orion ${domains[index]} writer`,
          prompt: [{ type: 'text', text: prompt(f.batch(domains[index]!, round)) }],
          signal: AbortSignal.timeout(180000), maxDepth: 1, toolFilter: { allow: ['mnemon_runtime_memory', DONE] },
          agentOptions: { provider: PROVIDER, model: MODEL, maxTokens: 8192 },
        })
        try { expect((await run.result).stopReason).toBe('completed') } finally { await run.dispose() }
      }))
      await f.checkpoint(round)
    }
    expect(f.report.maxConcurrentModels).toBeGreaterThanOrEqual(4)
    await f.finish()
  }, 1200000)

  it('continues archiving through Flash routing with no user sessions open', async () => {
    const f = await harness('no-session-four-spaces', true)
    for (const handle of f.parentHandles) await handle.dispose()
    expect(f.ctx.agents.roots()).toHaveLength(0)
    const write = createWriteHandler(f.live, f.lifecycle)
    for (let round = 0; round < Math.max(2, Math.floor(rounds / 2)); round++) {
      for (const domain of domains) for (const item of f.batch(domain, round)) {
        const result = await write('runtime-memory', { workspaceId: 'workspace', action: 'add', target: 'memory', importance: 'normal', content: item.content })
        if (!result.ok) throw new Error(sanitized(result.error.message))
        expect(result).toMatchObject({ ok: true })
        f.recordWrite((result as { ok: true; value: RuntimeMemoryMutationResult }).value, item.content)
      }
      await f.checkpoint(round)
      expect(f.ctx.agents.roots()).toHaveLength(0)
    }
    await f.finish()
  }, 1200000)
})
