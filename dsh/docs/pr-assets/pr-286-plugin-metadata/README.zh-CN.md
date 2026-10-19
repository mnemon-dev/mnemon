# DSH 插件元数据 — PR #286

**简体中文** | [English](./README.md)

于 2026-09-26 验证 `84dc35aaa832e341a01add4b4e934a90a056c46b`。该提交在贡献者原始提交 `d16d017aa5bb0c3bbbdb40dc5a95554266b6522a` 后追加验证与发布维护；原始提交、locale 文案和 package 修改均保留。

## 环境与复现

- macOS，Node 24.20.0，pnpm 10.13.1。
- 完整工作区和制品验证使用仓库锁定的 DSH 0.1.5-rc.1 组合；元数据与浏览器验收使用单独安装的正式 DSH 0.1.7-rc.2 profile。
- 浏览器 profile 安装全部 17 个 Mnemon 打包制品，并启用三个可选 Strategy 增强。环境采用隔离的合成数据和 DSH 官方界面，没有额外皮肤，也未调用外部模型或 Provider API。
- 根 tarball 为 `dsh-mnemon-0.5.15.tgz`，SHA-256 为 `c36e723d632895108b8e8fcbdf8a8e15a79fd7362143758691b9f2bbebd1d301`，包含本 PR 改动但未提升发布版本。浏览器使用完全相同的制品字节。

构建并打包本 PR，将 tarball 安装到一次性 DSH profile 后重启。打开“插件”，选择 `dsh-mnemon`，检查中文文案；再在“设置 → 常规 → 语言”切换为 English，关闭设置并检查同一插件。下方两张截图均使用 1280 × 720 视口。

## 可见结果

中文插件详情及组件行显示 `三级记忆 (dsh-mnemon)` 和贡献者提交的中文说明。

![DSH 中文插件元数据](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/pr-286-plugin-metadata/plugins-zh.jpg)

英文界面的同一条目保留现有 `dsh-mnemon` 名称及完整 package 说明。

![DSH 英文 manifest 回退](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/pr-286-plugin-metadata/plugins-en.jpg)

截图中 `cordis:group` 显示关闭而子组件仍运行，是另行调查的既有状态问题。本记录仅验证元数据展示，不作为分组启停行为的验收证据。

## 验证结果

```sh
pnpm exec vitest run tests/package-locales.spec.mjs tests/release.spec.mjs
pnpm verify
pnpm verify:plugins
pnpm release:intent
```

- 42 项定向测试全部通过，其中包含 23 项 locale 资源测试。拒绝路径覆盖错误通配/具体导出目标、缺少 `en.json`、非法文件名与目录、损坏 JSON、非对象字典/元数据及空白或非字符串展示字段。
- 完整工作区验证通过：确定性构建、独立插件类型与测试、1,403 项根测试、真实 Headless 激活、包内容、公开入口及包 lint。8 项选择性根测试按默认跳过，性能和制品上限保持不变。
- 16 个独立插件仓库与 17 个制品的打包验证全部通过，包含外部消费者构建/类型/测试、从 0.4.7 进行真实 Starter 升级，以及三个可选 Strategy 插件同时激活。
- 发布意图验证确认根包 patch changeset 已覆盖。本次补充使仅修改 locale 的变更也需要发布意图，并在准备发布时检查版本提升。
- 包含 52 个文件，解包大小为 1,374,969 bytes，低于保持不变的 1,376,000-byte 上限。

正式发布的 `@deepseek-ai/dsh-app-boot@0.1.7-rc.2` 中 `readPluginMeta()` 也读取了本次新打包并解压的制品。英文名称和说明与基线展示文案逐字一致，中文字段与截图一致。[脱敏结果](./metadata-verification.json)。

本次仅覆盖根插件元数据，不包含所有组件包的翻译，不改变配置或已存记忆，也不代表已验证所有 DSH 版本、桌面皮肤或远程 Provider。已复核中英文界面指南、快速开始、配置参考与运维指南，其操作流程仍适用。
