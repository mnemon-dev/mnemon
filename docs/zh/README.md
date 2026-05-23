<p align="center">
  <img src="../logo/logo.svg" width="160" height="160" alt="Mnemon Logo" />
</p>

# Mnemon

[English](../../README.md) | **中文**

**LLM 智能体的持久记忆系统** — LLM 监督式、钩子集成、四图架构。

[![Go 1.24+](https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![CI](https://github.com/mnemon-dev/mnemon/actions/workflows/ci.yml/badge.svg)](https://github.com/mnemon-dev/mnemon/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/mnemon-dev/mnemon)](https://goreportcard.com/report/github.com/mnemon-dev/mnemon)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](../../LICENSE)

---

LLM 智能体在会话之间会遗忘一切。上下文压缩丢失关键决策，跨会话知识消失，长对话将早期信息推出窗口。

Mnemon 为你的 LLM 提供持久的跨会话记忆 — 四图知识存储、意图感知检索、重要度衰减、自动去重。单一二进制，零 API 密钥，一条命令完成部署。

在更大的 harness 方向上，Mnemon 是给已有 agent 使用的事件溯源生命周期层。它不
替换 Codex、Claude Code、OpenClaw 或未来宿主，而是在它们外围增加可治理的
memory、skill、eval、proposal 和 audit 生命周期。

> **Claude Max / Pro 订阅用户？** Mnemon 完全通过你现有的订阅运作——不需要额外的 API 密钥。你的 LLM 订阅*本身*就是智能层。两条命令即可完成。

### 为什么选择 Mnemon？

多数记忆工具在管线内嵌入自己的 LLM。Mnemon 采用不同路线：**你的宿主 LLM 就是监督者。** 二进制处理确定性计算（存储、图索引、搜索、衰减）；LLM 做判断（记什么、怎么关联、何时遗忘）。没有中间人，没有额外推理开销。

| 模式 | LLM 角色 | 代表项目 |
|---|---|---|
| **LLM-Embedded** | 管线内部的执行者 | Mem0, Letta |
| **File Injection** | 无 — 会话启动时读取文件 | Claude Code Memory |
| **MCP Server** | 通过 MCP 协议提供工具 | claude-mem |
| **LLM-Supervised** | 独立二进制的外部监督者 | **Mnemon** |

Mnemon 同时填补了协议栈中的空白。MCP 标准化了 LLM 如何发现和调用工具，ODBC/JDBC 标准化了应用如何访问数据库，但 LLM 以记忆语义与数据库交互——这一层尚无协议。Mnemon 的三个原语——`remember`、`link`、`recall`——构成一个意图原生协议：命令名称映射到 LLM 的认知词汇（`remember` 而非 INSERT，`recall` 而非 SELECT），输出是带有信号透明度的结构化 JSON，而非原始数据库行。

<p align="center">
  <img src="../diagrams/llm-supervised-concept.jpg" width="720" alt="LLM 监督式架构 — 三种模式对比，及 Mnemon 钩子、协议边界和确定性记忆引擎" />
  <br />
  <sub>LLM 监督式模式：钩子驱动生命周期，宿主 LLM 做判断，二进制处理确定性计算。</sub>
</p>

记忆具有**复利效应** — 积累越久，价值越大。LLM 引擎不断迭代，技能文件几乎零成本编写，但记忆是随用户一起增长的私有资产。它是智能体生态中唯一值得深度投入的组件。

<p align="center">
  <img src="../diagrams/10-knowledge-graph.jpg" width="720" alt="知识图谱 — 87 条洞察通过时序、实体、语义和因果边连接" />
  <br />
  <sub>Mnemon 构建的真实知识图谱 — 87 条洞察，2150 条边，横跨四种图类型。</sub>
</p>

详见 [设计与架构](DESIGN.md)。

## 快速开始

### 安装

**Homebrew**（macOS / Linux）：

```bash
brew install mnemon-dev/tap/mnemon
```

**Go install**：

```bash
go install github.com/mnemon-dev/mnemon@latest
```

**从源码构建**：

```bash
git clone https://github.com/mnemon-dev/mnemon.git && cd mnemon
make install
```

**验证安装**：

```bash
mnemon --version
```

### Claude Code

```bash
mnemon setup
```

`mnemon setup` 自动检测 Claude Code，交互式部署技能文件、钩子和行为引导。启动新会话 — 记忆自动运作。

### [OpenClaw](https://github.com/openclaw/openclaw)

```bash
mnemon setup --target openclaw --yes
```

一条命令将技能文件、钩子、插件和行为引导部署到 `~/.openclaw/`。重启 OpenClaw 网关即可激活。

### [NanoClaw](https://github.com/qwibitai/nanoclaw)

NanoClaw 在 Linux 容器内运行智能体。使用 `/add-mnemon` 技能集成：

1. 在宿主机安装 mnemon（见上方）
2. 在 NanoClaw 项目中运行 `/add-mnemon` — Claude Code 将修改 Dockerfile、添加容器技能、配置卷挂载
3. 每个 WhatsApp 群组获得独立的记忆存储，可选全局共享记忆（只读）

技能文件位于 NanoClaw 仓库的 `.claude/skills/add-mnemon/` 目录。

### 卸载

```bash
mnemon setup --eject
```

## 工作原理

设置完成后，记忆通过轻量 harness 运作：`SKILL.md` 教命令，`GUIDELINE.md` 教判断，hook 在生命周期边界提醒，`mnemon` binary 执行确定性记忆操作。已支持的 setup 命令可以自动化这些步骤，但 harness 本身仅靠 Markdown 也可安装。

```text
会话启动
    |
    v
  Prime   -> 让 skill、guideline 和当前 store 可见
    |
    v
用户 prompt 到达
    |
    v
  Remind  -> 判断 recall 是否可能改变当前任务
    |
    v
Agent 工作，并且只在有用时调用 Mnemon
    |
    v
  Nudge   -> 判断 durable writeback 是否有正当性
    |
    v
上下文压缩前
    |
    v
  Compact -> 只保存关键连续性
```

四个 hook phase 是提醒，不是硬 workflow。**Prime** 让 skill、guideline 和当前 store 可见。**Remind** 触发 recall 判断。**Nudge** 触发 writeback 判断。**Compact** 在上下文压缩前只保留关键连续性。

你不需要自己运行 mnemon 命令。Agent 会在 guideline 判断 memory 有用时执行。

## 特性

- **零用户操作** — 安装一次；支持 hook 的 runtime 可用 hook，minimal runtime 可用持久规则
- **LLM 监督式** — 宿主 LLM 主动决定记什么、更新什么、遗忘什么；无内嵌 LLM，无 API 密钥
- **Markdown 可安装 harness** — `SKILL.md`、`INSTALL.md`、`GUIDELINE.md` 和四个生命周期提醒
- **四图架构** — 时序、实体、因果、语义四种边，不仅仅是向量相似度
- **意图原生协议** — 三个原语（`remember`、`link`、`recall`）映射到 LLM 的认知词汇而非数据库语法；结构化 JSON 输出，带信号透明度
- **意图感知召回** — 图遍历 + 可选向量搜索（RRF 融合），所有查询默认启用
- **内置去重** — `remember` 自动检测重复和冲突；跳过或自动替换
- **保留度生命周期** — 重要性衰减、访问计数提升、免疫规则、垃圾回收
- **可选嵌入向量** — 本地 [Ollama](https://ollama.ai) 集成，支持混合向量+关键词搜索

## 愿景

所有本地 AI 智能体 — 跨会话、跨框架 — 共享一个活跃的记忆池。

```
  Claude Code ──┐
                │
  OpenClaw ─────┤
                │
  NanoClaw ─────┤
                ├──▶  ~/.mnemon  ◀── 共享记忆
  OpenCode ─────┤
                │
  Gemini CLI ───┘
```

基础已就绪：一个 `~/.mnemon` 数据库，任何 agent 都可以读写。Claude Code setup 可自动安装 hook；OpenClaw 可以使用 plugin hooks；NanoClaw 通过容器技能和卷挂载集成。同一个 harness 可以安装到任何支持 skill、rule、system prompt 或 event hook 的 LLM CLI。

更长远的方向是**记忆网关**：协议层与存储引擎解耦。当前 SQLite 后端是第一个适配器；协议面（`remember / link / recall`）可运行在 PostgreSQL、Neo4j 或任何图数据库之上。Agent 侧优化（何时召回、记什么）与存储侧优化（索引、图算法）独立演进。详见[未来方向](design/08-decisions.md#82-未来方向)。

## 常见问题

**不同会话共享记忆吗？**
是的。默认情况下，所有会话使用同一个 `default` 记忆体 — 一个会话中记住的决策在所有未来会话中可用。

**能否按项目或 agent 隔离记忆？**
可以。使用命名记忆体（store）隔离数据：

```bash
mnemon store create work        # 创建新记忆体
mnemon store set work           # 设为默认
MNEMON_STORE=work mnemon recall "query"  # 或按进程使用环境变量
```

不同 agent/进程可通过 `MNEMON_STORE` 环境变量使用不同的记忆体 — 无全局状态竞争。

**本地模式还是全局模式？**
`mnemon setup` 默认**本地**（项目级 `.claude/`），适合大多数用户。**全局**（`mnemon setup --global`，安装到 `~/.claude/`）在所有项目中激活 mnemon — 如果想让其他框架（如 OpenClaw）通过 Claude Code CLI 共享记忆很方便，但可能增加维护开销。

**如何自定义行为？**
编辑当前 setup 流程生成的 guideline（`~/.mnemon/prompt/guide.md`），或以可安装的 [memory loop GUIDE](../../harness/loops/memory/GUIDE.md) 作为来源。Skill 文件应专注于命令语法。

**什么是 Sub-agent 委派？**
Sub-agent 委派是可选执行策略。当 runtime 支持时，主 agent 可以决定*记什么*，再让更便宜或隔离的 worker 执行 `mnemon remember`。它有用，但不是 Mnemon 架构必需品。

## 配置

| 环境变量 | 默认值 | 说明 |
|---------|-------|------|
| `MNEMON_DATA_DIR` | `~/.mnemon` | 基础数据目录 |
| `MNEMON_STORE` | *（active 文件或 `default`）* | 命名记忆体，用于数据隔离 |
| `MNEMON_EMBED_ENDPOINT` | `http://localhost:11434` | Ollama API 端点 |
| `MNEMON_EMBED_MODEL` | `nomic-embed-text` | 嵌入模型名称 |

也可在命令上使用 `--data-dir` 或 `--store` 标志覆盖。

## 开发

```bash
make build          # 构建二进制
make install        # 构建 + 安装到 $GOBIN
make test           # 运行 E2E 测试套件
mnemon setup        # 交互式设置（检测环境 + 部署钩子/技能/引导）
mnemon setup --eject  # 移除所有集成
make help           # 显示所有目标
```

**依赖**：Go 1.24+、`modernc.org/sqlite`、`spf13/cobra`、`google/uuid`

**可选**：[Ollama](https://ollama.ai) + `nomic-embed-text` 嵌入支持

## 文档

- [Modular Self-Evolution Harness](harness/README.md) — modular agent、memory loop 与 skill loop 的正式 harness 文档
- [Memory Loop Harness](../../harness/loops/memory/README.md) — 可安装 memory loop 资产
- [Skill Loop Harness](../../harness/loops/skill/README.md) — 可安装 skill loop 资产
- [设计与架构](DESIGN.md) — 当前 engine architecture、核心概念、算法、集成设计
- [用法与参考](USAGE.md) — CLI 命令、嵌入向量支持、架构概览
- [架构图](../diagrams/) — 系统架构、记忆/召回流程、四图模型、生命周期管理

## 参考文献

Mnemon 取用了一篇论文的范式和另一篇论文的方法论，并基于图记忆与 LLM 注意力同构这一结构洞察。详见[理论基础](DESIGN.md#25-理论基础)。

- **RLM** — Zhang, Kraska & Khattab. [Recursive Language Models](https://arxiv.org/abs/2512.24601). 2025. 建立范式：LLM 作为外部环境的 orchestrator 比直接处理数据更有效。
- **MAGMA** — Zou et al. [A Multi-Graph based Agentic Memory Architecture](https://arxiv.org/abs/2601.03236). 2025. 提供方法论：四图模型（temporal、entity、causal、semantic）+ intent-adaptive retrieval。
- **Graph-LLM 结构洞察** — Joshi & Zhu. [Building Powerful GNNs from Transformers](https://arxiv.org/abs/2506.22084). 2025；及图智能体记忆综述（Chang Yang et al., 2026）。证实 LLM 注意力机制在计算上等价于 GNN 操作——图记忆是结构性匹配，而非工程便利。

## 许可证

[Apache-2.0](../../LICENSE)
