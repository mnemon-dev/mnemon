# DSH 0.1.5 compatibility — issue 223

[简体中文](./README.zh-CN.md) | [Issue 223](https://github.com/omdsh-dev/dsh-mnemon/issues/223)

Verification record for the isolated `codex/fix-223-dsh-015` worktree based on main `1790251919ec731f9dd49b43da78f78b37890a00`. The tested DSH release is the published npm `latest`, `0.1.5-rc.1`; no DSH source is modified.

The main build reproduced HTTP 405 in the real WebUI. With the Starter connection dependency patch, all seven Mnemon RPC channels register and Status loads. Lifecycle messages omit invalid summary metadata. A synthetic conversation emitted by the published 0.1.2 Agent loop reproduces the official migration refusal; the repair command preserves the original, then a deliberately installed copy migrates to v3 and cold-reopens under the published 0.1.5 persistence backend, for JSONL and Zstandard.

The reusable fixture is [issue-223-legacy-v0.jsonl](../../../tests/fixtures/issue-223-legacy-v0.jsonl). It contains only a synthetic user request, the two historical Mnemon pre-step messages and a fixed assistant response. [Regression tests](../../../tests/legacy-session-repair.spec.ts) check byte preservation, exclusive output publication, unrelated metadata, malformed input, frame truncation, bounds, and official migration. See [recovery instructions](../../en/guides/operations.md#dsh-015-compatibility-and-legacy-session-recovery).


## Result

Command entrypoint follow-up: `a389438a4e12a77f953c80ce79828e221653ee8f`. The final full local verification passes at that revision, including a regression that invokes the npm-style executable symlink for help and repair preview. The WebUI captures below cover the unchanged Runtime/UI implementation.

Implementation: `595e197670574f6a731e445b896dbc5c78271027`; base: `1790251919ec731f9dd49b43da78f78b37890a00`. Captured on 2026-09-10 with Node 25.1.0, pnpm 11.19.0, Playwright 1.63.0 / Chromium 153, macOS arm64, and Mnemon Native 0.2.8. The UI shows package version 0.5.6 because the changeset has not yet performed the release version bump. [Machine-readable results and screenshot hashes](./verification.json).

| Check | Result |
|---|---|
| Frozen install and `pnpm verify` | Passed: 1194 tests, 7 opt-in skips; types, deterministic build, docs, Headless, strict package checks |
| `pnpm verify:plugins` | Passed: 16 standalone plugin repositories, 17 packed artifacts, external SDK/Client consumer, real DSH packed upgrade and optional Strategy composition |
| Real Native integration | Passed: 1 opt-in test, disposable write/recall/delete |
| Repair regression | Passed: 17 tests including real v0 → v3 loaders, both encodings, cold reopen, byte preservation, exclusive output, checksum/truncation and rejection paths |
| Node 20 repair preview | Passed on the same v0 JSONL fixture; 2 repairs, original preserved |
| DSH 0.1.2-rc.1 Headless | Passed with the current plugin: 39 tools / 8 representative Mnemon tools, settings migration, restart, disable whole Starter |
| DSH 0.1.5-rc.1 Headless | Passed: 38 tools / 8 representative Mnemon tools, the same restart and disable checks |
| Browser authorization | Cookie-authenticated pages work; unauthenticated status RPC returns 401; read-only Runtime and Save-to-memory controls reject writes |

The real WebUI sequence used only disposable data:

1. Open Memory System without a session, then Settings: the main build fails with HTTP 405; the fixed build loads Status, the Source catalog and Provider settings.
2. Create USER and MEMORY entries, edit Working Memory, filter it, and verify the exact text. Create a disposable entry in Builtin, cancel removal, then remove it after restoring write access; the original two entries remain.
3. Select the disposable workspace and create a real DSH conversation through the Mnemon E2E preset. Create and search a Document, read its body, then read it again after restart.
4. Create a Native Memory Space through the UI. Seed one synthetic fact with the real Native CLI, activate the space in the UI, run keyword Recall, inspect Entities and Content, restart and read the same fact. Cancel Forget, then confirm soft deletion and verify Content is empty. This is a CLI-seeded read/delete check, not an AI-distillation quality claim.
5. Save Sidebar → Builtin and Builtin → Sidebar through Settings; verify exactly one entry is mounted and both display the same data. Check English and Chinese settings. Export a backup ZIP through the real pack RPC.
6. Restart with `writeEnabled: false`: Runtime has no mutation controls and the conversation Save-to-memory confirmation stays disabled. Restore write access only in the disposable fixture.
7. Use the copy-repair command on a compressed v0 Session, install that copy in the disposable Session root, load its original user/assistant history, send a second turn, restart DSH, and cold-open both turns. The final log contains 30 events, 6 user messages, and 3 valid Mnemon context messages without summary. The original backup SHA-256 is unchanged.

For the WebUI legacy scenario only, the generated fixture header was assigned the disposable workspace and `mnemon-e2e` preset, and the synthetic model route was mapped to the loopback provider. Those are fixture setup changes; the repair command only removes the two known summary properties. DSH itself publishes and validates the v3 successor.

## Screenshots

| Evidence | Image |
|---|---|
| Main reproduces both HTTP 405 errors | [Before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/00-before-http-405.png) |
| No-session Status connects | [Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/01-status-connected.png) |
| Runtime create/edit result | [Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/02-runtime-crud.png) |
| Document creation and search | [Documents](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/03-documents-search.png) |
| Native keyword Recall | [Recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/04-native-recall.png) |
| Settings save and connected catalog | [Settings](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/05-settings-connected.png) |
| Builtin uses the same Runtime data | [Builtin](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/06-builtin-runtime.png) |
| Read-only mode after restart | [Read-only](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/07-readonly-restart.png) |
| Chinese Settings | [中文](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/10-chinese-settings.png) |
| Repaired legacy conversation after resume and restart | [Cold reopen](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/11-legacy-cold-reopen.png) |
| Native content retained through restart, before soft deletion | [Content](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/issue-223-dsh-015/12-content-after-restart.png) |

## Reproduction and limits

Run `pnpm install --frozen-lockfile`, `pnpm verify`, and `pnpm verify:plugins`. For the browser, build first, then run `MNEMON_CLI_PATH=/absolute/path/to/mnemon pnpm e2e:serve`; select the printed disposable workspace and Mnemon E2E. The fixture uses the current persona `prefix` and disables Open-in-App together with its disabled subprocess dependency. Use the checked-in legacy fixture with `node bin/repair-legacy-session.mjs --input tests/fixtures/issue-223-legacy-v0.jsonl` for a non-writing preview. The real loader regression creates and cleans its own storage roots.

This verifies published DSH APIs and desktop WebUI behavior. It does not retest Windows, Electron, physical mobile devices, live third-party accounts, model quality, or every historical DSH release. DSH 0.1.2 received Headless regression only. Older peer ranges remain, but historical reports keep their own revisions. The copy utility deliberately refuses unrelated corruption and does not overwrite live Sessions. DSH v3 cannot be treated as an old-version rollback artifact.

Sources: [official DSH 0.1.5-rc.1 release](https://github.com/deepseek-ai/deepseek-harness/releases/tag/dsh-v0.1.5-rc.1), [Node Zstandard APIs](https://nodejs.org/api/zlib.html#zlibzstddecompresssyncbuffer-options), [Zstandard framing, RFC 8878](https://www.rfc-editor.org/rfc/rfc8878.html#section-3.1.1). CI results belong to the final PR revision and are linked from the PR, separately from this local capture.
