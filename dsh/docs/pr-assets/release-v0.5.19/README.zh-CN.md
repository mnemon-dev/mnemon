# v0.5.19 发布验收

[English](README.md)

于 2026-09-29（Asia/Shanghai）在 #307 之后合入 #311 的基础上验证。版本化候选为 `dsh-mnemon@0.5.19`，以及 `dsh-mnemon-source-runtime@0.5.11`、`dsh-mnemon-source-documents@0.5.8`、`dsh-mnemon-source-memory-spaces@0.5.13` 与 `dsh-mnemon-provider-mnemon-native@0.5.7`；其余 13 个组件按 Starter 固定的版本从 npm 安装。

## 两个受支持宿主上的官方 WebUI

分别使用未修改的 npm DSH `0.2.0-rc.1` 与 `0.1.7-rc.2`，Node `24.19.0`，全新的用户目录与 profile：

1. 启动未安装 Mnemon 的宿主，打开**插件 → 添加插件**。
2. 安装 `dsh-mnemon`。安装源由 DSH 自己选择（测速选择了中国大陆镜像源）。本地 registry 返回 npm 的真实元数据，并把五个变化的版本作为一周前发布的新版本加入，因此未变化的包直接从 npm 下载。
3. 点击**立即启用**并打开记忆系统。[状态页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/status.png)显示 **dsh-mnemon 0.5.19 / 系统正常**，Mnemon Native 显示 **Mnemon 0.2.7**，宿主未重启。
4. 在运行时记忆中添加 `RELEASE_0519_RUNTIME` 条目，刷新页面后读回：[运行时记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/runtime.png)。
5. 在 WebUI 中创建并激活 Mnemon Native 记忆空间“发布验收”：[记忆空间](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/spaces.png)。用 Mnemon CLI `0.2.7` 写入 `RELEASE_0519_NATIVE` 并用 CLI 召回，再用 WebUI 的**直接检索**找到同一条事实：[检索](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/recall.png)。
6. 在每个宿主上另建一个用命令行安装的全新 profile，由真实的 DeepSeek 模型回答第一条消息，写入两条运行时记忆（[回复](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/live-reply.png)）；**存入记忆**把候选内容交给任务 Agent，任务 Agent 将其存入新建的记忆空间并给出回执（[回执](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/live-receipt.png)）。

两个宿主的每一步均通过，无控制台错误，每个宿主进程只启动一次。[validation.json](validation.json) 记录了制品哈希、全部 18 个包的安装版本与各宿主的结果。profile 与记忆均为合成数据；API Key 仅通过环境变量交给 DSH。

![DSH 0.2.0-rc.1 上的状态页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/status.png)

![直接检索找到 CLI 写入的 Native 记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/recall.png)

## 验证与发布边界

`pnpm run release:check` 选择 Starter、三个 Source 与 Mnemon Native 发布。三个 Source 使用了本版 Starter 首次提供的 Source 页面 SDK 导出，因此它们的 `dsh-mnemon` peer 下限为 `^0.5.19`。锁文件只改变新的版本说明符，pnpm 10.13.1 以 `--frozen-lockfile` 接受它。

发布 PR 与发布工作流会运行完整的工作区与打包插件验证。发布时冻结合并后的 main 修订，先发布变化的插件再发布 Starter，读回 npm 制品，安装完整的 18 包组合并检查真实的 Registry 升级，然后创建 GitHub Release。这些截图展示的是版本化本地候选，本身不能证明 npm 发布结果。

相关证据：[桌面版窗口（#310）](../issue-310-desktop-window/README.zh-CN.md)、[安装图集](../../assets/install-v0.5.19/README.md)与 [v0.5.19 真实模型图集](../../assets/webui-v0.5.19/README.md)。
