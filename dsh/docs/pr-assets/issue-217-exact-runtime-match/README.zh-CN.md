# Runtime 精确条目匹配 — Issue #217

[English](./README.md)

2026-09-16，在真实 DSH WebUI 中，未修改的 main `1363ebffaf19c9ab4badf0137f6fe87acacaf989` 复现了报告中的匹配冲突，修复版本为 `098b8127444c67145e77a4e39f6a6f160280f5ed`。两者分别使用独立 worktree 和临时 Profile；环境为 macOS arm64、Node 25.1.0、pnpm 11.19.0、正式发布的 DSH 0.1.5-rc.1，以及已核验官方校验和的 Mnemon CLI 0.2.8。Scoped、Light context、Active capture 三个 Strategy 插件同时启用。

## 浏览器复现与验证

分别构建对应版本，再运行 `MNEMON_CLI_PATH=/path/to/mnemon pnpm e2e:serve --strategy-extensions`。在 Chrome 的「记忆系统 → 运行时」中添加工作记忆 `X` 和 `EGO_LINUX_CHROME`。以下修改均通过可见 UI 操作，并经过正常确认弹窗。

| 操作 | main | 修复后 |
| --- | --- | --- |
| 移除 `X` | 提示 `Multiple memory entries contain "X"; use a unique substring.`，两条记录均保留 | 仅移除 `X` |
| 将 `X` 编辑为 `LINUX` | 同样报歧义，原 `X` 保留 | 成功，包含它的另一条记录保持原样 |
| 移除编辑后的 `LINUX` | 编辑失败，未进入此步骤 | 即使它也是 `EGO_LINUX_CHROME` 的子串，仍成功移除 |

修复后又添加新的 `X` 并直接移除，重复了报告中的原始操作。直接读取临时目录中的 `memories.json` 和 `MEMORY.md`，确认最终内容只有 `EGO_LINUX_CHROME`，其创建和更新时间均未改变。基线在两次失败操作后仍保留两条原始记录。

![基线：精确移除被拒绝](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-217-exact-runtime-match/before-remove.png)

![修复后：精确替换成功](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-217-exact-runtime-match/after-edit.png)

![修复后：精确移除，包含该子串的另一条记录保留](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-217-exact-runtime-match/after-remove.png)

## 自动验证与边界

修改生产代码前，新增的五个 controller 用例和两个 Source/client 用例在旧实现上失败。修复后 Runtime 包的 51 个测试全部通过，覆盖两种 target 和两种操作、重复精确条目拒绝、子串回退、分支投影、拒绝时文件不变、容量维护、管理操作的确认/修订检查、只读 UI 和 Source 实例隔离。

`MNEMON_NATIVE_TEST_CLI=/path/to/mnemon pnpm run verify` 通过：所有插件构建、类型和测试，1,128 个根测试（五个预期的可选测试跳过），确定性构建、真实隔离 Headless 激活、包与入口检查，以及发布意图验证。真实 Native 集成覆盖写入、召回和遗忘，根测试还执行了实际 Native 容量工作流；Memory Spaces 包另有一个预期的可选测试跳过。以上直接 UI 操作不需要模型 API；E2E Profile 的模型端点是确定性回环夹具。

内置浏览器在测试开始前未能加载 DSH 客户端 bundle；Chrome 正常加载同一未修改的 Host，未调整浏览器或 Host 安全设置。这份记录对应 macOS/Chrome，不代表已验证报告者的 Windows/WSL 浏览器环境。修复不引入持久化格式变更或数据迁移。
