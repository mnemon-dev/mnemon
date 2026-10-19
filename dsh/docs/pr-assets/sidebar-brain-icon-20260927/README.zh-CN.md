# 记忆系统大脑图标

[English](./README.md)

验证的实现提交：`d02f1502d05c49784452b502c2e3946748d990b0`。分支包含已合并的 #290、#291，完整保留“可组合记忆 (dsh-mnemon)”及中英文可组合记忆描述。

记忆系统改用原创的经典大脑轮廓。原生侧栏、备用入口和 Better Sidebar 共用同一份 SVG 路径，采用 16 单位画布、1 单位描边及 `currentColor`。原生 DSH 继续负责按钮尺寸、标签和选中状态。

| 修改前 | 修改后 |
|---|---|
| [当前名称与数据库图标](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-combined.png) | [当前名称与大脑图标](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-brain-icon-20260927/expanded.png) |

[折叠入口](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-brain-icon-20260927/collapsed.png)可打开状态正常的记忆系统。已在真实 WebUI 验证展开、折叠导航以及返回“插件”页；插件列表仍显示“可组合记忆 (dsh-mnemon)”。

环境：macOS、Node 24.20.0、pnpm 11.19.0、正式 DSH `0.1.7-rc.2`、Mnemon CLI `0.2.9`、本分支 Root 制品及十六个未修改的伴随插件制品。Root SHA-256：`3f0d0008471f565abd9d9dcb415df3ba8fb46b3f28d030ffa6ab8f6f7a0094cc`。profile、工作区和记忆均为隔离测试数据，未调用真实模型 API。

验证通过：`pnpm build`、`pnpm typecheck`、四个侧栏测试文件中的全部 40 项测试、`pnpm exec vitest run --dir tests --maxWorkers=2`（1,420 项通过、9 项跳过）、`pnpm verify:package`、`pnpm release:intent`、`pnpm verify:docs` 和 `git diff --check`。浏览器证据覆盖原生 DSH；已有自动化测试覆盖备用入口和 Better Sidebar 的注册与行为。不涉及存储、配置、权限或通信契约变更。
