# v0.5.7 发布验证

**简体中文** | [English](./README.md)

2026-09-10 验证的版本化提交为 `f237e9a1b195c879472713fac160d965c7b0d522`，基于已合并的 main `accabbd3ed4cd2e7fd0e2c6d399b4fe7d04a7122`。环境与截图 hash 见 [verification.json](./verification.json)。

`pnpm verify` 通过 1,218 项测试（根包 895、插件 323），以及确定性构建、类型检查、真实 Headless 激活/重启和包验证。跳过七项 opt-in 测试：五项真实模型检查、Native 集成和 Windows 二进制冒烟。`node scripts/verify-plugin-artifacts.mjs --skip-build` 验证十六个独立仓库、十七个 tarball、公开 SDK/Client 消费者与真实 DSH Starter/可选插件激活。`release:intent` 和 `release:check` 精确选择四个变化包：Starter 0.5.7 与三个 Source 0.5.6。

构建后运行 `pnpm e2e:serve --document-archive`。在真实浏览器中检查无会话状态页，再使用 Mnemon E2E preset 绑定临时工作区，创建并激活 Native 记忆空间，新建合成档案。在确认框中归档，刷新浏览器后重新打开归档原文，再从记忆空间 → 内容读取 Native 索引。原文与 SHA-256 均保留，持久化档案为 revision 2、状态 `archived`。归档步骤未记录到浏览器页面错误或失败的 Mnemon HTTP 响应。

| 截图 | 验证结果 |
|---|---|
| [版本与状态](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.7/01-version-status.png) | 实际运行 0.5.7，RPC 已连接，无需活动会话 |
| [归档原文](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.7/02-document-archived.png) | 刷新后仍可读取原文与 Host 回执 |
| [刷新后的索引](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.7/03-index-after-reload.png) | 一个 Native 索引，含冷存储引用与匹配的内容 hash |

模型仅提供预设的归档决策；浏览器、DSH 传输、Host、存储和 Native CLI 均为真实实现。全部内容为合成数据，结束后清理临时 Profile 和浏览器。本次发布冒烟不重复历史完整浏览器矩阵、Windows/Electron 检查或真实模型质量实验。[DSH 兼容报告](../issue-223-dsh-015/README.zh-CN.md) 与[归档回归报告](../issue-222-document-archive/README.md) 保留原始 revision 和更广的覆盖范围。
