# Mnemon — Design & Architecture

> **Mnemon**（/ˈniːmɒn/），源自古希腊语 μνήμων（mnḗmōn），由 μνάομαι（"铭记"）与施事后缀 -μων 构成，意为"铭记者、善于记忆之人"。荷马在《奥德赛》中以 "καὶ γὰρ μνήμων εἰμί"（我记得很清楚）描述这一特质。在古希腊城邦制度中，Mnemones 是专职的记录官员，在财产交易与法律程序中承担见证与存档职责，是口述传统向书面记录过渡时期的制度性记忆载体。
>
> 该词同源于记忆女神 Mnemosyne（Μνημοσύνη）——宙斯与她结合诞生了九位缪斯，象征记忆是一切知识与创造的源泉。

Mnemon 是一个为 LLM agent 设计的持久化记忆系统。它采用 **LLM-Supervised** 模式：宿主 LLM 作为独立记忆 Binary 的外部编排者，通过符号化 CLI 接口交互，而 Binary 负责确定性的存储、图索引和生命周期管理。记忆以四图知识结构组织 — temporal、entity、causal、semantic 四种 edge。以单一 Go binary + SQLite 的形式实现，不依赖任何外部 API。

本文档描述当前 Mnemon binary 与 engine architecture。正式 modular self-evolution harness 文档见 [Mnemon Harness](harness/README.md)，可安装 runtime 资产位于仓库根目录的 [harness](../../harness/) 目录。

---

## 目录

### [1. 愿景与问题](design/01-vision.md)

Mnemon 存在的原因 — LLM agent 的失忆问题、传统方案的结构性瓶颈，以及与现有方案（Mem0、MemGPT、Claude Code Memory）的对比。

### [2. 引擎设计哲学](design/02-philosophy.md)

当前 engine 的 LLM-Supervised 模式、Hook-native / LLM-led / Protocol-constrained 原则、器官 vs 教科书隐喻、记忆网关协议（LLM↔DB 交互的 MCP 类比）、关键设计洞察，以及 RLM、MAGMA 和 Graph-LLM 结构分析的理论基础。

### [3. 核心概念与架构](design/03-concepts.md)

Insight/Edge 数据模型、数据库 Schema（SQLite WAL）、系统架构（CLI 层 → 引擎 → 存储）、代码结构，以及通过命名 Store 实现的数据隔离。

### [4. 图模型与结构理论](design/04-graph-model.md)

MAGMA 四图模型（temporal、entity、causal、semantic），LLM 注意力与图存储之间的结构同构，Extract→Candidate→Associate 范式，读写对称性，`remember/link/recall` 作为通用代数，LLM↔DB 协议缺口，以及学术定位。

### [5. 读写管线](design/05-pipelines.md)

写入管线（`remember` 内置 diff）、读取管线（Smart Recall：意图检测、RRF 锚点融合、Beam Search 遍历、多因子重排序），以及去重/冲突检测。

### [6. 生命周期与嵌入向量](design/06-lifecycle.md)

有效重要性（EI）衰减公式、豁免规则、自动清理、GC 命令，以及通过 Ollama（nomic-embed-text）实现的可选嵌入向量支持。

### [7. LLM CLI 集成](design/07-integration.md)

Markdown 可安装的 runtime 集成：`SKILL.md`、`INSTALL.md`、`GUIDELINE.md`、四个 hook phase（Prime、Remind、Nudge、Compact）、agent 主导的记忆判断、可选 setup 自动化，以及轻量 Markdown 自进化。

### [Self-Evolution Harness](harness/README.md)

正式 modular harness 文档，覆盖 agent-agnostic 安装挂载、memory loop、skill loop 与未来可外挂 evolution modules。

### [8. 设计决策与未来方向](design/08-decisions.md)

关键权衡（LLM-Supervised vs 嵌入式、SQLite WAL vs 图数据库、Beam Search vs BFS、软删除）、与 MAGMA 论文的偏差、存储侧可插拔性路线图，以及迈向记忆网关的愿景。
