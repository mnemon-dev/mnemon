# dsh-mnemon-strategy-general

A role-agnostic main Strategy for `dsh-mnemon`. It admits every available
Source, whatever its role, shares one projection budget between them and offers
their Routes and Actions round-robin, so each admitted Source is reachable
before any Source receives a second operation. The model then decides whether
and how to use each Source; the Strategy adds no retrieval policy of its own.

Compared with `dsh-mnemon-strategy-default-three-tier`:

| | Layered strategy | General strategy |
|---|---|---|
| Source roles | one working-context, narrative and durable-evidence Source | any role, any number (up to 32) |
| Resident context | Runtime Memory | Sources listed in `residentSourceKeys`, else working-context Sources |
| Reads | recall, search, inspect and related Routes with per-turn deduplication | every offered Route, bounded by Core's call and result budgets |
| Automatic maintenance | idle review and Runtime capacity archiving | none; the model writes through offered Actions |

Exactly one main Strategy composes a View. Select it on the `dsh-mnemon` page
under **Plugins**; the Starter installs it disabled. The optional enhancements (scoped composition, light context and
active capture) use Core's standard `selection`, `projection` and `capture`
slots, so they work with either main Strategy.

## Configuration

| Field | Default | Meaning |
|---|---|---|
| `residentSourceKeys` | working-context Sources | Source instances projected into every turn; the others stay on demand |
| `instruction` | none | Extra guidance appended to the memory protocol |

Run `pnpm verify` for standalone checks.
