# Building Memory Plugins

[简体中文](../../zh-CN/development/extensions.md) | **English** | [Documentation Center](../README.md)

Choose the ownership boundary before writing code. The complete external examples under [plugin-consumer](../../../scripts/fixtures/plugin-consumer) are compiled and tested against packed artifacts outside this repository.

## Distinguish contribution responsibilities

| Plugin | Owns | Public dependency |
|---|---|---|
| `dsh-mnemon-source-*` | A memory authority, projection, retrieval/mutation and optional pages | Core contracts and extension SDK |
| `dsh-mnemon-strategy-*` | A complete View Strategy, or an additive contribution supported by its target Strategy | Core contracts and extension SDK; the target Strategy's extension SDK |
| `dsh-mnemon-provider-*` | A driver under Memory Spaces, with its descriptor, capabilities, connection schema and icons | Memory Spaces Provider SDK |

A Source need not implement a Provider. A Provider need not know View composition. A Strategy receives facts, not Source objects. Cross-package imports target declared public exports only; never reach into another plugin's `src`, controller, registry or build configuration.

| Entry | Responsibility |
|---|---|
| `dsh-mnemon` | DSH Host and default Starter |
| `dsh-mnemon/core` | Cordis plugin installing the Source-neutral service; no engine exports or Host/UI |
| `dsh-mnemon/contracts` | Source/Strategy callback contracts and JSON-safe manifests, facts, ViewSpec, View, Evidence and Receipt |
| `dsh-mnemon/extension-sdk` | Source/Strategy definitions, lifecycle installation and validators |
| `dsh-mnemon/testing` | Scoped composition, route/action and management testing; JSON diagnostics; built Client artifact loader |
| `dsh-mnemon/client` | DSH workspace and Source-page SDK |
| `dsh-mnemon-source-memory-spaces/provider-sdk` | Memory Spaces' own Provider child-module contract |
| `dsh-mnemon-source-memory-spaces/testing` | Private-child module fixture and driver authority/connection fixture |

## A contribution service, not an engine

Following the DSH service/Slot pattern, the public extension point is a small contribution contract. `Context.mnemonMemory` is typed as `MnemonMemoryService` and its actual, frozen object exposes only `installContributions`. Authors call `installMemory(ctx, contribution, options?)`: it resolves the calling Entry identity and binds registration cleanup to that Fiber. Do not introduce a parallel registration API.

The engine, installed records, registry, generations and leases stay internal. Neither `extension-sdk`, `contracts` nor the Core plugin exports them. `MemorySourceRuntime` is intentionally public: it describes the callbacks **your Source implements**, not an engine handle. Definitions/factories are Host-side executable contracts; metadata, operation inputs and results are JSON-safe. This service boundary is not a sandbox for arbitrary JavaScript.

## Source lifecycle

Define a `MemorySourceDefinition` with `defineMemorySource`. Its manifest declares API version, type id, package name, role, consistency and supported routes/actions. Its factory receives stable instance provenance and immutable configuration.

- `facts(request)`: bounded, non-sensitive availability, revision and capabilities.
- `project(request)`: bounded text plus a Source-owned ReadGrant consistent with the selected revision. Capture concurrent snapshots per request/scope; do not share one mutable “last snapshot”.
- `query`: receive only a `{ id, scope }` View identity and this instance's own `grant`, enforce the Source's scope, and return bounded Evidence with provenance.
- `mutate`: perform only an authorized action, honor cancellation and distinguish committed/partial/failed results. It receives the same narrow View identity and an optional instance-local `grant`; a read grant is not write authorization.
- `manage` (optional): authenticated human operations, separate from model grants; validate confirmation and exact revision again.
- `dispose` (optional): release runtime-owned resources after generation leases drain.

The complete `ComposableMemoryView` stays in the Host and composition tests, never in Source callbacks. Do not inspect other instances' projections or grants through `request.view`; writes needing their own pinned read scope use `request.grant` directly.

