# Sidebar icon weight

[简体中文](./README.zh-CN.md)

This record covers the initial stroke correction. The final brain-icon follow-up is recorded [separately](../sidebar-brain-icon-20260927/README.md).

Implementation: `de6cb7fd3032ba4dd800b0eb4e590b33bf4b8c1a`, based on `main` at `a16ab47f8a61bed19537a07d53ad1030f4d3934b`. The before screenshots use the existing naming preview at `364fe650644fa80d99c02cfed3c3360c4cc47a34`; its sidebar icon is identical to this main revision. The naming change is separately tracked in [PR #290](https://github.com/omdsh-dev/dsh-mnemon/pull/290), so the plugin description differs between these two previews.

## Result

The official Plugins icon uses a 1-unit stroke on a 16-unit SVG canvas. Memory System used 1.5 units, making it visibly heavier at the same size. Native, fallback and Better Sidebar icons now use 1 unit. The production change is one stroke-width value in each of the three renderers.

After merging the naming and compatibility-documentation changes into this branch, revision `37d781b1bf89781bb420528c0b1b934de88bbda3` retains `可组合记忆 (dsh-mnemon)` and both composable-memory descriptions. The [combined preview](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-combined.png) shows the current name and the corrected icon together. Its Root tarball SHA-256 is `d69c10cb453beb1e5d4c8c38ef7728e5e370f05ada74a61977f5c1c10083ec9f`. The earlier screenshots below remain a record of the original icon-only preview; their old plugin title does not represent the merge result.

The real WebUI uses published DSH `0.1.7-rc.2`, this branch's packed Root, sixteen unchanged published companion artifacts, and Mnemon CLI `0.2.9`. The Root tarball SHA-256 is `0404e788c5101b5d1c5d6d7c56904d6e78519f570eafd955f069dbbcfe5f7668`. The fixture isolates its profile, workspace and memory; no live model API is used.

[DOM measurements](./measurements.json) confirm matching stroke weights in the expanded 16px and collapsed 18px native icons. Clicking the collapsed entry opens a healthy Memory System. Runtime opens normally and remains selected after a round trip through Plugins.

| Native sidebar | Before | After |
|---|---|---|
| Expanded | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/before-expanded.png) | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-expanded.png) |
| Collapsed | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/before-collapsed.png) | [Screenshot](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-collapsed.png) |

[Runtime after the navigation round trip](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-runtime.png). Browser verification covers the native sidebar; the existing automated sidebar tests also cover fallback and Better Sidebar behavior.

## Validation

- `pnpm verify`: documentation, types, deterministic builds and all plugin suites passed. The Root suite passed 1,419 tests and skipped 9; its default-concurrency performance check took 5,311ms against a 5,000ms limit.
- `pnpm exec vitest run --dir tests --maxWorkers=2`: 1,420 passed, 9 skipped, including the unchanged performance threshold and all sidebar suites.
- `pnpm verify:headless`: 39 tools, eight representative Mnemon tools, legacy core disabling and restart passed.
- `pnpm verify:package`: packed files, public entries, types, strict package lint and export checks passed.
- `pnpm release:intent`, `pnpm verify:docs` and `git diff --check`: passed.

The bilingual UI, getting-started, configuration and operations guides were reviewed; this visual adjustment requires no workflow or configuration changes.
