# Windows workspace ancestors — issue #266

[简体中文](./README.zh-CN.md) | [Issue #266](https://github.com/omdsh-dev/dsh-mnemon/issues/266) | [Windows observations](./windows-results.json) | [WebUI observations](./webui-verification.json) | [Verification](./verification.json)

On 2026-09-22, real Windows runners reproduced the invalid workspace identity reported in #266. The baseline returned storage hashes for paths below a regular file; the fix rejects both a child and a deeper descendant with `ENOTDIR`. Baseline: `65c0e23ba410993e16c00c8b3adf92d59d853425` (v0.5.12). Implementation: `1750e497e58c24406c0092e4fc1549996283aab8`.

## Native Windows before and after

The runners used Windows build 26100, Node 22.19.0 / libuv 1.51.0 and Node 24.20.0 / libuv 1.52.1. Each run creates a disposable regular file, directory and junction, then imports the repository's TypeScript module directly. The native probe observes `realpathSync.native(file/child)` throwing `ENOENT` (errno `-4058`) while `realpathSync.native(file)` succeeds. No filesystem calls are mocked in this observation.

| Run | Native contract cases, each Node version | Reported test files, each Node version |
|---|---|---|
| [Baseline](https://github.com/omdsh-dev/dsh-mnemon/actions/runs/35715519552) | 10 pass, 2 fail: file child and deeper descendant return hashes | 26 pass, 3 fail |
| [Fixed](https://github.com/omdsh-dev/dsh-mnemon/actions/runs/35715781789) | 12 pass; both file descendants throw `ENOTDIR` | 31 pass, 0 fail |

The baseline workflow deliberately requires the reported failures; its successful workflow status means reproduction succeeded. Both phases retain existing file identities, missing directory descendants, Unicode names, existing roots and matching junction identities. Invalid relative, empty and NUL-containing inputs remain rejected. The original file's content remains unchanged.

The three baseline test failures were the actual non-directory ancestor assertion, the Client import boundary's hardcoded POSIX separators, and the delegated workspace scope's hardcoded POSIX identity. The latter two fixes only make test paths portable. A permanent Windows CI matrix now runs all three files on Node 22.19 and 24.

The validation-only branch contains the [baseline workflow and harness](https://github.com/omdsh-dev/dsh-mnemon/tree/22690919434d18d8537ef74e08a4d200282a199e/scripts/issue-266-windows) and [fixed workflow and harness](https://github.com/omdsh-dev/dsh-mnemon/tree/fde19025ff6452fcfd0643f377593b83279ca232/scripts/issue-266-windows). Check out the corresponding revision on Windows and run:

```powershell
node --experimental-strip-types scripts/issue-266-windows/observe.mjs baseline
# Use fixed instead of baseline at the fixed validation revision.
pnpm install --frozen-lockfile
pnpm exec vitest run tests/workspace-storage.spec.ts tests/client-platform-boundary.spec.ts tests/async-subagent-memory.spec.ts
```

The JSON evidence retains all native case observations and concise test results. Its SHA-256 fields verify the imported source against the stated product revisions, including the runner's LF-to-CRLF checkout conversion. Complete routine Vitest output remains attached to the Actions runs.

## Portable regression and integration checks

Before changing production code, the new portable regression failed against the baseline: 1 failed / 5 passed in `workspace-storage.spec.ts`. It replaces only the first native `realpath` call with Windows's `ENOENT`; resolution of the existing file and filesystem type inspection remain real. This models the missing branch on POSIX and is separate from the actual Windows evidence above. All 31 focused tests pass after the fix on macOS too.

Local verification used macOS arm64, Node 25.1.0, pnpm 10.13.1, published DSH 0.1.5-rc.1 and the official Mnemon CLI 0.2.8. With `MNEMON_NATIVE_TEST_CLI` set, `pnpm run verify` passed 1,185 root tests and 382 plugin tests, deterministic builds, type checks, real Native integration and capacity checks, isolated Headless activation/restart, and package validation. Five root live-Flash tests, one live-OpenViking test and one macOS-inapplicable Windows Native smoke were skipped.

Then `MNEMON_PLUGIN_VERIFY_CONCURRENCY=4 pnpm run verify:plugins --skip-build` passed for 16 independent plugin repositories, 17 artifacts, the public SDK-only external consumer, packed-Starter activation and all three optional Strategies together. Builds and artifact consumers ran sequentially. Seventeen verified artifacts were packed from the clean implementation revision for the separate macOS WebUI smoke below.

## macOS WebUI with published RC2 and real CLI

Chrome on macOS loaded all 17 artifacts from implementation `1750e497`, with published DSH 0.1.5-rc.2 and the real Mnemon CLI 0.2.8. Scoped, Light context and Active capture were enabled together. The isolated profile contains 234 DSH packages, all exactly RC2; all 51 checked peer resolutions stay inside its `node_modules`, and four Client entry files match public package bytes. The Root tarball SHA-256 is `3d5259f8a995a662ecf3e015282f42490a4be13e006e4672715aa05a8f6c353b`; the [WebUI record](./webui-verification.json) contains all 17 artifact hashes and the CLI hash.

The UI selected centralized workspaces and an existing directory named `项目 with spaces`. Sending `compatibility-261` used a local deterministic model to call the real `mnemon_runtime_memory` add and `mnemon_status` tools. The status page showed a healthy system and one Runtime entry. Clicking Runtime → 添加记忆 then saved `Issue 266 WebUI: 中文与空格工作区使用稳定集中存储，Runtime 写入后可读回。`; the page showed two entries and a success message.

| Centralized workspace status | Runtime write and readback |
|---|---|
| ![Healthy centralized Unicode workspace](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-266-workspace-ancestor/webui-centralized-status.png) | ![Two Runtime entries after the UI write](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-266-workspace-ancestor/webui-runtime-write.png) |

Read-only inspection confirmed the canonical workspace hash `b541b67e760240d3e9cf9985777163114eb92d9dad1ce95546b73fd0e04c4c12`, with exactly two entries under `<central-root>/workspaces/<hash>/runtime/memories.json` and the exact added text in its `MEMORY.md` projection. The global Runtime remained empty, and no `.mnemon` directory appeared inside the workspace. The JSON and projection hashes are retained in the WebUI record. The screenshots' “Issue 261” session title and first entry are intentionally retained labels from the reused compatibility fixture.

An unpinned install during this task encountered an incomplete public RC3 transitive release. The smoke therefore used a fresh consumer pinned to the exact published RC2 dependency graph from a previously verified profile, without `--legacy-peer-deps`. An external copy of the [existing setup harness](../issue-261-dsh-slots/harness/packed-e2e.mjs) was adjusted only to honor `--framework-root` in fixed mode; its [small setup delta](./webui-rc2-pins.patch) is retained, and its SHA-256 is `9de47863b19cae46122ab030675879c11b6e598bd46f31c302bd9eeb59b598d2`. No DSH package source or release manifest was changed.

This smoke checks valid centralized workspace operation on macOS, separately from the native Windows rejection evidence. The browser run checked the Native CLI version but did not create a Native Memory Space; real Native writes, recall, forget and capacity checks belong to the full verification above. Model responses and all stored content were synthetic; no external model API or personal credentials were used.

## Compatibility and data

The additional directory check runs only after a successful ancestor resolution with a nonempty missing suffix. Existing file paths without descendants retain their previous identity behavior. Valid workspace IDs, aliases, Unicode names and not-yet-created directory descendants are unchanged. Resolving an identity still writes nothing.

Paths below regular files are now rejected before hashing on Windows. Any old central directory remains on disk; there is no format change, migration, merge or deletion. Rolling back restores the old acceptance behavior without a data conversion. The patch changeset covers only `dsh-mnemon`; no DSH source, published contract, Provider credential or external service behavior changes. The other storage modes and UI instructions were reviewed and require no changes.
