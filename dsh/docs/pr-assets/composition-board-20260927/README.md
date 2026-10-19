# Memory composition drawn from component declarations

[简体中文](./README.zh-CN.md)

Tested implementation: `b348fffa`, stacked on the component linkage record ([record](../plugin-linkage-20260927/README.md)). macOS 15.6, Node 25.1.0, published DSH 0.1.7-rc.2, headless Chrome at 1280 × 860 (and 420 × 900). Each run starts from a fresh isolated WebUI fixture with the Starter's defaults and its test model. No personal memory or credentials were used.

## The board

Groups come from the roles components declare; names and descriptions come from each component's own declaration. Nothing is said while memory is composed as chosen.

| Default | Rows name what they relate to |
|---|---|
| ![Board](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-default.png) | ![Relation chips](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-relations.png) |

## A component's page

Selecting a row opens its page: state and switch, origin and package, relations, and what its switch would also move. The gear opens the same page at its declared options.

| Relations | Declared options |
|---|---|
| ![Memory Spaces page](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-details.png) | ![Active capture options](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-options.png) |

## Switches that move others, and problems

| Turning off Memory Spaces takes Active capture along | Memory not in use: the one fix |
|---|---|
| ![Cascade with Undo](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-cascade.png) | ![Problem line](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/board-memory-off.png) |

The Memory System shows a layer whose Source component is off as off:

![Project Documents off](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/workspace-layer-off.png)

## Dark theme and narrow column

| Board | Page | Cascade |
|---|---|---|
| ![Board, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/dark-board-default.png) | ![Page, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/dark-board-details.png) | ![Cascade, dark](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/dark-board-cascade.png) |

| Board, 420 px | Page, 420 px |
|---|---|
| ![Board, narrow](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/narrow-board-default.png) | ![Page, narrow](https://raw.githubusercontent.com/omdsh-dev/dsh-mnemon/2296898ddde876aa49a5dd68165d6c40c069fcd1/docs/pr-assets/composition-board-20260927/narrow-board-details.png) |

## Verification

- Unit tests cover switch planning (dependencies, exclusive claims, extension slots, main Strategy switches, unmanaged Sources), relations, option writes with Undo, the board (selector, groups by role, unknown roles, search, relation chips, pages, options, problem line) and the Memory System's off layers.
- WebUI: 7 light, 3 dark and 2 narrow states captured by a scripted walk on a fresh fixture, with no console errors; the same states were driven by hand in the in-app browser, including an option write and reset on the real Host.

## Limits

Component-owned settings still sit in page-level groups: Memory providers and embedding belong to Memory Spaces, Background tasks to Default three-tier. Moving them into their components' pages through a contribution slot is the next change. The fixture's shipped enhancements all fit any main Strategy, so the closed group for other Strategies and the search are covered by unit tests only.
