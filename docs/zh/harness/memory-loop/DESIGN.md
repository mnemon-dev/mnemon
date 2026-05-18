# Memory Loop MVP 设计

相关可视化页面：[site.html](../../../site/memory-loop/site.html)

英文版本：[DESIGN.md](../../../harness/memory-loop/DESIGN.md)

可安装 MVP 资产：[harness/modules/memory-loop](../../../../harness/modules/memory-loop/README.md)

Memory loop 是 self-evolution harness 的第一个可落地切片。它给 HostAgent 提供一份面向 prompt 的工作记忆，同时使用 Mnemon 作为持久长期记忆。Harness 本身保持很小：围绕已有 HostAgent 安装 Markdown policy、hook prompt、protocol skills 和一个维护型 subagent。

## 设计目标

MVP 要回答一个问题：如何让 HostAgent 在不变成自定义 agent runtime 的前提下，跨任务记住有用信息？

答案是双层记忆循环：

- `MEMORY.md` 是 working memory。它小、模型可读，并且会进入 prompt。
- Mnemon 是 long-term memory。它能存储超出 prompt 的信息，并通过 recall/write 协议访问。
- Dreaming 是 consolidation。它把耐久信息从 working memory 写入 Mnemon，然后压缩或移除工作记忆。

这样在线路径足够简单，同时保留长期记忆能力。

## 热/冷记忆边界

Memory loop 有意区分 LLM-native memory 和 system-native memory。

`MEMORY.md` 是热记忆。它模型友好，并且 eager load 到 prompt 中，所以行为效果
最好。但它也昂贵：会消耗上下文、注意力和 prompt budget；如果没有 quota 和
consolidation，也容易积累噪声。

Mnemon 是冷记忆。它系统友好：持久、可索引、可查询、保存成本低，并且适合
零散长期内容的高效召回。它相对不那么模型原生，因为召回内容必须先被筛选，
再进入 prompt。但这个取舍是合理的，因为冷记忆给 agent 带来更大的容量和更低
的在线成本。

可以用计算机内存类比：

```text
MEMORY.md -> RAM / cache
Mnemon    -> indexed disk / durable store
Dreaming  -> writeback + compaction + eviction
Recall    -> page-in / retrieval into context
```

高频、高置信、当前有用的上下文应留在 working memory 中。低频历史、零散事实、
决策和经验应保存在 Mnemon 中，直到 focused recall 再把它们带回上下文。

这个边界是一种 pattern，而不是固定实现组合。在 MVP 中，`MEMORY.md` 代表热
记忆实现，Mnemon 代表冷记忆实现。未来可以分别增强两侧：

- model-driven filesystem memory、分层 Markdown、structured prompt memory
  或 agent-maintained notes，都是在增强热的 LLM-native 侧；
- RAG-enhanced storage、vector indexes、graph memory、hybrid retrieval 或更强的
  episodic/semantic stores，都是在增强冷的 system-native 侧；
- 更好的 dreaming、promotion、demotion、compaction 和 eviction，则是在增强二者
  之间的交换协议。

因此，memory-loop 的 contract 是：

```text
LLM-native hot memory
  <-> consolidation / promotion / demotion
System-native cold memory
```

`MEMORY.md` 和 Mnemon 是这个 contract 的第一组具体选择，不是唯一可能选择。

## Memory 与 Search/Retrieval 的边界

知识库和外部 RAG corpus 默认不应被视为 memory。

Memory 是 agent、user 或 project 积累出来的状态：偏好、决策、经验、失败、
约定和连续性。它可以被写入、巩固、替换、遗忘和召回。

Knowledge-base retrieval 更接近 search。它查询外部文档、网页、API docs、
论文、公司材料或代码索引。这类能力应更接近 `web_search`、`docs_search`、
`code_search` 和其他 retrieval tools。

边界是：

```text
Memory     -> 当前 agent/user/project 积累出的状态
Search/RAG -> agent 可以查询的外部知识源
```

Search result 只有在被 agent 内化为耐久的 user、project 或 task state 后才会
成为 memory。例如 API 文档查询结果是 search output；基于这个结果形成的项目
决策才可能成为 memory。

## 核心主体

