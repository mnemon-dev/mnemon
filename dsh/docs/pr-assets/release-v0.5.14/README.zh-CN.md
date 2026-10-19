# v0.5.14 打包发布验证

[English](./README.md) | **简体中文**

测试 revision：`07dac3d3d2d029ce3e4610226d40a757509d6dc7`，日期为 2026-09-25。后续只补充证据的提交不改变任何包的发布输入。[verification.json](./verification.json) 记录十七个制品的完整性、截图哈希和 Runtime 正文文件的精确哈希。

发布 worktree 消费按 #277、#276、#278 顺序合入的修复，只将 Starter 升级到 0.5.14、OpenViking Provider 升级到 0.5.6、Memory Spaces Source 升级到 0.5.9。

## 检查

- Node 24.20.0 / pnpm 10.13.1 下完整 `pnpm verify` 通过：根测试 1,360 项通过、7 项跳过；工作区插件 402 项通过、2 项跳过。官方 Native CLI 0.2.9 的两项 opt-in 检查均实际运行，包括同一 View 内的两个归档目的地。其余跳过项为在线模型、codec/外部 cohort opt-in、真实服务或平台限定场景。
- 串行 `pnpm verify:plugins --skip-build` 通过，覆盖十六个独立插件仓库、十七个打包制品、外部 SDK 消费者和三个可选 Strategy 同时启用的真实 DSH Starter。仅限制 worker 数量，测试与性能阈值保持不变。
- `release:status`、`release:version`、`release:check`、`release:intent` 通过。pnpm 10.13.1 重新生成 lockfile，没有增删或升级第三方包版本。
- 版本化前的集成分支通过 99 项针对性测试及两套官方 Teams 矩阵：0.1.7-rc.1 与旧版 cohort 各十六种组合。旧版预期拒绝与验证边界见 [#275 记录](../issue-275-agent-teams/README.zh-CN.md)。

## 真实 WebUI

使用已有的 [Team 制品夹具](../../../scripts/fixtures/serve-team-review.mjs)，将十七个发布版本 tarball 安装到隔离的官方 DSH/Teams 0.1.7-rc.1 Profile，配置官方 CLI 并启用三个 Strategy 增强。存储为一次性测试数据，模型 Endpoint 为本地夹具，没有调用外部模型 API。

1. 打开记忆系统，确认版本显示 `dsh-mnemon 0.5.14` 且系统正常。
2. 通过界面新增合成 Runtime 记忆，编辑后确认精确保存正文。
3. 在设置页将 Agent Teams 从默认 `pause` 切换为 `scoped`，确认已保存并实时生效。
4. 使用夹具的 `SIGUSR2` 冷启动处理停止并重启 Host，再打开认证后的界面。Runtime 正文仍可见，文件 SHA-256 逐字节一致；设置仍选择 `scoped`，三个 Strategy 增强开关仍全部开启。

| 观察 | 截图 |
|---|---|
| 编辑与重启前新增 Runtime | [runtime-saved.png](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.14/runtime-saved.png) |
| 冷启动后的版本与健康状态 | [status-after-restart.png](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.14/status-after-restart.png) |
| 冷启动后保留编辑后的 Runtime 正文 | [runtime-after-restart.png](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.14/runtime-after-restart.png) |
| 冷启动后保留受限审查设置 | [settings-after-restart.png](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.14/settings-after-restart.png) |

可选的源码链接夹具在固定的 DSH 0.1.5-rc.1 开发 Profile 上未能加载 `dsh-client-hmr` bundle，尚未确定原因。打包安装到 0.1.7-rc.1 后通过，且没有修改 DSH 官方代码；本记录不声称已修复源码链接失败。发布冒烟检查不重复真实云账户或在线模型验收，更广的前后对比仍以原始 Issue 记录为准。
