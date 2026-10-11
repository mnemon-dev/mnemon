# Issue 261 已发布包兼容性验证工具

[English](HARNESS.md) | [简体中文](HARNESS.zh-CN.md)

本目录包含独立的验证脚本。脚本不会修改 DSH 包，也不会通过别名将包指向源码检出目录。已构建的 Mnemon 源码只作为打包命令的输入；安装后的测试环境从测试专用的本机回环 registry 解析全部 17 个 Mnemon 包，并校验 SHA-512 完整性。Root bundle 组合全部 16 个配套插件，并启用 scoped、light-context 和 auto-capture 策略扩展。

运行环境需要 Node、npm、tar、Git，以及真实的 Mnemon Native CLI。本次记录使用 Node v25.1.0 和 Native v0.2.8。请在本验证工具目录中运行以下命令。将 `MNEMON_SOURCE` 设置为已构建的 Mnemon 检出目录，`NATIVE_CLI` 设置为 Native 可执行文件，`VERIFICATION_ROOT` 设置为空的外部目录。首次安装时，每次运行应使用独立的根目录。

```sh
node pack-artifacts.mjs --source "$MNEMON_SOURCE" --output "$VERIFICATION_ROOT/artifacts/fixed"
node pack-peer-fixture.mjs --output "$VERIFICATION_ROOT/artifacts/peer-fixture"
node packed-e2e.mjs --mode fixed --cohort alpha --artifacts "$VERIFICATION_ROOT/artifacts/fixed" --peer-artifact "$VERIFICATION_ROOT/artifacts/peer-fixture/artifact.json" --peers true --run-root "$VERIFICATION_ROOT/fixed-alpha" --native-cli "$NATIVE_CLI" --serve true
```

`--cohort rc` 使用已发布的 DSH 0.1.5-rc.2；`alpha` 固定使用 0.1.6-alpha.2。修复后的测试环境采用正常的 npm peer 依赖解析。超过 200 条 DSH 包记录必须全部使用所选版本组的精确版本。已安装的公开 SlotCore、Chat、Renderer 和 Conversation 模块必须与未经修改的公开 tarball 字节一致；缺少参照 tarball 时，脚本会从公开 npm registry 获取并校验其公开完整性值。

两种模式均可使用 `--framework-root /path/to/previously-verified-consumer`，把已验证环境中实际安装的公开框架版本组和配套 peer 固定为测试环境的精确依赖。DSH 版本必须与所选版本组一致，固定结果记录在 `framework-pins.json`。这样可避免上游 peer 范围把较新的预发布版本带入历史复现；修复模式仍采用正常 npm 解析，不修改公开包的元数据或字节。Issue #265 的未固定 RC2 首次安装遇到不完整的 RC3 传递发布后，采用了此选项。

`--serve false` 只准备并验证测试环境，不启动 DSH。`--reuse-install true` 跳过 npm 安装，重新启动之前准备好的测试环境，并保留其 home、workspace 和 Mnemon 数据。它仍会检查锁定依赖图、本地包完整性、依赖解析以及原始公开模块的字节。此选项仅用于已经验证过的运行目录，不能用于首次安装。本机回环 registry 可能使用新端口；锁文件中保留的历史 tarball 解析 URL 用于记录制品来源。

`server.json` 记录 WebUI 启动认证 URL、验证进程 PID、DSH PID、CLI 路径以及隔离目录。启动 URL 可能在浏览器访问后失效，不要提前请求它来判断 HTTP 服务是否就绪。向验证进程 PID 发送 `SIGUSR2` 会重启其 DSH 子进程，保留隔离数据，并生成新的启动 URL。`SIGTERM` 会停止 DSH 子进程以及本地 registry 和模型服务。脚本不会安装后台服务。

## 真实模型与工具调用流程

在 WebUI 或 Headless CLI 中发送内容完全为 `compatibility-261` 的用户消息。本地模型测试桩会调用真实的 `mnemon_runtime_memory` add 工具，要求返回成功回执，再调用真实的 `mnemon_status`，最后输出固定的完成文本。记忆由实际安装的 Runtime Source 写入其 JSON 存储。真实的 Native CLI 已配置并通过版本检查，但此 Runtime add 不代表执行了 Native 记忆空间后端写入。标题生成请求只返回文本，不会发起记忆写入。子级维护请求通过其提供的完成工具报告跳过操作。其他用户回合返回固定的就绪文本。测试桩接收 OpenAI 和 Anthropic 请求格式，并输出对应的 SSE 传输格式。它设置了请求次数上限，不调用外部网络模型。

`model-requests.jsonl` 保留请求路径、检测到的协议、真实工具列表、提示词和工具回执。`model-events.jsonl` 保留受限测试桩的决策记录。这些文件都是可丢弃的测试输入和输出。

可选的共存测试插件使用 `e2e/peer-fixture` 中的普通源码，由 `pack-peer-fixture.mjs` 打包，并通过 DSH 官方的 bundle/客户端加载器加载。它注册两个不同的 ID：`issue261-peer-a:tail` 和 `issue261-peer-b:tail`，显示文本分别为 “Compatibility peer A” 和 “Compatibility peer B”。在 RC 的 chain 模式中，这两项的 selector 均不命中；alpha 的 list 渲染器则同时渲染两项。测试不会修改 registry 内部状态或框架模块。关闭再开启 Mnemon 的回合栏控件，并重载页面，可检查其他贡献项是否仍能独立共存。

## 使用同一打包依赖图进行 Headless 检查

