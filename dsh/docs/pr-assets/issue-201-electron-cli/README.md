# Issue #201: Electron npm CLI verification

## Revision and environment / 版本与环境

- Issue: [#201](https://github.com/omdsh-dev/dsh-mnemon/issues/201).
- Baseline: `main` at `0d5f5fa`; source tested after repair: `1dc3a21018a9918188bf15224d2f9fa59ac719d3`. Evidence-only commits do not change that implementation.
- Date: 2026-09-09. macOS 15.6 arm64; Electron 44.3.0 with Node 24.20.0; ordinary Node Host 25.1.0; pnpm 11.19.0; Playwright 1.63.0 / Chromium 153.0.8010.12, 1440 × 1050.
- Published DSH 0.1.2-rc.1, locally built dsh-mnemon 0.5.5 and Source 0.5.4 artifacts. Each Profile, memory root, browser state and npm installation was disposable. npm prefixes deliberately contained spaces. Absolute checkout paths were masked when capturing version dialogs.

本记录对应从 `main@0d5f5fa` 创建的独立 worktree，修复代码为 `1dc3a21`。DSH 使用正式发布包，未修改其源码或依赖制品。Electron 主进程中的 `process.type` 为 `browser`，`process.execPath` 为 Electron GUI 可执行文件，Host 的 `ELECTRON_RUN_AS_NODE` 未设置。npm、Profile、记忆根目录和浏览器均使用隔离测试数据；截图中的 checkout 路径在拍摄时遮盖。

## Failure and repair / 复现与修复

On the baseline Electron Host, the version dialog found the npm launcher but could not read its version. Creating a Memory Space timed out after 10,000 ms because the child was launched as another Electron application. The Windows report's exact `invalid JSON` text was not reproduced on macOS; the same incorrect GUI launch was reproduced with a real Electron main process.

The repair sets `ELECTRON_RUN_AS_NODE=1` only on verified JavaScript launcher invocations: memory commands, `--version`, the Windows npm ownership probe and the Mnemon update launcher. Saved embedding overrides retain priority; `auto` does not regain an inherited protocol. Native executables keep their direct invocation, and the Host environment is not mutated.

修复前，真实 Electron Host 的版本弹窗无法读取 npm CLI 版本，创建空间在 10 秒后超时。macOS 未复现 Windows 报告中的完全相同错误文本，但复现了相同的错误 GUI 启动路径。修复后仅为 JavaScript 启动器子进程设置 Node 模式；记忆调用、版本检查、npm 归属检查和更新均覆盖，embedding 配置、原生二进制调用及 Host 环境保持原有语义。

## Automated checks / 自动验证

| Check / 检查 | Result / 结果 |
|---|---|
| Regression tests before the fix | 5 expected failures: 3 real child-environment cases and 2 Electron version/update cases; 48 passed |
| Targeted runner, runner configuration and version suites after the fix | 63 passed, including case-insensitive environment keys, paths with spaces, embedding disabled/auto/explicit protocol, unchanged Host environment and native process options |
| `pnpm verify` | Passed: 1,121 test executions, 2 opt-in skips; types, deterministic builds, plugin builds, real Headless activation and package validation passed |
| `pnpm verify:plugins` | Passed: 16 independent plugin repositories / 17 packed artifacts, external SDK/Client consumer and real packed DSH composition |
| `MNEMON_NATIVE_TEST_CLI=<isolated npm CLI> pnpm exec vitest run plugins/dsh-mnemon-source-memory-spaces/tests/native-integration.spec.ts` | Passed: real Source/Provider create, View write, recall and forget; this separately exercised one of the full-suite opt-in skips |
| Real Electron main-process smoke | npm symlink, Windows-shaped `.cmd`, and native binary each passed status, write, recall, forget and version checks; Host Node-mode variable remained absent |
| Actual npm launcher `update` in Electron | Passed against the isolated npm prefix; upstream 0.2.8 was already current, so this was a real reinstall/current-version check |
| Documentation and whitespace | `pnpm verify:docs` and `git diff --check` passed |

The selected, credential-free Electron process results are retained in [electron-smoke.txt](./electron-smoke.txt). The existing rejection tests for unrelated npm roots, missing npm, broken launchers and updates that do not activate the expected version also passed.

完整验证通过 1,121 项测试执行，2 项按需测试默认跳过；随后单独启用了真实 Native Source/Provider 集成测试并通过。真实 Electron 冒烟覆盖 npm 链接、Windows 形式的 `.cmd` 和原生二进制三条路径。Windows 专用冒烟仍未运行。错误 npm 归属、npm 缺失、启动器故障及更新后版本未变化的拒绝路径均通过已有回归测试。

## WebUI checks / WebUI 验证

All actions below used the real published DSH Web stack and the locally built plugin. The ordinary memory scenarios used the published Mnemon npm 0.2.8 package and its native binary. The model endpoint returned a fixed response; the no-session Sidebar's constrained write used the real Source mutation path, so these checks do not claim LLM distillation quality.

| Scenario / 场景 | Observed result / 观察结果 | Screenshot / 截图 |
|---|---|---|
| Baseline Electron version check | npm launcher found, version unreadable | [Before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/01-before-versions.png) |
| Repaired Electron npm version check | 0.2.8, npm ownership recognized; Check again and all 16 subpackage entries worked | [Versions](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/03-after-versions.png) |
| Electron npm write and browse | Created `Electron issue 201`, activated it, deactivated `default`, wrote a synthetic fact with tags/entities and read it back | [Content](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/04-after-write-content.png) |
| Electron npm keyword recall | Retrieved the exact persisted sentinel with its space and Provider provenance | [Recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/05-after-recall.png) |
| Electron npm entity and graph reads | Entity lookup returned the sentinel; related lookup completed; both stores reported healthy storage | [Space health](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/06-after-space-health.png) |
| Forget cancellation and confirmation | Cancel retained the item; Confirm forget removed it and showed the empty state | [After forget](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/07-after-forget.png) |
| Electron update dialog with a controlled npm package | Real RPC and process execution advanced a test version from 0.2.7 to 0.2.8; success shown and Update removed | [Update fixture](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/08-update-fixture-success.png) |
| Ordinary Node npm version check | 0.2.8 and npm ownership recognized | [Node versions](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/09-node-host-versions.png) |
| Ordinary Node npm memory workflow | Create, activate, write, browse, keyword recall, empty filter, filter recovery and confirmed forget passed | [Node recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/10-node-host-recall.png) |
| Electron with explicit native `cliPath` | Existing spaces survived restart; version 0.2.8, write and keyword recall passed; automatic npm update remained unavailable for the direct binary | [Native path](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-201-electron-cli/11-electron-native-binary.png) |

真实 WebUI 已验证修复前故障、修复后版本识别与重新检查、子包展开、空间创建和激活、写入和内容浏览、关键词召回、实体和关联读取、删除取消与确认、Node Host 回归，以及显式原生二进制路径。上述记忆操作均写入隔离的真实 Mnemon 数据库。

The update-dialog scenario intentionally used a separate controlled package because npm had only the current 0.2.8 stable release. Its launcher recorded Electron 44.3.0 and `ELECTRON_RUN_AS_NODE=1` for `--version`, `update`, and the post-update recheck. It changed a test version marker; it was not an actual upstream 0.2.7-to-0.2.8 upgrade. The actual published npm update command was exercised separately as described above.

更新弹窗使用独立受控包验证 0.2.7 → 0.2.8 的状态转换，真实 RPC 和 Electron 子进程均参与，但变化的是测试版本标记，并非真实上游升级。实际官方 npm 启动器的自更新已另行执行，当时 0.2.8 已是最新版本。

## Reproduction / 复测

Build with `pnpm verify`. Install test Electron and Mnemon into disposable directories, then run the [Electron WebUI fixture](../../../scripts/fixtures/electron-dsh-host.cjs) through the normal E2E server:

```sh
MNEMON_CLI_PATH="<isolated npm prefix>/bin/mnemon" \
npm_config_prefix="<isolated npm prefix>" \
pnpm e2e:serve --electron="<test Electron>/Electron.app/Contents/MacOS/Electron"
```

Use the printed loopback URL. Repeat the table's actions. Omit `--electron` for the ordinary Node Host. For the direct native path, set `mnemon.cliPath` in the disposable Profile settings to the installed platform binary. The fixture enables `--expose-internals` for the published Cordis loader; it runs DSH inside Electron rather than spawning a separate Node core. Ctrl-C removes the disposable Profile and memory data.

运行 `pnpm verify` 后，在临时目录安装测试 Electron 与 Mnemon，按上方命令启动并打开输出的本地地址。移除 `--electron` 即可验证普通 Node Host；原生路径通过临时 Profile 的 `mnemon.cliPath` 设置。Ctrl-C 清理临时 Profile 和记忆数据。

## Limits / 边界

Windows 11 and the specific anywhere-labs desktop shell were not available locally. The Windows `.cmd` layout and npm invocation branches were exercised by automated tests, and a Windows-shaped shim executed successfully in real Electron on macOS. A desktop build with Electron's `runAsNode` fuse disabled still requires the documented native-binary path. No live embedding service or LLM quality was tested; embedding environment merging was verified through actual child-process regression tests.

本地未实测 Windows 11 或用户指定的 anywhere-labs 桌面壳。`.cmd` 布局、npm 调用分支已有自动化覆盖，并在 macOS 的真实 Electron 中成功执行 Windows 形式的 shim。关闭 `runAsNode` fuse 的桌面构建仍需文档所述原生路径。本次没有验证真实 embedding 服务或模型质量；embedding 环境合并通过真实子进程测试验证。