| 主体 | 作用 | 边界 |
| --- | --- | --- |
| HostAgent | 运行任务、接收 hooks，并决定是否加载 protocol skills 或启动 dreaming subagent。 | 不拥有记忆存储协议。 |
| `MEMORY.md` | Prime 阶段加载到 prompt 的热工作记忆。 | 由 `memory_set.md` 和 dreaming subagent 维护。 |
| Mnemon | 冷长期记忆 binary 和 store，用于持久 recall 与 write。 | 通过 `memory_get.md` 和 dreaming subagent 访问。 |

其他内容都是围绕这三个主体的 harness 资产。

## Harness 概念

| 概念 | Memory Loop 资产 | 职责 | 边界 |
| --- | --- | --- | --- |
| GUIDE | `GUIDE.md` | 定义何时读、何时写、何时压缩、何时巩固。 | 只写 policy，不绑定存储目标。 |
| setup | `harness/setup` + host projection | 安装 hooks、protocol skills、dreaming subagent、memory 文件和环境变量。 | 只负责安装，不参与 runtime 判断。 |
| hook | `prime/remind/nudge/compact` | 提供 Host 生命周期时机和短提醒。 | 不承载复杂推理或存储协议。 |
| protocol | `memory_get.md` / `memory_set.md` | 定义在线 Mnemon recall 和在线 `MEMORY.md` 编辑。 | 只有 GUIDE 判断需要时才由 HostAgent 调用。 |
| subagent | `dreaming` | 将 `MEMORY.md` 巩固到 Mnemon，并重写工作记忆。 | 后台或显式维护流程，不是每轮在线行为。 |

## Policy 与 Protocol 分离

`GUIDE.md` 必须保持 storage-agnostic。它用模型友好的语言描述记忆行为：

- 当前是否应该读记忆？
- 当前是否应该写记忆？
- 这条事实是否足够稳定，值得保留？
- 这是长期偏好、项目约定，还是可复用事实？
- 这是否只是 transient transcript，应当忽略？
- 是否应该压缩或巩固工作记忆？

它不要求 HostAgent 判断存储目标是 `MEMORY.md` 还是 Mnemon。

目标映射属于 protocol 资产：

- `memory_get.md` 将读记忆行为映射到 Mnemon recall。
- `memory_set.md` 将写记忆行为映射到 `$MNEMON_MEMORY_LOOP_DIR/MEMORY.md` 编辑。
- `dreaming` 将巩固行为映射到 Mnemon write 加 `MEMORY.md` 压缩或移除。

这个拆分让 GUIDE 能跨不同 HostAgent 复用，也让每个 protocol skill 足够窄、足够可复用。

## 运行流程

### Prime

Prime 是唯一的直接加载路径。

输入：

- `GUIDE.md`
- `MEMORY.md`

动作：

- 将二者注入 HostAgent 的 system prompt。

边界：

- Prime 不调用 `memory_get.md`。
- Prime 不召回 Mnemon。
- Prime 不写长期记忆。

### Remind / Recall

Remind 创造读取长期记忆的机会。

流程：

1. Remind 根据 `GUIDE.md` 提醒 HostAgent 判断是否应该读记忆。
2. 如果需要，HostAgent 加载 `memory_get.md`。
3. `memory_get.md` 说明如何调用 Mnemon recall。
4. Mnemon 返回有界 recall context 给 HostAgent。

边界：

- 长期记忆不会被全量注入。
- recall 结果不会自动写回 `MEMORY.md`。
- `GUIDE.md` 不需要知道 Mnemon 协议细节。

### Nudge / Accumulate

Nudge 创造写工作记忆的机会。

流程：

1. Nudge 根据 `GUIDE.md` 提醒 HostAgent 判断是否应该积累记忆。
2. 如果需要，HostAgent 加载 `memory_set.md`。
3. `memory_set.md` 说明如何新增、替换或删除 `MEMORY.md` 条目。

边界：

- 在线积累只写 `MEMORY.md`。
- 它不直接写 Mnemon。
- 它应避免记录流水账、一次性进度和低置信度观察。

### Compact

Compact 是上下文边界时的 Nudge。

流程：

