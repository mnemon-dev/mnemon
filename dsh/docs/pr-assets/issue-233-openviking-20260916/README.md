# OpenViking exact writes — issue #233

[简体中文](./README.zh-CN.md) | [Issue #233](https://github.com/omdsh-dev/dsh-mnemon/issues/233) | [Original contribution #234](https://github.com/omdsh-dev/dsh-mnemon/pull/234) | [Verification data](./verification.json)

The baseline can report a stored memory after an extraction task that saves none of the supplied text. The fixed Provider creates the original content, verifies its index status and reads it back before returning a stored receipt. A controlled HTTP fixture reproduces the false receipt through the real WebUI and Host; a separate published OpenViking v0.4.20 service verifies actual storage, search and deletion.

Baseline: `1363ebffaf19c9ab4badf0137f6fe87acacaf989`. Tested implementation and reusable fixtures: `11c5534cb916351f6f2ad22cd6d0dc22b8237e1b`. Izgenlre's original commit `bf83a4240f505e22713f20393d9b5cfa97fda05f` remains an ancestor, integrated by merge `2c5de91f0c9864de2d1f1918c4377dba7ea35a80`; it was not rewritten or squashed.

The 2026-09-16 environment uses macOS arm64, Node 25.1.0, pnpm 11.19.0, DSH 0.1.5-rc.1 and official Native CLI 0.2.8. Browser runs use disposable Host state and synthetic content. Model decisions are scripted; actual Host tools, the delegated writer, Source/Provider composition, transport and WebUI run normally. No external model API or personal memory is used.

## Baseline and controlled comparison

The [regression test](../../../plugins/dsh-mnemon-provider-openviking/tests/write-regression.spec.ts) failed on main before the contributor branch was merged:

```text
receipt.action: stored
expected persisted files: ["Release approvals require a completed canary.\n保留原文和换行。"]
actual persisted files: []
```

The contributor change saved the body but still failed the next assertion because its receipt omitted `id`, which the Host needs for lineage and compensation. Both failures were retained before completing the fix.

For the browser comparison, start the [protocol fixture](../../../scripts/fixtures/openviking-protocol.mjs), then the [scripted model](../../../scripts/fixtures/openviking-write-model.mjs) with the selected product revision:

```sh
node scripts/fixtures/openviking-protocol.mjs
MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve --openviking-write --strategy-extensions
```

The browser runs use official Native CLI 0.2.8 and enable Scoped, Light context and Active capture together. Configure OpenViking at `http://127.0.0.1:19335`, account/user `default`, no API key, and send `openviking-write-233` in Mnemon E2E. Only fixture code is copied into the baseline checkout. The model selects the unique enabled default OpenViking namespace and drives its real delegated writer once. The fixture deliberately returns a cosmetic extraction update without storing the candidate; this is controlled protocol evidence, not a claim about live LLM extraction.

| Same running HTTP fixture | Baseline | Fixed |
|---|---:|---:|
| Session creations / commits | 1 / 1 | 0 / 0 |
| Direct content writes | 0 | 1 |
| Persisted memories | 0 | 1 |
| Exact UTF-8 body | absent | 117 bytes |
| Writes to the other owner | 0 | 0 |

The baseline displays `stored`, one extracted update and a committed Host receipt while the Content tab is empty. The fixed build sends `mode: create`, `wait: true`, performs exact readback and displays the original bilingual body. Counts for the fixed run exclude the baseline requests already retained by the same fixture.

| Baseline stored receipt | Baseline empty content |
|---|---|
| ![Baseline reports a stored memory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-233-openviking-20260916/before-stored-receipt.png) | ![Baseline contains no memory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-233-openviking-20260916/before-empty-content.png) |

![Fixed content appears in the intended namespace](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-233-openviking-20260916/after-protocol-content.png)

## Real backend acceptance

The separate service at `http://127.0.0.1:19333` uses the published `openviking==0.4.20` Python wheel and Python 3.12.13. Its audited source tag resolves to `b54001e2e5c974ffd7a09ba543813fa104a99561`. Storage, HTTP handling and vector indexing are real OpenViking. A local 128-dimensional token-hash embedding endpoint makes the test deterministic; chat-model requests fail explicitly. Authentication uses an isolated account-admin key omitted from this record.

The [opt-in integration test](../../../plugins/dsh-mnemon-provider-openviking/tests/integration.spec.ts) creates a unique 137-byte bilingual memory, reads the exact original, finds it through search and browsing, deletes its exact URI, then confirms its absence. It passes against the authenticated server. The recorded receipt contains the matching `id`/`uri`, `writtenBytes: 137` and `vectorStatus: complete`; no session extraction endpoint is called.

```sh
# Supply an account-admin key through the environment when required.
MNEMON_OPENVIKING_TEST_ENDPOINT=http://127.0.0.1:19333 \
  pnpm --filter dsh-mnemon-provider-openviking exec vitest run tests/integration.spec.ts
```

The service must be disposable. The test accepts loopback endpoints only and removes its own canary. On v0.4.20, namespace discovery needs an explicit account and an account-admin key; a ROOT bootstrap key cannot access tenant content, and unauthenticated dev mode rejects admin discovery.

The fixed WebUI was then switched to this real service through its ordinary settings and discovery flow. The same tool command stored the 117-byte browser canary with a committed Host receipt, and an independent HTTP read matched it byte-for-byte. The Content page displayed it in `default`; direct search for `release approval staged rollout` returned the full original. The synthetic embedding score of 0.707 is recorded only as observed output. Clicking Forget and confirming removed it; a separate HTTP read returned `404 NOT_FOUND`. The compact [WebUI readback record](./real-webui-readback.json) includes the exact receipt and deletion result.

| Real OpenViking content | Real OpenViking direct search |
|---|---|
| ![Actual backend contains the full original](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-233-openviking-20260916/after-real-content.png) | ![Direct search returns the full original](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-233-openviking-20260916/after-real-search.png) |

![Confirmed deletion leaves the real namespace empty](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-233-openviking-20260916/after-real-forget.png)

## Contract and failure handling

The implementation was checked against the official [content writer](https://github.com/volcengine/OpenViking/blob/v0.4.20/openviking/storage/content_write.py), [HTTP content routes](https://github.com/volcengine/OpenViking/blob/v0.4.20/openviking/server/routers/content.py) and [namespace rules](https://github.com/volcengine/OpenViking/blob/v0.4.20/openviking/core/namespace.py). A successful HTTP response alone is insufficient: the write must report the requested URI, mode, memory type, updated content, correct byte count, completed vector status, and completed or skipped semantic status without queue errors. Public content readback must then equal the input exactly. Search and browse likewise hydrate full public content.

New files use UUID identities and `create`, so collisions cannot replace existing memory. The Provider validates owner roots, discovered owner IDs and exact deletion scope. The persisted shorthand `viking://user/memories` resolves to the configured or authenticated owner without rewriting settings. A failed, pending, canceled or uncertain write produces an error naming the candidate URI; it is not retried or automatically deleted. Older servers without the required write/read contract fail explicitly. Existing content is retained on downgrade.

The [Host composition tests](../../../tests/openviking-host.spec.mjs) exercise separate OpenViking instances alongside Native, Holographic, Runtime and Documents. They verify exact receipts and full reads, scoped writes/deletes, archive lineage, compensation of only the newly created index after a local archive conflict, and retention of original documents when indexing fails.

## Validation and limits

- `pnpm install --frozen-lockfile` passes in the isolated worktree.
- `MNEMON_NATIVE_TEST_CLI=/absolute/path/to/mnemon pnpm run verify` passes: root 1,134 tests / 5 opt-in skips; OpenViking 49 / 1 opt-in skip; Memory Spaces 169 / 1 Windows-only skip. Documentation, types, deterministic builds, plugin suites, real Native integration, real Headless activation and package checks pass.
- `pnpm run verify:plugins` passes for 16 independently installed plugins and 17 packed artifacts, including public SDK composition and actual DSH activation.
- Regression cases cover malformed receipts, missing/incomplete/failed indexing, readback mismatch, HTTP errors, timeout/cancellation, unsafe roots/IDs, inherited object-property category names, account isolation and rollback. The separate real OpenViking integration passes; ordinary CI intentionally skips it.
- The changeset covers the aggregate Host, OpenViking Provider and Memory Spaces Source. Release-intent validation passes.

The deterministic backend model validates storage and index contracts, not extraction quality or semantic recall quality. Live Flash, Windows and Electron were not exercised. This record contains only synthetic screenshots, compact counts and hashes; credentials, full sessions and personal data are excluded.
