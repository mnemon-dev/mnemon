# Web、Headless、工具、命令与 RPC

**简体中文** | [English](../../en/reference/interfaces.md) | [文档中心](../README.md)

本页是集成参考。日常使用请先看 [Sidebar 与对话交互指南](../guides/ui-guide.md)。

## 记忆空间命名与兼容性

产品统一使用**记忆空间（memory space）**。Source 导出 `MemorySpace`、`MemorySpaceView`、`MemorySpaceCatalog` 及对应的请求和元数据类型；已发布的 `MemoryBody*` 类型作为弃用别名保留，数据形状不变。

既有 v0.5.x 标识，如 `mnemon_memory_bodies`、`mnemon_memory_body_create`、`body-directory` 和 `memoryBodyId` / `memoryBodyIds`，仍表示记忆空间。为兼容已安装的工具、Source 页面、Provider 适配器和持久化的 Document/Pack 来源信息，这些标识保持稳定；`.dsh-memory-bodies.json` 文件名与其中的 `bodies` 字段也继续保留。术语调整不会重命名用户创建的空间、修改 ID、移动数据库或改变访问授权。

## 用户界面入口

| 入口 | 默认 | 说明 |
|---|---:|---|
| Sidebar | 是 | 左侧栏的“记忆系统”工作台：状态、运行时、档案、记忆空间 |
| 会话标签页 | 否 | 在会话内打开同一工作台，自动跟随所属会话的范围 |
| 本回合记忆 | 是 | 已完成回合下方的记忆工具摘要；展开后可跳到对应页面 |
| 存入记忆 | 是 | 已定稿助手回复旁的操作；确认后调用监督写入 |
| 插件页中的 dsh-mnemon 页面 | 是 | 记忆组合、组件页面、存储、界面与备份 |
| `/mnemon` | — | 对话命令入口 |
| 模型工具 | — | Root Agent 的结构化读写入口 |

`displayMode` 选择 `sidebar`（默认）或 `builtin`（会话标签页）；`tabEnabled` 控制所选入口是否显示。会话标签页不展示 Sidebar 的范围控件。对话内两个快捷入口在**界面**中分别开关，通过 `mnemon-ui` 设置范围保存。

## Profile 能力面

| 能力 | Web | Headless |
|---|---:|---:|
| 运行时上下文与生命周期提示 | 是 | 是 |
| 模型工具与独立任务 Agent | 是 | 是 |
| `workspace` 范围按 Agent cwd 路由 | 是 | 是 |
| Sidebar / 对话操作 | 是 | 否 |
| Host 到客户端 RPC | 是 | 否 |
| Agent idle 后的延迟评分审查 | Host 持续运行时执行 | 一次性进程退出时取消 |

Headless 会获得完整模型工具面。它把命令行任务作为普通用户消息提交，不提供交互式斜杠命令分发；Agent 进入 idle 前已经完成的显式和模型引导写入会持久化。

模型工具、生命周期 hook 与系统调度都使用 `automatic` trigger；Web/RPC 中由用户直接发起的数据面操作使用 `manual` trigger。因此把某层设为 `manual` 会保留人工管理能力，同时拒绝模型工具和自动投影。`memory-system` 与 `status` 属于控制面观察，即使某层关闭也仍可读取。

## 模型工具

### 只读工具

| 工具 | 用途 | Root Agent 路径 |
|---|---|---|
| `mnemon_status` | CLI、配置、存储与目录聚合状态 | 直接服务 |
| `mnemon_memory_bodies` | 读取记忆空间目录、Provider 能力、健康与可用统计 | 直接服务 |
| `mnemon_recall` | 从一个或多个 active Provider 召回；异构结果排名融合 | 受 pinned Source 权限约束的 Host 直接服务 |
| `mnemon_related` | 在 `capabilities.related=true` 的记忆空间中遍历关系 | 受 pinned Source 权限约束的 Host 直接服务；Root 默认两跳 |
| `mnemon_document_search` | 确定性搜索受管档案 | Documents 控制层 |

