# View workspace — 2026-09-01 verification

Implementation and interaction verification, not a model-quality benchmark or release.

## Scope

- Branch: `codex/view-concretization`; starting revision `c2d2f7db74956dffdbf791003bb30fa21ffb9f32`.
- Browser/UI checkpoint: `250463c`; final code checkpoint: `4f88a3f` adds only Host-side validation/isolation for malformed optional editor metadata, plus regressions. It does not change rendering, default composition or model prompts.
- Main remains `a990d45c42d85102ec9390b7df04746d4f6654c9`; no push or publication.
- Tab names: **视图** (Chinese), **View** (English). Shared Sidebar/Builtin page, existing theme tokens, responsive inspection/configuration panels.
- Actual pinned/latest View and read-only draft preview are distinct. No fabricated historical snapshot, model calls, or memory writes in preview.
- Dedicated Strategy Entries optionally export a public, pure configuration descriptor. Native Cordis Entry updates own activation; DSH settings persist Profile-local preferences. No alternate plugin registry or package-YAML rewrite.
- Existing Source/Provider business behavior and the default Starter's activation remain unchanged. Optional strategy plugins still require installation/registration through DSH.

## Automated checks

Environment: macOS, Node `v25.1.0`, pnpm `11.19.0`, DSH `0.1.1-rc.2`, Cordis `4.0.1`.

| Check | Observed result |
| --- | --- |
| Final `VITEST_MAX_THREADS=4 VITEST_MAX_FORKS=4 pnpm verify` at `4f88a3f` | Passed: root/plugin typechecks and builds, tests, deterministic build, real Headless, package/public-entry lint; only verification concurrency is limited |
| Final expanded Vitest suite | **110 files / 972 tests passed**, 2 files / 2 tests conditionally skipped |
| Deterministic root build | All 38 generated files matched |
| Performance fences | 100 three-Source compositions for each default/additive profile; existing CPU < 2,000 ms and wall < 5,000 ms thresholds passed, unchanged |
| Real Headless | 37 total tools / 7 representative Mnemon tools; settings preservation and restart passed |
| Package/public entries | 45 packed root files; 11 Node-compatible entries / 25 public type dependencies; `publint --strict` and `attw` passed |
| `pnpm verify:plugins --skip-build` | Passed after the full build: 16 repositories outside the workspace / 17 exact packed artifacts |
| Independent editor author | External consumer compiled and tested a Strategy configuration descriptor against packed public SDKs; each plugin independently installed/typechecked/tested/built |
| `pnpm release:check` | Passed: coordinated `0.5.0-beta.1`, `beta` tag, no publication |

Package tests are included in the root count, not added twice. Skips are the opt-in Native binary test and Windows-only smoke. Upstream UI-primitives missing-source-map warnings were not hidden. An earlier full run found the old SystemPrompt-order fixture lacked generation/snapshot fields; the fixture now models those fields and additionally asserts the actual Host snapshot. It was rerun successfully, without weakening the ordering assertion or bypassing production code.

The unmodified-concurrency gate passed 965 tests at `9bb9973`. A subsequent expanded-suite run passed 966 tests but exceeded the existing additive wall-time fence once: 5,861.92 ms for 100 compositions against a 5,000 ms limit (the CPU fence passed). This failed run is retained as a timing limitation; no production algorithm or threshold was changed to make it pass. Both unchanged performance tests passed in isolation (whole-case durations 1.224 / 1.053 s), in the four-worker 967-test suite (1.450 / 1.365 s), and in the final 972-test gate (1.670 / 1.810 s). These durations include setup/warmup and are not per-request production latency. The pattern is consistent with parallel-load variability, not a guarantee that the unrestricted-worker gate never fluctuates. Headless and package/public-entry checks were rerun successfully after the final suite.

Additional focused tests cover real native Loader discovery of disabled Entries; read-only preview; immutable old pins; individual toggle preservation; complete Strategy selection and return; Profile-isolated persistence; concurrent revision conflicts; activation/storage rollback; new workspace attachment during a transaction; bounded failure restoration; malformed optional editors; remote/read-channel write rejection; explicit-empty versus omitted fields; stale drafts and asynchronous UI responses; locale subscriptions and lifecycle disposal. Final metadata regressions reject invalid localized labels, Source-role lists, defaults and non-JSON values locally, while the real default View remains available.

## Real browser checks

Used the real pinned DSH WebUI with the built packages through `node scripts/serve-e2e.mjs --strategy-extensions`. Its disposable Profile, workspace and memory were separate from personal data; all four conversation turns and the session title used a loopback-only model stub (five requests total). Preview produced no additional model requests.

| Interaction | Observed result |
| --- | --- |
| No-session View | Honest empty state; configuration and preview still available |
| Enable scoped/light-context/auto-capture together | All three active after explicit save; actual Cordis Entries and Profile settings agreed |
| First actual turn | 512 projected characters, three fragments, three extensions; a real Runtime test note was present |
| Change budget and disable only auto-capture | Preview 384 characters with scoped/light-context; saved first-turn View and generation IDs stayed identical |
| Next actual turn | 384 characters with only scoped/light-context |
| Restart the same disposable Profile | Enabled states and explicit 384-character value restored; no fabricated old snapshot; next actual turn used restored settings |
| Reopen Sidebar after another turn with an unsaved draft | Actual View advanced to turn 4; the draft remained unsaved and did not affect that turn |
| Chinese/English live switch | All five tab labels updated; Chinese 视图 and English View verified |
| Layout | Light/dark appearances, Sidebar/Builtin, wide two-column and 320/360px stacked layouts checked; narrow English buttons corrected and retested with no panel overflow |
| Narrow-screen preview click | Succeeded at 360px; read-only preview remained clearly labelled |
| Existing pages | Runtime note creation, Document creation/Markdown reading, and Memory Spaces navigation worked |
| Source changes after a turn | New Document appeared in fresh preview; the actual pinned snapshot still showed its original zero-Document state |
| Browser errors | Error-level browser log query returned an empty list after final interactions |

Browser checks caught stale retained Sidebar snapshots, cached Source navigation translations, and narrow English action-label overflow. Fixes use existing visibility/locale lifecycles and CSS; Source Fibers are not remounted to translate labels.

The test tab was closed, viewport override reset, server/model stub stopped, and the owned `mnemon-web-e2e-OOCIGx` temporary directory removed. No production profile, memory or cloud account was modified.

## Limits

This verifies UI and local composition behavior, not real-model recording quality, production latency, live cloud Provider accounts or Native/Windows binaries. Resident character counts are neither tokens nor total prompt cost. View inspection exposes no read grants or provider credentials. Recent snapshots last only for the running Host/agent lifecycle, not as a persistent historical transcript. Strategy conflicts remain explicit; not every independent complete Strategy is an additive plugin.

Usage: [English](../../en/guides/ui-guide.md), [简体中文](../../zh-CN/guides/ui-guide.md). Authoring: [English](../../en/development/extensions.md), [简体中文](../../zh-CN/development/extensions.md).
