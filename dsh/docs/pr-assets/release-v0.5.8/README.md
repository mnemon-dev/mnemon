# v0.5.8 release verification

[简体中文](./README.zh-CN.md) | **English**

Versioned application revision: `099d39f8fabf8d118b87455b4399f80eb2f98f7b`, based on merged main `ae710d3a552df76739b8ada36240e9176f2e4726`. This record and its screenshots are added afterward without changing application code.

The release advances only Starter to 0.5.8 and consumes six patch changesets. All sixteen official plugin versions are unchanged. The frozen release plan selects exactly `dsh-mnemon@0.5.8`.

Validation on macOS arm64, Node 25.1.0 and pnpm 11.19.0:

- `pnpm run verify`: 1,301 passed (978 root and 323 plugin tests); seven opt-in checks skipped. Types, deterministic builds, real Headless activation/restart and package validation passed.
- `node scripts/verify-plugin-artifacts.mjs --skip-build`: sixteen independent repositories, seventeen packed artifacts, public SDK/Client consumers, packed Starter and all three optional Strategy plugins passed.
- Real Mnemon CLI 0.2.7 integration: create, write, recall and forget passed in an isolated store.
- Release-intent consumption and versioned release selection passed against the preceding v0.5.7 release.
- Package: 47 files, 284,282 packed bytes, 1,278,996 unpacked bytes, within the 1,280,000-byte limit.

Real browser verification used DSH 0.1.5-rc.1 with the Starter and three Strategy enhancements enabled. The authenticated, session-free status displayed 0.5.8. A synthetic Runtime record remained visible after a browser reload. A Native Memory Space was created and activated; Status showed Mnemon CLI 0.2.7 connected with 1/1 space active. Both screenshots were visually reviewed. The disposable Profile, process and browser tab were removed after verification.

| Runtime after reload | Version and Native status |
| --- | --- |
| [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.8/01-runtime-after-reload.jpg) | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/release-v0.5.8/02-version-native-status.jpg) |

No external model API was called. These checks validate application and plugin behavior; they do not measure model quality, cache hit rates or historical token consumption. Earlier issue reports retain their original reproduction revisions.

