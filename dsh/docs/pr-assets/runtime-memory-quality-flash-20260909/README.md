# Issue #203 automatic memory acceptance

[简体中文](README.zh-CN.md) · [Aggregate results](results.json) · [Minimal finding examples](examples.json) · [Full evidence ZIP](https://github.com/user-attachments/files/32006240/dsh-mnemon-pr212-evidence-20260909-2994e3d.zip) · [Archive SHA-256 and contents](../runtime-flash-evidence-20260909.json)

**Acceptance: passed, with non-blocking improvements retained.** Following user review, this bounded simulation was accepted: 144/152 fresh answers were correct (94.7%), eight were unknown, and all 16 tested formal corrections were answered correctly. No stale, cross-module or unsupported answers occurred; all 192 developer turns finished with passing configuration checks.

Measured production revision: `fce3873e9669c9a9eefb902b76ca8d0ddbf68700`, 2026-09-09. All requests and observed responses used `deepseek-v4-flash`, thinking disabled. The original strict automated verdicts remain `fail`; `acceptanceDecision` records the later user-reviewed pass. The findings below remain unfixed.

| Metric | One space, 12 waves | Four spaces, 12 waves | Four spaces, 24 waves |
| --- | ---: | ---: | ---: |
| Developer turns / fresh sessions | 48 / 12 | 48 / 12 | 96 / 28 |
| Current facts covered | 35/36 | 36/36 | 80/80 |
| Correct fresh answers | 34/36 | 36/36 | 74/80 |
| Correct formal corrections | 4/4 | 4/4 | 8/8 |
| Retained transient markers | 0 | 1 | 3 |
| Obsolete base documents | 4 | 4 | 4 |
| Final hot entries / documents | 22 / 6 | 29 / 5 | 13 / 23 |
| Peak / final hot bytes | 7,095 / 7,095 | 6,684 / 6,684 | 10,079 / 5,615 |
| Idle reviews / model calls | 40 / 321 | 41 / 299 | 85 / 654 |
| Rejected memory calls | 1 | 3 | 4 |
| Duration | 4m 34s | 4m 9s | 12m 5s |
| Reviewed acceptance | Pass | Pass | Pass |

## Findings against the complaint

- **Messy memory partly reproduced:** long-run waves 21 and 23 supplied partial text to a whole-entry replacement. One frontend row lost its module label and seven fields; another lost `timeout_ms=1800`. Exact before/after text and successful calls remain in [examples.json](examples.json). The [Runtime Source](../../../plugins/dsh-mnemon-source-runtime/src/source.ts) applied the submitted whole-entry content; capacity archival was not involved.
- **Retention did not guarantee recall:** one short-run fact was never captured; another existed but was answered unknown. All six long-run unknowns were frontend values still present in Documents. Readers sometimes searched empty cold memory without proceeding to document search.
- **Old and unwanted material persisted:** all 12 base documents retained obsolete values despite Runtime corrections; create-only maintenance accumulated supplements. A wave-13 document retained literal do-not-persist markers from wave 10. No mutations occurred during the no-write turn itself; later history review reintroduced the examples.
- **Operations recovered:** eight rejected calls, duplicate hot facts and missing attribution remain follow-ups. All final development checks passed. The short-four scorer's `development-workload-incomplete` flag counted two recovered intermediate failures; its original flag is preserved alongside this adjudication.
- **No manual cleanup or capacity blockage occurred:** natural consolidation kept hot memory below 10,240 bytes. All three runs had zero archive cycles and empty cold stores. Post-archive quality was not exercised; repeated archival is covered by the separate [capacity test](../runtime-capacity-flash-20260909/README.md).

## Method and limits

Four backend/frontend/Android/operations sessions ran concurrently in one real DSH process. Developer tools actually read, update and validate temporary JSON configurations; the model chose memory actions and the default lifecycle performed idle review. Fresh readers had neither development transcripts nor file access. This is bounded configuration work, not full application builds.

Environment: macOS arm64, Node v25.1.0, DSH 0.1.2-rc.1, Native CLI 0.2.7. Guided recall/writeback and the default capacity stayed unchanged; idle debounce alone changed from 30 to 5 seconds. [protocol.json](protocol.json) retains preregistered parameters, hashes and the pre-run extension rationale. There was one scored run per condition; the two-wave pilot is excluded. Windows, independent processes and a half-day human workflow remain untested.

Frozen scoring required at least 90% coverage and accuracy, all corrections correct, and zero noise, stale/cross-module/unsupported answers, no-write mutations, cold duplicates, capacity overruns or execution errors. Original strict failures and later reviewed acceptance are separate. All 98 final rows and 152 answer cells were reviewed. The experiments used 1,274 Flash calls, 4,557,451 input tokens and 292,774 output tokens.

## Reproduce and inspect

Provide `DEEPSEEK_API_KEY` and a verified `MNEMON_NATIVE_TEST_CLI`. The [test](../../../tests/runtime-memory-flash-quality.spec.ts), [live fixture](../../../tests/fixtures/flash-quality/live.ts) and [workload generator](../../../tests/fixtures/flash-quality/workload.ts) remain in Git:

```sh
MNEMON_RUN_FLASH_QUALITY=1 MNEMON_FLASH_QUALITY_REPORT=/tmp/mnemon-flash-quality.json pnpm exec vitest run tests/runtime-memory-flash-quality.spec.ts
MNEMON_RUN_FLASH_QUALITY=1 MNEMON_FLASH_QUALITY_WAVES=24 MNEMON_FLASH_QUALITY_REPORT=/tmp/mnemon-flash-quality-long.json pnpm exec vitest run tests/runtime-memory-flash-quality.spec.ts -t 'four preconfigured'
```

The ZIP preserves the full original reports, expanded inputs, wave metrics, 152 answer cells, all 98 final rows with review notes, and harness snapshot from `2994e3de8cc27a53cd3b54d9c5dc7d6628d1646c`. Its `MANIFEST.json` lists individual hashes; the linked archive manifest records the ZIP SHA-256. This storage change did not rerun or rescore any experiment. Harness report-only changes after execution are listed in the protocol.

The separate [validation record](validation.json) covers the main merge: full verification passed 836 root tests (five live tests skipped) and independent validation passed 16 plugin repositories / 17 artifacts. Historical model results still apply to their recorded production revision.
