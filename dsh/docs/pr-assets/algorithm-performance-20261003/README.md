# Algorithm performance audit — 2026-10-03

[简体中文](./README.zh-CN.md) | [Development guide](../../en/development/README.md)

This change removes repeated work in local compaction, retrieval, namespace discovery and batch preparation. Storage formats, selection rules, permission checks, result order and Provider call ordering are preserved. It does not claim a globally optimal implementation or measure a remote database's internal search algorithm.

## Reproduction and measurement

Baseline: `d13d9c1ab118e01926c0e60fbf273de8457872df` (main, v0.5.22). Optimized source revision and aggregate measurements are recorded in [metrics.json](./metrics.json). Both checkouts use the same benchmark source and frozen dependency lockfile, run sequentially on the same machine. The primary developer checkout is untouched.

```sh
pnpm install --frozen-lockfile
MNEMON_BENCH_OUTPUT=/tmp/algorithm-benchmark.json pnpm bench:algorithms
# Optional: only the requested small sizes
MNEMON_BENCH_SIZES=1,10,100 pnpm bench:algorithms
```

To reproduce the baseline, copy `tests/algorithm-performance.measure.ts` and `scripts/benchmark-algorithms.config.ts` into a disposable checkout at the baseline revision, then run `pnpm exec vitest run --config scripts/benchmark-algorithms.config.ts`. Baseline lacks the new package script.

The harness creates synthetic data in temporary directories and removes it afterward. No production data, paid APIs, LLM, real network or external CLI is involved. Each case has three warm-up operations followed by nine samples. A sample averages ten operations at N=1/10/100 or three at N=1000 (one for the expensive pinned-recall and batch-prepare cases). The event loop yields between samples, outside timing. Setup/reset, dependency installation and assertions are outside timing; full local method execution, including its filesystem I/O, is inside timing. Results include median and p95 **of sample means**, not individual-request p95. OS page caches are warm, GC is not forced, and these are single-process latency measurements rather than concurrent throughput measurements.

N varies independently by case:

- Runtime: N committed entries plus a sentinel; compaction removes the sentinel and packs N mixed-priority entries. The configured test capacity is 1 MiB per target.
- Documents: N approximately 4 KiB bodies, limit 10, with a 16 MiB test capacity. Search includes its normal last-access metadata write; catalog includes filesystem health checks.
- Holographic: N JSON facts, deterministic trust scores, fixed queries and output limits (10 for search, 20 for related). Writes include full JSON serialization and atomic replacement.
- Discovery: N Provider namespaces; native catalog: N real directories with synthetic database marker files. Provider discovery uses an in-memory registry to isolate reconciliation from persistence.
- Recall: N active spaces with 30 fixed rows each, limit 10, one normalized-score Provider type. `pinned` requests every space explicitly. The real service and registry execute, while the adapter returns local fixtures. This measures orchestration, not remote latency or retrieval quality.
- Batch preparation: N requests to one destination in an N-space registry, with an ordered synthetic batch receipt. `batch-grouping` replaces only `get()` with a constant fixture to separate grouping from directory projection. Provider writes/checkpoint persistence are excluded from these two cases.
- Core: the real three-Source composition with N short Runtime entries; Source count remains three. It is a control case, not a claim about scaling to N Sources.

N=1/10/100 can be dominated by constant overhead. N=1000 and deterministic work counters help distinguish that overhead from repeated scans. Timings do not prove Big-O; the bounds below follow from the code's loops and data structures.

## Results

Environment: Apple M4, darwin/arm64, Node v25.1.0, pnpm 11.25.0. Units: milliseconds per operation, **before → after**, medians. There are 68 cases per revision. Small differences in unchanged control cases reflect run variability.

| Case | N=1 | N=10 | N=100 | N=1000 |
|---|---:|---:|---:|---:|
| `runtime.snapshot` | 0.023 → 0.019 | 0.033 → 0.033 | 0.167 → 0.174 | 1.749 → 1.618 |
| `runtime.projection` | 0.102 → 0.103 | 0.123 → 0.119 | 0.306 → 0.311 | 2.735 → 2.456 |
| `runtime.compact` | 0.496 → 0.514 | 0.510 → 0.505 | 1.114 → 0.796 | 41.347 → 4.047 |
| `runtime.write` | 0.611 → 0.462 | 0.479 → 0.484 | 0.642 → 0.645 | 2.982 → 2.570 |
| `documents.catalog` | 0.078 → 0.081 | 0.096 → 0.107 | 0.396 → 0.393 | 4.126 → 3.358 |
| `documents.search` | 0.268 → 0.270 | 0.573 → 0.580 | 3.923 → 2.322 | 40.484 → 22.165 |
| `documents.write` | 0.434 → 0.418 | 0.485 → 0.481 | 1.159 → 1.074 | 9.252 → 7.709 |
| `holographic.search` | 0.021 → 0.019 | 0.055 → 0.054 | 0.405 → 0.376 | 4.158 → 3.577 |
| `holographic.related` | 0.017 → 0.016 | 0.048 → 0.046 | 0.343 → 0.306 | 3.746 → 3.044 |
| `holographic.write` | 0.177 → 0.164 | 0.190 → 0.185 | 0.334 → 0.341 | 2.902 → 2.063 |
| `registry.discovery` | 0.013 → 0.013 | 0.032 → 0.033 | 0.226 → 0.194 | 5.311 → 1.820 |
| `registry.native-catalog` | 0.029 → 0.028 | 0.062 → 0.064 | 0.386 → 0.354 | 7.038 → 2.925 |
| `recall.federated` | 0.018 → 0.017 | 0.128 → 0.100 | 3.045 → 0.970 | 254.008 → 9.834 |
| `recall.pinned` | 0.019 → 0.016 | 0.384 → 0.093 | 21.860 → 0.902 | 2154.052 → 9.400 |
| `storage.batch-prepare` | 0.006 → 0.007 | 0.263 → 0.035 | 18.045 → 0.223 | 1825.665 → 2.210 |
| `storage.batch-grouping` | 0.002 → 0.002 | 0.009 → 0.006 | 0.073 → 0.040 | 1.189 → 0.295 |
| `core.compose` | 11.218 → 10.682 | 11.039 → 11.239 | 12.312 → 11.634 | 11.440 → 12.355 |

