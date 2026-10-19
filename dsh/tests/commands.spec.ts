import { describe, expect, it, vi } from 'vitest'
import { createMnemonCommand } from "../src/host/commands.ts"
import type { HostAgent } from "../src/host/dsh.ts"
import { resolveConfig } from '../src/host/config.ts'
import type { MnemonAgentRuntimeSource, MnemonRuntimeGraph } from '../src/host/runtime.ts'
import type { MnemonSubagentCoordinator } from "../src/host/subagent.ts"
import { MemoryExecutions } from '../src/host/memory-executions.ts'

function runtime(fixture: { config: { writeEnabled: boolean; defaultRecallLimit: number }; status?: () => Promise<unknown> }): MnemonAgentRuntimeSource {
  const config = resolveConfig(fixture.config)
  const source = {
    config,
    forAgent: () => ({ config, source: () => ({ read: fixture.status }) }) as unknown as MnemonRuntimeGraph,
    bindAgentRuntime: () => () => {},
  }
  return { ...source, executions: new MemoryExecutions(source) }
}

const agent = { id: 'session-1', session: { header: {} } } as HostAgent
function invocation(rawInput: string) {
  return { agent, rawInput, signal: new AbortController().signal }
}

function coordinator(overrides: Partial<MnemonSubagentCoordinator> = {}): MnemonSubagentCoordinator {
  return {
    recall: vi.fn(async (_agent, request) => ({ query: request.query, mode: 'smart', results: [] })),
    related: vi.fn(async () => ({ query: 'related', mode: 'related', results: [] })),
    remember: vi.fn(async () => ({ delegated: true, runId: 'child-1', provider: 'spawn', summary: '', action: 'stored', memoryBodyIds: ['project'] })),
    write: vi.fn(async () => ({ delegated: true, runId: 'child-1', provider: 'spawn', summary: '', action: 'forgotten', memoryBodyIds: ['project'] })),
    ...overrides,
  } as unknown as MnemonSubagentCoordinator
}

describe('/mnemon command', () => {
  it('renders status without involving the model', async () => {
    const service = {
      config: { writeEnabled: true, defaultRecallLimit: 10 },
      status: vi.fn(async () => ({
        healthy: true,
        version: '0.1.2',
        cliPath: '/usr/local/bin/mnemon',
        dataDir: '/tmp/mnemon',
        store: 'project',
        mnemonDefaultStore: 'default',
        dshActiveStores: ['project'],
        writeEnabled: true,
        defaultRecallLimit: 10,
        stats: { totalInsights: 3, edgeCount: 2, deletedInsights: 1 },
      })),
    }
    const result = await createMnemonCommand(runtime(service), coordinator()).handler(invocation('status'))
    expect(result).toEqual(expect.objectContaining({
      kind: 'success',
      text: expect.stringMatching(/default=default[\s\S]*DSH 已激活: project/u),
    }))
    expect(service.status).toHaveBeenCalledOnce()
  })

  it('reports Mnemon Native as optional when its CLI is missing', async () => {
    const service = {
      config: { writeEnabled: true, defaultRecallLimit: 10 },
      status: vi.fn(async () => ({
        healthy: true, commandFound: false, cliPath: 'mnemon', dataDir: '/tmp/mnemon', mnemonDefaultStore: 'default',
        dshActiveStores: ['mem0-notes'], writeEnabled: true, defaultRecallLimit: 10, stats: { totalInsights: 4, edgeCount: 0, deletedInsights: 0 },
      })),
    }
    const result = await createMnemonCommand(runtime(service), coordinator()).handler(invocation('status'))
    expect(result).toEqual(expect.objectContaining({ kind: 'success', text: expect.stringMatching(/^Mnemon Native: 未安装 CLI[\s\S]*DSH 已激活: mem0-notes[\s\S]*有效记忆: 4/u) }))
    expect((result as { text: string }).text).not.toContain('CLI: mnemon')
  })

  it('runs a bounded recall and includes full ids', async () => {
    const service = {
      config: { writeEnabled: true, defaultRecallLimit: 20 },
    }
    const memoryCoordinator = coordinator({
      recall: vi.fn(async () => ({ query: '为什么使用 SQLite', mode: 'smart', results: [{ id: 'memory-full-id', content: '选择 SQLite 以便本地优先', score: 0.8, memoryBodyId: 'project' }] })),
    })
    const result = await createMnemonCommand(runtime(service), memoryCoordinator).handler(invocation('recall 为什么使用 SQLite'))
    expect(memoryCoordinator.recall).toHaveBeenCalledWith(agent, { query: '为什么使用 SQLite', limit: 10 }, expect.any(AbortSignal))
    expect(result).toEqual(expect.objectContaining({ kind: 'success', text: expect.stringContaining('memory-full-id') }))
  })

  it('rejects mutation subcommands in read-only mode', async () => {
    const remember = vi.fn()
    const service = { config: { writeEnabled: false, defaultRecallLimit: 10 }, remember }
    const result = await createMnemonCommand(runtime(service), coordinator()).handler(invocation('remember 永久记住这条'))
    expect(result).toEqual({ kind: 'error', text: 'Mnemon 当前为只读模式，不能写入记忆。' })
    expect(remember).not.toHaveBeenCalled()
  })

  it('returns the memory subagent write receipt', async () => {
    const service = {
      config: { writeEnabled: true, defaultRecallLimit: 10 },
    }
    const result = await createMnemonCommand(runtime(service), coordinator()).handler(invocation('remember 一条稳定记忆'))
    expect(result).toEqual({ kind: 'success', text: 'Mnemon 记忆 Agent 已处理：stored · 记忆空间 project' })
  })

  it('reports an explicit forget only after a forgotten receipt', async () => {
    const memoryCoordinator = coordinator()
    const result = await createMnemonCommand(runtime({ config: { writeEnabled: true, defaultRecallLimit: 10 } }), memoryCoordinator)
      .handler(invocation('forget memory-exact-id'))
    expect(memoryCoordinator.write).toHaveBeenCalledWith(agent, 'forget', { id: 'memory-exact-id' }, expect.any(AbortSignal))
    expect(result).toEqual({ kind: 'success', text: '已软删除 Mnemon 记忆：memory-exact-id' })
  })

  it.each(['skipped', 'failed', 'accepted', 'unknown'])('does not report deletion after a %s worker receipt', async action => {
    const memoryCoordinator = coordinator({ write: vi.fn(async () => ({
      delegated: true as const, runId: 'child-1', provider: 'spawn', summary: 'No deletion was committed.', action, memoryBodyIds: [],
    })) })
    const result = await createMnemonCommand(runtime({ config: { writeEnabled: true, defaultRecallLimit: 10 } }), memoryCoordinator)
      .handler(invocation('forget memory-exact-id'))
    expect(result.kind).toBe('error')
    expect(result.text).toContain('No deletion was committed.')
    expect(result.text).not.toContain('已软删除')
  })
})
