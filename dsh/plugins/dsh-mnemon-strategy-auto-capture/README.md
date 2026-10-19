# dsh-mnemon-strategy-auto-capture

Optional additive, **in-turn guidance** for any Mnemon main Strategy that accepts Core's standard `capture` slot, including the Layered and General strategies.

The default targets ready `durable-evidence` Sources whose selected View exposes the Source-local `remember` Action. Custom Sources must configure the Action ids that actually mean recording:

```yaml
- id: mnemon-strategy-auto-capture
  name: dsh-mnemon-strategy-auto-capture
  config:
    sourceKeys:
      - source:team-notes
    actionIds:
      - append
```

The plugin asks the current LLM to consider at most one qualified durable fact. It starts no background Agent or timer, executes no write during View composition, cannot expose an Action removed by the main Strategy or Host, and cannot bypass ActionOffer authorization. Model compliance and capture quality require task-level evaluation.

## Installation and verification

The default Starter already installs this package as a disabled Entry. Use its switch on the `dsh-mnemon` page under **Plugins**; do not add a second copy of the same Entry. The configuration example above is for an explicitly composed custom Profile.

Installing an npm package is not the same as activating a contribution. From a source checkout, run `pnpm verify` with declared public peers installed; use a packed peer for unreleased SDK work.

[Plugin development and independent fixtures](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/extensions.md) · [中文指南](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/zh-CN/development/extensions.md)