“只读”表示不修改受管正文或长期语义内容。`mnemon_document_search` 命中后仍会更新 `lastAccessedAt`，用于 LRU 排序，因此功能只读不等于磁盘只读。

### `writeEnabled=true` 时的工具

| 工具 | 用途 | Root Agent 路径 |
|---|---|---|
| `mnemon_runtime_memory` | `add` / `replace` / `remove` 热记忆 | 确定性控制；add 溢出时可能启动 worker |
| `mnemon_document_manage` | 创建、更新或归档档案 | 创建/更新确定性；归档使用 worker |
| `mnemon_document_create` | 新建独立档案，不更新或归档已有档案 | 确定性 Source `create` Action；供后台审查使用 |
| `mnemon_remember` | 按 Provider 语义沉淀洞察，回执区分已接受与持久提交 | `spawn` write worker |
| `mnemon_link` | 在支持能力的 Provider 中建立 typed relationship | `spawn` write worker |
| `mnemon_forget` | 在支持能力的 Provider 中按精确 ID 删除 | 仅限显式操作的 `spawn` write worker；自主蒸馏不含此工具 |
| `mnemon_memory_body_create` | 由 Agent 创建独立 Mnemon Native 记忆空间；第三方连接只由用户在 WebUI 管理 | `spawn` write worker |
| `mnemon_memory_body_update` | 更新名称、说明或 active | `spawn` write worker |
| `mnemon_memory_body_merge` | 非破坏性合并 Mnemon Native 记忆空间 | `spawn` write worker |

worker 内调用同名工具时直接进入服务层，不再递归委派。

自主 write worker（蒸馏与 supervised writeback 运行）自行决定写入内容，因此宿主在其硬性工具白名单中排除了 `mnemon_forget`：遇到重复或冲突时只能跳过或写入修正后的条目，不能删除已有记忆。请求目标由用户或用户侧流程显式给出的操作（`/mnemon forget`、`mnemon_link` 与记忆空间管理）保留完整 write 工具面。

后台审查可以搜索档案并调用 `mnemon_document_create`，不能调用 `mnemon_document_manage` 或 `mnemon_view_action`。已有档案覆盖候选时跳过；有新增知识时，新建一份独立档案并引用相关已有档案 ID。Source 的 `create` Action 接收 `title`、`content`，以及可选的 `description`、`sourcePaths`、`sessionIds`；拒绝 `action`、`id` 等额外字段。容量不足时拒绝创建，不归档已有档案。已有的用户档案和 Agent 创建档案都受此保护；显式编辑继续使用原有管理路径。

`mnemon_runtime_memory` 的 `target=memory` 写入支持可选 `branches` 数组（git 分支名）。带分支范围的条目只在会话 workspace 处于所列分支上时投影进每回合 Runtime 快照；无分支标签的条目始终投影。`replace` 时提供 `branches` 修改范围，传空数组清除范围，省略则保持当前范围；该参数对 `target=user` 会被拒绝。

## 工具准入建议

- **运行时**：明确偏好、稳定项目约定、环境事实和高频经验。
- **档案**：具有完整结构和理由的设计、调查、流程、复盘或交接。
- **记忆空间**：需要跨任务保留，或适合图关系与深召回的稳定事实、决策和洞察。
- **跳过**：问题、猜测、临时进度、完成日志、原始输出、秘密和可轻易从仓库重新发现的普通事实。

`mnemon_forget` 是破坏性语义操作；只有用户明确要求，或内容已确认错误 / 过时时才应执行。宿主通过把该工具排除出自主 worker 的白名单来强制这一点；用户明确要求时主代理仍可调用。

## `/mnemon` 命令

```text
/mnemon
/mnemon status
/mnemon recall <查询>
/mnemon related <完整记忆 ID>
/mnemon remember <内容>
/mnemon forget <精确 ID>
```

