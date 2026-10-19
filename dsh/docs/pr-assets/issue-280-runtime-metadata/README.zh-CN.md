# Runtime 投影元数据 — Issue 280

[English](./README.md)

基线为 `6dc4e4201585223f7f1da371256da11deee34d6c`（v0.5.14），生产修改为 `5b8cd640` 和 `ff66e6ba`。环境为 macOS arm64、Node 24.20.0、pnpm 10.13.1、正式发布的 DSH 0.1.7-rc.2 和官方 Mnemon CLI 0.2.9。Profile 安装完整的十七个 Mnemon tarball，同时开启 Scoped、Light Context 和 Auto Capture。

Runtime 模型投影现在会在每条正文上方显示已记录的重要性及经过的完整天数，例如 `[importance=critical; created=14d; updated=2d]`。未来时间显示为 `future`，无效时间显示为 `unknown`；整份投影只采样一次时钟。正文、持久化 JSON/Markdown、容量计量和修改匹配规则不变。替换与删除仍只使用正文，当前指令继续优先。

元数据标注计入现有投影预算。默认 Strategy 现在明确 Runtime 受预算限制，避免密集存储被截断时仍声称内容完整。没有引入迁移、新设置或按需展开路由。

## 证据与验证

真实 WebUI 基线的模型请求收到三个合成存储条目，但实际出站内容包含 **零条元数据标注**。修复后请求包含三条标注：critical USER 为 **14d/2d**，normal MEMORY 为 **7d/3d**，low MEMORY 为 **4d/1d**（依次为创建/更新年龄）。`METADATA280_SHOW` 从该次请求中提取 Runtime 快照，页面回复不预设期望答案。

| 基线 | 修复后 |
| --- | --- |
| ![投影缺少元数据](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-280-runtime-metadata/before-model-projection.png) | ![实际投影中的重要性和年龄](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-280-runtime-metadata/after-model-projection.png) |

随后通过真实 WebUI 对话新增、替换、删除一条 USER 记录，每步均收到已提交的工具回执；最终 Runtime 恢复为原来的三个种子条目。界面创建档案后，按正文中的 `DOC280_CHECK` 搜索返回唯一匹配文档。界面创建并激活 Native 空间，再由官方 CLI 0.2.9 写入 `NATIVE280_CHECK: The disposable project uses SQLite WAL for durable storage.`，WebUI 关键词检索返回唯一结果，正文与记录 ID 均完全一致。

![CLI 写入后由 WebUI 读回](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-280-runtime-metadata/native-cli-webui-recall.png)

- 六个新元数据回归在 Source 修复前失败；另一个密集存储回归在 Strategy 提示修正前失败。修复后定向检查 **50 项通过**，Runtime Source **63 项**、默认 Strategy **17 项**测试通过。
- `pnpm verify`：Root **1,363 项通过 / 7 项跳过**，插件 **406 项通过 / 2 项跳过**，以及类型、确定性构建、Headless 和包检查全部通过。Root 解包体积为 **1,374,802 字节**，低于未调整的 1,376,000 字节上限。
- `verify:plugins --skip-build` 的 **16 个独立插件仓库和 17 个制品全部通过**。可选 Native 集成测试使用已校验的 CLI 0.2.9；另一个隔离存储冒烟也完成了合成事实的写入、召回和软删除。

复用的基线 tarball 校验和与已保存的 `6dc4e4` 制品完全一致，修复版重新打包。生成的 Client 注释包含构建位置，因此不将无关 tarball 的哈希差异等同于生产代码修改。

结构化证据：[验证汇总和制品哈希](./verification.json)、[实际 Runtime 投影与修改回执](./wire-evidence.json)、[CLI/WebUI 流程](./cli-webui-evidence.json)、[隔离 CLI 冒烟检查](./cli-smoke.json)。

## 复现

将 [rc2-profile.json](./rc2-profile.json) 复制为新临时 Profile 中的 `package.json`，用 Node 24 在该目录执行 `npm install --ignore-scripts --registry https://registry.npmjs.org`。在基线与修复版 checkout 中分别构建 Root 和插件，再将十七个包打入各自的空目录：

```sh
pnpm build
pnpm --workspace-concurrency=1 -r build
node --input-type=module - /absolute/empty-artifacts <<'JS'
import { readReleasePackages, createReleasePlan, packRelease } from './scripts/release.mjs'
await packRelease(createReleasePlan(await readReleasePackages(), { baseVersions: new Map() }), process.argv[2])
JS
MNEMON_CLI_PATH=/absolute/verified/mnemon node scripts/fixtures/serve-runtime-metadata.mjs \
  --profile /absolute/rc2-profile --artifacts /absolute/packed-artifacts \
  --state /absolute/empty-state
```

两次对比的最后一条 fixture 命令都从本 PR checkout 执行，仅将 `--artifacts` 切换为基线或修复版 tarball 目录，并使用新的 `--state`。基线 checkout 本身不包含这些新增复现脚本。

[场景脚本](../../../scripts/fixtures/serve-runtime-metadata.mjs) 使用共享的[打包 Profile 辅助脚本](../../../scripts/fixtures/packed-web-fixture.mjs)。启动前写入三个简短 USER/MEMORY 合成条目，分别使用 critical/normal/low 重要性及不同创建、更新年龄，每个年龄均在完整天数后额外留出六小时。真实 Runtime 初始化会保留这些值。

打开 state 中 `private.json` 记录的私有 URL，选择其 `workspace` 目录和官方 Coding 预设。每轮分别发送 `METADATA280_SHOW`、`METADATA280_ADD`、`METADATA280_REPLACE`、`METADATA280_SHOW`、`METADATA280_REMOVE`、`METADATA280_SHOW`。这些修改使用真实的 `mnemon_runtime_memory` 工具，省略 USER 的 branches，并汇总实际回执。`observations.json` 记录合成 Runtime 出站内容、实际工具 schema 和回执，不导出完整系统提示。

若本浏览器环境对打印出的 `127.0.0.1` URL 返回 `ERR_BLOCKED_BY_CLIENT`，只将 hostname 换成 `localhost` 即可通过正常的 token 换 cookie 重定向访问同一回环 Host；本轮即采用此方式，认证机制和浏览器保护没有改变。认证 URL 和原始日志保持私有。`SIGTERM` 停止夹具；可选 `--port` 可指定稳定端口。

全程只使用临时合成数据。正式适配器、工具、Provider、传输、持久化和浏览器均为真实实现；确定性模型读取实际请求内容。本记录不评价真实 Flash 质量或 Windows 行为。Host 重启覆盖来自自动化 Headless 检查，不声称手动完成了冷启动后的浏览器验收。Flash、Teams、lifecycle、OpenViking 服务及 Windows 可选检查保留其明确跳过记录。
