# Native Sidebar and bundle verification

[中文](./README.zh-CN.md)

Base: `6d79c663262f71c4309cd6db5bd70b204abb143d` (v0.5.15). Tested implementation: `8d3a16ec`. The fixture uses Node 24.20.0, published DSH 0.1.7-rc.2, all seventeen packed Mnemon packages, and the official Mnemon CLI 0.2.9. [Verification record](./verification.json) records the artifact hashes and checks.

## Native navigation

On main, Memory System inserted its own row outside DSH's native panel list. Its typography and spacing differed from Plugins, and clicking Plugins could leave the memory overlay covering the selected page.

The fix contributes an icon and label to `sidebar.panellist` and a matching `main` seat, using `layout.selectPanel`. DSH owns the button, typography, tooltip, selection, and collapsed layout. The shell-owned Source tree is retained through a portal into that seat, preserving editor state and Source render authority. Better Sidebar and replacement-layout fallback remain covered.

| Before | After |
|---|---|
| [Default: memory overlay remains over Plugins](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/before-default-panel-switch.jpg) | [Default: Plugins becomes the visible page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/after-default-plugin-switch.jpg) |
| [Maid Atelier: separate navigation styles and competing selection](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/before-maid-panel-switch.jpg) | [Maid Atelier: native navigation and accessible page header](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/after-maid-memory.jpg) |

The [measured navigation geometry](./native-alignment.json) confirms equal native row font/line-height, padding, label origin, and icon sizing. Expanded rows use 14 px type, 22 px line-height, 7 px / 8 px padding, and the same label origin. Collapsed rows are 36 × 36 px with 18 px icons. The native main seat reserves `--dsh-frame-top-clearance` once; desktop window chrome is not emulated in the Web acceptance.

Real WebUI checks passed for Plugins / Memory switching, return to chat, New Session, collapsed navigation, language switching, and state retention. Runtime add/edit persisted, Documents create/search/read succeeded, and a real CLI write was recalled through the WebUI ([screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/after-native-cli-recall.jpg)). A real native `mnemon_status` tool also completed through the conversation. Only model choices are scripted locally; this does not evaluate model quality or external Provider services.

Maid Atelier 0.3.2 was loaded through the official marketplace's skin-center / market plugins 0.4.2 in disposable storage. Its active-skin configuration was selected locally because the skin center's own Apply persistence failed in this environment. No personal DSH profile or memory was used.

## Bundle diagnostic and isolated upstream candidate

Published DSH 0.1.7-rc.2 lists the native `cordis:group` container as an off component even though its children run. Its inventory deliberately excludes groups, so trying the displayed switch returns `unknown-plugin` ([before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/before-bundle-toggle-error.jpg)). Mnemon's stable group is necessary to preserve the legacy core disable gate and safe reloads; deleting its ID produced duplicate instances and was rejected.

The committed lifecycle fixture checks the public core target, independent component settings, all seventeen activation sentinels, reload/restart, whole-bundle toggles, and private Provider teardown. It exercises published Loader/manager contracts; real Source behavior is separately covered by packed Headless and WebUI checks.

An isolated DSH plugin-manager candidate filters group containers from the displayed component list while retaining declared IDs and child rows. It is **not part of Mnemon's runtime dependencies and has not been released or submitted upstream**. The [patch](./upstream-fix.patch) and [feedback draft](./upstream-discussion-draft.md) are retained for review. Its separate disposable WebUI showed eight running components, supported Documents off/on and whole-bundle off/on, and passed the stricter declared-row lifecycle check ([candidate screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/upstream-candidate-group-fixed.jpg)). This candidate result is not a claim that official DSH has already fixed the defect.

A separate, explicitly installed manager-adapter bundle is technically possible through the public `PluginManager.listBundles()` method. An rc.2 service-level probe passed the same lifecycle matrix, but it replaces the Host-wide management service and needs its own version/configuration maintenance. Putting that adapter inside Mnemon makes Mnemon management-required and prevents bundle disable. No adapter package is added by this PR; it has not received WebUI acceptance.

## Reproduction and checks

Build and pack Root plus all sixteen plugins using the repository's artifact workflow, then use an empty disposable state directory:

```sh
MNEMON_CLI_PATH=/absolute/path/to/mnemon node scripts/serve-sidebar-bundle-reproduction.mjs \
  --profile /absolute/path/to/published-dsh-rc2-install \
  --artifacts /absolute/path/to/seventeen-tarballs \
  --state /absolute/path/to/empty-disposable-state
```

Optional `--skin-center` and `--skin-market` arguments accept the official plugin tarballs. Authentication URLs remain in the mode-600 private fixture record, not this evidence.

Passed: `pnpm verify` (1,390 Root tests, eight unrelated opt-in skips), `pnpm verify:plugins` (sixteen external plugin repositories and seventeen artifacts), native CLI integration, two-Native-namespace Runtime archival, and the published floor / rc bundle lifecycle matrix. The final CSS changes also passed 49 focused tests and a complete repeat of `verify`. The Root package is 1,344,131 unpacked bytes, below the unchanged 1,376,000-byte budget. Final installed runtime files were compared against all seventeen recorded tarballs.

The Runtime / Documents / Memory Spaces frame consistency fix is an independent PR and is not included here. Windows and a real Electron window were not exercised in this run.