- 空 `/mnemon` 等价于 `status`。
- `status` 是确定性读取，不启动模型。
- `recall`、`related` 通过命令所在 live Agent 的限定 Source 路由直接读取，不启动 worker；`remember`、`forget` 使用该 Agent 作为写入 worker 的 parent。
- 命令 recall 最多返回 10 条。
- `forget` 必须接收一个不含空格的精确 ID。
- `forget` 仅在收到 `forgotten` 回执时报告删除成功；跳过、失败或未确认的结果返回错误并附 worker 摘要。

## 界面注册

Mnemon 在 DSH 中展示的一切都注册到 DSH 自己的界面区域。对话内的注册都是增量的，不替换 DSH 官方渲染。

| DSH 区域 | 注册 | 行为 |
|---|---|---|
| `conversation.chat.turnTail` | list，`id=dsh-mnemon/turn-tail` | 通过 `turn-activity` 汇总完成回合中的 `mnemon_*` 调用，以及工具活动元数据中每次调用读到或写入的内容；无活动或未完成回合不渲染 |
| `conversation.chat.assistant-actions` | list，`id=mnemon-save` | 通过 `assistant-message` 读取已定稿文本；只在用户确认后调用 `supervise` |
| `conversation.session.header.lineage` | DSH 官方条目的低优先级副本 | 任务 Agent 会话页眉只统计该 Agent 自己的 token，不含其 fork 来源的日志 |
| `plugins.bundle.config` | keyed，`dsh-mnemon` | 插件页中 dsh-mnemon 页面上的全部配置 |
| `plugins.row.config` | keyed，`dsh-mnemon#<row>` | 每个随包组件的行打开的页面，含该组件注册的设置 |
| `plugins.detail.actions` | list，`id=dsh-mnemon/open-workspace` | 从插件详情页打开记忆系统 |

Mnemon 自己声明两个 keyed 区域，都以组件包名为键：`mnemon.component.settings` 渲染在该组件的页面上，`mnemon.component.status` 是它在状态页上的卡片。官方组件与第三方组件都通过这两个区域注册，见[插件开发](../development/extensions.md)。

`assistant-message` 读取的候选可编辑，长回复会按界面上限截取；确认后会启动独立任务 Agent，写入结果以它的落定回执为准。

## 工作区路由

Web 工作台请求携带 `sessionId` 和可选 `workspaceId`。Host 只接受 `workspaceRegistry` 已登记的 ID：

- 确定性读取与人工维护可以路由到 `workspaceId` 选择的查看根；
- Agent、工具、命令和生命周期仍按 `sessionId` 对应 Agent cwd 路由；
- `status.workspaceContext` 返回 selected / effective roots 与 `aligned`；
- 工作台发起的独立任务 Agent 使用查看工作区，不要求当前主会话与其对齐；对话工具仍使用所属会话的 cwd 和本轮 View，不借用工作台的查看范围。

Headless 等没有 Web 工作区目录的 profile 不提供任意查看目标；Agent 执行仍直接根据 session cwd 路由 `workspace` 范围。

## RPC 通道

RPC 是 DSH Host 与插件客户端之间的内部桥，不是稳定外部 HTTP API。页面与组件插件应使用 `dsh-mnemon/client` 提供的限定范围客户端，见[插件开发](../development/extensions.md)。

| 通道 | 承载 | 远程页面 |
|---|---|---|
| `/dsh-mnemon-read` | 状态、目录、检索与对话读取 | 允许 |
| `/dsh-mnemon-activation` | 开关单个记忆空间 | 允许 |
| `/dsh-mnemon-write` | 其余所有 mutation | 需要 `remoteAccess: trusted-host` |
| `/dsh-mnemon-pack` | 备份导出与导入 | 需要 `remoteAccess: trusted-host` |
| `/dsh-mnemon-settings` | Host 与界面设置 | `get` 允许；`mutate` 需要 `remoteAccess: trusted-host` |
| `/dsh-mnemon-view` | 记忆组合读取 | 允许 |
| `/dsh-mnemon-view-settings` | 保存记忆组合、安装组件 | 需要 `remoteAccess: trusted-host` |