`facts(request, signal)` and `project(request, signal)` receive cancellation separately from the JSON input. Forward it to network reads and never write data in these callbacks. Core reads independent Sources concurrently with a default 10-second deadline per read. The DSH Host uses its existing `timeoutMs`; independent tests can set `MemoryCompositionRunner({ sourceTimeoutMs })`. Timeouts and remote failures produce sanitized `view.diagnostics`; malformed protocols still reject the View. Cancelling a turn is never downgraded to an optional Source outage. Deadlines cannot interrupt synchronous code blocking the event loop: this is not a malicious-plugin sandbox.

```ts
import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryPlugin, installMemory } from 'dsh-mnemon/extension-sdk'
import { notesSource } from './source.js'

export const name = 'dsh-mnemon-source-notes'
export const inject = ['mnemonMemory']
export const memoryPlugin = defineMemoryPlugin({
  packageName: name,
  label: { en: 'Notes', 'zh-CN': '笔记' },
  description: { en: 'Durable personal notes.', 'zh-CN': '长期个人笔记。' },
  roles: ['source'],
  provides: [{ id: 'source' }, { id: 'source.durable-evidence' }],
})
export function apply(ctx: Context): void {
  installMemory(ctx, { plugin: memoryPlugin, sources: [notesSource] })
}
```

The example assumes `source.js` exports the definition. [external-source.ts](../../../scripts/fixtures/plugin-consumer/src/external-source.ts) implements the complete file-backed example. Source and Strategy are roles, not mandatory package/repository boundaries. One package can call `installMemory(ctx, { sources: [source], strategies: [strategy] })`: both contributions install and unload together under the same Fiber, retaining distinct instance keys. Split packages only for independent reuse or replacement. Strategy selection remains explicit; bundling one does not override the user's selected Strategy.

Every composable plugin should export one JSON-safe `memoryPlugin` descriptor and pass the same object to `installMemory`. `roles` describe contributions; they do not create separate plugin classes. `provides` and `requires` form the activation graph. Mark a provided capability `exclusive: true` only when two providers cannot participate together. Declare only hard requirements: a plugin that can truthfully degrade or no-op must not force an unrelated dependency. Core validates the active graph before running Source factories, and remains the only component that compiles the graph into one View. Plugins without this prerelease descriptor keep their existing composition behavior, but the Host can infer only limited identity and relationship information.

An Entry id identifies an instance; type id identifies its implementation. Never strip Loader include prefixes. Direct `ctx.plugin()` mounts without Loader identity must supply `installMemory(..., { instanceId })`. Paths/credentials remain instance-owned. Do not use a module-global database or service registry.

## Complete Strategies and additive contributions

Use `defineMemoryStrategy` and install with `{ strategies: [definition] }`. Declare deterministic composition, supported roles and maxima; a role-agnostic Strategy lists `ANY_MEMORY_SOURCE_ROLE` (`'*'`) alone. Pure `compose` returns a `MemoryViewSpec` selecting exact Source keys, eager/routed projection budgets and Source-local route/action ids. It performs no network, storage or secret access.

Several main Strategies can be installed; exactly one composes each View. `memoryView.strategyTypeId` selects it, and when the selected Strategy is switched off while exactly one other remains, the Host composes with that one and reports a `strategy-fallback` diagnostic. A Strategy may declare exclusive `extensionSlots`. A small plugin uses `defineMemoryStrategyExtension`, installed through `{ strategyExtensions: [definition] }`. Enabling contributes bounded JSON to one target slot; disabling removes only that contribution. Different slots compose into one View. Duplicate slots for the same target reject registration instead of using installation order. Contributions targeting an unselected Strategy are observable but do not execute. Unsupported slots reject the candidate generation and preserve existing Serving; invalid dynamic results reject the affected turn rather than silently ignoring the plugin.

Core validates identities, JSON and the 64,000-character bound, deterministic replay, lifecycle, and the final View's existing budgets and permissions. It defines only the standard `selection`, `projection` and `capture` value contracts below; other slot names belong to the owning Strategy. Callbacks see only the request and permission-filtered Source facts, never Source handles, grants, or write callbacks. The owning Strategy's public SDK defines slot semantics.

### Optional configuration descriptor

