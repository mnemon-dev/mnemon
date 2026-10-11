# 空闲审查的证据复用与工具组合

**简体中文** | [English](./README.md)

关联 [Issue #211](https://github.com/omdsh-dev/dsh-mnemon/issues/211)。2026-09-11 基于 `1e19caf40f0bf37edf678c3d829f2398ab28792a` 及本 PR 的 Host 修改进行验证。[verification.json](./verification.json) 记录源码哈希、环境、实测结果和限制。

基线的 `REVIEW_TOOLS` 已排除 AOCI。本次复现的是更具体的组合缺口：DSH 的 `tools.restrict()` 过滤继承工具，却保留直接注册在子 Agent 作用域中的工具。如果另一个插件为每个 Agent 都注册概览工具，空闲审查仍能看到并执行它。真实基线中的审查已继承五个完整概览分块，却成功再次读取其中一个分块。

修复在子 Agent 发布时安装 DSH 的单调 `tools.guard()`。精确的异步启动标识配合 `agents.isOwnedBy` 区分并发及嵌套审查；保护持续到受管理的子 Agent 清理结束。它允许既有审查工具集及 `run_code` 传输，并继续检查每个 Code Mode 子调用。Persona 明确要求复用完整的继承证据；有界档案搜索仍不足以确认的候选直接跳过。父会话工具和正常手动记忆操作继续可用。

## 复现与结果

只有模型决策和合成概览 Provider 由夹具控制。Agent 发布、继承历史、工具执行、fork Provider、记忆组合、持久化、CLI、HTTP 与 WebUI 均使用真实已发布实现。夹具包含 335 个合成条目、五个分块，共 19,112 个字符。修复前后执行相同的子 Agent 读取尝试，不根据新 Persona 改变模型选择。

| 观测项 | 基线 | 修复后 |
|---|---:|---:|
| 完整继承的概览分块 | 5 | 5 |
| 父会话授权读取次数 | 5 | 5 |
| 子 Agent 尝试读取次数 | 1 | 1 |
| 子 Agent 实际读取次数 | 1 | 0 |
| 成功的有界档案搜索 | 1 | 1 |
| 审查完成结果 | skipped | skipped |

运行 `pnpm exec vitest run tests/review-evidence-host.spec.ts tests/review-tools.spec.ts tests/subagent.spec.ts`。真实 Host 测试覆盖 native 工具、已发布的 worker-thread Code Mode runtime，以及协调器尚未收到运行句柄时就发生的工具调用。向基线 worktree 仅复制新的真实 Host 测试及浏览器夹具，保留原 Host 源码，即可复现失败。

浏览器验证先构建 Root 和插件，再运行 `pnpm e2e:serve --review-evidence --strategy-extensions`。选择打印出的临时工作区与 Mnemon E2E preset，依次发送：

1. `Read the complete synthetic overview once, then review the inherited checkpoint.`
2. `The five overview chunks are complete, covering all 335 synthetic modules. Finish this bounded checkpoint review using the evidence already present. This isolated fixture contains no new durable user preference and no missing overview section.`

活动评分门槛不变，夹具仅将空闲等待缩短至五秒。通过会话中的子 Agent 菜单打开审查记录。两次浏览器验证均同时启用三个可选 Strategy 增强。

![基线子 Agent 重复读取已继承概览](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-211-20260911/before-redundant-read.png)

![修复后拒绝读取，并通过授权工具完成审查](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-211-20260911/after-read-denied.png)

## 验证与限制

Root 测试通过 935 项，插件测试通过 323 项，共跳过七项 opt-in 测试；定向测试通过 94 项。类型检查、可复现构建、Headless 激活 / 重启 / 禁用、包内容、公开入口与包 lint 通过。首次包检查测得解包大小 1,271,378 字节，超过原 1,270,000 上限；为审计过的 Host guard 将上限调整为 1,275,000 后，包检查通过。

通过 `MNEMON_NATIVE_TEST_CLI=/opt/homebrew/bin/mnemon` 启用真实 Native Source 测试，169 项通过、仅跳过一项 Windows 测试。真实 WebUI 在审查后成功新建 Native 空间；CLI 0.2.7 通过 `--no-diff` 写入精确 canary，UI 读取成功，再通过正常“忘记”操作恢复零条内容。

![真实 Native CLI 写入内容仍可通过正常 WebUI 操作读取](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-211-20260911/after-native-cli-read.png)

检查了已发布 DSH 0.1.1-rc.1 和 0.1.2-rc.1 tarball 中的 `isOwnedBy`、作用域工具过滤与 `tools.guard`；实际 Host / 浏览器执行使用 0.1.5-rc.1、macOS arm64、Node 25.1.0 和 pnpm 11.19.0。缺少 guard 能力时审查明确失败。没有修改持久化格式、凭据、RPC authority 或个人记忆。

本记录**没有**复现报告者的完整 Windows / AOCI 配置、历史六次读取或约 37K token 消耗，也没有调用在线模型、连接 AOCI 服务或声称测得 token 节省。证据验证的是当前工具组合缺口及执行层修复，不评价模型判断质量。HTTP 夹具与真实 Host 测试同时支持旧的独立结果工具协议，以及稳定结果工具的 request envelope 协议。
