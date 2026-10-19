import type { CommandDefinition, CommandInvocation, CommandResult, CommandService } from "./dsh.ts"
import type { Insight, MemorySpacesStatus } from 'dsh-mnemon-source-memory-spaces/contracts'
import { agentScope, type MnemonAgentRuntimeSource } from './runtime.ts'
import type { MnemonSubagentCoordinator } from './subagent.ts'

const USAGE = '用法：/mnemon [status|recall <查询>|related <ID>|remember <内容>|forget <ID>]'

function error(text: string): CommandResult {
  return { kind: 'error', text: `${text}\n${USAGE}` }
}

function clip(value: string, max = 600): string {
  return value.length <= max ? value : `${value.slice(0, max - 1)}…`
}

function insightLine(insight: Insight, index: number): string {
  const meta = [
    insight.memoryBodyId === undefined ? undefined : `space=${insight.memoryBodyId}`,
    insight.category,
    insight.score === undefined ? undefined : `score=${insight.score.toFixed(3)}`,
    insight.depth === undefined ? undefined : `depth=${insight.depth}`,
  ].filter((value): value is string => value !== undefined).join(' · ')
  return `${index + 1}. ${clip(insight.content)}\n   ID: ${insight.id}${meta === '' ? '' : ` · ${meta}`}`
}

function splitInput(rawInput: string): { verb: string; argument: string } {
  const input = rawInput.trim()
  if (input === '') return { verb: 'status', argument: '' }
  const separator = input.search(/\s/u)
  return separator < 0
    ? { verb: input.toLowerCase(), argument: '' }
    : { verb: input.slice(0, separator).toLowerCase(), argument: input.slice(separator).trim() }
}

async function execute(serviceOrSource: MnemonAgentRuntimeSource, coordinator: MnemonSubagentCoordinator, invocation: CommandInvocation): Promise<CommandResult> {
  const graph = serviceOrSource.forAgent(invocation.agent)
  const source = graph.source('memory-spaces', agentScope(invocation.agent, graph.config))
  const { verb, argument } = splitInput(invocation.rawInput)
  switch (verb) {
    case 'status': {
      if (argument !== '') return error('status 不接受额外参数。')
      const status = await source.read<MemorySpacesStatus>('status', null, invocation.signal)
      if (!status.healthy) return { kind: 'error', text: `Mnemon 不可用：${status.error ?? '未知错误'}` }
      const stats = status.stats
      // Mnemon Native is optional: without its CLI the other providers still serve memory.
      const nativeInstalled = status.commandFound !== false
      return {
        kind: 'success',
        text: [
          nativeInstalled ? `Mnemon ${status.version ?? ''} · default=${status.mnemonDefaultStore}`.trim() : 'Mnemon Native: 未安装 CLI（可选，其他 Provider 不受影响）',
          `DSH 已激活: ${status.dshActiveStores.join(', ') || 'none'}`,
          ...(nativeInstalled ? [`CLI: ${status.cliPath}`] : []),
          `数据目录: ${status.dataDir}`,
          `有效记忆: ${stats?.totalInsights ?? 0} · 连接: ${stats?.edgeCount ?? 0} · 已删除: ${stats?.deletedInsights ?? 0}`,
          `模式: ${status.writeEnabled ? '读写' : '只读'} · 默认召回: ${status.defaultRecallLimit}`,
        ].join('\n'),
      }
    }
    case 'recall': {
      if (argument === '') return error('recall 需要一个明确查询。')
      const response = await coordinator.recall(invocation.agent, {
        query: argument,
        limit: Math.min(graph.config.defaultRecallLimit, 10),
      }, invocation.signal)
      if (response.results.length === 0) return { kind: 'success', text: `没有找到与“${argument}”相关的记忆。` }
      return { kind: 'success', text: `召回 ${response.results.length} 条：\n\n${response.results.map(insightLine).join('\n\n')}` }
    }
    case 'related': {
      if (argument === '') return error('related 需要 recall 返回的完整 ID。')
      const results = (await coordinator.related(invocation.agent, argument, undefined, invocation.signal)).results
      if (results.length === 0) return { kind: 'success', text: `ID ${argument} 的两跳内没有关联记忆。` }
      return { kind: 'success', text: `关联记忆 ${results.length} 条：\n\n${results.map(insightLine).join('\n\n')}` }
    }
    case 'remember': {
      if (!graph.config.writeEnabled) return { kind: 'error', text: 'Mnemon 当前为只读模式，不能写入记忆。' }
      if (argument === '') return error('remember 需要一条自包含的记忆内容。')
      const result = await coordinator.remember(invocation.agent, { content: argument, source: 'user' }, invocation.signal)
      return { kind: 'success', text: `Mnemon 记忆 Agent 已处理：${result.action}${result.memoryBodyIds.length === 0 ? '' : ` · 记忆空间 ${result.memoryBodyIds.join(', ')}`}${result.summary === '' ? '' : `\n${result.summary}`}` }
    }
    case 'forget': {
      if (!graph.config.writeEnabled) return { kind: 'error', text: 'Mnemon 当前为只读模式，不能删除记忆。' }
      if (argument === '' || /\s/u.test(argument)) return error('forget 需要一条记忆的精确 ID。')
      const result = await coordinator.write(invocation.agent, 'forget', { id: argument }, invocation.signal)
      if (result.action !== 'forgotten') return { kind: 'error', text: `Mnemon 未确认删除记忆：${argument}（${result.action}）${result.summary === '' ? '' : `\n${result.summary}`}` }
      return { kind: 'success', text: `已软删除 Mnemon 记忆：${argument}` }
    }
    default:
      return error(`未知 Mnemon 子命令：${verb}`)
  }
}

export function createMnemonCommand(service: MnemonAgentRuntimeSource, coordinator: MnemonSubagentCoordinator): CommandDefinition {
  return {
    name: 'mnemon',
    description: '查看、召回或管理 Mnemon 外置记忆',
    input: { hint: '[status|recall <查询>|related <ID>|remember <内容>|forget <ID>]' },
    handler: invocation => execute(service, coordinator, invocation).catch((reason: unknown) => ({
      kind: 'error',
      text: reason instanceof Error ? reason.message : String(reason),
    })),
  }
}

export function registerCommands(commands: CommandService, service: MnemonAgentRuntimeSource, coordinator: MnemonSubagentCoordinator): void {
  commands.register(createMnemonCommand(service, coordinator))
}
