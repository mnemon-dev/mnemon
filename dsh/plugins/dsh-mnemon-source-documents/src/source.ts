import { randomUUID } from 'node:crypto'
import type { MemoryEvidenceItem, MemoryJsonValue, MemoryReadGrant, MemorySourceDefinition } from 'dsh-mnemon/contracts'
import { COMPOSABLE_MEMORY_API_VERSION } from 'dsh-mnemon/contracts'
import { defineMemorySource, createMemoryMutationReceipt as receipt, memoryInputRecord as record, memoryInputStringArray as stringArray, memoryInputText as text, truncateMemoryText as truncate } from 'dsh-mnemon/extension-sdk'
import { DocumentManager } from './controller.ts'
import type { DocumentMutation } from './contracts.ts'
import { documentsSourceConfig, type Config } from './config.ts'
import { documentEvidence } from './evidence.ts'

function workspace(scope: { workspaceId?: string }): string | undefined {
  const value = scope.workspaceId?.trim()
  return value === undefined || value === '' ? undefined : value
}

function grantIds(grant: MemoryReadGrant, includeArchived = false): string[] {
  const value = record(grant.value, 'Documents ReadGrant')
  return [...(stringArray(value.documentIds, 'documentIds', 10_000) ?? []),
    ...(includeArchived ? stringArray(value.archivedDocumentIds, 'archivedDocumentIds', 10_000) ?? [] : [])]
}

const CREATE_PROPERTIES = {
  title: { type: 'string' }, description: { type: 'string' }, content: { type: 'string' },
  sourcePaths: { type: 'array' }, sessionIds: { type: 'array' },
}

function documentCreation(value: MemoryJsonValue): DocumentMutation {
  const input = record(value, 'Documents create')
  // Enforce the narrower capability at the Source boundary as well as in its schema.
  for (const key of Object.keys(input)) {
    if (!Object.hasOwn(CREATE_PROPERTIES, key)) throw new Error('unsupported Documents create field: ' + key)
  }
  return documentMutation({ ...input, action: 'create' })
}

function documentMutation(value: MemoryJsonValue, allowedIds?: readonly string[]): DocumentMutation {
  const input = record(value, 'Documents mutation')
  const action = text(input.action, 'action', 20)!
  let mutation: DocumentMutation
  if (action === 'create') {
    mutation = {
      action,
      title: text(input.title, 'title', 160)!,
      content: text(input.content, 'content', 1_000_000)!,
      ...(text(input.description, 'description', 600, false) === undefined ? {} : { description: text(input.description, 'description', 600, false)! }),
      ...(input.sourcePaths === undefined ? {} : { sourcePaths: stringArray(input.sourcePaths, 'sourcePaths') ?? [] }),
      ...(input.sessionIds === undefined ? {} : { sessionIds: stringArray(input.sessionIds, 'sessionIds', 20) ?? [] }),
    }
  } else if (action === 'update') {
    const id = text(input.id, 'id', 300)!
    if (allowedIds !== undefined && !allowedIds.includes(id)) throw new Error('Documents update target is outside this View ReadGrant')
    mutation = {
      action,
      id,
      ...(text(input.title, 'title', 160, false) === undefined ? {} : { title: text(input.title, 'title', 160, false)! }),
      ...(text(input.description, 'description', 600, false) === undefined ? {} : { description: text(input.description, 'description', 600, false)! }),
      ...(text(input.content, 'content', 1_000_000, false) === undefined ? {} : { content: text(input.content, 'content', 1_000_000, false)! }),
      ...(input.sourcePaths === undefined ? {} : { sourcePaths: stringArray(input.sourcePaths, 'sourcePaths') ?? [] }),
      ...(input.sessionIds === undefined ? {} : { sessionIds: stringArray(input.sessionIds, 'sessionIds', 20) ?? [] }),
    }
  } else {
    throw new Error(`unsupported Documents action: ${action}`)
  }
  return mutation
}

