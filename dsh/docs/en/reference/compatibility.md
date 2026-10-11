# Compatibility and upgrades

**English** | [简体中文](../../zh-CN/reference/compatibility.md) | [Documentation](../README.md)

The Starter pins a tested combination of official plugins. The table records verification scope, not a promise that every upstream release or account configuration works.

| Component | Baseline | What is verified |
|---|---|---|
| DSH | `0.2.0-rc.2` (npm `latest` and `next`), `0.1.7-rc.2` | The two supported hosts; `0.2.0-rc.1` also installs. 0.1.7-rc.2 is the pinned development baseline: published contracts, WebUI and isolated Headless activation, profile settings and retained-settings recovery, producer-owned Session V4 messages, plugin manager activation and the Agent Teams review matrix. For DSH 0.2 see [below](#dsh-02) |
| Node.js | `22.19` and `24` | Source CI and packed-artifact CI respectively; development requires `^22.19.0 || >=24.0.0` |
| Node.js 20 | Public package imports only | Does not establish that the DSH Host runs on Node 20 |
| Mnemon Native CLI | `0.2.9` | Opt-in tests against a real CLI and disposable data; install the CLI separately |
| Third-party Providers | Adapter contracts and fixtures | Does not establish live cloud-account conformance or upstream availability |

The root DSH peers (`dsh-app-boot` is optional) and the Memory Spaces Source's `dsh-client-ui-primitives` peer are all `^0.1.7-rc.2 || ^0.2.0-rc.1`. The five Strategy packages require `dsh-mnemon ^0.5.17`, whose extension SDK they use; the Sources keep `^0.5.1`, and the Providers depend on the Memory Spaces Source instead. Older DSH releases are no longer supported: keep dsh-mnemon `v0.5.16`, the last release verified with DSH `0.1.5-rc.1` through `0.1.7-alpha.1`, until the host is upgraded. Earlier Headless and WebUI records remain historical evidence.

Turn memory registers with a stable ID in DSH's `conversation.chat.turnTail` list slot and checks that the turn is complete before reading or showing activity. The sidebar placement and the `dsh-mnemon` page under Plugins follow DSH's public default/main session binding; the conversation-tab placement and Better Sidebar keep their explicit owning session. DSH 0.1.7 keeps live settings in profile Config and edits a plugin's configuration on its page under Plugins; Mnemon's configuration page writes through it and recover retained legacy preferences as described below. Memory data and Provider formats do not change.

See [DSH 0.1.7 settings verification](../../pr-assets/issue-267-settings-migration/README.md), [RC/alpha verification and before/after screenshots](../../pr-assets/issue-261-dsh-slots/README.md), [DSH 0.1.5 verification](../../pr-assets/issue-223-dsh-015/README.md), [Host compatibility evidence](../../pr-assets/dsh-rc1-compat/README.md), [upgrade evidence](../../pr-assets/main-rebase-20260904/README.md), and [current development checks](../development/README.md). A passing mechanism test is not an LLM quality benchmark. OS-specific and real-CLI checks may be skipped unless their environment is explicitly available.

The [v0.5.19 Light gallery](../../assets/webui-v0.5.19/README.md) covers the bilingual desktop pages, the Plugins page, and a conversation, Memory Spaces and the composition board at 390 × 844, where long names truncate. It does not retest every Host settings surface or physical phones, so phone support is not declared complete; the historical v0.5.2 layout failure at 390 px keeps its [versioned evidence](../../pr-assets/documentation-refresh/README.md).

## DSH 0.2

Before DSH installs a plugin, and each time a profile starts, it checks every `@deepseek-ai/dsh` and `@deepseek-ai/dsh-*` peer range against its own version, prereleases included. `^0.1.7-rc.2` excludes 0.2.0, so DSH 0.2 refuses to install dsh-mnemon 0.5.18 and earlier as incompatible and turns an installed one off at startup. From 0.5.19 these peers also accept `^0.2.0-rc.1`; `tests/dsh-host-compatibility.spec.ts` runs DSH's own check over all 18 package manifests for 0.1.7-rc.2, 0.2.0-rc.1 and 0.2.0-rc.2.

**Upgrade order.** Update dsh-mnemon to 0.5.19 or later on your current DSH first, then upgrade DSH. If DSH was upgraded first, the older release is turned off and memory data is untouched; updating the plugin brings it back. Do not use `allow-version` to let an older release through.

**Verified on 0.2.0-rc.2.** The [v0.5.22 release acceptance](../../pr-assets/release-v0.5.22/README.md) installs the release packages through **Add plugin** on a fresh 0.2.0-rc.2 profile and enables them without a restart. It checks Status, runtime memory, a Native space and CLI-to-WebUI recall. The [sidebar entry record](../../pr-assets/issue-318-sidebar-entry/README.md) shows its panel rows and glyph sizes match 0.1.7-rc.2.

**Verified on 0.2.0-rc.1.** With the published DSH 0.2.0-rc.1 installed globally, starting from an empty profile: installing from the command line and from the Plugins page, Enable now without a restart, Status, runtime memory writes, a first live-model conversation with Save to memory, a Headless job, and the read and write routes of desktop windows and remote pages. With every DSH development dependency moved to 0.2.0-rc.1, type checking, the builds, all plugin tests and the Headless verification pass, and among the root tests only the assertion about the pinned development baseline itself differs. The [installation gallery](../../assets/install-v0.5.19/README.md) has the screenshots.

**Host behavior on a first install.**

- When nobody has chosen a registry yet, **Add plugin** measures which one answers faster: in mainland China it usually defaults to the **Mainland China mirror**, elsewhere to the **Official npm registry**.
- pnpm 11 does not pick versions published less than 24 hours ago. On a release day, installing plain `dsh-mnemon` can install the previous release, which DSH 0.2 refuses as incompatible; install `dsh-mnemon@<version>` instead, or wait 24 hours, as [Install and start](../guides/installation.md#common-problems) describes. A versioned install pins the version; upgrade it later with `dsh plugin --profile web update --latest dsh-mnemon`. Within the same 24 hours, `update` and `update --latest` keep the installed version, so existing installations also add the new version by name.
- The install result card shows the package's English description; the name and description in the plugin list follow the interface language.
- The `desktop` profile belongs to the desktop app, and DSH 0.2's command line refuses to manage it; desktop users install and manage plugins on the app's Plugins page.

## Desktop windows

DSH desktop windows load from the app's own `dsh-app://app/` address instead of a loopback URL, and the DSH 0.1.7 desktop app declares no transport for the page. dsh-mnemon 0.5.18 and earlier therefore treated desktop windows as remote pages: every call went through the API Gateway, and the default `remoteAccess: read-only` made runtime memory, Memory Spaces and the plugin settings read only in the interface while agent tools could still write ([#310](https://github.com/omdsh-dev/dsh-mnemon/issues/310)).

From 0.5.19, a page the application serves itself (an address other than `http:` or `https:`) uses Mnemon's local channels; it still counts as remote only when DSH declares a transport for it that does not own the Host. The DSH 0.2 desktop app declares `ownsHost: true` and uses the local channels as well. Pages opened from another device keep the API Gateway and its read-only default, and the Memory System and the plugin settings now say that `remoteAccess: trusted-host` and a DSH restart are needed. The local channels and `/api` share DSH's Host/Origin checks and browser-session authentication.

How it was verified: an Electron window standing in for the desktop shell registers the same standard, secure `dsh-app` scheme as the official app and proxies to a Host that listens only on 127.0.0.1. The published 0.5.18 reproduced the read-only state on DSH 0.1.7-rc.2 (all 13 Mnemon calls through the API Gateway); with the fix, both DSH 0.1.7-rc.2 and 0.2.0-rc.1 (`ownsHost: true`) add runtime memory through the local `/dsh-mnemon-write` channel, and Memory Spaces and the plugin settings are editable, while a remote page opened through a trusted authority stays read only and says why.

## Desktop profile generations

Desktop may remove private `@deepseek-ai/*` packages from a plugin generation and use its own framework versions. Mnemon builds its live settings schema from the host's public Volatile APIs, which DSH `0.1.7-rc.2` provides through Cosmokit `1.8.5`. A generation whose host carries an older Cosmokit without them, such as `1.8.3`, is an unsupported host; keep dsh-mnemon `v0.5.16` there.

In the affected `0.5.13` install, Desktop's fallback hides a missing `createVolatile` export behind `Cannot find package 'dsh-mnemon'`. Flattening `mnemon-bundle` does not fix that import failure. The Starter retains its group, root disable gate, independent Source/Strategy choices, private Provider children and existing settings namespaces. Update Mnemon in the owning profile and restart; no memory or configuration migration is required for this fix.

See the [original Desktop reproduction and verification](../../pr-assets/issue-274-profile-generation/README.md).

## Installing and enabling the Starter without a restart

On DSH `0.1.7-rc.2`, Desktop generation directories and pnpm installs can expose only the `dsh-mnemon` root in the profile. The component packages exist in its dependency tree, but a host started without the bundle selected may still use its startup package-resolution table. First activation then reports `ERR_MODULE_NOT_FOUND` for Runtime, Documents, Memory Spaces and the default Strategy.

The Starter's component group `mnemon-bundle` (the module `dsh-mnemon/bundle`) prepares dependency resolution and then mounts its children with DSH's own `cordis:group`. It uses the host's public package-resolution service, preserves startup routes for other bundles, and lets the host reject incompatible module rebinding. It does not flatten packages, rewrite profile links or patch DSH. Existing Entry IDs, the core disable gate, independent component choices and memory data stay intact. In 0.5.18 and 0.5.19 a separate readiness entry, `dsh-mnemon/starter` (`mnemon-starter`), did this, and the group waited for its `mnemonStarterReady`. From 0.5.20 they are one entry: the Plugins page no longer shows a separate readiness row, and the group cannot be left waiting because that row was turned off.

Mnemon also registers Web RPC routes in its own injected transport scope, so an already-running official Connection does not need to restart. Use the bundle or core switch to control the composition. In 0.5.18 and 0.5.19, turning `dsh-mnemon/starter` off leaves `mnemon-bundle` waiting for `mnemonStarterReady`: `dsh web` starts without the Memory System, and the desktop app treats it as a failed start; see [Install and start](../guides/installation.md#dsh-says-waiting-for-service-mnemonstarterready). After an update to 0.5.20, a leftover `- id: mnemon-starter` row in a profile patch matches no entry and the host ignores it (the desktop app logs `patch: entry "mnemon-starter" not found`); you can delete those two lines.

The cost is in DSH's config schema export: `dsh web --dump-config-schema` walks only native `cordis:group` and `cordis:include` carriers, so it reports `mnemon-bundle` as an unrecognized tree carrier and leaves out its components' schemas, as it does for DSH's own agent presets. Runtime configuration, the Plugins page and profile patches are unaffected.

A fresh installation through the official WebUI can use **Enable now** in the same host process. A previously installed, disabled bundle can also start directly. Updating or removing packages that Node has already loaded remains subject to DSH's normal restart requirements; this fix does not replace loaded modules. For example, enabling components after an in-place update without a restart reports that an entry point the new version added is not exported (`ERR_PACKAGE_PATH_NOT_EXPORTED`): `./starter` after an update from 0.5.17 or earlier, `./bundle` after an update from 0.5.18 or 0.5.19. The Loader does not swap the module of a running entry, so an update from 0.5.18 or 0.5.19 can instead leave the old group waiting for the removed `mnemonStarterReady` (`mnemon-bundle (dsh-mnemon/bundle): pending …`). Both clear on restart; see the [upgrade evidence](../../pr-assets/starter-group-upgrade/README.md). See the [installation and activation evidence](../../pr-assets/desktop-live-activation/README.md) and the [component group evidence](../../pr-assets/starter-group-readiness/README.md).

## DSH 0.1.7 bundle component list

In DSH `0.1.7-rc.2`, the **Plugins → dsh-mnemon** detail page lists the internal `mnemon-bundle` container as an off component (shown as `dsh-mnemon/bundle` from 0.5.20, `cordis:group` before); DSH `0.2.0-rc.1` shows it the same way. With all nine real components running the list reads “10 total · 9 running · 1 off”; 0.5.18 and 0.5.19 add a readiness row, for “11 total · 10 running · 1 off.” Clicking the container switch returns `unknown-plugin`; the Chinese UI reports “组件启用失败：找不到该插件”. See the [original screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-native-20260926/before-bundle-toggle-error.jpg) and [upstream issue #649](https://github.com/dsh-external/issues/issues/649).

The host's display and management inventories disagree: its bundle declaration list includes native groups, but its manageable plugin inventory deliberately excludes them. This container's off state does not mean the Mnemon core or its children are disabled, and does not establish whether memory reads and writes work.

When this happens:

1. Inspect **Memory System → Status** and the actual Source and Strategy components. If only the container is incorrectly shown as off, while the required components and reads/writes work, you can continue using Mnemon. Actual component errors or failed operations still need separate investigation.
2. Use the top-level `dsh-mnemon` bundle switch or the core component with entry ID `mnemon` to stop and restore the composition. Do not use the `mnemon-bundle` container row's switch. The `dsh-mnemon` configuration page says this right above the component list.
3. Keep existing configuration and memory. This display issue requires neither a data reset nor configuration or memory migration.

The Starter retains the stable group ID and the existing `mnemon` configuration target. Disabling the core stops its Sources, Strategies and private Provider children; re-enabling it restores their independent choices. Removing the group leaves enabled dependents waiting for the missing core, while making the group anonymous can leave old instances alive after a profile reload. Do not remove the container, its stable ID or alter the group declaration to hide the row. The [published lifecycle regression](../development/README.md#test-ownership-and-coverage) checks manager persistence, restarts and literal or expression-based legacy disable flags without changing the installed host.

An [upstream candidate patch](../../pr-assets/sidebar-native-20260926/upstream-fix.patch), verified in an isolated environment, omits containers from the display while retaining their actual plugin children. It is not included in published DSH `0.1.7-rc.2` or Mnemon `v0.5.17`. The local management-adapter approach requires taking over DSH's global plugin-management service; embedding it in Mnemon would compromise independent disabling of Mnemon, so it is not shipped with the plugin. Follow [#649](https://github.com/dsh-external/issues/issues/649) and subsequent DSH release notes for the fix. Screenshots of the candidate environment do not establish that the published host is fixed.

## DSH 0.1.7 settings recovery

DSH 0.1.7 replaces `settings.register()` and `settings-file` with Config-backed forms. Mnemon exposes live Config fields and persists its existing UI operations through the host's revision-checked profile writer. Changes to transport authority still require a normal plugin reload.

Mnemon messages use the producer-owned `dsh-mnemon` source kind accepted by both Session V3 and V4. Historical wrappers and DSH's migrated `plugin:dsh-mnemon` messages remain recognizable for filtering and deduplication; no existing Session files are rewritten by this change.

On startup, Mnemon can recover the retained `<profile home>/settings.yaml.imported` backup into the editable root `mnemon` entry. It restores the canonical root settings, `mnemon-ui` conversation toggles, and only the current profile's exact `mnemon-view-*` / `mnemon-plugins-*` namespaces. Explicit current profile values win, including an explicitly empty plugin selection; existing Source and Strategy rows are preserved. A Strategy row using a dynamic expression keeps that expression and skips its legacy overlay rather than freezing the current value. Recovery records `legacySettingsImported: true` in the root Config and leaves the backup unchanged.

If the original `settings.yaml` still exists when Mnemon starts, DSH owns that import. Restart the profile once more after it finishes to recover Mnemon-specific preferences. DSH exposes no completion signal for its importer, so Mnemon deliberately waits for a new host process instead of racing it. A read-only or ambiguous root cannot be edited; malformed relevant sections are not partially imported or marked complete. Retain the backup and review the startup diagnostic before repairing it. Custom root IDs and another profile's hashed namespaces are not guessed.

When rolling back DSH, keep a copy of the profile patch and retained settings backup, and reinstall dsh-mnemon `v0.5.16` with the older host. Settings subsequently edited on 0.1.7 are not automatically exported back to the older settings file; restore the matching backup or reapply those preferences in the older host. Runtime, Documents, Memory Spaces, and Provider data are unaffected.

## Upgrade the default installation

1. Export a Mnemon Pack and protect any required Provider connection backup as described in [Operations](../guides/operations.md#backup-and-recovery).
2. Upgrade `dsh-mnemon` in each DSH profile that uses it. The Starter installs its exact plugin versions; do not update its child packages independently and assume the resulting mixture is tested.
3. Restart DSH after installing new package code. Check **Memory System → Status**, then read an existing Runtime entry, Document and active Memory Space.
4. Verify the selected storage scope before writing. Changing the scope selects a different authority; it does not migrate data.

v0.5 preserves the default v0.4 storage, configuration and sidebar workflow. The optional conversation-tab placement (`builtin`) uses the same Source pages, and the retained `buildin` spelling is normalized. The General strategy and the three memory enhancements ship disabled. Memory composition on the Plugins page lists, switches and configures the installed components; there is no separate View tab.

Independent plugin authors use declared peer ranges and public exports. Old private controller imports are not a supported upgrade surface. Custom compositions must be verified separately from the Starter. See [Extensions](../development/extensions.md) and the [v0.5.0 release boundary](../releases/v0.5.0.md).
