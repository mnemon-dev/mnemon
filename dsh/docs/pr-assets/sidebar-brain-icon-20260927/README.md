# Memory System brain icon

[简体中文](./README.zh-CN.md)

Tested implementation: `d02f1502d05c49784452b502c2e3946748d990b0`. The branch includes merged PRs #290 and #291, retaining `可组合记忆 (dsh-mnemon)` and both composable-memory descriptions.

Memory System now uses an original classic brain outline. Native, fallback and Better Sidebar entries share the same SVG paths, a 16-unit canvas, 1-unit strokes and `currentColor`. Native DSH continues to own button sizing, labels and selected state.

| Before | After |
|---|---|
| [Database icon with the current name](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-combined.png) | [Brain icon with the current name](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-brain-icon-20260927/expanded.png) |

The [collapsed entry](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-brain-icon-20260927/collapsed.png) opens a healthy Memory System. Expanded/collapsed navigation and the return to Plugins were exercised in the real WebUI. The plugin list still displays `可组合记忆 (dsh-mnemon)`.

Environment: macOS, Node 24.20.0, pnpm 11.19.0, published DSH `0.1.7-rc.2`, Mnemon CLI `0.2.9`, this branch's Root tarball and sixteen unchanged companion artifacts. Root SHA-256: `3f0d0008471f565abd9d9dcb415df3ba8fb46b3f28d030ffa6ab8f6f7a0094cc`. The profile, workspace and memory are disposable; no live model API was used.

Validation: `pnpm build`, `pnpm typecheck`, all 40 tests across the four sidebar suites, `pnpm exec vitest run --dir tests --maxWorkers=2` (1,420 passed, 9 skipped), `pnpm verify:package`, `pnpm release:intent`, `pnpm verify:docs`, and `git diff --check` passed. Browser evidence covers native DSH; existing automated tests cover fallback and Better Sidebar registration and behavior. No storage, configuration, permission or wire contracts change.
