# 主策略选择与通用主策略

[English](./README.md)

实测实现：`4a42eb33567461ed7f4b890cd014373956e2f0d6`，叠加在 DSH 0.1.7 清理（`6200e087a37f8be6c252c951aeabea6c7efa0806`）之上。环境为 macOS 15.6、Node 25.1.0、pnpm 11.19.0 与正式 DSH 0.1.7-rc.2。WebUI 通过 `pnpm e2e:serve` 运行，使用隔离 profile 与脚本化回环模型；未使用个人记忆、凭据或外部 Provider。

## 展示结果

“插件”中的 `dsh-mnemon` 页面在 DSH 原生组件列表上方显示主策略选择与记忆增强，并提示详细参数在设置中调整。**设置 → 记忆系统**显示同一组控件。切换经一次 View 事务生效，DSH 组件开关随之一致。

| 状态 | 插件页 | 设置 |
|---|---|---|
| 默认（默认三层，增强关闭） | [中文](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/plugins-zh-default.png) | |
| 通用 + 轻量上下文 | [中文](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/plugins-zh-general.png)、[English](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/plugins-en-general.png) | [中文](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/settings-zh-general.png)、[English](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/settings-en-general.png) |

截图时设置对话框叠在插件页之上，由此发现两份同时挂载的控件使用了重复的元素 id；现在每份控件有各自的 id，一处的修改会让另一处重新加载，被拒绝的修改会显示 Host 当前状态。

## 通用主策略对话

`pnpm e2e:serve --general-strategy` 选中通用主策略，只把模型决策脚本化。第 1 回合（`general-strategy-check remember`）确认系统提示包含通用记忆协议，Runtime、项目档案与记忆空间三个 Source 均已接入，并提供了具名 Runtime 工具与 Route 信封；随后保存一条事实，得到已提交回执。第 2 回合（`general-strategy-check recall`）在常驻投影中读到了这条事实。见[对话截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/main-strategy-choice-20260927/general-conversation.png)与 [evidence.json](./evidence.json)。

## 验证

- `pnpm verify`：文档（2,104 个本地链接、81 个锚点）、确定性构建（42 个文件）、17 个 workspace 包的构建、类型检查与测试（通用主策略 6、三层 17、Runtime 61、项目档案 39、记忆空间 168 且 2 个条件跳过）、根测试 1,417 通过且 6 个条件跳过、真实 Headless 激活（37 个工具，其中 8 个代表性 Mnemon 工具）、旧设置导入与重启、根停用门、打包 52 个文件、publint 与 attw。
- `pnpm verify:plugins`：17 个独立插件仓库与 18 个打包制品通过，包括外部消费者把打包的增强作用于打包的通用主策略、真实 DSH 同时启用三个可选策略，以及从正式 0.5.15 升级的真实 DSH 检查。
- `pnpm release:intent` 与 `pnpm release:check`：通过；九个包带 patch 发布意图，包括新增的通用主策略。

## 限制

脚本化模型只证明协议、Source、工具与投影进入了真实回合，不代表真实模型如何在 Source 之间取舍。`verify:plugins` 的 Headless 升级从正式 0.5.15 开始，其固定的插件版本大多与打包版本相同，pnpm 在该步骤可能沿用未变版本插件的已发布副本；打包插件代码由独立安装、外部消费者与策略组合检查覆盖。插件页仍把 DSH 的 `cordis:group` 行显示为已关闭（[dsh-external/issues#649](https://github.com/dsh-external/issues/issues/649)）。
