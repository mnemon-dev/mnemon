# dsh-mnemon-provider-holographic

Local structured-fact storage with trust metadata, entities and related evidence.

No external server is required. Choose a data path when a Source-local default is not appropriate; this adapter owns its JSON format.

## Use

The default `dsh-mnemon` Starter already installs this package. Enable and configure the service on the `dsh-mnemon` page under **Plugins**, then inspect its synchronized Memory Spaces.

For a custom composition, install this package alongside `dsh-mnemon-source-memory-spaces` and include it in that Source's `config.providers`:

```yaml
providers:
  - use: dsh-mnemon-provider-holographic
    instanceId: holographic
```

This is a **Memory Spaces child module**, not a top-level Source or a complete Strategy. It registers through `dsh-mnemon-source-memory-spaces/provider-sdk`; each parent Source owns its child Fibers, connection settings and lifetime. Credentials stay on the Host.

[Provider setup and capability matrix](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/guides/memory-providers.md) · [中文指南](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/zh-CN/guides/memory-providers.md)

## Develop independently

From a source checkout with the declared dependencies installed, run `pnpm verify`. Tests, build and public exports belong to this package, without importing another package's controllers or repository configuration. Use the Source's public `/testing` fixtures for child registration and driver conformance; live service tests require a separately authorized environment.

[Plugin author guide](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/extensions.md)
