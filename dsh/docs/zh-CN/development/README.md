# 开发与验证

**简体中文** | [English](../../en/development/README.md) | [文档中心](../README.md)

## 环境与命令

插件的 Node engine 下限为 20；锁定的完整 DSH 开发 Profile 是正式发布的 0.1.7-rc.2，需要 Node `^22.19.0 || >=24.0.0`，建议开发使用 Node 24。Root、Source Client 测试和外部制品消费者均使用该依赖族；`dsh-invariants` 闭合 peer 图，`dsh-client-store` 则提供子 Agent projection 适配器使用的公开 selector 类型。CI 另在 Node 20 冒烟导入公开 Node 入口。npm `next` 上的 DSH 0.2.0-rc.1 同样受支持：`tests/dsh-host-compatibility.spec.ts` 对全部 18 个包的清单执行 DSH 自己的安装前兼容检查，覆盖两个运行时；验证 0.2 行为时，按[兼容性说明](../reference/compatibility.md#dsh-02)在隔离目录全局安装 DSH，或把开发依赖临时切换到 0.2.0-rc.1。

DSH 0.1.7 UI primitives 在制品中导入 Markdown/高亮依赖，但其已发布 manifest 将这些包列为开发依赖。Root、三个 Source 与外部消费者显式声明完整依赖族，使独立 Client 测试可执行；Host 制品仍使用 DSH 提供的 UI 模块。测试同步使用公开的异步 Agent 工厂及持久化 `assistant/message` 事件。`tests/legacy-session-repair.spec.ts` 对普通和压缩格式的合成历史日志执行已发布 v0 → v3 迁移，检查显式副本修复、冷启动重读和带时间戳的 stream 回放。审计用例覆盖三个旧 Mnemon summary、兼容 v2 descriptor、packed 占位值展开、null→空字符串 delta name，以及具有已记录 provider ID 的闭合工具链；另验证多调用 provenance、owner 引用拒绝、原件及其他插件保留。`pnpm e2e:serve --legacy-session-replay` 还要求实际 WebUI 的回环续写服务器核对历史 wire call/result ID 与正文，匹配后才返回成功。

```sh
pnpm install --frozen-lockfile
pnpm run verify
pnpm run verify:plugins
```

`verify` 包含类型检查、根包确定性构建、独立插件构建、完整测试集、真实隔离 DSH Headless 和包出口/内容验证。独立插件检查在所有公开制品构建完成后分阶段执行。不要用 `pnpm -r verify` 同时清理重建制品和运行读取它们的测试；整个工作区使用 `pnpm verify`。`verify:plugins` 在工作区**外部**，基于 semver 安装的 tarball 重复验证，并测试外部 Source/Strategy/Provider/Client 消费者；还向真实 DSH 仅安装根包 tarball，从 loopback registry 解析全部十七个官方插件，不使用工作区链接或改写 manifest，再单独验证三个随附增强从默认停用到同时启用。外部消费者还通过完整 Strategy 的打包 SDK 编译自己实现的策略贡献。

## 仓库归属

```text
src/
  core/       contracts, View compilation, generations, turn leases
  sdk/        installMemory, validation, test fixtures
  host/       DSH lifecycle, settings, tools, RPC, worker coordination
  client/     shared workspace, settings, Source-page SDK
plugins/
  dsh-mnemon-source-runtime/
  dsh-mnemon-source-documents/
  dsh-mnemon-source-memory-spaces/
  dsh-mnemon-strategy-default-three-tier/  # 分层策略，默认主策略
  dsh-mnemon-strategy-general/        # 随附的另一种主策略
  dsh-mnemon-strategy-scoped/         # 随附、默认关闭的选择贡献
  dsh-mnemon-strategy-light-context/  # 随附、默认关闭的投影贡献
  dsh-mnemon-strategy-auto-capture/   # 随附、默认关闭的对话内记录贡献
  dsh-mnemon-provider-*/
tests/        Host/Core/UI composition and boundary tests
scripts/      reproducible build, artifacts, Headless and Web fixtures
cordis.patch.yml   default Starter composition
```

根包拥有 Core/SDK、DSH Host 和默认 Starter，不拥有 Source 存储实现。`plugins/` 下每个目录都是可独立发布的项目。默认发行包按公开 semver 依赖全部十七个官方插件。Starter 安装通用策略与三个增强，其 Entry 默认停用；`memoryView.strategyTypeId` 记录由哪个主策略组合 View，各组件开关由 DSH 插件管理器记录。Source/Strategy 通过 peer 使用 Core SDK，策略贡献使用其完整 Strategy 的公开 SDK，Provider 使用 Memory Spaces SDK。peer/开发关系会产生包管理器环依赖提示；生产代码导入边界另有独立检查。

不再保留私有工作区包、控制器转发文件、业务 binding 或 compatibility 目录。兼容指用户配置、数据与使用流程，不是延续历史内部符号。

插件 Client 测试需要根包的公开浏览器制品时，先构建根包：

```sh
pnpm build
pnpm --workspace-concurrency=1 -r build
pnpm --filter dsh-mnemon-source-runtime verify
```

声明的 peer 版本可用后，任意插件可复制到新仓库，运行自己的 `pnpm install && pnpm verify`。未发布开发使用打包制品及本地 registry 验收，不以仓库源码路径代替公开依赖。

## 测试归属与覆盖

| 边界 | 测试 |
|---|---|
| Core/SDK | 不可变 View、预算、Strategy 校验、并发回合、grant、租约、换代、清理与性能 |
| 策略贡献 | 独立槽、组合/顺序、卸载、冲突、只读范围、共享配额与真实 Host 激活 |
| Source | 自己的控制器/存储、修订、快照、JSON 操作、Client 点击与实例隔离 |
| Provider | 驱动、凭据、真实能力与故障响应 |
| Memory Spaces | Provider 子节点生命周期、跨 Provider conformance、合并/路由/召回质量、Native 进程串行化 |
| Host | 默认组合、配置/数据范围、工具、监督流程、RPC 权限、回执与体验 |
| 制品 | 所有公开入口、独立安装/构建/测试、外部组合与浏览器制品 |

工作区身份、Client 平台边界和委派工作区范围测试还会在 Windows 的 Node 22.19 与 24 上运行。它们覆盖真实文件系统错误和 junction 别名，并包含模拟 Windows 对文件后代返回 `ENOENT` 的跨平台回归；该模拟不能替代真实 Windows 运行。

`tests/bundle-activation.spec.mjs` 用正式发布的 DSH Loader 启动临时激活计数器，覆盖 Starter 的全部十八个包。它验证旧版 `mnemon` 配置／停用目标及组件独立选择，再调用正式插件管理器：真实组件和 bundle 开关必须在重载、重启后保持，核心总开关必须停止全部运行中的子项，任何包都不能留下重复实例。两组用例都在锁定的宿主上运行；设置 `MNEMON_BUNDLE_TEST_PROFILE=/absolute/profile` 可改为检查另一份安装。夹具子进程不继承 `pnpm exec` 注入的 `NODE_PATH`：DSH 0.2 通过 Node 的搜索路径（包括 `NODE_PATH`）为插件包选路，继承它会解析到本仓库的真实插件而不是夹具包。夹具创建自己的临时 profile，不修改所提供的安装。它验证生命周期契约；真实 Mnemon 行为仍由制品 Headless 和 WebUI 检查负责。最后它核对 DSH 的配置 schema 收集器只把 Starter 的组件组报告为无法识别的树载体（已知取舍，见[兼容性说明](../reference/compatibility.md#安装后直接启用-starter)），不允许出现其他错误。执行 `node --expose-internals tests/fixtures/bundle-activation.mjs /absolute/profile manager --check-declared-rows` 可额外要求每个声明的 bundle 组件都可由管理器操作；该诊断会在[兼容性说明](../reference/compatibility.md#dsh-017-bundle-组件列表)中的 `0.1.7-rc.2` 原生 group 列表缺陷处失败。

远程 Provider 使用可控 HTTP 响应；Native 进程测试使用可控命令 runner，另有可选 Windows 二进制冒烟。额外的 opt-in 测试接受经过官方 checksum 校验的 Native 二进制，创建临时记忆空间，通过 View 写入、召回并删除：

```sh
MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon pnpm --filter dsh-mnemon-source-memory-spaces exec vitest run tests/native-integration.spec.ts
MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon pnpm exec vitest run tests/runtime-capacity-workflow.spec.ts -t 'two same-View Native'
```

该测试不会发现个人数据根或安装二进制。这些检查不等于验证过所有真实远端服务或账号配置。Provider Lab 是需要明确启动的独立集成环境。

OpenViking 提供显式启用的回环集成测试：`MNEMON_OPENVIKING_TEST_ENDPOINT=http://127.0.0.1:1933 pnpm --filter dsh-mnemon-provider-openviking exec vitest run tests/integration.spec.ts`。只使用临时后端。测试对唯一的中英文合成资料执行创建、精确读回、搜索、浏览和精确删除；普通 CI 跳过该测试。正式发布的 v0.4.20 服务配合本地确定性 embedding 可验证 HTTP、存储和索引契约，不能证明语义模型质量。

Runtime 用例在 View 固定后，通过真实 Host 工具创建并激活两个 Native 空间，归档两条完整检查点并验证待新增内容。路由决策由本地脚本固定，不调用模型 API。

Agent Teams 矩阵只通过包出口加载正式发布的 DSH 包，并随常规测试在锁定的宿主上运行；`MNEMON_TEAM_TEST_PROFILE=/absolute/profile pnpm exec vitest run tests/agent-team-review-host.spec.ts` 可检查同一 DSH/Teams 版本的另一份安装。测试执行真实 fork/spawn、原生/Code Mode 工具、全部三个 Strategy 增强、子代理执行限制、Runtime 提交与父 Team 工具。[Issue #275 记录](../../pr-assets/issue-275-agent-teams/README.zh-CN.md)提供复现 profile、制品 WebUI 夹具与人工验收证据。

可选的 Flash 压力测试使用四个真实 DSH 会话、委派写入者、独立维护任务和临时 Native 存储，保留默认 10 KiB 上限，验证反复归档后的精确原文、命名空间路由和无会话 Web 管理。通过 `DEEPSEEK_API_KEY` 提供 DeepSeek 凭据，通过 `MNEMON_NATIVE_TEST_CLI` 提供已验证的 CLI，然后运行：

```sh
MNEMON_RUN_FLASH_STRESS=1 MNEMON_FLASH_STRESS_ROUNDS=8 MNEMON_FLASH_STRESS_REPORT=/tmp/mnemon-flash-stress.json pnpm exec vitest run tests/runtime-capacity-flash-stress.spec.ts
```

测试逐一核对出站请求与返回模型均为 `deepseek-v4-flash`，使用非思考模式，始终不选择 Pro。模型提交前的文本变化与存储后的变化分别计数，所有项目事实均为合成数据，临时存储和会话在完成后清理。目标授权范围是硬性断言，主题归类匹配度单独作为模型质量指标报告。设置 `MNEMON_FLASH_STRESS_JSON_PROMPT=1` 可重跑带引号 JSON 输入诊断。普通 CI 默认跳过，需要明确授权使用真实 API。

另一组自动记忆验收在四个并发 DSH 会话中模拟后端、前端、Android 和运维工作。开发提示包含已确定的决策、纠正和临时诊断，由模型自行选择记忆操作。开发任务实际读写临时 JSON 文件，默认生命周期真实执行空闲审阅；新的召回会话无法访问开发会话历史或配置文件。提供相同的凭据和 CLI 环境变量后运行：

```sh
MNEMON_RUN_FLASH_QUALITY=1 MNEMON_FLASH_QUALITY_REPORT=/tmp/mnemon-flash-quality.json pnpm exec vitest run tests/runtime-memory-flash-quality.spec.ts
MNEMON_RUN_FLASH_QUALITY=1 MNEMON_FLASH_QUALITY_WAVES=24 MNEMON_FLASH_QUALITY_REPORT=/tmp/mnemon-flash-quality-long.json pnpm exec vitest run tests/runtime-memory-flash-quality.spec.ts -t 'four preconfigured'
```

默认每会话 12 轮，扩展工作负载为 24 轮。两者均使用关闭思考的 Flash、guided 召回与写回，以及默认 10,240 字节记忆上限。只将空闲防抖从 30 秒缩短为 5 秒，审阅资格规则不变。测试记录保留事实、临时标记、纠正后的回答、模块归属、禁止写入回合、工具错误和归档。Vitest 成功仅表示实验执行完成：应检查 `finalEvaluation.automatedVerdict`，并逐条审阅过期文档、重复与归属错误，才能判断质量是否验收通过。[2026-09-09 验收报告](../../pr-assets/runtime-memory-quality-flash-20260909/README.zh-CN.md) 同时记录失败发现与通过项。普通 CI 跳过两个真实 API 用例。该模拟不能替代完整应用构建、真人半天工作流或 Windows 验证。

生成的真实运行数据放在仓库外，上述命令已采用这种方式。遵循[证据存放规范](../../pr-assets/README.md)：提交摘要、复现输入与最小样例，完整脱敏数据作为 PR 附件保存，并注明 revision 和 SHA-256。

性能回归对 100 次三 Source View 组合约束 wall/CPU 时间；确定性构建比较所有生成文件 hash。二者不承诺生产网络延迟或 LLM 质量。

`pnpm bench:algorithms` 在临时合成存储中测量 N=1/10/100/1000 的本地查询、存储、压缩与召回扩展性，默认写入 `.cache/algorithm-benchmark.json`；可用 `MNEMON_BENCH_OUTPUT` 指定输出，或用 `MNEMON_BENCH_SIZES=1,10,100` 运行小规模测试。运行时不要并行执行其他测试/构建。[算法审计报告](../../pr-assets/algorithm-performance-20261003/README.zh-CN.md)记录工作负载、前后对比、操作计数、复杂度和外部 Provider 边界。普通 CI 使用确定性的等价性/工作量上界测试，不使用该基准的延迟断言。

默认组合和三插件组合均运行上述性能门槛。
[2026-09-01 策略贡献验证](../../pr-assets/strategy-extensions-20260901/README.md)
记录了共存、独立制品、真实 Headless 激活结果及其边界。

## 真实 WebUI

皮肤作者的接入位置、稳定选择器、作用域、旧类名迁移与验收步骤见[皮肤开发与 Mnemon 适配](./skin-integration.md)。

```sh
pnpm build
pnpm --workspace-concurrency=1 -r build
pnpm e2e:serve
```

夹具输出临时工作区及 loopback URL，隔离 `DSH_HOME`、`MNEMON_DATA_DIR`、工作区和模型端点。会话选择 **Mnemon E2E**：该测试自有 preset 去除 Shell 依赖，保留 Host 记忆工具。模型固定回复，因此这里只检查 UI/传输，不评价真实模型沉淀质量。Ctrl-C 停止并清理合成测试数据。

`pnpm e2e:serve --review-evidence --strategy-extensions` 添加按 Agent 注册的合成概览工具及固定的父会话 / 审查调用，检查五个完整分块的继承，并在修复前后尝试相同的外部工具读取；见 [Issue #211 验证记录](../../pr-assets/issue-211-20260911/README.zh-CN.md)。`tests/review-evidence-host.spec.ts` 还覆盖真实 DSH native 与 Code Mode 中，在 Provider 的 start Promise 返回前发生的工具执行。

检查无会话 Sidebar、所有一级/二级页面、Runtime 增改删与清空分支、Documents 创建/搜索/读取、Provider 设置与发现、激活、故障态、取消弹窗、存入记忆、布局切换、locale、返回聊天后交互恢复。在“插件 → 可组合记忆”页面中，还要检查主策略选择器、各组件开关与组件页面、DSH 组件行及其打开的页面、默认/自定义存储位置，以及备份导入预览。读写/删除使用临时 Provider 或受控夹具，不能对个人记忆做实验。

Issue #233 使用 `node scripts/fixtures/openviking-protocol.mjs` 与 `pnpm e2e:serve --openviking-write`。把 OpenViking 配置为 `http://127.0.0.1:19335`，account/user 为 `default`，不设置 API key，然后在 Mnemon E2E 中发送 `openviking-write-233`。模型夹具只驱动一次真实 Host 和受监督 writer 工具。协议夹具刻意返回有 cosmetic update、但未存储候选正文的抽取结果，用于暴露基线误报；`/__fixture` 明确标注并记录合成请求和文件。后端验收需再对临时真实 OpenViking 服务执行同样流程；协议夹具不能代表真实抽取或语义质量。

验证内嵌 Electron Host 时，使用 `pnpm e2e:serve --electron=/absolute/path/to/electron`（macOS 指向 `Electron.app/Contents/MacOS/Electron`）。单独安装测试用 Electron，并通过 `MNEMON_CLI_PATH` 和 `npm_config_prefix` 指定隔离的 npm 安装。夹具将正式发布的 DSH Web 栈运行在 Electron 主进程内，Host 不设置 `ELECTRON_RUN_AS_NODE`。它为正式 Cordis loader 开放 Node internals，无需重新编译或修改 DSH 包。照常用 Ctrl-C 停止。

另检查 `displayMode` 实时切换：Sidebar 与会话标签页（`builtin`）不得同时挂载，二者使用同一组 Source 页面。会话标签页的全局/工作区/集中工作区/自定义范围读写及任务遵循所属会话，隐藏范围控件，切换会话时清理旧数据与编辑器。验证旧 `buildin` 规范化，以及原生 Sidebar 皮肤和已支持布局插件下的折叠图标。

[2026-09-04 main rebase 验证记录](../../pr-assets/main-rebase-20260904/README.md)列明精确的 v0.4.7/DSH rc.1 revision、registry 与源码覆盖完整测试、独立制品、插件组合重启持久化和真实双入口验证及其限制。

[2026-08-30 npm 回归记录](../../pr-assets/npm-sidebar-cli/README.md#简体中文)保留了旧 Taskboard/SSH 与 CLI 调查。已移除的历史夹具不是当前 checkout 的命令；当前 WebUI 验证使用 `pnpm e2e:serve`。

区分上游 Profile/传输告警与 Mnemon 故障，不隐藏控制台。

文档归档回归使用 `pnpm e2e:serve --document-archive`：创建并启用临时的精确写入记忆空间，新建档案后从工作台归档。标题包含 `REJECT` 时夹具故意选择无效目标，检查档案仍为 active 且没有新增索引，再改名重试。在 Mnemon E2E 对话的三个回合中依次发送 `archive-tool-222 prepare`、`archive-tool-222 update`、`archive-tool-222`，会驱动真实的新建 → 更新 → 归档工具调用，并断言返回的 lineage。只有模型决策由脚本控制，存储、工具、传输和浏览器均为真实实现。搭配旧 Host 构建时，同一夹具可复现旧的回执序号不匹配错误。

Runtime 写入范围回归使用 `MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve --runtime-write-scope --strategy-extensions`，并启动全新临时夹具。在 Mnemon E2E 中发送 `archive-scope-250`。脚本模型先保存两条检查点，在已固定的 View 内创建并激活两个真实 Native 空间，再新增一条越限检查点。基线版本会在空间已激活的情况下拒绝新增；修复后的 Host 会把原文归档到两个有权限的目标，再提交新增内容。发送 `archive-scope-250 retry` 可在新回合重试同一份待新增输入。夹具限制子 Agent 调用次数，且必须收到真实 create/update 回执才完成调用。

`pnpm e2e:serve --idle-review --strategy-extensions` 通过确定性回环模型选择验证部分失败。连续发送两个至少 150 字符的合成用户回合，再等待五秒；真实 reviewer 创建一份档案和一条 Runtime 记忆后，夹具故意返回模型错误。刷新记忆系统状态核对两份已提交回执，再发送更多回合，确认每会话一次的上限阻止新增子 Agent。与生产默认值的差别只有防抖和最小尝试间隔（均为 5 秒）以及 1 次会话上限。该夹具不模拟真实 Agent Teams policy；该组合由上文的可选 Agent Teams 矩阵覆盖。不使用个人凭据或记忆。详见[双语复现与证据](../../pr-assets/idle-review-agent-team/README.zh-CN.md)。

`pnpm e2e:serve --strict-template` 复现 [#327](https://github.com/omdsh-dev/dsh-mnemon/issues/327)。回环模型的行为与 Ollama 0.33 运行“要求用户查询的聊天模板”时一致：
- 保留系统提示，以及其余消息中能放进窗口的最长后缀，并总是保留最后一条消息；
- 截断后没有用户文本消息时返回 500 `no user query found in messages`；
- 窗口在后台审查发出第 3 次请求时确定。

连续发送两个至少 150 字符的用户回合，再等待五秒。修复后，第 4 次审查请求被拒一次，随即带着用户消息重试，审查会创建档案“Review checkpoint storage”，状态页没有审查失败；未修复的构建则在状态页显示后台审查失败，并列出已提交的档案回执。夹具以 `Strict template:` 行输出每次审查请求。与生产默认值的差别只有后台审查的时间设置（5 秒、每会话一次）。详见[双语复现与证据](../../pr-assets/issue-327-subagent-user-turn/README.zh-CN.md)。

`pnpm e2e:serve --general-strategy` 启动时已选中通用策略，并停用分层策略。先发送 `general-strategy-check remember`，下一回合再发送 `general-strategy-check recall`。脚本模型检查系统提示中包含通用记忆协议，且 Runtime、项目档案和记忆空间三个 Source 均已接入；随后通过具名 Runtime 工具保存一条事实，并且只有当这条事实以常驻记忆投影出现时，第二回合才答出它。夹具以 `General strategy:` 行输出每项检查。只有模型决策是脚本化的；策略、其 View、Runtime 写入和浏览器均为真实运行。

`pnpm e2e:serve --without-mnemon-cli` 把 `cliPath` 指向不存在的文件，即使 PATH 中有 Mnemon CLI，Host 也按未安装处理。用它确认：状态页不显示 Mnemon Native 卡片，版本对话框把 CLI 列为可选，插件页停用向量测试，新建记忆空间从已就绪的 Provider 开始。先连接一个 Provider，例如在“插件 → 可组合记忆”页面中连接 Provider Lab 的服务。

`pnpm e2e:serve --docs-demo`（中文）或 `--docs-demo=en` 会预置[文档截图集](../../assets/webui-v0.5.19/README.md)使用的虚构项目 Lumen：用户画像与工作记忆、六份项目档案和三个记忆空间，都在宿主启动前通过各 Source 自己的管理操作写入。包含“结账”的消息会经真实 View 工具完成一次档案检索与两次记忆空间召回后作答；随后包含“记住”的消息（例如新的 LCP 目标）会替换对应的工作记忆条目。脚本只决定模型调用哪些工具，不涉及任何个人数据。加上 `--live-model` 则改由 DeepSeek API 作答，密钥从 `DEEPSEEK_API_KEY` 读取；此时提问会经过真实的模型、任务 Agent 与 Agent 查询，图集即以这种方式采集。界面变化后应使用该夹具重新采集截图集。

`pnpm e2e:serve --trusted-host=memory.test:4331` 把该授权主机加入 DSH 的浏览器信任边界，用于以远程页面身份检查 WebUI；再加 `--remote-management` 即设置 `remoteAccess: trusted-host`。同时设置 `MNEMON_E2E_PORT=4331` 使端口一致，在测试浏览器中把该名称解析到 127.0.0.1（Chrome 可用 `--host-resolver-rules="MAP memory.test 127.0.0.1"` 并加 `--no-proxy-server`），再把打印出的启动 URL 中的回环地址换成 `memory.test:4331` 打开。未授权时“插件 → 可组合记忆”页面为只读；授权后，保存会经 API Gateway 持久化。

## 发布

官方包采用独立版本，Starter 固定经过验证的组合。Changeset、冻结制品、Registry 验证与失败恢复见[发布流程](./releasing.md)。

## 文档、存储与历史证据

保持中英文页面一致、公开示例可执行。Mermaid 表达归属和流程；真实截图位于 `docs/assets`。不保留空目录占位或历史转发文件。

没有明确迁移方案与测试前，保留持久格式及配置键。存储改动须验证锁、原子 rename、修订、损坏输入和复制数据根上的升级/回退。

v0.3 benchmark 是冻结的历史结果，不是本架构的性能证明。失效的可执行评估框架已移出工作树；[发布记录](../releases/v0.3.0.md) 链接到固定版本历史源码。当前验收入口为上述脚本。
