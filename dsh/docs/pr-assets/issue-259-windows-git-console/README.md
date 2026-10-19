# Windows Git probe console — issue #259

[简体中文](./README.zh-CN.md) | [Issue #259](https://github.com/omdsh-dev/dsh-mnemon/issues/259) | [Verification data](./verification.json) | [Windows observations](./windows-results.json)

On 2026-09-17, the baseline Runtime Git probe produced visible console windows from a console-free Windows Node process. Adding `windowsHide: true` eliminated them in four measured trials. The command, timeout, branch result and failure fallback remain unchanged. Baseline: `6da061e8fa51a30ed50c6fff2a4cadcddf7a4a6a`; implementation: `1a9d455004c9204a74b87c920d81b67b3f7b954f`.

## Windows reproduction

The observer ran on Windows Server 2025, build 26100, with Node 22.19.0 / libuv 1.51.0 and Git 2.55.0.windows.5. A GUI executable starts Node with `DETACHED_PROCESS`; `AttachConsole` fails with error 6 for every A/B parent, confirming there is no inherited console. A positive control starts a visible console. Continuous `EnumWindows` observation counts new console-class windows only when visible, non-minimized, uncloaked and nonempty.

| Run | Positive control | Baseline trial 1 / 2 | Fixed trial 1 / 2 |
|---|---:|---:|---:|
| [Primary observation](https://github.com/omdsh-dev/dsh-mnemon/actions/runs/35240525857) | 1 | 105 / 104 | 0 / 0 |
| [Screenshot observation](https://github.com/omdsh-dev/dsh-mnemon/actions/runs/35241111560) | 1 | 105 / 105 | 0 / 0 |

Each trial makes 100 normal branch queries plus four Git calls for a padded path, detached HEAD, a non-repository and a missing path. All return-value assertions pass; absent/blank roots also retain the no-launch fallback. Counts are distinct visible windows, not process counts: each baseline trial shows 104 Terminal windows, with one additional classic console window in three trials.

| Baseline: actual opening frame | Fixed: Git queries running |
|---|---|
| ![Visible baseline terminal opening frame](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/windows-before.png) | ![No console window during fixed Git queries](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/windows-after.png) |

Git was not delayed to obtain the screenshot. The baseline frame precedes completed Terminal painting. The screenshot run temporarily minimized one preexisting runner console and restored it afterward. Capture sets focus, so these images do not independently prove focus theft. The zero-window result comes from continuous observation of the entire fixed phase, rather than one screenshot.

The [pinned workflow and harness](https://github.com/omdsh-dev/dsh-mnemon/tree/b81ccd541e93644d3ae2106a8163acdce654534c/scripts/issue-259-windows) are on a validation-only branch. The workflow provides PowerShell 7, Node 22.19.0, Git, full Git history and `RUNNER_TEMP`. A manual run also needs an interactive Windows desktop and the Framework64 .NET C# compiler used by the script. Check out that revision with its baseline history, open PowerShell 7 at the checkout root, and provide a fresh temporary directory:

```powershell
$env:RUNNER_TEMP = Join-Path $env:TEMP ("mnemon-259-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $env:RUNNER_TEMP | Out-Null
./scripts/issue-259-windows/run.ps1
```

The archive also contains the reusable observer, worker, runner and workflow. Both runs verified the downloaded source bytes against Git blobs `eca1a05ed7fc1b382ed31ccdcbadfbc800afba64` (baseline) and `340e4817a695d4807142e12d09bd70e792cec847` (fixed).

The [downloadable evidence archive](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/windows-evidence.tar.gz) preserves both successful runs beyond Actions retention. Its SHA-256 is `f976d55ec68f6b18322e2c9e7b391f3a9d4ab4c965f15db9da6a60ff3a996dea`; the [44-file manifest](./windows-archive-manifest.json) identifies every member. It contains synthetic observations, source blobs, screenshots and harness files; full Actions logs and unsuccessful harness setup attempts are excluded.

## Real WebUI and CLI

Separate clean worktrees ran both revisions on macOS arm64, Node 25.1.0, pnpm 11.19.0 and published DSH 0.1.5-rc.1. The official Mnemon CLI was 0.2.8; its executable SHA-256 is recorded in the verification data. Scoped, Light context and Active capture were enabled together. Each run used disposable DSH home, data and Git workspace directories. The loopback model fixture returns deterministic responses; the real Host, plugins, transport, Git, CLI and WebUI execute normally. The displayed Flash model name does not imply a live model API call.

Build each revision and use the existing server with the [pass-through trace helper](./trace-git-probe.mjs):

```sh
pnpm build
pnpm --workspace-concurrency=4 -r build
MNEMON_CLI_PATH=/absolute/path/to/mnemon \
MNEMON_GIT_PROBE_TRACE=/tmp/git-probes.jsonl \
MNEMON_GIT_PROBE_PHASE=before \
NODE_OPTIONS=--import=/absolute/path/to/trace-git-probe.mjs \
node scripts/serve-e2e.mjs --strategy-extensions
```

Use `after` for the fixed run. Initialize the printed disposable workspace as a Git repository with an empty commit on `webui-main`, then create `webui-other`. In the WebUI:

1. Select that workspace, send a synthetic turn and rename the session `Git projection A`.
2. Add the two synthetic Runtime entries shown below, scoped respectively to `webui-main` and `webui-other`.
3. Create `Git projection B`, send a turn, switch B → A → B and reload the browser.
4. On the fixed build, check out `webui-other` and send another turn; detach HEAD and send another turn; restore `webui-main`, reload, and check the CLI version from Memory System → Status.

Both revisions created the entries, completed conversations, switched sessions and restored the selected conversation after reload. The fixed build also completed the other-branch and detached-HEAD turns. The version dialog returned the actual CLI version 0.2.8; no update was installed.

| Baseline Runtime writes | Fixed Runtime writes |
|---|---|
| ![Two baseline Runtime entries](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/before-runtime.png) | ![Two fixed Runtime entries](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/after-runtime.png) |

| Baseline conversation switch | Fixed conversation switch |
|---|---|
| ![Baseline session B after switching](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/before-conversation-switch.png) | ![Fixed session B after switching](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/after-conversation-switch.png) |

[Fixed detached-HEAD turn after reload](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/after-detached-reload.png) · [Actual CLI version check](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-259-windows-git-console/after-cli-version.png)

The [trace](./webui-git-probes.json) records two baseline calls without `windowsHide` and four fixed calls with `windowsHide: true`, including `webui-other` and detached HEAD. It passes the original arguments, options, return values and errors through. In this DSH/macOS fixture, actual probes occur on conversation turns; hot A/B switching and browser reload do not add probes. Runtime screenshots show persisted branch labels, not proof of model-context filtering; the existing projection tests cover that behavior.

## Checks and scope

- The new launch-options regression first failed against unchanged baseline source: 1 failed / 11 passed. After the change, all 12 focused tests and all 59 Runtime tests pass.
- `MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon pnpm run verify` passes: 1,162 root tests, all plugin suites, deterministic builds, types, real CLI/Native capacity checks, Headless startup/restart and package checks. Five root live-Flash checks, one live-OpenViking check and one macOS-inapplicable Windows test are skipped.
- `MNEMON_PLUGIN_VERIFY_CONCURRENCY=4 pnpm run verify:plugins --skip-build` passes for 16 independent plugin repositories and 17 artifacts, public SDK-only consumers, real packed-Starter activation and all three optional Strategies together.
- Documentation and changeset coverage pass. The Runtime patch changeset also schedules the aggregate Starter patch through its exact dependency. Host and Memory Spaces process runners already hide Windows consoles.

Only Runtime's launch option changes in production. There is no DSH-source, protocol, Provider, storage, credential or migration change. This evidence establishes real Windows process behavior and macOS WebUI compatibility; it does not replay the reporter's complete Windows 11 Electron session-switch environment. No external model API or personal data is used.
