// Documentation demo: a fictional project's memory, seeded through the public
// Source contracts, and a scripted model that reads and corrects it with the
// real View tools. It holds no personal memory and needs the Mnemon CLI for
// the native Memory Spaces.
import { realpathSync } from 'node:fs'
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as runtimePlugin from 'dsh-mnemon-source-runtime'
import * as documentsPlugin from 'dsh-mnemon-source-documents'
import * as spacesPlugin from 'dsh-mnemon-source-memory-spaces'
import * as strategyPlugin from 'dsh-mnemon-strategy-default-three-tier'

const CONTENT = {
  'zh-CN': {
    user: [
      '回答先给结论，再列依据，尽量简洁。',
      '代码示例默认使用 TypeScript，包管理用 pnpm。',
      '评审意见用中文；提交信息保持英文 Conventional Commits。',
    ],
    memory: [
      { content: '本周重点：结账页性能，目标 p75 LCP 低于 2.5 秒。', importance: 'critical' },
      { content: 'Lumen 通过 Changesets 发版；合并到 main 后手动触发 publish 工作流。', importance: 'normal' },
      { content: '测试数据库是 docker compose 里的 postgres-test，端口 55432。', importance: 'normal' },
      { content: '周五下午不做生产部署。', importance: 'critical' },
      { content: 'API 错误响应统一为 { code, message, details }。', importance: 'low' },
    ],
    documents: [
      { title: '性能调查：结账页 LCP', description: '结账页首屏的瓶颈与改进计划', content: '# 性能调查：结账页 LCP\n\n当前 p75 LCP 为 3.1 秒，目标 2.5 秒以内。\n\n## 发现\n\n1. 首屏商品图没有声明尺寸，引发布局偏移。\n2. 支付 SDK 同步加载，阻塞首屏约 400 毫秒。\n\n## 下一步\n\n- 为首屏图片声明尺寸并预加载。\n- 支付 SDK 改为用户交互后再加载。\n' },
      { title: '架构决策：事件摄取改用队列', description: '埋点事件经 Kafka 批量写入 ClickHouse', content: '# 架构决策：事件摄取改用队列\n\n## 背景\n\n高峰期直接写入 ClickHouse 造成写放大，9 月 12 日出现指标延迟。\n\n## 决策\n\n埋点事件先进入 Kafka，再由消费者批量写入 ClickHouse：每批 5,000 条或每 2 秒提交一次。\n\n## 影响\n\n- 指标延迟上限约 5 秒。\n- 消费者按 event_id 去重，保证写入幂等。\n' },
      { title: '故障复盘：9 月 12 日指标延迟', description: '营销活动流量暴露的写入瓶颈', content: '# 故障复盘：9 月 12 日指标延迟\n\n**影响**：仪表盘指标延迟约 40 分钟。\n\n**原因**：营销活动带来 6 倍流量，ClickHouse 合并队列积压。\n\n**处理**：临时扩容并对写入限流；长期改为队列摄取，见架构决策。\n\n**后续**：写入延迟超过 2 分钟时告警。\n' },
      { title: '发布检查清单', description: '每次发版前逐项确认', content: '# 发布检查清单\n\n1. 确认 main 上的 CI 全部通过。\n2. 运行 `pnpm changeset version`，检查版本号与更新说明。\n3. 在预发环境走通结账、登录和报表三条主路径。\n4. 手动触发 publish 工作流，并在 #release 频道同步。\n5. 发布后 30 分钟内关注错误率与 LCP 面板。\n' },
      { title: '新人上手指南', description: '本地环境、分支与求助方式', content: '# 新人上手指南\n\n- 克隆仓库后运行 `pnpm install`，再用 `pnpm dev` 启动。\n- 本地数据库：`docker compose up postgres-test`。\n- 分支命名 `feat/…`、`fix/…`；PR 需要一位评审通过。\n- 遇到问题先查项目档案，再到 #lumen-dev 提问。\n' },
      { title: '旧版部署流程', description: '已由 publish 工作流取代', archived: true, content: '# 旧版部署流程\n\n登录跳板机，手动执行部署脚本。已由 publish 工作流取代，仅供追溯。\n' },
    ],
    spaces: [
      { name: 'Lumen 项目', description: '技术决策、系统事实与它们之间的关系', active: true, memories: [
        { category: 'decision', importance: 5, tags: ['architecture'], entities: ['Kafka', 'ClickHouse', '事件摄取'], content: '事件摄取改为 Kafka 队列，由消费者批量写入 ClickHouse。' },
        { category: 'fact', importance: 4, tags: ['data'], entities: ['ClickHouse', 'Postgres'], content: 'ClickHouse 存储埋点事件，Postgres 存储账户与订单。' },
        { category: 'fact', importance: 4, tags: ['performance'], entities: ['结账服务', '支付 SDK'], content: '结账页同步加载支付 SDK，阻塞首屏约 400 毫秒。' },
        { category: 'insight', importance: 4, tags: ['performance'], entities: ['结账页'], content: '首屏图片没有声明尺寸，是结账页布局偏移的主要原因。' },
        { category: 'decision', importance: 4, tags: ['observability'], entities: ['Grafana', 'ClickHouse'], content: '指标写入延迟超过 2 分钟时，由 Grafana 告警。' },
        { category: 'context', importance: 3, tags: ['incident'], entities: ['ClickHouse', '营销活动'], content: '9 月 12 日的营销活动带来 6 倍流量，暴露了 ClickHouse 写入瓶颈。' },
        { category: 'decision', importance: 3, tags: ['architecture'], entities: ['Kafka', 'event_id'], content: '摄取消费者按 event_id 去重，保证写入幂等。' },
        { category: 'fact', importance: 3, tags: ['data'], entities: ['报表服务', 'ClickHouse'], content: '报表服务每 5 分钟从 ClickHouse 预聚合一次。' },
        { category: 'fact', importance: 3, tags: ['infrastructure'], entities: ['Redis'], content: 'Redis 缓存会话与限流计数，TTL 为 15 分钟。' },
        { category: 'fact', importance: 3, tags: ['frontend'], entities: ['Next.js', '结账页'], content: '前端基于 Next.js，结账页使用服务端渲染。' },
      ] },
      { name: '团队约定', description: '流程、评审与协作习惯', active: true, memories: [
        { category: 'preference', importance: 4, tags: ['review'], entities: ['PR 评审'], content: 'PR 至少需要一位评审通过才能合并。' },
        { category: 'preference', importance: 3, tags: ['git'], entities: ['Conventional Commits'], content: '提交信息使用英文 Conventional Commits。' },
        { category: 'fact', importance: 3, tags: ['release'], entities: ['Changesets'], content: '发版使用 Changesets，版本说明中英双语。' },
        { category: 'preference', importance: 3, tags: ['release'], entities: ['生产部署'], content: '周五下午不做生产部署。' },
        { category: 'context', importance: 2, tags: ['meeting'], entities: ['站会'], content: '站会每天 10:00；周四下午做技术分享。' },
      ] },
      { name: '调研归档', description: '已结束调研的结论', active: false, memories: [
        { category: 'insight', importance: 3, tags: ['research'], entities: ['TimescaleDB', 'ClickHouse'], content: '评估过 TimescaleDB，写入吞吐不足以替代 ClickHouse。' },
        { category: 'insight', importance: 2, tags: ['research'], entities: ['Web Vitals'], content: 'Web Vitals 采样 10% 已足以稳定 p75 指标。' },
      ] },
    ],
    title: '结账性能与事件摄取',
    question: /结账/u,
    correction: /记住/u,
    documentQuery: '结账 LCP 事件摄取',
    recallQueries: ['结账页 首屏 性能', '事件摄取 决策'],
    target: '目标 p75 LCP 低于 2.5 秒',
    corrected: '本周重点：结账页性能，目标 p75 LCP 低于 2.2 秒。',
    keys: { images: '尺寸', sdk: '支付 SDK', ingestion: 'Kafka' },
    answer: ({ target, causes, ingestion, documents }) => [
      `**目标**：结账页 p75 LCP 降到 2.5 秒以内${target ? '（工作记忆）' : ''}。`,
      causes.length === 0 ? '' : `\n**已知原因**（记忆空间）：\n${causes.map(cause => `- ${cause}`).join('\n')}`,
      ingestion === undefined ? '' : `\n**事件摄取**：${ingestion}`,
      documents.length === 0 ? '' : `\n详见档案${documents.map(title => `《${title}》`).join('、')}。`,
    ].filter(Boolean).join('\n'),
    saved: '已更新工作记忆：结账页目标改为 p75 LCP 低于 2.2 秒。',
    idle: '可以问我 Lumen 的目标、决策或流程，我会先查记忆再回答。',
    assistant: { name: 'Lumen 助手', description: 'Lumen 项目的工程助手（演示数据）。', persona: '你是 Lumen 项目的工程助手。用户用什么语言提问，就用什么语言回答。提到档案或记忆时，用标题或内容称呼，不要写出 id。' },
  },
  en: {
    user: [
      'Prefers concise answers: conclusion first, then the evidence.',
      'Uses TypeScript for code examples and pnpm for packages.',
      'Writes review comments in English and commits as Conventional Commits.',
    ],
    memory: [
      { content: 'This week: checkout performance, target p75 LCP under 2.5 s.', importance: 'critical' },
      { content: 'Lumen releases through Changesets; publish runs manually after merging to main.', importance: 'normal' },
      { content: 'The test database is postgres-test in docker compose, on port 55432.', importance: 'normal' },
      { content: 'No production deploys on Friday afternoons.', importance: 'critical' },
      { content: 'API errors always return { code, message, details }.', importance: 'low' },
    ],
    documents: [
      { title: 'Investigation: checkout LCP', description: 'Bottlenecks on the checkout first paint and the plan', content: '# Investigation: checkout LCP\n\nCurrent p75 LCP is 3.1 s; the target is under 2.5 s.\n\n## Findings\n\n1. Above-the-fold product images declare no dimensions, which shifts the layout.\n2. The payment SDK loads synchronously and blocks the first paint for about 400 ms.\n\n## Next steps\n\n- Declare image dimensions and preload the hero image.\n- Load the payment SDK after the first interaction.\n' },
      { title: 'Decision: queue event ingestion', description: 'Events go through Kafka into ClickHouse in batches', content: '# Decision: queue event ingestion\n\n## Context\n\nWriting straight to ClickHouse amplified writes at peak and delayed metrics on September 12.\n\n## Decision\n\nTracking events go to Kafka first; consumers write them to ClickHouse in batches of 5,000 or every 2 seconds.\n\n## Consequences\n\n- Metrics lag by at most about 5 seconds.\n- Consumers deduplicate by event_id so writes stay idempotent.\n' },
      { title: 'Incident review: delayed metrics on September 12', description: 'The write bottleneck a campaign exposed', content: '# Incident review: delayed metrics on September 12\n\n**Impact**: dashboard metrics lagged by about 40 minutes.\n\n**Cause**: a marketing campaign brought 6x traffic and the ClickHouse merge queue backed up.\n\n**Response**: scaled up and throttled writes; the lasting fix is queued ingestion, see the decision.\n\n**Follow-up**: alert when write latency exceeds 2 minutes.\n' },
      { title: 'Release checklist', description: 'Confirm each step before a release', content: '# Release checklist\n\n1. Confirm CI is green on main.\n2. Run `pnpm changeset version` and check the versions and notes.\n3. Walk the checkout, sign-in and reporting paths on staging.\n4. Trigger the publish workflow and post in #release.\n5. Watch the error rate and LCP dashboards for 30 minutes.\n' },
      { title: 'Onboarding guide', description: 'Local setup, branches and where to ask', content: '# Onboarding guide\n\n- Clone the repository, run `pnpm install`, then start with `pnpm dev`.\n- Local database: `docker compose up postgres-test`.\n- Branches are `feat/…` or `fix/…`; a PR needs one approving review.\n- Check the project Documents first, then ask in #lumen-dev.\n' },
      { title: 'Legacy deploy procedure', description: 'Replaced by the publish workflow', archived: true, content: '# Legacy deploy procedure\n\nSign in to the bastion host and run the deploy script by hand. Replaced by the publish workflow; kept for reference.\n' },
    ],
    spaces: [
      { name: 'Lumen project', description: 'Technical decisions, system facts and how they relate', active: true, memories: [
        { category: 'decision', importance: 5, tags: ['architecture'], entities: ['Kafka', 'ClickHouse', 'Event ingestion'], content: 'Event ingestion moves to a Kafka queue; consumers write to ClickHouse in batches.' },
        { category: 'fact', importance: 4, tags: ['data'], entities: ['ClickHouse', 'Postgres'], content: 'ClickHouse stores tracking events; Postgres stores accounts and orders.' },
        { category: 'fact', importance: 4, tags: ['performance'], entities: ['Checkout service', 'Payment SDK'], content: 'Checkout loads the payment SDK synchronously, blocking the first paint for about 400 ms.' },
        { category: 'insight', importance: 4, tags: ['performance'], entities: ['Checkout page'], content: 'Hero images without declared dimensions are the main cause of layout shift on checkout.' },
        { category: 'decision', importance: 4, tags: ['observability'], entities: ['Grafana', 'ClickHouse'], content: 'Grafana alerts when metric write latency exceeds 2 minutes.' },
        { category: 'context', importance: 3, tags: ['incident'], entities: ['ClickHouse', 'Marketing campaign'], content: 'The September 12 campaign brought 6x traffic and exposed the ClickHouse write bottleneck.' },
        { category: 'decision', importance: 3, tags: ['architecture'], entities: ['Kafka', 'event_id'], content: 'Ingestion consumers deduplicate by event_id so writes stay idempotent.' },
        { category: 'fact', importance: 3, tags: ['data'], entities: ['Reporting service', 'ClickHouse'], content: 'The reporting service pre-aggregates from ClickHouse every 5 minutes.' },
        { category: 'fact', importance: 3, tags: ['infrastructure'], entities: ['Redis'], content: 'Redis caches sessions and rate-limit counters with a 15-minute TTL.' },
        { category: 'fact', importance: 3, tags: ['frontend'], entities: ['Next.js', 'Checkout page'], content: 'The frontend runs on Next.js; checkout is server-rendered.' },
      ] },
      { name: 'Team conventions', description: 'Process, reviews and how we work together', active: true, memories: [
        { category: 'preference', importance: 4, tags: ['review'], entities: ['Code review'], content: 'A PR needs at least one approving review before it merges.' },
        { category: 'preference', importance: 3, tags: ['git'], entities: ['Conventional Commits'], content: 'Commit messages follow Conventional Commits in English.' },
        { category: 'fact', importance: 3, tags: ['release'], entities: ['Changesets'], content: 'Releases use Changesets, with notes in English and Chinese.' },
        { category: 'preference', importance: 3, tags: ['release'], entities: ['Production deploys'], content: 'No production deploys on Friday afternoons.' },
        { category: 'context', importance: 2, tags: ['meeting'], entities: ['Stand-up'], content: 'Stand-up is at 10:00 daily; tech talks are on Thursday afternoons.' },
      ] },
      { name: 'Research archive', description: 'Conclusions from finished investigations', active: false, memories: [
        { category: 'insight', importance: 3, tags: ['research'], entities: ['TimescaleDB', 'ClickHouse'], content: 'TimescaleDB was evaluated; its write throughput cannot replace ClickHouse.' },
        { category: 'insight', importance: 2, tags: ['research'], entities: ['Web Vitals'], content: 'Sampling 10% of Web Vitals is enough for a stable p75.' },
      ] },
    ],
    title: 'Checkout performance and ingestion',
    question: /checkout/iu,
    correction: /remember/iu,
    documentQuery: 'checkout LCP event ingestion',
    recallQueries: ['checkout layout shift first paint', 'event ingestion decision'],
    target: 'target p75 LCP under 2.5 s',
    corrected: 'This week: checkout performance, target p75 LCP under 2.2 s.',
    keys: { images: 'dimensions', sdk: 'payment SDK', ingestion: 'Kafka' },
    answer: ({ target, causes, ingestion, documents }) => [
      `**Target**: checkout p75 LCP under 2.5 s${target ? ' (working memory)' : ''}.`,
      causes.length === 0 ? '' : `\n**Known causes** (Memory Spaces):\n${causes.map(cause => `- ${cause}`).join('\n')}`,
      ingestion === undefined ? '' : `\n**Event ingestion**: ${ingestion}`,
      documents.length === 0 ? '' : `\nSee ${documents.map(title => `“${title}”`).join(' and ')} in Documents.`,
    ].filter(Boolean).join('\n'),
    saved: 'Working memory updated: the checkout target is now p75 LCP under 2.2 s.',
    idle: 'Ask me about Lumen’s goals, decisions or process; I check memory before answering.',
    assistant: { name: 'Lumen assistant', description: 'Engineering assistant for the Lumen project (demo data).', persona: 'You are the engineering assistant for the Lumen project. Answer in the language the user writes in. Refer to documents and memories by title or content, never by id.' },
  },
}

