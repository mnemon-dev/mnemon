# Security Policy

**English** | [简体中文](#简体中文)

## Supported versions

| Version | Supported |
|---|---|
| Latest stable `dsh-mnemon` Starter and its pinned official plugins | Security fixes |
| Older releases | Upgrade to the latest stable Starter |

Only the latest stable release receives security fixes. Official Source, Strategy and Provider packages in this repository are in scope even when published independently; report both the Starter version and the affected package version. The Starter pins a tested combination. See the verified Host and runtime baselines in [Compatibility](./docs/en/reference/compatibility.md).

## Reporting a vulnerability

Please **do not open a public issue** for security vulnerabilities.

Report privately through one of these channels, in order of preference:

1. **GitHub Security Advisories** — use the “Report a vulnerability” flow on the [Security tab](https://github.com/mnemon-dev/mnemon/security) of this repository (private vulnerability reporting).
2. **Email** — `grivn.wang@gmail.com` (maintainer). Use a subject prefix like `[dsh-mnemon security]`.

Please include, when possible:

- affected version(s);
- a minimal reproduction or proof of concept;
- whether the issue is already exploited publicly;
- your disclosure preference (e.g., coordinated disclosure, credit, embargo).

## What to expect

- Acknowledge receipt within **7 days**.
- A fix or a reasoned “not a vulnerability” response within **30 days** where feasible.
- Credit in the release notes unless you prefer to stay anonymous.

## Scope

In scope:

- The plugin bundle (`lib/`, `cordis.patch.yml`): data-loss, privilege-escalation, path-traversal, secret-leak, and memory-corruption style issues in plugin code.
- The control plane: lock handling, revision conflict checks, atomic writes, and subagent isolation boundaries.
- The composable-memory boundary: generation/turn ownership, View validation, grant and live authority checks, and Evidence/Receipt integrity.
- The WebUI: XSS or injection via rendered memory content.

Out of scope:

- Issues in upstream dependencies (`mnemon`, `cordis`, DSH core, React) — report those upstream; we will still help triage and pin workarounds.
- Missing features, documentation gaps, and non-security bugs — use regular issues.
- Credentials or secrets you intentionally stored in memory data yourself; the plugin has no secret scanner (see the disclosure in the README).

## Known limitations

- There is no deterministic credential/secret detection today. Do not write keys, tokens, or private keys into Runtime Memory, Documents, or Memory Spaces.
- Cordis isolation provides lifecycle ownership and scope composition, not a security sandbox. Sources, their Providers, and Strategies are ordinary JavaScript in the DSH Host process. Install extensions only from trusted sources.
- Model-generated Strategy source is not executed automatically. Manifests, scoped grants, replay, and Core/Host validation reduce accidental authority but cannot make hostile in-process code safe.
- Runtime Memory and Documents are local, and Mnemon Native is local by default. Enabled third-party Providers can make network or local-process calls and expose data according to their configured endpoints and credentials.

---

## 简体中文

### 支持的版本

| 版本 | 支持 |
|---|---|
| 最新稳定版 `dsh-mnemon` Starter 及其固定的官方插件组合 | 提供安全修复 |
| 较旧版本 | 升级到最新稳定版 Starter |

仅最新稳定版本获得安全修复。本仓库的官方 Source、Strategy 和 Provider 包即使独立发布，也属于报告范围；请同时提供 Starter 与受影响包的版本。Starter 固定经过验证的组合，已验证的宿主和运行环境见[兼容性说明](./docs/zh-CN/reference/compatibility.md)。

### 报告漏洞

请**不要**为安全漏洞开公开 issue，按优先级使用以下渠道私下报告：

1. **GitHub Security Advisories**：在本仓库 [Security 页](https://github.com/mnemon-dev/mnemon/security) 使用“Report a vulnerability”（私密漏洞报告）。
2. **邮件**：`grivn.wang@gmail.com`（维护者），主题加 `[dsh-mnemon security]` 前缀。

请尽量附上：受影响版本、最小复现或 PoC、是否已被公开利用、披露偏好（协调披露 / 署名 / 保密期）。

### 预期

- **7 天**内确认收到；可行情况下 **30 天**内给出修复或“非漏洞”的结论。
- 除非你要求匿名，否则在发布说明中致谢。

### 范围

**范围内**：插件 bundle 与 `cordis.patch.yml` 中的数据丢失、提权、路径穿越、秘密泄露类问题；控制面（锁、revision 冲突检查、原子写、子 Agent 隔离）；可组合记忆边界（generation/turn 所有权、View 校验、grant 与当前权限检查、Evidence/Receipt 完整性）；WebUI 对记忆内容渲染的 XSS/注入。

**范围外**：上游依赖（`mnemon`、`cordis`、DSH 核心、React）自身的问题（报告给上游，我们可协助定位与临时规避）；功能缺失、文档问题（走普通 issue）；你自行写入记忆数据中的凭据或秘密（插件当前没有秘密检测器，见 README 披露）。

### 已知限制

- 当前没有确定性的凭据/秘密检测，请勿向热记忆、Documents 或 Memory Spaces 写入密钥、token、私钥。
- Cordis isolate 提供生命周期所有权和作用域组合，不是安全沙箱。Source、其内部 Provider 与 Strategy 都是在 DSH Host 进程执行的普通 JavaScript，只能安装受信任来源的扩展。
- 模型生成的 Strategy 源码不会自动执行。manifest、限定范围的 grant、replay 与 Core/Host 校验可以降低意外越权，但无法让恶意同进程代码变安全。
- Runtime Memory 与 Documents 为本地存储，Mnemon Native 默认本地；显式启用的三方 Provider 可能按配置 endpoint 与凭据发起网络调用或本地进程调用，并向对应服务暴露数据。
