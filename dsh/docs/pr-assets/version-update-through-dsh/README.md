# One-click update in the desktop app

[简体中文](./README.zh-CN.md) | [Verification data](./verification.json)

In the desktop app, **Status → Check versions** found a newer dsh-mnemon but could not update it. The dialog only said 检测到 DSH Profile 安装，但当前找不到 pnpm 命令。, because the version check ran pnpm from PATH and the app's Host has none. The app ships its own pnpm and hands it to DSH's plugin manager. With this change the Starter updates through that plugin manager, so the app's own pnpm installs it. After DSH swaps in the new page, the Memory System reopens on the result. Until DSH restarts, a notice above every Memory System page names the installed version and the one that still runs.

Tested revision: `b6e463569103c8380d6159ee4108e20d505a8da1`. Baseline: dsh-mnemon 0.5.20 from npm. The runs took place on 2026-10-02 (Asia/Shanghai):
- macOS 15.6 arm64 and Node 24.19.0;
- headless Chrome 154 at 1280×800, zh-CN, light;
- a fresh home, DSH home and pnpm store for each run.

Profiles and memories are synthetic.

## How the desktop app was reproduced

The installed DeepSeek Harness app (0.2.0-rc.2) starts its Host with `runProfile({ packageManager })`. That `packageManager` runs `Contents/Resources/runtime/pnpm/bin/pnpm.mjs` with the app's own executable, and DSH's plugin manager uses it instead of a PATH pnpm.

These runs start npm DSH 0.2.0-rc.2 the same way:
- a small launcher calls DSH's exported `runCli({ packageManager })` with a copy of the app's pnpm 11.7.0;
- the Host gets the app Host's own PATH, which has no pnpm.

The app itself was not driven: it owns a fixed port and the user's profile, and its files were only read.

## Before: published 0.5.20

![dsh-mnemon 0.5.20 can update to 0.5.21, but the dialog only says pnpm is missing and offers no Update button](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/before.jpg)

The row reads 可更新 0.5.20 → 0.5.21 with 检测到 DSH Profile 安装，但当前找不到 pnpm 命令。 and no **Update** button, as reported.

## After: update, reopen, restart

A loopback registry serves npm's real metadata plus this revision's Starter twice:
- as `0.5.21-dshupdate.0`, installed first;
- as `0.5.21`, in place of npm's 0.5.21.

The version check reads npm's own `latest`, 0.5.21, so the dialog offers that update. Serving this revision as 0.5.21 lets the update land on a page that contains this change, as an update from this release to a later one will.