Counted UTF-8 input characters during one full Runtime compaction (instrumented separately from timings):

| N | Before | After |
|---|---:|---:|
| 1 | 252 | 255 |
| 10 | 5,139 | 2,397 |
| 100 | 330,399 | 24,177 |
| 1000 | 31,641,849 | 245,577 |

From N=100 to 1000, counted work grows approximately 96× before and 10× after. The end-to-end method still performs linear JSON validation and writes.


## Complexity and decisions

Use N for records, B for their total text/serialized bytes, Q for query tokens, M for matching candidates, k for returned rows, S for configured spaces, P for requested spaces, C for total fetched candidates, W for batch writes and U for distinct write destinations. Map/Set lookup bounds are expected/amortized; string hashing and filesystem/Provider costs are additional. Metadata fields, branch count and most configured output limits are bounded.

| Path | Before | After | Reason and retained behavior |
|---|---|---|---|
| Runtime compaction | O(N²L) for N entries averaging L characters, plus JSON I/O | O(N + B) | Index original content once, preserve its first occurrence; use three stable priority buckets and incremental UTF-8/delimiter accounting. Return retained entries in original order. |
| Runtime snapshot, projection, add/replace/remove | O(N + B) per operation | Same bound | Complete JSON validation, exact matching, projection repair, capacity checks and commit-marker writes remain necessary. A sequence of N individual growing-store writes still costs O(N²) in total. |
| Document search | O(QB + M log M), plus index I/O; excerpt processing over every eligible body | Same main bound; excerpt work only over the returned bodies | Keep live body reads, substring scoring, token coverage, stable score/date ties, allowed IDs and last-access writes. No stale text index is introduced. |
| Document metadata/read/write | Catalog O(N) metadata and stats; get O(N + selected body bytes); write O(index bytes + body bytes + N stats), plus uncached excerpt bytes | Same bound | Existing disk-identity caches, locking, revisions and complete index persistence are retained. Sorting for archival candidates remains O(N log N). |
| Provider namespace reconciliation | O(SD) matching D discovered namespaces against S existing entries | O(S + D) matching | Request-local lookup preserves existing IDs, user/AI metadata, activation, timestamps and first-match behavior. Field normalization and registry serialization still process their bytes. |
| Native directory reconciliation | O(DS) ID comparisons for D disk entries | O(S + D) membership work | Still enumerate and check database existence every time; use a Set only within that scan. |
| Pinned space resolution | P full catalog projections: O(PS), excluding the old directory nested scan | One catalog and P lookups: O(S + P) | Same ID validation, active/enabled checks, deduplication and caller order. Rebuild on every operation. |
| Federated ranking + quality | O(C log C + PC) | O(C log C + C + P) | Replace per-space filtering of all evaluated/selected candidates with one aggregate pass. Preserve rank fusion, every counter, fallback policy, failure status and all policy inputs. C is normally at most 50P for conforming adapters. |
| Batch grouping/preparation | O(W²) array copying/membership plus W destination projections | O(W) grouping plus U destination resolutions | Preserve synchronous validation before any writes, destination order, Native large-entry fallback, receipt order, partial-failure behavior and per-destination activation. Total preparation still includes O(U(S + D)) directory work; committed providers add their own I/O. |
| Holographic overlap | O(a + b) work and temporary union allocation for token sets of sizes a,b | O(min(a,b)) membership work, constant auxiliary space | Use the exact Jaccard union cardinality a+b−intersection. Tokenization remains proportional to text size. |
| Holographic search/related ranking | O(M log M) full sorting | O(M log k + k log k), O(k) ranking auxiliary space | Worst-retained heap, then stable output sort. Small collections use native sort. Non-finite legacy scores retain the old full-sort path; worst-case legacy complexity therefore remains O(M log M). Candidate collection and JSON parsing still occupy O(N+B) space. |
| Holographic list/graph/status/write | Whole-store read/write O(N+B); list O(N log N); entity aggregation plus O(E log E) entity sorting | Same bound | Preserve whole-file refresh, exact deduplication, timestamps, file mode, graph identity and full persistence. Search/related improvements do not turn the JSON file into an indexed database. |
| Core/Strategies | Source facts/projections run concurrently; JSON processing traverses bytes and sorts object keys; strategy-specific source selection also contributes | Unchanged | Preserve deterministic replay, immutable Views, digests, budgets, grants, timeouts and cancellation. Default Layered composition admits three roles; arbitrary custom Strategies have their own bounds. Some generic validation/extension membership scans remain quadratic in small, bounded declaration lists. |
| Graph/list/entity federation | Graph projection O(V+E); list filtering O(returned bytes); entity merge O(E + H log H) for H unique entities, plus space resolution/Providers | Same data-processing bounds; pinned resolution improved | Keep all edges and existing namespace IDs, ordering, totals, per-source diagnostics and filters. |
| Native CLI adapter | Response normalization O(response bytes); exact batch archive O(existing + requested bytes) with Maps | Unchanged | Actual SQLite/vector/graph search complexity belongs to the external Mnemon version. Preserve readonly inspection, exact import verification, output caps and serialized process behavior. |
| Remote Provider adapters | Local decoding O(response bytes); network/service work external | Unchanged | Honcho, Mem0, Hindsight, RetainDB, ByteRover and Supermemory do not expose their server algorithms here. OpenViking additionally hydrates up to k records concurrently. Hindsight related traverses edges per depth: O(depth·E + V), with service depth capped at five. |
| Pack import/export | O(payload bytes) validation/hashing/copying plus file-name sorting | Unchanged | Transaction staging, checksums, authority checks and rollback are correctness work; no persistence format or migration change is made. |

