# Modular Agent Harness 设计

英文版本：[DESIGN.md](../../../harness/modular-agent/DESIGN.md)

Mnemon 的核心优势是 modular agent 模型：自进化能力应该作为外置
harness 挂载到已有 agent 上，而不是重新实现一个 agent framework。

Mnemon 不拥有 agent runtime，但它拥有 harness runtime substrate。这个
substrate 是让独立 harness modules 能被安装、组合、调度、审计，并安全地与
宿主 agent 协作的系统层。

## 核心判断

任何支持标准扩展点的宿主 agent，都可以通过安装 Mnemon harness module
获得自进化能力。

宿主 agent 拥有 ReAct loop：

```text
观察上下文 -> 推理 -> 调用工具 -> 检查结果 -> 继续或停止
```

Mnemon 在这个 runtime 外围挂载额外 loop：

```text
Memory Loop：经验 -> working memory -> long-term memory -> recall
Skill Loop：重复 workflow -> evidence -> proposal -> skill lifecycle
Future Loops：evaluation、risk review、safety checks、benchmark feedback
```

关键区分是：

```text
Host Agent = execution runtime
Mnemon     = harness runtime substrate
Modules    = memory / skill / eval / risk / review / audit / policy
```

## 外置化的 Agent 能力

一个重要设计 insight 是：很多被称为高级 agent 特性的能力，并不一定需要新的
runtime。如果宿主已经拥有 ReAct loop，那么围绕这个 loop 的行为层通常可以用
这些方式表达：

- skills 或 protocol documents：定义可复用动作
- hooks：定义生命周期时机
- Markdown guides：定义 policy、判断规则和 procedure
- filesystem state：保存持久 memory、proposal、report 和 index
- subagents 或 daemon：执行较重的维护任务

换句话说，很多行为层能力本质上是：

```text
ReAct loop + skill/protocol + hook timing + Markdown policy + durable state
```

宿主 runtime 仍然拥有底层执行：UI、permissions、tool routing、sandboxing、
model calls 和 session management。Mnemon 聚焦的是可以挂载到这个 runtime
外围的行为层。

这也是为什么架构强调 harness modules，而不是新的 agent framework。目标是
把高级 agent 行为变成可移植、可检查、可安装的模块。

但是，当多个 module 需要协作时，仅有 skill、hook 和 Markdown 资产还不够。
Mnemon 需要自己的 substrate 来处理：

- module registry 和 versioning
- canonical filesystem layout
- environment 和 configuration resolution
- hook binding 和 prompt injection boundaries
- projection 到 host-native skill surfaces
- proposal、report、audit 和 state schemas
- locks、leases、queues 和 background job status
- setup、uninstall、upgrade 和 recovery paths
- cross-module protocols

这个 substrate 仍然不是 agent runtime。它不拥有 ReAct loop，不和用户对话，
也不替代宿主的 tool routing。

## Memory-Centered Harness Layer

Mnemon 的 harness 模型是 memory-driven 的。持久 agent 不应该只是调用工具或
遵循 prompt；它应该把经验转化为可治理的长期状态，并用这些状态改进未来行为。

这让 Mnemon 区别于纯工具连接层。工具协议帮助 agent 连接外部工具、数据源和
服务；Mnemon 则围绕宿主 runtime 组织 memory-centered governance layer：

```text
experience -> memory -> skills -> goals -> eval / risk / review / audit
```

Memory 是连续性的中心。Skill evolution 依赖被记住的 evidence 和重复
workflows。Goal module 依赖 durable objective state。Eval、risk、review 和
audit loops 依赖 decisions、changes 和 outcomes 的记录。Backup 和 replication
保护的也是这组以 memory 为中心的 harness state。

这不意味着所有事实都应该被强行写入 memory。这里的区别是：memory 保存
agent-specific experience、preferences、decisions、failures、skills 和
long-running state。外部知识库、web search 和 tool retrieval 仍然是 retrieval
surfaces，除非它们的结果被沉淀为持久 agent state。

## 宿主与 Harness 分工

| 层 | 所属 | 职责 |
| --- | --- | --- |
| ReAct loop | Host agent | 任务执行、规划、工具调用、验证、用户交互。 |
| Prompt assembly | Host agent | 决定哪些上下文进入模型。 |
| Tool routing | Host agent | 在宿主权限模型下选择和执行工具。 |
| Native skills | Host agent | 使用宿主自己的机制发现和调用 skill。 |
| Evolution modules | Mnemon harness | 通过可挂载资产增加 memory、skill evolution、evaluation、review loop。 |
| Canonical state | Mnemon harness | 保存持久记忆、skill lifecycle state、evidence、proposal 和 report。 |
| Harness substrate | Mnemon harness | 提供 module registry、filesystem layout、environment、setup、projection、reports、proposals、locks、queues 和跨模块协议。 |
| Maintenance runner | Mnemon harness | 可选地调度模块后台任务，但不成为 agent runtime。 |

