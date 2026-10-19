# 内置集中工作区存储 — issue #189

[English](./README.md) · [证据索引](../README.md)

2026-09-09 在 macOS 上使用正式发布的 DSH 0.1.2-rc.1、Node 22.19.0、pnpm 10.13.1 和 Mnemon Native 0.2.7 验证。新实现从 `main` 的 `820b0b092fe68eb7ce6fbf1eaa12e9cf39087e31` 分出，替代 PR #215 的独立存储插件方案。原工作目录和旧分支均保留。

功能验收 revision 为 `2314d6a44ee21b74f1e3bb22a0533d90285c5eea`，最终布局和完整自动化验证对应 `51446b51ed636eace6f6ca4d206969561ada194d`。截图 01–03、05–07 对应功能版本，04、08、09 对应最终布局；均为本次重新采集，没有复用旧插件 PR 的截图。

## 实现边界

根包 Host 统一解析四种布局和规范化工作区标识；Core 传递操作范围并保持 View 权限边界。现有 Source 通过原配置契约接收目录。没有新增包、依赖、存储贡献 API 或 SDK 导出。现有 Memory Spaces 包仅调整了长路径展示样式。

## 自动化验证

精确复现命令见[英文记录](./README.md#automated-verification)，使用固定版本的 Node/pnpm 环境。

| 检查 | 结果 |
|---|---|
| 根包测试 | 852 通过；5 个 opt-in Flash 检查跳过 |
| `verify` 中的独立插件测试 | 324 通过；1 个 Windows 专属冒烟测试跳过 |
| 确定性构建、TypeScript、包出口与发布包检查 | 通过 |
| 真实 Headless Profile | 39 个工具、8 个代表性 Mnemon 工具；重启与旧 Entry 停用检查通过 |
| 真实 Native CLI | 临时 View 写入、召回、删除通过 |
| 独立制品 | 16 个独立插件仓库、17 个 tarball 通过；真实 DSH 仅安装根包 Starter 后可正常激活 |

覆盖四种存储模式、旧 `dataDir` 配置、registry 拒绝路径、Headless/Builtin 会话归属、真实 Source 的四区域写入、USER.md 两种范围、符号链接别名、尚未创建的子路径、Unicode、同名目录、重命名和无破坏切换。最新 main 的 23 个容量工作流测试也全部通过。

最初发布包检查触发现有体积上限。单独构建 base revision 后，基线为 1,249,360 字节，最终为 1,256,247 字节，增加 6,887 字节，文件数仍为 46。增量仅来自 Host/UI、公开范围类型和 README 文案；没有打入 Source 或 Provider 实现。上限小幅调整为 1,260,000 字节。

## 真实 WebUI 验收

构建后运行已有的 `pnpm e2e:serve`。夹具使用临时 Profile、全局根、两个临时工作区、自定义集中根和仅监听 loopback 的固定回复模型；操作经过真实 DSH 设置、RPC 和 Source。

1. 选择集中工作区存储和全局 USER.md；相对根目录无法保存，绝对目录保存后实时生效。
2. 在 A 写入项目事实和全局偏好，创建档案，启用 Holographic，并通过真实 Native CLI 创建 88 KB 数据库。
3. B 看不到 A 的项目事实、档案或 Provider 映射，只能看到全局偏好；另写入 B 的独立项目事实。
4. 清空集中根并保存后使用默认根的工作区子目录，全局偏好仍可见；恢复自定义根后原有事实恢复。切换根目录及 Sidebar/Builtin 前后，5 个控制文件的 SHA-256 一致。
5. 建立真实 A/B 会话，Builtin 跟随所属会话且不显示工作区选择器。重启 Host 后，两份项目事实、全局偏好、A 的档案和 Provider 映射均保留。
6. 当前会话属于 B 时查看 A，差异提示出现；对齐操作返回 B。完成中文深色、英文浅色及 390×844 Sidebar 验证。

磁盘断言确认 A 的 `runtime`、`data`、`documents`、`state` 同属一个哈希子目录，两个项目均没有 `.mnemon`。桌面 Provider 画布的可视/滚动宽度均为 992px；窄屏 Runtime 和 Memory Spaces 均为 326px。最终浏览器控制台未发现错误。

截图复查发现并修正了长目录导致的横向溢出、英文差异提示挤压工作区选择器两处布局问题。最终截图中的路径受列宽约束，标题栏控件可换行；视觉验收后同步更新了已有样式指纹。

## 截图

| 证据 | 内容 |
|---|---|
| [01 中文深色设置](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/01-settings-zh-dark.png) | 范围、可选集中根和独立全局用户档案 |
| [02 A 运行时](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/02-workspace-a-runtime.png) | A 项目事实与全局偏好 |
| [03 A 档案](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/03-workspace-a-document.png) | 托管档案创建成功 |
| [04 A Provider](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/04-workspace-a-providers.png) | 真实 Native 数据库、Holographic 映射及修正后的长路径布局 |
| [05 B 隔离](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/05-workspace-b-isolation.png) | 全局偏好可见，A 项目事实不可见 |
| [06 英文浅色设置](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/06-settings-en-light.png) | 完整的集中存储标签与目录控件 |
| [07 重启后的 Builtin](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/07-builtin-b-after-restart.png) | B 所属会话读取恢复后的 B 事实 |
| [08 工作区对齐](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/08-workspace-alignment.png) | 查看 A、执行 B，选择器和对齐操作均可见 |
| [09 390px Sidebar](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-189-core-workspaces/09-sidebar-390px.png) | 窄屏记忆空间、长路径和对齐控件 |

## 验证限制

本次验证本地持久化和 UI/传输，不代表真实模型沉淀质量或远端 Provider 账号验收。5 个真实 Flash 检查未开启，Windows 二进制冒烟需要 Windows 环境。移动或重命名工作区会使用新的路径哈希，旧数据保留且不自动迁移；远端 Provider 仍遵循自身命名空间共享语义。验证结束后清理临时数据与浏览器标签页，截图仅含合成内容和临时路径。
