[< 返回设计概览](../DESIGN.md)

---

# 2. 引擎设计哲学

## 2.1 LLM-Supervised：Binary 是器官，LLM 是监督者

传统的 LLM 记忆系统（如 Mem0、MAGMA 原始实现）在管线内部嵌入一个小型 LLM 来处理记忆操作——实体提取、冲突检测、因果推理。这是 **LLM-Embedded** 模式。

Mnemon 采用 **LLM-Supervised** 模式：

| 模式 | LLM 在哪 | LLM 做什么 | 代表 |
|------|---------|-----------|------|
| **LLM-Embedded** | 管线内部 | 执行者（提取、分类、推理） | Mem0, MAGMA |
| **File Injection** | 会话启动时读取文件 | 无——静态文件加载到 context window | Claude Code CLAUDE.md |
| **MCP Server** | 通过 MCP 协议提供工具 | 将记忆操作暴露为 MCP 工具，供宿主 LLM 调用 | MemCP |
| **LLM-Supervised** | 管线外部 | 监督者（审查候选、做判断、决策取舍） | Mnemon |

在 LLM-Supervised 模式下，职责清晰分为两层：

| 层级 | 角色 | 处理内容 |
|------|------|----------|
| **Binary（器官）** | 确定性运算 | 存储、图索引、关键词搜索、向量计算、衰减公式、自动剪枝 |
| **宿主 LLM（监督者）** | 高价值判断 | 因果链评估、语义相关性判断、实体补充、记忆取舍决策 |

这意味着：

- **零额外 API 成本**：所有计算在本地完成
- **更强的判断能力**：Opus 级别的 LLM 评估候选链接，而非 gpt-4o-mini
- **LLM 可替换**：同一套 Binary + Skill 可在 Claude Code、Cursor、任何 LLM CLI 中使用

当前 engine 遵循更上层的 [Mnemon Harness](../harness/README.md) 立场：hook-native、LLM-led、protocol-constrained，并围绕宿主 agent 模块化挂载。Harness doctrine 与当前 engine architecture 分开维护，这样可以讨论原则，而不默认今天的 binary 就是最终 runtime 形态。

## 2.2 Tools are Organs, Skills are Textbooks

这一哲学可以用游戏开发的类比来理解：

| 游戏开发 | Agent 生态 | Mnemon 对应 |
|---------|-----------|-------------|
| 游戏引擎（Unity/Unreal） | LLM CLI（Claude Code/Cursor） | 宿主环境 |
| 原生插件（C++ Plugin） | Binary 工具 | `mnemon` 二进制 |
| 脚本/蓝图（C#/Blueprint） | Skill（.md 定义） | `SKILL.md` 命令参考 |
| Gameplay 逻辑 | Agent 行为配置 | `guide.md` 执行手册 |

- **Binary = Organ（器官）**——能不能做。封装存储、图遍历、生命周期管理等确定性能力
- **Skill（.md）= Textbook（教材）**——怎么做。教 LLM 何时检索记忆、如何判断去重、怎样调用命令

Binary 封装了所有不需要 LLM 的逻辑，Skill 只教 LLM 做需要智能判断的部分。**记忆管理逻辑从 prompt 变成代码——确定性、可测试、可移植。**

## 2.3 记忆网关：协议而非数据库

大多数 Agent 记忆项目将两个不同的问题混为一体：**如何存储和检索记忆**（存储引擎问题）和**LLM 如何决定何时写入、查询什么、如何解读结果**（交互协议问题）。Mem0 在写入路径中嵌入 LLM 调用——存储逻辑和 LLM 逻辑交织在一起。MemGPT 发明了 OS 式的内存分页，上下文管理策略与存储模型不可分离。OpenViking 构建了自己的虚拟文件系统抽象。每个项目都从头重造 LLM 到数据库的交互层——等同于每个 Web 应用重新发明 HTTP。

**协议栈存在空白。** MCP 标准化了 LLM 发现和调用工具的方式。ODBC/JDBC 标准化了应用访问数据库的方式。但 LLM 如何以记忆语义与数据库交互——这一层没有协议：

