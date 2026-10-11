# Issue #139: Builtin placement regression evidence

## 中文

2026-08-31，在从 `main`（`d6fa550`）创建的独立 worktree 中验证 [issue #139](https://github.com/omdsh-dev/dsh-mnemon/issues/139)。修复代码为 `d1bf61ed4fde5a8403ac73ae07d0f0696902bd8f`；截图中的 `dsh-mnemon 0.4.1` 是本地 link 构建保留的包版本，不代表新发布包。原版对照来自未经修改的 npm `dsh-mnemon@0.4.1`。

### 环境与复现

- Host：发布版 `@deepseek-ai/dsh@0.1.1-rc.2`；Web：`@linxin666/dsh-web-all@0.3.6`，官方默认皮肤。
- Native CLI：官方 Mnemon 0.2.5，macOS arm64；下载文件已按官方 SHA-256 校验。
- 独立 DSH_HOME、两份测试工作区、全局根和自定义根，仅使用合成数据；未修改个人配置或记忆。
- 普通环境，无通知丢失注入。会话由本地固定响应模型生成；最终重启验证禁用模型 Endpoint，只进行设置、读取和编辑器操作。
- 对照截图为原始 1600×900 页面，补丁截图为原始 1280×720 页面。没有缩放、拼接或改写截图内容；不将不同视口用于像素级布局对比。

```sh
pnpm install --frozen-lockfile
pnpm run verify
node scripts/serve-web-regression.mjs --cli /absolute/path/to/test-owned/mnemon --display-mode buildin --storage-scope workspace
node scripts/serve-web-regression.mjs --cli /absolute/path/to/test-owned/mnemon --package dsh-mnemon@0.4.1 --display-mode buildin --storage-scope workspace
```

两条 Web 命令均刻意传入历史 `buildin`。对照版仍只有 Sidebar；修复版自动保存 `builtin` 并注册会话标签页。用最终代码重启同一测试根后，再次验证已有数据、配置热更新迁移及所有范围映射。

### 验证结果

| 场景 | 结果 |
|---|---|
| 配置规范化 | 启动和外部热更新自动将 `buildin` 写回 `builtin`；旧客户端 RPC 也保存规范值；注释及无关字段保留 |
| 幂等与并发 | Headless 重启后配置字节不变；修订号冲突重新读取，新 Sidebar 选择优先；只读设置不绕过权限 |
| 入口切换 | Sidebar 与 Builtin 实时双向切换、刷新后保留，始终只挂载一个入口；默认 Sidebar 不变 |
| 共用界面 | 相同四个主页面、导航、筛选和编辑器；Builtin 只隐藏页眉范围/工作区/对齐控件，无专用 CSS |
| 工作区范围 | A/B 的运行时记忆相互隔离；A 的档案和已激活 Native 记忆空间不出现在 B |
| 全局和自定义范围 | 两个会话分别共享全局根或配置的自定义根，不混入工作区记忆；切回工作区后原数据仍在 |
| 页面往返 | 最终版本连续十轮任务看板与会话 Builtin 往返，始终读取 A 的原记录；存入记忆弹窗取消不写入 |
| Native | 真实 CLI 状态调用成功，A 显示 Mnemon 0.2.5、1/1 记忆空间已激活及一份项目档案 |

完整 `pnpm run verify` 通过：630 项测试通过，1 项 Windows 专用测试在 macOS 跳过；108 个构建文件确定性检查、35 个 Headless 工具及 5 个代表调用、Headless 配置迁移和幂等重启、115 文件发布包、10 个 Node 兼容入口、publint 与 attw 均通过。组件测试覆盖两种入口的全部八种记忆层组合、旧请求失效、会话切换时编辑器清理、独立任务作用域、可见性和快捷入口清理；Host 测试覆盖 global/workspace/custom 及旧 dataDir 路由。

配置迁移前后以及最终范围往返后，以下四份 `runtime/memories.json` 的 SHA-256 完全相同。截图中的旧单词 `buildin` 是刻意保留的测试记忆内容，配置迁移没有修改记忆正文。

| 测试根 | SHA-256 |
|---|---|
| Workspace A | `c49c88ad340b981e058b13c5dcddb270e43d584fc20d420d1b7e3db91a1855cd` |
| Workspace B | `45aeff0a6a755584c49741752517615a4465ba7b4088e88b8545351c9a1a71f6` |
| Global | `aff509817b6de2636c926f77c68e7103bc22e6740d86559fb5303a78a9be46bd` |
| Custom | `a8f3639dfe6c3c17a650b87cc924c58e99bf921d5a34b54ac233f0d5a4e3a4d2` |

边界：未调用真实付费模型或第三方远程 Provider；SSH 只验证入口往返，未配置其后端。主动重启 Host 时出现短暂连接重试和第三方 terminal 断连日志，后续 Mnemon 操作无新增错误。额外的 1600×900 补拍遇到浏览器控制连接超时，因此仅提交已确认完成加载的 1280×720 补丁截图，不宣称完成宽屏补拍。保留上游 rc.2 缺失 source map 的非失败构建告警。机器路径、原始日志和完整 fixture 留在本地，不纳入公开证据。

## English

Verified on 2026-08-31 in an independent worktree based on `main` at `d6fa550`. The patched source is `d1bf61ed4fde5a8403ac73ae07d0f0696902bd8f`; its UI still says 0.4.1 because this is a local linked build, not a newly published release. The control is the unmodified npm `dsh-mnemon@0.4.1` package.

The environment uses published DSH 0.1.1-rc.2, Web-all 0.3.6 with the official default skin, and the real official Mnemon 0.2.5 macOS arm64 binary verified against its published SHA-256. DSH_HOME, both workspaces, and global/custom roots are fixture-owned. Only synthetic memories and local fixed model responses were used; the final restart deliberately disabled model requests. The control screenshot is 1600×900; patched screenshots are 1280×720. All captures are unmodified, with no pixel-level comparison claimed across different viewports.

The commands above intentionally start both environments with `buildin`. The control keeps Sidebar and has no conversation memory tab. The patch automatically persists `builtin`. Startup, external settings edits, and legacy RPC writes normalize the setting without changing unrelated fields or comments. Headless restart is byte-idempotent; revision conflicts re-read current preferences so a newer Sidebar choice wins. Read-only settings are recognized without bypassing write restrictions.

Sidebar and Builtin switch live without duplicate entries and survive reloads. They share all pages, filters, and editors; Builtin hides only the scope/workspace/alignment header and adds no CSS. Workspace A/B memories stay isolated, including A's document and active Native space. Global/custom roots are shared across both sessions independently of workspace-local data. Switching back restores the original records. The final build passed ten Task Board/conversation round trips. Canceling Save to memory did not write. Real Native status shows CLI 0.2.5, one active space, and one document in A.

Full `pnpm run verify` passed: 630 tests, one Windows-only skip on macOS; 108 deterministic build files; 35 Headless tools and five representative calls; migration plus an idempotent Headless restart; a 115-file package; ten Node-compatible public entries; publint and attw. Component coverage includes all eight layer combinations on both surfaces, stale responses/editors across sessions, task scoping, visibility, and shortcut cleanup. Host routing covers global/workspace/custom and legacy dataDir. The four memory-file hashes listed above remained identical through migration and final scope round trips; the old word `buildin` inside a synthetic memory was deliberately not rewritten.

Limitations: no paid model or remote Provider validation. SSH navigation only; its backend was not configured. The intentional Host restart produced temporary reconnect and third-party terminal-disconnect messages, but subsequent Mnemon actions added no errors. An additional 1600×900 capture attempt encountered browser-control connection timeouts, so only confirmed fully loaded 1280×720 patched screenshots are included; completed wide-screen recapture is not claimed. The upstream rc.2 missing-source-map warning remains non-fatal. Machine paths, raw logs, and complete fixtures remain local.

## Screenshots / 截图

| 场景 / Scenario | 截图 / Screenshot |
|---|---|
| 原版 0.4.1 忽略旧偏好 / Unmodified 0.4.1 ignores the old preference | [Before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/builtin-139/before-0.4.1-conversation.png) |
| 规范 Builtin 配置 / Canonical Builtin setting | [Settings](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/screenshots/settings-entry-placement.png) |
| Builtin 中读取 A / Read A through Builtin | [Builtin](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/builtin-139/after-builtin-runtime.png) |
| Sidebar 中读取相同内容 / Same content through Sidebar | [Sidebar](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/builtin-139/after-sidebar-runtime.png) |
| B 的隔离记忆 / Isolated B memory | [Workspace B](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/builtin-139/after-workspace-b-isolated.png) |
| 真实 Native 状态 / Real Native status | [Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/builtin-139/after-native-status.png) |
