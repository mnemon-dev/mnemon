# Issue 265: Builtin width-handle evidence

[English](README.md) | [简体中文](README.zh-CN.md)

On main `65c0e23ba410993e16c00c8b3adf92d59d853425` (Mnemon 0.5.12), DSH's conversation width handles remain mounted over the Builtin memory page. Hovering the left strip shows a vertical mark; hit testing reaches the native handle instead of the memory page.

The fix uses a CSS `:has()` rule for the conversation that directly owns Mnemon's Builtin surface. Only that conversation's direct `data-width-handle` children receive `visibility: hidden` and `pointer-events: none`. The Builtin root remains `position: static; z-index: auto`; its resident composer remains usable. Returning to Chat restores native dragging. The direct ownership chain also excludes Sidebar, nested/sibling conversations, and other plugin views. A raised stacking layer was rejected after it occluded the composer; its prototype runs are excluded here.

## Actual WebUI evidence

Recorded on 2026-09-22, using public DSH packages, all 17 packed Mnemon packages, and a loopback model fixture on macOS arm64 / Node 25.1.0. The final artifact set is `fixed-scoped`; its working-tree patch was packed on the main commit above. All 47 packed Root files match the final verified build. [verification.json](verification.json) records all 17 tarball hashes, public-module byte checks, observations, and screenshot hashes.

| Run | Observation | Screenshot |
| --- | --- | --- |
| Main baseline, DSH 0.1.5-rc.2 | Both edge points hit native handles; left hover mark opacity is 1. | [Before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-265-builtin-width-handles/before-rc2.png) |
| Scoped fix, DSH 0.1.5-rc.2 | Both handles hidden and noninteractive; edge points hit Builtin; composer visible and directly hittable. | [After RC2](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-265-builtin-width-handles/after-rc2.png) |
| Scoped fix, DSH 0.1.6-alpha.2 | Same native hit-test result. | [After alpha2](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-265-builtin-width-handles/after-alpha2.png) |
| RC2 Runtime Source | Actual body-portal Add dialog accepted a new memory and displayed a success receipt. Both entries survived reload and session reselection. | [Dialog](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-265-builtin-width-handles/runtime-dialog-rc2.png), [saved memory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-265-builtin-width-handles/runtime-write-rc2.png) |
| RC2 resident composer and Chat | Sent a turn while Builtin was open; Chat showed the new turn and completed model reply. Native width dragging also worked. | [Chat](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-265-builtin-width-handles/chat-restored-rc2.png) |

![RC2 baseline with the native width mark over Builtin](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-265-builtin-width-handles/before-rc2.png)

![RC2 with scoped suppression and the resident composer visible](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-265-builtin-width-handles/after-rc2.png)

Actual Chat drags moved the left edge by 60 px in both cohorts and reduced composer width from 943.20 to 823.20 px. The RC2 width was restored with another real drag. After the Runtime write and browser reload, the handle points still hit Builtin and the composer still hit its own input.

## Validation

- `npx --yes pnpm@10.13.1 run verify`: passed; 1,189 Root tests and 382 plugin tests passed. Seven existing opt-in tests were skipped: five real-model pressure/quality cases, one Windows Native smoke case, and one configured-endpoint OpenViking integration case. Types, deterministic builds, Headless, package contents, `publint`, and `attw` passed. `MNEMON_NATIVE_TEST_CLI` enabled real Native 0.2.8 space creation, View write, recall, forget and two-namespace capacity workflows in isolated storage.
- `npx --yes pnpm@10.13.1 run verify:plugins --skip-build`: passed for 16 independent plugin repositories and 17 packed artifacts, including public SDK composition, Client tests, all three optional strategies together, and Root-only activation.
- The six selector contract tests pass; running them against main's CSS gives three failures. They cover ownership, composer, peer/Sidebar exclusion, nested/sibling isolation, and automatic restoration. Their JSDOM adapter reads the authored rule; the browser runs above separately verify native CSS painting and hit testing.
- Both final Headless runs expose all 17 `mnemon_*` tools, preserve real Runtime add/status data across a new CLI process, enable three strategy extensions, and expose zero Mnemon tools after Root is disabled. Native Mnemon 0.2.8 is configured and version-checked.
- RC2 contains 234 exact-version DSH package records; alpha2 contains 251. Each resolves all 17 Mnemon packages, passes 51 resolution checks, and verifies four installed public UI modules against official tarball bytes. No test Client package is installed.

## Repeating the runs

Use the [published-package harness](../issue-261-dsh-slots/harness/HARNESS.md). Build the selected source first. Set `MNEMON_SOURCE`, `NATIVE_CLI`, and an empty `VERIFICATION_ROOT`; set `RC_FRAMEWORK_ROOT` and `ALPHA_FRAMEWORK_ROOT` to previously verified public consumers of the matching exact cohort on the current platform. From the repository root:

```sh
HARNESS=docs/pr-assets/issue-261-dsh-slots/harness
node "$HARNESS/pack-artifacts.mjs" --source "$MNEMON_SOURCE" --output "$VERIFICATION_ROOT/artifacts/fixed-scoped"
node "$HARNESS/packed-e2e.mjs" --mode fixed --cohort rc --artifacts "$VERIFICATION_ROOT/artifacts/fixed-scoped" --framework-root "$RC_FRAMEWORK_ROOT" --run-root "$VERIFICATION_ROOT/fixed-scoped-rc" --native-cli "$NATIVE_CLI" --serve true
node "$HARNESS/packed-e2e.mjs" --mode fixed --cohort alpha --artifacts "$VERIFICATION_ROOT/artifacts/fixed-scoped" --framework-root "$ALPHA_FRAMEWORK_ROOT" --run-root "$VERIFICATION_ROOT/fixed-scoped-alpha" --native-cli "$NATIVE_CLI" --serve true
node "$HARNESS/headless-check.mjs" --consumer "$VERIFICATION_ROOT/fixed-scoped-rc/dsh-home/profiles/web" --output "$VERIFICATION_ROOT/headless-scoped-rc" --native-cli "$NATIVE_CLI"
node "$HARNESS/headless-check.mjs" --consumer "$VERIFICATION_ROOT/fixed-scoped-alpha/dsh-home/profiles/web" --output "$VERIFICATION_ROOT/headless-scoped-alpha" --native-cli "$NATIVE_CLI"
```

Serve commands remain running; use separate terminals. For the baseline, build and pack the clean main commit and use the same RC2 command with separate artifact/run directories. All three initial installs use ordinary npm peer resolution. Exact framework pins avoid an incomplete upstream RC3 release reached by fresh RC2 transitive ranges; no DSH bytes or metadata are patched and no legacy-peer bypass is used.

Screenshots retain the reusable fixture title “Issue 261 compatibility.” Alpha's terminal-recovery toast comes from the deliberately disabled terminal fixture. These runs certify the public WebUI behavior shown, not terminal operation, Windows, the native desktop executable, or a Native memory-space backend write. Peer isolation is covered by contract tests; the final browser runs did not load the optional peer fixture. This CSS change does not change persistence, configuration, RPC, or credentials.