```
  LLM
   ↕  MCP (LLM ↔ Tools)         ← 已标准化
  Tools
   ↕  ??? (LLM ↔ Database)      ← 无协议
  Database
   ↕  ODBC/JDBC (App ↔ Database) ← 已标准化
  Storage
```

Mnemon 将这两者视为刻意分离的两层：

![两层架构，刻意解耦](../../diagrams/11-two-layer-architecture.jpg)

两层都承载着真实的价值。**存储引擎**——四图模型、意图自适应 Beam Search、RRF 融合、EI 衰减——是检索质量的来源。**协议面**——CLI 命令、带信号透明度的结构化 JSON 输出、生命周期钩子——定义了任何 LLM 与记忆交互的方式。单独任何一层都不够。

**为什么协议面是这个形状。** 三个核心命令——`remember`、`link`、`recall`——不是任意的 API 设计，而是图构建引擎通用范式 **Extract → Candidate → Associate** 的映射。任何 agent 记忆系统，无论底层存储模型如何，都实现了这三个原语——区别仅在于每一步的显式程度或退化程度。写路径分解为 `remember`（Extract + Candidate）和 `link`（Associate）；读路径为 `recall`（反向的 Extract + Candidate + Associate）。在图结构存储上，这一范式达到最完整的表达，而且关键的是，读写路径是**对称的**：两者遵循同一个三步模型的相反方向，这意味着 LLM 只需掌握一种认知模式即可同时处理读和写操作。

这使 Mnemon 的协议面与 MCP 形成类比：

| 维度 | MCP | Memory Layer Protocol |
|------|-----|-----------------------|
| **解决什么** | LLM 如何发现和调用工具 | LLM 如何以记忆语义读写数据库 |
| **原语数量** | 3（resources / tools / prompts） | 3（remember / link / recall） |
| **后端无关** | 任何工具实现 MCP server | 任何 DB 实现协议 adapter |
| **协议性质** | 发现 + 调用 | 写入 + 关联 + 检索 |

**Agent 侧的可插拔已经实现。** 通过二进制分发 + Skill 文件，上层边界已经解耦。同一个 `mnemon` 二进制附带一份技能定义（`.md`），教会每个宿主 LLM 命令协议。Claude Code 将其作为 Skill 自动发现，Cursor 作为 rules 读取，OpenClaw 作为插件加载——Agent 侧的集成是一份 markdown 文件，不是代码依赖。换 LLM 或换 CLI 框架，不需要改动二进制。

这与 Claude Code 的核心设计洞察一脉相承：**将工程问题与 LLM 问题分离。** Claude Code 不重新发明终端——它让 LLM 通过 bash 操作 Unix 几十年积累的工具链。Mnemon 遵循同样的原则：为记忆图谱构建专用存储引擎，并通过干净的协议边界将其暴露给 LLM。DB 优化归 DB，LLM 交互归协议层。

## 2.4 核心洞察

- **引擎层不需要自己建**——大厂持续优化 LLM 和 CLI 工具，开发者只需引入即用
- **Skill 层边际成本极低**——写 markdown 即可定义 agent 行为，类似游戏蓝图让非程序员也能参与
- **记忆层是唯一需要深耕的部分**——记忆有复利效应，是 agent 从"工具"变成"助手"的分界线
- **LLM 本身就是最好的编排器**——不需要 Python DAG 编排调用链，LLM 读了 Skill 就知道该怎么做
- **存储与协议分离**——记忆如何存储和检索（引擎）与 LLM 如何与之交互（协议）是不同的问题，有不同的优化策略。保持解耦让两侧各自独立演进

## 2.5 理论基础

Mnemon 的设计取用了一篇论文的**范式**和另一篇论文的**方法论**，并在两者之间做出了自己的工程选择。

**RLM Paradigm：LLM as Orchestrator**

