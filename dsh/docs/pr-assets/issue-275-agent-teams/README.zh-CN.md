# Agent Teams 空闲审查 — issue #275

[English](./README.md)

测试基线：`84d469ffa838a36fa579295d94029fcac8ac058e`（Mnemon 0.5.13）、macOS、Node 24.20.0 与 pnpm 10.13.1。仅使用正式发布的 DSH 契约，根项目开发依赖族和 lockfile 未改变。

旧 Team gate 在正式 DSH/Teams 0.1.7-rc.1 中仍于创建子代理前跳过 fork 与 spawn；[before-published.json](./before-published.json)同时记录无 Teams、只有 TeamService 的对照。旧 0.1.5-rc.2 / Teams 0.1.5-alpha.2 使用动态策略，0.1.7-rc.1 改为静态策略。本修复保留默认 `pause`，新增显式 `scoped` 设置，不移除其他插件策略，也不放松发布、归属、深度或执行限制。

浏览器验收还发现有界检查点错误读取不存在的用户消息包装；两个被测 DSH 世代的公开 `user/message` 都是扁平载荷。回归测试在修复前真实失败，修复后检查完整用户决策与中英禁止写入指令，拒绝注入召回、摘要和未知来源。当前 surface、已完成回合、整条消息预算规则均保留。

## 复现

将 [rc1-profile.json](./rc1-profile.json) 或 [legacy-profile.json](./legacy-profile.json)复制为新临时目录中的 `package.json`，用 Node 24 在其中安装。旧版精确组合使用 `npm install --ignore-scripts --legacy-peer-deps`；显式版本 pin 防止较新预发布替换历史 runtime。不修改任何官方包。

```sh
MNEMON_TEAM_TEST_PROFILE=/absolute/rc1-profile pnpm exec vitest run tests/agent-team-review-host.spec.ts --maxWorkers=1 --minWorkers=1
MNEMON_TEAM_TEST_PROFILE=/absolute/legacy-profile MNEMON_TEAM_TEST_LEGACY=1 pnpm exec vitest run tests/agent-team-review-host.spec.ts --maxWorkers=1 --minWorkers=1
```

矩阵组合 fork/spawn、原生/Code Mode、无 Teams/只有 TeamService/全部 Team 工具，以及 pause/scoped。Scoped、Light Context、Auto Capture 同时加载，每次模型调用都检查编译 View。只有模型选择由脚本固定；DSH 实际发布子代理、执行/拒绝工具、提交 Runtime、保留父 Team 任务并 dispose 完成的子代理。模型必须收到父会话明确用户证据，另在写入前执行取消用例。

WebUI 先构建 Root 与十六个独立插件，将全部 tarball 放入同一目录，另保留基线制品。前后两侧使用不同的空状态目录启动：

```sh
MNEMON_CLI_PATH=/absolute/verified/mnemon node scripts/fixtures/serve-team-review.mjs \
  --profile /absolute/rc1-profile --artifacts /absolute/packed-artifacts \
  --state /absolute/disposable-state --policy pause
```

夹具通过公开 DSH plugin 命令安装根包，经 loopback registry 解析全部插件制品。前后两侧均启用官方 Coding、Teams 与 PTC 服务；仅将原生目录对话框替换为官方浏览器目录选择器。私有连接信息与原始日志保存在受限权限的状态目录，启动时通过真实公开 DeepSeek 适配器检查文本与工具 SSE。

选择夹具工作区，修复后在 Mnemon 设置中选择**受限子代理审查**，然后分两轮发送 [English 记录](./README.md#reproduce)中的两条完整合成用户消息。

子代理模型端点只有在自身请求中包含两条完整用户消息时才继续。随后尝试被禁止的 Team 委派、提交一条 Runtime 事实、执行有界 Document 搜索并完成。之后发送 `TEAM_CHECK`，调用真实父代理 `team_task_create`；继续完成回合仍应遵守夹具的一次尝试预算。关闭空闲审查后，Teams 和已有数据仍应可用。

## 结果与边界

[最终验证报告](./verification.json)记录 `verify` 与 `verify:plugins --skip-build` 均成功退出。根测试通过 1,354 项，默认跳过八项可选测试；十六个插件包通过 381 项，默认跳过三项可选测试。公开 Team 组合与使用 CLI 0.2.9 的真实 Native Provider 集成另行执行并通过。独立制品验证覆盖全部十六个插件仓库与十七个 tarball，包括公开 SDK 消费方和真实 DSH Starter/Strategy 组合。四个 Vitest worker 变量及制品并发均设为一，超时和性能限制未改变。报告逐项列出默认跳过项。

最终包包含 49 个文件，解包体积 1,373,650 字节，比基线增加 2,872 字节。全部发布文件与最终浏览器验收使用的制品逐一相同。包体积预算为 1,376,000 字节，剩余 2,350 字节。

[0.1.7-rc.1 矩阵](./after-published.json)的十六种组合全部通过：十二次受限审查成功、四次暂停且不创建子代理；另有两次原生取消尝试，没有新增写入。[旧版矩阵](./legacy-published.json)的十六个预期结果也全部通过：八个无 Teams/仅服务对照成功、四次暂停、四次完整 Teams 的 scoped 运行在委派被拒后报 `TEAM_NOT_MEMBER`，在 Runtime 写入前停止，失败 provider 不被重放。两个矩阵均验证全部三个 Strategy 贡献和父 Team 工具成功。

[已完成的浏览器报告](./web-acceptance.json)记录基线两个成功回合、零审查子代理、零 Runtime 事实。修复后父会话四个成功回合、恰好一个审查子代理和一条 Runtime 事实；额外预算验证回合没有重复写入。两个完整原用户消息出现在持久化子代理检查点和四次模型请求中。子代理的 `spawn_teammate` 被执行限制拒绝，随后得到 Runtime committed、Document 搜索成功和 completion 回执；父会话之后仍成功创建官方 Team 任务。Mnemon CLI 0.2.9 另通过 Store 只读快照读回原有 canary。

| 截图 | 观察结果 |
|---|---|
| [修复前 Team 暂停](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-275-agent-teams/before-team-paused.png) | 两个合格回合后仍显示原警告，没有 Runtime 事实 |
| [受限设置](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-275-agent-teams/final-scoped-settings.png) | 真实设置界面保存显式兼容选项 |
| [子代理用户证据](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-275-agent-teams/final-child-user-evidence.png) | 有界检查点保留完整真实用户决策 |
| [子代理执行限制与写入](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-275-agent-teams/final-child-guard-and-receipt.png) | Team 委派被拒绝，允许的 Runtime 工具成功 |
| [父会话 Team 任务](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-275-agent-teams/final-parent-team-task.png) | 父会话 Team 工具继续有效 |
| [预算验证后的 Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-275-agent-teams/final-runtime.png) | 只有一条项目事实，没有重复记录 |

![原 Team 兼容暂停](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-275-agent-teams/before-team-paused.png)

![额外预算验证回合后的 Runtime 事实](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-275-agent-teams/final-runtime.png)

精简报告只包含合成用例元数据与回执。确定性模型验证集成与证据传输，不评价真实模型的记忆判断质量。关闭审查、禁止写入指令与启动错误另有自动测试，不额外宣称 GUI 验收。不使用生产凭据、个人记忆或替换个人 CLI。此次 macOS 运行未覆盖 Windows。
