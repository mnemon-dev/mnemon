# Memory space terminology UI evidence

This is the pre-release terminology evidence. Current Light screenshots and bilingual recordings using imported data are in [the v0.5.4 gallery](../webui-v0.5.4/README.md).

本目录是版本化前的用语改动证据。当前浅色截图与双语录制已移至 [v0.5.4 图集](../webui-v0.5.4/README.md)，使用备份导入数据。

Captured on 2026-09-07 from the real local DSH WebUI, using the `codex/memory-space-terminology` working tree based on v0.5.3 (`7e6216094a9361e9abb091a5edb2b83cb7ed05df`). The version badge identifies the base package version. These captures were made before versioning and include the terminology update released in v0.5.4.

## Environment and scope

- Node.js 25.1.0, DSH 0.1.2-rc.1, and the existing Mnemon CLI 0.2.7 on macOS.
- `node scripts/serve-e2e.mjs` supplied a disposable DSH Profile, workspace, and data directory. The fixture linked the local Starter, Sources, and Providers.
- Synthetic Atlas data: one native memory space, two memories, one Runtime item, and one managed Document. Some earlier screenshots precede creation of the Runtime item and Document. User-authored sample content is intentionally bilingual and does not change with the interface language.
- Desktop viewport: 1365 × 1000. Narrow viewport: 390 × 844.
- Screenshots are unchanged JPEG bytes returned by browser capture. [The manifest](./manifest.json) records their sizes and SHA-256 hashes. [Layout measurements](./layout-checks.json) record the final narrow-layout checks.
- No production memory, cloud Provider calls, or LLM requests were used. Creation, editing, activation, browsing, language switching, and form cancellation were exercised; AI metadata and semantic archiving were not invoked. The disposable fixture was removed after verification.

## Captures

| Surface | English | 中文 |
|---|---|---|
| Status | [Status](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/status.jpg) | [状态](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/status.jpg) |
| Runtime | [Runtime](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/runtime.jpg) | [运行时](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/runtime.jpg) |
| Documents | [Documents](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/documents.jpg) | [档案](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/documents.jpg) |
| Memory Spaces | [Directory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/spaces.jpg) | [记忆空间目录](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/spaces.jpg) |
| Content | [Content](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/content.jpg) | [内容](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/content.jpg) |
| Create | [Form](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/create.jpg) | [创建表单](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/create.jpg) |
| Settings | [Layers and enhancements](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/enhancements.jpg) | [记忆层与增强](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/enhancements.jpg) |
| Narrow directory | [390 px directory](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/spaces-mobile.jpg) | [390 像素目录](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/spaces-mobile.jpg) |
| Narrow create | [390 px form](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/en/create-mobile.jpg) | [390 像素表单](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/assets/memory-space-terminology/zh-CN/create-mobile.jpg) |

## Layout findings

The longer labels exposed a directory header that exceeded its available width. The header now has a bounded grid column, the directory path truncates within that column, and action buttons wrap. Provider metadata badges also wrap. Compact navigation uses tighter spacing, and the storage badge can move below the title. Both locales keep the directory and page canvas within their widths, with creation buttons inside the viewport after the sheet animation completes.

## 中文说明

本组截图来自真实 DSH WebUI 与临时测试数据，展示基于 v0.5.3 的用语更新；界面统一使用“记忆空间 / memory space”。截图在版本化前采集，包版本号仍是基础版本；所展示的用语与布局改动纳入 v0.5.4。

桌面与窄屏分别按 1365 × 1000、390 × 844 检查。实测覆盖创建、编辑、激活、浏览、语言切换和取消表单；没有调用模型、云端 Provider 或生产记忆。窄屏目录按钮、Provider 状态标签和顶部存储标识可以换行，创建表单的底部按钮保持可见。截图保留浏览器原始 JPEG 数据，哈希与布局测量见上方记录。

The [v0.5.2 showcase](../showcase/README.md) remains historical evidence with its original screenshot pixels and recording.