export function createDocumentsMemorySource(config: Config = {}): MemorySourceDefinition {
 const configured = Object.freeze({ ...config })
 return defineMemorySource({
  manifest: {
    apiVersion: COMPOSABLE_MEMORY_API_VERSION,
    kind: 'source',
    typeId: 'documents',
    packageName: 'dsh-mnemon-source-documents',
    role: 'narrative',
    capabilities: ['status', 'project', 'search', 'read', 'write'],
    consistency: 'namespace-pinned-live-read',
    routes: [{
      id: 'search',
      description: 'Search only the project Documents pinned into this View.',
      capability: 'search',
      inputSchema: {
        type: 'object',
        required: ['query'],
        additionalProperties: false,
        properties: { query: { type: 'string' }, limit: { type: 'integer' }, includeArchived: { type: 'boolean' } },
      },
      maxCalls: 4,
      maxResults: 20,
      maxCharacters: 16_000,
    }],
    actions: [{
      id: 'manage',
      description: 'Create a project Document or update a Document pinned into this View.',
      capability: 'write',
      inputSchema: {
        type: 'object',
        required: ['action'],
        additionalProperties: false,
        properties: {
          action: { type: 'string', enum: ['create', 'update'] },
          id: { type: 'string' }, ...CREATE_PROPERTIES,
        },
      },
    }, {
      id: 'create',
      description: 'Create one new project Document without updating or archiving existing documents. Capacity exhaustion rejects the write.',
      capability: 'write',
      inputSchema: {
        type: 'object', required: ['title', 'content'], additionalProperties: false,
        properties: CREATE_PROPERTIES,
      },
    }],
    management: {
      label: 'Project Documents',
      description: 'Workspace-scoped narrative memory with namespace-pinned reads.',
    },
  },
  create(context) {
    const effective = documentsSourceConfig({ ...context.configuration, ...configured }, context.sourceInstanceKey)
    const documents = new DocumentManager(effective.limitBytes, undefined, () => effective.dataDir)
    const snapshot = (workspaceId: string | undefined) => workspaceId === undefined ? undefined : documents.forWorkspace(workspaceId).catalog()
    const prepared = new WeakMap<object, NonNullable<ReturnType<typeof snapshot>>>()
    return {
      facts(request) {
        const root = workspace(request.scope)
        if (root === undefined) return {
          sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'documents', role: 'narrative', availability: 'unavailable',
          revision: 'unavailable:no-workspace', capabilities: ['status'], routeIds: [], actionIds: [], hints: { reason: 'no-workspace' },
        }
        if (request.scenario.startsWith('management.') && request.scenario !== 'management.catalog') return {
          sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'documents', role: 'narrative', availability: 'ready',
          revision: documents.forWorkspace(root).revision(), capabilities: ['status', 'project', 'search', 'read', 'write'], routeIds: ['search'], actionIds: ['manage', 'create'],
        }
        const current = snapshot(root)!
        prepared.set(request.scope, current)
        const active = current.documents.filter(document => document.status === 'active' && document.healthy)
        return {
          sourceInstanceKey: context.sourceInstanceKey, sourceTypeId: 'documents', role: 'narrative', availability: 'ready',
          revision: current.revision, capabilities: ['status', 'project', 'search', 'read', 'write'], routeIds: ['search'], actionIds: ['manage', 'create'],
          hints: { activeCount: active.length },
        }
      },
      project(request) {
        const root = workspace(request.scope)
        if (root === undefined) return { fragments: [] }
        const current = prepared.get(request.scope) ?? snapshot(root)!
        prepared.delete(request.scope)
        if (current.revision !== request.expectedRevision) throw new Error('Documents projection revision changed during composition')
        const active = current.documents.filter(document => document.status === 'active' && document.healthy).sort((a, b) => a.id.localeCompare(b.id))
        const readGrant: MemoryReadGrant = {
          id: `${context.sourceInstanceKey}/grant/${current.revision}`,
          sourceInstanceKey: context.sourceInstanceKey,
          schema: 'dsh-mnemon.documents/v1',
          value: { workspaceRoot: root, documentIds: active.map(document => document.id),
            archivedDocumentIds: current.documents.filter(document => document.status === 'archived' && document.healthy).map(document => document.id).sort() },
          revision: current.revision,
          consistency: 'namespace-pinned-live-read',
        }
        return {
          fragments: request.includeProjection ? [{
            id: `${context.sourceInstanceKey}/projection`, sourceInstanceKey: context.sourceInstanceKey, mode: request.mode,
            text: truncate(`${active.length} active project Document${active.length === 1 ? '' : 's'} available through the documents/search route.`, request.maxCharacters),
            revision: current.revision,
            provenance: { sourceTypeId: 'documents' },
          }] : [],
          readGrant,
          presentation: {
            visibleItems: active.length,
            totalItems: current.documents.length,
            items: [...active]
              .sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt) || left.id.localeCompare(right.id))
              .slice(0, 24)
              .map(document => ({
                id: document.id,
                title: truncate(document.title, 160),
                ...((document.description || document.excerpt).trim() === '' ? {} : { excerpt: truncate(document.description || document.excerpt, 600) }),
              })),
          },
        }
      },
      async query(request) {
        const root = workspace(request.view.scope)
        if (root === undefined) throw new Error('Documents Route requires a workspace-scoped View')
        const input = record(request.input, 'Documents search')
        const query = text(input.query, 'query', 2_000, false) ?? ''
        const limitValue = input.limit
        const limit = Math.min(request.route.maxResults ?? 20, typeof limitValue === 'number' && Number.isInteger(limitValue) ? Math.max(1, Math.min(20, limitValue)) : 10)
        const controller = documents.forWorkspace(root)
        const allowedIds = grantIds(request.grant, input.includeArchived === true)
        const result = await controller.search(query, { limit, allowedIds, includeArchived: input.includeArchived === true })
        const suggestions = result.results.length === 0 && query !== '' ? controller.snapshot().documents
          .filter(document => allowedIds.includes(document.id) && (input.includeArchived === true || document.status === 'active'))
          .sort((left, right) => Date.parse(right.updatedAt) - Date.parse(left.updatedAt))
          .slice(0, Math.min(3, limit)) : []
        let remaining = request.route.maxCharacters ?? 16_000
        const items: MemoryEvidenceItem[] = []
        let truncated = result.total > limit
        for (const document of result.results) {
          if (remaining <= 0) { truncated = true; break }
          const content = documentEvidence(document.content, query, Math.min(2_600, remaining))
          remaining -= content.length
          truncated ||= content !== document.content
          items.push({
            id: document.id, text: content, score: document.score, revision: String(document.revision),
            provenance: { kind: 'match', documentId: document.id, title: document.title, description: document.description,
              status: document.status, relativePath: document.relativePath, sourcePaths: document.sourcePaths.slice(0, 8) },
          })
        }
        for (const document of suggestions) {
          if (remaining <= 0) { truncated = true; break }
          const excerpt = truncate(document.excerpt, Math.min(1_000, remaining))
          remaining -= excerpt.length
          items.push({ id: document.id, score: 0, text: excerpt,
            provenance: { kind: 'suggestion', documentId: document.id, title: document.title, description: document.description, status: document.status } })
        }
        return {
          metadata: { query: result.query, includeArchived: result.includeArchived, total: result.total },
          id: `evidence:${randomUUID()}`, viewId: request.view.id, routeId: request.route.id, sourceInstanceKey: context.sourceInstanceKey,
          observedAt: new Date().toISOString(), truncated, items,
        }
      },
      async manage(request) {
        const root = workspace(request.scope)
        if (root === undefined) throw new Error('Documents management requires a workspace')
        const controller = documents.forWorkspace(root)
        const input = request.input === null ? {} : record(request.input, 'Documents management')
        let value: unknown
        if (request.mode === 'read') {
          switch (request.operation) {
            case 'snapshot': value = controller.snapshot(); break
            case 'document': value = controller.get(text(input.id, 'id', 300)!); break
            case 'capacity-plan': value = controller.capacityPlan(documentMutation(request.input)); break
            case 'search': value = await controller.search(text(input.query, 'query', 2_000, false) ?? '', {
              includeArchived: input.includeArchived === true,
              ...(input.limit === undefined ? {} : { limit: Math.min(100, Math.max(1, Number(input.limit) || 50)) }),
            }); break
            default: throw new Error('unsupported Documents management read operation: ' + request.operation)
          }
        } else {
          if (!request.confirmed) throw new Error('Documents management mutation requires explicit confirmation')
          if (request.operation === 'mutate') value = await controller.mutate(documentMutation(request.input), request.expectedRevision)
          else if (request.operation === 'archive') {
            const document = controller.get(text(input.id, 'id', 300)!)
            const revision = input.documentRevision === undefined ? document.revision : input.documentRevision
            if (typeof revision !== 'number' || !Number.isInteger(revision)) throw new Error('documentRevision must be an integer')
            value = await controller.archive(document.id, revision, {
              summary: text(input.summary, 'summary', 10_000, false) ?? 'Archived locally by the user.',
              memoryBodyIds: stringArray(input.memoryBodyIds, 'memoryBodyIds', 100) ?? [],
            })
          } else throw new Error('unsupported Documents management mutation operation: ' + request.operation)
        }
        const completed = value as { revision?: unknown; snapshot?: { revision?: string } }
        const revision = completed.snapshot?.revision ?? (typeof completed.revision === 'string' ? completed.revision : controller.revision())
        return { revision, value: value as MemoryJsonValue }
      },
      async mutate(request) {
        const root = workspace(request.view.scope)
        if (root === undefined) throw new Error('Documents Action requires a workspace-scoped View')
        const grant = request.grant
        const mutation = request.offer.sourceActionId === 'create'
          ? documentCreation(request.input)
          : documentMutation(request.input, grant === undefined ? [] : grantIds(grant))
        const result = await documents.forWorkspace(root).mutate(mutation)
        return receipt(request.view.id, request.offer.id, context.sourceInstanceKey, result.snapshot.revision, result as unknown as MemoryJsonValue, 'committed')
      },
    }
  },
 })
}

export const DOCUMENTS_MEMORY_SOURCE = createDocumentsMemorySource()
