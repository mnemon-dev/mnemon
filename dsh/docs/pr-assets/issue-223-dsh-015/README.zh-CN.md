# DSH 0.1.5 兼容 — issue 223

[English](./README.md) | [Issue 223](https://github.com/omdsh-dev/dsh-mnemon/issues/223)

本记录来自独立 `codex/fix-223-dsh-015` worktree，基于 main `1790251919ec731f9dd49b43da78f78b37890a00`。测试目标为 npm `latest` 发布的 DSH `0.1.5-rc.1`，未修改 DSH 官方源码。

使用 main 制品在真实 WebUI 复现 HTTP 405。Starter 连接依赖补丁恢复七个 Mnemon RPC 通道，状态页正常加载。生命周期消息移除不合法的 summary。通过已发布的 0.1.2 Agent loop 生成的合成会话复现官方迁移拒绝；修复命令保留原件，明确安装副本后，已发布的 0.1.5 持久化后端可将其升级到 v3 并冷启动重读，JSONL 和 Zstandard 均覆盖。

可复用夹具为 [issue-223-legacy-v0.jsonl](../../../tests/fixtures/issue-223-legacy-v0.jsonl)，仅包含合成用户请求、两类历史 Mnemon pre-step 消息及固定回复。[回归测试](../../../tests/legacy-session-repair.spec.ts)检查逐字节保留、拒绝覆盖、其他元数据、损坏输入、帧截断、大小上限与官方迁移。操作方法见[恢复流程](../../zh-CN/guides/operations.md#dsh-015-兼容与旧会话恢复)。


## 结果

命令入口补充修复 revision：`a389438a4e12a77f953c80ce79828e221653ee8f`。最终完整本地验证在此 revision 通过，额外回归验证 npm 安装生成的可执行符号链接能显示帮助并预览修复。下列 WebUI 截图对应此前未改变的 Runtime/UI 实现。

实现 revision：`595e197670574f6a731e445b896dbc5c78271027`；基线：`1790251919ec731f9dd49b43da78f78b37890a00`。采集日期为 2026-09-10，使用 macOS arm64、Node 25.1.0、pnpm 11.19.0、Playwright 1.63.0 / Chromium 153，以及 Mnemon Native 0.2.8。截图显示包版本 0.5.6，因为 changeset 尚未执行发布升版。[机器可读结果与截图哈希](./verification.json)。

| 检查 | 结果 |
|---|---|
| 冻结安装与 `pnpm verify` | 通过：1194 项测试、7 项 opt-in 跳过；类型、确定性构建、文档、Headless、严格包检查全部通过 |
| `pnpm verify:plugins` | 通过：16 个独立插件仓库、17 个 tarball、外部 SDK/Client 消费者、真实 DSH 打包升级与可选 Strategy 组合 |
| 真实 Native 集成 | 1 项 opt-in 测试通过，覆盖临时数据写入、召回、删除 |
| 修复工具回归 | 17 项通过，覆盖真实 v0 → v3 加载、两种编码、冷启动重读、字节保留、拒绝覆盖、校验和/截断与拒绝路径 |
| Node 20 修复预览 | 同一 v0 JSONL 夹具通过：预览 2 项修复，原件保留 |
| DSH 0.1.2-rc.1 Headless | 当前插件通过：39 个工具中核对 8 个代表性 Mnemon 工具，以及设置迁移、重启、整体停用 Starter |
| DSH 0.1.5-rc.1 Headless | 通过：38 个工具中核对相同 8 个代表性工具，以及相同的重启、停用检查 |
| 浏览器授权 | Cookie 认证页面正常；未认证状态 RPC 返回 401；只读 Runtime 和“存入记忆”入口禁止写入 |

真实 WebUI 全程使用一次性数据，依次验证：

1. 无会话打开记忆系统及设置：main 制品复现 HTTP 405；修复制品正常加载状态、Source 目录与 Provider 配置。
2. 新增 USER 和 MEMORY 条目，编辑、筛选并核对原文；在 Builtin 新增一次性条目，取消删除，恢复写权限后实际删除，原有两条保留。
3. 选择临时工作区，以 Mnemon E2E 预设创建真实 DSH 会话；创建档案、搜索并读取正文，重启后再次读取。
4. 在 UI 创建 Native 记忆空间，用真实 Native CLI 写入一条合成事实；在 UI 激活、关键词召回、查看实体和内容，重启后重读。取消遗忘后核对仍在，再确认软删除，内容页变为空。这是 CLI 预置数据的读取/删除检查，不是 AI 沉淀质量评测。
5. 在设置中保存 Sidebar → Builtin → Sidebar，确认始终只有一个入口且数据相同；检查中英文设置，通过真实 pack RPC 导出备份 ZIP。
6. 以 `writeEnabled: false` 重启：Runtime 不显示修改入口，会话“存入记忆”确认按钮保持禁用。只在一次性夹具内恢复写权限。
7. 对压缩 v0 会话运行副本修复命令，在临时会话根安装副本；打开原有用户/助手历史，发送第二轮，重启 DSH 后再次读取两轮内容。最终日志有 30 个事件、6 条 user/message，其中 3 条 Mnemon 上下文消息均不含 summary；原备份 SHA-256 未改变。

WebUI 旧会话夹具额外设置了临时工作区和 `mnemon-e2e` 预设，并将合成模型路由映射到 loopback Provider。这些属于夹具准备；修复命令仍只删除两个已知 summary 属性。v3 后继文件由 DSH 自己发布与校验。

## 截图

| 证据 | 图片 |
|---|---|
| main 复现两个 HTTP 405 | [修复前](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/00-before-http-405.png) |
| 无会话状态页正常连接 | [状态](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/01-status-connected.png) |
| Runtime 新增、编辑结果 | [Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/02-runtime-crud.png) |
| 档案创建与搜索 | [档案](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/03-documents-search.png) |
| Native 关键词召回 | [召回](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/04-native-recall.png) |
| 设置保存及目录正常连接 | [设置](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/05-settings-connected.png) |
| Builtin 使用同一份 Runtime | [Builtin](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/06-builtin-runtime.png) |
| 重启后的只读模式 | [只读](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/07-readonly-restart.png) |
| 中文设置 | [中文](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/10-chinese-settings.png) |
| 旧会话续写后重启重读 | [冷启动重读](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/11-legacy-cold-reopen.png) |
| Native 内容重启后仍在，随后完成软删除 | [内容](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/12-content-after-restart.png) |

## 复现与边界

执行 `pnpm install --frozen-lockfile`、`pnpm verify` 和 `pnpm verify:plugins`。浏览器检查先构建，再运行 `MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve`，选择其打印的临时工作区及 Mnemon E2E。夹具使用新版 persona `prefix`，并随已停用的 subprocess 一起停用 Open-in-App。用 `node bin/repair-legacy-session.mjs --input tests/fixtures/issue-223-legacy-v0.jsonl` 可对仓库夹具执行不写入的预览。真实加载器回归自行创建并清理存储根。

本次验证正式发布的 DSH API 与桌面 WebUI，未重测 Windows、Electron、真实手机、三方真实账号、模型质量或所有旧版 DSH。DSH 0.1.2 本次仅做 Headless 回归；保留旧 peer 范围不代表历史记录对应代码变为本次验证。副本工具明确拒绝其他损坏，不覆盖正在使用的会话；DSH v3 不能直接用于回滚旧版。

来源：[DSH 0.1.5-rc.1 官方发布](https://github.com/deepseek-ai/deepseek-harness/releases/tag/dsh-v0.1.5-rc.1)、[Node Zstandard API](https://nodejs.org/api/zlib.html#zlibzstddecompresssyncbuffer-options)、[RFC 8878 帧规范](https://www.rfc-editor.org/rfc/rfc8878.html#section-3.1.1)。最终 CI 结果在 PR 中关联到最终 revision，与本次本地截图分别记录。
