# v0.5.7 release verification

**English** | [简体中文](./README.zh-CN.md)

Tested `f237e9a1b195c879472713fac160d965c7b0d522`, the versioned release tree based on merged main `accabbd3ed4cd2e7fd0e2c6d399b4fe7d04a7122`, on 2026-09-10. Environment and screenshot hashes are in [verification.json](./verification.json).

`pnpm verify` passed 1,218 tests (895 root, 323 plugin), deterministic builds, type checks, real Headless activation/restart and package validation. Seven opt-in tests were skipped: five live-model checks, Native integration and the Windows binary smoke test. `node scripts/verify-plugin-artifacts.mjs --skip-build` verified 16 independent repositories and 17 tarballs, public SDK/Client consumers and real DSH Starter/optional-plugin activation. `release:intent` and `release:check` selected exactly four changed packages: Starter 0.5.7 and the three Sources at 0.5.6.

Run `pnpm e2e:serve --document-archive` after the builds. In the real browser, check no-session Status, bind the disposable workspace with the Mnemon E2E preset, create and activate a Native Memory Space, and create a synthetic document. Archive it through the confirmation dialog, reload the browser, reopen the archived original, then inspect the Native index in Memory Spaces → Content. The original content and its SHA-256 were preserved; the persisted document is revision 2 with status `archived`. The archive step recorded no browser page errors or failing Mnemon HTTP responses.

| Screenshot | Observation |
|---|---|
| [Version and Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.7/01-version-status.png) | Actual 0.5.7, connected RPC, no live Session required |
| [Archived original](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.7/02-document-archived.png) | Original and Host receipt remain readable after reload |
| [Index after reload](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.7/03-index-after-reload.png) | One Native index with the cold reference and matching content hash |

The model only scripts archive decisions; the browser, DSH transport, Host, storage and Native CLI remain real. All test content is synthetic and the disposable Profile/browser are removed afterward. This release smoke does not repeat the full historical browser matrix, Windows/Electron checks or live model-quality experiments. The [DSH compatibility report](../issue-223-dsh-015/README.md) and [archive regression report](../issue-222-document-archive/README.md) preserve their original revisions and broader coverage.
