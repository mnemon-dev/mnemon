# Desktop profile generations — issue #274

[中文](./README.zh-CN.md) · [Issue](https://github.com/omdsh-dev/dsh-mnemon/issues/274) · [Compatibility](../../en/reference/compatibility.md)

Recorded on 2026-09-25, from main `84d469ffa838a36fa579295d94029fcac8ac058e` plus this fix. The failure is a framework capability mismatch, not a group-relative resolution bug. The production change keeps the existing bundle and chooses the ordinary schema before attempting to import a live schema on older hosts.

## Original runtime and failure

The fixture uses the unchanged [Desktop v0.9.2 macOS arm64 release](https://github.com/dataelement/dsh-desktop/releases/tag/v0.9.2), containing DSH `0.1.5-rc.2`, Cosmokit `1.8.3`, and the shipped market installer and patched Cordis loader. The ZIP is 212,666,216 bytes; its SHA-256 matches the release asset digest:

```text
7832934f5c1c6606826791c625db5eaf5db4ab2e20bd72b2a3585d7d486dfa4c
```

`installGeneration`, `writeDesired`, and `projectGenerations` install published `dsh-mnemon@0.5.13` with `autoInstallPeers: false`. The result is exactly the reported generation `dsh-mnemon+0.5.13+5b2e12f917e4`. The official installer removes framework singletons from that generation. The aliased `schemastery-live` package consequently imports host Cosmokit `1.8.3`, whose exports do not include `createVolatile` or `isVolatile`.

The old root imports that schema unconditionally. A direct import fails with `SyntaxError: ... does not provide an export named 'createVolatile'`. The Desktop loader's real fallback first tries a bare import from its application directory, then resolves the root through the profile. It catches the second import's error and rethrows the first `ERR_MODULE_NOT_FOUND`, hiding the schema failure. The original Group shares its parent's EntryTree; removing the Group leaves the same failure.

| Probe | Before | After |
|---|---|---|
| Complete built root; public Cosmokit 1.8.3 | Missing `createVolatile`; regression fails | Ordinary Config; passes |
| Complete built root; public Cosmokit 1.8.4 | Passes | Live Config; passes |
| Original Desktop fallback, no native addons | `ERR_MODULE_NOT_FOUND` | Complete root imports with PlainConfig |
| Grouped / flattened fallback diagnostics | Both fail importing the root | Bundle remains grouped |
| Normal Desktop Web startup with native addons | Original SyntaxError, exit 1, no URL | Real WebUI starts |
| Shipped Electron Node 24.18.1 direct / fallback | Both fail with their respective errors | Both pass |

The fallback probes use `--no-addons` to make the original loader select its existing fallback. They do not alter loader fields, fake exports, or replace resolution. Full WebUI and Headless checks retain native addons. The WebUI uses the release's actual `harness-node-entry.mjs` and modules under Node `24.20.0`; the additional import probes use the release's own Electron binary with `ELECTRON_RUN_AS_NODE=1`. No DSH source or resource file was modified. [Release-file hashes](./logs/desktop-file-hashes.json) identify the inspected runtime files.

Full sanitized diagnostics: [direct import](./logs/before-direct.log), [fallback](./logs/before-fallback.log), [grouped](./logs/before-grouped.log), [flat](./logs/before-flat.log), [normal Web startup](./logs/before-web.log), [fixed fallback](./logs/after-fallback.log), [Electron before](./logs/electron-before-import.log), [Electron after](./logs/electron-after-import.log). Fixture and checkout paths are replaced with placeholders. Before startup never reached an HTTP URL, so there is no before WebUI screenshot.

## Verification

The regression copies the complete built package into a real profile/generation symlink layout and uses public Cosmokit packages, not mocks. It failed on the unchanged main build with 1.8.3, while 1.8.4 passed. After rebuilding the fix, both generation tests and all seven existing live-config tests passed: [before](./logs/regression-before.log), [after](./logs/regression-after.log).

- [Legacy Settings](./logs/legacy-settings.json): real Desktop profile activation, ordinary Config, existing `mnemon` / `mnemon-ui` namespaces, validated save, full restart with unchanged saved bytes, all three Strategy extensions, and the root gate disposing all eight entries.
- [DSH 0.1.7-rc.1](./logs/live-rc1.json): public npm profile runner; retained root/UI/View/Source settings recover under their existing namespaces; live update retains the owning fiber; stale revision is rejected; one Source independently re-enables; the `!!js 15 * 1000` expression and backup bytes survive saving and restart; the root gate still disables all eight entries.
- [Desktop Headless](./logs/after-checks.json): actual generation, complete representative Mnemon tool surface and simultaneous scoped/light-context/auto-capture contributions; disabling the root removes Mnemon tools without pending dependents and the Host still completes its turn.
- Real Mnemon native CLI `0.2.9`, verified against the official archive digest, is selected in every disposable Host. The WebUI's actual `mnemon_status` result reports `healthy: true` and `commandFound: true`.
- `pnpm run verify` passed under Node 24.20.0 / pnpm 10.13.1: 98 Root files, 1,351 passed / 6 skipped Root tests; all 16 plugin suites, 382 passed / 2 skipped tests; deterministic builds, real Headless and package/export checks passed.
- Sequential `pnpm run verify:plugins --skip-build` passed: 16 independent plugin repositories and 17 tarballs; isolated install/type/test/build checks, an external SDK/Client consumer, packed Starter Headless and all three optional Strategies together. All four Vitest worker environment variables and `MNEMON_PLUGIN_VERIFY_CONCURRENCY` were `1`; no test timeout was changed. [Verification summary and full-log hashes](./logs/verification-summary.json).

The skipped cases are the five opt-in live-model stress/quality tests, one separately supplied V4 codec check, one Windows-only smoke test and one unconfigured live OpenViking test. Native Mnemon integration was enabled and passed.

The tested fixed root tarball SHA-256 is `13ce7382311342126dc0089e05dfbd841aaf4ac79ae0b0110978967a6b620b02`. Child packages use the Starter's pinned published versions. Third-party Provider backends are installed as private children but not contacted; this check makes no claim about their remote availability. Storage formats, RPC authority, client/host scope, bundle IDs and namespaces are unchanged.

The disposable profiles disable the unrelated shell/PTY tools; the newer settings probe also disables their PTC/workflow dependents. Native attachment services stay enabled for full WebUI runs.

## Real WebUI acceptance

Only model decisions are deterministic. Browser, DSH tools, Runtime storage, CLI discovery, settings writes and restart are real. The reused [Issue 261 model fixture](../issue-261-dsh-slots/harness/compatibility-model.mjs) retains its `compatibility-261` trigger and visible “Issue 261” test text; these screenshots were captured in this Issue 274 Desktop generation environment.

The conversation successfully calls Runtime add and status:

![Actual tool receipts and healthy CLI](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-274-profile-generation/images/after-tool-receipts.png)

The UI saved `idleReview.minIntervalMs = 310000`. A new Host process reopened the same profile; the setting remained visible and the settings file was byte-identical:

![Saved interval after a cold Host restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-274-profile-generation/images/after-cold-restart-settings.png)

The single synthetic work-memory entry remained visible, and `runtime/memories.json` was byte-identical:

![Runtime entry after a cold Host restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-274-profile-generation/images/after-cold-restart-runtime.png)

## Reproduction

Use an extracted official Desktop release, Node 24, pnpm 10.13.1, a verified native Mnemon CLI and disposable directories. No personal `DSH_HOME`, model credentials or memory is used. `DSH_DESKTOP_RESOURCES`, `MNEMON_CLI`, `PNPM_ENTRY`, and `FIXED_PACKAGE` below refer to those explicit local files; `FIXED_PACKAGE` is a root tarball produced after `pnpm build` and `pnpm pack`.

```bash
node docs/pr-assets/issue-274-profile-generation/harness/desktop-generation.mjs \
  --resources "$DSH_DESKTOP_RESOURCES" --run-root /tmp/issue274-before \
  --package dsh-mnemon@0.5.13 --pnpm-entry "$PNPM_ENTRY" \
  --native-cli "$MNEMON_CLI" --serve true

node --no-addons docs/pr-assets/issue-274-profile-generation/harness/desktop-import.mjs \
  --state /tmp/issue274-before/server.json --mode grouped
# Repeat with --mode flat, direct, and import; all four fail before the fix.

node docs/pr-assets/issue-274-profile-generation/harness/desktop-generation.mjs \
  --resources "$DSH_DESKTOP_RESOURCES" --run-root /tmp/issue274-after \
  --package "$FIXED_PACKAGE" --pnpm-entry "$PNPM_ENTRY" \
  --native-cli "$MNEMON_CLI" --serve true
```

Open the URL from the mode-0600 `server.json` locally; never attach the file or paste its access key into a report. Use the fixture workspace, select Mnemon E2E and send `compatibility-261`. Save the interval in Memory System settings, send `SIGUSR2` to the harness PID from that JSON, then reopen its updated URL to confirm settings and Runtime content. `SIGTERM` stops the disposable server. A restart does not reinstall packages or rewrite settings.

For Headless, use a fresh run directory and append `--profile headless`; repeat with `--root-disabled true`. Desktop's projector keeps only its Web-owned builtins, so the harness adds the official Headless bundle to its disposable profile after projection. The resulting real model requests and tool surface are asserted before `headlessVerified` is written.

`profile-legacy.mjs` takes the same resource/package/runtime arguments and checks old Settings through the original CLI's profile runner. `profile-live.mjs` takes `--desktop-resources`, `--framework-modules` pointing at a separate public npm DSH `0.1.7-rc.1` installation, and the same package/pnpm/run-root/native-cli arguments. It uses the Desktop installer for the generation and the unmodified newer profile runner for live settings. All raw model requests, access URLs and process logs remain local; the repository retains only these sanitized diagnostics, aggregate assertions and three screenshots.
