# Architecture assets / 架构素材

The README overview has matching [English](./en/composable-memory.svg) and [Chinese](./zh-CN/composable-memory.svg) editable sources. Each adjacent PNG is a 2× rendered fallback for Markdown consumers that do not render Mermaid or SVG. Labels describe the current public boundary, not a proposed new pipeline.

[中文](./zh-CN/composable-memory.svg)与[英文](./en/composable-memory.svg)概览各自保留可编辑源，同目录 PNG 是以 2 倍分辨率生成的兼容版本，供不支持 Mermaid 或 SVG 的 Markdown 展示使用。图中描述当前公开边界，并非待实现的新流程。中英文 README 使用各自语言的图，不翻译 API 标识。

Detailed ownership, data-flow and sequence diagrams stay in the [English architecture guide](../../en/development/architecture.md) / [中文架构指南](../../zh-CN/development/architecture.md). Keep SVG and PNG synchronized when changing the overview. Older language-specific `memory-system-flow.svg` files remain the sources of the historical three-tier media; they are not unused capture files.

完整归属、数据流和时序图见上述中英文架构指南。修改概览时同步更新两种语言的 SVG 与 PNG；旧版 `memory-system-flow.svg` 仍是历史三层记忆素材的源文件，应予保留。

The Chinese SVG labels use 记忆空间. Their PNGs were regenerated from the editable sources with resvg at 2× on 2026-09-07; the historical flow diagram retains its original v0.2.0 behavior and version label.

中文 SVG 标签统一使用“记忆空间”；对应 PNG 于 2026-09-07 由 resvg 按 2 倍尺寸重新生成。历史流程图仍保留 v0.2.0 的行为与版本标识。
