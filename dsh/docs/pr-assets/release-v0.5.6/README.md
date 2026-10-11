# v0.5.6 release verification

[简体中文](./README.zh-CN.md) · [Release notes](../../en/releases/v0.5.6.md)

Run on 2026-09-10 (Asia/Shanghai), from the dedicated release worktree based on main `0a499d11e9853851ed11aa92633e8122d11f9545`. These screenshots use the versioned release composition: Starter 0.5.6, five changed plugins 0.5.5 and eleven unchanged plugins 0.5.4. This release commit changes versions, generated Provider version metadata and documentation; it adds no runtime behavior beyond that main revision.

Environment: macOS arm64, Node 22.19.0, pnpm 10.13.1, published DSH 0.1.2-rc.1 and Native CLI 0.2.7.

| Check | Result |
|---|---|
| Complete `pnpm run verify` | Passed: 852 root tests and 324 plugin tests |
| Native integration | Passed: disposable View write, recall and forget |
| Deterministic build, types, public entries, package lint | Passed; root has 46 files and 1,256,247 unpacked bytes |
| Real Headless profile | Passed: 39 tools, 8 representative Mnemon tools, restart and legacy disable |
| `node scripts/verify-plugin-artifacts.mjs --skip-build` | Passed: 16 independent repositories, 17 packed artifacts, external consumer and real Starter composition |
| Release selection against v0.5.5 | Six packages selected, with dependency-safe layers and stable `latest` tags |
| WebUI | Settings save, version display, Runtime write and reload passed; zero console errors |

Five opt-in Flash tests and one Windows-only binary test were skipped. Historical model results are not rerun or rescored by this release check.

## Browser smoke

Start `pnpm e2e:serve` after the complete build. Use its authenticated launch URL and disposable Profile; all requests remain on loopback with a scripted model endpoint.

1. In Settings → Memory system, select centralized workspace isolation and global USER.md. Set the central root inside the temporary fixture. Save reports that the configuration is live.
2. Register the fixture workspace. The status page displays `dsh-mnemon 0.5.6` and centralized workspace storage.
3. Add the synthetic work memory shown below. Reload the page and reopen Runtime: one entry remains with its original creation time.
4. Check the filesystem: the entry is in `central/workspaces/<canonical-path-hash>/runtime/memories.json`; no `.mnemon` directory was created in the project.

The three screenshots were visually reviewed. The temporary Profile, memory and browser tabs were removed afterward. The broader nine-screenshot [Core workflow](../issue-189-core-workspaces/README.md) retains its original revision labels.

| Screenshot | Evidence |
|---|---|
| [01 Settings saved](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.6/01-settings-saved.png) | Centralized isolation and its persisted root setting |
| [02 Version and storage](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.6/02-version-and-storage.png) | Release version, workspace selector, active scope and one project entry |
| [03 Runtime after reload](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.6/03-runtime-after-reload.png) | Synthetic memory persists after a real page reload |
