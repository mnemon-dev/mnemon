# dsh-mnemon-strategy-default-three-tier

One ordinary Cordis Strategy plugin. It consumes public Source facts and proposes
a three-tier View; it imports no Source implementation and grants no authority.

`compose` is pure. `createTurn` owns the executing turn's retrieval policy:
one Documents query (4 results / 6,000 content characters), two different Recall
queries sharing 6 results / 4,800 content characters, duplicate replay,
relevance admission, and one Related traversal from admitted evidence.
Named tools and generic View routes use the same state. Independent child turns
never share quotas, even when they inherit the same immutable View.

Source schemas and provenance are the public read protocol. This plugin formats
the familiar compact model response; Core retains the evidence/authority envelope
for auditing without serializing it again into every model tool result.

The default plugin also owns the existing three-tier instructions and reminders.
They are pinned in `ViewSpec.guidance`, separately from Source projections. The
Host renders them for the selected turn; a different Strategy does not inherit
this plugin's instructions. DSH-assisted write/archival workflows remain Host
adapters over Source operations; Core does not schedule autonomous maintenance.

The public `threeTierActionWorkflow` policy in `./extension-sdk` identifies Runtime
capacity maintenance only while this Strategy is selected. The DSH Host applies
it to named tools, generic View Actions, child writes and browser management.
The operation retains its exact Source instance, scope, revision and authority;
archive destinations stay within the selected writable Source and pinned
namespaces. A browser write needs no conversation. One eligible destination
requires no model; routing and local profile compaction use a clean workspace
task Agent when needed. Sources retain their independent storage protocols,
and other Strategies do not acquire this workflow implicitly.

## Additive plugins

The selected complete Strategy still produces one View. Its optional, exclusive
slots are public through `dsh-mnemon-strategy-default-three-tier/extension-sdk`:

| Slot | Value | Optional plugin |
| --- | --- | --- |
| `selection` | Ordered Source keys and an optional writable subset | `dsh-mnemon-strategy-scoped` |
| `projection` | A ceiling within the Host's projection-character budget | `dsh-mnemon-strategy-light-context` |
| `capture` | In-turn recording guidance, explicit recording Action ids and optional Source keys | `dsh-mnemon-strategy-auto-capture` |

Enable these ordinary Cordis Entries alongside this Strategy; no `strategyId`
change is needed. They can coexist because each owns a different slot. Core
rejects duplicate slot owners rather than letting installation order win. Slot
names and value schemas belong to this Strategy, not to Core. External authors
use `defineThreeTierExtension` and `installMemory(ctx, { strategyExtensions: [...] })`.

All Sources share the existing turn's read quotas and one projection budget.
Runtime guidance describes snapshots as budget-limited even without extensions:
metadata on many short entries can exceed the projection budget while stored
content remains within capacity. An omitted entry is not evidence of deletion.
Replay and Related admission include the exact Source instance identity. Writes
still require an offered Action and Host authorization, including implicit
capacity maintenance. Explicit operator management is a separate channel.

With no extensions the original three-tier composition is retained. Disabling
one extension only removes its contribution for new turns; running turns keep
their pinned View. If disabling explicit selection leaves multiple Sources in
the same role, the original ambiguity check applies rather than silently
choosing a Source. Projection limits are not token limits or delta injection;
capture guidance is not a background worker or a guarantee of model behavior.

Its own directory is a standalone project: install its declared dependencies,
then run `pnpm verify`. When developing against an unreleased Mnemon, install a
packed `dsh-mnemon` artifact as the peer instead of linking its source tree.

The Starter or a user's Profile explicitly mounts its Entry. This package has
no auto-activation patch; installing it does not override a configured Strategy.

## Installation and verification

The default Starter already mounts and selects this Strategy. In an independent Profile, install the package, mount its Entry and explicitly select `default-three-tier`; another installed Strategy does not win by import order.

Installing an npm package is not the same as activating a contribution. From a source checkout, run `pnpm verify` with declared public peers installed; use a packed peer for unreleased SDK work.

[Plugin development and independent fixtures](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/en/development/extensions.md) · [中文指南](https://github.com/mnemon-dev/mnemon/blob/master/dsh/docs/zh-CN/development/extensions.md)
