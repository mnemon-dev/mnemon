// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ClientConnectionHandle } from "../src/host/dsh.ts"
import type { ClientSettingsScope, ClientSettingsSnapshot } from "../src/host/dsh.ts"
import type { Config } from "../src/host/config.ts"
import type { VersionRestartStatus } from '../src/host/protocol.ts'
import { ComposedMnemonWorkbench as MnemonWorkbench } from './fixtures/client.tsx'
import { MnemonActionSeat } from '../src/client/action-seat.ts'
import { MnemonChangeSignal } from '../src/client/change-signal.ts'
import { clearStarterUpdate, pendingStarterUpdate, recordStarterUpdate } from '../src/client/starter-update.ts'
import { translateEn } from '../src/client/locales.ts'
import { TEST_PROVIDERS as MEMORY_PROVIDER_CATALOG } from './fixtures/providers.ts'
import { memoryPageStyles } from '../src/client/page-kit.tsx'
import { installClientFrameStyles } from './helpers/client-frame-styles.ts'
import { settingsScope as staticSettingsScope } from './helpers/settings-scope.ts'

describe('MnemonWorkbench', () => {
  afterEach(cleanup)
  async function selectWorkspaceTab(name: string): Promise<void> {
    const navigation = await screen.findByRole('tablist', { name: 'Mnemon 页面' })
    if (['概览', '检索', '实体', '内容'].includes(name)) {
      fireEvent.click(within(navigation).getByRole('tab', { name: '记忆空间' }))
      fireEvent.click(await screen.findByRole('tab', { name }))
      return
    }
    fireEvent.click(within(navigation).getByRole('tab', { name }))
  }

  async function openRememberDialog(): Promise<void> {
    await selectWorkspaceTab('记忆空间')
    fireEvent.click(await screen.findByRole('button', { name: '存入记忆' }))
  }
  const settingsSnapshot = { status: 'ready' as const, value: { storageScope: 'custom' as const }, base: {}, user: {}, revision: 0, writable: true, mode: 'host' as const }
  const settingsScope = staticSettingsScope<Config>(settingsSnapshot)
  const readOnlySettingsScope = staticSettingsScope<Config>({ status: 'unavailable', writable: false, mode: 'host' })

  function createConnection(options: { reviewPartial?: boolean; reviewTeam?: boolean; reviewError?: string; isLoopback?: boolean; withInactiveBody?: boolean; withSecondActiveBody?: boolean; metadataFailureBodyId?: string; withPlacement?: boolean; withProviderSources?: boolean; listCount?: number; searchCount?: number; entityCount?: number; entityInsightCount?: number; documentCount?: number; runtimeCount?: number; runtimeBranch?: boolean; longContent?: boolean; workspaceMismatch?: boolean; nativeUnhealthy?: boolean; graphPending?: boolean; statusPending?: boolean; summaryWithoutVersion?: boolean; restartPending?: VersionRestartStatus; directoryPending?: boolean; reconnectPending?: boolean; relatedDeferred?: boolean; versionsDeferred?: boolean; layerSwitches?: Record<'runtime' | 'documents' | 'memory-spaces', boolean>; componentsOff?: string[]; composition?: { serving: boolean; state: 'ready' | 'incomplete' | 'rejected'; diagnostics: Array<{ code: string; message: string }> } } = {}) {
    const body = {
      id: 'project',
      provider: MEMORY_PROVIDER_CATALOG.find(item => item.id === 'mnemon-native')!, providerId: 'mnemon-native', providerEnabled: true, providerSettings: {}, configuredSecrets: [],
      name: '项目记忆空间',
      description: '项目决策、约定与交付上下文。',
      active: true,
      mnemonDefault: true,
      dbPath: '/tmp/mnemon/data/project/mnemon.db',
      createdAt: '2026-08-13T02:00:00.000Z',
      updatedAt: '2026-08-13T03:00:00.000Z',
      ...(options.withPlacement === true ? {
        placement: {
          mode: 'automatic' as const,
          providerId: 'mnemon-native' as const,
          decidedBy: 'llm' as const,
          reason: '精确写入与关系图谱比跨项目共享更重要。',
          confidence: 'high' as const,
          candidateProviderIds: ['mnemon-native' as const, 'openviking' as const],
          appliedRules: ['preference:balanced'],
          decidedAt: '2026-08-13T02:00:00.000Z',
        },
      } : {}),
      healthy: options.nativeUnhealthy !== true,
      ...(options.nativeUnhealthy === true ? { error: 'Mnemon Store 无法打开' } : {}),
      stats: { totalInsights: 12, deletedInsights: 0, edgeCount: 9, oplogCount: 20, dbSizeBytes: 4096, byCategory: {}, topEntities: [{ entity: 'SQLite', count: 2 }] },
    }
    const includeSecondaryBody = options.withInactiveBody === true || options.withSecondActiveBody === true
    let secondaryActive = options.withSecondActiveBody === true
    let mnemonVersionUpdated = false
    const secondaryBody = {
      ...body,
      id: 'preferences',
      name: '偏好记忆空间',
      description: '长期稳定的表达与协作偏好。',
      active: secondaryActive,
      mnemonDefault: false,
      dbPath: '/tmp/mnemon/data/preferences/mnemon.db',
      stats: { ...body.stats, totalInsights: 1, edgeCount: 0, topEntities: [{ entity: 'DSH', count: 1 }] },
    }
    const status = {
      healthy: options.nativeUnhealthy !== true,
      ...(options.nativeUnhealthy === true ? { error: '项目记忆空间: Mnemon Store 无法打开' } : {}),
      version: '0.1.2',
      dshMnemonVersion: '0.1.2',
      ...(options.restartPending === undefined ? {} : { restartPending: options.restartPending }),
      cliPath: '/usr/local/bin/mnemon',
      commandFound: true,
      dataDir: '/tmp/mnemon',
      store: 'project',
      writeEnabled: true,
      timeoutMs: 10000,
      defaultRecallLimit: 10,
      memoryBodyDirectory: '/tmp/mnemon/data',
      memoryBodies: includeSecondaryBody ? [body, secondaryBody] : [body],
      providerServices: [
        { providerId: 'openviking' as const, label: 'OpenViking', enabled: true, configured: true, status: 'healthy' as const, memoryBodyCount: 1, activeMemoryBodyCount: 1 },
        { providerId: 'mem0' as const, label: 'Mem0', enabled: false, configured: true, status: 'disabled' as const, memoryBodyCount: 1, activeMemoryBodyCount: 0 },
      ],
      stats: { totalInsights: 12, deletedInsights: 0, edgeCount: 9, oplogCount: 20, dbSizeBytes: 4096, byCategory: {}, topEntities: [] },
      storage: {
        activeKind: 'custom', activeRoot: '/tmp/mnemon', generatedAt: '2026-08-13T03:00:00.000Z',
        scopes: [{
          kind: 'custom', root: '/tmp/mnemon', configured: true, active: true, available: true, totalBytes: 8192,
          areas: [
            { kind: 'runtime', path: '/tmp/mnemon/runtime', status: 'ready', bytes: 1024, itemCount: 1, details: { userEntries: 1, memoryEntries: 0 } },
            { kind: 'memory-bodies', path: '/tmp/mnemon/data', status: 'ready', bytes: 7168, itemCount: 1, details: { activeBodies: 1, databases: 1 } },
            { kind: 'documents', path: '/tmp/mnemon/documents', status: 'empty', bytes: 0, itemCount: 0, details: { activeDocuments: 0, archivedDocuments: 0 } },
            { kind: 'state', path: '/tmp/mnemon/state', status: 'missing', bytes: 0, itemCount: 0, details: {} },
          ],
        }],
      },
      lifecycle: {
        enabled: true,
        recallMode: 'guided',
        writebackMode: 'guided',
        idleReviewMs: 30000,
        activeAgents: 1,
        sessionAvailable: true,
        taskAgentAvailable: true,
        counters: { primes: 1, recallCues: 2, writebackCues: 2, supervisedRequests: 1, failures: 0 },
        subagents: { recalls: 2, answers: 1, reviews: 1, writes: 1, failures: 0 },
        current: {
          sessionId: 'session-1',
          status: 'idle',
          startSource: 'startup',
          primePending: false,
          guidedTurns: 1,
          memoryToolCalls: 2,
          idleReviewPending: false,
          reviewRunning: false,
          lastReviewAt: '2026-08-13T02:59:00.000Z',
          lastReviewAction: 'skipped',
          lastPhase: 'writeback',
          ...(options.reviewError === undefined ? {} : { lastError: options.reviewError }),
          ...(options.reviewTeam ? { idleReviewBlocked: 'agent-team' } : {}),
          ...(options.reviewPartial ? { lastReviewFailure: { status: 'partial', runId: 'review-run-255', provider: 'spawn', receipts: [
            { tool: 'mnemon_document_create', action: 'created', documentId: 'committed-document-255' },
            { tool: 'mnemon_runtime_memory', action: 'added', target: 'memory', revision: 'committed-runtime-revision' },
          ] } } : {}),
          lastAt: '2026-08-13T03:00:00.000Z',
        },
      },
      ...(options.layerSwitches === undefined && options.composition === undefined ? {} : {
        memorySystem: {
          serving: options.composition?.serving ?? true,
          evaluation: { state: options.composition?.state ?? 'ready', contributionRevision: 1, sourceInstanceKeys: [], diagnostics: options.composition?.diagnostics ?? [] },
          sources: [],
          configuration: {
            id: 'default-three-tier', strategyId: 'default-three-tier',
            layers: Object.fromEntries((['runtime', 'documents', 'memory-spaces'] as const).map(id => [id, {
              enabled: options.layerSwitches?.[id] ?? true,
              participation: { recall: 'automatic', write: 'automatic', projection: 'automatic', maintenance: 'automatic' }, adapterIds: [],
            }])),
          },
        },
      }),
      ...(options.workspaceMismatch === true ? {
        workspaceContext: {
          mode: 'workspace',
          selectedRoot: '/tmp/workspace-two/.mnemon',
          effectiveRoot: '/tmp/workspace-one/.mnemon',
          aligned: false,
          selectedWorkspace: { id: 'workspace-2', title: 'Workspace Two', path: '/tmp/workspace-two' },
          effectiveWorkspace: { id: 'workspace-1', title: 'Workspace One', path: '/tmp/workspace-one' },
        },
      } : {}),
    }
    const memory = { providerId: 'mnemon-native', providerLabel: 'mnemon', memoryCapabilities: MEMORY_PROVIDER_CATALOG.find(item => item.id === 'mnemon-native')!.capabilities, id: 'memory-12345678', content: options.longContent === true ? '这是一段非常长的记忆内容，用于验证图谱检查器对超长文本的截断展示，以及全文预览窗口的打开与关闭。'.repeat(6) : '项目选择 SQLite，因为需要单文件部署。', category: 'decision', importance: 4, tags: ['architecture'], entities: ['SQLite'], color: '#e74c3c', memoryBodyId: body.id, memoryBodyName: body.name, graphId: `${body.id}:memory-12345678` }
    const secondaryMemory = { id: 'preference-1', content: '用户偏好简洁中文回答。', category: 'preference', importance: 4, tags: ['style'], entities: ['DSH'], color: '#9b59b6', memoryBodyId: secondaryBody.id, memoryBodyName: secondaryBody.name, graphId: `${secondaryBody.id}:preference-1` }
    const relatedResolvers: Array<(response: { ok: true; value: Array<typeof memory> }) => void> = []
    const versionResponse = () => ({ ok: true as const, value: {
      checkedAt: '2026-08-15T03:00:00.000Z',
      components: [
        { id: 'mnemon', name: 'Mnemon CLI', executablePath: '/usr/local/bin/mnemon', current: mnemonVersionUpdated ? '0.2.0' : '0.1.2', latest: '0.2.0', outdated: !mnemonVersionUpdated, installMode: 'homebrew', updateSupported: true, updateHint: 'brew' },
        { id: 'dsh-mnemon', name: 'dsh-mnemon', installProfile: 'web', installPath: '/workspace/dsh-mnemon', current: '0.1.2', latest: '0.1.3', outdated: true, installMode: 'link', updateSupported: false, updateHint: 'link' },
      ],
    } })
    const versionResolvers: Array<(response: ReturnType<typeof versionResponse>) => void> = []
    const providerSources = options.withProviderSources === true ? {
      graph: [
        { memoryBodyId: body.id, memoryBodyName: body.name, providerId: 'mnemon-native', providerLabel: 'mnemon', mode: 'graph', status: 'ready', itemCount: 2, edgeCount: 2 },
        { memoryBodyId: 'viking-lab', memoryBodyName: 'OpenViking 团队知识', providerId: 'openviking', providerLabel: 'OpenViking', mode: 'projection', status: 'ready', itemCount: 3, edgeCount: 0, hint: '该 Provider 返回内容投影，不承诺原生关系边。' },
        { memoryBodyId: 'retain-lab', memoryBodyName: 'RetainDB 协作记录', providerId: 'retaindb', providerLabel: 'RetainDB', mode: 'query-only', status: 'query-required', itemCount: 0, edgeCount: 0 },
      ],
      list: [
        { memoryBodyId: body.id, memoryBodyName: body.name, providerId: 'mnemon-native', providerLabel: 'mnemon', mode: 'enumerable', status: 'ready', itemCount: 1 },
        { memoryBodyId: 'viking-lab', memoryBodyName: 'OpenViking 团队知识', providerId: 'openviking', providerLabel: 'OpenViking', mode: 'query-only', status: 'query-required', itemCount: 0 },
        { memoryBodyId: 'supermemory-lab', memoryBodyName: 'Supermemory 研究素材', providerId: 'supermemory', providerLabel: 'Supermemory', mode: 'unsupported', status: 'unsupported', itemCount: 0 },
      ],
      entities: [
        { memoryBodyId: body.id, memoryBodyName: body.name, providerId: 'mnemon-native', providerLabel: 'mnemon', mode: 'entities', status: 'ready', itemCount: 1 },
        { memoryBodyId: 'mem0-lab', memoryBodyName: 'Mem0 用户画像', providerId: 'mem0', providerLabel: 'Mem0', mode: 'unsupported', status: 'unsupported', itemCount: 0 },
      ],
      search: [
        { memoryBodyId: body.id, memoryBodyName: body.name, providerId: 'mnemon-native', providerLabel: 'mnemon', mode: 'search', status: 'ready', itemCount: 1 },
        { memoryBodyId: 'mem0-lab', memoryBodyName: 'Mem0 用户画像', providerId: 'mem0', providerLabel: 'Mem0', mode: 'search', status: 'empty', itemCount: 0 },
      ],
    } : undefined
    const branchEntry = { content: 'feat/branch-ui 分支上决定用 v2 渲染管线', created_at: '2026-08-13T02:05:00.000Z', updated_at: '2026-08-13T02:05:00.000Z', target: 'memory' as const, importance: 'normal' as const, branches: ['feat/branch-ui'] }
    let runtimeEntries = options.runtimeCount === undefined
      ? (options.runtimeBranch ? [{ content: '用户偏好简洁中文回答。', created_at: '2026-08-13T02:00:00.000Z', updated_at: '2026-08-13T02:00:00.000Z', target: 'user', importance: 'critical' }, branchEntry] : [{ content: '用户偏好简洁中文回答。', created_at: '2026-08-13T02:00:00.000Z', updated_at: '2026-08-13T02:00:00.000Z', target: 'user', importance: 'critical' }])
      : Array.from({ length: options.runtimeCount }, (_, index) => ({ content: `运行时条目 ${index + 1}`, created_at: `2026-08-13T02:${String(index).padStart(2, '0')}:00.000Z`, updated_at: `2026-08-13T02:${String(index).padStart(2, '0')}:00.000Z`, target: index % 2 === 0 ? 'user' : 'memory', importance: index % 3 === 0 ? 'critical' : 'normal' }))
    const baseDocument = {
      id: 'document-12345678', title: '发布验证清单', description: '发布前的完整验证路径。', status: 'active', filename: 'release-document-1234.md',
      relativePath: '.mnemon/documents/active/release-document-1234.md', sourcePaths: ['package.json'], sessionIds: ['session-1'],
      createdAt: '2026-08-13T02:00:00.000Z', updatedAt: '2026-08-13T02:00:00.000Z', lastAccessedAt: '2026-08-13T02:00:00.000Z',
      revision: 1, contentHash: 'a'.repeat(64), sizeBytes: 640, memoryBodyIds: [] as string[], healthy: true, excerpt: '发布前运行 typecheck、单元测试与真实 WebUI E2E。',
      content: '# 发布验证\n\n发布前运行 **typecheck**、单元测试与真实 WebUI E2E。\n\n- 检查构建产物\n- 检查真实页面\n\n[架构说明](https://example.com/architecture)\n\n[不安全链接](javascript:alert(1))',
    }
    let documents = options.documentCount === undefined
      ? [baseDocument]
      : Array.from({ length: options.documentCount }, (_, index) => ({ ...baseDocument, id: `document-${String(index + 1).padStart(8, '0')}`, title: `档案条目 ${index + 1}`, filename: `document-${index + 1}.md`, relativePath: `.mnemon/documents/active/document-${index + 1}.md`, content: `# 档案条目 ${index + 1}\n\n正文 ${index + 1}`, excerpt: `档案摘要 ${index + 1}` }))
    const call = vi.fn(async (_channel: string, endpoint: string, payload?: Record<string, unknown>) => {
      const bodies = includeSecondaryBody ? [body, { ...secondaryBody, active: secondaryActive }] : [body]
      if (endpoint === 'runtime-memory') {
        if (payload?.action !== undefined) {
          const target = String(payload.target)
          if (payload.action === 'add') runtimeEntries = [...runtimeEntries, { content: String(payload.content), created_at: '2026-08-13T03:01:00.000Z', updated_at: '2026-08-13T03:01:00.000Z', target, importance: String(payload.importance ?? 'normal') }]
          if (payload.action === 'replace') runtimeEntries = runtimeEntries.map(entry => entry.target === target && entry.content.includes(String(payload.old_text)) ? { ...entry, content: String(payload.content), importance: String(payload.importance ?? entry.importance), updated_at: '2026-08-13T03:01:00.000Z' } : entry)
          if (payload.action === 'remove') runtimeEntries = runtimeEntries.filter(entry => !(entry.target === target && entry.content.includes(String(payload.old_text))))
          const targetEntries = runtimeEntries.filter(entry => entry.target === target)
          return { ok: true, value: { success: true, message: 'updated', target, entryCount: targetEntries.length, usage: { used: targetEntries.reduce((sum, entry) => sum + entry.content.length, 0), limit: target === 'user' ? 4096 : 10240 } } }
        }
        const targetView = (target: string, limit: number) => { const entries = runtimeEntries.filter(entry => entry.target === target); return { target, entryCount: entries.length, used: entries.reduce((sum, entry) => sum + entry.content.length, 0), limit, markdownPath: `/tmp/mnemon/runtime/${target === 'user' ? 'USER' : 'MEMORY'}.md` } }
        return { ok: true, value: { directory: '/tmp/mnemon/runtime', sourcePath: '/tmp/mnemon/runtime/memories.json', generatedAt: '2026-08-13T03:00:00.000Z', entries: runtimeEntries, targets: { user: targetView('user', 4096), memory: targetView('memory', 10240) } } }
      }
      if (endpoint === 'documents') {
        const active = documents.filter(document => document.status === 'active')
        return { ok: true, value: { workspaceRoot: '/tmp/project', directory: '/tmp/project/.mnemon/documents', indexPath: '/tmp/project/.mnemon/documents/index.json', generatedAt: '2026-08-13T03:00:00.000Z', revision: 'r1', limitBytes: 10 * 1024 * 1024, activeBytes: active.reduce((sum, document) => sum + document.sizeBytes, 0), activeCount: active.length, archivedCount: documents.length - active.length, total: documents.length, documents: documents.map(({ content: _content, ...document }) => document) } }
      }
      if (endpoint === 'document-search') {
        const query = String(payload?.query ?? '').toLowerCase()
        const includeArchived = payload?.includeArchived === true
        const results = documents.filter(document => (includeArchived || document.status === 'active') && `${document.title} ${document.description} ${document.content}`.toLowerCase().includes(query)).map(document => ({ ...document, score: 8 }))
        return { ok: true, value: { query, includeArchived, total: results.length, generatedAt: '2026-08-13T03:00:00.000Z', results } }
      }
      if (endpoint === 'document') {
        if (payload?.action === 'create') {
          const created = { ...documents[0]!, id: 'document-new-1234', title: String(payload.title), description: String(payload.description ?? ''), content: String(payload.content), excerpt: String(payload.content).slice(0, 120), sourcePaths: payload.sourcePaths as string[] ?? [], filename: 'new-document-new.md', relativePath: '.mnemon/documents/active/new-document-new.md' }
          documents = [...documents, created]
          return { ok: true, value: { success: true, action: 'created', document: created, snapshot: {} } }
        }
        if (payload?.action === 'update') {
          documents = documents.map(document => document.id === payload.id ? { ...document, title: String(payload.title ?? document.title), description: String(payload.description ?? document.description), content: String(payload.content ?? document.content), revision: document.revision + 1 } : document)
          return { ok: true, value: { success: true, action: 'updated', document: documents.find(document => document.id === payload.id), snapshot: {} } }
        }
        if (payload?.action === 'archive') {
          documents = documents.map(document => document.id === payload.id ? { ...document, status: 'archived', relativePath: `.mnemon/documents/archived/${document.filename}`, archiveSummary: '已写入发布记忆空间索引。', memoryBodyIds: ['project'] } : document)
          return { ok: true, value: { success: true, action: 'archived', document: documents.find(document => document.id === payload.id), snapshot: {}, maintenance: { runId: 'archive-child', provider: 'spawn', summary: 'indexed', memoryBodyIds: ['project'], archivedDocumentIds: [payload.id] } } }
        }
        return { ok: true, value: documents.find(document => document.id === payload?.id) }
      }
      if (endpoint === 'status-summary' && options.statusPending === true) return { ok: true, value: { ...status, memoryBodies: bodies.map(item => ({ ...item, statusLoading: true })) } }
      // The Host's summary carries the CLI version only after a full status has read it.
      if (endpoint === 'status-summary' && options.summaryWithoutVersion === true) {
        const { version: _version, ...summary } = status
        return { ok: true, value: { ...summary, memoryBodies: bodies } }
      }
      if (endpoint === 'status' && options.statusPending === true) return await new Promise<never>(() => {})
      if (endpoint === 'status' || endpoint === 'status-summary') return { ok: true, value: { ...status, memoryBodies: bodies } }
      if (endpoint === 'dashboard') {
        // The shipped Source components, named as they declare themselves; a switched-off one has registered no type.
        const names: Record<string, { en: string; 'zh-CN': string }> = { runtime: { en: 'Runtime memory', 'zh-CN': '运行时记忆' }, documents: { en: 'Project Documents', 'zh-CN': '项目档案' }, 'memory-spaces': { en: 'Memory Spaces', 'zh-CN': '记忆空间' } }
        const entries = ['runtime', 'documents', 'memory-spaces'].map(layer => {
          const enabled = options.componentsOff?.includes(layer) !== true
          return { entryId: 'source-' + layer, packageName: 'dsh-mnemon-source-' + layer, roles: ['source'], ...enabled ? { typeId: layer } : {}, label: names[layer]!, description: { en: '', 'zh-CN': '' },
            fields: [], provides: [{ id: 'source', exclusive: false }], requires: [], requiredBy: [], enabled, active: enabled, writable: true, config: {} }
        })
        return { ok: true, value: { revision: 'view-1', writable: true, strategyTypeId: 'default-three-tier', entries, sources: [], diagnostics: [], pluginInstallation: { supported: false, suggestions: [] } } }
      }
      if (endpoint === 'versions') {
        if (options.versionsDeferred === true) return await new Promise<ReturnType<typeof versionResponse>>(resolve => versionResolvers.push(resolve))
        return versionResponse()
      }
      if (endpoint === 'version-update') {
        mnemonVersionUpdated = payload?.component === 'mnemon'
        status.version = mnemonVersionUpdated ? '0.2.0' : status.version
        return { ok: true, value: { component: payload?.component, previousVersion: '0.1.2', currentVersion: '0.2.0', updated: true, restartRequired: false } }
      }
      if (endpoint === 'body-directory' && options.directoryPending === true) return await new Promise<never>(() => {})
      if (endpoint === 'bodies' || endpoint === 'body-directory') return { ok: true, value: { items: bodies, providers: MEMORY_PROVIDER_CATALOG, total: bodies.length, activeCount: bodies.filter(item => item.active).length, directory: '/tmp/mnemon/data', generatedAt: '2026-08-13T03:00:00.000Z' } }
      if (endpoint === 'body-reconnect') {
        if (options.reconnectPending === true) return await new Promise<never>(() => {})
        return { ok: true, value: bodies.find(item => item.id === payload?.memoryBodyId) ?? body }
      }
      if (endpoint === 'body-metadata-maintain') {
        const memoryBodyId = Array.isArray(payload?.memoryBodyIds) ? String(payload.memoryBodyIds[0]) : ''
        if (memoryBodyId === options.metadataFailureBodyId) return { ok: false, error: { code: 'metadata_failed', message: `${memoryBodyId} subagent failed` } }
        const target = memoryBodyId === secondaryBody.id ? secondaryBody : body
        target.name = memoryBodyId === secondaryBody.id ? '协作偏好' : '产品决策'
        target.description = memoryBodyId === secondaryBody.id
          ? '记录跨会话稳定的表达风格与协作偏好。'
          : '记录稳定的产品范围、架构取舍与依据，在规划和复盘产品方向时召回。'
        return { ok: true, value: { delegated: true, runId: `metadata-child-${memoryBodyId}`, provider: 'spawn', summary: `已更新${target.name}元信息。`, updates: [{ memoryBodyId, title: target.name, description: target.description }] } }
      }
      if (endpoint === 'graph') {
        if (options.graphPending === true) return await new Promise<never>(() => {})
        return {
        ok: true,
        value: {
          nodes: [memory, { id: 'memory-graph-2', content: 'Mnemon 使用四图持久记忆。', category: 'fact', color: '#3498db', memoryBodyId: body.id, memoryBodyName: body.name, graphId: `${body.id}:memory-graph-2` }, ...(secondaryActive ? [secondaryMemory] : [])],
          edges: [
            { sourceId: `${body.id}:${memory.id}`, targetId: `${body.id}:memory-graph-2`, label: 'backbone', color: '#aaaaaa', type: 'temporal' },
            { sourceId: `${body.id}:${memory.id}`, targetId: `${body.id}:memory-graph-2`, label: 'SQLite', color: '#2ecc71', type: 'entity' },
          ],
          generatedAt: '2026-08-13T03:00:00.000Z',
          ...(providerSources === undefined ? {} : { sources: providerSources.graph }),
        },
      }
      }
      if (endpoint === 'list') {
        const items = options.listCount === undefined
          ? [memory]
          : Array.from({ length: options.listCount }, (_, index) => ({ ...memory, id: `memory-${index + 1}`, graphId: `${body.id}:memory-${index + 1}`, content: `记忆条目 ${index + 1}` }))
        return { ok: true, value: { items, total: items.length, generatedAt: '2026-08-13T03:00:00.000Z', ...(providerSources === undefined ? {} : { sources: providerSources.list }) } }
      }
      if (endpoint === 'entities') {
        const items = options.entityCount === undefined ? [{ entity: 'SQLite', count: 2 }] : Array.from({ length: options.entityCount }, (_, index) => ({ entity: `实体 ${index + 1}`, count: options.entityCount! - index }))
        const selected = payload?.entity === undefined ? undefined : String(payload.entity)
        const insights = selected === undefined ? [] : Array.from({ length: options.entityInsightCount ?? 1 }, (_, index) => ({ ...memory, id: `entity-memory-${index + 1}`, graphId: `${body.id}:entity-memory-${index + 1}`, content: `${selected} 关联记忆 ${index + 1}` }))
        return { ok: true, value: { items, ...(selected === undefined ? {} : { selected }), insights, ...(providerSources === undefined ? {} : { sources: providerSources.entities }) } }
      }
      // The Entities page lists the memories that carry an entity, then related ones, in two reads.
      if (endpoint === 'entity-memories') {
        const selected = String(payload?.entity)
        const all = Array.from({ length: options.entityInsightCount ?? 1 }, (_, index) => ({ ...memory, id: `entity-memory-${index + 1}`, graphId: `${body.id}:entity-memory-${index + 1}`, content: `${selected} 关联记忆 ${index + 1}` }))
        const offset = Number(payload?.offset ?? 0)
        const limit = Number(payload?.limit ?? 50)
        return { ok: true, value: { entity: selected, total: all.length, offset, items: all.slice(offset, offset + limit), complete: true, sources: providerSources?.entities ?? [] } }
      }
      if (endpoint === 'entity-related') return { ok: true, value: { entity: String(payload?.entity), items: [], sources: [] } }
      if (endpoint === 'search') return {
        ok: true,
        value: {
          query: 'SQLite',
          mode: 'smart',
          results: options.searchCount === undefined ? [memory] : Array.from({ length: options.searchCount }, (_, index) => ({ ...memory, id: `search-memory-${index + 1}`, graphId: `${body.id}:search-memory-${index + 1}`, content: `检索记忆 ${index + 1}` })),
          ...(providerSources === undefined ? {} : { sources: providerSources.search }),
        },
      }
      if (endpoint === 'agent-search') return {
        ok: true,
        value: {
          query: 'SQLite', mode: 'smart', results: [memory],
          answer: '项目选择 SQLite，以满足单文件部署。', citations: ['project/memory-12345678'],
          delegation: { runId: 'answer-child-1', provider: 'spawn' },
        },
      }
      if (endpoint === 'related') {
        if (options.relatedDeferred === true) return await new Promise<{ ok: true; value: Array<typeof memory> }>(resolve => relatedResolvers.push(resolve))
        return { ok: true, value: [] }
      }
      if (endpoint === 'supervise') return { ok: true, value: { delegated: true, sessionId: 'session-1', runId: 'child-1', provider: 'spawn', summary: '已提炼并写入项目交付约束。', action: 'stored', memoryBodyIds: ['project'] } }
      if (endpoint === 'remember') return { ok: true, value: { delegated: true, runId: 'child-2', provider: 'spawn', summary: '已按高级约束写入。', action: 'stored', memoryBodyIds: ['project'] } }
      if (endpoint === 'forget') return { ok: true, value: { action: 'forgotten' } }
      if (endpoint === 'body') {
        const target = payload?.memoryBodyId === secondaryBody.id ? secondaryBody : body
        target.active = Boolean(payload?.active)
        if (target === secondaryBody) secondaryActive = target.active
        return { ok: true, value: { ...target } }
      }
      if (endpoint === 'body-update') {
        const target = payload?.memoryBodyId === secondaryBody.id ? secondaryBody : body
        if (payload?.name !== undefined) target.name = String(payload.name)
        if (payload?.description !== undefined) target.description = String(payload.description)
        if (payload?.active !== undefined) {
          target.active = Boolean(payload.active)
          if (target === secondaryBody) secondaryActive = target.active
        }
        return { ok: true, value: { ...target } }
      }
      if (endpoint === 'body-delete') return { ok: true, value: { ...body } }
      if (endpoint === 'body-create') return { ok: true, value: { ...body, id: 'new-body', name: String(payload?.name ?? '') } }
      return { ok: false, error: { code: 'unexpected', message: endpoint } }
    })
    return {
      connection: { rpc: { call }, isLoopback: options.isLoopback ?? true } as unknown as ClientConnectionHandle,
      call,
      resolveRelated: (index: number, content: string) => relatedResolvers[index]?.({ ok: true, value: [{ ...memory, id: `related-${index}`, graphId: `${body.id}:related-${index}`, content }] }),
      resolveVersions: (index: number) => versionResolvers[index]?.(versionResponse()),
    }
  }

  it.each(['sidebar', 'builtin'] as const)('accepts scoped skin variables on the %s workspace without styling peer views', async surface => {
    const selector = '[data-dsh-plugin="dsh-mnemon"][data-dsh-part="mnemon-view"]'
    const skin = document.createElement('style')
    skin.textContent = `${selector} { --mn-bg: rgb(232, 241, 249); --mn-backdrop: rgb(217, 232, 245); --mn-surface: rgba(232, 241, 249, 0.74); }`
    document.head.append(skin)
    try {
      const { connection } = createConnection()
      const { container } = render(<>
        <section data-dsh-plugin="peer-plugin" data-dsh-part="peer-view">Peer view</section>
        <MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} />
      </>)
      await screen.findByText('已连接')
      const workbench = screen.getByRole('main')
      expect(container.querySelectorAll(selector)).toHaveLength(1)
      expect(container.querySelector(selector)).toBe(workbench)
      expect(workbench.getAttribute('data-mnemon-surface')).toBe(surface)
      const style = getComputedStyle(workbench)
      expect(style.getPropertyValue('--mn-bg')).toBe('rgb(232, 241, 249)')
      expect(style.getPropertyValue('--mn-backdrop')).toBe('rgb(217, 232, 245)')
      expect(style.getPropertyValue('--mn-surface')).toBe('rgba(232, 241, 249, 0.74)')
      expect(getComputedStyle(screen.getByText('Peer view')).getPropertyValue('--mn-surface')).toBe('')
    } finally {
      skin.remove()
    }
  })

  it.each(['sidebar', 'builtin'] as const)('renders all eight Source switch combinations as reversible %s states', async surface => {
    const ids = ['runtime', 'documents', 'memory-spaces'] as const
    const labels = { runtime: '运行时记忆', documents: '项目档案', 'memory-spaces': '记忆空间' }
    const disabledTitles = { runtime: '运行时记忆 已关闭', documents: '项目档案 已关闭', 'memory-spaces': '记忆空间 已关闭' }
    const endpoints = { runtime: ['runtime-memory'], documents: ['documents'], 'memory-spaces': ['bodies', 'body-directory', 'graph'] }

    for (let mask = 0; mask < 8; mask += 1) {
      const layerSwitches = Object.fromEntries(ids.map((id, index) => [id, (mask & (1 << index)) !== 0])) as Record<typeof ids[number], boolean>
      const { connection, call } = createConnection({ layerSwitches })
      render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} />)
      await screen.findByText('已连接')
      const navigation = screen.getByRole('tablist', { name: 'Mnemon 页面' })

      for (const id of ids) {
        const item = within(navigation).getByRole('tab', { name: layerSwitches[id] ? labels[id] : `${labels[id]} · 已关闭` })
        expect(item.hasAttribute('data-layer-disabled')).toBe(!layerSwitches[id])
        if (layerSwitches[id]) continue

        const before = call.mock.calls.filter(([, endpoint]) => endpoints[id].includes(endpoint)).length
        fireEvent.click(item)
        await screen.findByRole('heading', { name: disabledTitles[id] })
        expect(screen.getByText('已有数据完整保留；在“插件 → 可组合记忆”页面中重新开启后，即可恢复读取、写入与按需调用。')).toBeTruthy()
        expect(call.mock.calls.filter(([, endpoint]) => endpoints[id].includes(endpoint))).toHaveLength(before)
      }
      cleanup()
    }
  })

  it('links to the configuration on the Plugins page while that page offers navigation', async () => {
    const { connection } = createConnection({ layerSwitches: { runtime: false, documents: true, 'memory-spaces': true } })
    const configuration = new MnemonActionSeat()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" configuration={configuration} />)
    await screen.findByText('已连接')
    expect(screen.queryByRole('button', { name: '配置' })).toBeNull()

    const open = vi.fn()
    let withdraw = (): void => {}
    act(() => { withdraw = configuration.provide(open) })
    fireEvent.click(screen.getByRole('button', { name: '配置' }))
    expect(open).toHaveBeenCalledTimes(1)
    fireEvent.click(within(screen.getByRole('tablist', { name: 'Mnemon 页面' })).getByRole('tab', { name: '运行时记忆 · 已关闭' }))
    await screen.findByRole('heading', { name: '运行时记忆 已关闭' })
    fireEvent.click(screen.getByRole('button', { name: '前往配置' }))
    expect(open).toHaveBeenCalledTimes(2)

    act(() => withdraw())
    expect(screen.queryByRole('button', { name: '配置' })).toBeNull()
    expect(screen.queryByRole('button', { name: '前往配置' })).toBeNull()
  })

  it('keeps the tab of a layer whose component is off and says why', async () => {
    const { connection, call } = createConnection({ layerSwitches: { runtime: true, documents: true, 'memory-spaces': true }, componentsOff: ['documents'] })
    const configuration = new MnemonActionSeat()
    const open = vi.fn()
    configuration.provide(open)
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" configuration={configuration} runningSources={['runtime', 'memory-spaces']} />)
    await screen.findByText('已连接')
    const navigation = screen.getByRole('tablist', { name: 'Mnemon 页面' })
    // Documents keeps its place between the other two layers. Its component is
    // the layer's switch in the configuration, so the layer reads as off.
    await waitFor(() => expect(within(navigation).getAllByRole('tab').map(tab => tab.getAttribute('aria-label') ?? tab.textContent)).toEqual(['状态', '运行时记忆', '项目档案 · 已关闭', '记忆空间']))
    const cards = screen.getByRole('region', { name: 'Mnemon 运行状态' })
    expect(within(cards).getByText('在配置中重新开启后恢复')).toBeTruthy()
    expect(within(cards).queryByText('等待工作区')).toBeNull()

    const before = call.mock.calls.filter(([, endpoint]) => endpoint === 'documents').length
    fireEvent.click(within(navigation).getByRole('tab', { name: '项目档案 · 已关闭' }))
    await screen.findByRole('heading', { name: '项目档案 已关闭' })
    expect(screen.getByText('已有数据完整保留；在“插件 → 可组合记忆”页面中重新开启后，即可恢复读取、写入与按需调用。')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '前往配置' }))
    expect(open).toHaveBeenCalledOnce()
    expect(call.mock.calls.filter(([, endpoint]) => endpoint === 'documents')).toHaveLength(before)
  })

  it('says a layer whose component is on but not running is not running', async () => {
    const { connection } = createConnection({ layerSwitches: { runtime: true, documents: true, 'memory-spaces': true } })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" runningSources={['runtime', 'memory-spaces']} />)
    await screen.findByText('已连接')
    const navigation = screen.getByRole('tablist', { name: 'Mnemon 页面' })
    await waitFor(() => expect(within(navigation).getByRole('tab', { name: '项目档案 · 未运行' })).toBeTruthy())
    expect(within(screen.getByRole('region', { name: 'Mnemon 运行状态' })).getByText('组件已开启，但没有运行')).toBeTruthy()
    fireEvent.click(within(navigation).getByRole('tab', { name: '项目档案 · 未运行' }))
    await screen.findByRole('heading', { name: '项目档案 未运行' })
    expect(screen.getByText('这一层的组件已开启，但没有运行。')).toBeTruthy()
  })

  it('says memory is not in use while nothing is composed, and links to the configuration', async () => {
    const { connection } = createConnection({ composition: { serving: false, state: 'incomplete', diagnostics: [{ code: 'missing-strategy', message: 'No Memory Strategy contribution is installed.' }] } })
    const configuration = new MnemonActionSeat()
    const open = vi.fn()
    configuration.provide(open)
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" configuration={configuration} runningSources={[]} />)
    const notice = await screen.findByRole('alert', { name: '记忆未生效' })
    expect(within(notice).getByText('没有正在运行的主策略。对话照常进行，但不会使用记忆。')).toBeTruthy()
    expect(screen.queryByText('Mnemon 尚未就绪')).toBeNull()
    fireEvent.click(within(notice).getByRole('button', { name: '前往配置' }))
    expect(open).toHaveBeenCalledOnce()
    const navigation = screen.getByRole('tablist', { name: 'Mnemon 页面' })
    // The Sources run; nothing composes them, so each layer is not in use rather than stopped.
    expect(within(screen.getByRole('region', { name: 'Mnemon 运行状态' })).getAllByText('主策略恢复运行后可用')).toHaveLength(3)
    fireEvent.click(within(navigation).getByRole('tab', { name: '记忆空间 · 未生效' }))
    await screen.findByRole('heading', { name: '记忆空间 未生效' })
    expect(screen.getByText('记忆当前未生效，这一层暂不可用。')).toBeTruthy()
  })

  it('warns while a rejected change leaves the previous composition serving', async () => {
    const { connection } = createConnection({ composition: { serving: true, state: 'rejected', diagnostics: [{ code: 'composition-rejected', message: 'memory plugin dependency unavailable: dsh-mnemon-strategy-auto-capture requires source.durable-evidence' }] } })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" t={translateEn} locale="en" />)
    const notice = await screen.findByRole('status', { name: 'The latest component change did not take effect' })
    expect(within(notice).getByText('dsh-mnemon-strategy-auto-capture is missing source.durable-evidence. Memory still runs as composed before.')).toBeTruthy()
  })

  it('re-reads in place after a save that keeps the storage, and remounts for another storage', async () => {
    const { connection, call } = createConnection()
    let snapshot: ClientSettingsSnapshot<Config> = { ...settingsSnapshot, revision: 1 }
    const listeners = new Set<() => void>()
    const scope = { getSnapshot: () => snapshot, subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }, mutate: vi.fn(async () => {}) } as unknown as ClientSettingsScope<Config>
    const publish = (next: typeof snapshot) => act(() => { snapshot = next; for (const listener of [...listeners]) listener() })
    const statusReads = () => call.mock.calls.filter(([, endpoint]) => endpoint === 'status-summary').length
    render(<MnemonWorkbench connection={connection} settingsScope={scope} sessionId="session-1" />)
    await screen.findByText('已连接')
    const canvas = screen.getByTestId('mnemon-canvas')
    const reads = statusReads()

    publish({ ...snapshot, revision: 2, value: { ...snapshot.value, idleReview: { enabled: false } } as Config })
    await waitFor(() => expect(statusReads()).toBe(reads + 1))
    expect(screen.getByTestId('mnemon-canvas')).toBe(canvas)

    publish({ ...snapshot, revision: 3, value: { ...snapshot.value, storageScope: 'global' } })
    await waitFor(() => expect(screen.getByTestId('mnemon-canvas')).not.toBe(canvas))
  })

  it('re-reads the status when a component switches elsewhere and when the Sidebar reopens it', async () => {
    const { connection, call } = createConnection()
    const components = new MnemonChangeSignal()
    const statusReads = () => call.mock.calls.filter(([, endpoint]) => endpoint === 'status-summary').length
    const { rerender } = render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" componentChanges={components} />)
    await screen.findByText('已连接')
    const initial = statusReads()

    act(() => components.bump())
    await waitFor(() => expect(statusReads()).toBe(initial + 1))

    rerender(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" componentChanges={components} active={false} />)
    rerender(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" componentChanges={components} active />)
    await waitFor(() => expect(statusReads()).toBe(initial + 2))
  })

  it('reads the full status for the CLI version a summary does not carry yet', async () => {
    // A fresh install has no memory space loading, so only the missing version asks for it.
    const { connection, call } = createConnection({ summaryWithoutVersion: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    expect(await screen.findByText('Mnemon 0.1.2')).toBeTruthy()
    expect(screen.queryByText('等待版本信息')).toBeNull()
    expect(call).toHaveBeenCalledWith('/dsh-mnemon-read', 'status', expect.anything())
  })

  it.each([true, false])('shows a background-review warning in writable=%s sessions', async writable => {
    const error = 'CONTEXT_WINDOW_EXCEEDED: request (145508 tokens) exceeds the available context size (98304 tokens)'
    const { connection } = createConnection({ reviewError: error })
    render(<MnemonWorkbench connection={connection} settingsScope={writable ? settingsScope : readOnlySettingsScope} t={translateEn} locale="en" />)
    const warning = await screen.findByRole('alert', { name: 'Background review failed' })
    expect(within(warning).getByText(error)).toBeTruthy()
    expect(within(warning).getByText(/bounded checkpoint review/)).toBeTruthy()
    expect(screen.queryByText('System nominal')).toBeNull()
  })

  it('shows partial review receipts to a read-only observer without offering replay', async () => {
    const { connection } = createConnection({ reviewError: 'Model failed after confirmed commits', reviewPartial: true })
    render(<MnemonWorkbench connection={connection} settingsScope={readOnlySettingsScope} t={translateEn} locale="en" />)
    const warning = await screen.findByRole('alert', { name: 'Background review failed' })
    expect(within(warning).getByText(/Some writes committed/)).toBeTruthy()
    expect(within(warning).getByText(/review-run-255/)).toBeTruthy()
    expect(within(warning).getByText(/committed-document-255/)).toBeTruthy()
    expect(within(warning).getByText(/committed-runtime-revision/)).toBeTruthy()
    expect(within(warning).queryByRole('button')).toBeNull()
  })

  it('explains an automatic-review Team pause without presenting a failed child', async () => {
    const { connection } = createConnection({ reviewTeam: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} t={translateEn} locale="en" />)
    expect(await screen.findByText(/Idle review is paused by the Agent Teams compatibility setting/)).toBeTruthy()
    expect(screen.getByText(/select “Scoped child review”/u)).toBeTruthy()
    expect(screen.queryByRole('alert', { name: 'Background review failed' })).toBeNull()
  })

  it('shows the live graph, sidebar pages, and memory write dialog by default', async () => {
    const { connection } = createConnection()
    const { container } = render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    await selectWorkspaceTab('记忆空间')
    expect(screen.getByRole('heading', { name: '记忆空间' })).toBeTruthy()
    expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy()
    await waitFor(() => expect(screen.getAllByText('项目记忆空间').length).toBeGreaterThan(0))
    expect(screen.getByRole('switch', { name: '项目记忆空间读取开关' }).getAttribute('aria-checked')).toBe('true')
    expect(container.querySelector('[class*="telemetry"]')).toBeNull()
    expect(screen.getByRole('heading', { name: '记忆系统', level: 1 })).toBeTruthy()
    expect(container.querySelector('[data-mnemon-surface="sidebar"]')).toBeTruthy()
    expect(screen.queryByText('LLM-supervised 4-graph persistent memory for AI agents.')).toBeNull()
    expect(screen.queryByRole('img', { name: 'Mnemon' })).toBeNull()
    await waitFor(() => expect(screen.getByRole('img', { name: /Mnemon 实时记忆图谱/ })).toBeTruthy())
    expect(screen.getByRole('button', { name: '记忆空间: 项目记忆空间' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '实体: SQLite' })).toBeTruthy()
    expect(screen.getByText('1 个空间 · 2 条记忆 · 1 个实体')).toBeTruthy()
    const entityEdges = container.querySelectorAll('path[data-edge="entity"]')
    expect(entityEdges).toHaveLength(1)
    expect(entityEdges[0]?.getAttribute('data-source-kind')).toBe('entity')
    expect(entityEdges[0]?.getAttribute('data-target-kind')).toBe('memory')
    expect(entityEdges[0]?.getAttribute('data-source-id')).toBe(container.querySelector('[data-kind="entity"]')?.getAttribute('data-node-id'))
    fireEvent.click(await screen.findByRole('button', { name: '实体: SQLite' }))
    expect(screen.getByText('实体详情')).toBeTruthy()
    expect(screen.getByText('索引次数')).toBeTruthy()
    expect(screen.getByRole('toolbar', { name: '图谱布局' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '自然铺开' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '均匀重置' })).toBeTruthy()

    const graphNode = screen.getByRole('button', { name: /决策: 项目选择 SQLite/ })
    const naturalPosition = graphNode.getAttribute('transform')
    fireEvent.click(await screen.findByRole('button', { name: '均匀重置' }))
    await waitFor(() => expect(graphNode.getAttribute('transform')).not.toBe(naturalPosition))
    expect(screen.getByRole('status', { name: '布局状态：均匀布局' })).toBeTruthy()

    fireEvent.keyDown(graphNode, { key: 'ArrowRight' })
    expect(screen.getByRole('status', { name: '布局状态：自定义布局' })).toBeTruthy()

    await selectWorkspaceTab('运行时记忆')
    expect(screen.getByRole('heading', { name: '运行时记忆' })).toBeTruthy()
    await waitFor(() => expect(screen.getByText('用户偏好简洁中文回答。')).toBeTruthy())
    expect(screen.getByRole('region', { name: '用户画像' })).toBeTruthy()
    expect(screen.getByRole('region', { name: '工作记忆' })).toBeTruthy()
    fireEvent.click(await screen.findByRole('button', { name: '添加记忆' }))
    fireEvent.change(screen.getByRole('textbox', { name: '运行时记忆内容' }), { target: { value: '项目默认使用 pnpm。' } })
    fireEvent.click(await screen.findByRole('button', { name: '添加' }))
    await waitFor(() => expect(screen.getByText('项目默认使用 pnpm。')).toBeTruthy())

    await selectWorkspaceTab('检索')
    expect(screen.getByRole('heading', { name: '检索记忆' })).toBeTruthy()

    await selectWorkspaceTab('项目档案')
    expect(screen.getByRole('heading', { name: '项目档案' })).toBeTruthy()
    await waitFor(() => expect(screen.getByText('发布验证清单')).toBeTruthy())
    const documentReader = screen.getByRole('region', { name: '档案阅读器' })
    await waitFor(() => expect(documentReader.querySelector('h1')?.textContent).toBe('发布验证'))
    const selectedDocument = screen.getByRole('button', { name: /发布验证清单/ })
    expect(selectedDocument.getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(selectedDocument)
    expect(selectedDocument.getAttribute('aria-pressed')).toBe('true')
    expect(documentReader.querySelector('h1')?.textContent).toBe('发布验证')
    expect(documentReader.querySelector('strong')?.textContent).toBe('typecheck')
    expect(documentReader.querySelectorAll('li')).toHaveLength(2)
    expect(documentReader.querySelector('a[href="https://example.com/architecture"]')?.getAttribute('target')).toBe('_blank')
    expect(documentReader.querySelector('a[href^="javascript:"]')).toBeNull()
    expect(documentReader.querySelector('pre')).toBeNull()
    expect(screen.getByText('640 B / 10.0 MB')).toBeTruthy()
    expect(screen.getByText('`.mnemon/documents/index.json` 是控制面事实源；active 总量固定不超过 10 MB，archived 不计入上限，项目源文件不会被修改。')).toBeTruthy()

    await selectWorkspaceTab('实体')
    expect(screen.getByRole('heading', { name: '实体查阅' })).toBeTruthy()

    await openRememberDialog()
    expect(screen.getByRole('heading', { name: '存入记忆' })).toBeTruthy()
    expect(screen.getByRole('dialog', { name: '存入记忆' })).toBeTruthy()
    expect(screen.getByText('高级选项')).toBeTruthy()
    fireEvent.click((await screen.findByRole('dialog', { name: '存入记忆' })).querySelector('[data-dialog-close]')!)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '存入记忆' })).toBeNull())

    await selectWorkspaceTab('内容')
    expect(screen.getByRole('heading', { name: '记忆内容' })).toBeTruthy()

    await selectWorkspaceTab('状态')
    expect(screen.getByRole('heading', { name: '系统状态' })).toBeTruthy()
    expect(screen.queryByRole('tab', { name: '视图' })).toBeNull()
    expect(screen.queryByRole('button', { name: '记忆插件' })).toBeNull()
    expect(within(screen.getByRole('region', { name: 'Mnemon 运行状态' })).getAllByRole('article')).toHaveLength(4)
    expect(screen.queryByText('记忆子 Agent 可用')).toBeNull()
    expect(screen.queryByRole('heading', { name: '子 Agent 生命周期' })).toBeNull()
    expect(screen.queryByRole('heading', { name: '记忆系统流转' })).toBeNull()
    expect(screen.getByRole('heading', { name: '记忆 Provider' })).toBeTruthy()
    const providerStatus = screen.getByRole('region', { name: '记忆 Provider 状态' })
    const nativeProviderStatus = within(providerStatus).getByRole('article', { name: 'Mnemon Native 状态' })
    expect(within(nativeProviderStatus).getByText('Mnemon Native')).toBeTruthy()
    // Native leads the one Provider list as a peer of the third-party Providers.
    expect(within(providerStatus).getAllByRole('article')[0]).toBe(nativeProviderStatus)
    expect(within(providerStatus).getByText('OpenViking')).toBeTruthy()
    expect(within(providerStatus).getByText('连接正常')).toBeTruthy()
    // Providers that are off share one line instead of a row each.
    expect(within(providerStatus).queryByText('Mem0')).toBeNull()
    expect(within(providerStatus).getByText(/另有 \d+ 个 Provider 未启用/)).toBeTruthy()
    expect(screen.getByRole('heading', { name: '存储域' })).toBeTruthy()
    expect(within(screen.getByRole('region', { name: '存储域' })).getAllByRole('article')).toHaveLength(3)
    expect(screen.queryByText('后台状态')).toBeNull()
    expect(screen.getByText('/tmp/mnemon')).toBeTruthy()
  }, 10_000)

  it('keeps a Native Provider failure out of the dsh-mnemon engine summary', async () => {
    const { connection } = createConnection({ nativeUnhealthy: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    const engineStatus = await screen.findByRole('region', { name: 'Mnemon 运行状态' })
    expect(within(engineStatus).getByText('dsh-mnemon 0.1.2')).toBeTruthy()
    expect(within(engineStatus).getByText('插件运行正常')).toBeTruthy()
    expect(screen.getByText('系统正常')).toBeTruthy()
    expect(screen.queryByText('Mnemon 不可用')).toBeNull()

    const nativeStatus = screen.getByRole('article', { name: 'Mnemon Native 状态' })
    expect(within(nativeStatus).getByText(/^连接需要检查/u)).toBeTruthy()
    expect(within(nativeStatus).getByText('项目记忆空间: Mnemon Store 无法打开')).toBeTruthy()
  })

  it.each(['sidebar', 'builtin'] as const)('activates an additional memory space through the narrow %s control route without crashing the live graph', async surface => {
    const { connection, call } = createConnection({ withInactiveBody: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    await selectWorkspaceTab('记忆空间')
    const toggle = await screen.findByRole('switch', { name: '偏好记忆空间读取开关' })
    expect(toggle.getAttribute('aria-checked')).toBe('false')
    fireEvent.click(toggle)

    await waitFor(() => expect(screen.getByRole('switch', { name: '偏好记忆空间读取开关' }).getAttribute('aria-checked')).toBe('true'))
    expect(call).toHaveBeenCalledWith('/dsh-mnemon-activation', 'body', { memoryBodyId: 'preferences', active: true, sessionId: 'session-1' })
    expect(call).not.toHaveBeenCalledWith('/dsh-mnemon-write', 'body-update', expect.objectContaining({ memoryBodyId: 'preferences', active: true }))
    expect(screen.getByRole('button', { name: /偏好: 用户偏好简洁中文回答/ })).toBeTruthy()
    expect(screen.getByRole('img', { name: /Mnemon 实时记忆图谱，7 个元素/ })).toBeTruthy()
    expect(screen.getByRole('button', { name: '记忆空间: 偏好记忆空间' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '实体: DSH' })).toBeTruthy()
  })

  it.each(['sidebar', 'builtin'] as const)('keeps only %s activation controls writable when remote management is not authorized', async surface => {
    const { connection, call } = createConnection({ isLoopback: false, withInactiveBody: true })
    render(<MnemonWorkbench connection={connection} settingsScope={readOnlySettingsScope} sessionId="session-1" surface={surface} />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '检查版本' }))
    const versions = screen.getByRole('dialog', { name: '检查与更新版本' })
    await waitFor(() => expect(within(versions).getByText('Mnemon CLI')).toBeTruthy())
    expect(within(versions).queryByRole('button', { name: '更新' })).toBeNull()
    fireEvent.click(within(versions).getAllByRole('button', { name: '关闭' }).at(-1)!)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    expect(await screen.findByText('仅可切换激活状态')).toBeTruthy()
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    expect((screen.getByRole('button', { name: '存入记忆' }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.queryByRole('button', { name: '创建记忆空间' })).toBeNull()
    expect(screen.queryByRole('button', { name: '整理名称与说明' })).toBeNull()
    expect((screen.getByRole('button', { name: '编辑项目记忆空间' }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: '删除项目记忆空间' }) as HTMLButtonElement).disabled).toBe(true)

    fireEvent.click(await screen.findByRole('article', { name: '重新连接项目记忆空间' }))
    await waitFor(() => expect(call).toHaveBeenCalledWith('/api', 'body-reconnect', { memoryBodyId: 'project', sessionId: 'session-1' }))

    const toggle = screen.getByRole('switch', { name: '偏好记忆空间读取开关' }) as HTMLButtonElement
    expect(toggle.disabled).toBe(false)
    fireEvent.click(toggle)
    await waitFor(() => expect(toggle.getAttribute('aria-checked')).toBe('true'))
    expect(call).toHaveBeenCalledWith('/api', 'body', { memoryBodyId: 'preferences', active: true, sessionId: 'session-1' })
    expect(call.mock.calls.some(([channel]) => channel === '/dsh-mnemon-write')).toBe(false)
  })

  it('says why a remote page is read only when the Host withholds the management grant', async () => {
    const { connection } = createConnection({ isLoopback: false })
    render(<MnemonWorkbench connection={connection} settingsScope={staticSettingsScope<Config>({ ...settingsSnapshot, writable: false })} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    expect(screen.getByText(/^远程访问为只读：在 profile 的 mnemon 配置中设置 remoteAccess: trusted-host/)).toBeTruthy()
  })

  it('does not show the remote read-only reason on a local page', async () => {
    const { connection } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={staticSettingsScope<Config>({ ...settingsSnapshot, writable: false })} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    expect(screen.queryByText(/^远程访问为只读/)).toBeNull()
  })

  it('enables memory management controls from a writable authenticated Host snapshot', async () => {
    const { connection } = createConnection({ isLoopback: false, withInactiveBody: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())

    // A writable space shows no restriction tag in its header.
    expect(screen.queryByText('只读模式')).toBeNull()
    expect(screen.queryByText('仅可切换激活状态')).toBeNull()
    expect((screen.getByRole('button', { name: '存入记忆' }) as HTMLButtonElement).disabled).toBe(false)
    expect(screen.getByRole('button', { name: '创建记忆空间' })).toBeTruthy()
    expect(screen.getByRole('button', { name: '整理名称与说明' })).toBeTruthy()
    expect((screen.getByRole('button', { name: '编辑项目记忆空间' }) as HTMLButtonElement).disabled).toBe(false)
    expect((screen.getByRole('button', { name: '删除项目记忆空间' }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('keeps overview, recall, content, and entities aligned with each provider read contract', async () => {
    const { connection } = createConnection({ withProviderSources: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    const snapshot = await screen.findByRole('region', { name: '快照可观察范围' })
    expect(within(snapshot).getByText('真实关系图')).toBeTruthy()
    expect(within(snapshot).getByText('内容投影')).toBeTruthy()
    expect(within(snapshot).getByText('仅查询')).toBeTruthy()
    expect(within(snapshot).getByText(/2 条真实关系/)).toBeTruthy()
    expect(within(snapshot).getAllByText('mnemon').length).toBeGreaterThan(0)
    const openVikingSnapshot = within(snapshot).getByText('OpenViking 团队知识').closest('article')
    expect(openVikingSnapshot?.getAttribute('data-provider')).toBe('openviking')
    expect(openVikingSnapshot?.querySelector('[data-provider="openviking"]')?.textContent).toBe('OpenViking')
    expect(document.querySelector('[data-kind="space"][data-provider="mnemon-native"]')).toBeTruthy()

    const memoryTabs = screen.getByRole('tablist', { name: '记忆空间页面' })
    fireEvent.click(within(memoryTabs).getByRole('tab', { name: '检索' }))
    fireEvent.change(screen.getByRole('textbox', { name: '记忆查询' }), { target: { value: 'SQLite' } })
    fireEvent.click(await screen.findByRole('button', { name: '直接检索' }))
    const recallSources = await screen.findByRole('region', { name: '本次检索范围' })
    expect(within(recallSources).getByText('项目记忆空间')).toBeTruthy()
    expect(within(recallSources).getByText('Mem0 用户画像')).toBeTruthy()
    expect(within(recallSources).getByText('已连接 · 暂无内容')).toBeTruthy()
    const mem0RecallSource = within(recallSources).getByText('Mem0 用户画像').closest('article')
    expect(mem0RecallSource?.getAttribute('data-provider')).toBe('mem0')
    expect(mem0RecallSource?.querySelector('[data-provider="mem0"]')?.textContent).toBe('Mem0')

    fireEvent.click(within(memoryTabs).getByRole('tab', { name: '内容' }))
    const contentSources = await screen.findByRole('region', { name: 'Provider 内容模型' })
    const openVikingFilter = within(contentSources).getByRole('button', { name: /OpenViking 团队知识/ })
    fireEvent.click(openVikingFilter)
    expect(openVikingFilter.getAttribute('aria-pressed')).toBe('true')
    expect(await screen.findByRole('heading', { name: '查询型记忆空间等待问题' })).toBeTruthy()
    expect(within(contentSources).getByText('当前表面不可用')).toBeTruthy()
    fireEvent.click(openVikingFilter)
    expect(openVikingFilter.getAttribute('aria-pressed')).toBe('false')
    expect(within(contentSources).getByRole('button', { name: '全部 Provider' }).getAttribute('aria-pressed')).toBe('true')

    fireEvent.click(within(memoryTabs).getByRole('tab', { name: '实体' }))
    const entitySources = await screen.findByRole('region', { name: '实体能力范围' })
    expect(within(entitySources).getByText('实体索引')).toBeTruthy()
    expect(within(entitySources).getByText('Mem0 用户画像')).toBeTruthy()
    expect(within(entitySources).getByText('不支持')).toBeTruthy()
  })

  it('checks both product versions and only offers a safe supported update', async () => {
    const { connection, call } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('dsh-mnemon 0.1.2')).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '检查版本' }))
    const dialog = screen.getByRole('dialog', { name: '检查与更新版本' })
    await waitFor(() => expect(within(dialog).getByText('Mnemon CLI')).toBeTruthy())
    expect(within(dialog).getByText('/usr/local/bin/mnemon')).toBeTruthy()
    expect(within(dialog).getByText('dsh-mnemon')).toBeTruthy()
    expect(within(dialog).getByText('本地 Link')).toBeTruthy()
    expect(within(dialog).getByText('源码 · Profile web')).toBeTruthy()
    expect(within(dialog).getByText('/workspace/dsh-mnemon')).toBeTruthy()
    expect(within(dialog).getByText(/请在源码目录拉取并构建/)).toBeTruthy()
    expect(within(dialog).getAllByRole('button', { name: '更新' })).toHaveLength(1)

    fireEvent.click(within(dialog).getByRole('button', { name: '更新' }))
    await waitFor(() => expect(within(dialog).getByText('Mnemon CLI 已更新')).toBeTruthy())
    expect(call).toHaveBeenCalledWith('/dsh-mnemon-write', 'version-update', { component: 'mnemon' })
    await waitFor(() => expect(within(dialog).getByText('已是最新')).toBeTruthy())
    expect(call.mock.calls.filter(([, endpoint]) => endpoint === 'versions')).toHaveLength(2)
    await waitFor(() => expect(screen.getByText('Mnemon 0.2.0')).toBeTruthy())
  })

  it('says on every page which update waits for a DSH restart', async () => {
    const { connection } = createConnection({ restartPending: { running: '0.1.2', installed: '0.1.3' } })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    const reminder = await screen.findByRole('status', { name: 'dsh-mnemon 0.1.3 已安装' })
    expect(reminder.textContent).toContain('当前运行的仍是 0.1.2；重启 DSH 后生效，桌面版请完全退出后重新打开。')
    // The engine card names the version that runs.
    expect(screen.getByText('dsh-mnemon 0.1.2')).toBeTruthy()
    await selectWorkspaceTab('运行时记忆')
    expect(screen.getByRole('status', { name: 'dsh-mnemon 0.1.3 已安装' })).toBeTruthy()
  })

  it('names packages updated on their own until a restart, and stays quiet otherwise', async () => {
    const { connection } = createConnection({ restartPending: { running: '0.1.2', packages: ['dsh-mnemon-strategy-scoped', 'dsh-mnemon-strategy-general'] } })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    const reminder = await screen.findByRole('status', { name: 'dsh-mnemon-strategy-scoped、dsh-mnemon-strategy-general 已更新' })
    expect(reminder.textContent).toContain('重启 DSH 后生效')
    cleanup()
    render(<MnemonWorkbench connection={createConnection().connection} settingsScope={settingsScope} sessionId="session-1" />)
    await waitFor(() => expect(screen.getByText('dsh-mnemon 0.1.2')).toBeTruthy())
    expect(screen.queryByText(/重启 DSH 后生效/u)).toBeNull()
  })

  it('reopens the version dialog on Status in the client DSH swapped in during a Starter update', async () => {
    recordStarterUpdate({ from: '0.1.2', to: '0.1.3' })
    try {
      // The Host that ran the update still runs the version it loaded.
      const { connection, call } = createConnection({ restartPending: { running: '0.1.2', installed: '0.1.3' } })
      render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
      const dialog = await screen.findByRole('dialog', { name: '检查与更新版本' })
      await waitFor(() => expect(within(dialog).getByText('Mnemon CLI')).toBeTruthy())
      expect(call).not.toHaveBeenCalledWith('/dsh-mnemon-write', 'version-update', expect.anything())
      fireEvent.click(within(dialog).getAllByRole('button', { name: '关闭' }).at(-1)!)
      await waitFor(() => expect(screen.queryByRole('dialog', { name: '检查与更新版本' })).toBeNull())
      expect(pendingStarterUpdate()).toBeUndefined()
    } finally {
      clearStarterUpdate()
    }
  })

  it('does not reopen the version dialog once DSH has restarted onto the update', async () => {
    // The dialog was left open, DSH restarted, and the page reloaded within the record's lifetime.
    recordStarterUpdate({ from: '0.1.1', to: '0.1.2' })
    try {
      const { connection } = createConnection()
      render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
      await waitFor(() => expect(screen.getByText('dsh-mnemon 0.1.2')).toBeTruthy())
      await waitFor(() => expect(pendingStarterUpdate()).toBeUndefined())
      expect(screen.queryByRole('dialog', { name: '检查与更新版本' })).toBeNull()
    } finally {
      clearStarterUpdate()
    }
  })

  it('keeps a version check dismissible and moves focus into ready content', async () => {
    const { connection, resolveVersions } = createConnection({ versionsDeferred: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('dsh-mnemon 0.1.2')).toBeTruthy())
    const trigger = screen.getByRole('button', { name: '检查版本' })
    trigger.focus()
    fireEvent.click(trigger)
    const dialog = screen.getByRole('dialog', { name: '检查与更新版本' })
    // The header's close button and the footer's Close both dismiss the check.
    const [close, cancel] = [within(dialog).getAllByRole('button', { name: '关闭' })[0]!, within(dialog).getAllByRole('button', { name: '关闭' }).at(-1)!]
    expect(dialog.getAttribute('aria-busy')).toBe('true')
    expect(close.hasAttribute('disabled')).toBe(false)
    expect(cancel.hasAttribute('disabled')).toBe(false)
    expect(document.activeElement).toBe(close)

    await act(async () => { resolveVersions(0) })
    const recheck = await within(dialog).findByRole('button', { name: '重新检查' })
    await waitFor(() => expect(document.activeElement).toBe(recheck))

    fireEvent.click(recheck)
    expect(within(dialog).getByRole('button', { name: /^检查中/ }).hasAttribute('disabled')).toBe(true)
    expect(cancel.hasAttribute('disabled')).toBe(false)
    fireEvent.keyDown(window, { key: 'Escape' })
    expect(screen.queryByRole('dialog', { name: '检查与更新版本' })).toBeNull()
    expect(document.activeElement).toBe(trigger)
    await act(async () => { resolveVersions(1) })
    expect(screen.queryByRole('dialog', { name: '检查与更新版本' })).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it.each(['sidebar', 'builtin'] as const)('shares Source navigation, dialogs, filters, and the same skin in %s placement', async surface => {
    const { connection, call } = createConnection({ withInactiveBody: true })
    const onClose = vi.fn()
    const { container } = render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} {...(surface === 'sidebar' ? { onClose } : {})} />)

    expect(screen.queryByLabelText('存储范围：自定义') !== null).toBe(surface === 'sidebar')
    expect(screen.queryByLabelText('存储范围：—')).toBeNull()
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    expect(container.querySelector(`[data-mnemon-surface="${surface}"]`)).toBeTruthy()
    const sidebarHeader = screen.getByRole('heading', { name: '记忆系统', level: 1 }).closest('header')
    if (sidebarHeader === null) throw new Error('Sidebar header missing')
    if (surface === 'sidebar') {
      const back = within(sidebarHeader).getByRole('button', { name: '返回会话' })
      expect(sidebarHeader.firstElementChild).toBe(back)
      fireEvent.click(back)
      expect(onClose).toHaveBeenCalledTimes(1)
    } else {
      expect(within(sidebarHeader).queryByRole('button', { name: '返回会话' })).toBeNull()
    }
    expect(within(sidebarHeader).queryByLabelText('存储范围：自定义') !== null).toBe(surface === 'sidebar')
    expect(within(sidebarHeader).getByText('已连接')).toBeTruthy()
    expect(within(sidebarHeader).queryByText(/个已激活/)).toBeNull()
    expect(screen.getByRole('heading', { name: '记忆系统', level: 1 })).toBeTruthy()
    expect(screen.queryByRole('heading', { name: 'Mnemon', level: 1 })).toBeNull()
    expect(screen.queryByRole('img', { name: 'Mnemon' })).toBeNull()
    expect(screen.queryByRole('region', { name: '记忆统计' })).toBeNull()
    expect(screen.queryByText('运行与诊断')).toBeNull()

    const navigation = screen.getByRole('tablist', { name: 'Mnemon 页面' })
    const items = within(navigation).getAllByRole('tab')
    const statusItem = within(navigation).getByRole('tab', { name: '状态' })
    const runtimeItem = within(navigation).getByRole('tab', { name: '运行时记忆' })
    const bodiesItem = within(navigation).getByRole('tab', { name: '记忆空间' })
    const documentsItem = within(navigation).getByRole('tab', { name: '项目档案' })
    expect(items).toHaveLength(4)
    expect(items.map(item => item.textContent)).toEqual(['状态', '运行时记忆', '项目档案', '记忆空间'])
    expect(statusItem.hasAttribute('aria-current')).toBe(false)
    expect(statusItem.hasAttribute('data-active')).toBe(true)
    expect(bodiesItem.hasAttribute('aria-current')).toBe(false)
    expect(screen.getByRole('tablist', { name: 'Mnemon 页面' }).hasAttribute('aria-orientation')).toBe(false)

    const canvas = screen.getByTestId('mnemon-canvas')
    expect(canvas.hasAttribute('data-lock-page-header')).toBe(true)
    canvas.scrollTop = 240
    fireEvent.click(bodiesItem)
    expect(canvas.scrollTop).toBe(0)
    expect(canvas.hasAttribute('data-lock-page-header')).toBe(false)
    expect(statusItem.hasAttribute('aria-current')).toBe(false)
    expect(statusItem.hasAttribute('data-active')).toBe(false)
    expect(bodiesItem.hasAttribute('aria-current')).toBe(false)
    expect(bodiesItem.hasAttribute('data-active')).toBe(true)
    const memoryTablist = screen.getByRole('tablist', { name: '记忆空间页面' })
    const memoryTabs = within(memoryTablist).getAllByRole('tab')
    const overviewTab = within(memoryTablist).getByRole('tab', { name: '概览' })
    const searchTab = within(memoryTablist).getByRole('tab', { name: '检索' })
    const rememberAction = screen.getByRole('button', { name: '存入记忆' })
    expect(memoryTabs).toHaveLength(4)
    expect(overviewTab.getAttribute('aria-selected')).toBe('true')
    expect(rememberAction.className).toContain('primaryButton')
    expect(screen.getByRole('heading', { name: '记忆空间', level: 2 })).toBeTruthy()
    expect(screen.getByText('由 Mnemon Native 或第三方 Provider 承载的长期记忆；已激活的空间参与召回。')).toBeTruthy()
    expect(screen.getByRole('heading', { name: '概览', level: 2 })).toBeTruthy()
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())

    const createBodyButton = screen.getByRole('button', { name: '创建记忆空间' })
    createBodyButton.focus()
    fireEvent.click(createBodyButton)
    const bodyCreateDialog = screen.getByRole('dialog', { name: '创建记忆空间' })
    const bodyCreateName = within(bodyCreateDialog).getByRole('textbox', { name: '新记忆空间名称' })
    const bodyCreateAction = within(bodyCreateDialog).getByRole('button', { name: '创建' })
    expect(bodyCreateName).toBeTruthy()
    expect(within(bodyCreateDialog).getByRole('textbox', { name: '新记忆空间描述' })).toBeTruthy()
    expect(bodyCreateDialog.getAttribute('aria-labelledby')).toBeTruthy()
    expect(bodyCreateDialog.getAttribute('aria-describedby')).toBeTruthy()
    expect(bodyCreateAction.getAttribute('form')).toBe(bodyCreateName.closest('form')?.id)
    expect(bodyCreateAction.closest('footer')?.parentElement).toBe(bodyCreateDialog)
    const bodyCreateCancel = within(bodyCreateDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (bodyCreateCancel === undefined) throw new Error('memory space create cancel button missing')
    fireEvent.click(bodyCreateCancel)
    expect(screen.queryByRole('dialog', { name: '创建记忆空间' })).toBeNull()
    expect(document.activeElement).toBe(createBodyButton)

    fireEvent.click(await screen.findByRole('button', { name: '编辑项目记忆空间' }))
    const bodyDialog = screen.getByRole('dialog', { name: '编辑项目记忆空间' })
    const bodyName = within(bodyDialog).getByRole('textbox', { name: '名称' })
    const bodySave = within(bodyDialog).getByRole('button', { name: '保存' })
    expect(bodyName).toBeTruthy()
    expect(within(bodyDialog).getByRole('textbox', { name: '路由说明' })).toBeTruthy()
    expect(bodySave.getAttribute('form')).toBe(bodyName.closest('form')?.id)
    expect(bodySave.closest('footer')?.parentElement).toBe(bodyDialog)
    const bodyCancel = within(bodyDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (bodyCancel === undefined) throw new Error('memory space cancel button missing')
    fireEvent.click(bodyCancel)
    expect(screen.queryByRole('dialog', { name: '编辑项目记忆空间' })).toBeNull()

    fireEvent.click(await screen.findByRole('button', { name: '删除项目记忆空间' }))
    const deleteDialog = screen.getByRole('dialog', { name: '删除“项目记忆空间”？' })
    expect(within(deleteDialog).getByText(/永久删除这个记忆空间及其中的全部记忆与关系/)).toBeTruthy()
    const deleteCancel = within(deleteDialog).getAllByRole('button', { name: '取消' }).at(-1)
    expect(document.activeElement).toBe(deleteCancel)
    expect(deleteCancel?.closest('footer')?.parentElement).toBe(deleteDialog)
    fireEvent.click(within(deleteDialog).getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '删除“项目记忆空间”？' })).toBeNull())
    expect(call).toHaveBeenCalledWith(expect.anything(), 'body-delete', { memoryBodyId: 'project', sessionId: 'session-1' })

    fireEvent.click(rememberAction)
    const rememberDialog = screen.getByRole('dialog', { name: '存入记忆' })
    const rememberCandidate = within(rememberDialog).getByRole('textbox', { name: '候选内容' })
    const rememberSubmit = within(rememberDialog).getByRole('button', { name: '交给任务 Agent' })
    expect(rememberCandidate).toBeTruthy()
    expect(rememberSubmit.getAttribute('form')).toBe(rememberCandidate.closest('form')?.id)
    expect(rememberSubmit.closest('footer')?.parentElement).toBe(rememberDialog)
    const advanced = rememberDialog.querySelector('details')
    expect(advanced?.hasAttribute('open')).toBe(false)
    fireEvent.click(within(rememberDialog).getByText('高级选项'))
    expect(advanced?.hasAttribute('open')).toBe(true)
    const rememberCancel = within(rememberDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (rememberCancel === undefined) throw new Error('remember cancel button missing')
    fireEvent.click(rememberCancel)
    expect(screen.queryByRole('dialog', { name: '存入记忆' })).toBeNull()
    expect(overviewTab.getAttribute('aria-selected')).toBe('true')

    fireEvent.click(searchTab)
    expect(searchTab.getAttribute('aria-selected')).toBe('true')
    expect(screen.getByRole('heading', { name: '检索记忆', level: 2 })).toBeTruthy()
    fireEvent.click(statusItem)
    expect(screen.queryByRole('tablist', { name: '记忆空间页面' })).toBeNull()
    fireEvent.click(bodiesItem)
    expect(within(screen.getByRole('tablist', { name: '记忆空间页面' })).getByRole('tab', { name: '检索' }).getAttribute('aria-selected')).toBe('true')
    expect(screen.getByRole('heading', { name: '检索记忆', level: 2 })).toBeTruthy()

    fireEvent.click(runtimeItem)
    expect(canvas.hasAttribute('data-lock-page-header')).toBe(true)
    fireEvent.click(await screen.findByRole('button', { name: '添加记忆' }))
    const runtimeDialog = screen.getByRole('dialog', { name: '添加运行时记忆' })
    expect(within(runtimeDialog).getByRole('textbox', { name: '运行时记忆内容' })).toBeTruthy()
    const runtimeCancel = within(runtimeDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (runtimeCancel === undefined) throw new Error('runtime cancel button missing')
    fireEvent.click(runtimeCancel)
    expect(screen.queryByRole('dialog', { name: '添加运行时记忆' })).toBeNull()
    const runtimeList = screen.getByRole('region', { name: '运行时记忆列表' })
    expect(within(runtimeList).getByRole('group', { name: '运行时记忆范围' })).toBeTruthy()
    expect(within(runtimeList).getByRole('textbox', { name: '筛选运行时记忆' })).toBeTruthy()
    fireEvent.click(await within(runtimeList).findByRole('button', { name: '编辑' }))
    const runtimeEditDialog = screen.getByRole('dialog', { name: '编辑运行时记忆' })
    expect(within(runtimeEditDialog).getByRole('textbox', { name: '编辑运行时记忆' })).toBeTruthy()
    const runtimeEditCancel = within(runtimeEditDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (runtimeEditCancel === undefined) throw new Error('runtime edit cancel button missing')
    fireEvent.click(runtimeEditCancel)
    expect(screen.queryByRole('dialog', { name: '编辑运行时记忆' })).toBeNull()
    fireEvent.click(within(runtimeList).getByRole('button', { name: '移除' }))
    const runtimeRemoveDialog = screen.getByRole('dialog', { name: '移除运行时记忆？' })
    expect(within(runtimeRemoveDialog).getByText(/不再随每轮上下文加载/)).toBeTruthy()
    const runtimeRemoveCancel = within(runtimeRemoveDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (runtimeRemoveCancel === undefined) throw new Error('runtime remove cancel button missing')
    expect(runtimeRemoveCancel.closest('footer')?.parentElement).toBe(runtimeRemoveDialog)
    fireEvent.click(runtimeRemoveCancel)
    expect(screen.queryByRole('dialog', { name: '移除运行时记忆？' })).toBeNull()

    fireEvent.click(bodiesItem)
    expect(canvas.hasAttribute('data-lock-page-header')).toBe(false)
    const contentTab = within(screen.getByRole('tablist', { name: '记忆空间页面' })).getByRole('tab', { name: '内容' })
    fireEvent.click(contentTab)
    fireEvent.click(await screen.findByRole('button', { name: '忘记' }))
    const forgetDialog = screen.getByRole('dialog', { name: '软删除这条记忆？' })
    expect(within(forgetDialog).getByText('项目选择 SQLite，因为需要单文件部署。')).toBeTruthy()
    const forgetCancel = within(forgetDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (forgetCancel === undefined) throw new Error('forget cancel button missing')
    expect(forgetCancel.closest('footer')?.parentElement).toBe(forgetDialog)
    fireEvent.click(forgetCancel)
    expect(screen.queryByRole('dialog', { name: '软删除这条记忆？' })).toBeNull()

    fireEvent.click(documentsItem)
    expect(canvas.hasAttribute('data-lock-page-header')).toBe(true)
    fireEvent.click(await screen.findByRole('button', { name: '新建档案' }))
    const documentDialog = screen.getByRole('dialog', { name: '创建托管档案' })
    expect(within(documentDialog).getByRole('textbox', { name: '标题' })).toBeTruthy()
    const documentCancel = within(documentDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (documentCancel === undefined) throw new Error('document cancel button missing')
    fireEvent.click(documentCancel)
    expect(screen.queryByRole('dialog', { name: '创建托管档案' })).toBeNull()
    const documentReader = screen.getByRole('region', { name: '档案阅读器' })
    fireEvent.click(await within(documentReader).findByRole('button', { name: '编辑' }))
    const documentEditDialog = screen.getByRole('dialog', { name: '编辑活跃档案' })
    expect(within(documentEditDialog).getByRole('textbox', { name: '标题' })).toBeTruthy()
    const documentEditCancel = within(documentEditDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (documentEditCancel === undefined) throw new Error('document edit cancel button missing')
    fireEvent.click(documentEditCancel)
    expect(screen.queryByRole('dialog', { name: '编辑活跃档案' })).toBeNull()
    fireEvent.click(within(documentReader).getByRole('button', { name: '归档' }))
    const documentArchiveDialog = screen.getByRole('dialog', { name: '确认建立 Mnemon 索引并迁移这份档案？' })
    expect(within(documentArchiveDialog).getByText(/Host 校验后写入冷索引/)).toBeTruthy()
    const documentArchiveCancel = within(documentArchiveDialog).getAllByRole('button', { name: '取消' }).at(-1)
    if (documentArchiveCancel === undefined) throw new Error('document archive cancel button missing')
    expect(documentArchiveCancel.closest('footer')?.parentElement).toBe(documentArchiveDialog)
    fireEvent.click(documentArchiveCancel)
    expect(screen.queryByRole('dialog', { name: '确认建立 Mnemon 索引并迁移这份档案？' })).toBeNull()
    expect(screen.queryByRole('img', { name: 'Mnemon' })).toBeNull()
  })

  it('shows a branch badge on scoped runtime entries and a branch input in the add dialog', async () => {
    const { connection } = createConnection({ runtimeBranch: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    await screen.findByText('已连接')
    const navigation = screen.getByRole('tablist', { name: 'Mnemon 页面' })
    fireEvent.click(within(navigation).getByRole('tab', { name: '运行时记忆' }))
    expect(await screen.findByText('feat/branch-ui 分支上决定用 v2 渲染管线')).toBeTruthy()
    expect(screen.getByText('feat/branch-ui', { selector: '[title]' })).toBeTruthy()
    fireEvent.click(await screen.findByRole('button', { name: '添加记忆' }))
    const runtimeDialog = screen.getByRole('dialog', { name: '添加运行时记忆' })
    expect(within(runtimeDialog).getByRole('textbox', { name: '适用分支' })).toBeTruthy()
  })

  it('keeps DSH activation independent from the protected Mnemon default Store', async () => {
    const { connection } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByText('Mnemon 默认')).toBeTruthy())
    expect(screen.getByText(/点击卡片检查连接，开关决定它是否参与召回/)).toBeTruthy()
    const toggle = screen.getByRole('switch', { name: '项目记忆空间读取开关' })
    expect(toggle.hasAttribute('disabled')).toBe(false)
    fireEvent.click(toggle)
    await waitFor(() => expect(toggle.getAttribute('aria-checked')).toBe('false'))
    expect(screen.getByText('Mnemon 默认')).toBeTruthy()
    const deleteButton = screen.getByRole('button', { name: '删除项目记忆空间' })
    expect(deleteButton.hasAttribute('disabled')).toBe(true)
    expect(deleteButton.getAttribute('title')).toContain('至少一个原生 Store')
  })

  it('keeps the Memory Space directory interactive while the live graph is still pending', async () => {
    const { connection } = createConnection({ graphPending: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    expect(await screen.findByText('项目记忆空间')).toBeTruthy()
    expect(screen.getByRole('switch', { name: '项目记忆空间读取开关' }).hasAttribute('disabled')).toBe(false)
    expect(screen.getByText('正在同步多记忆空间实时快照…')).toBeTruthy()
    expect(screen.getAllByRole('status', { name: '正在加载多记忆空间实时快照' })).toHaveLength(1)
  })

  it('uses the card status signal as the only per-space reconnect spinner', async () => {
    const { connection, call } = createConnection({ reconnectPending: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    const title = await screen.findByText('项目记忆空间')
    const card = title.closest('article')
    if (card === null) throw new Error('Memory Space card missing')
    fireEvent.click(card)

    await waitFor(() => expect(call).toHaveBeenCalledWith(expect.anything(), 'body-reconnect', expect.objectContaining({ memoryBodyId: 'project' })))
    expect(card.hasAttribute('data-reconnecting')).toBe(true)
    expect(within(card).getByText('重连中')).toBeTruthy()
    expect(card.querySelector('[class*="bodySignal"]')).toBeTruthy()
  })

  it('runs one full entry sync and keeps later card sync scoped to one Memory Space', async () => {
    const interval = vi.spyOn(window, 'setInterval')
    const { connection, call } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    const title = await screen.findByText('项目记忆空间')
    await waitFor(() => expect(screen.getByText('存储正常')).toBeTruthy())
    expect(screen.getByText('上次全量同步：刚刚')).toBeTruthy()
    // The whole directory syncs again from the workspace's refresh, not from a button of its own.
    expect(screen.queryByRole('button', { name: '立即同步' })).toBeNull()
    expect(interval.mock.calls.some(([, delay]) => delay === 15_000)).toBe(false)

    const wholeSyncs = () => call.mock.calls.filter(([, endpoint]) => endpoint === 'bodies' || endpoint === 'body-directory').length
    const entrySyncCount = wholeSyncs()
    expect(entrySyncCount).toBeGreaterThan(0)
    const card = title.closest('article')
    if (card === null) throw new Error('Memory Space card missing')
    fireEvent.click(card)

    await waitFor(() => expect(call).toHaveBeenCalledWith(expect.anything(), 'body-reconnect', expect.objectContaining({ memoryBodyId: 'project' })))
    await waitFor(() => expect(card.hasAttribute('data-reconnecting')).toBe(false))
    expect(wholeSyncs()).toBe(entrySyncCount)
    fireEvent.click(screen.getByRole('button', { name: '刷新' }))
    await waitFor(() => expect(wholeSyncs()).toBeGreaterThan(entrySyncCount))
    interval.mockRestore()
  })

  it('shows one page-level spinner while deep status checks continue in the background', async () => {
    const { connection } = createConnection({ statusPending: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '状态' }))
    expect(await screen.findByRole('heading', { name: '系统状态' })).toBeTruthy()
    expect(screen.getAllByRole('status', { name: '检查中…' })).toHaveLength(1)
    expect(screen.getByText('dsh-mnemon 0.1.2')).toBeTruthy()
    expect(screen.queryByText('连接需要检查')).toBeNull()
  })

  it('edits an existing Memory Space name and description', async () => {
    const { connection, call } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    await selectWorkspaceTab('记忆空间')

    fireEvent.click(await screen.findByRole('button', { name: '编辑项目记忆空间' }))
    fireEvent.change(screen.getByRole('textbox', { name: '名称' }), { target: { value: '项目决策空间' } })
    fireEvent.change(screen.getByRole('textbox', { name: '路由说明' }), { target: { value: '存放架构与交付决策。' } })
    fireEvent.click(await screen.findByRole('button', { name: '保存' }))

    await waitFor(() => expect(screen.getAllByText('项目决策空间').length).toBeGreaterThan(0))
    expect(screen.getByText('存放架构与交付决策。')).toBeTruthy()
    expect(screen.queryByRole('button', { name: '编辑项目决策空间' })).toBeTruthy()
    expect(call).toHaveBeenCalledWith(expect.anything(), 'body-update', { memoryBodyId: 'project', name: '项目决策空间', description: '存放架构与交付决策。', sessionId: 'session-1' })
  })

  it.each(['sidebar', 'builtin'] as const)('generates active Memory Space metadata with the %s task context', async surface => {
    const { connection, call } = createConnection({ withInactiveBody: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '整理名称与说明' }))
    const dialog = screen.getByRole('dialog', { name: 'AI 维护记忆空间元信息' })
    const project = within(dialog).getByRole('checkbox', { name: /项目记忆空间/ })
    expect(within(dialog).queryByRole('checkbox', { name: /偏好记忆空间/ })).toBeNull()
    fireEvent.click(project)
    fireEvent.click(within(dialog).getByRole('button', { name: 'AI 生成（1）' }))

    await waitFor(() => expect(within(dialog).getByText('产品决策')).toBeTruthy())
    expect(call).toHaveBeenCalledWith(expect.anything(), 'body-metadata-maintain', { memoryBodyIds: ['project'], ...(surface === 'builtin' ? { sessionId: 'session-1' } : {}) })
    expect(screen.getByRole('dialog', { name: 'AI 维护记忆空间元信息' })).toBe(dialog)
    expect(within(dialog).getByText('记录稳定的产品范围、架构取舍与依据，在规划和复盘产品方向时召回。')).toBeTruthy()
    expect(within(dialog).getByText('产品决策').closest('label')?.hasAttribute('data-refreshed')).toBe(true)
    expect(screen.queryByText('已更新产品决策元信息。')).toBeNull()
  })

  it('uses an available lifecycle Agent when the standalone Memory UI has no selected session', async () => {
    const { connection, call } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    const action = screen.getByRole('button', { name: '整理名称与说明' })
    expect(action.hasAttribute('disabled')).toBe(false)
    fireEvent.click(action)
    const dialog = screen.getByRole('dialog', { name: 'AI 维护记忆空间元信息' })
    expect(within(dialog).getByText(/最快的原生查询路径读取少量样本/)).toBeTruthy()
    fireEvent.click(within(dialog).getByRole('checkbox', { name: /项目记忆空间/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'AI 生成（1）' }))

    await waitFor(() => expect(within(dialog).getByText('产品决策')).toBeTruthy())
    expect(call).toHaveBeenCalledWith(expect.anything(), 'body-metadata-maintain', { memoryBodyIds: ['project'] })
  })

  it('opens metadata maintenance from the status directory while detailed Memory data is pending', async () => {
    const { connection } = createConnection({ directoryPending: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    const action = await screen.findByRole('button', { name: '整理名称与说明' })
    expect(action.hasAttribute('disabled')).toBe(false)
    fireEvent.click(action)

    const dialog = screen.getByRole('dialog', { name: 'AI 维护记忆空间元信息' })
    expect(within(dialog).getByRole('checkbox', { name: /项目记忆空间/ })).toBeTruthy()
  })

  it('keeps metadata maintenance runnable from the selected workspace when the conversation points elsewhere', async () => {
    const { connection, call } = createConnection({ workspaceMismatch: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" workspaceId="workspace-2" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    const action = screen.getByRole('button', { name: '整理名称与说明' })
    expect(action.hasAttribute('disabled')).toBe(false)
    fireEvent.click(action)

    const dialog = screen.getByRole('dialog', { name: 'AI 维护记忆空间元信息' })
    expect(within(dialog).queryByText('暂时无法运行：未找到与当前记忆范围匹配的活跃 Agent')).toBeNull()
    fireEvent.click(within(dialog).getByRole('checkbox', { name: /项目记忆空间/ }))
    expect(within(dialog).getByRole('button', { name: 'AI 生成（1）' }).hasAttribute('disabled')).toBe(false)
    fireEvent.click(within(dialog).getByRole('button', { name: 'AI 生成（1）' }))
    await waitFor(() => expect(call).toHaveBeenCalledWith(expect.anything(), 'body-metadata-maintain', { memoryBodyIds: ['project'], workspaceId: 'workspace-2' }))
  })

  it.each(['sidebar', 'builtin'] as const)('isolates concurrent metadata tasks and partial failures in %s', async surface => {
    const { connection, call } = createConnection({ withSecondActiveBody: true, metadataFailureBodyId: 'preferences' })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '整理名称与说明' }))
    const dialog = screen.getByRole('dialog', { name: 'AI 维护记忆空间元信息' })
    fireEvent.click(within(dialog).getByRole('checkbox', { name: /项目记忆空间/ }))
    fireEvent.click(within(dialog).getByRole('checkbox', { name: /偏好记忆空间/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'AI 生成（2）' }))

    await waitFor(() => expect(within(dialog).getByText('产品决策')).toBeTruthy())
    await waitFor(() => expect(within(dialog).getByText('失败：preferences subagent failed')).toBeTruthy())
    expect(call).toHaveBeenCalledWith(expect.anything(), 'body-metadata-maintain', { memoryBodyIds: ['project'], ...(surface === 'builtin' ? { sessionId: 'session-1' } : {}) })
    expect(call).toHaveBeenCalledWith(expect.anything(), 'body-metadata-maintain', { memoryBodyIds: ['preferences'], ...(surface === 'builtin' ? { sessionId: 'session-1' } : {}) })
    expect(call).not.toHaveBeenCalledWith(expect.anything(), 'body-metadata-maintain', { memoryBodyIds: ['project', 'preferences'] })
    expect(within(dialog).getByText('产品决策').closest('label')?.hasAttribute('data-refreshed')).toBe(true)
    expect(within(dialog).getByText('偏好记忆空间').closest('label')?.hasAttribute('data-failed')).toBe(true)
    expect(screen.getByRole('dialog', { name: 'AI 维护记忆空间元信息' })).toBe(dialog)
    expect(screen.getByRole('button', { name: '整理名称与说明' }).hasAttribute('disabled')).toBe(false)
  })

  it('adds OpenViking through the existing Memory Space creation flow', async () => {
    const { connection, call } = createConnection({ withInactiveBody: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '创建记忆空间' }))
    const dialog = screen.getByRole('dialog', { name: '创建记忆空间' })
    expect(within(dialog).getByRole('radio', { name: /Mnemon Native/ }).getAttribute('value')).toBe('mnemon-native')
    const openVikingChoice = within(dialog).getByRole('radio', { name: /OpenViking/ })
    expect(openVikingChoice.closest('label')?.querySelector('[data-provider-icon="openviking"]')).toBeTruthy()
    fireEvent.click(openVikingChoice)
    expect(openVikingChoice.closest('label')?.hasAttribute('data-selected')).toBe(true)
    fireEvent.change(within(dialog).getByRole('textbox', { name: '新记忆空间名称' }), { target: { value: '团队 OpenViking' } })
    fireEvent.change(within(dialog).getByRole('textbox', { name: '新记忆空间描述' }), { target: { value: '跨项目共享的团队长期记忆。' } })
    fireEvent.change(within(dialog).getByRole('textbox', { name: '记忆范围 URI' }), { target: { value: 'viking://user/team/memories' } })
    fireEvent.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() => expect(screen.queryByRole('dialog', { name: '创建记忆空间' })).toBeNull())
    expect(call).toHaveBeenCalledWith(expect.anything(), 'body-create', expect.objectContaining({
      name: '团队 OpenViking',
      description: '跨项目共享的团队长期记忆。',
      providerId: 'openviking',
      connection: expect.objectContaining({
        targetUri: 'viking://user/team/memories',
      }),
      sessionId: 'session-1',
    }))
  })

  it('keeps manual creation explicit and saves automatic Provider selection as a distillation strategy', async () => {
    const { connection, call } = createConnection({ withInactiveBody: true })
    const mutate = vi.fn(async () => {})
    const strategySettingsScope = { ...settingsScope, mutate }
    render(<MnemonWorkbench connection={connection} settingsScope={strategySettingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '创建记忆空间' }))
    const createDialog = screen.getByRole('dialog', { name: '创建记忆空间' })
    expect(within(createDialog).queryByRole('radio', { name: /智能选择/ })).toBeNull()
    expect(within(createDialog).getByText(/明确的手动创建/)).toBeTruthy()
    fireEvent.click(within(createDialog).getAllByRole('button', { name: '取消' }).at(-1)!)

    fireEvent.click(await screen.findByRole('button', { name: '新空间的 Provider' }))
    const dialog = screen.getByRole('dialog', { name: '新空间的 Provider' })
    await waitFor(() => expect(within(dialog).getByRole('radio', { name: /智能选择/ })).toBeTruthy())
    const manualMode = within(dialog).getByRole('radio', { name: /手动指定/ }) as HTMLInputElement
    const saveStrategy = within(dialog).getByRole('button', { name: '保存' })
    await waitFor(() => expect(document.activeElement).toBe(manualMode))
    expect(dialog.getAttribute('aria-busy')).toBeNull()
    expect(manualMode.checked).toBe(true)
    expect(saveStrategy.getAttribute('form')).toBe(manualMode.closest('form')?.id)
    expect(saveStrategy.closest('footer')?.parentElement).toBe(dialog)
    fireEvent.click(within(dialog).getByRole('radio', { name: /智能选择/ }))
    fireEvent.change(within(dialog).getByRole('textbox', { name: '策略 Prompt（可选）' }), { target: { value: '这是团队知识；满足精确写入后优先共享。' } })
    fireEvent.click(within(dialog).getByRole('button', { name: /^软偏好/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: '共享优先' }))
    const exactWrite = within(dialog).getByRole('checkbox', { name: '精确写入' })
    const openVikingCandidate = within(dialog).getByRole('checkbox', { name: /OpenViking/ })
    fireEvent.click(exactWrite)
    fireEvent.click(openVikingCandidate)
    expect(exactWrite.closest('label')?.hasAttribute('data-selected')).toBe(true)
    expect(openVikingCandidate.closest('label')?.hasAttribute('data-selected')).toBe(true)
    fireEvent.change(within(dialog).getByRole('textbox', { name: '记忆范围 URI' }), { target: { value: 'viking://user/team/memories' } })
    fireEvent.click(saveStrategy)

    await waitFor(() => expect(screen.queryByRole('dialog', { name: '新空间的 Provider' })).toBeNull())
    expect(mutate).toHaveBeenCalledWith([{ op: 'set', path: ['persistenceStrategy'], value: {
      mode: 'automatic',
      providerId: 'mnemon-native',
      prompt: '这是团队知识；满足精确写入后优先共享。',
      rules: {
        allowedProviderIds: ['mnemon-native', 'openviking'],
        dataBoundary: 'allow-remote',
        preference: 'shared-first',
        requiredCapabilities: ['exact-write'],
      },
      providerConnections: {
        openviking: expect.objectContaining({ targetUri: 'viking://user/team/memories' }),
      },
    } }])
    expect(call.mock.calls.some(([, endpoint]) => endpoint === 'body-create')).toBe(false)
  })

  it('shows every third-party engine under the existing Memory Space flow', async () => {
    const { connection, call } = createConnection({ withInactiveBody: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '创建记忆空间' }))
    const dialog = screen.getByRole('dialog', { name: '创建记忆空间' })
    for (const provider of ['Mnemon Native', 'OpenViking', 'Honcho', 'Mem0', 'Hindsight', 'Holographic', 'RetainDB', 'ByteRover', 'Supermemory']) {
      expect(within(dialog).getByRole('radio', { name: new RegExp(provider) })).toBeTruthy()
    }

    fireEvent.click(within(dialog).getByRole('radio', { name: /Mem0/ }))
    fireEvent.change(within(dialog).getByRole('textbox', { name: '新记忆空间名称' }), { target: { value: '用户画像' } })
    fireEvent.change(within(dialog).getByRole('textbox', { name: '新记忆空间描述' }), { target: { value: '跨会话用户偏好与事实。' } })
    fireEvent.click(within(dialog).getByRole('button', { name: '创建' }))

    await waitFor(() => expect(screen.queryByRole('dialog', { name: '创建记忆空间' })).toBeNull())
    expect(call).toHaveBeenCalledWith(expect.anything(), 'body-create', expect.objectContaining({
      providerId: 'mem0',
      connection: expect.objectContaining({ userId: 'dsh-user', agentId: 'dsh', rerank: false }),
    }))
  })

  it('enforces the local-only rule while keeping local third-party engines eligible', async () => {
    const { connection } = createConnection({ withInactiveBody: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByRole('region', { name: '记忆空间目录' })).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '新空间的 Provider' }))
    const dialog = screen.getByRole('dialog', { name: '新空间的 Provider' })
    await waitFor(() => expect(within(dialog).getByRole('radio', { name: /智能选择/ })).toBeTruthy())
    fireEvent.click(within(dialog).getByRole('radio', { name: /智能选择/ }))
    fireEvent.click(within(dialog).getByRole('button', { name: /^数据边界/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: '仅限本地' }))

    expect((within(dialog).getByRole('checkbox', { name: /Mem0/ }) as HTMLInputElement).disabled).toBe(true)
    expect((within(dialog).getByRole('checkbox', { name: /Holographic/ }) as HTMLInputElement).disabled).toBe(false)
    expect((within(dialog).getByRole('checkbox', { name: /ByteRover/ }) as HTMLInputElement).disabled).toBe(false)
  })

  it('shows the persisted provider decision on the Memory Space card', async () => {
    const { connection } = createConnection({ withPlacement: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    await waitFor(() => expect(screen.getByText('Agent 智能选择')).toBeTruthy())
    expect(screen.getByText('置信度：高')).toBeTruthy()
    expect(screen.getByText('精确写入与关系图谱比跨项目共享更重要。')).toBeTruthy()
  })

  it('shows the inspected workspace, warns on divergence, and offers one-click alignment', async () => {
    const { connection } = createConnection({ workspaceMismatch: true })
    const onSelect = vi.fn()
    const onAlign = vi.fn()
    const workspaceSettingsSnapshot = { ...settingsSnapshot, value: { storageScope: 'workspace' as const } }
    const workspaceSettingsScope = { ...settingsScope, getSnapshot: () => workspaceSettingsSnapshot }
    render(<MnemonWorkbench
      connection={connection}
      settingsScope={workspaceSettingsScope}
      sessionId="session-1"
      workspaceId="workspace-2"
      workspaceSelection={{
        options: [
          { id: 'workspace-1', title: 'Workspace One', path: '/tmp/workspace-one' },
          { id: 'workspace-2', title: 'Workspace Two', path: '/tmp/workspace-two' },
        ],
        selectedWorkspaceId: 'workspace-2',
        effectiveWorkspaceId: 'workspace-1',
        onSelect,
        onAlign,
      }}
    />)

    expect(screen.getByLabelText('存储范围：工作区')).toBeTruthy()
    expect(screen.getByRole('button', { name: '选择要查看的记忆工作区 Workspace Two' })).toBeTruthy()
    const header = (await screen.findByRole('heading', { name: '记忆系统', level: 1 })).closest('header')
    if (header === null) throw new Error('Sidebar header missing')
    expect(within(header).getByLabelText('存储范围：工作区')).toBeTruthy()
    const alignment = within(header).getByRole('status', { name: /查看目录与当前会话未对齐/ })
    expect(alignment.getAttribute('aria-label')).toContain('查看：/tmp/workspace-two/.mnemon')
    expect(alignment.getAttribute('aria-label')).toContain('生效：/tmp/workspace-one/.mnemon')
    fireEvent.click(screen.getByRole('button', { name: '选择要查看的记忆工作区 Workspace Two' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Workspace One' }))
    expect(onSelect).toHaveBeenCalledWith('workspace-1')
    fireEvent.click(within(header).getByRole('button', { name: '对齐对话' }))
    expect(onAlign).toHaveBeenCalledTimes(1)
  })

  it.each(['sidebar', 'builtin'] as const)('clears %s Source data and editors before the new scope finishes loading', async surface => {
    const { call } = createConnection({ runtimeCount: 3 })
    const delayedCall = vi.fn(async (channel: string, endpoint: string, payload?: Record<string, unknown>) => {
      if ((payload?.workspaceId === 'workspace-2' || payload?.sessionId === 'session-2') && (endpoint === 'status-summary' || endpoint === 'runtime-memory')) return await new Promise<never>(() => {})
      return call(channel, endpoint, payload)
    })
    const delayedConnection = { rpc: { call: delayedCall }, isLoopback: true } as unknown as ClientConnectionHandle
    const view = (workspaceId: string) => <MnemonWorkbench connection={delayedConnection} settingsScope={settingsScope} surface={surface} {...(surface === 'sidebar' ? { sessionId: 'session-1', workspaceId } : { sessionId: workspaceId === 'workspace-1' ? 'session-1' : 'session-2' })} />
    const { rerender } = render(view('workspace-1'))

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    fireEvent.click(await screen.findByRole('tab', { name: '运行时记忆' }))
    const oldRuntime = await screen.findByRole('region', { name: '运行时记忆列表' })
    await waitFor(() => expect(within(oldRuntime).getByText('运行时条目 1')).toBeTruthy())
    fireEvent.change(within(oldRuntime).getByRole('textbox', { name: '筛选运行时记忆' }), { target: { value: '条目 1' } })
    const oldCanvas = screen.getByTestId('mnemon-canvas')
    oldCanvas.scrollTop = 240

    fireEvent.click(screen.getByRole('button', { name: '添加记忆' }))
    expect(screen.getByRole('dialog', { name: '添加运行时记忆' })).toBeTruthy()

    rerender(view('workspace-2'))

    const newCanvas = screen.getByTestId('mnemon-canvas')
    expect(newCanvas).not.toBe(oldCanvas)
    expect(newCanvas.scrollTop).toBe(0)
    expect(screen.queryByText('运行时条目 1')).toBeNull()
    expect((await screen.findByRole('textbox', { name: '筛选运行时记忆' }) as HTMLInputElement).value).toBe('')
    expect(screen.queryByRole('dialog', { name: '添加运行时记忆' })).toBeNull()
    expect(screen.queryByLabelText('存储范围：自定义') !== null).toBe(surface === 'sidebar')
    await waitFor(() => expect(delayedCall).toHaveBeenCalledWith(expect.anything(), 'status-summary', expect.objectContaining(surface === 'sidebar' ? { workspaceId: 'workspace-2' } : { sessionId: 'session-2' })))
  })

  it.each(['sidebar', 'builtin'] as const)('reloads %s Source data when a saved storage setting is published', async surface => {
    const base = createConnection({ runtimeCount: 1 })
    let settingsGeneration = 0
    let releaseNextGeneration!: () => void
    const nextGenerationReady = new Promise<void>(resolve => { releaseNextGeneration = resolve })
    const call = vi.fn(async (channel: string, endpoint: string, payload?: Record<string, unknown>) => {
      const response = await base.call(channel, endpoint, payload)
      if (settingsGeneration === 0 || (endpoint !== 'status-summary' && endpoint !== 'runtime-memory')) return response
      await nextGenerationReady
      if (!response.ok) return response
      if (endpoint === 'status-summary') {
        const value = response.value as Record<string, unknown>
        const storage = value.storage as Record<string, unknown>
        return { ...response, value: { ...value, dataDir: '/Users/test/.mnemon', storage: { ...storage, activeKind: 'global', activeRoot: '/Users/test/.mnemon' }, workspaceContext: { mode: 'global', selectedRoot: '/Users/test/.mnemon', effectiveRoot: '/Users/test/.mnemon', aligned: true } } }
      }
      const value = response.value as { targets: Record<string, Record<string, unknown>> }
      const content = '全局目录实时加载的运行时记忆。'
      return {
        ...response,
        value: {
          ...value,
          entries: [{ content, created_at: '2026-08-15T01:00:00.000Z', updated_at: '2026-08-15T01:00:00.000Z', target: 'memory', importance: 'normal' }],
          targets: {
            ...value.targets,
            user: { ...value.targets.user, entryCount: 0, used: 0 },
            memory: { ...value.targets.memory, entryCount: 1, used: content.length },
          },
        },
      }
    })
    const connection = { rpc: { call }, isLoopback: true } as unknown as ClientConnectionHandle
    let snapshot: ClientSettingsSnapshot<Config> = { status: 'ready', value: { storageScope: 'custom' }, base: {}, user: {}, revision: 0, writable: true, mode: 'host' }
    const listeners = new Set<() => void>()
    const liveSettingsScope = {
      getSnapshot: () => snapshot,
      subscribe: (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener) },
      mutate: async () => {},
    } satisfies ClientSettingsScope<Config>
    render(<MnemonWorkbench connection={connection} settingsScope={liveSettingsScope} sessionId="session-1" surface={surface} />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    fireEvent.click(await screen.findByRole('tab', { name: '运行时记忆' }))
    await waitFor(() => expect(screen.getByText('运行时条目 1')).toBeTruthy())
    const oldCanvas = screen.getByTestId('mnemon-canvas')
    oldCanvas.scrollTop = 240
    fireEvent.change(screen.getByRole('textbox', { name: '筛选运行时记忆' }), { target: { value: '运行时条目' } })

    settingsGeneration = 1
    snapshot = { ...snapshot, value: { storageScope: 'global' }, revision: 1 }
    act(() => { for (const listener of listeners) listener() })

    await waitFor(() => expect(screen.getByTestId('mnemon-canvas')).not.toBe(oldCanvas))
    expect(screen.getByTestId('mnemon-canvas').scrollTop).toBe(0)
    expect(screen.queryByLabelText('存储范围：全局') !== null).toBe(surface === 'sidebar')
    expect(screen.queryByText('运行时条目 1')).toBeNull()
    expect((screen.getByRole('textbox', { name: '筛选运行时记忆' }) as HTMLInputElement).value).toBe('')

    releaseNextGeneration()
    await waitFor(() => expect(screen.getByText('全局目录实时加载的运行时记忆。')).toBeTruthy())
    expect(call.mock.calls.filter(([, endpoint]) => endpoint === 'status-summary')).toHaveLength(2)
    expect(call.mock.calls.filter(([, endpoint]) => endpoint === 'runtime-memory')).toHaveLength(2)
  })

  it('clamps long node content in the graph inspector and opens a full-text preview', async () => {
    const { connection } = createConnection({ longContent: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    await selectWorkspaceTab('记忆空间')
    await waitFor(() => expect(screen.getByRole('img', { name: /Mnemon 实时记忆图谱/ })).toBeTruthy())

    fireEvent.click(await screen.findByRole('button', { name: /^决策: 这是一段非常长的记忆内容/ }))
    const eye = await screen.findByRole('button', { name: '查看全文' })
    eye.focus()
    fireEvent.click(eye)

    const dialog = screen.getByRole('dialog', { name: '内容全文' })
    expect(dialog.textContent).toContain('全文预览窗口的打开与关闭')
    expect(dialog.getAttribute('aria-labelledby')).toBeTruthy()
    expect(dialog.getAttribute('aria-describedby')).toBeTruthy()
    const close = within(dialog).getByRole('button', { name: '关闭' })
    expect(document.activeElement).toBe(close)
    fireEvent.click(close)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(document.activeElement).toBe(eye)
  })

  it.each([1280, 700])('shares Status page insets and scroll ownership across Source pages at %ipx', async width => {
    const disposeStyles = installClientFrameStyles(width)
    try {
      const { connection } = createConnection()
      render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
      await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
      const canvas = screen.getByTestId('mnemon-canvas')
      const ancestors = (element: HTMLElement): HTMLElement[] => {
        const result: HTMLElement[] = []
        for (let current: HTMLElement | null = element; current !== null && current !== canvas; current = current.parentElement) result.push(current)
        return result
      }
      const nonzero = (value: string) => value !== '' && value !== '0px' && value !== '0' && value !== 'auto'
      const frame = () => {
        const page = Array.from(canvas.getElementsByClassName(memoryPageStyles.page!)).at(-1) as HTMLElement
        expect(page).toBeTruthy()
        const styles = ancestors(page).map(element => getComputedStyle(element))
        return {
          top: styles.map(style => style.paddingTop).filter(nonzero),
          left: styles.map(style => style.paddingLeft).filter(nonzero),
          right: styles.map(style => style.paddingRight).filter(nonzero),
          bottom: styles.map(style => style.paddingBottom).filter(nonzero),
          minimumHeights: styles.map(style => style.minHeight).filter(nonzero),
          innerScrollports: styles.filter(style => /auto|scroll/u.test(style.overflow + style.overflowY)).length,
        }
      }
      const statusFrame = frame()
      expect(statusFrame.top).toEqual([width <= 760 ? '16px' : '20px'])
      expect(statusFrame.left).toEqual([width <= 760 ? '16px' : '24px'])
      expect(statusFrame.minimumHeights).toEqual(['100%'])
      expect(statusFrame.innerScrollports).toBe(0)
      expect(getComputedStyle(canvas).overflow).toBe('auto')
      const checkHeader = (heading: HTMLElement) => {
        const styles = ancestors(heading).map(element => getComputedStyle(element))
        const sticky = styles.filter(style => style.position === 'sticky')
        expect(sticky).toHaveLength(1)
        expect(sticky[0]!.top).toBe('0px')
        const offset = (padding: 'paddingTop' | 'paddingLeft', margin: 'marginTop' | 'marginLeft') => styles.reduce((sum, style) => sum + (Number.parseFloat(style[padding]) || 0) + (Number.parseFloat(style[margin]) || 0), 0)
        expect(offset('paddingTop', 'marginTop')).toBe(width <= 760 ? 16 : 20)
        expect(offset('paddingLeft', 'marginLeft')).toBe(width <= 760 ? 16 : 24)
      }
      checkHeader(within(canvas).getAllByRole('heading', { level: 2 })[0]!)
      for (const tab of ['运行时记忆', '项目档案', '记忆空间']) {
        await selectWorkspaceTab(tab)
        await waitFor(() => expect(within(canvas).getAllByRole('heading', { level: 2 }).length).toBeGreaterThan(0))
        expect(frame()).toEqual(statusFrame)
        checkHeader(within(canvas).getAllByRole('heading', { level: 2 })[0]!)
      }
      const memoryNavigation = screen.getByRole('tablist', { name: '记忆空间页面' })
      expect(ancestors(memoryNavigation).filter(element => getComputedStyle(element).position === 'sticky')).toHaveLength(1)
      expect(getComputedStyle(memoryNavigation.parentElement!).paddingInline).toBe('')
      for (const tab of ['检索', '内容', '实体', '概览']) {
        fireEvent.click(within(memoryNavigation).getByRole('tab', { name: tab }))
        expect(frame()).toEqual(statusFrame)
        const headings = within(canvas).getAllByRole('heading', { level: 2 })
        checkHeader(headings[0]!)
        expect(ancestors(headings[1]!).filter(element => getComputedStyle(element).position === 'sticky')).toHaveLength(0)
        expect(Array.from(canvas.querySelectorAll<HTMLElement>('*')).filter(element => getComputedStyle(element).position === 'sticky')).toHaveLength(1)
      }
    } finally { disposeStyles() }
  })

  it('resets the shared canvas scroll position when switching pages', async () => {
    const { connection } = createConnection()
    render(<div data-testid="dsh-host-scrollport"><div data-testid="peer-plugin-scrollport" /><MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" /></div>)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    const canvas = screen.getByTestId('mnemon-canvas')
    const hostScrollport = screen.getByTestId('dsh-host-scrollport')
    const peerScrollport = screen.getByTestId('peer-plugin-scrollport')
    hostScrollport.scrollTop = 240
    peerScrollport.scrollTop = 180
    canvas.scrollTop = 900
    await selectWorkspaceTab('运行时记忆')
    expect(canvas.scrollTop).toBe(0)
    expect(hostScrollport.scrollTop).toBe(240)
    canvas.scrollTop = 900
    await selectWorkspaceTab('记忆空间')
    expect(canvas.scrollTop).toBe(0)
    expect(hostScrollport.scrollTop).toBe(240)
    const memoryNavigation = screen.getByRole('tablist', { name: '记忆空间页面' })
    for (const tab of ['检索', '内容', '实体', '概览']) {
      canvas.scrollTop = 900
      fireEvent.click(within(memoryNavigation).getByRole('tab', { name: tab }))
      expect(canvas.scrollTop).toBe(0)
      expect(hostScrollport.scrollTop).toBe(240)
      expect(peerScrollport.scrollTop).toBe(180)
    }
  })

  it('progressively renders long content lists instead of mounting every card', async () => {
    const { connection } = createConnection({ listCount: 60 })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())

    await selectWorkspaceTab('内容')
    await waitFor(() => expect(screen.getByText('当前显示 12 / 60')).toBeTruthy())
    expect(screen.getByText('记忆条目 12')).toBeTruthy()
    expect(screen.queryByText('记忆条目 13')).toBeNull()

    fireEvent.click(await screen.findByRole('button', { name: '再显示 12 条' }))
    expect(screen.getByText('当前显示 24 / 60')).toBeTruthy()
    expect(screen.getByText('记忆条目 24')).toBeTruthy()
    expect(screen.queryByText('记忆条目 25')).toBeNull()
  })

  it('progressively reveals large Sidebar collections and resets scoped readers', async () => {
    const { connection } = createConnection({ runtimeCount: 23, searchCount: 15, entityCount: 25, entityInsightCount: 15, listCount: 25, documentCount: 17 })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())

    fireEvent.click(await screen.findByRole('tab', { name: '运行时记忆' }))
    const runtimeList = await screen.findByRole('region', { name: '运行时记忆列表' })
    await waitFor(() => expect(within(runtimeList).getByText('当前显示 10 / 23')).toBeTruthy())
    expect(within(runtimeList).queryByText('运行时条目 11')).toBeNull()
    fireEvent.click(within(runtimeList).getByRole('button', { name: '再显示 10 条' }))
    expect(within(runtimeList).getByText('运行时条目 20')).toBeTruthy()
    fireEvent.change(within(runtimeList).getByRole('textbox', { name: '筛选运行时记忆' }), { target: { value: '运行时条目 21' } })
    expect(within(runtimeList).getByText('当前显示 1 / 1')).toBeTruthy()
    expect(within(runtimeList).getByText('运行时条目 21')).toBeTruthy()

    fireEvent.click(await screen.findByRole('tab', { name: '记忆空间' }))
    fireEvent.click(within(screen.getByRole('tablist', { name: '记忆空间页面' })).getByRole('tab', { name: '检索' }))
    fireEvent.change(screen.getByRole('textbox', { name: '记忆查询' }), { target: { value: 'SQLite' } })
    fireEvent.click(await screen.findByRole('button', { name: '直接检索' }))
    await waitFor(() => expect(screen.getByText('当前显示 6 / 15')).toBeTruthy())
    expect(screen.queryByText('检索记忆 7')).toBeNull()
    fireEvent.click(await screen.findByRole('button', { name: '再显示 6 条' }))
    expect(screen.getByText('检索记忆 12')).toBeTruthy()

    fireEvent.click(within(screen.getByRole('tablist', { name: '记忆空间页面' })).getByRole('tab', { name: '实体' }))
    await waitFor(() => expect(screen.getByText('当前显示 10 / 25')).toBeTruthy())
    expect(screen.queryByRole('button', { name: /实体 11/ })).toBeNull()
    fireEvent.click(await screen.findByRole('button', { name: '再显示 10 条' }))
    fireEvent.click(await screen.findByRole('button', { name: /实体 11/ }))
    await waitFor(() => expect(screen.getByText('当前显示 6 / 15')).toBeTruthy())
    expect(screen.queryByText('实体 11 关联记忆 7')).toBeNull()

    // entity rail live-filters the top-entity list as the user types into the name input
    const entityInput = screen.getByRole('textbox', { name: '实体名称' })
    fireEvent.change(entityInput, { target: { value: '实体 2' } })
    await waitFor(() => expect(screen.queryByRole('button', { name: /实体 11/ })).toBeNull())
    expect(screen.queryByRole('button', { name: /实体 3/ })).toBeNull()
    expect(screen.getAllByRole('button', { name: /实体 2/ }).length).toBeGreaterThan(0)
    expect(screen.queryByText('本地无匹配。点“查阅”向宿主发起查询。')).toBeNull()
    fireEvent.change(entityInput, { target: { value: '实体 999' } })
    expect(screen.getByText('本地无匹配。点“查阅”向宿主发起查询。')).toBeTruthy()
    fireEvent.keyDown(entityInput, { key: 'Escape' })
    expect(screen.queryByText('本地无匹配。点“查阅”向宿主发起查询。')).toBeNull()
    expect(screen.getAllByRole('button', { name: /实体 1/ }).length).toBeGreaterThan(0)
    // clear input so the rest of the test runs against the unfiltered rail
    fireEvent.change(entityInput, { target: { value: '' } })

    fireEvent.click(within(screen.getByRole('tablist', { name: '记忆空间页面' })).getByRole('tab', { name: '内容' }))
    await waitFor(() => expect(screen.getByText('当前显示 12 / 25')).toBeTruthy())
    expect(screen.queryByText('记忆条目 13')).toBeNull()
    fireEvent.click(await screen.findByRole('button', { name: '再显示 12 条' }))
    expect(screen.getByText('记忆条目 24')).toBeTruthy()

    fireEvent.click(await screen.findByRole('tab', { name: '项目档案' }))
    const documentList = await screen.findByRole('complementary', { name: '项目档案列表' })
    await waitFor(() => expect(within(documentList).getByText('当前显示 8 / 17')).toBeTruthy())
    expect(within(documentList).queryByText('档案条目 9')).toBeNull()
    fireEvent.click(within(documentList).getByRole('button', { name: '再显示 8 条' }))
    expect(within(documentList).getByText('档案条目 16')).toBeTruthy()
    const documentReader = screen.getByRole('region', { name: '档案阅读器' })
    expect(documentReader.hasAttribute('data-scroll-region')).toBe(true)
    documentReader.scrollTop = 240
    fireEvent.click(within(documentList).getByRole('button', { name: /档案条目 2/ }))
    expect(documentReader.scrollTop).toBe(0)
  }, 15_000)

  it('requires dialog confirmation before forgetting a recalled memory', async () => {
    const { connection, call } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())

    await selectWorkspaceTab('检索')
    fireEvent.change(screen.getByRole('textbox', { name: '记忆查询' }), { target: { value: 'SQLite' } })
    fireEvent.click(await screen.findByRole('button', { name: '直接检索' }))
    await waitFor(() => expect(screen.getByText('项目选择 SQLite，因为需要单文件部署。')).toBeTruthy())

    fireEvent.click(await screen.findByRole('button', { name: '忘记' }))
    expect(screen.getByRole('dialog', { name: '软删除这条记忆？' })).toBeTruthy()
    expect(call).not.toHaveBeenCalledWith(expect.anything(), 'forget', expect.anything())

    fireEvent.click(await screen.findByRole('button', { name: '确认忘记' }))
    await waitFor(() => expect(screen.queryByText('项目选择 SQLite，因为需要单文件部署。')).toBeNull())
    expect(call).toHaveBeenCalledWith(expect.anything(), 'forget', { id: 'memory-12345678', memoryBodyId: 'project', sessionId: 'session-1' })
  })

  it('keeps the newest related-memory response when requests finish out of order', async () => {
    const { connection, resolveRelated } = createConnection({ relatedDeferred: true })
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" />)
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())

    await selectWorkspaceTab('检索')
    fireEvent.change(screen.getByRole('textbox', { name: '记忆查询' }), { target: { value: 'SQLite' } })
    fireEvent.click(await screen.findByRole('button', { name: '直接检索' }))
    await waitFor(() => expect(screen.getByText('项目选择 SQLite，因为需要单文件部署。')).toBeTruthy())

    fireEvent.click(await screen.findByRole('button', { name: '查看关联' }))
    fireEvent.click(await screen.findByRole('button', { name: '查看关联' }))
    await act(async () => { resolveRelated(1, '较新的关联响应') })
    expect(await screen.findByText('较新的关联响应')).toBeTruthy()
    await act(async () => { resolveRelated(0, '已经过期的关联响应') })
    expect(screen.getByText('较新的关联响应')).toBeTruthy()
    expect(screen.queryByText('已经过期的关联响应')).toBeNull()
  })

  function queueAnimationFrames() {
    let next = 0
    const pending = new Map<number, FrameRequestCallback>()
    const request = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => { pending.set(++next, callback); return next })
    const cancel = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(frame => { pending.delete(frame) })
    return {
      pending,
      flush: () => { const frames = [...pending.values()]; pending.clear(); for (const frame of frames) frame(0) },
      restore: () => { request.mockRestore(); cancel.mockRestore() },
    }
  }

  it.each([120, 210])('reveals Related below a %ipx Source header without scrolling DSH or peer plugins', async headerHeight => {
    const { connection } = createConnection({ relatedDeferred: true })
    const frames = queueAnimationFrames()
    try {
      render(<div data-testid="dsh-host-scrollport"><div data-testid="peer-plugin-scrollport" /><MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" /></div>)
      await selectWorkspaceTab('检索')
      fireEvent.change(screen.getByRole('textbox', { name: '记忆查询' }), { target: { value: 'SQLite' } })
      fireEvent.click(screen.getByRole('button', { name: '直接检索' }))
      await screen.findByText('项目选择 SQLite，因为需要单文件部署。')
      const canvas = screen.getByTestId('mnemon-canvas')
      const host = screen.getByTestId('dsh-host-scrollport'), peer = screen.getByTestId('peer-plugin-scrollport')
      host.scrollTop = 240; peer.scrollTop = 180; canvas.scrollTop = 473.5
      const header = screen.getByRole('heading', { name: '记忆空间' }).closest('section')!
      vi.spyOn(canvas, 'getBoundingClientRect').mockImplementation(() => new DOMRect(280, 87, 1000, 633))
      vi.spyOn(header, 'getBoundingClientRect').mockImplementation(() => new DOMRect(280, 87, 1000, headerHeight))
      fireEvent.click(screen.getByRole('button', { name: '查看关联' }))
      const close = screen.getByRole('button', { name: '关闭关联记忆' }), pane = close.closest('aside')!
      const contentTop = canvas.scrollTop + 108
      vi.spyOn(pane, 'getBoundingClientRect').mockImplementation(() => new DOMRect(800, contentTop - canvas.scrollTop, 360, 400))
      vi.spyOn(close, 'getBoundingClientRect').mockImplementation(() => new DOMRect(1130, contentTop - canvas.scrollTop + 13.625, 26, 26))
      expect(close.getBoundingClientRect().bottom).toBeLessThan(header.getBoundingClientRect().bottom)
      act(() => frames.flush())
      expect(pane.getBoundingClientRect().top).toBeGreaterThanOrEqual(header.getBoundingClientRect().bottom + 14)
      expect(close.getBoundingClientRect().top).toBeGreaterThan(header.getBoundingClientRect().bottom)
      expect(host.scrollTop).toBe(240)
      expect(peer.scrollTop).toBe(180)
    } finally { cleanup(); frames.restore() }
  })

  it.each(['close', 'unmount'] as const)('cancels a pending Related reveal on %s without disturbing another Source', async action => {
    const first = createConnection({ relatedDeferred: true }), second = createConnection({ relatedDeferred: true })
    const frames = queueAnimationFrames()
    try {
      const owner = render(<MnemonWorkbench connection={first.connection} settingsScope={settingsScope} sessionId="first" />)
      const peer = render(<MnemonWorkbench connection={second.connection} settingsScope={settingsScope} sessionId="second" />)
      const openRelated = async (container: HTMLElement) => {
        const view = within(container)
        fireEvent.click(await view.findByRole('tab', { name: '记忆空间' }))
        fireEvent.click(await view.findByRole('tab', { name: '检索' }))
        fireEvent.change(view.getByRole('textbox', { name: '记忆查询' }), { target: { value: 'SQLite' } })
        fireEvent.click(view.getByRole('button', { name: '直接检索' }))
        await view.findByText('项目选择 SQLite，因为需要单文件部署。')
        const canvas = view.getByTestId('mnemon-canvas')
        canvas.scrollTop = 600
        vi.spyOn(view.getByRole('heading', { name: '记忆空间' }).closest('section')!, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 0, 500, 120))
        fireEvent.click(view.getByRole('button', { name: '查看关联' }))
        vi.spyOn(view.getByRole('button', { name: '关闭关联记忆' }).closest('aside')!, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 30, 300, 400))
        return canvas
      }
      const ownCanvas = await openRelated(owner.container), peerCanvas = await openRelated(peer.container)
      expect(frames.pending.size).toBe(2)
      if (action === 'close') fireEvent.click(within(owner.container).getByRole('button', { name: '关闭关联记忆' }))
      else owner.unmount()
      expect(frames.pending.size).toBe(1)
      act(() => frames.flush())
      expect(ownCanvas.scrollTop).toBe(600)
      expect(peerCanvas.scrollTop).toBe(496)
      expect(within(peer.container).getByRole('button', { name: '关闭关联记忆' })).toBeTruthy()
    } finally { cleanup(); frames.restore() }
  })

  it.each(['sidebar', 'builtin'] as const)('shows an Agent answer using the %s task context', async surface => {
    const { connection, call } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} />)
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())

    await selectWorkspaceTab('检索')
    fireEvent.change(screen.getByRole('textbox', { name: '记忆查询' }), { target: { value: 'SQLite' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Agent 查询' }))

    await waitFor(() => expect(screen.getByRole('region', { name: 'Agent 查询结果' })).toBeTruthy())
    expect(screen.getByText('项目选择 SQLite，以满足单文件部署。')).toBeTruthy()
    // A citation names the recalled memory it points at; its identifier stays on hover.
    expect(screen.getByTitle('project/memory-12345678').textContent).toBe('1. 项目选择 SQLite，因为需要单文件部署。')
    expect(screen.getByText('原始召回内容')).toBeTruthy()
    expect(screen.getByText('项目选择 SQLite，因为需要单文件部署。')).toBeTruthy()
    expect(call).toHaveBeenCalledWith(expect.anything(), 'agent-search', expect.objectContaining({ query: 'SQLite' }))
    const agentSearchPayload = call.mock.calls.find((entry: unknown[]) => entry[1] === 'agent-search')?.[2]
    if (surface === 'builtin') expect(agentSearchPayload).toHaveProperty('sessionId', 'session-1')
    else expect(agentSearchPayload).not.toHaveProperty('sessionId')
  })

  it.each(['sidebar', 'builtin'] as const)('creates and cold-archives a managed Document through the shared %s UI', async surface => {
    const { connection, call } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} />)
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    await selectWorkspaceTab('项目档案')
    await waitFor(() => expect(screen.getByText('发布验证清单')).toBeTruthy())

    fireEvent.click(await screen.findByRole('button', { name: '新建档案' }))
    fireEvent.change(screen.getByRole('textbox', { name: '标题' }), { target: { value: '架构交接说明' } })
    fireEvent.change(screen.getByRole('textbox', { name: '检索说明' }), { target: { value: '解释存储控制层与并发边界。' } })
    fireEvent.change(screen.getByRole('textbox', { name: '来源路径' }), { target: { value: 'src/documents.ts' } })
    fireEvent.change(screen.getByRole('textbox', { name: 'Markdown 内容' }), { target: { value: '# 控制层\n\nindex.json 是事实源。' } })
    fireEvent.click(await screen.findByRole('button', { name: '创建档案' }))

    await waitFor(() => expect(screen.getAllByText('架构交接说明').length).toBeGreaterThan(0))
    expect(call).toHaveBeenCalledWith(expect.anything(), 'document', expect.objectContaining({ action: 'create', title: '架构交接说明', content: '# 控制层\n\nindex.json 是事实源。', sessionId: 'session-1' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '归档' })).toBeTruthy())
    fireEvent.click(await screen.findByRole('button', { name: '归档' }))
    fireEvent.click(await screen.findByRole('button', { name: '确认归档' }))
    await waitFor(() => expect(screen.getByText(/已建立 Mnemon 冷索引并归档/)).toBeTruthy())
    expect(call).toHaveBeenCalledWith(expect.anything(), 'document', { action: 'archive', id: 'document-new-1234', sessionId: 'session-1' })
    expect(screen.getByText('Mnemon 冷索引回执')).toBeTruthy()
    expect(screen.getByText('已写入发布记忆空间索引。')).toBeTruthy()
  })

  it.each(['sidebar', 'builtin'] as const)('dispatches distillation with the %s task context', async surface => {
    const { connection, call } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" surface={surface} />)
    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())

    await openRememberDialog()
    fireEvent.change(screen.getByRole('textbox', { name: '候选内容' }), { target: { value: '项目发布前必须通过真实 WebUI 验证。' } })
    fireEvent.click(await screen.findByRole('button', { name: '交给任务 Agent' }))

    await waitFor(() => expect(call).toHaveBeenCalledWith(expect.anything(), 'supervise', { content: '项目发布前必须通过真实 WebUI 验证。', ...(surface === 'builtin' ? { sessionId: 'session-1' } : {}) }))
    // The dialog stays on the task Agent's receipt, which leads to what was written.
    await waitFor(() => expect(screen.getByText('已提炼并写入项目交付约束。')).toBeTruthy())
    expect(screen.getByText('已存入')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '在记忆空间中查看' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '存入记忆' })).toBeNull())
    expect(screen.getByRole('tab', { name: '内容' }).getAttribute('aria-selected')).toBe('true')
    expect(call).not.toHaveBeenCalledWith(expect.anything(), 'remember', expect.anything())
  })

  it('renders a usable empty overview when no memory spaces exist', async () => {
    const call = vi.fn(async (_channel: string, endpoint: string) => {
      if (endpoint === 'status' || endpoint === 'status-summary') return {
        ok: true,
        value: {
          healthy: true,
          version: '0.2.0',
          cliPath: '/usr/local/bin/mnemon',
          commandFound: true,
          dataDir: '/tmp/mnemon',
          store: 'none',
          writeEnabled: true,
          timeoutMs: 10000,
          defaultRecallLimit: 10,
          memoryBodyDirectory: '/tmp/mnemon/data',
          memoryBodies: [],
          stats: { totalInsights: 0, deletedInsights: 0, edgeCount: 0, oplogCount: 0, dbSizeBytes: 0, byCategory: {}, topEntities: [] },
        },
      }
      if (endpoint === 'bodies') return { ok: false, error: { code: 'provider-unavailable', message: 'memory-body catalog unavailable' } }
      if (endpoint === 'graph') return { ok: true, value: { nodes: [], edges: [], generatedAt: '2026-08-13T03:00:00.000Z' } }
      return { ok: false, error: { code: 'unexpected', message: endpoint } }
    })
    render(<MnemonWorkbench connection={{ rpc: { call }, isLoopback: true } as unknown as ClientConnectionHandle} settingsScope={settingsScope} sessionId="session-1" />)

    await selectWorkspaceTab('记忆空间')
    await waitFor(() => expect(screen.getAllByRole('heading', { name: '还没有记忆空间' })).toHaveLength(1))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByText('0 / 0 已激活')).toBeTruthy()
    expect(screen.getByRole('button', { name: '创建记忆空间' })).toBeTruthy()
  })

  it('marks an unavailable Source directory as unsynchronized instead of reporting zero bodies', async () => {
    const call = vi.fn(async (_channel: string, endpoint: string) => {
      if (endpoint === 'status' || endpoint === 'status-summary') return {
        ok: true,
        value: {
          healthy: true,
          version: '0.2.0',
          cliPath: '/usr/local/bin/mnemon',
          commandFound: true,
          dataDir: '/tmp/mnemon',
          store: 'legacy',
          writeEnabled: true,
          timeoutMs: 10000,
          defaultRecallLimit: 10,
          stats: { totalInsights: 2, deletedInsights: 0, edgeCount: 6, oplogCount: 4, dbSizeBytes: 4096, byCategory: {}, topEntities: [] },
        },
      }
      if (endpoint === 'bodies') return { ok: false, error: { code: 'provider-unavailable', message: 'unknown endpoint' } }
      if (endpoint === 'graph') return { ok: true, value: { nodes: [], edges: [], generatedAt: '2026-08-13T03:00:00.000Z' } }
      return { ok: false, error: { code: 'unexpected', message: endpoint } }
    })
    render(<MnemonWorkbench connection={{ rpc: { call }, isLoopback: true } as unknown as ClientConnectionHandle} settingsScope={settingsScope} sessionId="session-1" />)

    await waitFor(() => expect(screen.getByText('已连接')).toBeTruthy())
    await selectWorkspaceTab('记忆空间')
    await waitFor(() => expect(screen.getAllByText('记忆空间目录尚未同步').length).toBeGreaterThan(0))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByText('0 / 0')).toBeNull()
    expect(screen.queryByRole('switch')).toBeNull()
  })

  it('renders all product copy in English with Memory Space terminology', async () => {
    const { connection } = createConnection()
    render(<MnemonWorkbench connection={connection} settingsScope={settingsScope} sessionId="session-1" t={translateEn} locale="en" />)

    await waitFor(() => expect(screen.getByText('Connected')).toBeTruthy())
    const navigation = await screen.findByRole('tablist', { name: 'Mnemon pages' })
    fireEvent.click(within(navigation).getByRole('tab', { name: 'Memory Spaces' }))
    expect(screen.getByRole('heading', { name: 'Memory Spaces' })).toBeTruthy()
    expect(screen.getByRole('region', { name: 'Memory Space Directory' })).toBeTruthy()
    expect(navigation).toBeTruthy()
    expect(screen.getByRole('tab', { name: 'Recall' })).toBeTruthy()
    expect(within(navigation).getByRole('tab', { name: 'Project Documents' })).toBeTruthy()
    expect(screen.queryByText('PERSISTENT AGENT MEMORY')).toBeNull()
    expect(screen.queryByText(/Memory Bod(y|ies)/i)).toBeNull()

    fireEvent.click(within(navigation).getByRole('tab', { name: 'Status' }))
    expect(screen.getByRole('heading', { name: 'System Status' })).toBeTruthy()
    expect(within(screen.getByRole('region', { name: 'Mnemon runtime status' })).getAllByRole('article')).toHaveLength(4)
    expect(screen.queryByText('Recall worker')).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Subagent Lifecycle' })).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Memory System Flow' })).toBeNull()
    expect(screen.getAllByText('Memory Spaces').length).toBeGreaterThan(0)
  })
})
