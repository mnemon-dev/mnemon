# v0.5.18 release acceptance

[中文](README.zh-CN.md)

Verified on 2026-09-28, after merging #308 on top of #306. The versioned candidate is `dsh-mnemon@0.5.18` with `dsh-mnemon-source-memory-spaces@0.5.12`; the other 16 component packages come from npm at the Starter's pinned versions.

## Real official WebUI

Using unmodified npm DSH `0.1.7-rc.2`, Node `24.20.0` and a fresh isolated profile:

1. Started the host without Mnemon installed.
2. Installed the versioned candidate through **Add plugin**. The loopback registry served only the two changed candidate tarballs; unchanged packages came from the official npm registry.
3. Clicked **Enable now** and opened Memory System. The page reports **dsh-mnemon 0.5.18 / System nominal** without a host restart.
4. Created and read the `RELEASE_0518_ACCEPTANCE` Runtime entry through the WebUI.
5. Created and activated a Native space through the WebUI, wrote `RELEASE_0518_NATIVE` with Mnemon CLI `0.2.9`, recalled it with the CLI, then retrieved the same fact using the official WebUI's basic recall.

[Artifact hashes, installed versions and unchanged host PID](validation.json) identify this candidate. Profiles, paths and memories are synthetic. No remote model or third-party Provider was needed for these installation checks.

![Versioned Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.18/status.png)

[Runtime write/read](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.18/runtime.png)

![Native CLI memory retrieved by the official WebUI](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.18/recall.png)

## Validation and release boundary

`pnpm run release:check` selects only the Starter and Memory Spaces Source for publication. `pnpm run verify:package` passes package content checks, 13 public Node entry imports, type declarations, `publint` and `attw`.

The release PR and publication workflow run complete workspace and packed-plugin verification. Publication freezes the merged main revision, publishes the changed Source before the Starter, reads back the npm artifacts, installs the complete 18-package combination and checks a real Registry upgrade before creating the GitHub release. These screenshots show the versioned local candidate; they do not by themselves certify npm publication.

The [activation regression evidence](../desktop-live-activation/README.md) additionally covers Documents, bundle disable/re-enable with retained data, isolated Desktop generation dependencies and real Electron CLI execution. [Issue #305 evidence](../issue-305-desktop-console/README.md) covers the npm Native launcher and its verification limits.
