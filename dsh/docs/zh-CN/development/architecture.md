<a id="composable-view-memory-架构"></a>

# 可组合视图记忆架构

**简体中文** | [English](../../en/development/architecture.md) | [文档中心](../README.md)

可组合视图记忆（Composable View Memory）只有三个主要业务概念：**Source（记忆来源）** 拥有记忆及其操作；**Strategy（组合策略）** 提议可用 Source 如何参与；**View（上下文视图）** 是特定范围和场景下交给 LLM 的有界上下文与交互形态。运行时、档案、记忆空间是默认组合，不是 Core 对所有记忆的固定分类。

## 归属与装配

```mermaid
flowchart TB
  Starter["dsh-mnemon 默认安装包 · cordis.patch.yml"] --> Host["宿主 · ctx.mnemonMemory"]
  Starter --> Runtime["dsh-mnemon-source-runtime"]
  Starter --> Docs["dsh-mnemon-source-documents"]
  Starter --> Spaces["dsh-mnemon-source-memory-spaces"]
  Starter --> Strategy["dsh-mnemon-strategy-default-three-tier · 分层策略，默认"]
  Starter --> General["dsh-mnemon-strategy-general · 可选主策略，默认停用"]
  Starter --> Helpers["三种随附增强 · 默认停用"]
  Helpers -. 选择 / 投影 / 记录 .-> Strategy
  Spaces --> Providers["dsh-mnemon-provider-* · 私有子 Fiber"]
```

图中实线表示 Starter 安装归属，虚线表示用户开启后才生效的策略贡献，不是业务调用链。DSH 创建顶层 Entry/Fiber；Source、Strategy 与策略贡献使用同一套 `installMemory(ctx, ...)` SDK 注册，由 Cordis 负责卸载。Core 只提供 `ctx.mnemonMemory`，不替 Memory Spaces 实现 Fiber，也不提供 `ctx.mnemonMemorySpace`。

Memory Spaces **自己定义内部 Fiber 与 Provider 协议**。每个 Provider 都来自明确安装并配置的子模块；两个 Source 实例可以使用同名子节点，各自持有独立目录和凭据。不存在扫描依赖自动选择实现、全局 Provider 注册表等隐式装配。

借鉴 Spring Boot Starter，默认发行包负责选依赖、给默认配置，不把 Source 业务收回 Core。用户仍只安装 `dsh-mnemon`；17 个插件包可独立开发、测试与发布。Starter 安装全部官方包，其中通用策略与三个策略增强以停用 Entry 随附：增强在开关打开后参与 View，通用策略只有被选为主策略后才组合 View。更换主策略始终需要显式选择。

| 归属 | 负责 | 不负责 |
|---|---|---|
| Core | 内部注册、协议校验、不可变 View、预算、运行代与租约 | Provider 驱动、Source 数据格式与存储决策、页面、DSH 生命周期策略 |
| SDK | 小型贡献服务、Source/Strategy 作者契约、安装辅助与限定范围的测试工具 | 引擎/注册表构造器、已安装记录、运行代句柄 |
| Source | 数据权威、facts、投影、grant、查询/修改、可选管理协议与 Client | 其他 Source 的控制器、全局策略选择 |
| Strategy | 确定性的纯函数 `request + facts + 自有槽贡献 → ViewSpec`，拥有槽语义 | 原始数据、凭据、驱动、副作用、扩张权限 |
| Host | scope、阶段 hook、工具/RPC、认证、设置、监督任务 | Source 私有实现与注册表 |
| Starter | 包集合、Entry id、默认配置 | 第二套加载器或运行时 |

`ctx.mnemonMemory` 是真正受限的服务对象，不是给引擎套一个更窄的 TypeScript 类型。它只暴露一个注册原语，由 `installMemory` 使用；Host 执行能力留在内部。Provider 在自己的 Source 内遵循同样原则，只收到绑定子节点的 `host.install` 能力。公开测试夹具验证这些协议，不交出私有宿主对象。

## 内置存储范围

