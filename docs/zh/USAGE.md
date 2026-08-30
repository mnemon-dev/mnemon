# Mnemon Memory — 用法与参考

> 你不需要自己运行 Memory 命令 — agent 会在 Hook 和 Skill 指引下执行。本文档只介绍根命名空间下的 Memory CLI，供理解能力、调试和高级手动操作使用。持久 Agent 工作与 Peer 协作请参阅 [Agency Preview 指南](AGENCY.md)。

---

## Memory 根标志

以下根标志用于配置 Memory 命令：

| 标志 | 默认值 | 说明 |
|---|---|---|
| `--store <name>` | (自动) | 命名记忆体（覆盖 `MNEMON_STORE` 和 active 文件） |
| `--data-dir <path>` | `~/.mnemon` | 基础数据目录 |
| `--embed-model <name>` | `nomic-embed-text` | 嵌入模型（覆盖 `MNEMON_EMBED_MODEL`） |
| `--readonly` | `false` | 打开不可变的 Memory 数据库快照；拒绝写命令且不创建 WAL 文件 |

`--readonly` 适用于只读挂载上的静态数据库快照。它会拒绝修改 Memory
数据的命令，并禁用 recall 计数器和 oplog 等附带写入。请勿用它跟随由另一个
进程持续修改的数据库；不可变快照会有意忽略并发 WAL 更新。
| `--version` | | 打印版本并退出 |

---

## Memory 设置

将 mnemon 部署到 LLM CLI 环境中。安装后首先运行此命令。

```bash
# 交互式：检测环境并安装（项目本地）
mnemon setup

# 用户级安装（所有项目）
mnemon setup --global

# 非交互式：仅指定目标
mnemon setup --target claude-code
mnemon setup --target codex
mnemon setup --target cursor
mnemon setup --target zcode --global
mnemon setup --target minimax-code
mnemon setup --target trae
mnemon setup --target qoder
mnemon setup --target qoderwork
mnemon setup --target codebuddy
mnemon setup --target workbuddy
mnemon setup --target kimi
mnemon setup --target opencode
mnemon setup --target openclaw
mnemon setup --target pi
mnemon setup --target nanobot --global
mnemon setup --target hermes

# 自动确认所有提示（CI 友好）
mnemon setup --yes

# 移除 mnemon 集成
mnemon setup --eject
mnemon setup --eject --target claude-code
```

| 标志 | 默认值 | 说明 |
|---|---|---|
| `--global` | `false` | 安装到用户级配置而非项目本地（ZCode 生命周期 hooks 必须使用；MiniMax Code 安装到 `~/.minimax/`；Nanobot 推荐安装到 `~/.nanobot/workspace/`；Pi 安装到 `~/.pi/agent/`；Hermes 安装到 `~/.hermes/`；QoderWork 安装到 `~/.qoderwork/`；Kimi Code 安装到 `~/.kimi-code/` 或 `$KIMI_CODE_HOME/`；OpenCode 安装到 `~/.config/opencode/`） |
| `--target <name>` | (自动检测) | 目标环境：`claude-code`、`codex`、`cursor`、`zcode`、`minimax-code`、`trae`、`qoder`、`qoderwork`、`codebuddy`、`workbuddy`、`kimi`、`opencode`、`openclaw`、`nanobot`、`pi` 或 `hermes` |
| `--eject` | `false` | 移除 mnemon 集成 |
| `--yes` | `false` | 自动确认所有提示 |

---

## Memory CLI 命令

### 核心命令

```bash
# Remember — 存储新洞察（内置 diff：重复跳过，冲突自动替换）
mnemon remember "选择 Qdrant 而非 Milvus 做向量搜索" \
  --cat decision --imp 5 --entities "Qdrant,Milvus" --tags "architecture,search" --source agent

# 跳过重复/冲突检测
mnemon remember "原始笔记" --no-diff

# Recall — 意图感知的图增强检索（默认输出为紧凑格式）
mnemon recall "vector database" --limit 10

# 输出完整召回结果（signals、meta、时间戳）
mnemon recall "vector database" --verbose

# 显式指定意图覆盖
mnemon recall "为什么选择 Qdrant" --intent WHY

# 按分类/来源过滤
mnemon recall "auth" --cat decision --source agent

# 简单 SQL LIKE 匹配（更快，无图遍历）
mnemon recall "auth" --basic

# Search — 基于 token 评分的关键词搜索
mnemon search "authentication" --limit 10

# Import — 批量导入 Memory draft（格式与 LLM prompt 见 docs/IMPORT.md）
mnemon import memory_draft.json
mnemon import --dry-run memory_draft.json   # 只验证，不写入
mnemon import --no-diff memory_draft.json   # 跳过去重

# Forget — 软删除洞察
mnemon forget <id>
```

**Remember 标志：**

