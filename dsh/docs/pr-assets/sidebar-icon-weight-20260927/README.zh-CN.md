# 侧栏图标线宽

[English](./README.md)

本记录对应最初的线宽调整；后续最终采用的大脑图标见[独立验收记录](../sidebar-brain-icon-20260927/README.zh-CN.md)。

实现提交：`de6cb7fd3032ba4dd800b0eb4e590b33bf4b8c1a`，基于最新 `main` 的 `a16ab47f8a61bed19537a07d53ad1030f4d3934b`。修复前截图来自已有命名预览 `364fe650644fa80d99c02cfed3c3360c4cc47a34`，其侧栏图标与该 main 完全相同。命名调整由独立的 [PR #290](https://github.com/omdsh-dev/dsh-mnemon/pull/290) 跟踪，因此两组预览中的插件描述不同。

## 结果

官方“插件”图标在 16 单位 SVG 画布上使用 1 单位描边；记忆系统使用 1.5 单位，同尺寸下明显更粗。原生侧栏、备用入口和 Better Sidebar 现统一为 1 单位。生产代码仅调整三个渲染位置各自的线宽值。

本分支合入命名调整和兼容性文档后的提交 `37d781b1bf89781bb420528c0b1b934de88bbda3` 完整保留“可组合记忆 (dsh-mnemon)”及中英文可组合记忆描述。[当前组合预览](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-combined.png)同时展示新名称和修正后的图标，Root 制品 SHA-256 为 `d69c10cb453beb1e5d4c8c38ef7728e5e370f05ada74a61977f5c1c10083ec9f`。下方旧截图保留图标单独修复时的原始记录；其中的旧插件名称不代表合并结果。

真实 WebUI 使用正式 DSH `0.1.7-rc.2`、本分支打包的 Root、十六个未修改的正式伴随插件制品，以及 Mnemon CLI `0.2.9`。Root 制品 SHA-256：`0404e788c5101b5d1c5d6d7c56904d6e78519f570eafd955f069dbbcfe5f7668`。夹具隔离 profile、工作区和记忆目录，不调用真实模型 API。

[DOM 实测](./measurements.json)确认展开时 16px、折叠时 18px 的原生图标均与官方入口线宽一致。点击折叠图标可打开状态正常的记忆系统；运行时页面正常打开，往返“插件”页后仍保留当前页面。

| 原生侧栏 | 修复前 | 修复后 |
|---|---|---|
| 展开 | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/before-expanded.png) | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-expanded.png) |
| 折叠 | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/before-collapsed.png) | [截图](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-collapsed.png) |

[往返导航后的运行时页面](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/sidebar-icon-weight-20260927/after-runtime.png)。浏览器验收覆盖原生侧栏；已有侧栏自动化测试同时覆盖备用入口和 Better Sidebar 行为。

## 验证

- `pnpm verify`：文档、类型、确定性构建与全部插件测试通过。Root 测试通过 1,419 项、跳过 9 项；默认并发下的一项性能检查用时 5,311ms，超过 5,000ms 阈值。
- `pnpm exec vitest run --dir tests --maxWorkers=2`：1,420 项通过、9 项跳过，包含未调整阈值的性能检查和全部侧栏测试。
- `pnpm verify:headless`：39 个工具、8 个代表性 Mnemon 工具、旧版核心停用及重启验证通过。
- `pnpm verify:package`：制品文件、公开入口、类型、严格包校验及导出检查通过。
- `pnpm release:intent`、`pnpm verify:docs` 和 `git diff --check`：通过。

已复核中英文 UI、快速开始、配置和运维指南；此视觉调整无需修改使用流程或配置说明。
