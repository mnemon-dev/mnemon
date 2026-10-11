import type { MemoryJsonValue, MemorySourceDefinition } from 'dsh-mnemon/contracts'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { defineMemorySource, createMemoryMutationReceipt as receipt, memoryInputRecord as record, memoryInputStringArray as stringArray, memoryInputText as text, truncateMemoryText as truncate } from 'dsh-mnemon/extension-sdk'
import { resolveGitBranch } from './git-branch.ts'
import { RuntimeMemoryController } from './controller.ts'
import type { RuntimeMemoryAction, RuntimeMemoryCompactedEntry, RuntimeMemoryImportance, RuntimeMemoryMutation, RuntimeMemoryTarget } from './contracts.ts'
import { runtimeSourceConfig, type Config } from './config.ts'

const ACTIONS = new Set<RuntimeMemoryAction>(['add', 'replace', 'remove'])
const TARGETS = new Set<RuntimeMemoryTarget>(['memory', 'user'])
const IMPORTANCE = new Set<RuntimeMemoryImportance>(['critical', 'normal', 'low'])

function runtimeMutation(value: MemoryJsonValue): RuntimeMemoryMutation {
  const input = record(value, 'Runtime Memory mutation')
  const action = text(input.action, 'action', 20) as RuntimeMemoryAction
  const target = text(input.target, 'target', 20) as RuntimeMemoryTarget
  if (!ACTIONS.has(action)) throw new Error(`unsupported Runtime Memory action: ${action}`)
  if (!TARGETS.has(target)) throw new Error(`unsupported Runtime Memory target: ${target}`)
  const importance = text(input.importance, 'importance', 20, false) as RuntimeMemoryImportance | undefined
  if (importance !== undefined && !IMPORTANCE.has(importance)) throw new Error(`unsupported Runtime Memory importance: ${importance}`)
  return {
    action,
    target,
    ...(text(input.content, 'content', 100_000, false) === undefined ? {} : { content: text(input.content, 'content', 100_000, false)! }),
    ...(text(input.oldText ?? input.old_text, 'oldText', 100_000, false) === undefined ? {} : { oldText: text(input.oldText ?? input.old_text, 'oldText', 100_000, false)! }),
    ...(importance === undefined ? {} : { importance }),
    ...(input.branches === undefined ? {} : { branches: stringArray(input.branches, 'branches', 100) ?? [] }),
  }
}