Full global candidate sorting remains intentional: custom quality policies receive the complete sorted array and can make arbitrary selections. Truncating before policy evaluation would change results. Durable JSON reads/writes and exact arbitrary-substring matching cannot be made constant-time by replacing a loop alone. Persistent indexes, stale cross-request caches, ANN substitution, cancellation changes and altered concurrency ceilings would need separate compatibility/quality evidence.

## Compatibility and validation

- Full `pnpm run verify` passed under the CI toolchain, Node 22.19.0 + pnpm 10.13.1: 452 independent-plugin tests and 1,611 Root/Host/Core/UI tests passed; 3 plugin and 6 root optional tests skipped. This includes docs/type checks, deterministic double build, all standalone builds, real isolated Headless activation, package contents/public entries, publint and attw.
- `pnpm run release:intent` confirms changeset coverage for Root and all four changed plugins.
- Both sequential benchmark runs passed: 68 case/size combinations each. [metrics.json](./metrics.json) includes source revisions, workload parameters, median/p95 sample means, counters and identical harness SHA-256 values.
- The baseline run of the new compaction/service regression files has exactly five expected failures (linear-byte-work, two quality-stat sizes, pinned catalog count and destination resolution count); the optimized run passes all 12 tests. Additional ranking and Document/Holographic regressions pass.
- `pnpm run verify:plugins` passed: 17 independent plugin repositories, 18 packed artifacts, no workspace links, external public-SDK/Client consumer typecheck/build/tests, real DSH packed-Starter activation and all three optional packed Strategy plugins activated together.

The initial system-toolchain attempt exposed two baseline environment problems: pnpm 11.25 rejects this repository's intentional workspace task cycles, and Node 25.1.0 on this Mac fails an existing Native CLI symlink-removal fixture. The latter also fails on untouched main. The same nine CLI fixture tests pass on Node 22.19.0; complete verification uses that CI version. No source/test workaround for these environment issues is included. Benchmarks on both revisions use the same Node 25.1.0 environment, independently of the subsequent compatibility verification.

Skipped cases require external Native/OpenViking/Flash services or a Windows integration environment. Local tests do not substitute for those live integrations; GitHub CI owns the Windows and Node 20/24 jobs.

The targeted regressions cover exact UTF-8 boundaries (including supplementary characters and unpaired surrogates), priorities, stable ties, empty/full budgets, preserved timestamps/branch scope, duplicate legacy content, live Document edits and missing files, read-grant membership, disabled/unknown spaces, every quality counter, Provider failures, ordered batch receipts and validation before side effects. Heap results are compared with stable full sorting across ascending, descending, tied, mixed and non-finite scores.

Deterministic work tests bound Runtime counted characters by input size and recall-stat visits by C+k, so they do not depend on CPU speed. These tests are also run against the baseline to demonstrate that the old quadratic paths violate the bounds.

No schema, configuration, locale/UI flow, RPC DTO, package dependency version, external request payload, persistent cache, model policy or paid-service configuration changes. Existing on-disk data needs no migration; rollback remains compatible. Results validate the tested workloads and contracts, not an absolute guarantee against every production failure or a prediction of end-to-end LLM/network latency.
