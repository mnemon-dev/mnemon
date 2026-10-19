# 空闲审查限额与 Agent Teams 兼容性

**简体中文** | [English](./README.md)

关联 [#100](https://github.com/omdsh-dev/dsh-mnemon/issues/100) 和 [#255](https://github.com/omdsh-dev/dsh-mnemon/issues/255)，于 2026-09-16 基于 `1363ebffaf19c9ab4badf0137f6fe87acacaf989` 复现。实现已提交为 `2ca95ee1953f1cadd589a17255f21f87375e6038`，本证据提交仅改变文档和图片。浏览器验证使用真实 DSH WebUI、已发布的 Host 包、临时本地存储，并开启全部三个 Strategy 增强。模型回复来自确定性的本地回环夹具；这些检查不衡量模型判断质量或外部 API 可靠性。

## 修改前后

| 检查 | 修改前 | 本次修改后 |
| --- | --- | --- |
| 30 轮符合条件的对话，审查成功，次数上限设为 2 | 创建 15 个审查子代理 | 创建 2 个；清空或压缩上下文不会重置已加载 Agent 的次数预算 |
| 150 秒内 30 轮对话，审查失败 | 尝试 29 次 | 在配置的五分钟最小间隔内仅尝试 1 次 |
| 缺少 fork Provider | 审查拒绝执行 | 默认使用有界 spawn；显式 fork 可在启动前降级为 spawn 或跳过 |
| 已发布的 Agent Teams 服务和工具同时启用 | fork 与 spawn 都在子代理首步后出现 `TEAM_NOT_MEMBER` | 创建子代理前暂停审查，并说明不兼容组合 |
| Document 与 Runtime 写入后审查失败 | 工作区的失败状态没有同时携带两项已提交回执 | 仍明确报告失败，并列出回执供核对，不会重放本次运行 |

两项频率回归均先在基线实现上运行并复现失败，修复后 `tests/lifecycle.spec.ts` 的对应断言通过。产品默认尝试间隔为五分钟，每个已加载父 Agent 最多尝试 20 次；测试采用较小上限以直接观察边界。

| 基线上的真实 Team 错误 | 修改后的兼容性暂停 | 携带已提交回执的部分失败 |
| --- | --- | --- |
| [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/before-team-error.png) | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-team-paused.png) | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-final-partial-receipts.png) |

Team WebUI 工作负载包含两个已完成父轮次和五段完整概览，共 335 条合成条目。常规准入阈值仍为 5，第二条实质性消息使累计分数达到 6。基线创建一个审查子代理，发生 Team 错误后释放驻留实例，但保留非活跃目录记录。修改后的构建在记录的 190 秒安静期内没有创建子代理，也没有请求审查模型。[脱敏 WebUI 观测](./webui-evidence.json) 包含包版本、工作负载、构建哈希、释放证据及两次遥测的差异。

Team 记录使用的构建早于最终 Settings 布局优化及清理成功后再增加成功计数的调整；公开兼容性检查和调度准入逻辑未变，独立的受保护矩阵也已针对最终源码重新执行。截图仅包含合成记忆和不透明的测试标识。

## 已发布 Agent Teams 复现

可选夹具仅使用 npm DSH `0.1.5-rc.2` 及两个 Agent Teams `0.1.5-alpha.2` 包，不修改 DSH 源码，也不把实验包加入本仓库依赖。使用支持 TypeScript stripping 的 Node（已用 Node 25.1.0 验证）：

```sh
team_profile=$(mktemp -d)
npm install --prefix "$team_profile" --save-exact \
  @deepseek-ai/dsh@0.1.5-rc.2 \
  @deepseek-ai/dsh-experimental-agent-team@0.1.5-alpha.2 \
  @deepseek-ai/dsh-experimental-tool-agent-team@0.1.5-alpha.2

node --experimental-strip-types scripts/fixtures/reproduce-published-team.mjs \
  --profile "$team_profile" --output "$team_profile/baseline.json"
node --experimental-strip-types scripts/fixtures/reproduce-published-team.mjs \
  --profile "$team_profile" --guard --output "$team_profile/guarded.json"
```

必须显式指定 Profile 路径。脚本断言实际解析的包版本，并删除每个用例的临时会话。`--guard` 导入本仓库真实的 `idleReviewBlockReason`，没有复制检查逻辑。六种预期结果均符合时，两条命令都以零状态退出；基线中的预期错误是缺陷证据，不代表审查成功。

| 组合 | Fork | Spawn | 子代理模型调用数 / 探针执行数 |
| --- | --- | --- | --- |
| 无 Team 插件 | 完成 | 完成 | 2 / 1 |
| 仅 Team 服务 | 完成 | 完成 | 2 / 1 |
| Team 服务与工具 | 失败 | 失败 | 1 / 1 |
| Team 服务与工具，加兼容性检查 | 创建子代理前跳过 | 创建子代理前跳过 | 0 / 0 |

