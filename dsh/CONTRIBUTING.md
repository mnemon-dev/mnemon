# Contributing

[简体中文](./CONTRIBUTING.zh-CN.md) | **English**

Thank you for contributing to dsh-mnemon. This document defines the Issue, Pull Request, verification, and maintenance rules. See the [Development and Verification Guide](./docs/en/development/README.md) for implementation details and test scenarios.

## Pull Request scope

The repository accepts these external Pull Requests:

- **Fixes**: reproducible bugs, compatibility problems, and security hardening;
- **Enhancements and optimization**: improvements to existing capabilities, performance, stability, and user experience;
- **Maintenance**: tests, build work, refactors, and dependency compatibility.

New capabilities, Providers, persistence formats, RPC authority, or security-boundary changes require an Issue and maintainer approval before implementation begins.

Documentation-only PRs from external contributors are not accepted directly. Open an Issue first so maintainers can confirm the scope. Maintainer release notes, bilingual synchronization, and documentation maintenance are exempt.

## Prerequisites

- Node.js `^22.19.0 || >=24.0.0`; DSH 0.1.1-rc.2 uses host primitives unavailable on Node 20, and CI covers Node.js 22.19 plus 24 with pnpm 10.13.1;
- use only published `@deepseek-ai/*` NPM contracts; do not modify DSH source or point tsconfig at a DSH source checkout;
- do not commit tokens, credentials, private memory, sensitive paths, or unredacted logs in repository configuration, fixtures, screenshots, or PR descriptions;
- `lib/` is generated build output and must not be committed or edited manually.

## Quick start

```sh
git clone https://github.com/mnemon-dev/mnemon.git
cd mnemon/dsh
pnpm install
pnpm run verify
```

`verify` runs TypeScript checks, Vitest, reproducible double builds, isolated real Headless-profile activation, and published-package validation.

## Commit convention

Commit messages and PR titles use Conventional Commits:

```text
type(scope): subject
```

Allowed types are `feat`, `fix`, `chore`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, and `revert`. Use a module or topic for the scope, for example:

```text
fix(version): run pnpm update inside the owning profile
fix(client): honor trusted remote management grant
```

Code, comments, documentation, commit messages, and PR titles must not contain emoji.

## Checklist before opening a PR

1. **Use the latest main**: rebase onto or merge the latest `main`, resolve conflicts, and rerun verification before submitting.
2. **Run complete verification**: run `pnpm run verify` by default. If a check cannot run, document the reason, alternative evidence, and remaining risk in the PR.
3. **Add targeted regression coverage**: bugs need a test that fails before the fix and passes afterward. UI behavior must cover both authorized and rejected paths.
4. **Provide real evidence**: attach a local screenshot or short video for user-visible changes. For Host, CLI, or Headless changes, attach redacted logs or command results.
5. **Keep both languages synchronized**: long-lived documents under `docs/en/` and `docs/zh-CN/` must have matching names and responsibilities. Keep `README.md` and `README.zh-CN.md` synchronized.
6. **Explain security and data effects**: changes to persistence formats, paths, RPC authority, Provider credentials, or import/export must explain compatibility, migration or rejection, rollback, and data retention.
7. **Keep boundaries stable**: `src/host/protocol.ts` is the browser-safe source of Client and Host wire DTOs. Do not import executable Host modules into browser code.
8. **Disclose AI coding**: complete the PR template with the model and coding tool used. Contributors remain responsible for the final code, verification, and security.
9. **Do not commit generated output**: ensure the worktree contains no `lib/` output or temporary Provider-lab data.

## Documentation and persistence constraints

- When user-visible behavior changes, review the English and Chinese versions of `ui-guide.md`, `getting-started.md`, `configuration.md`, and `operations.md`;
- commands, configuration keys, paths, and code symbols must match across both languages;
- Runtime, Documents, and Memory Space registry format changes must not happen silently; include legacy-format handling and damaged-input tests;
- do not send user preferences into long-term Memory Spaces or use real private data in fixtures and documentation examples.

## Issue rules

- Search open and closed Issues before submitting;
- use the bilingual [Bug report form](.github/ISSUE_TEMPLATE/bug_report.yml) for bugs, describing the symptom, expected outcome, reproduction steps and environment. Evidence may be plain text, JSON or another code block anywhere in the report; separate evidence, smoke tests, code references and patch proposals are optional;
- use the bilingual [standard Issue form](.github/ISSUE_TEMPLATE/standard_issue.yml) for features, enhancements, documentation, and questions;
- CLI/API submissions may use the same section headings; the bot adds `bug` when the report type is “Bug 报告 / Bug report”, so reporters do not need label permissions;
- template checks request missing basic information without closing the Issue. Edit the original body to supply details; the bot updates its existing reminder. Maintainers decide whether further investigation or closure is appropriate;
- see [Issue Triage](./ISSUE_TRIAGE.md) or [Issue 分类标准](./ISSUE_TRIAGE.zh-CN.md) for labels, classification, and closure criteria;
- report security vulnerabilities privately through [SECURITY.md](./SECURITY.md), not in a public Issue.

## Pull Request rules

- Keep the bilingual [PR template](.github/pull_request_template.md) intact and complete its summary, context, affected areas, type, latest-code confirmation, AI disclosure, compatibility and data safety, local validation, and user-visible evidence;
- CI and PR policy checks must pass. Correct the description or implementation when a check fails; do not remove template sections;
- PRs superseded by a more complete solution, based on an obsolete design, or unable to upgrade safely will be closed with an explanation;
- reviews use the current `main`, public contracts, and released behavior as the source of truth, not only the PR description.
