# v0.5.17 release acceptance

[简体中文](./README.zh-CN.md) | **English**

This record checks the composition in [v0.5.17](../../en/releases/v0.5.17.md), based on main `c417f087bdfbc5988e0ff9b19a81ab69cff929d8`, after thirteen merged PRs. It uses the published DSH 0.1.7-rc.2, Node 24.20.0 and real Mnemon CLI 0.2.9. The disposable profile installs only the packed Starter; a loopback registry resolves all seventeen exact companion packages. No DSH source is modified. All memory contents below are synthetic.

## Functional evidence

| Check | Observed result |
|---|---|
| Composition | Layered → General through the Plugins selector; dependent component switches reflect the active strategy. Scoped, Light Context and Auto Capture are enabled together. |
| Component settings | Runtime settings open from the composition card in their own dialog. |
| Model write | Live `deepseek-flash` executes `mnemon_runtime_memory`, committing “RELEASE0517 canary: Aurora integration uses pnpm 11.” to MEMORY; USER stays empty. |
| Runtime | The saved entry appears in the Runtime page. |
| Documents | Create an active Markdown document in the WebUI, then search `staging-aurora`; the saved result is returned. |
| Native | Create and activate a Native Memory Space in the WebUI, write a synthetic fact using the real CLI, then retrieve it from the WebUI with keyword recall. |
| Restart | A cold Host restart preserves hashes of six Runtime, Documents and Memory Space metadata files. A new conversation reads pnpm 11 from its injected Runtime projection. |

[Layered composition](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/composition-layered.png) · [General composition](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/composition-general.png) · [Component settings](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/component-settings.png) · [Flash write](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/flash-runtime-write.png) · [Saved Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/runtime-persisted.png) · [Document search](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/documents-search.png) · [Native keyword recall](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/native-recall.png)

## Release blocker reproduced and fixed

The initial General-strategy Flash recall failed at the real DSH boundary: `value is not lossless JSON`. The Source correctly omitted optional Evidence fields; the Host fallback materialized `score` and `revision` as `undefined`. The fix omits absent fields and preserves supplied values, including a zero score. It applies to both named reads, recall and related, without changing Provider data or relaxing DSH validation.

The regression uses the public General strategy with real composition and Holographic Provider fixtures. Before the fix, three of four metadata combinations fail; afterwards all four pass, alongside six existing Layered Host tests. [Before the fix](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/general-recall-before.png).

The rebuilt eighteen-package composition installs in a second clean Profile with an unchanged copy of the synthetic data. The identical read-only Flash request succeeds: pnpm 11 from Runtime, and the violet gate from one Native keyword recall. All three live model requests return HTTP 200; the actual tool receipt has `isError: false`. The Status page reports v0.5.17, CLI 0.2.9, one Runtime entry, one document and one active Memory Space. [After the fix](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/general-recall-after.png) · [Final Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.17/status-final.png) · [Redacted verification and artifact hashes](./verification.json).

## Verification scope

Final `pnpm verify` passed 1,510 Root tests (6 skipped), workspace builds/type checks/tests, deterministic build, package/public-entry checks and real Headless checks. Seventeen independent plugin consumers and the complete eighteen-package install/upgrade passed. The independent-plugin checks also passed after the final Host fix, including the packed upgrade from v0.5.15.

Real Native CLI integration and all 43 Runtime-capacity tests passed with `MNEMON_EMBED_ENDPOINT=http://127.0.0.1:1`, explicitly exercising the lexical fallback. The first run using the ambient embedding endpoint timed out; semantic embedding quality and live third-party Provider services are not claimed as covered. The real Flash requests use the published DSH adapter and tool runtime; credentials, auth URLs and full prompts are kept outside the repository.

The upstream `cordis:group` display/toggle limitation remains documented. Local tarball verification is distinct from npm publication: the publish workflow must verify frozen artifacts, read back registry integrity, install the entire registry composition and upgrade from v0.5.16 before creating the GitHub Release.