各父代理先完成一轮对话。子代理采用有界 persona、`maxDepth: 1`、空闲审查标签，以及只允许一个合成探针的工具列表。两种 Provider 都先发布尚无 descriptor 的子代理；此时 Team 认为其为 lead 并安装策略。共用 driver 随后追加一次性 descriptor，成员资格随即消失。首个探针仍执行，但下一步组装提示词时 Team 策略抛出 `TEAM_NOT_MEMBER`。

检查仅使用公开的 `ctx.get('agentTeams')` 与 `tools.get('spawn_teammate', parent)` 能力，必须明确传入父代理作用域。仅启用服务的对照组仍正常工作。受保护的 Team 组合不创建子代理或目录记录，之后父代理仍成功执行官方 `team_task_create` 工具。结果：[基线矩阵](./published-team-baseline.json)、[受保护矩阵](./published-team-guarded.json)。

此处理会暂停受影响组合的自动审查，不修复上游 Team 策略，也不声称该组合下的手动委派已可正常工作。移除 Team 工具后，下一个符合条件的已完成轮次可重新调度审查。

## 部分失败与 Settings 复现

构建仓库，将 `MNEMON_CLI_PATH` 指向官方 Mnemon 0.2.8 可执行文件，再运行：

```sh
pnpm run build
pnpm --workspace-concurrency=4 -r build
MNEMON_CLI_PATH=/path/to/mnemon pnpm e2e:serve --idle-review --strategy-extensions
```

选择 Mnemon E2E 预设，发送两条至少 150 字符的实质性用户消息以达到常规活动阈值，等待至少五秒。该夹具设有 `idleReview.minIntervalMs: 5000` 与 `idleReview.maxPerSession: 1`。它执行真实的 Document 创建与项目 Runtime 添加，确认两项回执后故意返回 HTTP 400 错误 `SYNTHETIC_REVIEW_FAILURE_AFTER_COMMIT`。刷新记忆系统状态即可看到失败运行及两项回执。该已加载 Agent 的预算耗尽后，继续发送符合条件的消息也不会再创建审查子代理。

第一次部分失败记录采用默认五分钟最小间隔，其后安静轮次只证明冷却和不立即重放。载入最终构建后，浏览器将间隔改为 5,000 毫秒，并打开新的父会话。两轮符合条件的对话产生一个失败审查；第三轮符合条件的对话结束后等待超过 30 秒，仍无第二个审查，单独验证了一次上限。随后关闭审查并将上限提高至 2，第四轮符合条件的对话后等待超过 10 秒仍无新增子代理，单独验证了独立开关。同一夹具日志包含两个会话，不能宣称整份日志只有一个子代理。[脱敏部分失败观测](./partial-failure-evidence.json) 区分了两次记录。最终状态显示两份档案，是因为前一会话已提交的档案仍被保留。

| 启用，五秒间隔，上限 1 | 关闭，尚有剩余预算（上限 2） | 四个已完成轮次与一个子代理 |
| --- | --- | --- |
| [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-review-budget.png) | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-review-disabled.png) | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/idle-review-agent-team/after-bounded-conversation.png) |

Settings 中的自动审查开关独立于召回和主动写入设置。数值限额在保存前校验，现有授权 Settings RPC 同样校验范围并拒绝只读模式下的写入。自动化覆盖这些拒绝路径、取消、缺少保护能力、其他子代理回执，以及其他插件自身作用域工具被拒绝执行的情况。

## 验证与边界

在 macOS arm64、Node 25.1.0、pnpm 11.19.0 上，`MNEMON_NATIVE_TEST_CLI=/path/to/mnemon pnpm run verify` 通过：根目录 1,156 项测试通过，两个文件中的五项真实模型可选检查跳过，Windows 专用插件冒烟检查在 macOS 上跳过；所有常规插件测试及真实 Native Source 的创建、写入、召回、遗忘集成均通过。类型、确定性构建、39 个工具的 Headless 激活、公开入口、publint 和 attw 均通过。根包共 47 个文件，压缩后 298,457 字节，解包后 1,335,573 字节；新增检查点、回执与 Settings 实现仍处于已审查的 1,338,000 字节上限内，没有新增包文件或暴露实现源码。

真实 Native CLI 版本为 0.2.8，SHA-256 为 `f8f21151ce9777983b8d2dfb9cbea1a00b223de6063659af232d04aa57cedad0`。真实 Host 证据测试覆盖 fork/spawn 与原生工具、Code Mode 的组合，包括完整 19,112 字符概览证据和其他工具被拒绝的情况。

尝试次数存于已加载 Agent 的内存中，卸载后重新打开 Agent 或重启 Host 会重置。公开释放 API 移除驻留子代理，但已发布 Host 没有面向插件的历史归档或 TTL API。因此保留既有历史，不删除私有持久化文件或无关代理。有界 spawn 可省略过大或较早消息；显式 fork 继承完整父上下文。`maxTokens` 限制每次模型回复，不代表整次多步运行的总限额。
