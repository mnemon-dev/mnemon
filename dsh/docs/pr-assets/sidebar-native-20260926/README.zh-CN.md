# 原生 Sidebar 与 bundle 验证

[English](./README.md)

基线：`6d79c663262f71c4309cd6db5bd70b204abb143d`（v0.5.15）。实测实现：`8d3a16ec`。环境为 Node 24.20.0、正式发布的 DSH 0.1.7-rc.2、全部 17 个 Mnemon tarball，以及官方 Mnemon CLI 0.2.9。[验证记录](./verification.json) 包含制品哈希和检查结果。

## 原生导航

main 中，记忆系统在 DSH 原生面板列表外插入自己的入口，字号和间距与“插件”不同；点击“插件”后，记忆浮层还可能遮住已选页面。

修复通过 `sidebar.panellist` 提供图标和标签，通过配套 `main` 挂载位及 `layout.selectPanel` 导航。按钮、字体、提示、选中状态和折叠布局都由 DSH 管理。Source 页面树仍由 shell 持有，通过 portal 进入主面板，保留编辑状态与 Source 渲染归属。Better Sidebar 与替代布局的降级路径仍有覆盖。

| 修复前 | 修复后 |
|---|---|
| [默认主题：记忆浮层仍遮住插件页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/before-default-panel-switch.jpg) | [默认主题：正确显示插件页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/after-default-plugin-switch.jpg) |
| [Maid Atelier：导航样式不同、选中状态冲突](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/before-maid-panel-switch.jpg) | [Maid Atelier：原生导航与可用的页面标题](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/after-maid-memory.jpg) |

[导航测量](./native-alignment.json) 确认两项具有相同字号、行高、内边距、文字起点和图标尺寸。展开时均为 14 px 字号、22 px 行高、7 px / 8 px 内边距；折叠时按钮均为 36 × 36 px，图标为 18 px。原生主面板仅预留一次 `--dsh-frame-top-clearance`；本次 Web 验收没有模拟桌面窗口控件。

真实 WebUI 已通过插件／记忆切换、返回对话、新建会话、折叠导航、语言切换及状态保留。运行时记忆新增和编辑可持久化，档案创建、检索和阅读成功，真实 CLI 写入能通过 WebUI 召回（[截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/after-native-cli-recall.jpg)）。对话中的原生 `mnemon_status` 工具也已完成。仅模型选择由本地脚本固定；这些结果不代表模型质量或外部 Provider 服务验收。

Maid Atelier 0.3.2 通过官方市场的 skin-center / market 0.4.2 插件加载，使用独立临时存储。该环境中皮肤中心自身的 Apply 持久化失败，因此通过本地皮肤配置选择已安装皮肤。没有使用个人 DSH 配置或记忆。

## 分组诊断与隔离的上游候选补丁

正式 DSH 0.1.7-rc.2 把 `cordis:group` 原生容器显示为已关闭组件，但它的子插件实际正常运行。运行清单有意排除 group，因此点击该开关会返回 `unknown-plugin`（[修复前](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/before-bundle-toggle-error.jpg)）。Mnemon 的稳定分组用于保留旧版核心禁用语义和安全重载；删除分组 ID 会产生重复实例，已排除。

提交的生命周期夹具覆盖旧版核心配置目标、子组件独立设置、17 个激活哨兵、重载／重启、整包开关和私有 Provider 释放。它验证正式 Loader／管理器契约；真实 Source 行为由另行执行的制品 Headless 和 WebUI 验证负责。

隔离构建的 DSH plugin-manager 候选补丁在展示列表时过滤 group 容器，保留声明 ID 和子组件。它**没有加入 Mnemon 的运行依赖，尚未发布，也未提交上游**。[补丁](./upstream-fix.patch) 和[反馈草稿](./upstream-discussion-draft.md) 保留供审阅。单独临时 WebUI 显示 8 个组件全部运行，档案组件及整包关闭／开启均正常，更严格的声明行生命周期检查也通过（[候选环境截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/upstream-candidate-group-fixed.jpg)）。这不代表官方 DSH 已经修复。

通过公开的 `PluginManager.listBundles()` 方法，技术上可以单独安装一个管理适配 bundle。rc.2 服务层探针已通过同样的生命周期矩阵，但它会替换 Host 全局管理服务，需要单独维护版本与配置兼容。把适配器放进 Mnemon 本身会使 Mnemon 变成不可停用的管理必需组件。本 PR 未新增适配包，该方案也尚未进行 WebUI 验收。

## 复现与检查

按仓库制品流程构建和打包 Root 与全部 16 个插件，再指定空的临时状态目录：

```sh
MNEMON_CLI_PATH=/absolute/path/to/mnemon node scripts/serve-sidebar-bundle-reproduction.mjs \
  --profile /absolute/path/to/published-dsh-rc2-install \
  --artifacts /absolute/path/to/seventeen-tarballs \
  --state /absolute/path/to/empty-disposable-state
```

可选 `--skin-center` 和 `--skin-market` 接受官方插件 tarball。认证地址仅保存在权限为 600 的临时私有记录中，不进入证据。

已通过 `pnpm verify`（Root 1,390 项，8 项无关的可选集成测试跳过）、`pnpm verify:plugins`（16 个外部插件仓库与 17 个制品）、真实 CLI 集成、双 Native 命名空间的运行时归档，以及正式最低版本／rc 的 bundle 生命周期矩阵。最终 CSS 调整另通过 49 项定向测试及完整 `verify` 重跑。Root 解包体积 1,344,131 字节，低于未调整的 1,376,000 字节上限。最终安装环境的运行文件已逐一对照全部 17 个 tarball。

Runtime／Documents／Memory Spaces 的框架一致性另行提交独立 PR，不包含在本修复中。本轮未运行 Windows 或真实 Electron 窗口验收。
