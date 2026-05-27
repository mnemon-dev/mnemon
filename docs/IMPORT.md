# Mnemon — 记忆导入指南

本文档说明如何将历史聊天记录或外部上下文批量导入 Mnemon 的记忆图谱。

---

## 工作流概览

```
聊天导出 / Markdown  →  LLM（使用下方提示词）  →  memory_draft.json  →  mnemon import <file>
```

1. 将原始聊天记录或笔记导出为 Markdown 或纯文本。
2. 将文本连同下方的**参考提示词**一起发送给 LLM，生成符合本文档格式的 `memory_draft.json`。
3. 运行 `mnemon import memory_draft.json`，Mnemon 自动完成去重、图谱边构建、向量嵌入和生命周期评分。

---

## 导入文件格式（schema_version: "1"）

```json
{
  "schema_version": "1",
  "source": "chat-export",
  "insights": [
    {
      "content": "选择了 Qdrant 而非 Milvus 作为向量搜索引擎，主要原因是其过滤查询性能更好。",
      "category": "decision",
      "importance": 5,
      "tags": ["architecture", "search", "vector-db"],
      "entities": ["Qdrant", "Milvus"],
      "source": "agent",
      "created_at": "2024-03-15T09:30:00Z"
    },
    {
      "content": "用户偏好简洁的 API 响应，不希望看到多余的解释文本。",
      "category": "preference",
      "importance": 4,
      "tags": ["ux", "api"]
    }
  ],
  "edges": [
    {
      "source_index": 0,
      "target_index": 1,
      "edge_type": "causal",
      "weight": 0.7,
      "reason": "向量引擎选型决策影响了 API 响应设计偏好"
    }
  ]
}
```

---

## 字段说明

### 顶层字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `schema_version` | string | **是** | 必须为 `"1"` |
| `source` | string | 否 | 整批导入的来源标签（如 `"chat-export"`、`"manual"`）。单条 insight 可通过自身的 `source` 字段覆盖此值 |
| `insights` | array | **是** | 记忆节点列表，至少包含一项 |
| `edges` | array | 否 | 显式关系列表；Mnemon 的图引擎也会自动创建边，此处为补充或强制指定 |

### insights 条目字段

| 字段 | 类型 | 必填 | 约束 | 说明 |
|---|---|---|---|---|
| `content` | string | **是** | 最多 8000 字符 | 记忆的文本内容 |
| `category` | string | 否 | 见下表，默认 `general` | 知识类型 |
| `importance` | integer | 否 | 1–5，默认 3 | 重要程度；影响保留优先级和自动剪枝 |
| `tags` | array | 否 | 最多 20 项，每项最多 100 字符 | 自由标签，用于检索和过滤 |
| `entities` | array | 否 | 最多 50 项，每项最多 200 字符 | 记忆中涉及的命名实体（人名、项目名、工具名等）；与 Mnemon 自动提取结果合并 |
| `source` | string | 否 | — | 覆盖顶层 `source`，该条记忆的具体来源 |
| `created_at` | string | 否 | RFC 3339 格式 | 原始创建时间；省略时使用导入时间 |

#### category 可选值

| 值 | 适用场景 |
|---|---|
| `preference` | 用户偏好、习惯、风格要求 |
| `decision` | 已确定的技术或产品决策 |
| `fact` | 客观事实、数据、规格参数 |
| `insight` | 推断、分析结论、经验总结 |
| `context` | 背景信息、项目概况、约束条件 |
| `general` | 其他不适合上述分类的记忆 |

#### importance 赋值建议

| 值 | 含义 |
|---|---|
| 5 | 核心决策或强烈偏好，必须长期保留 |
| 4 | 重要上下文，通常需要保留 |
| 3 | 一般记忆（默认值） |
| 2 | 次要细节，可能在自动剪枝时被移除 |
| 1 | 临时或低价值信息 |

### edges 条目字段

| 字段 | 类型 | 必填 | 约束 | 说明 |
|---|---|---|---|---|
| `source_index` | integer | **是** | insights 数组的零基索引 | 边的起点 |
| `target_index` | integer | **是** | 不能等于 `source_index` | 边的终点 |
| `edge_type` | string | **是** | 见下表 | 关系类型 |
| `weight` | float | 否 | 0.0–1.0，默认 0.5 | 关系强度 |
| `reason` | string | 否 | — | 说明该关系存在的原因（存入边元数据） |

#### edge_type 可选值

