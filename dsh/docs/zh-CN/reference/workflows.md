# 生命周期与核心流程

**简体中文** | [English](../../en/reference/workflows.md) | [文档中心](../README.md)

本页描述默认的**分层策略**。**通用策略**在同一份预算内提供全部可用 Source，由模型决定如何路由：具名工具服务各自的 Source，其余路由通过 `mnemon_view_route` 读取、`mnemon_view_action` 修改。它除各路由自身的上限外没有召回准入信封，不做 Runtime 容量维护与归档，也不做空闲审查。见[策略与增强](../guides/capabilities.md#策略与增强)。

## 每轮上下文

分层策略提供稳定的路由指引、静态 Runtime Memory 协议，以及按需更新的 Wake 快照消息：

- `mnemon:routing`：system prompt section；当 `routingGuidance=true` 时提供简短的分层查询边界；
- `mnemon:runtime-memory-protocol`：system prompt section，只包含不变的 Runtime Memory 语义与写入规则。它只在 eager Runtime Source 参与自动投影时出现，记忆变更前后保持逐字节一致；
- Wake 快照：由当前 root 回合固定的不可变 View 渲染，以 `source.plugin=dsh-mnemon`、`form=recall` 的自有消息承载。Runtime Memory 提供带 revision 的完整 USER/MEMORY 状态快照，不再重复静态协议；Documents 与 Memory Spaces 只贡献有界封面，不注入完整目录。

Host 只在 Wake 内容变化时追加新的 user-role 插件消息，不再重新发送其他插件的共享上下文。快照保持完整状态，不构造依赖旧消息的补丁链；静态协议留在 system 前缀，Source 文本按原文传递而不做模板插值。回合、子 Agent 与运行代的所有权见[架构说明](../development/architecture.md#生命周期与失败)。

生命周期会在 Host 组装 System Prompt 前固定 View，并让本回合所有模型 step 使用同一个 View：

```text
turn/start
  -> 进入 system-prompt/assemble hook
  -> beginTurn（根回合 + 操作范围）
  -> Source 能力事实 → Strategy 提案 ViewSpec → 校验 → Source 投影
  -> 固定 Source 修订/摘要与仅留在 Host 的权限
  -> 生成有界 Wake
  -> 继续 Host 提示词组装
  -> 让静态协议 section 与已固定的 Runtime Source 对齐
  -> 将变化后的 Wake 作为本插件的完整快照消息追加

agent/pre-step(step=1)
  -> 为新回合取消待执行/执行中的后台审查
  -> 仅标记一次 Prime
  -> 每个会话至多追加一次简短 recall/writeback cue
  -> 主 Agent 决定是否调用记忆工具
```

Source snapshot 不执行语义召回。Prime 只初始化路由状态，不执行异步 CLI 状态查询。

子 Agent 在 `agent/created`、driver 启动前捕获并保留存活父 Agent 的固定 View 与运行图。自己的各个回合固定这一被保留的 View，即使父回合已结束或已进入更新的 generation。子 Agent activation 销毁时释放委托；冷恢复重新获取委托；Host 显式创建且没有父模型回合的后台子任务生成新的 scoped View。子 Agent 拥有自己的回合权限，但不安装 root 专属的 cue 或空闲审查。详见[权限生命周期](../development/architecture.md#生命周期与失败)。

## Agent 召回

```text
根 Agent 或子 Agent 调用 mnemon_recall（query，可选 memoryBodyIds）
          |
          v
解析当前执行 Agent 自己固定的回合与保留的运行时
          |
          v
读取 Host 上已固定的记忆空间 Source 状态
          |
          v
校验请求 ID 是已固定集合的子集；未指定时使用全部已固定的激活 ID
          |
          v
记忆空间 Source 并发检索已授权的 Provider 命名空间
          |
          v
质量归一化 + 倒数排名融合
          |
          v
丢弃低相关项；首次最多准入 4 条 / 3,600 字符
          |
          v
LLM 判断 evidence 是否足够
          | 足够                        | 不足
          v                             v
直接回答，不再 Recall             显式提交一个不同查询
                                        |
                                        v
                              再检索一次、去重并关闭 Recall
          |
          v
两次在当前执行 Agent 回合共享至多 6 条、每条 1,200 字符、
总正文 4,800 字符的总预算
```

这一准入信封是分层策略的每回合策略；通用策略只应用各路由自身的上限与调用预算。

模型工具不暴露 `category`、`source` 或 `intent` 过滤器：模型猜错过滤条件不能遮住精确证据。Recall 并非强制执行，普通 root 回合是 0 次 Provider 查询。LLM 主动调用后，Host 允许一个首次查询；只有 LLM 查看 evidence 后仍认为不足，才允许再提交一个实质不同的精炼查询。同查询和并发重复请求会 join 或重放；第三个不同查询只重放最新 evidence，不再到达 Provider。随后至多执行一次 Related，而且只能使用两次 Recall 任一已准入的 `memoryBodyId + id`；重复 Related 同样重放结果。

Recall、Related 和单次 Documents 搜索槽位按执行中的 Agent 回合计预算。同一回合内并发调用共享状态，兄弟任务、后续回合和冷恢复的 activation 不会共享缓存 evidence 或占用彼此的预算。重放结果限于本次请求的 Memory Space 子集。Document search 另有独立边界：最多 4 条记录、每条最多 2,600 个查询附近字符、总正文最多 6,000 字符。模型侧 Memory Space 目录最多 16 项，`mnemon_status` 只返回紧凑健康汇总。完整记录、Provider 设置、路径和逐 Space 统计仍由 Web/RPC 控制面读取，不进入对话历史。

如果用户已经提供当前事实，或仓库可以直接回答，Agent 不应为了“展示记忆”而召回。

## Web 检索和 Agent 查询

Web “检索”页与模型工具路径不同：

```text
直接检索
  -> RPC 读取通道
  -> 限定到 Source 的管理检索
  -> 原始证据

Agent 查询
  -> 同一条确定性直接检索路径
  -> 派发不含 Mnemon 工具的任务 Agent
  -> 仅根据已提供的证据回答
  -> Host 将引用限定到实际的 memoryBodyId/id 对
```

“实体”和“内容”页也经 Source 管理协议执行确定性读取，不需要第二个模型。“内容”使用 Provider 的只读 browse 契约，不冒充语义 Recall。

## 显式长期写入

根 Agent 或 `/mnemon remember` 的长期写入流程：

```text
长期记忆候选
       |
       v
派发写入任务 Agent
       |
       +-- 列出记忆空间
       +-- 选择最小的适用范围
       +-- 需要查重或检查冲突时召回
       +-- 只有反复出现的独立领域才创建新范围
       +-- 按请求执行 remember / link / forget / merge
       v
结构化回执
```

空存储根首次创建 Memory Space 时使用 Mnemon 原生 `default` ID，后续 ID 由 Host 生成。向 inactive 目标写入成功后会激活它。这里的激活只影响 DSH 路由；来源数据库的合并是非破坏性的。

运行时 `add` / `replace` / `remove` 和 Document `create` / `update` 不需要模型做存储 I/O；它们通过 coordinator 进入确定性控制层。容量维护和归档才启动专用 worker。

## Runtime add：正常路径

```text
请求
  -> 规范化内容
  -> 获取进程内队列与文件锁
  -> 重新加载 memories.json
  -> 校验唯一匹配 / 重复 / 容量
  -> 写入临时 JSON 与 Markdown 投影
  -> 重命名投影文件
  -> 重命名 memories.json，作为提交标记
  -> 返回紧凑回执
```

`replace` 和 `remove` 在请求的目标内，优先用 `old_text` 唯一匹配完整正文；没有精确匹配时才使用唯一子串。重复的完整正文仍视为有歧义。只有请求中的 add 或增大正文的 replace 会超过目标上限时，才触发容量维护。

## USER.md 容量整理

容量整理与下文的 MEMORY.md 归档都属于分层策略。使用通用策略时，超出 Runtime 上限的写入会被直接拒绝。

```text
USER 新增后超过 4 KiB
          |
          v
保存修订与已提交条目的快照
          |
          v
派发无工具权限的本地压缩任务
          |
          v
返回压缩条目 + sourceIndexes
          |
          v
Host 校验：
  - 每个来源索引恰好出现一次
  - 没有重复或越界索引
  - 重要性未降低
  - 候选符合 Host 字节预算
  - 修订仍是当前版本
          |
          +-- 无效/冲突 -> 保留原始数据
          |
          v
按 UTF-8 字节确定性装填
          |
          v
重试待处理的新增操作
```

用户画像不会被发送到 Memory Spaces。worker 没有任何工具权限。

## MEMORY.md 归档与压缩

```text
分层策略下，本次 MEMORY 写入将超过配置上限（默认 10 KiB）
          |
          v
snapshot revision + 可归档的已提交 entries
（排除待提交 add，以及正被 replace/remove 的 entry）
          |
          v
Host 在本次操作的 Source / 命名空间范围内选择已有、active、可写的 Memory Spaces
          |
          v
单个目标：Host 直接路由
多个目标：独立无工具 worker 只返回 source-index 路由
  模型执行失败时，Host 在合格目标中确定性兜底
          |
          v
Host 校验精确 source coverage、目标、权限与源修订
          |
          +-- 无效/revision 已变 -> 不写 Provider；保留 Runtime
          |
          v
Host 把每条原始 entry 精确写入规划的已有 Space
  - committed receipt -> 绑定目标 digest
  - skipped -> 必须取得完全一致的 Recall evidence
          |
          v
Host 按重要性与字节预算保留原始热记忆条目
          |
          v
CAS compactAndMutate(revision, compaction, original mutation, lineage)
  -> JSON 与两个 Markdown projection 作为一次本地提交落盘
```

planner 没有数据面工具，不能创建 Memory Space。每次写入都由 Host 发起；只有每条已提交来源都有可验证的 durable destination 后，Runtime 才会变化。若远端写入后发生很晚的跨进程 revision 冲突，原 hot entries 仍会保留；远端 Provider 无法共享本地文件锁，因此已提交的 durable copy 会作为安全重复保留，而不会被破坏性回滚。

## Documents 创建、更新和归档

```text
创建/更新请求
          |
          v
按渲染后的 UTF-8 字节执行 capacityPlan
          |
     +----+----+
     |         |
   未超限     溢出
     |         |
     v         v
   提交    选择最久未访问的活跃档案
               |
               v
          保存档案与修订的快照
               |
               v
          派发归档任务 Agent
               |
               v
       写入并验证紧凑的 Mnemon 冷引用
       包含标题、摘要、计划路径、SHA-256
               |
          +----+----+
          |         |
         失败     回执有效
          |         |
          v         v
      保持活跃    检查修订
                    |
               +----+----+
               |         |
                冲突     一致
               |         |
               v         v
             保持活跃   将文件移入归档
                             |
                             v
                       重试原始修改
```

人工归档使用同一条“先索引、后迁移”路径。Mnemon 索引已经成功但 revision 冲突时不会回滚索引，因此可能出现安全的重复引用，而不会丢失 active 原文。

## 确定性活动评分和后台审查

完成的 turn 累计四种信号：

```text
score =
  min(floor(totalUserCharacters / 50), 3)
  + completedTurnCount
  + min(floor(completedToolResults / 5), 2)
  + toolDiversityScore

toolDiversityScore:
  不同工具数 < 3  -> 0
  不同工具数 = 3  -> 1
  不同工具数 >= 4 -> 2

score >= 5 时达到门槛
```

达到门槛并不代表一定写入：

```text
已完成的回合
      |
      v
score >= 5 ? -- 否 --> 为后续回合保留累计活动
      |
      是
      |
      v
Host 判断是否存在待整理内容
  - 当前轮明确要求不写记忆 -> 停止
  - 持久化意图、累计 >=320 用户字符、
    >=600 助手字符或已完成非 Mnemon 工作 -> 继续
      |
      v
等待 idleReviewMs（默认 30 秒），且距上次尝试
至少 idleReview.minIntervalMs（默认 5 分钟）
      |
      +-- 新回合 --> 取消定时器/任务，保留累计活动
      |
      v
确认 Agent 已空闲、已有 turn/end、该回合由分层策略组合，
且未达到 idleReview.maxPerSession
      |
      v
就已完成的回合启动任务 Agent
  - spawn（默认）：有界检查点，上限 idleReview.maxContextChars
  - fork：继承父 Agent 上下文
      |
      v
保守的整理决策，每轮只写一层
  - 先看档案：先检索；项目记录至多新建一份独立档案，
    不更新或归档已有档案
  - 热记忆：只收录用户新提出的、明确且持久的陈述；
    MEMORY.md 只收档案未涵盖的简短规则，不收项目记录
    （idleReview.runtimeMemory: false 时关闭）
  - 建了档案就不能再改工作记忆，反之亦然
  - 不提供记忆空间写入工具
      |
      +-- 已完成（含跳过）--> 清空累计活动
      |
      +-- 失败/中止 --------> 保留累计活动
```

admission 有意只使用结构信号，不调用 LLM 分类；因此达到 activity 门槛但没有 dirty candidate 的普通 checkpoint 不会启动后台模型。审查 Agent 的工具恰好是 `mnemon_document_search`、`mnemon_runtime_memory` 与 `mnemon_document_create`；`idleReview.runtimeMemory` 为 `false` 时不提供运行时记忆工具。审查 guard 让每轮只写一层：它放行的第一次新建档案或工作记忆修改决定本轮所写的层，另一层随后被拒绝，即使第一次调用失败也是如此；USER.md 的修改不受影响。“至多一份”档案或一次热记忆修改仍由 persona 约束，不是 Host mutation counter。审查的产出之后才进入记忆空间：工作记忆在容量整理时归档，项目档案在冷归档时建立索引。安装了 Agent Teams 工具时，除非设置 `idleReview.agentTeams: scoped`，审查会暂停；暂停期间状态页会给出提示。后台水位尚未持久化，Host 重启会丢失未处理的累计信号。

## 配置开关的关系

- `recallMode=off`：不再注入 recall cue，显式 `mnemon_recall` 仍可用。
- `writebackMode=off`：关闭写回 cue 和评分后台审查，显式写入仍由 `writeEnabled` 决定。
- `lifecycleEnabled=false`：关闭生命周期提醒和审查，不移除显式工具或 Web 入口。
- `routingGuidance=false`：只移除额外路由 section；Runtime Memory context 仍注册。
- `writeEnabled=false`：移除语义写工具和写 RPC，拒绝写命令；它不是文件系统只读挂载保证。
