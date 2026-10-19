# 兼容性与升级

**简体中文** | [English](../../en/reference/compatibility.md) | [文档中心](../README.md)

Starter 固定经过测试的官方插件组合。下表记录验证范围，不承诺所有上游版本或账号配置都已验证。

| 组件 | 基线 | 已验证的范围 |
|---|---|---|
| DSH | `0.2.0-rc.2`（npm `latest` 与 `next`）、`0.1.7-rc.2` | 两个受支持的宿主；`0.2.0-rc.1` 也可安装。0.1.7-rc.2 是锁定的开发基线：正式发布的契约、WebUI 与隔离 Headless 激活、profile 设置与旧设置恢复、Session V4 中的生产者专属消息、插件管理器激活以及 Agent Teams 审查矩阵。DSH 0.2 的验证范围见[下文](#dsh-02) |
| Node.js | `22.19`、`24` | 分别用于源码 CI 与打包制品 CI；开发要求 `^22.19.0 || >=24.0.0` |
| Node.js 20 | 仅公开包入口导入 | 不代表 DSH Host 能在 Node 20 运行 |
| Mnemon Native CLI | `0.2.9` | 显式启用的真实 CLI 与临时数据测试；CLI 需要另外安装 |
| 三方 Provider | 适配契约与夹具 | 不代表真实云账号一致性或上游服务可用性已验证 |

Root 的 DSH peer（`dsh-app-boot` 为可选）与记忆空间 Source 的 `dsh-client-ui-primitives` peer 均为 `^0.1.7-rc.2 || ^0.2.0-rc.1`。五个 Strategy 包使用新的扩展 SDK，要求 `dsh-mnemon ^0.5.17`；各 Source 保持 `^0.5.1`，Provider 则依赖记忆空间 Source。不再支持更早的 DSH：升级宿主前，请继续使用 dsh-mnemon `v0.5.16`，它是最后一个在 DSH `0.1.5-rc.1` 至 `0.1.7-alpha.1` 上验证的版本。较早的 Headless 和 WebUI 记录仅保留为历史证据。

“本回合记忆”以稳定 ID 注册到 DSH 的 `conversation.chat.turnTail` list 插槽，并在读取或展示活动前检查回合是否已完成。侧栏入口与**插件 → 可组合记忆**页面跟随 DSH 公开的默认／主会话 binding；会话标签页入口和 Better Sidebar 保留显式所属会话。DSH 0.1.7 将动态设置保存在 profile Config 中，并在“插件”中每个插件的页面编辑其配置；Mnemon 的配置页通过它写入，并按下述流程恢复保留的旧偏好。记忆数据和 Provider 格式不变。

参见[DSH 0.1.7 设置验证](../../pr-assets/issue-267-settings-migration/README.zh-CN.md)、[RC/alpha 验证与前后对比截图](../../pr-assets/issue-261-dsh-slots/README.zh-CN.md)、[DSH 0.1.5 验证](../../pr-assets/issue-223-dsh-015/README.zh-CN.md)、[宿主兼容证据](../../pr-assets/dsh-rc1-compat/README.md)、[升级证据](../../pr-assets/main-rebase-20260904/README.md)与[当前开发检查](../development/README.md)。机制测试通过不是 LLM 质量评测通过；特定 OS 与真实 CLI 检查在没有对应环境时可能跳过。

[v0.5.19 浅色图集](../../assets/webui-v0.5.19/README.md)覆盖双语桌面页面、插件页，以及 390 × 844 下的对话、记忆空间与记忆组合面板，该宽度下较长的名称会截断。它没有复测所有 Host 设置页或真实手机，因此不宣称完整支持手机；历史 v0.5.2 在 390px 下的布局问题保留[原版本证据](../../pr-assets/documentation-refresh/README.md)。

## DSH 0.2

DSH 在安装插件前，以及每次启动 profile 时，都会用自身版本检查插件所有 `@deepseek-ai/dsh` 与 `@deepseek-ai/dsh-*` peer 范围，预发布版本也参与比较。`^0.1.7-rc.2` 不包含 0.2.0，因此 dsh-mnemon 0.5.18 及更早的版本在 DSH 0.2 上安装时会被判为不兼容而拒绝，已安装的会在启动时停用。0.5.19 起，这些 peer 同时接受 `^0.2.0-rc.1`；`tests/dsh-host-compatibility.spec.ts` 对全部 18 个包的清单执行 DSH 自己的检查，覆盖 0.1.7-rc.2、0.2.0-rc.1 与 0.2.0-rc.2。

**升级顺序。** 先在现有 DSH 中把 dsh-mnemon 更新到 0.5.19 或更新的版本，再升级 DSH。若先升级了 DSH，旧版本会被停用，记忆数据不受影响；更新插件后即可恢复。不要用 `allow-version` 为旧版本放行。

**已验证的范围（0.2.0-rc.2）。** [v0.5.22 发布验收](../../pr-assets/release-v0.5.22/README.zh-CN.md)在全新的 0.2.0-rc.2 profile 上通过**添加插件**安装发布包，无需重启即可启用，并检查状态页、运行时记忆、Native 空间以及 CLI 与 WebUI 共享的检索。[侧栏入口记录](../../pr-assets/issue-318-sidebar-entry/README.zh-CN.md)显示其面板行与图标尺寸与 0.1.7-rc.2 相同。

**已验证的范围（0.2.0-rc.1）。** 全局安装正式 DSH 0.2.0-rc.1 后，从空白 profile 通过命令行与插件页两种方式安装、界面中“立即启用”无需重启、状态页、运行时记忆写入、真实模型的首轮对话与存入记忆、Headless 任务，以及桌面版窗口与远程页面的读写路由；另将全部 DSH 开发依赖切换到 0.2.0-rc.1：类型检查、构建、全部插件测试与 Headless 验证通过，根测试中只有核对锁定开发基线本身的断言不同。截图见[安装图集](../../assets/install-v0.5.19/README.md)。

**首次安装时的宿主行为。**

- 插件页的**添加插件**会在用户未选择过时测速，在中国大陆通常默认使用**中国大陆镜像源**，其他地区使用 **npm 官方源**。
- pnpm 11 默认不选用发布不足 24 小时的版本。新版本发布当天，直接安装 `dsh-mnemon` 可能装上旧版本，并在 DSH 0.2 上被判为不兼容；此时安装带版本号的 `dsh-mnemon@<版本>`，或等待 24 小时，见[安装与启动](../guides/installation.md#常见问题)。带版本号安装会固定版本，之后用 `dsh plugin --profile web update --latest dsh-mnemon` 升级。在同样的 24 小时内，`update` 与 `update --latest` 都会停留在已安装的版本，已有安装也要带版本号安装新版本。
- 安装结果卡片显示软件包的英文简介；插件列表中的名称与说明跟随界面语言。
- `desktop` profile 归桌面版所有，DSH 0.2 的命令行拒绝管理它；桌面版用户在应用的插件页中安装和管理插件。

## 桌面版窗口

DSH 桌面版窗口从应用自己的 `dsh-app://app/` 地址加载，而不是回环地址；DSH 0.1.7 桌面版也不为页面声明传输方式。dsh-mnemon 0.5.18 及更早的版本因此把桌面窗口当成远程页面：所有调用经 API Gateway，默认的 `remoteAccess: read-only` 使运行时记忆、记忆空间与插件设置在界面中只读，而 Agent 工具仍可写入（[#310](https://github.com/omdsh-dev/dsh-mnemon/issues/310)）。

0.5.19 起，应用自己提供的页面（非 `http:` / `https:` 地址）使用 Mnemon 的本地通道；只有 DSH 为页面声明了不持有 Host 的传输方式时，才仍按远程页面处理。DSH 0.2 桌面版声明 `ownsHost: true`，同样使用本地通道。从其他设备打开的页面继续经 API Gateway、默认只读，并在记忆系统与插件设置中写明需要 `remoteAccess: trusted-host` 与重启 DSH。本地通道与 `/api` 使用 DSH 相同的 Host/Origin 校验与浏览器会话认证。

验证方式：模拟桌面壳的 Electron 窗口注册与官方桌面版相同的 `dsh-app` 标准安全协议，把请求代理到只监听 127.0.0.1 的 Host。已发布的 0.5.18 在 DSH 0.1.7-rc.2 上复现了只读（13 次 Mnemon 调用全部经 API Gateway）；修复后在 DSH 0.1.7-rc.2 与 0.2.0-rc.1（`ownsHost: true`）上均可添加运行时记忆，写入经本地 `/dsh-mnemon-write`，记忆空间与插件设置可编辑；通过可信 authority 打开的远程页面仍然只读并显示原因。

## Desktop profile generation

Desktop 可能删除插件 generation 内的私有 `@deepseek-ai/*` 包，改用宿主自己的框架版本。Mnemon 使用宿主公开的 Volatile API 构建动态设置 schema，DSH `0.1.7-rc.2` 通过 Cosmokit `1.8.5` 提供这些 API。宿主若携带不含这些 API 的旧版 Cosmokit（例如 `1.8.3`），即属于不受支持的宿主，请在该环境继续使用 dsh-mnemon `v0.5.16`。

受影响的 `0.5.13` 安装中，Desktop 的回退分支会将缺少 `createVolatile` 导出的真实错误隐藏为 `Cannot find package 'dsh-mnemon'`。平铺 `mnemon-bundle` 不能修复该导入失败。Starter 保留 group、根停用总开关、独立 Source/Strategy 选择、私有 Provider 子项和已有设置命名空间。在所属 profile 更新 Mnemon 后重启即可；本修复不需要迁移记忆或配置。

参见[原版 Desktop 复现与验收记录](../../pr-assets/issue-274-profile-generation/README.zh-CN.md)。

## 安装后直接启用 Starter

DSH `0.1.7-rc.2` 的 Desktop generation 目录和 pnpm 安装可能只在 profile 中暴露 `dsh-mnemon` 根包。各组件已在它的依赖树中安装，但如果宿主启动时未选中该 bundle，首次启用仍可能使用启动时的包解析表，导致 Runtime、Documents、Memory Spaces 和默认 Strategy 抛出 `ERR_MODULE_NOT_FOUND`。

Starter 的组件组 `mnemon-bundle`（模块 `dsh-mnemon/bundle`）先准备依赖解析，再用 DSH 自己的 `cordis:group` 挂载子组件。它调用宿主公开的包解析服务，保留其他 bundle 启动时的依赖路径，并由宿主拒绝不兼容的模块重绑定；不平铺组件包、不改写 profile 链接、不修改 DSH。已有 Entry ID、核心停用总开关、各组件的独立选择和记忆数据均保持不变。0.5.18 与 0.5.19 由单独的就绪条目 `dsh-mnemon/starter`（`mnemon-starter`）完成这一步，组件组等它提供 `mnemonStarterReady`；0.5.20 起两者合为一个条目，插件页不再显示单独的就绪行，组件组也不会因为它被关闭而一直等待。

Mnemon 同时在自己的传输注入作用域内注册 Web RPC，已运行的官方 Connection 无需重启。使用 bundle 或核心开关控制整套组合。在 0.5.18 与 0.5.19 中关闭 `dsh-mnemon/starter`，`mnemon-bundle` 会一直等待 `mnemonStarterReady`：`dsh web` 照常启动但没有记忆系统，桌面版则按启动失败处理，见[安装与启动](../guides/installation.md#dsh-提示waiting-for-service-mnemonstarterready)。更新到 0.5.20 后，profile patch 中残留的 `- id: mnemon-starter` 不再对应任何条目，宿主会忽略它（桌面版日志提示 `patch: entry "mnemon-starter" not found`），可以删除这两行。

代价在 DSH 的配置 schema 导出：`dsh web --dump-config-schema` 只识别原生 `cordis:group` 与 `cordis:include`，会把 `mnemon-bundle` 报告为无法识别的树载体，并略去其中各组件的 schema，与 DSH 自带的 agent preset 相同。运行时配置、插件页与 profile patch 不受影响。

官方 WebUI 首次安装后，可以在同一宿主进程中点击“立即启用”；已安装但停用的 bundle 也可直接启动。更新或卸载 Node 已加载过的包时，仍须遵循 DSH 的正常重启要求，本修复不替换已加载模块。例如原地更新后不重启就启用组件，会提示新版本才有的入口未导出（`ERR_PACKAGE_PATH_NOT_EXPORTED`）：从 0.5.17 或更早的版本更新时是 `./starter`，从 0.5.18 或 0.5.19 更新时是 `./bundle`。加载器不会为运行中的条目更换模块，因此从 0.5.18 或 0.5.19 更新时，也可能是运行中的旧组件组一直等待已移除的 `mnemonStarterReady`（`mnemon-bundle (dsh-mnemon/bundle): pending …`）。重启后两者都会恢复，见[升级验收记录](../../pr-assets/starter-group-upgrade/README.zh-CN.md)。参见[安装与启用验收记录](../../pr-assets/desktop-live-activation/README.zh-CN.md)与[组件组验收记录](../../pr-assets/starter-group-readiness/README.zh-CN.md)。

## DSH 0.1.7 bundle 组件列表

DSH `0.1.7-rc.2` 的“插件 → dsh-mnemon”详情页会把 `mnemon-bundle` 内部容器列为“已关闭”的组件（0.5.20 起显示为 `dsh-mnemon/bundle`，此前为 `cordis:group`），DSH `0.2.0-rc.1` 的显示相同。9 个实际组件全部运行时，列表显示“共 10 个 · 9 运行中 · 1 已停用”；0.5.18 与 0.5.19 多一行就绪条目，对应为“共 11 个 · 10 运行中 · 1 已停用”。点击容器开关会返回 `unknown-plugin`，中文界面提示“组件启用失败：找不到该插件”。参见[原始截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/before-bundle-toggle-error.jpg)与[上游问题 #649](https://github.com/dsh-external/issues/issues/649)。

这是宿主的展示与管理清单不一致：bundle 声明列表包含原生 group，但可管理插件清单明确排除了 group。该行的“已关闭”不代表 Mnemon 核心或其子插件已停用，也不能据此判断记忆读写是否正常。

遇到这一现象时：

1. 查看“记忆系统 → 状态”以及实际 Source、Strategy 组件的状态。如果只有内部容器误显示关闭，而所需组件和读写正常，可以继续使用；实际组件报错或读写失败仍需单独排查。
2. 停用或恢复整套组合时，使用顶层 `dsh-mnemon` bundle 开关，或对应 `mnemon` 条目的核心组件开关；不要使用 `mnemon-bundle` 容器行的开关。可组合记忆配置页在组件列表上方也注明了这一点。
3. 保留已有配置和记忆。此显示问题不需要重置数据，也不需要迁移配置或记忆。

Starter 保留稳定的 group ID 和已有 `mnemon` 配置目标。停用核心会停止其 Source、Strategy 和私有 Provider 子项；重新启用后，各组件恢复各自的独立选择。移除 group 会让仍启用的依赖项等待缺失的核心；将 group 改为匿名条目则可能在 profile 重载后留下旧实例。不要通过删除容器、稳定 ID 或修改分组声明来隐藏这一行。[正式宿主生命周期回归](../development/README.md#测试归属与覆盖)在不修改已安装宿主的前提下，验证管理器持久化、重启及旧版字面值／表达式停用标志。

已在独立环境验证的[上游候选补丁](../../pr-assets/sidebar-native-20260926/upstream-fix.patch)会过滤容器展示并保留实际子插件；它未包含在正式 DSH `0.1.7-rc.2` 或 Mnemon `v0.5.17` 中。本地管理适配器方案需要接管 DSH 全局插件管理服务，把它内置于 Mnemon 会影响 Mnemon 的独立停用，因此未随插件提供。修复进展以 [#649](https://github.com/dsh-external/issues/issues/649) 及后续 DSH 发布说明为准；候选环境的截图不代表正式宿主已修复。

## DSH 0.1.7 设置恢复

DSH 0.1.7 用基于 Config 的表单替代了 `settings.register()` 和 `settings-file`。Mnemon 提供动态 Config 字段，现有界面操作通过宿主检查 revision 的 profile 写入器持久化；修改传输权限仍需正常重载插件。

Mnemon 消息使用 Session V3、V4 均接受的生产者专属来源 `dsh-mnemon`。过滤和去重仍识别历史包装，以及 DSH 迁移后的 `plugin:dsh-mnemon` 消息；本改动不会重写已有 Session 文件。

启动时，Mnemon 可将 `<profile home>/settings.yaml.imported` 中保留的备份恢复到可编辑的根 `mnemon` 条目，包括根设置、`mnemon-ui` 对话开关，以及仅属于当前 profile 的精确 `mnemon-view-*` / `mnemon-plugins-*` 命名空间。当前 profile 的显式值优先，包括显式清空的插件选择；已有 Source 和 Strategy 条目保持原样；Strategy 条目使用动态表达式时，会保留原表达式并跳过该条目的旧覆盖值，避免将当前求值固定下来。恢复后会在根 Config 中记录 `legacySettingsImported: true`，备份文件保持不变。

若 Mnemon 启动时原 `settings.yaml` 仍存在，由 DSH 执行导入；完成后再重启一次该 profile，恢复 Mnemon 专属偏好。DSH 未公开导入完成信号，因此 Mnemon 等待新宿主进程，避免并发写入。只读或归属不明确的根条目不会被修改；相关旧设置节损坏时，不做部分导入，也不标记完成。请保留备份，参考启动诊断后再修复；不会猜测自定义根 ID 或其他 profile 的 hash。

回滚 DSH 时，保留 profile patch 和旧设置备份，并随旧宿主重新安装 dsh-mnemon `v0.5.16`。0.1.7 上后来修改的设置不会自动反向导出到旧设置文件，需要恢复对应备份或在旧宿主重新设置；Runtime、档案、记忆空间与 Provider 数据不受影响。

## 升级默认安装

1. 导出 Mnemon Pack；需要保留 Provider 连接时，按[运维指南](../guides/operations.md#备份与恢复)保护额外备份。
2. 在每个使用它的 DSH Profile 中升级 `dsh-mnemon`。Starter 安装精确的插件版本；不要单独更新其子包后，就假定混合组合已经验证。
3. 安装新包代码后重启 DSH。检查“记忆系统 → 状态”，再读取一条已有 Runtime、档案及已激活记忆空间。
4. 写入前确认存储范围。切换范围只是选择另一份数据权威，不会迁移数据。

v0.5 保留默认 v0.4 的存储、配置与侧栏工作流。可选的会话标签页入口（`builtin`）使用同一组 Source 页面，旧 `buildin` 拼写会规范化。通用策略与三个记忆增强默认关闭。插件页中的记忆组合负责列出、开关和配置已安装的组件；没有单独的 View 页。

独立插件作者使用声明的 peer 范围与公开出口；旧的私有控制器导入不属于受支持的升级表面。自定义组合需要独立于 Starter 验证，参见[插件开发](../development/extensions.md)与[v0.5.0 发布边界](../releases/v0.5.0.md)。
