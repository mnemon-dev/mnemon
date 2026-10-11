# 存储与记忆模型

**简体中文** | [English](../../en/reference/storage-model.md) | [文档中心](../README.md)

## 为什么分三类记忆

默认 Starter 用三个独立 Source 处理不同的访问模式，再由分层策略把它们组合进每一轮。这是默认组合，不是 Core 的固定分类，也不限制第三方 Source 的形态：

| 问题 | 存放位置 | 原因 |
|---|---|---|
| 下一轮必须直接知道什么？ | Runtime Memory | 极小、直接进入 prompt |
| 哪份设计或流程需要快速完整阅读？ | active Documents | 保留 Markdown 结构，不必做深召回 |
| 哪些历史事实和关系应跨会话存在？ | Memory Spaces | 独立数据库、图关系、按需召回 |
| 长文已不常用但仍需追溯怎么办？ | archived Documents | Mnemon 留索引，冷层保留原文 |

推荐查询梯度：

```text
current request and repository facts
             |
             v
Runtime Memory already in prompt
             |
             v
search active Documents
             |
             v
recall active Memory Spaces
             |
             v
follow an exact cold reference when full text is required
```

## 跨 Agent 共享边界

共享不意味着广播对话或文件。多个 Agent 都集成 Mnemon，且能够访问相同存储根与可识别的 Store 时，可以共享 Native 长期事实。三方 Provider 通过自己的 workspace、user、bank、project 或 container 范围共享；Runtime 与 Documents 不会自动进入其他 Agent 的上下文。