export const DOCS_DEMO_LANGUAGES = Object.keys(CONTENT)

/** The conversation preset the demo shows in place of the test preset. */
export function docsDemoAssistant(language) {
  const content = CONTENT[language]
  if (content === undefined) throw new Error('Unknown docs demo language: ' + language)
  return content.assistant
}

/**
 * Seed the demo into a disposable data root before the Host starts, through
 * the Sources' own management operations.
 */
export async function seedDocsDemo({ dataDir, workspace, language }) {
  const content = CONTENT[language]
  if (content === undefined) throw new Error('Unknown docs demo language: ' + language)
  const runner = new MemoryCompositionRunner()
  try {
    await runner.mount(strategyPlugin, { instanceId: 'docs-strategy' })
    await runner.mount(runtimePlugin, { instanceId: 'runtime', config: { dataDir, userDataDir: dataDir } })
    await runner.mount(documentsPlugin, { instanceId: 'documents', config: { dataDir } })
    await runner.mount(spacesPlugin, { instanceId: 'memory-spaces', config: {
      dataDir, timeoutMs: 20_000, defaultRecallLimit: 20,
      providers: [{ use: 'dsh-mnemon-provider-mnemon-native', instanceId: 'mnemon-native' }],
    } })
    // The Host names a workspace by its resolved path, as DSH reports it.
    const scope = { storage: 'global', workspaceId: realpathSync(workspace) }
    const runtime = await runner.managementClient('source:runtime', scope)
    for (const entry of content.user) await runtime.mutate('mutate', { action: 'add', target: 'user', content: entry, importance: 'normal' }, { confirmed: true })
    for (const entry of content.memory) await runtime.mutate('mutate', { action: 'add', target: 'memory', ...entry }, { confirmed: true })

    const documents = await runner.managementClient('source:documents', scope)
    for (const document of content.documents) {
      await documents.mutate('mutate', { action: 'create', title: document.title, description: document.description, content: document.content }, { confirmed: true })
    }
    const snapshot = (await documents.read('snapshot', null)).value
    const listed = [...(snapshot.active ?? snapshot.documents ?? [])]
    for (const document of content.documents.filter(candidate => candidate.archived)) {
      const created = listed.find(candidate => candidate.title === document.title)
      if (created !== undefined) await documents.mutate('archive', { id: created.id, summary: document.description }, { confirmed: true })
    }

    const spaces = await runner.managementClient('source:memory-spaces', scope)
    for (const space of content.spaces) {
      const body = (await spaces.mutate('body-create', { name: space.name, description: space.description, active: true, providerId: 'mnemon-native', connection: {} }, { confirmed: true })).value
      for (const memory of space.memories) await spaces.mutate('remember', { ...memory, source: 'user', memoryBodyId: body.id }, { confirmed: true })
      if (!space.active) await spaces.mutate('body-update', { memoryBodyId: body.id, active: false }, { confirmed: true })
    }
  } finally {
    await runner.dispose()
  }
}

