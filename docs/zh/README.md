<p align="center">
  <img src="../logo/logo.svg" width="160" height="160" alt="Mnemon Logo" />
</p>

<h1 align="center">Mnemon</h1>

<p align="center"><a href="../../README.md">English</a> · <strong>中文</strong></p>

<p align="center">
  <a href="https://www.npmjs.com/package/@mnemon-dev/mnemon"><img alt="npm 版本" src="https://img.shields.io/npm/v/@mnemon-dev/mnemon?label=npm" /></a>
  <a href="https://github.com/mnemon-dev/mnemon/releases/latest"><img alt="GitHub 发布版本" src="https://img.shields.io/github/v/release/mnemon-dev/mnemon" /></a>
  <a href="https://github.com/mnemon-dev/mnemon/stargazers"><img alt="GitHub 收藏数" src="https://img.shields.io/github/stars/mnemon-dev/mnemon?label=stars" /></a>
  <a href="https://go.dev/"><img alt="Go 1.24+" src="https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go&amp;logoColor=white" /></a>
  <a href="https://github.com/mnemon-dev/mnemon/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/mnemon-dev/mnemon/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="../../LICENSE"><img alt="许可证：Apache-2.0" src="https://img.shields.io/badge/License-Apache--2.0-blue.svg" /></a>
</p>

<p align="center"><strong>LLM 智能体的持久记忆系统</strong> — LLM 监督式、钩子集成、四图架构。</p>

---

LLM 智能体在会话之间会遗忘一切。上下文压缩丢失关键决策，跨会话知识消失，长对话将早期信息推出窗口。

Mnemon 为你的 LLM 提供持久的跨会话记忆 — 四图知识存储、意图感知检索、重要度衰减、自动去重。`mnemon` 记忆路径仍是一个本地二进制，零 API 密钥，一条命令完成部署。

Mnemon 只发布一个 `mnemon` 可执行文件，同时提供两套相互独立的能力：根级
Memory 命令保存跨会话知识；Preview 阶段的 `mnemon agency ...` 为项目内 Agent
提供持久、受约束的协作状态。Agency 以 Pi 为首个 Runtime 集成，详情见
[Agency 指南](AGENCY.md)。

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

**npm**（推荐；macOS / Linux / Windows，需要 Node.js 22+）：

```bash
npm install --global @mnemon-dev/mnemon
```

之后可随时升级 npm 管理的 CLI：

```bash
mnemon update
```

npm 包会按宿主操作系统和 CPU 安装对应的原生 Go 可执行文件。Mnemon 引擎
仍然是单一原生二进制；Node.js 只用于 npm 启动器和包管理。

**其他安装方式**：

```bash
brew install --cask mnemon-dev/tap/mnemon
go install github.com/mnemon-dev/mnemon@latest
```

Homebrew、`go install`、源码构建及其他 Node 包管理器安装的版本，必须继续使用
各自原来的安装方式。迁移时请先执行一次 npm 安装命令，并确保 npm 全局 bin 目录
在 `PATH` 中排在旧可执行文件之前；此后的 `mnemon update` 将由 npm 管理。

Windows 支持核心 Memory 命令。Agency 的本地权威边界完成原生 Windows
安全实现前，在 Windows 上保持不可用。

**从源码构建**（macOS / Linux）：

```bash
git clone https://github.com/mnemon-dev/mnemon.git && cd mnemon
make install
```

**验证安装**：

```bash
mnemon --version
mnemon agency --version
```

### Agency（Preview · Pi-first）

```bash
mnemon agency setup --runtime pi --project-root .
```

每个项目设置一次，之后照常使用 Pi。Agency 支持 macOS 和 Linux，并与
Memory 保持独立：`mnemon setup --target pi --yes` 启用 Memory，以上命令启用
Agency。当前成熟度与兼容边界、工作方式及可选 peer 配置见
[Agency 指南](AGENCY.md)。

### [Claude Code](https://github.com/anthropics/claude-code)