export function createRuntimeMemorySource(config: Config = {}): MemorySourceDefinition {
 const configured = Object.freeze({ ...config })
 return defineMemorySource({
  manifest: {
    apiVersion: COMPOSABLE_MEMORY_API_VERSION,
    kind: 'source',
    typeId: 'runtime',
    packageName: 'dsh-mnemon-source-runtime',
    role: 'working-context',
    capabilities: ['status', 'project', 'write'],
    consistency: 'exact-snapshot',
    actions: [{
      id: 'mutate',
      description: 'Add, replace, or remove an entry in Runtime Memory.',
      capability: 'write',
      inputSchema: {
        type: 'object',
        required: ['action', 'target'],
        additionalProperties: false,
        properties: {
          action: { type: 'string', enum: ['add', 'replace', 'remove'] },
          target: { type: 'string', enum: ['memory', 'user'] },
          content: { type: 'string' },
          oldText: { type: 'string', description: 'Full content of the existing entry, or a unique substring, excluding the generated metadata line. A unique full-content match takes precedence within the selected target. Required for replace and remove.' },
          importance: { type: 'string', enum: ['critical', 'normal', 'low'] },
          branches: { type: 'array' },
        },
      },
    }],
    management: {
      label: 'Runtime Memory',
      description: 'Exact, bounded working context and user profile projection.',
    },
  },
  create(context) {
    const effective = runtimeSourceConfig({ ...context.configuration, ...configured }, context.sourceInstanceKey)
    const controller = new RuntimeMemoryController(
      { effectiveDataDir: () => effective.dataDir }, undefined,
      { memory: effective.memoryLimitBytes, user: effective.userLimitBytes },
      { effectiveDataDir: () => effective.userDataDir },
    )
    const projection = (workspaceId?: string) => controller.contextProjection(resolveGitBranch(workspaceId))
    const prepared = new WeakMap<object, ReturnType<typeof projection>>()
    return {
      facts(request) {
        if (request.scenario.startsWith('management.')) return {
          sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'runtime', role: 'working-context', availability: 'ready',
          revision: controller.snapshot().revision, capabilities: ['status', 'project', 'write'], routeIds: [], actionIds: ['mutate'],
        }
        const current = projection(request.scope.workspaceId)
        prepared.set(request.scope, current)
        return {
          sourceInstanceKey: context.sourceInstanceKey,
          sourceTypeId: 'runtime',
          role: 'working-context',
          availability: 'ready',
          revision: current.revision,
          capabilities: ['status', 'project', 'write'],
          routeIds: [],
          actionIds: ['mutate'],
        }
      },
      project(request) {
        if (!request.includeProjection) return { fragments: [] }
        const current = prepared.get(request.scope) ?? projection(request.scope.workspaceId)
        prepared.delete(request.scope)
        if (current.revision !== request.expectedRevision) throw new Error('Runtime projection revision changed during composition')
        return {
          fragments: [{
            id: `${context.sourceInstanceKey}/projection`,
            sourceInstanceKey: context.sourceInstanceKey,
            mode: request.mode,
            text: truncate(current.text, request.maxCharacters),
            revision: current.revision,
            provenance: { sourceTypeId: 'runtime' },
          }],
          presentation: {
            visibleItems: current.entries.length,
            totalItems: current.totalEntries,
            items: current.entries.slice(0, 24).map((entry, index) => ({
              id: `${entry.target}:${entry.created_at}:${index}`,
              title: truncate(entry.content, 160),
            })),
          },
        }
      },
      async manage(request) {
        const input = request.input === null ? {} : record(request.input, 'Runtime Memory management')
        if (request.mode === 'read') {
          let value: unknown
          if (request.operation === 'snapshot') value = controller.snapshot()
          else if (request.operation === 'maintenance-plan') value = await controller.planMaintenance(runtimeMutation(request.input))
          else throw new Error('unsupported Runtime management read operation: ' + request.operation)
          return { revision: controller.snapshot().revision, value: value as MemoryJsonValue }
        }
        if (!request.confirmed) throw new Error('Runtime management mutation requires explicit confirmation')
        let result: unknown
        if (request.operation === 'mutate') result = await controller.mutate(runtimeMutation(request.input))
        else if (request.operation === 'compact-and-mutate') {
          if (!Array.isArray(input.compacted)) throw new Error('compacted must be an array')
          const compacted = input.compacted.map(value => {
            const entry = record(value, 'compacted entry')
            const importance = text(entry.importance, 'importance', 20)! as RuntimeMemoryImportance
            if (!IMPORTANCE.has(importance)) throw new Error('invalid compacted importance')
            return {
              content: text(entry.content, 'content', 100_000)!, importance,
              ...(entry.branches === undefined ? {} : { branches: stringArray(entry.branches, 'branches', 100) ?? [] }),
            } satisfies RuntimeMemoryCompactedEntry
          })
          result = await controller.compactAndMutate(
            text(input.revision, 'revision', 300)!, runtimeMutation(input.mutation!), compacted,
            typeof input.maxBytes === 'number' ? input.maxBytes : undefined,
          )
        } else throw new Error('unsupported Runtime management mutation operation: ' + request.operation)
        return { revision: controller.snapshot().revision, value: result as unknown as MemoryJsonValue }
      },
      async mutate(request) {
        const result = await controller.mutate(runtimeMutation(request.input))
        const revision = controller.snapshot().revision
        return receipt(request.view.id, request.offer.id, context.sourceInstanceKey, revision, result as unknown as MemoryJsonValue, 'committed')
      },
    }
  },
 })
}

export const RUNTIME_MEMORY_SOURCE = createRuntimeMemorySource()