const text = message => typeof message?.content === 'string' ? message.content : (message?.content ?? []).map(block => block.text ?? '').join('\n')
const parse = message => { try { return JSON.parse(text(message)) } catch { return undefined } }

/** Every string a tool result holds under the given keys, in order. */
function fields(value, keys, found = []) {
  if (Array.isArray(value)) for (const item of value) fields(item, keys, found)
  else if (value !== null && typeof value === 'object') {
    for (const [key, item] of Object.entries(value)) {
      if (keys.includes(key) && typeof item === 'string') found.push(item)
      else fields(item, keys, found)
    }
  }
  return found
}

/**
 * Script only the model's decisions. The Documents search, the Memory Spaces
 * recall and the working-memory correction run through the Host's real View
 * tools, and the answer is assembled from what they return.
 */
export function docsDemoModel(language, report) {
  const content = CONTENT[language]
  if (content === undefined) throw new Error('Unknown docs demo language: ' + language)
  return request => {
    const messages = request.messages ?? []
    const userIndex = messages.findLastIndex(message => message.role === 'user')
    if (userIndex < 0) return content.idle
    const human = text(messages[userIndex]).split('\n')[0]
    // DSH titles a session with a separate request that carries no memory protocol.
    if (human.startsWith('Generate the session title')) return content.title
    const results = messages.slice(userIndex + 1).filter(message => message.role === 'tool').map(parse)
    if (content.correction.test(human)) {
      if (results.length === 0) return { name: 'mnemon_runtime_memory', args: { action: 'replace', target: 'memory', old_text: content.target, content: content.corrected, importance: 'critical' } }
      report({ event: 'correction', success: results[0]?.success === true })
      return content.saved
    }
    if (!content.question.test(human)) return content.idle
    // Documents first, then one Recall query and the one refinement the Host allows.
    if (results.length === 0) return { name: 'mnemon_document_search', args: { query: content.documentQuery, limit: 3 } }
    if (results.length <= content.recallQueries.length) return { name: 'mnemon_recall', args: { query: content.recallQueries[results.length - 1], limit: 6 } }
    const prompt = messages.map(text).join('\n')
    const evidence = results.slice(1).flatMap(result => fields(result, ['content']))
    const pick = key => evidence.find(item => item.includes(key))
    const causes = [pick(content.keys.images), pick(content.keys.sdk)].filter(item => item !== undefined)
    const documents = [...new Set(fields(results[0], ['title']))].slice(0, 2)
    report({ event: 'answer', resident: prompt.includes(content.target), recalled: evidence.length, documents: documents.length })
    return content.answer({ target: prompt.includes(content.target), causes, ingestion: pick(content.keys.ingestion), documents })
  }
}