存储布局属于根包的 Host 基础设施。同一解析器处理 `global`、`workspace`、`custom` 与 `workspaces`；Core 传递选定的操作范围并执行 View 权限约束。文件系统路径归一化、环境变量与用户目录默认值、工作区目录哈希留在 Host，不新增存储贡献 API 或独立发布包。

Host 将解析后的目录交给各默认 Source。Source 继续拥有自己的格式、事务和 Provider 状态；切换布局不搬迁其数据。显式选择全局 USER.md 时仅用户档案使用全局根，项目数据仍留在所选工作区根。设置和只读清单使用同一布局解析器。

## 默认插件组合

| 插件 | 记忆权威 | 默认 View 贡献 |
|---|---|---|
| `dsh-mnemon-source-runtime` | Runtime JSON、USER/MEMORY 投影、分支过滤与容量 | eager 精确工作上下文 |
| `dsh-mnemon-source-documents` | 受管 Markdown、索引、搜索、修订与归档 | 有界叙事封面与搜索 route |
| `dsh-mnemon-source-memory-spaces` | 记忆空间目录、内部 Provider、能力与召回质量策略 | 有界持久证据封面与 recall/related route |
| `dsh-mnemon-strategy-default-three-tier` | 不存储记忆 | 分层策略：选择三种角色，分配投影、route 与 action |
| `dsh-mnemon-strategy-general` | 不存储记忆 | 可替代分层策略：在同一份预算内接入全部可用 Source，由模型决定路由 |

九个独立 Provider 插件包为 `dsh-mnemon-provider-{mnemon-native,openviking,honcho,mem0,hindsight,holographic,retaindb,byterover,supermemory}`。Provider 运行在 Memory Spaces **内部**，负责存储/检索驱动，不是 Core 的新贡献种类。Git、Notion、健康记录通常应实现 Source；不同的组合方式应实现 Strategy。

未加扩展的分层策略对同一默认角色出现多个实例报歧义错误。可启用 `strategy-scoped` 显式组合多个实例；停用后恢复默认歧义检查，不按加载顺序猜测。`selection`、`projection` 与 `capture` 三个扩展槽是标准槽，两个随附主策略都声明了它们，因此更换主策略后增强仍然生效。Core 只传递有界贡献并执行原有预算/权限校验，不理解这些槽的业务语义。

## View 数据流

```mermaid
flowchart LR
  Facts["Source 能力事实"] --> Strategy["Strategy → ViewSpec"]
  Strategy --> Core["Core 校验"]
  Core --> Project["Source 投影 + ReadGrant"]
  Project --> View["不可变 View"]
  View --> Wake["Wake → LLM"]
  View --> Route["读取 / 写入 → 所属 Source"]
  Route --> Result["证据 Evidence / 回执 Receipt"]
```

View 包含投影片段、route、action offer 与仅留在 Host 的 ReadGrant。Wake 只渲染有界模型表示，并包含可调用 route/action 的 schema；不泄露 grant 载荷、控制器或凭据。Evidence 记录来源与一致性，不是另一份持久记忆。

Strategy 输出只是提案。Core 校验实例身份、声明能力、route/action 及预算；Host 执行时再次检查当前权限。一致性明确区分：捕获内容的 `exact-snapshot`，以及只能固定命名空间、远端内容仍可变化的 `namespace-pinned-live-read`。后者不冒充历史数据库快照。

```mermaid
sequenceDiagram
  participant DSH
  participant Host
  participant Core
  participant Source
  participant LLM
  DSH->>Host: 回合开始（范围、场景）
  Host->>Core: 获取当前服务运行代并组合
  Core->>Source: 获取能力事实；按 Strategy 选择调用投影
  Source-->>Core: 片段 + 不透明 ReadGrant
  Core-->>Host: 不可变 View
  Host->>LLM: 自有插件消息：有界 Wake + 读取/写入入口
  LLM->>Host: 选中的读取/写入入口 + 输入
  Host->>Core: 检查范围、权限与预算
  Core->>Source: 查询 / 修改
  Source-->>LLM: 经 Host 返回有界证据 / 提交回执
  DSH->>Host: 回合结束
  Host->>Core: 释放租约；排空已退役运行代
```

## 生命周期与失败

