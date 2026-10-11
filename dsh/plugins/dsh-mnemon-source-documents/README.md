# dsh-mnemon-source-documents

Owns managed Markdown, search, revisions and local archiving. It requires a workspace identity even when storage is shared globally.

## Use

The `dsh-mnemon` Starter installs and mounts this Source by default. The package alone does not activate an instance or select a Strategy.

For a custom Profile, mount an explicit Entry after the Host's `ctx.mnemonMemory` service is available:

```yaml
- id: personal-documents
  name: dsh-mnemon-source-documents
  config:
    dataDir: /absolute/path/to/personal-memory
```

Choose an existing data authority deliberately. Without `dataDir`, each stable Source instance uses an isolated directory under `~/.mnemon/sources/`; the default Starter supplies the existing product paths. Replacing the default Source or adding a second instance requires an explicit selection Strategy, not a second implicit default role.

Manual management can create a document without a model, but it still needs a workspace in the request scope. Standalone archiving retains content locally; cross-Source LLM distillation is an optional Host workflow.

## Create-only Action / 仅创建操作

The `create` Action accepts `title`, `content`, and optional `description`, `sourcePaths`, and `sessionIds`. It always creates a separate document, rejects mutation selectors such as `action` or `id`, and fails at capacity without updating or archiving existing documents. The existing `manage` Action remains available for create/update workflows. Mnemon idle review receives only the create-only document tool and skips already-covered candidates. No document format migration is required.

`create` Action 接收 `title`、`content`，以及可选的 `description`、`sourcePaths`、`sessionIds`。它始终新建独立档案，拒绝 `action`、`id` 等 mutation 选择字段；容量不足时失败，不更新或归档已有档案。原有 `manage` Action 继续支持创建和更新。Mnemon 后台审查只获得仅创建档案工具，已有内容覆盖候选时跳过。本变更无需迁移档案格式。

## Source-owned UI and tests

The optional `./client` entry is an ordinary DSH Client plugin. This package owns its pages and `presentation/` resources and uses `dsh-mnemon/client` for the shared frame and scoped management client. It never receives a Host Context, credentials or another Source's controller.

From a source checkout, install the declared dependencies and run `pnpm verify` to check Host behavior, Source-backed page interactions and Host/browser artifacts. Unreleased SDK work consumes a packed `dsh-mnemon` peer, not repository aliases or copied root tests.

[Plugin development](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/extensions.md) · [中文指南](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/zh-CN/development/extensions.md)
