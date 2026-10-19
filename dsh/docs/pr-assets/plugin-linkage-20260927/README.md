# Component switches, dependencies and linked UI

[简体中文](./README.zh-CN.md)

Tested implementation: `3e9f28eb`, stacked on the configuration under Plugins ([record](../plugin-config-20260927/README.md)). macOS 15.6, Node 25.1.0, published DSH 0.1.7-rc.2, headless Chrome at 1280 × 860. Each run starts from a fresh isolated WebUI fixture with the Starter's defaults and its test model; components are switched on DSH's own component list below the configuration, as a user would. No personal memory or credentials were used.

## Main Strategy states

Before this change, turning on a second main Strategy showed nothing, turning off the selected one showed a note only after reopening the page, and with none running the note claimed that another Strategy composed memory. The Strategy group now follows DSH's list without a reload and names each state with the switch that resolves it:

| A second main Strategy runs unused | The selected one is off; a fallback composes | No main Strategy runs |
|---|---|---|
| ![Idle second main Strategy](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/strategy-idle.png) | ![Fallback Strategy](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/strategy-fallback.png) | ![No main Strategy](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/strategy-none.png) |

Choosing a main Strategy turns the other main Strategies off. Enhancements wait while the selected main Strategy is not running, because the Host validates every write against it.

## Conversations continue without memory

Before this change, with both main Strategy components off, every new turn failed with "no Serving memory generation is available: No Memory Strategy contribution is installed." The same turn now completes; the Host logs once: `[dsh-mnemon] this turn runs without memory: …`.

![A turn completing with no main Strategy](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/conversation-without-memory.png)

The Memory System says so above its pages, marks each layer **Not in use**, and links to the configuration, where **Turn on “Default three-tier”** restores memory:

![Memory not in use](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/status-memory-off.png)

## Source components switched off

With Documents and Memory Spaces switched off on DSH's list, each memory layer row says its component is off and offers **Turn on component**. Providers are not read while their Source is off, so the group shows why instead of the Host error "Source memory-spaces is not installed"; Mnemon Native no longer claims a missing CLI:

![Layer and Provider notes](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/layers-component-off.png)

The workspace keeps the Memory Spaces tab in place, marked **Not running**; Status cards say how to restore the layer instead of "Waiting for workspace" or "Directory not synchronized":

| Status | The stopped page | After the component runs again |
|---|---|---|
| ![Status with a stopped layer](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/status-component-off.png) | ![Stopped Memory Spaces page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/workspace-stopped.png) | ![Memory Spaces resumed](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/workspace-resumed.png) |

The open stopped page moves to the layer's own page once its Source runs again.

## Dark theme

| Fallback | Memory not in use | Component notes | Stopped page |
|---|---|---|---|
| ![Fallback, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/dark-strategy-fallback.png) | ![Memory not in use, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/dark-status-memory-off.png) | ![Component notes, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/dark-layers-component-off.png) | ![Stopped page, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/plugin-linkage-20260927/dark-workspace-stopped.png) |

## Verification

- `pnpm run verify`: documentation 2,216 local links and 83 anchors; deterministic build of 42 files; workspace package builds, typechecks and tests (Memory Spaces 174 passed and 2 conditionally skipped, Runtime 61, Documents 39); root tests 1,447 passed and 6 conditionally skipped; real Headless activation (37 tools), legacy settings import with an idempotent restart, and the root disable gate; package of 52 files, publint strict and attw.
- `pnpm run release:intent`: changesets cover every changed package.
- WebUI: 9 light and 4 dark states captured by a scripted walk on a fresh fixture, with no console errors. The same walk was driven by hand in the in-app browser, including Strategy exclusivity from the configuration's selector.

## Limits

The fixture's test model answers every turn with a fixed reply, so the screenshots show that a turn completes, not what memory would add. The `cordis:group` row in DSH's component list still shows as off with a switch that does not work (DSH #649); the configuration now says so right above the list. Strategy option editors and localized row titles for the Starter's components remain later work.