Host 的 `MemoryExecutions` 统一持有 Core 回合及对应的运行时绑定。Agent 生命周期与后台协调器使用同一份所有权：并发维护共享一个 View，新回合等待子任务及工具清理完成后，再选择当前运行时。是否中止闲置整理仍由生命周期决定；执行所有者不会中止无关的已授权写入，也不会增加权限。

候选组合先验证，再发布。额外候选被拒绝时，不会悄悄替换 Serving 运行代；显式移除当前依赖的贡献时，该运行代退役，不再接新回合。已开始的回合和操作持有租约，结束后再排空旧 Source 及其私有资源。

每回合固定一个不可变 View。写入生成回执，后续回合读取新修订；并发父/子 Agent 不能借用另一回合的 grant。Source/Provider 故障保持局部、可观察；部分成功、失败、取消、已提交不会混为一谈。停用参与不删除数据。

子 Agent 在派发时捕获委托 View 及运行代，保留至自身释放，不因父回合结束或 Serving 换代而改变。每次子执行具有独立回合身份与检索预算，不会回退到父 Agent 的最新 View。Wake 以 `dsh-mnemon` 自有插件消息追加在共享上下文之后，不重复注入或插值其他插件的上下文。

## WebUI 与管理面

默认 Source 在自己的 `presentation/` 中持有双语文案与布局资源，并由自己的 Client 加载；Starter 也通过公开包路径组合这些资源，保留既有可选页面工具的导出。工作台负责导航、容器和主题，不再掌握 Source 专属选择器。这是默认产品的呈现组合，不是 Core 要求或新的 UI 框架。

Source 自己拥有可选的 `./client` DSH 模块、页面、管理协议与测试，通过公开 Source 页面 SDK 注册到工作台的 `mnemon.source.page` Slot。Client 生命周期与 React 渲染仍由 DSH 管理。

Host 交给页面的是限定实例的管理客户端与脱敏元信息，不是裸 RPC、Host Context 或 LLM grant。读取和带确认、修订栅栏的修改指向一个 Source。档案归档到记忆空间等默认协作由 Host 编排，也只调用公开管理协议。

同一个共用工作台提供两个互斥的 DSH 入口。Sidebar 在受支持的 DSH（0.1.7-rc.2 与 0.2.0-rc.2）公开的 `sidebar.panellist` 与 `main` Slot 中注册配对的 `mnemon` 入口。DSH 负责按钮、图标尺寸、标签、选中状态和主面板导航。`shell.overlay` 注册保留 Source 子 Slot 的渲染权限，将工作台 portal 到持久的原生主面板挂载点，从而保留切换面板时的页面状态和独立的 Better Sidebar 挂载点。未提供原生面板契约的替换布局继续使用现有入口及浮层回退。Sidebar 无会话也可打开，保留自己的工作区选择，与 Taskboard/SSH 协调，并通过 `layout.selectPanel(null)` 返回会话。会话标签页（`builtin`）使用 `conversation.view`，读写和任务均使用所属会话的存储范围，不提供独立工作区选择器。两个入口渲染同一组 Source 自有子 Slot，不创建第二个 React root、兜底页面注册表或复制业务页面。

## 使用兼容与未来演进

默认 Starter 保留存储选择、持久格式、具名工具及使用流程。`displayMode` 选择默认的 `sidebar` 或 `builtin`；Host 接受旧 `buildin`，并在配置可写时通过 DSH 带修订栅栏的设置写入器保存规范拼写。这只修改一项偏好，不改变记忆数据或 Core/Source 契约；其他配置键保留原有语义。这**不意味着**保留私有控制器、旧根包 `kernel/layers/provider-sdk` 入口和历史包装包。当前公开入口见[扩展开发](./extensions.md)。

RSI 的边界保持简洁：产出候选 Source/Strategy 制品，用固定 facts/request 测试与回放，审查权限，再按正常装配方式安装和选择。运行代支持验证后的替换与排空，不是自动执行生成代码或自动晋升服务。Cordis 的归属/隔离并非安全沙箱；高风险外部操作仍需单独的授权边界，不能以“记忆”之名自动获得权限。
