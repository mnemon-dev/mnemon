# 兜底侧栏入口与原生行一致 — issue #318

[English](./README.md) | [Issue #318](https://github.com/omdsh-dev/dsh-mnemon/issues/318) | [测量数据](./measurements.json)

在没有 DSH 原生面板席位的布局中，Mnemon 会在“新建会话”下方插入自己的“记忆系统”入口。main 上该入口仍沿用旧的插件入口样式，与 DSH 的行并排时字更小、颜色偏灰且向右缩进。修复后，它在展开侧栏、打开状态和折叠侧栏中的测量值都与 DSH 自身的行一致。

基线：`dfb3196cbcea58fb9b36b7283ccac4662d366858`（main，即 0.5.20 发布的版本）。修复：`1d18173aa88933528860ac6b40b9dcdf001b5726`。运行环境为 macOS 15.6 arm64、Node 24.19.0 与仓库锁定的 DSH 0.1.7-rc.2；DSH 0.2.0-rc.2 的 `.panelRow` 规则与面板图标尺寸与之相同。

## 方法

`pnpm e2e:serve` 提供原生席位，因此“记忆系统”平时显示为 DSH 自身的行。为了在同一页面上比较兜底入口与原生行，检查按兜底逻辑构建同样的元素：
- 使用页面已加载的 Mnemon 样式表中的类名（`NS3bAW_entry`、`_entryIcon`、`_entryLabel`）；
- 使用共用的大脑图标，尺寸取兜底逻辑传入的值（main 为 18，修复后为 16）；
- 按 `placeEntry` 的位置插入到“新建会话”正下方。

数值取自浏览器的计算样式与元素边框，测量时所有行都在页面上，以便与 DSH 自身的记忆系统行比较。实际使用中二者不会同时出现：一旦原生席位可用，兜底入口就会被移除。因此截图隐藏了 DSH 的记忆系统行，每张都呈现兜底布局中的样子：“新建会话”下方的兜底**记忆系统**入口，以及 DSH 的**插件**行。

## 修复前后

| | main | 修复后 |
|---|---|---|
| 展开 | ![兜底行字更小、偏灰且缩进](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-318-sidebar-entry/before-expanded.jpg) | ![兜底行与原生行一致](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-318-sidebar-entry/after-expanded.jpg) |
| 打开 | ![打开时加粗且背景更深](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-318-sidebar-entry/before-open.jpg) | ![打开时使用 DSH 选中行的背景](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-318-sidebar-entry/after-open.jpg) |
| 折叠 | ![圆形且图标更大](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-318-sidebar-entry/before-collapsed.jpg) | ![与 DSH 折叠行相同的圆角方形](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-318-sidebar-entry/after-collapsed.jpg) |

| 展开状态 | DSH“插件”行 | main 的兜底入口 | 修复后的兜底入口 |
|---|---|---|---|
| 字号 | 14px | 13px | 14px |
| 文字颜色 | 主要文字色 `rgb(15, 17, 21)` | 次要文字色 `rgb(97, 102, 107)` | `rgb(15, 17, 21)` |
| 内边距 / 外边距 | `7px 8px` / `0 2px` | `0 10px` / `0` | `7px 8px` / `0 2px` |
| 尺寸 / 圆角 | 252 × 36 / 12px | 256 × 36 / 8px | 252 × 36 / 12px |
| 图标与左边缘 | 16px，位于 22px | 18px，位于 25px | 16px，位于 22px |
| 文字左边缘 | 46px | 54px | 46px |

打开时，DSH 的行使用悬停背景 `rgba(38, 49, 72, 0.06)`，并标记 `aria-current="page"`。main 上兜底入口会加粗（600），背景为 `rgba(38, 49, 72, 0.1)`；修复后与原生行一致，也带有 `aria-current`。折叠侧栏中，DSH 的行是 36 × 36 的圆角方形，图标 18px；main 上兜底入口是圆形、图标 20px，修复后与原生一致。

## 原因与修复

兜底入口沿用的是 2026 年 8 月插件入口（SSH、任务看板）的样式：24px 容器中的 18px 图标、`0 10px` 内边距与圆形的折叠项。这些入口此后已改用 DSH 的面板行，而 Mnemon 的原生路径本就由 DSH 绘制行。兜底入口现在沿用 `dsh-client-ui-sidebar` 的 `.panelRow`：
- 继承的 14px 字号、主要文字色与 22px 行高；
- `7px 8px` 内边距、`0 2px` 外边距与 `--dsw-radius-md` 圆角；
- 16px 图标，折叠侧栏为 18px；
- 悬停与打开状态使用同一悬停背景；
- 原生的焦点环。

打开时入口还会设置 `aria-current="page"`。有原生席位的布局不受影响。

## 自动化检查

- `pnpm run verify` 通过。它覆盖文档链接、类型检查、确定性构建、全部 17 个插件的构建、类型检查与测试，以及根测试（113 个文件，1,549 项通过，6 项跳过）；还覆盖 Headless 激活、包内容、公共入口、publint 与 attw。
- `tests/client-sidebar-css.spec.ts` 读取锁定版本的 DSH 侧栏 CSS。兜底入口与 `.panelRow`、其悬停或焦点规则，或 DSH 的 16/18px 图标尺寸不一致时，测试会失败；把内边距改回 `0 10px` 即可让它失败。
- `tests/client-sidebar.spec.tsx` 检查打开时入口为 `aria-current="page"`，关闭工作区后该标记被移除。

## 限制

兜底元素是在页面上按兜底入口自身的结构与已加载的样式表构建的，而不是由缺少原生席位的布局挂载。它的样式完全来自样式表，因此对侧栏列与 DSH 一致的任何布局都成立；重绘原生行的皮肤可以使用 `[data-dsh-plugin="dsh-mnemon"][data-dsh-part="sidebar-entry"]`。截图仅包含 DSH 默认浅色主题。