`global` 适合本机公共根，`custom` 适合显式约定的目录，`workspace` 将共享限制在项目。多进程并发依赖 Provider 自身的一致性保证；离线复制或直接修改本地数据库前，应停止所有使用者，参见[运维指南](../guides/operations.md#备份与恢复)。

## 统一根目录

```text
<storageRoot>/
+-- runtime/
|   +-- memories.json
|   +-- USER.md
|   +-- MEMORY.md
+-- documents/
|   +-- index.json
|   +-- active/
|   +-- archived/
+-- data/
|   +-- .dsh-memory-bodies.json
|   +-- <memory-space-id>/
|       +-- mnemon.db
+-- state/
    +-- memory-providers.json     # 第三方连接控制面，0600，不进入 Mnemon Pack
```

`storageScope` 决定整个根，而不只是 Mnemon 数据库。`workspace` 范围会为每个已登记 DSH 工作区解析独立的 `<workspace>/.mnemon`。显式启用的 `runtimeUserScope=global` 是唯一的分根例外：Runtime 从全局根读取 USER.md，MEMORY.md 与其他所有组件仍留在所选根。工作台任务使用查看工作区；对话工具与生命周期使用所属会话的 cwd 和已固定的 View。`state/memory-providers.json` 保存第三方 endpoint、目标 URI、身份和可选凭据；文件权限为 `0600`，Host 只返回已配置字段名，不回传凭据值。

`workspaces` 布局把四个 area 集中在 `<集中根>/workspaces/<工作区路径哈希>/`；Host 解析目录时不创建文件或修改旧根。只有显式 `runtimeUserScope: global` 会把 USER.md 放在该工作区子目录之外。


<a id="runtime-memory"></a>

## 运行时记忆

### 语义

- `target=user`：身份、角色、长期偏好、习惯、沟通风格和明确协作要求。
- `target=memory`：项目、环境、决策、约定、工具特性和可复用经验。
- `importance=critical|normal|low`：记录的重要性，在模型投影中可见，也用于整理时的保留优先级。
- `branches`（可选，仅 `target=memory`）：限定该条目在每回合 Runtime 快照中投影到的 git 分支名列表；没有分支列表的条目在所有分支可见。

当前不实现 `daily` target。

### 事实源和投影

在单个根内，`runtime/memories.json` 是唯一事实源。启用 `runtimeUserScope=global` 后，有效快照只组合全局事实源中的 `target=user` 条目与所选事实源中的 `target=memory` 条目。两个 JSON 文件和两组 Markdown 投影始终保持完整且不改写；过滤只发生在有效 Runtime controller。每条记录包含：

```text
content
created_at
updated_at
target
importance
branches（可选）
```

`branches` 是 `target=memory` 条目的可选 git 分支名列表。没有 `branches`（或为空列表）的条目在所有分支可见。当会话的 workspace 是位于分支 `B` 的 git 工作树时，每回合的 Runtime 快照会隐藏 `branches` 列表不包含 `B` 的 `memory` 条目；非 git 工作区和 detached HEAD 下所有条目都会投影。`USER.md` 条目永远不携带 `branches`。

`USER.md` 和 `MEMORY.md` 是完整事实源的确定性派生文件。每个条目被归一成单行，条目之间使用单独一行的 `§` 分隔；`§` 是保留字符。启动和 prompt 组装时，控制层会从 JSON 修复缺失或被手工修改的投影。分支过滤只作用于 prompt 投影，不作用于这两个文件。

Runtime Source 会在面向模型的快照中，为每条正文添加一行元数据：

```text
[importance=critical; created=14d; updated=2d]
偏好简洁回复。
```

`created` 和 `updated` 表示距存储时间戳经过的完整 24 小时天数，在捕获投影时统一计算。不到一天显示 `0d`；未来时间戳显示 `future`，无法解析的时间戳显示 `unknown`。用户档案位于全局根时，两个根使用同一个捕获时间。当前回合保持已捕获的天数；下一回合会重新计算，即使存储 revision 没有变化。

这些行是正文之外的注释。`old_text` / `oldText` 只能匹配正文，不包含元数据行。记录的重要性和时间跨度不会覆盖当前指令。JSON、磁盘 Markdown、正文匹配、条目顺序和存储容量均保持不变。注释计入已有的模型投影字符预算，因此短条目较多时，即使默认 Strategy 也可能截断或省略条目。其指引明确说明快照受预算限制，缺失不代表删除；light-context 可以进一步降低预算。

### 操作

- `add` 写入独立新事实，完全相同的内容不会重复添加。
- `replace` 和 `remove` 先在请求的 `target` 内，用规范化后的 `old_text` 匹配完整正文，再替换或移除整个条目。唯一的精确匹配优先，即使其他条目也包含同样的文本。
- 没有完整正文匹配时，仍可用唯一子串定位条目。
- 零命中、重复的精确匹配和有歧义的子串匹配均拒绝，不改动存储，也不执行模糊修改。分支标签控制投影，不用于选择 mutation 的目标条目。

### 容量

| 目标 | 默认上限 | 维护方式 |
|---|---:|---|
| `USER.md` | 4 KiB | 本地、无工具 worker 保守合并，不进入 Memory Space |
| `MEMORY.md` | 10 KiB | Host 精确归档已提交条目，再确定性装填热记忆余量 |

两个上限都可以通过 `runtimeMemory.userLimitBytes` 与 `runtimeMemory.memoryLimitBytes` 调整，见 [Runtime Memory 容量与维护预算](./configuration.md#runtime-memory-容量与维护预算)。分层策略下，合法写入超过上限时才触发容量维护；其他策略会直接拒绝该写入。具名工具、通用 Action、后台子 Agent 和 Web 管理共用同一 Host 流程；Web 写入按所选存储范围执行，不要求打开用户会话。归档失败会保留热记忆并返回错误。底层 Source 独立使用时仍只执行自己的存储操作，自定义 Strategy 不会隐式继承默认归档。

容量按存储正文和条目分隔符的实际 UTF-8 字节计算，不包含仅用于 prompt 的元数据。单条内容最大 8 KiB。当 `add`、`replace` 或 `remove` 遇到容量溢出时，Host 会在任何 Provider 写入前重新检查源 revision。只有一个可写 Memory Space 时完全不调用模型；存在多个空间时，worker 只读取有界路由摘录并返回目标 id，不重写记忆内容。Mnemon Native 先从只读命名空间快照复用完全相同的原文，合并批内相同条目，再按目标空间通过 schema-v1 draft 和 `--no-diff` 各导入一次剩余原文，避免内容相似但不同的事实被跳过或相互覆盖。其他 Provider 继续使用适配器定义的写入语义。Host 要求每个源条目都有一条精确终态回执（跳过的重复项还必须有精确 Recall 证据），随后按重要性和字节预算选择热记忆保留项，并在原 revision fence 下把余量与待处理变更一次提交。Provider 无法与本地文件共享同一事务，因此稍后的 revision 冲突或并发外部写入可能留下已经归档的重复项；现有热记忆仍受修订检查保护。

<a id="project-documents"></a>

## 项目档案

### 用途

Documents 保存比单条记忆更完整、又希望快速阅读的项目知识，例如：

- 架构设计和理由；
- 有证据的调查结论；
- 操作流程、发布清单和故障复盘；
- 实现交接与长期维护说明。

用户画像、普通聊天、临时进度、原始大日志和秘密不应进入 Documents。

### 控制面

`documents/index.json` 是元数据事实源，管理 ID、标题、description、状态、文件名、来源路径、session、时间、revision、SHA-256、大小和 Memory Space 引用。Markdown 托管副本带有生成的 frontmatter。

`sourcePaths`：

- 只能指向当前会话工作区内部；
- 只作为来源引用，不会被插件修改；
- 当前实现不要求路径实际存在；
- 不允许指向受管 `documents/` 目录自身。

### 范围

Documents 的物理共享范围由 `storageScope` 决定：

- `workspace` / `workspaces`：通常随项目隔离；
- `global` / `custom`：多个工作区可能共享同一个 `documents/index.json`。

因此“项目档案”表示内容类型，不保证天然按工作区物理隔离。当前会话工作区只约束新写入的 `sourcePaths`。

### 容量与冷热分层

| 项目 | 限制 |
|---|---:|
| 单份正文 | 最大 2 MiB |
| active 总量 | 最大 10 MiB，包含生成后的 frontmatter |
| archived 总量 | 不计 active 上限 |

创建或更新前会计算真实投影大小。空间不足时按 `lastAccessedAt`、再按 `updatedAt` 选择最久未访问的 active 文档；先写入/验证 Mnemon 冷引用，再在 revision 未变化时迁移原文。

默认搜索只覆盖 active。搜索会更新命中文档的 `lastAccessedAt`，因此它对正文只读，但会写索引元数据。

<a id="memory-spaces"></a>

<a id="记忆体"></a>

## 记忆空间

记忆空间是长期记忆的统一语义与路由单位，具体数据面由 Provider 决定：

```text
id            Host 生成或沿用已发现的 Mnemon Store 名
name          人类可读名称
description   路由边界：什么属于这里、何时召回
active        是否参与 DSH 读取与路由
provider      mnemon-native 或已登记的三方引擎
location      本地 Store/CLI 作用域，或远程 endpoint + Provider 作用域
```

### 读写边界

- Mnemon 原生层在初始化后至少保留一个 Store，并通过 `<storageRoot>/active` 选择一个默认 Store；普通 Mnemon Agent 仍按这套单 Store 语义工作。
- dsh-mnemon 的激活状态是独立控制面：任意 0..N 个记忆空间可以激活，全部未激活也不会改变 Mnemon 默认 Store 或远程数据。
- 召回与浏览只使用已激活记忆空间；图谱、实体、关联、链接和删除由 Provider 能力决定。
- 指定未激活记忆空间进行读取会被拒绝。
- 写入可以选择任何支持 `remember` 的已登记记忆空间；回执会反映 Provider 的精确写入或异步提炼语义。
- 对未激活目标写入成功后，插件自动激活它。
- 没有显式目标且激活数量不是 1 时，确定性服务要求调用方先选择目标。

### 创建、发现和合并

- 未初始化的空根可以保持零 Store；第一次显式创建记忆空间时使用 Mnemon 原生 `default` ID，名称与路由说明仍由用户决定，后续创建使用 Host 生成的 UUID。
- 初始化后不能删除最后一个原生 Store，但可以将最后一个记忆空间设为未激活。删除 Mnemon 默认 Store 时，插件会先切换到另一个现存 Store。
- 既有 `<storageRoot>/data/<store>/mnemon.db` 会被发现并登记，不移动数据库。
- 合并通过 Mnemon import 把来源内容导入目标；来源数据库保留，默认只将来源设为未激活。
- Pack 替换不能把已初始化的 Store 集合清空；替换后若原默认 Store 已不存在，插件会选择一个现存 Store 修复原生默认指针。
- `forget` 是按精确 ID 的软删除，不等于删除数据库文件。
- 用户可在既有“创建记忆空间”弹窗选择 Mnemon Native 或任意已登记三方引擎，也可在智能模式中显式加入已配置候选；断开只删除本地连接登记，不删除 Provider 数据。
- 智能 placement 的允许列表、数据边界与必需能力是 Host 强制规则；软偏好与 Prompt 只用于多个合格候选之间的语义选择，不能绕过硬规则。决策回执与记忆空间元数据一起保存。
- 合并仍只适用于 Mnemon Native。图谱、关系、浏览、精确/异步写入以及硬/软/不支持删除都以各 Provider 声明能力为准；UI 与 Agent 不会假装补齐缺失行为。见 [Provider 能力矩阵](../guides/memory-providers.md)。

### 跨 Agent 可见性

`mnemon.db` 是 Mnemon 原生数据面，不是 dsh-mnemon 私有格式。其他 Mnemon-enabled Agent 在使用同一个 `storageRoot` 和 Store 时，可以访问同一份长期记忆。dsh-mnemon 也会发现磁盘上兼容的 Store；其 DSH 专有名称、说明和激活状态仍由 `.dsh-memory-bodies.json` 管理。

三方可见性由各自 Provider 作用域决定，例如服务与 URI、workspace/peers、bank、project/user、知识目录或 container。任何 Provider 的共享都不延伸到 `runtime/` 或 `documents/`；不能把“共享第三层记忆”表述为自动共享完整 DSH 上下文。

## 四类关系

Mnemon Native 保留 `temporal`、`semantic`、`causal` 和 `entity` 关系；Hindsight 投影 Provider 图谱，Holographic 生成本地实体/语义关系。没有图谱边的 Provider 只贡献有界无边节点，适配器不会伪造关系。记忆空间页会按能力隐藏不适用的关联、链接、浏览与遗忘动作。

## 数据权威表

| 数据 | 权威源 | 派生/缓存 |
|---|---|---|
| 热记忆 | `runtime/memories.json` | `USER.md`、`MEMORY.md` |
| Documents | `documents/index.json` + 托管 Markdown | excerpt、搜索排序、状态聚合 |
| Mnemon Native 目录 | `data/.dsh-memory-bodies.json` + 磁盘 Store | Web 状态聚合 |
| 第三方 Provider 连接 | `state/memory-providers.json` | 脱敏的 Provider 能力与状态 |
| 长期记忆 | Mnemon `mnemon.db` 或远程 Provider | 图谱投影、跨 Provider 排名融合 |
| 审查水位 | Host 进程内存 | 状态页快照；尚未持久化 |