A dedicated Strategy Entry can additionally export `memoryStrategyConfiguration`, created with `defineMemoryStrategyConfiguration` from the Core SDK. It declares public fields (`number`, `text`, `textarea`, `string-list`, `source-list`) and a **pure `create(config)` factory shared with `apply()`**. The factory returns exactly one Strategy or extension contribution together with the same `memoryPlugin` descriptor; it performs no I/O, credential access or Fiber mounting. This optional convention is for the Host and future tooling; it does not define plugin identity or activation relations.

The helper validates and freezes a copy of the metadata without running the factory. Its returned `create(config)` validates supplied fields and the declared contribution before returning it; omitted defaults remain the factory's responsibility. `number` values are finite integers, lists contain at most 32 unique nonempty strings of at most 500 characters, and text defaults to a 4,000-character limit. Host discovery uses the same validation for modules without the helper, while retaining Loader identity checks and local error isolation.

Ordinary users see no generic memory-plugin discovery, dependency graph, or installation dialog. Every installed memory component appears on the **Memory composition** board of the `dsh-mnemon` page under **Plugins**, above DSH's own component list. Where DSH's plugin manager is available, their enablement is saved in the DSH profile patch, so the native component switches on the Plugins page and Mnemon's controls always agree; Mnemon saves only the selected main Strategy and each Entry's configuration.

#### How a plugin appears on the board

The board is drawn from what each plugin declares, so a new plugin needs no Mnemon change to be listed, switched and configured:

| Declaration | On the board |
|---|---|
| `roles` | `strategy` is one choice of **Main strategy**; `source` has a row under **Memory sources**, even while off; `strategy-extension` has a row under **Enhancements**; any other role gets a group of its own |
| `label`, `description` (`en`, `zh-CN`) | The row's name and description, in the page's language. Shipped components declare theirs the same way; the component's page also shows its package and whether it ships with dsh-mnemon |
| `strategyTypeId` of an extension | `'*'` or the selected main Strategy's type lists the row with the others; another type moves it to a closed group for other main Strategies |
| `requires`, `provides` | Turning a component on also turns on the first installed provider of a capability it requires that nothing provides; turning one off also turns off what loses a capability it required. A main Strategy keeps its last Source: that switch is refused. Rows name a single provider they need, and the running component that depends on them alone, as link chips; the component's page lists every relation |
| `provides[].exclusive`, extension `slot` | Two components claiming the same capability, where either claims it alone, or two extensions filling the same slot for overlapping main Strategies, cannot run together: turning one on turns the other off |
| `memoryStrategyConfiguration.fields` | A gear on the row, and an options section on the component's page with one control per field, its label, description, default and limits; **Apply** appears once one changes. `source-list` offers the running Source instances whose role is in `sourceRoles`, named by the components that registered them |

The component's page says what else its switch would move, and the toast after a switch names what moved, with **Undo**. Declare `requires` and `provides` precisely: they are how users see what your plugin needs, and what it cannot run beside.

DSH's own plugin list names a package from its `locale/en.json` and `locale/zh.json` (`meta.title`, `meta.description`), exported as `./locale/*.json` and listed in `files`; without them it shows the package name. Give them the same text as the declaration's `label` and `description`, as the shipped packages do, so the component reads the same on DSH's list and on the board.

#### A component's own settings and Status card

When declared options are not enough, a component adds its own settings to its page from its `./client` entry, the way the shipped components do: Runtime Memory's user profile scope, Memory Spaces' Providers and embedding, the Layered strategy's background tasks. A Source can also say what its card on the Memory System's Status page shows while it runs.

```tsx
import { installMemoryComponentUI } from 'dsh-mnemon/client'

export function apply(ctx) {
  ctx.effect(() => installMemoryComponentUI(ctx, {
    packageName: 'acme-memory-notes',
    settings: ({ component, writable, language }) => <NotesSettings enabled={component.enabled} readOnly={!writable} language={language} />,
    status: ({ language }) => <><strong>{notesHeadline(language)}</strong><p>{notesDetail(language)}</p></>,
  }))
}
```

