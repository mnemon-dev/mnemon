# 贡献指南

**简体中文** | [English](./CONTRIBUTING.md)

欢迎为 dsh-mnemon 贡献代码。本文件定义 Issue、Pull Request、验证和维护规则；实现细节与测试场景见[开发和验证指南](./docs/zh-CN/development/README.md)。

## PR 范围

本仓库接受以下外部 PR：

- **修复**：可复现的 Bug、兼容性问题和安全加固；
- **增强与优化**：现有能力的改进，以及性能、稳定性和体验优化；
- **维护**：测试、构建、重构和依赖兼容性工作。

全新能力、新 Provider、持久化格式、RPC 权限或安全边界变更必须先提交 Issue，获得维护者确认后再开始实现。

外部贡献者的仅文档类 PR 不直接接受；请先提交 Issue 讨论，由维护者确认范围。仓库维护者的发布说明、双语同步和文档维护不受此限制。

## 开发前置

- Node.js `^22.19.0 || >=24.0.0`；DSH 0.1.1-rc.2 使用 Node 20 缺失的宿主原语，CI 使用 pnpm 10.13.1 覆盖 Node.js 22.19 与 24；
- 仅使用正式发布的 `@deepseek-ai/*` NPM 契约，禁止修改 DSH 源码或让 tsconfig 指向 DSH 源码 checkout；
- 不要在仓库配置、测试夹具、日志、截图或 PR 描述中提交 token、凭据、私有记忆、敏感路径或未脱敏日志；
- `lib/` 是构建生成物，不进入版本库，也不得手工编辑。

## 快速开始

```sh
git clone https://github.com/mnemon-dev/mnemon.git
cd mnemon/dsh
pnpm install
pnpm run verify
```

`verify` 包含 TypeScript 检查、Vitest、可复现双构建、隔离的真实 Headless profile 激活和发布包验证。

## 提交规范

提交信息和 PR 标题使用 Conventional Commits：

```text
type(scope): subject
```

允许的 type 包括 `feat`、`fix`、`chore`、`docs`、`test`、`refactor`、`perf`、`build`、`ci` 和 `revert`。scope 使用模块或主题，例如：

```text
fix(version): run pnpm update inside the owning profile
fix(client): honor trusted remote management grant
```

代码、注释、文档、提交信息和 PR 标题禁止 emoji。

## 提 PR 前检查清单

1. **基于最新 main**：提交前 rebase 或合并最新 `main`，解决冲突并重新验证。
2. **完整验证**：默认运行 `pnpm run verify`；若无法运行某一项，在 PR 中写明原因、替代验证和剩余风险。
3. **定向回归**：为缺陷补充失败前和通过后的回归测试；UI 行为同时验证授权和拒绝路径。
4. **真实证据**：用户可见变更附本地截图或短视频；Host、CLI、Headless 变更附脱敏日志或操作结果。
5. **双语同步**：长期文档在 `docs/en/` 和 `docs/zh-CN/` 保持同名、同职责；根 README 同步维护 `README.md` 和 `README.zh-CN.md`。
6. **安全与数据**：持久化格式、路径、RPC authority、Provider 凭据或导入导出变更必须说明兼容、迁移或拒绝、回滚和数据保留行为。
7. **边界稳定**：`src/host/protocol.ts` 是可在浏览器使用的 Client 与 Host wire DTO 来源；不得在浏览器半区引入可执行的 Host 模块。
8. **AI 披露**：按 PR 模板如实填写使用的模型和编码工具；贡献者仍对最终代码、验证和安全负责。
9. **不提交生成物**：确认工作区不包含 `lib/` 或临时 Provider lab 数据。

## 文档和持久化约束

- 修改用户可见行为时检查 `ui-guide.md`、`getting-started.md`、`configuration.md` 和 `operations.md` 的中英文版本；
- 命令、配置键、路径和代码符号在两种语言中必须一致；
- Runtime、Documents 和 Memory Space registry 的格式变更不得静默发生，必须提供旧格式处理和损坏输入测试；
- 不得把用户偏好发送到长期 Memory Space，也不得在测试或文档示例中使用真实私有数据。

## Issue 规则

- 提交前搜索 open 和 closed Issue，确认没有重复；
- Bug 使用双语[Bug 报告表单](.github/ISSUE_TEMPLATE/bug_report.yml)，描述现象、预期结果、复现步骤和环境。证据可用普通文本、JSON 或其他代码块，放在正文任意位置；单独的证据、冒烟测试、代码引用和补丁草案均为选填；
- 功能、增强、文档和问题使用双语[标准 Issue 表单](.github/ISSUE_TEMPLATE/standard_issue.yml)；
- CLI/API 提交可使用相同段落标题；类型为“Bug 报告 / Bug report”时机器人会补齐 `bug` 标签，无需提交者拥有标签权限；
- 模板检查仅提示补充缺失的基础信息，不关闭 Issue。直接编辑原正文补充信息，机器人会更新已有提示；进一步排查或关闭由维护者决定；
- 标签体系、分类和关闭标准见[Issue 分类标准](./ISSUE_TRIAGE.zh-CN.md)或英文版 [Issue Triage](./ISSUE_TRIAGE.md)；
- 安全漏洞必须按 [SECURITY.md](./SECURITY.md) 私下报告，不要创建公开 Issue。

## PR 规则

- 保持双语 [PR 模板](.github/pull_request_template.md)完整，填写摘要、背景、涉及区域、类型、最新代码确认、AI 披露、兼容和数据安全、本地验证与用户可见证据；
- CI 和 PR 规则检查必须通过；失败时修正 PR 描述或实现，不要删除模板段落；
- 已被更完整方案取代、基于过时设计或无法安全升级的 PR 会被关闭并说明原因；
- Review 结论以当前 `main`、公开契约和已发布行为为准，而不是仅以 PR 描述为准。
