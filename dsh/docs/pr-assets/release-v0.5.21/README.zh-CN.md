# v0.5.21 发布验收

[English](README.md)

验证日期为 2026-10-01（北京时间）。受测包为 `dsh-mnemon@0.5.21`、`dsh-mnemon-source-memory-spaces@0.5.14` 与 `dsh-mnemon-provider-mnemon-native@0.5.8`，由 `pnpm release:version` 基于包含 #321、#322 与 #323 的 `main` `73beea9a` 构建。其余 15 个组件包按 Starter 锁定的版本来自 npm，0.5.21 未改动它们。

## 两个受支持宿主上的真实官方 WebUI

在未经修改的 npm DSH `0.2.0-rc.2`（npm `latest` 与 `next`）和 `0.1.7-rc.2` 上，分别使用 Node `24.19.0` 以及全新的 home、DSH home 与 pnpm store：

1. 在未安装 Mnemon 的情况下启动宿主，打开**插件 → 添加插件**。
2. 安装 `dsh-mnemon`。安装源由 DSH 自行选择，其测速选中了中国大陆镜像。本机 registry 返回 npm 的真实元数据，并附上三个新版本。安装后 profile 中为 dsh-mnemon 0.5.21、记忆空间 Source 0.5.14 与 Mnemon Native 0.5.8，未变更的组件为 npm 上锁定的版本。
3. 点击**立即启用**，记忆系统无需重启宿主即出现。[状态页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.21/status.png)显示 **dsh-mnemon 0.5.21 / 系统正常**，Mnemon Native 显示 **Mnemon 0.2.9**。
4. 添加一条运行时记忆，刷新页面后读回：[运行时记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.21/runtime.png)。
5. 在 WebUI 中创建并激活一个 Mnemon Native 空间：[记忆空间](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.21/spaces.png)。用 Mnemon CLI `0.2.9` 写入并召回一条事实，再用 WebUI 的**直接检索**找到同一条事实：[检索](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.21/recall.png)。

两个宿主的每一步均通过，没有宿主警告或控制台错误，每个宿主进程只启动一次。[validation.json](validation.json) 记录了包摘要与各宿主的结果。profile 与记忆均为合成数据。

![DSH 0.2.0-rc.2 上的状态页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.21/status.png)

![直接检索找到 CLI 写入的 Native 记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.21/recall.png)

## 本版本的修复

每项修复另有 WebUI 上的修复前后记录：
- [大型 Native 记忆空间](../issue-320-native-store-dumps/README.zh-CN.md)：900 条记忆的空间可以列出内容、绘制图谱并完成归档。
- [空闲审查只写一层](../issue-319-review-layers/README.zh-CN.md)：审查不再把项目档案重复写入工作记忆，且可以关闭**写入运行时记忆**。
- [兜底侧栏入口](../issue-318-sidebar-entry/README.zh-CN.md)：与 DSH 自身的面板行一致。

## 验证与发布边界

`pnpm run release:check` 确认 Starter 0.5.21 使用 `latest` 标签，其组合锁定记忆空间 Source 0.5.14 与 Mnemon Native 0.5.8；发布流程会根据上一版本计算需要发布的包。没有插件使用 Starter 新增的 SDK 导出，因此 peer 下限不变。版本变更只改动 lockfile 中的 specifier 行，CI 使用的 pnpm 10.13.1 以 `--frozen-lockfile` 接受它。

发布 PR 与发布工作流会运行完整的工作区与打包插件验证。之后发布流程会：
- 冻结合并后的 main 修订；
- 发布变更的包并从 npm 读回；
- 安装完整的 18 包组合并检查真实 Registry 升级；
- 创建 GitHub Release。

这些截图展示的是版本化的本地包，本身不证明 npm 发布结果。