The settings render in the `mnemon.component.settings` region, keyed by the package name the plugin's declaration names, after the page's state, relations and declared options; the component's row gets a gear that opens them. They receive only what the page knows: `component` (`packageName`, `label`, `enabled`), `writable`, `language`, and the conversation's `sessionId` and `workspace` when there is one; bring your own services through your own registration. Registration waits until dsh-mnemon's configuration exists and ends with the returned function. Follow the page's interaction rules: switches and selectors apply when they change, typed values wait for your own **Apply**, and your settings never join another group's save.

The Status card renders in the `mnemon.component.status` region, keyed the same way, as a headline in `<strong>` and one line in `<p>` under the component's declared name. It receives `component`, `language`, and the Memory System's `sessionId` and `workspace` when there is one, and reads its own Source. Every Source component has a card whether or not it contributes one: a component that is off, or whose Source is not running, shows the page's own note instead, and one that contributed nothing shows that it runs. Tabs, the card and the header use the name the component declares.

Third-party packages continue to use DSH's native Profile/Loader workflow. Install an exact package with `dsh plugin --profile <Profile> add <name>@<version> --save-exact`, verify its `peerDependencies` and `dsh.bundle.patch`, then activate it explicitly in Profile composition after restarting. Downloading an npm package is not activation, and Mnemon does not hot-load it into the current process. External standalone repositories following this guide are welcome; generic graphical management may be revisited after the contracts and community cases settle, but is not a v0.5 promise.

<a id="default-three-tier-extensions"></a>

### Standard View extensions

`defineMemoryViewExtension` from `dsh-mnemon/extension-sdk` targets `ANY_MEMORY_STRATEGY` (`'*'`): the extension follows whichever selected Strategy declares its slot, and stays inactive with a diagnostic otherwise. A slot has one owner: registering a second extension for it is rejected when both target the same Strategy or either targets `'*'`, because the pair would meet after a main Strategy switch. `validateMemoryViewExtension` and `memoryViewExtensionValues` give a Strategy the same validated values. `dsh-mnemon-strategy-default-three-tier/extension-sdk` keeps `defineThreeTierExtension` for an extension that should apply only to that Strategy. The shipped enhancements use the standard slots:

| Optional plugin | Slot | Contribution |
|---|---|---|
| `dsh-mnemon-strategy-scoped` | `selection` | Source key order and writable subset; does not create Sources or change physical storage scope |
| `dsh-mnemon-strategy-light-context` | `projection` | One shared projection cap; not incremental injection or summarization |
| `dsh-mnemon-strategy-auto-capture` | `capture` | Current-turn instructions, targets and explicit recording Action ids; no background Agent or direct writes |

The Starter installs all three packages and registers their DSH Entries disabled. The switches are therefore always available, while the default composition, allocation and guidance preserve v0.4 behavior. Enabling one contributes to the selected main Strategy without changing it; disabling it removes only that contribution and never deletes Source data. The General strategy (`dsh-mnemon-strategy-general`) accepts the same three slots. Runtime currently has no expansion route: an aggressively small resident cap can hide hot context and needs workload-level evaluation.

Source keys in `scoped` must retain any Loader include prefix. Omitting its configuration deterministically selects existing instances by role/key and creates no storage. The board's **Options** panel edits the fields the package declares; Profile configuration remains available for automation.

`scoped.sourceKeys` expresses priority and `writableSourceKeys` narrows the writable subset. Automatic capacity maintenance also checks the current View's write scope; a denied operation preserves the original data and fails instead of migrating around the restriction. Explicit operator management remains separately authorized.

```ts
import type { Context } from '@deepseek-ai/cordis'
import { defineMemoryViewExtension, installMemory } from 'dsh-mnemon/extension-sdk'

export const inject = ['mnemonMemory']
export function apply(ctx: Context): void {
  installMemory(ctx, { strategyExtensions: [defineMemoryViewExtension({
    typeId: 'my-light-context', packageName: 'dsh-mnemon-strategy-my-light-context',
    slot: 'projection', contribute: () => ({ maxProjectionCharacters: 4096 }),
  })] })
}
```

