#!/usr/bin/env node

import { existsSync, mkdirSync } from 'node:fs'
import { homedir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as spaces from 'dsh-mnemon-source-memory-spaces'
import * as strategy from 'dsh-mnemon-strategy-default-three-tier'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const stateRoot = process.env.MNEMON_DATA_DIR === undefined ? join(root, 'provider-lab', '.state') : join(process.env.MNEMON_DATA_DIR, 'provider-lab')
const byteroverDefaultPath = join(homedir(), '.brv-cli', 'bin', 'brv')
const byteroverPath = process.env.BRV_PATH ?? (existsSync(byteroverDefaultPath) ? byteroverDefaultPath : 'brv')

const supermemoryApiKey = process.env.SUPERMEMORY_API_KEY?.trim()

const dataDir = process.env.MNEMON_DATA_DIR ?? join(stateRoot, 'memory')

const providers = [
  {
    id: 'mnemon-native',
    name: 'Provider Lab · Mnemon Native',
    description: '官方原生基准：精确写入、软删除、实体索引与类型化关系图。',
  },
  {
    id: 'openviking',
    name: 'Provider Lab · OpenViking',
    description: 'Docker 私有化的目录型共享记忆：分层内容、语义提取与文件系统式上下文。',
    connection: {
      endpoint: 'http://127.0.0.1:1933',
      targetUri: 'viking://user/memories',
      apiKey: process.env.OPENVIKING_ROOT_API_KEY ?? 'dsh-provider-lab-local-only',
      account: 'dsh-lab',
      user: 'demo-user',
      actorPeerId: 'dsh',
    },
  },
  {
    id: 'honcho',
    name: 'Provider Lab · Honcho',
    description: 'Docker 私有化的 Peer 结论与用户建模记忆，用于跨会话协作心智验证。',
    connection: { endpoint: 'http://127.0.0.1:18000', apiKey: '', workspace: 'dsh-lab', userId: 'demo-user', agentId: 'dsh' },
  },
  {
    id: 'mem0',
    name: 'Provider Lab · Mem0',
    description: 'Docker 私有化的语义事实记忆，以用户与 Agent 作用域隔离测试内容。',
    connection: { endpoint: 'http://127.0.0.1:18888', apiKey: '', mode: 'self-hosted', userId: 'demo-user', agentId: 'dsh', rerank: false },
  },
  {
    id: 'hindsight',
    name: 'Provider Lab · Hindsight',
    description: 'Docker 私有化的时序、实体与知识图记忆，验证真实图谱能力边界。',
    connection: { endpoint: 'http://127.0.0.1:18889', apiKey: '', bankId: 'dsh-lab', budget: 'mid' },
  },
  {
    id: 'holographic',
    name: 'Provider Lab · Holographic',
    description: '本地结构化可信事实记忆，验证实体解析、信任分与组合式召回。',
    connection: { dataPath: join(stateRoot, 'holographic', 'facts.json'), defaultTrust: 0.7, minTrust: 0.3 },
  },
  {
    id: 'retaindb',
    name: 'Provider Lab · RetainDB',
    description: 'Docker 私有化的 RetainDB Local，验证项目、用户与类型化持久事实。',
    connection: { endpoint: 'http://127.0.0.1:18990', apiKey: 'local-lab', project: 'dsh-lab', userId: 'demo-user' },
  },
  {
    id: 'byterover',
    name: 'Provider Lab · ByteRover',
    description: '官方 brv 本地 CLI 的层级知识树，用查询型而非全量列表心智呈现。',
    connection: { cliPath: byteroverPath, workingDirectory: join(stateRoot, 'byterover'), apiKey: '' },
  },
  {
    id: 'supermemory',
    name: 'Provider Lab · Supermemory',
    description: 'Docker 私有化的语义记忆与摄取文档，以独立 containerTag 隔离测试。',
    connection: { endpoint: 'http://127.0.0.1:18787', apiKey: supermemoryApiKey ?? '', containerTag: 'dsh-lab', searchMode: 'hybrid' },
  },
]

const requestedProviders = new Set((process.env.PROVIDER_LAB_ONLY ?? '').split(',').map(value => value.trim()).filter(Boolean))
const selectedProviders = requestedProviders.size === 0 ? providers : providers.filter(provider => requestedProviders.has(provider.id))
if (selectedProviders.length !== requestedProviders.size && requestedProviders.size > 0) {
  const known = new Set(providers.map(provider => provider.id))
  throw new Error(`Unknown Provider Lab id: ${[...requestedProviders].filter(id => !known.has(id)).join(', ')}`)
}
if (selectedProviders.some(provider => provider.id === 'supermemory') && !supermemoryApiKey) {
  throw new Error('SUPERMEMORY_API_KEY is required when seeding Supermemory; copy the sm_... key from `docker compose logs supermemory`.')
}

if (selectedProviders.some(provider => provider.id === 'byterover')) mkdirSync(join(stateRoot, 'byterover'), { recursive: true, mode: 0o700 })

const memories = [
  { category: 'decision', importance: 5, tags: ['architecture', 'three-tier'], entities: ['DSH', 'Mnemon'], content: 'DSH 记忆系统采用三层结构：运行时热记忆、项目档案、可替换的长期记忆 Provider。第三层允许 Mnemon Native、OpenViking、Mem0 等实现并存。' },
  { category: 'preference', importance: 4, tags: ['routing', 'policy'], entities: ['LLM', 'Provider'], content: '自动创建记忆空间时，用户可以用规则与 Prompt 描述数据边界、共享倾向和能力要求，再由 LLM 在允许的 Provider 候选中作出可解释选择。' },
  { category: 'fact', importance: 5, tags: ['contract', 'webui'], entities: ['WebUI', 'Provider'], content: 'WebUI 不把所有 Provider 伪装成相同数据库：概览区分真实关系图与内容投影，内容区分可枚举与仅查询，实体页只展示真实实体索引。' },
  { category: 'context', importance: 4, tags: ['privacy', 'docker'], entities: ['Docker', 'Ollama'], content: 'Provider Lab 的私有化服务全部由 Docker Compose 启动并只绑定 127.0.0.1；模型与向量计算通过本机 Ollama 完成，测试内容不需要离开机器。' },
  { category: 'insight', importance: 4, tags: ['product', 'compatibility'], entities: ['Provider', 'DSH'], content: '多 Provider 的价值在于兼容成熟记忆实现与社区生态，但 dsh-mnemon 仍以既有记忆空间心智为主，并保留 Mnemon Native 的官方优先级。' },
]

async function postJson(url, body) {
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (response.ok || response.status === 409) return response
  const detail = await response.text()
  if (!detail.toLowerCase().includes('already')) throw new Error(`${url} failed (${response.status}): ${detail}`)
  return response
}

async function ensureHonchoScope() {
  await postJson('http://127.0.0.1:18000/v3/workspaces', { id: 'dsh-lab', metadata: { source: 'dsh-mnemon-provider-lab' } })
  for (const id of ['dsh', 'demo-user']) {
    await postJson('http://127.0.0.1:18000/v3/workspaces/dsh-lab/peers', { id, metadata: { source: 'dsh-mnemon-provider-lab' } })
  }
}

async function ensureMem0Config() {
  await postJson('http://127.0.0.1:18888/configure', {
    version: 'v1.1',
    vector_store: {
      provider: 'pgvector',
      config: {
        host: 'mem0-db', port: 5432, dbname: 'postgres', user: 'postgres', password: 'mem0',
        collection_name: 'dsh_memory_768', embedding_model_dims: 768,
      },
    },
    llm: { provider: 'openai', config: { api_key: 'ollama', temperature: 0.2, model: process.env.MEM0_LLM_MODEL ?? 'qwen2.5:3b' } },
    embedder: { provider: 'openai', config: { api_key: 'ollama', model: 'nomic-embed-text', embedding_dims: 768 } },
    history_db_path: '/app/history/history.db',
  })
}

if (selectedProviders.some(provider => provider.id === 'honcho')) {
  try { await ensureHonchoScope() } catch (error) { console.warn(`WARN  Honcho scope: ${error instanceof Error ? error.message : String(error)}`) }
}
if (selectedProviders.some(provider => provider.id === 'mem0')) {
  try { await ensureMem0Config() } catch (error) { console.warn(`WARN  Mem0 config: ${error instanceof Error ? error.message : String(error)}`) }
}

// The lab is a consumer of public plugin artifacts, not private controllers.
const runner = new MemoryCompositionRunner()
const report = []
try {
  await runner.mount(strategy, { instanceId: 'lab-strategy' })
  await runner.mount(spaces, { instanceId: 'lab-spaces', config: {
    dataDir, timeoutMs: 10_000, defaultRecallLimit: 20,
    providers: selectedProviders.map(provider => ({ use: 'dsh-mnemon-provider-' + provider.id, instanceId: provider.id })),
  } })
  const management = await runner.managementClient('source:lab-spaces')
  const read = async (operation, input = null) => (await management.read(operation, input)).value
  const mutate = async (operation, input) => (await management.mutate(operation, input, { confirmed: true })).value
  for (const provider of selectedProviders) {
    let body
    try {
      // Mnemon Native has no service settings: its CLI is its connection.
      let memorySettings = {}
      if (provider.id !== 'mnemon-native') {
        const descriptor = (await read('provider-services')).providers.find(item => item.id === provider.id)
        if (descriptor === undefined) throw new Error('Provider descriptor not available: ' + provider.id)
        const connection = provider.connection ?? {}
        const settings = Object.fromEntries(descriptor.fields.filter(field => field.scope === 'service' && connection[field.key] !== undefined).map(field => [field.key, connection[field.key]]))
        memorySettings = Object.fromEntries(descriptor.fields.filter(field => field.scope === 'memory' && connection[field.key] !== undefined).map(field => [field.key, connection[field.key]]))
        await mutate('provider-service-update', { providerId: provider.id, settings, enabled: true })
      }
      const existing = (await read('body-directory')).items
      body = existing.find(candidate => candidate.provider.id === provider.id && candidate.name === provider.name)
        ?? existing.find(candidate => candidate.provider.id === provider.id)
      if (body === undefined) {
        body = await mutate('body-create', { name: provider.name, description: provider.description, active: true, providerId: provider.id, connection: memorySettings })
      } else {
        body = await mutate('body-update', { memoryBodyId: body.id, name: provider.name, description: provider.description, active: true, connection: memorySettings })
      }
      const current = body.provider.capabilities.browse
        ? (await read('list', { memoryBodyIds: [body.id], limit: 20 })).items
        : (await read('search', { memoryBodyIds: [body.id], query: '三层结构 Provider', limit: 20 })).results
      let stored = 0
      const failures = []
      if (current.length === 0) {
        for (const memory of memories) {
          try {
            await mutate('remember', { ...memory, source: 'user', memoryBodyId: body.id })
            stored += 1
          } catch (error) { failures.push(error instanceof Error ? error.message : String(error)) }
        }
      }
      report.push({ provider: provider.id, bodyId: body.id, currentCount: current.length, stored, failures })
    } catch (error) {
      report.push({ provider: provider.id, bodyId: body?.id, currentCount: 0, stored: 0, failures: [error instanceof Error ? error.message : String(error)] })
    }
  }
} finally { await runner.dispose() }
console.log('Lab memory root: ' + dataDir)

for (const item of report) {
  const state = item.failures.length === 0 ? 'READY' : item.stored > 0 ? 'PART ' : 'FAIL '
  console.log(`${state}  ${item.provider.padEnd(14)} body=${item.bodyId ?? 'not-created'} existing=${item.currentCount} stored=${item.stored}`)
  for (const failure of [...new Set(item.failures)].slice(0, 3)) console.log(`      ${failure}`)
}

if (report.some(item => item.failures.length > 0)) process.exitCode = 1
