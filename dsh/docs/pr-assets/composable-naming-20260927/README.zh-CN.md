# 可组合记忆命名

[English](./README.md)

实测实现：`6227ed41afdba1212409e7b02dc25e184851a122`，基于 `a16ab47f8a61bed19537a07d53ad1030f4d3934b`（v0.5.16）。环境为 macOS、Node 24.20.0、pnpm 11.19.0、正式 DSH 0.1.7-rc.2 与 Mnemon CLI 0.2.9。临时 Web profile 安装本分支 Root tarball，以及 v0.5.16 组合中的十六个正式伴随包。测试包仍标记为 0.5.16；changeset 为后续补丁发布提供版本意图。

## 展示结果

中文插件名为 `可组合记忆 (dsh-mnemon)`，说明为：

> 面向 DeepSeek Harness 的可组合视图记忆。记忆来源与策略可插拔，开箱即用提供三层记忆。

在真实插件详情页打开已安装的制品，并通过 DSH 设置切换中文、英文。组合包标题和 Root 组件行均展示新文案；英文名称保留 `dsh-mnemon`，说明与用户提供的英文原文一致。正式 DSH 的 `readPluginMeta()` 也从同一安装环境读出了相同文案；[metadata.json](./metadata.json) 记录测试 tarball 的哈希。

| 语言 | 修改前 | 本次实现 |
|---|---|---|
| 中文 | [PR #286 原始证据](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/pr-286-plugin-metadata/plugins-zh.jpg) | [可组合记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composable-naming-20260927/plugins-zh.png) |
| 英文 | [PR #286 原始证据](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/pr-286-plugin-metadata/plugins-en.jpg) | [新版说明](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composable-naming-20260927/plugins-en.png) |

截图保留正式 DSH 的分组展示问题，已由 [dsh-external/issues#649](https://github.com/dsh-external/issues/issues/649) 跟踪。本次文案修改没有替换插件管理器。

## 验证

- `pnpm verify`：文档、类型、确定性构建和各插件测试通过。Root 默认并发下有两项性能测试超过 wall-clock 门槛，其余 Root 测试通过；未调整门槛。
- `pnpm exec vitest run --dir tests --maxWorkers=2`：1,420 项全部通过，9 项可选或环境测试跳过，包括前述两项性能测试在内均通过。
- `pnpm verify:headless` 与 `pnpm verify:package`：通过，覆盖重启、旧版禁用语义、公开入口、类型和包检查。
- `pnpm verify:plugins --skip-build`：十六个独立插件项目与十七个制品通过，包含更新后的外部元数据断言及真实 DSH 安装、组合检查。
- `pnpm release:intent`：Root patch changeset 检查通过；`mnemon --version` 为 0.2.9。

本次无需调用真实模型或外部 Provider API。本记录验证元数据展示与打包，不代表新增记忆行为验收。