In-turn writes still use Host tools, authorization and Source receipts. A capture contributor must name actual recording Actions, not infer them from generic write capability. Retrieval limits remain shared across the executing turn; Source-qualified replay and Related admission prevent cross-instance aliasing. If removal cannot produce a replacement generation, new turns fail closed rather than revive the disabled policy: they run without a memory View, and the conversation itself continues. Existing pinned turns retain their leases.

Optional `createTurn(view)` supplies an execution-local `query(request, read)` policy. The only supplied I/O is `read(input, narrowerLimits?)`, bound to the selected Route and its private grant. Core still validates inputs, ceilings, dispatched calls and lifetime. A policy may admit/replay results and supply a compact `Evidence.output` for the model; it does not obtain a Source object, write continuation or authority. Separate executions get separate policy state even when they inherit the same immutable View. Without this hook, reads go directly to the Source through the same Core fences.

The Layered strategy plugin uses this hook for the old Documents slot, two-query Recall envelope, deduplication and Related admission. Named tools and generic View routes share that policy. Source implementations retain raw search, storage and maintenance; explicit DSH-assisted writing/archiving remains a Host workflow, not a generic Core background job.

The default plugin's public, pure `threeTierActionWorkflow` policy identifies capacity maintenance for Runtime `mutate`. The Host applies it to named tools, generic View Actions, child Agents and browser management only while the Layered strategy is selected. Sources retain independent management protocols; Core gains no three-tier storage logic. Model writes retain their initiating View, instance and authority, with archival limited to that View's writable Memory Spaces Source and its Source-defined write scope. Ambiguous writable archive Sources are rejected. Browser operations carry the registered workspace scope, exact instance and confirmed revision without requiring a user conversation. An independent maintenance task is created only when model judgment is needed.

For archive preflight the Host can pass `{ writeScope: { viewId, grant } }` to the selected Memory Spaces Source's `body-directory` read. The optional response `writeScope: { viewId, sourceInstanceKey, memoryBodyIds }` uses the same authority as `remember`: known namespaces in the grant plus namespaces created by that View. The Source validates grant ownership; the Host validates the echoed View and Source identity, treats empty scopes as deny-all, and rechecks authority and live capabilities before writes. Source implementations that omit this response retain the narrower active namespace pins. Malformed responses fail closed. The Host never substitutes another Source or a live catalog for missing authority; recall keeps its original namespace pins.

Selected Sources are required by default. Set `required: false` to explicitly permit omission when that instance is unavailable or its projection fails. Required failures reject the turn without silently switching strategies. The Layered strategy selects available Sources as optional so an external read failure does not remove healthy layers. A Strategy must explicitly reject a missing required instance rather than return an empty selection.

[external-strategy.ts](../../../scripts/fixtures/plugin-consumer/src/external-strategy.ts) is a working explicit-selector example. Once installed, it appears in the **Main strategy** selector on the `dsh-mnemon` page under Plugins, which saves its type id as `memoryView.strategyTypeId`; for automation, `mnemon.memoryTopology.strategyId` selects it when no choice has been saved. More than one applicable Strategy is an error, not “last imported wins”. A profile replaces default Entries explicitly; installing a package alone is not authority to replace them.

Optional `ViewSpec.guidance` carries the Strategy's trusted `system`, `routing` and read/write reminders separately from quoted Source data. It is validated and pinned into the View digest. The Host supplies generic routing guidance when none is provided and renders named tool availability without repeating schemas already in DSH's tool catalog; external/unbound operations keep their exact ids and schemas. Existing product tools and explicit management remain available; tool presence does not mean that a Source participates in this turn's View. Automatic three-tier background review runs only with `default-three-tier`, never implicitly under a custom Strategy.

## Open operations, bounded execution

A Source owns its operation names, input schemas, storage, indexes and maintenance. There is no mandatory five-action taxonomy, summary tree, representation vocabulary or multidimensional budget protocol. A Strategy selects public operations it understands, by id, role and/or capability. Core derives `MemoryAvailableSource.routes/actions` from manifests, live facts and Host permissions; plugin authors do not duplicate descriptors in facts.

Core bounds projection characters, evidence characters/items and dispatched calls. These are payload ceilings, not complete LLM-prompt token estimates. Sources receive effective bounds and own excerpts and structured formatting; Core omits oversized evidence rather than manufacturing a summary or splitting JSON. Failed dispatched reads consume a call. There is no automatic retry.

