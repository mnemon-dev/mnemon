> 提交 PR 前请阅读[贡献指南](../CONTRIBUTING.zh-CN.md)、[Contributing Guide](../CONTRIBUTING.md)与[开发和验证指南](../docs/zh-CN/development/README.md) / [Development and Verification Guide](../docs/en/development/README.md)。
> Before opening a PR, read the [Contributing Guide](../CONTRIBUTING.md), [贡献指南](../CONTRIBUTING.zh-CN.md), and the [Development and Verification Guide](../docs/en/development/README.md) / [开发和验证指南](../docs/zh-CN/development/README.md).
> PR 标题和提交信息必须使用 Conventional Commits（`type(scope): subject`），且不得包含 emoji。 / PR titles and commits must use Conventional Commits (`type(scope): subject`) and must not contain emoji.
> 本仓库接受 Bug 修复、兼容性适配、现有能力增强、性能或体验优化和维护类 PR。全新能力、Provider、持久化格式或安全边界变更必须先提 Issue 并获得维护者确认。 / This repository accepts bug fixes, compatibility work, improvements to existing capabilities, performance or UX optimization, and maintenance. New capabilities, Providers, persistence formats, or security-boundary changes require prior maintainer approval in an Issue.
> 外部贡献者的仅文档类 PR 不直接接受；请先提 Issue 讨论。维护者的发布说明与文档维护不受此限制。 / Documentation-only PRs from external contributors are not accepted directly; open an Issue first. Maintainer release notes and documentation maintenance are exempt.

## 摘要 / Summary

<!-- 用一两句话说明改了什么、为什么改，以及用户或系统行为如何变化。 / In one or two sentences, explain what changed, why, and how user or system behavior changes. -->

## 关联 Issue 或背景 / Related Issue or Context

<!-- 优先使用 Closes #123 或 Fixes #123。没有 Issue 时填写 N/A 并解释为什么可以直接提交。 / Prefer Closes #123 or Fixes #123. If there is no Issue, enter N/A and explain why a direct PR is appropriate. -->

## 涉及区域 / Affected Areas

<!-- 至少勾选一项。 / Check at least one item. -->

- [ ] Host 激活、Headless 或 bundle / Host activation, Headless, or bundle
- [ ] 运行时记忆 / Runtime Memory
- [ ] 项目档案 / Project Documents
- [ ] 记忆空间或 Provider / Memory Spaces or Providers
- [ ] 子 Agent 或 Agent 工作流 / Subagent or Agent workflow
- [ ] Web UI 或对话交互 / Web UI or conversation interaction
- [ ] 设置、存储或安全 / Settings, storage, or security
- [ ] CLI、RPC、命令或工具 / CLI, RPC, commands, or tools
- [ ] 安装、更新或发布 / Installation, update, or release
- [ ] 测试、构建或文档 / Tests, build, or documentation
- [ ] 其他（请说明）/ Other (explain below)

## PR 类型 / PR Type

<!-- 至少勾选一项；仅文档类 PR 的限制见文件顶部说明。 / Check at least one item; see the note above for documentation-only PRs. -->

- [ ] 面向用户的功能或行为变更 / User-facing feature or behavior change
- [ ] Bug 修复 / Bug fix
- [ ] 增强或优化 / Enhancement or optimization
- [ ] 兼容性适配 / Compatibility change
- [ ] 维护或重构 / Maintenance or refactor
- [ ] 测试或构建 / Tests or build

## 最新代码确认 / Latest Codebase Confirmation

- [ ] 我已基于最新 `main` 分支开发，或在提交前已 rebase 或合并最新 `main`。 / I developed from the latest `main`, or rebased or merged the latest `main` before submitting.

同步命令 / Sync command:

<!-- 示例 / Example: git fetch origin && git rebase origin/main -->

## AI 编码披露 / AI Coding Disclosure

<!-- 必填。勾选且仅勾选一项；模型和工具字段不得留空。 / Required. Check exactly one option; the model and tool fields must not be blank. -->

