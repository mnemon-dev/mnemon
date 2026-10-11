# Composable memory naming

[简体中文](./README.zh-CN.md)

Tested implementation: `6227ed41afdba1212409e7b02dc25e184851a122`, based on `a16ab47f8a61bed19537a07d53ad1030f4d3934b` (v0.5.16). macOS, Node 24.20.0, pnpm 11.19.0, published DSH 0.1.7-rc.2 and Mnemon CLI 0.2.9. The temporary Web profile installed this branch's Root tarball with the sixteen published companion packages from the v0.5.16 composition. The Root still carries 0.5.16 for testing; the changeset requests a subsequent patch release.

## Visible result

The Chinese plugin title is `可组合记忆 (dsh-mnemon)`. English retains the package name and uses this description:

> Composable, view-based memory for DeepSeek Harness. Pluggable sources and strategies, with three-tier memory out of the box.

The installed artifact was opened in the real Plugins detail page, then switched between Chinese and English through DSH Settings. Both the bundle heading and Root component row showed the new copy. DSH's published `readPluginMeta()` also returned the same strings from that installation; [metadata.json](./metadata.json) includes the tested tarball hash.

| Language | Before | This implementation |
|---|---|---|
| Chinese | [PR #286 evidence](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/pr-286-plugin-metadata/plugins-zh.jpg) | [Composable memory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composable-naming-20260927/plugins-zh.png) |
| English | [PR #286 evidence](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/pr-286-plugin-metadata/plugins-en.jpg) | [Updated description](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composable-naming-20260927/plugins-en.png) |

The screenshots retain the official DSH group-list defect tracked in [dsh-external/issues#649](https://github.com/dsh-external/issues/issues/649). This copy change does not include a plugin-manager replacement.

## Validation

- `pnpm verify`: docs, types, deterministic builds and all plugin tests passed. The Root phase had two wall-clock performance failures at its default concurrency; all other Root tests passed. Thresholds were not changed.
- `pnpm exec vitest run --dir tests --maxWorkers=2`: all 1,420 tests passed, with 9 optional/environment skips, including both performance checks.
- `pnpm verify:headless` and `pnpm verify:package`: passed, including restart, legacy disable behavior, public entries, types and package lint.
- `pnpm verify:plugins --skip-build`: sixteen standalone plugin projects and seventeen packed artifacts passed, including the updated external metadata assertion and real DSH installation/composition.
- `pnpm release:intent`: passed for the Root patch changeset. `mnemon --version`: 0.2.9.

No live model or external Provider API was needed. This record verifies metadata presentation and packaging, not new memory behavior.
