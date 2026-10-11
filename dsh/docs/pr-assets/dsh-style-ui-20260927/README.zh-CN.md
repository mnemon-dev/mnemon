# 与 DSH 统一的记忆界面

[English](./README.md)

实测实现：`0213857a7f7d`，叠加在 Mnemon CLI 可选化（`adeb9f701374`）之上。环境为 macOS 15.6、Node 25.1.0、pnpm 11.19.0、正式 DSH 0.1.7-rc.2 与 Mnemon CLI 0.2.7，无头 Chrome 153，除特别说明外为 1280 × 860。WebUI 夹具使用 [Mnemon CLI 可选化记录](../optional-mnemon-cli-20260927/README.zh-CN.md)中的 Provider Lab 数据：Mnemon Native，加上 Docker 中的 OpenViking、Honcho、Mem0、Hindsight、RetainDB、Supermemory，以及本地 Holographic。未使用个人记忆或凭据。

“改版前”截图取自同一夹具中的 base 版本；回合记忆栏的改版前截图来自 #293 的记录，之后的改动没有涉及它。

## 设置 → 记忆系统

| 改版前 | 改版后 |
|---|---|
| ![改版前的设置页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-before.png) | ![改版后的设置页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-after.png) |

设置页改为 DSH 的偏好行，分成六组。展示位置、存储范围和用户画像范围由选择卡片改为 DSH Menu 下拉，所有开关都是 DSH Switch。

| 存储 | 记忆 Provider |
|---|---|
| ![存储分组](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-storage.png) | ![Provider 列表](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-providers.png) |

“存储”包含存储范围、唯一的“数据目录”输入框（留空即默认目录）、用户画像范围以及 ZIP 备份与迁移，这些设置都不再放在 Mnemon Native 面板里。Mnemon Native 是第一张 Provider 卡片，展开后只包含它自己的嵌入设置，使用 DSH 的编辑区样式：

![展开的 Mnemon Native](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-native.png)

![后台任务](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/settings-background.png)

任务 Agent 模型与空闲审查同属“后台任务”；空闲审查开启时才显示其选项，间隔与预算收在“审查参数”中。

## 状态

| 改版前 | 改版后 |
|---|---|
| ![改版前的状态页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/status-before.png) | ![改版后的状态页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/status-after.png) |

Mnemon Native 是同一个“记忆 Provider”列表的第一行，已启用数量也把它计算在内。深色主题使用同一组令牌：

![深色主题的状态页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/status-dark.png)

## 记忆空间

| 改版前 | 改版后 |
|---|---|
| ![改版前的记忆空间](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/spaces-before.png) | ![改版后的记忆空间](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/spaces-after.png) |
| ![改版前的实体页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/entities-before.png) | ![改版后的实体页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/entities-after.png) |

卡片去掉了 Provider 彩色边条与渐变，改用 DSH 标签、状态点与开关，删除与断开使用 DSH 卡片删除样式。Tab 下的页面以分组标题的层级命名区块。实体能力卡片把不支持的 Provider 标为闲置，而不是故障。

## 插件页与对话

| 改版前 | 改版后 |
|---|---|
| ![改版前的插件页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/plugins-before.png) | ![改版后的插件页](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/plugins-after.png) |
| ![改版前的回合记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/turn-before.png) | ![改版后的回合记忆](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/turn-after.png) |

主策略是一行下拉选择，增强是开关行，与下方 DSH 自带的组件列表一致。回合记忆栏使用 DSH 数据图标与展开箭头，工具名显示为中性标签。“存入记忆”对话框在 DSH 设置字段中编辑候选内容：

![存入记忆对话框](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/dsh-style-ui-20260927/save-dialog.png)

## 验证

- `pnpm run verify`：文档 2,144 个本地链接与 81 个锚点；确定性构建 42 个文件；各 workspace 包的构建、类型检查与测试（记忆空间 174 通过、2 个条件跳过，运行时 61，档案 39）；根测试 1,419 通过、6 个条件跳过；真实 Headless 激活（37 个工具）、旧设置导入与幂等重启、根停用门；打包 52 个文件，publint strict 与 attw 通过。
- `pnpm run verify:plugins --skip-build`：17 个独立插件仓库与 18 个打包制品，包括真实 DSH 激活打包的 Starter 与三个可选 Strategy。
- `pnpm run release:intent`：变更说明覆盖所有改动的包。
- WebUI：浅色与深色主题下 1280 × 860 各 16 个状态，浅色主题下 700 × 900 的 16 个状态，两种主题下设置页的每个分组，一次驱动的对话回合（含存入记忆对话框）与一次记忆工具回合；所有运行均无控制台错误。另在设置中开启 Builtin，从会话标签打开记忆系统，再切回侧边栏。

## 限制

截图来自一个预置数据的夹具，只说明布局与样式，不代表 Provider 行为。Native Provider 在空间上的标签仍来自其描述符，显示为“mnemon”。v0.5.4 的文档媒体早于本次改版，随下一次发布媒体一起更新。Builtin 模式下，DSH 的会话标签仍位于记忆系统标签上方，与改版前相同。
