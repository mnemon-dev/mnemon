# dsh-mnemon-provider-mnemon-native

The default local Mnemon CLI backend: exact writes, typed relationships, recall and soft deletion.

Install the `mnemon` CLI separately and ensure the DSH Host can resolve it. The npm package contains the driver, not the binary.

## Use

The default `dsh-mnemon` Starter already installs this package. Native is the default backend while its CLI is installed; without the CLI, Memory Spaces use another ready Provider.

For a custom composition, install this package alongside `dsh-mnemon-source-memory-spaces` and include it in that Source's `config.providers`:

```yaml
providers:
  - use: dsh-mnemon-provider-mnemon-native
    instanceId: mnemon-native
```

This is a **Memory Spaces child module**, not a top-level Source or a complete Strategy. It registers through `dsh-mnemon-source-memory-spaces/provider-sdk`; each parent Source owns its child Fibers, connection settings and lifetime. Credentials stay on the Host.

Runtime bulk archival reuses only exact content found in a readonly namespace snapshot and groups identical entries within the pending batch. It imports the remaining originals with `mnemon import --no-diff`, so semantic similarity cannot skip a distinct fact or replace an existing one. Import counts, source indexes and exact content must match before receipts are returned. Ordinary `remember` continues to use the CLI's normal deduplication policy. No storage format or Source/Strategy contract changes are required.

[Provider setup and capability matrix](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/memory-providers.md) · [中文指南](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/zh-CN/guides/memory-providers.md)

## Develop independently

From a source checkout with the declared dependencies installed, run `pnpm verify`. Tests, build and public exports belong to this package, without importing another package's controllers or repository configuration. Use the Source's public `/testing` fixtures for child registration and driver conformance; live service tests require a separately authorized environment.

[Plugin author guide](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/extensions.md)
