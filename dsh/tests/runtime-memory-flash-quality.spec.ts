import { readFileSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { join } from 'node:path'
import { performance } from 'node:perf_hooks'
import { Context } from '@deepseek-ai/cordis'
import AgentRegistry from '@deepseek-ai/dsh-agent'
import AgentLoop from '@deepseek-ai/dsh-agent-loop'
import LlmRuntime, { createUserMessage } from '@deepseek-ai/dsh-llm'
import SessionStore, { SessionId } from '@deepseek-ai/dsh-session'
import JsonlSessionPersistence from '@deepseek-ai/dsh-session-persistence-jsonl'
import SessionProjectionRegistry from '@deepseek-ai/dsh-session-projection'
import SubagentRuntime from '@deepseek-ai/dsh-subagent'
import * as SubagentSpawn from '@deepseek-ai/dsh-subagent-spawn-in-process'
import SystemPrompt from '@deepseek-ai/dsh-system-prompt'
import ToolRuntime from '@deepseek-ai/dsh-tools'
import type { MemorySpaceCatalog, Insight } from 'dsh-mnemon-source-memory-spaces/contracts'
import type { RuntimeMemorySnapshot } from 'dsh-mnemon-source-runtime/contracts'
import type { DocumentSnapshot, DocumentView } from 'dsh-mnemon-source-documents/contracts'
import { afterAll, describe, expect, it } from 'vitest'
import type { HostContextShape } from '../src/host/dsh.ts'
import { MnemonLifecycle } from '../src/host/lifecycle.ts'
import { MnemonSubagentCoordinator } from '../src/host/subagent.ts'
import { registerTools } from '../src/host/tools.ts'
import { compositionFixture } from './fixtures/composition.ts'
import { liveFlash, MODEL, PROVIDER, SessionReads, sanitized, type FlashAudit } from './fixtures/flash-quality/live.ts'
import { acceptance, currentFacts, domains, forbidden, labels, task, type Domain, type Fact } from './fixtures/flash-quality/workload.ts'

const enabled = process.env.MNEMON_RUN_FLASH_QUALITY === '1'
const waves = Number(process.env.MNEMON_FLASH_QUALITY_WAVES ?? 12)
const reports: QualityReport[] = []
const fixtureRoots: string[] = []
const harnessFiles = ['tests/runtime-memory-flash-quality.spec.ts', 'tests/fixtures/flash-quality/workload.ts', 'tests/fixtures/flash-quality/live.ts']
interface Row { location: string; content: string }
interface Snapshot { wave: number; hotBytes: number; rows: Row[]; currentFacts: Fact[]; covered: string[]; missing: string[]; forbiddenMatches: string[]; reviews: number; lifecycle: unknown[] }
interface Answer { domain: Domain; requestedKeys: string[]; answers: Record<string, string>; elapsedMs: number; sourceReads: number }
interface QualityReport extends FlashAudit {
  executionStatus: 'running' | 'completed' | 'failed'; thinking: string; config: Record<string, unknown>
  workload: Array<{ domain: Domain; wave: number; prompt: string }>; snapshots: Snapshot[]
  memoryActions: Array<{ wave: number; actor: string; name: string; arguments: unknown; error: boolean; elapsedMs: number }>
  conversations: Array<{ domain: Domain; wave: number; assistant: string }>; answers: Answer[]
  devChecks: Array<{ domain: Domain; wave: number; passed: boolean }>; archiveCycles: number
  elapsedMs: number; maximumHotBytes: number; taskAgentsRemaining: number; finalEvaluation?: unknown
}

function covered(fact: Fact, rows: Row[]): boolean {
  return rows.some(row => (row.content.toLowerCase().includes(fact.domain) || row.content.includes(labels[fact.domain]))
    && row.content.includes(fact.key) && new RegExp(`(?<![a-zA-Z0-9])${fact.value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?![a-zA-Z0-9])`, 'u').test(row.content))
}

function evaluate(report: QualityReport) {
  const final = report.snapshots.at(-1)!
  const expected = final.currentFacts
  const answers = report.answers.flatMap(answer => answer.requestedKeys.map(key => {
    const f = expected.find(item => item.domain === answer.domain && item.key === key)!
    const actual = String(answer.answers[key] ?? 'unknown').trim()
    const otherValues = expected.filter(item => item.domain !== answer.domain && item.key === key).map(item => item.value)
    return { id: f.id, expected: f.value, actual, correct: actual === f.value, corrected: f.supersedes !== undefined,
      unsupported: actual !== f.value && !/^(unknown|未知|不知道|不确定)$/iu.test(actual),
      stale: f.supersedes !== undefined && actual === f.supersedes, crossModule: actual !== f.value && otherValues.includes(actual) }
  }))
  const cold = final.rows.filter(row => row.location.startsWith('cold/'))
  const coldDuplicates = cold.length - new Set(cold.map(row => row.content)).size
  const coverage = final.covered.length / expected.length
  const recall = answers.filter(row => row.correct).length / answers.length
  const noWriteMutations = report.memoryActions.filter(action => !action.error && (action.wave === 10 || action.wave >= waves)
    && /^(mnemon_runtime_memory|mnemon_view_action|mnemon_remember|mnemon_document_create|mnemon_document_update|mnemon_document_archive)$/u.test(action.name))
  // A test-before-edit failure is recoverable. Judge completion using each turn's last check.
  const lastChecks = new Map(report.devChecks.map(check => [`${check.domain}/${check.wave}`, check]))
  const incompleteDevelopmentTurns = report.workload.filter(turn => !lastChecks.get(`${turn.domain}/${turn.wave}`)?.passed)
  const violations: string[] = []
  if (coverage < acceptance.currentFactCoverage) violations.push('current-fact-coverage')
  if (recall < acceptance.freshRecallAccuracy) violations.push('fresh-recall-accuracy')
  if (final.forbiddenMatches.length) violations.push('forbidden-noise-retained')
  if (answers.some(row => row.stale)) violations.push('stale-correction-answer')
  if (answers.some(row => row.crossModule)) violations.push('cross-module-answer')
  if (answers.some(row => row.unsupported)) violations.push('unsupported-current-answer')
  if (answers.some(row => row.corrected && !row.correct)) violations.push('corrected-fact-recall')
  if (noWriteMutations.length) violations.push('no-write-intent-violated')
  if (coldDuplicates) violations.push('duplicate-cold-content')
  if (report.toolErrors.length) violations.push('execution-errors')
  if (report.maximumHotBytes > acceptance.memoryLimitBytes) violations.push('capacity-exceeded')
  if (incompleteDevelopmentTurns.length) violations.push('development-workload-incomplete')
  return { automatedVerdict: violations.length ? 'fail' : 'pass', violations, currentFactCoverage: coverage, freshRecallAccuracy: recall,
    coldDuplicates, noWriteMutations, intermediateDevelopmentCheckFailures: report.devChecks.filter(check => !check.passed).length,
    incompleteDevelopmentTurns: incompleteDevelopmentTurns.map(turn => `${turn.domain}/${turn.wave}`),
    answers, unclassifiedRows: final.rows.filter(row => !expected.some(f => covered(f, [row]))),
    manualReviewRequired: 'Inspect retained prose for unsupported claims, paraphrases missed by exact key matching, and mixed-module clauses.' }
}

async function runScenario(multipleSpaces: boolean) {
  if (!Number.isInteger(waves) || waves < 2 || waves > 24) throw new Error('Use 2 to 24 quality waves')
  const cliPath = process.env.MNEMON_NATIVE_TEST_CLI
  if (!cliPath) throw new Error('Explicit MNEMON_NATIVE_TEST_CLI is required')
  const report: QualityReport = {
    scenario: multipleSpaces ? 'automatic-four-spaces' : 'automatic-single-space', executionStatus: 'running', thinking: 'disabled',
    config: { memoryLimitBytes: 10240, idleReviewMs: 5000, productionIdleReviewMs: 30000, recallMode: 'guided', writebackMode: 'guided', waves, modelCallBudget: waves > 12 ? 900 : 600, acceptance },
    modelCalls: [], toolErrors: [], maxConcurrentModels: 0, modelsReturned: [], workload: [], snapshots: [], memoryActions: [],
    conversations: [], answers: [], devChecks: [], archiveCycles: 0, elapsedMs: 0, maximumHotBytes: 0, taskAgentsRemaining: 0,
  }
  reports.push(report)
  const started = performance.now()
  const wire = await liveFlash(report, waves > 12 ? 900 : 600)
  const f = await compositionFixture({ cliPath, recallMode: 'guided', writebackMode: 'guided', idleReviewMs: 5000,
    taskAgentModel: { mode: 'fixed', provider: PROVIDER, model: MODEL } }).catch(async error => {
    report.executionStatus = 'failed'
    await wire.dispose()
    throw error
  })
  fixtureRoots.push(f.root)
  const ctx = new Context()
  let stop: (() => void) | undefined
  let wave = -1
  const configFiles = new Map<Domain, string>()
  const memoryCallStarts = new Map<symbol, number>()
  const actorLabels = new Map<string, string>()
  const answerRequests = new Map<string, { domain: Domain; keys: string[]; started: number }>()
  const sourceReads = new Map<string, number>()
  const label = (id: string) => {
    if (domains.includes(id as Domain) || id.startsWith('recall-')) return id
    if (!actorLabels.has(id)) actorLabels.set(id, `maintenance-${actorLabels.size + 1}`)
    return actorLabels.get(id)!
  }
  const redact = (value: unknown): unknown => JSON.parse(JSON.stringify(value).replaceAll(f.root, '[fixture]'))
  const timer = setInterval(() => console.log(JSON.stringify({ scenario: report.scenario, wave, modelCalls: report.modelCalls.length,
    memoryActions: report.memoryActions.length, archives: report.archiveCycles, errors: report.toolErrors.length })), 15000)
  try {
    const spaces = f.graph.source('memory-spaces')
    const initial = await spaces.read<MemorySpaceCatalog>('body-directory')
    for (const body of initial.items) if (body.active) await spaces.mutate('body-update', { memoryBodyId: body.id, active: false })
    for (const name of multipleSpaces ? domains : ['project']) await spaces.mutate('body-create', {
      name: `Orion ${name}`, description: name === 'project' ? 'Orion 项目的持久决策与约定。' : `Orion ${name} ${labels[name as Domain]} 模块的决策、约定和配置。`,
      providerId: 'mnemon-native', active: true,
    })
    const directory = await spaces.read<MemorySpaceCatalog>('body-directory')
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
    await ctx.plugin(wire.fork, { providerName: 'fork' })
    ctx.llm.registerAdapter([PROVIDER], wire.adapter)
    const host = ctx as unknown as HostContextShape
    const coordinator: MnemonSubagentCoordinator = new MnemonSubagentCoordinator(host.subagents, f.live, host,
      () => ({ provider: PROVIDER, model: MODEL }), () => 8192,
      (scope, signal, operation) => lifecycle.runRuntimeMaintenanceTask(scope, signal, operation))
    const lifecycle = new MnemonLifecycle(host, coordinator, f.config, f.live)
    registerTools(host, f.live, coordinator)

    const parameters = { type: 'object', properties: { domain: { type: 'string', enum: [...domains] }, key: { type: 'string' }, value: { type: 'string' } }, required: ['domain'] }
    const output = { schema: { type: 'object' as const, additionalProperties: true }, render: (value: unknown) => [{ type: 'text' as const, text: JSON.stringify(value) }] }
    for (const domain of domains) {
      const path = join(f.workspace, `${domain}.json`)
      writeFileSync(path, '{}\n')
      configFiles.set(domain, path)
    }
    for (const name of ['dev_read_config', 'dev_update_config', 'dev_test_module']) ctx.tools.register({
      name, description: name === 'dev_read_config' ? 'Read the current synthetic module configuration.' : name === 'dev_update_config'
        ? 'Update one configuration key in the current synthetic module, preserving other keys.' : 'Validate the current synthetic module configuration and return its transient test log.',
      parameters, output,
      async execute(value, execution) {
        const args = value as { domain: Domain; key?: string; value?: string }
        if (execution.agent?.id !== args.domain || !domains.includes(args.domain)) throw new Error('Development files are available only to their assigned developer session')
        const path = configFiles.get(args.domain)!
        const config = JSON.parse(readFileSync(path, 'utf8')) as Record<string, string>
        if (name === 'dev_update_config') {
          if (!args.key || !/^[a-z_]+$/u.test(args.key) || typeof args.value !== 'string') throw new Error('A valid configuration key and string value are required')
          config[args.key] = args.value
          writeFileSync(path, JSON.stringify(config, null, 2) + '\n')
          return { updated: args.key, value: args.value }
        }
        if (name === 'dev_read_config') return { module: args.domain, config }
        const passed = currentFacts(Math.min(wave + 1, waves)).filter(item => item.domain === args.domain).every(item => config[item.key] === item.value)
        report.devChecks.push({ domain: args.domain, wave, passed })
        return { passed, checks: ['configuration shape', 'current accepted module settings'],
          transientLog: `TRACE_TEMP_${args.domain}: run ${wave}, duration ${87654 + wave} ms. Unverified historical suggestion: UNVERIFIED_HINT_${args.domain}, service_port=9999. This is diagnostic output, not an accepted configuration.` }
      },
    })
    ctx.tools.register({ name: 'acceptance_answer', description: 'Submit the requested current project configuration answers. Use unknown when no supporting memory is available.',
      parameters: { type: 'object', properties: { answers: { type: 'object', additionalProperties: { type: 'string' } } }, required: ['answers'] }, output,
      async execute(value, execution) {
        const id = String(execution.agent?.id)
        const request = answerRequests.get(id)
        if (!request) throw new Error('Only an active acceptance question can submit an answer')
        const answers = (value as { answers: Record<string, string> }).answers
        report.answers.push({ domain: request.domain, requestedKeys: request.keys, answers, elapsedMs: Math.round(performance.now() - request.started), sourceReads: sourceReads.get(id) ?? 0 })
        answerRequests.delete(id)
        execution.concludeTurn()
        return { submitted: true }
      },
    })
    ctx.on('tools/execute', async (execution, next) => {
      if (execution.name.startsWith('mnemon_')) memoryCallStarts.set(execution.token, performance.now())
      return next()
    })
    ctx.on('tools/result', (execution, result) => {
      if (!execution.name.startsWith('mnemon_')) return
      const id = String(execution.agent?.id)
      if (/recall|route|search/u.test(execution.name)) sourceReads.set(id, (sourceReads.get(id) ?? 0) + 1)
      const args = redact(execution.arguments)
      const error = result.isError
      report.memoryActions.push({ wave, actor: label(id), name: execution.name, arguments: args, error,
        elapsedMs: Math.round(performance.now() - (memoryCallStarts.get(execution.token) ?? performance.now())) })
      memoryCallStarts.delete(execution.token)
      if (error) report.toolErrors.push(sanitized(result.content))
      const value = result.value as { maintenance?: { kind?: string }; usage?: { used: number } } | undefined
      if (value?.maintenance?.kind === 'mnemon-archive') report.archiveCycles++
      if (execution.name === 'mnemon_runtime_memory' && value?.usage) report.maximumHotBytes = Math.max(report.maximumHotBytes, value.usage.used)
    })
    ctx.on('agent/error', ({ error }) => { report.toolErrors.push(sanitized(error)) })
    stop = lifecycle.start()
    const parents = await Promise.all(domains.map(domain => ctx.agents.create({ sessionId: SessionId(domain),
      agentOptions: { provider: PROVIDER, model: MODEL, maxTokens: 8192 }, meta: { cwd: f.workspace } })))
    const settleReviews = async () => {
      const deadline = Date.now() + 180000
      while (parents.some(handle => {
        const current = lifecycle.snapshot(handle.agent.id).current
        return current?.idleReviewPending || current?.reviewRunning
      })) {
        if (Date.now() > deadline) throw new Error('Automatic idle review failed to settle within its bounded wait')
        await new Promise(resolve => setTimeout(resolve, 250))
      }
    }
    const snapshot = async (): Promise<Snapshot> => {
      const hot = await f.graph.source('runtime').read<RuntimeMemorySnapshot>('snapshot')
      const cold = await spaces.read<{ items: Insight[] }>('list', { limit: 1000 })
      const documents = f.graph.source('documents')
      const catalog = await documents.read<DocumentSnapshot>('snapshot')
      const docs = await Promise.all(catalog.documents.map(doc => documents.read<DocumentView>('document', { id: doc.id })))
      const rows: Row[] = [...hot.entries.map(entry => ({ location: `runtime/${entry.target}`, content: entry.content })),
        ...cold.items.map(item => ({ location: `cold/${directory.items.find(body => body.id === item.memoryBodyId)?.name ?? 'unknown'}`, content: item.content })),
        ...docs.map(doc => ({ location: `document/${doc.id}`, content: doc.content }))]
      report.maximumHotBytes = Math.max(report.maximumHotBytes, hot.targets.memory.used)
      expect(hot.targets.memory.limit).toBe(10240)
      const facts = currentFacts(wave + 1)
      return { wave, hotBytes: hot.targets.memory.used, rows, currentFacts: facts,
        covered: facts.filter(fact => covered(fact, rows)).map(fact => fact.id), missing: facts.filter(fact => !covered(fact, rows)).map(fact => fact.id),
        forbiddenMatches: forbidden.filter(token => rows.some(row => row.content.includes(token))), reviews: coordinator.snapshot().reviews,
        lifecycle: parents.map(handle => lifecycle.snapshot(handle.agent.id).current) }
    }
    for (wave = 0; wave < waves; wave++) {
      await Promise.all(parents.map(async (handle, index) => {
        const domain = domains[index]!
        const prompt = task(domain, wave)
        report.workload.push({ domain, wave, prompt })
        const before = handle.agent.session.snapshotEvents().length
        handle.agent.followup(createUserMessage({ content: [{ type: 'text', text: prompt }], source: { kind: 'user' } }))
        await handle.agent.whenIdle()
        const assistant = handle.agent.session.snapshotEvents().slice(before).filter(event => event.type === 'assistant/message')
          .flatMap(event => ((event.data as unknown as { message?: { content?: Array<{ type: string; text?: string }> } }).message?.content ?? [])
            .filter(block => block.type === 'text').map(block => block.text ?? '')).join('\n')
        report.conversations.push({ domain, wave, assistant })
      }))
      await settleReviews()
      const snap = await snapshot()
      report.snapshots.push(snap)
      console.log(JSON.stringify({ scenario: report.scenario, wave, currentFacts: snap.currentFacts.length, covered: snap.covered.length,
        rows: snap.rows.length, hotBytes: snap.hotBytes, noise: snap.forbiddenMatches, reviews: snap.reviews, errors: report.toolErrors.length }))
    }
    for (const handle of parents) await handle.dispose()
    // Fresh sessions have no development transcript and cannot read developer files.
    const facts = currentFacts(waves)
    for (let offset = 0; offset < Math.max(...domains.map(domain => facts.filter(f => f.domain === domain).length)); offset += 3) {
      await Promise.all(domains.map(async domain => {
        const keys = facts.filter(f => f.domain === domain).slice(offset, offset + 3).map(f => f.key)
        if (!keys.length) return
        const id = `recall-${domain}-${offset}`
        const handle = await ctx.agents.create({ sessionId: SessionId(id), agentOptions: { provider: PROVIDER, model: MODEL, maxTokens: 8192 }, meta: { cwd: f.workspace } })
        answerRequests.set(id, { domain, keys, started: performance.now() })
        try {
          handle.agent.followup(createUserMessage({ content: [{ type: 'text', text: `请从 Orion 项目的可用记忆回答 ${domain}（${labels[domain]}）模块当前确定的这些配置值：${keys.join('、')}。不要用其他模块的值，也不要猜测；没有证据就写 unknown。本轮不要写入或更新记忆，不要读取开发配置文件，不要创建文档。调用 acceptance_answer 提交 answers，键为上述配置键，值仅填对应配置字符串，不要附加解释。` }], source: { kind: 'user' } }))
          await handle.agent.whenIdle()
          if (answerRequests.has(id)) {
            report.answers.push({ domain, requestedKeys: keys, answers: {}, elapsedMs: 0, sourceReads: sourceReads.get(id) ?? 0 })
            answerRequests.delete(id)
          }
        } finally { await handle.dispose() }
      }))
    }
    report.taskAgentsRemaining = ctx.agents.roots().length
    expect(report.taskAgentsRemaining).toBe(0)
    expect(await wire.drain()).toEqual([])
    expect(report.modelsReturned).toEqual([MODEL])
    expect(report.maxConcurrentModels).toBeGreaterThanOrEqual(4)
    report.finalEvaluation = evaluate(report)
    report.executionStatus = 'completed'
  } finally {
    try { await ctx.fiber.dispose(); stop?.() } finally {
      await wire.dispose()
      clearInterval(timer)
      if (report.executionStatus === 'running') report.executionStatus = 'failed'
      await f.dispose()
      report.elapsedMs = Math.round(performance.now() - started)
    }
  }
}

afterAll(() => {
  if (!enabled || !process.env.MNEMON_FLASH_QUALITY_REPORT) return
  const key = process.env.DEEPSEEK_API_KEY
  const testedCommit = execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim()
  const harnessSha256 = Object.fromEntries(harnessFiles.map(path => [path, createHash('sha256').update(readFileSync(path)).digest('hex')]))
  writeFileSync(process.env.MNEMON_FLASH_QUALITY_REPORT,
    JSON.stringify({ model: MODEL, testedCommit, harnessSha256, reports }, (_name, value: unknown) => {
      if (typeof value !== 'string') return value
      return fixtureRoots.reduce((text, root) => text.replaceAll(root, '[fixture]'), key ? value.replaceAll(key, '[credential]') : value)
    }, 2) + '\n')
})

describe.skipIf(!enabled)('automatic memory quality with real DeepSeek V4 Flash', () => {
  it('evaluates natural developer work across four sessions with one shared cold space', async () => { await runScenario(false) }, 1800000)
  it('evaluates the same work with four preconfigured topic spaces', async () => { await runScenario(true) }, 1800000)
})
