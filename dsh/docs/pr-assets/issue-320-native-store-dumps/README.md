# Large Native Memory Spaces — issue #320

[简体中文](./README.zh-CN.md) | [Issue #320](https://github.com/omdsh-dev/dsh-mnemon/issues/320) | [Verification data](./verification.json)

With a Mnemon Native store of 900 insights, the baseline cannot list the space, draw its graph or archive runtime memory at capacity. Each of these reads the whole store, and the output passes the 2 MiB cap on every Mnemon command; the error then suggests installing Mnemon. The fixed build lists 900 memories, draws the graph and archives the working-memory entries on the first try, and both archived originals read back byte for byte.

Baseline: `dfb3196cbcea58fb9b36b7283ccac4662d366858` (main, with Memory Spaces 0.5.13 and Mnemon Native 0.5.7 as published in 0.5.20). Fix: `dd7289d57dddec408b60c11ccb3aed1b2409f0f1`. Only these two plugins differ between the runs.

The 2026-10-01 runs use macOS 15.6 arm64, Node 24.19.0, pnpm 11.19.0, the repository's DSH 0.1.7-rc.2 and Mnemon CLI 0.2.9. Each run owns a disposable `DSH_HOME`, data directory, workspace and loopback model stub. No model is called and no personal memory is read.

## Setup

```sh
pnpm build
pnpm --workspace-concurrency=4 -r build
node docs/pr-assets/issue-320-native-store-dumps/large-store-draft.mjs /tmp/i320-draft.json
mnemon --data-dir /tmp/i320-store --store default import /tmp/i320-draft.json --no-diff
MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve --runtime-write-scope
```

The import takes several minutes. Before opening the WebUI, copy `/tmp/i320-store/data/default/mnemon.db` to `<fixture>/data/data/default/mnemon.db`, where `<fixture>` is the path the server prints at startup. Memory Spaces discovers it as the `default` space. `--runtime-write-scope` limits working memory to 512 bytes and archives to Mnemon Native.

The [draft generator](./large-store-draft.mjs) writes 900 synthetic insights of about 3.3 KB each; the imported store has 14,956 edges. Whole-store commands on it exceed the 2,097,152-byte cap:

| Command | Output bytes | Used by |
|---|---:|---|
| `recall "" --basic --limit 100000` | 3,248,516 | Contents list; exact-content check before archiving |
| `viz --format html --output -` | 5,687,735 | Graph |
| `search <query> --limit 10` | 34,545 | Search |
| `status` | 1,652 | Space card and Status |

Both runs take the same steps: the Memory Spaces overview and contents, then **Runtime Memory → Add memory** three times. Entries A (200 B) and B (210 B) bring working memory to 414 of 512 B, so entry C (205 B) needs capacity maintenance. With one eligible space, the Host routes the archive without a model.

## Baseline

| Graph | Contents |
|---|---|
| ![The space's graph reads connection unavailable](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-320-native-store-dumps/before-graph.jpg) | ![Contents reads 0 memories](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-320-native-store-dumps/before-contents.jpg) |

The space card reads 900 memories because `status` is small. The graph section reads "connection unavailable" and says the space supports only on-demand queries. Contents reads 0 memories and suggests saving the first one.

Adding entry C fails and the dialog stays open:

```text
mnemon output exceeded 2097152 bytes. Install Mnemon and ensure "mnemon" is on PATH, or set MNEMON_CLI_PATH or mnemon.cliPath.
```

![Adding C fails with the install hint](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-320-native-store-dumps/before-capacity-error.jpg)

Nothing is written: the store keeps 900 insights and working memory keeps A and B. Every later add that needs room fails the same way, so working memory stays full.

## Cause and fix

Mnemon Native reads the whole store in three places. Its contents list and the exact-content check before runtime memory archives both run `recall "" --basic --limit 100000`, and its graph runs `viz --format html --output -`. The Memory Spaces process runner stops a Mnemon command after 2 MiB of output and did not forward a per-call cap. Its error handler added the install hint to every failure.

- Memory Spaces forwards `maxOutputBytes` to the process, and the Provider SDK's `MemorySpaceNativeRunOptions` carries it. Source hosts before this change ignore it.
- Mnemon Native allows 128 MiB for the two whole-store reads (`STORE_DUMP_MAX_OUTPUT_BYTES`). Every other command keeps 2 MiB.
- Process failures carry a reason. Only a failed launch adds the install hint; an oversized output reads, for example, `mnemon recall stopped: output exceeded 2097152 bytes`.

The 2 MiB constant in the root package's `lib/index.js`, which the issue cites, belongs to plugin installation and version checks. It never runs Mnemon, so it is unchanged.

## Fixed build

| Graph | Contents |
|---|---|
| ![The graph of 900 memories](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-320-native-store-dumps/after-graph.jpg) | ![Contents lists 900 memories](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-320-native-store-dumps/after-contents.jpg) |

The graph section reads 900 observable memories and 14,956 relations and draws the live snapshot. Contents lists 900 memories.

Adding entry C succeeds on the first try: "容量整理完成：已先归档到记忆空间 default，再更新工作记忆" (capacity maintenance finished: archived to the `default` space, then updated working memory). Working memory holds only C at 205 of 512 B, and the space reads 902 memories and 14,958 relations.

| Capacity maintenance | The space after archiving |
|---|---|
| ![Capacity maintenance archives to default](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-320-native-store-dumps/after-capacity-archived.jpg) | ![The space reads 902 memories](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-320-native-store-dumps/after-archived-space.jpg) |

Read back with the Mnemon CLI in `--readonly` mode, the store holds 902 insights, and A and B equal the added entries as exact UTF-8 strings. `runtime/memories.json` holds only C, also exact. Neither run logged a console error. The [verification data](./verification.json) records the counts, page text, entry hashes and screenshot hashes.

A direct Provider check with the real CLI covers the graph limit separately. On a store of 1,000 short insights, `recall ""` returns 782,779 bytes and the graph 5,148,040 bytes. The fixed Provider lists 1,000 insights, draws 1,000 nodes and 26,486 edges, and archives through `rememberMany`.

## Automated checks

- `pnpm run verify` passes. It covers docs links, typecheck, the deterministic build, build, typecheck and tests for all 17 plugins, and root tests (113 files, 1,549 passed, 6 skipped). It also covers Headless activation, package contents, public entries, publint and attw.
- Memory Spaces: 17 files pass and 2 are skipped (191 tests). New cases cover:
  - each failure reason (launch, timeout, cancellation, output limit);
  - a per-call cap above the default;
  - cap forwarding in `runJson`, `runText` and `runTextBatch`;
  - messages that add the install hint only after a failed launch.
- Mnemon Native: 9 tests pass. The 128 MiB cap applies to `list`, `graph` and the `rememberMany` snapshot, and never to import, search or status.

## Limits

- Windows was not run. The reporter measured on Windows 11 with the same Mnemon 0.2.9, and the cap does not depend on the platform.
- Both runs use the repository's DSH 0.1.7-rc.2. The change stays inside the two plugins and no DSH contract changes.
- Output beyond 128 MiB still stops, now with a message that names the cause.
- The screenshots contain synthetic content only.