Every mutation receipt declares `completion`: `accepted`, `candidate`, `committed`, `partial`, `failed`, or `unknown`. Only a confirmed full commit can include `committedAt`; Core never invents that timestamp. `createMemoryMutationReceipt(..., completion)` defaults to `unknown`; pass `'committed'` only after the requested durable effect completes. Cancellation or a transport exception does not prove that no write occurred, and a cross-Source workflow is not atomic. Generic and named product tools preserve this distinction for the model.

## Provider child and Client page

Provider authors use `defineMemorySpaceProvider` from Memory Spaces' SDK. A module receives only its bound `host.install(ctx, definition)` capability; the private parent Host, Snapshot and Registry are not SDK exports. `installMemorySpaces` mounts explicit children and returns `Promise<void>`, not a parent handle. [external-provider.ts](../../../scripts/fixtures/plugin-consumer/src/external-provider.ts) is tested in two independent parent Sources with identical child ids.

Use `mountMemorySpaceProvider` from the Source's `/testing` entry to test real child registration, aliased adapter creation and cleanup. It returns frozen descriptor/manifest metadata, `registered`, `createAdapter` and `dispose`. `createMemorySpaceProviderFixture` separately supplies validated connection data and a scoped driver authority. The [published API test](../../../scripts/fixtures/plugin-consumer/tests/public-api.spec.ts) combines both without private imports.

```yaml
- id: work-spaces
  name: dsh-mnemon-source-memory-spaces
  config:
    dataDir: /absolute/path/to/work-memory
    providers:
      - use: dsh-mnemon-provider-holographic
        instanceId: local-facts
```

The Source itself authors the Provider Fibers. Core supplies no Provider factory registry and no second Context service. Each module owns truthful capabilities: a query-only backend must not fabricate browse results or graph edges.

A Provider that declares `entities` feeds the Entities page. By default the Source builds each space's entity index from `list()`: return every memory up to the requested limit with its `entities`, and an entity's count is then the number of listed memories that carry it, the same memories the page lists. A Provider whose `list()` stops short of the whole space, such as at a service page size, or that cannot list, implements the optional `entityIndex(body, signal)` and returns `{ memories, complete }`: the memories that carry entities, and whether that is all of them. The page marks an incomplete index as partial. An entity Provider with neither listing nor `entityIndex` is shown as query-only; its memories reach the page only as related memories found by recall. The Source keeps one index per space while the space's `status()` statistics stay the same, so return statistics that change with every write.

An optional `./client` entry calls `installMemorySourceUI` from `dsh-mnemon/client`; DSH loads it as an ordinary Client plugin. It receives `MemorySourcePageProps` with selected instance, locale, writability and scoped `management.read/mutate`. Use `MemorySourcePageFrame` for shared locale/appearance. No Host Context, driver, token, LLM grant or transport is passed to React. Missing pages use generic management; duplicate page ownership and rendering failure have local diagnostics.

The workbench owns the outer page inset, minimum height and page scroll container. Nested `memoryPageStyles.page` content shares that frame instead of adding another viewport height or inset, including through DSH renderer wrappers. A Source keeps its business layout and may retain bounded readers or dialogs. On local page changes, call the optional `onResetScroll` callback before paint; it resets only the owning workbench canvas. Do not scroll DSH ancestors or peer plugins. A Source with a compound sticky title/tab header, such as Memory Spaces, sets `navigation.stickyHeader: false` to prevent the Host from also pinning its subordinate headings, and owns that compound header's sticky layout itself.

To reveal content, pass an element from the Source's own ref to the optional `onRevealElement(element, topInset)` callback. Without `topInset` it lands below the locked page header; a Source that pins its own header passes that header's measured height instead. The Host scrolls only its canvas and ignores elements outside it; avoid global IDs and `scrollIntoView`, which can move DSH ancestors. Cancel pending animation frames when the selection closes or the Source unmounts. Both scroll callbacks are optional: new Sources still load with the existing Root peer floor, with manual scrolling when an older Host does not supply them.

