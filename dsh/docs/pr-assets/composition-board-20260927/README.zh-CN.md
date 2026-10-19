# 由组件声明生成的记忆组合

[English](./README.md)

实测实现：`b348fffa`，叠加在组件联动记录之上（[记录](../plugin-linkage-20260927/README.zh-CN.md)）。macOS 15.6、Node 25.1.0、正式发布的 DSH 0.1.7-rc.2，headless Chrome 1280 × 860（及 420 × 900）。每次运行都从全新的隔离 WebUI 夹具开始，使用 Starter 默认配置与测试模型，不涉及个人记忆或凭据。

## 面板

分组来自组件声明的角色，名称与说明来自各组件自己的声明。记忆按所选方式组合时，面板不额外提示。

| 默认 | 行内点名相关组件 |
|---|---|
| ![面板](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-default.png) | ![关联标签](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-relations.png) |

## 组件详情

点击一行打开组件详情：状态与开关、来源与包名、关系，以及开关还会带动的组件。齿轮打开同一详情中的选项。

| 关系 | 声明的选项 |
|---|---|
| ![记忆空间详情](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-details.png) | ![主动记录选项](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-options.png) |

## 带动其他组件的开关与问题提示

| 关闭记忆空间会一同关闭主动记录 | 记忆未生效时的唯一修复 |
|---|---|
| ![连带关闭与撤销](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-cascade.png) | ![问题提示](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-memory-off.png) |

记忆系统把 Source 组件已关闭的层显示为“已关闭”：

![项目档案已关闭](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/workspace-layer-off.png)

## 深色与窄列

| 面板 | 详情 | 连带关闭 |
|---|---|---|
| ![面板，深色](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/dark-board-default.png) | ![详情，深色](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/dark-board-details.png) | ![连带关闭，深色](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/dark-board-cascade.png) |

| 面板，420 px | 详情，420 px |
|---|---|
| ![面板，窄列](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/narrow-board-default.png) | ![详情，窄列](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/narrow-board-details.png) |

## 验证

- 单元测试覆盖开关规划（依赖、独占能力、增强槽位、切换主策略、未托管的 Source）、关系、带撤销的选项写入、面板（选择器、按角色分组、未知角色、搜索、关联标签、详情、选项、问题提示）以及记忆系统中已关闭的层。
- WebUI：脚本在全新夹具上录制 7 个浅色、3 个深色与 2 个窄列状态，无控制台错误；同样的状态也在应用内浏览器中手动操作过，包括在真实 Host 上写入并恢复默认选项。

## 限制

组件自己的设置仍在页面级分组中：记忆 Provider 与嵌入属于“记忆空间”，后台任务属于“默认三层”。下一步通过贡献插槽把它们移入各自组件的详情。夹具中随附的增强都适用于任一主策略，因此“适用于其他主策略”的折叠分组与搜索仅由单元测试覆盖。