| 值 | 含义 |
|---|---|
| `temporal` | 时间顺序关系（事件 A 发生在事件 B 之前） |
| `causal` | 因果关系（A 导致或影响 B） |
| `semantic` | 语义相似关系（A 与 B 讨论同一主题） |
| `entity` | 实体共现关系（A 与 B 涉及同一命名实体） |

---

## 使用命令

```bash
# 基本导入
mnemon import memory_draft.json

# 验证格式但不写入数据库
mnemon import --dry-run memory_draft.json

# 跳过去重检测，将所有条目作为新记忆插入
mnemon import --no-diff memory_draft.json

# 指定具体 store
mnemon import --store project-alpha memory_draft.json
```

### 输出示例

```json
{
  "imported": 8,
  "updated": 1,
  "skipped": 2,
  "errors": 0,
  "edges_inserted": 3,
  "auto_pruned": 0,
  "results": [
    {"index": 0, "id": "a1b2c3d4...", "content": "选择了 Qdrant...", "action": "added"},
    {"index": 1, "id": "e5f6a7b8...", "content": "用户偏好简洁的...", "action": "skipped"}
  ]
}
```

| 字段 | 说明 |
|---|---|
| `imported` | 新增的记忆数量 |
| `updated` | 替换了已有冲突记忆的数量 |
| `skipped` | 检测为重复而跳过的数量 |
| `errors` | 写入失败的数量；导入允许部分成功，脚本调用方应检查此字段是否为 0 |
| `edges_inserted` | 成功插入的显式边数量 |
| `auto_pruned` | 超出容量限制后自动删除的记忆数量 |

---

## 参考提示词（用于生成 memory_draft.json）

将以下提示词和你的聊天记录一起发送给 LLM：

```
你是一个记忆提取助手。请从下方的聊天记录或文档中提取有价值的知识片段，
生成一个符合 Mnemon memory draft 格式（schema_version: "1"）的 JSON 文件。

## 提取规则

1. 每条 insight 必须是独立、完整的知识单元，不依赖上下文即可理解。
2. 去除闲聊、重复表述和无实质内容的对话。
3. 如果同一主题多次出现，合并为一条最完整的表述，不要重复。
4. 按以下优先级分配 importance：
   - 5：关键架构决策、明确的用户核心偏好
   - 4：重要上下文、反复出现的模式
   - 3：一般事实和背景信息
   - 2：细节或一次性提及的内容
   - 1：临时状态或极低价值信息
5. entities 填写记忆中出现的具体名词：人名、项目名、工具/库名、组织名。
6. tags 使用小写英文，用连字符分隔词语（如 "vector-db"、"api-design"）。
7. 如果能从上下文推断出原始时间，在 created_at 中填写 RFC 3339 格式的时间。
8. edges 只填写显而易见的强关联关系（因果、同一决策链），不要过度连接。

## 输出要求

- 只输出 JSON，不要有任何解释文字。
- 严格遵守以下 schema：

{
  "schema_version": "1",
  "source": "chat-export",
  "insights": [
    {
      "content": "...",
      "category": "preference|decision|fact|insight|context|general",
      "importance": 1-5,
      "tags": ["tag1", "tag2"],
      "entities": ["Entity1", "Entity2"],
      "created_at": "2024-01-15T09:30:00Z"
    }
  ],
  "edges": [
    {
      "source_index": 0,
      "target_index": 1,
      "edge_type": "causal|semantic|temporal|entity",
      "weight": 0.0-1.0,
      "reason": "..."
    }
  ]
}

## 待处理内容

[在此粘贴你的聊天记录或文档]
```

---

## 常见问题

**Q: `created_at` 必须填吗？**  
不必须。省略时 Mnemon 使用导入时间。如果原始聊天记录有时间戳，建议填写以保留历史顺序。

**Q: 导入后如何验证结果？**  
运行 `mnemon log` 查看最新记忆，或用 `mnemon search <关键词>` 确认可检索。

**Q: `edges` 数组必须填吗？**  
不必须。Mnemon 的图引擎会根据时间、语义相似度和实体共现自动创建边。显式 `edges` 用于指定自动检测难以发现的强关联关系。

**Q: 如何分批导入大型聊天记录？**  
将聊天记录按时间段或主题切分为多个文件，依次执行 `mnemon import`。重复内容会被去重机制自动跳过。

**Q: 可以导入非英文内容吗？**  
可以，`content`、`tags`、`entities` 均支持任意 Unicode 文本。