这个分工让 Mnemon 保持可移植。宿主可以只采用某一个 module，而不必更换
runtime。

它也避免另一个误解：Mnemon 不应被看作只是一堆 Markdown skills。Harness
substrate 让 modules 可以协作，同时又不变成单体 agent framework。

## 执行平面与治理循环

Modular agent 模型把宿主执行平面和 harness 治理循环分开。

宿主 agent 拥有执行平面：它运行 ReAct loop、和用户交互、调用工具，并决定
具体工作怎样执行。Mnemon 拥有围绕这个执行平面挂载的治理循环：memory、
skill lifecycle、goal tracking、evaluation、risk、review、audit、policy，
以及未来的 backup 或 replication。

这类似服务系统中 application logic 与 control plane 的关系。Application
仍然完成实际工作；control plane 提供 state、policy、observability、review、
recovery 和 coordination。Mnemon 应该在 agent 架构中承担这个 harness 角色。

这个区分很重要：agent 核心执行和外围治理 loop 可以独立演进。宿主可以持续
改进 reasoning 和 tool execution；Mnemon 则可以独立改进 memory、skills、
evaluation、review、audit 或 replication，而不需要把所有关注点揉进一个
agent framework。

## 标准接入面

| 原语 | Harness 用法 |
| --- | --- |
| Hooks | 在 Prime、Remind、Nudge、Compact 或等价宿主事件上安装生命周期提醒。 |
| Skills | 暴露 `memory_get`、`memory_set`、`skill_observe`、`skill_manage` 等 protocol 操作。 |
| Subagents | 在在线任务路径之外运行 dreaming、curator review 等较重的维护任务。 |
| Daemon | 可选地调度已安装 module 的后台维护任务。 |
| Filesystem | 在可预测目录和 project/user scope 下保存 canonical module state。 |
| Environment | 让 protocol skill 通过环境变量解析路径，而不是写死某个宿主 agent。 |

最低要求是宿主具备 hook-like 生命周期机制。Skills 和 subagents 会让集成更
自然，但有能力的 agent 也可以直接遵循 Markdown protocol。

## Harness Daemon

`mnemon-daemon` 是 proposed harness daemon：用于已安装 Mnemon modules 的后台
maintenance runner。

它有价值，是因为部分模块工作不适合放在在线 ReAct loop 中执行：

- memory consolidation 的 dreaming
- skill curator review
- evaluation jobs
- risk scans
- audit 和 report 写入
- leases、locks、queues 和 module status

daemon 不是宿主 agent，也不是第二个 runtime。它不应和用户对话，不应接管
任务执行，不应替宿主进行 tool routing，也不应绕过 proposal 和 approval
policy。

边界应保持为：

```text
Host Agent      -> 在线任务执行和用户交互
mnemon-daemon   -> 离线 harness maintenance 和定时 module jobs
Harness Modules -> memory、skills、eval、risk、review、audit、policy
```

在 MVP 阶段，module 仍然可以通过人工触发或 host hooks 运行。当多个 module
需要共享 scheduling、logs、reports、locks 和 status 时，daemon 会变得重要。

## 当前 Module

| Module | 目的 | 当前参考宿主 |
| --- | --- | --- |
| Memory Loop | 增加 working memory、long-term memory 和 dreaming consolidation。 | Claude Code setup 位于 `harness/setup/install.sh --host claude-code --module memory-loop`。 |
| Skill Loop | 增加 active/stale/archived skill lifecycle、evidence capture、curator proposal 和批准后的 lifecycle mutation。 | Claude Code setup 位于 `harness/setup/install.sh --host claude-code --module skill-loop`。 |

## 与 Skill Packs 的关系

Mnemon 不是以 skill collection 为主要定位。

Skill packs 为宿主 agent 提供任务或 workflow 能力。例如 coding skill pack 可以
教 agent 进行 planning、debugging、testing、review、release 或 skill authoring。
这些 skill 是面向宿主的实用能力。

Mnemon 位于另一层：

```text
Host Agent
  -> task/workflow skill packs
  -> Mnemon harness modules
```

任务 skill 帮助 agent 做事。Mnemon harness modules 帮助 agent 管理围绕这些
工作的 memory、skill lifecycle、evaluation、risk、audit、review 和 policy。

