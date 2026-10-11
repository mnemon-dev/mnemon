# Component pages and one interaction rule

[简体中文](./README.zh-CN.md)

Tested implementation: `41fced12` for the configuration, `7fde5ecb` for the Memory System, and `8869ae6f` with `73fc8344` for DSH's list and row pages, stacked on the composition board record ([record](../composition-board-20260927/README.md)). macOS 15.6, Node 25.1.0, published DSH 0.1.7-rc.2, headless Chrome at 1280 × 860 (and 420 × 900). Each run walks an isolated WebUI fixture from the Starter's defaults with its test model. No personal memory or credentials were used.

## The configuration keeps what belongs to no component

A gear marks each component with settings of its own and opens its page; the main Strategy's gear opens the selected Strategy. Storage names the components that keep their data in its directory and shows where memory lives now. Interface choices apply at once.

| Board | Storage and Interface |
|---|---|
| ![Board with gears](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-board.png) | ![Storage and Interface](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-storage-interface.png) |

Moving the storage waits for its Apply, which says what applying does:

![Storage Apply line](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-storage-apply.png)

## Settings on their components' pages

| Runtime Memory: where USER.md lives | Memory Spaces: Providers and embedding |
|---|---|
| ![Runtime Memory page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-runtime.png) | ![Memory Spaces page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-spaces.png) |

| Default three-tier: background tasks | Review limits wait for their Apply |
|---|---|
| ![Default three-tier page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-three-tier.png) | ![Review limits edited](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-three-tier-limits.png) |

## Names open pages

| Relation chips on rows | A related name opens in place, with Back |
|---|---|
| ![Relation chips](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-chips.png) | ![Memory Spaces opened from Active capture](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-related.png) |

Declared options show Apply once one changes:

![Light context option edited](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/pages-options-edit.png)

## The Memory System names what the components declare

Tabs, the header's main Strategy and one Status card per Source component carry the names the components declare, as the configuration shows them; each card says what its component contributed.

| Light | Dark |
|---|---|
| ![Status page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/workspace-status.png) | ![Status page, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/dark-workspace-status.png) |

## DSH's component list opens the same pages

Each component package now gives DSH the name and description it declares, so DSH's own list below the configuration reads like the board; a component's name there opens DSH's page for it, which carries the same component page. Related components open over it.

| DSH's list before (package names) | DSH's list now |
|---|---|
| ![List before](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/rows-list-before.png) | ![List now](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/rows-list.png) |

| Default three-tier on DSH's row page | A related component opens over it |
|---|---|
| ![Row page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/row-page.png) | ![Related page over the row page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/row-page-related.png) |

## Dark theme and narrow column

| Board | Storage and Interface | Review limits |
|---|---|---|
| ![Board, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/dark-pages-board.png) | ![Storage, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/dark-pages-storage-interface.png) | ![Limits, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/dark-pages-three-tier-limits.png) |

| Board, 420 px | Storage Apply, 420 px | Review limits, 420 px |
|---|---|---|
| ![Board, narrow](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/narrow-pages-board.png) | ![Storage, narrow](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/narrow-pages-storage-apply.png) | ![Limits, narrow](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/component-pages-20260927/narrow-pages-three-tier-limits.png) |

## Verification

- Unit tests cover the settings region (registration, release, directory), the shipped panels (Runtime Memory's profile scope, Memory Spaces' managed switch and connection, Default three-tier's route, review choices and limits), storage Apply with legacy keys retired, interface choices at once with a refused write shown and reverted, gears, names, relation chips and Back on the board, declared options with Apply, and the dialog's box model.
- Full `pnpm run verify` and `pnpm run release:intent` passed; the package budget records the measured growth.
- Unit tests also cover the Status card region and the shipped Sources' cards, and a workbench whose tabs, cards (including a Source that is off and one that contributed nothing) and header are named from the components.
- Unit tests also cover the page mode of the board (one component, related pages over it), the eight row registrations, and each package's DSH metadata against its declaration and the Starter's patch; `pnpm run verify:plugins` packed and activated the Starter with its metadata in real DSH.
- WebUI: 14 light, 4 dark and 3 narrow states captured by a scripted walk with no console errors; the same flows were driven by hand in the in-app browser, including an idle review choice written to the real Host and a refused nested write caught before the fix.

## Limits

On the Status page, the Provider list and the storage section remain the Host's own sections rather than contributions. Row pages exist for the Starter's own rows; an installed package's rows keep DSH's default page, while its component still has its page on the board.
