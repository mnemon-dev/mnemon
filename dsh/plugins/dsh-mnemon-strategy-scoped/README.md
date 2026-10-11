# dsh-mnemon-strategy-scoped

Optional additive Source selection for any Mnemon main Strategy that accepts Core's standard `selection` slot, including the Layered and General strategies.

Enable the plugin alongside the selected main Strategy. With no configuration it includes every ready Source whose role is `working-context`, `narrative`, or `durable-evidence`, in deterministic role/key order. Configure exact `sourceKeys` to express priority; configure `writableSourceKeys` to make the remaining selected Sources read-only in the View.

```yaml
- id: mnemon-strategy-scoped
  name: dsh-mnemon-strategy-scoped
  config:
    sourceKeys:
      - source:global-runtime
      - source:project-runtime
    writableSourceKeys:
      - source:project-runtime
```

Keys name already installed Source instances; this plugin does not create or migrate storage. Uninstalling it removes only its selection contribution. If duplicate roles remain installed, the Layered strategy, unextended, reports the ambiguity rather than choosing by load order; the General strategy admits every instance.

## Installation and verification

The default Starter already installs this package as a disabled Entry. Use its switch on the `dsh-mnemon` page under **Plugins**; do not add a second copy of the same Entry. The configuration example above is for an explicitly composed custom Profile.

Installing an npm package is not the same as activating a contribution. From a source checkout, run `pnpm verify` with declared public peers installed; use a packed peer for unreleased SDK work.

[Plugin development and independent fixtures](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/extensions.md) · [中文指南](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/zh-CN/development/extensions.md)
