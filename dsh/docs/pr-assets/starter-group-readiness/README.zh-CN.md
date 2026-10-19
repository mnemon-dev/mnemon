# Starter 组件组

[English](README.md)

2026-09-29（Asia/Shanghai）在隔离 profile 与合成数据上验证。全部改动都在 dsh-mnemon 中，未修改 DSH。

## 问题

0.5.18 与 0.5.19 的 Starter 有一个单独的就绪条目 `dsh-mnemon/starter`（`mnemon-starter`），插件页像普通组件一样列出它，组件组要等它提供 `mnemonStarterReady`。关闭这一行（例如在 DSH 桌面版原地更新后，为了消除一条报错）会让全部组件一直等待：`dsh web` 启动后没有记忆系统，桌面版则按启动失败处理并提议移除插件。关闭开关保存在 profile patch 中，重新安装的插件仍会继续等待。

## 改动

`mnemon-bundle` 改为使用模块 `dsh-mnemon/bundle`。它先让 Starter 的依赖可被解析（与原来相同的准备步骤），再用加载器自己的 `cordis:group` 挂载子组件。就绪行和对它的等待都已去掉，profile patch 中残留的 `mnemon-starter` 行不再对应任何条目，会被忽略。Entry ID、核心总开关、各组件的选择、配置目标与记忆数据均保持不变。

这一行无法简单隐藏：DSH 会列出插件包 patch 中每一个带 id 的条目，也没有隐藏标记；而不带 id 的条目在每次 patch 重载时都会被重建，从而重启全部组件。

| DSH 0.2.0-rc.1 上已发布的 0.5.19：共 11 个组件，第一行是 `dsh-mnemon/starter` | DSH 0.2.0-rc.1 上的候选包：共 10 个组件，第一行是 `dsh-mnemon/bundle` |
| --- | --- |
| ![改动前](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/starter-group-readiness/before-components.png) | ![改动后](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/starter-group-readiness/after-components.png) |

英文界面：[改动前](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/starter-group-readiness/before-components-en.png) · [改动后](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/starter-group-readiness/after-components-en.png)。容器行仍与原来的 `cordis:group` 一样显示“已关闭”，因为 DSH 的插件清单会跳过 group 条目（[上游 #649](https://github.com/dsh-external/issues/issues/649)）。

## 验收

在正式发布的 DSH 0.2.0-rc.1 与 0.1.7-rc.2 的全新 profile 上，通过**插件 → 添加插件**安装候选根包（其余 17 个包按 0.5.19 固定的版本安装），再点击**立即启用**：

| 检查 | DSH 0.2.0-rc.1 | DSH 0.1.7-rc.2 |
| --- | --- | --- |
| 不重启直接“立即启用” | 记忆系统出现；宿主无警告，控制台无错误 | 相同 |
| 组件列表 | 共 10 个 · 5 运行中 · 5 已停用：容器与四个默认关闭的可选策略 | 相同 |
| 重启 | 无警告 | 无警告 |
| profile patch 中残留 `mnemon-starter: disabled: true` | 正常启动，记忆系统可用 | 相同 |
| 打开再关闭“通用策略” | 只有该组件变化 | 未测 |

DSH 0.2.0-rc.1 上的功能验收从安装到结束只使用一个宿主进程。状态页显示系统正常与 Mnemon 0.2.7，页面与 [v0.5.19 发布验收](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.19/status.png)的截图逐字节一致。运行时记忆刷新后能读回，在界面中创建并激活了 Native 记忆空间，**直接检索**能找到 Mnemon CLI 写入的事实。

![直接检索找到 Mnemon CLI 写入的事实](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/starter-group-readiness/recall.png)

`dsh web` 的 profile 以 `nodeLinker: hoisted` 安装依赖，18 个包都在 profile 自己的 `node_modules` 中，准备步骤发现它们已经可被解析。真正需要这一步的是桌面版的插件 generation 布局：profile 只暴露根包。激活夹具（`tests/bundle-activation.spec.mjs`）用 DSH 自己的启动流程与插件管理器复现这种布局：使用原生 group、不做准备时，冷启动后再启用 bundle，两个宿主上都有四个组件导入失败；使用组件组时，所有已启用的组件都正常启动。夹具的其他用例在两个宿主上同样通过：保留另一个 bundle 的依赖路径、旧版 `mnemon` 总开关与组件选择，以及管理器的组件与 bundle 开关。

## 取舍：配置 schema 导出

`dsh web --dump-config-schema` 只遍历原生 `cordis:group` 与 `cordis:include`。它现在会把 `mnemon-bundle` 报告为无法识别的树载体，并略去 Mnemon 各组件的 schema。两个宿主的默认 profile 执行该命令本来就以 1 退出，并对 DSH 自带的 agent preset（`preset-standard`、`preset-ptc`、`preset-minimal`、`preset-cordis`）报告同样的错误；其余结果不变。运行时配置、插件页与 profile patch 不受影响。夹具断言的正是这一条诊断，出现其他错误即失败。

## 更新

与以前一样，原地更新后需要重启：在重启之前，从 0.5.18 或 0.5.19 更新后启用组件会提示 `./bundle` 未导出（`ERR_PACKAGE_PATH_NOT_EXPORTED`）。`dsh-mnemon/starter` 仍然导出，供仍插入它的组合使用。

## 记录

[validation.json](validation.json) 记录了候选包摘要、基线提交、各宿主结果与 schema 导出对比。候选包沿用未发布的版本号 0.5.19；changeset 请求发布补丁版本。