[Recursive Language Models](https://arxiv.org/abs/2512.24601) 论文（Zhang, Kraska & Khattab, MIT 2025）建立了一个范式：LLM 作为外部结构化环境的 orchestrator，比直接处理原始数据更有效。论文在范式层的关键发现：

- 一个 8B 模型通过将数据视为外部环境变量，可以处理超出 context window **100 倍**的输入
- **两阶段 pipeline**（fast filtering + LLM semantic verification）持续优于单遍处理方案
- 向模型传递 **constant-size metadata** — 而非原始数据 — 效果更好

RLM 论文自身的实现采用**代码生成 + Python REPL** 作为交互机制：LLM 写 Python 代码，sandbox 执行，结果反馈。Mnemon 共享这一范式，但在协议层走了不同的路径（见下文）。

**RLM 推论：为什么记忆协议必须是 Intent-Native 的**

上述三个 RLM 发现不仅关乎 LLM 能力——它们约束了记忆协议应有的形态。如果 LLM 是 orchestrator，协议就必须在 orchestrator 的层面说话：**intent 和语义**，而非机制和语法。

| RLM 发现 | 对协议的约束 | 它解释的反模式 |
|---------|------------|--------------|
| LLM 做 orchestrator 而非 data processor | 协议应让 LLM 表达*需要什么*（intent），而非*怎么取*（mechanism） | 嵌入 LLM 做 entity 抽取，是将 orchestrator 降级为 data processor |
| 定长元数据优于原始数据 | 协议输出应是带 signal transparency 的语义摘要，而非数据库行 | 返回原始查询结果的系统迫使 LLM 从数据中重新推导含义 |
| 两阶段优于单遍 | 确定性过滤和 LLM 判断必须分离为不同阶段 | 在嵌入的 LLM 调用中混合两者，正是 RLM 证伪的单遍模式 |

许多现有项目在记忆 pipeline 中嵌入 LLM 调用——用于 entity 抽取、冲突检测、因果推理。这揭示了一个诊断模式：**当协议无法表达语义意图时，系统就通过注入 LLM 来弥补差距。** 嵌入的 LLM 同时在做两件事：**语义补偿**（协议缺乏表达力，LLM 在 intent 和 mechanism 之间翻译）和**智能判断**（确实需要 LLM 推理）。Mnemon 将这两个关注点分离：提升协议的表达力来处理前者，将后者委托给宿主 LLM 作为 supervisor。

RLM 自身的实现选择提供了间接支撑。论文选择代码生成 + Python REPL，是因为结构化数据交互领域不存在领域专用的语义协议——Python 是通用退路。但对于记忆领域，代码生成过度通用：LLM 必须将 intent（"找出因果相关的记忆"）翻译成 Python 代码（`graph.query(type='causal', ...)`），引入了一个既是信息损失点、又是出错面的翻译步骤。领域专用协议消除了这层翻译：

```
代码生成（RLM）：     intent → Python 代码 → 执行 → 结果 → 解读
语义协议（Mnemon）：  intent → mnemon recall "..." --intent causal → 结果
```

LLM intent 与系统动作之间的翻译步骤越少，交互越忠实。这就是协议表面使用 `remember` 而非 INSERT、`link` 而非 CREATE EDGE、`recall` 而非 SELECT 的原因——**命令名是语义化的，不是语法化的**，直接映射到 LLM 的认知词汇而非数据库的操作词汇。

**MAGMA Methodology：Four-Graph Memory Architecture**

[MAGMA](https://arxiv.org/abs/2601.03236) 论文提供了**外部环境应包含什么**的具体方法论。其核心贡献：单一的 edge type（如 vector similarity）不足以支撑记忆系统 — 不同 query intent 需要不同的关系视角。MAGMA 的四图架构（temporal、entity、causal、semantic）加上 intent-adaptive retrieval 和 multi-signal fusion，为 Mnemon 提供了 data model 和 retrieval algorithm。

**Graph-LLM 结构洞察：为什么是这个协议形状**

图数据模型与 LLM 的信息组织方式是结构同构的。LLM 注意力、图数据模型和自然语言描述的是同一件事——实体间的带权关联：

```
LLM 注意力：     token ←weight→ token
图数据模型：      node  ←edge→   node
自然语言：       主语  ←谓语→    宾语
```

这不是比喻。Transformers-as-GNNs 文献（arXiv 2506.22084, 2012.09699）已正式证明 transformer 注意力在计算上等价于 GNN 在完全图上的操作。Mnemon 将这一洞察从计算层面延伸到存储层面：**如果 LLM 内部就是图操作，那么外部记忆用图来存储就是结构匹配，而非工程便利。**

其他存储类型是图的退化形式——每种都丢失了一个维度的关系语义：

| 存储类型 | 相比图丢失了什么 |
|---------|----------------|
| **KV** | 孤立节点，零边 |
| **关系型** | 边被压缩为外键，类型固定在 schema 设计时 |
| **文档型** | 边被内联为嵌套结构，失去全局可遍历性 |
| **向量** | 所有边都是同一种类型（相似度），无语义区分 |

向量数据库能回答"什么跟什么**像**"，但不能回答"什么**导致**什么"或"什么**属于**什么"。这一观察与 Graph-based Agent Memory 综述（Chang Yang 等，arXiv 2602.05665，2026.02）独立得出的结论一致："传统记忆形式可以被视为图记忆范式中的退化或简化案例"。

该分析产生了两个直接塑造 Mnemon 协议的结论：

1. **通用代数**：`remember`（Extract）、`link`（Associate）、`recall`（Retrieve）是任何 agent 记忆系统的最小完备接口。从原生 RAG 到 OpenViking 到 Mem0，所有系统都是这三个原语的不同实例化，`link` 的退化程度各异。`link` 操作越退化，LLM 在召回时推断未存储关联的负担就越重。将 `link` 分离为一等原语——而非将其折叠进写入或读取路径——是现有框架中未见的贡献（对比 CoALA 的 retrieval/reasoning/learning，或标准 CRUD API）。
2. **读写对称性**：在图结构存储上，写路径（text → graph）和读路径（graph → text）遵循同一个 Extract → Candidate → Associate 模型。这意味着 LLM 只需掌握一种认知模式即可同时处理 `remember` 和 `recall`——这一性质在关系型或文档型数据库上不成立。

完整分析（包括跨系统验证和退化谱系）见 [Graph-LLM 理论基础](04-graph-model.md#graph-llm-理论基础)。

**Mnemon's Own Contribution：The Engineering Bridge**

以上理论来源都未解决如何在生产环境中将 LLM orchestrator 与 graph-structured memory 连接起来。Mnemon 填补了这一空白：

| 层级 | 来源 | 选择 |
|---|---|---|
| **Paradigm** — 谁来编排？ | RLM | 宿主 LLM，而非 embedded model |
| **Protocol semantics** — 为什么 intent-native？ | RLM | LLM 表达 intent 而非 mechanism；两阶段验证了这一分离 |
| **Methodology** — 环境里放什么？ | MAGMA | 四图架构 + intent-adaptive retrieval |
| **Protocol algebra** — 为什么是这个形状？ | Graph-LLM Insight | remember/link/recall 作为通用原语；读写对称性 |
| **Protocol** — 如何通信？ | Mnemon | CLI 命令 + structured JSON（非代码生成） |
| **Lifecycle** — 记忆如何演化？ | Mnemon | Hook-driven remember → diff → link → gc |
| **Distribution** — 如何分发？ | Mnemon | 单一 Go binary，零依赖 |

RLM 的实现依赖 sandboxed REPL 中的代码生成（灵活但需要 runtime 且有安全顾虑），Mnemon 用确定性的 CLI 命令作为 symbolic interface — 受限，但可审计、可移植、零 sandbox。MAGMA 的参考实现是 Python library + 内存中的 NetworkX 图，Mnemon 将一切持久化到 SQLite 并提供完整的 write-back lifecycle。

最终结果是：**RLM 的范式 + MAGMA 的方法论 + CLI-native 的工程路径** — 无需 Python，无需 sandbox，无需 API key，可运行在任何 LLM CLI 上。

![LLM-Supervised Architecture](../../diagrams/05-llm-supervised.jpg)

![System Architecture](../../diagrams/01-system-architecture.jpg)
