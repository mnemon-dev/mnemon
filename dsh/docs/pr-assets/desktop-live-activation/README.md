# Desktop and official WebUI activation

[中文](README.zh-CN.md)

Verified on 2026-09-28 using isolated profiles and synthetic data. All changes are in dsh-mnemon; neither DSH nor the user's Desktop installation was modified.

## Reproduction and fix

The baseline is published dsh-mnemon `0.5.17` on unmodified DSH `0.1.7-rc.2`. The profile exposes only the root bundle; its 17 component packages exist in a separate installation generation. Start DSH without selecting Mnemon, then enable it through the official plugin page. Runtime, Documents, Memory Spaces and the default Strategy fail with `ERR_MODULE_NOT_FOUND` because the running host still has its startup resolution table.

The candidate prepares Starter dependency resolution before native group activation. A caller-owned Web transport scope also registers Mnemon RPC routes when the official Connection is already running. Without this second fix, a fresh WebUI install activates the components but its page RPC returns HTTP 405.

Existing Entry IDs, the core gate, independent component choices, native configuration schemas and private Provider children remain intact. Previously loaded bundles retain their dependency routes even when disabled. Changes to packages Node has already loaded can still require a host restart.

| Published baseline: missing components and settings | Candidate: components running and settings available |
| --- | --- |
| ![Before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/before.png) | ![After](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/after.png) |

## Candidate and environments

- Base: `accd87513c6330fbf8af64f4352657dcca390991` plus this PR. The candidate retains the unreleased package version `0.5.17`; the changeset requests a patch release.
- Official WebUI: npm DSH `0.1.7-rc.2`, Node `24.20.0`, clean profile, all 18 candidate tarballs installed through the official **Add plugin** flow. A loopback registry serves the candidate packages; no source checkout is linked into the profile.
- Desktop: unmodified DSH `0.1.7-rc.2` bundled with Desktop `0.10.0`, run in a real Electron `44.0.0` main process with Node `24.18.1` using the repository's Electron fixture. `ELECTRON_RUN_AS_NODE` is unset. This checks the bundled host and official WebUI, not a complete Desktop installer update.
- Native CLI: Mnemon `0.2.9` with isolated data. No remote model or third-party Provider was needed for this activation regression.
- [Sanitized verification record](validation.json) records the candidate digest, CLI result and unchanged WebUI host PID.

## Real WebUI acceptance

1. Start the official WebUI with no Mnemon installed. The [clean baseline](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/web-before-install.png) is a separate fresh profile with the same host version.
2. Install the candidate root tarball through **Add plugin**, then choose **Enable now** in the [installation result](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/web-installed.png). The host remains running throughout installation, activation and the operations below.
3. Open Memory System: [Status is healthy](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/web-status.png), with the three Sources and default Strategy available.
4. Create a Runtime entry and read it back: [Runtime result](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/web-runtime.png).
5. Create and read a Project Document: [Documents result](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/web-documents.png).
6. Create and activate a Native Memory Space in the UI. Write a synthetic fact with `mnemon remember`, confirm it with CLI `recall`, then retrieve that exact fact using **Recall → Basic match** in the [official WebUI](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/web-recall.png).
7. Disable and re-enable the bundle. The sidebar returns, Runtime and Documents retain their content, and the [same active Native space and memory remain](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/web-reenabled.png). The original host PID is still alive; the fixture launched it only once.

CLI commands used, with `MNEMON_DATA_DIR` pointing to the isolated test directory:

```bash
mnemon --data-dir "$MNEMON_DATA_DIR" --store default remember \
  'LIVE_INSTALL_ACCEPTANCE: Native CLI and official WebUI share durable memory immediately after installation.' \
  --cat fact --no-diff
mnemon --data-dir "$MNEMON_DATA_DIR" --store default recall LIVE_INSTALL_ACCEPTANCE --basic
```

![Official WebUI retrieves the fact written by the native CLI](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/web-recall.png)

## Electron acceptance

Start the bundled DSH host inside Electron with the installed bundle unselected, then enable it using the official WebUI. The [component list](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/after-components.png), [healthy Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/electron-status.png) and [new Native store](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/desktop-live-activation/electron-native.png) show successful activation and CLI subprocess execution. The existing upstream `cordis:group` display issue remains documented in [compatibility notes](../../en/reference/compatibility.md#dsh-017-bundle-component-list); it does not represent a stopped Source or Strategy.

## Automated verification

```bash
pnpm run verify
pnpm run verify:plugins
```

Both passed on Node `24.20.0`:

- Root suite: 1,513 passed, 6 conditional integration tests skipped. The skipped cases require opt-in Flash stress/quality runs or a dedicated Native capacity fixture; the real Native activation path was checked above.
- Build, type checks, documentation links, package contents, public entry imports, `publint` and `attw` passed. The package contains 55 files and is 1,471,089 bytes unpacked.
- Headless activation exposes 37 tools, including 8 representative Mnemon tools. Settings migration, restart and the legacy core-disable gate pass.
- All 17 independent plugin packages pass standalone installation/build/type checks and their tests, with environment-gated Native/Windows/OpenViking integrations skipped. All 18 packed artifacts pass SDK/Client composition checks, optional Strategy composition and root-only Starter installation.
- Published-host lifecycle regressions cover late activation with isolated dependencies, another bundle disabled since startup, independent component choices, native configuration schema discovery, core/bundle toggles and restart persistence. The official Connection regression confirms late RPC registration and removal when Mnemon is disposed.
