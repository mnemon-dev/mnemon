# Runtime capacity pressure with DeepSeek V4 Flash

[简体中文](README.zh-CN.md) · [Aggregate results](results.json) · [Before-fix examples](before-fixes.json) · [Full evidence ZIP](https://github.com/user-attachments/files/32006240/dsh-mnemon-pr212-evidence-20260909-2994e3d.zip) · [Archive SHA-256 and contents](../runtime-flash-evidence-20260909.json)

**Passed:** on 2026-09-09, three scenarios completed 240 writes and 38 automatic archives. A quoted-input diagnostic completed another 96 writes and 17 archives. All committed text was preserved exactly, the default 10,240-byte limit held, and no maintenance tasks leaked. Every request and observed response used `deepseek-v4-flash`, with thinking disabled.

The workload models [issue #203](https://github.com/omdsh-dev/dsh-mnemon/issues/203): backend, frontend, Android and operations workstreams share project Runtime. Facts and stores are synthetic and disposable. Environment: macOS arm64, Node v25.1.0, DSH 0.1.2-rc.1, Native CLI 0.2.7. The recorded baseline is `583842c2847bfb1f0bf4423cc41c261fd4b941d9` plus the fixes committed in `fce3873e9669c9a9eefb902b76ca8d0ddbf68700`; tested source hashes remain in the aggregate results.

| Scenario | Writes / archives | Peak hot bytes | Final hot / cold rows | Duration |
| --- | ---: | ---: | ---: | ---: |
| Four root sessions, one space | 96 / 16 | 10,235 | 11 / 94 | 67.5 s |
| Four child writers, four spaces | 96 / 16 | 10,229 | 13 / 95 | 244.0 s |
| No open user sessions, Web RPC | 48 / 6 | 10,237 | 12 / 45 | 76.5 s |
| Quoted-input diagnostic, one space | 96 / 17 | 9,646 | 11 / 94 | 65.5 s |

Hot and cold rows can overlap and must not be summed as unique facts. Both concurrent cases reached four simultaneous model calls and write dispatches. Children used `maxDepth: 1`; all 80 child-scenario and 30 session-free maintenance tasks were released. The session-free case disposed all parent handles before writing.

The main run used 275 Flash calls, 441,228 input tokens and 77,231 output tokens. Tool-write p95 was 834 ms for one space and 14,157 ms for child writers across four spaces; RPC latency was not individually instrumented. These measurements include queueing and model routing.

## Findings and limits

- **Fixed exact archival:** ordinary Native semantic deduplication imported only one of three similar originals. The child workload blocked after 14 writes because the Host rejected an inexact receipt. The Provider now reuses exact snapshot content and imports remaining originals with `--no-diff`, validating exact receipts. Ordinary `remember` semantics remain unchanged.
- **Fixed UTF-8 corruption:** chunk-wise decoding corrupted a split Chinese comma after 84 writes / 13 archives. Each owning process module now streams stdout/stderr decoding independently. The diagnostic rerun preserved all 96 actual submissions; model-added surrounding quotes are counted separately.
- **Routing remains imperfect:** 85/95 child-scenario cold rows matched their intended topic; ten went to another authorized space and three duplicate cold copies remained. Session-free routing matched 45/45. Authorization and exact retention passed; semantic routing is a separate quality finding.
- Explicit memory writes drove capacity pressure; idle recall/distillation was disabled. Natural-conversation quality is covered by the separate [automatic-memory acceptance](../runtime-memory-quality-flash-20260909/README.md). Windows, independent DSH processes, long uptime and other live Providers were not tested.

## Reproduce and inspect

Provide `DEEPSEEK_API_KEY` and a verified `MNEMON_NATIVE_TEST_CLI`, then run the retained [opt-in harness](../../../tests/runtime-capacity-flash-stress.spec.ts):

```sh
MNEMON_RUN_FLASH_STRESS=1 MNEMON_FLASH_STRESS_ROUNDS=8 MNEMON_FLASH_STRESS_REPORT=/tmp/mnemon-flash-stress.json pnpm exec vitest run tests/runtime-capacity-flash-stress.spec.ts
MNEMON_RUN_FLASH_STRESS=1 MNEMON_FLASH_STRESS_ROUNDS=8 MNEMON_FLASH_STRESS_JSON_PROMPT=1 MNEMON_FLASH_STRESS_REPORT=/tmp/mnemon-flash-json.json pnpm exec vitest run tests/runtime-capacity-flash-stress.spec.ts -t 'four concurrent root'
```

The harness rejects non-Flash requests before dispatch, checks returned model ids, caps each scenario at 240 calls and cleans up its sessions/stores. Ordinary CI skips the three live tests. Eight configured rounds produce four waves in the session-free case.

The ZIP preserves the complete original reports, per-wave results and harness snapshot from `2994e3de8cc27a53cd3b54d9c5dc7d6628d1646c`. Verify its SHA-256 using the linked manifest; `MANIFEST.json` inside lists each file's hash. This evidence reorganization did not rerun the model or alter measurements.

After the fixes, full verification passed 834 root tests and the independent-artifact gate passed 16 plugin repositories / 17 artifacts. After the main merge, verification passed 836 root tests (five live opt-in tests skipped), types, documentation, deterministic builds, Headless and package checks; see the revision-specific [validation record](../runtime-memory-quality-flash-20260909/validation.json).
