# Mnemon Harness Roadmap

英文版本：[ROADMAP.md](../../harness/ROADMAP.md)

这份 roadmap 描述 Mnemon Harness 如何从当前 MVP loops，逐步成长为更完整的
modular-agent governance layer。它是方向性路线图，不是固定 release schedule。

核心原则很简单：一次做好一个 loop，让每个 module 都能独立产生价值，同时不把
Mnemon 做成替代宿主的 agent runtime。

这份路线图是 memory-driven 的，而不是 module-driven 的。Memory 是让 agent
经验变成持久状态的连续性中心。其他 loop 应该围绕这些状态进行增强、治理或
运行，而不是变成彼此割裂的功能。

## 当前 MVP Loops

Mnemon 已经有两个可安装的 MVP harness loops。

| Loop | 状态 | 目的 |
| --- | --- | --- |
| Memory Loop | 已实现 MVP | 连接 prompt-facing working memory、Mnemon long-term memory 和 dreaming consolidation。 |
| Skill Loop | 已实现 MVP | 通过 evidence、curator review 和批准后的 lifecycle change 管理 active、stale、archived skills。 |

这两个 MVP loops 使用同一套 harness 词汇：

- GUIDE 文件定义 loop policy。
- setup scripts 将 loop 挂载到宿主 agent。
- hooks 在宿主定义的生命周期时机注入提示。
- protocol skills 暴露可复用操作。
- subagents 执行较重的维护工作。
- Mnemon-owned state 把 module 数据保存在宿主 runtime 之外。

Claude Code 是第一个 reference host，因为它提供 hooks、skills、subagents 和
project/user configuration。架构仍应保持可移植，面向其他具备类似扩展点的
宿主 agent。

## Phase 1：稳定核心 Loops

重点：让当前 Memory Loop 和 Skill Loop 可靠可用。

- 加固 setup、uninstall 和 upgrade 路径。
- 改进 path 和 environment resolution。
- 保持 hook prompts 足够短，把 policy 放入 GUIDE 文件。
- 为每个 loop 观察到什么、改变了什么提供更清晰的 report。
- 验证 local 和 project-level installation scopes。
- 保持 loops 可独立安装。

成功标准是：宿主 agent 可以单独安装 memory 或 skill evolution，并清楚理解发生
了哪些改变。

## Phase 2：Harness Runtime Substrate

重点：让多个 loops 更容易协同运行。

这一阶段应该引入 modules 所需的最小共享 substrate：

- module registry 和 version metadata
- canonical filesystem layout
- shared state、reports、proposals 和 audit records
- locks、leases、queues 和 background job status
- setup、uninstall、upgrade 和 recovery conventions
- 可选的 `mnemon-daemon`，用于 scheduled maintenance

`mnemon-daemon` 应该是 harness maintenance runner，而不是 agent runtime。它可以
运行 dreaming、curator review、eval jobs、risk scans、audit writing，以及其他
离线 module 工作。

## Phase 3：Goal Loop

重点：支持长程任务，但不替代宿主 agent。

未来的 `mnemon-goal` module 应维护 durable goal state：

- objectives
- milestones
- blockers
- decisions
- handoffs
- progress reports
- stale 或 due goal detection

宿主 agent 仍然执行实际工作。`mnemon-goal` 协调外围 harness loops：memory
recall 与 consolidation、skill proposal、evaluation、risk review、human
review、audit 和 policy reminders。

## Phase 4：Governance Loops

重点：为自进化增加控制、质量和问责能力。

可能的 modules：

- Eval Loop：tests、benchmarks、checklists 和 outcome feedback。
- Risk Loop：扫描 proposed memory、skill、policy 或 setup changes。
- Review Loop：协调 human approval 和 release gates。
- Audit Loop：记录 triggers、decisions、actors、changes 和 outcomes。
- Policy Loop：保持宿主特定 constraints 和 permission guidance 可见。

这些 loops 应该通过显式 proposals、reports 和 approval boundaries 组合，而不是
静默修改彼此的 state。

## Phase 5：Portability And Replication

重点：让 harness state 能在不同 agents、projects 和 machines 之间迁移。

Portability 工作包括：

- 更多 host-agent setup targets
- host capability detection
- adapter-light installation guides
- harness state import 和 export
- backup 和 restore
- memory、skills、goals、proposals、reports、audit logs 和 policy state 的
  replication

Replication 应从保守形态开始：primary-writer model、snapshots、restore、node
identity、leases 或 locks、conflict detection、merge proposals 和 audit
records。多节点 active-active coordination 是后续设计。

## 近期非目标

- 不构建新的通用 agent runtime。
- 不在核心 loops 稳定前实现所有未来 loop。
- 不要求每个宿主 agent 使用相同 skill format。
- 不让 self-modifying changes 绕过 review 和 audit。
- 不在 local harness state 稳定前过度设计 distributed replication。

Mnemon 应该逐个 loop 成长。长期目标是形成 modular harness layer，让 memory、
skills、goals、evaluation、risk、review、audit、policy 和 replication，都能
围绕宿主 agent 的 execution loop 独立演进。