```bash
mnemon setup
```

`mnemon setup` 自动检测 Claude Code，交互式部署技能文件、钩子和行为引导。启动新会话 — 记忆自动运作。

### [ZCode](https://zcode.z.ai/)

```bash
mnemon setup --target zcode --global --yes
```

ZCode 会将 Mnemon skill 安装到 `~/.zcode/skills/`，并在
`~/.zcode/cli/config.json` 中注册用户级生命周期 hooks。新会话会注入记忆状态，
模型调用前会收到 recall 指引，停止时会评估 durable-memory 回写。不加
`--global` 时只安装项目 skill，因为 ZCode 当前不会执行项目级 hooks 配置。

### [MiniMax Code](https://github.com/MiniMax-AI/minimax-code)

```bash
mnemon setup --target minimax-code --yes
```

一条命令将 Mnemon skill 安装到 `.minimax/skills/mnemon/SKILL.md`。添加
`--global` 后会安装到 `~/.minimax/skills/mnemon/SKILL.md`，供所有项目使用。
当前 MiniMax Code 会原生发现这两个目录。该集成刻意保持 skill-only：在
MiniMax Code 3.0.65 中，本地 Agent V2 路径不会触发可靠自动 recall 所需的
用户提示生命周期 hook。

### [TRAE](https://www.trae.ai/) (TRAE Work)

```bash
mnemon setup --target trae --yes
```

一条命令将 mnemon skill、prompt 文件和 TRAE 原生 hooks 部署到 `.trae/`，
同时覆盖 TRAE IDE 和 TRAE Work。该集成使用 `.trae/hooks.json` 中的
`SessionStart`、`UserPromptSubmit` 和 `Stop` hooks。

### [Qoder](https://qoder.com/) (QoderWork)

```bash
mnemon setup --target qoder --yes
mnemon setup --target qoderwork --yes
```

Qoder 会将 mnemon skill、prompt 文件和原生 hooks 部署到 `.qoder/` 或
`~/.qoder/`。QoderWork 使用原生用户级配置 `~/.qoderwork/`。两者都会在
`settings.json` 中注册 `SessionStart`、`UserPromptSubmit` 和 `Stop` hooks。

### [CodeBuddy](https://www.codebuddy.cn/)

```bash
mnemon setup --target codebuddy --yes
```

CodeBuddy 会将 mnemon skill、prompt 文件和原生 hooks 部署到 `.codebuddy/`
或 `~/.codebuddy/`。该集成会在 `settings.json` 中注册 `SessionStart`、
`UserPromptSubmit` 和 `Stop` hooks。

### [WorkBuddy](https://www.codebuddy.cn/work/)

```bash
mnemon setup --target workbuddy --yes
```

WorkBuddy 会将 mnemon skill、prompt 文件和原生 hooks 部署到 `.workbuddy/`
或 `~/.workbuddy/`。该集成会在 `settings.json` 中注册 `SessionStart`、
`UserPromptSubmit` 和 `Stop` hooks。

### [Kimi Code](https://github.com/MoonshotAI/kimi-code)

```bash
mnemon setup --target kimi --yes
```

Kimi Code 会将 mnemon skill、prompt 文件和原生生命周期 hooks 部署到
`~/.kimi-code/` 或 `$KIMI_CODE_HOME/`。该集成会在 `config.toml` 中注册
`SessionStart`、`UserPromptSubmit` 和 `Stop` hooks。

### [OpenCode](https://opencode.ai/)

```bash
mnemon setup --target opencode --yes
```

OpenCode 会将 mnemon skill 部署到 `.opencode/skills/`，通过
`opencode.json` 的 `instructions` 注册生成的 guide，并在
`.opencode/plugins/` 安装原生 plugin。该 plugin 会在聊天请求前注入
recall context，并在 session compaction 中加入 Mnemon guidance。

### [OpenClaw](https://github.com/openclaw/openclaw)

```bash
mnemon setup --target openclaw --yes
```