```sh
node headless-check.mjs --consumer "$VERIFICATION_ROOT/fixed-alpha/dsh-home/profiles/web" --output "$VERIFICATION_ROOT/headless/fixed-alpha" --native-cli "$NATIVE_CLI"
```

Headless profile 使用独立的 home、data 和 workspace。它唯一的 `node_modules` 链接指向上述测试专用的打包安装环境。脚本会断言：DSH 使用精确版本组；全部 17 个本地制品记录存在；Root 完整组合已生效；三项策略扩展全部启用；真实 add/status 流程完成；启动新的 CLI 进程后 Runtime 记忆仍被保留；禁用 Root 后不再暴露任何 `mnemon_*` 工具。每条命令的超时上限为 90 秒。结果与日志保留在输出目录中。每次运行都应使用新的输出目录。

## Alpha 基线的特殊处理

已发布的 Root 0.5.11 在两个框架 peer 依赖范围中都未包含 alpha2。只有 `--mode baseline --cohort alpha` 会使用 `--legacy-peer-deps`，以便复现原始的浏览器注册失败。由于该选项也会阻止 DSH 自动安装 peer 依赖，基线复现需要精确固定公开框架包的版本。随工具提供的 `e2e/framework-peers-alpha.json` 和 `e2e/framework-peers-rc.json` 快照采集自 macOS ARM64；在其他平台上，脚本会拒绝隐式回退到此 alpha 快照。在新主机上，请将 `FRAMEWORK_ROOT` 指向通过正常安装获得、使用精确 alpha 版本的公开 profile，以便从实际已安装的包推导该平台所需依赖。打包基线前，请将 `BASELINE_SOURCE` 设置为已构建的已发布 Root 0.5.11 检出目录：

```sh
node pack-artifacts.mjs --source "$BASELINE_SOURCE" --output "$VERIFICATION_ROOT/artifacts/baseline"
node packed-e2e.mjs --mode baseline --cohort alpha --artifacts "$VERIFICATION_ROOT/artifacts/baseline" --framework-root "$FRAMEWORK_ROOT" --run-root "$VERIFICATION_ROOT/baseline-alpha" --native-cli "$NATIVE_CLI" --serve true
```

`framework-pins.mjs` 根据实际已安装的公开包生成精确版本列表，因此不会将其他平台的可选包记录固定为直接依赖。快照保留在 `baseline-framework-pins.json` 中。`install-policy.json` 记录此仅限基线的依赖检查绕过；它不能证明正常安装兼容。两个修复后的版本组都必须在不绕过检查的情况下完成安装。

原始 alpha 浏览器中的缺少 ID 错误已独立复现。首次浏览器和 Headless 尝试中，仅支持 OpenAI 的测试桩因协议不匹配而失败；这是测试桩问题，不是产品错误。修正后的双协议运行已单独记录。`evidence-summary.json` 保留结果和制品标识。原始运行日志、依赖树、模型请求、启动认证 URL 和生成的 tarball 保留在仓库之外，不纳入本工具包。

## 长时间验证中的制品复用

`pack-artifacts.mjs` 从已构建的源码树打包全部制品。本次修复在并行执行完整清理构建前先打包 Root；随后，`assemble-fixed-artifacts.mjs` 验证每个配套插件的 Git 跟踪源码与 manifest 均与发布基线一致，并复用字节完全相同的 tarball。该脚本接受 `--source`、`--baseline` 和 `--output`；输出目录必须已包含 Root tarball，以及保存为 `root-pack.json` 的 `npm pack --ignore-scripts --json` 结果。全部 17 个 tarball 的标识与源码来源记录在 `artifacts.json` 中。

## 复用限制与复制审计

- 本工具包启动本地模型服务、registry 服务和官方 DSH CLI，不自动操作浏览器。浏览器截图与观察结果记录在上级证据报告中。
- 打包前必须构建所选 Mnemon 源码。17 个生成的 tarball 和可选的 peer tarball 由上述命令生成，并非缺失的仓库测试文件。`assemble-fixed-artifacts.mjs` 是可选脚本，要求基线 Git 提交在本地可访问。
- 首次安装需要访问公开 npm registry。脚本精确检查 DSH 版本和所选 Mnemon 制品的字节；其他间接依赖仍遵循公开的 semver 范围，实际解析版本可能变化。若要精确重现历史运行，应保留每次生成的锁文件。
- Alpha 基线的 legacy-peer 绕过仅用于复现已发布 Root 0.5.11。两个修复后的版本组均使用正常的 peer 依赖解析。主机必须能够使用相应平台的公开包和 Native 可执行文件；本次证据未覆盖其他操作系统。
- Headless 输出目录必须是新目录。复用 WebUI 安装要求保留原先已经验证的安装环境和制品集；该选项不用于安装器迁移。
- 受限模型测试桩只实现指定的 add/status 流程、标题文本和跳过子级维护的行为。它不是通用语言模型模拟器，也不是 Native 记忆空间后端写入测试。
- 工具会执行官方 npm 包的安装脚本，并继承调用者的环境。它不是操作系统沙箱。DeepSeek key 明确为测试用假值，模型端点绑定在本机回环地址。可复用副本为两个公开版本组都设置了受支持的 `DSH_TELEMETRY_MODE=DISABLED`。

`bundle-audit.json` 记录每个复制源文件及其仓库副本的 SHA-256。相较于原始导出包，可执行脚本有两项调整：添加基线平台检查，并显式设置遥测模式；文档和清理后的摘要说明可移植性及未纳入仓库的本地运行文件。已记录的制品与结果哈希保持不变。在仓库中新编写的译文单独记录，不虚构原始导出文件的哈希。
