# Plugin description: layered memory

[简体中文](./README.zh-CN.md)

Tested implementation: `b65064ea`. macOS 15.6, Node 25.1.0, published DSH 0.1.7-rc.2, headless Chrome at 1280 × 860, on an isolated WebUI fixture from the Starter's defaults with its test model. No personal memory or credentials were used.

The plugin page header now describes layered memory, matching the Layered strategy selected below it.

![Plugin page header](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-description-20260928/plugin-header.png)

## Verification

- Full `pnpm run verify` and `pnpm run release:intent` passed.
- `git check-ignore` confirms the new rules: `.env` and `.env.*` files, `.cache/`, `.claude/worktrees/` and `.claude/settings.local.json` are ignored, while `.env.example` files and other `.claude/` files stay visible.