1. 在上下文压缩前，Compact 提醒 HostAgent 判断是否有重要信息会丢失。
2. 如果需要，HostAgent 加载 `memory_set.md`。
3. `memory_set.md` 将必要的最后补丁写入 `MEMORY.md`。

边界：

- Compact 不是 dreaming。
- Compact 不做全量工作记忆清理。
- Compact 不直接写长期记忆。

### Dreaming

Dreaming 是维护型 subagent，不是普通在线 hook，也不是 protocol skill。

流程：

1. HostAgent 启动专用 dreaming subagent。
2. subagent 读取完整 `MEMORY.md`。
3. subagent 按 Mnemon 协议将耐久信息写入 Mnemon。
4. subagent 压缩、整理或移除 `MEMORY.md` 条目。

可能触发：

- `MEMORY.md` 超过 quota。
- 即将发生上下文压缩。
- 用户或 HostAgent 主动要求。

边界：

- Dreaming 负责巩固与清理。
- 它不替代 Remind、Nudge 或 Compact。
- 它需要保留 prompt-facing 有用性，同时把耐久信息移动到长期记忆。

## 工作记忆规则

`MEMORY.md` 应保持小而模型友好。

适合写入：

- 耐久用户偏好。
- 项目约定。
- 通过重复工作发现的稳定事实。
- 已知坑点及其修复方式。
- 仍然相关的长期目标。

不适合写入：

- 原始对话 transcript。
- 一次性进度。
- 未验证猜测。
- 应写入源码、测试或文档的信息。
- 更适合落入 Mnemon 的大量历史细节。

当 `MEMORY.md` 增长过大时，dreaming 应先把耐久内容写入 Mnemon，再压缩或移除工作记忆条目。

## Setup 预期

第一条具体 setup 路径是 Claude Code，但 layout 应保持 host-agnostic。

Setup 应安装：

- `env.sh`，包括 `MNEMON_MEMORY_LOOP_DIR` 和阈值变量。
- 初始 `MEMORY.md`。
- 最小 `GUIDE.md`。
- Prime、Remind、Nudge、Compact hooks。
- `memory_get.md` 和 `memory_set.md` protocol skills。
- dreaming subagent spec。

Mnemon 本身仍然是独立 binary 和长期存储。Harness 假设它在 recall 或 consolidation 使用前已经安装。

## MVP 范围

MVP 包含：

- Markdown policy 和 protocol 资产。
- Host hook 安装。
- 通过 `MEMORY.md` 进行工作记忆读写。
- 通过 Mnemon 进行长期 recall。
- 通过 dreaming 将信息巩固到 Mnemon。

MVP 不包含：

- 自定义 agent runtime。
- 复杂 adapter framework。
- 多种 working-memory 格式。
- 普通在线 hook 直接写长期记忆。
- always-on daemon。第一版 dreaming 可以手动触发，或由 Host 生命周期边界触发。

## 风险边界

- **过度捕获临时上下文：** 并不是每个看起来有用的任务细节都应该成为记忆。GUIDE 应避免 raw transcript 和低置信度观察进入记忆。
- **敏感数据：** 工作记忆和长期记忆应避免保存 secret、credential 和私有任务内容，除非用户明确要求保留。
- **Recall 污染：** Mnemon recall 应保持有界且相关。长期记忆容量更大，但不是所有存储内容都应被重新加载进 prompt。
- **Dreaming 误整理：** dreaming 在压缩时应保留 prompt-facing 有用性，不应静默删除仍有效的偏好或项目约定。
- **存储边界混淆：** 在线 hooks 写 `MEMORY.md`；耐久 Mnemon write 属于 dreaming。保持这个边界能避免每轮任务都变成长期写入。
- **宿主可移植性：** 短 hooks、Markdown protocol skills 和 spawned subagent 之外的能力，应视为 host-specific setup，而不是基础 contract。

## 循环摘要

```text
Prime 加载 GUIDE + MEMORY.md
Remind 可调用 memory_get -> Mnemon recall
Nudge / Compact 可调用 memory_set -> MEMORY.md patch
Dreaming 将 MEMORY.md 巩固到 Mnemon，并重写 MEMORY.md
```

这个循环是有意非对称的：working memory 模型友好并被 eager load；long-term memory 容量友好，并通过 bounded recall 或 consolidation 访问。
