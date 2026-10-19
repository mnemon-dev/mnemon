export const domains = ['backend', 'frontend', 'android', 'operations'] as const
export type Domain = typeof domains[number]
export const labels: Record<Domain, string> = { backend: '后端', frontend: '前端', android: '安卓', operations: '运维' }

// Frozen before running any model. These are synthetic project decisions, not memory-tool instructions.
export const acceptance = {
  currentFactCoverage: 0.90, freshRecallAccuracy: 0.90, forbiddenNoiseMatches: 0,
  staleCorrectionAnswers: 0, crossModuleAnswers: 0, unsupportedCurrentAnswers: 0,
  correctedFactRecall: 1, noWriteTurnMutations: 0, exactColdDuplicates: 0,
  memoryLimitBytes: 10240,
} as const

const specs = [
  ['service_port', '模块开发服务监听端口', ['8411', '8421', '8431', '8441'], '固定端口让联调脚本能找到对应进程，其他模块各有独立端口，不能相互借用。'],
  ['retry_limit', '模块失败重试上限', ['3', '5', '7', '9'], '上限只约束本模块的失败重试，达到次数后交由人工检查，不代表其他模块也应采用这个值。'],
  ['timeout_ms', '模块请求超时毫秒数', ['1200', '1800', '2400', '3000'], '这是已经确定的常规请求超时，不能拿临时网络抖动日志中的耗时覆盖这项配置。'],
  ['cache_ttl_seconds', '模块缓存有效期秒数', ['61', '73', '89', '97'], '过期缓存必须重新校验后再使用，本次确定的是模块默认值，单次压测输出不是新的默认配置。'],
  ['schema_version', '模块持久化结构版本', ['be-v17', 'fe-v23', 'and-v31', 'ops-v41'], '版本号用于本模块数据兼容性判断，模块之间各自演进；它既不是产品版本，也不是整个项目的统一版本号。'],
  ['queue_name', '模块异步队列名称', ['orion-be-outbox', 'orion-fe-refresh', 'orion-and-sync', 'orion-ops-rollout'], '生产者和消费者都按这个名字绑定，本模块内保持一致；不要把其他工作流的队列名字复制进来。'],
  ['idempotency_key', '模块幂等键表达式', ['tenant+order+request', 'form+revision+submit', 'device+draft+sequence', 'release+batch+attempt'], '重放相同业务请求时必须保持键稳定，三个字段缺一不可；日志里的随机跟踪编号不参与业务幂等。'],
  ['release_gate', '模块发布验收门槛标识', ['ledger-balanced', 'keyboard-focus-safe', 'offline-draft-preserved', 'rollback-watermark-safe'], '发布前必须通过这道检查，临时跳过一次检查不能改变这个长期门槛；其他模块有各自不同的验收条件。'],
] as const

// Additional independently scoped decisions for a natural workload that can reach archival.
const extendedSpecs = [
  ['conflict_policy', '模块冲突处理策略', ['preserve-pending-order', 'reject-stale-form', 'retain-offline-draft', 'freeze-rollout-batch']],
  ['retention_days', '模块审计记录保留天数', ['37', '43', '53', '67']],
  ['partition_key', '模块任务分区字段', ['tenant_id', 'filter_id', 'device_id', 'release_id']],
  ['max_batch_size', '模块批处理条目上限', ['117', '131', '149', '163']],
  ['backoff_ms', '模块重试初始退避毫秒数', ['271', '353', '419', '487']],
  ['checkpoint_prefix', '模块恢复检查点前缀', ['be-ledger-cp', 'fe-view-cp', 'and-draft-cp', 'ops-release-cp']],
  ['audit_event', '模块提交审计事件名', ['be.payment.committed', 'fe.form.submitted', 'and.draft.synchronized', 'ops.release.promoted']],
  ['dedup_window_seconds', '模块重复请求检测窗口秒数', ['191', '223', '257', '293']],
  ['locale_fallback', '模块语言回退配置', ['zh-CN', 'en-GB', 'zh-TW', 'en-US']],
  ['recovery_marker', '模块恢复成功标识', ['be-replay-ok', 'fe-rehydrate-ok', 'and-reconnect-ok', 'ops-watermark-ok']],
  ['health_route', '模块健康检查路径', ['/be/readyz', '/fe/readyz', '/and/readyz', '/ops/readyz']],
] as const

