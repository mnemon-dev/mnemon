import type { HostContextShape, ToolDefinition, ToolExecution } from './dsh.ts'
import type { DocumentMutation } from 'dsh-mnemon-source-documents/contracts'
import type { RuntimeMemoryImportance, RuntimeMemoryTarget } from 'dsh-mnemon-source-runtime/contracts'
import type { Category, EdgeType, Source } from 'dsh-mnemon-source-memory-spaces/contracts'
import { CATEGORIES, EDGE_TYPES, SOURCES } from './protocol.ts'
import { assertParticipation } from './access.ts'
import type { MemoryCapability, MemoryJsonValue } from '../core/contracts/index.ts'
import { agentScope, type MnemonAgentRuntimeSource, type MnemonRuntimeGraph } from './runtime.ts'
import { isSubagent, MnemonSubagentCoordinator } from './subagent.ts'
import { memoryReadPresentation, memoryWritePresentation } from './activity-presentation.ts'

const text = (value: unknown): Array<{ type: 'text'; text: string }> => [{
  type: 'text',
  text: typeof value === 'string' ? value : JSON.stringify(value, null, 2),
}]

function definition(value: ToolDefinition): ToolDefinition {
  return value
}

// DSH tool outputs use its supported JSON Schema subset. `type: "json"` is
// valid in the parameter DSL, but it is not a JSON Schema type.
const JSON_OBJECT_OUTPUT = { type: 'object', additionalProperties: true } as const
/** Register a deliberately small model-facing surface over Mnemon's protocol. */
function requireAgent(exec: ToolExecution) {
  if (exec.agent === undefined) throw new Error('Mnemon semantic operations require a live DSH agent')
  return exec.agent
}

