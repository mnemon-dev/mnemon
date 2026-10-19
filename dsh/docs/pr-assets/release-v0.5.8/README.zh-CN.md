# v0.5.8 发布验证

**简体中文** | [English](./README.md)

实际运行的版本化应用提交为 `099d39f8fabf8d118b87455b4399f80eb2f98f7b`，基于已合并 main `ae710d3a552df76739b8ada36240e9176f2e4726`。本记录和截图随后提交，不再改动应用代码。

本次仅将 Starter 升级到 0.5.8，消费六个 patch changeset；十六个官方插件版本不变。冻结发布计划仅选择 `dsh-mnemon@0.5.8`。

验证环境：macOS arm64、Node 25.1.0、pnpm 11.19.0。

- `pnpm run verify`：1,301 项通过，其中主包 978 项、插件 323 项；七项 opt-in 检查跳过。类型、确定性构建、真实 Headless 激活与重启、包验证通过。
- `node scripts/verify-plugin-artifacts.mjs --skip-build`：十六个独立仓库、十七个制品、公开 SDK/Client 消费方、打包 Starter 及三个可选策略插件通过。
- 真实 Mnemon CLI 0.2.7 集成验证：在隔离 Store 中创建、写入、召回与删除通过。
- 基于前一版本 v0.5.7 的发布意图消费与版本选择校验通过。
- 包含 47 个文件，压缩后 284,282 B，解包后 1,278,996 B，低于 1,280,000 B 上限。

真实浏览器使用 DSH 0.1.5-rc.1，启用 Starter 和三个策略增强插件。鉴权后的无会话状态页显示 0.5.8；新增的合成 Runtime 记录在刷新浏览器后仍可读取。创建并激活 Native 记忆空间后，状态页显示 Mnemon CLI 0.2.7 连接正常、1/1 空间激活。两张截图均经过目视检查。验证结束后已清理临时 Profile、进程和浏览器标签页。

| 刷新后的 Runtime 记录 | 版本与 Native 状态 |
| --- | --- |
| [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.8/01-runtime-after-reload.jpg) | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.8/02-version-native-status.jpg) |

未调用外部模型 API。这些检查验证应用与插件行为，不评价模型质量、缓存命中或历史 token 消耗。此前各 issue 记录保留各自的实际复现提交。

