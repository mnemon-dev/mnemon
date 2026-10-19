# Issue #225 与 #230：复验已有 v0.5.7 修复

[English](./README.md) | [Issue #225](https://github.com/omdsh-dev/dsh-mnemon/issues/225) | [Issue #230](https://github.com/omdsh-dev/dsh-mnemon/issues/230)

两个问题已由 [PR #226](https://github.com/omdsh-dev/dsh-mnemon/pull/226) 修复，并通过 [PR #228](https://github.com/omdsh-dev/dsh-mnemon/pull/228) 随 v0.5.7 发布。本记录补充 main `1e19caf` 的独立复验，不新增实现或发布版本。每个 Issue 均从该 revision 创建单独 worktree。

环境为 macOS arm64、Node 25.1.0、pnpm 11.19.0、已发布 DSH 0.1.5-rc.1 包及本机已安装的 Mnemon CLI 0.2.7。两套真实 WebUI 各自使用可丢弃的 `DSH_HOME`、`MNEMON_DATA_DIR`、工作区和本地模型端点。[结果与截图哈希](./verification.json)。

## #225：严格迁移与显式副本恢复

仓库中的 [v0 夹具](../../../tests/fixtures/issue-223-legacy-v0.jsonl) 由已发布 DSH 0.1.2 Agent loop 生成。本次浏览器验证只将会话 id、工作区、preset 和模型路由指向隔离环境，保留两处历史非法 summary，并将各行编码为带校验和的 Zstandard 帧。

真实 WebUI 打开时复现 `user/message 5 source summary requires notice form; source v0 artifact remains unchanged`。这也确认当前插件不会静默改写已有日志。

已有 `dsh-mnemon-repair-session` 实现生成了新的压缩副本。解码后的逐字节比较确认只删除两处已知 `summary`，16 行全部保留。原始备份 SHA-256 始终为 `74ba9572caf1b816b18e39b994a036f70d9a262960b6df423b39cee7dffc9f01`。

在可丢弃的会话目录中显式安装修复副本并重启 DSH 后，原始用户请求和助手回复均可读取。通过真实 WebUI 继续第二回合，再次重启 Host 并冷打开，两回合均保留。DSH 生成的 v3 后继文件含 29 个事件、6 条用户消息、2 条助手消息；3 条 Mnemon 上下文消息均不带 `summary`。

| 修复前 | 继续对话并冷打开后 |
|---|---|
| ![严格迁移拒绝](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issues-225-230-20260911/225-before-repair.png) | ![两回合均保留](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issues-225-230-20260911/225-cold-reopen.png) |

已有[修复与生命周期测试](../../../tests/legacy-session-repair.spec.ts)通过，覆盖普通和压缩日志的严格迁移、副本独占发布、字节保留、损坏输入拒绝与冷打开。生命周期测试使用已发布 v0 payload validator 检查新 instructions/recall，并验证其他插件上下文消息得到保留。已有受影响日志请按[恢复步骤](../../zh-CN/guides/operations.md#dsh-015-兼容与旧会话恢复)处理；只升级插件不会修复已有制品。

## #230：真实 CLI 与已连接的 WebUI

issue-230 独立 worktree 通过 `--strategy-extensions` 启用完整 Starter 和全部三个可选策略。状态页及设置页正常加载。已认证的 `/dsh-mnemon-read/status-summary` POST 返回 HTTP 200，且 `healthy: true`、`commandFound: true`、`cliPath: /opt/homebrew/bin/mnemon`；相同未认证请求返回 401。

通过 UI 创建并激活 Native 记忆空间，真实 CLI 写入合成事实 “Issue230 canary: the disposable compatibility workspace uses cedar notebooks.”，关键词检索返回原文。Host 重启后，状态页显示 Mnemon 0.2.7，内容页仍可读取该事实。最后确认 UI 的软删除对话框，内容页不再显示该事实。

| 重启后的状态 | 设置 |
|---|---|
| ![Native 0.2.7 已连接](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issues-225-230-20260911/230-restarted-status.png) | ![设置已连接](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issues-225-230-20260911/230-current-settings.png) |

| 关键词检索 | 重启后保留的内容 |
|---|---|
| ![召回真实 CLI 写入的事实](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issues-225-230-20260911/230-native-recall.png) | ![持久化 Native 内容](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issues-225-230-20260911/230-content-after-restart.png) |

未重新搭建 #230 原报告中的 0.5.5 安装和缺失 CLI 环境。早期 HTTP 405 的前后对照仍由其实际 revision 对应的 [PR #226 验证记录](../issue-223-dsh-015/README.zh-CN.md)和[原始失败截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/00-before-http-405.png)负责。本次验证已修复的当前状态及真实 CLI 发现，不声称新增实现修复。

## 命令与限制

```sh
pnpm install --frozen-lockfile
pnpm exec vitest run tests/legacy-session-repair.spec.ts tests/lifecycle.spec.ts tests/dsh-connection-compat.spec.ts
pnpm run verify
MNEMON_NATIVE_TEST_CLI=/opt/homebrew/bin/mnemon pnpm --filter dsh-mnemon-source-memory-spaces test -- tests/native-integration.spec.ts
MNEMON_CLI_PATH=/opt/homebrew/bin/mnemon pnpm e2e:serve
MNEMON_CLI_PATH=/opt/homebrew/bin/mnemon pnpm e2e:serve --strategy-extensions
```

定向测试 58 项通过。完整验证 1,249 项通过、7 项 opt-in 跳过，类型、确定性构建、文档、制品验证及真实 Headless 激活均通过。单独启用真实 Native 的 Source 测试 169 项通过，含真实 CLI 创建、写入、召回和忘记；Windows 冒烟跳过。Headless 暴露 38 个工具，包含全部 8 个代表性 Mnemon 工具，并通过重启及整体停用 Starter 检查。

浏览器使用固定的本地模型回复，不评估模型质量。未复测 Windows、Electron、实体移动设备或真实三方 Provider。仅文档新增不重复独立打包制品检查，其历史结果仍属于 PR #226。未包含用户会话、私有记忆或凭据。