/** Named product tools are views over the same Source Route/Action protocol. */
export function registerTools(ctx: HostContextShape, runtimeSource: MnemonAgentRuntimeSource, coordinator: MnemonSubagentCoordinator): void {
  const runtimeFor = (exec: ToolExecution): MnemonRuntimeGraph => runtimeSource.forAgent(requireAgent(exec))
  const sourceFor = (exec: ToolExecution, typeId: string) => {
    const graph = runtimeFor(exec)
    return graph.source(typeId, agentScope(requireAgent(exec), graph.config))
  }
  const requireSource = (exec: ToolExecution, typeId: string, capability: MemoryCapability): MnemonRuntimeGraph => {
    const graph = runtimeFor(exec)
    assertParticipation(graph.config, typeId, capability, 'automatic')
    if (['write', 'archive', 'link', 'forget', 'import'].includes(capability) && !runtimeSource.config.writeEnabled) throw new Error('dsh-mnemon is configured read-only')
    return graph
  }
  const config = runtimeSource.config
  const composableTurn = (exec: ToolExecution) => {
    const agent = requireAgent(exec)
    const graph = runtimeFor(exec)
    const manager = graph.composableTurns
    const turn = manager.activeTurn(agentScope(agent, graph.config).agentId!)
    if (turn === undefined) throw new Error('Composable Memory View is not pinned to the current turn')
    return { manager, turn }
  }
  const sourceAction = async (exec: ToolExecution, typeId: string, actionId: string, input: unknown) => {
    const receipt = await sourceFor(exec, typeId).action(actionId, input, offer => runtimeSource.config.writeEnabled && offer.authority === undefined, exec.signal)
    const details = receipt.details
    const result = typeof details === 'object' && details !== null && !Array.isArray(details) && 'result' in details ? details.result : details
    // Keep familiar product fields, but never erase accepted/candidate/unknown completion for the model.
    return { ...(typeof result === 'object' && result !== null && !Array.isArray(result) ? result : { result }),
      memoryReceipt: { status: receipt.status, completion: receipt.completion, ...(receipt.committedAt === undefined ? {} : { committedAt: receipt.committedAt }) },
    }
  }

  ctx.tools.register(definition({
    name: 'mnemon_view_route',
    description: 'Execute one exact Route offered in the current MNEMON VIEW ROUTES envelope. Use only ids present in that envelope; the Host binds the request to its hidden ReadGrant, enforces call/result/character budgets, and returns Evidence.',
    parameters: {
      type: 'object',
      properties: {
        routeId: { type: 'string', description: 'Exact View Route id.' },
        input: { type: 'object', additionalProperties: true, description: 'Route-specific JSON input described by the offered Route.' },
      },
      required: ['routeId', 'input'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryReadPresentation(undefined, 'view-route', 'routeId') },
    execute: async (args: { routeId: string; input: MemoryJsonValue }, exec: ToolExecution) => {
      const { manager, turn } = composableTurn(exec)
      const evidence = await manager.executeRoute(turn.turnId, args.routeId, args.input, exec.signal)
      return evidence.output ?? evidence
    },
    presentCall: (args: { routeId: string }) => ({ card: 'generic', title: 'Query composable memory', kind: 'search', rawInput: args.routeId }),
    presentResult: () => ({ card: 'generic', title: 'Composable memory evidence ready' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_view_action',
    description: 'Execute one exact Action offered in the current MNEMON VIEW ROUTES envelope. The Host rechecks write policy and authority at call time and returns a mutation Receipt; an offer is never itself authorization.',
    parameters: {
      type: 'object',
      properties: {
        offerId: { type: 'string', description: 'Exact View ActionOffer id.' },
        input: { type: 'object', additionalProperties: true, description: 'Action-specific JSON input described by the offered Action.' },
      },
      required: ['offerId', 'input'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation(undefined, 'view-action', 'offerId') },
    execute: (args: { offerId: string; input: MemoryJsonValue }, exec: ToolExecution) => {
      if (!config.writeEnabled) throw new Error('dsh-mnemon is configured read-only (writeEnabled: false)')
      return coordinator.action(requireAgent(exec), args.offerId, args.input, exec.signal)
    },
    presentCall: (args: { offerId: string }) => ({ card: 'generic', title: 'Apply composable memory action', kind: 'edit', rawInput: args.offerId }),
    presentResult: () => ({ card: 'generic', title: 'Composable memory receipt ready' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_memory_bodies',
    description: 'Inspect a bounded Memory Space catalog with ids, routing, capabilities, activation, and health; paths, settings, and statistics are omitted. Use only for explicit space inspection or management, or before a capability-dependent write. Never call it to route Recall: omit memoryBodyIds and mnemon_recall searches every pinned active space.',
    parameters: { type: 'object', properties: {} },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value) },
    async execute(_args: unknown, exec: ToolExecution) {
      requireSource(exec, 'memory-spaces', 'status')
      const evidence = await sourceFor(exec, 'memory-spaces').route('inspect', { section: 'directory' }, exec.signal)
      return evidence.output ?? evidence
    },
    presentCall: () => ({ card: 'generic', title: 'Inspect Mnemon Memory Spaces', kind: 'search' }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon Memory Spaces ready' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_recall',
    description: 'Recall bounded durable evidence from this turn\'s pinned MemorySource only when the current question needs history. The Host allows one initial query plus one LLM-chosen different-query refinement, shares one evidence envelope, validates Memory Spaces, ignores brittle semantic filters, and replays duplicate queries. Omit memoryBodyIds to search every pinned active space.',
    parameters: {
      type: 'object',
      properties: {
        query: { type: 'string', description: 'Focused natural-language memory query.' },
        mode: { type: 'string', enum: ['smart', 'keyword', 'basic'], description: 'smart=graph-enhanced default, keyword=token ranking, basic=SQL LIKE fallback.' },
        limit: { type: 'integer', description: 'Maximum number of results. The model path caps output at 6.' },
        memoryBodyIds: { type: 'array', items: { type: 'string' }, description: 'Optional known active Memory Space ids to narrow recall. Omit this field to search every pinned active space; do not list the catalog only to populate it. The Host rejects ids outside the pinned Source.' },
      },
      required: ['query'],
    },
    output: {
      schema: JSON_OBJECT_OUTPUT,
      render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryReadPresentation('memory-spaces', 'recall'),
    },
    async execute(args: { query: string; mode?: 'smart' | 'keyword' | 'basic'; limit?: number; memoryBodyIds?: string[] }, exec: ToolExecution) {
      requireSource(exec, 'memory-spaces', 'recall')
      const agent = requireAgent(exec)
      return coordinator.recall(agent, args, exec.signal, { requirePinnedView: true })
    },
    presentCall: (args: { query: string }) => ({ card: 'generic', title: 'Recall Mnemon memory', kind: 'search', rawInput: args.query }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon recall complete' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_related',
    description: 'Traverse one insight admitted by this turn\'s mnemon_recall. At most one traversal is allowed per turn; use it only when the owning Memory Space reports capabilities.related=true and graph neighbors materially help. OpenViking does not currently support this operation.',
    parameters: {
      type: 'object',
      properties: {
        id: { type: 'string', description: 'Insight id returned by mnemon_recall.' },
        depth: { type: 'integer', description: 'Traversal depth. The service accepts 1 through 5.' },
        edge: { type: 'string', enum: [...EDGE_TYPES] },
        memoryBodyId: { type: 'string', description: 'Active Memory Space that returned this insight id.' },
      },
      required: ['id'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryReadPresentation('memory-spaces', 'related') },
    async execute(args: { id: string; depth?: number; edge?: EdgeType; memoryBodyId?: string }, exec: ToolExecution) {
      requireSource(exec, 'memory-spaces', 'related')
      const agent = requireAgent(exec)
      return coordinator.related(agent, args.id, args.memoryBodyId, exec.signal, {
        ...(args.depth === undefined ? {} : { depth: args.depth }),
        ...(args.edge === undefined ? {} : { edge: args.edge }),
        requirePinnedView: true,
      })
    },
    presentCall: (args: { id: string }) => ({ card: 'generic', title: 'Traverse Mnemon graph', kind: 'search', rawInput: args.id }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon graph traversal complete' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_status',
    description: 'Check a bounded health summary for memory-provider integrations and Memory Spaces. Use only when a memory operation fails or the user asks about memory health; full paths, settings, directories, and per-Space statistics stay in the control plane.',
    parameters: { type: 'object', properties: {} },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value) },
    async execute(_args: unknown, exec: ToolExecution) {
      requireSource(exec, 'memory-spaces', 'status')
      const evidence = await sourceFor(exec, 'memory-spaces').route('inspect', { section: 'health' }, exec.signal)
      return evidence.output ?? evidence
    },
    presentCall: () => ({ card: 'generic', title: 'Check Mnemon status', kind: 'other' }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon status checked' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_document_search',
    description: 'Run one focused search over project-scoped managed Documents before durable Recall. The Host admits only one Documents query per executing Agent turn; parallel calls share that slot, while child turns have independent budgets. Results contain bounded query-local evidence, not complete records. Cold archives are excluded unless a known archive reference requires them.',
    parameters: {
      type: 'object',
      properties: {
        query: { type: 'string', description: 'Focused natural-language or keyword query. Empty lists recent documents.' },
        includeArchived: { type: 'boolean', description: 'Include cold archived originals only for explicit deep-reference inspection.' },
        limit: { type: 'integer', description: 'Maximum results, 1 through 4 for model calls.' },
      },
      required: ['query'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryReadPresentation('documents', 'search') },
    async execute(args: { query: string; includeArchived?: boolean; limit?: number }, exec: ToolExecution) {
      const agent = requireAgent(exec)
      requireSource(exec, 'documents', 'search')
      return coordinator.documentQuery(agent, args, exec.signal)
    },
    presentCall: (args: { query: string }) => ({ card: 'generic', title: 'Search Mnemon Documents', kind: 'search', rawInput: args.query }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon Documents ready' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_document_create',
    description: 'Create one new managed project Document without updating or archiving existing documents. Search first and skip duplicates; save only substantial new reusable project knowledge, with references to related document ids. Capacity exhaustion rejects creation and preserves existing documents.',
    parameters: {
      type: 'object', additionalProperties: false,
      properties: {
        title: { type: 'string', description: 'Meaningful project-document title.' },
        description: { type: 'string', description: 'Concise routing description.' },
        content: { type: 'string', description: 'New managed Markdown body.' },
        sourcePaths: { type: 'array', items: { type: 'string' }, description: 'Read-only source paths relative to the workspace.' },
      },
      required: ['title', 'content'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('documents', 'create') },
    execute: (args: { title: string; description?: string; content: string; sourcePaths?: string[] }, exec: ToolExecution) => {
      requireSource(exec, 'documents', 'write')
      return sourceAction(exec, 'documents', 'create', { ...args, sessionIds: [requireAgent(exec).id] })
    },
    presentCall: (args: { title: string }) => ({ card: 'generic', title: 'Create Mnemon Document', kind: 'edit', rawInput: args.title }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon Document created' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_document_manage',
    description: 'Create or update one managed project Document through the Mnemon Documents control plane. Use for substantial reusable project knowledge, not user-profile preferences, routine progress, raw transcripts, secrets, or small hot-memory facts. Source paths are references inside the workspace and are never edited. Archive is allowed only from a root request and first writes a durable Mnemon cold-reference through an isolated subagent.',
    parameters: {
      type: 'object',
      properties: {
        action: { type: 'string', enum: ['create', 'update', 'archive'] },
        id: { type: 'string', description: 'Required for update and archive.' },
        title: { type: 'string', description: 'Meaningful project-document title. Required for create.' },
        description: { type: 'string', description: 'Concise routing description.' },
        content: { type: 'string', description: 'Managed Markdown body. Required for create.' },
        sourcePaths: { type: 'array', items: { type: 'string' }, description: 'Read-only source paths relative to the workspace.' },
      },
      required: ['action'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('documents', 'manage') },
    execute: (args: { action: 'create' | 'update' | 'archive'; id?: string; title?: string; description?: string; content?: string; sourcePaths?: string[] }, exec: ToolExecution) => {
      if (!config.writeEnabled) throw new Error('dsh-mnemon is configured read-only (writeEnabled: false)')
      const agent = requireAgent(exec)
      if (args.action === 'archive') {
        requireSource(exec, 'documents', 'archive')
        requireSource(exec, 'memory-spaces', 'write')
        if (isSubagent(agent)) throw new Error('idle document workers cannot cold-archive directly')
        if (args.id === undefined) throw new Error('document id is required for archive')
        return coordinator.archiveDocument(agent, args.id, exec.signal)
      }
      requireSource(exec, 'documents', 'write')
      const request: DocumentMutation = args.action === 'create'
        ? { action: 'create', title: args.title ?? '', content: args.content ?? '', ...(args.description === undefined ? {} : { description: args.description }), ...(args.sourcePaths === undefined ? {} : { sourcePaths: args.sourcePaths }), sessionIds: [agent.id] }
        : { action: 'update', id: args.id ?? '', ...(args.title === undefined ? {} : { title: args.title }), ...(args.description === undefined ? {} : { description: args.description }), ...(args.content === undefined ? {} : { content: args.content }), ...(args.sourcePaths === undefined ? {} : { sourcePaths: args.sourcePaths }), sessionIds: [agent.id] }
      return isSubagent(agent) ? sourceAction(exec, 'documents', 'manage', request) : coordinator.document(agent, request, exec.signal)
    },
    presentCall: (args: { action: string; title?: string }) => ({ card: 'generic', title: `${args.action} Mnemon Document`, kind: 'edit', ...(args.title === undefined ? {} : { rawInput: args.title }) }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon Document processed' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_runtime_memory',
    description: 'Maintain compact hot memory injected into future turns. Write only new reusable facts supplied or corrected by the user, or information the user explicitly asks to save. Never copy, promote, or summarize evidence returned by Documents, Recall, or Related unless the user explicitly asks to save that exact evidence; answering a read question must stay read-only. add creates one independent fact; replace corrects or consolidates one uniquely matched entry; remove is only for an explicitly withdrawn, obsolete, or wrong entry. target=user is only for who the user is; target=memory is for project/environment/decisions/lessons. Skip questions, guesses, assistant-authored claims, temporary progress, completed-work logs, raw dumps, secrets, rediscoverable facts, and skill-covered guidance. This tool exclusively writes runtime MEMORY.md and USER.md; capacity archival and compaction are automatic. Optional branches (git branch names, target=memory only) project an entry only in sessions on those branches: use them for branch-specific decisions and experiments, tag new branch-scoped entries with the git branch reported in the snapshot header, omit for cross-branch facts; on replace an empty list clears the scope, and an omitted list keeps the current scope.',
    parameters: {
      type: 'object',
      properties: {
        action: { type: 'string', enum: ['add', 'replace', 'remove'], description: 'add a new entry, replace one uniquely matched entry, or remove one uniquely matched entry.' },
        target: { type: 'string', enum: ['memory', 'user'], description: 'user for user identity/preferences; memory for project, environment, decisions, and lessons.' },
        content: { type: 'string', description: 'Compact entry content. Required for add and replace.' },
        old_text: { type: 'string', description: 'Full content of the existing entry, or a unique substring. A unique full-content match takes precedence within the selected target. Required for replace and remove.' },
        importance: { type: 'string', enum: ['critical', 'normal', 'low'], description: 'critical for explicit must/always/never rules; low for transient facts; normal by default.' },
        branches: { type: 'array', items: { type: 'string' }, description: 'Optional git branch names restricting where a target=memory entry is injected. Omit for cross-branch facts; on replace an empty list clears the scope and an omitted list keeps it. For target=user, omit or pass []; non-empty branches are rejected.' },
      },
      required: ['action', 'target'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('runtime', 'mutate') },
    execute: (args: { action: 'add' | 'replace' | 'remove'; target: RuntimeMemoryTarget; content?: string; old_text?: string; importance?: RuntimeMemoryImportance; branches?: string[] }, exec: ToolExecution) => {
      if (!config.writeEnabled) throw new Error('dsh-mnemon is configured read-only (writeEnabled: false)')
      requireSource(exec, 'runtime', 'write')
      composableTurn(exec)
      const request = {
        action: args.action,
        target: args.target,
        ...(args.content === undefined ? {} : { content: args.content }),
        ...(args.old_text === undefined ? {} : { oldText: args.old_text }),
        ...(args.importance === undefined ? {} : { importance: args.importance }),
        ...(args.branches === undefined || (args.target === 'user' && Array.isArray(args.branches) && args.branches.length === 0) ? {} : { branches: args.branches }),
      }
      return coordinator.runtime(requireAgent(exec), request, exec.signal)
    },
    presentCall: (args: { action: string; target: string }) => ({ card: 'generic', title: `${args.action} runtime ${args.target} memory`, kind: 'edit' }),
    presentResult: () => ({ card: 'generic', title: 'Runtime memory updated' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_remember',
    description: 'Archive one durable insight in a selected provider-backed Memory Space. Ordinary new hot memory belongs in mnemon_runtime_memory; use direct archival only for explicit long-term persistence or runtime capacity migration. Choose the narrowest existing space, search it first, verify capabilities.remember=true, and wait for the provider receipt. Exact writes require confirmed persistence; providers using semantic extraction may truthfully return skipped. Do not dump transcripts, temporary progress, routine observations, or repository-obvious facts.',
    parameters: {
      type: 'object',
      properties: {
        content: { type: 'string', description: 'One concise, self-contained durable insight.' },
        category: { type: 'string', enum: [...CATEGORIES] },
        importance: { type: 'integer', description: 'Durable value from 1 through 5.' },
        tags: { type: 'array', items: { type: 'string' }, description: 'At most 20 concise tags.' },
        entities: { type: 'array', items: { type: 'string' }, description: 'At most 50 named entities.' },
        source: { type: 'string', enum: [...SOURCES], description: 'Defaults to agent for model-authored writeback.' },
        memoryBodyId: { type: 'string', description: 'Target Memory Space id. Required unless exactly one space is active.' },
      },
      required: ['content'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('memory-spaces', 'remember') },
    async execute(args: { content: string; category?: Category; importance?: number; tags?: string[]; entities?: string[]; source?: Source; memoryBodyId?: string }, exec: ToolExecution) {
      requireSource(exec, 'memory-spaces', 'write')
      const request = { ...args, source: args.source ?? 'agent' }
      return isSubagent(exec.agent)
        ? sourceAction(exec, 'memory-spaces', 'remember', request)
        : coordinator.remember(requireAgent(exec), request, exec.signal)
    },
    presentCall: () => ({ card: 'generic', title: 'Write Mnemon memory', kind: 'edit' }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon memory processed' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_link',
    description: 'Create a typed, bidirectional relation between two known insights in one Memory Space. Use only when its provider reports capabilities.link=true (currently Mnemon Native), the relation improves future recall, and both ids were verified through recall or graph traversal.',
    parameters: {
      type: 'object',
      properties: {
        sourceId: { type: 'string' },
        targetId: { type: 'string' },
        type: { type: 'string', enum: [...EDGE_TYPES] },
        weight: { type: 'number', description: 'Relationship confidence from 0 through 1.' },
        reason: { type: 'string' },
        memoryBodyId: { type: 'string', description: 'Body containing both insight ids.' },
      },
      required: ['sourceId', 'targetId'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('memory-spaces', 'link') },
    async execute(args: { sourceId: string; targetId: string; type?: EdgeType; weight?: number; reason?: string; memoryBodyId?: string }, exec: ToolExecution) {
      requireSource(exec, 'memory-spaces', 'link')
      return isSubagent(exec.agent)
        ? sourceAction(exec, 'memory-spaces', 'link', args)
        : coordinator.write(requireAgent(exec), 'link', args, exec.signal)
    },
    presentCall: () => ({ card: 'generic', title: 'Link Mnemon insights', kind: 'edit' }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon insights linked' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_forget',
    description: 'Forget one insight by exact id only when its provider reports capabilities.forget=true (currently Mnemon Native soft-delete). This is a destructive semantic operation; use only when the user explicitly asks or the insight is verified obsolete or incorrect.',
    parameters: {
      type: 'object',
      properties: { id: { type: 'string' }, memoryBodyId: { type: 'string', description: 'Body containing the insight id.' } },
      required: ['id'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('memory-spaces', 'forget') },
    execute: (args: { id: string; memoryBodyId?: string }, exec: ToolExecution) => {
      requireSource(exec, 'memory-spaces', 'forget')
      return isSubagent(exec.agent)
        ? sourceAction(exec, 'memory-spaces', 'forget', args)
        : coordinator.write(requireAgent(exec), 'forget', args, exec.signal)
    },
    presentCall: (args: { id: string }) => ({ card: 'generic', title: 'Forget Mnemon insight', kind: 'edit', rawInput: args.id }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon insight forgotten' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_memory_body_create',
    description: 'Create a new isolated Memory Space under the user-configured persistence strategy. First inspect mnemon_memory_bodies.persistenceStrategy. In manual mode the host fixes the Provider. In automatic mode select only an eligible configured Provider from that policy and supply a concise reason and confidence; the host validates every hard rule and injects saved connection settings. Never invent credentials or endpoints. Use only for a distinct recurring durable scope, then write the qualifying insight with mnemon_remember, which activates it.',
    parameters: {
      type: 'object',
      properties: {
        name: { type: 'string', description: 'Topic-specific human-readable name that remains meaningful in the directory.' },
        description: { type: 'string', description: 'Precise routing boundary: what durable knowledge belongs here and when it should be recalled.' },
        providerId: { type: 'string', description: 'Automatic mode only: one eligible Provider id from persistenceStrategy.' },
        reason: { type: 'string', description: 'Automatic mode only: concise user-facing reason for this Provider choice.' },
        confidence: { type: 'string', enum: ['high', 'medium', 'low'], description: 'Automatic mode only: calibrated confidence in the Provider choice.' },
      },
      required: ['name', 'description'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('memory-spaces', 'manage-spaces') },
    execute: (args: { name: string; description: string; providerId?: string; reason?: string; confidence?: string }, exec: ToolExecution) => {
      requireSource(exec, 'memory-spaces', 'write')
      return isSubagent(exec.agent)
      ? sourceAction(exec, 'memory-spaces', 'manage-spaces', { operation: 'create', request: { name: args.name, description: args.description }, ...(args.providerId === undefined && args.reason === undefined && args.confidence === undefined ? {} : { selection: { providerId: args.providerId ?? '', reason: args.reason ?? '', confidence: args.confidence ?? '' } }) })
      : coordinator.write(requireAgent(exec), 'create-memory-body', args, exec.signal)
    },
    presentCall: () => ({ card: 'generic', title: 'Create Memory Space', kind: 'edit' }),
    presentResult: () => ({ card: 'generic', title: 'Memory Space created' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_memory_body_update',
    description: 'Update a Memory Space name, routing description, or activation state. Activation controls reads only. Use conservatively; prefer the user-facing toggle for ordinary manual activation changes.',
    parameters: {
      type: 'object',
      properties: {
        memoryBodyId: { type: 'string' },
        name: { type: 'string' },
        description: { type: 'string' },
        active: { type: 'boolean' },
      },
      required: ['memoryBodyId'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('memory-spaces', 'manage-spaces') },
    execute: (args: { memoryBodyId: string; name?: string; description?: string; active?: boolean }, exec: ToolExecution) => {
      requireSource(exec, 'memory-spaces', 'write')
      return isSubagent(exec.agent)
        ? sourceAction(exec, 'memory-spaces', 'manage-spaces', { operation: 'update', ...args })
        : coordinator.write(requireAgent(exec), 'update-memory-body', args, exec.signal)
    },
    presentCall: () => ({ card: 'generic', title: 'Update Mnemon Memory Space', kind: 'edit' }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon Memory Space updated' }),
  } as never))

  ctx.tools.register(definition({
    name: 'mnemon_memory_body_merge',
    description: 'Non-destructively merge complete Mnemon Native source Memory Spaces into one Mnemon Native target through import, preserving durable nodes and typed graph edges. External providers are not mergeable. Use only after confirming substantial scope overlap or when the user requests consolidation. Source databases are retained and merely deactivated by default.',
    parameters: {
      type: 'object',
      properties: {
        targetMemoryBodyId: { type: 'string' },
        sourceMemoryBodyIds: { type: 'array', items: { type: 'string' }, description: 'One through 20 source Memory Space ids.' },
        deactivateSources: { type: 'boolean', description: 'Defaults to true. Never deletes source databases.' },
      },
      required: ['targetMemoryBodyId', 'sourceMemoryBodyIds'],
    },
    output: { schema: JSON_OBJECT_OUTPUT, render: (_args: unknown, value: unknown) => text(value),
      presentationMeta: memoryWritePresentation('memory-spaces', 'manage-spaces') },
    execute: (args: { targetMemoryBodyId: string; sourceMemoryBodyIds: string[]; deactivateSources?: boolean }, exec: ToolExecution) => {
      requireSource(exec, 'memory-spaces', 'write')
      return isSubagent(exec.agent)
        ? sourceAction(exec, 'memory-spaces', 'manage-spaces', { operation: 'merge', ...args })
        : coordinator.write(requireAgent(exec), 'merge-memory-bodies', args, exec.signal)
    },
    presentCall: () => ({ card: 'generic', title: 'Merge Mnemon Memory Spaces', kind: 'edit' }),
    presentResult: () => ({ card: 'generic', title: 'Mnemon Memory Spaces merged' }),
  } as never))
}