| 标志 | 默认值 | 说明 |
|---|---|---|
| `--cat` | `general` | 分类：`preference`、`decision`、`fact`、`insight`、`context`、`general` |
| `--imp` | `3` | 重要性：1–5 |
| `--tags` | | 逗号分隔的标签 |
| `--entities` | | 逗号分隔的实体（与自动提取合并） |
| `--entity-mode` | `merge` | 实体处理模式：`merge`（传入实体 + 自动抽取）、`provided`（只用 `--entities`）、`auto`（只用自动抽取） |
| `--source` | `user` | 来源：`user`、`agent`、`external` |
| `--no-diff` | `false` | 跳过重复/冲突检测 |

**Recall 标志：**

| 标志 | 默认值 | 说明 |
|---|---|---|
| `--limit` | `10` | 最大结果数 |
| `--intent` | (自动检测) | 覆盖意图：`WHY`、`WHEN`、`ENTITY`、`GENERAL` |
| `--cat` | | 按分类过滤 |
| `--source` | | 按来源过滤 |
| `--basic` | `false` | 使用简单 SQL LIKE 匹配代替智能召回 |
| `--verbose` | `false` | 输出完整召回响应（signals、meta、时间戳） |

默认紧凑输出针对 LLM/agent 消费优化，包含 `id`、`content`、`category`、
`importance`、`intent`、`matched_via`、`confidence` 和 `score`。使用
`--verbose` 可恢复包含 signals、遍历元数据和时间戳的完整响应。置信度标签只在
紧凑模式输出；完整响应保留原始分数，供调用方自行设置阈值。

**Import 标志：**

| 标志 | 默认值 | 说明 |
|---|---|---|
| `--dry-run` | `false` | 只验证 draft 文件，不写入数据库 |
| `--no-diff` | `false` | 跳过去重，将全部洞察作为新记录插入 |

### 图操作

```bash
# Link — 创建类型化边
mnemon link <source_id> <target_id> --type semantic --weight 0.85
mnemon link <source_id> <target_id> --type causal --weight 0.8 \
  --meta '{"sub_type":"causes","reason":"..."}'

# Related — 从某个洞察出发的 BFS 遍历
mnemon related <id> --edge causal --depth 2
```

### 生命周期管理

```bash
# GC — 查看低保留度候选
mnemon gc --threshold 0.5 --limit 20

# GC keep — 提升某个洞察的保留度
mnemon gc --keep <id>
```

### 记忆体管理

Mnemon 支持命名记忆体（store）进行数据隔离。每个记忆体拥有独立的数据库。

```bash
# 列出所有记忆体（* 标记当前活跃的）
mnemon store list

# 创建新记忆体
mnemon store create work

# 切换默认活跃记忆体
mnemon store set work

# 删除记忆体（不可删除当前活跃的）
mnemon store remove old-project
```

**记忆体解析优先级**（从高到低）：

1. `--store <name>` CLI 标志
2. `MNEMON_STORE` 环境变量
3. `~/.mnemon/active` 文件
4. 回退到 `"default"`

不同 agent 或进程可通过 `MNEMON_STORE` 环境变量使用不同记忆体 — 无全局状态竞争。旧版数据库（`~/.mnemon/mnemon.db`）在首次运行时自动迁移到 `~/.mnemon/data/default/`。

### 可观测性

```bash
mnemon status             # 记忆统计
mnemon log                # 操作日志（默认：最近 20 条）
mnemon log --limit 50     # 显示更多条目
mnemon receipt            # 输出包含近期操作哈希的 JSON 回执
mnemon receipt --limit 50 # 在回执中包含更多操作
```

`mnemon receipt` 是经过隐私缩减的 Memory 边界审计导出，用于共享或归档观察，
而不公开原始记忆、召回查询、路径或操作详情。它输出操作名、时间戳以及标识符和
详情的 SHA-256 哈希，便于团队关联观察到的 `remember`、`recall`、`forget` 或
GC 活动，同时不暴露底层内容；它不是带签名、可由第三方独立验证的 proof。

示例结构：

```json
{
  "schema": "mnemon.memory.receipt.v1",
  "privacy": {
    "raw_detail_included": false,
    "hash_algorithm": "sha256"
  },
  "events": [
    {
      "event_name": "mnemon.memory.operation.observed",
      "operation": "remember",
      "detail_present": true,
      "detail_hash": "..."
    }
  ]
}
```

### 可视化

导出知识图谱进行可视化探索：

```bash
# DOT 格式 — 使用 Graphviz 渲染（brew install graphviz）
mnemon viz --format dot -o graph.dot
dot -Tpng graph.dot -o graph.png

# 交互式 HTML — 直接在浏览器中打开（vis.js，无需安装）
mnemon viz --format html -o graph.html
open graph.html
```

节点按分类着色（decision、fact、insight、preference、context），边按类型着色（temporal、semantic、causal、entity）。

