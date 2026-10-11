# Runtime 归档写入权限 — issue #250

[English](./README.md) | [Issue #250](https://github.com/omdsh-dev/dsh-mnemon/issues/250) | [验证数据](./verification.json)

基线已复现报告中的失败顺序：两个符合能力要求的 Native 空间已激活，但越限新增失败；在下一回合重试相同输入后成功。修复版本在首回合即可成功，两条归档原文和待新增 Runtime 条目都通过逐字节核验。产品基线为 `6ad99cc1890714355e1bb0e9230f3fce674bfb73`。修复实现与可复用夹具位于 `246a50e2ae3cd63ef2fa05354c1297c7c8de5d57`；基线 checkout 仅复制了夹具代码。

2026-09-14 的环境为 macOS arm64、Node 25.1.0、pnpm 11.19.0、已发布的 DSH 0.1.5-rc.1 包，以及经过官方校验和验证的 Mnemon CLI 0.2.8。Native 发布压缩包 SHA-256 为 `96159ad2fe8f0531b5a00ab009849614c234032146b95fa2db5aa726a8922e2d`。每次浏览器运行使用独立的临时 `DSH_HOME`、`MNEMON_DATA_DIR`、工作区和本地模型端点，同时启用 Scoped、Light context、Active capture 三个可选 Strategy 插件。脚本只提供模型决策；View、工具、Source/Provider 插件、Native CLI、传输与 WebUI 均正常执行真实实现。界面显示的模型选择不代表调用了真实模型 API。

## 复现

构建所选 revision 后执行：

```sh
pnpm build
pnpm --workspace-concurrency=4 -r build
MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve --runtime-write-scope --strategy-extensions
```

在全新的 Mnemon E2E 对话中发送 `archive-scope-250`。[夹具](../../../scripts/fixtures/runtime-write-scope-model.mjs) 执行七次真实根 Agent 工具调用：两次 Runtime 新增、两次委派 Native 空间创建、两次委派激活，以及一次越限新增。委派工具继承发起回合的 View。夹具采用 512 字节的工作记忆投影上限与合成检查点，不读取个人记忆。子 Agent 必须收到真实 Source 回执后才完成，且调用次数有上限。

基线的前六次调用成功，第七次返回报告中的原始错误，且没有 unsupported-destinations 后缀：

```text
runtime memory archival requires an existing active writable Memory Space with exact writes and safe forget; activate a supported Memory Space or increase runtimeMemory.memoryLimitBytes
```

状态页确认 Runtime 保留两条条目、两个 Native 空间全部激活，归档记忆数量为零。待新增内容尚未提交，两条原有 Runtime 条目仍在。下一回合发送 `archive-scope-250 retry` 后，待新增检查点在完成归档后成功提交。两次尝试之间没有修改空间或 Provider 配置。

| 首回合越限新增失败 | 两个 Native 空间均激活，归档数量为零 |
|---|---|
| ![原始容量错误](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-250-runtime-write-scope/before-capacity-error.jpg) | ![两个 Native 空间均激活](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-250-runtime-write-scope/before-two-active-spaces.jpg) |

![下一回合重试同一份待新增输入成功](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-250-runtime-write-scope/before-next-turn-retry.jpg)

## 原因与实现

基线 Host 使用 View 召回授权中的 `memoryBodyIds` 过滤归档目标。当两个空间在同一 View 内稍后创建时，回合开始时固定的已激活命名空间范围仍为空。所属 Memory Spaces Source 的 `remember` 本来已经允许写入已知命名空间，以及该 View 创建的命名空间，因此 Host 的归档过滤拒绝了实际有写入权限的目标。

修复通过 `body-directory` 可选返回由 Source 定义、绑定精确 View 与 Source 的写入范围，与 Source 现有 `remember` 权限共用实现。Host 校验返回身份与命名空间数组，并在归档写入前复查权限和当前能力。空范围始终不授权任何目标；其他操作在回合开始后创建的命名空间仍被排除，需要新回合才能使用。未返回新字段的旧 Source 保留较窄的召回固定范围。错误会区分目录为空、写入范围为空或排除目标、Provider 能力不支持，附带范围与数量，并明确提示重试尚未提交的输入。

没有改变持久化格式、凭据或存储路径，回滚后保留所有已存条目。仅聚合 Host 与 Memory Spaces Source 需要 patch 发布，changeset 同时列出了这两个包。

## 修复侧验证

相同夹具在修复版本的首回合即成功：七次根工具调用、四个委派子 Agent、一个回合、八个步骤，无需下一回合重试。状态页显示一条 Runtime 条目、两个已激活 Native 空间和两条归档记忆。Runtime 仅保留待新增检查点，投影占用为 269/512 字节；内容页显示每个 Native 空间各有一条原始检查点。

| 首回合成功 | 两个已激活空间中各有一条归档 |
|---|---|
| ![第一次越限新增成功](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-250-runtime-write-scope/after-first-turn-success.jpg) | ![两条 Native 归档与一条 Runtime 条目](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-250-runtime-write-scope/after-two-archived-spaces.jpg) |

| Runtime 仅保留待新增检查点 | Native 中显示两条完整原文 |
|---|---|
| ![待新增 Runtime 检查点](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-250-runtime-write-scope/after-pending-runtime.jpg) | ![架构与发布检查点原文](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-250-runtime-write-scope/after-exact-native-content.jpg) |

使用 Native CLI 0.2.8 的 `--readonly` 模式从两个 store 各读取到且仅读取到一条内容，其 UTF-8 字节分别与夹具的两条 `archiveScopeSaved` 字符串完全一致。Runtime 恰有一条条目，其 UTF-8 字节与 `archiveScopePending` 完全一致。[验证数据](./verification.json) 保留汇总数量、相等性断言，以及所核验内容和截图的哈希。这项检查独立验证实际持久化结果，不依赖脚本模型的成功回复。

修复实现的自动化验证已通过：

- `MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon pnpm run verify`：Root 1,003 项通过、5 项显式开启用例跳过；Memory Spaces 169 项通过、1 项 Windows 专属用例跳过。workspace 类型、确定性构建、插件测试、真实 Headless 激活与制品验证全部通过。
- `pnpm run verify:plugins`：16 个独立插件仓库、17 个打包制品通过独立检查、公开 SDK 组合、真实 packed Starter 激活，以及三个增强同时激活验证。
- Native 0.2.8 Host 测试将两条完整检查点分别归档到同一 View 创建的两个空间，并提交待新增内容，不调用外部模型 API。
- 回归覆盖已知空间在固定后激活、同 View 创建、外部创建及下一回合重试、显式空范围收窄、返回范围格式错误或身份不符、权限撤销、精确 Source 实例选择、取消、回合结束、已有租约下卸载，以及旧 Source 的保守回退。
- 四项夹具测试验证收到成功回执后不重复子 Agent 写入、失败调用能够结束、无回执尝试有次数上限，以及激活时使用实际返回的命名空间 id。

首次完整验证在前面所有检查通过后触发了制品大小上限。最终制品解包后为 1,281,433 字节，基线为 1,279,223 字节，增加 2,210 字节。大小检查仍启用，上限小幅调整到 1,284,000 字节。随后完整 `verify` 运行通过。

## 限制

本记录证明了可确定复现的时间顺序与权限范围不一致，以及基线在下一回合恢复的行为；不代表已经确定原始 Windows 报告中的具体时序，也不证明存在瞬时目录读取竞态。模型决策由脚本控制，不评估语义路由质量。不宣称已测试 Windows、Electron 或真实第三方服务。记录仅包含合成截图与精简验证数据，没有复制完整会话、凭据或个人记忆。