这两层应当兼容。Mnemon 可以观察、评估、整理、归档、恢复或审计 skill
collections，但不应被描述为仅仅另一个 skill pack。

## Memory 差异化

Memory module 使用冷热记忆模型：

- Working memory 面向模型。它是小型 Markdown 上下文，进入 prompt，由
  agent 维护。
- Long-term memory 面向工程。Mnemon 在 prompt 外保存更大、更持久的记忆，
  并按需召回。
- Dreaming 负责二者之间的巩固：把 durable working memory 写入 Mnemon，
  然后压缩或淘汰 prompt-facing working memory。

这保留了 Markdown memory 的模型友好性，同时避免单个 always-loaded 文件的
容量上限。

## 未来 Module

同样的 harness 模式可以继续支持更多 loop：

- Eval loop：收集结果、运行 benchmark，并把失败反馈为 proposal。
- Risk loop：在 skill 或 memory 变更生效前进行扫描。
- Review loop：协调人工审批、checkpoint 和 release gate。
- Audit loop：记录哪个 module 因为什么行动，以及改变了什么。
- Policy loop：维护宿主特定的安全与权限策略。
- Backup / replication loop：在不同机器、节点或宿主 agent 环境之间保存和恢复
  harness state。

每个 module 都应保持可独立安装。Module 可以选择使用 `mnemon-daemon` 做后台
调度，但 basic install path 不应强依赖 daemon。

Backup 和 replication 应从保守形态开始。第一版更适合采用 primary-writer
模型，支持 snapshot、restore、node identity、leases 或 locks、conflict
detection、merge proposal 和 audit record。多节点 active-active coordination
可以留到后续设计。

## 可组合 Module Flow

Harness modules 应通过显式 state 和 proposal 边界组合，而不是静默互相调用。

示例：

```text
Skill Loop 产生 skill proposal
  -> Risk Loop 扫描 proposal
  -> Review Loop 请求 approval
  -> Audit Loop 记录决策
  -> Skill Loop 应用已批准的变更
```

同样的模式也可以用于 memory consolidation、policy update、benchmark failure
或 host setup change。一个 module 可以产生 evidence 或 proposal；另一个
module 可以 review、scan、approve 或 record。宿主 agent 仍然是决定何时调用
这些能力的 runtime。

## 长程 Goal Modules

未来的 `mnemon-goal` module 可以基于这个架构支持长程 agent 工作，但它本身
不成为任务 runtime。

`mnemon-goal` 会维护 objective state、milestones、blockers、decisions、
handoffs 和 progress reports。围绕一个长期目标，它可以多次协调其他 harness
modules：

- Memory Loop 在任务开始时 recall context，并在 milestone 后保存 durable
  decisions。
- Skill Loop 观察重复 workflow，并提出可复用 skill。
- Eval Loop 通过 tests、benchmarks 或 checklists 检查 milestone 质量。
- Risk Loop 在危险变更执行或应用前进行扫描。
- Review Loop 对关键 proposal 或高影响步骤请求 approval。
- Audit Loop 记录 triggers、decisions、changes 和 outcomes。
- Policy Loop 持续暴露项目约束和用户偏好。
- `mnemon-daemon` 可以发现 stale、blocked 或 due goals，并调度维护任务。

这使 `mnemon-goal` 成为一个 orchestrating harness module：它围绕 durable
objective 协调 memory、skills、evaluation、risk、review、audit 和 policy，
而实际任务执行仍然由宿主 agent 完成。

## 非目标

- 不替换宿主 agent runtime。
- 不让 `mnemon-daemon` 变成 agent runtime。
- 不把 Mnemon 降低为只是 skill pack 或 prompt collection。
- 不要求唯一通用 skill 格式。
- 不把所有 state 注入 prompt。
- 不在缺少明确策略和 review 的情况下进行 self-modifying change。

## 参考宿主案例

Claude Code 是第一个 modular-agent case，因为它目前暴露了相对完整的一组扩展
能力：hooks、skills、subagents、filesystem config，以及 project/user scope。

这让 Claude Code 很适合作为 Mnemon harness modules 的实验性挂载点：

- hooks 可以承载 Prime、Remind、Nudge、Compact 和未来 module triggers
- skills 可以暴露可移植的 protocol operations
- subagents 可以运行 dreaming、curator review 和其他维护任务
- project/user config 可以验证 local/global install scope
- settings files 可以让 setup 和 uninstall 可重复执行

Claude Code 是 reference host，不是唯一支持的 runtime。它的作用是验证
harness attachment model。架构仍应保持可移植，面向任何具备类似扩展点的
宿主 agent。