- [ ] 完全 AI 编码：全部编程改动由 AI 产出，并由贡献者接受和审查。 / Fully AI-coded: AI produced all programming changes, which the contributor accepted and reviewed.
- [ ] 部分 AI 辅助：AI 帮助编写或修改了部分内容。 / Partially AI-assisted: AI helped write or modify part of the change.
- [ ] 未使用 AI 编码辅助。 / No AI coding assistance was used.

使用的 AI 模型 / AI model used:

<!-- 使用 AI 时填写具体模型；未使用时填 N/A。 / Name the model when AI was used; otherwise enter N/A. -->

使用的编码 Agent 工具 / Coding Agent tool used:

<!-- 使用 AI 时填写 Codex、Claude Code、Cursor 等；未使用时填 N/A。 / Name tools such as Codex, Claude Code, or Cursor when AI was used; otherwise enter N/A. -->

## 仓库规范检查 / Repository Rules

<!-- 本仓库硬性规范，请逐项确认。 / Confirm every mandatory repository rule. -->

- [ ] 未修改 DSH 官方源码，未让 tsconfig 指向 DSH 源码 checkout，仅使用正式的 `@deepseek-ai/*` NPM 契约。 / I did not modify DSH source or point tsconfig at a DSH source checkout, and used only published `@deepseek-ai/*` NPM contracts.
- [ ] Client 与 Host 边界仍以浏览器安全的 `src/host/protocol.ts` 为准，没有在两侧重复定义 wire DTO。 / The Client and Host boundary still uses browser-safe `src/host/protocol.ts` as the source for wire DTOs.
- [ ] 持久化格式、RPC 权限、路径或凭据处理的变更包含兼容或拒绝路径、安全分析和相应测试。 / Changes to persistence formats, RPC authority, paths, or credentials include compatibility or rejection paths, security analysis, and tests.
- [ ] 没有提交 token、密钥、私有记忆、未脱敏日志或生成的 `lib/` 文件。 / I did not commit tokens, credentials, private memory, unredacted logs, or generated `lib/` files.
- [ ] 用户可见文案和长期文档已同步维护中文与英文版本，命令、配置键和路径保持一致。 / User-facing copy and long-lived documentation are synchronized in Chinese and English, with matching commands, configuration keys, and paths.
- [ ] 会改变发布制品或其元数据的 PR 已添加 changeset；仅测试、CI 或站点文档变更可不添加。 / A PR that changes a published artifact or its metadata includes a changeset; test-only, CI-only, and site-documentation-only changes may omit one.
- [ ] 新增和修改的代码、注释、文档、提交信息不含 emoji。 / New and modified code, comments, documentation, and commits contain no emoji.

## 兼容性与数据安全 / Compatibility and Data Safety

<!-- 必填。说明对 DSH、Mnemon、Provider、配置、存储格式、升级或回滚和现有数据的影响。没有影响时填 N/A 并说明理由。 / Required. Explain effects on DSH, Mnemon, Providers, configuration, storage formats, upgrades or rollback, and existing data. Enter N/A with a reason when there is no impact. -->

## 本地验证 / Local Validation

执行的命令 / Commands run:

```bash
pnpm run verify
```

结果摘要 / Result summary:

<!-- 写明通过、跳过或失败的项目和原因，不要留空。 / Report passed, skipped, or failed checks and the reasons; do not leave this blank. -->

## 用户可见变更证据 / Local Feature Evidence

<!--
面向用户的功能或行为变更必填。 / Required for user-facing feature or behavior changes.
附截图或短视频，展示 / Attach screenshots or a short video showing:
- 使用的是本 PR 或最新代码 / the PR or latest code is running
- 功能已启用或配置 / the feature is enabled or configured
- 操作成功并出现可见结果 / the action succeeds with a visible result
- 没有泄露凭据、私有记忆或敏感路径 / no credentials, private memory, or sensitive paths are exposed
纯内部改动可填 N/A 并解释。 / For internal-only changes, enter N/A and explain.
-->

证据 / Evidence:
