# 长期记忆 Provider

**简体中文** | [English](../../en/guides/memory-providers.md) | [文档中心](../README.md)

记忆空间保存 dsh-mnemon 的长期记忆，其后端是可以替换的 Provider：记忆空间契约保持稳定，Provider 负责具体数据面。**Mnemon Native 是官方优先、默认实现**；三方 Provider 是显式选择的集成，适合已经使用其他记忆引擎，或需要不同共享、提炼与召回模型的团队。

只有 Mnemon Native 需要 Mnemon CLI。缺少 CLI 时，其他已就绪的 Provider 也可以为记忆空间服务，已保存的 Provider 选择依然有效。

![插件中的记忆空间页面：Mnemon Native 与各第三方 Provider](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/webui-v0.5.19/zh-CN/plugin-spaces.jpg)

每个适配器都是独立发布的 `dsh-mnemon-provider-*` 包，由 Memory Spaces Source 作为子插件安装。Starter 随附九个包，外部服务仍需显式配置后启用；不捆绑外部后端服务或 CLI。参见[官方包列表](../../../README.zh-CN.md#官方插件)和 [Provider 作者契约](../development/extensions.md)。

## Provider 能力矩阵

| Provider | 数据面 | 召回 / 浏览 | 图谱 / 关联 | 写入 | 遗忘 |
|---|---|---|---|---|---|
| **Mnemon Native** | 官方 CLI 操作本地 `mnemon.db` | 支持 / 支持 | 完整类型图谱 / 支持 | 精确写入 | 软删除 |
| **OpenViking** | 已有 HTTP 服务与 `viking://` 记忆根 | 支持 / 支持 | 投影节点 / 不支持 | 经验证的精确正文 | 仅允许精确用户 `.md` 资源的受控硬删除 |
| **Honcho** | v3 工作区 conclusions | 支持 / 支持 | 不支持 / 不支持 | 精确 Peer conclusion | 硬删除 |
| **Mem0** | Platform v3 或自托管 HTTP API | 支持 / 支持 | 不支持 / 不支持 | 异步提炼 | 硬删除 |
| **Hindsight** | Memory bank API 与知识图谱 | 支持 / 支持 | Provider 图谱 / 支持 | 异步 retain | invalidation（软删除） |
| **Holographic** | 本地原子结构化事实文件 | 支持 / 支持 | 实体/语义图谱 / 支持 | 精确事实 | 硬删除 |
| **RetainDB** | Project/User 作用域 HTTP API | 支持 / 支持 | 不支持 / 不支持 | 精确记忆 | 硬删除 |
| **ByteRover** | 本地 `brv` CLI 与知识目录 | 支持 / 不支持 | 不支持 / 不支持 | 异步 curate | 不支持 |
| **Supermemory** | Container 作用域 HTTP API | 支持 / 支持 | 投影节点 / 不支持 | 异步文档摄取 | Provider forget |

Host 只暴露适配器能够兑现的能力；UI 与 Agent 工具不会伪造缺失的图谱、关联、链接、浏览或删除语义。

<a id="服务与记忆体字段"></a>

## 服务与记忆空间字段

| Provider | 工作区行为 | 记忆空间页面中的服务配置 | 记忆空间中的实例配置 |
|---|---|---|---|
| OpenViking | 保持 Provider 全局作用域 | `endpoint`、`apiKey`、`account`、可选 `discoveryUser` | `targetUri`、`user`、`actorPeerId` |
| Honcho | 保持 Provider 全局作用域 | `endpoint`、`apiKey` | `workspace`、`userId`、`agentId` |
| Mem0 | 保持 Provider 全局作用域 | `endpoint`、`apiKey`、`mode` | `userId`、`agentId`、`rerank` |
| Hindsight | 保持 Provider 全局作用域 | `endpoint`、`apiKey` | `bankId`、`budget` |
| Holographic | 默认随工作区，可由路径覆盖 | `dataPath` | `defaultTrust`、`minTrust` |
| RetainDB | 保持 Provider 全局作用域 | `endpoint`、`apiKey` | `project`、`userId` |
| ByteRover | 默认随工作区，可由目录覆盖 | `cliPath`、`apiKey`、`defaultDirectory` | `workingDirectory` |
| Supermemory | 保持 Provider 全局作用域 | `endpoint`、`apiKey` | `containerTag`、`searchMode` |

“记忆空间”页面（在**插件 → 可组合记忆**中点击“记忆空间”）的“记忆 Provider”只保存 Provider 服务配置，不创建记忆空间；同一范围内该 Provider 的所有记忆空间复用它。“**记忆空间 → 概览**”负责创建、编辑、启停与删除记忆空间，并只呈现 workspace、user、bank、container、target URI 等实例范围。Host 在调用适配器前合并两层配置。Secret 保存在 `<storageRoot>/state/memory-providers.json`，权限为 `0600`；WebUI 只以掩码表示已配置的 Secret，输入新值即可替换。

DSH 的“工作区”模式不会统一重写所有 Provider 命名空间。Mnemon Native 自动随工作区切换；Holographic 与 ByteRover 默认使用工作区下的本地路径，但允许显式路径覆盖；其余远程 Provider 继续使用记忆空间中配置的 URI、workspace、user、bank、project 或 container，切换 DSH 工作区不会隐式改写这些身份。

## 手动与智能选择

手动模式保留原工作流：创建记忆空间、选择一个引擎、完成配置，之后仍使用同一套检索、内容、实体和沉淀入口。

智能模式从用户勾选的候选建立 allowlist：

1. Host 先强制执行数据边界与必需能力；
2. 只剩一个合格 Provider 时，由规则确定性选择；
3. 有多个合格候选时，独立任务 Agent 结合路由说明、软偏好和用户策略 Prompt 判断；
4. Host 再验证结果属于合格集合，并保存选择来源、理由、置信度与候选 ID。

连接凭据永远不会进入 selector Prompt。`local-only` 会在模型选择前排除全部远程 Provider。Mnemon CLI 可用时，Mnemon Native 始终是本地候选。

## 运维边界

- OpenViking 使用 `content/write`、`mode: "create"`、`wait: true` 和来源/分类标签创建新的 `.md` 文件。分类分别映射至 `preferences`、`experiences`（insight/decision）、`events`（context）或 `entities`（fact/general）。只有 URI、字节数、向量索引完成状态和公开完整正文读回均匹配时才返回 stored；记忆文件的语义处理状态允许为 `skipped`。召回与浏览读取完整正文，不把摘要当作原文；这些写入不再使用会话抽取或 LLM 提炼。
- OpenViking 服务须支持上述正文 API；可选集成测试已覆盖正式发布的 v0.4.20 后端。默认通过 Admin API 发现，需配置 `account` 与能枚举该账号用户的 key。没有 admin 权限的 user key 可在 `endpoint`、`apiKey`、`account` 之外填写 **User Key 所属用户（跳过 Admin）**（`discoveryUser`）。这会显式选择 `viking://user/<discoveryUser>/memories`，通过只读 `GET /api/v1/fs/ls` 验证后同步一个空间。此模式不发送 account/user 身份请求头，由服务端从 key 解析身份；`account` 标识本地映射，不能覆盖 key 所属租户。账号和用户标识应向服务管理员获取。根目录不存在、访问被拒或响应无效时拒绝保存并保留原配置；不会只凭 health 成功保存，也不在 admin 错误后自动降级。云服务可用性与写入权限需要另外验证，确定性测试不代表真实云账号认证。参见上游[身份验证](https://github.com/volcengine/OpenViking/blob/main/docs/en/guides/04-authentication.md)与[托管服务入门](https://github.com/volcengine/OpenViking/blob/main/docs/en/getting-started/02-quickstart.md)。
- 已有显式 `viking://user/<user>/memories` 根保持有效；旧 `viking://user/memories` 通过已配置 `user` 展开，未配置时使用经过身份验证的 system-status 用户信息。user-key 发现使用显式所属用户，拒绝不匹配的空间用户。拒绝危险路径成分以及所选用户根以外的删除。注册表格式不变；降级前用当前版本清空 `discoveryUser` 并使用具有 admin 权限的配置，或禁用服务并恢复兼容配置。远端记忆保持不变。
- OpenViking 错误、索引未完成和超时不会返回已提交回执。远端文件可能已经存在，错误会附带请求 URI，重试前应检查该文件；不自动回退到抽取或重试。经过验证的回执包含精确文件 `id`，使 Host 能在后续本地归档失败时只删除本次新建索引。结果不明的远端写入保留待检查。升级或回退适配器均不会删除已有远端文件；降级会恢复旧写入行为。
- WebUI 不直接调用远程服务或本地 CLI；Provider I/O 都留在 Host，统一具备取消、超时、进程输出上限和 shell-disabled 参数执行。
- “断开”三方记忆空间只删除本地目录登记，不删除底层数据。单条记忆的“遗忘”是另一项按能力开放的操作。
- Holographic 是对本地结构化事实语义的 TypeScript 适配，使用原子 JSON 存储，并保持独立的数据格式与生命周期实现。
- Hindsight 使用轻量存活检查，并从 Provider 的 bank stats、实体目录与图谱响应读取真实统计、实体和关系；旧版缺少统计接口时仍可使用召回与图谱表面。
- ByteRover 只开放聚焦的 `status`、`query` 与 `curate`；不会虚构广域知识树浏览和删除能力。
- Supermemory 的浏览结果合并已抽取 memory entries 与仍可浏览的 ingested documents，并按 Provider ID 去重；文档未完成抽取时也不会从“内容”页消失。
- Mnemon Pack 包含 Mnemon Native 记忆空间、运行时与档案；三方连接、凭据、本地三方 Store 与远程数据都不进入 Pack。
- 外部产品的可用性、价格、隐私、保留策略和许可证由各自运营方决定。把私有记忆发送给远程 Provider 前应先评估这些边界。

来源归属与许可证边界见[第三方声明](../../../THIRD_PARTY_NOTICES.md)。
