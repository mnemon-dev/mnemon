# npm Sidebar and CLI regression — 2026-08-30

Historical evidence / 历史证据。These instructions and results belong to the named 2026-08-30 builds, not the current checkout. 操作步骤与成绩仅属于当时的构建，不代表当前版本。

[Recorded source](https://github.com/omdsh-dev/dsh-mnemon/tree/5907523) · [Current WebUI fixture](../../en/development/README.md#real-webui) · [当前 WebUI 夹具](../../zh-CN/development/README.md#真实-webui)

## English

The latest [issue #139 Builtin verification record](../builtin-139/README.md) covers the unmodified npm 0.4.1 control, canonical `builtin` migration, all three storage scopes, and real Web screenshots. The baseline description and 2026-08-30 record below remain historical evidence for the earlier npm 0.3.5 regressions.

This regression uses the `v0.3.5` source corresponding to npm `dsh-mnemon@0.3.5` as its baseline, not the view-based development branch (then labeled 0.4.0, now planned for v0.5). The Host is pinned to the latest non-alpha npm release at the time of testing, `@deepseek-ai/dsh@0.1.1-rc.2`. The Web UI is `@linxin666/dsh-web-all@0.3.6`, with additional coverage of its original package name, `@linxin666/dsh-web-ui-all@0.3.6`.

The fixes were first completed against that npm baseline, then synchronized with `main`, still at 0.3.5, and verified again for the PR. Keep separate records of the original and patched packages; current `main` is not an unmodified npm control.

### Isolated startup

Install dependencies and build in a separate worktree:

```sh
pnpm install --frozen-lockfile
pnpm build
pnpm --workspace-concurrency=1 -r build
node scripts/serve-web-regression.mjs --cli /absolute/path/to/test-owned/mnemon
```

Each run creates an independent DSH_HOME, memory directory, workspace, random loopback port, and local model response service. It does not change global dsh, user profiles, personal memories, or existing workspaces. The session model returns fixed test text without calling a paid model. Shell tools and optional SSH/PTY/tunnel installation scripts are outside this regression. `fixture.json` records versions, directories, test switches, and owned processes; `dsh.log` stores this instance's service log. Ctrl-C stops only this instance and retains its directory for audit.

`--package dsh-mnemon@0.3.5` starts the unmodified npm control; `--package /absolute/path/to/local-pack.tgz` installs a locally built patch. By default, the profile uses `cliPath: mnemon` and a child-process-only PATH to test command-name resolution instead of bypassing the issue with an absolute cliPath. Use `--cli-name` to select a different configured value.

### Panel navigation

#### Entry placement and storage scope

The harness also accepts `--display-mode sidebar|builtin` (default `sidebar`, with legacy `buildin` input accepted) and `--storage-scope global|workspace|custom` (default `custom`). Global mode uses the fixture-owned `MNEMON_DATA_DIR`, workspace mode uses the test session's `.mnemon`, and custom mode uses the fixture's explicit directory. No personal root is used.

```sh
node scripts/serve-web-regression.mjs --cli /absolute/path/to/test-owned/mnemon --display-mode buildin --storage-scope workspace
node scripts/serve-web-regression.mjs --cli /absolute/path/to/test-owned/mnemon --package dsh-mnemon@0.4.1 --display-mode buildin --storage-scope workspace
```

Both commands intentionally start with the old spelling; the second is the unmodified issue #139 control. The patched Host must save `mnemon.displayMode: builtin` automatically. Verify that the conversation tab shares Sidebar pages and dialogs, has no scope selector, and follows the owning session for reads and writes. Switch entry placement live and reload to check persistence; switch conversations across two workspaces to check isolation. Record the linked commit or packed artifact separately from the displayed package version.

`pnpm verify:headless` also starts with a legacy YAML user setting, checks its canonical writeback and preservation of unrelated fields/comments, then restarts Headless to verify idempotence. No Web connection or real model is needed for migration.

#### Sidebar panel round trips

1. Click Memory System → Task Board → Memory System, checking the genuinely visible main page after each step.
2. Repeat multiple rounds, including session and task data, SSH, returning to the conversation, collapsing and reopening the sidebar, and reloading.
3. Confirm that session content, task data, and the selected memory page remain intact instead of checking only sidebar highlights.
4. Use `--mnemon-first` to cover installing Mnemon before the Web UI.

The reported stuck navigation did not reproduce directly with the ordinary npm combination in this run. Do not describe it as an inevitable failure of the default original environment. Code-level tests demonstrate desynchronization when an activation announcement is missing: Mnemon's private state still says open, so clicking its entry incorrectly closes it.

For a repeatable browser before/after comparison, add `--panel-event-loss`. This installs a **test-only plugin excluded from the published package** that drops the active Task Board's `dsh-panel-activate` announcement. It does not replace Task Board, modify npm plugin code, or rewrite the displayed result. This explicitly labeled, controlled compatibility fault does not establish that the user's environment lost that announcement.

The control still displays Task Board after the third click; the patched package restores Memory System. Unit tests also cover SSH, programmatic DOM activation, navigation before synchronization, and subsequent refreshes under the legacy Web UI protocol so a hidden panel cannot reclaim the foreground.

### CLI checks

1. Confirm that the test CLI is executable and on the harness child processes' PATH.
2. With `cliPath: mnemon`, the original status page incorrectly reports that Mnemon CLI was not found, while the version dialog displays its version.
3. The patched package agrees in both places. With no active Memory Space, it reports service readiness rather than pretending to have a healthy active space.
4. Temporarily move the CLI within its test-owned directory and click Recheck: it should report missing. Restore the file and recheck again: availability should recover without restarting DSH.
5. Create and activate a test native Memory Space to verify actual CLI status execution and health. Never use personal memory directories.

### Automated verification

```sh
pnpm typecheck
pnpm exec vitest run --reporter=json --outputFile=/absolute/path/to/test-results.json
pnpm verify:build
pnpm verify:headless
pnpm verify:package
```

Save screenshots, browser-visible state records, and before/after JSON test results alongside the run's `fixture.json`. Label screenshots as ordinary-environment or controlled-fault evidence.

### Public verification record: 2026-08-30

The original control is the unmodified npm 0.3.5 release. PR code commit `5907523` incorporates `main` at `05c128b`; the patched screenshots below use a local tarball built from that commit, not a newly published version. The CLI is official Mnemon 0.2.5 for macOS arm64.

| Scenario | Screenshot | Visible result |
|---|---|---|
| Original, ordinary environment | [Incorrect CLI status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/npm-sidebar-cli/before-cli.jpg) | The installed CLI is reported missing |
| PR code, ordinary environment | [After ten round trips](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/npm-sidebar-cli/after-normal.jpg) | Memory System is visible; the CLI reports readiness with no active Memory Space |
| Original, controlled announcement loss | [Failed return](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/npm-sidebar-cli/before-controlled.jpg) | Task Board remains visible after the third click on Memory System |
| PR code, controlled announcement loss | [Successful return](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/npm-sidebar-cli/after-controlled.jpg) | Memory System is visible after the third click |

After synchronization, `pnpm run verify` passed 541 tests and skipped one Windows-only test on macOS. Determinism for 106 build files, 35 Headless tools, 10 Node-compatible public entries, publint, and attw all passed. The upstream rc.2 missing-source-map warning remains and does not fail verification. The ordinary environment completed another ten real panel round trips, the CLI version check showed 0.2.5, and the browser console had no errors. The controlled run confirmed that the test plugin actually dropped an announcement and still returned successfully.

Patch tarball SHA-256: `0d3757f30a7f1dd79a31bec5d988bbdcdf20a9802f5b75d0e8aa9072b74b68f9`. Raw logs, version dialogs showing machine paths, and fixtures remain local; the public screenshots contain no credentials or personal memory.

## 简体中文

最新的 [issue #139 Builtin 验证记录](../builtin-139/README.md)覆盖未修改的 npm 0.4.1 对照版、规范 `builtin` 迁移、三种存储范围及真实 Web 截图。下述基线说明和 2026-08-30 记录仍保留为较早 npm 0.3.5 回归的历史证据。

本轮以 npm `dsh-mnemon@0.3.5` 对应的 `v0.3.5` 源码为基线，不使用 view-based 开发分支（当时标为 0.4.0，现计划于 v0.5 发布）。Host 固定为 npm 的非 alpha 最新版 `@deepseek-ai/dsh@0.1.1-rc.2`；Web UI 使用 `@linxin666/dsh-web-all@0.3.6`，并覆盖原包名 `@linxin666/dsh-web-ui-all@0.3.6`。

修复先在该 npm 基线上完成，再同步仍为 0.3.5 的 `main` 并重新验证 PR 代码。保留原版与补丁版的独立记录，不能将最新 `main` 当作未经修改的 npm 对照版。

### 隔离启动

在独立 worktree 中安装依赖并构建：

```sh
pnpm install --frozen-lockfile
pnpm build
pnpm --workspace-concurrency=1 -r build
node scripts/serve-web-regression.mjs --cli /absolute/path/to/test-owned/mnemon
```

脚本为每次运行创建独立 DSH_HOME、记忆目录、工作区、随机 loopback 端口及本地模型响应服务。不会修改全局 dsh、用户 profile、真实记忆或已有工作区。会话模型只返回固定测试文本，不调用付费模型；shell 工具与可选的 SSH/PTY/tunnel 安装脚本未参与此回归。`fixture.json` 记录版本、目录、测试开关和所属进程；`dsh.log` 保存本次服务日志。Ctrl-C 只停止本次测试实例，保留目录供审计。

`--package dsh-mnemon@0.3.5` 启动未修改的 npm 对照版；`--package /absolute/path/to/local-pack.tgz` 安装本地构建的补丁包。默认以 `cliPath: mnemon` 和仅对子进程生效的 PATH 验证命令名解析，而不是用绝对 cliPath 绕过问题。可通过 `--cli-name` 指定另一个配置值。

### 页面切换

#### 入口位置与存储范围

脚本还接受 `--display-mode sidebar|builtin`（默认 `sidebar`，也接受旧输入 `buildin`）与 `--storage-scope global|workspace|custom`（默认 `custom`）。全局模式使用夹具独占的 `MNEMON_DATA_DIR`，工作区模式使用测试会话的 `.mnemon`，自定义模式使用夹具的显式目录，不使用个人数据根。

```sh
node scripts/serve-web-regression.mjs --cli /absolute/path/to/test-owned/mnemon --display-mode buildin --storage-scope workspace
node scripts/serve-web-regression.mjs --cli /absolute/path/to/test-owned/mnemon --package dsh-mnemon@0.4.1 --display-mode buildin --storage-scope workspace
```

两条命令都刻意使用旧拼写启动，第二条是 issue #139 的未修改对照组；修复后的 Host 必须自动保存 `mnemon.displayMode: builtin`。会话标签页应共用 Sidebar 页面与弹窗、不显示范围选择，并按所属会话读写。实时切换入口并刷新检查持久化；在两个工作区之间切换会话检查隔离。单独记录 link commit 或打包产物，不把页面显示的包版本当作修复身份。

`pnpm verify:headless` 也会从含旧拼写的 YAML 用户设置启动，检查规范写回、其他字段及注释保留，再重启 Headless 验证幂等性。迁移不依赖 Web 连接或真实模型。

#### Sidebar 面板往返

1. 点击“记忆系统 → 任务看板 → 记忆系统”，每一步检查真正可见的主页面。
2. 重复多轮，覆盖有会话、任务数据、SSH、返回会话、侧栏收起再展开和刷新页面。
3. 检查会话内容、任务数据与记忆页选择仍保留，而不是只检查侧栏高亮。
4. 使用 `--mnemon-first` 覆盖先安装 Mnemon、再安装 Web UI 的顺序。

普通 npm 组合下本轮未直接复现用户报告的卡住现象；不要将其描述为“原版默认环境必现”。代码级测试证实了激活通知缺失时的状态失同步：Mnemon 私有状态仍为打开，再点入口会错误地执行关闭。

为得到可重复的浏览器前后对照，可加 `--panel-event-loss`。该开关安装一个 **仅供测试、不进入发布包** 的插件，丢弃处于打开状态的任务看板的 `dsh-panel-activate` 通知。它不替换任务看板、不修改 npm 插件代码，也不改写页面显示结果。这是明确标记的受控兼容性故障，不能用来声称用户环境确实发生了通知丢失。

对照版第三次点击仍显示任务看板；补丁版可恢复记忆系统。单元测试还覆盖 SSH、程序化 DOM 激活、同步前再次导航，以及旧 Web UI 协议中的后续刷新，防止被隐藏的面板重新抢回前台。

### CLI 检查

1. 确认测试 CLI 可执行且在脚本子进程的 PATH 中。
2. 原版 `cliPath: mnemon` 的状态页误报“未找到 Mnemon CLI”，但版本弹窗能显示 CLI 版本。
3. 补丁版两处一致；没有激活的记忆空间时显示“服务就绪”，并不冒充已有健康记忆空间。
4. 在测试专属目录内临时移走 CLI，点击“重新检查”应显示缺失；恢复文件后再次检查应恢复，无需重启 DSH。
5. 创建并激活一个测试原生记忆空间，验证实际 CLI 状态调用与健康显示。不要操作真实记忆目录。

### 自动化验证

```sh
pnpm typecheck
pnpm exec vitest run --reporter=json --outputFile=/absolute/path/to/test-results.json
pnpm verify:build
pnpm verify:headless
pnpm verify:package
```

截图、浏览器可见状态记录及前后 JSON 测试结果应与运行的 `fixture.json` 一起保存。截图必须标明普通环境还是受控故障环境。

### 2026-08-30 公开验证记录

原版对照为未经修改的 npm 0.3.5。PR 代码提交 `5907523` 已同步 `main` 的 `05c128b`；下表的修复后截图来自该提交打出的本地 tarball，不是已发布的新版本。CLI 为官方 Mnemon 0.2.5（macOS arm64）。

| 场景 | 截图 | 可见结果 |
|---|---|---|
| 原版，普通环境 | [CLI 状态误报](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/npm-sidebar-cli/before-cli.jpg) | 已安装 CLI，却显示“未找到 Mnemon CLI” |
| PR 代码，普通环境 | [十轮切换后](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/npm-sidebar-cli/after-normal.jpg) | 返回记忆系统；CLI 显示服务就绪，无已激活记忆空间 |
| 原版，受控通知丢失 | [返回失败](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/npm-sidebar-cli/before-controlled.jpg) | 第三次点击记忆系统后仍显示任务看板 |
| PR 代码，受控通知丢失 | [返回成功](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/npm-sidebar-cli/after-controlled.jpg) | 第三次点击后显示记忆系统 |

同步后的 `pnpm run verify`：541 项通过、1 项 Windows 专用测试在 macOS 跳过；106 个构建文件确定性检查、35 个 Headless 工具、10 个 Node 兼容公共入口、publint 与 attw 均通过。上游 rc.2 缺失 source map 的告警保留，不影响测试结果。普通环境再次完成十轮真实页面切换，CLI 版本检查显示 0.2.5，浏览器控制台无 error；受控故障环境确认测试插件确实丢弃通知后仍可返回。

补丁 tarball SHA-256：`0d3757f30a7f1dd79a31bec5d988bbdcdf20a9802f5b75d0e8aa9072b74b68f9`。原始日志、带本机路径的版本弹窗与 fixture 仅保存在本地；公开截图没有凭据或个人记忆。
