# v0.5.15 release composition verification

[简体中文](./README.zh-CN.md)

The tested version commit is `d4dfa3b3`, containing merged PRs #282, #283 and #284. The environment is macOS arm64, Node 24.20.0, pnpm 10.13.1, published DSH 0.1.7-rc.2 and official Mnemon CLI 0.2.9. All seventeen local tarballs were installed into a fresh isolated profile with Scoped, Light Context and Auto Capture enabled together. Versions and SHA-256 hashes are in the [verification manifest](./verification.json). The subsequent evidence commit adds only documentation and a fixture, without changing published package contents.

## Real WebUI

The model endpoint is a deterministic loopback fixture. It reads actual inbound projections and selects real DSH tools; tools, adapters and storage are not mocked. All content is synthetic. No external model or personal memory was used.

1. Send `RELEASE515_REPLACE` in a standard-mode conversation in the temporary workspace. The real `mnemon_runtime_memory` receives `target=user` and `branches: []`, returning `success: true` and a committed receipt.
2. Send `RELEASE515_SHOW`. The next turn's actual projection contains the replacement content and `[importance=critical; created=14d; updated=0d]`.
3. Open Runtime and inspect the critical USER entry. Open and cancel its editor, reload the browser and reopen Runtime; the record remains. The public root selector matches exactly once, and the default background stays opaque.

[Minimal tool call and receipt](./wire.json) · [Browser observation](./browser.json)

![Actual USER write](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.15/user-write.png)

![Actual injected Runtime metadata](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.15/runtime-projection.png)

![Runtime record after reload](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.15/runtime-after-reload.png)

## Checks and limits

- Release composition, consumed changesets, documentation, types, deterministic builds, Headless activation/restart, package exports and contents pass. Root unpacked size is 1,374,746 bytes, below the unchanged 1,376,000-byte limit.
- Local plugin tests: 406 passed, 2 skipped. Native CLI integration and same-View two-destination archival actually ran. Sixteen independent plugin repositories, seventeen artifacts and external SDK/Client composition checks pass.
- Local Root tests: 1,379 passed, 7 skipped, 1 failed. The default composition took 5.24 seconds against the existing 5-second wall-time fence. Both compositions also exceeded the wall-time fence in the isolated rerun; CPU assertions passed. These failures are retained, and no threshold was relaxed.
- [Clean CI for the same version commit](https://github.com/omdsh-dev/dsh-mnemon/actions/runs/36221228218) passes throughout: 1,784 integrated tests passed, 11 skipped. Both performance tests completed in 1.25 seconds combined and passed the original CPU and wall-time assertions. Windows path checks, independent artifacts and PR policy checks pass.

Optional live-model, external-service and Teams/lifecycle checks retain their skip conditions. This WebUI run is macOS acceptance, not Windows WebUI or live-model quality evidence. The corresponding Issue reports retain each fix's full before/after comparison.

## Reproduction

Follow the [skin-hook setup steps](../issue-273-skin-hooks/README.md#reproduce) to prepare the official rc.2 profile and seventeen current-version tarballs, then start this directory's reusable fixture:

```sh
MNEMON_CLI_PATH=/absolute/verified/mnemon node docs/pr-assets/release-v0.5.15/serve-release.mjs \
  --profile /absolute/official-profile --artifacts /absolute/packed-artifacts \
  --state /absolute/empty-disposable-state
```

Open the WebUI using the generated private connection information and follow the steps above. Do not publish `private.json` or raw Host logs. Stop the service with Ctrl-C.