| Offered | Updating |
|---|---|
| ![The row offers Update through DSH's own plugin installer](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/offered.jpg) | ![Updating](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/updating.jpg) |

| Reopened with the result | After a restart |
|---|---|
| ![The reopened dialog: dsh-mnemon updated, restart DSH](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/reopened.jpg) | ![After a restart: up to date, no restart notice](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/restarted.jpg) |

1. The dsh-mnemon row reads 由当前 DSH Profile 管理；用 DSH 自己的插件安装方式更新，完成后重启 DSH。 with **Update**.
2. DSH's plugin manager ran `pnpm add dsh-mnemon@0.5.21` with the app's pnpm, and the profile's log ends `Done in 384ms using pnpm v11.7.0`. The profile now lists `"dsh-mnemon": "0.5.21"` and its node_modules holds 0.5.21. The enabled bundles are unchanged.
3. 1.64 s after the update request, the page fetched dsh-mnemon's new `client.js` revision, then the components' clients. DSH's client HMR had swapped in the new page, while the Host kept running the old code.
4. The new page reopened Status and **Check versions**. By 3.75 s the dialog showed **dsh-mnemon 已更新** with the restart notice, and the row read 待重启 · 0.5.21.
5. After DSH was stopped and started again, Status reads dsh-mnemon 0.5.21 / 系统正常. The dialog reads 已是最新, with no restart notice and no **Update** button.

![The update sequence: offered, updating, reopened while checking, then the result](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/update-sequence.gif)

## Why the page reopens

DSH's shipped Web composition, which the desktop app also runs, includes `dsh-client-hmr`. It polls each plugin's `lib/client.js` and swaps a changed plugin into open pages, which drops the plugin's React state. An in-place update changes the Starter's files, so the Memory System and its dialog used to close about two seconds after the click, often before the update reported back. The pnpm route behaved the same way.

With npm's real 0.5.21 as the target, whose page lacks this change, the update installed npm's tarball, and its integrity matches npm's. The page then showed DSH's home without the Memory System at 2.5 s.

Now the dialog records a Starter update in the page's session storage before it starts:
- the page DSH swaps in reopens the Memory System, and Status reopens the dialog;
- the dialog shows the result once the Host reports the restart that version needs;
- the Host's version check waits up to 3 s for an update that is still finishing, so the reopened dialog sees how it ended.

## DSH 0.1.7-rc.2

DSH 0.1.7-rc.2's CLI entry takes no package manager from a launcher, so its plugin manager runs pnpm from PATH. This run used `dsh web` with pnpm 11.19.0 on PATH:
- the update went through DSH's plugin manager (`Done in 562ms using pnpm v11.19.0`);
- the new page arrived 2.42 s after the request, and the dialog reopened with the result by 3.70 s;
- after a restart, the dialog read 已是最新.

## Without any package manager

![The row asks for pnpm and offers no Update button](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/no-package-manager.jpg)

Here npm DSH 0.2.0-rc.2 was started directly with `dsh web`, without a launcher and without pnpm on PATH, so DSH's own Plugins page cannot install either. The row reads 可更新 with 检测到 DSH Profile 安装，但宿主找不到 pnpm 命令；安装 pnpm 并重启 DSH 后即可在此更新。, and there is no **Update** button.

## An optional Strategy added on its own

| Offered | Updated |
|---|---|
| ![dsh-mnemon-strategy-scoped 0.5.4, maintained by the profile, offers Update](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/strategy-offered.jpg) | ![dsh-mnemon-strategy-scoped updated, restart DSH](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/strategy-updated.jpg) |

`dsh-mnemon-strategy-scoped@0.5.4` from npm was added with the packaged-app `dsh plugin add`. It is a DSH bundle the profile maintains (Profile 独立维护). Its **Update** went through DSH's plugin manager to npm's 0.5.5 (`Done in 267ms using pnpm v11.7.0`). Only the Strategy's page changed, so the dialog stayed open, and the enabled bundles were unchanged.

## Restart reminder

An update replaces the installed files, but the Host keeps the code it loaded until DSH restarts, while DSH already swaps in the new page. Revision `375d1ba43fae7de1b82e9717ec69dadd1a8375db` adds a notice above every Memory System page that names the installed version and the one that still runs, and Status shows the running version:
- the Host records the version it loaded and compares it with the Starter on disk on each status read, so the notice covers `dsh plugin` too;
- **Check versions** reads the installed Starter from disk as well.

These runs used a build of that revision, served as above: `0.5.21-dshupdate.0` installed first, and the same build as `0.5.21`.

| After Check versions updated the Starter | After a restart |
|---|---|
| ![Above Status: dsh-mnemon 0.5.21 is installed, while 0.5.21-dshupdate.0 still runs](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/reminder-installed.jpg) | ![After a restart: no notice, and the engine card reads dsh-mnemon 0.5.21](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/reminder-restarted.jpg) |

**Check versions, on DSH 0.2.0-rc.2 with the packaged-app launcher:**
- DSH's plugin manager installed 0.5.21 (`Done in 598ms using pnpm v11.7.0`).
- Status and Runtime both showed **dsh-mnemon 0.5.21 已安装** with 当前运行的仍是 0.5.21-dshupdate.0；重启 DSH 后生效，桌面版请完全退出后重新打开。
- The engine card kept the running dsh-mnemon 0.5.21-dshupdate.0. Check versions read 待重启, installed 0.5.21, with no **Update** button.
- After a restart, the notice was gone, the engine card read dsh-mnemon 0.5.21, and the dialog read 已是最新.

**`dsh plugin add` while the Memory System was open:**
- The packaged-app `dsh plugin --profile web add dsh-mnemon@0.5.21` ran in the same profile while DSH served the page (`Done in 419ms using pnpm v11.7.0`).
- About 0.5 s after it returned, DSH swapped in the new client and the open Memory System closed to DSH's home. This is DSH's client HMR; the dialog's reopening covers only updates started from the dialog.
- Reopened from the sidebar, Status and Runtime showed the same notice. Check versions read 待重启 with 0.5.21 installed and no **Update** button, instead of offering the version already installed.

![Check versions after dsh plugin add: 0.5.21 installed, restart needed, no Update button](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/reminder-cli-dialog.jpg)

**Back to the running version:** `dsh plugin add dsh-mnemon@0.5.21-dshupdate.0` closed the Memory System the same way. Reopened, it showed no notice, and Check versions offered 0.5.21-dshupdate.0 → 0.5.21 again.

**An optional Strategy updated from Check versions:**
- `dsh-mnemon-strategy-scoped@0.5.4` was added on its own, then updated to 0.5.5 (`Done in 312ms using pnpm v11.7.0`).
- Status and Runtime showed **dsh-mnemon-strategy-scoped 已更新** with 重启 DSH 后生效，桌面版请完全退出后重新打开。 The Starter stayed 0.5.21-dshupdate.0.
- The notice was gone after a restart.

![Above Runtime: dsh-mnemon-strategy-scoped updated, restart DSH](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/version-update-through-dsh/reminder-strategy.jpg)

**DSH 0.1.7-rc.2:** `dsh web` ran with pnpm 11.19.0 on PATH.
- Check versions updated the Starter through DSH's plugin manager (`Done in 419ms using pnpm v11.19.0`).
- The same notice appeared on Status and Runtime.
- After a restart, it was gone, and the engine card read 0.5.21.

None of these runs logged a console error.

## Design review

An independent review of this PR found no broken design or contract. It found five behaviors worth fixing, all fixed in `f395626a87bbbc5fea8baef4252f7bf369880084`:
- **Failure messages.** A failed install named the first line of pnpm's output, often a retry warning, and hid the `ERR_PNPM_*` line. The dialog now names the error line, the failure kind DSH read off it, and DSH's log path.
- **Files a failed install leaves.** DSH restores the Profile's `package.json` and lockfile when an install fails, but files it downloaded can stay. Check versions read `node_modules`, so a failed update could look installed and waiting for a restart. Installed is now the exact version the Profile records, for the Starter and for a package the Profile added, with `node_modules` as the fallback.
- **The reopened dialog.** The page DSH swaps in judged success from that same state. The Host now keeps how the last update ended and returns it with each check, and the reopened dialog shows that success or error.
- **A package put back.** A Strategy updated from the dialog and then put back with `dsh plugin` stayed named in the notice until a restart. Each update now remembers the version it replaced.
- **Reload after a restart.** With the dialog left open across a DSH restart, a reload within ten minutes reopened it. Status now waits for the Host, and drops the record once DSH runs the updated version with nothing pending.

The live runs above were repeated on that revision, on the same setup:
- Check versions on DSH 0.2.0-rc.2 and 0.1.7-rc.2: the notice and 待重启 · 0.5.21 before the restart, no notice and 已是最新 after it.
- `dsh plugin add` and putting the version back: the same results as before.
- An optional Strategy updated and restarted: the same results as before.
- **A real failure.** The Profile's registry served no 0.5.21 while npm named it. Clicking Update failed with:

  `DSH could not install dsh-mnemon@0.5.21: operation-error (no-matching-version: [ERR_PNPM_NO_MATCHING_VERSION] No matching version found for dsh-mnemon@0.5.21 while fetching it from http://127.0.0.1:<port>/); log: <profile>/.plugin-manager/logs/<operation>/pnpm.log`

  No notice followed, and Check versions still read 可更新 · 0.5.21-dshupdate.0 with **Update**.

No run logged a console error.

## Automated checks

`pnpm run verify` passes on the tested revision. It covers:
- docs (2,965 local links), typecheck and the deterministic build;
- build, typecheck and tests for all 17 plugins;
- root tests: 113 files, 1,578 passed, 6 skipped;
- Headless activation and public entries, publint and attw;
- package contents at 1,506,720 unpacked bytes, with the budget moved to 1,509,000.

On the reminder revision, `pnpm run verify` passes again: docs at 2,997 local links, root tests at 113 files with 1,584 passed and 6 skipped, and package contents at 1,511,189 unpacked bytes, with the budget moved to 1,515,000.

On the design-review revision `f395626a` it passes with root tests at 113 files, 1,592 passed and 6 skipped, docs at 2,997 local links, and package contents at 1,514,466 unpacked bytes, with the budget moved to 1,517,000. New tests there cover: the error line and log in a failure; the recorded Starter after a failed install that leaves files, with the failure reported; a finished update reported to a swapped-in page; a package put back to the version it replaced; the reopened dialog showing the outcome the Host reports, ignoring an older one; and Status not reopening the dialog once DSH has restarted onto the update.

New Host tests cover:
- DSH's route without a PATH pnpm, and its preference over one;
- the exact spec with `enabled: false`;
- never another profile;
- the pnpm route without a plugin manager, and no update without any package manager;
- DSH's route with a PATH pnpm where the launcher supplies none;
- failure reasons: the first diagnostic line, the DSH an incompatible release needs, and an installer that throws;
- a version mismatch, and a check that waits for a finishing update;
- a Strategy bundle through DSH while a package that is not a bundle stays with pnpm.

New Client tests cover:
- the record: written for a Starter update, kept on success, cleared on close or failure, never written for the CLI;
- the reopened dialog, which shows the outcome the Host reports and claims nothing else;
- reopening the workspace only for a fresh record;
- Status reopening the dialog in the workbench.

New tests for the reminder cover:
- the restart status: an installed Starter however it arrived, and packages Check versions updated on their own;
- a Starter `dsh plugin` installed, which Check versions shows as installed and waiting for the restart, with no update to offer;
- no notice once the Starter on disk is the running one again;
- the status every page reads, with the running version and an installed update;
- the notice on every page, for the Starter and for packages, and none otherwise.

## Limits

- The desktop app was reproduced with npm DSH 0.2.0-rc.2 through the same `packageManager` contract and a copy of the app's pnpm. The packaged app itself was not driven.
- The Starter updates on 0.2.0-rc.2 and 0.1.7-rc.2 install this revision served as 0.5.21, so the swap can be shown without a newer release. Another run installs npm's real 0.5.21, whose page lacks the reopening.
- The reopening starts with updates from the release that contains this change. On 0.5.21 or earlier, the desktop app needs one removal and reinstall on its Plugins page, as the guides say.
- The live runs use the default sidebar layout. The builtin layout reopens through the same workspace opener, which unit tests cover.
- Without session storage the update still works; only the reopening is lost.
- After `dsh plugin` installs an update, DSH's client HMR closes an open Memory System. The notice shows once it is opened again.
- DSH's Plugins page, which updates only by removing and installing again, was not run.
- Only the Starter is compared with the version that runs. A package other than the Starter that is updated outside Check versions is not named.