---

## 配置

| 变量 | 默认值 | 说明 |
|---|---|---|
| `MNEMON_DATA_DIR` | `~/.mnemon` | 基础数据目录 |
| `MNEMON_STORE` | `default` | 活跃命名记忆体 |
| `MNEMON_EMBED_ENDPOINT` | `http://localhost:11434` | 嵌入 API 端点 |
| `MNEMON_EMBED_MODEL` | `nomic-embed-text` | 嵌入模型 |
| `MNEMON_EMBED_PROTOCOL` | （自动探测） | `ollama` 或 `openai`；以 `/v1` 结尾的端点自动选择 `openai` |
| `MNEMON_EMBED_API_KEY` | （无） | OpenAI 兼容服务器的 Bearer 令牌 |
| `MNEMON_EMBED_DIMENSIONS` | (原生维度) | 嵌入向量维度；可设置截断值（例如 Matryoshka 模型使用 `256`） |
| `MNEMON_MAX_INSIGHTS` | `1000` | 触发自动清理的活跃洞察数量上限；设为 `0` 可关闭自动清理 |

---

## 嵌入向量支持（可选）

Mnemon 无需嵌入服务即可完整运行 — 所有核心功能（remember、recall、link、图遍历）开箱即用。配置 Ollama 或 OpenAI 兼容服务器可通过向量相似度增强召回精度，但从不是必需的。

### 有无嵌入的对比

| 能力 | 无嵌入向量 | 有嵌入向量 |
|---|---|---|
| **召回锚点** | 关键词 + 时间 | 关键词 + 向量 + 时间（RRF 混合） |
| **语义边** | Token 重叠（较粗） | 余弦相似度 ≥ 0.50（精确） |
| **遍历评分** | 纯结构分 | 结构 + 语义 |
| **重排序权重** | 关键词 45%、实体 25%、图 30% | 关键词 30%、实体 15%、相似度 35%、图 20% |

配置的嵌入服务不可用时，重排序系统会自动将相似度权重重新分配给关键词和图信号 — 无需额外配置或降级模式标志。Mnemon 在运行时以 2 秒超时检测服务可用性。

### 安装

Ollama 仍是默认服务：

```bash
brew install ollama              # 或参见 https://ollama.ai
ollama pull nomic-embed-text     # 下载嵌入模型
```

使用 OpenAI 兼容服务器时，将端点指向其 `/v1` 基础 URL，并选择服务器上的嵌入模型。无需认证的本地服务器可省略 API key：

```bash
export MNEMON_EMBED_ENDPOINT=http://127.0.0.1:18000/v1
export MNEMON_EMBED_MODEL=bge-m3-mlx-8bit
export MNEMON_EMBED_API_KEY=sk-... # 无需认证的本地服务器可省略
```

仅当兼容端点不以 `/v1` 结尾时，才需要显式设置 `MNEMON_EMBED_PROTOCOL=openai`。

验证：

```bash
mnemon embed --status
```

```json
{
  "total_insights": 87,
  "embedded": 87,
  "coverage": "100%",
  "embedding_available": true,
  "ollama_available": true,
  "protocol": "ollama",
  "model": "nomic-embed-text"
}
```

为兼容现有脚本，`ollama_available` 字段会继续保留；新集成应使用
`embedding_available` 和 `protocol`。

### 回填已有洞察

如果在使用 mnemon 之后才配置嵌入服务，已有洞察不会有嵌入向量。一条命令即可回填：

```bash
mnemon embed --all
```

这会为所有未嵌入的洞察生成嵌入向量并自动创建语义边。可在前后使用 `mnemon embed --status` 检查覆盖率。

---

## 架构

```
┌──────────────────┐     CLI commands      ┌──────────────────┐
│   LLM Agent      │ ───────────────────── │     Mnemon       │
│ (Claude Code,    │  remember, recall,    │                  │
│  Cursor, etc.)   │  link, forget, gc     │  SQLite (WAL)    │
└──────────────────┘                       │  ┌────────────┐  │
                                           │  │ Insights   │  │
        The LLM decides WHAT               │  ├────────────┤  │
        to remember and link.              │  │ 4 Edge     │  │
                                           │  │ Types:     │  │
        Mnemon handles HOW                 │  │ temporal   │  │
        to store, index, and               │  │ entity     │  │
        retrieve.                          │  │ causal     │  │
                                           │  │ semantic   │  │
      ┌──────────────────┐                 │  ├────────────┤  │
      │ Embedding server │  (optional)     │  │ Embeddings │  │
      │ configured model │ ◄───────────── │  └────────────┘  │
      └──────────────────┘                 └──────────────────┘
```

受 [MAGMA](https://arxiv.org/abs/2601.03236) 四图模型启发。详见[设计与架构](DESIGN.md)。
