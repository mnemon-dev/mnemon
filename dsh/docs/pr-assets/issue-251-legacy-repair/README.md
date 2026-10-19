# Issue #251: explicit legacy Session recovery

[简体中文](./README.zh-CN.md) | [Issue #251](https://github.com/omdsh-dev/dsh-mnemon/issues/251) | [Recovery procedure](../../en/guides/operations.md#dsh-015-compatibility-and-legacy-session-recovery)

Reproduced from main `6ad99cc1890714355e1bb0e9230f3fce674bfb73` on 2026-09-14, using macOS arm64, Node 25.1.0, pnpm 11.19.0 and the published DSH 0.1.5-rc.1 packages. Only synthetic Session logs and disposable Profile, workspace and memory directories were used. The repair stays in the Starter's explicit maintenance CLI; it does not change Core/Source/Provider ownership, intercept history loading or modify DSH's frozen validator.

## Before and after in the actual WebUI

The [runtime fixture](../../../tests/fixtures/issue-251-legacy-v0.jsonl) extends the synthetic 0.1.2-produced history with the missing historical Runtime summary and an independent plugin's snapshot. Its session id, workspace, preset and model route were adapted to the disposable `scripts/serve-e2e.mjs` harness, then each line was encoded as a checksummed Zstandard frame.

The old CLI reported two repaired messages. Opening its copy in the actual WebUI still failed with `user/message 7 source summary requires notice form`: `Runtime memory snapshot` remained. Repository commit `000ecc1568899a794ecb5f8ccf6f28a90c13894e` confirms this exact summary was emitted as `dsh-mnemon` / `recall` by `memorySnapshotMessage()`.

The updated CLI reported three repairs. The original historical user request and assistant answer loaded. A new canary turn completed in the WebUI; after restarting the Host and reopening the Session, both turns remained readable. The selected configured model route used a loopback response server; no external model API was called. The original compressed backup SHA-256 remained `415d0a9470f9eb7301bcd02333d8be428b29082fe7b0ae74e43da43336022d3e`; the repaired copy was `2e69362ea5284683b933ef6b8f2e480486a723930a0fa262b657b80c63fa87e0`.

| Before: old repair leaves Runtime summary | After: resume, restart and cold reopen |
|---|---|
| ![Historical summary refusal](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-before-runtime-summary.jpg) | ![Both conversation turns retained](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-after-cold-reopen.jpg) |

A second WebUI artifact uses the [combined fixture](../../../tests/fixtures/issue-251-repairable-v0.jsonl): all three summaries, a compatible v2 descriptor and a packed row with empty string ID/name. The old repair still failed at Runtime summary 7. The updated CLI reported three summary repairs, one descriptor promotion and one packed row expanded into three chunks, with no blockers. The actual DSH loader migrated the copy, and the UI displayed the historical user request, assistant answer and expanded `synthetic_lookup {}` call with `Synthetic tool response.` Selecting the configured loopback `DeepSeek-V4-Flash` route restored the interactive composer. The preserved original hash is `94f7da957d213c896c4794603580c9e982670bcd725ecffeee241eab3ad709e5`; the repaired copy is `07db52eaacf6d45fc2d4c0a2e8931604674ce332c3a0f823aeed4d4aacaeed3b`.

| Combined artifact before repair | After migration and tool-history rendering |
|---|---|
| ![Combined legacy history refused](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-combined-before.jpg) | ![Historical messages and expanded tool result](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-combined-after.jpg) |

A canary turn also completed through the combined Session's actual WebUI. Cold reopening after a `SIGUSR2` Host restart retained the historical messages, expanded tool result and new exchange. The physical v3 log contained 43 rows. Structural comparison retained all five historical user/plugin messages apart from the three removed summary fields, both historical assistant messages, the durable tool call/result and the expanded stream. The original and repaired v0 hashes remained unchanged; external model calls remained zero.

| Historical messages and tool output after cold reopen | Canary exchange retained after cold reopen |
|---|---|
| ![Combined history after Host restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-combined-cold-reopen.jpg) | ![Tool output and continued exchange after Host restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-combined-cold-new-turn.jpg) |

Two further fixtures isolate null streaming names: [raw deltas](../../../tests/fixtures/issue-251-null-name-v0.jsonl) and [packed deltas](../../../tests/fixtures/issue-251-null-name-packed-v0.jsonl). Both retain valid durable tool identities and start with an empty argument fragment. The actual WebUI rejected the originals at the v1→v2 accumulator and packed decoder respectively; the preceding CLI revision `83d0c40` also refused copies. The updated CLI normalized three logical names in each, expanding only the packed row. Both histories then loaded, displayed the original user/assistant messages and tool result, and accepted a new canary turn through the loopback route. Each v3 log had 42 physical rows; structural comparison retained historical messages, durable calls/results and exact timed replay after the intended name normalization. Original and repaired v0 hashes remained unchanged, as recorded in [machine-readable evidence](./verification.json).

| Null-name artifact before repair | After migration and history rendering |
|---|---|
| ![Raw null names refused](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-null-raw-before.jpg) | ![Raw null names repaired](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-null-raw-after.jpg) |
| ![Packed null names refused](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-null-packed-before.jpg) | ![Packed null names repaired](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-null-packed-after.jpg) |

After a Host restart, both Sessions cold reopened in an independent Chrome test window against the same disposable Profile. Historical tool output and the earlier canary exchanges remained visible. The in-app browser had encountered a client-bundle load failure before Session selection; the bundle returned HTTP 200 and passed syntax checking. Chrome loaded the same Host normally, without changing Host or browser security configuration.

| Raw null-name history after cold reopen | Packed null-name history after cold reopen |
|---|---|
| ![Raw history, tool result and canary after restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-null-raw-cold-reopen.jpg) | ![Packed history, tool result and canary after restart](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-null-packed-cold-reopen.jpg) |

Two final fixtures cover the completed empty-ID chain: [recorded ID](../../../tests/fixtures/issue-251-existing-id-v0.jsonl) and [all four legacy families](../../../tests/fixtures/issue-251-all-legacy-v0.jsonl). They use consistent `deepseek-official` writer metadata, complete usage records and an independent plugin snapshot. The ID-only WebUI input expands the fixture's packed placeholders into equivalent raw deltas to isolate the durable-ID failure. The full combination retains packed null-name placeholders, all three summaries and descriptor v2. Session id, workspace and preset are adapted to the disposable Profile; original inputs are kept separately.

CLI revision `2612856` refused both inputs without publishing output. The actual WebUI rejected the ID-only history at `assistant/message 27 ... id must be a non-empty string`; the full combination failed at summary 5. The updated CLI restored one chain and seven identity fields in each. The combination additionally repaired three summaries and one descriptor, expanded one packed row into two chunks, and normalized two names. Both histories displayed the original user/assistant messages and expanded tool result in Chrome.

| Before the recorded-ID repair | After history and tool-output recovery |
|---|---|
| ![Empty durable tool identity](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-existing-id-before.jpg) | ![Recorded identity recovered](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-existing-id-after.jpg) |
| ![All four legacy shapes](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-all-legacy-before.jpg) | ![All four shapes repaired together](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-all-legacy-after.jpg) |

Each Session then received a `legacy-replay-251` canary through the real WebUI with the configured loopback Flash route. The [test model](../../../scripts/fixtures/legacy-session-replay-model.mjs) validates the actual HTTP request: the historical `tool_calls[].id` and `tool_call_id` must equal the recorded `provider-existing-id-251`, the tool name and arguments must remain `synthetic_lookup` / `{}`, and the output must remain `Synthetic tool response with the original provider ID.` Missing, conflicting or freshly invented identities cannot produce the success response.

Both canaries completed. After a Host restart, the historical messages, expanded tool result and new exchange reopened. The 42 physical v3 rows present before restart remained structurally identical; DSH appended one normal `session/end-seed` boundary, resulting in 43 rows. Original and repaired v0 hashes remained unchanged. The full combination's original SHA-256 is `02918e5bf3ddc83a1c7087fce7622ad46b91bd8c1987ffb423d2669ce276b281`; its repaired copy is `1e30311e7422a4eb6c6b3f5c679901b8dfe902205e6b287f9a7a49ab885ad7f6`. The [machine-readable record](./verification.json) includes both cases, cold artifact hashes and wire payload hashes.

| ID-only history and canary after restart | All-four history and canary after restart |
|---|---|
| ![Recorded-ID cold reopen](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-existing-id-cold-reopen.jpg) | ![All-four cold reopen](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/251-all-legacy-cold-reopen.jpg) |

The shared Starter baseline also loaded all three optional strategy extensions and reported the installed Native CLI 0.2.8. A separate disposable real-CLI create/write/keyword-recall/forget smoke passed. [Native status screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-251-legacy-repair/baseline-native-status.jpg). These checks do not imply that this patch changes Native storage.

## Audited transformations and boundaries

| Shape | Verified behavior |
|---|---|
| Three historical Mnemon summaries | Remove only the recognized summary members; preserve bodies and other plugin sources. |
| Compatible descriptor v2 | Change only version 2 to 3 after checking the exact historical keys and every applicable frozen v3 constraint. |
| Packed tool deltas with empty string ID or name | Expand to their exact raw delta events, preserving logical sequence, timing, names and arguments. |
| Already raw empty string deltas outside a proven identity repair | Keep byte-identical; the actual released migration supports them. |
| Own null names in exact raw or packed tool deltas | Retain the field as `name:""`, preserving assembly and token timing; expand packed rows without changing IDs, logical coordinates or arguments. |
| Cleared durable identities with one original provider ID retained in the exact completed stream | Restore only that recorded identity after stream, advertisement, execution, result and related-reference checks. |
| Missing/conflicting identity candidates, unresolved owner references, incompatible descriptors, unknown or unsafe affected delta shapes | Report bounded line/event/path diagnostics, exit 1 and publish no output. |

Descriptor v2 was found in published `dsh-subagent@0.1.1-rc.2`; the audited `0.1.2-alpha.2` and `0.1.2-rc.1` artifacts already use v3. Comparing `lib/types/descriptor.js` and the cold-resume code in `continuation.js` shows v3 adds optional `agentReasoningEffort`. Leaving that absent preserves the old declared composition. One-shot records permit only version/mode/provider and optional label. Continuable records permit label, a paired agentProvider/agentModel, persona and closed allow/deny tool filters. No field is trimmed, synthesized or discarded. Runtime defaults across different DSH releases are outside this equivalence claim.

Historical `dsh-session@0.1.2-rc.1/lib/types/chunk-rows.js` accepts string placeholders and defines their exact expansion. The current physical decoder rejects packed empty IDs. Packed empty names can pass migration but then fail `expandAssistantStream`. In contrast, the actual v0→v3 path preserves raw empty-string deltas through `AssistantStreamAccumulator`. Tests therefore validate complete migration **and timed stream replay**, not just the exported standalone payload validator.

For null names, the official `dsh-llm-deepseek@0.1.2-rc.1` emitter copies a non-undefined transport name without a string check. Both the old and current `BlockAssembler` assign names only when truthy, so null→empty produces the same state transition at every prefix. Both versions count a token when arguments are nonempty **or the name property is present**; retaining the property preserves TTFT, including an initial empty argument fragment. The current accumulator keeps empty-string names as raw records. A separate audit executed the published old and new assemblers across 36 prefixes and five variants, compared actual old/current Session statistics, and verified v3 publication, cold reopen and durable/owner metadata preservation. Packed null names were outside the old codec, but normalization followed by expansion matches that codec's decoding of the normalized row exactly. The repair uses closed field sets, safe coordinates and duplicate-key checks, without matching a later call or inventing a name.

The published `dsh-llm-deepseek@0.1.2-rc.1` adapter can clear an earlier nonempty provider ID when a continuation explicitly supplies an empty or null ID. The current adapter treats those continuation values as no update. Independent probes executed both published adapters against the same synthetic SSE frames: restoring the earlier recorded ID made the old chunks equal the current output. The old official Session hot-write path also accepted the resulting broken durable chain, while cold seeding rejected it. The old agent loop records `tool/result.sourceEventSeqs` pointing to its `tool/call` event.

Recovery selects the exact assistant stream through ordered provenance, checks block order, completion, usage, names and argument assembly, and pairs every affected advertisement with its execution and result. A single-call fallback can prove the result association when result provenance is absent; conflicting explicit provenance is refused. Text/reasoning and multiple independently provable calls are supported. Approval, PTC, copied-message, replacement and plugin identity references must be accounted for; unresolved ownership causes whole-file refusal. Nonempty chains that share the step are checked for internal consistency too. No ID is generated, and empty deltas before the first recorded candidate remain unchanged.

Dropping deltas would lose timestamps, arguments and provenance. Deleting a null name property can change token timing; replacing its value with an empty string does not. Logs with no retained provider identity or an ambiguous association cannot be reconstructed from a new synthetic ID, so those inputs still refuse output. These limits are distinct from the four reported shape families covered by the positive migration and WebUI fixtures.

The published v0→v1 migration `lib/index.js` is byte-identical in 0.1.5-rc.1 and 0.1.5-rc.2 (SHA-256 `15ae26b90310d83b1b90a5e7cad9e2f34282fddaba2f19f2fd2232382065603d`). Full execution here uses rc.1. No published `dsh@0.1.2-rc.2` was found in the registry; the reported source build cannot be identified without its commit.

## Regression and reproduction

[Repair tests](../../../tests/legacy-session-repair.spec.ts) exercise published JSONL persistence, v3 publication, cold reopen and exact timed stream expansion; strict descriptor gates; combined repairs; null-name prefix assembly and token timing; unrelated plugin preservation; raw and compressed idempotence; exclusive output creation; diagnostics and refusal; duplicate fields, malformed frames, unsafe coordinates and expanded-size limits. The [combined fixture](../../../tests/fixtures/issue-251-repairable-v0.jsonl) covers summaries, descriptors and string placeholders; the separate raw/packed null-name fixtures isolate that failure. [Machine-readable evidence](./verification.json).

Final `pnpm verify` passed: 1,104 root tests and 323 independent plugin tests, with seven opt-in skips. The repair suite has 132 cases; ten fixture tests ensure invalid continuation identities or changed tool payloads cannot return a false WebUI success. Deterministic builds, types, docs, public entries, publint/attw and real Headless activation passed. The package contains 47 files: 293,317 packed bytes and 1,313,898 unpacked bytes, within the 1,318,000-byte guard. Host and Client bundle code is unchanged by this compatibility repair.

Independent review checked 144 descriptor combinations, 125 packed coordinate boundaries, five null-name variants across 36 prefixes, and 28 recorded-ID boundary cases. All eight accepted identity cases also passed separate real migration, cold reopen and embedded-stream checks. A separate 300-chain preview smoke recovered 2,100 identity fields; its final output hash was stable. This scale preview is not a separate migration assertion.

```sh
pnpm install --frozen-lockfile
pnpm exec vitest run tests/legacy-session-repair.spec.ts tests/lifecycle.spec.ts
pnpm run verify
node bin/repair-legacy-session.mjs --input tests/fixtures/issue-251-repairable-v0.jsonl
node bin/repair-legacy-session.mjs --input tests/fixtures/issue-251-null-name-v0.jsonl
node bin/repair-legacy-session.mjs --input tests/fixtures/issue-251-all-legacy-v0.jsonl
MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve --strategy-extensions --legacy-session-replay
```

The WebUI screenshots cover Runtime summary recovery, summary/descriptor/packed-string combinations, both null-name forms, recorded durable IDs and all four families together. Exact timed delta preservation is additionally verified by the published loader and stream replay tests. No Windows source build, live third-party Provider or production Session was tested.
