# 稳定的 Mnemon 皮肤钩子 — Issue 273

[English](./README.md)

基线为 `6dc4e4201585223f7f1da371256da11deee34d6c`（v0.5.14），生产修改为 `fb584052`。环境为 macOS arm64、Node 24.20.0、pnpm 10.13.1、正式发布的 DSH 0.1.7-rc.2 和官方 Mnemon CLI 0.2.9。每个 Profile 均安装完整的十七个 Mnemon tarball，并同时启用 Scoped、Light Context 和 Auto Capture。

工作台在 Sidebar 和 Builtin 模式下均提供 `[data-dsh-plugin="dsh-mnemon"][data-dsh-part="mnemon-view"]`。皮肤可通过此稳定选择器覆盖 `--mn-bg`、`--mn-backdrop` 和 `--mn-surface`。生产 CSS 和默认不透明表面保持不变；挂载到 body 的弹窗不属于此钩子的范围。

## 浏览器证据

夹具通过公开的 DSH 插件命令打包、安装一个小型 Client 皮肤。它只使用上述属性组合与三个已声明 token，不依赖哈希类名、`!important` 或浏览器注入 CSS。

- [基线](./before-browser.json)：皮肤样式位于 Mnemon 样式之前，但没有根节点匹配钩子，仍显示官方白色表面。
- [修复后 Sidebar](./after-before-order-browser.json)：恰好一个根节点匹配；即使皮肤样式排在前面，实际计算背景仍采用浅绿色皮肤。
- [修复后 Builtin](./after-builtin-browser.json)：同一皮肤生效，输入框保持可见，三个增强全部开启，设置保存成功，对话正常回复。
- [Runtime 编辑弹窗](./dialog-browser.json)：portal 没有工作台钩子，保留默认表面。界面新增合成 Runtime 记录、编辑为 SQLite WAL 后，浏览器刷新仍保留该记录和 Builtin 模式。

新的 Chrome 会话完成了其余层叠顺序对照：[皮肤样式后置](./after-order-browser.json)时，根节点、页头和画布均采用浅绿色表面（[截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-273-skin-hooks/after-order-sidebar.png)）；[不安装皮肤](./default-browser.json)时，三者均保留原有不透明白色表面和两层渐变（[截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-273-skin-hooks/default-sidebar.png)）。两组中的 Runtime、Documents 和 Memory Spaces 导航均正常。这两组只检查 Sidebar 表面，没有启动任务会话或写入记忆。

| 基线 Sidebar | 修复后 Sidebar |
| --- | --- |
| ![基线表面](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-273-skin-hooks/before-skin-sidebar.png) | ![稳定皮肤覆盖](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-273-skin-hooks/after-skin-sidebar.png) |

![修复后 Builtin 表面](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-273-skin-hooks/after-skin-builtin.png)

## 验证

两个新增钩子回归在基线失败，修复后的定向 Client 检查 **67 项通过**。`pnpm verify` 通过：Root **1,364 项通过 / 7 项跳过**，插件 **402 项通过 / 2 项跳过**，以及类型、确定性构建、Headless 和包检查。Root 解包体积为 **1,374,875 字节**，低于未调整的 1,376,000 字节上限。`verify:plugins --skip-build` 的 **16 个独立插件仓库和 17 个制品全部通过**。

可选 Native 集成测试使用已校验的 CLI 0.2.9 实际运行；另一个隔离存储冒烟完成了合成事实的写入、召回和软删除。复用的基线制品校验和与已保存的 `6dc4e4` 制品完全一致；修复版重新打包。生成的 Client 注释包含构建位置，因此不将无关 tarball 的哈希差异等同于生产代码修改。

结构化汇总：[验证结果和制品哈希](./verification.json)、[隔离 CLI 冒烟检查](./cli-smoke.json)。

## 复现

将 [rc2-profile.json](./rc2-profile.json) 复制为新临时 Profile 中的 `package.json`，用 Node 24 在该目录执行 `npm install --ignore-scripts --registry https://registry.npmjs.org`。在基线与修复版 checkout 中分别构建 Root 和插件，再将十七个包打入各自的空目录：

```sh
pnpm build
pnpm --workspace-concurrency=1 -r build
node --input-type=module - /absolute/empty-artifacts <<'JS'
import { readReleasePackages, createReleasePlan, packRelease } from './scripts/release.mjs'
await packRelease(createReleasePlan(await readReleasePackages(), { baseVersions: new Map() }), process.argv[2])
JS
MNEMON_CLI_PATH=/absolute/verified/mnemon node scripts/fixtures/serve-skin-hook.mjs \
  --profile /absolute/rc2-profile --artifacts /absolute/packed-artifacts \
  --state /absolute/empty-state --skin-order before
```

两次对比的最后一条 fixture 命令都从本 PR checkout 执行，仅将 `--artifacts` 切换为基线或修复版 tarball 目录，并使用新的 `--state`。基线 checkout 本身不包含这些新增复现脚本。

[场景脚本](../../../scripts/fixtures/serve-skin-hook.mjs) 使用共享的[打包 Profile 辅助脚本](../../../scripts/fixtures/packed-web-fixture.mjs)。打开 state 中 `private.json` 记录的私有 URL，选择其 `workspace` 目录和官方 Coding 预设，再打开 Mnemon。使用新的 state，并传入 `--skin-order after` 或 `none`，可分别检查皮肤样式后置或未安装皮肤的默认表现；也可从 DSH 插件设置停用已安装皮肤。

本次浏览器环境对打印出的 `127.0.0.1` URL 返回 `ERR_BLOCKED_BY_CLIENT`；只将 hostname 换成 `localhost` 后，即可通过正常的 token 换 cookie 重定向访问同一回环 Host。认证机制和浏览器保护没有改变。认证 URL 和原始日志保持私有。`SIGTERM` 停止夹具；可选 `--port` 可指定稳定端口。

全程只使用临时合成数据。正式适配器、工具、传输、存储和浏览器均为真实实现，回环模型仅提供确定性回复。本记录不代表真实模型质量或 Windows 执行结果。Flash、Teams、lifecycle、OpenViking 服务及 Windows 可选检查保留其明确跳过记录。