export interface Fact { id: string; domain: Domain; key: string; value: string; description: string; wave: number; supersedes?: string }
export function decision(domain: Domain, wave: number): Fact | undefined {
  const index = domains.indexOf(domain)
  const spec = specs[wave]
  if (spec) return { id: `${domain}/${spec[0]}`, domain, key: spec[0], value: spec[2][index]!, description: spec[1], wave }
  if (wave === 9) return { id: `${domain}/retry_limit`, domain, key: 'retry_limit', value: ['11', '13', '17', '19'][index]!, description: '模块失败重试上限', wave, supersedes: specs[1][2][index]! }
  if (wave === 11) return { id: `${domain}/handoff_channel`, domain, key: 'handoff_channel', value: ['be-settlement', 'fe-interaction', 'and-recovery', 'ops-rollout'][index]!, description: '模块故障交接通道', wave }
  const extended = extendedSpecs[wave - 12]
  if (extended) return { id: `${domain}/${extended[0]}`, domain, key: extended[0], value: extended[2][index]!, description: extended[1], wave }
  if (wave === 23) return { id: `${domain}/service_port`, domain, key: 'service_port', value: ['8841', '8851', '8861', '8871'][index]!, description: '模块开发服务监听端口', wave, supersedes: specs[0][2][index]! }
  return undefined
}

export function task(domain: Domain, wave: number): string {
  const label = labels[domain]
  const prefix = `我们继续 Orion 项目的 ${domain}（${label}）模块开发。`
  const suffix = `请使用 dev_read_config 查看 ${domain} 配置，再用 dev_update_config 只修改这一项，最后运行 dev_test_module 检查。保持其他字段和其他模块不变，用两句话报告实际变更与检查结果即可。`
  const f = decision(domain, wave)
  if (f) {
    const detail = specs[wave]?.[3] ?? (f.supersedes !== undefined
      ? `之前的 ${f.supersedes} 已废弃，是旧方案，不应继续作为当前${f.key === 'retry_limit' ? '重试上限' : '监听端口'}。不要因为历史日志仍有旧值而撤回这次正式纠正。`
      : wave === 11 ? '这个通道标识作为后续值班交接的固定约定，不是今天的一次临时聊天室。'
        : '这个值作为本模块后续开发、发布和故障排查的正式约定，验收脚本依赖它；与其他模块同名的配置也各自独立，请保留模块归属，不要把一次临时日志当作新的正式规则。')
    return `${prefix}我们已确定：${f.description} ${f.key} = ${f.value}。${detail}${suffix}`
  }
  if (wave === 8) return `${prefix}下面只是尚未采纳的排查假设：也许把 service_port 改成 9999 会更方便，但当前没有决策，不要修改现行配置。今天跑出来的临时耗时为 87654 毫秒，跟踪标记 TEMP_USER_${domain} 只对应这一次调试，不是长期约定。请读本模块配置并运行检查，说明为什么不能仅凭这条日志更改既定配置。只讨论，不做新的配置变更。`
  return `${prefix}这轮仅做一次临时排查，请不要写入或更新记忆。临时沙箱代号 NO_MEMORY_${domain}，虚构的测试凭据 FAKE_CREDENTIAL_${domain} 仅用于检查日志遮蔽，不是真实密钥，也不是用户偏好。service_port=9999 仍只是未采纳的假设。请读取本模块现行配置，运行检查，用两句话说明现有规则，不更改配置，不创建新的项目文档。`
}

export const forbidden = domains.flatMap(domain => [`TEMP_USER_${domain}`, `TRACE_TEMP_${domain}`, `NO_MEMORY_${domain}`, `FAKE_CREDENTIAL_${domain}`, `UNVERIFIED_HINT_${domain}`])
export function currentFacts(waves: number): Fact[] {
  const current = new Map<string, Fact>()
  for (let wave = 0; wave < waves; wave++) for (const domain of domains) {
    const f = decision(domain, wave)
    if (f) current.set(f.id, f)
  }
  return [...current.values()]
}
