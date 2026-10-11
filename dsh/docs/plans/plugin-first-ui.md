# Plugin-first interface

## Aim

Every capability of dsh-mnemon may be extended: Sources, main Strategies, enhancements, Providers, and roles not yet defined. The interface is therefore a host of regions that components fill, not a set of screens written for the shipped components. Shipped components use the same path as installed extensions. The structure stays small: DSH's slots, dsh-mnemon's existing declarations, and as few new definitions as the regions require.

## Two layers

1. **Declarations, no UI code.** `defineMemoryPlugin` gives a component's name and description (`en`, `zh-CN`), roles, `requires`, `provides`, exclusive claims and extension slot; `memoryStrategyConfiguration.fields` gives its public options. These alone draw the Memory composition board, the component's page (state, relations, what its switch moves, generated option controls), and the names the Memory System shows.
2. **Contributions, when a component needs its own UI.** A component registers into a region dsh-mnemon declares as a DSH slot. Nothing else is required of it.

## Regions

| Region | Slot | Kind, key | Declared on | Status |
|---|---|---|---|---|
| Memory System page and tab | `mnemon.source.page` | list | `shell.overlay`, `conversation.view` | existing |
| A component's own settings, on its page | `mnemon.component.settings` | keyed by package name | `plugins.bundle.config` (DSH's row pages render what was registered there, since a child slot has one declaring entry) | new |
| A component's card on the Status page | `mnemon.component.status` | keyed by package name | `shell.overlay`, `conversation.view` | new |

`installMemoryComponentUI(ctx, { packageName, settings?, status? })` in `dsh-mnemon/client` registers a component's contributions, beside the existing `installMemorySourceUI`. Owner props carry only what the host knows about the component (its declaration view, writability, language); a registrant injects its own services through its registration, as DSH slots already allow.

## A component's page

One content, two DSH containers:

- the dialog opened from the board's row, and
- for rows of the Starter bundle, DSH's own row page through `plugins.row.config` (`dsh-mnemon#<row id>`), which gives the row in DSH's component list a configure control. DSH heads that page with the name and description each package's `locale/*.json` gives, the same as its declaration, so the page omits its own package line; related components open over it.

The page shows, in order: state and switch; origin (shipped or installed package); relations; what the switch would also move; declared options; then the component's contributed settings.

## Moves for the shipped components

- **Runtime Memory** contributes where its user profile (USER.md) lives.
- **Memory Spaces** contributes Memory providers and embedding. They leave the page's top level.
- **Layered strategy** contributes the background tasks it drives: the task Agent model and idle review.
- The Sources contribute their Status cards; the Memory System names tabs, cards, storage areas, the header's main Strategy and layers from declarations rather than a table of shipped names. Every Source component has a card, a component that is off included.
- dsh-mnemon keeps what belongs to no component: storage, backup, interface, versions. Storage names the components that keep their data in its directory, from the Sources a backup carries.

## Rules

- A region renders nothing for a component that contributed nothing; declarations still give it a page.
- Copy stays short: names, states, chips and one-line descriptions; sentences only where an action needs its reason.
- No new registry, store or protocol beyond the slots above; the View transaction and settings scopes stay the only writers.

## Interaction

The same control behaves the same way everywhere, on the configuration and on every component page, shipped or installed.

| What | Behaves |
|---|---|
| A gear on a component's row | Marks a component with settings of its own (declared options or contributed settings) and opens its page; on the main Strategy row it opens the selected Strategy's page. It is the visible way in. |
| A component's name: a row title, a relation chip, a name on a page, a Storage chip | Opens that component's page too, underlined on hover. A name on a page opens its page in place, with a way back. |
| A switch or a selector | Applies when it changes. It shows the new value at once, and the saved one again beside the reason if the write fails. |
| Typed values (text, numbers, lists) | Wait for their group's Apply, which appears only once something changed and says what to fix while a value is refused. Discard puts the saved values back. The page stays open after Apply. |
| A change that moves data (the storage location) | Waits for Apply, and the Apply line says what applying does. |
| Buttons, focus, hover | DSH's Button and focus ring; no control of dsh-mnemon's own shape. |

Every dialog body carries the page's box model (`.surface`), since DSH renders dialogs outside the page; field grids fit as many columns as their width allows.

## Acceptance

- A third-party component with only a declaration appears on the board, has a page with relations and options, and switches with its dependencies.
- A third-party component that registers settings sees them on its page in both containers; a shipped component's settings arrive the same way.
- The Status page and tabs show a new Source by its declared name, and its card when it registers one.
- Tests cover slot registration and rendering, the moved panels' saves, and unchanged stored settings; documentation for authors lists every region with its props.
- Every row in the tables above behaves as stated, with a test for each kind of control.