`refreshKey` changes each time the user presses **Refresh** in the workspace header; reload the page's data when it changes instead of adding a refresh or sync button. `navigationInput` is `{ seed, nonce }` only when a conversation anchor, such as an item in the turn memory bar, opens the page; a tab opens it without one. The default Sources read the seed as a document id (Project Documents selects it), a recall query (Memory Spaces runs it on arrival) or an entry's text (Runtime memory highlights and reveals it). A new `nonce` means a new request for the same seed.

`dsh-mnemon/client` also exports the controls the default Sources use, so an installed Source looks and behaves like them: `SearchField` (DSH's input with a search icon), `SelectField` (a labelled DSH selector menu, with `inline`, `size: 'sm'`, `hideLabel` and `ariaLabel`), `WriteReceipt` (a write's outcome, summary and optional view action) and `TaskAgentTag` (whether a task Agent can take the request). They follow the [interaction conventions](../guides/ui-guide.md#interaction-conventions). A Source that imports them, or reads `refreshKey`, declares a `dsh-mnemon` peer floor of 0.5.19, the first Starter to export them.

## Independent repository checklist

Keep business copy and layout inside the Source package. The default Sources demonstrate this with public data-only `presentation/locales.json`, `presentation/page.module.css` and `presentation/sidebar.module.css` assets, plus a private Client presentation module. Their own builds load the assets without importing repository build scripts. They retain the existing page-kit class namespace for usage compatibility; a third-party Source can use its own namespace and does not need to add words or selectors to Root.

A plugin directory owns `package.json`, exports, `src/`, `tests/`, TypeScript/build/test configuration and README. Declare compatible public peers and development dependencies. Publish built Host/Client artifacts and declarations; never depend on sibling paths, workspace source aliases, root test helpers or implicit hoisting.

The default Starter alone mounts defaults. Source/Strategy packages do not ship self-activating default patches. A user's profile, bundle or explicit parent composition controls activation and replacement.

```ts
import { MemoryCompositionRunner } from 'dsh-mnemon/testing'
import * as notes from './index.js'
import * as focus from './strategy.js'

const runner = new MemoryCompositionRunner()
try {
  await runner.mount(notes, { instanceId: 'work', config: { path: '/tmp/test-notes.txt' } })
  await runner.mount(focus, {
    instanceId: 'focus',
    config: { sourceKeys: ['source:work'], mode: 'eager' },
  })
  const turn = await runner.beginTurn()
  try {
    const route = turn.view.routes.find(route => route.sourceRouteId === 'read')!
    const evidence = await turn.executeRoute(route.id, {})
    // Assert projection, evidence and provenance. For writes, use
    // turn.executeAction(offer.id, input, authorize), with explicit test authority.
  } finally { turn.release() }
} finally { await runner.dispose() }
```

Use unique temporary paths in real tests. Release turns before disposing the runner. `runner.inspect()` returns JSON-only evaluation/generation diagnostics; `managementClient(sourceKey)` supplies scoped human operations. No turn exposes a lease, and no runner exposes the engine. Unmount/remount plugins to test replacement instead of editing an internal registry. Disposal also releases retained turns; in-flight operations hold their own leases until completion. This is a test harness over real Cordis/Core, not another production Loader.

Test at least: valid composition; missing/ambiguous dependencies; two instances; schema/capability/authority denial; concurrent snapshots; stale revisions; cancellation and partial failure; unload/drain/reload; persistence; management and actual page clicks. Providers additionally test credentials, truthful capabilities, malformed upstream data, timeouts and conformance inside their parent Source.

Run each plugin's `pnpm verify`. At repository level, `pnpm verify:plugins` packs all 17 artifacts, installs every plugin outside the workspace through ordinary semver manifests, then type-checks/tests/builds each and compiles the external consumer. No source aliases, manifest overrides or workspace links are permitted in that gate.

For RSI, keep candidate inputs/artifacts reproducible, compare against a known composition, and promote only through an explicit installation/selection decision. Passing a Strategy replay does not sandbox arbitrary JavaScript or grant permission to trade, send messages or delete external data.
