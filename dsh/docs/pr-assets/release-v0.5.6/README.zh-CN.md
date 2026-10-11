# v0.5.6 发布验证

[English](./README.md) · [发布说明](../../zh-CN/releases/v0.5.6.md)

于 2026-09-10（Asia/Shanghai）在独立发布 worktree 中执行，基于 main `0a499d11e9853851ed11aa92633e8122d11f9545`。截图使用已升级版本的发布组合：Starter 0.5.6、五个变化插件 0.5.5、十一个未变化插件 0.5.4。本次发布提交调整版本、生成的 Provider 版本声明与文档，没有在该 main revision 之外添加运行时行为。

环境：macOS arm64、Node 22.19.0、pnpm 10.13.1、已发布的 DSH 0.1.2-rc.1、Native CLI 0.2.7。

| 检查 | 结果 |
|---|---|
| 完整 `pnpm run verify` | 通过：852 个根包测试、324 个插件测试 |
| Native 集成 | 临时 View 写入、召回与 forget 通过 |
| 确定性构建、类型、公开入口、包检查 | 通过；根包 46 个文件，解包后 1,256,247 字节 |
| 真实 Headless Profile | 39 个工具、8 个代表性 Mnemon 工具、重启与旧入口禁用通过 |
| `node scripts/verify-plugin-artifacts.mjs --skip-build` | 16 个独立仓库、17 个制品、外部 consumer 与真实 Starter 组合通过 |
| 相对 v0.5.5 的发布选择 | 选中六个包，依赖分层与正式 `latest` 标签正确 |
| WebUI | 设置保存、版本显示、Runtime 写入与刷新通过；控制台零错误 |

跳过五个需显式开启的 Flash 测试和一个 Windows 专用二进制测试。本次发布检查未重跑或重评历史模型实验。

## 浏览器冒烟

完整构建后运行 `pnpm e2e:serve`，使用其带认证的启动 URL 和临时 Profile；请求均使用回环地址和脚本模型端点。

1. 在设置 → 记忆系统中选择集中式工作区隔离与全局 USER.md，将集中根目录设在临时 fixture 内。保存后界面显示已实时生效。
2. 注册 fixture 工作区。状态页显示 `dsh-mnemon 0.5.6` 和集中式工作区存储。
3. 添加截图中的合成工作记忆，刷新页面后重新打开 Runtime，仍显示一条记忆及原始创建时间。
4. 检查磁盘：条目位于 `central/workspaces/<canonical-path-hash>/runtime/memories.json`；项目目录未产生 `.mnemon`。

三张截图均已目视检查。测试后删除临时 Profile、记忆与浏览器标签页。更全面的九张 [Core 工作流截图](../issue-189-core-workspaces/README.zh-CN.md) 保留原始 revision 标注。

| 截图 | 证据 |
|---|---|
| [01 已保存设置](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.6/01-settings-saved.png) | 集中隔离模式和持久化根目录设置 |
| [02 版本与存储](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.6/02-version-and-storage.png) | 发布版本、工作区选择器、当前范围和一条项目记忆 |
| [03 刷新后 Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.6/03-runtime-after-reload.png) | 真实刷新页面后合成记忆仍然存在 |
