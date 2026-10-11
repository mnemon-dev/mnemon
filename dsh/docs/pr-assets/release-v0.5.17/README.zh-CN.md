# v0.5.17 发布验收

**简体中文** | [English](./README.md)

本记录验收 [v0.5.17](../../zh-CN/releases/v0.5.17.md) 的实际组合，基于已合入十三个 PR 的 main `c417f087bdfbc5988e0ff9b19a81ab69cff929d8`。使用正式发布的 DSH 0.1.7-rc.2、Node 24.20.0 与真实 Mnemon CLI 0.2.9。隔离 Profile 只安装打包后的 Starter，由本地临时 Registry 解析十七个精确版本的伴随包。未修改 DSH 源码，下面的记忆均为合成验收数据。

## 功能证据

| 验收项 | 实际结果 |
|---|---|
| 组合 | 在插件页由分层策略切换为通用策略，依赖组件开关随之变化。Scoped、Light Context、Auto Capture 三个增强同时启用。 |
| 组件设置 | 由组合卡片打开 Runtime 独立设置弹窗。 |
| 模型写入 | 真实 `deepseek-flash` 调用 `mnemon_runtime_memory`，将 “RELEASE0517 canary: Aurora integration uses pnpm 11.” 写入 MEMORY；USER 仍为空。 |
| Runtime | Runtime 页面显示已保存条目。 |
| Documents | WebUI 创建活动 Markdown 档案，再搜索 `staging-aurora`，返回已保存的文档。 |
| Native | WebUI 创建并激活 Native 记忆空间，真实 CLI 写入合成事实，再从 WebUI 关键词召回。 |
| 重启 | Host 冷重启前后，六个 Runtime、Documents 与记忆空间元数据文件哈希不变；新对话从注入的 Runtime 投影读出 pnpm 11。 |

[分层组合](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/composition-layered.png) · [通用组合](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/composition-general.png) · [组件设置](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/component-settings.png) · [Flash 写入](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/flash-runtime-write.png) · [已保存 Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/runtime-persisted.png) · [文档搜索](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/documents-search.png) · [Native 关键词召回](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/native-recall.png)

## 已复现并修复的发布阻塞问题

首次通用策略 Flash 召回在真实 DSH 边界失败：`value is not lossless JSON`。Source 正确省略了可选证据字段，但 Host 转换时把 `score` 与 `revision` 写成 `undefined`。修复后省略缺失字段，保留已有值（包括零分数），同时覆盖 recall 与 related，不改变 Provider 数据、不放宽 DSH 校验。

回归测试使用公开的通用策略与真实组合、Holographic Provider fixture。修复前四组元数据中三组失败；修复后四组全部通过，同时六项已有分层策略 Host 测试通过。[修复前](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/general-recall-before.png)。

重新打包后的十八包组合安装至第二个全新 Profile，使用内容未变的合成数据副本。同一条只读 Flash 请求成功：由 Runtime 读出 pnpm 11，由一次 Native 关键词召回读出 violet gate。三次真实模型请求均返回 HTTP 200，实际工具回执为 `isError: false`。Status 显示 v0.5.17、CLI 0.2.9、一条 Runtime、一份档案及一个活动记忆空间。[修复后](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/general-recall-after.png) · [最终状态](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/status-final.png) · [脱敏验证记录与制品哈希](./verification.json)。

## 验证范围

最终 `pnpm verify` 通过 1,510 项 Root 测试（跳过 6 项），以及工作区构建、类型检查、测试、确定性构建、包与公共入口检查、真实 Headless 验证。十七个独立插件 consumer 和完整十八包安装、升级通过。最终 Host 修复之后，独立插件检查也全部通过，包括从 v0.5.15 升级到打包制品。

真实 Native CLI 集成及全部 43 项 Runtime 容量测试在 `MNEMON_EMBED_ENDPOINT=http://127.0.0.1:1` 下通过，明确验证关键词回退。首次使用环境默认 embedding 端点的测试超时；不声称已验证语义嵌入质量或所有第三方 Provider 的线上服务。真实 Flash 请求经过正式 DSH adapter 与工具运行时，凭据、认证 URL 和完整提示均保留在仓库外。

上游 `cordis:group` 显示与开关问题仍按文档说明处理。本地 tarball 验收不等于 npm 发布完成：发布工作流必须验证冻结制品、回读 Registry 完整性、安装完整组合并验证从 v0.5.16 升级，最后才创建 GitHub Release。