回环页面直接调用这些通道，由 DSH 浏览器会话认证。远程页面经 DSH API Gateway 到达同一组处理器：通道 `/api`，endpoint 为 `dshMnemon/read`、`dshMnemon/activation`、`dshMnemon/write`、`dshMnemon/pack`、`dshMnemon/settings`、`dshMnemon/view` 与 `dshMnemon/viewWrite`。Gateway 负责 Host/Origin 校验、浏览器配对与响应封装；Mnemon 只额外施加上表中的 `remoteAccess` 授权，并在启动时确定。见[远程管理](../guides/operations.md#远程管理)。

### 读通道

| Endpoint | 行为 |
|---|---|
| `status` / `status-summary` | 服务、版本、生命周期、档案、存储与工作区上下文及当前 Memory System 描述符；`status-summary` 不等待任何 Provider I/O |
| `memory-system` | Serving/候选评估、脱敏 Source 实例描述符与当前参与配置 |
| `versions` | Mnemon CLI 与 dsh-mnemon 的当前 / 最新版本和安装来源 |
| `task-agent-models` | 独立任务 Agent 可用的模型 |
| `runtime-memory` | 运行时快照 |
| `documents` / `document` / `document-search` | 档案目录、正文与确定性搜索 |
| `graph` / `bodies` / `body-directory` | active 多空间图谱投影、含 Provider 能力的记忆空间目录与快速目录投影 |
| `body-reconnect` | 清除短期健康状态并刷新单个记忆空间，不修改持久数据 |
| `provider-services` | 脱敏的 Provider 服务目录；可包含已配置的凭据字段名，绝不包含凭据值 |
| `embedding-status` | Mnemon 嵌入模型、可用性与默认 Store 覆盖率 |
| `list` / `entities` | 内容列表与实体聚合 |
| `search` / `agent-search` / `related` | 直接检索、证据回答与关系遍历 |
| `turn-activities` / `turn-activity` | 会话或单回合的记忆工具活动 |
| `assistant-message` | 按 messageId 读取已定稿助手文本 |
| `source-management-catalog` / `source-management-read` / `source-assistance` | 通用 Source 管理：实例目录、声明的读取操作与只读辅助检索 |

### 激活通道

`/dsh-mnemon-activation` 的请求 schema 比写通道更窄。`body` endpoint 只接受 `memoryBodyId`、布尔值 `active` 和常规 session / workspace 路由字段；`source-assistance` 以经确认的 `activation` 操作接受同样两个字段，仅作用于 Memory Spaces 实例。两者只控制记忆空间是否参与 DSH 读取与路由，不接受元信息、Provider 连接、凭据、删除或持久记忆 mutation。只读模式会在 Host 边界拒绝。

### 写通道

| Endpoint | 行为 |
|---|---|
| `runtime-memory` | 热记忆 mutation |
| `document` | create / update / archive |
| `supervise` | 用独立任务 Agent 处理候选并返回落定回执 |
| `remember` / `link` / `forget` | 长期语义写入、关系与软删除 |
| `body-create` / `body-update` / `body-delete` / `body-merge` | 创建或连接、编辑、确认后的 Native 删除或远程断开，以及合并记忆空间 |
| `body-metadata-maintain` | 由任务 Agent 刷新 1 到 20 个 active 记忆空间的名称与说明 |
| `provider-service-update` | 更新单个 Provider 服务；凭据仅在 Host 保存，响应脱敏 |
| `source-management-mutate` / `source-assistance` | 通用 Source 管理 mutation，以及按 Source 当前修订确认的 Host 辅助操作 |
| `version-update` | 更新明确组件；Host 固定命令与参数 |

`provider-services` 通过读通道返回脱敏目录。设置编辑器只知道哪些凭据字段已经配置；保存新的值或显式清除字段，不回读已保存的凭据值。

`writeEnabled=false` 时激活通道与写通道仍保持注册，但所有 mutation 都在 Host 边界拒绝。浏览器也会根据 Host settings snapshot 在传输前禁用 mutation 控件。

### 备份通道

| Endpoint | 行为 |
|---|---|
| `target` | 当前有效根、范围，以及“默认位置”对应的默认根 |
| `export` | 导出完整、带 manifest 与 SHA-256 校验的 ZIP |
| `inspect` | 解析并校验待导入 ZIP，返回组件与占用预览 |
| `import` | 把 ZIP 安全合并到当前有效根；只读模式拒绝 |

备份包含私有记忆；调用方必须把已认证 DSH 浏览器会话视为完整 Host 权限，并单独保护导出的归档。

### 设置通道

`/dsh-mnemon-settings` 为两个命名空间提供 `get` 与 `mutate`：`mnemon` 管理 Host 与存储设置，`mnemon-ui` 管理 `turnBar` 与 `saveAction`，保存为 `conversationInteraction`。mutation 携带 settings revision，防止覆盖并发编辑。插件页中的 dsh-mnemon 页面（`plugins.bundle.config`）在回环页面和远程页面上都通过该通道读写。回环页面还会跟随 DSH `ctx.configForms` 中 `mnemon` 条目的 revision，在其他页面或 profile 重新加载改动它时重新读取该通道。

### 记忆组合通道

| 通道 | Endpoint | 行为 |
|---|---|---|
| `/dsh-mnemon-view` | `dashboard` | 已安装组件、当前主策略、当前会话的 View 与回合活动，以及此处能否安装组件 |
| `/dsh-mnemon-view` | `preview` | 按当前修订校验拟保存的组合，不写入 |
| `/dsh-mnemon-view` | `inspect-plugin` | 读取组件包的 registry manifest：版本、`dsh-mnemon` peer 范围，以及当前 Profile 是否已安装 |
| `/dsh-mnemon-view-settings` | `apply` | 保存经确认的组合：主策略、组件开关与选项 |
| `/dsh-mnemon-view-settings` | `install-plugin` | 用 DSH CLI 把经确认的包版本安装到当前 Profile |

`apply` 把主策略与各组件选项保存在 `mnemon-view` 设置范围（保存为 `memoryView`）；组件开关由 DSH 插件管理器写入 profile patch。见[配置参考](./configuration.md)。

## npm 导出与扩展服务

Core 发布 `ctx.mnemonMemory: MnemonMemoryService`，实际服务只提供 `installContributions`。Source/Strategy 作者通过 `inject = ['mnemonMemory']` 与 `installMemory` 注册，不获得引擎、控制器或注册表。各包的公开子入口以其 `package.json#exports` 为准；Source 可提供自己的 `./contracts` 和可选 `./client`。

| 入口 | 职责 |
|---|---|
| `dsh-mnemon` | DSH Host 与默认 Starter |
| `dsh-mnemon/core` | 不带 Host/UI 的、Source 无关的 `ctx.mnemonMemory` 服务 |
| `dsh-mnemon/contracts` | JSON 安全的 manifest、facts、ViewSpec、View、Evidence、Receipt |
| `dsh-mnemon/extension-sdk` | Source/Strategy 定义、生命周期注册与校验 |
| `dsh-mnemon/testing` | 真实 Cordis 组合测试夹具与已构建 Client 制品加载器 |
| `dsh-mnemon/client` | DSH 工作台与 Source 页面 SDK |
| `dsh-mnemon-source-memory-spaces/provider-sdk` | Memory Spaces 自己的 Provider 子模块协议 |
| `dsh-mnemon-source-memory-spaces/testing` | Provider 驱动测试夹具 |

通用模型工具 `mnemon_view_route`、`mnemon_view_action` 只执行当前 View 内的 route/offer；原有具名工具保留默认工作流。浏览器管理使用 `source-management-catalog`、`source-management-read`、`source-management-mutate` 与可选 `source-assistance`，Host/Source 检查实例、确认、修订与当前权限。内部 RPC 名称不是插件 SDK，页面应使用限定实例的管理客户端。见[插件开发](../development/extensions.md)。

## 国际化范围

主要 Sidebar 工作台、插件页中的配置与对话内入口支持中文和英文，并跟随 DSH locale 实时切换。品牌名、工具名和配置键不翻译。`/mnemon` 命令、模型工具卡、部分 Host 错误与兼容元数据尚未完全国际化。