一条命令将技能文件、钩子、插件和行为引导部署到 `~/.openclaw/`。重启 OpenClaw 网关即可激活。

### [Pi](https://pi.dev)

```bash
mnemon setup --target pi --yes
```

一条命令将 mnemon skill、prompt 文件和 Pi TypeScript extension 部署到
`.pi/`。这个 extension 会把 Mnemon 的 lifecycle reminder 映射到 Pi 事件
（`resources_discover`、`before_agent_start`、`agent_end`、
`session_before_compact`）。启动新的 Pi session 或运行 `/reload` 即可激活。

### [Hermes Agent](https://github.com/NousResearch/hermes-agent)

```bash
mnemon setup --target hermes --yes
```

一条命令将 mnemon skill、prompt 文件和 Hermes shell hooks 部署到
`~/.hermes/`。该集成使用 Hermes 原生生命周期 hooks：
`on_session_start`、`pre_llm_call`、`post_llm_call`，以及可选的
`on_session_finalize`。Hermes 可能会在首次运行时提示批准这些 shell hooks。

### [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness)

DeepSeek Harness（DSH）通过 [dsh-mnemon](https://github.com/omdsh-dev/dsh-mnemon) 插件集成：该插件把 DSH 的运行时热记忆、受管项目档案与 Mnemon 长期记忆体组织成一套受监督的三层记忆系统。

宿主机安装好 `mnemon`（见[安装](#安装)）后，安装插件并重启 DSH Web profile：

```bash
dsh plugin --profile web add dsh-mnemon
dsh --profile web
```

Mnemon 主仓库也可以直接作为 GitHub 安装源。未发布到 npm 的插件版本仍可从
独立仓库安装；本地开发检出使用绝对路径：

```bash
dsh plugin --profile web add github:mnemon-dev/mnemon
dsh plugin --profile web add "github:omdsh-dev/dsh-mnemon"
dsh plugin --profile web add "link:/absolute/path/to/dsh-mnemon"
```

从 Mnemon 主仓库执行全新安装时，会解析 npm 上标记为 `latest` 的
`dsh-mnemon` 版本，因此发布新版插件后无需同步修改本仓库。已有安装仍会保留
此前解析到的版本，直至重新安装或更新插件。

然后在 DSH 的「设置 → 插件配置 → Mnemon」选择存储范围，并在会话的「记忆系统」Tab 创建或激活记忆体。召回只读取已激活的记忆体，持久写入经由受监督子 Agent 执行。

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

设置完成后，Memory 通过轻量的 runtime 投影运作：各 runtime 的 `SKILL.md`
教授命令，共享的 `guide.md`（默认位于 `~/.mnemon/prompt/guide.md`）提供判断
指引，原生 hook 或 extension 在支持的生命周期边界给出提醒。`mnemon` binary 执行确定性记忆操作，
`mnemon setup` 则为每个受支持的 runtime 安装最接近其原生机制的映射。

```text
会话启动
    |
    v
  Prime   -> 让 skill、guide 和当前 store 可见
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

四个 hook phase 是提醒，不是硬 workflow。**Prime** 让 skill、guide 和当前
store 可见。**Remind** 触发 recall 判断。**Nudge** 触发 writeback 判断。
**Compact** 在上下文压缩前只保留关键连续性。

你不需要自己运行 mnemon 命令。Agent 会在 guide 判断 memory 有用时执行。

## 特性

- **零用户操作** — 安装一次；支持 hook 的 runtime 可用 hook，minimal runtime 可用持久规则
- **LLM 监督式** — 宿主 LLM 主动决定记什么、更新什么、遗忘什么；无内嵌 LLM，无 API 密钥
- **多框架支持** — Claude Code、Codex、Cursor、ZCode、TRAE/TRAE Work、Qoder/QoderWork、CodeBuddy、WorkBuddy、Kimi Code、OpenCode 和 Hermes Agent（hooks/plugins）、OpenClaw（plugins）、Pi（extensions）、MiniMax Code 和 Nanobot（skills）、DeepSeek Harness（通过 dsh-mnemon 插件）等
- **Runtime 原生集成** — 各 runtime 的 `SKILL.md`、共享 `guide.md`，以及受支持的 hook 或 extension
- **四图架构** — 时序、实体、因果、语义四种边，不仅仅是向量相似度
- **意图原生协议** — 三个原语（`remember`、`link`、`recall`）映射到 LLM 的认知词汇而非数据库语法；结构化 JSON 输出，带信号透明度
- **意图感知召回** — 图遍历 + 可选向量搜索（RRF 融合），所有查询默认启用
- **内置去重** — `remember` 自动检测重复和冲突；跳过或自动替换
- **保留度生命周期** — 重要性衰减、访问计数提升、免疫规则、垃圾回收
- **可选嵌入向量** — 可使用本地 [Ollama](https://ollama.ai) 或 OpenAI 兼容服务器，支持混合向量+关键词搜索

## 愿景

所有本地 AI 智能体 — 跨会话、跨框架 — 共享一个活跃的记忆池。

```
  Claude Code ───────┐
                     │
  Codex ─────────────┤
                     │
  Cursor ────────────┤
                     │
  ZCode ─────────────┤
                     │
  MiniMax Code ──────┤
                     │
  TRAE ──────────────┤
                     │
  TRAE Work ─────────┤
                     │
  Qoder ─────────────┤
                     │
  QoderWork ─────────┤
                     │
  CodeBuddy ─────────┤
                     │
  WorkBuddy ─────────┤
                     │
  Kimi Code ─────────┤
                     │
  Hermes Agent ──────┤
                     │
  DeepSeek Harness ──┤
                     │
  OpenClaw ──────────┤
                     │
  Pi ────────────────┤
                     │
  Nanobot ───────────┤
                     │
  NanoClaw ──────────┤
                     ├──▶  ~/.mnemon  ◀── 共享记忆
  OpenCode ──────────┤
                     │
  Gemini CLI ────────┘
```

基础已就绪：一个 `~/.mnemon` 数据库，任何 agent 都可以读写。Claude Code、Codex、Cursor、ZCode、TRAE/TRAE Work、Qoder/QoderWork、CodeBuddy、WorkBuddy、Kimi Code、OpenCode 和 Hermes Agent setup 可自动安装 hook/plugin；OpenClaw 可以使用 plugin hooks；Pi 通过原生 skill 和 TypeScript lifecycle extension 集成；MiniMax Code 和 Nanobot 通过 skill 文件集成；NanoClaw 通过容器技能和卷挂载集成。同一套 integration bundle 可以安装到任何支持 skill、rule、system prompt 或 event hook 的 LLM CLI。

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
编辑当前 setup 流程生成的 guideline（`~/.mnemon/prompt/guide.md`）。Skill 文件应专注于命令语法。

**什么是 Sub-agent 委派？**
Sub-agent 委派是可选执行策略。当 runtime 支持时，主 agent 可以决定*记什么*，再让更便宜或隔离的 worker 执行 `mnemon remember`。它有用，但不是 Mnemon 架构必需品。

## 配置

| 环境变量 | 默认值 | 说明 |
|---------|-------|------|
| `MNEMON_DATA_DIR` | `~/.mnemon` | 基础数据目录 |
| `MNEMON_STORE` | *（active 文件或 `default`）* | 命名记忆体，用于数据隔离 |
| `MNEMON_MAX_INSIGHTS` | `1000` | 活跃 insight 上限；设为 `0` 可关闭自动清理 |
| `MNEMON_AUTO_PRUNE_MIN_AGE` | `24h` | 自动清理前的保护期；支持 `24h`、`7d` 或 `0` |
| `MNEMON_EMBED_ENDPOINT` | `http://localhost:11434` | 嵌入 API 端点 |
| `MNEMON_EMBED_MODEL` | `nomic-embed-text` | 嵌入模型名称 |
| `MNEMON_EMBED_PROTOCOL` | *（自动探测）* | `ollama` 或 `openai`；端点以 `/v1` 结尾时自动切换 |
| `MNEMON_EMBED_API_KEY` | *（无）* | OpenAI 兼容服务器（oMLX、vLLM 等）的 Bearer 令牌 |
| `MNEMON_EMBED_DIMENSIONS` | *（原生维度）* | 可选的 Matryoshka 维度截断 |

每次自动删除均为软删除，以 `prune` 操作记录到 oplog，并通过触发命令的
`auto_pruned_ids` 字段返回具体 ID。

嵌入客户端默认使用 Ollama API；当端点以 `/v1` 结尾（或显式设置
`MNEMON_EMBED_PROTOCOL=openai`）时改用 OpenAI 兼容的 embeddings API。例如，
可通过以下配置对接 [oMLX](https://omlx.dev) 等本地服务器：

```bash
export MNEMON_EMBED_ENDPOINT=http://127.0.0.1:18000/v1
export MNEMON_EMBED_MODEL=bge-m3-mlx-8bit
export MNEMON_EMBED_API_KEY=sk-... # 无需认证的本地服务器可省略
mnemon embed --status
```

也可在命令上使用 `--data-dir` 或 `--store` 标志覆盖。

## 开发

```bash
make build          # 构建单一 mnemon 可执行文件
make install        # 构建 + 安装到 $GOBIN
make test           # 运行确定性 CI 测试
make test-integration  # 按需运行 CLI E2E 与 Agency 边界测试
mnemon setup        # 交互式设置（检测环境 + 部署钩子/技能/引导）
mnemon setup --eject  # 移除所有集成
make help           # 显示所有目标
```

**依赖**：Go 1.24+、`modernc.org/sqlite`、`spf13/cobra`、`google/uuid`

**可选**：[Ollama](https://ollama.ai) 或 OpenAI 兼容的嵌入服务器

## 文档

- [Agency Preview 指南](AGENCY.md) — 成熟度边界、Pi 设置、View → Intent → Receipt 与可选 peer 协作
- [Go 工程规范](../development/go-engineering-standard.md) — 可维护性、并发、持久化、测试与质量 ratchet
- [设计与架构](DESIGN.md) — 当前 engine architecture、核心概念、算法、集成设计
- [Memory 用法与参考](USAGE.md) — 根级 Memory 命令、导入、回执与嵌入向量支持
- [记忆导入指南](IMPORT.md) — 导入历史聊天的 schema 与 LLM 提取提示词
- [架构图](../diagrams/) — 系统架构、记忆/召回流程、四图模型、生命周期管理

## 参考文献

Mnemon 取用了一篇论文的范式和另一篇论文的方法论，并基于图记忆与 LLM 注意力同构这一结构洞察。详见[理论基础](DESIGN.md#25-理论基础)。

- **RLM** — Zhang, Kraska & Khattab. [Recursive Language Models](https://arxiv.org/abs/2512.24601). 2025. 建立范式：LLM 作为外部环境的 orchestrator 比直接处理数据更有效。
- **MAGMA** — Zou et al. [A Multi-Graph based Agentic Memory Architecture](https://arxiv.org/abs/2601.03236). 2025. 提供方法论：四图模型（temporal、entity、causal、semantic）+ intent-adaptive retrieval。
- **Graph-LLM 结构洞察** — Joshi & Zhu. [Building Powerful GNNs from Transformers](https://arxiv.org/abs/2506.22084). 2025；及图智能体记忆综述（Chang Yang et al., 2026）。证实 LLM 注意力机制在计算上等价于 GNN 操作——图记忆是结构性匹配，而非工程便利。

## 许可证

Copyright 2026 Grivn and Mnemon contributors.

[Apache-2.0](../../LICENSE)

`LICENSE` 末尾带方括号的版权示例属于 Apache 2.0 标准许可证的应用附录；
本节所列内容才是本项目的实际版权声明。
